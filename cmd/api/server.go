package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"reconcile-engine/internal/broker"
	"reconcile-engine/internal/domain"
	"reconcile-engine/internal/parser"
	"reconcile-engine/internal/repository"
	"reconcile-engine/internal/storage"
)

const maxUploadBytes int64 = 10 << 20 // 10 MB

// InvoiceRepository defines the ledger operations required by the HTTP API server.
type InvoiceRepository interface {
	Ping(ctx context.Context) error
	GetInvoiceBySHA256(ctx context.Context, fileSHA256 string) (*domain.Invoice, error)
	CreateUploadedInvoice(ctx context.Context, fileSHA256, storageKey string, sourceFormat domain.SourceFormat) (*domain.Invoice, bool, error)
	ListInvoices(ctx context.Context, filter repository.InvoiceFilter) ([]domain.Invoice, int, error)
	GetInvoiceDetail(ctx context.Context, invoiceID uuid.UUID) (*domain.InvoiceDetail, error)
	ResolveInvoice(ctx context.Context, invoiceID uuid.UUID, targetStatus domain.InvoiceStatus, actor, reason string) (*domain.Invoice, error)
	GetMetrics(ctx context.Context) (*domain.MetricsSummary, error)
}

// BrokerClient defines the broker operations required by the HTTP API server.
type BrokerClient interface {
	broker.Publisher
	HealthCheck() error
}

// Server wires HTTP handlers to PostgreSQL, MinIO, and RabbitMQ.
type Server struct {
	repo   InvoiceRepository
	store  storage.Store
	broker BrokerClient
	logger *slog.Logger
	mux    *http.ServeMux
}

// NewServer builds an HTTP server with all Phase 2 routes registered.
func NewServer(repo InvoiceRepository, store storage.Store, b BrokerClient, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		repo:   repo,
		store:  store,
		broker: b,
		logger: logger,
		mux:    http.NewServeMux(),
	}
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("POST /api/v1/invoices", s.handleUploadInvoice)
	s.mux.HandleFunc("GET /api/v1/invoices", s.handleListInvoices)
	s.mux.HandleFunc("GET /api/v1/invoices/{id}", s.handleGetInvoiceDetail)
	s.mux.HandleFunc("GET /api/v1/invoices/{id}/file", s.handleGetInvoiceFile)
	s.mux.HandleFunc("POST /api/v1/invoices/{id}/resolve", s.handleResolveInvoice)
	s.mux.HandleFunc("GET /api/v1/metrics", s.handleGetMetrics)
}

// Handler returns the root http.Handler wrapped with CORS and correlation-id middleware.
func (s *Server) Handler() http.Handler {
	return s.corsMiddleware(s.correlationMiddleware(s.mux))
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Correlation-ID, X-Filename")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type corrIDKey struct{}

func (s *Server) correlationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		corrID := strings.TrimSpace(r.Header.Get("X-Correlation-ID"))
		if corrID == "" {
			corrID = "req-" + uuid.New().String()[:8]
		}
		w.Header().Set("X-Correlation-ID", corrID)
		ctx := context.WithValue(r.Context(), corrIDKey{}, corrID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func correlationIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(corrIDKey{}).(string); ok && v != "" {
		return v
	}
	return "req-" + uuid.New().String()[:8]
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	deps := map[string]string{
		"postgres": "ok",
		"minio":    "ok",
		"rabbitmq": "ok",
	}
	healthy := true

	if err := s.repo.Ping(ctx); err != nil {
		deps["postgres"] = err.Error()
		healthy = false
	}
	if err := s.store.HealthCheck(ctx); err != nil {
		deps["minio"] = err.Error()
		healthy = false
	}
	if err := s.broker.HealthCheck(); err != nil {
		deps["rabbitmq"] = err.Error()
		healthy = false
	}

	statusCode := http.StatusOK
	overall := "ok"
	if !healthy {
		statusCode = http.StatusServiceUnavailable
		overall = "degraded"
	}

	writeJSON(w, statusCode, map[string]any{
		"status":       overall,
		"dependencies": deps,
		"timestamp":    time.Now().UTC(),
	})
}

type uploadResponse struct {
	InvoiceID    uuid.UUID            `json:"invoice_id"`
	Status       domain.InvoiceStatus `json:"status"`
	FileSHA256   string               `json:"file_sha256"`
	StorageKey   string               `json:"storage_key"`
	SourceFormat domain.SourceFormat  `json:"source_format"`
	Duplicate    bool                 `json:"duplicate"`
}

func (s *Server) handleUploadInvoice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	data, sha256Hex, contentType, filename, err := readAndHashUpload(r)
	if err != nil {
		if errors.Is(err, errPayloadTooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "invoice file exceeds maximum allowed size of 10 MB")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	sourceFormat, err := parser.DetectFormat(contentType, filename, data)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Fast-path SHA-256 deduplication before writing to MinIO or RabbitMQ.
	existing, err := s.repo.GetInvoiceBySHA256(ctx, sha256Hex)
	if err == nil {
		writeJSON(w, http.StatusOK, uploadResponse{
			InvoiceID:    existing.ID,
			Status:       existing.Status,
			FileSHA256:   existing.FileSHA256,
			StorageKey:   existing.StorageKey,
			SourceFormat: existing.SourceFormat,
			Duplicate:    true,
		})
		return
	}
	if !errors.Is(err, domain.ErrNotFound) {
		s.logger.Error("deduplication lookup failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to check invoice deduplication")
		return
	}

	canonicalCT := storage.FormatContentType(sourceFormat)
	storageKey, err := s.store.PutInvoice(ctx, sha256Hex, sourceFormat, canonicalCT, data)
	if err != nil {
		s.logger.Error("object storage upload failed", "error", err, "sha256", sha256Hex)
		writeError(w, http.StatusBadGateway, "failed to persist raw invoice file")
		return
	}

	inv, created, err := s.repo.CreateUploadedInvoice(ctx, sha256Hex, storageKey, sourceFormat)
	if err != nil {
		s.logger.Error("ledger insert failed", "error", err, "sha256", sha256Hex)
		writeError(w, http.StatusInternalServerError, "failed to create invoice ledger entry")
		return
	}

	// Handle concurrent duplicate upload race without re-publishing.
	if !created {
		writeJSON(w, http.StatusOK, uploadResponse{
			InvoiceID:    inv.ID,
			Status:       inv.Status,
			FileSHA256:   inv.FileSHA256,
			StorageKey:   inv.StorageKey,
			SourceFormat: inv.SourceFormat,
			Duplicate:    true,
		})
		return
	}

	env, err := broker.NewEnvelope(broker.RoutingKeyInvoiceUploaded, correlationIDFromContext(ctx), broker.InvoiceUploadedPayload{
		InvoiceID:    inv.ID,
		FileSHA256:   inv.FileSHA256,
		StorageKey:   inv.StorageKey,
		ContentType:  canonicalCT,
		SourceFormat: inv.SourceFormat,
	})
	if err != nil {
		s.logger.Error("failed to construct invoice.uploaded envelope", "error", err, "invoice_id", inv.ID)
		writeError(w, http.StatusInternalServerError, "failed to build event envelope")
		return
	}

	if err := s.broker.PublishEvent(ctx, broker.RoutingKeyInvoiceUploaded, env); err != nil {
		s.logger.Error("failed to publish invoice.uploaded", "error", err, "invoice_id", inv.ID)
		writeError(w, http.StatusBadGateway, "failed to enqueue invoice for parsing")
		return
	}

	writeJSON(w, http.StatusAccepted, uploadResponse{
		InvoiceID:    inv.ID,
		Status:       inv.Status,
		FileSHA256:   inv.FileSHA256,
		StorageKey:   inv.StorageKey,
		SourceFormat: inv.SourceFormat,
		Duplicate:    false,
	})
}

var errPayloadTooLarge = errors.New("payload exceeds max size")

// readAndHashUpload streams the upload body through sha256.New() via io.TeeReader while
// enforcing the 10 MB size ceiling. Supports both multipart/form-data ("file") and raw bodies.
func readAndHashUpload(r *http.Request) (data []byte, sha256Hex, contentType, filename string, err error) {
	reqCT := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))

	var src io.Reader
	if strings.HasPrefix(reqCT, "multipart/form-data") {
		mr, mrErr := r.MultipartReader()
		if mrErr != nil {
			return nil, "", "", "", fmt.Errorf("invalid multipart upload: %w", mrErr)
		}
		var found bool
		for {
			part, nextErr := mr.NextPart()
			if errors.Is(nextErr, io.EOF) {
				break
			}
			if nextErr != nil {
				return nil, "", "", "", fmt.Errorf("read multipart upload: %w", nextErr)
			}
			if part.FormName() == "file" || part.FileName() != "" {
				filename = part.FileName()
				contentType = part.Header.Get("Content-Type")
				src = part
				found = true
				break
			}
			_ = part.Close()
		}
		if !found {
			return nil, "", "", "", fmt.Errorf("multipart form missing required 'file' field")
		}
	} else {
		contentType = r.Header.Get("Content-Type")
		filename = r.Header.Get("X-Filename")
		src = r.Body
	}

	hasher := sha256.New()
	limited := io.LimitReader(src, maxUploadBytes+1)
	tee := io.TeeReader(limited, hasher)

	var buf bytes.Buffer
	n, copyErr := io.Copy(&buf, tee)
	if copyErr != nil {
		return nil, "", "", "", fmt.Errorf("read upload stream: %w", copyErr)
	}
	if n == 0 {
		return nil, "", "", "", fmt.Errorf("uploaded invoice file is empty")
	}
	if n > maxUploadBytes {
		return nil, "", "", "", errPayloadTooLarge
	}

	return buf.Bytes(), hex.EncodeToString(hasher.Sum(nil)), contentType, filename, nil
}

func (s *Server) handleListInvoices(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))

	filter := repository.InvoiceFilter{
		Status:       q.Get("status"),
		SupplierCode: q.Get("supplier_code"),
		SourceFormat: q.Get("source_format"),
		Search:       q.Get("q"),
		Limit:        limit,
		Offset:       offset,
	}

	invoices, total, err := s.repo.ListInvoices(r.Context(), filter)
	if err != nil {
		s.logger.Error("list invoices failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list invoices")
		return
	}

	if filter.Limit <= 0 || filter.Limit > 200 {
		filter.Limit = 50
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"invoices": invoices,
		"total":    total,
		"limit":    filter.Limit,
		"offset":   filter.Offset,
	})
}

func (s *Server) handleGetInvoiceDetail(w http.ResponseWriter, r *http.Request) {
	invoiceID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid invoice id")
		return
	}

	detail, err := s.repo.GetInvoiceDetail(r.Context(), invoiceID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "invoice not found")
			return
		}
		s.logger.Error("get invoice detail failed", "error", err, "invoice_id", invoiceID)
		writeError(w, http.StatusInternalServerError, "failed to load invoice detail")
		return
	}

	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handleGetInvoiceFile(w http.ResponseWriter, r *http.Request) {
	invoiceID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid invoice id")
		return
	}

	detail, err := s.repo.GetInvoiceDetail(r.Context(), invoiceID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "invoice not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load invoice metadata")
		return
	}

	raw, err := s.store.Get(r.Context(), detail.Invoice.StorageKey)
	if err != nil {
		s.logger.Error("fetch invoice file from storage failed", "error", err, "storage_key", detail.Invoice.StorageKey)
		writeError(w, http.StatusBadGateway, "failed to fetch invoice source file")
		return
	}

	w.Header().Set("Content-Type", storage.FormatContentType(detail.Invoice.SourceFormat))
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", detail.Invoice.ID.String()+"."+storage.FormatExtension(detail.Invoice.SourceFormat)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

type resolveRequest struct {
	Status string `json:"status"`
	Action string `json:"action"`
	Actor  string `json:"actor"`
	Reason string `json:"reason"`
}

func (s *Server) handleResolveInvoice(w http.ResponseWriter, r *http.Request) {
	invoiceID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid invoice id")
		return
	}

	var req resolveRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON request body")
		return
	}

	targetStatus, err := parseResolutionStatus(req.Status, req.Action)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	actor := strings.TrimSpace(req.Actor)
	if actor == "" {
		writeError(w, http.StatusBadRequest, "actor is required")
		return
	}

	updated, err := s.repo.ResolveInvoice(r.Context(), invoiceID, targetStatus, actor, req.Reason)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "invoice not found")
			return
		}
		if errors.Is(err, domain.ErrInvalidStateTransition) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		s.logger.Error("resolve invoice failed", "error", err, "invoice_id", invoiceID)
		writeError(w, http.StatusInternalServerError, "failed to resolve invoice")
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

func parseResolutionStatus(statusRaw, actionRaw string) (domain.InvoiceStatus, error) {
	candidate := strings.ToUpper(strings.TrimSpace(statusRaw))
	if candidate == "" {
		candidate = strings.ToUpper(strings.TrimSpace(actionRaw))
	}
	switch candidate {
	case string(domain.StatusManuallyApproved), "APPROVE", "APPROVED":
		return domain.StatusManuallyApproved, nil
	case string(domain.StatusRejected), "REJECT":
		return domain.StatusRejected, nil
	default:
		return "", fmt.Errorf("status/action must be MANUALLY_APPROVED (APPROVE) or REJECTED (REJECT)")
	}
}

func (s *Server) handleGetMetrics(w http.ResponseWriter, r *http.Request) {
	metrics, err := s.repo.GetMetrics(r.Context())
	if err != nil {
		s.logger.Error("get metrics failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to compute reconciliation metrics")
		return
	}
	writeJSON(w, http.StatusOK, metrics)
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, statusCode int, message string) {
	writeJSON(w, statusCode, map[string]string{
		"error": message,
	})
}
