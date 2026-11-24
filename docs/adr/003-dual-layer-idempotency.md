# ADR-003: Dual-Layer Idempotency — SHA-256 Content Hashing + Composite Unique Constraint

- **Status:** Accepted
- **Date:** 2026-11-23

## Context
In an event-driven accounts payable pipeline, duplicates arise from multiple independent sources:
1. Client HTTP timeouts/retries or operator double-clicks re-uploading the exact same file bytes.
2. Suppliers emailing a UBL XML invoice and later re-sending a PDF copy of the same invoice number.
3. RabbitMQ at-least-once delivery redelivering an unacknowledged message after a worker restart.

## Decision
Enforce defense-in-depth idempotency across ingestion, database constraints, and workers:
1. **Byte-level SHA-256 deduplication at ingestion:** `POST /api/v1/invoices` streams the request body (up to 10 MB) through `sha256.New()` via `io.TeeReader` and checks `invoices.file_sha256 UNIQUE` (backed by `ON CONFLICT (file_sha256) DO NOTHING` for concurrent upload races). Duplicate bytes immediately return `HTTP 200 OK` with `duplicate: true` and the original `invoice_id`, without publishing a second RabbitMQ event.
2. **Business-key deduplication after parsing:** PostgreSQL enforces `CONSTRAINT uq_supplier_invoice_num UNIQUE (supplier_id, invoice_number)`. If a different file (different SHA-256, e.g., PDF after XML) claims an already-recorded `(supplier_id, invoice_number)`, `SaveParsedInvoice` returns `domain.ErrDuplicateInvoice`, transitioning the duplicate upload to `PARSE_FAILED` and dead-lettering the AMQP message.
3. **Consumer-side idempotency guards:** Both `ParserWorker` and `MatcherWorker` inspect the locked invoice status before applying mutations and `Ack` already-processed redeliveries safely.

## Trade-offs & Rationale
- **Zero extra coordination infrastructure:** Leveraging PostgreSQL unique constraints avoids introducing Redis locks or an external idempotency store that could drift out of sync with the ledger.
- **Clear HTTP semantics:** `202 Accepted` indicates newly queued work, whereas `200 OK` (`duplicate: true`) confirms idempotent replay without failing automated retry clients.
- **Trade-off accepted:** Computing SHA-256 requires buffering up to the 10 MB max upload ceiling before writing to MinIO; this bound keeps API memory predictable while ensuring invalid or oversized files never touch object storage.

## Implementation References
- [`cmd/api/server.go`](../../cmd/api/server.go)
- [`internal/repository/postgres.go`](../../internal/repository/postgres.go)
- [`migrations/000001_init_schema.up.sql`](../../migrations/000001_init_schema.up.sql)
- [`cmd/worker/worker.go`](../../cmd/worker/worker.go)
