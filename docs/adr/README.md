# Architecture Decision Records (`docs/adr/`)

This directory contains the individual Architecture Decision Records (ADRs) for **Reconcile Engine**. A concise single-page summary is also maintained in [`docs/05-adrs-and-tradeoffs.md`](../05-adrs-and-tradeoffs.md).

| ADR | Title | Status |
| :--- | :--- | :--- |
| [ADR-001](./001-rabbitmq-over-kafka.md) | RabbitMQ 3.13 over Apache Kafka for Event Transport | Accepted |
| [ADR-002](./002-integer-minor-units-over-floats.md) | Integer Minor Units (`int64` Pence & Basis Points) over Floating-Point (`float64`) | Accepted |
| [ADR-003](./003-dual-layer-idempotency.md) | Dual-Layer Idempotency: SHA-256 Deduplication + Composite Unique Constraint | Accepted |
| [ADR-004](./004-minio-object-storage-over-bytea.md) | MinIO S3-Compatible Object Storage over PostgreSQL `BYTEA` | Accepted |
| [ADR-005](./005-deterministic-parsers-over-llm-apis.md) | Deterministic Multi-Format Parsers over Paid Multimodal LLM / Cloud OCR APIs | Accepted |
| [ADR-006](./006-hitl-state-concurrency-and-row-locking.md) | Pessimistic Row-Level Locking (`SELECT ... FOR UPDATE`) for State Transitions & HITL Concurrency | Accepted |
| [ADR-007](./007-bigint-ui-and-static-demo-mode.md) | Pure `BigInt` Currency Presentation Formatting & Opt-In Static Demo Mode (`web/`) | Accepted |
