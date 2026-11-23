package domain

import (
	"time"

	"github.com/google/uuid"
)

type InvoiceStatus string

const (
	StatusUploaded         InvoiceStatus = "UPLOADED"
	StatusParsed           InvoiceStatus = "PARSED"
	StatusParseFailed      InvoiceStatus = "PARSE_FAILED"
	StatusAutoApproved     InvoiceStatus = "AUTO_APPROVED"
	StatusNeedsReview      InvoiceStatus = "NEEDS_REVIEW"
	StatusManuallyApproved InvoiceStatus = "MANUALLY_APPROVED"
	StatusRejected         InvoiceStatus = "REJECTED"
)

type SourceFormat string

const (
	FormatUBLXML SourceFormat = "UBL_XML"
	FormatPDF    SourceFormat = "PDF"
	FormatCSV    SourceFormat = "CSV"
)

type POStatus string

const (
	POStatusOpen              POStatus = "OPEN"
	POStatusPartiallyReceived POStatus = "PARTIALLY_RECEIVED"
	POStatusReceived          POStatus = "RECEIVED"
	POStatusClosed            POStatus = "CLOSED"
	POStatusCancelled         POStatus = "CANCELLED"
)

const (
	SeverityBlocking = "BLOCKING"
	SeverityWarning  = "WARNING"
)

type ParsedInvoice struct {
	SupplierCode    string       `json:"supplier_code"`
	SupplierVAT     string       `json:"supplier_vat"`
	InvoiceNumber   string       `json:"invoice_number"`
	PONumber        string       `json:"po_number"`
	IssueDate       time.Time    `json:"issue_date"`
	DueDate         *time.Time   `json:"due_date,omitempty"`
	Currency        string       `json:"currency"`
	SubtotalPence   int64        `json:"subtotal_pence"`
	VATTotalPence   int64        `json:"vat_total_pence"`
	GrandTotalPence int64        `json:"grand_total_pence"`
	Lines           []ParsedLine `json:"lines"`
}

type ParsedLine struct {
	LineID         *uuid.UUID `json:"line_id,omitempty"`
	LineNumber     int        `json:"line_number"`
	SKU            string     `json:"sku"`
	Description    string     `json:"description"`
	Quantity       int64      `json:"quantity"`
	UnitPricePence int64      `json:"unit_price_pence"`
	VATRateBPS     int64      `json:"vat_rate_bps"` // 2000 = 20.00%
	LineNetPence   int64      `json:"line_net_pence"`
	LineVATPence   int64      `json:"line_vat_pence"`
}

type Discrepancy struct {
	ID            uuid.UUID  `json:"id"`
	InvoiceID     uuid.UUID  `json:"invoice_id"`
	InvoiceLineID *uuid.UUID `json:"invoice_line_id,omitempty"`
	LineNumber    int        `json:"line_number,omitempty"`
	SKU           string     `json:"sku,omitempty"`
	Code          string     `json:"code"`
	Severity      string     `json:"severity"` // BLOCKING | WARNING
	Message       string     `json:"message"`
	ExpectedValue string     `json:"expected_value"`
	ActualValue   string     `json:"actual_value"`
	VariancePence int64      `json:"variance_pence"`
}

// Supplier represents a vendor master record.
type Supplier struct {
	ID           uuid.UUID `json:"id"`
	SupplierCode string    `json:"supplier_code"`
	Name         string    `json:"name"`
	VATNumber    string    `json:"vat_number"`
	PeppolID     string    `json:"peppol_id,omitempty"`
}

// PurchaseOrder is the reconciliation view of an internal Purchase Order.
type PurchaseOrder struct {
	ID            uuid.UUID `json:"id"`
	PONumber      string    `json:"po_number"`
	SupplierID    uuid.UUID `json:"supplier_id"`
	SupplierCode  string    `json:"supplier_code,omitempty"`
	Currency      string    `json:"currency"`
	Status        POStatus  `json:"status"`
	TotalNetPence int64     `json:"total_net_pence"`
	Lines         []POLine  `json:"lines"`
}

// POLine represents a single line item on a Purchase Order.
type POLine struct {
	ID             uuid.UUID `json:"id,omitempty"`
	LineNumber     int       `json:"line_number"`
	SKU            string    `json:"sku"`
	Description    string    `json:"description"`
	OrderedQty     int64     `json:"ordered_qty"`
	UnitPricePence int64     `json:"unit_price_pence"`
	VATRateBPS     int64     `json:"vat_rate_bps"` // 2000 = 20.00%
}

// GoodsReceipt represents a single warehouse delivery receipt for a Purchase Order.
type GoodsReceipt struct {
	ID              uuid.UUID          `json:"id"`
	GRNNumber       string             `json:"grn_number"`
	PurchaseOrderID uuid.UUID          `json:"purchase_order_id"`
	PONumber        string             `json:"po_number,omitempty"`
	ReceivedAt      time.Time          `json:"received_at"`
	WarehouseNotes  string             `json:"warehouse_notes,omitempty"`
	Lines           []GoodsReceiptLine `json:"lines"`
}

// GoodsReceiptLine records accepted and rejected quantities for a single SKU on a GRN.
type GoodsReceiptLine struct {
	ID          uuid.UUID `json:"id,omitempty"`
	SKU         string    `json:"sku"`
	AcceptedQty int64     `json:"accepted_qty"`
	RejectedQty int64     `json:"rejected_qty"`
}

// GoodsReceiptSummary is the aggregated reconciliation view of all GRNs for a Purchase Order.
type GoodsReceiptSummary struct {
	PurchaseOrderID uuid.UUID        `json:"purchase_order_id"`
	ReceiptCount    int              `json:"receipt_count"`
	AcceptedBySKU   map[string]int64 `json:"accepted_by_sku"`
}

// SummarizeGoodsReceipts aggregates accepted quantities per SKU across a slice of GRNs.
func SummarizeGoodsReceipts(poID uuid.UUID, receipts []GoodsReceipt) GoodsReceiptSummary {
	accepted := make(map[string]int64)
	for _, r := range receipts {
		for _, line := range r.Lines {
			accepted[line.SKU] += line.AcceptedQty
		}
	}
	return GoodsReceiptSummary{
		PurchaseOrderID: poID,
		ReceiptCount:    len(receipts),
		AcceptedBySKU:   accepted,
	}
}
