import {
  formatPenceDecimal,
  sumPence,
  toSafeBigInt,
} from "./money.ts";
import type {
  AuditEvent,
  Discrepancy,
  DiscrepancyCodeMetric,
  GoodsReceiptSummary,
  HealthResponse,
  Invoice,
  InvoiceDetail,
  InvoiceLine,
  InvoiceListResponse,
  InvoiceStatus,
  MetricsSummary,
  PurchaseOrder,
  ResolveInvoiceRequest,
  SourceFormat,
  UploadInvoiceResult,
} from "./types.ts";

export interface DemoListInvoicesParams {
  status?: InvoiceStatus | "";
  supplier_code?: string;
  source_format?: SourceFormat | "";
  q?: string;
  limit?: number;
  offset?: number;
}

export class DemoStoreError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = "DemoStoreError";
    this.status = status;
  }
}

/**
 * Opt-in Static Demo Mode flag (`NEXT_PUBLIC_DEMO_MODE=true`).
 * Defaults to `false` so live `cmd/api` mode is always used unless explicitly enabled.
 */
export function isDemoMode(): boolean {
  const raw = process.env.NEXT_PUBLIC_DEMO_MODE?.trim().toLowerCase();
  return raw === "true" || raw === "1";
}

interface DemoRecord {
  detail: InvoiceDetail;
  rawText?: string;
  rawPdfBytes?: Uint8Array;
}

function deterministicSha256(seedNum: number): string {
  const prefix = seedNum.toString(16).padStart(4, "0");
  return `${prefix}a9c4e7f102b83d65`.repeat(4);
}

function demoUUID(kind: "inv" | "po" | "sup" | "disc" | "aud" | "line", idx: number, sub = 0): string {
  const kindMap: Record<typeof kind, string> = {
    inv: "1000",
    po: "2000",
    sup: "3000",
    disc: "4000",
    aud: "5000",
    line: "6000",
  };
  const a = kindMap[kind];
  const b = idx.toString(10).padStart(4, "0");
  const c = sub.toString(10).padStart(12, "0");
  return `00000000-${a}-4000-8${b.slice(0, 3)}-${b.slice(3)}${c.slice(1)}`;
}

const SUPPLIERS = {
  ACME: {
    id: demoUUID("sup", 1),
    code: "SUP-ACME-01",
    name: "Acme Industrial Supplies Ltd",
    vat: "GB123456789",
  },
  THAMES: {
    id: demoUUID("sup", 2),
    code: "SUP-THAMES-02",
    name: "Thames Precision Fasteners PLC",
    vat: "GB234567891",
  },
  NORTH: {
    id: demoUUID("sup", 3),
    code: "SUP-NORTH-03",
    name: "Northern Hydraulics & Valves Ltd",
    vat: "GB345678912",
  },
  MIDLAND: {
    id: demoUUID("sup", 4),
    code: "SUP-MIDLAND-04",
    name: "Midland Electrical Components Ltd",
    vat: "GB456789123",
  },
  CLYDE: {
    id: demoUUID("sup", 5),
    code: "SUP-CLYDE-05",
    name: "Clyde Marine Bearings Co",
    vat: "GB567891234",
  },
} as const;

function xmlEscape(val: string): string {
  return val
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

function buildUBLXml(detail: InvoiceDetail): string {
  const inv = detail.invoice;
  const currency = inv.currency || "GBP";
  const linesXml = detail.lines
    .map(
      (line) => `  <cac:InvoiceLine>
    <cbc:ID>${line.line_number}</cbc:ID>
    <cbc:InvoicedQuantity unitCode="EA">${line.quantity}</cbc:InvoicedQuantity>
    <cbc:LineExtensionAmount currencyID="${currency}">${formatPenceDecimal(line.line_net_pence)}</cbc:LineExtensionAmount>
    <cac:Item>
      <cbc:Name>${xmlEscape(line.description)}</cbc:Name>
      <cac:SellersItemIdentification><cbc:ID>${xmlEscape(line.sku)}</cbc:ID></cac:SellersItemIdentification>
      <cac:ClassifiedTaxCategory>
        <cbc:ID>S</cbc:ID>
        <cbc:Percent>${formatPenceDecimal(line.vat_rate_bps)}</cbc:Percent>
        <cac:TaxScheme><cbc:ID>VAT</cbc:ID></cac:TaxScheme>
      </cac:ClassifiedTaxCategory>
    </cac:Item>
    <cac:Price>
      <cbc:PriceAmount currencyID="${currency}">${formatPenceDecimal(line.unit_price_pence)}</cbc:PriceAmount>
    </cac:Price>
  </cac:InvoiceLine>`,
    )
    .join("\n");

  return `<?xml version="1.0" encoding="UTF-8"?>
<Invoice xmlns="urn:oasis:names:specification:ubl:schema:xsd:Invoice-2"
         xmlns:cac="urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2"
         xmlns:cbc="urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2">
  <cbc:CustomizationID>urn:cen.eu:en16931:2017#compliant#urn:fdc:peppol.eu:2017:poacc:billing:3.0</cbc:CustomizationID>
  <cbc:ID>${xmlEscape(inv.invoice_number ?? "")}</cbc:ID>
  <cbc:IssueDate>${(inv.issue_date ?? "2026-11-20").slice(0, 10)}</cbc:IssueDate>
  <cbc:DueDate>${(inv.due_date ?? "2026-12-20").slice(0, 10)}</cbc:DueDate>
  <cbc:DocumentCurrencyCode>${currency}</cbc:DocumentCurrencyCode>
  <cac:OrderReference>
    <cbc:ID>${xmlEscape(inv.po_number_raw ?? "")}</cbc:ID>
  </cac:OrderReference>
  <cac:AccountingSupplierParty>
    <cac:Party>
      <cac:PartyIdentification><cbc:ID>${xmlEscape(inv.supplier_code_raw ?? "")}</cbc:ID></cac:PartyIdentification>
      <cac:PartyName><cbc:Name>${xmlEscape(inv.supplier_name ?? "")}</cbc:Name></cac:PartyName>
    </cac:Party>
  </cac:AccountingSupplierParty>
  <cac:TaxTotal>
    <cbc:TaxAmount currencyID="${currency}">${formatPenceDecimal(inv.vat_total_pence)}</cbc:TaxAmount>
  </cac:TaxTotal>
  <cac:LegalMonetaryTotal>
    <cbc:TaxExclusiveAmount currencyID="${currency}">${formatPenceDecimal(inv.subtotal_pence)}</cbc:TaxExclusiveAmount>
    <cbc:PayableAmount currencyID="${currency}">${formatPenceDecimal(inv.grand_total_pence)}</cbc:PayableAmount>
  </cac:LegalMonetaryTotal>
${linesXml}
</Invoice>
`;
}

function buildCSVManifest(detail: InvoiceDetail): string {
  const inv = detail.invoice;
  const header =
    "invoice_number,po_number,supplier_code,issue_date,currency,sku,description,qty,unit_price_pence,vat_rate_bps,line_net_pence,line_vat_pence";
  const rows = detail.lines.map((line) => {
    const desc =
      line.description.includes(",") || line.description.includes('"')
        ? `"${line.description.replace(/"/g, '""')}"`
        : line.description;
    return [
      inv.invoice_number ?? "",
      inv.po_number_raw ?? "",
      inv.supplier_code_raw ?? "",
      (inv.issue_date ?? "2026-11-20").slice(0, 10),
      inv.currency || "GBP",
      line.sku,
      desc,
      line.quantity,
      line.unit_price_pence,
      line.vat_rate_bps,
      line.line_net_pence,
      line.line_vat_pence,
    ].join(",");
  });
  return `${header}\r\n${rows.join("\r\n")}\r\n`;
}

function pdfEscape(text: string): string {
  return text
    .replace(/\\/g, "\\\\")
    .replace(/\(/g, "\\(")
    .replace(/\)/g, "\\)")
    .replace(/[^\x20-\x7e]/g, " ");
}

/**
 * Generates a valid, self-contained minimal %PDF-1.4 document so PDF invoices render
 * natively inside the browser's iframe viewer in Static Demo Mode with zero network calls.
 */
function buildMinimalDemoPdf(lines: string[]): Uint8Array {
  const ops: string[] = ["BT", "/F1 11 Tf", "48 770 Td", "15 TL"];
  lines.forEach((rawLine, idx) => {
    const safe = pdfEscape(rawLine);
    if (idx === 0) {
      ops.push(`(${safe}) Tj`);
    } else {
      ops.push(`T* (${safe}) Tj`);
    }
  });
  ops.push("ET");
  const streamBody = ops.join("\n") + "\n";

  const encoder = new TextEncoder();
  const objects = [
    "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n",
    "2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n",
    "3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>\nendobj\n",
    "4 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Courier >>\nendobj\n",
    `5 0 obj\n<< /Length ${encoder.encode(streamBody).byteLength} >>\nstream\n${streamBody}endstream\nendobj\n`,
  ];

  let doc = "%PDF-1.4\n";
  const offsets: number[] = [0];
  for (const obj of objects) {
    offsets.push(encoder.encode(doc).byteLength);
    doc += obj;
  }
  const xrefOffset = encoder.encode(doc).byteLength;
  doc += `xref\n0 ${objects.length + 1}\n`;
  doc += "0000000000 65535 f \n";
  for (let i = 1; i <= objects.length; i += 1) {
    doc += `${String(offsets[i]).padStart(10, "0")} 00000 n \n`;
  }
  doc += `trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${xrefOffset}\n%%EOF\n`;
  return encoder.encode(doc);
}

function buildDemoPdfForDetail(detail: InvoiceDetail): Uint8Array {
  const inv = detail.invoice;
  if (inv.status === "PARSE_FAILED") {
    return buildMinimalDemoPdf([
      "RECONCILE ENGINE - STATIC DEMO MODE",
      "============================================================",
      "Fixture: 18_scanned_outlined.pdf (Simulated Scanned PDF)",
      `Invoice ID: ${inv.id}`,
      "Status:     PARSE_FAILED (ErrScannedPDFUnsupported)",
      "",
      "In the live Go worker, gopdf inspects page.OutlineHint()",
      "and quarantines image-only / outlined PDFs without an",
      "embedded text layer directly to PARSE_FAILED + DLQ.",
    ]);
  }

  const cur = inv.currency || "GBP";
  const rows: string[] = [
    `SUPPLIER INVOICE (${inv.source_format})`,
    "============================================================",
    `Invoice No:    ${inv.invoice_number ?? "-"}`,
    `PO Number:     ${inv.po_number_raw ?? "-"}`,
    `Supplier Code: ${inv.supplier_code_raw ?? "-"} (${inv.supplier_name ?? ""})`,
    `Issue Date:    ${(inv.issue_date ?? "2026-11-20").slice(0, 10)}   Currency: ${cur}`,
    "",
    "SKU             Qty   Unit       VAT%     Net        VAT",
    "------------------------------------------------------------",
  ];

  for (const l of detail.lines) {
    const sku = l.sku.padEnd(15, " ").slice(0, 15);
    const qty = String(l.quantity).padStart(4, " ");
    const unit = formatPenceDecimal(l.unit_price_pence).padStart(9, " ");
    const vatPct = `${formatPenceDecimal(l.vat_rate_bps)}%`.padStart(8, " ");
    const net = formatPenceDecimal(l.line_net_pence).padStart(10, " ");
    const vat = formatPenceDecimal(l.line_vat_pence).padStart(9, " ");
    rows.push(`${sku} ${qty}  ${unit}  ${vatPct}  ${net}  ${vat}`);
    rows.push(`  ${l.description}`);
  }

  rows.push("------------------------------------------------------------");
  rows.push(`Subtotal (${cur}): ${formatPenceDecimal(inv.subtotal_pence)}`);
  rows.push(`VAT Total (${cur}): ${formatPenceDecimal(inv.vat_total_pence)}`);
  rows.push(`Grand Total (${cur}): ${formatPenceDecimal(inv.grand_total_pence)}`);
  rows.push("");
  rows.push("Deterministic Static Demo Mode Preview (No External APIs)");
  return buildMinimalDemoPdf(rows);
}

interface FixtureSpec {
  idx: number;
  filename: string;
  format: SourceFormat;
  status: InvoiceStatus;
  supplier: (typeof SUPPLIERS)[keyof typeof SUPPLIERS];
  invoiceNumber?: string;
  poNumberRaw?: string;
  currency?: string;
  subtotalPence: number;
  vatTotalPence: number;
  grandTotalPence: number;
  parseError?: string;
  lines: InvoiceLine[];
  po?: PurchaseOrder | null;
  grn?: GoodsReceiptSummary | null;
  discrepancies?: Array<Omit<Discrepancy, "id" | "invoice_id">>;
  rawTextOverride?: string;
}

function buildRecordFromSpec(spec: FixtureSpec): DemoRecord {
  const invId = demoUUID("inv", spec.idx);
  const sha = deterministicSha256(spec.idx);
  const ext =
    spec.format === "UBL_XML" ? "xml" : spec.format === "PDF" ? "pdf" : "csv";
  const createdMinute = String(spec.idx).padStart(2, "0");
  const createdAt = `2026-11-24T09:${createdMinute}:00Z`;
  const parsedAt = `2026-11-24T09:${createdMinute}:02Z`;
  const matchedAt = `2026-11-24T09:${createdMinute}:04Z`;

  const discrepancies: Discrepancy[] = (spec.discrepancies ?? []).map(
    (d, dIdx) => ({
      ...d,
      id: demoUUID("disc", spec.idx, dIdx + 1),
      invoice_id: invId,
    }),
  );

  const auditEvents: AuditEvent[] = [
    {
      id: demoUUID("aud", spec.idx, 1),
      invoice_id: invId,
      actor: "system:api",
      from_status: null,
      to_status: "UPLOADED",
      reason: `Ingested ${spec.filename} (SHA-256 verified)`,
      created_at: createdAt,
    },
  ];

  if (spec.status === "PARSE_FAILED") {
    auditEvents.push({
      id: demoUUID("aud", spec.idx, 2),
      invoice_id: invId,
      actor: "system:parser",
      from_status: "UPLOADED",
      to_status: "PARSE_FAILED",
      reason: spec.parseError ?? "Deterministic parser error",
      created_at: parsedAt,
    });
  } else {
    auditEvents.push({
      id: demoUUID("aud", spec.idx, 2),
      invoice_id: invId,
      actor: "system:parser",
      from_status: "UPLOADED",
      to_status: "PARSED",
      reason: "Invoice parsed deterministically",
      created_at: parsedAt,
    });
    auditEvents.push({
      id: demoUUID("aud", spec.idx, 3),
      invoice_id: invId,
      actor: "system:matcher",
      from_status: "PARSED",
      to_status: spec.status,
      reason:
        discrepancies.length === 0
          ? "3-way PO/GRN/Invoice match passed with 0 discrepancies"
          : `Flagged ${discrepancies.length} blocking discrepancy (${discrepancies.map((d) => d.code).join(", ")})`,
      created_at: matchedAt,
    });
  }

  const invoice: Invoice = {
    id: invId,
    file_sha256: sha,
    storage_key: `raw-invoices/2026/11/${sha}.${ext}`,
    source_format: spec.format,
    status: spec.status,
    supplier_id: spec.status === "PARSE_FAILED" ? undefined : spec.supplier.id,
    supplier_code_raw:
      spec.status === "PARSE_FAILED" ? undefined : spec.supplier.code,
    supplier_name:
      spec.status === "PARSE_FAILED" ? undefined : spec.supplier.name,
    invoice_number: spec.invoiceNumber,
    po_number_raw: spec.poNumberRaw,
    purchase_order_id: spec.po?.id,
    issue_date: spec.status === "PARSE_FAILED" ? undefined : "2026-11-20T00:00:00Z",
    due_date: spec.status === "PARSE_FAILED" ? undefined : "2026-12-20T00:00:00Z",
    currency: spec.currency ?? "GBP",
    subtotal_pence: spec.subtotalPence,
    vat_total_pence: spec.vatTotalPence,
    grand_total_pence: spec.grandTotalPence,
    parse_error: spec.parseError,
    discrepancy_count: discrepancies.length,
    created_at: createdAt,
    updated_at: spec.status === "PARSE_FAILED" ? parsedAt : matchedAt,
  };

  const detail: InvoiceDetail = {
    invoice,
    lines: spec.lines,
    discrepancies,
    audit_events: auditEvents,
    purchase_order: spec.po ?? null,
    goods_receipts: spec.grn ?? null,
  };

  if (spec.format === "UBL_XML") {
    return {
      detail,
      rawText: spec.rawTextOverride ?? buildUBLXml(detail),
    };
  }
  if (spec.format === "CSV") {
    return {
      detail,
      rawText: spec.rawTextOverride ?? buildCSVManifest(detail),
    };
  }
  return {
    detail,
    rawPdfBytes: buildDemoPdfForDetail(detail),
  };
}

function buildInitialRecords(): DemoRecord[] {
  const po1001: PurchaseOrder = {
    id: demoUUID("po", 1001),
    po_number: "PO-2026-1001",
    supplier_id: SUPPLIERS.ACME.id,
    supplier_code: SUPPLIERS.ACME.code,
    currency: "GBP",
    status: "RECEIVED",
    total_net_pence: 25000,
    lines: [
      {
        id: demoUUID("line", 1001, 1),
        line_number: 1,
        sku: "SKU-BOLT-M12",
        description: "M12 Stainless Steel Hex Bolts",
        ordered_qty: 10,
        unit_price_pence: 2500,
        vat_rate_bps: 2000,
      },
    ],
  };

  const po1002: PurchaseOrder = {
    id: demoUUID("po", 1002),
    po_number: "PO-2026-1002",
    supplier_id: SUPPLIERS.THAMES.id,
    supplier_code: SUPPLIERS.THAMES.code,
    currency: "GBP",
    status: "RECEIVED",
    total_net_pence: 23000,
    lines: [
      {
        id: demoUUID("line", 1002, 1),
        line_number: 1,
        sku: "SKU-NUT-M12",
        description: "M12 Stainless Hex Nuts",
        ordered_qty: 20,
        unit_price_pence: 850,
        vat_rate_bps: 2000,
      },
      {
        id: demoUUID("line", 1002, 2),
        line_number: 2,
        sku: "SKU-WASH-M12",
        description: "M12 Spring Washers",
        ordered_qty: 15,
        unit_price_pence: 400,
        vat_rate_bps: 2000,
      },
    ],
  };

  const po1003: PurchaseOrder = {
    id: demoUUID("po", 1003),
    po_number: "PO-2026-1003",
    supplier_id: SUPPLIERS.NORTH.id,
    supplier_code: SUPPLIERS.NORTH.code,
    currency: "GBP",
    status: "RECEIVED",
    total_net_pence: 11200,
    lines: [
      {
        id: demoUUID("line", 1003, 1),
        line_number: 1,
        sku: "SKU-VALVE-20",
        description: "Brass Ball Valve 20mm",
        ordered_qty: 5,
        unit_price_pence: 1800,
        vat_rate_bps: 2000,
      },
      {
        id: demoUUID("line", 1003, 2),
        line_number: 2,
        sku: "SKU-SEAL-KIT",
        description: "EPDM Gasket Seal Kit",
        ordered_qty: 4,
        unit_price_pence: 550,
        vat_rate_bps: 2000,
      },
    ],
  };

  const po1004: PurchaseOrder = {
    id: demoUUID("po", 1004),
    po_number: "PO-2026-1004",
    supplier_id: SUPPLIERS.MIDLAND.id,
    supplier_code: SUPPLIERS.MIDLAND.code,
    currency: "GBP",
    status: "RECEIVED",
    total_net_pence: 78000,
    lines: [
      {
        id: demoUUID("line", 1004, 1),
        line_number: 1,
        sku: "SKU-CABLE-100",
        description: "3-Core Armoured Power Cable (10m)",
        ordered_qty: 10,
        unit_price_pence: 4200,
        vat_rate_bps: 2000,
      },
      {
        id: demoUUID("line", 1004, 2),
        line_number: 2,
        sku: "SKU-BREAKER-32A",
        description: "32A Type-B DIN Rail MCB",
        ordered_qty: 12,
        unit_price_pence: 3000,
        vat_rate_bps: 2000,
      },
    ],
  };

  const po1005: PurchaseOrder = {
    id: demoUUID("po", 1005),
    po_number: "PO-2026-1005",
    supplier_id: SUPPLIERS.CLYDE.id,
    supplier_code: SUPPLIERS.CLYDE.code,
    currency: "GBP",
    status: "RECEIVED",
    total_net_pence: 12500,
    lines: [
      {
        id: demoUUID("line", 1005, 1),
        line_number: 1,
        sku: "SKU-BEARING-6204",
        description: "6204-2RS Deep Groove Ball Bearing",
        ordered_qty: 10,
        unit_price_pence: 1250,
        vat_rate_bps: 2000,
      },
    ],
  };

  const po1006: PurchaseOrder = {
    id: demoUUID("po", 1006),
    po_number: "PO-2026-1006",
    supplier_id: SUPPLIERS.ACME.id,
    supplier_code: SUPPLIERS.ACME.code,
    currency: "GBP",
    status: "RECEIVED",
    total_net_pence: 30000,
    lines: [
      {
        id: demoUUID("line", 1006, 1),
        line_number: 1,
        sku: "SKU-FLANGE-50",
        description: "DN50 Forged Steel Weld Flange",
        ordered_qty: 12,
        unit_price_pence: 2500,
        vat_rate_bps: 2000,
      },
    ],
  };

  const po1007: PurchaseOrder = {
    id: demoUUID("po", 1007),
    po_number: "PO-2026-1007",
    supplier_id: SUPPLIERS.THAMES.id,
    supplier_code: SUPPLIERS.THAMES.code,
    currency: "GBP",
    status: "RECEIVED",
    total_net_pence: 42000,
    lines: [
      {
        id: demoUUID("line", 1007, 1),
        line_number: 1,
        sku: "SKU-ANCHOR-M16",
        description: "M16 Throughbolt Concrete Anchor",
        ordered_qty: 30,
        unit_price_pence: 1400,
        vat_rate_bps: 2000,
      },
    ],
  };

  const po1008: PurchaseOrder = {
    id: demoUUID("po", 1008),
    po_number: "PO-2026-1008",
    supplier_id: SUPPLIERS.NORTH.id,
    supplier_code: SUPPLIERS.NORTH.code,
    currency: "GBP",
    status: "RECEIVED",
    total_net_pence: 24000,
    lines: [
      {
        id: demoUUID("line", 1008, 1),
        line_number: 1,
        sku: "SKU-HOSE-HYD",
        description: "High-Pressure Hydraulic Hose 1.5m",
        ordered_qty: 8,
        unit_price_pence: 3000,
        vat_rate_bps: 2000,
      },
    ],
  };

  const po1009: PurchaseOrder = {
    id: demoUUID("po", 1009),
    po_number: "PO-2026-1009",
    supplier_id: SUPPLIERS.ACME.id,
    supplier_code: SUPPLIERS.ACME.code,
    currency: "GBP",
    status: "RECEIVED",
    total_net_pence: 16000,
    lines: [
      {
        id: demoUUID("line", 1009, 1),
        line_number: 1,
        sku: "SKU-CLAMP-40",
        description: "Heavy-Duty 40mm Pipe Clamp",
        ordered_qty: 16,
        unit_price_pence: 1000,
        vat_rate_bps: 2000,
      },
    ],
  };

  const po1010: PurchaseOrder = {
    id: demoUUID("po", 1010),
    po_number: "PO-2026-1010",
    supplier_id: SUPPLIERS.MIDLAND.id,
    supplier_code: SUPPLIERS.MIDLAND.code,
    currency: "GBP",
    status: "CLOSED",
    total_net_pence: 18000,
    lines: [
      {
        id: demoUUID("line", 1010, 1),
        line_number: 1,
        sku: "SKU-RELAY-24V",
        description: "24V DC Industrial Control Relay",
        ordered_qty: 9,
        unit_price_pence: 2000,
        vat_rate_bps: 2000,
      },
    ],
  };

  const po1011: PurchaseOrder = {
    id: demoUUID("po", 1011),
    po_number: "PO-2026-1011",
    supplier_id: SUPPLIERS.CLYDE.id,
    supplier_code: SUPPLIERS.CLYDE.code,
    currency: "GBP",
    status: "OPEN",
    total_net_pence: 32000,
    lines: [
      {
        id: demoUUID("line", 1011, 1),
        line_number: 1,
        sku: "SKU-SHAFT-30",
        description: "30mm Precision Marine Drive Shaft",
        ordered_qty: 4,
        unit_price_pence: 8000,
        vat_rate_bps: 2000,
      },
    ],
  };

  const po1012: PurchaseOrder = {
    id: demoUUID("po", 1012),
    po_number: "PO-2026-1012",
    supplier_id: SUPPLIERS.ACME.id,
    supplier_code: SUPPLIERS.ACME.code,
    currency: "GBP",
    status: "RECEIVED",
    total_net_pence: 15000,
    lines: [
      {
        id: demoUUID("line", 1012, 1),
        line_number: 1,
        sku: "SKU-FILTER-OIL",
        description: "Spin-On Hydraulic Oil Filter",
        ordered_qty: 6,
        unit_price_pence: 2500,
        vat_rate_bps: 2000,
      },
    ],
  };

  const po1013: PurchaseOrder = {
    id: demoUUID("po", 1013),
    po_number: "PO-2026-1013",
    supplier_id: SUPPLIERS.THAMES.id,
    supplier_code: SUPPLIERS.THAMES.code,
    currency: "GBP",
    status: "PARTIALLY_RECEIVED",
    total_net_pence: 20000,
    lines: [
      {
        id: demoUUID("line", 1013, 1),
        line_number: 1,
        sku: "SKU-STUD-M10",
        description: "M10 Threaded Studding 1m",
        ordered_qty: 20,
        unit_price_pence: 1000,
        vat_rate_bps: 2000,
      },
    ],
  };

  const po1014: PurchaseOrder = {
    id: demoUUID("po", 1014),
    po_number: "PO-2026-1014",
    supplier_id: SUPPLIERS.NORTH.id,
    supplier_code: SUPPLIERS.NORTH.code,
    currency: "GBP",
    status: "RECEIVED",
    total_net_pence: 18000,
    lines: [
      {
        id: demoUUID("line", 1014, 1),
        line_number: 1,
        sku: "SKU-GAUGE-10B",
        description: "Glycerine Pressure Gauge 0-10 Bar",
        ordered_qty: 12,
        unit_price_pence: 1500,
        vat_rate_bps: 2000,
      },
    ],
  };

  const po1015: PurchaseOrder = {
    id: demoUUID("po", 1015),
    po_number: "PO-2026-1015",
    supplier_id: SUPPLIERS.CLYDE.id,
    supplier_code: SUPPLIERS.CLYDE.code,
    currency: "GBP",
    status: "RECEIVED",
    total_net_pence: 25000,
    lines: [
      {
        id: demoUUID("line", 1015, 1),
        line_number: 1,
        sku: "SKU-PUMP-SEAL",
        description: "Mechanical Pump Shaft Seal 30mm",
        ordered_qty: 5,
        unit_price_pence: 5000,
        vat_rate_bps: 2000,
      },
    ],
  };

  const specs: FixtureSpec[] = [
    // 1-4: Clean AUTO_APPROVED across UBL_XML, PDF, CSV, Multi-line PDF
    {
      idx: 1,
      filename: "01_clean_ubl.xml",
      format: "UBL_XML",
      status: "AUTO_APPROVED",
      supplier: SUPPLIERS.ACME,
      invoiceNumber: "INV-2026-0001",
      poNumberRaw: "PO-2026-1001",
      subtotalPence: 25000,
      vatTotalPence: 5000,
      grandTotalPence: 30000,
      lines: [
        {
          line_number: 1,
          sku: "SKU-BOLT-M12",
          description: "M12 Stainless Steel Hex Bolts",
          quantity: 10,
          unit_price_pence: 2500,
          vat_rate_bps: 2000,
          line_net_pence: 25000,
          line_vat_pence: 5000,
        },
      ],
      po: po1001,
      grn: {
        purchase_order_id: po1001.id,
        receipt_count: 1,
        accepted_by_sku: { "SKU-BOLT-M12": 10 },
      },
    },
    {
      idx: 2,
      filename: "02_clean_pdf.pdf",
      format: "PDF",
      status: "AUTO_APPROVED",
      supplier: SUPPLIERS.THAMES,
      invoiceNumber: "INV-2026-0002",
      poNumberRaw: "PO-2026-1002",
      subtotalPence: 23000,
      vatTotalPence: 4600,
      grandTotalPence: 27600,
      lines: [
        {
          line_number: 1,
          sku: "SKU-NUT-M12",
          description: "M12 Stainless Hex Nuts",
          quantity: 20,
          unit_price_pence: 850,
          vat_rate_bps: 2000,
          line_net_pence: 17000,
          line_vat_pence: 3400,
        },
        {
          line_number: 2,
          sku: "SKU-WASH-M12",
          description: "M12 Spring Washers",
          quantity: 15,
          unit_price_pence: 400,
          vat_rate_bps: 2000,
          line_net_pence: 6000,
          line_vat_pence: 1200,
        },
      ],
      po: po1002,
      grn: {
        purchase_order_id: po1002.id,
        receipt_count: 1,
        accepted_by_sku: { "SKU-NUT-M12": 20, "SKU-WASH-M12": 15 },
      },
    },
    {
      idx: 3,
      filename: "03_clean_csv.csv",
      format: "CSV",
      status: "AUTO_APPROVED",
      supplier: SUPPLIERS.NORTH,
      invoiceNumber: "INV-2026-0003",
      poNumberRaw: "PO-2026-1003",
      subtotalPence: 11200,
      vatTotalPence: 2240,
      grandTotalPence: 13440,
      lines: [
        {
          line_number: 1,
          sku: "SKU-VALVE-20",
          description: "Brass Ball Valve 20mm",
          quantity: 5,
          unit_price_pence: 1800,
          vat_rate_bps: 2000,
          line_net_pence: 9000,
          line_vat_pence: 1800,
        },
        {
          line_number: 2,
          sku: "SKU-SEAL-KIT",
          description: "EPDM Gasket Seal Kit",
          quantity: 4,
          unit_price_pence: 550,
          vat_rate_bps: 2000,
          line_net_pence: 2200,
          line_vat_pence: 440,
        },
      ],
      po: po1003,
      grn: {
        purchase_order_id: po1003.id,
        receipt_count: 1,
        accepted_by_sku: { "SKU-VALVE-20": 5, "SKU-SEAL-KIT": 4 },
      },
    },
    {
      idx: 4,
      filename: "04_clean_multipage_pdf.pdf",
      format: "PDF",
      status: "AUTO_APPROVED",
      supplier: SUPPLIERS.MIDLAND,
      invoiceNumber: "INV-2026-0004",
      poNumberRaw: "PO-2026-1004",
      subtotalPence: 78000,
      vatTotalPence: 15600,
      grandTotalPence: 93600,
      lines: [
        {
          line_number: 1,
          sku: "SKU-CABLE-100",
          description: "3-Core Armoured Power Cable (10m)",
          quantity: 10,
          unit_price_pence: 4200,
          vat_rate_bps: 2000,
          line_net_pence: 42000,
          line_vat_pence: 8400,
        },
        {
          line_number: 2,
          sku: "SKU-BREAKER-32A",
          description: "32A Type-B DIN Rail MCB",
          quantity: 12,
          unit_price_pence: 3000,
          vat_rate_bps: 2000,
          line_net_pence: 36000,
          line_vat_pence: 7200,
        },
      ],
      po: po1004,
      grn: {
        purchase_order_id: po1004.id,
        receipt_count: 1,
        accepted_by_sku: { "SKU-CABLE-100": 10, "SKU-BREAKER-32A": 12 },
      },
    },

    // 5-8: Pass A Internal Integrity Rules (INT-01 through INT-04)
    {
      idx: 5,
      filename: "05_int01_line_math.csv",
      format: "CSV",
      status: "NEEDS_REVIEW",
      supplier: SUPPLIERS.CLYDE,
      invoiceNumber: "INV-2026-0005",
      poNumberRaw: "PO-2026-1005",
      subtotalPence: 13000,
      vatTotalPence: 2600,
      grandTotalPence: 15600,
      lines: [
        {
          line_number: 1,
          sku: "SKU-BEARING-6204",
          description: "6204-2RS Deep Groove Ball Bearing",
          quantity: 10,
          unit_price_pence: 1250,
          vat_rate_bps: 2000,
          line_net_pence: 13000,
          line_vat_pence: 2600,
        },
      ],
      po: po1005,
      grn: {
        purchase_order_id: po1005.id,
        receipt_count: 1,
        accepted_by_sku: { "SKU-BEARING-6204": 10 },
      },
      discrepancies: [
        {
          line_number: 1,
          sku: "SKU-BEARING-6204",
          code: "LINE_MATH_ERROR",
          severity: "BLOCKING",
          message:
            "line 1 net £130.00 does not match qty (10) * unit_price (£12.50) = £125.00",
          expected_value: "12500",
          actual_value: "13000",
          variance_pence: 500,
        },
      ],
    },
    {
      idx: 6,
      filename: "06_int02_vat_mismatch.xml",
      format: "UBL_XML",
      status: "NEEDS_REVIEW",
      supplier: SUPPLIERS.ACME,
      invoiceNumber: "INV-2026-0006",
      poNumberRaw: "PO-2026-1006",
      subtotalPence: 30000,
      vatTotalPence: 6300,
      grandTotalPence: 36300,
      lines: [
        {
          line_number: 1,
          sku: "SKU-FLANGE-50",
          description: "DN50 Forged Steel Weld Flange",
          quantity: 12,
          unit_price_pence: 2500,
          vat_rate_bps: 2000,
          line_net_pence: 30000,
          line_vat_pence: 6300,
        },
      ],
      po: po1006,
      grn: {
        purchase_order_id: po1006.id,
        receipt_count: 1,
        accepted_by_sku: { "SKU-FLANGE-50": 12 },
      },
      discrepancies: [
        {
          line_number: 1,
          sku: "SKU-FLANGE-50",
          code: "VAT_MISMATCH",
          severity: "BLOCKING",
          message:
            "line 1 VAT £63.00 deviates from calculated 20.00% VAT £60.00 by +£3.00",
          expected_value: "6000",
          actual_value: "6300",
          variance_pence: 300,
        },
      ],
    },
    {
      idx: 7,
      filename: "07_int03_total_mismatch.pdf",
      format: "PDF",
      status: "NEEDS_REVIEW",
      supplier: SUPPLIERS.THAMES,
      invoiceNumber: "INV-2026-0007",
      poNumberRaw: "PO-2026-1007",
      subtotalPence: 42000,
      vatTotalPence: 8400,
      grandTotalPence: 51400,
      lines: [
        {
          line_number: 1,
          sku: "SKU-ANCHOR-M16",
          description: "M16 Throughbolt Concrete Anchor",
          quantity: 30,
          unit_price_pence: 1400,
          vat_rate_bps: 2000,
          line_net_pence: 42000,
          line_vat_pence: 8400,
        },
      ],
      po: po1007,
      grn: {
        purchase_order_id: po1007.id,
        receipt_count: 1,
        accepted_by_sku: { "SKU-ANCHOR-M16": 30 },
      },
      discrepancies: [
        {
          code: "TOTAL_MISMATCH",
          severity: "BLOCKING",
          message:
            "invoice grand total £514.00 does not match subtotal (£420.00) + VAT (£84.00) = £504.00",
          expected_value: "50400",
          actual_value: "51400",
          variance_pence: 1000,
        },
      ],
    },
    {
      idx: 8,
      filename: "08_int04_currency_mismatch.xml",
      format: "UBL_XML",
      status: "NEEDS_REVIEW",
      supplier: SUPPLIERS.NORTH,
      invoiceNumber: "INV-2026-0008",
      poNumberRaw: "PO-2026-1008",
      currency: "EUR",
      subtotalPence: 24000,
      vatTotalPence: 4800,
      grandTotalPence: 28800,
      lines: [
        {
          line_number: 1,
          sku: "SKU-HOSE-HYD",
          description: "High-Pressure Hydraulic Hose 1.5m",
          quantity: 8,
          unit_price_pence: 3000,
          vat_rate_bps: 2000,
          line_net_pence: 24000,
          line_vat_pence: 4800,
        },
      ],
      po: po1008,
      grn: {
        purchase_order_id: po1008.id,
        receipt_count: 1,
        accepted_by_sku: { "SKU-HOSE-HYD": 8 },
      },
      discrepancies: [
        {
          code: "CURRENCY_MISMATCH",
          severity: "BLOCKING",
          message: "invoice currency EUR does not match PO-2026-1008 currency GBP",
          expected_value: "GBP",
          actual_value: "EUR",
          variance_pence: 0,
        },
      ],
    },

    // 9-17: Pass B 3-Way Matching Rules (3WAY-01 through 3WAY-09)
    {
      idx: 9,
      filename: "09_3way01_missing_po.xml",
      format: "UBL_XML",
      status: "NEEDS_REVIEW",
      supplier: SUPPLIERS.ACME,
      invoiceNumber: "INV-2026-0009",
      poNumberRaw: "PO-2026-9999",
      subtotalPence: 19000,
      vatTotalPence: 3800,
      grandTotalPence: 22800,
      lines: [
        {
          line_number: 1,
          sku: "SKU-BOLT-M12",
          description: "M12 Stainless Steel Hex Bolts",
          quantity: 10,
          unit_price_pence: 1900,
          vat_rate_bps: 2000,
          line_net_pence: 19000,
          line_vat_pence: 3800,
        },
      ],
      po: null,
      grn: null,
      discrepancies: [
        {
          code: "MISSING_PO",
          severity: "BLOCKING",
          message: "purchase order PO-2026-9999 does not exist in the ledger",
          expected_value: "VALID_PO",
          actual_value: "PO-2026-9999",
          variance_pence: 22800,
        },
      ],
    },
    {
      idx: 10,
      filename: "10_3way02_supplier_mismatch.pdf",
      format: "PDF",
      status: "NEEDS_REVIEW",
      supplier: SUPPLIERS.THAMES,
      invoiceNumber: "INV-2026-0010",
      poNumberRaw: "PO-2026-1009",
      subtotalPence: 16000,
      vatTotalPence: 3200,
      grandTotalPence: 19200,
      lines: [
        {
          line_number: 1,
          sku: "SKU-CLAMP-40",
          description: "Heavy-Duty 40mm Pipe Clamp",
          quantity: 16,
          unit_price_pence: 1000,
          vat_rate_bps: 2000,
          line_net_pence: 16000,
          line_vat_pence: 3200,
        },
      ],
      po: po1009,
      grn: {
        purchase_order_id: po1009.id,
        receipt_count: 1,
        accepted_by_sku: { "SKU-CLAMP-40": 16 },
      },
      discrepancies: [
        {
          code: "PO_SUPPLIER_MISMATCH",
          severity: "BLOCKING",
          message:
            "invoice supplier SUP-THAMES-02 does not match PO-2026-1009 supplier SUP-ACME-01",
          expected_value: "SUP-ACME-01",
          actual_value: "SUP-THAMES-02",
          variance_pence: 0,
        },
      ],
    },
    {
      idx: 11,
      filename: "11_3way03_po_closed.xml",
      format: "UBL_XML",
      status: "NEEDS_REVIEW",
      supplier: SUPPLIERS.MIDLAND,
      invoiceNumber: "INV-2026-0011",
      poNumberRaw: "PO-2026-1010",
      subtotalPence: 18000,
      vatTotalPence: 3600,
      grandTotalPence: 21600,
      lines: [
        {
          line_number: 1,
          sku: "SKU-RELAY-24V",
          description: "24V DC Industrial Control Relay",
          quantity: 9,
          unit_price_pence: 2000,
          vat_rate_bps: 2000,
          line_net_pence: 18000,
          line_vat_pence: 3600,
        },
      ],
      po: po1010,
      grn: {
        purchase_order_id: po1010.id,
        receipt_count: 1,
        accepted_by_sku: { "SKU-RELAY-24V": 9 },
      },
      discrepancies: [
        {
          code: "PO_CLOSED",
          severity: "BLOCKING",
          message: "purchase order PO-2026-1010 is CLOSED and cannot be billed",
          expected_value: "OPEN|PARTIALLY_RECEIVED|RECEIVED",
          actual_value: "CLOSED",
          variance_pence: 0,
        },
      ],
    },
    {
      idx: 12,
      filename: "12_3way04_missing_grn.csv",
      format: "CSV",
      status: "NEEDS_REVIEW",
      supplier: SUPPLIERS.CLYDE,
      invoiceNumber: "INV-2026-0012",
      poNumberRaw: "PO-2026-1011",
      subtotalPence: 32000,
      vatTotalPence: 6400,
      grandTotalPence: 38400,
      lines: [
        {
          line_number: 1,
          sku: "SKU-SHAFT-30",
          description: "30mm Precision Marine Drive Shaft",
          quantity: 4,
          unit_price_pence: 8000,
          vat_rate_bps: 2000,
          line_net_pence: 32000,
          line_vat_pence: 6400,
        },
      ],
      po: po1011,
      grn: {
        purchase_order_id: po1011.id,
        receipt_count: 0,
        accepted_by_sku: {},
      },
      discrepancies: [
        {
          code: "MISSING_GRN",
          severity: "BLOCKING",
          message: "zero goods received notes exist for PO-2026-1011",
          expected_value: ">=1 GRN",
          actual_value: "0 GRNs",
          variance_pence: 38400,
        },
      ],
    },
    {
      idx: 13,
      filename: "13_3way05_unmatched_sku.pdf",
      format: "PDF",
      status: "NEEDS_REVIEW",
      supplier: SUPPLIERS.ACME,
      invoiceNumber: "INV-2026-0013",
      poNumberRaw: "PO-2026-1012",
      subtotalPence: 20000,
      vatTotalPence: 4000,
      grandTotalPence: 24000,
      lines: [
        {
          line_number: 1,
          sku: "SKU-FILTER-OIL",
          description: "Spin-On Hydraulic Oil Filter",
          quantity: 6,
          unit_price_pence: 2500,
          vat_rate_bps: 2000,
          line_net_pence: 15000,
          line_vat_pence: 3000,
        },
        {
          line_number: 2,
          sku: "SKU-EXTRA-999",
          description: "Unordered Express Handling Surcharge",
          quantity: 1,
          unit_price_pence: 5000,
          vat_rate_bps: 2000,
          line_net_pence: 5000,
          line_vat_pence: 1000,
        },
      ],
      po: po1012,
      grn: {
        purchase_order_id: po1012.id,
        receipt_count: 1,
        accepted_by_sku: { "SKU-FILTER-OIL": 6 },
      },
      discrepancies: [
        {
          line_number: 2,
          sku: "SKU-EXTRA-999",
          code: "UNMATCHED_SKU",
          severity: "BLOCKING",
          message: "SKU SKU-EXTRA-999 does not exist on purchase order PO-2026-1012",
          expected_value: "PO_SKU",
          actual_value: "SKU-EXTRA-999",
          variance_pence: 5000,
        },
      ],
    },
    {
      idx: 14,
      filename: "14_3way06_qty_exceeds_grn.xml",
      format: "UBL_XML",
      status: "NEEDS_REVIEW",
      supplier: SUPPLIERS.THAMES,
      invoiceNumber: "INV-2026-0014",
      poNumberRaw: "PO-2026-1013",
      subtotalPence: 20000,
      vatTotalPence: 4000,
      grandTotalPence: 24000,
      lines: [
        {
          line_number: 1,
          sku: "SKU-STUD-M10",
          description: "M10 Threaded Studding 1m",
          quantity: 20,
          unit_price_pence: 1000,
          vat_rate_bps: 2000,
          line_net_pence: 20000,
          line_vat_pence: 4000,
        },
      ],
      po: po1013,
      grn: {
        purchase_order_id: po1013.id,
        receipt_count: 1,
        accepted_by_sku: { "SKU-STUD-M10": 14 },
      },
      discrepancies: [
        {
          line_number: 1,
          sku: "SKU-STUD-M10",
          code: "QTY_EXCEEDS_GRN",
          severity: "BLOCKING",
          message:
            "invoiced quantity 20 for SKU-STUD-M10 exceeds GRN accepted quantity 14 (+6 units)",
          expected_value: "14",
          actual_value: "20",
          variance_pence: 6000,
        },
      ],
    },
    {
      idx: 15,
      filename: "15_3way07_qty_exceeds_po.csv",
      format: "CSV",
      status: "NEEDS_REVIEW",
      supplier: SUPPLIERS.NORTH,
      invoiceNumber: "INV-2026-0015",
      poNumberRaw: "PO-2026-1014",
      subtotalPence: 22500,
      vatTotalPence: 4500,
      grandTotalPence: 27000,
      lines: [
        {
          line_number: 1,
          sku: "SKU-GAUGE-10B",
          description: "Glycerine Pressure Gauge 0-10 Bar",
          quantity: 15,
          unit_price_pence: 1500,
          vat_rate_bps: 2000,
          line_net_pence: 22500,
          line_vat_pence: 4500,
        },
      ],
      po: po1014,
      grn: {
        purchase_order_id: po1014.id,
        receipt_count: 1,
        accepted_by_sku: { "SKU-GAUGE-10B": 15 },
      },
      discrepancies: [
        {
          line_number: 1,
          sku: "SKU-GAUGE-10B",
          code: "QTY_EXCEEDS_PO",
          severity: "BLOCKING",
          message:
            "invoiced quantity 15 for SKU-GAUGE-10B exceeds PO ordered quantity 12 (+3 units)",
          expected_value: "12",
          actual_value: "15",
          variance_pence: 4500,
        },
      ],
    },
    {
      idx: 16,
      filename: "16_3way08_price_variance.pdf",
      format: "PDF",
      status: "NEEDS_REVIEW",
      supplier: SUPPLIERS.CLYDE,
      invoiceNumber: "INV-2026-0016",
      poNumberRaw: "PO-2026-1015",
      subtotalPence: 27000,
      vatTotalPence: 5400,
      grandTotalPence: 32400,
      lines: [
        {
          line_number: 1,
          sku: "SKU-PUMP-SEAL",
          description: "Mechanical Pump Shaft Seal 30mm",
          quantity: 5,
          unit_price_pence: 5400,
          vat_rate_bps: 2000,
          line_net_pence: 27000,
          line_vat_pence: 5400,
        },
      ],
      po: po1015,
      grn: {
        purchase_order_id: po1015.id,
        receipt_count: 1,
        accepted_by_sku: { "SKU-PUMP-SEAL": 5 },
      },
      discrepancies: [
        {
          line_number: 1,
          sku: "SKU-PUMP-SEAL",
          code: "PRICE_VARIANCE",
          severity: "BLOCKING",
          message:
            "unit price £54.00 exceeds tolerance (£0.50 and 1.00%) against PO unit price £50.00 (+£4.00 / +8.00%)",
          expected_value: "5000",
          actual_value: "5400",
          variance_pence: 2000,
        },
      ],
    },
    {
      idx: 17,
      filename: "17_3way09_vat_rate_mismatch.csv",
      format: "CSV",
      status: "NEEDS_REVIEW",
      supplier: SUPPLIERS.ACME,
      invoiceNumber: "INV-2026-0017",
      poNumberRaw: "PO-2026-1001",
      subtotalPence: 25000,
      vatTotalPence: 1250,
      grandTotalPence: 26250,
      lines: [
        {
          line_number: 1,
          sku: "SKU-BOLT-M12",
          description: "M12 Stainless Steel Hex Bolts",
          quantity: 10,
          unit_price_pence: 2500,
          vat_rate_bps: 500,
          line_net_pence: 25000,
          line_vat_pence: 1250,
        },
      ],
      po: po1001,
      grn: {
        purchase_order_id: po1001.id,
        receipt_count: 1,
        accepted_by_sku: { "SKU-BOLT-M12": 10 },
      },
      discrepancies: [
        {
          line_number: 1,
          sku: "SKU-BOLT-M12",
          code: "VAT_RATE_MISMATCH",
          severity: "BLOCKING",
          message:
            "invoice line VAT rate 5.00% (500 bps) does not match PO-2026-1001 VAT rate 20.00% (2000 bps)",
          expected_value: "2000",
          actual_value: "500",
          variance_pence: -3750,
        },
      ],
    },

    // 18-20: PARSE_FAILED quarantine fixtures (routed to reconcile.dlx)
    {
      idx: 18,
      filename: "18_scanned_outlined.pdf",
      format: "PDF",
      status: "PARSE_FAILED",
      supplier: SUPPLIERS.ACME,
      subtotalPence: 0,
      vatTotalPence: 0,
      grandTotalPence: 0,
      parseError:
        "scanned or image-only PDF without embedded text layer is unsupported (ErrScannedPDFUnsupported)",
      lines: [],
    },
    {
      idx: 19,
      filename: "19_corrupt_ubl.xml",
      format: "UBL_XML",
      status: "PARSE_FAILED",
      supplier: SUPPLIERS.ACME,
      subtotalPence: 0,
      vatTotalPence: 0,
      grandTotalPence: 0,
      parseError: "XML syntax error on line 4: unexpected EOF (corrupt UBL payload)",
      lines: [],
      rawTextOverride: `<?xml version="1.0" encoding="UTF-8"?>
<Invoice xmlns="urn:oasis:names:specification:ubl:schema:xsd:Invoice-2">
  <cbc:ID>INV-CORRUPT-19</cbc:ID>
  <cac:LegalMonetaryTotal><cbc:PayableAmount>250.00`,
    },
    {
      idx: 20,
      filename: "20_corrupt_csv.csv",
      format: "CSV",
      status: "PARSE_FAILED",
      supplier: SUPPLIERS.ACME,
      subtotalPence: 0,
      vatTotalPence: 0,
      grandTotalPence: 0,
      parseError:
        "CSV header schema mismatch: missing required columns [po_number, vat_rate_bps, line_net_pence, line_vat_pence]",
      lines: [],
      rawTextOverride: `invoice_number,supplier,sku,amount\r\nINV-BAD-20,SUP-ACME-01,SKU-BOLT-M12,150.00\r\n`,
    },
  ];

  return specs.map(buildRecordFromSpec);
}

let demoRecords: DemoRecord[] = buildInitialRecords();

function cloneDetail(detail: InvoiceDetail): InvoiceDetail {
  return structuredClone(detail);
}

export function resetDemoStore(): void {
  demoRecords = buildInitialRecords();
}

export function demoFetchHealth(): HealthResponse {
  return {
    status: "ok",
    dependencies: {
      mode: "static-demo (NEXT_PUBLIC_DEMO_MODE=true)",
      postgres: "simulated-in-memory",
      rabbitmq: "simulated-in-memory",
      minio: "simulated-in-memory",
    },
    timestamp: new Date().toISOString(),
  };
}

export function demoFetchMetrics(): MetricsSummary {
  const countsByStatus: Partial<Record<InvoiceStatus, number>> = {};
  let totalBilled = 0n;
  let approvedPence = 0n;
  let needsReviewPence = 0n;
  let totalVariance = 0n;

  let autoApprovedCount = 0;
  let needsReviewCount = 0;
  let manuallyApprovedCount = 0;
  let rejectedCount = 0;
  let parseFailedCount = 0;

  const byCode = new Map<string, { count: number; variance: bigint }>();

  for (const rec of demoRecords) {
    const inv = rec.detail.invoice;
    countsByStatus[inv.status] = (countsByStatus[inv.status] ?? 0) + 1;

    const grand = toSafeBigInt(inv.grand_total_pence);
    totalBilled += grand;

    switch (inv.status) {
      case "AUTO_APPROVED":
        autoApprovedCount += 1;
        approvedPence += grand;
        break;
      case "MANUALLY_APPROVED":
        manuallyApprovedCount += 1;
        approvedPence += grand;
        break;
      case "NEEDS_REVIEW":
        needsReviewCount += 1;
        needsReviewPence += grand;
        break;
      case "REJECTED":
        rejectedCount += 1;
        break;
      case "PARSE_FAILED":
        parseFailedCount += 1;
        break;
    }

    for (const disc of rec.detail.discrepancies) {
      const v = toSafeBigInt(disc.variance_pence);
      const absV = v < 0n ? -v : v;
      totalVariance += absV;
      const bucket = byCode.get(disc.code) ?? { count: 0, variance: 0n };
      bucket.count += 1;
      bucket.variance += absV;
      byCode.set(disc.code, bucket);
    }
  }

  const reconciledCount = BigInt(
    autoApprovedCount + needsReviewCount + manuallyApprovedCount + rejectedCount,
  );
  // Integer basis points (10000 = 100.00%) using pure BigInt half-up rounding
  const autoApprovalRateBps =
    reconciledCount > 0n
      ? Number(
          (BigInt(autoApprovedCount) * 10000n + reconciledCount / 2n) /
            reconciledCount,
        )
      : 0;

  const discrepanciesByCode: DiscrepancyCodeMetric[] = [...byCode.entries()]
    .sort((a, b) => {
      if (b[1].count !== a[1].count) {
        return b[1].count - a[1].count;
      }
      return a[0].localeCompare(b[0]);
    })
    .map(([code, bucket]) => ({
      code,
      count: bucket.count,
      total_variance_pence: Number(bucket.variance),
    }));

  return {
    total_invoices: demoRecords.length,
    counts_by_status: countsByStatus,
    total_billed_pence: Number(totalBilled),
    approved_pence: Number(approvedPence),
    needs_review_pence: Number(needsReviewPence),
    total_variance_pence: Number(totalVariance),
    discrepancies_by_code: discrepanciesByCode,
    auto_approved_count: autoApprovedCount,
    needs_review_count: needsReviewCount,
    manually_approved_count: manuallyApprovedCount,
    rejected_count: rejectedCount,
    parse_failed_count: parseFailedCount,
    auto_approval_rate_bps: autoApprovalRateBps,
  };
}

export function demoFetchInvoices(
  params: DemoListInvoicesParams = {},
): InvoiceListResponse {
  const status = params.status?.trim() || "";
  const format = params.source_format?.trim() || "";
  const supplierCode = params.supplier_code?.trim().toLowerCase() || "";
  const query = params.q?.trim().toLowerCase() || "";
  const limit =
    params.limit && params.limit > 0 && params.limit <= 200 ? params.limit : 50;
  const offset = params.offset && params.offset > 0 ? params.offset : 0;

  const filtered = demoRecords
    .map((r) => r.detail.invoice)
    .filter((inv) => {
      if (status && inv.status !== status) {
        return false;
      }
      if (format && inv.source_format !== format) {
        return false;
      }
      if (
        supplierCode &&
        !(inv.supplier_code_raw ?? "").toLowerCase().includes(supplierCode)
      ) {
        return false;
      }
      if (query) {
        const hay = [
          inv.invoice_number ?? "",
          inv.po_number_raw ?? "",
          inv.supplier_code_raw ?? "",
          inv.supplier_name ?? "",
          inv.id,
        ]
          .join(" ")
          .toLowerCase();
        if (!hay.includes(query)) {
          return false;
        }
      }
      return true;
    })
    .toSorted((a, b) => b.created_at.localeCompare(a.created_at));

  const paged = filtered.slice(offset, offset + limit).map((inv) => structuredClone(inv));

  return {
    invoices: paged,
    total: filtered.length,
    limit,
    offset,
  };
}

function findRecord(id: string): DemoRecord {
  const rec = demoRecords.find((r) => r.detail.invoice.id === id);
  if (!rec) {
    throw new DemoStoreError(404, "invoice not found");
  }
  return rec;
}

export function demoFetchInvoiceDetail(id: string): InvoiceDetail {
  return cloneDetail(findRecord(id).detail);
}

export function demoResolveInvoice(
  id: string,
  payload: ResolveInvoiceRequest,
): Invoice {
  const rec = findRecord(id);
  const inv = rec.detail.invoice;

  if (
    payload.status !== "MANUALLY_APPROVED" &&
    payload.status !== "REJECTED"
  ) {
    throw new DemoStoreError(
      400,
      "status must be MANUALLY_APPROVED or REJECTED",
    );
  }
  const actor = payload.actor.trim();
  if (!actor) {
    throw new DemoStoreError(400, "actor is required");
  }
  if (inv.status !== "NEEDS_REVIEW") {
    throw new DemoStoreError(
      409,
      `invalid state transition: ${inv.status} -> ${payload.status}`,
    );
  }

  const now = new Date().toISOString();
  const fromStatus = inv.status;
  inv.status = payload.status;
  inv.updated_at = now;

  rec.detail.audit_events.push({
    id: `demo-aud-${Date.now()}-${rec.detail.audit_events.length + 1}`,
    invoice_id: inv.id,
    actor,
    from_status: fromStatus,
    to_status: payload.status,
    reason: payload.reason.trim(),
    metadata: { demo_mode: true },
    created_at: now,
  });

  return structuredClone(inv);
}

export function demoFetchInvoiceFileText(id: string): {
  text: string;
  contentType: string;
  sizeBytes: number;
} {
  const rec = findRecord(id);
  const fmt = rec.detail.invoice.source_format;
  const text = rec.rawText ?? "";
  const contentType =
    fmt === "UBL_XML"
      ? "application/xml"
      : fmt === "CSV"
        ? "text/csv"
        : "application/pdf";
  const sizeBytes = new TextEncoder().encode(text).byteLength;
  return { text, contentType, sizeBytes };
}

export function demoGetInvoiceFileBlob(id: string): {
  bytes: Uint8Array;
  contentType: string;
  filename: string;
} {
  const rec = findRecord(id);
  const inv = rec.detail.invoice;
  const baseName = inv.invoice_number || inv.id.slice(0, 8);

  if (inv.source_format === "PDF") {
    return {
      bytes: rec.rawPdfBytes ?? buildDemoPdfForDetail(rec.detail),
      contentType: "application/pdf",
      filename: `${baseName}.pdf`,
    };
  }
  const text = rec.rawText ?? "";
  const ext = inv.source_format === "UBL_XML" ? "xml" : "csv";
  const contentType =
    inv.source_format === "UBL_XML" ? "application/xml" : "text/csv";
  return {
    bytes: new TextEncoder().encode(text),
    contentType,
    filename: `${baseName}.${ext}`,
  };
}

function detectFormatFromNameOrBytes(
  filename: string,
  bytes: Uint8Array,
): SourceFormat | null {
  const header = new TextDecoder("utf-8", { fatal: false })
    .decode(bytes.slice(0, 16))
    .trimStart();
  if (header.startsWith("%PDF-")) {
    return "PDF";
  }
  if (header.startsWith("<")) {
    return "UBL_XML";
  }
  const lower = filename.toLowerCase();
  if (lower.endsWith(".pdf")) {
    return "PDF";
  }
  if (lower.endsWith(".xml")) {
    return "UBL_XML";
  }
  if (lower.endsWith(".csv")) {
    return "CSV";
  }
  return null;
}

async function sha256Hex(bytes: Uint8Array): Promise<string> {
  if (typeof globalThis.crypto?.subtle?.digest === "function") {
    const copy = new Uint8Array(bytes.byteLength);
    copy.set(bytes);
    const digest = await globalThis.crypto.subtle.digest("SHA-256", copy.buffer);
    return Array.from(new Uint8Array(digest))
      .map((b) => b.toString(16).padStart(2, "0"))
      .join("");
  }
  // Deterministic fallback hash when Web Crypto is unavailable in insecure contexts.
  let h1 = 0x811c9dc5n;
  let h2 = 0x01000193n;
  for (const b of bytes) {
    h1 = ((h1 ^ BigInt(b)) * 0x01000193n) & 0xffffffffffffffffn;
    h2 = ((h2 + BigInt(b) * 31n) ^ (h1 >> 7n)) & 0xffffffffffffffffn;
  }
  return (
    h1.toString(16).padStart(16, "0") +
    h2.toString(16).padStart(16, "0")
  ).repeat(2);
}

export async function demoUploadInvoice(
  file: File,
): Promise<UploadInvoiceResult> {
  const arrayBuffer = await file.arrayBuffer();
  const bytes = new Uint8Array(arrayBuffer);
  if (bytes.byteLength === 0) {
    throw new DemoStoreError(400, "uploaded invoice file is empty");
  }

  const format = detectFormatFromNameOrBytes(file.name, bytes);
  if (!format) {
    throw new DemoStoreError(
      400,
      "unsupported invoice file format (expected UBL_XML, PDF, or CSV)",
    );
  }

  const hash = await sha256Hex(bytes);
  const existing = demoRecords.find(
    (r) => r.detail.invoice.file_sha256 === hash,
  );
  if (existing) {
    const inv = existing.detail.invoice;
    return {
      httpStatus: 200,
      data: {
        invoice_id: inv.id,
        status: inv.status,
        file_sha256: inv.file_sha256,
        storage_key: inv.storage_key,
        source_format: inv.source_format,
        duplicate: true,
      },
    };
  }

  const nextIdx = demoRecords.length + 1;
  const invId = demoUUID("inv", nextIdx);
  const ext = format === "UBL_XML" ? "xml" : format === "PDF" ? "pdf" : "csv";
  const storageKey = `raw-invoices/2026/11/${hash}.${ext}`;
  const now = new Date().toISOString();

  const sampleLine: InvoiceLine = {
    line_number: 1,
    sku: "SKU-DEMO-UPLOAD",
    description: `Uploaded fixture (${file.name})`,
    quantity: 1,
    unit_price_pence: 15000,
    vat_rate_bps: 2000,
    line_net_pence: 15000,
    line_vat_pence: 3000,
  };

  const subtotal = Number(sumPence([sampleLine.line_net_pence]));
  const vat = Number(sumPence([sampleLine.line_vat_pence]));
  const grand = Number(sumPence([subtotal, vat]));

  const inv: Invoice = {
    id: invId,
    file_sha256: hash,
    storage_key: storageKey,
    source_format: format,
    status: "NEEDS_REVIEW",
    supplier_id: SUPPLIERS.ACME.id,
    supplier_code_raw: SUPPLIERS.ACME.code,
    supplier_name: SUPPLIERS.ACME.name,
    invoice_number: `INV-DEMO-${String(nextIdx).padStart(4, "0")}`,
    po_number_raw: "PO-2026-DEMO",
    issue_date: now,
    currency: "GBP",
    subtotal_pence: subtotal,
    vat_total_pence: vat,
    grand_total_pence: grand,
    discrepancy_count: 1,
    created_at: now,
    updated_at: now,
  };

  const detail: InvoiceDetail = {
    invoice: inv,
    lines: [sampleLine],
    discrepancies: [
      {
        id: demoUUID("disc", nextIdx, 1),
        invoice_id: invId,
        code: "MISSING_PO",
        severity: "BLOCKING",
        message:
          "Demo Mode upload simulated: PO-2026-DEMO does not exist in the static ledger",
        expected_value: "VALID_PO",
        actual_value: "PO-2026-DEMO",
        variance_pence: grand,
      },
    ],
    audit_events: [
      {
        id: demoUUID("aud", nextIdx, 1),
        invoice_id: invId,
        actor: "system:api",
        from_status: null,
        to_status: "UPLOADED",
        reason: `Uploaded ${file.name} in Static Demo Mode`,
        created_at: now,
      },
      {
        id: demoUUID("aud", nextIdx, 2),
        invoice_id: invId,
        actor: "system:matcher",
        from_status: "PARSED",
        to_status: "NEEDS_REVIEW",
        reason: "Simulated deterministic match in Static Demo Mode (MISSING_PO)",
        created_at: now,
      },
    ],
    purchase_order: null,
    goods_receipts: null,
  };

  const rec: DemoRecord =
    format === "PDF"
      ? { detail, rawPdfBytes: bytes }
      : { detail, rawText: new TextDecoder("utf-8", { fatal: false }).decode(bytes) };

  demoRecords.unshift(rec);

  return {
    httpStatus: 202,
    data: {
      invoice_id: inv.id,
      status: inv.status,
      file_sha256: inv.file_sha256,
      storage_key: inv.storage_key,
      source_format: inv.source_format,
      duplicate: false,
    },
  };
}
