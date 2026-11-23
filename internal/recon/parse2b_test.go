package recon

import (
	"os"
	"strings"
	"testing"
)

func TestParseGSTR2BFixture(t *testing.T) {
	f, err := os.Open("testdata/gstr2b-sample.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	got, err := ParseGSTR2B(f)
	if err != nil {
		t.Fatalf("ParseGSTR2B: %v", err)
	}
	if got.GSTIN != "29AAGCB7383J1ZS" {
		t.Errorf("recipient GSTIN = %q, want 29AAGCB7383J1ZS", got.GSTIN)
	}
	if got.Period != "092025" || got.PeriodText != "September 2025" {
		t.Errorf("period = %q / %q, want 092025 / September 2025", got.Period, got.PeriodText)
	}
	if len(got.Invoices) != 4 {
		t.Fatalf("read %d invoices, want 4", len(got.Invoices))
	}

	want := []struct {
		gstin, name, no, date                            string
		value, taxable, igst, cgst, sgst, cess, totalTax int64
	}{
		{"27AAPFU0939F1ZV", "Sundar Metals", "INV/001/25-26", "15-07-2025", 11800000, 10000000, 1800000, 0, 0, 0, 1800000},
		{"27AAPFU0939F1ZV", "Sundar Metals", "INV/002/25-26", "20-07-2025", 5900000, 5000000, 900000, 0, 0, 0, 900000},
		{"29AABCU9603R1ZM", "Nandi Hardware", "NH-0045", "02-08-2025", 2360000, 2000000, 0, 180000, 180000, 0, 360000},
		{"29AABCU9603R1ZM", "Nandi Hardware", "NH-0051", "09-08-2025", 1230000, 1000000, 0, 90000, 90000, 50000, 230000},
	}
	for i, w := range want {
		g := got.Invoices[i]
		if g.SupplierGSTIN != w.gstin || g.InvoiceNo != w.no || g.DateText() != w.date {
			t.Errorf("invoice %d = %s / %s / %s, want %s / %s / %s", i, g.SupplierGSTIN, g.InvoiceNo, g.DateText(), w.gstin, w.no, w.date)
		}
		if g.SupplierName != w.name {
			t.Errorf("invoice %d supplier name = %q, want %q", i, g.SupplierName, w.name)
		}
		if g.Source != Source2B {
			t.Errorf("invoice %d source = %q, want %q", i, g.Source, Source2B)
		}
		if g.InvoiceValue != w.value {
			t.Errorf("invoice %d value = %d paise, want %d", i, g.InvoiceValue, w.value)
		}
		if g.TaxableValue != w.taxable {
			t.Errorf("invoice %d taxable = %d paise, want %d (the item lines added up)", i, g.TaxableValue, w.taxable)
		}
		if g.IGST != w.igst || g.CGST != w.cgst || g.SGST != w.sgst || g.Cess != w.cess {
			t.Errorf("invoice %d heads = %d/%d/%d/%d, want %d/%d/%d/%d", i, g.IGST, g.CGST, g.SGST, g.Cess, w.igst, w.cgst, w.sgst, w.cess)
		}
		if g.TotalTax() != w.totalTax {
			t.Errorf("invoice %d total tax = %d paise, want %d", i, g.TotalTax(), w.totalTax)
		}
	}

	// The credit note section is not reconciled, so it must be called out.
	joined := strings.Join(got.Notes, " ")
	if !strings.Contains(joined, "cdnr") {
		t.Errorf("notes should mention the credit note section, got %v", got.Notes)
	}
	if strings.Contains(joined, "b2ba") {
		t.Errorf("an empty amendment section should not be mentioned, got %v", got.Notes)
	}
}

func TestParseGSTR2BShapes(t *testing.T) {
	cases := []struct {
		name    string
		json    string
		wantNo  string
		wantIGS int64
		wantTax int64
	}{
		{
			name:    "without the data wrapper",
			json:    `{"docdata":{"b2b":[{"ctin":"27AAPFU0939F1ZV","inv":[{"inum":"A-1","idt":"01-04-2025","val":1180,"itms":[{"num":1,"itm_det":{"txval":1000,"iamt":180}}]}]}]}}`,
			wantNo:  "A-1",
			wantIGS: 18000,
			wantTax: 100000,
		},
		{
			name:    "keys in other casing",
			json:    `{"Data":{"DocData":{"B2B":[{"CTIN":"27AAPFU0939F1ZV","Inv":[{"INum":"A-1","IDt":"01-04-2025","Val":1180,"Itms":[{"Itm_Det":{"TxVal":1000,"IAmt":180}}]}]}]}}}`,
			wantNo:  "A-1",
			wantIGS: 18000,
			wantTax: 100000,
		},
		{
			name:    "amounts written as strings",
			json:    `{"docdata":{"b2b":[{"ctin":"27AAPFU0939F1ZV","inv":[{"inum":"A-1","idt":"01-04-2025","val":"1180.00","itms":[{"itm_det":{"txval":"1,000.00","iamt":"180.00"}}]}]}]}}`,
			wantNo:  "A-1",
			wantIGS: 18000,
			wantTax: 100000,
		},
		{
			name:    "an invoice number written as a number",
			json:    `{"docdata":{"b2b":[{"ctin":"27AAPFU0939F1ZV","inv":[{"inum":1001,"idt":"01-04-2025","itms":[{"itm_det":{"txval":1000,"iamt":180}}]}]}]}}`,
			wantNo:  "1001",
			wantIGS: 18000,
			wantTax: 100000,
		},
		{
			name:    "heads flattened onto the invoice",
			json:    `{"docdata":{"b2b":[{"ctin":"27AAPFU0939F1ZV","inv":[{"inum":"A-1","idt":"01-04-2025","txval":1000,"iamt":180}]}]}}`,
			wantNo:  "A-1",
			wantIGS: 18000,
			wantTax: 100000,
		},
		{
			name:    "heads flattened onto the item row",
			json:    `{"docdata":{"b2b":[{"ctin":"27AAPFU0939F1ZV","inv":[{"inum":"A-1","idt":"01-04-2025","itms":[{"txval":1000,"iamt":180}]}]}]}}`,
			wantNo:  "A-1",
			wantIGS: 18000,
			wantTax: 100000,
		},
		{
			name:    "missing heads count as zero",
			json:    `{"docdata":{"b2b":[{"ctin":"27AAPFU0939F1ZV","inv":[{"inum":"A-1","idt":"01-04-2025","itms":[{"itm_det":{"txval":1000}}]}]}]}}`,
			wantNo:  "A-1",
			wantIGS: 0,
			wantTax: 100000,
		},
		{
			name:    "null heads count as zero",
			json:    `{"docdata":{"b2b":[{"ctin":"27AAPFU0939F1ZV","inv":[{"inum":"A-1","idt":"01-04-2025","itms":[{"itm_det":{"txval":1000,"iamt":null,"camt":null}}]}]}]}}`,
			wantNo:  "A-1",
			wantIGS: 0,
			wantTax: 100000,
		},
		{
			name:    "a bare b2b document",
			json:    `{"b2b":[{"ctin":"27AAPFU0939F1ZV","inv":[{"inum":"A-1","idt":"01-04-2025","itms":[{"itm_det":{"txval":1000,"iamt":180}}]}]}]}`,
			wantNo:  "A-1",
			wantIGS: 18000,
			wantTax: 100000,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseGSTR2B(strings.NewReader(c.json))
			if err != nil {
				t.Fatalf("ParseGSTR2B: %v", err)
			}
			if len(got.Invoices) != 1 {
				t.Fatalf("read %d invoices, want 1", len(got.Invoices))
			}
			g := got.Invoices[0]
			if g.InvoiceNo != c.wantNo {
				t.Errorf("invoice number = %q, want %q", g.InvoiceNo, c.wantNo)
			}
			if g.IGST != c.wantIGS {
				t.Errorf("IGST = %d paise, want %d", g.IGST, c.wantIGS)
			}
			if g.TaxableValue != c.wantTax {
				t.Errorf("taxable value = %d paise, want %d", g.TaxableValue, c.wantTax)
			}
			if g.Key().FinancialYear != "2025-26" {
				t.Errorf("financial year = %q, want 2025-26", g.Key().FinancialYear)
			}
		})
	}
}

func TestParseGSTR2BAddsUpEveryItemLine(t *testing.T) {
	const j = `{"docdata":{"b2b":[{"ctin":"27AAPFU0939F1ZV","inv":[{"inum":"A-1","idt":"01-04-2025","itms":[
		{"num":1,"itm_det":{"rt":18,"txval":1000,"iamt":180}},
		{"num":2,"itm_det":{"rt":5,"txval":2000,"iamt":100}},
		{"num":3,"itm_det":{"rt":12,"txval":500,"camt":30,"samt":30,"csamt":10}}]}]}]}}`
	got, err := ParseGSTR2B(strings.NewReader(j))
	if err != nil {
		t.Fatal(err)
	}
	g := got.Invoices[0]
	if g.TaxableValue != 350000 {
		t.Errorf("taxable = %d paise, want 350000", g.TaxableValue)
	}
	if g.IGST != 28000 || g.CGST != 3000 || g.SGST != 3000 || g.Cess != 1000 {
		t.Errorf("heads = %d/%d/%d/%d, want 28000/3000/3000/1000", g.IGST, g.CGST, g.SGST, g.Cess)
	}
	if g.TotalTax() != 35000 {
		t.Errorf("total tax = %d paise, want 35000", g.TotalTax())
	}
}

func TestParseGSTR2BRejectsBadInputWithAClearError(t *testing.T) {
	cases := []struct {
		name, json, wantIn string
	}{
		{"empty file", "", "empty"},
		{"not JSON at all", "supplier,invoice\n1,2\n", "GSTR-2B JSON download"},
		{"an HTML page", "<!doctype html><html><body>Error</body></html>", "GSTR-2B JSON download"},
		{"truncated JSON", `{"docdata":{"b2b":[{"ctin":"27AAPFU0939F1ZV"`, "not valid JSON"},
		{"a JSON list", `[{"ctin":"27AAPFU0939F1ZV"}]`, "GSTR-2B JSON download"},
		{"no docdata", `{"data":{"gstin":"29AAGCB7383J1ZS"}}`, "no docdata section"},
		{"b2b is not a list", `{"docdata":{"b2b":{"ctin":"27AAPFU0939F1ZV"}}}`, "not a list of suppliers"},
		{"supplier without a GSTIN", `{"docdata":{"b2b":[{"trdnm":"No GSTIN","inv":[]}]}}`, "has no GSTIN"},
		{"invoice without a number", `{"docdata":{"b2b":[{"ctin":"27AAPFU0939F1ZV","inv":[{"idt":"01-04-2025"}]}]}}`, "has no invoice number"},
		{"invoice without a date", `{"docdata":{"b2b":[{"ctin":"27AAPFU0939F1ZV","inv":[{"inum":"A-1"}]}]}}`, "has no invoice date"},
		{"an impossible date", `{"docdata":{"b2b":[{"ctin":"27AAPFU0939F1ZV","inv":[{"inum":"A-1","idt":"29-02-2025"}]}]}}`, "not a date"},
		{"a tax amount that is not a number", `{"docdata":{"b2b":[{"ctin":"27AAPFU0939F1ZV","inv":[{"inum":"A-1","idt":"01-04-2025","itms":[{"itm_det":{"iamt":"eighteen"}}]}]}]}}`, "not a number"},
		{"an object where an amount belongs", `{"docdata":{"b2b":[{"ctin":"27AAPFU0939F1ZV","inv":[{"inum":"A-1","idt":"01-04-2025","itms":[{"itm_det":{"iamt":{"amount":180}}}]}]}]}}`, "expected an amount"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseGSTR2B(strings.NewReader(c.json))
			if err == nil {
				t.Fatalf("ParseGSTR2B accepted %s and returned %d invoices", c.name, len(got.Invoices))
			}
			if !strings.Contains(err.Error(), c.wantIn) {
				t.Errorf("error = %q, want it to mention %q", err.Error(), c.wantIn)
			}
		})
	}
}

func TestParseGSTR2BErrorNamesTheInvoice(t *testing.T) {
	const j = `{"docdata":{"b2b":[{"ctin":"27AAPFU0939F1ZV","inv":[
		{"inum":"GOOD-1","idt":"01-04-2025","itms":[{"itm_det":{"txval":1000,"iamt":180}}]},
		{"inum":"BAD-2","idt":"01-04-2025","itms":[{"itm_det":{"iamt":"oops"}}]}]}]}}`
	_, err := ParseGSTR2B(strings.NewReader(j))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "BAD-2") || !strings.Contains(err.Error(), "27AAPFU0939F1ZV") {
		t.Errorf("error = %q, want it to name the supplier and the invoice so the user can find it", err.Error())
	}
}

func TestParseGSTR2BWithNoB2BSection(t *testing.T) {
	got, err := ParseGSTR2B(strings.NewReader(`{"data":{"rtnprd":"042025","docdata":{"cdnr":[]}}}`))
	if err != nil {
		t.Fatalf("a statement with nothing in it is not an error: %v", err)
	}
	if len(got.Invoices) != 0 {
		t.Errorf("read %d invoices, want none", len(got.Invoices))
	}
	if len(got.Notes) == 0 || !strings.Contains(strings.Join(got.Notes, " "), "no B2B section") {
		t.Errorf("notes = %v, want an explanation", got.Notes)
	}
}

func TestParseGSTR2BNotesMissingTaxableValue(t *testing.T) {
	const j = `{"docdata":{"b2b":[{"ctin":"27AAPFU0939F1ZV","inv":[{"inum":"A-1","idt":"01-04-2025","val":1180,"itms":[{"itm_det":{"iamt":180}}]}]}]}}`
	got, err := ParseGSTR2B(strings.NewReader(j))
	if err != nil {
		t.Fatal(err)
	}
	if got.Invoices[0].TaxableValue != 0 || got.Invoices[0].IGST != 18000 {
		t.Fatalf("invoice = %+v", got.Invoices[0])
	}
	if len(got.Notes) == 0 || !strings.Contains(strings.Join(got.Notes, " "), "no taxable value") {
		t.Errorf("notes = %v, want the missing taxable value explained", got.Notes)
	}
}

func TestPeriodText(t *testing.T) {
	cases := []struct{ in, want string }{
		{"092025", "September 2025"},
		{"012026", "January 2026"},
		{"122025", "December 2025"},
		{"", ""},
		{"2025", "2025"},
		{"132025", "132025"},
		{"002025", "002025"},
		{"ab2025", "ab2025"},
	}
	for _, c := range cases {
		if got := PeriodText(c.in); got != c.want {
			t.Errorf("PeriodText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseGSTR2BDoesNotPanicOnOddInput(t *testing.T) {
	odd := []string{
		"", " ", "{", "}", "[]", "null", "0", `""`, "\ufeff{}",
		`{"docdata":null}`,
		`{"docdata":{"b2b":null}}`,
		`{"docdata":{"b2b":[null]}}`,
		`{"docdata":{"b2b":[{}]}}`,
		`{"docdata":{"b2b":[{"ctin":"27AAPFU0939F1ZV","inv":null}]}}`,
		`{"docdata":{"b2b":[{"ctin":"27AAPFU0939F1ZV","inv":[null]}]}}`,
		`{"docdata":{"b2b":[{"ctin":"27AAPFU0939F1ZV","inv":[{"inum":"A","idt":"01-04-2025","itms":null}]}]}}`,
		`{"docdata":{"b2b":[{"ctin":["list"],"inv":[]}]}}`,
		`{"docdata":{"b2b":[{"ctin":"27AAPFU0939F1ZV","inv":[{"inum":"A","idt":"01-04-2025","itms":[{}]}]}]}}`,
		`{"data":{"docdata":{"b2b":[]}},"docdata":{"b2b":[]}}`,
		strings.Repeat(`{"a":`, 200) + "1" + strings.Repeat("}", 200),
	}
	for _, j := range odd {
		// Either an answer or an error, never a panic.
		if _, err := ParseGSTR2B(strings.NewReader(j)); err != nil && err.Error() == "" {
			t.Errorf("input %q gave an empty error message", j)
		}
	}
}
