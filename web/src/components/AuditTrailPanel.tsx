import { ArrowRight, History } from "lucide-react";

import type { AuditEvent } from "@/lib/types";

function hasMetadataKeys(metadata: AuditEvent["metadata"]): boolean {
  return (
    metadata !== null &&
    metadata !== undefined &&
    typeof metadata === "object" &&
    Object.keys(metadata).length > 0
  );
}

export function AuditTrailPanel({ events }: { events: AuditEvent[] }) {
  return (
    <section className="rounded-xl border border-slate-200 bg-white shadow-sm">
      <div className="flex items-center justify-between border-b border-slate-200 px-4 py-3">
        <div className="flex items-center gap-2">
          <History className="h-4 w-4 text-slate-500" />
          <div>
            <h3 className="text-sm font-semibold text-slate-900">
              Immutable Audit Trail
            </h3>
            <p className="text-xs text-slate-500">
              Chronological state transitions recorded in PostgreSQL.
            </p>
          </div>
        </div>
        <span className="rounded-full bg-slate-100 px-2.5 py-0.5 font-mono text-[11px] font-medium text-slate-700 tabular-nums">
          {events.length} event{events.length === 1 ? "" : "s"}
        </span>
      </div>

      {events.length === 0 ? (
        <div className="px-4 py-6 text-center text-xs text-slate-500">
          No audit events recorded yet.
        </div>
      ) : (
        <ol className="divide-y divide-slate-100">
          {events.map((ev, index) => (
            <li key={ev.id} className="p-4 space-y-2">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="flex flex-wrap items-center gap-2 text-xs">
                  <span className="inline-flex h-5 w-5 items-center justify-center rounded-full bg-slate-100 font-mono text-[10px] font-semibold text-slate-600 tabular-nums">
                    {index + 1}
                  </span>

                  <div className="inline-flex items-center gap-1.5 font-mono text-[11px]">
                    <span className="rounded bg-slate-100 px-1.5 py-0.5 text-slate-600">
                      {ev.from_status ?? "INIT"}
                    </span>
                    <ArrowRight className="h-3 w-3 text-slate-400" />
                    <span className="rounded bg-slate-900 px-1.5 py-0.5 font-semibold text-white">
                      {ev.to_status}
                    </span>
                  </div>

                  <span className="rounded border border-slate-200 bg-slate-50 px-2 py-0.5 font-mono text-[11px] text-slate-700">
                    actor: {ev.actor}
                  </span>
                </div>

                <time
                  dateTime={ev.created_at}
                  className="font-mono text-[11px] text-slate-500 tabular-nums"
                >
                  {ev.created_at.replace("T", " ").replace("Z", " UTC")}
                </time>
              </div>

              {ev.reason ? (
                <p className="pl-7 text-xs text-slate-800">{ev.reason}</p>
              ) : null}

              {hasMetadataKeys(ev.metadata) ? (
                <div className="pl-7">
                  <pre className="overflow-x-auto rounded-lg border border-slate-200 bg-slate-50 px-3 py-2 font-mono text-[11px] text-slate-700">
                    {JSON.stringify(ev.metadata, null, 2)}
                  </pre>
                </div>
              ) : null}
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}
