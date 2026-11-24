# ADR-006: Pessimistic Row-Level Locking (`SELECT ... FOR UPDATE`) for State Transitions & HITL Concurrency

- **Status:** Accepted
- **Date:** 2026-11-24

## Context
During Phase 2 and Phase 3 implementation, two concurrent mutation scenarios needed strict correctness guarantees:
1. **Worker redelivery races:** Multiple `cmd/worker` consumers (`QoS=5`) or RabbitMQ redeliveries attempting to parse or match the same invoice concurrently.
2. **Concurrent HITL operator actions:** Two finance reviewers opening the same `NEEDS_REVIEW` invoice in the Next.js dashboard and clicking `Approve (MANUALLY_APPROVED)` and `Reject (REJECTED)` seconds apart.

## Decision
Centralize all lifecycle validation in `domain.CanTransition` (`internal/domain/state_machine.go`) and enforce every database state transition inside a PostgreSQL transaction using **pessimistic row-level locking** (`SELECT status FROM invoices WHERE id = $1 FOR UPDATE`):
- Valid transitions are strictly limited to:
  - `UPLOADED -> PARSED | PARSE_FAILED`
  - `PARSED -> AUTO_APPROVED | NEEDS_REVIEW`
  - `NEEDS_REVIEW -> MANUALLY_APPROVED | REJECTED`
- Within the same transaction, the repository updates `invoices.status`, replaces child rows where applicable (`invoice_lines` / `discrepancies`), and appends an immutable `audit_events` row before committing.
- In `ResolveInvoice` (`POST /api/v1/invoices/{id}/resolve`), if another operator already transitioned the invoice out of `NEEDS_REVIEW`, `domain.CanTransition` returns `domain.ErrInvalidStateTransition`, `cmd/api` responds with **`HTTP 409 Conflict`**, and `web/src/components/ResolutionPanel.tsx` displays a conflict alert and automatically refreshes the ledger view.

## Trade-offs & Rationale
- **Stronger correctness than application-only checks:** Row locking inside the DB transaction guarantees that no two actors can both observe `NEEDS_REVIEW` and write conflicting terminal outcomes or duplicate audit events.
- **Why pessimistic `FOR UPDATE` over optimistic `version` columns?** Invoice transitions occur per-document with low per-row contention and millisecond transaction lifetimes. `SELECT ... FOR UPDATE` serializes the rare two-operator collision cleanly at the database level without requiring version-column plumbing across every query.
- **Trade-off accepted:** Transactions touching a locked invoice row must stay concise and never perform external I/O (e.g., MinIO downloads or RabbitMQ publishes occur *outside* the PostgreSQL lock window).

## Implementation References
- [`internal/domain/state_machine.go`](../../internal/domain/state_machine.go)
- [`internal/repository/postgres.go`](../../internal/repository/postgres.go)
- [`cmd/api/server.go`](../../cmd/api/server.go)
- [`web/src/components/ResolutionPanel.tsx`](../../web/src/components/ResolutionPanel.tsx)
