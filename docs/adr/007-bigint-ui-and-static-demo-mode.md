# ADR-007: Pure `BigInt` Currency Presentation Formatting & Opt-In Static Demo Mode (`web/`)

- **Status:** Accepted
- **Date:** 2026-11-24

## Context
Two frontend architectural challenges surfaced during Phases 3 and 4:
1. **Zero-floating-point UI parity:** TypeScript/JavaScript's `number` type is IEEE-754 double-precision float. Naive UI formatting (`(pence / 100).toFixed(2)`) or JS float summation in accounting exports would violate the repository's zero-floating-point financial rule.
2. **Zero-infrastructure portfolio evaluation:** Reviewers browsing a deployed Vercel frontend should be able to explore the full split-pane HITL workflow, 3-way PO/GRN comparison tables, all 13 discrepancy rules (`INT-01`–`INT-04`, `3WAY-01`–`3WAY-09`), audit trails, and accounting exports even without Docker running locally—without degrading or altering live API mode.

## Decision
1. **Pure `BigInt` financial formatting & aggregation (`web/src/lib/money.ts`, `web/src/lib/export.ts`):**
   - Validate all incoming minor-unit amounts and basis points with `Number.isInteger` before converting to `BigInt`.
   - Format currencies (`formatPenceToGBP`, `formatPenceCurrency`, `formatPenceDecimal`) and percentages (`formatBPS`) purely via integer quotient/remainder (`abs / 100n`, `abs % 100n`) and `Intl.NumberFormat` grouping on whole pounds only.
   - Mirror Go's exact half-up VAT formula (`(net * bps + 5000n) / 10000n`) and `3WAY-08` price variance rule (`absDiff > 50n && absDiff * 10000n > po * 100n`) in `BigInt`.
2. **Opt-in Static Demo Mode (`NEXT_PUBLIC_DEMO_MODE=true`):**
   - Default to **live `cmd/api` mode** whenever `NEXT_PUBLIC_DEMO_MODE` is unset or not `"true"`.
   - When `NEXT_PUBLIC_DEMO_MODE=true`, `web/src/lib/api.ts` delegates reads, filters, metrics aggregation, interactive HITL resolution (with `409 Conflict` state-machine enforcement), SHA-256 upload deduplication, and inline UBL XML / CSV / native PDF source previews to an in-memory deterministic dataset in `web/src/lib/demo-data.ts`.

## Trade-offs & Rationale
- **Single UI codebase:** Putting the demo mode switch inside `web/src/lib/api.ts` keeps every page and component (`DashboardClient`, `InvoiceDetailClient`, `ThreeWayComparisonTable`, `ResolutionPanel`, `AccountingExportMenu`) exercising the exact same code paths in both live and static demo deployments.
- **No external runtime dependencies:** Uses native ES `BigInt`, Web Crypto SHA-256, and zero-dependency client-side PDF/XML/CSV generation so Vercel builds remain fast and 100% deterministic.
- **Trade-off accepted:** Demo mode mutations (approving/rejecting or uploading sample files) live in browser memory and reset on reload or via the "Reset Demo Data" control, clearly signposted by a `STATIC DEMO MODE` badge in the header.

## Implementation References
- [`web/src/lib/money.ts`](../../web/src/lib/money.ts)
- [`web/src/lib/export.ts`](../../web/src/lib/export.ts)
- [`web/src/lib/demo-data.ts`](../../web/src/lib/demo-data.ts)
- [`web/src/lib/api.ts`](../../web/src/lib/api.ts)
- [`web/src/lib/__tests__/domain.test.ts`](../../web/src/lib/__tests__/domain.test.ts)
- [`web/src/lib/__tests__/demo.test.ts`](../../web/src/lib/__tests__/demo.test.ts)
