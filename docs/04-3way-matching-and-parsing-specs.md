# 3-Way Matching Rules & Multi-Format Parsing Specifications

## 1. Integer Currency & VAT Arithmetic Rules
All calculations execute in `int64` minor units (pence) and basis points (`1% = 100 bps`, `20% = 2000 bps`):
- **Expected Line Net (pence):** `expected_net = quantity * unit_price_pence`
- **Expected Line VAT (pence, half-up rounding):** `expected_vat = (line_net_pence * vat_rate_bps + 5000) / 10000`
- **Expected Line Gross (pence):** `expected_gross = line_net_pence + line_vat_pence`
- **Rounding Tolerance:** `±1 pence` (`abs(expected - actual) <= 1`) is permitted on calculated VAT and document header sums to absorb line-by-line vs. invoice-level rounding differences.

## 2. Deterministic 3-Way Matching Rules

The Matcher Worker evaluates every `PARSED` invoice in two passes: **Internal Document Integrity** followed by **3-Way PO & GRN Reconciliation**.

### Pass A: Internal Invoice Integrity
| Rule ID | Discrepancy Code | Condition | Severity |
| :--- | :--- | :--- | :--- |
| `INT-01` | `LINE_MATH_ERROR` | `abs((qty * unit_price_pence) - line_net_pence) > 1` | `BLOCKING` |
| `INT-02` | `VAT_MISMATCH` | `abs(expected_line_vat - line_vat_pence) > 1` | `BLOCKING` |
| `INT-03` | `TOTAL_MISMATCH` | `abs(sum(line_net_pence) - subtotal_pence) > 1` OR `abs((subtotal_pence + vat_total_pence) - grand_total_pence) > 1` | `BLOCKING` |
| `INT-04` | `CURRENCY_MISMATCH` | `invoice.currency != po.currency` | `BLOCKING` |

### Pass B: 3-Way PO & GRN Verification
| Rule ID | Discrepancy Code | Condition | Severity |
| :--- | :--- | :--- | :--- |
| `3WAY-01` | `MISSING_PO` | `po_number_raw` is empty or does not match any `purchase_orders.po_number` | `BLOCKING` |
| `3WAY-02` | `PO_SUPPLIER_MISMATCH` | `invoice.supplier_id != po.supplier_id` | `BLOCKING` |
| `3WAY-03` | `PO_CLOSED` | `po.status IN ('CLOSED', 'CANCELLED')` | `BLOCKING` |
| `3WAY-04` | `MISSING_GRN` | Zero `goods_receipts` exist for `purchase_order_id` | `BLOCKING` |
| `3WAY-05` | `UNMATCHED_SKU` | Invoice line `sku` does not exist in `po_lines` for that PO | `BLOCKING` |
| `3WAY-06` | `QTY_EXCEEDS_GRN` | `invoiced_qty > sum(goods_receipt_lines.accepted_qty)` for that SKU | `BLOCKING` |
| `3WAY-07` | `QTY_EXCEEDS_PO` | `invoiced_qty > po_line.ordered_qty` | `BLOCKING` |
| `3WAY-08` | `PRICE_VARIANCE` | `abs(inv_unit_price - po_unit_price) > 50` (50p) AND `abs(inv_unit_price - po_unit_price) * 10000 > po_unit_price * 100` (1.00%) | `BLOCKING` |
| `3WAY-09` | `VAT_RATE_MISMATCH` | `inv_line.vat_rate_bps != po_line.vat_rate_bps` | `BLOCKING` |

- **Routing Outcome:**
  - `len(discrepancies) == 0` -> Transition to `AUTO_APPROVED`.
  - `len(discrepancies) > 0` -> Transition to `NEEDS_REVIEW`.

## 3. Multi-Format Parser Specifications

### Format 1: Peppol BIS Billing 3.0 (UBL 2.1 XML)
- **Detection:** Content-Type `application/xml` or `text/xml`, or first non-whitespace byte `<`.
- **Expected Namespace & Elements:**
  - Root element: `<Invoice xmlns="urn:oasis:names:specification:ubl:schema:xsd:Invoice-2">`
  - Header fields: `cbc:ID` (InvoiceNumber), `cbc:IssueDate` (`YYYY-MM-DD`), `cbc:DueDate`, `cbc:DocumentCurrencyCode` (`GBP`), `cac:OrderReference/cbc:ID` (PONumber).
  - Supplier fields: `cac:AccountingSupplierParty/cac:Party/cac:PartyIdentification/cbc:ID` (SupplierCode) and `cac:PartyTaxScheme/cbc:CompanyID` (VAT number).
  - Totals: `cac:TaxTotal/cbc:TaxAmount` and `cac:LegalMonetaryTotal/cbc:TaxExclusiveAmount` + `cbc:PayableAmount`.
  - Line items (`cac:InvoiceLine`): `cbc:ID`, `cbc:InvoicedQuantity`, `cbc:LineExtensionAmount` (LineNet), `cac:Item/cac:SellersItemIdentification/cbc:ID` (SKU), `cac:Item/cbc:Name`, `cac:Item/cac:ClassifiedTaxCategory/cbc:Percent` (VAT %), `cac:Price/cbc:PriceAmount`.
- **Decimal Conversion:** Parse decimal strings (e.g., `"145.50"`) via string splitting/fixed-point parsing directly into `int64` pence (`14550`) without intermediate `float64`.

### Format 2: Structured Native PDF (`github.com/razvandimescu/gopdf/pdf`)
- **Detection:** Magic bytes `%PDF-`.
- **Scanned PDF Guardrail:** Call `page.OutlineHint()` on page 0. If `hint.Possible()` is true and extracted text spans are empty, immediately return `ErrScannedPDFUnsupported` -> `PARSE_FAILED`.
- **Header Metadata Extraction:** Extract text spans with X/Y coordinates (`doc.Page(0).TextSpans()`) and match key-value anchors (`Invoice No:`, `PO Number:`, `Supplier Code:`, `Issue Date:`, `Subtotal:`, `VAT:`, `Total:`).
- **Line Table Extraction:** Use `pdf.FindTableAcrossPages(pages, &pdf.TableOpts{Headers: []string{"SKU", "Description", "Qty", "Unit Price", "VAT", "Net Total"}})` to extract multi-page line item rows deterministically.

### Format 3: Supplier CSV Manifest
- **Detection:** Content-Type `text/csv` or `.csv` extension when not XML/PDF.
- **Required Header Columns (exact order):**
  `invoice_number,po_number,supplier_code,issue_date,currency,sku,description,qty,unit_price_pence,vat_rate_bps,line_net_pence,line_vat_pence`
- **Grouping Rule:** All rows sharing the same `(supplier_code, invoice_number)` aggregate into a single `ParsedInvoice`; header totals are summed from the line rows and validated against Pass A rules.
