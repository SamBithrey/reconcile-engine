package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"reconcile-engine/internal/broker"
	"reconcile-engine/internal/domain"
	"reconcile-engine/internal/repository"
	"reconcile-engine/internal/storage"
)

type fakeRepo struct {
	mu       sync.Mutex
	bySHA    map[string]*domain.Invoice
	byID     map[uuid.UUID]*domain.Invoice
	pingErr  error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		bySHA: make(map[string]*domain.Invoice),
		byID:  make(map[uuid.UUID]*domain.Invoice),
	}
}

func (f *fakeRepo) Ping(context.Context) error { return f.pingErr }

func (f *fakeRepo) GetInvoiceBySHA256(_ context.Context, sha string) (*domain.Invoice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	inv, ok := f.bySHA[sha]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *inv
	return &cp, nil
}

func (f *fakeRepo) CreateUploadedInvoice(_ context.Context, sha, key string, format domain.SourceFormat) (*domain.Invoice, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if existing, ok := f.bySHA[sha]; ok {
		cp := *existing
		return &cp, false, nil
	}
	inv := &domain.Invoice{
		ID:           uuid.New(),
		FileSHA256:   sha,
		StorageKey:   key,
		SourceFormat: format,
		Status:       domain.StatusUploaded,
		Currency:     "GBP",
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	f.bySHA[sha] = inv
	f.byID[inv.ID] = inv
	cp := *inv
	return &cp, true, nil
}

func (f *fakeRepo) ListInvoices(_ context.Context, _ repository.InvoiceFilter) ([]domain.Invoice, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.Invoice, 0, len(f.byID))
	for _, inv := range f.byID {
		out = append(out, *inv)
	}
	return out, len(out), nil
}

func (f *fakeRepo) GetInvoiceDetail(_ context.Context, id uuid.UUID) (*domain.InvoiceDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	inv, ok := f.byID[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &domain.InvoiceDetail{Invoice: *inv}, nil
}

func (f *fakeRepo) ResolveInvoice(_ context.Context, id uuid.UUID, target domain.InvoiceStatus, actor, _ string) (*domain.Invoice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	inv, ok := f.byID[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if err := domain.CanTransition(inv.Status, target); err != nil {
		return nil, err
	}
	inv.Status = target
	inv.UpdatedAt = time.Now().UTC()
	_ = actor
	cp := *inv
	return &cp, nil
}

func (f *fakeRepo) GetMetrics(_ context.Context) (*domain.MetricsSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &domain.MetricsSummary{
		TotalInvoices:  int64(len(f.byID)),
		CountsByStatus: map[domain.InvoiceStatus]int64{},
	}, nil
}

type fakeStore struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newFakeStore() *fakeStore {
	return &fakeStore{objects: make(map[string][]byte)}
}

func (s *fakeStore) EnsureBucket(context.Context) error { return nil }
func (s *fakeStore) HealthCheck(context.Context) error  { return nil }
func (s *fakeStore) PutInvoice(_ context.Context, sha string, format domain.SourceFormat, _ string, data []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := storage.BuildStorageKey(time.Now().UTC(), sha, format)
	s.objects[key] = append([]byte(nil), data...)
	return key, nil
}
func (s *fakeStore) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.objects[key]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return b, nil
}

type fakeBroker struct {
	mu        sync.Mutex
	published []broker.EventEnvelope
	healthErr error
}

func (b *fakeBroker) PublishEvent(_ context.Context, _ string, env broker.EventEnvelope) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.published = append(b.published, env)
	return nil
}

func (b *fakeBroker) HealthCheck() error { return b.healthErr }

func TestAPI_UploadDeduplicationAndMaxSize(t *testing.T) {
	repo := newFakeRepo()
	store := newFakeStore()
	brk := &fakeBroker{}
	srv := NewServer(repo, store, brk, nil)

	xmlSample := []byte(`<?xml version="1.0"?><Invoice><cbc:ID>INV-1</cbc:ID></Invoice>`)

	makeMultipartReq := func(filename string, content []byte) *http.Request {
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		fw, err := w.CreateFormFile("file", filename)
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		_, _ = fw.Write(content)
		_ = w.Close()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/invoices", &body)
		req.Header.Set("Content-Type", w.FormDataContentType())
		return req
	}

	// 1. First upload -> 202 Accepted, 1 event published.
	rr1 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr1, makeMultipartReq("invoice.xml", xmlSample))
	if rr1.Code != http.StatusAccepted {
		t.Fatalf("first upload status = %d, want 202 (body=%s)", rr1.Code, rr1.Body.String())
	}
	var resp1 uploadResponse
	if err := json.Unmarshal(rr1.Body.Bytes(), &resp1); err != nil {
		t.Fatalf("decode resp1: %v", err)
	}
	if resp1.Duplicate || resp1.Status != domain.StatusUploaded || resp1.SourceFormat != domain.FormatUBLXML {
		t.Fatalf("unexpected first upload response: %+v", resp1)
	}
	if len(brk.published) != 1 {
		t.Fatalf("published events = %d, want 1", len(brk.published))
	}

	// 2. Duplicate SHA-256 upload -> 200 OK, same invoice_id, NO second publish.
	rr2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr2, makeMultipartReq("invoice_copy.xml", xmlSample))
	if rr2.Code != http.StatusOK {
		t.Fatalf("duplicate upload status = %d, want 200 (body=%s)", rr2.Code, rr2.Body.String())
	}
	var resp2 uploadResponse
	if err := json.Unmarshal(rr2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("decode resp2: %v", err)
	}
	if !resp2.Duplicate || resp2.InvoiceID != resp1.InvoiceID {
		t.Fatalf("duplicate response = %+v, want duplicate=true and invoice_id=%s", resp2, resp1.InvoiceID)
	}
	if len(brk.published) != 1 {
		t.Fatalf("published events after duplicate = %d, want still 1", len(brk.published))
	}

	// 3. Oversized upload (> 10 MB) -> 413 Request Entity Too Large.
	oversized := bytes.Repeat([]byte("a"), int(maxUploadBytes)+10)
	reqBig := httptest.NewRequest(http.MethodPost, "/api/v1/invoices", bytes.NewReader(oversized))
	reqBig.Header.Set("Content-Type", "application/xml")
	rrBig := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rrBig, reqBig)
	if rrBig.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized upload status = %d, want 413", rrBig.Code)
	}
}

func TestAPI_ResolveStateTransitions(t *testing.T) {
	repo := newFakeRepo()
	store := newFakeStore()
	brk := &fakeBroker{}
	srv := NewServer(repo, store, brk, nil)

	reviewID := uuid.New()
	repo.byID[reviewID] = &domain.Invoice{
		ID:           reviewID,
		Status:       domain.StatusNeedsReview,
		SourceFormat: domain.FormatPDF,
	}

	autoApprovedID := uuid.New()
	repo.byID[autoApprovedID] = &domain.Invoice{
		ID:           autoApprovedID,
		Status:       domain.StatusAutoApproved,
		SourceFormat: domain.FormatUBLXML,
	}

	// Valid resolution: NEEDS_REVIEW -> MANUALLY_APPROVED -> 200 OK.
	reqOK := httptest.NewRequest(http.MethodPost, "/api/v1/invoices/"+reviewID.String()+"/resolve",
		bytes.NewBufferString(`{"status":"MANUALLY_APPROVED","actor":"ops@acme.co.uk","reason":"verified with vendor"}`))
	rrOK := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rrOK, reqOK)
	if rrOK.Code != http.StatusOK {
		t.Fatalf("valid resolve status = %d, want 200 (body=%s)", rrOK.Code, rrOK.Body.String())
	}

	// Invalid transition: AUTO_APPROVED -> REJECTED -> 409 Conflict.
	reqConflict := httptest.NewRequest(http.MethodPost, "/api/v1/invoices/"+autoApprovedID.String()+"/resolve",
		bytes.NewBufferString(`{"action":"REJECT","actor":"ops@acme.co.uk","reason":"cannot reject auto approved"}`))
	rrConflict := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rrConflict, reqConflict)
	if rrConflict.Code != http.StatusConflict {
		t.Fatalf("invalid resolve status = %d, want 409 (body=%s)", rrConflict.Code, rrConflict.Body.String())
	}
}

func TestAPI_HealthAndMetrics(t *testing.T) {
	repo := newFakeRepo()
	store := newFakeStore()
	brk := &fakeBroker{}
	srv := NewServer(repo, store, brk, nil)

	rrHealth := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rrHealth, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rrHealth.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", rrHealth.Code)
	}

	brk.healthErr = fmt.Errorf("connection closed")
	rrDegraded := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rrDegraded, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rrDegraded.Code != http.StatusServiceUnavailable {
		t.Fatalf("degraded healthz status = %d, want 503", rrDegraded.Code)
	}

	rrMetrics := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rrMetrics, httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil))
	if rrMetrics.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want 200", rrMetrics.Code)
	}
}
