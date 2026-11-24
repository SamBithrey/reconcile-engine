# ADR-005: Deterministic Multi-Format Parsers over Paid Multimodal LLM / Cloud OCR APIs

- **Status:** Accepted
- **Date:** 2026-11-23

## Context
The project charter mandates £0 recurring operational cost, 100% offline execution in Docker Compose and GitHub Actions CI, and deterministic, reproducible reconciliation outcomes across a 20-invoice test suite.

## Decision
Build a pure-Go, zero-external-API `parser.Router` supporting three structured B2B formats:
1. **Peppol BIS Billing 3.0 UBL 2.1 XML** (`internal/parser/ubl.go`) via Go's standard `encoding/xml`.
2. **Structured Native PDF** (`internal/parser/pdf.go`) via pure-Go `github.com/razvandimescu/gopdf/pdf`, using coordinate-based key-value header extraction and `pdf.FindTableAcrossPages` for multi-page line tables.
3. **Supplier CSV Manifests** (`internal/parser/csv.go`) via Go's standard `encoding/csv` with strict 12-column schema validation.

Scanned or image-only PDFs without embedded text layers are explicitly detected via `page.OutlineHint()` + empty text span checks and deterministically rejected with `domain.ErrScannedPDFUnsupported`, transitioning the invoice to `PARSE_FAILED` and routing the message to `q.dlq.dead-letters`.

## Trade-offs & Rationale
- **Reproducibility & auditability:** Given the same raw bytes, every parser produces byte-identical `ParsedInvoice` output forever—no API keys, network timeouts, model version drift, or hallucinated line totals.
- **Clean extension point:** The `parser.Parser` interface allows an optional OCR or vendor-specific adapter to be added behind `parser.Router` in the future without altering RabbitMQ contracts, PostgreSQL schema, or 3-way matching rules.
- **Trade-off accepted:** Unstructured image scans and arbitrary non-standard PDF layouts are intentionally out of scope and fail closed (`PARSE_FAILED`) rather than guessing financial figures.

## Implementation References
- [`internal/parser/parser.go`](../../internal/parser/parser.go)
- [`internal/parser/ubl.go`](../../internal/parser/ubl.go)
- [`internal/parser/pdf.go`](../../internal/parser/pdf.go)
- [`internal/parser/csv.go`](../../internal/parser/csv.go)
- [`internal/parser/parser_test.go`](../../internal/parser/parser_test.go)
