package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"

	"reconcile-engine/internal/broker"
	"reconcile-engine/internal/domain"
	"reconcile-engine/internal/matcher"
	"reconcile-engine/internal/parser"
)

type mockAck struct {
	mu       sync.Mutex
	acked    int
	nacked   int
	requeued bool
}

func (m *mockAck) Ack(_ uint64, _ bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.acked++
	return nil
}

func (m *mockAck) Nack(_ uint64, _ bool, requeue bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nacked++
	m.requeued = requeue
	return nil
}

func (m *mockAck) Reject(_ uint64, requeue bool) error {
	return m.Nack(0, false, requeue)
}

type mockFailureHandler struct {
	mu        sync.Mutex
	calls     int
	permanent bool
	lastErr   string
}

func (f *mockFailureHandler) HandleDeliveryFailure(_ context.Context, d amqp.Delivery, permanent bool, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.permanent = permanent
	f.lastErr = reason
	if permanent {
		return d.Nack(false, false)
	}
	return d.Ack(false)
}

type mockPublisher struct {
	mu        sync.Mutex
	routing   []string
	envelopes []broker.EventEnvelope
}

func (p *mockPublisher) PublishEvent(_ context.Context, routingKey string, env broker.EventEnvelope) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.routing = append(p.routing, routingKey)
	p.envelopes = append(p.envelopes, env)
	return nil
}

type mockStore struct {
	data map[string][]byte
	err  error
}

func (s *mockStore) EnsureBucket(context.Context) error { return nil }
func (s *mockStore) HealthCheck(context.Context) error  { return nil }
func (s *mockStore) PutInvoice(context.Context, string, domain.SourceFormat, string, []byte) (string, error) {
	return "", nil
}
func (s *mockStore) Get(_ context.Context, key string) ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	b, ok := s.data[key]
	if !ok {
		return nil, fmt.Errorf("missing key %s", key)
	}
	return b, nil
}

type mockWorkerRepo struct {
	invoices      map[uuid.UUID]*domain.Invoice
	lines         map[uuid.UUID][]domain.ParsedLine
	discrepancies map[uuid.UUID][]domain.Discrepancy
	suppliers     map[string]*domain.Supplier
	posByNum      map[string]*domain.PurchaseOrder
	grnsByPO      map[uuid.UUID]*domain.GoodsReceiptSummary
	saveParsedErr error
}

func newMockWorkerRepo() *mockWorkerRepo {
	return &mockWorkerRepo{
		invoices:      make(map[uuid.UUID]*domain.Invoice),
		lines:         make(map[uuid.UUID][]domain.ParsedLine),
		discrepancies: make(map[uuid.UUID][]domain.Discrepancy),
		suppliers:     make(map[string]*domain.Supplier),
		posByNum:      make(map[string]*domain.PurchaseOrder),
		grnsByPO:      make(map[uuid.UUID]*domain.GoodsReceiptSummary),
	}
}

func (r *mockWorkerRepo) CheckInvoiceForParsing(_ context.Context, id uuid.UUID) (domain.InvoiceStatus, string, domain.SourceFormat, bool, error) {
	inv, ok := r.invoices[id]
	if !ok {
		return "", "", "", false, domain.ErrNotFound
	}
	return inv.Status, inv.StorageKey, inv.SourceFormat, inv.Status != domain.StatusUploaded, nil
}

func (r *mockWorkerRepo) SaveParsedInvoice(_ context.Context, id uuid.UUID, parsed *domain.ParsedInvoice, supID, poID *uuid.UUID, _ string) (bool, error) {
	if r.saveParsedErr != nil {
		return false, r.saveParsedErr
	}
	inv := r.invoices[id]
	if inv.Status != domain.StatusUploaded {
		return true, nil
	}
	if err := domain.CanTransition(inv.Status, domain.StatusParsed); err != nil {
		return false, err
	}
	inv.Status = domain.StatusParsed
	inv.SupplierID = supID
	inv.SupplierCodeRaw = parsed.SupplierCode
	inv.InvoiceNumber = parsed.InvoiceNumber
	inv.PONumberRaw = parsed.PONumber
	inv.PurchaseOrderID = poID
	inv.Currency = parsed.Currency
	inv.SubtotalPence = parsed.SubtotalPence
	inv.VATTotalPence = parsed.VATTotalPence
	inv.GrandTotalPence = parsed.GrandTotalPence
	r.lines[id] = parsed.Lines
	return false, nil
}

func (r *mockWorkerRepo) MarkParseFailed(_ context.Context, id uuid.UUID, msg, _ string, _ map[string]any) (bool, error) {
	inv := r.invoices[id]
	if inv.Status == domain.StatusParseFailed {
		return true, nil
	}
	if err := domain.CanTransition(inv.Status, domain.StatusParseFailed); err != nil {
		return false, err
	}
	inv.Status = domain.StatusParseFailed
	inv.ParseError = msg
	return false, nil
}

func (r *mockWorkerRepo) GetSupplierByCode(_ context.Context, code string) (*domain.Supplier, error) {
	if s, ok := r.suppliers[code]; ok {
		return s, nil
	}
	return nil, domain.ErrNotFound
}

func (r *mockWorkerRepo) GetPurchaseOrderByNumber(_ context.Context, num string) (*domain.PurchaseOrder, error) {
	if po, ok := r.posByNum[num]; ok {
		return po, nil
	}
	return nil, domain.ErrNotFound
}

func (r *mockWorkerRepo) GetPurchaseOrderByID(_ context.Context, id uuid.UUID) (*domain.PurchaseOrder, error) {
	for _, po := range r.posByNum {
		if po.ID == id {
			return po, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *mockWorkerRepo) GetGoodsReceiptSummaryByPOID(_ context.Context, poID uuid.UUID) (*domain.GoodsReceiptSummary, error) {
	if g, ok := r.grnsByPO[poID]; ok {
		return g, nil
	}
	return &domain.GoodsReceiptSummary{PurchaseOrderID: poID, ReceiptCount: 0, AcceptedBySKU: map[string]int64{}}, nil
}

func (r *mockWorkerRepo) GetInvoiceForMatching(_ context.Context, id uuid.UUID) (*domain.Invoice, []domain.ParsedLine, error) {
	inv, ok := r.invoices[id]
	if !ok {
		return nil, nil, domain.ErrNotFound
	}
	cp := *inv
	return &cp, r.lines[id], nil
}

func (r *mockWorkerRepo) SaveMatchResult(_ context.Context, id uuid.UUID, poID *uuid.UUID, res matcher.MatchResult, _ string) (bool, error) {
	inv := r.invoices[id]
	if inv.Status == domain.StatusAutoApproved || inv.Status == domain.StatusNeedsReview {
		return true, nil
	}
	if err := domain.CanTransition(inv.Status, res.Status); err != nil {
		return false, err
	}
	inv.Status = res.Status
	if poID != nil {
		inv.PurchaseOrderID = poID
	}
	r.discrepancies[id] = res.Discrepancies
	return false, nil
}

func makeDelivery(t *testing.T, routingKey string, payload any, ack *mockAck) amqp.Delivery {
	t.Helper()
	env, err := broker.NewEnvelope(routingKey, "corr-unit", payload)
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("Marshal envelope: %v", err)
	}
	return amqp.Delivery{
		Acknowledger: ack,
		RoutingKey:   routingKey,
		Body:         body,
	}
}

func TestParserWorker_HappyPathAndIdempotency(t *testing.T) {
	repo := newMockWorkerRepo()
	invID := uuid.New()
	supID := uuid.New()
	poID := uuid.New()

	repo.suppliers["SUP-ACME-01"] = &domain.Supplier{ID: supID, SupplierCode: "SUP-ACME-01"}
	repo.posByNum["PO-2026-1001"] = &domain.PurchaseOrder{ID: poID, PONumber: "PO-2026-1001", SupplierID: supID}
	repo.invoices[invID] = &domain.Invoice{
		ID:           invID,
		StorageKey:   "raw-invoices/2026/11/test.csv",
		SourceFormat: domain.FormatCSV,
		Status:       domain.StatusUploaded,
	}

	csvData := []byte("invoice_number,po_number,supplier_code,issue_date,currency,sku,description,qty,unit_price_pence,vat_rate_bps,line_net_pence,line_vat_pence\nINV-1,PO-2026-1001,SUP-ACME-01,2026-11-20,GBP,SKU-1,Bolts,10,2500,2000,25000,5000\n")
	store := &mockStore{data: map[string][]byte{"raw-invoices/2026/11/test.csv": csvData}}
	pub := &mockPublisher{}
	fh := &mockFailureHandler{}

	pw := NewParserWorker(repo, store, parser.NewDefaultRouter(), pub, fh, nil)

	// First delivery -> PARSED + publish invoice.parsed + Ack.
	ack1 := &mockAck{}
	pw.HandleDelivery(context.Background(), makeDelivery(t, broker.RoutingKeyInvoiceUploaded, broker.InvoiceUploadedPayload{
		InvoiceID:    invID,
		StorageKey:   "raw-invoices/2026/11/test.csv",
		SourceFormat: domain.FormatCSV,
	}, ack1))

	if repo.invoices[invID].Status != domain.StatusParsed {
		t.Fatalf("status = %s, want PARSED", repo.invoices[invID].Status)
	}
	if ack1.acked != 1 || len(pub.routing) != 1 || pub.routing[0] != broker.RoutingKeyInvoiceParsed {
		t.Fatalf("expected 1 ack and 1 invoice.parsed publish, got ack=%d pub=%v", ack1.acked, pub.routing)
	}

	// Redelivery while PARSED -> skips storage & parsing, Acks immediately, no duplicate publish.
	store.err = errors.New("should not be called on redelivery")
	ack2 := &mockAck{}
	pw.HandleDelivery(context.Background(), makeDelivery(t, broker.RoutingKeyInvoiceUploaded, broker.InvoiceUploadedPayload{
		InvoiceID:    invID,
		StorageKey:   "raw-invoices/2026/11/test.csv",
		SourceFormat: domain.FormatCSV,
	}, ack2))

	if ack2.acked != 1 || fh.calls != 0 || len(pub.routing) != 1 {
		t.Fatalf("redelivery should skip and ack cleanly: ack=%d fh=%d pub=%d", ack2.acked, fh.calls, len(pub.routing))
	}
}

func TestParserWorker_ParseFailureAndTransientRetry(t *testing.T) {
	t.Run("corrupt XML marks PARSE_FAILED and routes permanently to DLQ", func(t *testing.T) {
		repo := newMockWorkerRepo()
		invID := uuid.New()
		repo.invoices[invID] = &domain.Invoice{
			ID:           invID,
			StorageKey:   "raw-invoices/2026/11/bad.xml",
			SourceFormat: domain.FormatUBLXML,
			Status:       domain.StatusUploaded,
		}
		store := &mockStore{data: map[string][]byte{"raw-invoices/2026/11/bad.xml": []byte("<Invoice><broken")}}
		pub := &mockPublisher{}
		fh := &mockFailureHandler{}
		ack := &mockAck{}

		pw := NewParserWorker(repo, store, parser.NewDefaultRouter(), pub, fh, nil)
		pw.HandleDelivery(context.Background(), makeDelivery(t, broker.RoutingKeyInvoiceUploaded, broker.InvoiceUploadedPayload{
			InvoiceID:    invID,
			StorageKey:   "raw-invoices/2026/11/bad.xml",
			SourceFormat: domain.FormatUBLXML,
		}, ack))

		if repo.invoices[invID].Status != domain.StatusParseFailed {
			t.Fatalf("status = %s, want PARSE_FAILED", repo.invoices[invID].Status)
		}
		if fh.calls != 1 || !fh.permanent || ack.nacked != 1 || ack.requeued {
			t.Fatalf("expected permanent DLQ nack(requeue=false), got fh=(calls:%d perm:%v) nack=%d requeue=%v",
				fh.calls, fh.permanent, ack.nacked, ack.requeued)
		}
	})

	t.Run("transient storage error schedules non-permanent retry", func(t *testing.T) {
		repo := newMockWorkerRepo()
		invID := uuid.New()
		repo.invoices[invID] = &domain.Invoice{
			ID:           invID,
			StorageKey:   "raw-invoices/2026/11/ok.csv",
			SourceFormat: domain.FormatCSV,
			Status:       domain.StatusUploaded,
		}
		store := &mockStore{err: errors.New("minio connection reset")}
		pub := &mockPublisher{}
		fh := &mockFailureHandler{}
		ack := &mockAck{}

		pw := NewParserWorker(repo, store, parser.NewDefaultRouter(), pub, fh, nil)
		pw.HandleDelivery(context.Background(), makeDelivery(t, broker.RoutingKeyInvoiceUploaded, broker.InvoiceUploadedPayload{
			InvoiceID:    invID,
			StorageKey:   "raw-invoices/2026/11/ok.csv",
			SourceFormat: domain.FormatCSV,
		}, ack))

		if repo.invoices[invID].Status != domain.StatusUploaded {
			t.Fatalf("status changed on transient error: %s", repo.invoices[invID].Status)
		}
		if fh.calls != 1 || fh.permanent {
			t.Fatalf("expected transient retry failure call, got calls=%d permanent=%v", fh.calls, fh.permanent)
		}
	})

	t.Run("duplicate supplier invoice number routes to PARSE_FAILED and DLQ", func(t *testing.T) {
		repo := newMockWorkerRepo()
		repo.saveParsedErr = fmt.Errorf("uq_supplier_invoice_num: %w", domain.ErrDuplicateInvoice)
		invID := uuid.New()
		repo.invoices[invID] = &domain.Invoice{
			ID:           invID,
			StorageKey:   "raw-invoices/2026/11/dup.csv",
			SourceFormat: domain.FormatCSV,
			Status:       domain.StatusUploaded,
		}
		csvData := []byte("invoice_number,po_number,supplier_code,issue_date,currency,sku,description,qty,unit_price_pence,vat_rate_bps,line_net_pence,line_vat_pence\nINV-1,PO-1,SUP-1,2026-11-20,GBP,SKU-1,Bolts,10,2500,2000,25000,5000\n")
		store := &mockStore{data: map[string][]byte{"raw-invoices/2026/11/dup.csv": csvData}}
		pub := &mockPublisher{}
		fh := &mockFailureHandler{}
		ack := &mockAck{}

		pw := NewParserWorker(repo, store, parser.NewDefaultRouter(), pub, fh, nil)
		pw.HandleDelivery(context.Background(), makeDelivery(t, broker.RoutingKeyInvoiceUploaded, broker.InvoiceUploadedPayload{
			InvoiceID:    invID,
			StorageKey:   "raw-invoices/2026/11/dup.csv",
			SourceFormat: domain.FormatCSV,
		}, ack))

		if repo.invoices[invID].Status != domain.StatusParseFailed || !fh.permanent {
			t.Fatalf("expected PARSE_FAILED + permanent DLQ, got status=%s permanent=%v", repo.invoices[invID].Status, fh.permanent)
		}
	})
}

func TestMatcherWorker_CleanAndDiscrepancyMatching(t *testing.T) {
	supID := uuid.New()
	poID := uuid.New()

	newBaseRepo := func(unitPricePence int64) (*mockWorkerRepo, uuid.UUID) {
		repo := newMockWorkerRepo()
		invID := uuid.New()
		lineNet := domain.CalculateLineNetPence(10, unitPricePence)
		lineVAT := domain.CalculateVATPence(lineNet, 2000)

		repo.posByNum["PO-100"] = &domain.PurchaseOrder{
			ID:            poID,
			PONumber:      "PO-100",
			SupplierID:    supID,
			SupplierCode:  "SUP-1",
			Currency:      "GBP",
			Status:        domain.POStatusReceived,
			TotalNetPence: 25000,
			Lines: []domain.POLine{
				{LineNumber: 1, SKU: "SKU-1", OrderedQty: 10, UnitPricePence: 2500, VATRateBPS: 2000},
			},
		}
		repo.grnsByPO[poID] = &domain.GoodsReceiptSummary{
			PurchaseOrderID: poID,
			ReceiptCount:    1,
			AcceptedBySKU:   map[string]int64{"SKU-1": 10},
		}
		repo.invoices[invID] = &domain.Invoice{
			ID:              invID,
			Status:          domain.StatusParsed,
			SupplierID:      &supID,
			SupplierCodeRaw: "SUP-1",
			InvoiceNumber:   "INV-100",
			PONumberRaw:     "PO-100",
			Currency:        "GBP",
			SubtotalPence:   lineNet,
			VATTotalPence:   lineVAT,
			GrandTotalPence: lineNet + lineVAT,
		}
		repo.lines[invID] = []domain.ParsedLine{
			{
				LineNumber:     1,
				SKU:            "SKU-1",
				Quantity:       10,
				UnitPricePence: unitPricePence,
				VATRateBPS:     2000,
				LineNetPence:   lineNet,
				LineVATPence:   lineVAT,
			},
		}
		return repo, invID
	}

	t.Run("clean 3-way match transitions to AUTO_APPROVED", func(t *testing.T) {
		repo, invID := newBaseRepo(2500)
		pub := &mockPublisher{}
		fh := &mockFailureHandler{}
		ack := &mockAck{}

		mw := NewMatcherWorker(repo, pub, fh, nil)
		mw.HandleDelivery(context.Background(), makeDelivery(t, broker.RoutingKeyInvoiceParsed, broker.InvoiceParsedPayload{
			InvoiceID: invID,
		}, ack))

		if repo.invoices[invID].Status != domain.StatusAutoApproved {
			t.Fatalf("status = %s, want AUTO_APPROVED", repo.invoices[invID].Status)
		}
		if ack.acked != 1 || len(pub.routing) != 1 || pub.routing[0] != broker.RoutingKeyInvoiceReconciled {
			t.Fatalf("expected 1 ack and 1 invoice.reconciled publish")
		}
	})

	t.Run("price variance transitions to NEEDS_REVIEW with PRICE_VARIANCE discrepancy", func(t *testing.T) {
		repo, invID := newBaseRepo(2900)
		pub := &mockPublisher{}
		fh := &mockFailureHandler{}
		ack := &mockAck{}

		mw := NewMatcherWorker(repo, pub, fh, nil)
		mw.HandleDelivery(context.Background(), makeDelivery(t, broker.RoutingKeyInvoiceParsed, broker.InvoiceParsedPayload{
			InvoiceID: invID,
		}, ack))

		if repo.invoices[invID].Status != domain.StatusNeedsReview {
			t.Fatalf("status = %s, want NEEDS_REVIEW", repo.invoices[invID].Status)
		}
		if len(repo.discrepancies[invID]) != 1 || repo.discrepancies[invID][0].Code != matcher.CodePriceVariance {
			t.Fatalf("unexpected discrepancies: %+v", repo.discrepancies[invID])
		}
		if repo.discrepancies[invID][0].VariancePence != 4000 {
			t.Fatalf("variance_pence = %d, want 4000", repo.discrepancies[invID][0].VariancePence)
		}
	})
}
