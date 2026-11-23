package recon

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

// Source says where a record came from.
type Source string

const (
	// Source2B is the GSTR-2B auto-drafted statement downloaded from the GST portal.
	Source2B Source = "2b"
	// SourceBooks is the business's own purchase register.
	SourceBooks Source = "books"
)

// Invoice is one purchase invoice, from either side of the comparison.
// All money fields are whole paise.
type Invoice struct {
	SupplierGSTIN string    `json:"supplierGstin"`
	InvoiceNo     string    `json:"invoiceNo"`
	InvoiceDate   time.Time `json:"invoiceDate"`
	TaxableValue  int64     `json:"taxableValue"`
	IGST          int64     `json:"igst"`
	CGST          int64     `json:"cgst"`
	SGST          int64     `json:"sgst"`
	Cess          int64     `json:"cess"`
	Source        Source    `json:"source"`

	// Context carried for display and for error messages. None of these take
	// part in the match key or in the value comparison.
	SupplierName string `json:"supplierName,omitempty"`
	InvoiceValue int64  `json:"invoiceValue,omitempty"`
	Line         int    `json:"line,omitempty"`
}

// TotalTax is the input tax credit riding on this invoice.
func (i Invoice) TotalTax() int64 { return i.IGST + i.CGST + i.SGST + i.Cess }

// DateText renders the invoice date the way Indian invoices write it.
func (i Invoice) DateText() string {
	if i.InvoiceDate.IsZero() {
		return ""
	}
	return i.InvoiceDate.Format("02-01-2006")
}

// MatchKey identifies an invoice across the two sources. Two records describe
// the same invoice when the supplier, the normalised invoice number and the
// financial year all agree. The financial year is part of the key because
// invoice numbering restarts every 1 April, so "INV-1" of 2024-25 is a
// different document from "INV-1" of 2023-24.
type MatchKey struct {
	GSTIN         string `json:"gstin"`
	InvoiceNo     string `json:"invoiceNo"`
	FinancialYear string `json:"financialYear"`
}

func (k MatchKey) String() string {
	return k.GSTIN + "|" + k.InvoiceNo + "|" + k.FinancialYear
}

// Key is the invoice's match key.
func (i Invoice) Key() MatchKey {
	return MatchKey{
		GSTIN:         NormaliseGSTIN(i.SupplierGSTIN),
		InvoiceNo:     NormaliseInvoiceNo(i.InvoiceNo),
		FinancialYear: FinancialYear(i.InvoiceDate),
	}
}

// NormaliseGSTIN trims and upper-cases a GSTIN and removes inner spaces, so a
// register that writes "29 AAGCB 7383 J1ZS" still matches the portal.
func NormaliseGSTIN(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if unicode.IsSpace(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// NormaliseInvoiceNo reduces an invoice number to the form used for matching:
// upper-cased, with spaces and the separators / - \ . removed, and with the
// leading zeros of every run of digits dropped.
//
// Suppliers and buyers almost never type an invoice number identically, so
// "inv-001/23", "INV1/23" and "Inv 001 / 23" all reduce to "INV123".
//
// Leading zeros are dropped run by run, before the separators are removed, so
// that two genuinely different numbers do not collapse into one: "INV-10/023"
// gives "INV1023" while "INV-100/23" gives "INV10023".
func NormaliseInvoiceNo(s string) string {
	var out, digits strings.Builder
	flush := func() {
		if digits.Len() == 0 {
			return
		}
		d := strings.TrimLeft(digits.String(), "0")
		if d == "" {
			d = "0" // a run of zeros is the number zero, not nothing
		}
		out.WriteString(d)
		digits.Reset()
	}
	for _, r := range strings.ToUpper(s) {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
		case unicode.IsSpace(r), r == '/', r == '-', r == '\\', r == '.':
			flush()
		default:
			flush()
			out.WriteRune(r)
		}
	}
	flush()
	return out.String()
}

// FinancialYearStart is the month the Indian financial year begins in. It is
// fixed by law, and the one place this package decides which year an invoice
// date falls in.
const FinancialYearStart = time.April

// FinancialYear returns the Indian financial year containing d, written the
// way returns and ledgers write it: "2023-24".
//
// The Indian financial year runs from 1 April to 31 March, so 31 March 2024
// belongs to 2023-24 and 1 April 2024 begins 2024-25.
func FinancialYear(d time.Time) string {
	if d.IsZero() {
		return ""
	}
	y := d.Year()
	if d.Month() < FinancialYearStart {
		y--
	}
	return fmt.Sprintf("%d-%02d", y, (y+1)%100)
}

// dateLayouts are the ways invoice dates turn up in portal downloads and in
// registers exported from spreadsheets and accounting packages. The GSTR-2B
// JSON uses dd-mm-yyyy; the rest are tolerated so a register does not have to
// be reformatted before it can be checked.
var dateLayouts = []string{
	"02-01-2006",
	"02/01/2006",
	"02.01.2006",
	"2-1-2006",
	"2/1/2006",
	"2006-01-02",
	"2006/01/02",
	"02-Jan-2006",
	"02 Jan 2006",
	"2-Jan-2006",
	"02-01-06",
	"02/01/06",
	"02-01-2006 15:04:05",
	"02/01/2006 15:04:05",
	"2006-01-02 15:04:05",
	time.RFC3339,
}

// ParseInvoiceDate reads an invoice date. Impossible dates are rejected rather
// than rolled over, so 29-02-2023 is an error while 29-02-2024 is valid.
func ParseInvoiceDate(s string) (time.Time, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return time.Time{}, fmt.Errorf("invoice date is empty")
	}
	for _, layout := range dateLayouts {
		if d, err := time.Parse(layout, t); err == nil {
			return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC), nil
		}
	}
	return time.Time{}, fmt.Errorf("invoice date %q is not a date — write it as dd-mm-yyyy, for example 15-07-2024", s)
}

// gstinLength is the fixed length of a GSTIN: 2-digit state code, 10-character
// PAN, 1 entity digit, the letter Z, and 1 check character.
const gstinLength = 15

// ValidGSTINFormat reports whether s has the shape of a GSTIN, as laid down for
// the Goods and Services Tax Identification Number: 15 characters, being two
// digits (state code), five letters and four digits and one letter (the PAN),
// one entity character, the literal "Z", and one check character.
//
// Only the shape is checked, not the check character — a wrongly typed GSTIN in
// the purchase register is the commonest reason an invoice looks missing from
// GSTR-2B, and the shape catches almost all of those.
func ValidGSTINFormat(s string) bool {
	g := NormaliseGSTIN(s)
	if len(g) != gstinLength {
		return false
	}
	isDigit := func(b byte) bool { return b >= '0' && b <= '9' }
	isAlpha := func(b byte) bool { return b >= 'A' && b <= 'Z' }
	isAlnum := func(b byte) bool { return isDigit(b) || isAlpha(b) }
	for i := 0; i < gstinLength; i++ {
		var ok bool
		switch {
		case i < 2:
			ok = isDigit(g[i]) // state code
		case i < 7:
			ok = isAlpha(g[i]) // PAN: first five letters
		case i < 11:
			ok = isDigit(g[i]) // PAN: four digits
		case i == 11:
			ok = isAlpha(g[i]) // PAN: last letter
		case i == 12:
			ok = isAlnum(g[i]) // entity number
		case i == 13:
			ok = g[i] == 'Z'
		default:
			ok = isAlnum(g[i]) // check character
		}
		if !ok {
			return false
		}
	}
	return true
}
