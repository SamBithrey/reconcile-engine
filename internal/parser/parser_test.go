package parser_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/razvandimescu/gopdf/pdf"

	"reconcile-engine/internal/domain"
	"reconcile-engine/internal/parser"
)

const sampleValidUBL = `<?xml version="1.0" encoding="UTF-8"?>
<Invoice xmlns="urn:oasis:names:specification:ubl:schema:xsd:Invoice-2" xmlns:cac="urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2" xmlns:cbc="urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2">
  <cbc:CustomizationID>urn:cen.eu:en16931:2017#compliant#urn:fdc:peppol.eu:2017:poacc:billing:3.0</cbc:CustomizationID>
  <cbc:ProfileID>urn:fdc:peppol.eu:2017:poacc:billing:01:1.0</cbc:ProfileID>
  <cbc:ID>INV-2026-0001</cbc:ID>
  <cbc:IssueDate>2026-11-20</cbc:IssueDate>
  <cbc:DueDate>2026-12-20</cbc:DueDate>
  <cbc:InvoiceTypeCode>380</cbc:InvoiceTypeCode>
  <cbc:DocumentCurrencyCode>GBP</cbc:DocumentCurrencyCode>
  <cac:OrderReference>
    <cbc:ID>PO-2026-1001</cbc:ID>
  </cac:OrderReference>
  <cac:AccountingSupplierParty>
    <cac:Party>
      <cac:PartyIdentification>
        <cbc:ID>SUP-ACME-01</cbc:ID>
      </cac:PartyIdentification>
      <cac:PartyName>
        <cbc:Name>Acme Industrial Supplies Ltd</cbc:Name>
      </cac:PartyName>
      <cac:PartyTaxScheme>
        <cbc:CompanyID>GB123456789</cbc:CompanyID>
        <cac:TaxScheme>
          <cbc:ID>VAT</cbc:ID>
        </cac:TaxScheme>
      </cac:PartyTaxScheme>
    </cac:Party>
  </cac:AccountingSupplierParty>
  <cac:TaxTotal>
    <cbc:TaxAmount currencyID="GBP">50.00</cbc:TaxAmount>
  </cac:TaxTotal>
  <cac:LegalMonetaryTotal>
    <cbc:LineExtensionAmount currencyID="GBP">250.00</cbc:LineExtensionAmount>
    <cbc:TaxExclusiveAmount currencyID="GBP">250.00</cbc:TaxExclusiveAmount>
    <cbc:TaxInclusiveAmount currencyID="GBP">300.00</cbc:TaxInclusiveAmount>
    <cbc:PayableAmount currencyID="GBP">300.00</cbc:PayableAmount>
  </cac:LegalMonetaryTotal>
  <cac:InvoiceLine>
    <cbc:ID>1</cbc:ID>
    <cbc:InvoicedQuantity unitCode="EA">10</cbc:InvoicedQuantity>
    <cbc:LineExtensionAmount currencyID="GBP">250.00</cbc:LineExtensionAmount>
    <cac:Item>
      <cbc:Name>M12 Stainless Steel Hex Bolts (Box of 100)</cbc:Name>
      <cac:SellersItemIdentification>
        <cbc:ID>SKU-BOLT-M12</cbc:ID>
      </cac:SellersItemIdentification>
      <cac:ClassifiedTaxCategory>
        <cbc:ID>S</cbc:ID>
        <cbc:Percent>20.00</cbc:Percent>
        <cac:TaxScheme>
          <cbc:ID>VAT</cbc:ID>
        </cac:TaxScheme>
      </cac:ClassifiedTaxCategory>
    </cac:Item>
    <cac:Price>
      <cbc:PriceAmount currencyID="GBP">25.00</cbc:PriceAmount>
    </cac:Price>
  </cac:InvoiceLine>
</Invoice>`

const sampleValidCSV = `invoice_number,po_number,supplier_code,issue_date,currency,sku,description,qty,unit_price_pence,vat_rate_bps,line_net_pence,line_vat_pence
INV-CSV-001,PO-2026-1003,SUP-NORTH-03,2026-11-21,GBP,SKU-VALVE-20,Brass Ball Valve 20mm,5,1800,2000,9000,1800
INV-CSV-001,PO-2026-1003,SUP-NORTH-03,2026-11-21,GBP,SKU-SEAL-KIT,EPDM Seal Kit,4,550,2000,2200,440
`

func buildSyntheticNativePDF(t *testing.T, multiPage bool) []byte {
	t.Helper()
	c := pdf.NewCreator()

	p1 := c.NewPage(595, 842)
	p1.SetFont("Helvetica-Bold", 14)
	p1.DrawText(40, 800, "TAX INVOICE")
	p1.SetFont("Helvetica", 10)
	p1.DrawText(40, 775, "Invoice No: INV-PDF-2026-01")
	p1.DrawText(320, 775, "Issue Date: 2026-11-20")
	p1.DrawText(40, 758, "PO Number: PO-2026-1002")
	p1.DrawText(320, 758, "Due Date: 2026-12-20")
	p1.DrawText(40, 741, "Supplier Code: SUP-ACME-01")
	p1.DrawText(320, 741, "Currency: GBP")

	// Table header row on page 1
	p1.SetFont("Helvetica-Bold", 9)
	p1.DrawText(40, 690, "SKU")
	p1.DrawText(130, 690, "Description")
	p1.DrawText(310, 690, "Qty")
	p1.DrawText(360, 690, "Unit Price")
	p1.DrawText(440, 690, "VAT")
	p1.DrawText(505, 690, "Net Total")

	p1.SetFont("Helvetica", 9)
	p1.DrawText(40, 670, "SKU-BOLT-M12")
	p1.DrawText(130, 670, "M12 Hex Bolts")
	p1.DrawText(310, 670, "10")
	p1.DrawText(360, 670, "25.00")
	p1.DrawText(440, 670, "20.00%")
	p1.DrawText(505, 670, "250.00")

	if !multiPage {
		p1.DrawText(40, 650, "SKU-NUT-M12")
		p1.DrawText(130, 650, "M12 Hex Nuts")
		p1.DrawText(310, 650, "5")
		p1.DrawText(360, 650, "10.00")
		p1.DrawText(440, 650, "20.00%")
		p1.DrawText(505, 650, "50.00")

		p1.SetFont("Helvetica-Bold", 10)
		p1.DrawText(380, 590, "Subtotal: GBP 300.00")
		p1.DrawText(380, 572, "VAT: GBP 60.00")
		p1.DrawText(380, 554, "Total: GBP 360.00")
	} else {
		p2 := c.NewPage(595, 842)
		p2.SetFont("Helvetica-Bold", 9)
		p2.DrawText(40, 770, "SKU")
		p2.DrawText(130, 770, "Description")
		p2.DrawText(310, 770, "Qty")
		p2.DrawText(360, 770, "Unit Price")
		p2.DrawText(440, 770, "VAT")
		p2.DrawText(505, 770, "Net Total")

		p2.SetFont("Helvetica", 9)
		p2.DrawText(40, 750, "SKU-NUT-M12")
		p2.DrawText(130, 750, "M12 Hex Nuts")
		p2.DrawText(310, 750, "5")
		p2.DrawText(360, 750, "10.00")
		p2.DrawText(440, 750, "20.00%")
		p2.DrawText(505, 750, "50.00")

		p2.SetFont("Helvetica-Bold", 10)
		p2.DrawText(380, 680, "Subtotal: GBP 300.00")
		p2.DrawText(380, 662, "VAT: GBP 60.00")
		p2.DrawText(380, 644, "Total: GBP 360.00")
	}

	raw, err := c.Build()
	if err != nil {
		t.Fatalf("build synthetic PDF: %v", err)
	}
	return raw
}

func buildOutlinedScannedPDF(t *testing.T) []byte {
	t.Helper()
	c := pdf.NewCreator()
	p := c.NewPage(595, 842)

	// Draw 12 distinct glyph-sized shapes repeated 3 times each (36 candidates, 12 shapes)
	// with zero text operators so page.OutlineHint().Possible() == true and TextSpans() == 0.
	for shapeIdx := 0; shapeIdx < 12; shapeIdx++ {
		w := 5.0 + float64(shapeIdx)*1.5
		h := 8.0 + float64(shapeIdx%4)*2.5
		for rep := 0; rep < 3; rep++ {
			x := 50.0 + float64(shapeIdx)*35.0
			y := 700.0 - float64(rep)*25.0
			p.FillRect(x, y, w, h, 0, 0, 0)
		}
	}

	raw, err := c.Build()
	if err != nil {
		t.Fatalf("build outlined PDF: %v", err)
	}
	return raw
}

func TestDetectFormat(t *testing.T) {
	pdfBytes := buildSyntheticNativePDF(t, false)
	tests := []struct {
		name        string
		contentType string
		filename    string
		data        []byte
		want        domain.SourceFormat
		wantErr     bool
	}{
		{
			name:        "PDF magic bytes override empty content type",
			contentType: "",
			filename:    "upload.bin",
			data:        pdfBytes,
			want:        domain.FormatPDF,
		},
		{
			name:        "XML by content type",
			contentType: "application/xml; charset=utf-8",
			filename:    "invoice.xml",
			data:        []byte(sampleValidUBL),
			want:        domain.FormatUBLXML,
		},
		{
			name:        "XML by leading angle bracket",
			contentType: "application/octet-stream",
			filename:    "invoice.dat",
			data:        []byte("  \n" + sampleValidUBL),
			want:        domain.FormatUBLXML,
		},
		{
			name:        "CSV by content type",
			contentType: "text/csv",
			filename:    "manifest.csv",
			data:        []byte(sampleValidCSV),
			want:        domain.FormatCSV,
		},
		{
			name:        "CSV by extension",
			contentType: "application/octet-stream",
			filename:    "batch.CSV",
			data:        []byte(sampleValidCSV),
			want:        domain.FormatCSV,
		},
		{
			name:        "empty payload fails",
			contentType: "text/csv",
			filename:    "empty.csv",
			data:        nil,
			wantErr:     true,
		},
		{
			name:        "unknown binary payload fails",
			contentType: "application/octet-stream",
			filename:    "photo.bin",
			data:        []byte{0x01, 0x02, 0x03, 0x04},
			wantErr:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parser.DetectFormat(tc.contentType, tc.filename, tc.data)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("DetectFormat() = %s, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("DetectFormat() unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("DetectFormat() = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestUBLParser(t *testing.T) {
	router := parser.NewDefaultRouter()

	tests := []struct {
		name    string
		xml     string
		wantErr bool
		check   func(t *testing.T, inv *domain.ParsedInvoice)
	}{
		{
			name: "valid Peppol BIS Billing 3.0 invoice",
			xml:  sampleValidUBL,
			check: func(t *testing.T, inv *domain.ParsedInvoice) {
				if inv.InvoiceNumber != "INV-2026-0001" {
					t.Errorf("InvoiceNumber = %q, want INV-2026-0001", inv.InvoiceNumber)
				}
				if inv.PONumber != "PO-2026-1001" {
					t.Errorf("PONumber = %q, want PO-2026-1001", inv.PONumber)
				}
				if inv.SupplierCode != "SUP-ACME-01" {
					t.Errorf("SupplierCode = %q, want SUP-ACME-01", inv.SupplierCode)
				}
				if inv.SupplierVAT != "GB123456789" {
					t.Errorf("SupplierVAT = %q, want GB123456789", inv.SupplierVAT)
				}
				if inv.Currency != "GBP" {
					t.Errorf("Currency = %q, want GBP", inv.Currency)
				}
				if inv.DueDate == nil || inv.DueDate.Format("2006-01-02") != "2026-12-20" {
					t.Errorf("DueDate = %v, want 2026-12-20", inv.DueDate)
				}
				if inv.SubtotalPence != 25000 || inv.VATTotalPence != 5000 || inv.GrandTotalPence != 30000 {
					t.Errorf("totals = (%d, %d, %d), want (25000, 5000, 30000)",
						inv.SubtotalPence, inv.VATTotalPence, inv.GrandTotalPence)
				}
				if len(inv.Lines) != 1 {
					t.Fatalf("len(Lines) = %d, want 1", len(inv.Lines))
				}
				line := inv.Lines[0]
				if line.SKU != "SKU-BOLT-M12" || line.Quantity != 10 || line.UnitPricePence != 2500 ||
					line.VATRateBPS != 2000 || line.LineNetPence != 25000 || line.LineVATPence != 5000 {
					t.Errorf("unexpected line: %+v", line)
				}
			},
		},
		{
			name:    "malformed XML fails",
			xml:     "<Invoice><cbc:ID>BROKEN",
			wantErr: true,
		},
		{
			name:    "wrong XML namespace fails",
			xml:     `<?xml version="1.0"?><Invoice xmlns="urn:example:wrong"><ID>1</ID></Invoice>`,
			wantErr: true,
		},
		{
			name:    "missing invoice lines fails",
			xml:     strings.Replace(sampleValidUBL, "<cac:InvoiceLine>", "<!--", 1),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fmtDetected, inv, err := router.Parse("application/xml", "inv.xml", []byte(tc.xml))
			if fmtDetected != domain.FormatUBLXML {
				t.Fatalf("format = %s, want UBL_XML", fmtDetected)
			}
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got invoice %+v", inv)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tc.check(t, inv)
		})
	}
}

func TestPDFParser(t *testing.T) {
	router := parser.NewDefaultRouter()

	tests := []struct {
		name      string
		data      []byte
		wantErr   bool
		wantIsErr error
		check     func(t *testing.T, inv *domain.ParsedInvoice)
	}{
		{
			name: "valid single-page native PDF",
			data: buildSyntheticNativePDF(t, false),
			check: func(t *testing.T, inv *domain.ParsedInvoice) {
				if inv.InvoiceNumber != "INV-PDF-2026-01" {
					t.Errorf("InvoiceNumber = %q, want INV-PDF-2026-01", inv.InvoiceNumber)
				}
				if inv.PONumber != "PO-2026-1002" {
					t.Errorf("PONumber = %q, want PO-2026-1002", inv.PONumber)
				}
				if inv.SupplierCode != "SUP-ACME-01" {
					t.Errorf("SupplierCode = %q, want SUP-ACME-01", inv.SupplierCode)
				}
				if inv.SubtotalPence != 30000 || inv.VATTotalPence != 6000 || inv.GrandTotalPence != 36000 {
					t.Errorf("totals = (%d, %d, %d), want (30000, 6000, 36000)",
						inv.SubtotalPence, inv.VATTotalPence, inv.GrandTotalPence)
				}
				if len(inv.Lines) != 2 {
					t.Fatalf("len(Lines) = %d, want 2 (%+v)", len(inv.Lines), inv.Lines)
				}
				if inv.Lines[0].SKU != "SKU-BOLT-M12" || inv.Lines[0].LineNetPence != 25000 || inv.Lines[0].LineVATPence != 5000 {
					t.Errorf("unexpected line 0: %+v", inv.Lines[0])
				}
				if inv.Lines[1].SKU != "SKU-NUT-M12" || inv.Lines[1].LineNetPence != 5000 || inv.Lines[1].LineVATPence != 1000 {
					t.Errorf("unexpected line 1: %+v", inv.Lines[1])
				}
			},
		},
		{
			name: "valid multi-page native PDF via FindTableAcrossPages",
			data: buildSyntheticNativePDF(t, true),
			check: func(t *testing.T, inv *domain.ParsedInvoice) {
				if len(inv.Lines) != 2 {
					t.Fatalf("len(Lines) = %d, want 2 (%+v)", len(inv.Lines), inv.Lines)
				}
				if inv.Lines[0].SKU != "SKU-BOLT-M12" || inv.Lines[1].SKU != "SKU-NUT-M12" {
					t.Errorf("unexpected multi-page SKUs: %+v", inv.Lines)
				}
				if inv.GrandTotalPence != 36000 {
					t.Errorf("GrandTotalPence = %d, want 36000", inv.GrandTotalPence)
				}
			},
		},
		{
			name:      "outlined-glyph scanned PDF triggers OutlineHint and returns ErrScannedPDFUnsupported",
			data:      buildOutlinedScannedPDF(t),
			wantErr:   true,
			wantIsErr: domain.ErrScannedPDFUnsupported,
		},
		{
			name:    "corrupt PDF bytes fail cleanly",
			data:    []byte("%PDF-1.7\nnot-valid-xref-content"),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fmtDetected, inv, err := router.Parse("application/pdf", "invoice.pdf", tc.data)
			if fmtDetected != domain.FormatPDF {
				t.Fatalf("format = %s, want PDF", fmtDetected)
			}
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got invoice %+v", inv)
				}
				if tc.wantIsErr != nil && !errors.Is(err, tc.wantIsErr) {
					t.Fatalf("expected error wrapping %v, got %v", tc.wantIsErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tc.check(t, inv)
		})
	}
}

func TestCSVParser(t *testing.T) {
	router := parser.NewDefaultRouter()

	tests := []struct {
		name    string
		csv     string
		wantErr bool
		check   func(t *testing.T, inv *domain.ParsedInvoice)
	}{
		{
			name: "valid multi-line CSV grouped into one ParsedInvoice with computed totals",
			csv:  sampleValidCSV,
			check: func(t *testing.T, inv *domain.ParsedInvoice) {
				if inv.InvoiceNumber != "INV-CSV-001" || inv.PONumber != "PO-2026-1003" || inv.SupplierCode != "SUP-NORTH-03" {
					t.Errorf("unexpected header: %+v", inv)
				}
				if len(inv.Lines) != 2 {
					t.Fatalf("len(Lines) = %d, want 2", len(inv.Lines))
				}
				if inv.SubtotalPence != 11200 || inv.VATTotalPence != 2240 || inv.GrandTotalPence != 13440 {
					t.Errorf("totals = (%d, %d, %d), want (11200, 2240, 13440)",
						inv.SubtotalPence, inv.VATTotalPence, inv.GrandTotalPence)
				}
			},
		},
		{
			name:    "wrong header order rejected",
			csv:     "po_number,invoice_number,supplier_code,issue_date,currency,sku,description,qty,unit_price_pence,vat_rate_bps,line_net_pence,line_vat_pence\nPO-1,INV-1,SUP-1,2026-11-20,GBP,SKU-1,Desc,1,100,2000,100,20\n",
			wantErr: true,
		},
		{
			name:    "header only with no rows rejected",
			csv:     "invoice_number,po_number,supplier_code,issue_date,currency,sku,description,qty,unit_price_pence,vat_rate_bps,line_net_pence,line_vat_pence\n",
			wantErr: true,
		},
		{
			name:    "non-integer money value rejected",
			csv:     "invoice_number,po_number,supplier_code,issue_date,currency,sku,description,qty,unit_price_pence,vat_rate_bps,line_net_pence,line_vat_pence\nINV-1,PO-1,SUP-1,2026-11-20,GBP,SKU-1,Desc,1,10.50,2000,1050,210\n",
			wantErr: true,
		},
		{
			name: "multiple distinct invoices in single Parse rejected",
			csv: "invoice_number,po_number,supplier_code,issue_date,currency,sku,description,qty,unit_price_pence,vat_rate_bps,line_net_pence,line_vat_pence\n" +
				"INV-1,PO-1,SUP-1,2026-11-20,GBP,SKU-1,Desc,1,100,2000,100,20\n" +
				"INV-2,PO-2,SUP-1,2026-11-20,GBP,SKU-2,Desc,1,100,2000,100,20\n",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fmtDetected, inv, err := router.Parse("text/csv", "manifest.csv", []byte(tc.csv))
			if fmtDetected != domain.FormatCSV {
				t.Fatalf("format = %s, want CSV", fmtDetected)
			}
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got invoice %+v", inv)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tc.check(t, inv)
		})
	}
}
