package recon

import (
	"testing"
	"time"
)

func TestNormaliseInvoiceNo(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain", "INV123", "INV123"},
		{"lower case is raised", "inv123", "INV123"},
		{"slashes go", "INV/123", "INV123"},
		{"hyphens go", "INV-123", "INV123"},
		{"backslashes go", "INV\\123", "INV123"},
		{"dots go", "INV.123", "INV123"},
		{"spaces go", "INV 123", "INV123"},
		{"leading zeros of a run go", "INV001", "INV1"},
		{"a run of only zeros is zero", "INV000", "INV0"},
		{"zero stays zero", "0", "0"},
		{"inner zeros stay", "INV1002", "INV1002"},
		{"empty stays empty", "", ""},
		{"separators only", " - / . ", ""},
		{"tabs go", "INV\t123", "INV123"},
		{"a trailing separator goes", "INV-123-", "INV123"},
		{"suffix letters survive", "INV-001-A", "INV1A"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NormaliseInvoiceNo(c.in); got != c.want {
				t.Errorf("NormaliseInvoiceNo(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// The whole point of normalising: the supplier's spelling of an invoice number
// and the buyer's spelling of the same number must land on one key.
func TestNormaliseInvoiceNoTreatsTheSameNumberTheSame(t *testing.T) {
	groups := [][]string{
		{"inv-001/23", "INV1/23", "Inv 001 / 23", "INV.001.23", "inv\\001\\23", " INV-0001/0023 "},
		{"NH-0045", "NH45", "nh 45", "NH/045"},
		{"2324/0001", "2324/1", "2324-1"},
	}
	for _, group := range groups {
		want := NormaliseInvoiceNo(group[0])
		if want == "" {
			t.Fatalf("group %q normalised to nothing", group[0])
		}
		for _, in := range group[1:] {
			if got := NormaliseInvoiceNo(in); got != want {
				t.Errorf("NormaliseInvoiceNo(%q) = %q, want %q (same as %q)", in, got, want, group[0])
			}
		}
	}
}

// Normalisation must not be so eager that two different invoices merge: that
// would hide a missing invoice instead of reporting it.
func TestNormaliseInvoiceNoKeepsDifferentNumbersApart(t *testing.T) {
	pairs := [][2]string{
		{"INV-10/023", "INV-100/23"},
		{"INV1", "INV2"},
		{"INV001", "INV0001A"},
		{"A/1", "A1/1"},
		{"2324/001", "232/4001"},
		{"INV1", "1INV"},
		{"INV0", "INV"},
		{"INV-1A", "INV-1B"},
	}
	for _, p := range pairs {
		a, b := NormaliseInvoiceNo(p[0]), NormaliseInvoiceNo(p[1])
		if a == b {
			t.Errorf("NormaliseInvoiceNo collapsed two different numbers: %q and %q both became %q", p[0], p[1], a)
		}
	}
}

func TestFinancialYear(t *testing.T) {
	d := func(y int, m time.Month, day int) time.Time {
		return time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
	}
	cases := []struct {
		name string
		in   time.Time
		want string
	}{
		{"first day of the year", d(2025, time.April, 1), "2025-26"},
		{"last day of the year", d(2026, time.March, 31), "2025-26"},
		{"day before the year starts", d(2025, time.March, 31), "2024-25"},
		{"day after the year starts", d(2025, time.April, 2), "2025-26"},
		{"january belongs to the earlier year", d(2026, time.January, 1), "2025-26"},
		{"february belongs to the earlier year", d(2026, time.February, 28), "2025-26"},
		{"leap day belongs to the earlier year", d(2024, time.February, 29), "2023-24"},
		{"december", d(2025, time.December, 31), "2025-26"},
		{"a century rolls over", d(2099, time.December, 1), "2099-00"},
		{"zero time has no year", time.Time{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FinancialYear(c.in); got != c.want {
				t.Errorf("FinancialYear(%s) = %q, want %q", c.in.Format("02-01-2006"), got, c.want)
			}
		})
	}
}

// 31 March and 1 April are one day apart and in different financial years;
// that boundary is what makes the year part of the match key.
func TestFinancialYearBoundaryIsExactlyOneDay(t *testing.T) {
	last := time.Date(2026, time.March, 31, 0, 0, 0, 0, time.UTC)
	first := last.AddDate(0, 0, 1)
	if first.Format("02-01-2006") != "01-04-2026" {
		t.Fatalf("the day after 31-03-2026 is %s", first.Format("02-01-2006"))
	}
	if FinancialYear(last) == FinancialYear(first) {
		t.Fatalf("31-03-2026 and 01-04-2026 are both in %q, but they are different financial years", FinancialYear(last))
	}
	if FinancialYear(last) != "2025-26" || FinancialYear(first) != "2026-27" {
		t.Errorf("got %q then %q, want 2025-26 then 2026-27", FinancialYear(last), FinancialYear(first))
	}
}

func TestParseInvoiceDate(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		want  string // dd-mm-yyyy, or "" when an error is expected
		isErr bool
	}{
		{"portal format", "15-07-2025", "15-07-2025", false},
		{"slashes", "15/07/2025", "15-07-2025", false},
		{"dots", "15.07.2025", "15-07-2025", false},
		{"single digits", "5-7-2025", "05-07-2025", false},
		{"iso", "2025-07-15", "15-07-2025", false},
		{"month name", "15-Jul-2025", "15-07-2025", false},
		{"month name with spaces", "15 Jul 2025", "15-07-2025", false},
		{"two digit year", "15-07-25", "15-07-2025", false},
		{"spreadsheet timestamp", "15-07-2025 00:00:00", "15-07-2025", false},
		{"iso timestamp", "2025-07-15 13:45:00", "15-07-2025", false},
		{"surrounding spaces", "  15-07-2025 ", "15-07-2025", false},
		{"leap day in a leap year", "29-02-2024", "29-02-2024", false},
		{"last day of a long month", "31-01-2025", "31-01-2025", false},
		{"leap day in a common year", "29-02-2025", "", true},
		{"thirty first of april", "31-04-2025", "", true},
		{"month thirteen", "15-13-2025", "", true},
		{"empty", "", "", true},
		{"not a date", "last Tuesday", "", true},
		{"spreadsheet serial number", "45853", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseInvoiceDate(c.in)
			if c.isErr {
				if err == nil {
					t.Fatalf("ParseInvoiceDate(%q) = %s, want an error", c.in, got.Format("02-01-2006"))
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseInvoiceDate(%q): %v", c.in, err)
			}
			if got.Format("02-01-2006") != c.want {
				t.Errorf("ParseInvoiceDate(%q) = %s, want %s", c.in, got.Format("02-01-2006"), c.want)
			}
			if h, m, s := got.Clock(); h != 0 || m != 0 || s != 0 {
				t.Errorf("ParseInvoiceDate(%q) kept a time of day: %s", c.in, got)
			}
		})
	}
}

func TestNormaliseGSTIN(t *testing.T) {
	cases := []struct{ in, want string }{
		{"27aapfu0939f1zv", "27AAPFU0939F1ZV"},
		{" 27AAPFU0939F1ZV ", "27AAPFU0939F1ZV"},
		{"27 AAPFU 0939 F1ZV", "27AAPFU0939F1ZV"},
		{"", ""},
	}
	for _, c := range cases {
		if got := NormaliseGSTIN(c.in); got != c.want {
			t.Errorf("NormaliseGSTIN(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestValidGSTINFormat(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"a normal GSTIN", "27AAPFU0939F1ZV", true},
		{"lower case is still valid", "27aapfu0939f1zv", true},
		{"spaced out is still valid", "27 AAPFU 0939 F1ZV", true},
		{"digit in the entity position", "29AABCU9603R1ZM", true},
		{"too short", "27AAPFU0939F1Z", false},
		{"too long", "27AAPFU0939F1ZVX", false},
		{"state code not numeric", "2AAAPFU0939F1ZV", false},
		{"pan letters are digits", "27123450939F1ZV", false},
		{"pan digits are letters", "27AAPFUABCDF1ZV", false},
		{"fourteenth character is not Z", "27AAPFU0939F1AV", false},
		{"empty", "", false},
		{"a PAN is not a GSTIN", "AAPFU0939F", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ValidGSTINFormat(c.in); got != c.want {
				t.Errorf("ValidGSTINFormat(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestInvoiceKeyAndTotals(t *testing.T) {
	a := Invoice{
		SupplierGSTIN: "27aapfu0939f1zv",
		InvoiceNo:     "inv-001/25-26",
		InvoiceDate:   time.Date(2025, time.July, 15, 0, 0, 0, 0, time.UTC),
		IGST:          1800000,
		Source:        SourceBooks,
	}
	b := Invoice{
		SupplierGSTIN: "27AAPFU0939F1ZV",
		InvoiceNo:     "INV/1/25-26",
		InvoiceDate:   time.Date(2025, time.July, 15, 0, 0, 0, 0, time.UTC),
		CGST:          900000,
		SGST:          900000,
		Source:        Source2B,
	}
	if a.Key() != b.Key() {
		t.Errorf("the same invoice from both sides gave different keys:\n books %+v\n 2B    %+v", a.Key(), b.Key())
	}
	if got := a.Key().FinancialYear; got != "2025-26" {
		t.Errorf("key financial year = %q, want 2025-26", got)
	}
	if got := a.TotalTax(); got != 1800000 {
		t.Errorf("TotalTax = %d, want 1800000", got)
	}
	if got := b.TotalTax(); got != 1800000 {
		t.Errorf("TotalTax over CGST and SGST = %d, want 1800000", got)
	}
	if got := a.DateText(); got != "15-07-2025" {
		t.Errorf("DateText = %q, want 15-07-2025", got)
	}
	if got := (Invoice{}).DateText(); got != "" {
		t.Errorf("DateText of an invoice with no date = %q, want empty", got)
	}

	// The same number in a different financial year is a different invoice.
	c := a
	c.InvoiceDate = time.Date(2025, time.March, 31, 0, 0, 0, 0, time.UTC)
	if a.Key() == c.Key() {
		t.Error("the same invoice number in two financial years produced one key")
	}
}
