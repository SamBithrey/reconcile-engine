# Reconcile Engine — Event-Driven 3-Way Invoice Reconciliation Engine

[![CI](https://github.com/sambithrey/reconcile-engine/actions/workflows/ci.yml/badge.svg)](./.github/workflows/ci.yml)

A **£0-cost, 100% deterministic, offline** event-driven backend and full-stack Accounts Payable (AP) reconciliation system built with **Go 1.23+**, **PostgreSQL 16**, **RabbitMQ 3.13**, **MinIO (S3)**, and **Next.js 15**.

It ingests multi-format B2B supplier invoices (**Peppol BIS Billing 3.0 UBL 2.1 XML**, **native text-layered PDFs via `gopdf`**, and **CSV manifests**), persists raw bytes in content-addressed object storage, reconciles every line item against internal **Purchase Orders (PO)** and **Goods Received Notes (GRN)** across 13 deterministic rules (`INT-01`–`INT-04` and `3WAY-01`–`3WAY-09`), and routes exceptions to a split-pane **Human-in-the-Loop (HITL)** Next.js verification dashboard.

---

## Architecture Overview

```text
+-------------------+       POST /api/v1/invoices        +--------------------+
|  Client / Seeder  | ---------------------------------> |  Ingestion API     |
+-------------------+                                    |  (cmd/api - Go)    |
                                                         +---------+----------+
                                                                   |
                          +----------------------------------------+----------------------------------------+
                          | 1. PutObject (SHA-256 key)             | 2. INSERT (status=UPLOADED)            | 3. Publish invoice.uploaded
                          v                                        v                                        v
                 +-----------------+                      +-----------------+                      +------------------+
                 |  MinIO (S3)     |                      |  PostgreSQL 16  |                      |   RabbitMQ 3.13  |
                 |  raw-invoices/  |                      |  Ledger & State |                      | reconcile.events |
                 +--------+--------+                      +--------+--------+                      +--------+---------+
                          ^                                        ^                                        |
                          | Fetch raw bytes                        | Update PARSED / PARSE_FAILED           | Consume (QoS=5, autoAck=false)
                          +--------------------+-------------------+                                        v
                                               |                                                   +------------------+
                                      +--------+--------+                                          |  Parser Worker   |
                                      |  Parser Worker  | ---------------------------------------> |  (cmd/worker)    |
                                      |  XML / PDF / CSV|   Publish invoice.parsed (or DLQ)        +--------+---------+
                                      +-----------------+                                                   |
                                                                                                            v
                                                                                                   +------------------+
                                                                                                   |  Matcher Worker  |
                                                                                                   |  3-Way Engine    |
                                                                                                   +--------+---------+
                                                                                                            |
                                                            +-----------------------------------------------+
                                                            | Write discrepancies, status & audit_events atomically
                                                            v
                                                   +-----------------+       GET / resolve       +------------------+
                                                   |  PostgreSQL 16  | <------------------------ |  Next.js 15 UI   |
                                                   |  AUTO_APPROVED  | ------------------------> |  HITL Dashboard  |
                                                   |  NEEDS_REVIEW   |                           |  (Port 3000)     |
                                                   +-----------------+                           +------------------+
```

---

## Core Engineering Invariants

### 1. Zero Floating-Point Currency & Tax Math
- **`float32` / `float64` are forbidden** for monetary amounts and tax rates across Go, SQL, and TypeScript (enforced in CI by `.github/workflows/ci.yml`).
- All currency values are represented in **integer minor units (pence)**: `int64` in Go, `BIGINT` in PostgreSQL, exact JSON integers on the wire, and pure `BigInt` quotient/remainder formatting (`abs / 100n`, `abs % 100n`) in `web/src/lib/money.ts`.
- All tax rates and approval metrics use **integer basis points (`vat_rate_bps`)**, where `20.00% = 2000` and `1.00% = 100`.
- **Half-up integer VAT rounding:** `expected_vat = (line_net_pence * vat_rate_bps + 5000) / 10000`.
- **Decimal string parsing:** UBL XML and PDF decimal literals (e.g., `"145.50"`) parse directly via string splitting into `int64` pence (`internal/domain/money.go`) with zero intermediate floats.

### 2. Strict Invoice Lifecycle State Machine & Concurrency
```text
UPLOADED ──► PARSED ──► AUTO_APPROVED (Terminal)
   │            │
   │            └──► NEEDS_REVIEW ──► MANUALLY_APPROVED (Terminal)
   │                      │
   │                      └──► REJECTED (Terminal)
   └──► PARSE_FAILED (Terminal; routed to DLQ)
```
- Valid transitions (`internal/domain/state_machine.go`):
  - `UPLOADED -> PARSED | PARSE_FAILED`
  - `PARSED -> AUTO_APPROVED | NEEDS_REVIEW`
  - `NEEDS_REVIEW -> MANUALLY_APPROVED | REJECTED`
- Every mutation acquires a row-level lock (`SELECT status FROM invoices WHERE id = $1 FOR UPDATE`) inside a single PostgreSQL transaction, validates `domain.CanTransition`, updates invoice/discrepancy tables, and appends an immutable `audit_events` row atomically.
- Stale or concurrent HITL resolutions against an already-transitioned invoice return `domain.ErrInvalidStateTransition` (`HTTP 409 Conflict`).

### 3. Dual-Layer Idempotency & Deduplication
1. **Content-Addressable SHA-256 Deduplication:** `POST /api/v1/invoices` streams uploads (max 10 MB) through `sha256.New()` via `io.TeeReader` and enforces `invoices.file_sha256 UNIQUE`. Duplicate byte uploads return `HTTP 200 OK` (`duplicate: true`) with the original `invoice_id` without re-publishing to RabbitMQ.
2. **Composite Business-Key Constraint:** PostgreSQL enforces `UNIQUE (supplier_id, invoice_number)` so the same supplier cannot bill the same invoice number twice under different formats (e.g., UBL XML followed by PDF); duplicates transition to `PARSE_FAILED` and route to `reconcile.dlx`.

### 4. Deterministic Multi-Format Parsing & 13-Rule 3-Way Matcher
- **100% Offline, Zero LLM/OCR APIs:** Parses Peppol BIS Billing 3.0 `UBL_XML` (`encoding/xml`), text-layered `PDF` (`github.com/razvandimescu/gopdf/pdf`), and 12-column `CSV` manifests (`encoding/csv`). Image-only/scanned PDFs without text layers are detected via `page.OutlineHint()` and quarantined to `PARSE_FAILED` (`ErrScannedPDFUnsupported`).
- **Pass A — Internal Invoice Integrity:**
  - `INT-01` `LINE_MATH_ERROR`: `abs((qty * unit_price_pence) - line_net_pence) > 1`
  - `INT-02` `VAT_MISMATCH`: `abs(expected_line_vat - line_vat_pence) > 1`
  - `INT-03` `TOTAL_MISMATCH`: Header subtotal or grand total diverges from line sums by `> 1p`
  - `INT-04` `CURRENCY_MISMATCH`: `invoice.currency != po.currency`
- **Pass B — 3-Way PO & GRN Verification:**
  - `3WAY-01` `MISSING_PO`: PO reference missing or not found
  - `3WAY-02` `PO_SUPPLIER_MISMATCH`: `invoice.supplier_id != po.supplier_id`
  - `3WAY-03` `PO_CLOSED`: PO status is `CLOSED` or `CANCELLED`
  - `3WAY-04` `MISSING_GRN`: Zero Goods Received Notes exist for the PO
  - `3WAY-05` `UNMATCHED_SKU`: Invoice line SKU does not exist on the PO
  - `3WAY-06` `QTY_EXCEEDS_GRN`: Invoiced quantity exceeds summed GRN accepted quantity
  - `3WAY-07` `QTY_EXCEEDS_PO`: Invoiced quantity exceeds PO ordered quantity
  - `3WAY-08` `PRICE_VARIANCE`: `abs(inv - po) > 50` (50p) AND `abs(inv - po) * 10000 > po * 100` (1.00%)
  - `3WAY-09` `VAT_RATE_MISMATCH`: `inv_line.vat_rate_bps != po_line.vat_rate_bps`

---

## RabbitMQ Retry & Dead-Letter Topology

Declared idempotently by `internal/broker/rabbitmq.go`:

| Component | Name | Type / Config | Purpose |
| :--- | :--- | :--- | :--- |
| Primary Exchange | `reconcile.events` | `topic` (durable) | Routes `invoice.uploaded`, `invoice.parsed`, `invoice.reconciled` |
| Retry Exchange | `reconcile.retry` | `direct` (durable) | Routes transient failures to tiered TTL delay queues |
| Dead Letter Exchange | `reconcile.dlx` | `topic` (durable) | Captures poison payloads, parse failures, and exhausted retries |
| Primary Queues | `q.invoice.uploaded`, `q.invoice.parsed`, `q.invoice.reconciled` | Durable, `x-dead-letter-exchange: reconcile.dlx` | Consumed with `autoAck: false` and `QoS=5` |
| TTL Retry Queues | `q.retry.1s` (`1000ms`), `q.retry.5s` (`5000ms`), `q.retry.25s` (`25000ms`) | Durable, DLX back to `reconcile.events` | Non-blocking exponential backoff (`x-retry-count` 0 → 1 → 2 → 3) |
| Dead Letter Queue | `q.dlq.dead-letters` | Bound `#` on `reconcile.dlx` | Terminal quarantine for permanent errors or `x-retry-count >= 3` |

---

## One-Command Quick Start (`make demo`)

Prerequisites: **Docker Compose**, **Go 1.23+**, `curl`, and **Node.js 22+** (for `web/`).

```bash
make demo
```

`make demo` performs the complete end-to-end boot and verification automatically:
1. Starts **PostgreSQL 16**, **RabbitMQ 3.13**, **MinIO**, `minio-init`, `migrate`, `cmd/api`, and `cmd/worker` via `docker compose up -d --build`.
2. Ensures [`migrations/000001_init_schema.up.sql`](./migrations/000001_init_schema.up.sql) is applied cleanly.
3. Polls `GET http://localhost:8080/healthz` until Postgres, RabbitMQ, and MinIO all report healthy.
4. Runs `cmd/seed` to seed **5 suppliers**, **15 Purchase Orders**, and **14 Goods Received Notes**, then uploads **20 mixed-format invoices (`UBL_XML`, `PDF`, `CSV`)** and waits until asynchronous parsing and 3-way matching complete:
   - **4 `AUTO_APPROVED`** (clean UBL XML, native PDF, CSV manifest, and multi-page native PDF)
   - **13 `NEEDS_REVIEW`** (one dedicated fixture for every rule `INT-01`–`INT-04` and `3WAY-01`–`3WAY-09`)
   - **3 `PARSE_FAILED`** (scanned/outlined PDF without text layer, corrupt UBL XML, corrupt CSV)
5. Prints the live `/api/v1/metrics` JSON summary.

### Local Service Endpoints
| Service | URL | Credentials |
| :--- | :--- | :--- |
| Ingestion & Query API | `http://localhost:8080` (`GET /healthz`) | — |
| Next.js HITL Dashboard | `http://localhost:3000` | — |
| RabbitMQ Management UI | `http://localhost:15672` | `reconcile` / `reconcile` |
| MinIO Console | `http://localhost:9001` | `minioadmin` / `minioadmin` |

To stop services and wipe volumes cleanly:
```bash
make down
```

---

## Running the Next.js 15 HITL Dashboard (`web/`)

### Live API Mode (Default)
Run the frontend against the local `cmd/api` server started by `make demo` (or `make up`):

```bash
cd web
npm install
NEXT_PUBLIC_API_BASE_URL=http://localhost:8080 npm run dev
```

Open `http://localhost:3000` to:
- Inspect real-time KPI cards, integer pence totals, and filterable invoice queues.
- Drag & drop `.xml`, `.pdf`, or `.csv` invoices with live SHA-256 deduplication feedback (`202 Accepted` vs `200 OK` duplicate).
- Open any invoice for the **split-pane HITL workspace**: MinIO source document preview (PDF iframe, syntax-formatted UBL XML, or parsed CSV table) alongside the 3-way PO / GRN / Invoice comparison table, discrepancy cards, immutable audit trail, and operator **Approve (`MANUALLY_APPROVED`) / Reject (`REJECTED`)** controls.
- Export deterministic RFC-4180 CSV or JSON accounting bundles preserving exact integer pence.

### Optional Static Demo Mode (Vercel / Backend-Free Preview)
When deploying `web/` to Vercel (or previewing the UI without running Docker), opt into Static Demo Mode by setting `NEXT_PUBLIC_DEMO_MODE=true` (see [`web/.env.example`](./web/.env.example)):

```bash
cd web
NEXT_PUBLIC_DEMO_MODE=true npm run dev
# or build a production bundle for Vercel:
NEXT_PUBLIC_DEMO_MODE=true npm run build
```

- When `NEXT_PUBLIC_DEMO_MODE` is **unset or `false`**, the UI always connects to the live `cmd/api` backend.
- When `NEXT_PUBLIC_DEMO_MODE=true`, `web/src/lib/api.ts` serves all 20 deterministic fixtures (`UBL_XML`, `PDF`, `CSV`), PO/GRN lines, all 13 discrepancy rules (`INT-01`–`INT-04`, `3WAY-01`–`3WAY-09`), inline source previews, SHA-256 upload deduplication, and interactive HITL resolution (`NEEDS_REVIEW -> MANUALLY_APPROVED | REJECTED` with `409 Conflict` enforcement and a **Reset Demo Data** control) directly in memory.

---

## REST API Reference (`cmd/api`)

| Method & Path | Description |
| :--- | :--- |
| `GET /healthz` | Checks PostgreSQL, MinIO, and RabbitMQ readiness (`200 OK` / `503 Service Unavailable`) |
| `POST /api/v1/invoices` | Ingests `multipart/form-data` (`file`) or raw payload (max 10 MB); returns `202 Accepted` or `200 OK` (`duplicate: true`) on SHA-256 match |
| `GET /api/v1/invoices` | Lists invoices with `status`, `supplier_code`, `source_format`, `q`, `limit`, and `offset` query filters |
| `GET /api/v1/invoices/{id}` | Returns full `InvoiceDetail` (invoice, lines, discrepancies, PO + lines, GRN summary, immutable `audit_events`) |
| `GET /api/v1/invoices/{id}/file` | Streams the immutable raw `.xml`, `.pdf`, or `.csv` file directly from MinIO |
| `POST /api/v1/invoices/{id}/resolve` | Row-locked HITL resolution (`MANUALLY_APPROVED` or `REJECTED` + required `actor` & `reason`); returns `409 Conflict` on invalid/stale state |
| `GET /api/v1/metrics` | Returns integer pence totals, status counts, discrepancy breakdown by code, and `auto_approval_rate_bps` |

---

## Testing & CI

### Local Verification Commands
```bash
# Go static analysis & race-detected unit tests (parsers, domain math, 3-way matcher, API, workers, seed)
make vet
make test

# Go integration tests against live Postgres 16, RabbitMQ 3.13, and MinIO (start stack with `make up` first)
make test-integration

# Frontend TypeScript typecheck, unit tests (integer BigInt math, exports, demo mode), and Next.js build
make test-web
# or manually:
cd web && npx tsc --noEmit && npm test && npm run build
```

### GitHub Actions CI ([`.github/workflows/ci.yml`](./.github/workflows/ci.yml))
Every push and pull request runs three parallel jobs:
1. **`go-unit`**: Static zero-float check, `go vet ./...`, and `go test -race -count=1 ./...`.
2. **`go-integration`**: Provisions **PostgreSQL 16**, **RabbitMQ 3.13**, and **MinIO**, applies `migrations/000001_init_schema.up.sql`, and executes `make test-integration` with `-race`.
3. **`web`**: Runs `npm ci`, `npx tsc --noEmit`, `npm test`, `npm run build` (live mode), and `NEXT_PUBLIC_DEMO_MODE=true npm run build` (static demo mode).

---

## Repository Structure & Documentation

```text
reconcile-engine/
├── .github/workflows/ci.yml        # GitHub Actions CI (Go unit, Go integration, Next.js web)
├── cmd/
│   ├── api/                        # Ingestion, query, file proxy, HITL resolve & metrics HTTP server
│   ├── worker/                     # RabbitMQ ParserWorker + MatcherWorker (autoAck=false, QoS=5)
│   └── seed/                       # Deterministic fixture generator + DB/API seeder & verifier
├── internal/
│   ├── domain/                     # Canonical structs, int64 pence/bps math, state machine, errors
│   ├── matcher/                    # Deterministic Pass A (INT-01..04) & Pass B (3WAY-01..09) engine
│   ├── parser/                     # UBL 2.1 XML, gopdf native PDF (+ scanned guardrail), CSV parsers
│   ├── repository/                 # PostgreSQL 16 pgx/v5 ledger with SELECT ... FOR UPDATE locking
│   ├── broker/                     # RabbitMQ 3.13 topology, TTL retry queues (1s/5s/25s), DLQ routing
│   └── storage/                    # MinIO S3-compatible content-addressed object store
├── migrations/                     # golang-migrate SQL DDL (000001_init_schema.up/down.sql)
├── web/                            # Next.js 15 App Router HITL dashboard (+ opt-in Static Demo Mode)
├── docs/                           # Architecture specs & Architecture Decision Records (ADR-001..007)
│   ├── 01-project-charter-and-scope.md
│   ├── 02-architecture-and-event-flow.md
│   ├── 03-domain-schema-and-state-machine.md
│   ├── 04-3way-matching-and-parsing-specs.md
│   ├── 05-adrs-and-tradeoffs.md
│   ├── 06-reference-docs-and-standards.md
│   └── adr/                        # Individual ADR markdown records (001–007)
├── docker-compose.yml              # Full local stack (Postgres 16, RabbitMQ 3.13, MinIO, API, Worker)
├── Dockerfile                      # Multi-stage Go build (api, worker, seed targets)
└── Makefile                        # One-command workflows (up, down, test, test-integration, demo)
```
