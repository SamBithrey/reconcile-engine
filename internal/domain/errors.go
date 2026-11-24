package domain

import "errors"

var (
	// ErrDuplicateInvoice indicates an invoice with the same content hash or
	// (supplier_id, invoice_number) already exists.
	ErrDuplicateInvoice = errors.New("duplicate invoice")

	// ErrInvalidStateTransition indicates a disallowed invoice lifecycle transition.
	ErrInvalidStateTransition = errors.New("invalid invoice state transition")

	// ErrScannedPDFUnsupported indicates a PDF has no extractable text layer
	// (e.g., scanned raster image or outlined glyph paths).
	ErrScannedPDFUnsupported = errors.New("scanned PDF without embedded text layer is unsupported")

	// ErrNotFound indicates a requested entity does not exist in the ledger.
	ErrNotFound = errors.New("not found")
)
