package matcher

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	"reconcile-engine/internal/domain"
)

// Discrepancy codes produced by Pass A (internal integrity) and Pass B (3-way matching).
const (
	CodeLineMathError      = "LINE_MATH_ERROR"      // INT-01
	CodeVATMismatch        = "VAT_MISMATCH"         // INT-02
	CodeTotalMismatch      = "TOTAL_MISMATCH"       // INT-03
	CodeCurrencyMismatch   = "CURRENCY_MISMATCH"    // INT-04
	CodeMissingPO          = "MISSING_PO"           // 3WAY-01
	CodePOSupplierMismatch = "PO_SUPPLIER_MISMATCH" // 3WAY-02
	CodePOClosed           = "PO_CLOSED"            // 3WAY-03
	CodeMissingGRN         = "MISSING_GRN"          // 3WAY-04
	CodeUnmatchedSKU       = "UNMATCHED_SKU"        // 3WAY-05
	CodeQtyExceedsGRN      = "QTY_EXCEEDS_GRN"      // 3WAY-06
	CodeQtyExceedsPO       = "QTY_EXCEEDS_PO"       // 3WAY-07
	CodePriceVariance      = "PRICE_VARIANCE"       // 3WAY-08
	CodeVATRateMismatch    = "VAT_RATE_MISMATCH"    // 3WAY-09
)

const (
	roundingTolerancePence int64 = 1
	priceTolerancePence    int64 = 50  // £0.50
	priceToleranceBPS      int64 = 100 // 1.00% (compared via diff*10000 > poPrice*100)
)

// MatchInput provides all immutable inputs needed for pure, deterministic 3-way reconciliation.
type MatchInput struct {
	InvoiceID  uuid.UUID
	SupplierID *uuid.UUID
	Invoice    domain.ParsedInvoice
	PO         *domain.PurchaseOrder
	GRN        *domain.GoodsReceiptSummary
}

// MatchResult captures the deterministic reconciliation outcome and any discrepancies found.
type MatchResult struct {
	Status        domain.InvoiceStatus `json:"status"`
	Discrepancies []domain.Discrepancy `json:"discrepancies"`
}

// Evaluate runs Pass A (Internal Invoice Integrity) followed by Pass B (3-Way PO & GRN Verification).
// It is pure and deterministic, using integer minor-unit arithmetic exclusively.
func Evaluate(input MatchInput) MatchResult {
	var discrepancies []domain.Discrepancy

	addDiscrepancy := func(line *domain.ParsedLine, code, message, expected, actual string, variancePence int64) {
		d := domain.Discrepancy{
			ID:            uuid.New(),
			InvoiceID:     input.InvoiceID,
			Code:          code,
			Severity:      domain.SeverityBlocking,
			Message:       message,
			ExpectedValue: expected,
			ActualValue:   actual,
			VariancePence: variancePence,
		}
		if line != nil {
			d.InvoiceLineID = line.LineID
			d.LineNumber = line.LineNumber
			d.SKU = line.SKU
		}
		discrepancies = append(discrepancies, d)
	}

	// -------------------------------------------------------------------------
	// Pass A: Internal Invoice Integrity (INT-01 through INT-04)
	// -------------------------------------------------------------------------
	var sumLineNet int64
	for i := range input.Invoice.Lines {
		line := &input.Invoice.Lines[i]
		sumLineNet += line.LineNetPence

		// INT-01: LINE_MATH_ERROR
		expectedNet := domain.CalculateLineNetPence(line.Quantity, line.UnitPricePence)
		netDiff := line.LineNetPence - expectedNet
		if domain.AbsInt64(netDiff) > roundingTolerancePence {
			addDiscrepancy(
				line,
				CodeLineMathError,
				fmt.Sprintf("line %d (%s): qty %d * unit price %s != line net %s",
					line.LineNumber, line.SKU, line.Quantity,
					domain.FormatPence(line.UnitPricePence), domain.FormatPence(line.LineNetPence)),
				domain.FormatPence(expectedNet),
				domain.FormatPence(line.LineNetPence),
				netDiff,
			)
		}

		// INT-02: VAT_MISMATCH
		expectedVAT := domain.CalculateVATPence(line.LineNetPence, line.VATRateBPS)
		vatDiff := line.LineVATPence - expectedVAT
		if domain.AbsInt64(vatDiff) > roundingTolerancePence {
			addDiscrepancy(
				line,
				CodeVATMismatch,
				fmt.Sprintf("line %d (%s): expected VAT %s at %s on net %s, got %s",
					line.LineNumber, line.SKU,
					domain.FormatPence(expectedVAT), domain.FormatBPS(line.VATRateBPS),
					domain.FormatPence(line.LineNetPence), domain.FormatPence(line.LineVATPence)),
				domain.FormatPence(expectedVAT),
				domain.FormatPence(line.LineVATPence),
				vatDiff,
			)
		}
	}

	// INT-03: TOTAL_MISMATCH
	subtotalDiff := input.Invoice.SubtotalPence - sumLineNet
	if domain.AbsInt64(subtotalDiff) > roundingTolerancePence {
		addDiscrepancy(
			nil,
			CodeTotalMismatch,
			fmt.Sprintf("header subtotal %s does not match sum of line net amounts %s",
				domain.FormatPence(input.Invoice.SubtotalPence), domain.FormatPence(sumLineNet)),
			domain.FormatPence(sumLineNet),
			domain.FormatPence(input.Invoice.SubtotalPence),
			subtotalDiff,
		)
	}

	expectedGrandTotal := input.Invoice.SubtotalPence + input.Invoice.VATTotalPence
	grandTotalDiff := input.Invoice.GrandTotalPence - expectedGrandTotal
	if domain.AbsInt64(grandTotalDiff) > roundingTolerancePence {
		addDiscrepancy(
			nil,
			CodeTotalMismatch,
			fmt.Sprintf("grand total %s does not match subtotal + VAT (%s)",
				domain.FormatPence(input.Invoice.GrandTotalPence), domain.FormatPence(expectedGrandTotal)),
			domain.FormatPence(expectedGrandTotal),
			domain.FormatPence(input.Invoice.GrandTotalPence),
			grandTotalDiff,
		)
	}

	// INT-04: CURRENCY_MISMATCH (when PO exists)
	if input.PO != nil {
		invCurr := strings.ToUpper(strings.TrimSpace(input.Invoice.Currency))
		poCurr := strings.ToUpper(strings.TrimSpace(input.PO.Currency))
		if invCurr != poCurr {
			addDiscrepancy(
				nil,
				CodeCurrencyMismatch,
				fmt.Sprintf("invoice currency %q does not match PO %s currency %q",
					invCurr, input.PO.PONumber, poCurr),
				poCurr,
				invCurr,
				0,
			)
		}
	}

	// -------------------------------------------------------------------------
	// Pass B: 3-Way PO & GRN Verification (3WAY-01 through 3WAY-09)
	// -------------------------------------------------------------------------
	poNum := strings.TrimSpace(input.Invoice.PONumber)
	if poNum == "" || input.PO == nil {
		actual := poNum
		if actual == "" {
			actual = "(empty)"
		}
		addDiscrepancy(
			nil,
			CodeMissingPO,
			fmt.Sprintf("purchase order reference %q is missing or not found", actual),
			"existing purchase order",
			actual,
			input.Invoice.GrandTotalPence,
		)
		return buildResult(discrepancies)
	}

	// 3WAY-02: PO_SUPPLIER_MISMATCH
	if expectedSup, actualSup, mismatch := checkSupplierMismatch(input); mismatch {
		addDiscrepancy(
			nil,
			CodePOSupplierMismatch,
			fmt.Sprintf("invoice supplier %q does not match PO %s supplier %q",
				actualSup, input.PO.PONumber, expectedSup),
			expectedSup,
			actualSup,
			input.Invoice.GrandTotalPence,
		)
	}

	// 3WAY-03: PO_CLOSED
	if input.PO.Status == domain.POStatusClosed || input.PO.Status == domain.POStatusCancelled {
		addDiscrepancy(
			nil,
			CodePOClosed,
			fmt.Sprintf("PO %s has status %s and cannot be billed", input.PO.PONumber, input.PO.Status),
			"OPEN | PARTIALLY_RECEIVED | RECEIVED",
			string(input.PO.Status),
			input.Invoice.GrandTotalPence,
		)
	}

	// 3WAY-04: MISSING_GRN
	hasGRN := input.GRN != nil && input.GRN.ReceiptCount > 0
	if !hasGRN {
		addDiscrepancy(
			nil,
			CodeMissingGRN,
			fmt.Sprintf("no goods received notes (GRN) exist for PO %s", input.PO.PONumber),
			">= 1 GRN",
			"0 GRNs",
			input.Invoice.GrandTotalPence,
		)
	}

	// Index PO lines by SKU for O(1) lookup.
	poLinesBySKU := make(map[string]domain.POLine, len(input.PO.Lines))
	for _, pl := range input.PO.Lines {
		poLinesBySKU[pl.SKU] = pl
	}

	// Track cumulative invoiced quantity per SKU in case an invoice splits a SKU across lines.
	cumQtyBySKU := make(map[string]int64, len(input.Invoice.Lines))

	for i := range input.Invoice.Lines {
		line := &input.Invoice.Lines[i]
		poLine, found := poLinesBySKU[line.SKU]

		// 3WAY-05: UNMATCHED_SKU
		if !found {
			addDiscrepancy(
				line,
				CodeUnmatchedSKU,
				fmt.Sprintf("line %d: SKU %q does not exist on PO %s", line.LineNumber, line.SKU, input.PO.PONumber),
				fmt.Sprintf("SKU on PO %s", input.PO.PONumber),
				line.SKU,
				line.LineNetPence,
			)
			continue
		}

		cumQtyBySKU[line.SKU] += line.Quantity
		invoicedQty := cumQtyBySKU[line.SKU]

		// 3WAY-06: QTY_EXCEEDS_GRN
		if hasGRN {
			acceptedQty := input.GRN.AcceptedBySKU[line.SKU]
			if invoicedQty > acceptedQty {
				excessQty := invoicedQty - acceptedQty
				addDiscrepancy(
					line,
					CodeQtyExceedsGRN,
					fmt.Sprintf("line %d (%s): invoiced qty %d exceeds GRN accepted qty %d",
						line.LineNumber, line.SKU, invoicedQty, acceptedQty),
					fmt.Sprintf("%d", acceptedQty),
					fmt.Sprintf("%d", invoicedQty),
					excessQty*line.UnitPricePence,
				)
			}
		}

		// 3WAY-07: QTY_EXCEEDS_PO
		if invoicedQty > poLine.OrderedQty {
			excessQty := invoicedQty - poLine.OrderedQty
			addDiscrepancy(
				line,
				CodeQtyExceedsPO,
				fmt.Sprintf("line %d (%s): invoiced qty %d exceeds PO ordered qty %d",
					line.LineNumber, line.SKU, invoicedQty, poLine.OrderedQty),
				fmt.Sprintf("%d", poLine.OrderedQty),
				fmt.Sprintf("%d", invoicedQty),
				excessQty*line.UnitPricePence,
			)
		}

		// 3WAY-08: PRICE_VARIANCE
		// abs(inv - po) > 50p AND abs(inv - po) * 10000 > po * 100 (1.00%)
		priceDiff := line.UnitPricePence - poLine.UnitPricePence
		absPriceDiff := domain.AbsInt64(priceDiff)
		if absPriceDiff > priceTolerancePence && absPriceDiff*10000 > poLine.UnitPricePence*priceToleranceBPS {
			addDiscrepancy(
				line,
				CodePriceVariance,
				fmt.Sprintf("line %d (%s): unit price %s exceeds tolerance against PO price %s",
					line.LineNumber, line.SKU,
					domain.FormatPence(line.UnitPricePence), domain.FormatPence(poLine.UnitPricePence)),
				domain.FormatPence(poLine.UnitPricePence),
				domain.FormatPence(line.UnitPricePence),
				priceDiff*line.Quantity,
			)
		}

		// 3WAY-09: VAT_RATE_MISMATCH
		if line.VATRateBPS != poLine.VATRateBPS {
			expectedVATAtPORate := domain.CalculateVATPence(line.LineNetPence, poLine.VATRateBPS)
			addDiscrepancy(
				line,
				CodeVATRateMismatch,
				fmt.Sprintf("line %d (%s): VAT rate %s does not match PO VAT rate %s",
					line.LineNumber, line.SKU,
					domain.FormatBPS(line.VATRateBPS), domain.FormatBPS(poLine.VATRateBPS)),
				domain.FormatBPS(poLine.VATRateBPS),
				domain.FormatBPS(line.VATRateBPS),
				line.LineVATPence-expectedVATAtPORate,
			)
		}
	}

	return buildResult(discrepancies)
}

func checkSupplierMismatch(input MatchInput) (expected, actual string, mismatch bool) {
	poHasID := input.PO.SupplierID != uuid.Nil
	invHasID := input.SupplierID != nil && *input.SupplierID != uuid.Nil

	if poHasID && invHasID {
		if *input.SupplierID != input.PO.SupplierID {
			return input.PO.SupplierID.String(), input.SupplierID.String(), true
		}
		return "", "", false
	}

	poCode := strings.TrimSpace(input.PO.SupplierCode)
	invCode := strings.TrimSpace(input.Invoice.SupplierCode)
	if poCode != "" {
		if !strings.EqualFold(poCode, invCode) {
			return poCode, invCode, true
		}
		return "", "", false
	}

	if poHasID && !invHasID {
		return input.PO.SupplierID.String(), invCode, true
	}

	return "", "", false
}

func buildResult(discrepancies []domain.Discrepancy) MatchResult {
	if len(discrepancies) == 0 {
		return MatchResult{
			Status:        domain.StatusAutoApproved,
			Discrepancies: nil,
		}
	}
	return MatchResult{
		Status:        domain.StatusNeedsReview,
		Discrepancies: discrepancies,
	}
}
