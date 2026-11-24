package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"

	"reconcile-engine/internal/broker"
	"reconcile-engine/internal/domain"
	"reconcile-engine/internal/matcher"
	"reconcile-engine/internal/parser"
	"reconcile-engine/internal/storage"
)

const (
	actorParser  = "system:parser"
	actorMatcher = "system:matcher"
)

// WorkerRepository defines the database operations required by ParserWorker and MatcherWorker.
type WorkerRepository interface {
	CheckInvoiceForParsing(ctx context.Context, invoiceID uuid.UUID) (domain.InvoiceStatus, string, domain.SourceFormat, bool, error)
	SaveParsedInvoice(ctx context.Context, invoiceID uuid.UUID, parsed *domain.ParsedInvoice, supplierID, poID *uuid.UUID, actor string) (bool, error)
	MarkParseFailed(ctx context.Context, invoiceID uuid.UUID, parseErrMsg, actor string, metadata map[string]any) (bool, error)
	GetSupplierByCode(ctx context.Context, supplierCode string) (*domain.Supplier, error)
	GetPurchaseOrderByNumber(ctx context.Context, poNumber string) (*domain.PurchaseOrder, error)
	GetPurchaseOrderByID(ctx context.Context, poID uuid.UUID) (*domain.PurchaseOrder, error)
	GetGoodsReceiptSummaryByPOID(ctx context.Context, poID uuid.UUID) (*domain.GoodsReceiptSummary, error)
	GetInvoiceForMatching(ctx context.Context, invoiceID uuid.UUID) (*domain.Invoice, []domain.ParsedLine, error)
	SaveMatchResult(ctx context.Context, invoiceID uuid.UUID, poID *uuid.UUID, result matcher.MatchResult, actor string) (bool, error)
}

// FailureHandler abstracts RabbitMQ retry/DLQ routing for unit testing.
type FailureHandler interface {
	HandleDeliveryFailure(ctx context.Context, d amqp.Delivery, permanent bool, reason string) error
}

// ParserWorker consumes q.invoice.uploaded, fetches raw files from MinIO, parses deterministically,
// updates the PostgreSQL ledger, and emits invoice.parsed (or marks PARSE_FAILED and routes to DLQ).
type ParserWorker struct {
	repo     WorkerRepository
	store    storage.Store
	router   *parser.Router
	pub      broker.Publisher
	failures FailureHandler
	logger   *slog.Logger
}

// NewParserWorker creates a ParserWorker with the provided dependencies.
func NewParserWorker(
	repo WorkerRepository,
	store storage.Store,
	router *parser.Router,
	pub broker.Publisher,
	failures FailureHandler,
	logger *slog.Logger,
) *ParserWorker {
	if router == nil {
		router = parser.NewDefaultRouter()
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ParserWorker{
		repo:     repo,
		store:    store,
		router:   router,
		pub:      pub,
		failures: failures,
		logger:   logger,
	}
}

// HandleDelivery processes a single AMQP delivery from q.invoice.uploaded.
func (w *ParserWorker) HandleDelivery(ctx context.Context, d amqp.Delivery) {
	var env broker.EventEnvelope
	if err := json.Unmarshal(d.Body, &env); err != nil {
		_ = w.failures.HandleDeliveryFailure(ctx, d, true, fmt.Sprintf("invalid event envelope JSON: %v", err))
		return
	}

	var payload broker.InvoiceUploadedPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil || payload.InvoiceID == uuid.Nil {
		_ = w.failures.HandleDeliveryFailure(ctx, d, true, fmt.Sprintf("invalid invoice.uploaded payload: %v", err))
		return
	}

	// Idempotency guard using SELECT ... FOR UPDATE.
	status, storageKey, sourceFormat, skip, err := w.repo.CheckInvoiceForParsing(ctx, payload.InvoiceID)
	if err != nil {
		_ = w.failures.HandleDeliveryFailure(ctx, d, false, fmt.Sprintf("check invoice for parsing: %v", err))
		return
	}
	if skip {
		w.logger.Info("parser skipping already-processed invoice",
			"invoice_id", payload.InvoiceID,
			"status", status,
			"correlation_id", env.CorrelationID,
		)
		if ackErr := d.Ack(false); ackErr != nil {
			w.logger.Error("ack skipped delivery failed", "invoice_id", payload.InvoiceID, "error", ackErr)
		}
		return
	}

	if storageKey == "" {
		storageKey = payload.StorageKey
	}
	if sourceFormat == "" {
		sourceFormat = payload.SourceFormat
	}

	rawBytes, err := w.store.Get(ctx, storageKey)
	if err != nil {
		_ = w.failures.HandleDeliveryFailure(ctx, d, false, fmt.Sprintf("fetch object %q from storage: %v", storageKey, err))
		return
	}

	parsed, parseErr := w.router.ParseWithFormat(sourceFormat, rawBytes)
	if parseErr != nil {
		isScanned := errors.Is(parseErr, domain.ErrScannedPDFUnsupported)
		meta := map[string]any{
			"source_format": sourceFormat,
			"storage_key":   storageKey,
			"scanned_pdf":   isScanned,
		}
		if _, markErr := w.repo.MarkParseFailed(ctx, payload.InvoiceID, parseErr.Error(), actorParser, meta); markErr != nil {
			if !errors.Is(markErr, domain.ErrInvalidStateTransition) {
				_ = w.failures.HandleDeliveryFailure(ctx, d, false, fmt.Sprintf("mark PARSE_FAILED: %v", markErr))
				return
			}
		}
		w.logger.Warn("invoice parse failed; routing to DLQ",
			"invoice_id", payload.InvoiceID,
			"scanned_pdf", isScanned,
			"error", parseErr,
		)
		_ = w.failures.HandleDeliveryFailure(ctx, d, true, parseErr.Error())
		return
	}

	// Resolve optional supplier_id and purchase_order_id references.
	var supplierID *uuid.UUID
	if sup, supErr := w.repo.GetSupplierByCode(ctx, parsed.SupplierCode); supErr == nil && sup != nil {
		id := sup.ID
		supplierID = &id
	} else if supErr != nil && !errors.Is(supErr, domain.ErrNotFound) {
		_ = w.failures.HandleDeliveryFailure(ctx, d, false, fmt.Sprintf("lookup supplier %q: %v", parsed.SupplierCode, supErr))
		return
	}

	var poID *uuid.UUID
	if strings.TrimSpace(parsed.PONumber) != "" {
		if po, poErr := w.repo.GetPurchaseOrderByNumber(ctx, parsed.PONumber); poErr == nil && po != nil {
			id := po.ID
			poID = &id
		} else if poErr != nil && !errors.Is(poErr, domain.ErrNotFound) {
			_ = w.failures.HandleDeliveryFailure(ctx, d, false, fmt.Sprintf("lookup PO %q: %v", parsed.PONumber, poErr))
			return
		}
	}

	alreadyProcessed, err := w.repo.SaveParsedInvoice(ctx, payload.InvoiceID, parsed, supplierID, poID, actorParser)
	if err != nil {
		if errors.Is(err, domain.ErrDuplicateInvoice) {
			meta := map[string]any{
				"duplicate_invoice_number": parsed.InvoiceNumber,
				"supplier_code":            parsed.SupplierCode,
			}
			_, _ = w.repo.MarkParseFailed(ctx, payload.InvoiceID, err.Error(), actorParser, meta)
			_ = w.failures.HandleDeliveryFailure(ctx, d, true, err.Error())
			return
		}
		_ = w.failures.HandleDeliveryFailure(ctx, d, false, fmt.Sprintf("save parsed invoice: %v", err))
		return
	}
	if alreadyProcessed {
		_ = d.Ack(false)
		return
	}

	nextEnv, err := broker.NewEnvelope(broker.RoutingKeyInvoiceParsed, env.CorrelationID, broker.InvoiceParsedPayload{
		InvoiceID:     payload.InvoiceID,
		SupplierID:    supplierID,
		InvoiceNumber: parsed.InvoiceNumber,
		PONumber:      parsed.PONumber,
		SourceFormat:  sourceFormat,
	})
	if err != nil {
		_ = w.failures.HandleDeliveryFailure(ctx, d, true, fmt.Sprintf("build invoice.parsed envelope: %v", err))
		return
	}

	if err := w.pub.PublishEvent(ctx, broker.RoutingKeyInvoiceParsed, nextEnv); err != nil {
		_ = w.failures.HandleDeliveryFailure(ctx, d, false, fmt.Sprintf("publish invoice.parsed: %v", err))
		return
	}

	w.logger.Info("invoice parsed and published",
		"invoice_id", payload.InvoiceID,
		"invoice_number", parsed.InvoiceNumber,
		"po_number", parsed.PONumber,
		"lines", len(parsed.Lines),
		"correlation_id", env.CorrelationID,
	)

	if ackErr := d.Ack(false); ackErr != nil {
		w.logger.Error("ack parsed delivery failed", "invoice_id", payload.InvoiceID, "error", ackErr)
	}
}

// MatcherWorker consumes q.invoice.parsed, loads PO/GRN context, executes the deterministic
// 3-way matcher, persists discrepancies + status + audit event atomically, and publishes invoice.reconciled.
type MatcherWorker struct {
	repo     WorkerRepository
	pub      broker.Publisher
	failures FailureHandler
	logger   *slog.Logger
}

// NewMatcherWorker creates a MatcherWorker with the provided dependencies.
func NewMatcherWorker(
	repo WorkerRepository,
	pub broker.Publisher,
	failures FailureHandler,
	logger *slog.Logger,
) *MatcherWorker {
	if logger == nil {
		logger = slog.Default()
	}
	return &MatcherWorker{
		repo:     repo,
		pub:      pub,
		failures: failures,
		logger:   logger,
	}
}

// HandleDelivery processes a single AMQP delivery from q.invoice.parsed.
func (w *MatcherWorker) HandleDelivery(ctx context.Context, d amqp.Delivery) {
	var env broker.EventEnvelope
	if err := json.Unmarshal(d.Body, &env); err != nil {
		_ = w.failures.HandleDeliveryFailure(ctx, d, true, fmt.Sprintf("invalid event envelope JSON: %v", err))
		return
	}

	var payload broker.InvoiceParsedPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil || payload.InvoiceID == uuid.Nil {
		_ = w.failures.HandleDeliveryFailure(ctx, d, true, fmt.Sprintf("invalid invoice.parsed payload: %v", err))
		return
	}

	inv, lines, err := w.repo.GetInvoiceForMatching(ctx, payload.InvoiceID)
	if err != nil {
		_ = w.failures.HandleDeliveryFailure(ctx, d, false, fmt.Sprintf("load invoice for matching: %v", err))
		return
	}

	// Idempotent skip if already reconciled, resolved, or failed parsing.
	switch inv.Status {
	case domain.StatusAutoApproved,
		domain.StatusNeedsReview,
		domain.StatusManuallyApproved,
		domain.StatusRejected,
		domain.StatusParseFailed:
		w.logger.Info("matcher skipping already-reconciled invoice",
			"invoice_id", inv.ID,
			"status", inv.Status,
			"correlation_id", env.CorrelationID,
		)
		_ = d.Ack(false)
		return
	case domain.StatusUploaded:
		// Out-of-order redelivery before parser commit; retry transiently.
		_ = w.failures.HandleDeliveryFailure(ctx, d, false, "invoice still in UPLOADED state; retrying match")
		return
	}

	var po *domain.PurchaseOrder
	var poID *uuid.UUID
	if strings.TrimSpace(inv.PONumberRaw) != "" {
		foundPO, poErr := w.repo.GetPurchaseOrderByNumber(ctx, inv.PONumberRaw)
		if poErr == nil && foundPO != nil {
			po = foundPO
			id := foundPO.ID
			poID = &id
		} else if poErr != nil && !errors.Is(poErr, domain.ErrNotFound) {
			_ = w.failures.HandleDeliveryFailure(ctx, d, false, fmt.Sprintf("lookup PO %q: %v", inv.PONumberRaw, poErr))
			return
		}
	} else if inv.PurchaseOrderID != nil {
		foundPO, poErr := w.repo.GetPurchaseOrderByID(ctx, *inv.PurchaseOrderID)
		if poErr == nil && foundPO != nil {
			po = foundPO
			id := foundPO.ID
			poID = &id
		} else if poErr != nil && !errors.Is(poErr, domain.ErrNotFound) {
			_ = w.failures.HandleDeliveryFailure(ctx, d, false, fmt.Sprintf("lookup PO by id: %v", poErr))
			return
		}
	}

	var grnSummary *domain.GoodsReceiptSummary
	if po != nil {
		summary, grnErr := w.repo.GetGoodsReceiptSummaryByPOID(ctx, po.ID)
		if grnErr != nil {
			_ = w.failures.HandleDeliveryFailure(ctx, d, false, fmt.Sprintf("lookup GRN summary for PO %s: %v", po.PONumber, grnErr))
			return
		}
		grnSummary = summary
	}

	parsedView := domain.ParsedInvoice{
		SupplierCode:    inv.SupplierCodeRaw,
		InvoiceNumber:   inv.InvoiceNumber,
		PONumber:        inv.PONumberRaw,
		Currency:        inv.Currency,
		SubtotalPence:   inv.SubtotalPence,
		VATTotalPence:   inv.VATTotalPence,
		GrandTotalPence: inv.GrandTotalPence,
		Lines:           lines,
	}
	if inv.IssueDate != nil {
		parsedView.IssueDate = *inv.IssueDate
	}
	parsedView.DueDate = inv.DueDate

	result := matcher.Evaluate(matcher.MatchInput{
		InvoiceID:  inv.ID,
		SupplierID: inv.SupplierID,
		Invoice:    parsedView,
		PO:         po,
		GRN:        grnSummary,
	})

	alreadyProcessed, err := w.repo.SaveMatchResult(ctx, inv.ID, poID, result, actorMatcher)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidStateTransition) {
			_ = w.failures.HandleDeliveryFailure(ctx, d, true, err.Error())
			return
		}
		_ = w.failures.HandleDeliveryFailure(ctx, d, false, fmt.Sprintf("save match result: %v", err))
		return
	}
	if alreadyProcessed {
		_ = d.Ack(false)
		return
	}

	var totalVariance int64
	for _, disc := range result.Discrepancies {
		totalVariance += domain.AbsInt64(disc.VariancePence)
	}

	recEnv, err := broker.NewEnvelope(broker.RoutingKeyInvoiceReconciled, env.CorrelationID, broker.InvoiceReconciledPayload{
		InvoiceID:          inv.ID,
		Status:             result.Status,
		DiscrepancyCount:   len(result.Discrepancies),
		TotalVariancePence: totalVariance,
	})
	if err != nil {
		w.logger.Error("build invoice.reconciled envelope failed", "invoice_id", inv.ID, "error", err)
	} else if pubErr := w.pub.PublishEvent(ctx, broker.RoutingKeyInvoiceReconciled, recEnv); pubErr != nil {
		// DB transaction already committed; log error without failing ack to prevent duplicate work.
		w.logger.Warn("publish invoice.reconciled failed after commit", "invoice_id", inv.ID, "error", pubErr)
	}

	w.logger.Info("invoice reconciled",
		"invoice_id", inv.ID,
		"status", result.Status,
		"discrepancies", len(result.Discrepancies),
		"correlation_id", env.CorrelationID,
	)

	if ackErr := d.Ack(false); ackErr != nil {
		w.logger.Error("ack reconciled delivery failed", "invoice_id", inv.ID, "error", ackErr)
	}
}
