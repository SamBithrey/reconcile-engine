package domain

import "fmt"

// CanTransition validates whether an invoice may transition from `from` to `to`.
// Allowed transitions:
//   - UPLOADED -> PARSED | PARSE_FAILED
//   - PARSED -> AUTO_APPROVED | NEEDS_REVIEW
//   - NEEDS_REVIEW -> MANUALLY_APPROVED | REJECTED
func CanTransition(from, to InvoiceStatus) error {
	switch from {
	case StatusUploaded:
		if to == StatusParsed || to == StatusParseFailed {
			return nil
		}
	case StatusParsed:
		if to == StatusAutoApproved || to == StatusNeedsReview {
			return nil
		}
	case StatusNeedsReview:
		if to == StatusManuallyApproved || to == StatusRejected {
			return nil
		}
	}
	return fmt.Errorf("cannot transition invoice from %q to %q: %w", from, to, ErrInvalidStateTransition)
}

// IsTerminal reports whether status is a terminal lifecycle state.
func IsTerminal(status InvoiceStatus) bool {
	switch status {
	case StatusParseFailed, StatusAutoApproved, StatusManuallyApproved, StatusRejected:
		return true
	default:
		return false
	}
}
