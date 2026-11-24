# Architecture Decision Records (ADRs)

This document summarizes the key architectural decisions made across Phases 1–4 of the **Reconcile Engine** monorepo. Full individual decision records with detailed consequences and codebase references live in [`docs/adr/`](./adr/README.md).

| ID | Title | Status | Individual Record |
| :--- | :--- | :--- | :--- |
| ADR-001 | RabbitMQ 3.13 over Apache Kafka for Event Transport | Accepted | [`001-rabbitmq-over-kafka.md`](./adr/001-rabbitmq-over-kafka.md) |
| ADR-002 | Integer Minor Units (`int64` Pence & Basis Points) over Floating-Point (`float64`) | Accepted | [`002-integer-minor-units-over-floats.md`](./adr/002-integer-minor-units-over-floats.md) |
| ADR-003 | Dual-Layer Idempotency: SHA-256 Content Hashing + Composite Unique Constraint | Accepted | [`003-dual-layer-idempotency.md`](./adr/003-dual-layer-idempotency.md) |
| ADR-004 | MinIO S3-Compatible Object Storage over PostgreSQL `BYTEA` | Accepted | [`004-minio-object-storage-over-bytea.md`](./adr/004-minio-object-storage-over-bytea.md) |
| ADR-005 | Deterministic Multi-Format Parsers over Paid Multimodal LLM / Cloud OCR APIs | Accepted | [`005-deterministic-parsers-over-llm-apis.md`](./adr/005-deterministic-parsers-over-llm-apis.md) |
| ADR-006 | Pessimistic Row-Level Locking (`SELECT ... FOR UPDATE`) for State Transitions & HITL Concurrency | Accepted | [`006-hitl-state-concurrency-and-row-locking.md`](./adr/006-hitl-state-concurrency-and-row-locking.md) |
| ADR-007 | Pure `BigInt` Currency Presentation Formatting & Opt-In Static Demo Mode (`web/`) | Accepted | [`007-bigint-ui-and-static-demo-mode.md`](./adr/007-bigint-ui-and-static-demo-mode.md) |

---

## ADR-001: RabbitMQ 3.13 over Apache Kafka for Event Transport
- **Status:** Accepted
- **Context:** The system requires asynchronous decoupling between HTTP ingestion (`cmd/api`), multi-format document parsing, and 3-way PO/GRN reconciliation (`cmd/worker`), alongside non-blocking exponential retry backoffs and dead-letter isolation for poison or corrupt invoices.
- **Decision:** Use RabbitMQ 3.13 with durable topic/direct exchanges (`reconcile.events`, `reconcile.retry`, `reconcile.dlx`), manual consumer acknowledgements (`autoAck: false`), bounded consumer prefetch (`QoS=5`), tiered TTL retry queues (`q.retry.1s`, `q.retry.5s`, `q.retry.25s`), and a catch-all Dead Letter Queue (`q.dlq.dead-letters`).
- **Trade-offs & Rationale:**
  - Kafka excels at high-throughput ordered log replay and stream partitioning, but demands heavier container memory footprints and bespoke retry/DLQ topic orchestration.
  - RabbitMQ provides native per-message acknowledgements, `x-dead-letter-exchange` routing, per-queue TTL delayed requeuing, and a built-in Management UI (`:15672`) in a single lightweight container—ideal for work-queue task distribution and zero-cost reproducible demos.

## ADR-002: Integer Minor Units (`int64` Pence & Basis Points) over Floating-Point (`float64`)
- **Status:** Accepted
- **Context:** Financial reconciliation requires exact equality checks across line items (`INT-01`), VAT half-up rounding (`INT-02`), document header sums (`INT-03`), and 3-way PO price/tax tolerances (`3WAY-08`, `3WAY-09`).
- **Decision:** Represent all currency amounts in `int64` minor units (pence) in Go, `BIGINT` in PostgreSQL, and exact integer pence in JSON APIs. Represent tax rates and approval rates in integer basis points (`vat_rate_bps`, where `20.00% = 2000`, `1.00% = 100`). Parse decimal XML/PDF strings directly via fixed-point string splitting (`internal/domain/money.go`) with zero intermediate `float32`/`float64` values, and enforce half-up VAT rounding via `(line_net_pence * vat_rate_bps + 5000) / 10000`.
- **Trade-offs & Rationale:**
  - IEEE-754 binary floating-point cannot represent `0.10` or `0.20` exactly (`0.1 + 0.2 = 0.30000000000000004`), causing false-positive reconciliation discrepancies and non-deterministic rounding drift across Go, SQL, and TypeScript.
  - Integer minor-unit arithmetic is exact, fast, and trivially verifiable in CI with static `grep` checks. The minor trade-off of converting integers to `£0.00` only at the presentation boundary is cleanly isolated in `domain.FormatPence` and `web/src/lib/money.ts`.

## ADR-003: Dual-Layer Idempotency — SHA-256 Content Hashing + Composite Unique Constraint
- **Status:** Accepted
- **Context:** Network retries, operator double-clicks, duplicate supplier transmissions across formats, or RabbitMQ at-least-once redeliveries can cause the same invoice to be submitted or processed multiple times.
- **Decision:** Enforce two complementary idempotency boundaries backed by PostgreSQL unique constraints:
  1. Stream uploads through `sha256.New()` via `io.TeeReader` at ingestion (`POST /api/v1/invoices`, 10 MB cap) and enforce `invoices.file_sha256 UNIQUE`. Identical byte re-uploads return `HTTP 200 OK` (`duplicate: true`) with the existing `invoice_id` without re-publishing to RabbitMQ.
  2. Enforce `CONSTRAINT uq_supplier_invoice_num UNIQUE (supplier_id, invoice_number)` in PostgreSQL so the same supplier cannot bill the same invoice number twice under a different file format (e.g., UBL XML followed by PDF); violations transition the duplicate upload to `PARSE_FAILED` and route the delivery to `reconcile.dlx`.
- **Trade-offs & Rationale:**
  - Eliminates double-liability creation without requiring Redis, distributed locks, or external coordination services.
  - Relies on PostgreSQL transactional atomicity for both ingestion fast-paths (`ON CONFLICT (file_sha256) DO NOTHING`) and post-parse business-key deduplication.

## ADR-004: MinIO S3-Compatible Object Storage over PostgreSQL `BYTEA`
- **Status:** Accepted
- **Context:** Original supplier XML, PDF, and CSV files must be preserved immutably for financial audits and rendered side-by-side in the Next.js HITL review workspace (`GET /api/v1/invoices/{id}/file`).
- **Decision:** Store immutable raw invoice bytes in MinIO keyed deterministically by UTC year/month and SHA-256 digest (`raw-invoices/YYYY/MM/<sha256>.<ext>`), persisting only metadata and `storage_key` in PostgreSQL.
- **Trade-offs & Rationale:**
  - Keeps PostgreSQL WAL segments, `pg_dump` backups, and shared buffer caches lean even under high multi-page PDF volumes.
  - Mirrors production cloud S3 / GCS / Azure Blob Storage patterns using the standard `minio-go/v7` S3 client while running locally at £0 cost.

## ADR-005: Deterministic Multi-Format Parsers over Paid Multimodal LLM / Cloud OCR APIs
- **Status:** Accepted
- **Context:** The reconciliation engine must run 100% offline in CI/CD and local Docker Compose at £0 cost, with reproducible, auditable test outcomes for every invoice fixture.
- **Decision:** Implement deterministic parsers for structured B2B formats—Peppol BIS Billing 3.0 UBL 2.1 XML (`encoding/xml`), native text-layered PDFs (`github.com/razvandimescu/gopdf/pdf`), and supplier CSV manifests (`encoding/csv`)—while explicitly inspecting `page.OutlineHint()` on PDFs to quarantine scanned/image-only documents to `PARSE_FAILED` (`ErrScannedPDFUnsupported`).
- **Trade-offs & Rationale:**
  - Eliminates paid API keys, network latency, rate limits, and stochastic LLM extraction drift.
  - Keeps scope focused on backend distributed systems, deterministic accounting arithmetic, and HITL exception workflows; an optional OCR/extraction stage could plug into `internal/parser.Router` later without modifying the event bus or matcher.

## ADR-006: Pessimistic Row-Level Locking (`SELECT ... FOR UPDATE`) for State Transitions & HITL Concurrency
- **Status:** Accepted
- **Context:** Multiple worker replicas (`QoS=5`), redelivered AMQP messages, and concurrent finance operators reviewing the same `NEEDS_REVIEW` invoice can race to mutate an invoice's lifecycle state (`UPLOADED -> PARSED | PARSE_FAILED`, `PARSED -> AUTO_APPROVED | NEEDS_REVIEW`, `NEEDS_REVIEW -> MANUALLY_APPROVED | REJECTED`).
- **Decision:** Execute every state transition inside an explicit PostgreSQL transaction that acquires `SELECT status FROM invoices WHERE id = $1 FOR UPDATE`, validates the transition against the central Go state machine (`domain.CanTransition`), updates the invoice and child tables (`invoice_lines`, `discrepancies`), and appends an immutable `audit_events` row atomically before committing. Invalid or stale HITL transitions return `domain.ErrInvalidStateTransition`, mapped to `HTTP 409 Conflict` by `cmd/api`, and surfaced in `ResolutionPanel.tsx` with automatic ledger refresh.
- **Trade-offs & Rationale:**
  - Prevents lost updates, duplicate audit trail rows, and illegal transitions out of terminal states (`AUTO_APPROVED`, `MANUALLY_APPROVED`, `REJECTED`, `PARSE_FAILED`).
  - Pessimistic row-level locks are held only for single-invoice millisecond transactions, avoiding the retry complexity of optimistic version columns while serializing concurrent operators cleanly.

## ADR-007: Pure `BigInt` Currency Presentation Formatting & Opt-In Static Demo Mode (`web/`)
- **Status:** Accepted
- **Context:** JavaScript `number` is an IEEE-754 double-precision float; formatting `pence / 100` or computing VAT/totals with JS floats risks violating the repository's zero-floating-point currency invariant. Separately, portfolio reviewers visiting a static Vercel preview have no live Go/Postgres/RabbitMQ/MinIO stack running locally.
- **Decision:**
  1. Perform all frontend currency formatting, basis-point formatting, VAT half-up math, 3-way tolerance checks, and CSV/JSON export aggregation in `web/src/lib/money.ts` and `web/src/lib/export.ts` using native JS `BigInt` (`abs / 100n`, `abs % 100n`) and integer validation (`Number.isInteger`).
  2. Provide an opt-in static demo mode gated behind `NEXT_PUBLIC_DEMO_MODE=true` (`web/src/lib/demo-data.ts`, wired through `web/src/lib/api.ts`) that serves deterministic fixtures covering all 3 source formats and all 13 discrepancy rules (`INT-01`–`INT-04`, `3WAY-01`–`3WAY-09`) with interactive in-memory HITL resolution, SHA-256 upload deduplication, and inline UBL/CSV/PDF source previews, while defaulting strictly to live `cmd/api` HTTP calls when unset or `"false"`.
- **Trade-offs & Rationale:**
  - Preserves bit-exact parity with Go `int64` arithmetic across the entire stack—including exported RFC-4180 CSV and JSON accounting bundles.
  - Enables a zero-infrastructure interactive Vercel portfolio preview without branching UI components or compromising the live API default.
