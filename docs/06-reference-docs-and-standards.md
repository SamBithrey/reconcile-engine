# Reference Specifications & Engineering Conventions

## 1. Peppol BIS Billing 3.0 (UBL 2.1) Minimal Valid Fixture Template
Use this canonical XML structure when implementing `internal/parser/ubl.go` and `cmd/seed`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<Invoice xmlns="urn:oasis:names:specification:ubl:schema:xsd:Invoice-2" xmlns:cac="urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2" xmlns:cbc="urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2">
  <cbc:CustomizationID>urn:cen.eu:en16931:2017#compliant#urn:fdc:peppol.eu:2017:poacc:billing:3.0</cbc:CustomizationID>
  <cbc:ProfileID>urn:fdc:peppol.eu:2017:poacc:billing:01:1.0</cbc:ProfileID>
  <cbc:ID>INV-2026-0001</cbc:ID>
  <cbc:IssueDate>2026-11-20</cbc:IssueDate>
  <cbc:DueDate>2026-12-20</cbc:DueDate>
  <cbc:InvoiceTypeCode>380</cbc:InvoiceTypeCode>
  <cbc:DocumentCurrencyCode>GBP</cbc:DocumentCurrencyCode>
  <cac:OrderReference>
    <cbc:ID>PO-2026-1001</cbc:ID>
  </cac:OrderReference>
  <cac:AccountingSupplierParty>
    <cac:Party>
      <cac:PartyIdentification>
        <cbc:ID>SUP-ACME-01</cbc:ID>
      </cac:PartyIdentification>
      <cac:PartyName>
        <cbc:Name>Acme Industrial Supplies Ltd</cbc:Name>
      </cac:PartyName>
      <cac:PartyTaxScheme>
        <cbc:CompanyID>GB123456789</cbc:CompanyID>
        <cac:TaxScheme>
          <cbc:ID>VAT</cbc:ID>
        </cac:TaxScheme>
      </cac:PartyTaxScheme>
    </cac:Party>
  </cac:AccountingSupplierParty>
  <cac:TaxTotal>
    <cbc:TaxAmount currencyID="GBP">50.00</cbc:TaxAmount>
  </cac:TaxTotal>
  <cac:LegalMonetaryTotal>
    <cbc:LineExtensionAmount currencyID="GBP">250.00</cbc:LineExtensionAmount>
    <cbc:TaxExclusiveAmount currencyID="GBP">250.00</cbc:TaxExclusiveAmount>
    <cbc:TaxInclusiveAmount currencyID="GBP">300.00</cbc:TaxInclusiveAmount>
    <cbc:PayableAmount currencyID="GBP">300.00</cbc:PayableAmount>
  </cac:LegalMonetaryTotal>
  <cac:InvoiceLine>
    <cbc:ID>1</cbc:ID>
    <cbc:InvoicedQuantity unitCode="EA">10</cbc:InvoicedQuantity>
    <cbc:LineExtensionAmount currencyID="GBP">250.00</cbc:LineExtensionAmount>
    <cac:Item>
      <cbc:Name>M12 Stainless Steel Hex Bolts (Box of 100)</cbc:Name>
      <cac:SellersItemIdentification>
        <cbc:ID>SKU-BOLT-M12</cbc:ID>
      </cac:SellersItemIdentification>
      <cac:ClassifiedTaxCategory>
        <cbc:ID>S</cbc:ID>
        <cbc:Percent>20.00</cbc:Percent>
        <cac:TaxScheme>
          <cbc:ID>VAT</cbc:ID>
        </cac:TaxScheme>
      </cac:ClassifiedTaxCategory>
    </cac:Item>
    <cac:Price>
      <cbc:PriceAmount currencyID="GBP">25.00</cbc:PriceAmount>
    </cac:Price>
  </cac:InvoiceLine>
</Invoice>
```

## 2. Repository Directory Layout

```text
reconcile-engine/
├── .github/workflows/ci.yml      # Go race tests + Postgres/RabbitMQ integration suite
├── cmd/
│   ├── api/main.go               # HTTP REST server (upload, invoices, resolve, metrics)
│   ├── worker/main.go            # RabbitMQ consumers (ParserWorker + MatcherWorker)
│   └── seed/main.go              # Generates & submits 15 POs/GRNs + 20 mixed invoices
├── docs/
│   ├── 01-project-charter-and-scope.md
│   ├── 02-architecture-and-event-flow.md
│   ├── 03-domain-schema-and-state-machine.md
│   ├── 04-3way-matching-and-parsing-specs.md
│   ├── 05-adrs-and-tradeoffs.md
│   └── 06-reference-docs-and-standards.md
├── internal/
│   ├── broker/                   # RabbitMQ connection, topology setup, publisher, retry helper
│   ├── domain/                   # Core structs, state machine transitions, money helpers
│   ├── matcher/                  # Pure 3-way reconciliation engine + unit tests
│   ├── parser/                   # Strategy interface + UBL XML, gopdf PDF, CSV parsers
│   ├── repository/               # pgx PostgreSQL queries & transactional state updates
│   └── storage/                  # MinIO S3 client wrapper
├── migrations/                   # golang-migrate up/down SQL files
├── web/                          # Next.js 15 + TypeScript + Tailwind HITL dashboard
├── docker-compose.yml            # Postgres 16, RabbitMQ 3.13, MinIO, api, worker, web
├── Makefile                      # make up, make down, make test, make demo
└── README.md
```

## 3. Go Engineering & Testing Standards
- **Error Handling:** Wrap domain and infrastructure errors with `fmt.Errorf("context: %w", err)` and define sentinel errors (`ErrDuplicateInvoice`, `ErrInvalidStateTransition`, `ErrScannedPDFUnsupported`) for clean HTTP/worker mapping.
- **Context Propagation:** Pass `context.Context` as the first parameter to all database, MinIO, and RabbitMQ operations; handle `SIGINT`/`SIGTERM` gracefully to drain in-flight RabbitMQ deliveries before closing channels.
- **Testing:**
  - All financial math, state transitions, parsers, and 3-way matching rules must have table-driven unit tests (`go test -race ./...`).
  - Integration tests run against real PostgreSQL and RabbitMQ containers (using `testcontainers-go` or Docker Compose service containers in GitHub Actions).
