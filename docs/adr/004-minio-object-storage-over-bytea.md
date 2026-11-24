# ADR-004: MinIO S3-Compatible Object Storage over PostgreSQL `BYTEA`

- **Status:** Accepted
- **Date:** 2026-11-23

## Context
Every ingested invoice (UBL XML, multi-page native PDF, or CSV manifest) must be retained immutably so auditors and finance operators can inspect the exact original file alongside 3-way reconciliation results in the HITL review dashboard.

## Decision
Store raw invoice payloads in **MinIO** (S3-compatible object storage) under deterministic content-addressed keys (`raw-invoices/YYYY/MM/<sha256>.<ext>`), while storing only metadata (`file_sha256`, `storage_key`, `source_format`) in PostgreSQL. `cmd/api` proxies original files via `GET /api/v1/invoices/{id}/file` with canonical `Content-Type` headers, and `cmd/worker` fetches raw bytes by `storage_key` during parsing.

## Trade-offs & Rationale
- **Database hygiene:** Storing multi-megabyte PDFs in PostgreSQL `BYTEA` bloats TOAST tables, WAL replication streams, vacuum workloads, and logical backups. Offloading blobs to S3-compatible storage keeps PostgreSQL fast and focused on relational ledger transactions.
- **Cloud portability:** Using `github.com/minio/minio-go/v7` makes swapping local MinIO for AWS S3, Cloudflare R2, or GCS a pure environment-variable change.
- **Trade-off accepted:** Upload ingestion spans MinIO (`PutInvoice`) and PostgreSQL (`CreateUploadedInvoice`) without a distributed two-phase commit. Writing the immutable content-addressed object to MinIO *before* inserting the ledger row ensures a ledger row never points to a missing object, and orphaned objects from rare mid-upload crashes are harmless and deduplicated by SHA-256 key.

## Implementation References
- [`internal/storage/minio.go`](../../internal/storage/minio.go)
- [`internal/storage/minio_test.go`](../../internal/storage/minio_test.go)
- [`cmd/api/server.go`](../../cmd/api/server.go)
- [`docker-compose.yml`](../../docker-compose.yml)
