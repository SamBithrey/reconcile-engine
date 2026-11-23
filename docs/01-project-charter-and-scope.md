# Project Charter & Scope: Event-Driven 3-Way Invoice Reconciliation Engine

## 1. Executive Summary
A £0-cost, self-contained, event-driven backend and full-stack application that ingests multi-format B2B supplier invoices (Peppol BIS Billing 3.0 UBL XML, structured native PDFs, and CSV manifests), parses them deterministically, reconciles each invoice against internal **Purchase Orders (PO)** and **Goods Received Notes (GRN)** in PostgreSQL, and routes discrepancies to a Human-in-the-Loop (HITL) Next.js review dashboard.

## 2. Core Objectives & Portfolio Positioning
- **Zero External API Dependencies:** 100% deterministic parsing and financial validation. Runs completely offline in Docker Compose with £0 recurring infrastructure or API costs.
- **Distributed Systems Rigour:** Demonstrates asynchronous decoupling via RabbitMQ, SHA-256 content-addressable deduplication, at-least-once delivery with manual acknowledgements, TTL-based exponential retry queues, and Dead Letter Queue (DLQ) isolation.
- **Enterprise Domain Accuracy:** Implements strict 3-way matching (PO vs. GRN vs. Invoice), integer minor-unit (`int64` pence) currency arithmetic, configurable variance tolerances, and an immutable audit trail.
- **One-Command Reproducibility:** Any reviewer can run `make demo` to boot all containers, apply migrations, seed realistic POs/GRNs, ingest 20+ mixed-format test invoices, and inspect live results in the browser.

## 3. Non-Goals (Strict Scope Boundaries)
- **No Paid Cloud Services or External LLM APIs:** No OpenAI/Gemini/AWS Textract calls; scanned image-only PDFs without embedded text layers are explicitly detected and rejected to `PARSE_FAILED`.
- **No Floating-Point Currency:** `float32` and `float64` are banned for monetary amounts across Go, SQL, and TypeScript.
- **No Over-Engineered Microservice Sprawl:** Structured as a clean modular Go monorepo producing two binaries (`cmd/api` and `cmd/worker`) plus a `cmd/seed` fixture utility.

## 4. Technology Stack
- **Language & Backend:** Go 1.23+ (`net/http` standard library router, `encoding/xml`, `encoding/csv`, `github.com/razvandimescu/gopdf/pdf`, `github.com/rabbitmq/amqp091-go`, `github.com/jackc/pgx/v5`).
- **Message Broker:** RabbitMQ 3.13 (Management Alpine) with topic exchanges, quorum/durable queues, and Dead Letter Exchanges (DLX).
- **Primary Database:** PostgreSQL 16 with `golang-migrate` SQL schema migrations.
- **Object Storage:** MinIO (S3-compatible container) for raw immutable invoice files.
- **Frontend Dashboard:** Next.js 15 (App Router), TypeScript, Tailwind CSS.
- **Orchestration & CI:** Docker Compose, GNU Make, GitHub Actions (automated race-detector unit tests + containerized Postgres/RabbitMQ integration tests).

## 5. Phased Delivery Roadmap
1. **Phase 1 — Domain Core, Fixtures & Parsers:** Define canonical Go models, integer pence math helpers, 3-way matcher rules, deterministic XML/PDF/CSV parsers, synthetic fixture generator (`cmd/seed`), and table-driven unit tests (`go test -race ./...`).
2. **Phase 2 — Event-Driven Backbone & Ledger:** Stand up Postgres, RabbitMQ, and MinIO via Docker Compose; build `cmd/api` (upload, query, resolve, metrics) and `cmd/worker` (parser consumer + matcher consumer + retry/DLQ topology).
3. **Phase 3 — HITL Review Dashboard:** Build Next.js split-pane verification UI (source document viewer + 3-way PO/GRN/Invoice variance table + audit-logged resolution actions + CSV/JSON accounting export).
4. **Phase 4 — Portfolio Packaging & CI:** Configure GitHub Actions workflow, write Architecture Decision Records (`/docs/adr`), implement `make demo`, and deploy an optional static Vercel Demo Mode snapshot.
