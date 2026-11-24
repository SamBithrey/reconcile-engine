"use client";

import { useEffect, useMemo, useState } from "react";
import {
  AlertCircle,
  Check,
  Code2,
  Copy,
  Download,
  ExternalLink,
  FileText,
  Loader2,
  Table2,
} from "lucide-react";

import { SourceFormatBadge } from "@/components/StatusBadge";
import { fetchInvoiceFileText, getInvoiceFileUrl } from "@/lib/api";
import { parseCsvForPreview } from "@/lib/csv-preview";
import { getFormatMeta } from "@/lib/discrepancies";
import type { Invoice } from "@/lib/types";

/**
 * Deterministic pretty-printer for UBL XML so operators can inspect raw tags
 * safely without HTML injection.
 */
function formatXmlLines(rawXml: string): string[] {
  const normalized = rawXml
    .replace(/\r\n/g, "\n")
    .replace(/>\s*</g, ">\n<")
    .trim();

  if (!normalized) {
    return [];
  }

  const rawLines = normalized.split("\n");
  const out: string[] = [];
  let indent = 0;

  for (const rawLine of rawLines) {
    const line = rawLine.trim();
    if (!line) {
      continue;
    }

    const isClosingTag = /^<\//.test(line);
    const isProcessingOrDecl = /^<\?|^<!/.test(line);
    const isSelfClosing = /\/>$/.test(line);
    const hasInlineClose = /^<[^/!?][^>]*>.*<\/[^>]+>$/.test(line);

    if (isClosingTag) {
      indent = Math.max(0, indent - 1);
    }

    out.push(`${"  ".repeat(indent)}${line}`);

    if (
      !isClosingTag &&
      !isProcessingOrDecl &&
      !isSelfClosing &&
      !hasInlineClose &&
      /^<[^/!?]/.test(line)
    ) {
      indent += 1;
    }
  }

  return out;
}

function renderXmlLineTokens(line: string) {
  const indentMatch = line.match(/^(\s*)/);
  const leading = indentMatch ? indentMatch[1] : "";
  const content = line.slice(leading.length);

  // Highlight pure element lines like <cbc:ID>INV-001</cbc:ID> cleanly in JSX.
  const wrapped = content.match(/^(<[^/!?][^>]*>)([^<]+)(<\/[^>]+>)$/);
  if (wrapped) {
    return (
      <>
        <span>{leading}</span>
        <span className="text-indigo-700">{wrapped[1]}</span>
        <span className="font-semibold text-slate-900">{wrapped[2]}</span>
        <span className="text-indigo-700">{wrapped[3]}</span>
      </>
    );
  }

  if (content.startsWith("<")) {
    return (
      <>
        <span>{leading}</span>
        <span className="text-indigo-700">{content}</span>
      </>
    );
  }

  return <span>{line}</span>;
}

export function SourceDocumentViewer({ invoice }: { invoice: Invoice }) {
  const fileUrl = getInvoiceFileUrl(invoice.id);
  const formatMeta = getFormatMeta(invoice.source_format);
  const downloadFilename = `${invoice.invoice_number || invoice.id}.${formatMeta.extension}`;

  const [textContent, setTextContent] = useState<string | null>(null);
  const [sizeBytes, setSizeBytes] = useState<number | null>(null);
  const [loadingText, setLoadingText] = useState(false);
  const [textError, setTextError] = useState<string | null>(null);

  const [xmlView, setXmlView] = useState<"formatted" | "raw">("formatted");
  const [csvView, setCsvView] = useState<"table" | "raw">("table");
  const [copiedSha, setCopiedSha] = useState(false);
  const [copiedBody, setCopiedBody] = useState(false);

  const isTextFormat =
    invoice.source_format === "UBL_XML" || invoice.source_format === "CSV";

  useEffect(() => {
    if (!isTextFormat) {
      setTextContent(null);
      setSizeBytes(null);
      setTextError(null);
      return;
    }

    let cancelled = false;
    setLoadingText(true);
    setTextError(null);

    void fetchInvoiceFileText(invoice.id)
      .then((res) => {
        if (!cancelled) {
          setTextContent(res.text);
          setSizeBytes(res.sizeBytes);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setTextError(
            err instanceof Error
              ? err.message
              : "Failed to load source file from storage",
          );
        }
      })
      .finally(() => {
        if (!cancelled) {
          setLoadingText(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [invoice.id, isTextFormat]);

  const xmlLines = useMemo(() => {
    if (invoice.source_format !== "UBL_XML" || textContent === null) {
      return [];
    }
    return xmlView === "formatted"
      ? formatXmlLines(textContent)
      : textContent.replace(/\r\n/g, "\n").split("\n");
  }, [invoice.source_format, textContent, xmlView]);

  const parsedCsv = useMemo(() => {
    if (invoice.source_format !== "CSV" || textContent === null) {
      return null;
    }
    return parseCsvForPreview(textContent);
  }, [invoice.source_format, textContent]);

  const copyText = async (value: string, kind: "sha" | "body") => {
    try {
      await navigator.clipboard.writeText(value);
      if (kind === "sha") {
        setCopiedSha(true);
        window.setTimeout(() => setCopiedSha(false), 1500);
      } else {
        setCopiedBody(true);
        window.setTimeout(() => setCopiedBody(false), 1500);
      }
    } catch {
      // Clipboard API may be unavailable in restricted contexts.
    }
  };

  return (
    <section className="overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm">
      <div className="border-b border-slate-200 bg-slate-50/80 px-4 py-3 space-y-2.5">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-2">
            <FileText className="h-4 w-4 text-slate-600" />
            <h2 className="text-sm font-semibold text-slate-900">
              Source Document
            </h2>
            <SourceFormatBadge format={invoice.source_format} />
          </div>

          <div className="flex items-center gap-2">
            <a
              href={fileUrl}
              target="_blank"
              rel="noreferrer"
              className="inline-flex items-center gap-1 rounded-md border border-slate-300 bg-white px-2.5 py-1 text-xs font-medium text-slate-700 hover:bg-slate-50 transition-colors"
            >
              <ExternalLink className="h-3 w-3" />
              <span>Open</span>
            </a>
            <a
              href={fileUrl}
              download={downloadFilename}
              className="inline-flex items-center gap-1 rounded-md bg-slate-900 px-2.5 py-1 text-xs font-medium text-white hover:bg-slate-800 transition-colors"
            >
              <Download className="h-3 w-3" />
              <span>Download</span>
            </a>
          </div>
        </div>

        <div className="flex flex-wrap items-center justify-between gap-2 text-[11px] text-slate-600">
          <div className="truncate font-mono" title={invoice.storage_key}>
            <span className="text-slate-400">key:</span> {invoice.storage_key}
          </div>
          <div className="flex items-center gap-2">
            {sizeBytes !== null ? (
              <span className="font-mono text-slate-500 tabular-nums">
                {sizeBytes.toLocaleString("en-GB")} bytes
              </span>
            ) : null}
            <button
              type="button"
              onClick={() => void copyText(invoice.file_sha256, "sha")}
              title={`Copy SHA-256: ${invoice.file_sha256}`}
              className="inline-flex items-center gap-1 rounded border border-slate-200 bg-white px-2 py-0.5 font-mono text-[10px] text-slate-600 hover:bg-slate-100"
            >
              {copiedSha ? (
                <Check className="h-3 w-3 text-emerald-600" />
              ) : (
                <Copy className="h-3 w-3" />
              )}
              <span>sha256:{invoice.file_sha256.slice(0, 12)}…</span>
            </button>
          </div>
        </div>
      </div>

      {/* Format-specific preview area */}
      {invoice.source_format === "PDF" ? (
        <div className="bg-slate-100">
          <iframe
            src={fileUrl}
            title={`Invoice PDF ${invoice.invoice_number || invoice.id}`}
            className="h-[720px] w-full border-0 bg-white"
          />
          <div className="flex items-center justify-between border-t border-slate-200 bg-slate-50 px-3 py-2 text-[11px] text-slate-500">
            <span>Served directly from MinIO via GET /api/v1/invoices/{invoice.id.slice(0, 8)}…/file</span>
            <a
              href={fileUrl}
              target="_blank"
              rel="noreferrer"
              className="font-medium text-indigo-700 hover:underline"
            >
              Open PDF full-screen
            </a>
          </div>
        </div>
      ) : loadingText ? (
        <div className="flex h-80 items-center justify-center gap-2 text-xs text-slate-500">
          <Loader2 className="h-4 w-4 animate-spin" />
          <span>Fetching original file from object storage...</span>
        </div>
      ) : textError ? (
        <div role="alert" className="m-4 flex items-start gap-2 rounded-lg border border-rose-200 bg-rose-50 p-3 text-xs text-rose-800">
          <AlertCircle aria-hidden="true" className="mt-0.5 h-4 w-4 shrink-0 text-rose-600" />
          <div>{textError}</div>
        </div>
      ) : invoice.source_format === "UBL_XML" && textContent !== null ? (
        <div>
          <div className="flex items-center justify-between border-b border-slate-200 bg-white px-3 py-2">
            <div className="inline-flex rounded-md border border-slate-200 bg-slate-50 p-0.5 text-[11px]" role="group" aria-label="XML view mode">
              <button
                type="button"
                aria-pressed={xmlView === "formatted"}
                onClick={() => setXmlView("formatted")}
                className={`inline-flex items-center gap-1 rounded px-2 py-1 font-medium ${
                  xmlView === "formatted"
                    ? "bg-white text-slate-900 shadow-sm"
                    : "text-slate-600 hover:text-slate-900"
                }`}
              >
                <Code2 aria-hidden="true" className="h-3 w-3" />
                <span>Formatted XML</span>
              </button>
              <button
                type="button"
                aria-pressed={xmlView === "raw"}
                onClick={() => setXmlView("raw")}
                className={`rounded px-2 py-1 font-medium ${
                  xmlView === "raw"
                    ? "bg-white text-slate-900 shadow-sm"
                    : "text-slate-600 hover:text-slate-900"
                }`}
              >
                Raw
              </button>
            </div>

            <button
              type="button"
              onClick={() => void copyText(textContent, "body")}
              className="inline-flex items-center gap-1 rounded border border-slate-200 px-2 py-1 text-[11px] font-medium text-slate-600 hover:bg-slate-50"
            >
              {copiedBody ? (
                <Check className="h-3 w-3 text-emerald-600" />
              ) : (
                <Copy className="h-3 w-3" />
              )}
              <span>{copiedBody ? "Copied" : "Copy XML"}</span>
            </button>
          </div>

          <div className="max-h-[700px] overflow-auto bg-slate-50/40 p-3 font-mono text-[11px] leading-5">
            {xmlLines.map((line, idx) => (
              <div key={idx} className="flex gap-3 hover:bg-slate-100/80 px-1 rounded">
                <span className="w-7 shrink-0 select-none text-right text-slate-400 tabular-nums">
                  {idx + 1}
                </span>
                <span className="whitespace-pre text-slate-800">
                  {xmlView === "formatted" ? renderXmlLineTokens(line) : line}
                </span>
              </div>
            ))}
          </div>
        </div>
      ) : invoice.source_format === "CSV" && textContent !== null ? (
        <div>
          <div className="flex items-center justify-between border-b border-slate-200 bg-white px-3 py-2">
            <div className="inline-flex rounded-md border border-slate-200 bg-slate-50 p-0.5 text-[11px]" role="group" aria-label="CSV view mode">
              <button
                type="button"
                aria-pressed={csvView === "table"}
                onClick={() => setCsvView("table")}
                className={`inline-flex items-center gap-1 rounded px-2 py-1 font-medium ${
                  csvView === "table"
                    ? "bg-white text-slate-900 shadow-sm"
                    : "text-slate-600 hover:text-slate-900"
                }`}
              >
                <Table2 aria-hidden="true" className="h-3 w-3" />
                <span>Parsed Table</span>
              </button>
              <button
                type="button"
                aria-pressed={csvView === "raw"}
                onClick={() => setCsvView("raw")}
                className={`rounded px-2 py-1 font-medium ${
                  csvView === "raw"
                    ? "bg-white text-slate-900 shadow-sm"
                    : "text-slate-600 hover:text-slate-900"
                }`}
              >
                Raw CSV
              </button>
            </div>

            <button
              type="button"
              onClick={() => void copyText(textContent, "body")}
              className="inline-flex items-center gap-1 rounded border border-slate-200 px-2 py-1 text-[11px] font-medium text-slate-600 hover:bg-slate-50"
            >
              {copiedBody ? (
                <Check aria-hidden="true" className="h-3 w-3 text-emerald-600" />
              ) : (
                <Copy aria-hidden="true" className="h-3 w-3" />
              )}
              <span>{copiedBody ? "Copied" : "Copy CSV"}</span>
            </button>
          </div>

          {csvView === "table" && parsedCsv && !parsedCsv.error ? (
            <div
              tabIndex={0}
              role="region"
              aria-labelledby="csv-source-table-caption"
              className="table-wrapper max-h-[700px]"
            >
              <table className="min-w-full divide-y divide-slate-200 text-left font-mono text-[11px]">
                <caption id="csv-source-table-caption" className="visually-hidden">
                  Parsed CSV invoice manifest rows
                </caption>
                <thead className="bg-slate-100 text-slate-700">
                  <tr>
                    <th scope="col" className="border-b border-slate-200 bg-slate-100 px-2.5 py-2 text-slate-500">#</th>
                    {parsedCsv.headers.map((header, idx) => (
                      <th scope="col" key={idx} className="border-b border-slate-200 bg-slate-100 px-2.5 py-2 font-semibold whitespace-nowrap">
                        {header}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-100 bg-white">
                  {parsedCsv.rows.map((row, rIdx) => (
                    <tr key={rIdx} className="hover:bg-slate-50">
                      <th scope="row" className="px-2.5 py-1.5 font-normal text-slate-400 tabular-nums">
                        {rIdx + 1}
                      </th>
                      {parsedCsv.headers.map((_, cIdx) => (
                        <td
                          key={cIdx}
                          className="px-2.5 py-1.5 text-slate-800 whitespace-nowrap"
                        >
                          {row[cIdx] ?? ""}
                        </td>
                      ))}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <div className="max-h-[700px] overflow-auto bg-slate-50/40 p-3 font-mono text-[11px] leading-5">
              {parsedCsv?.error && csvView === "table" ? (
                <div className="mb-2 rounded border border-amber-200 bg-amber-50 px-2.5 py-1.5 font-sans text-xs text-amber-800">
                  {parsedCsv.error} — showing raw CSV source below.
                </div>
              ) : null}
              {textContent
                .replace(/\r\n/g, "\n")
                .split("\n")
                .map((line, idx) => (
                  <div key={idx} className="flex gap-3 hover:bg-slate-100/80 px-1 rounded">
                    <span className="w-7 shrink-0 select-none text-right text-slate-400 tabular-nums">
                      {idx + 1}
                    </span>
                    <span className="whitespace-pre text-slate-800">{line}</span>
                  </div>
                ))}
            </div>
          )}
        </div>
      ) : null}
    </section>
  );
}
