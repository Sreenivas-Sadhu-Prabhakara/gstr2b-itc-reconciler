package recon

import "testing"

func TestParseAmount(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		want  int64
		isErr bool
	}{
		{"empty is zero", "", 0, false},
		{"spaces only", "   ", 0, false},
		{"dash means nil", "-", 0, false},
		{"double dash means nil", "--", 0, false},
		{"whole rupees", "100", 10000, false},
		{"two decimals", "1000.50", 100050, false},
		{"one decimal is padded", "1000.5", 100050, false},
		{"zero", "0", 0, false},
		{"zero with decimals", "0.00", 0, false},
		{"paise only", ".75", 75, false},
		{"indian grouping", "1,00,000.00", 10000000, false},
		{"western grouping", "100,000.00", 10000000, false},
		{"rupee sign", "₹18,000.00", 1800000, false},
		{"rs prefix", "Rs. 1,234.56", 123456, false},
		{"inr prefix", "INR 500", 50000, false},
		{"lower case rs", "rs 500", 50000, false},
		{"leading and trailing spaces", "  250.25  ", 25025, false},
		{"explicit plus", "+99.99", 9999, false},
		{"negative", "-1,000.00", -100000, false},
		{"accounting negative", "(500.00)", -50000, false},
		{"rounds half up", "1234.567", 123457, false},
		{"rounds half up on exact half", "0.005", 1, false},
		{"rounds down below half", "0.004", 0, false},
		{"rounds a run of nines", "1.999", 200, false},
		{"non breaking space", "1 000.00", 100000, false},
		{"letters are rejected", "abc", 0, true},
		{"trailing letters are rejected", "100abc", 0, true},
		{"two decimal points are rejected", "1.2.3", 0, true},
		{"empty parentheses are zero", "()", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseAmount(c.in)
			if c.isErr {
				if err == nil {
					t.Fatalf("ParseAmount(%q) = %d, want an error", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseAmount(%q) returned error %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("ParseAmount(%q) = %d paise, want %d", c.in, got, c.want)
			}
		})
	}
}

func TestParseAmountRejectsHugeNumbers(t *testing.T) {
	if _, err := ParseAmount("999999999999999999999"); err == nil {
		t.Error("an amount too large for int64 paise should be an error, not a wrapped number")
	}
}

func TestRupees(t *testing.T) {
	cases := []struct {
		name  string
		paise int64
		want  string
	}{
		{"zero", 0, "0.00"},
		{"one paisa", 1, "0.01"},
		{"one rupee", 100, "1.00"},
		{"under a thousand", 99950, "999.50"},
		{"one thousand", 100000, "1,000.00"},
		{"ten thousand", 1000000, "10,000.00"},
		{"one lakh groups in twos", 10000000, "1,00,000.00"},
		{"ten lakh", 100000000, "10,00,000.00"},
		{"one crore", 1000000000, "1,00,00,000.00"},
		{"mixed", 1234567890, "1,23,45,678.90"},
		{"negative", -100000, "-1,000.00"},
		{"negative paise", -5, "-0.05"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Rupees(c.paise); got != c.want {
				t.Errorf("Rupees(%d) = %q, want %q", c.paise, got, c.want)
			}
		})
	}
}

func TestDecimal(t *testing.T) {
	cases := []struct {
		paise int64
		want  string
	}{
		{0, "0.00"},
		{5, "0.05"},
		{1800000, "18000.00"},
		{-123456, "-1234.56"},
	}
	for _, c := range cases {
		if got := Decimal(c.paise); got != c.want {
			t.Errorf("Decimal(%d) = %q, want %q", c.paise, got, c.want)
		}
	}
}

func TestParseAmountRoundTripsThroughDecimal(t *testing.T) {
	for _, in := range []string{"0.00", "1.05", "18000.00", "-1234.56", "99999999.99"} {
		p, err := ParseAmount(in)
		if err != nil {
			t.Fatalf("ParseAmount(%q): %v", in, err)
		}
		if got := Decimal(p); got != in {
			t.Errorf("Decimal(ParseAmount(%q)) = %q, want the same text back", in, got)
		}
	}
}
