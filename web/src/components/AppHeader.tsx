"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useCallback, useEffect, useState } from "react";
import { Activity, FileCheck2, RefreshCw, Server } from "lucide-react";

import { fetchHealth, getApiBaseUrl } from "@/lib/api";
import type { HealthResponse } from "@/lib/types";

export function AppHeader() {
  const pathname = usePathname();
  const [health, setHealth] = useState<HealthResponse | null>(null);
  const [healthError, setHealthError] = useState<string | null>(null);
  const [checking, setChecking] = useState<boolean>(false);

  const checkHealth = useCallback(async () => {
    setChecking(true);
    try {
      const res = await fetchHealth();
      setHealth(res);
      setHealthError(null);
    } catch (err) {
      setHealth(null);
      setHealthError(err instanceof Error ? err.message : "API unreachable");
    } finally {
      setChecking(false);
    }
  }, []);

  useEffect(() => {
    void checkHealth();
  }, [checkHealth]);

  const apiBase = getApiBaseUrl();
  const isHealthy = health?.status === "ok";
  const isDegraded = health?.status === "degraded";

  const depsSummary = health?.dependencies
    ? Object.entries(health.dependencies)
        .map(([k, v]) => `${k}: ${v}`)
        .join(" | ")
    : healthError ?? "Checking backend health";

  return (
    <header className="sticky top-0 z-30 border-b border-slate-800 bg-slate-950 text-slate-100">
      <div className="mx-auto flex max-w-[1440px] flex-wrap items-center justify-between gap-4 px-4 py-3 sm:px-6 lg:px-8">
        <div className="flex items-center gap-6">
          <Link href="/" className="flex items-center gap-3 group">
            <span
              aria-hidden="true"
              className="flex h-9 w-9 items-center justify-center rounded-lg bg-emerald-500/15 text-emerald-400 ring-1 ring-emerald-500/30 group-hover:bg-emerald-500/25 transition-colors"
            >
              <FileCheck2 className="h-5 w-5" />
            </span>
            <div>
              <div className="flex items-center gap-2">
                <span className="text-sm font-semibold tracking-tight text-white">
                  Reconcile Engine
                </span>
                <span className="rounded bg-slate-800 px-1.5 py-0.5 font-mono text-[10px] font-medium text-slate-300">
                  3-WAY HITL
                </span>
              </div>
              <p className="text-xs text-slate-400">
                Deterministic AP Verification &amp; Integer Pence Ledger
              </p>
            </div>
          </Link>

          <nav aria-label="Primary" className="hidden sm:flex items-center gap-2">
            <Link
              href="/"
              aria-current={pathname === "/" ? "page" : undefined}
              className="nav-link rounded-md px-3 py-1.5 text-xs font-medium text-slate-200 hover:bg-slate-800 hover:text-white transition-colors"
            >
              Invoice Queue
            </Link>
          </nav>
        </div>

        <div className="flex flex-wrap items-center gap-2.5 text-xs">
          <div
            className="hidden md:flex items-center gap-1.5 rounded-md border border-slate-800 bg-slate-900 px-2.5 py-1 font-mono text-[11px] text-slate-300"
            title="Configured backend API base URL"
          >
            <Server aria-hidden="true" className="h-3.5 w-3.5 text-slate-400" />
            <span>{apiBase}</span>
          </div>

          <button
            type="button"
            onClick={() => void checkHealth()}
            title={depsSummary}
            aria-label={`Refresh API health status (${depsSummary})`}
            className={`inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1 font-medium transition-colors ${
              isHealthy
                ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-300 hover:bg-emerald-500/20"
                : isDegraded
                  ? "border-amber-500/30 bg-amber-500/10 text-amber-300 hover:bg-amber-500/20"
                  : healthError
                    ? "border-rose-500/30 bg-rose-500/10 text-rose-300 hover:bg-rose-500/20"
                    : "border-slate-700 bg-slate-900 text-slate-300"
            }`}
          >
            <Activity aria-hidden="true" className="h-3.5 w-3.5" />
            <span>
              {isHealthy
                ? "API Healthy"
                : isDegraded
                  ? "API Degraded"
                  : healthError
                    ? "API Offline"
                    : "Checking..."}
            </span>
            <RefreshCw
              aria-hidden="true"
              className={`h-3 w-3 opacity-75 ${checking ? "animate-spin" : ""}`}
            />
          </button>
        </div>
      </div>
    </header>
  );
}
