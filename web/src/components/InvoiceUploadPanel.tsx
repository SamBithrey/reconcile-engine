"use client";

import Link from "next/link";
import { useRef, useState } from "react";
import {
  AlertCircle,
  ArrowRight,
  CheckCircle2,
  CopyCheck,
  Loader2,
  UploadCloud,
} from "lucide-react";

import { SourceFormatBadge, StatusBadge } from "@/components/StatusBadge";
import { ApiError, uploadInvoice } from "@/lib/api";
import type { UploadInvoiceResult } from "@/lib/types";

const MAX_UPLOAD_BYTES = 10 * 1024 * 1024; // 10 MB backend ceiling

interface InvoiceUploadPanelProps {
  onUploaded: (result: UploadInvoiceResult) => void;
}

export function InvoiceUploadPanel({ onUploaded }: InvoiceUploadPanelProps) {
  const inputRef = useRef<HTMLInputElement | null>(null);
  const [dragging, setDragging] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [selectedName, setSelectedName] = useState<string | null>(null);
  const [lastResult, setLastResult] = useState<UploadInvoiceResult | null>(
    null,
  );
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const handleFile = async (file: File) => {
    setErrorMsg(null);
    setLastResult(null);
    setSelectedName(file.name);

    if (file.size === 0) {
      setErrorMsg("Selected invoice file is empty.");
      return;
    }
    if (file.size > MAX_UPLOAD_BYTES) {
      setErrorMsg("Invoice file exceeds the 10 MB maximum upload size.");
      return;
    }

    setUploading(true);
    try {
      const result = await uploadInvoice(file);
      setLastResult(result);
      onUploaded(result);
    } catch (err) {
      if (err instanceof ApiError) {
        setErrorMsg(`Upload rejected (HTTP ${err.status}): ${err.message}`);
      } else {
        setErrorMsg(
          err instanceof Error ? err.message : "Failed to upload invoice",
        );
      }
    } finally {
      setUploading(false);
      if (inputRef.current) {
        inputRef.current.value = "";
      }
    }
  };

  return (
    <section className="rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
      <div className="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
        <div>
          <h2 className="text-sm font-semibold text-slate-900">
            Ingest Supplier Invoice
          </h2>
          <p className="mt-0.5 text-xs text-slate-500">
            Upload <span className="font-mono font-medium">UBL_XML</span>,
            native <span className="font-mono font-medium">PDF</span>, or{" "}
            <span className="font-mono font-medium">CSV</span> manifests (max 10
            MB). Identical bytes deduplicate automatically via SHA-256.
          </p>
        </div>

        <div
          onDragOver={(e) => {
            e.preventDefault();
            setDragging(true);
          }}
          onDragLeave={() => setDragging(false)}
          onDrop={(e) => {
            e.preventDefault();
            setDragging(false);
            const file = e.dataTransfer.files?.[0];
            if (file) {
              void handleFile(file);
            }
          }}
          className={`flex flex-1 max-w-2xl items-center justify-between gap-4 rounded-lg border-2 border-dashed px-4 py-3 transition-colors ${
            dragging
              ? "border-slate-900 bg-slate-100"
              : "border-slate-300 bg-slate-50/70 hover:border-slate-400"
          }`}
        >
          <div className="flex items-center gap-3 min-w-0">
            <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-slate-900 text-white">
              {uploading ? (
                <Loader2 className="h-4 w-4 animate-spin" />
              ) : (
                <UploadCloud className="h-4 w-4" />
              )}
            </div>
            <div className="min-w-0">
              <p className="truncate text-xs font-medium text-slate-800">
                {uploading
                  ? `Uploading ${selectedName ?? "invoice"}...`
                  : "Drag & drop .xml, .pdf, or .csv invoice here"}
              </p>
              <p className="text-[11px] text-slate-500">
                POST /api/v1/invoices · Content-addressed MinIO + RabbitMQ
              </p>
            </div>
          </div>

          <input
            ref={inputRef}
            type="file"
            accept=".xml,.pdf,.csv,application/xml,text/xml,application/pdf,text/csv"
            disabled={uploading}
            onChange={(e) => {
              const file = e.target.files?.[0];
              if (file) {
                void handleFile(file);
              }
            }}
            className="hidden"
          />
          <button
            type="button"
            disabled={uploading}
            onClick={() => inputRef.current?.click()}
            className="shrink-0 rounded-lg bg-slate-900 px-3.5 py-2 text-xs font-semibold text-white hover:bg-slate-800 disabled:opacity-50 transition-colors"
          >
            {uploading ? "Uploading..." : "Select File"}
          </button>
        </div>
      </div>

      {errorMsg ? (
        <div
          role="alert"
          className="mt-3 flex items-start gap-2.5 rounded-lg border border-rose-200 bg-rose-50 px-3.5 py-2.5 text-xs text-rose-800"
        >
          <AlertCircle aria-hidden="true" className="mt-0.5 h-4 w-4 shrink-0 text-rose-600" />
          <div>
            <span className="font-semibold">Upload failed: </span>
            <span>{errorMsg}</span>
          </div>
        </div>
      ) : null}

      {lastResult ? (
        lastResult.data.duplicate ? (
          <div
            aria-live="polite"
            className="mt-3 flex flex-wrap items-center justify-between gap-3 rounded-lg border border-indigo-200 bg-indigo-50 px-3.5 py-2.5 text-xs text-indigo-950"
          >
            <div className="flex flex-wrap items-center gap-2.5">
              <CopyCheck aria-hidden="true" className="h-4 w-4 shrink-0 text-indigo-700" />
              <span className="font-semibold">
                Duplicate Detected (HTTP {lastResult.httpStatus} OK — SHA-256
                Deduplicated):
              </span>
              <span className="text-indigo-800">
                Identical file already exists in the ledger; no duplicate
                pipeline job was queued.
              </span>
              <SourceFormatBadge format={lastResult.data.source_format} />
              <StatusBadge status={lastResult.data.status} size="xs" />
              <span
                title={lastResult.data.file_sha256}
                className="font-mono text-[11px] text-indigo-700"
              >
                sha256:{lastResult.data.file_sha256.slice(0, 12)}…
              </span>
            </div>
            <Link
              href={`/invoices/${lastResult.data.invoice_id}`}
              className="inline-flex items-center gap-1 rounded-md bg-indigo-700 px-2.5 py-1 text-xs font-semibold text-white hover:bg-indigo-800 transition-colors"
            >
              <span>Open Existing Invoice</span>
              <ArrowRight aria-hidden="true" className="h-3.5 w-3.5" />
            </Link>
          </div>
        ) : (
          <div
            aria-live="polite"
            className="mt-3 flex flex-wrap items-center justify-between gap-3 rounded-lg border border-emerald-200 bg-emerald-50 px-3.5 py-2.5 text-xs text-emerald-950"
          >
            <div className="flex flex-wrap items-center gap-2.5">
              <CheckCircle2 aria-hidden="true" className="h-4 w-4 shrink-0 text-emerald-700" />
              <span className="font-semibold">
                Invoice Queued (HTTP {lastResult.httpStatus} Accepted):
              </span>
              <SourceFormatBadge format={lastResult.data.source_format} />
              <StatusBadge status={lastResult.data.status} size="xs" />
              <span
                title={lastResult.data.file_sha256}
                className="font-mono text-[11px] text-emerald-800"
              >
                sha256:{lastResult.data.file_sha256.slice(0, 12)}…
              </span>
            </div>
            <Link
              href={`/invoices/${lastResult.data.invoice_id}`}
              className="inline-flex items-center gap-1 rounded-md bg-emerald-700 px-2.5 py-1 text-xs font-semibold text-white hover:bg-emerald-800 transition-colors"
            >
              <span>Inspect Invoice</span>
              <ArrowRight aria-hidden="true" className="h-3.5 w-3.5" />
            </Link>
          </div>
        )
      ) : null}
    </section>
  );
}
