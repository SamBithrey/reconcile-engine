# Architecture Decision Records (ADRs)

## ADR-001: RabbitMQ over Apache Kafka for Event Transport
- **Status:** Accepted
- **Context:** The system requires asynchronous decoupling between HTTP ingestion, document parsing, and 3-way reconciliation, alongside retry backoffs and dead-letter handling.
- **Decision:** Use RabbitMQ 3.13 with topic exchanges, manual consumer acknowledgements (`autoAck: false`), TTL retry queues, and a Dead Letter Exchange (`reconcile.dlx`).
- **Trade-offs & Rationale:**
  - Kafka excels at high-throughput log replay and stream partitioning, but requires heavier container memory footprints and manual retry/DLQ topic plumbing.
  - RabbitMQ provides native per-message acknowledgements, `x-dead-letter-exchange` routing, and a built-in Management UI out of the box in a single lightweight Docker container—ideal for work-queue task distribution and zero-cost local demos.

## ADR-002: Integer Minor Units (`int64` Pence) over Floating-Point (`float64`)
- **Status:** Accepted
- **Context:** Financial reconciliation requires exact equality checks across line items, VAT calculations, and grand totals.
- **Decision:** Represent all currency amounts in `int64` minor units (pence) in Go, `BIGINT` in PostgreSQL, and integer pence in JSON APIs (converting to formatted `£0.00` strings only at the UI presentation layer). Tax rates are stored in basis points (`int64`, where `20.00% = 2000`).
- **Trade-offs & Rationale:**
  - IEEE 754 binary floating-point cannot represent `0.10` or `0.20` exactly (`0.1 + 0.2 = 0.30000000000000004`), causing false-positive reconciliation discrepancies.
  - Integer arithmetic is deterministic, fast, and eliminates rounding drift across Go, SQL, and TypeScript.

## ADR-003: Content-Addressable SHA-256 Deduplication + Composite Unique Constraint
- **Status:** Accepted
- **Context:** Network retries, user double-clicks, or RabbitMQ at-least-once redeliveries can cause identical invoices to be submitted or processed multiple times.
- **Decision:** Enforce two layers of idempotency:
  1. Hash raw file bytes with SHA-256 at ingestion (`invoices.file_sha256 UNIQUE`) to deduplicate identical file uploads immediately.
  2. Enforce `UNIQUE (supplier_id, invoice_number)` in PostgreSQL so a supplier cannot bill the same invoice number twice under a different file format (e.g., XML followed by PDF).
- **Trade-offs & Rationale:**
  - Protects against double-liability creation without requiring distributed locks or external coordination services.

## ADR-004: MinIO Object Storage over PostgreSQL `BYTEA`
- **Status:** Accepted
- **Context:** Original supplier PDFs and XML files must be preserved immutably for audit trails and side-by-side preview in the HITL review dashboard.
- **Decision:** Store raw files in MinIO (S3-compatible object storage) keyed by SHA-256 hash (`raw-invoices/YYYY/MM/<sha256>.<ext>`) and persist only metadata and `storage_key` in PostgreSQL.
- **Trade-offs & Rationale:**
  - Keeps PostgreSQL WAL logs, backups, and buffer cache lean while mirroring standard cloud S3 / Azure Blob Storage architecture patterns.

## ADR-005: Deterministic Parsers over Paid Multimodal LLM APIs
- **Status:** Accepted
- **Context:** The project must operate at £0 cost, run offline in CI/CD, and produce reproducible test outcomes for hiring evaluations.
- **Decision:** Support structured B2B formats (Peppol UBL 2.1 XML, text-layered PDFs via pure-Go `gopdf`, and CSV manifests) using deterministic parsers, while explicitly detecting and quarantining image-only scanned PDFs.
- **Trade-offs & Rationale:**
  - Eliminates external API keys, rate limits, latency spikes, and non-deterministic hallucination edge cases while keeping focus on distributed systems engineering.
