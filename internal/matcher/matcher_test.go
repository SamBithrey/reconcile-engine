package matcher_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"reconcile-engine/internal/domain"
	"reconcile-engine/internal/matcher"
)

func baseFixture() matcher.MatchInput {
	supID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	poID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	invID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	lineID := uuid.MustParse("44444444-4444-4444-4444-444444444444")

	return matcher.MatchInput{
		InvoiceID:  invID,
		SupplierID: &supID,
		Invoice: domain.ParsedInvoice{
			SupplierCode:    "SUP-ACME-01",
			SupplierVAT:     "GB123456789",
			InvoiceNumber:   "INV-2026-0001",
			PONumber:        "PO-2026-1001",
			IssueDate:       time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC),
			Currency:        "GBP",
			SubtotalPence:   25000,
			VATTotalPence:   5000,
			GrandTotalPence: 30000,
			Lines: []domain.ParsedLine{
				{
					LineID:         &lineID,
					LineNumber:     1,
					SKU:            "SKU-BOLT-M12",
					Description:    "M12 Stainless Steel Hex Bolts",
					Quantity:       10,
					UnitPricePence: 2500,
					VATRateBPS:     2000,
					LineNetPence:   25000,
					LineVATPence:   5000,
				},
			},
		},
		PO: &domain.PurchaseOrder{
			ID:            poID,
			PONumber:      "PO-2026-1001",
			SupplierID:    supID,
			SupplierCode:  "SUP-ACME-01",
			Currency:      "GBP",
			Status:        domain.POStatusReceived,
			TotalNetPence: 25000,
			Lines: []domain.POLine{
				{
					LineNumber:     1,
					SKU:            "SKU-BOLT-M12",
					Description:    "M12 Stainless Steel Hex Bolts",
					OrderedQty:     10,
					UnitPricePence: 2500,
					VATRateBPS:     2000,
				},
			},
		},
		GRN: &domain.GoodsReceiptSummary{
			PurchaseOrderID: poID,
			ReceiptCount:    1,
			AcceptedBySKU: map[string]int64{
				"SKU-BOLT-M12": 10,
			},
		},
	}
}

func TestEvaluate_AllRules(t *testing.T) {
	otherSupID := uuid.MustParse("99999999-9999-9999-9999-999999999999")

	tests := []struct {
		name          string
		mutate        func(in *matcher.MatchInput)
		wantStatus    domain.InvoiceStatus
		wantCodes     []string
		wantVariances map[string]int64
	}{
		{
			name:       "clean exact 3-way match",
			mutate:     func(in *matcher.MatchInput) {},
			wantStatus: domain.StatusAutoApproved,
		},
		{
			name: "within +-1p rounding tolerance on line math, VAT, subtotal, and grand total",
			mutate: func(in *matcher.MatchInput) {
				// qty*price = 25000, line_net = 25001 (+1p ok)
				// expected VAT on 25001 at 20% = 5000, line_vat = 5001 (+1p ok)
				// sum(line_net) = 25001, subtotal = 25000 (-1p ok)
				// subtotal+vat = 30001, grand_total = 30002 (+1p ok)
				in.Invoice.Lines[0].LineNetPence = 25001
				in.Invoice.Lines[0].LineVATPence = 5001
				in.Invoice.SubtotalPence = 25000
				in.Invoice.VATTotalPence = 5001
				in.Invoice.GrandTotalPence = 30002
			},
			wantStatus: domain.StatusAutoApproved,
		},
		{
			name: "within price variance floor (50p on £25.00 is 2% but <= 50p)",
			mutate: func(in *matcher.MatchInput) {
				in.Invoice.Lines[0].UnitPricePence = 2550
				in.Invoice.Lines[0].LineNetPence = 25500
				in.Invoice.Lines[0].LineVATPence = 5100
				in.Invoice.SubtotalPence = 25500
				in.Invoice.VATTotalPence = 5100
				in.Invoice.GrandTotalPence = 30600
			},
			wantStatus: domain.StatusAutoApproved,
		},
		{
			name: "within price variance percent (100p on £100.00 is > 50p but exactly 1.00%)",
			mutate: func(in *matcher.MatchInput) {
				in.PO.Lines[0].UnitPricePence = 10000
				in.PO.TotalNetPence = 100000
				in.Invoice.Lines[0].UnitPricePence = 10100
				in.Invoice.Lines[0].LineNetPence = 101000
				in.Invoice.Lines[0].LineVATPence = 20200
				in.Invoice.SubtotalPence = 101000
				in.Invoice.VATTotalPence = 20200
				in.Invoice.GrandTotalPence = 121200
			},
			wantStatus: domain.StatusAutoApproved,
		},
		{
			name: "INT-01 LINE_MATH_ERROR",
			mutate: func(in *matcher.MatchInput) {
				in.Invoice.Lines[0].LineNetPence = 26000
				in.Invoice.Lines[0].LineVATPence = 5200
				in.Invoice.SubtotalPence = 26000
				in.Invoice.VATTotalPence = 5200
				in.Invoice.GrandTotalPence = 31200
			},
			wantStatus:    domain.StatusNeedsReview,
			wantCodes:     []string{matcher.CodeLineMathError},
			wantVariances: map[string]int64{matcher.CodeLineMathError: 1000},
		},
		{
			name: "INT-02 VAT_MISMATCH",
			mutate: func(in *matcher.MatchInput) {
				in.Invoice.Lines[0].LineVATPence = 5250
				in.Invoice.VATTotalPence = 5250
				in.Invoice.GrandTotalPence = 30250
			},
			wantStatus:    domain.StatusNeedsReview,
			wantCodes:     []string{matcher.CodeVATMismatch},
			wantVariances: map[string]int64{matcher.CodeVATMismatch: 250},
		},
		{
			name: "INT-03 TOTAL_MISMATCH subtotal disagrees with line net sum",
			mutate: func(in *matcher.MatchInput) {
				in.Invoice.SubtotalPence = 25500
				in.Invoice.GrandTotalPence = 30500
			},
			wantStatus:    domain.StatusNeedsReview,
			wantCodes:     []string{matcher.CodeTotalMismatch},
			wantVariances: map[string]int64{matcher.CodeTotalMismatch: 500},
		},
		{
			name: "INT-03 TOTAL_MISMATCH grand total disagrees with subtotal + VAT",
			mutate: func(in *matcher.MatchInput) {
				in.Invoice.GrandTotalPence = 30500
			},
			wantStatus:    domain.StatusNeedsReview,
			wantCodes:     []string{matcher.CodeTotalMismatch},
			wantVariances: map[string]int64{matcher.CodeTotalMismatch: 500},
		},
		{
			name: "INT-04 CURRENCY_MISMATCH",
			mutate: func(in *matcher.MatchInput) {
				in.Invoice.Currency = "EUR"
			},
			wantStatus:    domain.StatusNeedsReview,
			wantCodes:     []string{matcher.CodeCurrencyMismatch},
			wantVariances: map[string]int64{matcher.CodeCurrencyMismatch: 0},
		},
		{
			name: "3WAY-01 MISSING_PO empty po_number",
			mutate: func(in *matcher.MatchInput) {
				in.Invoice.PONumber = ""
				in.PO = nil
			},
			wantStatus:    domain.StatusNeedsReview,
			wantCodes:     []string{matcher.CodeMissingPO},
			wantVariances: map[string]int64{matcher.CodeMissingPO: 30000},
		},
		{
			name: "3WAY-01 MISSING_PO unknown po_number",
			mutate: func(in *matcher.MatchInput) {
				in.Invoice.PONumber = "PO-UNKNOWN-999"
				in.PO = nil
			},
			wantStatus:    domain.StatusNeedsReview,
			wantCodes:     []string{matcher.CodeMissingPO},
			wantVariances: map[string]int64{matcher.CodeMissingPO: 30000},
		},
		{
			name: "3WAY-02 PO_SUPPLIER_MISMATCH",
			mutate: func(in *matcher.MatchInput) {
				in.SupplierID = &otherSupID
			},
			wantStatus:    domain.StatusNeedsReview,
			wantCodes:     []string{matcher.CodePOSupplierMismatch},
			wantVariances: map[string]int64{matcher.CodePOSupplierMismatch: 30000},
		},
		{
			name: "3WAY-03 PO_CLOSED status CLOSED",
			mutate: func(in *matcher.MatchInput) {
				in.PO.Status = domain.POStatusClosed
			},
			wantStatus:    domain.StatusNeedsReview,
			wantCodes:     []string{matcher.CodePOClosed},
			wantVariances: map[string]int64{matcher.CodePOClosed: 30000},
		},
		{
			name: "3WAY-03 PO_CLOSED status CANCELLED",
			mutate: func(in *matcher.MatchInput) {
				in.PO.Status = domain.POStatusCancelled
			},
			wantStatus:    domain.StatusNeedsReview,
			wantCodes:     []string{matcher.CodePOClosed},
			wantVariances: map[string]int64{matcher.CodePOClosed: 30000},
		},
		{
			name: "3WAY-04 MISSING_GRN",
			mutate: func(in *matcher.MatchInput) {
				in.GRN = &domain.GoodsReceiptSummary{
					PurchaseOrderID: in.PO.ID,
					ReceiptCount:    0,
				}
			},
			wantStatus:    domain.StatusNeedsReview,
			wantCodes:     []string{matcher.CodeMissingGRN},
			wantVariances: map[string]int64{matcher.CodeMissingGRN: 30000},
		},
		{
			name: "3WAY-05 UNMATCHED_SKU",
			mutate: func(in *matcher.MatchInput) {
				in.Invoice.Lines[0].SKU = "SKU-ROGUE-999"
			},
			wantStatus:    domain.StatusNeedsReview,
			wantCodes:     []string{matcher.CodeUnmatchedSKU},
			wantVariances: map[string]int64{matcher.CodeUnmatchedSKU: 25000},
		},
		{
			name: "3WAY-06 QTY_EXCEEDS_GRN",
			mutate: func(in *matcher.MatchInput) {
				in.GRN.AcceptedBySKU["SKU-BOLT-M12"] = 7
			},
			wantStatus:    domain.StatusNeedsReview,
			wantCodes:     []string{matcher.CodeQtyExceedsGRN},
			wantVariances: map[string]int64{matcher.CodeQtyExceedsGRN: 7500},
		},
		{
			name: "3WAY-07 QTY_EXCEEDS_PO (also exceeds GRN if GRN==PO)",
			mutate: func(in *matcher.MatchInput) {
				// Received 12 (over-delivery accepted) but PO only ordered 10, invoiced 12.
				in.GRN.AcceptedBySKU["SKU-BOLT-M12"] = 12
				in.Invoice.Lines[0].Quantity = 12
				in.Invoice.Lines[0].LineNetPence = 30000
				in.Invoice.Lines[0].LineVATPence = 6000
				in.Invoice.SubtotalPence = 30000
				in.Invoice.VATTotalPence = 6000
				in.Invoice.GrandTotalPence = 36000
			},
			wantStatus:    domain.StatusNeedsReview,
			wantCodes:     []string{matcher.CodeQtyExceedsPO},
			wantVariances: map[string]int64{matcher.CodeQtyExceedsPO: 5000},
		},
		{
			name: "3WAY-08 PRICE_VARIANCE exceeds both 50p and 1%",
			mutate: func(in *matcher.MatchInput) {
				in.Invoice.Lines[0].UnitPricePence = 2600 // +100p (>50p) and +4% (>1%)
				in.Invoice.Lines[0].LineNetPence = 26000
				in.Invoice.Lines[0].LineVATPence = 5200
				in.Invoice.SubtotalPence = 26000
				in.Invoice.VATTotalPence = 5200
				in.Invoice.GrandTotalPence = 31200
			},
			wantStatus:    domain.StatusNeedsReview,
			wantCodes:     []string{matcher.CodePriceVariance},
			wantVariances: map[string]int64{matcher.CodePriceVariance: 1000},
		},
		{
			name: "3WAY-09 VAT_RATE_MISMATCH",
			mutate: func(in *matcher.MatchInput) {
				in.Invoice.Lines[0].VATRateBPS = 500
				in.Invoice.Lines[0].LineVATPence = 1250
				in.Invoice.VATTotalPence = 1250
				in.Invoice.GrandTotalPence = 26250
			},
			wantStatus:    domain.StatusNeedsReview,
			wantCodes:     []string{matcher.CodeVATRateMismatch},
			wantVariances: map[string]int64{matcher.CodeVATRateMismatch: -3750},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := baseFixture()
			tc.mutate(&in)

			res := matcher.Evaluate(in)
			if res.Status != tc.wantStatus {
				t.Fatalf("Status = %s, want %s (discrepancies=%+v)", res.Status, tc.wantStatus, res.Discrepancies)
			}

			if len(res.Discrepancies) != len(tc.wantCodes) {
				t.Fatalf("got %d discrepancies (%+v), want %d (%v)",
					len(res.Discrepancies), res.Discrepancies, len(tc.wantCodes), tc.wantCodes)
			}

			gotByCode := make(map[string]domain.Discrepancy, len(res.Discrepancies))
			for _, d := range res.Discrepancies {
				if d.Severity != domain.SeverityBlocking {
					t.Errorf("discrepancy %s severity = %q, want BLOCKING", d.Code, d.Severity)
				}
				if d.InvoiceID != in.InvoiceID {
					t.Errorf("discrepancy %s InvoiceID = %s, want %s", d.Code, d.InvoiceID, in.InvoiceID)
				}
				gotByCode[d.Code] = d
			}

			for _, code := range tc.wantCodes {
				d, ok := gotByCode[code]
				if !ok {
					t.Errorf("missing expected discrepancy code %s", code)
					continue
				}
				if wantVar, hasVar := tc.wantVariances[code]; hasVar && d.VariancePence != wantVar {
					t.Errorf("discrepancy %s VariancePence = %d, want %d", code, d.VariancePence, wantVar)
				}
			}
		})
	}
}
