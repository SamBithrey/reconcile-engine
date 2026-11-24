import type {
  DiscrepancyCode,
  DiscrepancyRuleId,
  InvoiceStatus,
  POStatus,
  SourceFormat,
} from "./types";

export interface DiscrepancyRuleMeta {
  ruleId: DiscrepancyRuleId | "RULE";
  code: string;
  title: string;
  pass: "PASS_A" | "PASS_B" | "UNKNOWN";
  description: string;
}

const DISCREPANCY_CATALOG: Record<DiscrepancyCode, DiscrepancyRuleMeta> = {
  LINE_MATH_ERROR: {
    ruleId: "INT-01",
    code: "LINE_MATH_ERROR",
    title: "Line Math Error",
    pass: "PASS_A",
    description: "abs((qty * unit_price_pence) - line_net_pence) > 1p",
  },
  VAT_MISMATCH: {
    ruleId: "INT-02",
    code: "VAT_MISMATCH",
    title: "Line VAT Mismatch",
    pass: "PASS_A",
    description: "abs(expected_line_vat - line_vat_pence) > 1p",
  },
  TOTAL_MISMATCH: {
    ruleId: "INT-03",
    code: "TOTAL_MISMATCH",
    title: "Document Total Mismatch",
    pass: "PASS_A",
    description: "Header subtotal or grand total diverges from line sums by > 1p",
  },
  CURRENCY_MISMATCH: {
    ruleId: "INT-04",
    code: "CURRENCY_MISMATCH",
    title: "Currency Mismatch",
    pass: "PASS_A",
    description: "Invoice currency does not match Purchase Order currency",
  },
  MISSING_PO: {
    ruleId: "3WAY-01",
    code: "MISSING_PO",
    title: "Missing Purchase Order",
    pass: "PASS_B",
    description: "PO reference is empty or does not exist in the ledger",
  },
  PO_SUPPLIER_MISMATCH: {
    ruleId: "3WAY-02",
    code: "PO_SUPPLIER_MISMATCH",
    title: "PO Supplier Mismatch",
    pass: "PASS_B",
    description: "Invoice supplier does not match the Purchase Order supplier",
  },
  PO_CLOSED: {
    ruleId: "3WAY-03",
    code: "PO_CLOSED",
    title: "Purchase Order Closed / Cancelled",
    pass: "PASS_B",
    description: "Purchase Order status is CLOSED or CANCELLED and cannot be billed",
  },
  MISSING_GRN: {
    ruleId: "3WAY-04",
    code: "MISSING_GRN",
    title: "Missing Goods Received Note",
    pass: "PASS_B",
    description: "Zero Goods Received Notes exist for the Purchase Order",
  },
  UNMATCHED_SKU: {
    ruleId: "3WAY-05",
    code: "UNMATCHED_SKU",
    title: "Unmatched Line SKU",
    pass: "PASS_B",
    description: "Invoiced SKU does not exist on the linked Purchase Order",
  },
  QTY_EXCEEDS_GRN: {
    ruleId: "3WAY-06",
    code: "QTY_EXCEEDS_GRN",
    title: "Quantity Exceeds GRN Accepted",
    pass: "PASS_B",
    description: "Cumulative invoiced quantity exceeds warehouse GRN accepted quantity",
  },
  QTY_EXCEEDS_PO: {
    ruleId: "3WAY-07",
    code: "QTY_EXCEEDS_PO",
    title: "Quantity Exceeds PO Ordered",
    pass: "PASS_B",
    description: "Cumulative invoiced quantity exceeds Purchase Order ordered quantity",
  },
  PRICE_VARIANCE: {
    ruleId: "3WAY-08",
    code: "PRICE_VARIANCE",
    title: "Unit Price Variance",
    pass: "PASS_B",
    description: "Unit price exceeds both £0.50 (50p) and 1.00% (100 bps) PO tolerance",
  },
  VAT_RATE_MISMATCH: {
    ruleId: "3WAY-09",
    code: "VAT_RATE_MISMATCH",
    title: "VAT Rate Mismatch",
    pass: "PASS_B",
    description: "Invoice line VAT rate (bps) differs from Purchase Order VAT rate",
  },
};

export function getDiscrepancyMeta(code: string): DiscrepancyRuleMeta {
  if (code in DISCREPANCY_CATALOG) {
    return DISCREPANCY_CATALOG[code as DiscrepancyCode];
  }
  return {
    ruleId: "RULE",
    code,
    title: code,
    pass: "UNKNOWN",
    description: "Reconciliation discrepancy flagged by matching engine",
  };
}

export interface InvoiceStatusMeta {
  status: InvoiceStatus;
  label: string;
  badgeClass: string;
  dotClass: string;
  description: string;
  isTerminal: boolean;
}

const STATUS_META: Record<InvoiceStatus, InvoiceStatusMeta> = {
  UPLOADED: {
    status: "UPLOADED",
    label: "Uploaded",
    badgeClass: "bg-slate-100 text-slate-700 border-slate-300",
    dotClass: "bg-slate-500",
    description: "Queued in MinIO & RabbitMQ for deterministic parsing",
    isTerminal: false,
  },
  PARSED: {
    status: "PARSED",
    label: "Parsed",
    badgeClass: "bg-sky-50 text-sky-700 border-sky-200",
    dotClass: "bg-sky-500",
    description: "Normalized into ledger; awaiting 3-way PO/GRN matching",
    isTerminal: false,
  },
  PARSE_FAILED: {
    status: "PARSE_FAILED",
    label: "Parse Failed",
    badgeClass: "bg-rose-50 text-rose-800 border-rose-200",
    dotClass: "bg-rose-600",
    description: "Corrupt document, duplicate invoice number, or unsupported scanned PDF",
    isTerminal: true,
  },
  AUTO_APPROVED: {
    status: "AUTO_APPROVED",
    label: "Auto Approved",
    badgeClass: "bg-emerald-50 text-emerald-800 border-emerald-200",
    dotClass: "bg-emerald-600",
    description: "Passed Pass A integrity and Pass B 3-way match with 0 discrepancies",
    isTerminal: true,
  },
  NEEDS_REVIEW: {
    status: "NEEDS_REVIEW",
    label: "Needs Review",
    badgeClass: "bg-amber-50 text-amber-800 border-amber-300",
    dotClass: "bg-amber-500",
    description: "Awaiting HITL operator verification and approval/rejection",
    isTerminal: false,
  },
  MANUALLY_APPROVED: {
    status: "MANUALLY_APPROVED",
    label: "Manually Approved",
    badgeClass: "bg-teal-50 text-teal-800 border-teal-200",
    dotClass: "bg-teal-600",
    description: "Verified and approved by human operator with immutable audit log",
    isTerminal: true,
  },
  REJECTED: {
    status: "REJECTED",
    label: "Rejected",
    badgeClass: "bg-red-50 text-red-800 border-red-200",
    dotClass: "bg-red-600",
    description: "Rejected by human operator with immutable audit log",
    isTerminal: true,
  },
};

export function getStatusMeta(status: InvoiceStatus): InvoiceStatusMeta {
  return STATUS_META[status];
}

export interface SourceFormatMeta {
  format: SourceFormat;
  label: string;
  extension: string;
  mimeType: string;
  badgeClass: string;
}

const FORMAT_META: Record<SourceFormat, SourceFormatMeta> = {
  UBL_XML: {
    format: "UBL_XML",
    label: "UBL XML",
    extension: "xml",
    mimeType: "application/xml",
    badgeClass: "bg-indigo-50 text-indigo-700 border-indigo-200",
  },
  PDF: {
    format: "PDF",
    label: "PDF",
    extension: "pdf",
    mimeType: "application/pdf",
    badgeClass: "bg-purple-50 text-purple-700 border-purple-200",
  },
  CSV: {
    format: "CSV",
    label: "CSV",
    extension: "csv",
    mimeType: "text/csv",
    badgeClass: "bg-cyan-50 text-cyan-800 border-cyan-200",
  },
};

export function getFormatMeta(format: SourceFormat): SourceFormatMeta {
  return FORMAT_META[format];
}

export function getPOStatusBadgeClass(status: POStatus): string {
  switch (status) {
    case "RECEIVED":
      return "bg-emerald-50 text-emerald-800 border-emerald-200";
    case "PARTIALLY_RECEIVED":
      return "bg-amber-50 text-amber-800 border-amber-200";
    case "OPEN":
      return "bg-sky-50 text-sky-800 border-sky-200";
    case "CLOSED":
    case "CANCELLED":
      return "bg-rose-50 text-rose-800 border-rose-200";
  }
}
