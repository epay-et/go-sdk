package epay

import (
	"errors"
	"testing"
)

func TestFormatAmount(t *testing.T) {
	valid := map[string]string{
		"250.00":       "250.00",
		"250":          "250.00",
		"250.5":        "250.50",
		"  42.10  ":    "42.10",
		"0.01":         "0.01",
		"999999999.99": "999999999.99",
	}

	for input, want := range valid {
		got, err := FormatAmount(input)
		if err != nil {
			t.Errorf("FormatAmount(%q) returned error: %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("FormatAmount(%q) = %q, want %q", input, got, want)
		}
	}

	invalid := []string{
		"0", "0.00", "-5.00", "1.234", "1000000000.00", "abc", "", "1e3", "250.",
	}

	for _, input := range invalid {
		if _, err := FormatAmount(input); !errors.Is(err, ErrValidation) {
			t.Errorf("FormatAmount(%q) should wrap ErrValidation, got %v", input, err)
		}
	}
}

func TestAmountFromMinorUnits(t *testing.T) {
	cases := map[int64]string{
		25000:       "250.00",
		1:           "0.01",
		150:         "1.50",
		99999999999: "999999999.99",
	}

	for cents, want := range cases {
		got, err := AmountFromMinorUnits(cents)
		if err != nil {
			t.Errorf("AmountFromMinorUnits(%d) returned error: %v", cents, err)
			continue
		}
		if got != want {
			t.Errorf("AmountFromMinorUnits(%d) = %q, want %q", cents, got, want)
		}
	}

	for _, cents := range []int64{0, -1, 100000000000} {
		if _, err := AmountFromMinorUnits(cents); !errors.Is(err, ErrValidation) {
			t.Errorf("AmountFromMinorUnits(%d) should wrap ErrValidation, got %v", cents, err)
		}
	}
}

func TestMinorUnitsRoundTrip(t *testing.T) {
	for _, amount := range []string{"250.00", "0.01", "1.50", "999999999.99"} {
		cents, err := MinorUnits(amount)
		if err != nil {
			t.Fatalf("MinorUnits(%q) returned error: %v", amount, err)
		}

		back, err := AmountFromMinorUnits(cents)
		if err != nil {
			t.Fatalf("AmountFromMinorUnits(%d) returned error: %v", cents, err)
		}
		if back != amount {
			t.Errorf("round trip of %q gave %q", amount, back)
		}
	}
}

func TestNormalizePhone(t *testing.T) {
	for _, input := range []string{
		"+251911234567",
		"251911234567",
		"0911234567",
		"911234567",
		"+251 91 123 4567",
		"0911-234-567",
		"(0911) 234 567",
	} {
		got, err := NormalizePhone(input)
		if err != nil {
			t.Errorf("NormalizePhone(%q) returned error: %v", input, err)
			continue
		}
		if got != "+251911234567" {
			t.Errorf("NormalizePhone(%q) = %q, want +251911234567", input, got)
		}
	}

	if got, err := NormalizePhone("0711234567"); err != nil || got != "+251711234567" {
		t.Errorf("NormalizePhone on the Safaricom range = %q, %v", got, err)
	}

	// The sandbox magic numbers from the Test Accounts reference.
	for _, magic := range []string{
		"251900000000", "251900000001", "251900000002", "251900000003", "251900000004",
	} {
		got, err := NormalizePhone(magic)
		if err != nil || got != "+"+magic {
			t.Errorf("NormalizePhone(%q) = %q, %v", magic, got, err)
		}
	}

	for _, input := range []string{
		"+251811234567", "0811234567", "09112345678", "091123456", "", "+1 555 0100", "abc",
	} {
		if _, err := NormalizePhone(input); !errors.Is(err, ErrValidation) {
			t.Errorf("NormalizePhone(%q) should wrap ErrValidation, got %v", input, err)
		}
	}
}

func TestNormalizeCurrency(t *testing.T) {
	for _, input := range []string{"etb", "ETB", " ETB "} {
		got, err := NormalizeCurrency(input)
		if err != nil || got != "ETB" {
			t.Errorf("NormalizeCurrency(%q) = %q, %v", input, got, err)
		}
	}

	for _, input := range []string{"ET", "ETBB", "123", ""} {
		if _, err := NormalizeCurrency(input); !errors.Is(err, ErrValidation) {
			t.Errorf("NormalizeCurrency(%q) should wrap ErrValidation, got %v", input, err)
		}
	}
}

func TestMaskSecret(t *testing.T) {
	if got := maskSecret("sk_live_supersecretvalue1234"); got != "sk_live_…1234" {
		t.Errorf("maskSecret = %q", got)
	}
	if got := maskSecret("short"); got != "***" {
		t.Errorf("maskSecret of a short value = %q, want ***", got)
	}
}
