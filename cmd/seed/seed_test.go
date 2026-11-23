package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"reconcile-engine/internal/domain"
	"reconcile-engine/internal/matcher"
	"reconcile-engine/internal/parser"
)

func TestGenerateFixtures_EndToEnd(t *testing.T) {
	outDir := t.TempDir()
	manifest, err := GenerateFixtures(outDir)
	if err != nil {
		t.Fatalf("GenerateFixtures() error: %v", err)
	}

	if len(manifest.PurchaseOrders) != 15 {
		t.Fatalf("PurchaseOrders count = %d, want 15", len(manifest.PurchaseOrders))
	}
	if len(manifest.Invoices) != 20 {
		t.Fatalf("Invoices count = %d, want 20", len(manifest.Invoices))
	}

	poByNum := make(map[string]*domain.PurchaseOrder, len(manifest.PurchaseOrders))
	for i := range manifest.PurchaseOrders {
		po := &manifest.PurchaseOrders[i]
		poByNum[po.PONumber] = po
	}

	supIDByCode := make(map[string]uuid.UUID, len(manifest.Suppliers))
	for _, s := range manifest.Suppliers {
		supIDByCode[s.SupplierCode] = s.ID
	}

	grnsByPO := make(map[uuid.UUID][]domain.GoodsReceipt)
	for _, g := range manifest.GoodsReceipts {
		grnsByPO[g.PurchaseOrderID] = append(grnsByPO[g.PurchaseOrderID], g)
	}

	router := parser.NewDefaultRouter()

	coveredCodes := make(map[string]bool)
	formatsSeen := make(map[domain.SourceFormat]int)

	for _, meta := range manifest.Invoices {
		t.Run(meta.Filename, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(outDir, "invoices", meta.Filename))
			if err != nil {
				t.Fatalf("read generated fixture %s: %v", meta.Filename, err)
			}

			detectedFmt, parsed, parseErr := router.Parse("", meta.Filename, raw)
			if detectedFmt != meta.Format {
				t.Fatalf("detected format = %s, want %s", detectedFmt, meta.Format)
			}
			formatsSeen[detectedFmt]++

			if meta.ExpectParseFail {
				if parseErr == nil {
					t.Fatalf("expected parse failure for %s, got parsed invoice %+v", meta.Filename, parsed)
				}
				if meta.ExpectScanned && !errors.Is(parseErr, domain.ErrScannedPDFUnsupported) {
					t.Fatalf("expected ErrScannedPDFUnsupported, got %v", parseErr)
				}
				return
			}
			if parseErr != nil {
				t.Fatalf("unexpected parse error for %s: %v", meta.Filename, parseErr)
			}

			var po *domain.PurchaseOrder
			var grnSummary *domain.GoodsReceiptSummary
			if foundPO, ok := poByNum[parsed.PONumber]; ok {
				po = foundPO
				summary := domain.SummarizeGoodsReceipts(foundPO.ID, grnsByPO[foundPO.ID])
				grnSummary = &summary
			}

			var supIDPtr *uuid.UUID
			if id, ok := supIDByCode[parsed.SupplierCode]; ok {
				supIDPtr = &id
			}

			res := matcher.Evaluate(matcher.MatchInput{
				InvoiceID:  deterministicUUID("invoice:" + meta.Filename),
				SupplierID: supIDPtr,
				Invoice:    *parsed,
				PO:         po,
				GRN:        grnSummary,
			})

			if res.Status != meta.ExpectedStatus {
				t.Fatalf("status = %s, want %s (discrepancies=%+v)", res.Status, meta.ExpectedStatus, res.Discrepancies)
			}

			gotCodes := make(map[string]bool, len(res.Discrepancies))
			for _, d := range res.Discrepancies {
				gotCodes[d.Code] = true
				coveredCodes[d.Code] = true
			}
			if len(res.Discrepancies) != len(meta.ExpectedCodes) {
				t.Fatalf("got discrepancies %+v, want codes %v", res.Discrepancies, meta.ExpectedCodes)
			}
			for _, wantCode := range meta.ExpectedCodes {
				if !gotCodes[wantCode] {
					t.Errorf("missing expected discrepancy code %s", wantCode)
				}
			}
		})
	}

	// Ensure every required rule INT-01..INT-04 and 3WAY-01..3WAY-09 is covered by the 20 seed invoices.
	allRuleCodes := []string{
		matcher.CodeLineMathError,
		matcher.CodeVATMismatch,
		matcher.CodeTotalMismatch,
		matcher.CodeCurrencyMismatch,
		matcher.CodeMissingPO,
		matcher.CodePOSupplierMismatch,
		matcher.CodePOClosed,
		matcher.CodeMissingGRN,
		matcher.CodeUnmatchedSKU,
		matcher.CodeQtyExceedsGRN,
		matcher.CodeQtyExceedsPO,
		matcher.CodePriceVariance,
		matcher.CodeVATRateMismatch,
	}
	for _, code := range allRuleCodes {
		if !coveredCodes[code] {
			t.Errorf("seed fixtures did not cover rule %s", code)
		}
	}

	// Ensure all three formats are represented.
	for _, f := range []domain.SourceFormat{domain.FormatUBLXML, domain.FormatPDF, domain.FormatCSV} {
		if formatsSeen[f] == 0 {
			t.Errorf("seed fixtures generated 0 invoices of format %s", f)
		}
	}
}
