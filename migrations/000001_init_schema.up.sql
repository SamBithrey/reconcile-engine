CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TYPE invoice_status AS ENUM (
    'UPLOADED',
    'PARSED',
    'PARSE_FAILED',
    'AUTO_APPROVED',
    'NEEDS_REVIEW',
    'MANUALLY_APPROVED',
    'REJECTED'
);

CREATE TYPE po_status AS ENUM (
    'OPEN',
    'PARTIALLY_RECEIVED',
    'RECEIVED',
    'CLOSED',
    'CANCELLED'
);

CREATE TABLE suppliers (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    supplier_code   TEXT NOT NULL UNIQUE,
    name            TEXT NOT NULL,
    vat_number      TEXT NOT NULL,
    peppol_id       TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE purchase_orders (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    po_number       TEXT NOT NULL UNIQUE,
    supplier_id     UUID NOT NULL REFERENCES suppliers(id),
    currency        CHAR(3) NOT NULL DEFAULT 'GBP',
    status          po_status NOT NULL DEFAULT 'OPEN',
    total_net_pence BIGINT NOT NULL CHECK (total_net_pence >= 0),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE po_lines (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    purchase_order_id   UUID NOT NULL REFERENCES purchase_orders(id) ON DELETE CASCADE,
    line_number         INT NOT NULL,
    sku                 TEXT NOT NULL,
    description         TEXT NOT NULL,
    ordered_qty         BIGINT NOT NULL CHECK (ordered_qty > 0),
    unit_price_pence    BIGINT NOT NULL CHECK (unit_price_pence >= 0),
    vat_rate_bps        BIGINT NOT NULL CHECK (vat_rate_bps >= 0),
    UNIQUE (purchase_order_id, sku)
);

CREATE TABLE goods_receipts (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    grn_number          TEXT NOT NULL UNIQUE,
    purchase_order_id   UUID NOT NULL REFERENCES purchase_orders(id),
    received_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    warehouse_notes     TEXT
);

CREATE TABLE goods_receipt_lines (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    goods_receipt_id    UUID NOT NULL REFERENCES goods_receipts(id) ON DELETE CASCADE,
    sku                 TEXT NOT NULL,
    accepted_qty        BIGINT NOT NULL CHECK (accepted_qty >= 0),
    rejected_qty        BIGINT NOT NULL DEFAULT 0 CHECK (rejected_qty >= 0)
);

CREATE TABLE invoices (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    file_sha256         CHAR(64) NOT NULL UNIQUE,
    storage_key         TEXT NOT NULL,
    source_format       TEXT NOT NULL CHECK (source_format IN ('UBL_XML', 'PDF', 'CSV')),
    status              invoice_status NOT NULL DEFAULT 'UPLOADED',
    supplier_id         UUID REFERENCES suppliers(id),
    supplier_code_raw   TEXT,
    invoice_number      TEXT,
    po_number_raw       TEXT,
    purchase_order_id   UUID REFERENCES purchase_orders(id),
    issue_date          DATE,
    due_date            DATE,
    currency            CHAR(3) NOT NULL DEFAULT 'GBP',
    subtotal_pence      BIGINT NOT NULL DEFAULT 0,
    vat_total_pence     BIGINT NOT NULL DEFAULT 0,
    grand_total_pence   BIGINT NOT NULL DEFAULT 0,
    parse_error         TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_supplier_invoice_num UNIQUE (supplier_id, invoice_number)
);

CREATE TABLE invoice_lines (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id          UUID NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    line_number         INT NOT NULL,
    sku                 TEXT NOT NULL,
    description         TEXT NOT NULL,
    invoiced_qty        BIGINT NOT NULL,
    unit_price_pence    BIGINT NOT NULL,
    vat_rate_bps        BIGINT NOT NULL,
    line_net_pence      BIGINT NOT NULL,
    line_vat_pence      BIGINT NOT NULL
);

CREATE TABLE discrepancies (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id          UUID NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    invoice_line_id     UUID REFERENCES invoice_lines(id) ON DELETE CASCADE,
    code                TEXT NOT NULL,
    severity            TEXT NOT NULL CHECK (severity IN ('BLOCKING', 'WARNING')),
    message             TEXT NOT NULL,
    expected_value      TEXT,
    actual_value        TEXT,
    variance_pence      BIGINT NOT NULL DEFAULT 0,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE audit_events (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id          UUID NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    actor               TEXT NOT NULL,
    from_status         invoice_status,
    to_status           invoice_status NOT NULL,
    reason              TEXT,
    metadata            JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_invoices_status ON invoices(status);
CREATE INDEX idx_invoices_po_id ON invoices(purchase_order_id);
CREATE INDEX idx_discrepancies_invoice_id ON discrepancies(invoice_id);
CREATE INDEX idx_audit_events_invoice_id ON audit_events(invoice_id);
