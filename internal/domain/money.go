package domain

import (
	"fmt"
	"math"
	"strings"
)

// AbsInt64 returns the absolute value of v using integer arithmetic only.
func AbsInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// CalculateLineNetPence returns quantity * unitPricePence in minor units.
func CalculateLineNetPence(qty, unitPricePence int64) int64 {
	return qty * unitPricePence
}

// CalculateVATPence computes VAT in pence using half-up integer rounding:
// (line_net_pence * vat_rate_bps + 5000) / 10000.
// Negative net amounts are rounded symmetrically away from zero on half-pennies.
func CalculateVATPence(lineNetPence, vatRateBPS int64) int64 {
	if lineNetPence < 0 {
		return -((-lineNetPence)*vatRateBPS + 5000) / 10000
	}
	return (lineNetPence*vatRateBPS + 5000) / 10000
}

// ParseDecimalToPence converts a decimal currency string (e.g. "250.00", "£1,234.50", "-0.05")
// directly into int64 pence without any floating-point parsing or arithmetic.
// Strings with more than 2 fractional digits are rounded half-up to the nearest penny.
func ParseDecimalToPence(raw string) (int64, error) {
	return parseScaledInt64(raw, 2)
}

// ParsePercentToBPS converts a percentage string (e.g. "20", "20.00", "20%", "5.50%")
// directly into int64 basis points (where 20.00% = 2000) without floating-point math.
func ParsePercentToBPS(raw string) (int64, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimSuffix(cleaned, "%")
	return parseScaledInt64(cleaned, 2)
}

func parseScaledInt64(raw string, scale int) (int64, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, fmt.Errorf("empty decimal value")
	}

	negative := false
	switch s[0] {
	case '-':
		negative = true
		s = strings.TrimSpace(s[1:])
	case '+':
		s = strings.TrimSpace(s[1:])
	}

	// Strip optional leading currency symbol or ISO code (may appear before or after sign).
	s = stripCurrencyPrefix(s)
	s = strings.TrimSpace(s)

	if len(s) > 0 && (s[0] == '-' || s[0] == '+') {
		if s[0] == '-' {
			if negative {
				return 0, fmt.Errorf("invalid decimal %q: duplicate sign", raw)
			}
			negative = true
		}
		s = strings.TrimSpace(s[1:])
	}
	s = stripCurrencyPrefix(s)
	s = strings.TrimSpace(s)

	// Remove thousands separators.
	s = strings.ReplaceAll(s, ",", "")
	if s == "" {
		return 0, fmt.Errorf("invalid decimal %q: no digits", raw)
	}

	parts := strings.Split(s, ".")
	if len(parts) > 2 {
		return 0, fmt.Errorf("invalid decimal %q: multiple decimal points", raw)
	}

	intPart := parts[0]
	fracPart := ""
	if len(parts) == 2 {
		fracPart = parts[1]
	}

	if intPart == "" && fracPart == "" {
		return 0, fmt.Errorf("invalid decimal %q: no digits", raw)
	}

	var whole uint64
	for i := 0; i < len(intPart); i++ {
		c := intPart[i]
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid decimal %q: unexpected character %q", raw, c)
		}
		digit := uint64(c - '0')
		if whole > (math.MaxInt64-digit)/10 {
			return 0, fmt.Errorf("invalid decimal %q: integer overflow", raw)
		}
		whole = whole*10 + digit
	}

	for i := 0; i < len(fracPart); i++ {
		c := fracPart[i]
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid decimal %q: unexpected character %q", raw, c)
		}
	}

	var mult uint64 = 1
	for i := 0; i < scale; i++ {
		mult *= 10
	}

	var frac uint64
	roundUp := false
	for i := 0; i < scale; i++ {
		var d uint64
		if i < len(fracPart) {
			d = uint64(fracPart[i] - '0')
		}
		frac = frac*10 + d
	}
	if len(fracPart) > scale && fracPart[scale] >= '5' {
		roundUp = true
	}

	if whole > uint64(math.MaxInt64)/mult {
		return 0, fmt.Errorf("invalid decimal %q: overflow", raw)
	}
	total := whole*mult + frac
	if roundUp {
		total++
	}

	if negative {
		if total > uint64(math.MaxInt64)+1 {
			return 0, fmt.Errorf("invalid decimal %q: underflow", raw)
		}
		if total == uint64(math.MaxInt64)+1 {
			return math.MinInt64, nil
		}
		return -int64(total), nil
	}

	if total > uint64(math.MaxInt64) {
		return 0, fmt.Errorf("invalid decimal %q: overflow", raw)
	}
	return int64(total), nil
}

// FormatPence renders int64 minor units as a fixed 2-decimal string (e.g. 25050 -> "250.50").
func FormatPence(pence int64) string {
	sign := ""
	u := uint64(pence)
	if pence < 0 {
		sign = "-"
		u = uint64(-(pence + 1)) + 1
	}
	major := u / 100
	minor := u % 100
	return fmt.Sprintf("%s%d.%02d", sign, major, minor)
}

// FormatBPS renders basis points as a percentage string (e.g. 2000 -> "20.00%").
func FormatBPS(bps int64) string {
	return FormatPence(bps) + "%"
}

func stripCurrencyPrefix(s string) string {
	for _, prefix := range []string{"£", "$", "€", "GBP ", "EUR ", "USD "} {
		if strings.HasPrefix(s, prefix) {
			return strings.TrimSpace(s[len(prefix):])
		}
	}
	return s
}
