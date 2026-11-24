import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { parseCsvForPreview } from "../csv-preview.ts";
import { getDiscrepancyMeta, getStatusMeta } from "../discrepancies.ts";
import {
  buildAccountingExportBundle,
  serializeAccountingCSV,
  serializeAccountingJSON,
} from "../export.ts";
import {
  calculateLineNetPence,
  calculateLineVATPence,
  formatBPS,
  formatPenceCurrency,
  formatPenceDecimal,
  formatPenceToGBP,
  isPriceOutsideTolerance,
} from "../money.ts";
import type { DiscrepancyCode, InvoiceDetail, InvoiceStatus } from "../types.ts";

describe("integer money & bps helpers", () => {
  it("formats integer pence to GBP without floating-point arithmetic", () => {
    assert.equal(formatPenceToGBP(0), "£0.00");
    assert.equal(formatPenceToGBP(5), "£0.05");
    assert.equal(formatPenceToGBP(25050), "£250.50");
    assert.equal(formatPenceToGBP(1234567), "£12,345.67");
    assert.equal(formatPenceToGBP(-300), "-£3.00");
    assert.equal(formatPenceToGBP(300, { showSign: true }), "+£3.00");
    assert.equal(formatPenceToGBP(-300, { showSign: true }), "-£3.00");
    assert.equal(formatPenceToGBP(0, { showSign: true }), "£0.00");
  });

  it("rejects non-integer currency amounts", () => {
    assert.throws(() => formatPenceToGBP(12.34), RangeError);
    assert.throws(() => formatBPS(20.5), RangeError);
  });

  it("formats multi-currency minor units and plain decimals", () => {
    assert.equal(formatPenceCurrency(1500, "GBP"), "£15.00");
    assert.equal(formatPenceCurrency(1500, "EUR"), "€15.00");
    assert.equal(formatPenceCurrency(1500, "USD"), "$15.00");
    assert.equal(formatPenceCurrency(1500, "CHF"), "CHF 15.00");
    assert.equal(formatPenceDecimal(1234567), "12345.67");
    assert.equal(formatPenceDecimal(-50), "-0.50");
  });

  it("formats basis points deterministically", () => {
    assert.equal(formatBPS(0), "0.00%");
    assert.equal(formatBPS(500), "5.00%");
    assert.equal(formatBPS(2000), "20.00%");
    assert.equal(formatBPS(2353), "23.53%");
  });

  it("matches Go half-up VAT rounding and 3WAY-08 price tolerance", () => {
    assert.equal(calculateLineNetPence(10, 2500), 25000n);
    assert.equal(calculateLineVATPence(25000, 2000), 5000n);
    // 15p * 5% = 0.75p -> rounds half-up to 1p
    assert.equal(calculateLineVATPence(15, 500), 1n);
    // 10p * 5% = 0.50p -> rounds half-up to 1p
    assert.equal(calculateLineVATPence(10, 500), 1n);

    // Within 50p absolute tolerance -> false
    assert.equal(isPriceOutsideTolerance(2040, 2000), false);
    // > 50p but <= 1% (e.g. 60p on 10,000p = 0.60%) -> false
    assert.equal(isPriceOutsideTolerance(10060, 10000), false);
    // > 50p AND > 1% (e.g. +400p / +8% on 5,000p) -> true
    assert.equal(isPriceOutsideTolerance(5400, 5000), true);
  });
});

describe("discrepancy & status catalogs", () => {
  it("maps all 13 backend discrepancy codes to INT-01..04 and 3WAY-01..09", () => {
    const expected: Record<DiscrepancyCode, string> = {
      LINE_MATH_ERROR: "INT-01",
      VAT_MISMATCH: "INT-02",
      TOTAL_MISMATCH: "INT-03",
      CURRENCY_MISMATCH: "INT-04",
      MISSING_PO: "3WAY-01",
      PO_SUPPLIER_MISMATCH: "3WAY-02",
      PO_CLOSED: "3WAY-03",
      MISSING_GRN: "3WAY-04",
      UNMATCHED_SKU: "3WAY-05",
      QTY_EXCEEDS_GRN: "3WAY-06",
      QTY_EXCEEDS_PO: "3WAY-07",
      PRICE_VARIANCE: "3WAY-08",
      VAT_RATE_MISMATCH: "3WAY-09",
    };

    for (const [code, ruleId] of Object.entries(expected)) {
      const meta = getDiscrepancyMeta(code);
      assert.equal(meta.ruleId, ruleId);
      assert.equal(meta.code, code);
    }
  });

  it("covers all 7 backend invoice lifecycle states", () => {
    const statuses: InvoiceStatus[] = [
      "UPLOADED",
      "PARSED",
      "PARSE_FAILED",
      "AUTO_APPROVED",
      "NEEDS_REVIEW",
      "MANUALLY_APPROVED",
      "REJECTED",
    ];
    for (const st of statuses) {
      assert.equal(getStatusMeta(st).status, st);
    }
  });
});

describe("CSV preview parser & deterministic accounting export", () => {
  it("parses RFC-4180 CSV with quoted commas", () => {
    const raw =
      'sku,description,qty\r\n"SKU-1","Bolts, M12 ""Hex""",10\r\n';
    const parsed = parseCsvForPreview(raw);
    assert.equal(parsed.error, undefined);
    assert.deepEqual(parsed.headers, ["sku", "description", "qty"]);
    assert.deepEqual(parsed.rows, [["SKU-1", 'Bolts, M12 "Hex"', "10"]]);
  });

  it("exports deterministic CSV and JSON preserving integer pence", () => {
    const detail: InvoiceDetail = {
      invoice: {
        id: "11111111-1111-1111-1111-111111111111",
        file_sha256: "a".repeat(64),
        storage_key: "raw-invoices/2026/11/sample.xml",
        source_format: "UBL_XML",
        status: "MANUALLY_APPROVED",
        supplier_code_raw: "SUP-CLYDE-05",
        supplier_name: "Clyde Marine Bearings Co",
        invoice_number: "INV-2026-0016",
        po_number_raw: "PO-2026-1015",
        issue_date: "2026-11-20T00:00:00Z",
        currency: "GBP",
        subtotal_pence: 27000,
        vat_total_pence: 5400,
        grand_total_pence: 32400,
        created_at: "2026-11-24T09:00:00Z",
        updated_at: "2026-11-24T09:05:00Z",
      },
      lines: [
        {
          line_number: 1,
          sku: "SKU-PUMP-SEAL",
          description: 'Mechanical Pump "Shaft" Seal, 30mm',
          quantity: 5,
          unit_price_pence: 5400,
          vat_rate_bps: 2000,
          line_net_pence: 27000,
          line_vat_pence: 5400,
        },
      ],
      discrepancies: [
        {
          id: "22222222-2222-2222-2222-222222222222",
          invoice_id: "11111111-1111-1111-1111-111111111111",
          line_number: 1,
          sku: "SKU-PUMP-SEAL",
          code: "PRICE_VARIANCE",
          severity: "BLOCKING",
          message: "unit price £54.00 exceeds tolerance against PO price £50.00",
          expected_value: "50.00",
          actual_value: "54.00",
          variance_pence: 2000,
        },
      ],
      audit_events: [
        {
          id: "33333333-3333-3333-3333-333333333333",
          invoice_id: "11111111-1111-1111-1111-111111111111",
          actor: "ops@acme.co.uk",
          from_status: "NEEDS_REVIEW",
          to_status: "MANUALLY_APPROVED",
          reason: "Approved with commercial sign-off",
          created_at: "2026-11-24T09:05:00Z",
        },
      ],
      purchase_order: {
        id: "44444444-4444-4444-4444-444444444444",
        po_number: "PO-2026-1015",
        supplier_id: "55555555-5555-5555-5555-555555555555",
        supplier_code: "SUP-CLYDE-05",
        currency: "GBP",
        status: "RECEIVED",
        total_net_pence: 25000,
        lines: [
          {
            line_number: 1,
            sku: "SKU-PUMP-SEAL",
            description: "Mechanical Pump Shaft Seal",
            ordered_qty: 5,
            unit_price_pence: 5000,
            vat_rate_bps: 2000,
          },
        ],
      },
      goods_receipts: {
        purchase_order_id: "44444444-4444-4444-4444-444444444444",
        receipt_count: 1,
        accepted_by_sku: { "SKU-PUMP-SEAL": 5 },
      },
    };

    const bundle = buildAccountingExportBundle([detail], "APPROVED_ONLY");
    assert.equal(bundle.invoice_count, 1);
    assert.equal(bundle.line_count, 1);
    assert.equal(bundle.totals_by_currency[0].grand_total_pence, 32400);
    assert.equal(bundle.totals_by_currency[0].grand_total_decimal, "324.00");
    assert.equal(bundle.invoices[0].lines[0].vat_rate_pct, "20.00%");
    assert.equal(bundle.invoices[0].discrepancies[0].rule_id, "3WAY-08");

    const json1 = serializeAccountingJSON([detail], "APPROVED_ONLY");
    const json2 = serializeAccountingJSON([detail], "APPROVED_ONLY");
    assert.equal(json1, json2);

    const csv = serializeAccountingCSV([detail]);
    assert.match(csv, /INV-2026-0016,MANUALLY_APPROVED/);
    assert.match(csv, /"Mechanical Pump ""Shaft"" Seal, 30mm"/);
    assert.match(csv, /,32400,324\.00,/);
    assert.match(csv, /,3WAY-08,PRICE_VARIANCE,2000,/);
  });
});
