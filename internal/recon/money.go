package recon

import (
	"fmt"
	"strings"
)

// All money in this package is held as whole paise in an int64, so that sums
// and comparisons are exact. 100 paise = ₹1.00.

// ParseAmount reads a rupee amount written the way accounting exports write it
// and returns whole paise.
//
// It accepts a leading currency marker (₹, Rs, Rs., INR), Indian or Western
// digit grouping ("1,00,000.50" and "100,000.50"), a sign, surrounding spaces,
// and the dashes and blanks that spreadsheets use for nil ("", "-", "--").
// Anything beyond two decimal places is rounded half away from zero, because a
// tax amount is never finer than a paisa.
func ParseAmount(s string) (int64, error) {
	t := strings.TrimSpace(s)
	t = strings.NewReplacer(" ", "", "₹", "", ",", "", " ", "").Replace(t)
	// Drop a currency word, in any case: "Rs", "Rs.", "INR".
	for _, p := range []string{"INR", "RS.", "RS"} {
		if len(t) >= len(p) && strings.EqualFold(t[:len(p)], p) {
			t = t[len(p):]
			break
		}
	}
	neg := false
	// Accounting negatives: (500) means -500.
	if len(t) >= 2 && t[0] == '(' && t[len(t)-1] == ')' {
		neg = true
		t = t[1 : len(t)-1]
	}
	if t != "" && (t[0] == '+' || t[0] == '-') {
		neg = neg != (t[0] == '-')
		t = t[1:]
	}
	// Blank and dash-only cells mean zero, not a mistake.
	if t == "" || strings.Trim(t, "-–—.") == "" {
		return 0, nil
	}

	intPart, fracPart := t, ""
	if i := strings.IndexByte(t, '.'); i >= 0 {
		intPart, fracPart = t[:i], t[i+1:]
		if strings.ContainsRune(fracPart, '.') {
			return 0, fmt.Errorf("amount %q has more than one decimal point", s)
		}
	}
	if intPart == "" {
		intPart = "0"
	}
	var rupees int64
	for i := 0; i < len(intPart); i++ {
		c := intPart[i]
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("amount %q is not a number", s)
		}
		if rupees > (1<<62)/100 {
			return 0, fmt.Errorf("amount %q is too large", s)
		}
		rupees = rupees*10 + int64(c-'0')
	}
	for i := 0; i < len(fracPart); i++ {
		if fracPart[i] < '0' || fracPart[i] > '9' {
			return 0, fmt.Errorf("amount %q is not a number", s)
		}
	}
	paise := rupees * 100
	if fracPart != "" {
		// Pad or round to exactly two decimal places.
		switch {
		case len(fracPart) == 1:
			paise += int64(fracPart[0]-'0') * 10
		default:
			p := int64(fracPart[0]-'0')*10 + int64(fracPart[1]-'0')
			if len(fracPart) > 2 && fracPart[2] >= '5' {
				p++
			}
			paise += p
		}
	}
	if neg {
		paise = -paise
	}
	return paise, nil
}

// Rupees formats paise the Indian way — groups of two above the last three
// digits — with two decimal places and no currency symbol, e.g. 1234567890
// becomes "1,23,45,678.90". Safe for CSV cells and plain-text messages.
func Rupees(paise int64) string {
	neg := paise < 0
	if neg {
		paise = -paise
	}
	whole := paise / 100
	frac := paise % 100
	digits := fmt.Sprintf("%d", whole)

	var grouped string
	if len(digits) <= 3 {
		grouped = digits
	} else {
		head, tail := digits[:len(digits)-3], digits[len(digits)-3:]
		var parts []string
		for len(head) > 2 {
			parts = append([]string{head[len(head)-2:]}, parts...)
			head = head[:len(head)-2]
		}
		if head != "" {
			parts = append([]string{head}, parts...)
		}
		grouped = strings.Join(parts, ",") + "," + tail
	}
	sign := ""
	if neg {
		sign = "-"
	}
	return fmt.Sprintf("%s%s.%02d", sign, grouped, frac)
}

// Decimal formats paise as a plain machine-readable decimal ("18000.00") for
// CSV columns that a spreadsheet or accounting package will read back.
func Decimal(paise int64) string {
	neg := paise < 0
	if neg {
		paise = -paise
	}
	sign := ""
	if neg {
		sign = "-"
	}
	return fmt.Sprintf("%s%d.%02d", sign, paise/100, paise%100)
}
