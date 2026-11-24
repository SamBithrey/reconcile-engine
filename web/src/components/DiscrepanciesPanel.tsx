import { AlertTriangle, CheckCircle2 } from "lucide-react";

import {
  DiscrepancyRuleBadge,
  SeverityBadge,
} from "@/components/StatusBadge";
import { absPence, formatPenceCurrency, sumPence } from "@/lib/money";
import type { Discrepancy, InvoiceStatus } from "@/lib/types";

export function DiscrepanciesPanel({
  discrepancies,
  currency,
  status,
}: {
  discrepancies: Discrepancy[];
  currency: string;
  status: InvoiceStatus;
}) {
  const totalAbsVariance = sumPence(
    discrepancies.map((d) => absPence(d.variance_pence)),
  );

  return (
    <section className="rounded-xl border border-slate-200 bg-white shadow-sm">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-slate-200 px-4 py-3">
        <div>
          <h3 className="text-sm font-semibold text-slate-900">
            Reconciliation Discrepancies
          </h3>
          <p className="mt-0.5 text-xs text-slate-500">
            Pass A internal integrity (INT-01..INT-04) &amp; Pass B 3-way rules
            (3WAY-01..3WAY-09).
          </p>
        </div>

        {discrepancies.length > 0 ? (
          <div className="flex items-center gap-2 text-xs">
            <span className="rounded-full border border-amber-300 bg-amber-50 px-2.5 py-0.5 font-mono font-semibold text-amber-800 tabular-nums">
              {discrepancies.length} issue{discrepancies.length === 1 ? "" : "s"}
            </span>
            <span className="rounded-full border border-slate-200 bg-slate-50 px-2.5 py-0.5 font-mono font-semibold text-slate-800 tabular-nums">
              Σ |variance|: {formatPenceCurrency(totalAbsVariance, currency)}
            </span>
          </div>
        ) : null}
      </div>

      {discrepancies.length === 0 ? (
        <div className="flex items-center gap-3 px-4 py-4 text-xs text-emerald-800 bg-emerald-50/40">
          <CheckCircle2 className="h-4 w-4 shrink-0 text-emerald-600" />
          <span>
            {status === "PARSE_FAILED"
              ? "Document failed parsing prior to 3-way matching; no matching discrepancies recorded."
              : status === "UPLOADED" || status === "PARSED"
                ? "Invoice is awaiting completion of the deterministic 3-way matcher."
                : "Zero discrepancies detected across Pass A integrity checks and Pass B PO/GRN verification."}
          </span>
        </div>
      ) : (
        <div className="divide-y divide-slate-100">
          {discrepancies.map((d) => (
            <div key={d.id} className="p-4 space-y-2.5 hover:bg-slate-50/60">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="flex flex-wrap items-center gap-2">
                  <AlertTriangle className="h-4 w-4 text-amber-600 shrink-0" />
                  <DiscrepancyRuleBadge code={d.code} showTitle />
                  <SeverityBadge severity={d.severity} />
                  {d.line_number && d.line_number > 0 ? (
                    <span className="rounded bg-slate-100 px-2 py-0.5 font-mono text-[11px] text-slate-700">
                      Line {d.line_number}
                      {d.sku ? ` · ${d.sku}` : ""}
                    </span>
                  ) : (
                    <span className="rounded bg-slate-100 px-2 py-0.5 font-mono text-[11px] text-slate-600">
                      Document-level
                    </span>
                  )}
                </div>

                <div className="text-right font-mono text-xs tabular-nums">
                  <span className="font-semibold text-rose-700">
                    {formatPenceCurrency(d.variance_pence, currency, {
                      showSign: true,
                    })}
                  </span>
                  <span className="ml-1.5 text-[11px] text-slate-400">
                    ({d.variance_pence > 0 ? "+" : ""}
                    {d.variance_pence}p)
                  </span>
                </div>
              </div>

              <p className="text-xs text-slate-800">{d.message}</p>

              <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 rounded-lg border border-slate-200 bg-slate-50/80 p-2.5 text-xs">
                <div>
                  <span className="block text-[10px] font-semibold uppercase tracking-wider text-slate-400">
                    Expected Value
                  </span>
                  <span className="font-mono font-medium text-emerald-800">
                    {d.expected_value || "—"}
                  </span>
                </div>
                <div>
                  <span className="block text-[10px] font-semibold uppercase tracking-wider text-slate-400">
                    Actual Value
                  </span>
                  <span className="font-mono font-medium text-rose-800">
                    {d.actual_value || "—"}
                  </span>
                </div>
              </div>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
