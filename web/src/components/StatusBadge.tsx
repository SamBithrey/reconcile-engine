import {
  getDiscrepancyMeta,
  getFormatMeta,
  getPOStatusBadgeClass,
  getStatusMeta,
} from "@/lib/discrepancies";
import type {
  DiscrepancySeverity,
  InvoiceStatus,
  POStatus,
  SourceFormat,
} from "@/lib/types";

export function StatusBadge({
  status,
  size = "sm",
}: {
  status: InvoiceStatus;
  size?: "xs" | "sm";
}) {
  const meta = getStatusMeta(status);
  const sizeClass =
    size === "xs" ? "px-2 py-0.5 text-[11px]" : "px-2.5 py-0.5 text-xs";

  return (
    <span
      title={meta.description}
      className={`inline-flex items-center gap-1.5 rounded-full border font-medium whitespace-nowrap ${sizeClass} ${meta.badgeClass}`}
    >
      <span className={`h-1.5 w-1.5 rounded-full ${meta.dotClass}`} />
      <span>{status}</span>
    </span>
  );
}

export function SourceFormatBadge({ format }: { format: SourceFormat }) {
  const meta = getFormatMeta(format);
  return (
    <span
      className={`inline-flex items-center rounded border px-2 py-0.5 font-mono text-[11px] font-medium whitespace-nowrap ${meta.badgeClass}`}
    >
      {meta.label}
    </span>
  );
}

export function POStatusBadge({ status }: { status: POStatus }) {
  return (
    <span
      className={`inline-flex items-center rounded-full border px-2 py-0.5 font-mono text-[11px] font-medium whitespace-nowrap ${getPOStatusBadgeClass(status)}`}
    >
      {status}
    </span>
  );
}

export function SeverityBadge({ severity }: { severity: DiscrepancySeverity }) {
  const cls =
    severity === "BLOCKING"
      ? "border-rose-300 bg-rose-50 text-rose-800"
      : "border-amber-300 bg-amber-50 text-amber-800";

  return (
    <span
      className={`inline-flex items-center rounded border px-1.5 py-0.5 font-mono text-[10px] font-semibold uppercase tracking-wide ${cls}`}
    >
      {severity}
    </span>
  );
}

export function DiscrepancyRuleBadge({
  code,
  showTitle = false,
}: {
  code: string;
  showTitle?: boolean;
}) {
  const meta = getDiscrepancyMeta(code);
  const passCls =
    meta.pass === "PASS_A"
      ? "border-orange-300 bg-orange-50 text-orange-900"
      : "border-amber-300 bg-amber-50 text-amber-900";

  return (
    <span
      title={`${meta.ruleId} (${meta.code}): ${meta.description}`}
      className={`inline-flex items-center gap-1.5 rounded border px-2 py-0.5 text-xs ${passCls}`}
    >
      <span className="font-mono text-[11px] font-bold">{meta.ruleId}</span>
      <span className="font-mono text-[11px]">{meta.code}</span>
      {showTitle ? (
        <span className="text-[11px] font-medium opacity-80">
          · {meta.title}
        </span>
      ) : null}
    </span>
  );
}
