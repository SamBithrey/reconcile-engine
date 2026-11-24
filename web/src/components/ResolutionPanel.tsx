"use client";

import { type FormEvent, useEffect, useRef, useState } from "react";
import {
  AlertCircle,
  AlertOctagon,
  CheckCircle2,
  Loader2,
  Lock,
  ShieldCheck,
  XCircle,
} from "lucide-react";

import { ApiError, resolveInvoice } from "@/lib/api";
import { getStatusMeta } from "@/lib/discrepancies";
import type { Invoice } from "@/lib/types";

const ACTOR_STORAGE_KEY = "reconcile_hitl_actor";

const QUICK_REASONS = [
  "Verified variance against supplier credit note / quote",
  "Approved within commercial tolerance after buyer sign-off",
  "Rejected: over-billed quantity / unapproved price increase",
];

interface ResolutionPanelProps {
  invoice: Invoice;
  onResolved: () => Promise<void>;
}

export function ResolutionPanel({ invoice, onResolved }: ResolutionPanelProps) {
  const reasonRef = useRef<HTMLTextAreaElement | null>(null);
  const [actor, setActor] = useState("");
  const [reason, setReason] = useState("");
  const [actorInvalid, setActorInvalid] = useState(false);
  const [reasonInvalid, setReasonInvalid] = useState(false);

  const [submitting, setSubmitting] = useState<
    "MANUALLY_APPROVED" | "REJECTED" | null
  >(null);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const [conflictMsg, setConflictMsg] = useState<string | null>(null);
  const [successMsg, setSuccessMsg] = useState<string | null>(null);

  useEffect(() => {
    try {
      const saved = window.localStorage.getItem(ACTOR_STORAGE_KEY);
      if (saved) {
        setActor(saved);
      }
    } catch {
      // Ignore storage access restrictions.
    }
  }, []);

  const canResolve = invoice.status === "NEEDS_REVIEW";
  const statusMeta = getStatusMeta(invoice.status);

  const supportsUserInvalid = () =>
    typeof CSS !== "undefined" && CSS.supports("selector(:user-invalid)");

  const syncControlValidity = (
    el: HTMLInputElement | HTMLTextAreaElement,
    setInvalid: (next: boolean) => void,
  ) => {
    const trimmed = el.value.trim();
    if (el.value.length > 0 && trimmed.length === 0) {
      el.setCustomValidity("Value cannot be whitespace only.");
    } else {
      el.setCustomValidity("");
    }
    const isInvalid = supportsUserInvalid()
      ? el.matches(":user-invalid")
      : !el.validity.valid && el.classList.contains("user-invalid-fallback");
    setInvalid(isInvalid);
  };

  const handleSubmit = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    setErrorMsg(null);
    setConflictMsg(null);
    setSuccessMsg(null);

    const submitEvent = e.nativeEvent as SubmitEvent;
    const submitter = submitEvent.submitter as HTMLButtonElement | null;
    if (
      submitter?.value !== "MANUALLY_APPROVED" &&
      submitter?.value !== "REJECTED"
    ) {
      return;
    }
    const targetStatus = submitter.value;

    const trimmedActor = actor.trim();
    const trimmedReason = reason.trim();

    if (!trimmedActor || !trimmedReason) {
      setActorInvalid(!trimmedActor);
      setReasonInvalid(!trimmedReason);
      e.currentTarget.reportValidity();
      return;
    }

    try {
      window.localStorage.setItem(ACTOR_STORAGE_KEY, trimmedActor);
    } catch {
      // Ignore storage access restrictions.
    }

    setSubmitting(targetStatus);
    try {
      const updated = await resolveInvoice(invoice.id, {
        status: targetStatus,
        actor: trimmedActor,
        reason: trimmedReason,
      });
      setReason("");
      setActorInvalid(false);
      setReasonInvalid(false);
      setSuccessMsg(
        `Transitioned invoice to ${updated.status} and appended immutable audit event.`,
      );
      await onResolved();
    } catch (err) {
      if (err instanceof ApiError && err.isConflict) {
        setConflictMsg(
          `HTTP 409 Conflict — State changed concurrently before this action completed (${err.message}). Ledger view has been refreshed.`,
        );
        await onResolved();
      } else if (err instanceof ApiError) {
        setErrorMsg(`HTTP ${err.status}: ${err.message}`);
      } else {
        setErrorMsg(
          err instanceof Error ? err.message : "Failed to resolve invoice",
        );
      }
    } finally {
      setSubmitting(null);
    }
  };

  return (
    <section
      className={`rounded-xl border shadow-sm ${
        canResolve
          ? "border-amber-300 bg-amber-50/40"
          : "border-slate-200 bg-white"
      }`}
    >
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-slate-200/80 px-4 py-3">
        <div className="flex items-center gap-2">
          {canResolve ? (
            <ShieldCheck aria-hidden="true" className="h-4 w-4 text-amber-700" />
          ) : (
            <Lock aria-hidden="true" className="h-4 w-4 text-slate-500" />
          )}
          <h3 className="text-sm font-semibold text-slate-900">
            HITL Operator Resolution
          </h3>
        </div>
        <span className="font-mono text-[11px] font-medium text-slate-600">
          State: {invoice.status}
        </span>
      </div>

      <div className="p-4 space-y-3">
        {conflictMsg ? (
          <div
            role="alert"
            className="flex items-start gap-2.5 rounded-lg border border-amber-300 bg-amber-50 px-3.5 py-2.5 text-xs text-amber-950"
          >
            <AlertOctagon
              aria-hidden="true"
              className="mt-0.5 h-4 w-4 shrink-0 text-amber-700"
            />
            <div>
              <div className="font-semibold">Concurrent State Conflict (409)</div>
              <div className="mt-0.5">{conflictMsg}</div>
            </div>
          </div>
        ) : null}

        {successMsg ? (
          <div
            aria-live="polite"
            className="flex items-start gap-2.5 rounded-lg border border-emerald-200 bg-emerald-50 px-3.5 py-2.5 text-xs text-emerald-900"
          >
            <CheckCircle2
              aria-hidden="true"
              className="mt-0.5 h-4 w-4 shrink-0 text-emerald-600"
            />
            <div>{successMsg}</div>
          </div>
        ) : null}

        {errorMsg ? (
          <div
            role="alert"
            className="flex items-start gap-2.5 rounded-lg border border-rose-200 bg-rose-50 px-3.5 py-2.5 text-xs text-rose-800"
          >
            <XCircle
              aria-hidden="true"
              className="mt-0.5 h-4 w-4 shrink-0 text-rose-600"
            />
            <div>{errorMsg}</div>
          </div>
        ) : null}

        {canResolve ? (
          <form onSubmit={(e) => void handleSubmit(e)} className="space-y-3">
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <div>
                <label
                  htmlFor="hitl-actor"
                  className="block text-xs font-semibold text-slate-700"
                >
                  Operator / Actor <span aria-hidden="true" className="text-rose-600">*</span>
                </label>
                <span
                  id="hitl-actor-hint"
                  className="mt-0.5 block text-[11px] text-slate-500"
                >
                  Enter your name or work email (recorded in audit_events).
                </span>
                <input
                  id="hitl-actor"
                  name="actor"
                  type="text"
                  required
                  pattern=".*\S.*"
                  autoComplete="email"
                  value={actor}
                  disabled={submitting !== null}
                  aria-invalid={actorInvalid ? "true" : "false"}
                  aria-describedby={
                    actorInvalid
                      ? "hitl-actor-hint hitl-actor-error"
                      : "hitl-actor-hint"
                  }
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      e.preventDefault();
                    }
                  }}
                  onChange={(e) => {
                    setActor(e.target.value);
                    syncControlValidity(e.currentTarget, setActorInvalid);
                  }}
                  onBlur={(e) => {
                    if (!supportsUserInvalid()) {
                      e.currentTarget.classList.toggle(
                        "user-invalid-fallback",
                        !e.currentTarget.validity.valid,
                      );
                    }
                    syncControlValidity(e.currentTarget, setActorInvalid);
                  }}
                  onInvalid={(e) => {
                    if (!supportsUserInvalid()) {
                      e.currentTarget.classList.add("user-invalid-fallback");
                    }
                    setActorInvalid(true);
                  }}
                  placeholder="ops.reviewer@company.co.uk"
                  className="hitl-field-control mt-1 w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-xs text-slate-900 placeholder:text-slate-400 focus:border-slate-500"
                />
                <div
                  id="hitl-actor-error"
                  className="hitl-field-error mt-1 items-center gap-1 text-[11px] font-medium text-rose-700"
                >
                  <AlertCircle aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />
                  <span>Error: Enter an operator name or email address.</span>
                </div>
              </div>

              <div className="flex items-end">
                <p className="text-[11px] text-slate-600">
                  Resolving executes a row-locked{" "}
                  <span className="font-mono font-medium">
                    SELECT ... FOR UPDATE
                  </span>{" "}
                  transition and writes an immutable{" "}
                  <span className="font-mono font-medium">audit_events</span>{" "}
                  record.
                </p>
              </div>
            </div>

            <div>
              <div className="flex flex-wrap items-center justify-between gap-2">
                <label
                  htmlFor="hitl-reason"
                  className="block text-xs font-semibold text-slate-700"
                >
                  Audit Reason / Verification Notes{" "}
                  <span aria-hidden="true" className="text-rose-600">*</span>
                </label>
                <div className="flex flex-wrap gap-1">
                  {QUICK_REASONS.map((preset) => (
                    <button
                      key={preset}
                      type="button"
                      disabled={submitting !== null}
                      onClick={() => {
                        setReason(preset);
                        setReasonInvalid(false);
                        if (reasonRef.current) {
                          reasonRef.current.setCustomValidity("");
                          reasonRef.current.classList.remove("user-invalid-fallback");
                        }
                      }}
                      className="rounded border border-slate-300 bg-white px-2 py-0.5 text-[10px] text-slate-600 hover:bg-slate-100"
                    >
                      {preset.slice(0, 32)}…
                    </button>
                  ))}
                </div>
              </div>
              <span
                id="hitl-reason-hint"
                className="mt-0.5 block text-[11px] text-slate-500"
              >
                State the PO amendment, credit note reference, or rejection basis.
              </span>
              <textarea
                ref={reasonRef}
                id="hitl-reason"
                name="reason"
                rows={3}
                required
                value={reason}
                disabled={submitting !== null}
                aria-invalid={reasonInvalid ? "true" : "false"}
                aria-describedby={
                  reasonInvalid
                    ? "hitl-reason-hint hitl-reason-error"
                    : "hitl-reason-hint"
                }
                onChange={(e) => {
                  setReason(e.target.value);
                  syncControlValidity(e.currentTarget, setReasonInvalid);
                }}
                onBlur={(e) => {
                  if (!supportsUserInvalid()) {
                    e.currentTarget.classList.toggle(
                      "user-invalid-fallback",
                      !e.currentTarget.validity.valid,
                    );
                  }
                  syncControlValidity(e.currentTarget, setReasonInvalid);
                }}
                onInvalid={(e) => {
                  if (!supportsUserInvalid()) {
                    e.currentTarget.classList.add("user-invalid-fallback");
                  }
                  setReasonInvalid(true);
                }}
                placeholder="Document verification decision (PO amendment, credit note ref, short-delivery confirmation)..."
                className="hitl-field-control auto-size-textarea mt-1 rounded-lg border border-slate-300 bg-white px-3 py-2 text-xs text-slate-900 placeholder:text-slate-400 focus:border-slate-500"
              />
              <div
                id="hitl-reason-error"
                className="hitl-field-error mt-1 items-center gap-1 text-[11px] font-medium text-rose-700"
              >
                <AlertCircle aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />
                <span>Error: Provide a non-empty audit justification before submitting.</span>
              </div>
            </div>

            <div className="flex flex-wrap items-center justify-end gap-2.5 pt-1">
              <button
                type="submit"
                name="resolution"
                value="REJECTED"
                disabled={submitting !== null}
                className="inline-flex items-center gap-1.5 rounded-lg border border-rose-300 bg-rose-600 px-4 py-2 text-xs font-semibold text-white hover:bg-rose-700 disabled:opacity-50 transition-colors"
              >
                {submitting === "REJECTED" ? (
                  <Loader2 aria-hidden="true" className="h-3.5 w-3.5 animate-spin" />
                ) : (
                  <XCircle aria-hidden="true" className="h-3.5 w-3.5" />
                )}
                <span>Reject (REJECTED)</span>
              </button>

              <button
                type="submit"
                name="resolution"
                value="MANUALLY_APPROVED"
                disabled={submitting !== null}
                className="inline-flex items-center gap-1.5 rounded-lg bg-emerald-700 px-4 py-2 text-xs font-semibold text-white hover:bg-emerald-800 disabled:opacity-50 transition-colors"
              >
                {submitting === "MANUALLY_APPROVED" ? (
                  <Loader2 aria-hidden="true" className="h-3.5 w-3.5 animate-spin" />
                ) : (
                  <CheckCircle2 aria-hidden="true" className="h-3.5 w-3.5" />
                )}
                <span>Approve (MANUALLY_APPROVED)</span>
              </button>
            </div>
          </form>
        ) : (
          <div className="rounded-lg border border-slate-200 bg-slate-50 px-3.5 py-2.5 text-xs text-slate-700">
            <span className="font-semibold">
              {statusMeta.isTerminal ? "Terminal State" : "Awaiting Pipeline"} (
              {invoice.status}):
            </span>{" "}
            {statusMeta.description}. Operator approve/reject controls are only
            permitted when status is{" "}
            <span className="font-mono font-semibold">NEEDS_REVIEW</span>.
          </div>
        )}
      </div>
    </section>
  );
}
