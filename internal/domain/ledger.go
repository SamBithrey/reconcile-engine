package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Invoice represents a persisted invoice row in the PostgreSQL ledger.
type Invoice struct {
	ID              uuid.UUID     `json:"id"`
	FileSHA256      string        `json:"file_sha256"`
	StorageKey      string        `json:"storage_key"`
	SourceFormat    SourceFormat  `json:"source_format"`
	Status          InvoiceStatus `json:"status"`
	SupplierID      *uuid.UUID    `json:"supplier_id,omitempty"`
	SupplierCodeRaw string        `json:"supplier_code_raw,omitempty"`
	SupplierName    string        `json:"supplier_name,omitempty"`
	InvoiceNumber   string        `json:"invoice_number,omitempty"`
	PONumberRaw     string        `json:"po_number_raw,omitempty"`
	PurchaseOrderID *uuid.UUID    `json:"purchase_order_id,omitempty"`
	IssueDate       *time.Time    `json:"issue_date,omitempty"`
	DueDate         *time.Time    `json:"due_date,omitempty"`
	Currency        string        `json:"currency"`
	SubtotalPence   int64         `json:"subtotal_pence"`
	VATTotalPence   int64         `json:"vat_total_pence"`
	GrandTotalPence int64         `json:"grand_total_pence"`
	ParseError      string        `json:"parse_error,omitempty"`
	DiscrepancyCnt  int           `json:"discrepancy_count,omitempty"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
}

// AuditEvent captures an immutable lifecycle transition for an invoice.
type AuditEvent struct {
	ID         uuid.UUID       `json:"id"`
	InvoiceID  uuid.UUID       `json:"invoice_id"`
	Actor      string          `json:"actor"`
	FromStatus *InvoiceStatus  `json:"from_status,omitempty"`
	ToStatus   InvoiceStatus   `json:"to_status"`
	Reason     string          `json:"reason,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

// InvoiceDetail combines an invoice with its line items, discrepancies, audit history,
// and matched Purchase Order / Goods Receipt context.
type InvoiceDetail struct {
	Invoice       Invoice              `json:"invoice"`
	Lines         []ParsedLine         `json:"lines"`
	Discrepancies []Discrepancy        `json:"discrepancies"`
	AuditEvents   []AuditEvent         `json:"audit_events"`
	PurchaseOrder *PurchaseOrder       `json:"purchase_order,omitempty"`
	GoodsReceipts *GoodsReceiptSummary `json:"goods_receipts,omitempty"`
}

// DiscrepancyCodeMetric counts occurrences and cumulative variance for a single rule code.
type DiscrepancyCodeMetric struct {
	Code               string `json:"code"`
	Count              int64  `json:"count"`
	TotalVariancePence int64  `json:"total_variance_pence"`
}

// MetricsSummary aggregates ledger status counts and integer minor-unit monetary totals.
type MetricsSummary struct {
	TotalInvoices        int64                   `json:"total_invoices"`
	CountsByStatus       map[InvoiceStatus]int64 `json:"counts_by_status"`
	TotalBilledPence     int64                   `json:"total_billed_pence"`
	ApprovedPence        int64                   `json:"approved_pence"`
	NeedsReviewPence     int64                   `json:"needs_review_pence"`
	TotalVariancePence   int64                   `json:"total_variance_pence"`
	DiscrepanciesByCode  []DiscrepancyCodeMetric `json:"discrepancies_by_code"`
	AutoApprovedCount    int64                   `json:"auto_approved_count"`
	NeedsReviewCount     int64                   `json:"needs_review_count"`
	ManuallyApprovedCnt  int64                   `json:"manually_approved_count"`
	RejectedCount        int64                   `json:"rejected_count"`
	ParseFailedCount     int64                   `json:"parse_failed_count"`
	AutoApprovalRateBps  int64                   `json:"auto_approval_rate_bps"`
}
