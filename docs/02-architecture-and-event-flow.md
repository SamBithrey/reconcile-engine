# System Architecture & Event-Driven Topology

## 1. High-Level Component Architecture

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
                 |  MinIO (S3)     |                      |  PostgreSQL 16  |                      |    RabbitMQ      |
                 |  raw-invoices/  |                      |  Ledger & State |                      | reconcile.events |
                 +--------+--------+                      +--------+--------+                      +--------+---------+
                          ^                                        ^                                        |
                          | Fetch raw bytes                        | Update PARSED / PARSE_FAILED           | Consume (QoS=5)
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
                                                            | Write discrepancies & transition state
                                                            v
                                                   +-----------------+       GET / resolve       +------------------+
                                                   |  PostgreSQL 16  | <------------------------ |  Next.js HITL UI |
                                                   |  AUTO_APPROVED  | ------------------------> |  (Port 3000)     |
                                                   |  NEEDS_REVIEW   |                           +------------------+
                                                   +-----------------+
```

## 2. RabbitMQ Exchange & Queue Topology

### Exchanges
- **`reconcile.events` (type: `topic`, durable: `true`):** Primary domain event bus.
- **`reconcile.retry` (type: `direct`, durable: `true`):** Routes transient failures into tiered TTL delay queues.
- **`reconcile.dlx` (type: `topic`, durable: `true`):** Dead Letter Exchange capturing poison messages or exhausted retries.

### Primary Queues
| Queue Name | Bound Exchange | Routing Key | Dead Letter Exchange | Dead Letter Routing Key |
| :--- | :--- | :--- | :--- | :--- |
| `q.invoice.uploaded` | `reconcile.events` | `invoice.uploaded` | `reconcile.dlx` | `dlq.invoice.uploaded` |
| `q.invoice.parsed` | `reconcile.events` | `invoice.parsed` | `reconcile.dlx` | `dlq.invoice.parsed` |
| `q.invoice.reconciled` | `reconcile.events` | `invoice.reconciled` | `reconcile.dlx` | `dlq.invoice.reconciled` |
| `q.dlq.dead-letters` | `reconcile.dlx` | `#` | — | — |

### Non-Blocking Exponential Retry Queues
To prevent head-of-line blocking during transient DB or MinIO blips, workers inspect the `x-retry-count` header on failure:
- **Attempt 1 fails (`retry_count == 0`):** Publish to `reconcile.retry` -> `q.retry.1s` (`x-message-ttl: 1000`, `x-dead-letter-exchange: reconcile.events`, preserves original routing key), then `Ack` original message.
- **Attempt 2 fails (`retry_count == 1`):** Route to `q.retry.5s` (`x-message-ttl: 5000`), then `Ack`.
- **Attempt 3 fails (`retry_count == 2`):** Route to `q.retry.25s` (`x-message-ttl: 25000`), then `Ack`.
- **Attempt 4 fails (`retry_count >= 3`) OR Permanent Parse Error:** Call `Nack(requeue=false)` so RabbitMQ immediately routes the message to `reconcile.dlx` -> `q.dlq.dead-letters`.

## 3. Event Envelope Schema (JSON)
All messages published to `reconcile.events` share a strict envelope:

```json
{
  "event_id": "019358a4-7c1b-7000-8000-000000000001",
  "event_type": "invoice.uploaded",
  "occurred_at": "2026-11-23T14:30:00Z",
  "correlation_id": "req-9f8e7d6c",
  "payload": {
    "invoice_id": "550e8400-e29b-41d4-a716-446655440000",
    "file_sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
    "storage_key": "raw-invoices/2026/11/e3b0c44298fc1c14.xml",
    "content_type": "application/xml"
  }
}
```

## 4. Concurrency & Idempotency Guarantees
1. **Upload Deduplication:** `POST /api/v1/invoices` streams the upload through a `sha256.New()` tee-reader while buffering/validating size (max 10 MB). If `file_sha256` already exists in `invoices`, the API returns `200 OK` with the existing `invoice_id` without re-publishing an event.
2. **Consumer Idempotency:**
   - **Parser Worker:** Checks `SELECT status FROM invoices WHERE id = $1 FOR UPDATE`. If status is already `PARSED` or beyond, it immediately `Ack`s and skips reprocessing.
   - **Matcher Worker:** Runs inside a single database transaction: deletes any prior uncommitted discrepancies for `invoice_id`, inserts newly evaluated discrepancies, updates `invoices.status`, inserts an `audit_events` row, and commits before sending `Ack` to RabbitMQ.
3. **Worker Prefetch (`QoS`):** Each consumer channel sets `channel.Qos(5, 0, false)` so in-flight memory stays bounded and work distributes evenly across scaled worker replicas.
