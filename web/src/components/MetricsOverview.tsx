"use client";

import {
  AlertTriangle,
  CheckCircle2,
  FileWarning,
  Layers,
  ShieldAlert,
  ShieldCheck,
  XCircle,
} from "lucide-react";

import { getDiscrepancyMeta } from "@/lib/discrepancies";
import { formatBPS, formatPenceToGBP } from "@/lib/money";
import type { InvoiceStatus, MetricsSummary } from "@/lib/types";

interface MetricsOverviewProps {
  metrics: MetricsSummary | null;
  loading: boolean;
  activeStatus: InvoiceStatus | "";
  onSelectStatus: (status: InvoiceStatus | "") => void;
}

export function MetricsOverview({
  metrics,
  loading,
  activeStatus,
  onSelectStatus,
}: MetricsOverviewProps) {
  const totalDiscrepancyHits =
    metrics?.discrepancies_by_code.reduce((acc, item) => acc + item.count, 0) ??
    0;

  const cards: Array<{
    key: string;
    label: string;
    filterStatus?: InvoiceStatus | "";
    value: string;
    subtext: string;
    tone: string;
    icon: React.ReactNode;
  }> = [
    {
      key: "TOTAL",
      label: "Total Volume",
      filterStatus: "",
      value: metrics ? String(metrics.total_invoices) : "—",
      subtext: metrics
        ? `${formatPenceToGBP(metrics.total_billed_pence)} billed`
        : "Loading...",
      tone: "border-slate-200 bg-white text-slate-900",
      icon: <Layers className="h-4 w-4 text-slate-500" />,
    },
    {
      key: "AUTO_APPROVED",
      label: "AUTO_APPROVED",
      filterStatus: "AUTO_APPROVED",
      value: metrics ? String(metrics.auto_approved_count) : "—",
      subtext: metrics
        ? `${formatBPS(metrics.auto_approval_rate_bps)} straight-through`
        : "Loading...",
      tone: "border-emerald-200 bg-emerald-50/40 text-emerald-950",
      icon: <CheckCircle2 className="h-4 w-4 text-emerald-600" />,
    },
    {
      key: "NEEDS_REVIEW",
      label: "NEEDS_REVIEW",
      filterStatus: "NEEDS_REVIEW",
      value: metrics ? String(metrics.needs_review_count) : "—",
      subtext: metrics
        ? `${formatPenceToGBP(metrics.needs_review_pence)} held`
        : "Loading...",
      tone: "border-amber-300 bg-amber-50/60 text-amber-950",
      icon: <AlertTriangle className="h-4 w-4 text-amber-600" />,
    },
    {
      key: "MANUALLY_APPROVED",
      label: "MANUALLY_APPROVED",
      filterStatus: "MANUALLY_APPROVED",
      value: metrics ? String(metrics.manually_approved_count) : "—",
      subtext: metrics
        ? `${formatPenceToGBP(metrics.approved_pence)} total approved`
        : "Loading...",
      tone: "border-teal-200 bg-teal-50/40 text-teal-950",
      icon: <ShieldCheck className="h-4 w-4 text-teal-600" />,
    },
    {
      key: "REJECTED",
      label: "REJECTED",
      filterStatus: "REJECTED",
      value: metrics ? String(metrics.rejected_count) : "—",
      subtext: "Operator rejected",
      tone: "border-red-200 bg-red-50/40 text-red-950",
      icon: <XCircle className="h-4 w-4 text-red-600" />,
    },
    {
      key: "PARSE_FAILED",
      label: "PARSE_FAILED",
      filterStatus: "PARSE_FAILED",
      value: metrics ? String(metrics.parse_failed_count) : "—",
      subtext: "Quarantined / DLQ",
      tone: "border-rose-200 bg-rose-50/40 text-rose-950",
      icon: <FileWarning className="h-4 w-4 text-rose-600" />,
    },
    {
      key: "VARIANCE",
      label: "Total Discrepancy Variance",
      value: metrics ? formatPenceToGBP(metrics.total_variance_pence) : "—",
      subtext: metrics
        ? `${totalDiscrepancyHits} discrepancy hit(s)`
        : "Loading...",
      tone: "border-slate-200 bg-white text-slate-900",
      icon: <ShieldAlert className="h-4 w-4 text-amber-600" />,
    },
  ];

  return (
    <section className="space-y-3">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4 xl:grid-cols-7">
        {cards.map((card) => {
          const isFilterable = card.filterStatus !== undefined;
          const isSelected =
            isFilterable && activeStatus === card.filterStatus;

          const baseClass = `rounded-xl border p-3.5 text-left transition-all ${card.tone} ${
            isSelected ? "ring-2 ring-slate-900 ring-offset-1" : ""
          } ${loading && !metrics ? "animate-pulse" : ""}`;

          const content = (
            <>
              <div className="flex items-center justify-between gap-2">
                <span className="truncate font-mono text-[11px] font-semibold uppercase tracking-wide text-slate-600">
                  {card.label}
                </span>
                {card.icon}
              </div>
              <div className="mt-2 text-2xl font-bold tracking-tight tabular-nums">
                {card.value}
              </div>
              <div className="mt-1 truncate text-xs text-slate-600 tabular-nums">
                {card.subtext}
              </div>
            </>
          );

          if (isFilterable) {
            return (
              <button
                key={card.key}
                type="button"
                aria-pressed={isSelected}
                onClick={() =>
                  onSelectStatus(
                    activeStatus === card.filterStatus &&
                      card.filterStatus !== ""
                      ? ""
                      : (card.filterStatus ?? ""),
                  )
                }
                className={`${baseClass} hover:shadow-sm cursor-pointer`}
              >
                {content}
              </button>
            );
          }

          return (
            <div key={card.key} className={baseClass}>
              {content}
            </div>
          );
        })}
      </div>

      {metrics && metrics.discrepancies_by_code.length > 0 ? (
        <div className="rounded-xl border border-slate-200 bg-white px-4 py-3">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span className="text-xs font-semibold uppercase tracking-wide text-slate-500">
              Discrepancies by Rule Code (Pass A &amp; Pass B)
            </span>
            <span className="text-xs text-slate-500 tabular-nums">
              {metrics.discrepancies_by_code.length} active rule(s)
            </span>
          </div>
          <div className="mt-2.5 flex flex-wrap gap-2">
            {metrics.discrepancies_by_code.map((item) => {
              const meta = getDiscrepancyMeta(item.code);
              return (
                <div
                  key={item.code}
                  title={meta.description}
                  className="inline-flex items-center gap-2 rounded-lg border border-slate-200 bg-slate-50 px-2.5 py-1 text-xs"
                >
                  <span className="font-mono font-bold text-amber-800">
                    {meta.ruleId}
                  </span>
                  <span className="font-mono text-slate-700">{item.code}</span>
                  <span className="rounded bg-slate-200 px-1.5 py-0.5 font-mono text-[11px] font-semibold text-slate-800 tabular-nums">
                    ×{item.count}
                  </span>
                  <span className="font-mono text-[11px] text-slate-600 tabular-nums">
                    {formatPenceToGBP(item.total_variance_pence)}
                  </span>
                </div>
              );
            })}
          </div>
        </div>
      ) : null}
    </section>
  );
}
