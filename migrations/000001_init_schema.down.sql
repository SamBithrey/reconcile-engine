DROP INDEX IF EXISTS idx_audit_events_invoice_id;
DROP INDEX IF EXISTS idx_discrepancies_invoice_id;
DROP INDEX IF EXISTS idx_invoices_po_id;
DROP INDEX IF EXISTS idx_invoices_status;

DROP TABLE IF EXISTS audit_events;
DROP TABLE IF EXISTS discrepancies;
DROP TABLE IF EXISTS invoice_lines;
DROP TABLE IF EXISTS invoices;
DROP TABLE IF EXISTS goods_receipt_lines;
DROP TABLE IF EXISTS goods_receipts;
DROP TABLE IF EXISTS po_lines;
DROP TABLE IF EXISTS purchase_orders;
DROP TABLE IF EXISTS suppliers;

DROP TYPE IF EXISTS po_status;
DROP TYPE IF EXISTS invoice_status;
