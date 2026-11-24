package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"reconcile-engine/internal/domain"
	"reconcile-engine/internal/matcher"
)

func TestPostgresRepo_Integration(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("set INTEGRATION_TEST=1 to run PostgreSQL repository integration tests")
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://reconcile:reconcile@localhost:5432/reconcile?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool, err := Connect(ctx, dbURL)
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	defer pool.Close()

	repo := NewPostgresRepo(pool)

	uniqueSuffix := uuid.New().String()[:8]
	supCode := "SUP-IT-" + uniqueSuffix
	poNum := "PO-IT-" + uniqueSuffix
	grnNum := "GRN-IT-" + uniqueSuffix
	invNum := "INV-IT-" + uniqueSuffix

	sup := domain.Supplier{
		ID:           uuid.New(),
		SupplierCode: supCode,
		Name:         "Integration Supplier Ltd",
		VATNumber:    "GB999999999",
	}
	if err := repo.UpsertSupplier(ctx, sup); err != nil {
		t.Fatalf("UpsertSupplier() error: %v", err)
	}

	po := domain.PurchaseOrder{
		ID:            uuid.New(),
		PONumber:      poNum,
		SupplierID:    sup.ID,
		SupplierCode:  supCode,
		Currency:      "GBP",
		Status:        domain.POStatusReceived,
		TotalNetPence: 25000,
		Lines: []domain.POLine{
			{
				ID:             uuid.New(),
				LineNumber:     1,
				SKU:            "SKU-IT-1",
				Description:    "Integration Bolt",
				OrderedQty:     10,
				UnitPricePence: 2500,
				VATRateBPS:     2000,
			},
		},
	}
	if err := repo.UpsertPurchaseOrder(ctx, po); err != nil {
		t.Fatalf("UpsertPurchaseOrder() error: %v", err)
	}

	grn := domain.GoodsReceipt{
		ID:              uuid.New(),
		GRNNumber:       grnNum,
		PurchaseOrderID: po.ID,
		ReceivedAt:      time.Now().UTC(),
		Lines: []domain.GoodsReceiptLine{
			{ID: uuid.New(), SKU: "SKU-IT-1", AcceptedQty: 10, RejectedQty: 0},
		},
	}
	if err := repo.UpsertGoodsReceipt(ctx, grn); err != nil {
		t.Fatalf("UpsertGoodsReceipt() error: %v", err)
	}

	// 1. Idempotent upload by SHA-256.
	sum1 := sha256.Sum256([]byte("invoice-content-1-" + uniqueSuffix))
	sha1 := hex.EncodeToString(sum1[:])
	inv1, created, err := repo.CreateUploadedInvoice(ctx, sha1, "raw-invoices/2026/11/"+sha1+".xml", domain.FormatUBLXML)
	if err != nil {
		t.Fatalf("CreateUploadedInvoice(1) error: %v", err)
	}
	if !created || inv1.Status != domain.StatusUploaded {
		t.Fatalf("expected created=true UPLOADED, got created=%v status=%s", created, inv1.Status)
	}

	inv1Dup, createdDup, err := repo.CreateUploadedInvoice(ctx, sha1, "raw-invoices/2026/11/"+sha1+".xml", domain.FormatUBLXML)
	if err != nil {
		t.Fatalf("CreateUploadedInvoice(dup) error: %v", err)
	}
	if createdDup || inv1Dup.ID != inv1.ID {
		t.Fatalf("expected duplicate upload to return same ID %s with created=false, got %s created=%v", inv1.ID, inv1Dup.ID, createdDup)
	}

	// 2. SaveParsedInvoice + idempotency guard.
	issueDate := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	parsed := &domain.ParsedInvoice{
		SupplierCode:    supCode,
		InvoiceNumber:   invNum,
		PONumber:        poNum,
		IssueDate:       issueDate,
		Currency:        "GBP",
		SubtotalPence:   25000,
		VATTotalPence:   5000,
		GrandTotalPence: 30000,
		Lines: []domain.ParsedLine{
			{
				LineNumber:     1,
				SKU:            "SKU-IT-1",
				Description:    "Integration Bolt",
				Quantity:       10,
				UnitPricePence: 2900, // +£4.00 variance -> PRICE_VARIANCE
				VATRateBPS:     2000,
				LineNetPence:   29000,
				LineVATPence:   5800,
			},
		},
	}

	already, err := repo.SaveParsedInvoice(ctx, inv1.ID, parsed, &sup.ID, &po.ID, "system:parser")
	if err != nil || already {
		t.Fatalf("SaveParsedInvoice() = (%v, %v), want (false, nil)", already, err)
	}

	// Re-checking parsing must skip.
	st, _, _, skip, err := repo.CheckInvoiceForParsing(ctx, inv1.ID)
	if err != nil || !skip || st != domain.StatusParsed {
		t.Fatalf("CheckInvoiceForParsing() after parse = (%s, skip=%v, err=%v)", st, skip, err)
	}

	// 3. Duplicate (supplier_id, invoice_number) under different SHA-256 -> ErrDuplicateInvoice.
	sum2 := sha256.Sum256([]byte("invoice-content-2-" + uniqueSuffix))
	sha2 := hex.EncodeToString(sum2[:])
	inv2, _, err := repo.CreateUploadedInvoice(ctx, sha2, "raw-invoices/2026/11/"+sha2+".pdf", domain.FormatPDF)
	if err != nil {
		t.Fatalf("CreateUploadedInvoice(2) error: %v", err)
	}
	_, dupErr := repo.SaveParsedInvoice(ctx, inv2.ID, parsed, &sup.ID, &po.ID, "system:parser")
	if !errors.Is(dupErr, domain.ErrDuplicateInvoice) {
		t.Fatalf("expected ErrDuplicateInvoice on uq_supplier_invoice_num, got %v", dupErr)
	}

	// 4. Transactional match persistence -> NEEDS_REVIEW.
	loadedInv, loadedLines, err := repo.GetInvoiceForMatching(ctx, inv1.ID)
	if err != nil {
		t.Fatalf("GetInvoiceForMatching() error: %v", err)
	}
	loadedPO, err := repo.GetPurchaseOrderByNumber(ctx, poNum)
	if err != nil {
		t.Fatalf("GetPurchaseOrderByNumber() error: %v", err)
	}
	grnSum, err := repo.GetGoodsReceiptSummaryByPOID(ctx, loadedPO.ID)
	if err != nil {
		t.Fatalf("GetGoodsReceiptSummaryByPOID() error: %v", err)
	}

	parsedView := domain.ParsedInvoice{
		SupplierCode:    loadedInv.SupplierCodeRaw,
		InvoiceNumber:   loadedInv.InvoiceNumber,
		PONumber:        loadedInv.PONumberRaw,
		Currency:        loadedInv.Currency,
		SubtotalPence:   loadedInv.SubtotalPence,
		VATTotalPence:   loadedInv.VATTotalPence,
		GrandTotalPence: loadedInv.GrandTotalPence,
		Lines:           loadedLines,
	}
	matchRes := matcher.Evaluate(matcher.MatchInput{
		InvoiceID:  loadedInv.ID,
		SupplierID: loadedInv.SupplierID,
		Invoice:    parsedView,
		PO:         loadedPO,
		GRN:        grnSum,
	})
	if matchRes.Status != domain.StatusNeedsReview || len(matchRes.Discrepancies) == 0 {
		t.Fatalf("expected NEEDS_REVIEW with discrepancies, got %+v", matchRes)
	}

	alreadyMatched, err := repo.SaveMatchResult(ctx, inv1.ID, &loadedPO.ID, matchRes, "system:matcher")
	if err != nil || alreadyMatched {
		t.Fatalf("SaveMatchResult() = (%v, %v), want (false, nil)", alreadyMatched, err)
	}

	// Re-running SaveMatchResult must be idempotent.
	alreadyMatched2, err := repo.SaveMatchResult(ctx, inv1.ID, &loadedPO.ID, matchRes, "system:matcher")
	if err != nil || !alreadyMatched2 {
		t.Fatalf("second SaveMatchResult() = (%v, %v), want (true, nil)", alreadyMatched2, err)
	}

	// 5. HITL Resolution: NEEDS_REVIEW -> MANUALLY_APPROVED, then invalid transition -> ErrInvalidStateTransition.
	resolved, err := repo.ResolveInvoice(ctx, inv1.ID, domain.StatusManuallyApproved, "finance@acme.co.uk", "Approved price variance per email")
	if err != nil {
		t.Fatalf("ResolveInvoice() error: %v", err)
	}
	if resolved.Status != domain.StatusManuallyApproved {
		t.Fatalf("resolved status = %s, want MANUALLY_APPROVED", resolved.Status)
	}

	if _, err := repo.ResolveInvoice(ctx, inv1.ID, domain.StatusRejected, "finance@acme.co.uk", "Too late"); !errors.Is(err, domain.ErrInvalidStateTransition) {
		t.Fatalf("expected ErrInvalidStateTransition on terminal invoice, got %v", err)
	}

	// 6. Detail & Metrics queries.
	detail, err := repo.GetInvoiceDetail(ctx, inv1.ID)
	if err != nil {
		t.Fatalf("GetInvoiceDetail() error: %v", err)
	}
	if len(detail.AuditEvents) < 4 {
		t.Fatalf("expected >= 4 audit events (UPLOADED, PARSED, NEEDS_REVIEW, MANUALLY_APPROVED), got %d", len(detail.AuditEvents))
	}
	if detail.PurchaseOrder == nil || detail.GoodsReceipts == nil {
		t.Fatalf("expected PO and GRN summary in InvoiceDetail")
	}

	metrics, err := repo.GetMetrics(ctx)
	if err != nil {
		t.Fatalf("GetMetrics() error: %v", err)
	}
	if metrics.TotalInvoices < 2 {
		t.Fatalf("expected TotalInvoices >= 2, got %d", metrics.TotalInvoices)
	}

	// Cleanup test records.
	_, _ = pool.Exec(ctx, `DELETE FROM invoices WHERE id IN ($1, $2)`, inv1.ID, inv2.ID)
	_, _ = pool.Exec(ctx, `DELETE FROM goods_receipts WHERE id = $1`, grn.ID)
	_, _ = pool.Exec(ctx, `DELETE FROM purchase_orders WHERE id = $1`, po.ID)
	_, _ = pool.Exec(ctx, `DELETE FROM suppliers WHERE id = $1`, sup.ID)
	_ = fmt.Sprintf("cleaned %s", uniqueSuffix)
}
