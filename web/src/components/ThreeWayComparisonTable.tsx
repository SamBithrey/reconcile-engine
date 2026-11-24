import { PackageCheck, ReceiptText } from "lucide-react";

import {
  DiscrepancyRuleBadge,
  POStatusBadge,
} from "@/components/StatusBadge";
import {
  absPence,
  calculateLineNetPence,
  calculateLineVATPence,
  formatBPS,
  formatPenceCurrency,
  isPriceOutsideTolerance,
} from "@/lib/money";
import type { Discrepancy, InvoiceDetail, POLine } from "@/lib/types";

export function ThreeWayComparisonTable({ detail }: { detail: InvoiceDetail }) {
  const { invoice, lines, discrepancies, purchase_order: po, goods_receipts: grn } =
    detail;
  const currency = invoice.currency || "GBP";

  const poBySku = new Map<string, POLine>();
  for (const poLine of po?.lines ?? []) {
    poBySku.set(poLine.sku, poLine);
  }

  const hasGRN = grn !== null && grn !== undefined && grn.receipt_count > 0;
  const acceptedBySku = grn?.accepted_by_sku ?? {};

  // Track cumulative invoiced quantity per SKU to match backend 3WAY-06/3WAY-07 rules.
  const cumulativeBySku = new Map<string, number>();
  const skuOccurrenceCount = new Map<string, number>();
  for (const line of lines) {
    skuOccurrenceCount.set(line.sku, (skuOccurrenceCount.get(line.sku) ?? 0) + 1);
  }

  // Group line-scoped discrepancies by line_id or line_number.
  const discsByLineId = new Map<string, Discrepancy[]>();
  const discsByLineNum = new Map<number, Discrepancy[]>();
  for (const d of discrepancies) {
    if (d.invoice_line_id) {
      const arr = discsByLineId.get(d.invoice_line_id) ?? [];
      arr.push(d);
      discsByLineId.set(d.invoice_line_id, arr);
    } else if (d.line_number && d.line_number > 0) {
      const arr = discsByLineNum.get(d.line_number) ?? [];
      arr.push(d);
      discsByLineNum.set(d.line_number, arr);
    }
  }

  const invoicedSkus = new Set(lines.map((l) => l.sku));
  const unbilledPOLines = (po?.lines ?? []).filter(
    (pl) => !invoicedSkus.has(pl.sku),
  );

  return (
    <section className="rounded-xl border border-slate-200 bg-white shadow-sm">
      <div className="border-b border-slate-200 px-4 py-3">
        <h3 className="text-sm font-semibold text-slate-900">
          3-Way PO vs. GRN vs. Invoice Comparison
        </h3>
        <p className="mt-0.5 text-xs text-slate-500">
          Line-by-line deterministic comparison in integer pence and basis
          points.
        </p>
      </div>

      {/* PO & GRN summary cards */}
      <div className="grid grid-cols-1 gap-3 border-b border-slate-200 bg-slate-50/60 p-4 md:grid-cols-2">
        <div className="rounded-lg border border-slate-200 bg-white p-3">
          <div className="flex items-center justify-between gap-2">
            <div className="flex items-center gap-1.5 text-xs font-semibold text-slate-800">
              <ReceiptText className="h-4 w-4 text-slate-500" />
              <span>Purchase Order</span>
            </div>
            {po ? <POStatusBadge status={po.status} /> : null}
          </div>

          {po ? (
            <dl className="mt-2 grid grid-cols-2 gap-x-3 gap-y-1 text-xs">
              <div>
                <dt className="text-[11px] text-slate-400">PO Number</dt>
                <dd className="font-mono font-semibold text-slate-900">
                  {po.po_number}
                </dd>
              </div>
              <div>
                <dt className="text-[11px] text-slate-400">Supplier Code</dt>
                <dd className="font-mono text-slate-800">
                  {po.supplier_code || "—"}
                </dd>
              </div>
              <div>
                <dt className="text-[11px] text-slate-400">Currency / Lines</dt>
                <dd className="font-mono text-slate-800">
                  {po.currency} · {po.lines.length} line(s)
                </dd>
              </div>
              <div>
                <dt className="text-[11px] text-slate-400">PO Total Net</dt>
                <dd className="font-mono font-semibold text-slate-900 tabular-nums">
                  {formatPenceCurrency(po.total_net_pence, po.currency)}
                </dd>
              </div>
            </dl>
          ) : (
            <p className="mt-2 text-xs text-rose-700">
              No matching Purchase Order found for reference{" "}
              <span className="font-mono font-semibold">
                {invoice.po_number_raw || "(empty)"}
              </span>
              .
            </p>
          )}
        </div>

        <div className="rounded-lg border border-slate-200 bg-white p-3">
          <div className="flex items-center justify-between gap-2">
            <div className="flex items-center gap-1.5 text-xs font-semibold text-slate-800">
              <PackageCheck className="h-4 w-4 text-slate-500" />
              <span>Goods Received Notes (GRN)</span>
            </div>
            {grn ? (
              <span
                className={`rounded-full border px-2 py-0.5 font-mono text-[11px] font-medium ${
                  grn.receipt_count > 0
                    ? "border-emerald-200 bg-emerald-50 text-emerald-800"
                    : "border-rose-200 bg-rose-50 text-rose-800"
                }`}
              >
                {grn.receipt_count} receipt{grn.receipt_count === 1 ? "" : "s"}
              </span>
            ) : null}
          </div>

          {grn && grn.receipt_count > 0 ? (
            <div className="mt-2 flex flex-wrap gap-1.5">
              {Object.entries(acceptedBySku).map(([sku, qty]) => (
                <span
                  key={sku}
                  className="inline-flex items-center gap-1 rounded border border-slate-200 bg-slate-50 px-2 py-0.5 font-mono text-[11px] text-slate-800 tabular-nums"
                >
                  <span className="font-semibold">{sku}:</span>
                  <span>{qty} accepted</span>
                </span>
              ))}
            </div>
          ) : po ? (
            <p className="mt-2 text-xs text-rose-700">
              Zero Goods Received Notes recorded for PO{" "}
              <span className="font-mono font-semibold">{po.po_number}</span>.
            </p>
          ) : (
            <p className="mt-2 text-xs text-slate-500">
              GRN lookup requires a valid linked Purchase Order.
            </p>
          )}
        </div>
      </div>

      {/* Line-by-line 3-way comparison table */}
      {lines.length === 0 ? (
        <div className="px-4 py-8 text-center text-xs text-slate-500">
          No parsed invoice line items available for this document.
        </div>
      ) : (
        <div
          tabIndex={0}
          role="region"
          aria-labelledby="three-way-comparison-caption"
          className="table-wrapper"
        >
          <table className="min-w-full divide-y divide-slate-200 text-left text-xs">
            <caption id="three-way-comparison-caption" className="visually-hidden">
              3-Way Purchase Order, Goods Receipt, and Invoice line comparison
            </caption>
            <thead className="bg-slate-50 text-[11px] font-semibold uppercase tracking-wider text-slate-500">
              <tr>
                <th scope="col" className="border-b border-slate-200 bg-slate-50 px-3 py-2.5">#</th>
                <th scope="col" className="border-b border-slate-200 bg-slate-50 px-3 py-2.5">SKU &amp; Description</th>
                <th scope="col" className="border-b border-slate-200 bg-slate-50 px-3 py-2.5 text-right">PO Qty</th>
                <th scope="col" className="border-b border-slate-200 bg-slate-50 px-3 py-2.5 text-right">GRN Acc.</th>
                <th scope="col" className="border-b border-slate-200 bg-slate-50 px-3 py-2.5 text-right">Inv Qty</th>
                <th scope="col" className="border-b border-slate-200 bg-slate-50 px-3 py-2.5 text-right">Unit Price</th>
                <th scope="col" className="border-b border-slate-200 bg-slate-50 px-3 py-2.5 text-right">VAT Rate</th>
                <th scope="col" className="border-b border-slate-200 bg-slate-50 px-3 py-2.5 text-right">Line Net</th>
                <th scope="col" className="border-b border-slate-200 bg-slate-50 px-3 py-2.5 text-right">Line VAT</th>
                <th scope="col" className="border-b border-slate-200 bg-slate-50 px-3 py-2.5">Line Checks</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100 bg-white">
              {lines.map((line) => {
                const poLine = poBySku.get(line.sku);
                const prevCum = cumulativeBySku.get(line.sku) ?? 0;
                const nextCum = prevCum + line.quantity;
                cumulativeBySku.set(line.sku, nextCum);

                const grnAccepted = hasGRN
                  ? (acceptedBySku[line.sku] ?? 0)
                  : null;

                const lineDiscs =
                  (line.line_id ? discsByLineId.get(line.line_id) : undefined) ??
                  discsByLineNum.get(line.line_number) ??
                  [];

                const qtyOverPO = Boolean(poLine && nextCum > poLine.ordered_qty);
                const qtyOverGRN = Boolean(
                  grnAccepted !== null && nextCum > grnAccepted,
                );
                const priceVariance = Boolean(
                  poLine &&
                    isPriceOutsideTolerance(
                      line.unit_price_pence,
                      poLine.unit_price_pence,
                    ),
                );
                const vatRateDiff = Boolean(
                  poLine && line.vat_rate_bps !== poLine.vat_rate_bps,
                );

                const expectedNet = calculateLineNetPence(
                  line.quantity,
                  line.unit_price_pence,
                );
                const lineMathDiff =
                  absPence(BigInt(line.line_net_pence) - expectedNet) > 1n;

                const expectedVAT = calculateLineVATPence(
                  line.line_net_pence,
                  line.vat_rate_bps,
                );
                const lineVatDiff =
                  absPence(BigInt(line.line_vat_pence) - expectedVAT) > 1n;

                const hasSplitSku = (skuOccurrenceCount.get(line.sku) ?? 0) > 1;
                const rowFlagged =
                  lineDiscs.length > 0 ||
                  (po !== null && po !== undefined && !poLine) ||
                  qtyOverPO ||
                  qtyOverGRN ||
                  priceVariance ||
                  vatRateDiff ||
                  lineMathDiff ||
                  lineVatDiff;

                return (
                  <tr
                    key={line.line_id ?? `${line.line_number}-${line.sku}`}
                    className={rowFlagged ? "bg-amber-50/40" : "hover:bg-slate-50"}
                  >
                    <td className="px-3 py-2.5 font-mono text-slate-500 tabular-nums">
                      {line.line_number}
                    </td>

                    <th scope="row" className="px-3 py-2.5 font-normal">
                      <div className="font-mono font-semibold text-slate-900">
                        {line.sku}
                      </div>
                      <div className="max-w-[220px] text-[11px] text-slate-600">
                        {line.description}
                      </div>
                    </th>

                    <td className="px-3 py-2.5 text-right font-mono tabular-nums">
                      {poLine ? (
                        <span className="text-slate-800">{poLine.ordered_qty}</span>
                      ) : po ? (
                        <span className="text-rose-700 font-semibold">
                          Unmatched
                        </span>
                      ) : (
                        <span className="text-slate-400">—</span>
                      )}
                    </td>

                    <td className="px-3 py-2.5 text-right font-mono tabular-nums">
                      {grnAccepted !== null ? (
                        <span className="text-slate-800">{grnAccepted}</span>
                      ) : (
                        <span className="text-slate-400">—</span>
                      )}
                    </td>

                    <td className="px-3 py-2.5 text-right font-mono tabular-nums">
                      <div
                        className={`font-semibold ${
                          qtyOverPO || qtyOverGRN
                            ? "text-rose-700"
                            : "text-slate-900"
                        }`}
                      >
                        {line.quantity}
                      </div>
                      {hasSplitSku ? (
                        <div className="text-[10px] text-slate-500">
                          cum: {nextCum}
                        </div>
                      ) : null}
                    </td>

                    <td className="px-3 py-2.5 text-right font-mono tabular-nums">
                      <div
                        className={
                          priceVariance
                            ? "font-semibold text-rose-700"
                            : "text-slate-900"
                        }
                      >
                        {formatPenceCurrency(line.unit_price_pence, currency)}
                      </div>
                      {poLine ? (
                        <div className="text-[10px] text-slate-500">
                          PO:{" "}
                          {formatPenceCurrency(
                            poLine.unit_price_pence,
                            po?.currency ?? currency,
                          )}
                        </div>
                      ) : null}
                    </td>

                    <td className="px-3 py-2.5 text-right font-mono tabular-nums">
                      <div
                        className={
                          vatRateDiff
                            ? "font-semibold text-rose-700"
                            : "text-slate-900"
                        }
                      >
                        {formatBPS(line.vat_rate_bps)}
                      </div>
                      {poLine && vatRateDiff ? (
                        <div className="text-[10px] text-slate-500">
                          PO: {formatBPS(poLine.vat_rate_bps)}
                        </div>
                      ) : null}
                    </td>

                    <td className="px-3 py-2.5 text-right font-mono tabular-nums">
                      <div
                        className={
                          lineMathDiff
                            ? "font-semibold text-rose-700"
                            : "font-medium text-slate-900"
                        }
                      >
                        {formatPenceCurrency(line.line_net_pence, currency)}
                      </div>
                      {lineMathDiff ? (
                        <div className="text-[10px] text-rose-600">
                          exp:{" "}
                          {formatPenceCurrency(expectedNet, currency)}
                        </div>
                      ) : null}
                    </td>

                    <td className="px-3 py-2.5 text-right font-mono tabular-nums">
                      <div
                        className={
                          lineVatDiff
                            ? "font-semibold text-rose-700"
                            : "text-slate-800"
                        }
                      >
                        {formatPenceCurrency(line.line_vat_pence, currency)}
                      </div>
                      {lineVatDiff ? (
                        <div className="text-[10px] text-rose-600">
                          exp:{" "}
                          {formatPenceCurrency(expectedVAT, currency)}
                        </div>
                      ) : null}
                    </td>

                    <td className="px-3 py-2.5">
                      {lineDiscs.length > 0 ? (
                        <div className="flex flex-col gap-1">
                          {lineDiscs.map((d) => (
                            <DiscrepancyRuleBadge key={d.id} code={d.code} />
                          ))}
                        </div>
                      ) : rowFlagged ? (
                        <span className="text-[11px] font-medium text-amber-700">
                          Variance
                        </span>
                      ) : (
                        <span className="inline-flex items-center rounded border border-emerald-200 bg-emerald-50 px-1.5 py-0.5 font-mono text-[10px] font-medium text-emerald-700">
                          MATCH
                        </span>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {unbilledPOLines.length > 0 ? (
        <div className="border-t border-slate-200 bg-slate-50/70 px-4 py-3 text-xs">
          <div className="font-semibold text-slate-700">
            Unbilled PO Lines ({unbilledPOLines.length})
          </div>
          <div className="mt-1.5 flex flex-wrap gap-2">
            {unbilledPOLines.map((pl) => (
              <span
                key={pl.sku}
                className="inline-flex items-center gap-2 rounded border border-slate-200 bg-white px-2.5 py-1 font-mono text-[11px] text-slate-700"
              >
                <span className="font-semibold">{pl.sku}</span>
                <span>qty {pl.ordered_qty}</span>
                <span>
                  @{" "}
                  {formatPenceCurrency(
                    pl.unit_price_pence,
                    po?.currency ?? currency,
                  )}
                </span>
              </span>
            ))}
          </div>
        </div>
      ) : null}
    </section>
  );
}
