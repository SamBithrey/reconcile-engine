package domain_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"reconcile-engine/internal/domain"
)

func TestCanTransition(t *testing.T) {
	allStatuses := []domain.InvoiceStatus{
		domain.StatusUploaded,
		domain.StatusParsed,
		domain.StatusParseFailed,
		domain.StatusAutoApproved,
		domain.StatusNeedsReview,
		domain.StatusManuallyApproved,
		domain.StatusRejected,
	}

	allowed := map[[2]domain.InvoiceStatus]bool{
		{domain.StatusUploaded, domain.StatusParsed}:              true,
		{domain.StatusUploaded, domain.StatusParseFailed}:         true,
		{domain.StatusParsed, domain.StatusAutoApproved}:          true,
		{domain.StatusParsed, domain.StatusNeedsReview}:           true,
		{domain.StatusNeedsReview, domain.StatusManuallyApproved}: true,
		{domain.StatusNeedsReview, domain.StatusRejected}:         true,
	}

	for _, from := range allStatuses {
		for _, to := range allStatuses {
			name := string(from) + "_to_" + string(to)
			t.Run(name, func(t *testing.T) {
				err := domain.CanTransition(from, to)
				wantOK := allowed[[2]domain.InvoiceStatus{from, to}]
				if wantOK && err != nil {
					t.Fatalf("expected valid transition %s -> %s, got error: %v", from, to, err)
				}
				if !wantOK {
					if err == nil {
						t.Fatalf("expected ErrInvalidStateTransition for %s -> %s, got nil", from, to)
					}
					if !errors.Is(err, domain.ErrInvalidStateTransition) {
						t.Fatalf("expected error wrapping ErrInvalidStateTransition, got %v", err)
					}
				}
			})
		}
	}
}

func TestIsTerminal(t *testing.T) {
	tests := []struct {
		status   domain.InvoiceStatus
		terminal bool
	}{
		{domain.StatusUploaded, false},
		{domain.StatusParsed, false},
		{domain.StatusNeedsReview, false},
		{domain.StatusParseFailed, true},
		{domain.StatusAutoApproved, true},
		{domain.StatusManuallyApproved, true},
		{domain.StatusRejected, true},
	}

	for _, tc := range tests {
		t.Run(string(tc.status), func(t *testing.T) {
			if got := domain.IsTerminal(tc.status); got != tc.terminal {
				t.Fatalf("IsTerminal(%s) = %v, want %v", tc.status, got, tc.terminal)
			}
		})
	}
}

func TestParseDecimalToPence(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int64
		wantErr bool
	}{
		{name: "whole pounds", input: "250", want: 25000},
		{name: "two decimals", input: "250.00", want: 25000},
		{name: "one decimal", input: "145.5", want: 14550},
		{name: "exact pence", input: "145.50", want: 14550},
		{name: "zero", input: "0.00", want: 0},
		{name: "leading dot", input: ".75", want: 75},
		{name: "pound symbol and commas", input: "£1,234.56", want: 123456},
		{name: "ISO GBP prefix", input: "GBP 1,234.56", want: 123456},
		{name: "euro symbol", input: "€99.99", want: 9999},
		{name: "dollar symbol", input: "$10.05", want: 1005},
		{name: "negative amount", input: "-12.34", want: -1234},
		{name: "negative with currency", input: "-£0.50", want: -50},
		{name: "currency before negative", input: "£-0.50", want: -50},
		{name: "half-up round up", input: "10.005", want: 1001},
		{name: "half-up round down", input: "10.004", want: 1000},
		{name: "whitespace trimmed", input: "  42.10  ", want: 4210},
		{name: "classic float trap 0.10", input: "0.10", want: 10},
		{name: "classic float trap 0.20", input: "0.20", want: 20},
		{name: "empty", input: "", wantErr: true},
		{name: "letters", input: "abc", wantErr: true},
		{name: "multiple dots", input: "12.34.56", wantErr: true},
		{name: "only currency symbol", input: "£", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := domain.ParseDecimalToPence(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseDecimalToPence(%q) = %d, want error", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDecimalToPence(%q) unexpected error: %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("ParseDecimalToPence(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestParsePercentToBPS(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int64
		wantErr bool
	}{
		{name: "standard 20 percent", input: "20.00", want: 2000},
		{name: "with percent sign", input: "20%", want: 2000},
		{name: "with decimals and sign", input: "20.00%", want: 2000},
		{name: "reduced 5 percent", input: "5", want: 500},
		{name: "fractional percent", input: "17.5%", want: 1750},
		{name: "zero percent", input: "0.00%", want: 0},
		{name: "invalid", input: "twenty%", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := domain.ParsePercentToBPS(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParsePercentToBPS(%q) = %d, want error", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePercentToBPS(%q) unexpected error: %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("ParsePercentToBPS(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestCalculateVATPence(t *testing.T) {
	tests := []struct {
		name    string
		net     int64
		rateBPS int64
		want    int64
	}{
		{name: "exact 20% on 25000", net: 25000, rateBPS: 2000, want: 5000},
		{name: "zero rated", net: 10000, rateBPS: 0, want: 0},
		{name: "reduced 5%", net: 10000, rateBPS: 500, want: 500},
		{name: "half-up exact half penny (3 * 2000 = 6000 -> +5000 = 11000 / 10000 = 1)", net: 3, rateBPS: 2000, want: 1},
		{name: "half-up exact 0.5 penny (25 * 200 = 5000 -> +5000 = 10000 / 10000 = 1)", net: 25, rateBPS: 200, want: 1},
		{name: "half-up below 0.5 penny (24 * 200 = 4800 -> +5000 = 9800 / 10000 = 0)", net: 24, rateBPS: 200, want: 0},
		{name: "17.5% on 333p = 58.275 -> 58", net: 333, rateBPS: 1750, want: 58},
		{name: "17.5% on 335p = 58.625 -> 59", net: 335, rateBPS: 1750, want: 59},
		{name: "negative credit note half-up", net: -25, rateBPS: 200, want: -1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := domain.CalculateVATPence(tc.net, tc.rateBPS); got != tc.want {
				t.Fatalf("CalculateVATPence(%d, %d) = %d, want %d", tc.net, tc.rateBPS, got, tc.want)
			}
		})
	}
}

func TestFormatHelpersAndGRNSummary(t *testing.T) {
	if got := domain.FormatPence(25050); got != "250.50" {
		t.Fatalf("FormatPence(25050) = %q, want 250.50", got)
	}
	if got := domain.FormatPence(-5); got != "-0.05" {
		t.Fatalf("FormatPence(-5) = %q, want -0.05", got)
	}
	if got := domain.FormatBPS(2000); got != "20.00%" {
		t.Fatalf("FormatBPS(2000) = %q, want 20.00%%", got)
	}

	poID := uuid.New()
	summary := domain.SummarizeGoodsReceipts(poID, []domain.GoodsReceipt{
		{
			ID:              uuid.New(),
			GRNNumber:       "GRN-1",
			PurchaseOrderID: poID,
			Lines: []domain.GoodsReceiptLine{
				{SKU: "SKU-A", AcceptedQty: 4, RejectedQty: 1},
				{SKU: "SKU-B", AcceptedQty: 2},
			},
		},
		{
			ID:              uuid.New(),
			GRNNumber:       "GRN-2",
			PurchaseOrderID: poID,
			Lines: []domain.GoodsReceiptLine{
				{SKU: "SKU-A", AcceptedQty: 6},
			},
		},
	})
	if summary.ReceiptCount != 2 {
		t.Fatalf("ReceiptCount = %d, want 2", summary.ReceiptCount)
	}
	if summary.AcceptedBySKU["SKU-A"] != 10 {
		t.Fatalf("AcceptedBySKU[SKU-A] = %d, want 10", summary.AcceptedBySKU["SKU-A"])
	}
	if summary.AcceptedBySKU["SKU-B"] != 2 {
		t.Fatalf("AcceptedBySKU[SKU-B] = %d, want 2", summary.AcceptedBySKU["SKU-B"])
	}
}
