"use client";

import { useState } from "react";
import { Download, FileJson, FileSpreadsheet, Loader2 } from "lucide-react";

import { fetchInvoiceDetail, fetchInvoices } from "@/lib/api";
import {
  type AccountingExportScope,
  serializeAccountingCSV,
  serializeAccountingJSON,
  triggerDownload,
} from "@/lib/export";
import type { Invoice, InvoiceDetail, InvoiceStatus } from "@/lib/types";

const APPROVED_STATUSES = new Set<InvoiceStatus>([
  "AUTO_APPROVED",
  "MANUALLY_APPROVED",
]);

const RECONCILED_STATUSES = new Set<InvoiceStatus>([
  "AUTO_APPROVED",
  "MANUALLY_APPROVED",
  "NEEDS_REVIEW",
  "REJECTED",
]);

async function fetchDetailsInBatches(
  ids: string[],
  onProgress: (done: number, total: number) => void,
): Promise<InvoiceDetail[]> {
  const results: InvoiceDetail[] = [];
  const batchSize = 5;

  for (let i = 0; i < ids.length; i += batchSize) {
    const slice = ids.slice(i, i + batchSize);
    const chunk = await Promise.all(slice.map((id) => fetchInvoiceDetail(id)));
    results.push(...chunk);
    onProgress(Math.min(i + slice.length, ids.length), ids.length);
  }

  return results;
}

async function fetchAllInvoicesForScope(
  scope: Exclude<AccountingExportScope, "CURRENT_VIEW">,
): Promise<Invoice[]> {
  const allowed =
    scope === "APPROVED_ONLY" ? APPROVED_STATUSES : RECONCILED_STATUSES;
  const collected: Invoice[] = [];
  const limit = 200;
  let offset = 0;
  let total = Infinity;

  while (offset < total) {
    const page = await fetchInvoices({ limit, offset });
    total = page.total;
    for (const inv of page.invoices) {
      if (allowed.has(inv.status)) {
        collected.push(inv);
      }
    }
    if (page.invoices.length === 0) {
      break;
    }
    offset += page.invoices.length;
  }

  return collected;
}

type AccountingExportMenuProps =
  | {
      mode: "single";
      detail: InvoiceDetail;
    }
  | {
      mode: "batch";
      filteredInvoices: Invoice[];
    };

export function AccountingExportMenu(props: AccountingExportMenuProps) {
  const [scope, setScope] = useState<AccountingExportScope>("APPROVED_ONLY");
  const [busyFormat, setBusyFormat] = useState<"csv" | "json" | null>(null);
  const [progressText, setProgressText] = useState<string | null>(null);
  const [feedback, setFeedback] = useState<string | null>(null);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  if (props.mode === "single") {
    const baseName = (
      props.detail.invoice.invoice_number ||
      props.detail.invoice.id.slice(0, 8)
    )
      .replace(/[^a-zA-Z0-9._-]+/g, "-")
      .toLowerCase();

    const handleSingleExport = (format: "csv" | "json") => {
      const details = [props.detail];
      if (format === "csv") {
        triggerDownload(
          serializeAccountingCSV(details),
          `${baseName}-accounting.csv`,
          "text/csv;charset=utf-8",
        );
      } else {
        triggerDownload(
          serializeAccountingJSON(details, "CURRENT_VIEW"),
          `${baseName}-accounting.json`,
          "application/json;charset=utf-8",
        );
      }
    };

    return (
      <div className="inline-flex items-center gap-2">
        <button
          type="button"
          onClick={() => handleSingleExport("csv")}
          className="inline-flex items-center gap-1.5 rounded-lg border border-slate-300 bg-white px-3 py-1.5 text-xs font-medium text-slate-700 hover:bg-slate-50 transition-colors"
        >
          <FileSpreadsheet className="h-3.5 w-3.5 text-emerald-700" />
          <span>Export CSV</span>
        </button>
        <button
          type="button"
          onClick={() => handleSingleExport("json")}
          className="inline-flex items-center gap-1.5 rounded-lg border border-slate-300 bg-white px-3 py-1.5 text-xs font-medium text-slate-700 hover:bg-slate-50 transition-colors"
        >
          <FileJson className="h-3.5 w-3.5 text-indigo-700" />
          <span>Export JSON</span>
        </button>
      </div>
    );
  }

  const handleBatchExport = async (format: "csv" | "json") => {
    setBusyFormat(format);
    setErrorMsg(null);
    setFeedback(null);
    setProgressText("Preparing export...");

    try {
      const targetInvoices =
        scope === "CURRENT_VIEW"
          ? props.filteredInvoices
          : await fetchAllInvoicesForScope(scope);

      if (targetInvoices.length === 0) {
        setErrorMsg("No invoices match the selected accounting export scope.");
        return;
      }

      setProgressText(`Fetching 0/${targetInvoices.length}...`);
      const details = await fetchDetailsInBatches(
        targetInvoices.map((inv) => inv.id),
        (done, total) => setProgressText(`Fetching ${done}/${total}...`),
      );

      const dateSlug = new Date().toISOString().slice(0, 10);
      const scopeSlug = scope.toLowerCase().replace(/_/g, "-");

      if (format === "csv") {
        const csv = serializeAccountingCSV(details);
        triggerDownload(
          csv,
          `reconcile-export-${scopeSlug}-${dateSlug}.csv`,
          "text/csv;charset=utf-8",
        );
      } else {
        const json = serializeAccountingJSON(details, scope);
        triggerDownload(
          json,
          `reconcile-export-${scopeSlug}-${dateSlug}.json`,
          "application/json;charset=utf-8",
        );
      }

      setFeedback(
        `Exported ${details.length} invoice(s) as ${format.toUpperCase()}`,
      );
    } catch (err) {
      setErrorMsg(
        err instanceof Error ? err.message : "Failed to generate export",
      );
    } finally {
      setBusyFormat(null);
      setProgressText(null);
    }
  };

  return (
    <div className="flex flex-col items-end gap-1">
      <div className="flex flex-wrap items-center gap-2">
        <label className="visually-hidden" htmlFor="accounting-export-scope">
          Accounting export scope
        </label>
        <select
          id="accounting-export-scope"
          value={scope}
          disabled={busyFormat !== null}
          onChange={(e) => setScope(e.target.value as AccountingExportScope)}
          className="rounded-lg border border-slate-300 bg-white px-2.5 py-1.5 text-xs font-medium text-slate-700 focus:border-slate-500"
        >
          <option value="APPROVED_ONLY">
            Export: Approved Only (AUTO + MANUAL)
          </option>
          <option value="ALL_RECONCILED">Export: All Reconciled</option>
          <option value="CURRENT_VIEW">
            Export: Current View ({props.filteredInvoices.length})
          </option>
        </select>

        <button
          type="button"
          disabled={busyFormat !== null}
          onClick={() => void handleBatchExport("csv")}
          className="inline-flex items-center gap-1.5 rounded-lg border border-slate-300 bg-white px-3 py-1.5 text-xs font-semibold text-slate-700 hover:bg-slate-50 disabled:opacity-50 transition-colors"
        >
          {busyFormat === "csv" ? (
            <Loader2 className="h-3.5 w-3.5 animate-spin" />
          ) : (
            <Download className="h-3.5 w-3.5 text-emerald-700" />
          )}
          <span>CSV</span>
        </button>

        <button
          type="button"
          disabled={busyFormat !== null}
          onClick={() => void handleBatchExport("json")}
          className="inline-flex items-center gap-1.5 rounded-lg border border-slate-300 bg-white px-3 py-1.5 text-xs font-semibold text-slate-700 hover:bg-slate-50 disabled:opacity-50 transition-colors"
        >
          {busyFormat === "json" ? (
            <Loader2 className="h-3.5 w-3.5 animate-spin" />
          ) : (
            <FileJson className="h-3.5 w-3.5 text-indigo-700" />
          )}
          <span>JSON</span>
        </button>
      </div>

      {progressText ? (
        <span className="text-[11px] text-slate-500">{progressText}</span>
      ) : errorMsg ? (
        <span role="alert" className="text-[11px] font-medium text-rose-600">
          {errorMsg}
        </span>
      ) : feedback ? (
        <span aria-live="polite" className="text-[11px] font-medium text-emerald-700">
          {feedback}
        </span>
      ) : null}
    </div>
  );
}
