import { getDiscrepancyMeta } from "./discrepancies.ts";
import {
  formatBPS,
  formatPenceDecimal,
  sumPence,
  toSafeBigInt,
} from "./money.ts";
import type { InvoiceDetail, InvoiceLine, POLine } from "./types.ts";

export type AccountingExportScope =
  | "APPROVED_ONLY"
  | "ALL_RECONCILED"
  | "CURRENT_VIEW";

export interface AccountingExportBundle {
  schema_version: "1.0.0";
  export_scope: AccountingExportScope;
  invoice_count: number;
  line_count: number;
  totals_by_currency: Array<{
    currency: string;
    invoice_count: number;
    subtotal_pence: number;
    vat_total_pence: number;
    grand_total_pence: number;
    grand_total_decimal: string;
  }>;
  invoices: Array<{
    invoice_id: string;
    invoice_number: string;
    status: string;
    source_format: string;
    file_sha256: string;
    storage_key: string;
    supplier_code: string;
    supplier_name: string;
    po_number: string;
    po_status: string | null;
    grn_receipt_count: number;
    issue_date: string | null;
    due_date: string | null;
    currency: string;
    subtotal_pence: number;
    vat_total_pence: number;
    grand_total_pence: number;
    grand_total_decimal: string;
    parse_error: string | null;
    lines: Array<{
      line_number: number;
      sku: string;
      description: string;
      invoiced_qty: number;
      po_ordered_qty: number | null;
      grn_accepted_qty: number | null;
      unit_price_pence: number;
      vat_rate_bps: number;
      vat_rate_pct: string;
      line_net_pence: number;
      line_vat_pence: number;
      line_gross_pence: number;
    }>;
    discrepancies: Array<{
      rule_id: string;
      code: string;
      severity: string;
      line_number: number | null;
      sku: string | null;
      message: string;
      expected_value: string;
      actual_value: string;
      variance_pence: number;
    }>;
    audit_events: Array<{
      actor: string;
      from_status: string | null;
      to_status: string;
      reason: string;
      created_at: string;
    }>;
    created_at: string;
    updated_at: string;
  }>;
}

function sortDetailsDeterministically(
  details: ReadonlyArray<InvoiceDetail>,
): InvoiceDetail[] {
  return details.toSorted((a, b) => {
    const byCreated = a.invoice.created_at.localeCompare(b.invoice.created_at);
    if (byCreated !== 0) {
      return byCreated;
    }
    return a.invoice.id.localeCompare(b.invoice.id);
  });
}

function indexPOLinesBySku(detail: InvoiceDetail): Map<string, POLine> {
  const map = new Map<string, POLine>();
  for (const poLine of detail.purchase_order?.lines ?? []) {
    map.set(poLine.sku, poLine);
  }
  return map;
}

const MAX_SAFE_BIGINT = BigInt(Number.MAX_SAFE_INTEGER);
const MIN_SAFE_BIGINT = BigInt(Number.MIN_SAFE_INTEGER);

function safeBigIntToNumber(value: bigint): number {
  if (value > MAX_SAFE_BIGINT || value < MIN_SAFE_BIGINT) {
    throw new RangeError(`Integer minor-unit amount ${value.toString()} exceeds safe JSON integer bounds`);
  }
  return Number(value);
}

function lineGrossPence(line: InvoiceLine): number {
  const net = toSafeBigInt(line.line_net_pence);
  const vat = toSafeBigInt(line.line_vat_pence);
  return safeBigIntToNumber(net + vat);
}

export function buildAccountingExportBundle(
  details: ReadonlyArray<InvoiceDetail>,
  scope: AccountingExportScope,
): AccountingExportBundle {
  const sorted = sortDetailsDeterministically(details);
  const currencyMap = new Map<
    string,
    {
      count: number;
      subtotalPence: bigint;
      vatPence: bigint;
      grandPence: bigint;
    }
  >();

  let totalLines = 0;

  const exportedInvoices = sorted.map((detail) => {
    const inv = detail.invoice;
    const currency = (inv.currency || "GBP").trim().toUpperCase();

    const curBucket = currencyMap.get(currency) ?? {
      count: 0,
      subtotalPence: 0n,
      vatPence: 0n,
      grandPence: 0n,
    };
    curBucket.count += 1;
    curBucket.subtotalPence += toSafeBigInt(inv.subtotal_pence);
    curBucket.vatPence += toSafeBigInt(inv.vat_total_pence);
    curBucket.grandPence += toSafeBigInt(inv.grand_total_pence);
    currencyMap.set(currency, curBucket);

    const poBySku = indexPOLinesBySku(detail);
    const grnMap = detail.goods_receipts?.accepted_by_sku ?? {};
    const hasGrnContext = detail.goods_receipts !== null && detail.goods_receipts !== undefined;

    const lines = (detail.lines ?? [])
      .toSorted((a, b) => a.line_number - b.line_number)
      .map((line) => {
        const poLine = poBySku.get(line.sku);
        const grnAccepted = hasGrnContext
          ? Object.hasOwn(grnMap, line.sku)
            ? grnMap[line.sku]
            : 0
          : null;

        return {
          line_number: line.line_number,
          sku: line.sku,
          description: line.description,
          invoiced_qty: line.quantity,
          po_ordered_qty: poLine ? poLine.ordered_qty : null,
          grn_accepted_qty: grnAccepted,
          unit_price_pence: line.unit_price_pence,
          vat_rate_bps: line.vat_rate_bps,
          vat_rate_pct: formatBPS(line.vat_rate_bps),
          line_net_pence: line.line_net_pence,
          line_vat_pence: line.line_vat_pence,
          line_gross_pence: lineGrossPence(line),
        };
      });

    totalLines += lines.length;

    const discrepancies = (detail.discrepancies ?? []).map((disc) => {
      const meta = getDiscrepancyMeta(disc.code);
      return {
        rule_id: meta.ruleId,
        code: disc.code,
        severity: disc.severity,
        line_number: disc.line_number ?? null,
        sku: disc.sku || null,
        message: disc.message,
        expected_value: disc.expected_value,
        actual_value: disc.actual_value,
        variance_pence: disc.variance_pence,
      };
    });

    const auditEvents = (detail.audit_events ?? []).map((ev) => ({
      actor: ev.actor,
      from_status: ev.from_status ?? null,
      to_status: ev.to_status,
      reason: ev.reason ?? "",
      created_at: ev.created_at,
    }));

    return {
      invoice_id: inv.id,
      invoice_number: inv.invoice_number ?? "",
      status: inv.status,
      source_format: inv.source_format,
      file_sha256: inv.file_sha256,
      storage_key: inv.storage_key,
      supplier_code: inv.supplier_code_raw ?? "",
      supplier_name: inv.supplier_name ?? "",
      po_number: inv.po_number_raw || detail.purchase_order?.po_number || "",
      po_status: detail.purchase_order?.status ?? null,
      grn_receipt_count: detail.goods_receipts?.receipt_count ?? 0,
      issue_date: inv.issue_date ? inv.issue_date.slice(0, 10) : null,
      due_date: inv.due_date ? inv.due_date.slice(0, 10) : null,
      currency,
      subtotal_pence: inv.subtotal_pence,
      vat_total_pence: inv.vat_total_pence,
      grand_total_pence: inv.grand_total_pence,
      grand_total_decimal: formatPenceDecimal(inv.grand_total_pence),
      parse_error: inv.parse_error || null,
      lines,
      discrepancies,
      audit_events: auditEvents,
      created_at: inv.created_at,
      updated_at: inv.updated_at,
    };
  });

  const totalsByCurrency = [...currencyMap.entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([currency, bucket]) => ({
      currency,
      invoice_count: bucket.count,
      subtotal_pence: safeBigIntToNumber(bucket.subtotalPence),
      vat_total_pence: safeBigIntToNumber(bucket.vatPence),
      grand_total_pence: safeBigIntToNumber(bucket.grandPence),
      grand_total_decimal: formatPenceDecimal(bucket.grandPence),
    }));

  return {
    schema_version: "1.0.0",
    export_scope: scope,
    invoice_count: exportedInvoices.length,
    line_count: totalLines,
    totals_by_currency: totalsByCurrency,
    invoices: exportedInvoices,
  };
}

export function serializeAccountingJSON(
  details: ReadonlyArray<InvoiceDetail>,
  scope: AccountingExportScope,
): string {
  return JSON.stringify(buildAccountingExportBundle(details, scope), null, 2) + "\n";
}

function escapeCsvField(value: string | number | bigint | null | undefined): string {
  if (value === null || value === undefined) {
    return "";
  }
  const str = String(value);
  if (
    str.includes(",") ||
    str.includes('"') ||
    str.includes("\n") ||
    str.includes("\r")
  ) {
    return `"${str.replace(/"/g, '""')}"`;
  }
  return str;
}

const CSV_HEADERS = [
  "invoice_id",
  "invoice_number",
  "status",
  "source_format",
  "supplier_code",
  "supplier_name",
  "po_number",
  "issue_date",
  "due_date",
  "currency",
  "invoice_subtotal_pence",
  "invoice_vat_total_pence",
  "invoice_grand_total_pence",
  "invoice_grand_total_decimal",
  "line_number",
  "sku",
  "description",
  "invoiced_qty",
  "po_ordered_qty",
  "grn_accepted_qty",
  "unit_price_pence",
  "vat_rate_bps",
  "vat_rate_pct",
  "line_net_pence",
  "line_vat_pence",
  "line_gross_pence",
  "discrepancy_count",
  "discrepancy_rule_ids",
  "discrepancy_codes",
  "total_variance_pence",
  "last_audit_actor",
  "last_audit_status",
  "last_audit_reason",
  "last_audit_at",
] as const;

/**
 * Serializes reconciled invoices to deterministic RFC-4180 CSV with one row per line item
 * (or one summary row for invoices without parsed lines, e.g. PARSE_FAILED).
 * All currency amounts are exported in exact integer pence alongside a BigInt-formatted decimal string.
 */
export function serializeAccountingCSV(
  details: ReadonlyArray<InvoiceDetail>,
): string {
  const sorted = sortDetailsDeterministically(details);
  const rows: string[] = [CSV_HEADERS.join(",")];

  for (const detail of sorted) {
    const inv = detail.invoice;
    const poBySku = indexPOLinesBySku(detail);
    const grnMap = detail.goods_receipts?.accepted_by_sku ?? {};
    const hasGrnContext = detail.goods_receipts !== null && detail.goods_receipts !== undefined;

    const discrepancies = detail.discrepancies ?? [];
    const ruleIds = discrepancies.map((d) => getDiscrepancyMeta(d.code).ruleId).join("|");
    const codes = discrepancies.map((d) => d.code).join("|");
    const totalVariancePence = sumPence(discrepancies.map((d) => d.variance_pence));

    const auditEvents = detail.audit_events ?? [];
    const lastAudit =
      auditEvents.length > 0 ? auditEvents[auditEvents.length - 1] : undefined;

    const commonPrefix = [
      inv.id,
      inv.invoice_number ?? "",
      inv.status,
      inv.source_format,
      inv.supplier_code_raw ?? "",
      inv.supplier_name ?? "",
      inv.po_number_raw || detail.purchase_order?.po_number || "",
      inv.issue_date ? inv.issue_date.slice(0, 10) : "",
      inv.due_date ? inv.due_date.slice(0, 10) : "",
      (inv.currency || "GBP").trim().toUpperCase(),
      inv.subtotal_pence,
      inv.vat_total_pence,
      inv.grand_total_pence,
      formatPenceDecimal(inv.grand_total_pence),
    ];

    const commonSuffix = [
      discrepancies.length,
      ruleIds,
      codes,
      totalVariancePence.toString(),
      lastAudit?.actor ?? "",
      lastAudit?.to_status ?? inv.status,
      lastAudit?.reason || inv.parse_error || "",
      lastAudit?.created_at ?? inv.updated_at,
    ];

    const lines = (detail.lines ?? []).toSorted(
      (a, b) => a.line_number - b.line_number,
    );

    if (lines.length === 0) {
      const emptyLineCols = ["", "", "", "", "", "", "", "", "", "", "", ""];
      rows.push(
        [...commonPrefix, ...emptyLineCols, ...commonSuffix]
          .map(escapeCsvField)
          .join(","),
      );
      continue;
    }

    for (const line of lines) {
      const poLine = poBySku.get(line.sku);
      const grnAccepted = hasGrnContext
        ? Object.hasOwn(grnMap, line.sku)
          ? grnMap[line.sku]
          : 0
        : "";

      const lineCols = [
        line.line_number,
        line.sku,
        line.description,
        line.quantity,
        poLine ? poLine.ordered_qty : "",
        grnAccepted,
        line.unit_price_pence,
        line.vat_rate_bps,
        formatBPS(line.vat_rate_bps),
        line.line_net_pence,
        line.line_vat_pence,
        lineGrossPence(line),
      ];

      rows.push(
        [...commonPrefix, ...lineCols, ...commonSuffix]
          .map(escapeCsvField)
          .join(","),
      );
    }
  }

  return rows.join("\r\n") + "\r\n";
}

export function triggerDownload(
  content: string,
  filename: string,
  mimeType: string,
): void {
  const blob = new Blob([content], { type: mimeType });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}
