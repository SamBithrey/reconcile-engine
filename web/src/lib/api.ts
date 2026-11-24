import {
  DemoStoreError,
  demoFetchHealth,
  demoFetchInvoiceDetail,
  demoFetchInvoiceFileText,
  demoFetchInvoices,
  demoFetchMetrics,
  demoGetInvoiceFileBlob,
  demoResolveInvoice,
  demoUploadInvoice,
  isDemoMode,
  resetDemoStore,
} from "./demo-data.ts";
import type {
  HealthResponse,
  Invoice,
  InvoiceDetail,
  InvoiceListResponse,
  InvoiceStatus,
  MetricsSummary,
  ResolveInvoiceRequest,
  SourceFormat,
  UploadInvoiceResponse,
  UploadInvoiceResult,
} from "./types.ts";

export { isDemoMode, resetDemoStore };

const DEFAULT_API_BASE_URL = "http://localhost:8080";
const DEFAULT_REQUEST_TIMEOUT_MS = 15_000;

export function getApiBaseUrl(): string {
  const configured = process.env.NEXT_PUBLIC_API_BASE_URL?.trim();
  const base = configured && configured.length > 0 ? configured : DEFAULT_API_BASE_URL;
  return base.replace(/\/+$/, "");
}

export class ApiError extends Error {
  readonly status: number;
  readonly isConflict: boolean;
  readonly isNotFound: boolean;

  constructor(status: number, message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = "ApiError";
    this.status = status;
    this.isConflict = status === 409;
    this.isNotFound = status === 404;
  }
}

function wrapDemoCall<T>(fn: () => T): T {
  try {
    return fn();
  } catch (err) {
    if (err instanceof DemoStoreError) {
      throw new ApiError(err.status, err.message, { cause: err });
    }
    throw err;
  }
}

function buildTimeoutSignal(
  userSignal?: AbortSignal | null,
  timeoutMs = DEFAULT_REQUEST_TIMEOUT_MS,
): AbortSignal {
  const timeoutSignal = AbortSignal.timeout(timeoutMs);
  if (userSignal) {
    return AbortSignal.any([userSignal, timeoutSignal]);
  }
  return timeoutSignal;
}

async function fetchWithDeadline(
  url: string,
  init?: RequestInit,
  timeoutMs = DEFAULT_REQUEST_TIMEOUT_MS,
): Promise<Response> {
  try {
    return await fetch(url, {
      ...init,
      signal: buildTimeoutSignal(init?.signal, timeoutMs),
    });
  } catch (err) {
    if (err instanceof DOMException && err.name === "TimeoutError") {
      throw new ApiError(
        408,
        `Request timed out after ${timeoutMs}ms`,
        { cause: err },
      );
    }
    throw err;
  }
}

async function parseErrorMessage(res: Response): Promise<string> {
  const fallback = `HTTP ${res.status} ${res.statusText || "Request failed"}`.trim();
  try {
    const contentType = res.headers.get("content-type") ?? "";
    if (contentType.includes("application/json")) {
      const body = (await res.json()) as { error?: string; message?: string };
      if (body.error) {
        return body.error;
      }
      if (body.message) {
        return body.message;
      }
    } else {
      const text = (await res.text()).trim();
      if (text) {
        return text;
      }
    }
  } catch {
    // Ignore parse errors and return status fallback.
  }
  return fallback;
}

async function requestJson<T>(path: string, init?: RequestInit): Promise<T> {
  const url = `${getApiBaseUrl()}${path}`;
  const res = await fetchWithDeadline(url, {
    ...init,
    cache: "no-store",
  });

  if (!res.ok) {
    throw new ApiError(res.status, await parseErrorMessage(res));
  }

  return (await res.json()) as T;
}

export async function fetchHealth(): Promise<HealthResponse> {
  if (isDemoMode()) {
    return wrapDemoCall(() => demoFetchHealth());
  }
  const url = `${getApiBaseUrl()}/healthz`;
  const res = await fetchWithDeadline(url, { cache: "no-store" }, 5_000);
  const text = await res.text();
  try {
    return JSON.parse(text) as HealthResponse;
  } catch (err) {
    throw new ApiError(
      res.status,
      text || "Failed to parse health response",
      { cause: err },
    );
  }
}

export async function fetchMetrics(): Promise<MetricsSummary> {
  if (isDemoMode()) {
    return wrapDemoCall(() => demoFetchMetrics());
  }
  const data = await requestJson<MetricsSummary>("/api/v1/metrics");
  return {
    ...data,
    counts_by_status: data.counts_by_status ?? {},
    discrepancies_by_code: data.discrepancies_by_code ?? [],
  };
}

export interface ListInvoicesParams {
  status?: InvoiceStatus | "";
  supplier_code?: string;
  source_format?: SourceFormat | "";
  q?: string;
  limit?: number;
  offset?: number;
}

export async function fetchInvoices(
  params: ListInvoicesParams = {},
): Promise<InvoiceListResponse> {
  if (isDemoMode()) {
    return wrapDemoCall(() => demoFetchInvoices(params));
  }
  const search = new URLSearchParams();
  if (params.status) {
    search.set("status", params.status);
  }
  if (params.supplier_code?.trim()) {
    search.set("supplier_code", params.supplier_code.trim());
  }
  if (params.source_format) {
    search.set("source_format", params.source_format);
  }
  if (params.q?.trim()) {
    search.set("q", params.q.trim());
  }
  if (params.limit !== undefined) {
    search.set("limit", String(params.limit));
  }
  if (params.offset !== undefined) {
    search.set("offset", String(params.offset));
  }

  const qs = search.toString();
  const path = qs ? `/api/v1/invoices?${qs}` : "/api/v1/invoices";
  const data = await requestJson<InvoiceListResponse>(path);

  return {
    invoices: data.invoices ?? [],
    total: data.total ?? 0,
    limit: data.limit ?? 50,
    offset: data.offset ?? 0,
  };
}

export async function fetchInvoiceDetail(id: string): Promise<InvoiceDetail> {
  if (isDemoMode()) {
    return wrapDemoCall(() => demoFetchInvoiceDetail(id));
  }
  const data = await requestJson<InvoiceDetail>(
    `/api/v1/invoices/${encodeURIComponent(id)}`,
  );
  return {
    ...data,
    lines: data.lines ?? [],
    discrepancies: data.discrepancies ?? [],
    audit_events: data.audit_events ?? [],
    purchase_order: data.purchase_order
      ? {
          ...data.purchase_order,
          lines: data.purchase_order.lines ?? [],
        }
      : null,
    goods_receipts: data.goods_receipts
      ? {
          ...data.goods_receipts,
          accepted_by_sku: data.goods_receipts.accepted_by_sku ?? {},
        }
      : null,
  };
}

export function getInvoiceFileUrl(id: string): string {
  return `${getApiBaseUrl()}/api/v1/invoices/${encodeURIComponent(id)}/file`;
}

export function getDemoInvoiceFileBlob(id: string): {
  bytes: Uint8Array;
  contentType: string;
  filename: string;
} {
  return wrapDemoCall(() => demoGetInvoiceFileBlob(id));
}

export async function fetchInvoiceFileText(
  id: string,
): Promise<{ text: string; contentType: string; sizeBytes: number }> {
  if (isDemoMode()) {
    return wrapDemoCall(() => demoFetchInvoiceFileText(id));
  }
  const res = await fetchWithDeadline(getInvoiceFileUrl(id), {
    cache: "no-store",
  });
  if (!res.ok) {
    throw new ApiError(res.status, await parseErrorMessage(res));
  }
  const contentType = res.headers.get("content-type") ?? "application/octet-stream";
  const buffer = await res.arrayBuffer();
  const text = new TextDecoder("utf-8", { fatal: false }).decode(buffer);
  return {
    text,
    contentType,
    sizeBytes: buffer.byteLength,
  };
}

export async function uploadInvoice(file: File): Promise<UploadInvoiceResult> {
  if (isDemoMode()) {
    try {
      return await demoUploadInvoice(file);
    } catch (err) {
      if (err instanceof DemoStoreError) {
        throw new ApiError(err.status, err.message, { cause: err });
      }
      throw err;
    }
  }
  const formData = new FormData();
  formData.append("file", file, file.name);

  const res = await fetchWithDeadline(
    `${getApiBaseUrl()}/api/v1/invoices`,
    {
      method: "POST",
      body: formData,
    },
    30_000,
  );

  if (!res.ok) {
    throw new ApiError(res.status, await parseErrorMessage(res));
  }

  const data = (await res.json()) as UploadInvoiceResponse;
  return {
    httpStatus: res.status,
    data,
  };
}

export async function resolveInvoice(
  id: string,
  payload: ResolveInvoiceRequest,
): Promise<Invoice> {
  if (isDemoMode()) {
    return wrapDemoCall(() => demoResolveInvoice(id, payload));
  }
  return requestJson<Invoice>(
    `/api/v1/invoices/${encodeURIComponent(id)}/resolve`,
    {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
      },
      body: JSON.stringify(payload),
    },
  );
}
