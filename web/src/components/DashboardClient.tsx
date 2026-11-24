"use client";

import Link from "next/link";
import { useCallback, useEffect, useRef, useState } from "react";
import {
  AlertCircle,
  ArrowRight,
  ChevronLeft,
  ChevronRight,
  FilterX,
  RefreshCw,
  Search,
} from "lucide-react";

import { AccountingExportMenu } from "@/components/AccountingExportMenu";
import { InvoiceUploadPanel } from "@/components/InvoiceUploadPanel";
import { MetricsOverview } from "@/components/MetricsOverview";
import { SourceFormatBadge, StatusBadge } from "@/components/StatusBadge";
import { fetchInvoices, fetchMetrics } from "@/lib/api";
import { formatPenceCurrency } from "@/lib/money";
import {
  ALL_INVOICE_STATUSES,
  ALL_SOURCE_FORMATS,
  type Invoice,
  type InvoiceStatus,
  type MetricsSummary,
  type SourceFormat,
} from "@/lib/types";

const PAGE_LIMIT = 50;

export function DashboardClient() {
  const [metrics, setMetrics] = useState<MetricsSummary | null>(null);
  const [metricsLoading, setMetricsLoading] = useState(true);

  const [invoices, setInvoices] = useState<Invoice[]>([]);
  const [total, setTotal] = useState(0);
  const [queueLoading, setQueueLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const [statusFilter, setStatusFilter] = useState<InvoiceStatus | "">("");
  const [formatFilter, setFormatFilter] = useState<SourceFormat | "">("");
  const [supplierFilter, setSupplierFilter] = useState("");
  const [searchInput, setSearchInput] = useState("");
  const [debouncedSearch, setDebouncedSearch] = useState("");
  const [offset, setOffset] = useState(0);

  const followUpTimerRef = useRef<number | null>(null);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setDebouncedSearch(searchInput);
      setOffset(0);
    }, 250);
    return () => window.clearTimeout(timer);
  }, [searchInput]);

  useEffect(() => {
    return () => {
      if (followUpTimerRef.current !== null) {
        window.clearTimeout(followUpTimerRef.current);
      }
    };
  }, []);

  const loadMetrics = useCallback(async () => {
    setMetricsLoading(true);
    try {
      const m = await fetchMetrics();
      setMetrics(m);
    } catch (err) {
      setErrorMsg(
        err instanceof Error ? err.message : "Failed to load metrics",
      );
    } finally {
      setMetricsLoading(false);
    }
  }, []);

  const loadInvoices = useCallback(async () => {
    setQueueLoading(true);
    try {
      const res = await fetchInvoices({
        status: statusFilter,
        source_format: formatFilter,
        supplier_code: supplierFilter,
        q: debouncedSearch,
        limit: PAGE_LIMIT,
        offset,
      });
      setInvoices(res.invoices);
      setTotal(res.total);
      setErrorMsg(null);
    } catch (err) {
      setErrorMsg(
        err instanceof Error ? err.message : "Failed to load invoice queue",
      );
    } finally {
      setQueueLoading(false);
    }
  }, [statusFilter, formatFilter, supplierFilter, debouncedSearch, offset]);

  const refreshAll = useCallback(async () => {
    await Promise.all([loadMetrics(), loadInvoices()]);
  }, [loadMetrics, loadInvoices]);

  useEffect(() => {
    void loadMetrics();
  }, [loadMetrics]);

  useEffect(() => {
    void loadInvoices();
  }, [loadInvoices]);

  // Poll gently only while an invoice on screen is still transitioning asynchronously.
  const hasInFlightInvoices = invoices.some(
    (inv) => inv.status === "UPLOADED" || inv.status === "PARSED",
  );

  useEffect(() => {
    if (!hasInFlightInvoices) {
      return;
    }
    const interval = window.setInterval(() => {
      void refreshAll();
    }, 2000);
    return () => window.clearInterval(interval);
  }, [hasInFlightInvoices, refreshAll]);

  const handleSelectStatus = (nextStatus: InvoiceStatus | "") => {
    setStatusFilter(nextStatus);
    setOffset(0);
  };

  const clearFilters = () => {
    setStatusFilter("");
    setFormatFilter("");
    setSupplierFilter("");
    setSearchInput("");
    setDebouncedSearch("");
    setOffset(0);
  };

  const hasActiveFilters = Boolean(
    statusFilter || formatFilter || supplierFilter.trim() || searchInput.trim(),
  );

  const rangeStart = total === 0 ? 0 : offset + 1;
  const rangeEnd = Math.min(offset + invoices.length, total);

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-xl font-bold tracking-tight text-slate-900">
            Reconciliation Queue
          </h1>
          <p className="mt-0.5 text-sm text-slate-600">
            Monitor deterministic 3-way PO / GRN / Invoice matching and resolve
            flagged discrepancies.
          </p>
        </div>

        <div className="flex flex-wrap items-center gap-2.5">
          <button
            type="button"
            onClick={() => void refreshAll()}
            className="inline-flex items-center gap-1.5 rounded-lg border border-slate-300 bg-white px-3 py-1.5 text-xs font-semibold text-slate-700 hover:bg-slate-50 transition-colors"
          >
            <RefreshCw
              className={`h-3.5 w-3.5 ${queueLoading || metricsLoading ? "animate-spin" : ""}`}
            />
            <span>Refresh</span>
          </button>
          <AccountingExportMenu mode="batch" filteredInvoices={invoices} />
        </div>
      </div>

      <MetricsOverview
        metrics={metrics}
        loading={metricsLoading}
        activeStatus={statusFilter}
        onSelectStatus={handleSelectStatus}
      />

      <InvoiceUploadPanel
        onUploaded={() => {
          void refreshAll();
          if (followUpTimerRef.current !== null) {
            window.clearTimeout(followUpTimerRef.current);
          }
          followUpTimerRef.current = window.setTimeout(() => {
            void refreshAll();
          }, 1500);
        }}
      />

      <section className="rounded-xl border border-slate-200 bg-white shadow-sm">
        <div className="border-b border-slate-200 p-4 space-y-3">
          {/* Status filter pills */}
          <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label="Filter invoices by status">
            <button
              type="button"
              aria-pressed={statusFilter === ""}
              onClick={() => handleSelectStatus("")}
              className={`inline-flex items-center gap-1.5 rounded-full border px-3 py-1 text-xs font-semibold transition-colors ${
                statusFilter === ""
                  ? "border-slate-900 bg-slate-900 text-white"
                  : "border-slate-300 bg-white text-slate-700 hover:bg-slate-50"
              }`}
            >
              <span>ALL</span>
              <span className="rounded-full bg-black/10 px-1.5 py-0.2 font-mono text-[10px] tabular-nums">
                {metrics?.total_invoices ?? total}
              </span>
            </button>

            {ALL_INVOICE_STATUSES.map((st) => {
              const count = metrics?.counts_by_status[st] ?? 0;
              const selected = statusFilter === st;
              return (
                <button
                  key={st}
                  type="button"
                  aria-pressed={selected}
                  onClick={() => handleSelectStatus(selected ? "" : st)}
                  className={`inline-flex items-center gap-1.5 rounded-full border px-3 py-1 font-mono text-[11px] font-medium transition-colors ${
                    selected
                      ? "border-slate-900 bg-slate-900 text-white"
                      : "border-slate-300 bg-slate-50 text-slate-700 hover:bg-slate-100"
                  }`}
                >
                  <span>{st}</span>
                  <span
                    className={`rounded-full px-1.5 py-0.2 text-[10px] tabular-nums ${
                      selected
                        ? "bg-white/20 text-white"
                        : "bg-slate-200 text-slate-700"
                    }`}
                  >
                    {count}
                  </span>
                </button>
              );
            })}
          </div>

          {/* Search & secondary filter controls */}
          <div className="flex flex-wrap items-center gap-3">
            <div className="relative flex-1 min-w-[240px]">
              <label htmlFor="invoice-queue-search" className="visually-hidden">
                Search invoice number, PO number, or supplier
              </label>
              <Search aria-hidden="true" className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" />
              <input
                id="invoice-queue-search"
                type="search"
                value={searchInput}
                onChange={(e) => setSearchInput(e.target.value)}
                placeholder="Search invoice #, PO #, supplier code or name..."
                className="w-full rounded-lg border border-slate-300 bg-white py-2 pl-9 pr-3 text-xs text-slate-900 placeholder:text-slate-400 focus:border-slate-500"
              />
            </div>

            <label htmlFor="invoice-format-filter" className="visually-hidden">
              Source format
            </label>
            <select
              id="invoice-format-filter"
              value={formatFilter}
              onChange={(e) => {
                setFormatFilter(e.target.value as SourceFormat | "");
                setOffset(0);
              }}
              className="rounded-lg border border-slate-300 bg-white px-3 py-2 text-xs font-medium text-slate-700 focus:border-slate-500"
            >
              <option value="">All Formats</option>
              {ALL_SOURCE_FORMATS.map((fmt) => (
                <option key={fmt} value={fmt}>
                  {fmt}
                </option>
              ))}
            </select>

            <label htmlFor="invoice-supplier-filter" className="visually-hidden">
              Supplier code
            </label>
            <input
              id="invoice-supplier-filter"
              type="text"
              value={supplierFilter}
              onChange={(e) => {
                setSupplierFilter(e.target.value);
                setOffset(0);
              }}
              placeholder="Supplier code (e.g. SUP-ACME-01)"
              className="w-52 rounded-lg border border-slate-300 bg-white px-3 py-2 font-mono text-xs text-slate-900 placeholder:font-sans placeholder:text-slate-400 focus:border-slate-500"
            />

            {hasActiveFilters ? (
              <button
                type="button"
                onClick={clearFilters}
                className="inline-flex items-center gap-1 rounded-lg border border-slate-300 bg-slate-50 px-3 py-2 text-xs font-medium text-slate-700 hover:bg-slate-100 transition-colors"
              >
                <FilterX aria-hidden="true" className="h-3.5 w-3.5" />
                <span>Clear</span>
              </button>
            ) : null}
          </div>
        </div>

        {errorMsg ? (
          <div role="alert" className="m-4 flex items-center gap-2 rounded-lg border border-rose-200 bg-rose-50 px-4 py-3 text-xs text-rose-800">
            <AlertCircle aria-hidden="true" className="h-4 w-4 shrink-0 text-rose-600" />
            <span>{errorMsg}</span>
          </div>
        ) : null}

        <div
          tabIndex={0}
          role="region"
          aria-labelledby="invoice-queue-caption"
          className="table-wrapper"
        >
          <table className="min-w-full divide-y divide-slate-200 text-left text-xs">
            <caption id="invoice-queue-caption" className="visually-hidden">
              Invoice reconciliation queue
            </caption>
            <thead className="bg-slate-50 text-[11px] font-semibold uppercase tracking-wider text-slate-500">
              <tr>
                <th scope="col" className="border-b border-slate-200 bg-slate-50 px-4 py-3">Invoice #</th>
                <th scope="col" className="border-b border-slate-200 bg-slate-50 px-4 py-3">Status</th>
                <th scope="col" className="border-b border-slate-200 bg-slate-50 px-4 py-3">Format</th>
                <th scope="col" className="border-b border-slate-200 bg-slate-50 px-4 py-3">Supplier</th>
                <th scope="col" className="border-b border-slate-200 bg-slate-50 px-4 py-3">PO Reference</th>
                <th scope="col" className="border-b border-slate-200 bg-slate-50 px-4 py-3">Issue Date</th>
                <th scope="col" className="border-b border-slate-200 bg-slate-50 px-4 py-3 text-right">Grand Total</th>
                <th scope="col" className="border-b border-slate-200 bg-slate-50 px-4 py-3">Findings</th>
                <th scope="col" className="border-b border-slate-200 bg-slate-50 px-4 py-3 text-right">Action</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100 bg-white">
              {queueLoading && invoices.length === 0 ? (
                <tr>
                  <td
                    colSpan={9}
                    className="px-4 py-12 text-center text-sm text-slate-500"
                  >
                    Loading reconciliation queue...
                  </td>
                </tr>
              ) : invoices.length === 0 ? (
                <tr>
                  <td
                    colSpan={9}
                    className="px-4 py-12 text-center text-sm text-slate-500"
                  >
                    No invoices match the current filter criteria.
                  </td>
                </tr>
              ) : (
                invoices.map((inv) => {
                  const discCnt = inv.discrepancy_count ?? 0;
                  const isReview = inv.status === "NEEDS_REVIEW";
                  const invoiceLabel =
                    inv.invoice_number || `unparsed invoice ${inv.id.slice(0, 8)}`;

                  return (
                    <tr
                      key={inv.id}
                      className={`hover:bg-slate-50/90 transition-colors ${
                        isReview ? "bg-amber-50/20" : ""
                      }`}
                    >
                      <th scope="row" className="px-4 py-3 whitespace-nowrap font-normal">
                        <Link
                          href={`/invoices/${inv.id}`}
                          className="font-mono font-semibold text-slate-900 hover:text-indigo-700 hover:underline"
                        >
                          {inv.invoice_number || `(unparsed ${inv.id.slice(0, 8)})`}
                        </Link>
                        <div className="mt-0.5 font-mono text-[10px] text-slate-400">
                          {inv.id.slice(0, 8)}…
                        </div>
                      </th>

                      <td className="px-4 py-3 whitespace-nowrap">
                        <StatusBadge status={inv.status} size="xs" />
                      </td>

                      <td className="px-4 py-3 whitespace-nowrap">
                        <SourceFormatBadge format={inv.source_format} />
                      </td>

                      <td className="px-4 py-3">
                        <div className="font-mono font-medium text-slate-800">
                          {inv.supplier_code_raw || "—"}
                        </div>
                        {inv.supplier_name ? (
                          <div className="max-w-[210px] truncate text-[11px] text-slate-500">
                            {inv.supplier_name}
                          </div>
                        ) : null}
                      </td>

                      <td className="px-4 py-3 whitespace-nowrap font-mono text-slate-700">
                        {inv.po_number_raw || "—"}
                      </td>

                      <td className="px-4 py-3 whitespace-nowrap font-mono text-slate-600 tabular-nums">
                        {inv.issue_date ? inv.issue_date.slice(0, 10) : "—"}
                      </td>

                      <td className="px-4 py-3 whitespace-nowrap text-right">
                        <div className="font-mono font-semibold text-slate-900 tabular-nums">
                          {formatPenceCurrency(
                            inv.grand_total_pence,
                            inv.currency,
                          )}
                        </div>
                        <div className="font-mono text-[10px] text-slate-400 tabular-nums">
                          {inv.grand_total_pence}p
                        </div>
                      </td>

                      <td className="px-4 py-3">
                        {inv.parse_error ? (
                          <span
                            title={inv.parse_error}
                            className="inline-block max-w-[230px] truncate rounded border border-rose-200 bg-rose-50 px-2 py-0.5 text-[11px] text-rose-800"
                          >
                            {inv.parse_error}
                          </span>
                        ) : discCnt > 0 ? (
                          <span className="inline-flex items-center rounded-full border border-amber-300 bg-amber-50 px-2 py-0.5 font-mono text-[11px] font-semibold text-amber-800 tabular-nums">
                            {discCnt} {discCnt === 1 ? "discrepancy" : "discrepancies"}
                          </span>
                        ) : (
                          <span className="text-slate-400">—</span>
                        )}
                      </td>

                      <td className="px-4 py-3 whitespace-nowrap text-right">
                        <Link
                          href={`/invoices/${inv.id}`}
                          className={`inline-flex items-center gap-1 rounded-md px-2.5 py-1 text-xs font-semibold transition-colors ${
                            isReview
                              ? "bg-amber-600 text-white hover:bg-amber-700"
                              : "border border-slate-300 bg-white text-slate-700 hover:bg-slate-100"
                          }`}
                        >
                          <span>{isReview ? "Review" : "Inspect"}</span>
                          <span className="visually-hidden"> {invoiceLabel}</span>
                          <ArrowRight aria-hidden="true" className="h-3 w-3" />
                        </Link>
                      </td>
                    </tr>
                  );
                })
              )}
            </tbody>
          </table>
        </div>

        {/* Pagination Footer */}
        <div className="flex flex-wrap items-center justify-between gap-3 border-t border-slate-200 px-4 py-3 text-xs text-slate-600">
          <div className="tabular-nums">
            Showing <span className="font-semibold text-slate-900">{rangeStart}</span>
            –<span className="font-semibold text-slate-900">{rangeEnd}</span> of{" "}
            <span className="font-semibold text-slate-900">{total}</span> invoices
          </div>
          <div className="flex items-center gap-2">
            <button
              type="button"
              disabled={offset === 0 || queueLoading}
              onClick={() => setOffset(Math.max(0, offset - PAGE_LIMIT))}
              className="inline-flex items-center gap-1 rounded-md border border-slate-300 bg-white px-2.5 py-1 font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-40"
            >
              <ChevronLeft className="h-3.5 w-3.5" />
              <span>Previous</span>
            </button>
            <button
              type="button"
              disabled={offset + PAGE_LIMIT >= total || queueLoading}
              onClick={() => setOffset(offset + PAGE_LIMIT)}
              className="inline-flex items-center gap-1 rounded-md border border-slate-300 bg-white px-2.5 py-1 font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-40"
            >
              <span>Next</span>
              <ChevronRight className="h-3.5 w-3.5" />
            </button>
          </div>
        </div>
      </section>
    </div>
  );
}
