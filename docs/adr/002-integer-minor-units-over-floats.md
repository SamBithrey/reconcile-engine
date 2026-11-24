# ADR-002: Integer Minor Units (`int64` Pence & Basis Points) over Floating-Point (`float64`)

- **Status:** Accepted
- **Date:** 2026-11-23

## Context
Financial reconciliation executes strict mathematical invariants across line totals (`INT-01`), VAT calculation (`INT-02`), document sums (`INT-03`), and 3-way PO price/tax variance checks (`3WAY-08`, `3WAY-09`). Even a 1-pence rounding drift between XML/PDF/CSV parsing, SQL storage, Go matching, and frontend display can trigger false-positive discrepancies or silent accounting mismatches.

## Decision
Ban `float32`/`float64` and IEEE-754 floating-point arithmetic for all money amounts and tax rates across Go, SQL, and TypeScript:
- **Currency amounts:** Stored as `int64` minor units (pence) in Go, `BIGINT` in PostgreSQL (`subtotal_pence`, `vat_total_pence`, `grand_total_pence`, `unit_price_pence`, `line_net_pence`, `line_vat_pence`, `variance_pence`), and exact JSON integers in REST responses.
- **Tax & approval rates:** Stored as integer basis points (`vat_rate_bps`, `auto_approval_rate_bps`, where `20.00% = 2000` and `1.00% = 100`).
- **Fixed-point decimal parsing:** Decimal strings from UBL XML and PDF tables (e.g., `"145.50"`, `"20.00"`) are parsed via deterministic string splitting and integer accumulation (`domain.ParseDecimalToPence`, `domain.ParsePercentToBPS`) without ever passing through `strconv.ParseFloat`.
- **VAT rounding & tolerances:** Half-up integer VAT calculation uses `(line_net_pence * vat_rate_bps + 5000) / 10000`, and `3WAY-08` price variance checks `abs(inv - po) > 50 && abs(inv - po) * 10000 > po * 100` purely in integer math.

## Trade-offs & Rationale
- **Eliminates binary representation errors:** Values such as `0.10`, `0.20`, and `0.30` cannot be represented exactly in IEEE-754 binary floating-point (`0.1 + 0.2 = 0.30000000000000004`). Integer minor units guarantee exact equality and deterministic cross-language behavior.
- **Simpler than arbitrary-precision decimal libraries:** For GBP/EUR/USD standard 2-decimal B2B invoicing, `int64` pence supports totals up to `±£92,233,720,368,547,758.07` with zero heap allocations and native PostgreSQL `BIGINT` indexing/aggregation.
- **Trade-off accepted:** Sub-penny unit pricing (e.g., `£0.0025` per unit) would require migrating minor units to tenths/hundredths of a penny (`millipence`); current scope targets standard 2-decimal commercial invoices.

## Implementation References
- [`internal/domain/money.go`](../../internal/domain/money.go)
- [`internal/matcher/matcher.go`](../../internal/matcher/matcher.go)
- [`migrations/000001_init_schema.up.sql`](../../migrations/000001_init_schema.up.sql)
- [`web/src/lib/money.ts`](../../web/src/lib/money.ts)
