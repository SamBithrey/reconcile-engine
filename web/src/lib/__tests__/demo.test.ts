import assert from "node:assert/strict";
import { afterEach, describe, it } from "node:test";

import {
  ApiError,
  fetchHealth,
  fetchInvoiceDetail,
  fetchInvoiceFileText,
  fetchInvoices,
  fetchMetrics,
  getDemoInvoiceFileBlob,
  isDemoMode,
  resetDemoStore,
  resolveInvoice,
  uploadInvoice,
} from "../api.ts";
import { getDiscrepancyMeta } from "../discrepancies.ts";
import type { DiscrepancyCode } from "../types.ts";

const ORIGINAL_DEMO_ENV = process.env.NEXT_PUBLIC_DEMO_MODE;

afterEach(() => {
  if (ORIGINAL_DEMO_ENV === undefined) {
    delete process.env.NEXT_PUBLIC_DEMO_MODE;
  } else {
    process.env.NEXT_PUBLIC_DEMO_MODE = ORIGINAL_DEMO_ENV;
  }
  resetDemoStore();
});

describe("opt-in static demo mode flag", () => {
  it("defaults to live API mode when NEXT_PUBLIC_DEMO_MODE is unset or false", () => {
    delete process.env.NEXT_PUBLIC_DEMO_MODE;
    assert.equal(isDemoMode(), false);

    process.env.NEXT_PUBLIC_DEMO_MODE = "false";
    assert.equal(isDemoMode(), false);

    process.env.NEXT_PUBLIC_DEMO_MODE = "";
    assert.equal(isDemoMode(), false);
  });

  it("enables demo mode only when NEXT_PUBLIC_DEMO_MODE is true or 1", () => {
    process.env.NEXT_PUBLIC_DEMO_MODE = "true";
    assert.equal(isDemoMode(), true);

    process.env.NEXT_PUBLIC_DEMO_MODE = "1";
    assert.equal(isDemoMode(), true);
  });
});

describe("static demo dataset & interactive HITL workflow", () => {
  it("covers all 20 deterministic fixtures, 3 source formats, and all 13 discrepancy rules (INT-01..04, 3WAY-01..09)", async () => {
    process.env.NEXT_PUBLIC_DEMO_MODE = "true";

    const health = await fetchHealth();
    assert.equal(health.status, "ok");
    assert.match(health.dependencies.mode ?? "", /static-demo/);

    const list = await fetchInvoices({ limit: 50, offset: 0 });
    assert.equal(list.total, 20);
    assert.equal(list.invoices.length, 20);

    const formats = new Set(list.invoices.map((i) => i.source_format));
    assert.deepEqual(formats, new Set(["UBL_XML", "PDF", "CSV"]));

    const foundCodes = new Set<string>();
    for (const inv of list.invoices) {
      assert.equal(Number.isInteger(inv.subtotal_pence), true);
      assert.equal(Number.isInteger(inv.vat_total_pence), true);
      assert.equal(Number.isInteger(inv.grand_total_pence), true);

      const detail = await fetchInvoiceDetail(inv.id);
      assert.ok(detail.audit_events.length >= 2);
      for (const disc of detail.discrepancies) {
        foundCodes.add(disc.code);
        assert.equal(Number.isInteger(disc.variance_pence), true);
      }

      if (inv.source_format === "UBL_XML" || inv.source_format === "CSV") {
        const src = await fetchInvoiceFileText(inv.id);
        assert.ok(src.sizeBytes > 0);
        assert.ok(src.text.length > 0);
      } else {
        const blob = getDemoInvoiceFileBlob(inv.id);
        assert.equal(blob.contentType, "application/pdf");
        const header = new TextDecoder().decode(blob.bytes.slice(0, 5));
        assert.equal(header, "%PDF-");
      }
    }

    const expectedCodes: DiscrepancyCode[] = [
      "LINE_MATH_ERROR",
      "VAT_MISMATCH",
      "TOTAL_MISMATCH",
      "CURRENCY_MISMATCH",
      "MISSING_PO",
      "PO_SUPPLIER_MISMATCH",
      "PO_CLOSED",
      "MISSING_GRN",
      "UNMATCHED_SKU",
      "QTY_EXCEEDS_GRN",
      "QTY_EXCEEDS_PO",
      "PRICE_VARIANCE",
      "VAT_RATE_MISMATCH",
    ];
    for (const code of expectedCodes) {
      assert.equal(
        foundCodes.has(code),
        true,
        `expected demo dataset to include ${getDiscrepancyMeta(code).ruleId} (${code})`,
      );
    }

    const metrics = await fetchMetrics();
    assert.equal(metrics.total_invoices, 20);
    assert.equal(metrics.auto_approved_count, 4);
    assert.equal(metrics.needs_review_count, 13);
    assert.equal(metrics.parse_failed_count, 3);
    assert.equal(Number.isInteger(metrics.total_billed_pence), true);
    assert.equal(Number.isInteger(metrics.auto_approval_rate_bps), true);
    // 4 / 17 reconciled = 23.53% -> 2353 bps (half-up)
    assert.equal(metrics.auto_approval_rate_bps, 2353);
  });

  it("supports filtering, interactive HITL resolution, 409 Conflict guard, and reset", async () => {
    process.env.NEXT_PUBLIC_DEMO_MODE = "true";

    const needsReview = await fetchInvoices({ status: "NEEDS_REVIEW" });
    assert.equal(needsReview.total, 13);

    const priceVar = await fetchInvoices({ q: "INV-2026-0016" });
    assert.equal(priceVar.total, 1);
    const targetId = priceVar.invoices[0].id;

    const resolved = await resolveInvoice(targetId, {
      status: "MANUALLY_APPROVED",
      actor: "auditor@acme.co.uk",
      reason: "Approved £4.00 unit price increase per buyer email",
    });
    assert.equal(resolved.status, "MANUALLY_APPROVED");

    const updatedDetail = await fetchInvoiceDetail(targetId);
    const lastAudit =
      updatedDetail.audit_events[updatedDetail.audit_events.length - 1];
    assert.equal(lastAudit.actor, "auditor@acme.co.uk");
    assert.equal(lastAudit.from_status, "NEEDS_REVIEW");
    assert.equal(lastAudit.to_status, "MANUALLY_APPROVED");

    // Re-resolving a terminal invoice must throw HTTP 409 Conflict
    await assert.rejects(
      () =>
        resolveInvoice(targetId, {
          status: "REJECTED",
          actor: "second@acme.co.uk",
          reason: "Stale tab attempt",
        }),
      (err: unknown) => err instanceof ApiError && err.isConflict && err.status === 409,
    );

    const afterMetrics = await fetchMetrics();
    assert.equal(afterMetrics.manually_approved_count, 1);
    assert.equal(afterMetrics.needs_review_count, 12);

    // Reset restores initial state deterministically
    resetDemoStore();
    const resetMetrics = await fetchMetrics();
    assert.equal(resetMetrics.manually_approved_count, 0);
    assert.equal(resetMetrics.needs_review_count, 13);
  });

  it("deduplicates identical uploaded bytes via SHA-256 in demo mode", async () => {
    process.env.NEXT_PUBLIC_DEMO_MODE = "true";

    const sampleXml = new File(
      ['<?xml version="1.0"?><Invoice><cbc:ID>INV-TEST-1</cbc:ID></Invoice>'],
      "sample.xml",
      { type: "application/xml" },
    );

    const first = await uploadInvoice(sampleXml);
    assert.equal(first.httpStatus, 202);
    assert.equal(first.data.duplicate, false);
    assert.equal(first.data.source_format, "UBL_XML");

    const second = await uploadInvoice(sampleXml);
    assert.equal(second.httpStatus, 200);
    assert.equal(second.data.duplicate, true);
    assert.equal(second.data.invoice_id, first.data.invoice_id);
    assert.equal(second.data.file_sha256, first.data.file_sha256);
  });
});
