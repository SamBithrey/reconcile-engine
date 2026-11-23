package parser

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/razvandimescu/gopdf/pdf"

	"reconcile-engine/internal/domain"
)

var pdfTableHeaders = []string{"SKU", "Description", "Qty", "Unit Price", "VAT", "Net Total"}

var pdfHeaderLabels = []string{
	"Supplier Code:",
	"Supplier VAT:",
	"VAT Number:",
	"Invoice No:",
	"PO Number:",
	"Issue Date:",
	"Due Date:",
	"Currency:",
	"Subtotal:",
	"VAT:",
	"Total:",
}

// PDFParser extracts canonical invoices from structured native text-layered PDFs using gopdf.
type PDFParser struct{}

// NewPDFParser returns a new deterministic native PDF invoice parser.
func NewPDFParser() *PDFParser {
	return &PDFParser{}
}

func (p *PDFParser) Format() domain.SourceFormat {
	return domain.FormatPDF
}

// Parse opens a PDF from raw bytes, rejects scanned/outlined PDFs without an embedded text layer,
// extracts header metadata from text spans, and extracts line items via FindTableAcrossPages.
func (p *PDFParser) Parse(data []byte) (*domain.ParsedInvoice, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty PDF input")
	}

	doc, err := pdf.OpenBytes(data)
	if err != nil {
		return nil, fmt.Errorf("open PDF: %w", err)
	}

	numPages := doc.NumPages()
	if numPages == 0 {
		return nil, fmt.Errorf("PDF contains zero pages")
	}

	// Scanned / outlined-glyph PDF guardrail on page 0.
	page0 := doc.Page(0)
	hint, err := page0.OutlineHint()
	if err != nil {
		return nil, fmt.Errorf("inspect page 0 outline hint: %w", err)
	}
	spans0, err := page0.TextSpans()
	if err != nil {
		return nil, fmt.Errorf("extract page 0 text spans: %w", err)
	}
	if hint.Possible() && len(spans0) == 0 {
		return nil, fmt.Errorf("page 0 has outlined glyphs (%d candidates, %d shapes) and no text layer: %w",
			hint.Candidates, hint.Shapes, domain.ErrScannedPDFUnsupported)
	}

	pagesSpans := make([][]pdf.TextSpan, numPages)
	pagesSpans[0] = spans0
	totalSpans := len(spans0)

	for i := 1; i < numPages; i++ {
		spans, err := doc.Page(i).TextSpans()
		if err != nil {
			return nil, fmt.Errorf("extract page %d text spans: %w", i, err)
		}
		pagesSpans[i] = spans
		totalSpans += len(spans)
	}

	if totalSpans == 0 {
		return nil, fmt.Errorf("PDF has no extractable text spans: %w", domain.ErrScannedPDFUnsupported)
	}

	// Extract header key-value metadata across all pages.
	fields := make(map[string]string)
	for _, spans := range pagesSpans {
		lines := pdf.BuildLines(spans)
		for _, line := range lines {
			extracted := extractLabeledValues(line.Text, pdfHeaderLabels)
			for k, v := range extracted {
				if _, exists := fields[k]; !exists || v != "" {
					fields[k] = v
				}
			}
		}
	}

	invNum := strings.TrimSpace(fields["Invoice No:"])
	if invNum == "" {
		return nil, fmt.Errorf("missing required PDF header field %q", "Invoice No:")
	}

	supplierCode := strings.TrimSpace(fields["Supplier Code:"])
	if supplierCode == "" {
		return nil, fmt.Errorf("missing required PDF header field %q", "Supplier Code:")
	}

	issueDateStr := strings.TrimSpace(fields["Issue Date:"])
	if issueDateStr == "" {
		return nil, fmt.Errorf("missing required PDF header field %q", "Issue Date:")
	}
	issueDate, err := time.Parse("2006-01-02", issueDateStr)
	if err != nil {
		return nil, fmt.Errorf("parse Issue Date %q: %w", issueDateStr, err)
	}

	var dueDate *time.Time
	if dueStr := strings.TrimSpace(fields["Due Date:"]); dueStr != "" {
		parsedDue, err := time.Parse("2006-01-02", dueStr)
		if err != nil {
			return nil, fmt.Errorf("parse Due Date %q: %w", dueStr, err)
		}
		dueDate = &parsedDue
	}

	subtotalStr := strings.TrimSpace(fields["Subtotal:"])
	if subtotalStr == "" {
		return nil, fmt.Errorf("missing required PDF header field %q", "Subtotal:")
	}
	subtotalPence, err := domain.ParseDecimalToPence(subtotalStr)
	if err != nil {
		return nil, fmt.Errorf("parse Subtotal %q: %w", subtotalStr, err)
	}

	vatStr := strings.TrimSpace(fields["VAT:"])
	if vatStr == "" {
		return nil, fmt.Errorf("missing required PDF header field %q", "VAT:")
	}
	vatTotalPence, err := domain.ParseDecimalToPence(vatStr)
	if err != nil {
		return nil, fmt.Errorf("parse VAT %q: %w", vatStr, err)
	}

	totalStr := strings.TrimSpace(fields["Total:"])
	if totalStr == "" {
		return nil, fmt.Errorf("missing required PDF header field %q", "Total:")
	}
	grandTotalPence, err := domain.ParseDecimalToPence(totalStr)
	if err != nil {
		return nil, fmt.Errorf("parse Total %q: %w", totalStr, err)
	}

	currency := strings.ToUpper(strings.TrimSpace(fields["Currency:"]))
	if currency == "" {
		currency = inferCurrencyFromAmounts(subtotalStr, vatStr, totalStr)
	}

	supplierVAT := strings.TrimSpace(fields["Supplier VAT:"])
	if supplierVAT == "" {
		supplierVAT = strings.TrimSpace(fields["VAT Number:"])
	}

	// Extract line items across pages using FindTableAcrossPages.
	table := pdf.FindTableAcrossPages(pagesSpans, &pdf.TableOpts{
		Headers: pdfTableHeaders,
		RowFilter: func(cells []string) bool {
			if len(cells) < 6 {
				return false
			}
			sku := strings.TrimSpace(cells[0])
			qty := strings.TrimSpace(cells[2])
			if sku == "" || qty == "" {
				return false
			}
			lowerSKU := strings.ToLower(sku)
			if strings.HasPrefix(lowerSKU, "subtotal") ||
				strings.HasPrefix(lowerSKU, "vat:") ||
				strings.HasPrefix(lowerSKU, "total") {
				return false
			}
			return true
		},
	})
	if table == nil || len(table.Rows) == 0 {
		return nil, fmt.Errorf("PDF line item table with headers %v not found or empty", pdfTableHeaders)
	}

	lines := make([]domain.ParsedLine, 0, len(table.Rows))
	for rowIdx := range table.Rows {
		sku := strings.TrimSpace(table.CellByName(rowIdx, "SKU"))
		desc := strings.TrimSpace(table.CellByName(rowIdx, "Description"))
		qtyStr := strings.TrimSpace(table.CellByName(rowIdx, "Qty"))
		unitPriceStr := strings.TrimSpace(table.CellByName(rowIdx, "Unit Price"))
		vatCell := strings.TrimSpace(table.CellByName(rowIdx, "VAT"))
		netTotalStr := strings.TrimSpace(table.CellByName(rowIdx, "Net Total"))

		lineNum := rowIdx + 1
		if sku == "" {
			return nil, fmt.Errorf("table row %d: empty SKU", lineNum)
		}

		qty, err := parseWholeQuantity(qtyStr)
		if err != nil {
			return nil, fmt.Errorf("table row %d (%s): parse Qty %q: %w", lineNum, sku, qtyStr, err)
		}

		unitPricePence, err := domain.ParseDecimalToPence(unitPriceStr)
		if err != nil {
			return nil, fmt.Errorf("table row %d (%s): parse Unit Price %q: %w", lineNum, sku, unitPriceStr, err)
		}

		lineNetPence, err := domain.ParseDecimalToPence(netTotalStr)
		if err != nil {
			return nil, fmt.Errorf("table row %d (%s): parse Net Total %q: %w", lineNum, sku, netTotalStr, err)
		}

		vatRateBPS, lineVATPence, err := parsePDFVATCell(vatCell, lineNetPence)
		if err != nil {
			return nil, fmt.Errorf("table row %d (%s): parse VAT %q: %w", lineNum, sku, vatCell, err)
		}

		lines = append(lines, domain.ParsedLine{
			LineNumber:     lineNum,
			SKU:            sku,
			Description:    desc,
			Quantity:       qty,
			UnitPricePence: unitPricePence,
			VATRateBPS:     vatRateBPS,
			LineNetPence:   lineNetPence,
			LineVATPence:   lineVATPence,
		})
	}

	return &domain.ParsedInvoice{
		SupplierCode:    supplierCode,
		SupplierVAT:     supplierVAT,
		InvoiceNumber:   invNum,
		PONumber:        strings.TrimSpace(fields["PO Number:"]),
		IssueDate:       issueDate,
		DueDate:         dueDate,
		Currency:        currency,
		SubtotalPence:   subtotalPence,
		VATTotalPence:   vatTotalPence,
		GrandTotalPence: grandTotalPence,
		Lines:           lines,
	}, nil
}

// parsePDFVATCell handles VAT cells formatted as:
//   - "20%" or "20.00%" (rate in percent; line VAT computed via half-up formula)
//   - "20.00% (£50.00)" or "20% (50.00)" (explicit rate AND explicit VAT amount)
func parsePDFVATCell(raw string, lineNetPence int64) (vatRateBPS int64, lineVATPence int64, err error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, 0, fmt.Errorf("empty VAT cell")
	}

	if openIdx := strings.IndexByte(s, '('); openIdx >= 0 {
		closeIdx := strings.LastIndexByte(s, ')')
		if closeIdx <= openIdx {
			return 0, 0, fmt.Errorf("malformed VAT cell %q", raw)
		}
		ratePart := strings.TrimSpace(s[:openIdx])
		amtPart := strings.TrimSpace(s[openIdx+1 : closeIdx])

		bps, err := domain.ParsePercentToBPS(ratePart)
		if err != nil {
			return 0, 0, err
		}
		vatAmt, err := domain.ParseDecimalToPence(amtPart)
		if err != nil {
			return 0, 0, err
		}
		return bps, vatAmt, nil
	}

	bps, err := domain.ParsePercentToBPS(s)
	if err != nil {
		return 0, 0, err
	}
	return bps, domain.CalculateVATPence(lineNetPence, bps), nil
}

func inferCurrencyFromAmounts(values ...string) string {
	for _, v := range values {
		upper := strings.ToUpper(v)
		switch {
		case strings.Contains(v, "£") || strings.Contains(upper, "GBP"):
			return "GBP"
		case strings.Contains(v, "€") || strings.Contains(upper, "EUR"):
			return "EUR"
		case strings.Contains(v, "$") || strings.Contains(upper, "USD"):
			return "USD"
		}
	}
	return "GBP"
}

type labelMatch struct {
	label string
	start int
	end   int
}

// extractLabeledValues finds all known anchor labels on a reconstructed text line
// and slices out the value following each label up to the next label (or end of line).
func extractLabeledValues(lineText string, labels []string) map[string]string {
	lowerLine := strings.ToLower(lineText)
	var matches []labelMatch

	for _, label := range labels {
		lowerLabel := strings.ToLower(label)
		searchFrom := 0
		for {
			idx := strings.Index(lowerLine[searchFrom:], lowerLabel)
			if idx < 0 {
				break
			}
			absStart := searchFrom + idx
			absEnd := absStart + len(label)
			if isLabelBoundary(lowerLine, absStart) {
				matches = append(matches, labelMatch{
					label: label,
					start: absStart,
					end:   absEnd,
				})
			}
			searchFrom = absEnd
		}
	}

	if len(matches) == 0 {
		return nil
	}

	// Sort by start position ascending; prefer longer label on identical start.
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].start != matches[j].start {
			return matches[i].start < matches[j].start
		}
		return len(matches[i].label) > len(matches[j].label)
	})

	// Drop overlapping shorter matches (e.g. "VAT:" inside "Supplier VAT:").
	var filtered []labelMatch
	lastEnd := -1
	for _, m := range matches {
		if m.start < lastEnd {
			continue
		}
		filtered = append(filtered, m)
		lastEnd = m.end
	}

	out := make(map[string]string, len(filtered))
	for i, m := range filtered {
		valEnd := len(lineText)
		if i+1 < len(filtered) {
			valEnd = filtered[i+1].start
		}
		out[m.label] = strings.TrimSpace(lineText[m.end:valEnd])
	}
	return out
}

func isLabelBoundary(s string, idx int) bool {
	if idx == 0 {
		return true
	}
	prev := s[idx-1]
	return prev == ' ' || prev == '\t' || prev == '|' || prev == ',' || prev == ';'
}
