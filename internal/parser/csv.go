package parser

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"reconcile-engine/internal/domain"
)

var csvRequiredHeader = []string{
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

const csvRequiredHeaderPrefix = "invoice_number,po_number,supplier_code,issue_date,currency,sku,description,qty,unit_price_pence,vat_rate_bps,line_net_pence,line_vat_pence"

// CSVParser parses structured supplier CSV invoice manifests.
type CSVParser struct{}

// NewCSVParser returns a new deterministic CSV invoice parser.
func NewCSVParser() *CSVParser {
	return &CSVParser{}
}

func (p *CSVParser) Format() domain.SourceFormat {
	return domain.FormatCSV
}

// Parse parses a CSV manifest representing a single invoice grouped by (supplier_code, invoice_number).
func (p *CSVParser) Parse(data []byte) (*domain.ParsedInvoice, error) {
	invoices, err := p.ParseAll(data)
	if err != nil {
		return nil, err
	}
	if len(invoices) == 0 {
		return nil, fmt.Errorf("CSV contains no invoice line items")
	}
	if len(invoices) > 1 {
		return nil, fmt.Errorf("CSV contains %d distinct (supplier_code, invoice_number) invoices; expected 1", len(invoices))
	}
	return invoices[0], nil
}

// ParseAll parses a CSV manifest and groups rows by (supplier_code, invoice_number) in order of appearance.
func (p *CSVParser) ParseAll(data []byte) ([]*domain.ParsedInvoice, error) {
	cleaned := bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if len(bytes.TrimSpace(cleaned)) == 0 {
		return nil, fmt.Errorf("empty CSV input")
	}

	reader := csv.NewReader(bytes.NewReader(cleaned))
	reader.FieldsPerRecord = len(csvRequiredHeader)

	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read CSV header: %w", err)
	}
	if err := validateCSVHeader(header); err != nil {
		return nil, err
	}

	type groupKey struct {
		supplierCode  string
		invoiceNumber string
	}
	groups := make(map[groupKey]*domain.ParsedInvoice)
	var order []groupKey

	rowNum := 1
	for {
		rec, err := reader.Read()
		if err == io.EOF {
			break
		}
		rowNum++
		if err != nil {
			return nil, fmt.Errorf("CSV row %d: %w", rowNum, err)
		}

		invNum := strings.TrimSpace(rec[0])
		poNum := strings.TrimSpace(rec[1])
		supCode := strings.TrimSpace(rec[2])
		issueDateStr := strings.TrimSpace(rec[3])
		currency := strings.ToUpper(strings.TrimSpace(rec[4]))
		sku := strings.TrimSpace(rec[5])
		description := strings.TrimSpace(rec[6])

		if invNum == "" {
			return nil, fmt.Errorf("CSV row %d: missing invoice_number", rowNum)
		}
		if supCode == "" {
			return nil, fmt.Errorf("CSV row %d: missing supplier_code", rowNum)
		}
		if currency == "" {
			return nil, fmt.Errorf("CSV row %d: missing currency", rowNum)
		}
		if sku == "" {
			return nil, fmt.Errorf("CSV row %d: missing sku", rowNum)
		}

		issueDate, err := time.Parse("2006-01-02", issueDateStr)
		if err != nil {
			return nil, fmt.Errorf("CSV row %d: parse issue_date %q: %w", rowNum, issueDateStr, err)
		}

		qty, err := strconv.ParseInt(strings.TrimSpace(rec[7]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("CSV row %d: parse qty %q: %w", rowNum, rec[7], err)
		}
		unitPricePence, err := strconv.ParseInt(strings.TrimSpace(rec[8]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("CSV row %d: parse unit_price_pence %q: %w", rowNum, rec[8], err)
		}
		vatRateBPS, err := strconv.ParseInt(strings.TrimSpace(rec[9]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("CSV row %d: parse vat_rate_bps %q: %w", rowNum, rec[9], err)
		}
		lineNetPence, err := strconv.ParseInt(strings.TrimSpace(rec[10]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("CSV row %d: parse line_net_pence %q: %w", rowNum, rec[10], err)
		}
		lineVATPence, err := strconv.ParseInt(strings.TrimSpace(rec[11]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("CSV row %d: parse line_vat_pence %q: %w", rowNum, rec[11], err)
		}

		key := groupKey{supplierCode: supCode, invoiceNumber: invNum}
		inv, exists := groups[key]
		if !exists {
			inv = &domain.ParsedInvoice{
				SupplierCode:  supCode,
				InvoiceNumber: invNum,
				PONumber:      poNum,
				IssueDate:     issueDate,
				Currency:      currency,
			}
			groups[key] = inv
			order = append(order, key)
		} else {
			if inv.PONumber != poNum {
				return nil, fmt.Errorf("CSV row %d: conflicting po_number %q vs %q for (%s, %s)",
					rowNum, poNum, inv.PONumber, supCode, invNum)
			}
			if !inv.IssueDate.Equal(issueDate) {
				return nil, fmt.Errorf("CSV row %d: conflicting issue_date %q vs %q for (%s, %s)",
					rowNum, issueDateStr, inv.IssueDate.Format("2006-01-02"), supCode, invNum)
			}
			if inv.Currency != currency {
				return nil, fmt.Errorf("CSV row %d: conflicting currency %q vs %q for (%s, %s)",
					rowNum, currency, inv.Currency, supCode, invNum)
			}
		}

		inv.Lines = append(inv.Lines, domain.ParsedLine{
			LineNumber:     len(inv.Lines) + 1,
			SKU:            sku,
			Description:    description,
			Quantity:       qty,
			UnitPricePence: unitPricePence,
			VATRateBPS:     vatRateBPS,
			LineNetPence:   lineNetPence,
			LineVATPence:   lineVATPence,
		})
		inv.SubtotalPence += lineNetPence
		inv.VATTotalPence += lineVATPence
		inv.GrandTotalPence = inv.SubtotalPence + inv.VATTotalPence
	}

	result := make([]*domain.ParsedInvoice, 0, len(order))
	for _, key := range order {
		result = append(result, groups[key])
	}
	return result, nil
}

func validateCSVHeader(header []string) error {
	if len(header) != len(csvRequiredHeader) {
		return fmt.Errorf("invalid CSV header: expected %d columns, got %d", len(csvRequiredHeader), len(header))
	}
	for i, want := range csvRequiredHeader {
		got := strings.TrimSpace(header[i])
		if got != want {
			return fmt.Errorf("invalid CSV header at column %d: expected %q, got %q", i+1, want, got)
		}
	}
	return nil
}
