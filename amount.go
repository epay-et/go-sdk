package epay

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// MaxAmount is the largest amount the API accepts.
const MaxAmount = "999999999.99"

// MaxListRangeDays is the longest span the transaction list endpoint allows
// between From and To.
const MaxListRangeDays = 90

var (
	amountPattern = regexp.MustCompile(`^\d{1,9}(\.\d{1,2})?$`)
	phoneStrip    = regexp.MustCompile(`[\s()\-.]`)
	phoneIntl     = regexp.MustCompile(`^251[79]\d{8}$`)
	phoneLocal    = regexp.MustCompile(`^0[79]\d{8}$`)
	phoneBare     = regexp.MustCompile(`^[79]\d{8}$`)
	currencyCode  = regexp.MustCompile(`^[A-Z]{3}$`)
)

// FormatAmount validates a decimal amount string and pads it to exactly two
// decimal places, so "250", "250.0", and "250.00" all become "250.00".
//
// Money is passed as a string throughout this SDK: Go has no decimal type in
// the standard library, and float64 cannot represent every decimal amount
// exactly. Use [AmountFromMinorUnits] when you hold cents as an integer.
//
// The returned error wraps [ErrValidation].
func FormatAmount(value string) (string, error) {
	candidate := strings.TrimSpace(value)

	if !amountPattern.MatchString(candidate) {
		return "", fmt.Errorf(
			"%w: amount must be a positive numeric string with at most 9 integer digits "+
				"and 2 decimal places, got %q", ErrValidation, value)
	}

	whole, fraction, _ := strings.Cut(candidate, ".")

	// Reject zero without float math: any amount of the form 0(.00) is invalid.
	if strings.Trim(whole, "0") == "" && strings.Trim(fraction, "0") == "" {
		return "", fmt.Errorf("%w: amount must be greater than 0, got %q", ErrValidation, value)
	}

	// The pattern already caps the integer part at 9 digits, which is exactly
	// the MaxAmount ceiling, so no separate range check is needed.
	padded := whole + "." + fraction + strings.Repeat("0", 2-len(fraction))

	return padded, nil
}

// AmountFromMinorUnits renders an integer number of cents as the amount string
// the API expects, exactly and without floating point.
//
//	epay.AmountFromMinorUnits(25000) // "250.00"
//
// The returned error wraps [ErrValidation] when the value is not positive or
// exceeds [MaxAmount].
func AmountFromMinorUnits(cents int64) (string, error) {
	if cents <= 0 {
		return "", fmt.Errorf("%w: amount must be greater than 0, got %d cents", ErrValidation, cents)
	}
	if cents > 99999999999 { // 999999999.99 in cents
		return "", fmt.Errorf("%w: amount must not exceed %s, got %d cents", ErrValidation, MaxAmount, cents)
	}

	return fmt.Sprintf("%d.%02d", cents/100, cents%100), nil
}

// MinorUnits parses an amount string back into an integer number of cents.
//
// Useful for arithmetic and for storing amounts in an integer column.
func MinorUnits(amount string) (int64, error) {
	formatted, err := FormatAmount(amount)
	if err != nil {
		return 0, err
	}

	whole, fraction, _ := strings.Cut(formatted, ".")

	units, err := strconv.ParseInt(whole+fraction, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: amount %q is not representable as cents", ErrValidation, amount)
	}

	return units, nil
}

// NormalizePhone rewrites an Ethiopian mobile number to +251XXXXXXXXX.
//
// It accepts local (0911234567), bare national (911234567), and international
// (251911234567, +251911234567) forms, with spaces, dashes, and parentheses
// anywhere. Both Ethio Telecom (9…) and Safaricom Ethiopia (7…) ranges are
// recognised.
//
// The returned error wraps [ErrValidation].
func NormalizePhone(value string) (string, error) {
	cleaned := phoneStrip.ReplaceAllString(value, "")
	digits := strings.TrimPrefix(cleaned, "+")

	if digits == "" {
		return "", fmt.Errorf("%w: customerPhone must not be empty", ErrValidation)
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return "", fmt.Errorf(
				"%w: customerPhone must contain only digits and separators, got %q",
				ErrValidation, value)
		}
	}

	var subscriber string
	switch {
	case phoneIntl.MatchString(digits):
		subscriber = digits[3:]
	case phoneLocal.MatchString(digits):
		subscriber = digits[1:]
	case phoneBare.MatchString(digits):
		subscriber = digits
	default:
		return "", fmt.Errorf(
			"%w: customerPhone must be an Ethiopian mobile number such as +251911234567 "+
				"or 0911234567, got %q", ErrValidation, value)
	}

	return "+251" + subscriber, nil
}

// NormalizeCurrency validates a 3-letter ISO 4217 code and uppercases it.
//
// The returned error wraps [ErrValidation].
func NormalizeCurrency(value string) (string, error) {
	code := strings.ToUpper(strings.TrimSpace(value))
	if !currencyCode.MatchString(code) {
		return "", fmt.Errorf(
			"%w: currency must be a 3-letter ISO 4217 code such as ETB, got %q",
			ErrValidation, value)
	}

	return code, nil
}

// maskSecret masks a secret so it can be logged safely.
func maskSecret(value string) string {
	if len(value) <= 12 {
		return "***"
	}
	return value[:8] + "…" + value[len(value)-4:]
}
