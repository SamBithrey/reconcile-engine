export type InvoiceStatus =
  | "UPLOADED"
  | "PARSED"
  | "PARSE_FAILED"
  | "AUTO_APPROVED"
  | "NEEDS_REVIEW"
  | "MANUALLY_APPROVED"
  | "REJECTED";

export const ALL_INVOICE_STATUSES: readonly InvoiceStatus[] = [
  "NEEDS_REVIEW",
  "AUTO_APPROVED",
  "MANUALLY_APPROVED",
  "REJECTED",
  "PARSE_FAILED",
  "PARSED",
  "UPLOADED",
] as const;

export type SourceFormat = "UBL_XML" | "PDF" | "CSV";

export const ALL_SOURCE_FORMATS: readonly SourceFormat[] = [
  "UBL_XML",
  "PDF",
  "CSV",
] as const;

export type POStatus =
  | "OPEN"
  | "PARTIALLY_RECEIVED"
  | "RECEIVED"
  | "CLOSED"
  | "CANCELLED";

export type DiscrepancySeverity = "BLOCKING" | "WARNING";

export type DiscrepancyCode =
  | "LINE_MATH_ERROR"
  | "VAT_MISMATCH"
  | "TOTAL_MISMATCH"
  | "CURRENCY_MISMATCH"
  | "MISSING_PO"
  | "PO_SUPPLIER_MISMATCH"
  | "PO_CLOSED"
  | "MISSING_GRN"
  | "UNMATCHED_SKU"
  | "QTY_EXCEEDS_GRN"
  | "QTY_EXCEEDS_PO"
  | "PRICE_VARIANCE"
  | "VAT_RATE_MISMATCH";

export type DiscrepancyRuleId =
  | "INT-01"
  | "INT-02"
  | "INT-03"
  | "INT-04"
  | "3WAY-01"
  | "3WAY-02"
  | "3WAY-03"
  | "3WAY-04"
  | "3WAY-05"
  | "3WAY-06"
  | "3WAY-07"
  | "3WAY-08"
  | "3WAY-09";

export interface Invoice {
  id: string;
  file_sha256: string;
  storage_key: string;
  source_format: SourceFormat;
  status: InvoiceStatus;
  supplier_id?: string;
  supplier_code_raw?: string;
  supplier_name?: string;
  invoice_number?: string;
  po_number_raw?: string;
  purchase_order_id?: string;
  issue_date?: string;
  due_date?: string;
  currency: string;
  subtotal_pence: number;
  vat_total_pence: number;
  grand_total_pence: number;
  parse_error?: string;
  discrepancy_count?: number;
  created_at: string;
  updated_at: string;
}

export interface InvoiceLine {
  line_id?: string;
  line_number: number;
  sku: string;
  description: string;
  quantity: number;
  unit_price_pence: number;
  vat_rate_bps: number;
  line_net_pence: number;
  line_vat_pence: number;
}

export interface Discrepancy {
  id: string;
  invoice_id: string;
  invoice_line_id?: string;
  line_number?: number;
  sku?: string;
  code: DiscrepancyCode | (string & {});
  severity: DiscrepancySeverity;
  message: string;
  expected_value: string;
  actual_value: string;
  variance_pence: number;
}

export interface AuditEvent {
  id: string;
  invoice_id: string;
  actor: string;
  from_status?: InvoiceStatus | null;
  to_status: InvoiceStatus;
  reason?: string;
  metadata?: Record<string, unknown> | null;
  created_at: string;
}

export interface POLine {
  id?: string;
  line_number: number;
  sku: string;
  description: string;
  ordered_qty: number;
  unit_price_pence: number;
  vat_rate_bps: number;
}

export interface PurchaseOrder {
  id: string;
  po_number: string;
  supplier_id: string;
  supplier_code?: string;
  currency: string;
  status: POStatus;
  total_net_pence: number;
  lines: POLine[];
}

export interface GoodsReceiptSummary {
  purchase_order_id: string;
  receipt_count: number;
  accepted_by_sku: Record<string, number>;
}

export interface InvoiceDetail {
  invoice: Invoice;
  lines: InvoiceLine[];
  discrepancies: Discrepancy[];
  audit_events: AuditEvent[];
  purchase_order?: PurchaseOrder | null;
  goods_receipts?: GoodsReceiptSummary | null;
}

export interface InvoiceListResponse {
  invoices: Invoice[];
  total: number;
  limit: number;
  offset: number;
}

export interface DiscrepancyCodeMetric {
  code: DiscrepancyCode | (string & {});
  count: number;
  total_variance_pence: number;
}

export interface MetricsSummary {
  total_invoices: number;
  counts_by_status: Partial<Record<InvoiceStatus, number>>;
  total_billed_pence: number;
  approved_pence: number;
  needs_review_pence: number;
  total_variance_pence: number;
  discrepancies_by_code: DiscrepancyCodeMetric[];
  auto_approved_count: number;
  needs_review_count: number;
  manually_approved_count: number;
  rejected_count: number;
  parse_failed_count: number;
  auto_approval_rate_bps: number;
}

export interface UploadInvoiceResponse {
  invoice_id: string;
  status: InvoiceStatus;
  file_sha256: string;
  storage_key: string;
  source_format: SourceFormat;
  duplicate: boolean;
}

export interface UploadInvoiceResult {
  httpStatus: number;
  data: UploadInvoiceResponse;
}

export interface ResolveInvoiceRequest {
  status: "MANUALLY_APPROVED" | "REJECTED";
  actor: string;
  reason: string;
}

export interface HealthResponse {
  status: "ok" | "degraded";
  dependencies: Record<string, string>;
  timestamp: string;
}
