package parser

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"

	"reconcile-engine/internal/domain"
)

const (
	ublInvoiceNS = "urn:oasis:names:specification:ubl:schema:xsd:Invoice-2"
	ublCACNS     = "urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2"
	ublCBCNS     = "urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2"
)

// UBLParser parses Peppol BIS Billing 3.0 (UBL 2.1) XML invoices.
type UBLParser struct{}

// NewUBLParser returns a new deterministic UBL 2.1 XML invoice parser.
func NewUBLParser() *UBLParser {
	return &UBLParser{}
}

func (p *UBLParser) Format() domain.SourceFormat {
	return domain.FormatUBLXML
}

type ublInvoice struct {
	XMLName         xml.Name           `xml:"Invoice"`
	ID              string             `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2 ID"`
	IssueDate       string             `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2 IssueDate"`
	DueDate         string             `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2 DueDate"`
	CurrencyCode    string             `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2 DocumentCurrencyCode"`
	OrderReference  *ublOrderReference `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2 OrderReference"`
	SupplierParty   ublSupplierParty   `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2 AccountingSupplierParty"`
	TaxTotals       []ublTaxTotal      `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2 TaxTotal"`
	MonetaryTotal   ublMonetaryTotal   `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2 LegalMonetaryTotal"`
	InvoiceLines    []ublInvoiceLine   `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2 InvoiceLine"`
}

type ublOrderReference struct {
	ID string `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2 ID"`
}

type ublSupplierParty struct {
	Party ublParty `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2 Party"`
}

type ublParty struct {
	Identifications []ublPartyIdentification `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2 PartyIdentification"`
	TaxSchemes      []ublPartyTaxScheme      `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2 PartyTaxScheme"`
}

type ublPartyIdentification struct {
	ID string `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2 ID"`
}

type ublPartyTaxScheme struct {
	CompanyID string `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2 CompanyID"`
}

type ublTaxTotal struct {
	TaxAmount string `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2 TaxAmount"`
}

type ublMonetaryTotal struct {
	LineExtensionAmount string `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2 LineExtensionAmount"`
	TaxExclusiveAmount  string `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2 TaxExclusiveAmount"`
	TaxInclusiveAmount  string `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2 TaxInclusiveAmount"`
	PayableAmount       string `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2 PayableAmount"`
}

type ublInvoiceLine struct {
	ID                  string        `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2 ID"`
	InvoicedQuantity    string        `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2 InvoicedQuantity"`
	LineExtensionAmount string        `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2 LineExtensionAmount"`
	TaxTotals           []ublTaxTotal `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2 TaxTotal"`
	Item                ublItem       `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2 Item"`
	Price               ublPrice      `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2 Price"`
}

type ublItem struct {
	Name                    string                    `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2 Name"`
	SellersItemID           *ublItemIdentification    `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2 SellersItemIdentification"`
	StandardItemID          *ublItemIdentification    `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2 StandardItemIdentification"`
	ClassifiedTaxCategories []ublClassifiedTaxCategory `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2 ClassifiedTaxCategory"`
}

type ublItemIdentification struct {
	ID string `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2 ID"`
}

type ublClassifiedTaxCategory struct {
	ID      string `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2 ID"`
	Percent string `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2 Percent"`
}

type ublPrice struct {
	PriceAmount string `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2 PriceAmount"`
}

// Parse decodes a Peppol BIS Billing 3.0 UBL 2.1 XML invoice into a canonical ParsedInvoice.
func (p *UBLParser) Parse(data []byte) (*domain.ParsedInvoice, error) {
	cleaned := bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	cleaned = bytes.TrimSpace(cleaned)
	if len(cleaned) == 0 {
		return nil, fmt.Errorf("empty UBL XML input")
	}

	var doc ublInvoice
	if err := xml.Unmarshal(cleaned, &doc); err != nil {
		return nil, fmt.Errorf("decode UBL XML: %w", err)
	}

	if doc.XMLName.Local != "Invoice" {
		return nil, fmt.Errorf("unexpected root element %q: expected Invoice", doc.XMLName.Local)
	}
	if doc.XMLName.Space != ublInvoiceNS {
		return nil, fmt.Errorf("unexpected XML namespace %q: expected %q", doc.XMLName.Space, ublInvoiceNS)
	}

	invNum := strings.TrimSpace(doc.ID)
	if invNum == "" {
		return nil, fmt.Errorf("missing cbc:ID (invoice number)")
	}

	issueDateStr := strings.TrimSpace(doc.IssueDate)
	if issueDateStr == "" {
		return nil, fmt.Errorf("missing cbc:IssueDate")
	}
	issueDate, err := time.Parse("2006-01-02", issueDateStr)
	if err != nil {
		return nil, fmt.Errorf("parse cbc:IssueDate %q: %w", issueDateStr, err)
	}

	var dueDate *time.Time
	if dueStr := strings.TrimSpace(doc.DueDate); dueStr != "" {
		parsedDue, err := time.Parse("2006-01-02", dueStr)
		if err != nil {
			return nil, fmt.Errorf("parse cbc:DueDate %q: %w", dueStr, err)
		}
		dueDate = &parsedDue
	}

	currency := strings.ToUpper(strings.TrimSpace(doc.CurrencyCode))
	if currency == "" {
		return nil, fmt.Errorf("missing cbc:DocumentCurrencyCode")
	}

	poNumber := ""
	if doc.OrderReference != nil {
		poNumber = strings.TrimSpace(doc.OrderReference.ID)
	}

	supplierCode := ""
	for _, ident := range doc.SupplierParty.Party.Identifications {
		if id := strings.TrimSpace(ident.ID); id != "" {
			supplierCode = id
			break
		}
	}
	if supplierCode == "" {
		return nil, fmt.Errorf("missing supplier code (cac:AccountingSupplierParty/cac:Party/cac:PartyIdentification/cbc:ID)")
	}

	supplierVAT := ""
	for _, ts := range doc.SupplierParty.Party.TaxSchemes {
		if cid := strings.TrimSpace(ts.CompanyID); cid != "" {
			supplierVAT = cid
			break
		}
	}

	subtotalStr := strings.TrimSpace(doc.MonetaryTotal.TaxExclusiveAmount)
	if subtotalStr == "" {
		subtotalStr = strings.TrimSpace(doc.MonetaryTotal.LineExtensionAmount)
	}
	if subtotalStr == "" {
		return nil, fmt.Errorf("missing cac:LegalMonetaryTotal/cbc:TaxExclusiveAmount")
	}
	subtotalPence, err := domain.ParseDecimalToPence(subtotalStr)
	if err != nil {
		return nil, fmt.Errorf("parse subtotal %q: %w", subtotalStr, err)
	}

	var vatTotalPence int64
	if len(doc.TaxTotals) == 0 || strings.TrimSpace(doc.TaxTotals[0].TaxAmount) == "" {
		return nil, fmt.Errorf("missing cac:TaxTotal/cbc:TaxAmount")
	}
	vatTotalPence, err = domain.ParseDecimalToPence(doc.TaxTotals[0].TaxAmount)
	if err != nil {
		return nil, fmt.Errorf("parse tax total %q: %w", doc.TaxTotals[0].TaxAmount, err)
	}

	payableStr := strings.TrimSpace(doc.MonetaryTotal.PayableAmount)
	if payableStr == "" {
		payableStr = strings.TrimSpace(doc.MonetaryTotal.TaxInclusiveAmount)
	}
	if payableStr == "" {
		return nil, fmt.Errorf("missing cac:LegalMonetaryTotal/cbc:PayableAmount")
	}
	grandTotalPence, err := domain.ParseDecimalToPence(payableStr)
	if err != nil {
		return nil, fmt.Errorf("parse payable amount %q: %w", payableStr, err)
	}

	if len(doc.InvoiceLines) == 0 {
		return nil, fmt.Errorf("invoice contains no cac:InvoiceLine elements")
	}

	lines := make([]domain.ParsedLine, 0, len(doc.InvoiceLines))
	for idx, rawLine := range doc.InvoiceLines {
		lineNum := idx + 1
		if idStr := strings.TrimSpace(rawLine.ID); idStr != "" {
			if n, err := strconv.Atoi(idStr); err == nil && n > 0 {
				lineNum = n
			}
		}

		qty, err := parseWholeQuantity(rawLine.InvoicedQuantity)
		if err != nil {
			return nil, fmt.Errorf("line %d: parse quantity %q: %w", lineNum, rawLine.InvoicedQuantity, err)
		}

		lineNetPence, err := domain.ParseDecimalToPence(rawLine.LineExtensionAmount)
		if err != nil {
			return nil, fmt.Errorf("line %d: parse LineExtensionAmount %q: %w", lineNum, rawLine.LineExtensionAmount, err)
		}

		unitPricePence, err := domain.ParseDecimalToPence(rawLine.Price.PriceAmount)
		if err != nil {
			return nil, fmt.Errorf("line %d: parse PriceAmount %q: %w", lineNum, rawLine.Price.PriceAmount, err)
		}

		sku := ""
		if rawLine.Item.SellersItemID != nil {
			sku = strings.TrimSpace(rawLine.Item.SellersItemID.ID)
		}
		if sku == "" && rawLine.Item.StandardItemID != nil {
			sku = strings.TrimSpace(rawLine.Item.StandardItemID.ID)
		}
		if sku == "" {
			return nil, fmt.Errorf("line %d: missing item SKU", lineNum)
		}

		var vatRateBPS int64
		if len(rawLine.Item.ClassifiedTaxCategories) > 0 && strings.TrimSpace(rawLine.Item.ClassifiedTaxCategories[0].Percent) != "" {
			vatRateBPS, err = domain.ParsePercentToBPS(rawLine.Item.ClassifiedTaxCategories[0].Percent)
			if err != nil {
				return nil, fmt.Errorf("line %d: parse VAT percent %q: %w", lineNum, rawLine.Item.ClassifiedTaxCategories[0].Percent, err)
			}
		}

		var lineVATPence int64
		if len(rawLine.TaxTotals) > 0 && strings.TrimSpace(rawLine.TaxTotals[0].TaxAmount) != "" {
			lineVATPence, err = domain.ParseDecimalToPence(rawLine.TaxTotals[0].TaxAmount)
			if err != nil {
				return nil, fmt.Errorf("line %d: parse line TaxAmount %q: %w", lineNum, rawLine.TaxTotals[0].TaxAmount, err)
			}
		} else {
			lineVATPence = domain.CalculateVATPence(lineNetPence, vatRateBPS)
		}

		lines = append(lines, domain.ParsedLine{
			LineNumber:     lineNum,
			SKU:            sku,
			Description:    strings.TrimSpace(rawLine.Item.Name),
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
		PONumber:        poNumber,
		IssueDate:       issueDate,
		DueDate:         dueDate,
		Currency:        currency,
		SubtotalPence:   subtotalPence,
		VATTotalPence:   vatTotalPence,
		GrandTotalPence: grandTotalPence,
		Lines:           lines,
	}, nil
}

// parseWholeQuantity parses an integer quantity string (e.g. "10" or "10.00") without floats.
func parseWholeQuantity(raw string) (int64, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, fmt.Errorf("empty quantity")
	}
	if idx := strings.IndexByte(s, '.'); idx >= 0 {
		frac := s[idx+1:]
		for i := 0; i < len(frac); i++ {
			if frac[i] != '0' {
				return 0, fmt.Errorf("fractional quantity %q is unsupported", raw)
			}
		}
		s = s[:idx]
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, err
	}
	return v, nil
}
