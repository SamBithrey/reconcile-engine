"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import {
  AlertCircle,
  ArrowLeft,
  FileWarning,
  Loader2,
  RefreshCw,
} from "lucide-react";

import { AccountingExportMenu } from "@/components/AccountingExportMenu";
import { AuditTrailPanel } from "@/components/AuditTrailPanel";
import { DiscrepanciesPanel } from "@/components/DiscrepanciesPanel";
import { ResolutionPanel } from "@/components/ResolutionPanel";
import { SourceDocumentViewer } from "@/components/SourceDocumentViewer";
import { SourceFormatBadge, StatusBadge } from "@/components/StatusBadge";
import { ThreeWayComparisonTable } from "@/components/ThreeWayComparisonTable";
import { ApiError, fetchInvoiceDetail } from "@/lib/api";
import { formatPenceCurrency } from "@/lib/money";
import type { InvoiceDetail } from "@/lib/types";

export function InvoiceDetailClient({ invoiceId }: { invoiceId: string }) {
  const [detail, setDetail] = useState<InvoiceDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const [isNotFound, setIsNotFound] = useState(false);

  const loadDetail = useCallback(
    async (silent = false) => {
      if (silent) {
        setRefreshing(true);
      } else {
        setLoading(true);
      }

      try {
        const res = await fetchInvoiceDetail(invoiceId);
        setDetail(res);
        setErrorMsg(null);
        setIsNotFound(false);
      } catch (err) {
        if (err instanceof ApiError && err.isNotFound) {
          setIsNotFound(true);
          setErrorMsg("Invoice not found in ledger.");
        } else {
          setErrorMsg(
            err instanceof Error
              ? err.message
              : "Failed to load invoice detail",
          );
        }
      } finally {
        setLoading(false);
        setRefreshing(false);
      }
    },
    [invoiceId],
  );

  useEffect(() => {
    void loadDetail(false);
  }, [loadDetail]);

  const status = detail?.invoice.status;
  const invoiceNumber = detail?.invoice.invoice_number;

  useEffect(() => {
    const label = invoiceNumber || `Invoice ${invoiceId.slice(0, 8)}`;
    document.title = `${label} | Reconcile Engine`;
  }, [invoiceNumber, invoiceId]);

  useEffect(() => {
    if (status !== "UPLOADED" && status !== "PARSED") {
      return;
    }
    const timer = window.setInterval(() => {
      void loadDetail(true);
    }, 2000);
    return () => window.clearInterval(timer);
  }, [status, loadDetail]);

  if (loading && !detail) {
    return (
      <div className="flex h-72 items-center justify-center gap-2 text-sm text-slate-600">
        <Loader2 className="h-4 w-4 animate-spin" />
        <span>Loading invoice reconciliation workspace...</span>
      </div>
    );
  }

  if (!detail) {
    return (
      <div className="space-y-4">
        <Link
          href="/"
          className="inline-flex items-center gap-1.5 text-xs font-semibold text-slate-700 hover:text-slate-900"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          <span>Back to Queue</span>
        </Link>

        <div className="flex items-start gap-3 rounded-xl border border-rose-200 bg-rose-50 p-5 text-sm text-rose-900">
          <AlertCircle className="mt-0.5 h-5 w-5 shrink-0 text-rose-600" />
          <div>
            <h1 className="font-semibold">
              {isNotFound ? "Invoice Not Found" : "Unable to Load Invoice"}
            </h1>
            <p className="mt-1 text-xs text-rose-800">{errorMsg}</p>
          </div>
        </div>
      </div>
    );
  }

  const { invoice, discrepancies, audit_events } = detail;
  const currency = invoice.currency || "GBP";

  return (
    <div className="space-y-5">
      {/* Top breadcrumb & action bar */}
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-slate-200 bg-white px-4 py-3 shadow-sm">
        <div className="flex flex-wrap items-center gap-3">
          <Link
            href="/"
            className="inline-flex items-center gap-1.5 rounded-lg border border-slate-300 bg-slate-50 px-2.5 py-1.5 text-xs font-semibold text-slate-700 hover:bg-slate-100 transition-colors"
          >
            <ArrowLeft className="h-3.5 w-3.5" />
            <span>Queue</span>
          </Link>

          <div>
            <div className="flex flex-wrap items-center gap-2">
              <h1 className="font-mono text-base font-bold text-slate-900">
                {invoice.invoice_number || `Invoice ${invoice.id.slice(0, 8)}…`}
              </h1>
              <StatusBadge status={invoice.status} />
              <SourceFormatBadge format={invoice.source_format} />
            </div>
            <p className="mt-0.5 font-mono text-[11px] text-slate-500">
              ID: {invoice.id}
            </p>
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <button
            type="button"
            onClick={() => void loadDetail(true)}
            className="inline-flex items-center gap-1.5 rounded-lg border border-slate-300 bg-white px-3 py-1.5 text-xs font-medium text-slate-700 hover:bg-slate-50 transition-colors"
          >
            <RefreshCw
              className={`h-3.5 w-3.5 ${refreshing ? "animate-spin" : ""}`}
            />
            <span>Refresh</span>
          </button>
          <AccountingExportMenu mode="single" detail={detail} />
        </div>
      </div>

      {errorMsg ? (
        <div role="alert" className="flex items-center gap-2 rounded-lg border border-rose-200 bg-rose-50 px-4 py-2.5 text-xs text-rose-800">
          <AlertCircle aria-hidden="true" className="h-4 w-4 shrink-0 text-rose-600" />
          <span>{errorMsg}</span>
        </div>
      ) : null}

      {/* Split-Pane HITL Workspace */}
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-12 lg:items-start">
        {/* Left Pane — Source Document Viewer */}
        <div className="lg:col-span-5 lg:sticky lg:top-20">
          <SourceDocumentViewer invoice={invoice} />
        </div>

        {/* Right Pane — 3-Way Reconciliation, HITL Resolution & Audit Trail */}
        <div className="space-y-5 lg:col-span-7">
          {/* Header metadata & integer pence financial totals */}
          <section className="rounded-xl border border-slate-200 bg-white p-4 shadow-sm space-y-4">
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-4 text-xs">
              <div>
                <div className="text-[11px] font-medium text-slate-400">
                  Invoice Number
                </div>
                <div className="mt-0.5 font-mono font-semibold text-slate-900">
                  {invoice.invoice_number || "—"}
                </div>
              </div>

              <div>
                <div className="text-[11px] font-medium text-slate-400">
                  PO Reference
                </div>
                <div className="mt-0.5 font-mono font-semibold text-slate-900">
                  {invoice.po_number_raw || "—"}
                </div>
              </div>

              <div>
                <div className="text-[11px] font-medium text-slate-400">
                  Supplier
                </div>
                <div className="mt-0.5 font-mono font-semibold text-slate-900">
                  {invoice.supplier_code_raw || "—"}
                </div>
                {invoice.supplier_name ? (
                  <div className="truncate text-[11px] text-slate-500">
                    {invoice.supplier_name}
                  </div>
                ) : null}
              </div>

              <div>
                <div className="text-[11px] font-medium text-slate-400">
                  Currency &amp; Dates
                </div>
                <div className="mt-0.5 font-mono text-slate-800">
                  {currency}
                </div>
                <div className="font-mono text-[11px] text-slate-500">
                  Issued: {invoice.issue_date ? invoice.issue_date.slice(0, 10) : "—"}
                  {invoice.due_date
                    ? ` · Due: ${invoice.due_date.slice(0, 10)}`
                    : ""}
                </div>
              </div>
            </div>

            {/* Integer pence monetary totals strip */}
            <div className="grid grid-cols-1 gap-3 border-t border-slate-200 pt-3 sm:grid-cols-3">
              <div className="rounded-lg border border-slate-200 bg-slate-50 px-3 py-2">
                <div className="text-[11px] font-medium text-slate-500">
                  Subtotal (Net)
                </div>
                <div className="mt-0.5 font-mono text-base font-bold text-slate-900 tabular-nums">
                  {formatPenceCurrency(invoice.subtotal_pence, currency)}
                </div>
                <div className="font-mono text-[10px] text-slate-400 tabular-nums">
                  {invoice.subtotal_pence} pence
                </div>
              </div>

              <div className="rounded-lg border border-slate-200 bg-slate-50 px-3 py-2">
                <div className="text-[11px] font-medium text-slate-500">
                  VAT Total
                </div>
                <div className="mt-0.5 font-mono text-base font-bold text-slate-900 tabular-nums">
                  {formatPenceCurrency(invoice.vat_total_pence, currency)}
                </div>
                <div className="font-mono text-[10px] text-slate-400 tabular-nums">
                  {invoice.vat_total_pence} pence
                </div>
              </div>

              <div className="rounded-lg border border-slate-900 bg-slate-900 px-3 py-2 text-white">
                <div className="text-[11px] font-medium text-slate-300">
                  Grand Total
                </div>
                <div className="mt-0.5 font-mono text-base font-bold tabular-nums">
                  {formatPenceCurrency(invoice.grand_total_pence, currency)}
                </div>
                <div className="font-mono text-[10px] text-slate-400 tabular-nums">
                  {invoice.grand_total_pence} pence
                </div>
              </div>
            </div>

            {invoice.parse_error ? (
              <div className="flex items-start gap-2.5 rounded-lg border border-rose-300 bg-rose-50 px-3.5 py-3 text-xs text-rose-900">
                <FileWarning className="mt-0.5 h-4 w-4 shrink-0 text-rose-600" />
                <div>
                  <div className="font-semibold">
                    Deterministic Parser Error ({invoice.status})
                  </div>
                  <div className="mt-0.5 font-mono text-[11px]">
                    {invoice.parse_error}
                  </div>
                </div>
              </div>
            ) : null}
          </section>

          <ResolutionPanel
            invoice={invoice}
            onResolved={async () => {
              await loadDetail(true);
            }}
          />

          <DiscrepanciesPanel
            discrepancies={discrepancies}
            currency={currency}
            status={invoice.status}
          />

          <ThreeWayComparisonTable detail={detail} />

          <AuditTrailPanel events={audit_events} />
        </div>
      </div>
    </div>
  );
}
