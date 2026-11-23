package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/razvandimescu/gopdf/pdf"

	"reconcile-engine/internal/domain"
	"reconcile-engine/internal/matcher"
)

// FixtureManifest describes all generated seed data and expected reconciliation scenarios.
type FixtureManifest struct {
	GeneratedAt    time.Time              `json:"generated_at"`
	Suppliers      []domain.Supplier      `json:"suppliers"`
	PurchaseOrders []domain.PurchaseOrder `json:"purchase_orders"`
	GoodsReceipts  []domain.GoodsReceipt  `json:"goods_receipts"`
	Invoices       []InvoiceFixtureMeta   `json:"invoices"`
}

// InvoiceFixtureMeta captures metadata and expected verification outcomes for each generated invoice file.
type InvoiceFixtureMeta struct {
	Filename        string               `json:"filename"`
	Format          domain.SourceFormat  `json:"format"`
	Scenario        string               `json:"scenario"`
	PONumber        string               `json:"po_number,omitempty"`
	ExpectedStatus  domain.InvoiceStatus `json:"expected_status"`
	ExpectedCodes   []string             `json:"expected_codes,omitempty"`
	ExpectScanned   bool                 `json:"expect_scanned,omitempty"`
	ExpectParseFail bool                 `json:"expect_parse_fail,omitempty"`
}

var fixtureNamespace = uuid.MustParse("7c9e6679-7425-40de-944b-e07fc1f90ae7")

func deterministicUUID(seed string) uuid.UUID {
	return uuid.NewSHA1(fixtureNamespace, []byte(seed))
}

// GenerateFixtures writes 15 POs/GRNs and 20 mixed-format invoices (UBL_XML, PDF, CSV)
// into outDir covering clean matches, INT-01..INT-04, 3WAY-01..3WAY-09, and scanned/corrupt cases.
func GenerateFixtures(outDir string) (*FixtureManifest, error) {
	if outDir == "" {
		return nil, fmt.Errorf("out-dir must not be empty")
	}

	invoicesDir := filepath.Join(outDir, "invoices")
	if err := os.MkdirAll(invoicesDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output directory %s: %w", invoicesDir, err)
	}

	suppliers := buildSuppliers()
	pos, grns := buildPOsAndGRNs(suppliers)
	poByNum := make(map[string]domain.PurchaseOrder, len(pos))
	for _, po := range pos {
		poByNum[po.PONumber] = po
	}
	supByCode := make(map[string]domain.Supplier, len(suppliers))
	for _, s := range suppliers {
		supByCode[s.SupplierCode] = s
	}

	issueBase := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	dueBase := issueBase.AddDate(0, 0, 30)

	type invoiceBuildSpec struct {
		meta InvoiceFixtureMeta
		raw  func() ([]byte, error)
	}

	makeCleanInvoice := func(invNum string, po domain.PurchaseOrder) domain.ParsedInvoice {
		sup := supByCode[po.SupplierCode]
		var subtotal, vatTotal int64
		lines := make([]domain.ParsedLine, 0, len(po.Lines))
		for _, pl := range po.Lines {
			lineNet := domain.CalculateLineNetPence(pl.OrderedQty, pl.UnitPricePence)
			lineVAT := domain.CalculateVATPence(lineNet, pl.VATRateBPS)
			subtotal += lineNet
			vatTotal += lineVAT
			lines = append(lines, domain.ParsedLine{
				LineNumber:     pl.LineNumber,
				SKU:            pl.SKU,
				Description:    pl.Description,
				Quantity:       pl.OrderedQty,
				UnitPricePence: pl.UnitPricePence,
				VATRateBPS:     pl.VATRateBPS,
				LineNetPence:   lineNet,
				LineVATPence:   lineVAT,
			})
		}
		due := dueBase
		return domain.ParsedInvoice{
			SupplierCode:    sup.SupplierCode,
			SupplierVAT:     sup.VATNumber,
			InvoiceNumber:   invNum,
			PONumber:        po.PONumber,
			IssueDate:       issueBase,
			DueDate:         &due,
			Currency:        po.Currency,
			SubtotalPence:   subtotal,
			VATTotalPence:   vatTotal,
			GrandTotalPence: subtotal + vatTotal,
			Lines:           lines,
		}
	}

	specs := []invoiceBuildSpec{
		// 1. Clean UBL XML
		{
			meta: InvoiceFixtureMeta{
				Filename:       "01_clean_ubl.xml",
				Format:         domain.FormatUBLXML,
				Scenario:       "Clean 3-way match (UBL XML)",
				PONumber:       "PO-2026-1001",
				ExpectedStatus: domain.StatusAutoApproved,
			},
			raw: func() ([]byte, error) {
				inv := makeCleanInvoice("INV-2026-0001", poByNum["PO-2026-1001"])
				return renderUBLXML(inv, supByCode[inv.SupplierCode].Name, false)
			},
		},
		// 2. Clean Native PDF
		{
			meta: InvoiceFixtureMeta{
				Filename:       "02_clean_pdf.pdf",
				Format:         domain.FormatPDF,
				Scenario:       "Clean 3-way match (Native PDF)",
				PONumber:       "PO-2026-1002",
				ExpectedStatus: domain.StatusAutoApproved,
			},
			raw: func() ([]byte, error) {
				inv := makeCleanInvoice("INV-2026-0002", poByNum["PO-2026-1002"])
				return renderNativePDF(inv, false, false)
			},
		},
		// 3. Clean CSV
		{
			meta: InvoiceFixtureMeta{
				Filename:       "03_clean_csv.csv",
				Format:         domain.FormatCSV,
				Scenario:       "Clean 3-way match (CSV manifest)",
				PONumber:       "PO-2026-1003",
				ExpectedStatus: domain.StatusAutoApproved,
			},
			raw: func() ([]byte, error) {
				inv := makeCleanInvoice("INV-2026-0003", poByNum["PO-2026-1003"])
				return renderCSV(inv)
			},
		},
		// 4. Clean Multi-Page Native PDF
		{
			meta: InvoiceFixtureMeta{
				Filename:       "04_clean_multipage_pdf.pdf",
				Format:         domain.FormatPDF,
				Scenario:       "Clean multi-page 3-way match (Native PDF across 2 pages)",
				PONumber:       "PO-2026-1004",
				ExpectedStatus: domain.StatusAutoApproved,
			},
			raw: func() ([]byte, error) {
				inv := makeCleanInvoice("INV-2026-0004", poByNum["PO-2026-1004"])
				return renderNativePDF(inv, true, false)
			},
		},
		// 5. INT-01: LINE_MATH_ERROR (CSV)
		{
			meta: InvoiceFixtureMeta{
				Filename:       "05_int01_line_math.csv",
				Format:         domain.FormatCSV,
				Scenario:       "INT-01 LINE_MATH_ERROR (qty * unit_price != line_net)",
				PONumber:       "PO-2026-1005",
				ExpectedStatus: domain.StatusNeedsReview,
				ExpectedCodes:  []string{matcher.CodeLineMathError},
			},
			raw: func() ([]byte, error) {
				inv := makeCleanInvoice("INV-2026-0005", poByNum["PO-2026-1005"])
				inv.Lines[0].LineNetPence += 500
				inv.Lines[0].LineVATPence = domain.CalculateVATPence(inv.Lines[0].LineNetPence, inv.Lines[0].VATRateBPS)
				return renderCSV(inv)
			},
		},
		// 6. INT-02: VAT_MISMATCH (UBL XML with explicit bad line TaxAmount)
		{
			meta: InvoiceFixtureMeta{
				Filename:       "06_int02_vat_mismatch.xml",
				Format:         domain.FormatUBLXML,
				Scenario:       "INT-02 VAT_MISMATCH (line VAT deviates by £3.00)",
				PONumber:       "PO-2026-1006",
				ExpectedStatus: domain.StatusNeedsReview,
				ExpectedCodes:  []string{matcher.CodeVATMismatch},
			},
			raw: func() ([]byte, error) {
				inv := makeCleanInvoice("INV-2026-0006", poByNum["PO-2026-1006"])
				inv.Lines[0].LineVATPence += 300
				inv.VATTotalPence += 300
				inv.GrandTotalPence = inv.SubtotalPence + inv.VATTotalPence
				return renderUBLXML(inv, supByCode[inv.SupplierCode].Name, true)
			},
		},
		// 7. INT-03: TOTAL_MISMATCH (PDF)
		{
			meta: InvoiceFixtureMeta{
				Filename:       "07_int03_total_mismatch.pdf",
				Format:         domain.FormatPDF,
				Scenario:       "INT-03 TOTAL_MISMATCH (grand total inflated by £10.00)",
				PONumber:       "PO-2026-1007",
				ExpectedStatus: domain.StatusNeedsReview,
				ExpectedCodes:  []string{matcher.CodeTotalMismatch},
			},
			raw: func() ([]byte, error) {
				inv := makeCleanInvoice("INV-2026-0007", poByNum["PO-2026-1007"])
				inv.GrandTotalPence += 1000
				return renderNativePDF(inv, false, false)
			},
		},
		// 8. INT-04: CURRENCY_MISMATCH (UBL XML)
		{
			meta: InvoiceFixtureMeta{
				Filename:       "08_int04_currency_mismatch.xml",
				Format:         domain.FormatUBLXML,
				Scenario:       "INT-04 CURRENCY_MISMATCH (EUR invoice against GBP PO)",
				PONumber:       "PO-2026-1008",
				ExpectedStatus: domain.StatusNeedsReview,
				ExpectedCodes:  []string{matcher.CodeCurrencyMismatch},
			},
			raw: func() ([]byte, error) {
				inv := makeCleanInvoice("INV-2026-0008", poByNum["PO-2026-1008"])
				inv.Currency = "EUR"
				return renderUBLXML(inv, supByCode[inv.SupplierCode].Name, false)
			},
		},
		// 9. 3WAY-01: MISSING_PO (UBL XML)
		{
			meta: InvoiceFixtureMeta{
				Filename:       "09_3way01_missing_po.xml",
				Format:         domain.FormatUBLXML,
				Scenario:       "3WAY-01 MISSING_PO (unknown PO reference)",
				PONumber:       "PO-2026-9999",
				ExpectedStatus: domain.StatusNeedsReview,
				ExpectedCodes:  []string{matcher.CodeMissingPO},
			},
			raw: func() ([]byte, error) {
				inv := makeCleanInvoice("INV-2026-0009", poByNum["PO-2026-1001"])
				inv.PONumber = "PO-2026-9999"
				return renderUBLXML(inv, supByCode[inv.SupplierCode].Name, false)
			},
		},
		// 10. 3WAY-02: PO_SUPPLIER_MISMATCH (PDF)
		{
			meta: InvoiceFixtureMeta{
				Filename:       "10_3way02_supplier_mismatch.pdf",
				Format:         domain.FormatPDF,
				Scenario:       "3WAY-02 PO_SUPPLIER_MISMATCH (wrong supplier billing PO)",
				PONumber:       "PO-2026-1009",
				ExpectedStatus: domain.StatusNeedsReview,
				ExpectedCodes:  []string{matcher.CodePOSupplierMismatch},
			},
			raw: func() ([]byte, error) {
				inv := makeCleanInvoice("INV-2026-0010", poByNum["PO-2026-1009"])
				inv.SupplierCode = "SUP-MIDLAND-04"
				inv.SupplierVAT = supByCode["SUP-MIDLAND-04"].VATNumber
				return renderNativePDF(inv, false, false)
			},
		},
		// 11. 3WAY-03: PO_CLOSED (UBL XML)
		{
			meta: InvoiceFixtureMeta{
				Filename:       "11_3way03_po_closed.xml",
				Format:         domain.FormatUBLXML,
				Scenario:       "3WAY-03 PO_CLOSED (PO status is CLOSED)",
				PONumber:       "PO-2026-1010",
				ExpectedStatus: domain.StatusNeedsReview,
				ExpectedCodes:  []string{matcher.CodePOClosed},
			},
			raw: func() ([]byte, error) {
				inv := makeCleanInvoice("INV-2026-0011", poByNum["PO-2026-1010"])
				return renderUBLXML(inv, supByCode[inv.SupplierCode].Name, false)
			},
		},
		// 12. 3WAY-04: MISSING_GRN (CSV)
		{
			meta: InvoiceFixtureMeta{
				Filename:       "12_3way04_missing_grn.csv",
				Format:         domain.FormatCSV,
				Scenario:       "3WAY-04 MISSING_GRN (zero GRNs exist for PO)",
				PONumber:       "PO-2026-1011",
				ExpectedStatus: domain.StatusNeedsReview,
				ExpectedCodes:  []string{matcher.CodeMissingGRN},
			},
			raw: func() ([]byte, error) {
				inv := makeCleanInvoice("INV-2026-0012", poByNum["PO-2026-1011"])
				return renderCSV(inv)
			},
		},
		// 13. 3WAY-05: UNMATCHED_SKU (PDF)
		{
			meta: InvoiceFixtureMeta{
				Filename:       "13_3way05_unmatched_sku.pdf",
				Format:         domain.FormatPDF,
				Scenario:       "3WAY-05 UNMATCHED_SKU (invoice bills SKU not on PO)",
				PONumber:       "PO-2026-1012",
				ExpectedStatus: domain.StatusNeedsReview,
				ExpectedCodes:  []string{matcher.CodeUnmatchedSKU},
			},
			raw: func() ([]byte, error) {
				inv := makeCleanInvoice("INV-2026-0013", poByNum["PO-2026-1012"])
				inv.Lines[0].SKU = "SKU-UNLISTED-999"
				return renderNativePDF(inv, false, false)
			},
		},
		// 14. 3WAY-06: QTY_EXCEEDS_GRN (UBL XML)
		{
			meta: InvoiceFixtureMeta{
				Filename:       "14_3way06_qty_exceeds_grn.xml",
				Format:         domain.FormatUBLXML,
				Scenario:       "3WAY-06 QTY_EXCEEDS_GRN (invoiced 20 vs 14 accepted on GRN)",
				PONumber:       "PO-2026-1013",
				ExpectedStatus: domain.StatusNeedsReview,
				ExpectedCodes:  []string{matcher.CodeQtyExceedsGRN},
			},
			raw: func() ([]byte, error) {
				inv := makeCleanInvoice("INV-2026-0014", poByNum["PO-2026-1013"])
				return renderUBLXML(inv, supByCode[inv.SupplierCode].Name, false)
			},
		},
		// 15. 3WAY-07: QTY_EXCEEDS_PO (CSV)
		{
			meta: InvoiceFixtureMeta{
				Filename:       "15_3way07_qty_exceeds_po.csv",
				Format:         domain.FormatCSV,
				Scenario:       "3WAY-07 QTY_EXCEEDS_PO (invoiced 15 vs PO ordered 12)",
				PONumber:       "PO-2026-1014",
				ExpectedStatus: domain.StatusNeedsReview,
				ExpectedCodes:  []string{matcher.CodeQtyExceedsPO},
			},
			raw: func() ([]byte, error) {
				inv := makeCleanInvoice("INV-2026-0015", poByNum["PO-2026-1014"])
				inv.Lines[0].Quantity = 15
				inv.Lines[0].LineNetPence = domain.CalculateLineNetPence(15, inv.Lines[0].UnitPricePence)
				inv.Lines[0].LineVATPence = domain.CalculateVATPence(inv.Lines[0].LineNetPence, inv.Lines[0].VATRateBPS)
				return renderCSV(inv)
			},
		},
		// 16. 3WAY-08: PRICE_VARIANCE (PDF)
		{
			meta: InvoiceFixtureMeta{
				Filename:       "16_3way08_price_variance.pdf",
				Format:         domain.FormatPDF,
				Scenario:       "3WAY-08 PRICE_VARIANCE (unit price +£4.00 / +8% above PO)",
				PONumber:       "PO-2026-1015",
				ExpectedStatus: domain.StatusNeedsReview,
				ExpectedCodes:  []string{matcher.CodePriceVariance},
			},
			raw: func() ([]byte, error) {
				inv := makeCleanInvoice("INV-2026-0016", poByNum["PO-2026-1015"])
				inv.Lines[0].UnitPricePence += 400
				inv.Lines[0].LineNetPence = domain.CalculateLineNetPence(inv.Lines[0].Quantity, inv.Lines[0].UnitPricePence)
				inv.Lines[0].LineVATPence = domain.CalculateVATPence(inv.Lines[0].LineNetPence, inv.Lines[0].VATRateBPS)
				inv.SubtotalPence = 0
				inv.VATTotalPence = 0
				for _, l := range inv.Lines {
					inv.SubtotalPence += l.LineNetPence
					inv.VATTotalPence += l.LineVATPence
				}
				inv.GrandTotalPence = inv.SubtotalPence + inv.VATTotalPence
				return renderNativePDF(inv, false, false)
			},
		},
		// 17. 3WAY-09: VAT_RATE_MISMATCH (CSV)
		{
			meta: InvoiceFixtureMeta{
				Filename:       "17_3way09_vat_rate_mismatch.csv",
				Format:         domain.FormatCSV,
				Scenario:       "3WAY-09 VAT_RATE_MISMATCH (5% VAT invoiced vs 20% on PO)",
				PONumber:       "PO-2026-1001",
				ExpectedStatus: domain.StatusNeedsReview,
				ExpectedCodes:  []string{matcher.CodeVATRateMismatch},
			},
			raw: func() ([]byte, error) {
				inv := makeCleanInvoice("INV-2026-0017", poByNum["PO-2026-1001"])
				inv.Lines[0].VATRateBPS = 500
				inv.Lines[0].LineVATPence = domain.CalculateVATPence(inv.Lines[0].LineNetPence, 500)
				return renderCSV(inv)
			},
		},
		// 18. Scanned / outlined-glyph PDF (PARSE_FAILED / ErrScannedPDFUnsupported)
		{
			meta: InvoiceFixtureMeta{
				Filename:        "18_scanned_outlined.pdf",
				Format:          domain.FormatPDF,
				Scenario:        "Scanned/outlined PDF without text layer (ErrScannedPDFUnsupported)",
				ExpectedStatus:  domain.StatusParseFailed,
				ExpectScanned:   true,
				ExpectParseFail: true,
			},
			raw: func() ([]byte, error) {
				return renderOutlinedPDF()
			},
		},
		// 19. Corrupt XML (PARSE_FAILED)
		{
			meta: InvoiceFixtureMeta{
				Filename:        "19_corrupt_ubl.xml",
				Format:          domain.FormatUBLXML,
				Scenario:        "Corrupt/truncated UBL XML",
				ExpectedStatus:  domain.StatusParseFailed,
				ExpectParseFail: true,
			},
			raw: func() ([]byte, error) {
				return []byte(`<?xml version="1.0" encoding="UTF-8"?><Invoice xmlns="urn:oasis:names:specification:ubl:schema:xsd:Invoice-2"><cbc:ID>INV-CORRUPT`), nil
			},
		},
		// 20. Corrupt CSV (PARSE_FAILED)
		{
			meta: InvoiceFixtureMeta{
				Filename:        "20_corrupt_csv.csv",
				Format:          domain.FormatCSV,
				Scenario:        "Corrupt CSV with missing required header columns",
				ExpectedStatus:  domain.StatusParseFailed,
				ExpectParseFail: true,
			},
			raw: func() ([]byte, error) {
				return []byte("inv_no,po,supplier,amount\nINV-BAD-20,PO-2026-1001,SUP-ACME-01,300.00\n"), nil
			},
		},
	}

	manifest := &FixtureManifest{
		GeneratedAt:    issueBase,
		Suppliers:      suppliers,
		PurchaseOrders: pos,
		GoodsReceipts:  grns,
		Invoices:       make([]InvoiceFixtureMeta, 0, len(specs)),
	}

	for _, spec := range specs {
		data, err := spec.raw()
		if err != nil {
			return nil, fmt.Errorf("render fixture %s: %w", spec.meta.Filename, err)
		}
		targetPath := filepath.Join(invoicesDir, spec.meta.Filename)
		if err := os.WriteFile(targetPath, data, 0o644); err != nil {
			return nil, fmt.Errorf("write fixture %s: %w", targetPath, err)
		}
		manifest.Invoices = append(manifest.Invoices, spec.meta)
	}

	if err := writeJSON(filepath.Join(outDir, "suppliers.json"), suppliers); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(outDir, "purchase_orders.json"), pos); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(outDir, "goods_receipts.json"), grns); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(outDir, "manifest.json"), manifest); err != nil {
		return nil, err
	}

	return manifest, nil
}

func buildSuppliers() []domain.Supplier {
	return []domain.Supplier{
		{
			ID:           deterministicUUID("supplier:SUP-ACME-01"),
			SupplierCode: "SUP-ACME-01",
			Name:         "Acme Industrial Supplies Ltd",
			VATNumber:    "GB123456789",
			PeppolID:     "0088:5060012340001",
		},
		{
			ID:           deterministicUUID("supplier:SUP-THAMES-02"),
			SupplierCode: "SUP-THAMES-02",
			Name:         "Thames Precision Fasteners PLC",
			VATNumber:    "GB234567891",
			PeppolID:     "0088:5060012340002",
		},
		{
			ID:           deterministicUUID("supplier:SUP-NORTH-03"),
			SupplierCode: "SUP-NORTH-03",
			Name:         "Northern Hydraulics & Valves Ltd",
			VATNumber:    "GB345678912",
			PeppolID:     "0088:5060012340003",
		},
		{
			ID:           deterministicUUID("supplier:SUP-MIDLAND-04"),
			SupplierCode: "SUP-MIDLAND-04",
			Name:         "Midland Electrical Components Ltd",
			VATNumber:    "GB456789123",
			PeppolID:     "0088:5060012340004",
		},
		{
			ID:           deterministicUUID("supplier:SUP-CLYDE-05"),
			SupplierCode: "SUP-CLYDE-05",
			Name:         "Clyde Marine Bearings Co",
			VATNumber:    "GB567891234",
			PeppolID:     "0088:5060012340005",
		},
	}
}

func buildPOsAndGRNs(suppliers []domain.Supplier) ([]domain.PurchaseOrder, []domain.GoodsReceipt) {
	receivedAt := time.Date(2026, 11, 18, 10, 30, 0, 0, time.UTC)

	type poSeed struct {
		num      string
		supIdx   int
		status   domain.POStatus
		currency string
		lines    []domain.POLine
		grnMode  string // "full", "none", "partial", "over"
	}

	seeds := []poSeed{
		{
			num: "PO-2026-1001", supIdx: 0, status: domain.POStatusReceived, currency: "GBP", grnMode: "full",
			lines: []domain.POLine{
				{LineNumber: 1, SKU: "SKU-BOLT-M12", Description: "M12 Stainless Steel Hex Bolts", OrderedQty: 10, UnitPricePence: 2500, VATRateBPS: 2000},
			},
		},
		{
			num: "PO-2026-1002", supIdx: 1, status: domain.POStatusReceived, currency: "GBP", grnMode: "full",
			lines: []domain.POLine{
				{LineNumber: 1, SKU: "SKU-NUT-M12", Description: "M12 Stainless Hex Nuts", OrderedQty: 20, UnitPricePence: 850, VATRateBPS: 2000},
				{LineNumber: 2, SKU: "SKU-WASH-M12", Description: "M12 Spring Washers", OrderedQty: 15, UnitPricePence: 400, VATRateBPS: 2000},
			},
		},
		{
			num: "PO-2026-1003", supIdx: 2, status: domain.POStatusReceived, currency: "GBP", grnMode: "full",
			lines: []domain.POLine{
				{LineNumber: 1, SKU: "SKU-VALVE-20", Description: "Brass Ball Valve 20mm", OrderedQty: 5, UnitPricePence: 1800, VATRateBPS: 2000},
				{LineNumber: 2, SKU: "SKU-SEAL-KIT", Description: "EPDM Gasket Seal Kit", OrderedQty: 4, UnitPricePence: 550, VATRateBPS: 2000},
			},
		},
		{
			num: "PO-2026-1004", supIdx: 3, status: domain.POStatusReceived, currency: "GBP", grnMode: "full",
			lines: []domain.POLine{
				{LineNumber: 1, SKU: "SKU-CABLE-10M", Description: "Armoured Power Cable 10m", OrderedQty: 4, UnitPricePence: 3200, VATRateBPS: 2000},
				{LineNumber: 2, SKU: "SKU-BREAKER-32A", Description: "32A Type-C MCB Breaker", OrderedQty: 6, UnitPricePence: 1450, VATRateBPS: 2000},
				{LineNumber: 3, SKU: "SKU-GLAND-M20", Description: "IP68 Brass Cable Gland M20", OrderedQty: 12, UnitPricePence: 350, VATRateBPS: 2000},
				{LineNumber: 4, SKU: "SKU-DIN-RAIL", Description: "35mm Slotted Steel DIN Rail", OrderedQty: 8, UnitPricePence: 625, VATRateBPS: 2000},
			},
		},
		{
			num: "PO-2026-1005", supIdx: 4, status: domain.POStatusReceived, currency: "GBP", grnMode: "full",
			lines: []domain.POLine{
				{LineNumber: 1, SKU: "SKU-BEAR-6205", Description: "6205-2RS Deep Groove Bearing", OrderedQty: 10, UnitPricePence: 1200, VATRateBPS: 2000},
			},
		},
		{
			num: "PO-2026-1006", supIdx: 0, status: domain.POStatusReceived, currency: "GBP", grnMode: "full",
			lines: []domain.POLine{
				{LineNumber: 1, SKU: "SKU-FLANGE-50", Description: "DN50 Carbon Steel Weld Flange", OrderedQty: 8, UnitPricePence: 2250, VATRateBPS: 2000},
			},
		},
		{
			num: "PO-2026-1007", supIdx: 1, status: domain.POStatusReceived, currency: "GBP", grnMode: "full",
			lines: []domain.POLine{
				{LineNumber: 1, SKU: "SKU-STUD-M16", Description: "M16 High-Tensile Threaded Stud", OrderedQty: 12, UnitPricePence: 1500, VATRateBPS: 2000},
			},
		},
		{
			num: "PO-2026-1008", supIdx: 2, status: domain.POStatusReceived, currency: "GBP", grnMode: "full",
			lines: []domain.POLine{
				{LineNumber: 1, SKU: "SKU-HOSE-HYD", Description: "High-Pressure Hydraulic Hose 2m", OrderedQty: 6, UnitPricePence: 4500, VATRateBPS: 2000},
			},
		},
		{
			num: "PO-2026-1009", supIdx: 0, status: domain.POStatusReceived, currency: "GBP", grnMode: "full",
			lines: []domain.POLine{
				{LineNumber: 1, SKU: "SKU-CLAMP-40", Description: "Heavy-Duty Pipe Clamp 40mm", OrderedQty: 15, UnitPricePence: 900, VATRateBPS: 2000},
			},
		},
		{
			num: "PO-2026-1010", supIdx: 3, status: domain.POStatusClosed, currency: "GBP", grnMode: "full",
			lines: []domain.POLine{
				{LineNumber: 1, SKU: "SKU-RELAY-24V", Description: "24VDC Industrial Control Relay", OrderedQty: 10, UnitPricePence: 1950, VATRateBPS: 2000},
			},
		},
		{
			num: "PO-2026-1011", supIdx: 4, status: domain.POStatusOpen, currency: "GBP", grnMode: "none",
			lines: []domain.POLine{
				{LineNumber: 1, SKU: "SKU-SHAFT-30", Description: "Hardened Linear Shaft 30mm", OrderedQty: 4, UnitPricePence: 6800, VATRateBPS: 2000},
			},
		},
		{
			num: "PO-2026-1012", supIdx: 1, status: domain.POStatusReceived, currency: "GBP", grnMode: "full",
			lines: []domain.POLine{
				{LineNumber: 1, SKU: "SKU-ANCHOR-M10", Description: "M10 Chemical Anchor Capsule", OrderedQty: 25, UnitPricePence: 600, VATRateBPS: 2000},
			},
		},
		{
			num: "PO-2026-1013", supIdx: 2, status: domain.POStatusPartiallyReceived, currency: "GBP", grnMode: "partial",
			lines: []domain.POLine{
				{LineNumber: 1, SKU: "SKU-GAUGE-10B", Description: "Glycerine Pressure Gauge 10 Bar", OrderedQty: 20, UnitPricePence: 2100, VATRateBPS: 2000},
			},
		},
		{
			num: "PO-2026-1014", supIdx: 3, status: domain.POStatusReceived, currency: "GBP", grnMode: "over",
			lines: []domain.POLine{
				{LineNumber: 1, SKU: "SKU-SWITCH-LIM", Description: "IP67 Roller Lever Limit Switch", OrderedQty: 12, UnitPricePence: 1650, VATRateBPS: 2000},
			},
		},
		{
			num: "PO-2026-1015", supIdx: 4, status: domain.POStatusReceived, currency: "GBP", grnMode: "full",
			lines: []domain.POLine{
				{LineNumber: 1, SKU: "SKU-PUMP-SEAL", Description: "Mechanical Pump Shaft Seal", OrderedQty: 5, UnitPricePence: 5000, VATRateBPS: 2000},
			},
		},
	}

	pos := make([]domain.PurchaseOrder, 0, len(seeds))
	grns := make([]domain.GoodsReceipt, 0, len(seeds))

	for idx, s := range seeds {
		sup := suppliers[s.supIdx]
		poID := deterministicUUID("po:" + s.num)
		var totalNet int64
		lines := make([]domain.POLine, len(s.lines))
		for i, l := range s.lines {
			l.ID = deterministicUUID(fmt.Sprintf("poline:%s:%s", s.num, l.SKU))
			lines[i] = l
			totalNet += domain.CalculateLineNetPence(l.OrderedQty, l.UnitPricePence)
		}

		pos = append(pos, domain.PurchaseOrder{
			ID:            poID,
			PONumber:      s.num,
			SupplierID:    sup.ID,
			SupplierCode:  sup.SupplierCode,
			Currency:      s.currency,
			Status:        s.status,
			TotalNetPence: totalNet,
			Lines:         lines,
		})

		if s.grnMode == "none" {
			continue
		}

		grnNum := fmt.Sprintf("GRN-2026-%04d", 2001+idx)
		grnLines := make([]domain.GoodsReceiptLine, 0, len(lines))
		notes := "Full delivery inspected and accepted"
		for _, l := range lines {
			accepted := l.OrderedQty
			rejected := int64(0)
			switch s.grnMode {
			case "partial":
				accepted = l.OrderedQty - 6
				rejected = 6
				notes = "Short delivery: 6 units damaged in transit and rejected"
			case "over":
				accepted = l.OrderedQty + 3
				notes = "Over-delivery accepted into quarantine bay pending PO amendment"
			}
			grnLines = append(grnLines, domain.GoodsReceiptLine{
				ID:          deterministicUUID(fmt.Sprintf("grnline:%s:%s", grnNum, l.SKU)),
				SKU:         l.SKU,
				AcceptedQty: accepted,
				RejectedQty: rejected,
			})
		}

		grns = append(grns, domain.GoodsReceipt{
			ID:              deterministicUUID("grn:" + grnNum),
			GRNNumber:       grnNum,
			PurchaseOrderID: poID,
			PONumber:        s.num,
			ReceivedAt:      receivedAt,
			WarehouseNotes:  notes,
			Lines:           grnLines,
		})
	}

	return pos, grns
}

func renderUBLXML(inv domain.ParsedInvoice, supplierName string, includeLineTaxTotal bool) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<Invoice xmlns="urn:oasis:names:specification:ubl:schema:xsd:Invoice-2" xmlns:cac="urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2" xmlns:cbc="urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2">` + "\n")
	b.WriteString("  <cbc:CustomizationID>urn:cen.eu:en16931:2017#compliant#urn:fdc:peppol.eu:2017:poacc:billing:3.0</cbc:CustomizationID>\n")
	b.WriteString("  <cbc:ProfileID>urn:fdc:peppol.eu:2017:poacc:billing:01:1.0</cbc:ProfileID>\n")
	fmt.Fprintf(&b, "  <cbc:ID>%s</cbc:ID>\n", escapeXML(inv.InvoiceNumber))
	fmt.Fprintf(&b, "  <cbc:IssueDate>%s</cbc:IssueDate>\n", inv.IssueDate.Format("2006-01-02"))
	if inv.DueDate != nil {
		fmt.Fprintf(&b, "  <cbc:DueDate>%s</cbc:DueDate>\n", inv.DueDate.Format("2006-01-02"))
	}
	b.WriteString("  <cbc:InvoiceTypeCode>380</cbc:InvoiceTypeCode>\n")
	fmt.Fprintf(&b, "  <cbc:DocumentCurrencyCode>%s</cbc:DocumentCurrencyCode>\n", escapeXML(inv.Currency))
	if inv.PONumber != "" {
		b.WriteString("  <cac:OrderReference>\n")
		fmt.Fprintf(&b, "    <cbc:ID>%s</cbc:ID>\n", escapeXML(inv.PONumber))
		b.WriteString("  </cac:OrderReference>\n")
	}
	b.WriteString("  <cac:AccountingSupplierParty>\n")
	b.WriteString("    <cac:Party>\n")
	b.WriteString("      <cac:PartyIdentification>\n")
	fmt.Fprintf(&b, "        <cbc:ID>%s</cbc:ID>\n", escapeXML(inv.SupplierCode))
	b.WriteString("      </cac:PartyIdentification>\n")
	b.WriteString("      <cac:PartyName>\n")
	fmt.Fprintf(&b, "        <cbc:Name>%s</cbc:Name>\n", escapeXML(supplierName))
	b.WriteString("      </cac:PartyName>\n")
	b.WriteString("      <cac:PartyTaxScheme>\n")
	fmt.Fprintf(&b, "        <cbc:CompanyID>%s</cbc:CompanyID>\n", escapeXML(inv.SupplierVAT))
	b.WriteString("        <cac:TaxScheme>\n")
	b.WriteString("          <cbc:ID>VAT</cbc:ID>\n")
	b.WriteString("        </cac:TaxScheme>\n")
	b.WriteString("      </cac:PartyTaxScheme>\n")
	b.WriteString("    </cac:Party>\n")
	b.WriteString("  </cac:AccountingSupplierParty>\n")
	b.WriteString("  <cac:TaxTotal>\n")
	fmt.Fprintf(&b, "    <cbc:TaxAmount currencyID=\"%s\">%s</cbc:TaxAmount>\n",
		escapeXML(inv.Currency), domain.FormatPence(inv.VATTotalPence))
	b.WriteString("  </cac:TaxTotal>\n")
	b.WriteString("  <cac:LegalMonetaryTotal>\n")
	fmt.Fprintf(&b, "    <cbc:LineExtensionAmount currencyID=\"%s\">%s</cbc:LineExtensionAmount>\n",
		escapeXML(inv.Currency), domain.FormatPence(inv.SubtotalPence))
	fmt.Fprintf(&b, "    <cbc:TaxExclusiveAmount currencyID=\"%s\">%s</cbc:TaxExclusiveAmount>\n",
		escapeXML(inv.Currency), domain.FormatPence(inv.SubtotalPence))
	fmt.Fprintf(&b, "    <cbc:TaxInclusiveAmount currencyID=\"%s\">%s</cbc:TaxInclusiveAmount>\n",
		escapeXML(inv.Currency), domain.FormatPence(inv.GrandTotalPence))
	fmt.Fprintf(&b, "    <cbc:PayableAmount currencyID=\"%s\">%s</cbc:PayableAmount>\n",
		escapeXML(inv.Currency), domain.FormatPence(inv.GrandTotalPence))
	b.WriteString("  </cac:LegalMonetaryTotal>\n")

	for _, line := range inv.Lines {
		b.WriteString("  <cac:InvoiceLine>\n")
		fmt.Fprintf(&b, "    <cbc:ID>%d</cbc:ID>\n", line.LineNumber)
		fmt.Fprintf(&b, "    <cbc:InvoicedQuantity unitCode=\"EA\">%d</cbc:InvoicedQuantity>\n", line.Quantity)
		fmt.Fprintf(&b, "    <cbc:LineExtensionAmount currencyID=\"%s\">%s</cbc:LineExtensionAmount>\n",
			escapeXML(inv.Currency), domain.FormatPence(line.LineNetPence))
		if includeLineTaxTotal {
			b.WriteString("    <cac:TaxTotal>\n")
			fmt.Fprintf(&b, "      <cbc:TaxAmount currencyID=\"%s\">%s</cbc:TaxAmount>\n",
				escapeXML(inv.Currency), domain.FormatPence(line.LineVATPence))
			b.WriteString("    </cac:TaxTotal>\n")
		}
		b.WriteString("    <cac:Item>\n")
		fmt.Fprintf(&b, "      <cbc:Name>%s</cbc:Name>\n", escapeXML(line.Description))
		b.WriteString("      <cac:SellersItemIdentification>\n")
		fmt.Fprintf(&b, "        <cbc:ID>%s</cbc:ID>\n", escapeXML(line.SKU))
		b.WriteString("      </cac:SellersItemIdentification>\n")
		b.WriteString("      <cac:ClassifiedTaxCategory>\n")
		b.WriteString("        <cbc:ID>S</cbc:ID>\n")
		fmt.Fprintf(&b, "        <cbc:Percent>%s</cbc:Percent>\n", domain.FormatPence(line.VATRateBPS))
		b.WriteString("        <cac:TaxScheme>\n")
		b.WriteString("          <cbc:ID>VAT</cbc:ID>\n")
		b.WriteString("        </cac:TaxScheme>\n")
		b.WriteString("      </cac:ClassifiedTaxCategory>\n")
		b.WriteString("    </cac:Item>\n")
		b.WriteString("    <cac:Price>\n")
		fmt.Fprintf(&b, "      <cbc:PriceAmount currencyID=\"%s\">%s</cbc:PriceAmount>\n",
			escapeXML(inv.Currency), domain.FormatPence(line.UnitPricePence))
		b.WriteString("    </cac:Price>\n")
		b.WriteString("  </cac:InvoiceLine>\n")
	}

	b.WriteString("</Invoice>\n")
	return b.Bytes(), nil
}

func escapeXML(s string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

func renderCSV(inv domain.ParsedInvoice) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	header := []string{
		"invoice_number",
		"po_number",
		"supplier_code",
		"issue_date",
		"currency",
		"sku",
		"description",
		"qty",
		"unit_price_pence",
		"vat_rate_bps",
		"line_net_pence",
		"line_vat_pence",
	}
	if err := w.Write(header); err != nil {
		return nil, err
	}
	for _, line := range inv.Lines {
		row := []string{
			inv.InvoiceNumber,
			inv.PONumber,
			inv.SupplierCode,
			inv.IssueDate.Format("2006-01-02"),
			inv.Currency,
			line.SKU,
			line.Description,
			strconv.FormatInt(line.Quantity, 10),
			strconv.FormatInt(line.UnitPricePence, 10),
			strconv.FormatInt(line.VATRateBPS, 10),
			strconv.FormatInt(line.LineNetPence, 10),
			strconv.FormatInt(line.LineVATPence, 10),
		}
		if err := w.Write(row); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func currencyPrefix(code string) string {
	switch code {
	case "EUR":
		return "EUR "
	case "USD":
		return "USD "
	default:
		return "GBP "
	}
}

func renderNativePDF(inv domain.ParsedInvoice, splitAcrossTwoPages bool, includeExplicitLineVAT bool) ([]byte, error) {
	c := pdf.NewCreator()
	prefix := currencyPrefix(inv.Currency)

	drawTableHeader := func(pb *pdf.PageBuilder, y float64) {
		pb.SetFont("Helvetica-Bold", 9)
		pb.DrawText(40, y, "SKU")
		pb.DrawText(135, y, "Description")
		pb.DrawText(320, y, "Qty")
		pb.DrawText(365, y, "Unit Price")
		pb.DrawText(435, y, "VAT")
		pb.DrawText(505, y, "Net Total")
	}

	drawLine := func(pb *pdf.PageBuilder, y float64, line domain.ParsedLine) {
		pb.SetFont("Helvetica", 9)
		pb.DrawText(40, y, line.SKU)
		pb.DrawText(135, y, line.Description)
		pb.DrawText(320, y, strconv.FormatInt(line.Quantity, 10))
		pb.DrawText(365, y, domain.FormatPence(line.UnitPricePence))
		vatText := domain.FormatBPS(line.VATRateBPS)
		if includeExplicitLineVAT {
			vatText = fmt.Sprintf("%s (%s)", vatText, domain.FormatPence(line.LineVATPence))
		}
		pb.DrawText(435, y, vatText)
		pb.DrawText(505, y, domain.FormatPence(line.LineNetPence))
	}

	drawTotals := func(pb *pdf.PageBuilder, startY float64) {
		pb.SetFont("Helvetica-Bold", 10)
		pb.DrawText(380, startY, fmt.Sprintf("Subtotal: %s%s", prefix, domain.FormatPence(inv.SubtotalPence)))
		pb.DrawText(380, startY-18, fmt.Sprintf("VAT: %s%s", prefix, domain.FormatPence(inv.VATTotalPence)))
		pb.DrawText(380, startY-36, fmt.Sprintf("Total: %s%s", prefix, domain.FormatPence(inv.GrandTotalPence)))
	}

	p1 := c.NewPage(595, 842)
	p1.SetFont("Helvetica-Bold", 14)
	p1.DrawText(40, 800, "COMMERCIAL TAX INVOICE")
	p1.SetFont("Helvetica", 10)
	p1.DrawText(40, 774, "Invoice No: "+inv.InvoiceNumber)
	p1.DrawText(320, 774, "Issue Date: "+inv.IssueDate.Format("2006-01-02"))
	p1.DrawText(40, 756, "PO Number: "+inv.PONumber)
	if inv.DueDate != nil {
		p1.DrawText(320, 756, "Due Date: "+inv.DueDate.Format("2006-01-02"))
	}
	p1.DrawText(40, 738, "Supplier Code: "+inv.SupplierCode)
	p1.DrawText(320, 738, "Currency: "+inv.Currency)
	if inv.SupplierVAT != "" {
		p1.DrawText(40, 720, "Supplier VAT: "+inv.SupplierVAT)
	}

	drawTableHeader(p1, 680)

	splitIdx := len(inv.Lines)
	if splitAcrossTwoPages && len(inv.Lines) > 2 {
		splitIdx = len(inv.Lines) / 2
	}

	y := 658.0
	for i := 0; i < splitIdx; i++ {
		drawLine(p1, y, inv.Lines[i])
		y -= 20.0
	}

	if splitIdx < len(inv.Lines) {
		p2 := c.NewPage(595, 842)
		p2.SetFont("Helvetica", 9)
		p2.DrawText(40, 800, "Invoice No: "+inv.InvoiceNumber+" (Page 2)")
		drawTableHeader(p2, 770)
		y2 := 748.0
		for i := splitIdx; i < len(inv.Lines); i++ {
			drawLine(p2, y2, inv.Lines[i])
			y2 -= 20.0
		}
		drawTotals(p2, y2-35.0)
	} else {
		drawTotals(p1, y-35.0)
	}

	return c.Build()
}

func renderOutlinedPDF() ([]byte, error) {
	c := pdf.NewCreator()
	p := c.NewPage(595, 842)
	// 12 distinct glyph-sized filled shapes repeated 3 times each (36 candidates, 12 shapes)
	// with no text operators so OutlineHint().Possible() is true and TextSpans() is empty.
	for shapeIdx := 0; shapeIdx < 12; shapeIdx++ {
		w := 5.0 + float64(shapeIdx)*1.5
		h := 8.0 + float64(shapeIdx%4)*2.5
		for rep := 0; rep < 3; rep++ {
			x := 50.0 + float64(shapeIdx)*35.0
			y := 700.0 - float64(rep)*25.0
			p.FillRect(x, y, w, h, 0, 0, 0)
		}
	}
	return c.Build()
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
