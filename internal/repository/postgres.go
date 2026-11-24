package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"reconcile-engine/internal/domain"
	"reconcile-engine/internal/matcher"
)

const pgUniqueViolationCode = "23505"

// InvoiceFilter controls filtering and pagination for ListInvoices.
type InvoiceFilter struct {
	Status       string
	SupplierCode string
	SourceFormat string
	Search       string
	Limit        int
	Offset       int
}

// PostgresRepo implements ledger queries and transactional state updates over pgxpool.
type PostgresRepo struct {
	pool *pgxpool.Pool
}

// Connect establishes a validated PostgreSQL connection pool.
func Connect(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, fmt.Errorf("postgres connect: database URL is required")
	}
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("postgres connect: parse config: %w", err)
	}
	cfg.MaxConns = 20
	cfg.MinConns = 2
	cfg.MaxConnLifetime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres connect: new pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres connect: ping: %w", err)
	}
	return pool, nil
}

// NewPostgresRepo wraps an existing pgxpool.Pool.
func NewPostgresRepo(pool *pgxpool.Pool) *PostgresRepo {
	return &PostgresRepo{pool: pool}
}

// Ping checks PostgreSQL connectivity.
func (r *PostgresRepo) Ping(ctx context.Context) error {
	if err := r.pool.Ping(ctx); err != nil {
		return fmt.Errorf("postgres ping: %w", err)
	}
	return nil
}

// GetInvoiceBySHA256 looks up an invoice by its content SHA-256 hex digest.
func (r *PostgresRepo) GetInvoiceBySHA256(ctx context.Context, fileSHA256 string) (*domain.Invoice, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT i.id, i.file_sha256, i.storage_key, i.source_format, i.status,
		       i.supplier_id, COALESCE(i.supplier_code_raw, ''), COALESCE(s.name, ''),
		       COALESCE(i.invoice_number, ''), COALESCE(i.po_number_raw, ''), i.purchase_order_id,
		       i.issue_date, i.due_date, TRIM(i.currency),
		       i.subtotal_pence, i.vat_total_pence, i.grand_total_pence,
		       COALESCE(i.parse_error, ''), i.created_at, i.updated_at
		FROM invoices i
		LEFT JOIN suppliers s ON s.id = i.supplier_id
		WHERE i.file_sha256 = $1
	`, fileSHA256)

	inv, err := scanInvoice(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get invoice by sha256: %w", err)
	}
	return inv, nil
}

// CreateUploadedInvoice inserts an invoice in UPLOADED state idempotently using
// ON CONFLICT (file_sha256) DO NOTHING. On duplicate SHA-256, it returns the existing
// invoice with created=false.
func (r *PostgresRepo) CreateUploadedInvoice(
	ctx context.Context,
	fileSHA256, storageKey string,
	sourceFormat domain.SourceFormat,
) (*domain.Invoice, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("create uploaded invoice: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row := tx.QueryRow(ctx, `
		INSERT INTO invoices (file_sha256, storage_key, source_format, status)
		VALUES ($1, $2, $3, 'UPLOADED')
		ON CONFLICT (file_sha256) DO NOTHING
		RETURNING id, file_sha256, storage_key, source_format, status,
		          supplier_id, COALESCE(supplier_code_raw, ''), '',
		          COALESCE(invoice_number, ''), COALESCE(po_number_raw, ''), purchase_order_id,
		          issue_date, due_date, TRIM(currency),
		          subtotal_pence, vat_total_pence, grand_total_pence,
		          COALESCE(parse_error, ''), created_at, updated_at
	`, fileSHA256, storageKey, string(sourceFormat))

	inv, err := scanInvoice(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Conflict on file_sha256; return existing row without creating an audit event.
			if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
				return nil, false, fmt.Errorf("create uploaded invoice: rollback on conflict: %w", rbErr)
			}
			existing, getErr := r.GetInvoiceBySHA256(ctx, fileSHA256)
			if getErr != nil {
				return nil, false, fmt.Errorf("create uploaded invoice: fetch existing on conflict: %w", getErr)
			}
			return existing, false, nil
		}
		return nil, false, fmt.Errorf("create uploaded invoice: insert: %w", err)
	}

	meta, err := marshalMetadata(map[string]any{
		"file_sha256":   fileSHA256,
		"storage_key":   storageKey,
		"source_format": sourceFormat,
	})
	if err != nil {
		return nil, false, fmt.Errorf("create uploaded invoice: marshal audit metadata: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (invoice_id, actor, from_status, to_status, reason, metadata)
		VALUES ($1, $2, NULL, $3, $4, $5)
	`, inv.ID, "system:api", string(domain.StatusUploaded), "Raw invoice uploaded to object storage", meta); err != nil {
		return nil, false, fmt.Errorf("create uploaded invoice: insert audit event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("create uploaded invoice: commit: %w", err)
	}
	return inv, true, nil
}

// CheckInvoiceForParsing acquires a row lock (SELECT ... FOR UPDATE) to inspect whether
// the invoice is still in UPLOADED state. If it is already PARSED or beyond, skip=true.
func (r *PostgresRepo) CheckInvoiceForParsing(ctx context.Context, invoiceID uuid.UUID) (
	status domain.InvoiceStatus,
	storageKey string,
	sourceFormat domain.SourceFormat,
	skip bool,
	err error,
) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", "", "", false, fmt.Errorf("check invoice for parsing: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var statusStr, formatStr string
	err = tx.QueryRow(ctx, `
		SELECT status, storage_key, source_format
		FROM invoices
		WHERE id = $1
		FOR UPDATE
	`, invoiceID).Scan(&statusStr, &storageKey, &formatStr)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", "", false, domain.ErrNotFound
		}
		return "", "", "", false, fmt.Errorf("check invoice for parsing: select for update: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", "", "", false, fmt.Errorf("check invoice for parsing: commit: %w", err)
	}

	status = domain.InvoiceStatus(statusStr)
	sourceFormat = domain.SourceFormat(formatStr)
	skip = status != domain.StatusUploaded
	return status, storageKey, sourceFormat, skip, nil
}

// SaveParsedInvoice atomically transitions UPLOADED -> PARSED under SELECT ... FOR UPDATE,
// persists header fields and invoice_lines, enforces uq_supplier_invoice_num (ErrDuplicateInvoice),
// and records an audit event.
func (r *PostgresRepo) SaveParsedInvoice(
	ctx context.Context,
	invoiceID uuid.UUID,
	parsed *domain.ParsedInvoice,
	supplierID *uuid.UUID,
	poID *uuid.UUID,
	actor string,
) (bool, error) {
	if parsed == nil {
		return false, fmt.Errorf("save parsed invoice: parsed invoice is nil")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("save parsed invoice: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var currentStatusStr string
	if err := tx.QueryRow(ctx, `
		SELECT status FROM invoices WHERE id = $1 FOR UPDATE
	`, invoiceID).Scan(&currentStatusStr); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, domain.ErrNotFound
		}
		return false, fmt.Errorf("save parsed invoice: lock invoice: %w", err)
	}

	currentStatus := domain.InvoiceStatus(currentStatusStr)
	if currentStatus != domain.StatusUploaded {
		if isParsedOrBeyond(currentStatus) {
			return true, nil
		}
		if err := domain.CanTransition(currentStatus, domain.StatusParsed); err != nil {
			return false, err
		}
	}

	if err := domain.CanTransition(currentStatus, domain.StatusParsed); err != nil {
		return false, err
	}

	currency := strings.ToUpper(strings.TrimSpace(parsed.Currency))
	if currency == "" {
		currency = "GBP"
	}

	var issueDate *time.Time
	if !parsed.IssueDate.IsZero() {
		d := parsed.IssueDate.UTC()
		issueDate = &d
	}

	var invNum *string
	if trimmed := strings.TrimSpace(parsed.InvoiceNumber); trimmed != "" {
		invNum = &trimmed
	}
	var supCode *string
	if trimmed := strings.TrimSpace(parsed.SupplierCode); trimmed != "" {
		supCode = &trimmed
	}
	var poNum *string
	if trimmed := strings.TrimSpace(parsed.PONumber); trimmed != "" {
		poNum = &trimmed
	}

	if _, err := tx.Exec(ctx, `
		UPDATE invoices
		SET status = 'PARSED',
		    supplier_id = $2,
		    supplier_code_raw = $3,
		    invoice_number = $4,
		    po_number_raw = $5,
		    purchase_order_id = $6,
		    issue_date = $7,
		    due_date = $8,
		    currency = $9,
		    subtotal_pence = $10,
		    vat_total_pence = $11,
		    grand_total_pence = $12,
		    parse_error = NULL,
		    updated_at = NOW()
		WHERE id = $1
	`, invoiceID, supplierID, supCode, invNum, poNum, poID,
		issueDate, parsed.DueDate, currency,
		parsed.SubtotalPence, parsed.VATTotalPence, parsed.GrandTotalPence); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolationCode {
			return false, fmt.Errorf("save parsed invoice: duplicate supplier invoice (%s): %w", parsed.InvoiceNumber, domain.ErrDuplicateInvoice)
		}
		return false, fmt.Errorf("save parsed invoice: update invoice: %w", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM invoice_lines WHERE invoice_id = $1`, invoiceID); err != nil {
		return false, fmt.Errorf("save parsed invoice: clear prior lines: %w", err)
	}

	for _, line := range parsed.Lines {
		if _, err := tx.Exec(ctx, `
			INSERT INTO invoice_lines (
				invoice_id, line_number, sku, description,
				invoiced_qty, unit_price_pence, vat_rate_bps,
				line_net_pence, line_vat_pence
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		`, invoiceID, line.LineNumber, line.SKU, line.Description,
			line.Quantity, line.UnitPricePence, line.VATRateBPS,
			line.LineNetPence, line.LineVATPence); err != nil {
			return false, fmt.Errorf("save parsed invoice: insert line %d: %w", line.LineNumber, err)
		}
	}

	meta, err := marshalMetadata(map[string]any{
		"invoice_number":    parsed.InvoiceNumber,
		"supplier_code":     parsed.SupplierCode,
		"po_number":         parsed.PONumber,
		"line_count":        len(parsed.Lines),
		"grand_total_pence": parsed.GrandTotalPence,
	})
	if err != nil {
		return false, fmt.Errorf("save parsed invoice: marshal audit metadata: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (invoice_id, actor, from_status, to_status, reason, metadata)
		VALUES ($1, $2, $3, 'PARSED', $4, $5)
	`, invoiceID, actor, string(currentStatus), "Invoice parsed deterministically", meta); err != nil {
		return false, fmt.Errorf("save parsed invoice: insert audit event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("save parsed invoice: commit: %w", err)
	}
	return false, nil
}

// MarkParseFailed atomically transitions UPLOADED -> PARSE_FAILED under SELECT ... FOR UPDATE.
func (r *PostgresRepo) MarkParseFailed(
	ctx context.Context,
	invoiceID uuid.UUID,
	parseErrMsg string,
	actor string,
	metadata map[string]any,
) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("mark parse failed: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var currentStatusStr string
	if err := tx.QueryRow(ctx, `
		SELECT status FROM invoices WHERE id = $1 FOR UPDATE
	`, invoiceID).Scan(&currentStatusStr); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, domain.ErrNotFound
		}
		return false, fmt.Errorf("mark parse failed: lock invoice: %w", err)
	}

	currentStatus := domain.InvoiceStatus(currentStatusStr)
	if currentStatus == domain.StatusParseFailed {
		return true, nil
	}
	if err := domain.CanTransition(currentStatus, domain.StatusParseFailed); err != nil {
		return false, err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE invoices
		SET status = 'PARSE_FAILED',
		    parse_error = $2,
		    updated_at = NOW()
		WHERE id = $1
	`, invoiceID, parseErrMsg); err != nil {
		return false, fmt.Errorf("mark parse failed: update invoice: %w", err)
	}

	meta, err := marshalMetadata(metadata)
	if err != nil {
		return false, fmt.Errorf("mark parse failed: marshal metadata: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (invoice_id, actor, from_status, to_status, reason, metadata)
		VALUES ($1, $2, $3, 'PARSE_FAILED', $4, $5)
	`, invoiceID, actor, string(currentStatus), parseErrMsg, meta); err != nil {
		return false, fmt.Errorf("mark parse failed: insert audit event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("mark parse failed: commit: %w", err)
	}
	return false, nil
}

// GetInvoiceForMatching retrieves an invoice and its persisted invoice_lines for 3-way matching.
func (r *PostgresRepo) GetInvoiceForMatching(ctx context.Context, invoiceID uuid.UUID) (*domain.Invoice, []domain.ParsedLine, error) {
	inv, err := r.getInvoiceByID(ctx, invoiceID)
	if err != nil {
		return nil, nil, err
	}
	lines, err := r.getInvoiceLines(ctx, invoiceID)
	if err != nil {
		return nil, nil, err
	}
	return inv, lines, nil
}

// SaveMatchResult executes the matcher reconciliation write inside a single transaction:
// locks the invoice row (SELECT ... FOR UPDATE), validates CanTransition, deletes prior
// discrepancies for invoice_id, inserts new discrepancies, updates invoices.status, and
// inserts an audit_events row atomically.
func (r *PostgresRepo) SaveMatchResult(
	ctx context.Context,
	invoiceID uuid.UUID,
	poID *uuid.UUID,
	result matcher.MatchResult,
	actor string,
) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("save match result: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var currentStatusStr string
	if err := tx.QueryRow(ctx, `
		SELECT status FROM invoices WHERE id = $1 FOR UPDATE
	`, invoiceID).Scan(&currentStatusStr); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, domain.ErrNotFound
		}
		return false, fmt.Errorf("save match result: lock invoice: %w", err)
	}

	currentStatus := domain.InvoiceStatus(currentStatusStr)
	if isMatchedOrResolved(currentStatus) {
		return true, nil
	}

	if err := domain.CanTransition(currentStatus, result.Status); err != nil {
		return false, err
	}

	if _, err := tx.Exec(ctx, `DELETE FROM discrepancies WHERE invoice_id = $1`, invoiceID); err != nil {
		return false, fmt.Errorf("save match result: delete prior discrepancies: %w", err)
	}

	codes := make([]string, 0, len(result.Discrepancies))
	var totalVariance int64
	for _, d := range result.Discrepancies {
		discID := d.ID
		if discID == uuid.Nil {
			discID = uuid.New()
		}
		severity := d.Severity
		if severity == "" {
			severity = domain.SeverityBlocking
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO discrepancies (
				id, invoice_id, invoice_line_id, code, severity,
				message, expected_value, actual_value, variance_pence
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		`, discID, invoiceID, d.InvoiceLineID, d.Code, severity,
			d.Message, d.ExpectedValue, d.ActualValue, d.VariancePence); err != nil {
			return false, fmt.Errorf("save match result: insert discrepancy %s: %w", d.Code, err)
		}
		codes = append(codes, d.Code)
		totalVariance += d.VariancePence
	}

	if _, err := tx.Exec(ctx, `
		UPDATE invoices
		SET status = $2,
		    purchase_order_id = COALESCE($3, purchase_order_id),
		    updated_at = NOW()
		WHERE id = $1
	`, invoiceID, string(result.Status), poID); err != nil {
		return false, fmt.Errorf("save match result: update invoice status: %w", err)
	}

	reason := "3-way match succeeded with zero discrepancies"
	if len(result.Discrepancies) > 0 {
		reason = fmt.Sprintf("3-way match found %d discrepancy(ies)", len(result.Discrepancies))
	}

	meta, err := marshalMetadata(map[string]any{
		"discrepancy_count":   len(result.Discrepancies),
		"discrepancy_codes":   codes,
		"total_variance_pence": totalVariance,
	})
	if err != nil {
		return false, fmt.Errorf("save match result: marshal audit metadata: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (invoice_id, actor, from_status, to_status, reason, metadata)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, invoiceID, actor, string(currentStatus), string(result.Status), reason, meta); err != nil {
		return false, fmt.Errorf("save match result: insert audit event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("save match result: commit: %w", err)
	}
	return false, nil
}

// ResolveInvoice transitions a NEEDS_REVIEW invoice to MANUALLY_APPROVED or REJECTED
// with SELECT ... FOR UPDATE locking, CanTransition enforcement, and an immutable audit event.
func (r *PostgresRepo) ResolveInvoice(
	ctx context.Context,
	invoiceID uuid.UUID,
	targetStatus domain.InvoiceStatus,
	actor, reason string,
) (*domain.Invoice, error) {
	if targetStatus != domain.StatusManuallyApproved && targetStatus != domain.StatusRejected {
		return nil, fmt.Errorf("invalid resolution target %q: %w", targetStatus, domain.ErrInvalidStateTransition)
	}
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return nil, fmt.Errorf("resolve invoice: actor is required")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve invoice: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var currentStatusStr string
	if err := tx.QueryRow(ctx, `
		SELECT status FROM invoices WHERE id = $1 FOR UPDATE
	`, invoiceID).Scan(&currentStatusStr); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("resolve invoice: lock invoice: %w", err)
	}

	currentStatus := domain.InvoiceStatus(currentStatusStr)
	if err := domain.CanTransition(currentStatus, targetStatus); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE invoices
		SET status = $2,
		    updated_at = NOW()
		WHERE id = $1
	`, invoiceID, string(targetStatus)); err != nil {
		return nil, fmt.Errorf("resolve invoice: update status: %w", err)
	}

	meta, err := marshalMetadata(map[string]any{
		"resolution": targetStatus,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve invoice: marshal metadata: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (invoice_id, actor, from_status, to_status, reason, metadata)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, invoiceID, actor, string(currentStatus), string(targetStatus), strings.TrimSpace(reason), meta); err != nil {
		return nil, fmt.Errorf("resolve invoice: insert audit event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("resolve invoice: commit: %w", err)
	}

	return r.getInvoiceByID(ctx, invoiceID)
}

// GetSupplierByCode looks up a supplier by its canonical supplier_code.
func (r *PostgresRepo) GetSupplierByCode(ctx context.Context, supplierCode string) (*domain.Supplier, error) {
	code := strings.TrimSpace(supplierCode)
	if code == "" {
		return nil, domain.ErrNotFound
	}
	var s domain.Supplier
	err := r.pool.QueryRow(ctx, `
		SELECT id, supplier_code, name, vat_number, COALESCE(peppol_id, '')
		FROM suppliers
		WHERE LOWER(supplier_code) = LOWER($1)
	`, code).Scan(&s.ID, &s.SupplierCode, &s.Name, &s.VATNumber, &s.PeppolID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get supplier by code %q: %w", code, err)
	}
	return &s, nil
}

// GetPurchaseOrderByNumber retrieves a Purchase Order and its line items by po_number.
func (r *PostgresRepo) GetPurchaseOrderByNumber(ctx context.Context, poNumber string) (*domain.PurchaseOrder, error) {
	num := strings.TrimSpace(poNumber)
	if num == "" {
		return nil, domain.ErrNotFound
	}

	var po domain.PurchaseOrder
	var statusStr string
	err := r.pool.QueryRow(ctx, `
		SELECT po.id, po.po_number, po.supplier_id, s.supplier_code,
		       TRIM(po.currency), po.status, po.total_net_pence
		FROM purchase_orders po
		JOIN suppliers s ON s.id = po.supplier_id
		WHERE po.po_number = $1
	`, num).Scan(&po.ID, &po.PONumber, &po.SupplierID, &po.SupplierCode,
		&po.Currency, &statusStr, &po.TotalNetPence)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get purchase order %q: %w", num, err)
	}
	po.Status = domain.POStatus(statusStr)

	lines, err := r.getPOLines(ctx, po.ID)
	if err != nil {
		return nil, err
	}
	po.Lines = lines
	return &po, nil
}

// GetPurchaseOrderByID retrieves a Purchase Order and its line items by UUID.
func (r *PostgresRepo) GetPurchaseOrderByID(ctx context.Context, poID uuid.UUID) (*domain.PurchaseOrder, error) {
	var po domain.PurchaseOrder
	var statusStr string
	err := r.pool.QueryRow(ctx, `
		SELECT po.id, po.po_number, po.supplier_id, s.supplier_code,
		       TRIM(po.currency), po.status, po.total_net_pence
		FROM purchase_orders po
		JOIN suppliers s ON s.id = po.supplier_id
		WHERE po.id = $1
	`, poID).Scan(&po.ID, &po.PONumber, &po.SupplierID, &po.SupplierCode,
		&po.Currency, &statusStr, &po.TotalNetPence)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get purchase order by id %s: %w", poID, err)
	}
	po.Status = domain.POStatus(statusStr)

	lines, err := r.getPOLines(ctx, po.ID)
	if err != nil {
		return nil, err
	}
	po.Lines = lines
	return &po, nil
}

// GetGoodsReceiptSummaryByPOID aggregates accepted GRN quantities per SKU for a PO.
func (r *PostgresRepo) GetGoodsReceiptSummaryByPOID(ctx context.Context, poID uuid.UUID) (*domain.GoodsReceiptSummary, error) {
	var receiptCount int
	if err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM goods_receipts WHERE purchase_order_id = $1
	`, poID).Scan(&receiptCount); err != nil {
		return nil, fmt.Errorf("count goods receipts for PO %s: %w", poID, err)
	}

	acceptedBySKU := make(map[string]int64)
	rows, err := r.pool.Query(ctx, `
		SELECT grl.sku, COALESCE(SUM(grl.accepted_qty), 0)
		FROM goods_receipt_lines grl
		JOIN goods_receipts gr ON gr.id = grl.goods_receipt_id
		WHERE gr.purchase_order_id = $1
		GROUP BY grl.sku
	`, poID)
	if err != nil {
		return nil, fmt.Errorf("aggregate goods receipt lines for PO %s: %w", poID, err)
	}
	defer rows.Close()

	for rows.Next() {
		var sku string
		var accepted int64
		if err := rows.Scan(&sku, &accepted); err != nil {
			return nil, fmt.Errorf("scan goods receipt aggregate: %w", err)
		}
		acceptedBySKU[sku] = accepted
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate goods receipt aggregates: %w", err)
	}

	return &domain.GoodsReceiptSummary{
		PurchaseOrderID: poID,
		ReceiptCount:    receiptCount,
		AcceptedBySKU:   acceptedBySKU,
	}, nil
}

// ListInvoices returns filtered invoices ordered newest first, plus total matching count.
func (r *PostgresRepo) ListInvoices(ctx context.Context, filter InvoiceFilter) ([]domain.Invoice, int, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	var conds []string
	var args []any
	argIdx := 1

	if s := strings.TrimSpace(filter.Status); s != "" {
		conds = append(conds, fmt.Sprintf("i.status = $%d::invoice_status", argIdx))
		args = append(args, s)
		argIdx++
	}
	if sc := strings.TrimSpace(filter.SupplierCode); sc != "" {
		conds = append(conds, fmt.Sprintf("(LOWER(i.supplier_code_raw) = LOWER($%d) OR LOWER(s.supplier_code) = LOWER($%d))", argIdx, argIdx))
		args = append(args, sc)
		argIdx++
	}
	if sf := strings.TrimSpace(filter.SourceFormat); sf != "" {
		conds = append(conds, fmt.Sprintf("i.source_format = $%d", argIdx))
		args = append(args, sf)
		argIdx++
	}
	if q := strings.TrimSpace(filter.Search); q != "" {
		pattern := "%" + q + "%"
		conds = append(conds, fmt.Sprintf("(i.invoice_number ILIKE $%d OR i.po_number_raw ILIKE $%d OR i.supplier_code_raw ILIKE $%d OR COALESCE(s.name, '') ILIKE $%d)", argIdx, argIdx, argIdx, argIdx))
		args = append(args, pattern)
		argIdx++
	}

	whereClause := ""
	if len(conds) > 0 {
		whereClause = "WHERE " + strings.Join(conds, " AND ")
	}

	var total int
	countSQL := fmt.Sprintf(`
		SELECT COUNT(*)
		FROM invoices i
		LEFT JOIN suppliers s ON s.id = i.supplier_id
		%s
	`, whereClause)
	if err := r.pool.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("list invoices count: %w", err)
	}

	querySQL := fmt.Sprintf(`
		SELECT i.id, i.file_sha256, i.storage_key, i.source_format, i.status,
		       i.supplier_id, COALESCE(i.supplier_code_raw, ''), COALESCE(s.name, ''),
		       COALESCE(i.invoice_number, ''), COALESCE(i.po_number_raw, ''), i.purchase_order_id,
		       i.issue_date, i.due_date, TRIM(i.currency),
		       i.subtotal_pence, i.vat_total_pence, i.grand_total_pence,
		       COALESCE(i.parse_error, ''), i.created_at, i.updated_at,
		       (SELECT COUNT(*) FROM discrepancies d WHERE d.invoice_id = i.id) AS disc_cnt
		FROM invoices i
		LEFT JOIN suppliers s ON s.id = i.supplier_id
		%s
		ORDER BY i.created_at DESC, i.id DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argIdx, argIdx+1)

	queryArgs := append(append([]any{}, args...), limit, offset)
	rows, err := r.pool.Query(ctx, querySQL, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list invoices query: %w", err)
	}
	defer rows.Close()

	invoices := make([]domain.Invoice, 0)
	for rows.Next() {
		var inv domain.Invoice
		var formatStr, statusStr string
		if err := rows.Scan(
			&inv.ID, &inv.FileSHA256, &inv.StorageKey, &formatStr, &statusStr,
			&inv.SupplierID, &inv.SupplierCodeRaw, &inv.SupplierName,
			&inv.InvoiceNumber, &inv.PONumberRaw, &inv.PurchaseOrderID,
			&inv.IssueDate, &inv.DueDate, &inv.Currency,
			&inv.SubtotalPence, &inv.VATTotalPence, &inv.GrandTotalPence,
			&inv.ParseError, &inv.CreatedAt, &inv.UpdatedAt, &inv.DiscrepancyCnt,
		); err != nil {
			return nil, 0, fmt.Errorf("list invoices scan: %w", err)
		}
		inv.SourceFormat = domain.SourceFormat(formatStr)
		inv.Status = domain.InvoiceStatus(statusStr)
		invoices = append(invoices, inv)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list invoices rows: %w", err)
	}

	return invoices, total, nil
}

// GetInvoiceDetail returns an invoice with its line items, discrepancies, audit history,
// and associated PO/GRN context if linked.
func (r *PostgresRepo) GetInvoiceDetail(ctx context.Context, invoiceID uuid.UUID) (*domain.InvoiceDetail, error) {
	inv, err := r.getInvoiceByID(ctx, invoiceID)
	if err != nil {
		return nil, err
	}

	lines, err := r.getInvoiceLines(ctx, invoiceID)
	if err != nil {
		return nil, err
	}

	discrepancies, err := r.getDiscrepancies(ctx, invoiceID)
	if err != nil {
		return nil, err
	}

	auditEvents, err := r.getAuditEvents(ctx, invoiceID)
	if err != nil {
		return nil, err
	}

	inv.DiscrepancyCnt = len(discrepancies)
	detail := &domain.InvoiceDetail{
		Invoice:       *inv,
		Lines:         lines,
		Discrepancies: discrepancies,
		AuditEvents:   auditEvents,
	}

	if inv.PurchaseOrderID != nil {
		po, poErr := r.GetPurchaseOrderByID(ctx, *inv.PurchaseOrderID)
		if poErr == nil {
			detail.PurchaseOrder = po
			grnSummary, grnErr := r.GetGoodsReceiptSummaryByPOID(ctx, po.ID)
			if grnErr == nil {
				detail.GoodsReceipts = grnSummary
			}
		}
	}

	return detail, nil
}

// GetMetrics computes ledger KPI counts and integer minor-unit monetary totals.
func (r *PostgresRepo) GetMetrics(ctx context.Context) (*domain.MetricsSummary, error) {
	summary := &domain.MetricsSummary{
		CountsByStatus:      make(map[domain.InvoiceStatus]int64),
		DiscrepanciesByCode: make([]domain.DiscrepancyCodeMetric, 0),
	}

	rows, err := r.pool.Query(ctx, `
		SELECT status, COUNT(*), COALESCE(SUM(grand_total_pence), 0)
		FROM invoices
		GROUP BY status
	`)
	if err != nil {
		return nil, fmt.Errorf("get metrics status summary: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var statusStr string
		var cnt, totalPence int64
		if err := rows.Scan(&statusStr, &cnt, &totalPence); err != nil {
			return nil, fmt.Errorf("get metrics scan status: %w", err)
		}
		st := domain.InvoiceStatus(statusStr)
		summary.CountsByStatus[st] = cnt
		summary.TotalInvoices += cnt
		summary.TotalBilledPence += totalPence

		switch st {
		case domain.StatusAutoApproved:
			summary.AutoApprovedCount = cnt
			summary.ApprovedPence += totalPence
		case domain.StatusManuallyApproved:
			summary.ManuallyApprovedCnt = cnt
			summary.ApprovedPence += totalPence
		case domain.StatusNeedsReview:
			summary.NeedsReviewCount = cnt
			summary.NeedsReviewPence += totalPence
		case domain.StatusRejected:
			summary.RejectedCount = cnt
		case domain.StatusParseFailed:
			summary.ParseFailedCount = cnt
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get metrics iterate status: %w", err)
	}

	// Auto-approval rate in basis points across reconciled invoices (integer half-up math).
	reconciledTotal := summary.AutoApprovedCount + summary.NeedsReviewCount + summary.ManuallyApprovedCnt + summary.RejectedCount
	if reconciledTotal > 0 {
		summary.AutoApprovalRateBps = (summary.AutoApprovedCount*10000 + reconciledTotal/2) / reconciledTotal
	}

	discRows, err := r.pool.Query(ctx, `
		SELECT code, COUNT(*), COALESCE(SUM(ABS(variance_pence)), 0)
		FROM discrepancies
		GROUP BY code
		ORDER BY COUNT(*) DESC, code ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("get metrics discrepancies: %w", err)
	}
	defer discRows.Close()

	for discRows.Next() {
		var m domain.DiscrepancyCodeMetric
		if err := discRows.Scan(&m.Code, &m.Count, &m.TotalVariancePence); err != nil {
			return nil, fmt.Errorf("get metrics scan discrepancy: %w", err)
		}
		summary.TotalVariancePence += m.TotalVariancePence
		summary.DiscrepanciesByCode = append(summary.DiscrepanciesByCode, m)
	}
	if err := discRows.Err(); err != nil {
		return nil, fmt.Errorf("get metrics iterate discrepancies: %w", err)
	}

	return summary, nil
}

// UpsertSupplier inserts or updates a supplier record idempotently.
func (r *PostgresRepo) UpsertSupplier(ctx context.Context, s domain.Supplier) error {
	id := s.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO suppliers (id, supplier_code, name, vat_number, peppol_id)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''))
		ON CONFLICT (supplier_code) DO UPDATE
		SET name = EXCLUDED.name,
		    vat_number = EXCLUDED.vat_number,
		    peppol_id = EXCLUDED.peppol_id
	`, id, s.SupplierCode, s.Name, s.VATNumber, s.PeppolID)
	if err != nil {
		return fmt.Errorf("upsert supplier %q: %w", s.SupplierCode, err)
	}
	return nil
}

// UpsertPurchaseOrder inserts or updates a Purchase Order and its line items idempotently.
func (r *PostgresRepo) UpsertPurchaseOrder(ctx context.Context, po domain.PurchaseOrder) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("upsert purchase order %q: begin tx: %w", po.PONumber, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	supplierID := po.SupplierID
	if supplierID == uuid.Nil && po.SupplierCode != "" {
		if err := tx.QueryRow(ctx, `SELECT id FROM suppliers WHERE supplier_code = $1`, po.SupplierCode).Scan(&supplierID); err != nil {
			return fmt.Errorf("upsert purchase order %q: lookup supplier %q: %w", po.PONumber, po.SupplierCode, err)
		}
	}

	poID := po.ID
	if poID == uuid.Nil {
		poID = uuid.New()
	}

	var persistedID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO purchase_orders (id, po_number, supplier_id, currency, status, total_net_pence)
		VALUES ($1, $2, $3, $4, $5::po_status, $6)
		ON CONFLICT (po_number) DO UPDATE
		SET supplier_id = EXCLUDED.supplier_id,
		    currency = EXCLUDED.currency,
		    status = EXCLUDED.status,
		    total_net_pence = EXCLUDED.total_net_pence
		RETURNING id
	`, poID, po.PONumber, supplierID, po.Currency, string(po.Status), po.TotalNetPence).Scan(&persistedID); err != nil {
		return fmt.Errorf("upsert purchase order %q: %w", po.PONumber, err)
	}

	for _, line := range po.Lines {
		lineID := line.ID
		if lineID == uuid.Nil {
			lineID = uuid.New()
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO po_lines (id, purchase_order_id, line_number, sku, description, ordered_qty, unit_price_pence, vat_rate_bps)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (purchase_order_id, sku) DO UPDATE
			SET line_number = EXCLUDED.line_number,
			    description = EXCLUDED.description,
			    ordered_qty = EXCLUDED.ordered_qty,
			    unit_price_pence = EXCLUDED.unit_price_pence,
			    vat_rate_bps = EXCLUDED.vat_rate_bps
		`, lineID, persistedID, line.LineNumber, line.SKU, line.Description,
			line.OrderedQty, line.UnitPricePence, line.VATRateBPS); err != nil {
			return fmt.Errorf("upsert po_line (%s, %s): %w", po.PONumber, line.SKU, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("upsert purchase order %q: commit: %w", po.PONumber, err)
	}
	return nil
}

// UpsertGoodsReceipt inserts or updates a Goods Receipt Note and its lines idempotently.
func (r *PostgresRepo) UpsertGoodsReceipt(ctx context.Context, grn domain.GoodsReceipt) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("upsert goods receipt %q: begin tx: %w", grn.GRNNumber, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	poID := grn.PurchaseOrderID
	if poID == uuid.Nil && grn.PONumber != "" {
		if err := tx.QueryRow(ctx, `SELECT id FROM purchase_orders WHERE po_number = $1`, grn.PONumber).Scan(&poID); err != nil {
			return fmt.Errorf("upsert goods receipt %q: lookup PO %q: %w", grn.GRNNumber, grn.PONumber, err)
		}
	}

	grnID := grn.ID
	if grnID == uuid.Nil {
		grnID = uuid.New()
	}
	receivedAt := grn.ReceivedAt
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	}

	var persistedID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO goods_receipts (id, grn_number, purchase_order_id, received_at, warehouse_notes)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''))
		ON CONFLICT (grn_number) DO UPDATE
		SET purchase_order_id = EXCLUDED.purchase_order_id,
		    received_at = EXCLUDED.received_at,
		    warehouse_notes = EXCLUDED.warehouse_notes
		RETURNING id
	`, grnID, grn.GRNNumber, poID, receivedAt, grn.WarehouseNotes).Scan(&persistedID); err != nil {
		return fmt.Errorf("upsert goods receipt %q: %w", grn.GRNNumber, err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM goods_receipt_lines WHERE goods_receipt_id = $1`, persistedID); err != nil {
		return fmt.Errorf("upsert goods receipt %q: clear lines: %w", grn.GRNNumber, err)
	}

	for _, line := range grn.Lines {
		lineID := line.ID
		if lineID == uuid.Nil {
			lineID = uuid.New()
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO goods_receipt_lines (id, goods_receipt_id, sku, accepted_qty, rejected_qty)
			VALUES ($1, $2, $3, $4, $5)
		`, lineID, persistedID, line.SKU, line.AcceptedQty, line.RejectedQty); err != nil {
			return fmt.Errorf("upsert goods receipt line (%s, %s): %w", grn.GRNNumber, line.SKU, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("upsert goods receipt %q: commit: %w", grn.GRNNumber, err)
	}
	return nil
}

// --- internal query helpers ---

func (r *PostgresRepo) getInvoiceByID(ctx context.Context, invoiceID uuid.UUID) (*domain.Invoice, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT i.id, i.file_sha256, i.storage_key, i.source_format, i.status,
		       i.supplier_id, COALESCE(i.supplier_code_raw, ''), COALESCE(s.name, ''),
		       COALESCE(i.invoice_number, ''), COALESCE(i.po_number_raw, ''), i.purchase_order_id,
		       i.issue_date, i.due_date, TRIM(i.currency),
		       i.subtotal_pence, i.vat_total_pence, i.grand_total_pence,
		       COALESCE(i.parse_error, ''), i.created_at, i.updated_at
		FROM invoices i
		LEFT JOIN suppliers s ON s.id = i.supplier_id
		WHERE i.id = $1
	`, invoiceID)

	inv, err := scanInvoice(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get invoice %s: %w", invoiceID, err)
	}
	return inv, nil
}

func (r *PostgresRepo) getInvoiceLines(ctx context.Context, invoiceID uuid.UUID) ([]domain.ParsedLine, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, line_number, sku, description,
		       invoiced_qty, unit_price_pence, vat_rate_bps,
		       line_net_pence, line_vat_pence
		FROM invoice_lines
		WHERE invoice_id = $1
		ORDER BY line_number ASC, id ASC
	`, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("get invoice lines for %s: %w", invoiceID, err)
	}
	defer rows.Close()

	lines := make([]domain.ParsedLine, 0)
	for rows.Next() {
		var lineID uuid.UUID
		var line domain.ParsedLine
		if err := rows.Scan(
			&lineID, &line.LineNumber, &line.SKU, &line.Description,
			&line.Quantity, &line.UnitPricePence, &line.VATRateBPS,
			&line.LineNetPence, &line.LineVATPence,
		); err != nil {
			return nil, fmt.Errorf("scan invoice line: %w", err)
		}
		idCopy := lineID
		line.LineID = &idCopy
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate invoice lines: %w", err)
	}
	return lines, nil
}

func (r *PostgresRepo) getPOLines(ctx context.Context, poID uuid.UUID) ([]domain.POLine, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, line_number, sku, description, ordered_qty, unit_price_pence, vat_rate_bps
		FROM po_lines
		WHERE purchase_order_id = $1
		ORDER BY line_number ASC, sku ASC
	`, poID)
	if err != nil {
		return nil, fmt.Errorf("get po_lines for PO %s: %w", poID, err)
	}
	defer rows.Close()

	lines := make([]domain.POLine, 0)
	for rows.Next() {
		var l domain.POLine
		if err := rows.Scan(&l.ID, &l.LineNumber, &l.SKU, &l.Description, &l.OrderedQty, &l.UnitPricePence, &l.VATRateBPS); err != nil {
			return nil, fmt.Errorf("scan po_line: %w", err)
		}
		lines = append(lines, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate po_lines: %w", err)
	}
	return lines, nil
}

func (r *PostgresRepo) getDiscrepancies(ctx context.Context, invoiceID uuid.UUID) ([]domain.Discrepancy, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT d.id, d.invoice_id, d.invoice_line_id,
		       COALESCE(il.line_number, 0), COALESCE(il.sku, ''),
		       d.code, d.severity, d.message,
		       COALESCE(d.expected_value, ''), COALESCE(d.actual_value, ''), d.variance_pence
		FROM discrepancies d
		LEFT JOIN invoice_lines il ON il.id = d.invoice_line_id
		WHERE d.invoice_id = $1
		ORDER BY d.created_at ASC, d.code ASC
	`, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("get discrepancies for %s: %w", invoiceID, err)
	}
	defer rows.Close()

	discrepancies := make([]domain.Discrepancy, 0)
	for rows.Next() {
		var d domain.Discrepancy
		if err := rows.Scan(
			&d.ID, &d.InvoiceID, &d.InvoiceLineID,
			&d.LineNumber, &d.SKU,
			&d.Code, &d.Severity, &d.Message,
			&d.ExpectedValue, &d.ActualValue, &d.VariancePence,
		); err != nil {
			return nil, fmt.Errorf("scan discrepancy: %w", err)
		}
		discrepancies = append(discrepancies, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate discrepancies: %w", err)
	}
	return discrepancies, nil
}

func (r *PostgresRepo) getAuditEvents(ctx context.Context, invoiceID uuid.UUID) ([]domain.AuditEvent, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, invoice_id, actor, from_status, to_status,
		       COALESCE(reason, ''), metadata, created_at
		FROM audit_events
		WHERE invoice_id = $1
		ORDER BY created_at ASC, id ASC
	`, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("get audit events for %s: %w", invoiceID, err)
	}
	defer rows.Close()

	events := make([]domain.AuditEvent, 0)
	for rows.Next() {
		var ev domain.AuditEvent
		var fromStatusStr *string
		var toStatusStr string
		if err := rows.Scan(
			&ev.ID, &ev.InvoiceID, &ev.Actor, &fromStatusStr, &toStatusStr,
			&ev.Reason, &ev.Metadata, &ev.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan audit event: %w", err)
		}
		if fromStatusStr != nil {
			st := domain.InvoiceStatus(*fromStatusStr)
			ev.FromStatus = &st
		}
		ev.ToStatus = domain.InvoiceStatus(toStatusStr)
		events = append(events, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit events: %w", err)
	}
	return events, nil
}

func scanInvoice(row pgx.Row) (*domain.Invoice, error) {
	var inv domain.Invoice
	var formatStr, statusStr string
	err := row.Scan(
		&inv.ID, &inv.FileSHA256, &inv.StorageKey, &formatStr, &statusStr,
		&inv.SupplierID, &inv.SupplierCodeRaw, &inv.SupplierName,
		&inv.InvoiceNumber, &inv.PONumberRaw, &inv.PurchaseOrderID,
		&inv.IssueDate, &inv.DueDate, &inv.Currency,
		&inv.SubtotalPence, &inv.VATTotalPence, &inv.GrandTotalPence,
		&inv.ParseError, &inv.CreatedAt, &inv.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	inv.SourceFormat = domain.SourceFormat(formatStr)
	inv.Status = domain.InvoiceStatus(statusStr)
	return &inv, nil
}

func marshalMetadata(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return []byte("{}"), nil
	}
	return json.Marshal(m)
}

func isParsedOrBeyond(s domain.InvoiceStatus) bool {
	switch s {
	case domain.StatusParsed,
		domain.StatusParseFailed,
		domain.StatusAutoApproved,
		domain.StatusNeedsReview,
		domain.StatusManuallyApproved,
		domain.StatusRejected:
		return true
	default:
		return false
	}
}

func isMatchedOrResolved(s domain.InvoiceStatus) bool {
	switch s {
	case domain.StatusAutoApproved,
		domain.StatusNeedsReview,
		domain.StatusManuallyApproved,
		domain.StatusRejected:
		return true
	default:
		return false
	}
}
