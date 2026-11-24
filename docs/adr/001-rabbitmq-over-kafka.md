# ADR-001: RabbitMQ 3.13 over Apache Kafka for Event Transport

- **Status:** Accepted
- **Date:** 2026-11-23

## Context
The invoice reconciliation pipeline must decouple synchronous HTTP uploads (`cmd/api`) from asynchronous multi-format parsing and 3-way PO/GRN reconciliation (`cmd/worker`). Transient infrastructure blips (PostgreSQL or MinIO restarts) require non-blocking exponential backoff retries without head-of-line blocking, while permanently corrupt documents or exhausted retries must be quarantined for operator inspection.

## Decision
Use **RabbitMQ 3.13** (`rabbitmq:3.13-management-alpine`) with:
- Durable exchanges: `reconcile.events` (`topic`), `reconcile.retry` (`direct`), and `reconcile.dlx` (`topic`).
- Durable primary queues (`q.invoice.uploaded`, `q.invoice.parsed`, `q.invoice.reconciled`) bound with `x-dead-letter-exchange: reconcile.dlx`.
- Manual consumer acknowledgements (`autoAck: false`) and bounded prefetch (`channel.Qos(5, 0, false)`).
- Three non-blocking TTL delay queues (`q.retry.1s`, `q.retry.5s`, `q.retry.25s`) that dead-letter back to `reconcile.events` while preserving the original routing key and incrementing the `x-retry-count` header.
- Immediate `Nack(requeue=false)` to `reconcile.dlx` -> `q.dlq.dead-letters` on permanent parse errors or when `x-retry-count >= 3`.

## Trade-offs & Rationale
- **Why not Apache Kafka?** Kafka excels at high-throughput partitioned event streaming and long-retention log replay, but requires significantly more RAM in local Docker setups and lacks built-in per-message `Ack`/`Nack` or native TTL dead-letter routing (requiring custom consumer pause/resume logic or sidecar delay topics).
- **Why RabbitMQ fits this domain:** Invoice reconciliation is a classic competing-consumers work queue where individual messages can fail independently while sibling messages continue processing. RabbitMQ delivers native per-message acknowledgements, declarative DLX routing, and a built-in Management UI (`http://localhost:15672`) in one lightweight container.
- **Trade-off accepted:** Messages are removed from queues once acknowledged rather than retained as an indefinite replayable log; immutable auditability is instead owned by PostgreSQL (`audit_events`) and MinIO (`raw-invoices/`).

## Implementation References
- [`internal/broker/events.go`](../../internal/broker/events.go)
- [`internal/broker/rabbitmq.go`](../../internal/broker/rabbitmq.go)
- [`internal/broker/rabbitmq_test.go`](../../internal/broker/rabbitmq_test.go)
- [`cmd/worker/worker.go`](../../cmd/worker/worker.go)
