package recon

import (
	"os"
	"strings"
	"testing"
)

func TestParsePurchaseCSVFixture(t *testing.T) {
	f, err := os.Open("testdata/purchase-register-sample.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	reg, err := ParsePurchaseCSV(f)
	if err != nil {
		t.Fatalf("ParsePurchaseCSV: %v", err)
	}
	if len(reg.Invoices) != 6 {
		t.Fatalf("read %d invoices, want 6 (the title, blank and total rows are not invoices)", len(reg.Invoices))
	}

	first := reg.Invoices[0]
	if first.SupplierGSTIN != "27AAPFU0939F1ZV" || first.InvoiceNo != "inv-1/25-26" {
		t.Errorf("first invoice = %s / %s", first.SupplierGSTIN, first.InvoiceNo)
	}
	if first.SupplierName != "Sundar Metals" {
		t.Errorf("supplier name = %q", first.SupplierName)
	}
	if first.DateText() != "15-07-2025" {
		t.Errorf("date = %q, want 15-07-2025", first.DateText())
	}
	if first.TaxableValue != 10000000 {
		t.Errorf("taxable = %d paise, want 10000000 (read from \"1,00,000.00\")", first.TaxableValue)
	}
	if first.IGST != 1800000 {
		t.Errorf("IGST = %d paise, want 1800000", first.IGST)
	}
	if first.Source != SourceBooks {
		t.Errorf("source = %q, want %q", first.Source, SourceBooks)
	}
	if first.Line != 3 {
		t.Errorf("line = %d, want 3 (the title row is line 1 and the headings line 2)", first.Line)
	}

	last := reg.Invoices[5]
	if last.InvoiceNo != "NH-0052" || last.CGST != 45000 || last.SGST != 45000 {
		t.Errorf("last invoice = %+v", last)
	}
	joined := strings.Join(reg.Notes, " ")
	if !strings.Contains(joined, "total") {
		t.Errorf("notes = %v, want the skipped total row explained", reg.Notes)
	}
}

// The fixtures are a matching pair: parsing both and reconciling them must
// give the five categories the tool is built to report.
func TestFixturesReconcileEndToEnd(t *testing.T) {
	jf, err := os.Open("testdata/gstr2b-sample.json")
	if err != nil {
		t.Fatal(err)
	}
	defer jf.Close()
	twoB, err := ParseGSTR2B(jf)
	if err != nil {
		t.Fatal(err)
	}
	cf, err := os.Open("testdata/purchase-register-sample.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer cf.Close()
	books, err := ParsePurchaseCSV(cf)
	if err != nil {
		t.Fatal(err)
	}

	res := Reconcile(twoB.Invoices, books.Invoices, DefaultOptions())

	if n := len(res.Matched); n != 2 {
		t.Errorf("Matched = %d, want 2", n)
	}
	if n := len(res.MatchedWithValueMismatch); n != 1 {
		t.Fatalf("MatchedWithValueMismatch = %d, want 1", n)
	}
	if no := res.MatchedWithValueMismatch[0].Books.InvoiceNo; !strings.Contains(no, "002") {
		t.Errorf("the mismatch is on %q, want invoice 002", no)
	}
	if n := len(res.In2BNotInBooks); n != 1 || res.In2BNotInBooks[0].InvoiceNo != "NH-0051" {
		t.Errorf("In2BNotInBooks = %+v, want NH-0051", res.In2BNotInBooks)
	}
	if n := len(res.InBooksNotIn2B); n != 2 {
		t.Fatalf("InBooksNotIn2B = %d, want 2", n)
	}
	if n := len(res.DuplicateInBooks); n != 1 {
		t.Errorf("DuplicateInBooks = %d, want 1", n)
	}
	// INV/003 carries ₹14,400 of IGST and NH-0052 ₹900 of CGST plus SGST.
	if want := int64(1530000); res.TotalAtRisk != want {
		t.Errorf("TotalAtRisk = %d paise (₹%s), want %d", res.TotalAtRisk, Rupees(res.TotalAtRisk), want)
	}
	if len(res.Suppliers) != 2 || res.Suppliers[0].GSTIN != "27AAPFU0939F1ZV" {
		t.Errorf("suppliers = %+v, want the larger amount first", res.Suppliers)
	}
}

func TestParsePurchaseCSVShapes(t *testing.T) {
	cases := []struct {
		name string
		csv  string
	}{
		{
			name: "plain headings",
			csv:  "Supplier GSTIN,Invoice no,Invoice date,Taxable value,IGST,CGST,SGST,Cess\n27AAPFU0939F1ZV,A-1,01-04-2025,1000.00,180.00,0,0,0\n",
		},
		{
			name: "accounting package headings",
			csv:  "GSTIN of Supplier,Bill No,Bill Date,Assessable Value,IGST Amount,CGST Amount,SGST/UTGST Amount,Cess Amount\n27AAPFU0939F1ZV,A-1,01-04-2025,1000.00,180.00,0,0,0\n",
		},
		{
			name: "portal style headings",
			csv:  "ctin,inum,idt,txval,iamt,camt,samt,csamt\n27AAPFU0939F1ZV,A-1,01-04-2025,1000.00,180.00,0,0,0\n",
		},
		{
			name: "units and symbols in the headings",
			csv:  "Supplier GSTIN,Invoice No.,Invoice Date (dd-mm-yyyy),Taxable Value (₹),IGST (₹),CGST (₹),SGST (₹),Cess (₹)\n27AAPFU0939F1ZV,A-1,01-04-2025,1000.00,180.00,0,0,0\n",
		},
		{
			name: "columns in another order",
			csv:  "Invoice date,Cess,SGST,CGST,IGST,Taxable value,Invoice no,Supplier GSTIN\n01-04-2025,0,0,0,180.00,1000.00,A-1,27AAPFU0939F1ZV\n",
		},
		{
			name: "a byte order mark and windows line endings",
			csv:  "\ufeffSupplier GSTIN,Invoice no,Invoice date,Taxable value,IGST,CGST,SGST,Cess\r\n27AAPFU0939F1ZV,A-1,01-04-2025,1000.00,180.00,0,0,0\r\n",
		},
		{
			name: "semicolon separated",
			csv:  "Supplier GSTIN;Invoice no;Invoice date;Taxable value;IGST;CGST;SGST;Cess\n27AAPFU0939F1ZV;A-1;01-04-2025;1000.00;180.00;0;0;0\n",
		},
		{
			name: "tab separated",
			csv:  "Supplier GSTIN\tInvoice no\tInvoice date\tTaxable value\tIGST\tCGST\tSGST\tCess\n27AAPFU0939F1ZV\tA-1\t01-04-2025\t1000.00\t180.00\t0\t0\t0\n",
		},
		{
			name: "title rows above the headings",
			csv:  "Acme Trading Co\nPurchase register\nApril 2025\n\nSupplier GSTIN,Invoice no,Invoice date,Taxable value,IGST,CGST,SGST,Cess\n27AAPFU0939F1ZV,A-1,01-04-2025,1000.00,180.00,0,0,0\n",
		},
		{
			name: "extra columns are ignored",
			csv:  "Sr No,Supplier GSTIN,HSN,Invoice no,Invoice date,Rate,Taxable value,IGST,CGST,SGST,Cess,Remarks\n1,27AAPFU0939F1ZV,7308,A-1,01-04-2025,18,1000.00,180.00,0,0,0,ok\n",
		},
		{
			name: "short rows are padded",
			csv:  "Supplier GSTIN,Invoice no,Invoice date,Taxable value,IGST,CGST,SGST,Cess\n27AAPFU0939F1ZV,A-1,01-04-2025,1000.00,180.00\n",
		},
		{
			name: "quoted cells with commas",
			csv:  "Supplier GSTIN,Supplier name,Invoice no,Invoice date,Taxable value,IGST,CGST,SGST,Cess\n27AAPFU0939F1ZV,\"Sundar Metals, Pune\",A-1,01-04-2025,\"1,000.00\",\"180.00\",0,0,0\n",
		},
		{
			name: "dashes for nil amounts",
			csv:  "Supplier GSTIN,Invoice no,Invoice date,Taxable value,IGST,CGST,SGST,Cess\n27AAPFU0939F1ZV,A-1,01-04-2025,1000.00,180.00,-,-,-\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reg, err := ParsePurchaseCSV(strings.NewReader(c.csv))
			if err != nil {
				t.Fatalf("ParsePurchaseCSV: %v", err)
			}
			if len(reg.Invoices) != 1 {
				t.Fatalf("read %d invoices, want 1", len(reg.Invoices))
			}
			g := reg.Invoices[0]
			if g.SupplierGSTIN != "27AAPFU0939F1ZV" {
				t.Errorf("GSTIN = %q", g.SupplierGSTIN)
			}
			if g.InvoiceNo != "A-1" {
				t.Errorf("invoice no = %q, want A-1", g.InvoiceNo)
			}
			if g.DateText() != "01-04-2025" {
				t.Errorf("date = %q, want 01-04-2025", g.DateText())
			}
			if g.TaxableValue != 100000 {
				t.Errorf("taxable = %d paise, want 100000", g.TaxableValue)
			}
			if g.IGST != 18000 {
				t.Errorf("IGST = %d paise, want 18000", g.IGST)
			}
			if g.CGST != 0 || g.SGST != 0 || g.Cess != 0 {
				t.Errorf("heads = %d/%d/%d, want zeros", g.CGST, g.SGST, g.Cess)
			}
		})
	}
}

func TestParsePurchaseCSVRejectsBadInputWithAClearError(t *testing.T) {
	cases := []struct {
		name, csv, wantIn string
	}{
		{"empty file", "", "empty"},
		{"no headings we know", "one,two,three\n1,2,3\n", "heading row"},
		{"no date column", "Supplier GSTIN,Invoice no,Taxable value,IGST\n27AAPFU0939F1ZV,A-1,1000,180\n", "no invoice date column"},
		{"headings but no rows", "Supplier GSTIN,Invoice no,Invoice date,Taxable value,IGST,CGST,SGST,Cess\n", "no invoice rows"},
		{
			"a row without a GSTIN",
			"Supplier GSTIN,Invoice no,Invoice date,Taxable value,IGST,CGST,SGST,Cess\n,A-1,01-04-2025,1000,180,0,0,0\n",
			"has no supplier GSTIN",
		},
		{
			"a row without an invoice number",
			"Supplier GSTIN,Invoice no,Invoice date,Taxable value,IGST,CGST,SGST,Cess\n27AAPFU0939F1ZV,,01-04-2025,1000,180,0,0,0\n",
			"has no invoice number",
		},
		{
			"a row without a date",
			"Supplier GSTIN,Invoice no,Invoice date,Taxable value,IGST,CGST,SGST,Cess\n27AAPFU0939F1ZV,A-1,,1000,180,0,0,0\n",
			"has no invoice date",
		},
		{
			"an impossible date",
			"Supplier GSTIN,Invoice no,Invoice date,Taxable value,IGST,CGST,SGST,Cess\n27AAPFU0939F1ZV,A-1,31-04-2025,1000,180,0,0,0\n",
			"not a date",
		},
		{
			"an amount that is not a number",
			"Supplier GSTIN,Invoice no,Invoice date,Taxable value,IGST,CGST,SGST,Cess\n27AAPFU0939F1ZV,A-1,01-04-2025,1000,eighteen,0,0,0\n",
			"IGST",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reg, err := ParsePurchaseCSV(strings.NewReader(c.csv))
			if err == nil {
				t.Fatalf("accepted a bad register and returned %d invoices", len(reg.Invoices))
			}
			if !strings.Contains(err.Error(), c.wantIn) {
				t.Errorf("error = %q, want it to mention %q", err.Error(), c.wantIn)
			}
		})
	}
}

func TestParsePurchaseCSVErrorNamesTheLine(t *testing.T) {
	const csv = "Supplier GSTIN,Invoice no,Invoice date,Taxable value,IGST,CGST,SGST,Cess\n" +
		"27AAPFU0939F1ZV,A-1,01-04-2025,1000,180,0,0,0\n" +
		"27AAPFU0939F1ZV,A-2,01-04-2025,1000,180,0,0,0\n" +
		"27AAPFU0939F1ZV,A-3,not a date,1000,180,0,0,0\n"
	_, err := ParsePurchaseCSV(strings.NewReader(csv))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "line 4") || !strings.Contains(err.Error(), "A-3") {
		t.Errorf("error = %q, want it to name line 4 and invoice A-3", err.Error())
	}
}

func TestParsePurchaseCSVNotes(t *testing.T) {
	t.Run("a mistyped GSTIN is flagged", func(t *testing.T) {
		const csv = "Supplier GSTIN,Invoice no,Invoice date,Taxable value,IGST,CGST,SGST,Cess\n" +
			"27AAPFU0939F1ZV,A-1,01-04-2025,1000,180,0,0,0\n" +
			"27AAPFU0939F1Z,A-2,01-04-2025,1000,180,0,0,0\n"
		reg, err := ParsePurchaseCSV(strings.NewReader(csv))
		if err != nil {
			t.Fatal(err)
		}
		if len(reg.Invoices) != 2 {
			t.Fatalf("read %d invoices, want 2: a doubtful GSTIN is reported, not dropped", len(reg.Invoices))
		}
		joined := strings.Join(reg.Notes, " ")
		if !strings.Contains(joined, "line 3") || !strings.Contains(joined, "15-character") {
			t.Errorf("notes = %v, want line 3 flagged", reg.Notes)
		}
	})

	t.Run("missing tax columns are called out", func(t *testing.T) {
		const csv = "Supplier GSTIN,Invoice no,Invoice date,Taxable value,IGST\n" +
			"27AAPFU0939F1ZV,A-1,01-04-2025,1000,180\n"
		reg, err := ParsePurchaseCSV(strings.NewReader(csv))
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(reg.Notes, " ")
		for _, head := range []string{"CGST", "SGST", "Cess"} {
			if !strings.Contains(joined, head) {
				t.Errorf("notes = %v, want %s named as missing", reg.Notes, head)
			}
		}
		if reg.Invoices[0].CGST != 0 {
			t.Errorf("a missing column must read as zero, got %d", reg.Invoices[0].CGST)
		}
	})

	t.Run("blank rows in the middle are skipped", func(t *testing.T) {
		const csv = "Supplier GSTIN,Invoice no,Invoice date,Taxable value,IGST,CGST,SGST,Cess\n" +
			"27AAPFU0939F1ZV,A-1,01-04-2025,1000,180,0,0,0\n" +
			",,,,,,,\n" +
			"27AAPFU0939F1ZV,A-2,01-04-2025,1000,180,0,0,0\n"
		reg, err := ParsePurchaseCSV(strings.NewReader(csv))
		if err != nil {
			t.Fatal(err)
		}
		if len(reg.Invoices) != 2 {
			t.Errorf("read %d invoices, want 2", len(reg.Invoices))
		}
	})
}

func TestParsePurchaseCSVDoesNotPanicOnOddInput(t *testing.T) {
	odd := []string{
		"", "\n", ",,,\n", "\"", "\"unclosed,quote\n1,2\n",
		"Supplier GSTIN\n27AAPFU0939F1ZV\n",
		"Supplier GSTIN,Invoice no\n27AAPFU0939F1ZV,A-1\n",
		"Supplier GSTIN,Invoice no,Invoice date\n27AAPFU0939F1ZV,A-1,01-04-2025\n",
		strings.Repeat("a,", 5000) + "\n",
		"\x00\x01\x02\n",
	}
	for _, c := range odd {
		if _, err := ParsePurchaseCSV(strings.NewReader(c)); err != nil && err.Error() == "" {
			t.Errorf("input %q gave an empty error message", c)
		}
	}
}

func TestDetectDelimiter(t *testing.T) {
	cases := []struct {
		in   string
		want rune
	}{
		{"a,b,c\n", ','},
		{"a;b;c\n", ';'},
		{"a\tb\tc\n", '\t'},
		{"\n\n a;b;c \n", ';'},
		{"", ','},
		{"single\n", ','},
	}
	for _, c := range cases {
		if got := detectDelimiter([]byte(c.in)); got != c.want {
			t.Errorf("detectDelimiter(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsTotalLabel(t *testing.T) {
	for _, s := range []string{"Total", "total", "TOTALS", "Grand Total", "Sub-total", " total "} {
		if !isTotalLabel(s) {
			t.Errorf("isTotalLabel(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "27AAPFU0939F1ZV", "A-1", "Totally Tools Pvt Ltd"} {
		if isTotalLabel(s) {
			t.Errorf("isTotalLabel(%q) = true, want false", s)
		}
	}
}
