package recon

import (
	"strings"
	"testing"
	"time"
)

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// inv builds a test invoice. Amounts are given in rupees for readability and
// held in paise, as everywhere else in the package.
func inv(src Source, gstin, no string, date time.Time, taxable, igst, cgst, sgst, cess int64) Invoice {
	return Invoice{
		SupplierGSTIN: gstin,
		InvoiceNo:     no,
		InvoiceDate:   date,
		TaxableValue:  taxable * 100,
		IGST:          igst * 100,
		CGST:          cgst * 100,
		SGST:          sgst * 100,
		Cess:          cess * 100,
		Source:        src,
	}
}

func TestReconcileSortsEveryInvoiceIntoOneCategory(t *testing.T) {
	jul15 := day(2025, time.July, 15)
	jul20 := day(2025, time.July, 20)
	jul25 := day(2025, time.July, 25)
	aug02 := day(2025, time.August, 2)

	twoB := []Invoice{
		inv(Source2B, "27AAPFU0939F1ZV", "INV/001/25-26", jul15, 100000, 18000, 0, 0, 0),
		inv(Source2B, "27AAPFU0939F1ZV", "INV/002/25-26", jul20, 50000, 9050, 0, 0, 0),
		inv(Source2B, "29AABCU9603R1ZM", "NH-0051", aug02, 10000, 0, 900, 900, 500),
	}
	books := []Invoice{
		// Same invoice, written differently: must match.
		inv(SourceBooks, "27aapfu0939f1zv", "inv-1/25-26", jul15, 100000, 18000, 0, 0, 0),
		// Same invoice, different tax: value mismatch.
		inv(SourceBooks, "27AAPFU0939F1ZV", "INV 002 / 25-26", jul20, 50000, 9000, 0, 0, 0),
		// Not in GSTR-2B at all: credit at risk.
		inv(SourceBooks, "27AAPFU0939F1ZV", "INV/003/25-26", jul25, 80000, 14400, 0, 0, 0),
		// Recorded twice.
		inv(SourceBooks, "29AABCU9603R1ZM", "NH-0060", aug02, 20000, 0, 1800, 1800, 0),
		inv(SourceBooks, "29AABCU9603R1ZM", "NH-60", aug02, 20000, 0, 1800, 1800, 0),
	}

	got := Reconcile(twoB, books, DefaultOptions())

	if n := len(got.Matched); n != 1 {
		t.Errorf("Matched = %d, want 1", n)
	} else if got.Matched[0].Books.InvoiceNo != "inv-1/25-26" {
		t.Errorf("matched the wrong invoice: %q", got.Matched[0].Books.InvoiceNo)
	}
	if n := len(got.MatchedWithValueMismatch); n != 1 {
		t.Fatalf("MatchedWithValueMismatch = %d, want 1", n)
	}
	m := got.MatchedWithValueMismatch[0]
	if len(m.Deltas) != 1 || m.Deltas[0].Head != "IGST" {
		t.Errorf("deltas = %+v, want one IGST delta", m.Deltas)
	}
	if m.Deltas[0].Delta != -5000 {
		t.Errorf("IGST delta = %d paise, want -5000 (books claim ₹50 less than GSTR-2B allows)", m.Deltas[0].Delta)
	}
	if m.TaxDelta != -5000 {
		t.Errorf("TaxDelta = %d paise, want -5000", m.TaxDelta)
	}

	if n := len(got.In2BNotInBooks); n != 1 || got.In2BNotInBooks[0].InvoiceNo != "NH-0051" {
		t.Errorf("In2BNotInBooks = %+v, want only NH-0051", got.In2BNotInBooks)
	}
	if n := len(got.InBooksNotIn2B); n != 2 {
		t.Fatalf("InBooksNotIn2B = %d, want 2", n)
	}
	if n := len(got.DuplicateInBooks); n != 1 {
		t.Fatalf("DuplicateInBooks = %d groups, want 1", n)
	}
	dup := got.DuplicateInBooks[0]
	if len(dup.Invoices) != 2 {
		t.Errorf("the duplicate group holds %d copies, want 2", len(dup.Invoices))
	}
	if dup.ExtraTax != 360000 {
		t.Errorf("ExtraTax = %d paise, want 360000 (the tax on the second copy)", dup.ExtraTax)
	}

	// The duplicated invoice is not in GSTR-2B, so it is also at risk — once.
	risk := map[string]bool{}
	for _, i := range got.InBooksNotIn2B {
		risk[i.InvoiceNo] = true
	}
	if !risk["INV/003/25-26"] || !risk["NH-0060"] {
		t.Errorf("InBooksNotIn2B holds %v, want INV/003/25-26 and NH-0060", risk)
	}

	wantRisk := int64((14400 + 3600) * 100)
	if got.TotalAtRisk != wantRisk {
		t.Errorf("TotalAtRisk = %d paise, want %d", got.TotalAtRisk, wantRisk)
	}
	if got.Summary.OnlyBooks.Tax != wantRisk {
		t.Errorf("summary tax for the at-risk category = %d, want %d", got.Summary.OnlyBooks.Tax, wantRisk)
	}
	if got.Summary.TwoBInvoices != 3 || got.Summary.BooksInvoices != 5 {
		t.Errorf("counted %d in GSTR-2B and %d in the books, want 3 and 5", got.Summary.TwoBInvoices, got.Summary.BooksInvoices)
	}

	// Every invoice in the books is accounted for exactly once.
	accounted := len(got.Matched) + len(got.MatchedWithValueMismatch) + len(got.InBooksNotIn2B)
	for _, g := range got.DuplicateInBooks {
		accounted += len(g.Invoices) - 1
	}
	if accounted != len(books) {
		t.Errorf("%d of %d book invoices are accounted for", accounted, len(books))
	}
}

func TestTotalAtRiskIsTheTaxOnInvoicesMissingFromGSTR2B(t *testing.T) {
	d := day(2025, time.May, 10)
	books := []Invoice{
		inv(SourceBooks, "27AAPFU0939F1ZV", "A1", d, 100000, 18000, 0, 0, 0),
		inv(SourceBooks, "27AAPFU0939F1ZV", "A2", d, 100000, 0, 9000, 9000, 1000),
	}
	got := Reconcile(nil, books, DefaultOptions())
	want := int64((18000 + 9000 + 9000 + 1000) * 100)
	if got.TotalAtRisk != want {
		t.Errorf("TotalAtRisk = %d paise, want %d (IGST + CGST + SGST + cess over both invoices)", got.TotalAtRisk, want)
	}
	if len(got.Matched) != 0 || len(got.In2BNotInBooks) != 0 {
		t.Error("with an empty GSTR-2B nothing can match")
	}
}

func TestToleranceBoundary(t *testing.T) {
	d := day(2025, time.June, 1)
	base := inv(Source2B, "27AAPFU0939F1ZV", "T-1", d, 100000, 18000, 0, 0, 0)

	cases := []struct {
		name         string
		tolerance    int64
		booksIGST    int64 // paise
		wantMismatch bool
	}{
		{"identical figures match", DefaultTolerancePaise, 1800000, false},
		{"a paisa inside the tolerance matches", DefaultTolerancePaise, 1800001, false},
		{"exactly the tolerance still matches", DefaultTolerancePaise, 1800100, false},
		{"one paisa beyond the tolerance is a mismatch", DefaultTolerancePaise, 1800101, true},
		{"the tolerance works downwards too", DefaultTolerancePaise, 1799900, false},
		{"one paisa below the tolerance is a mismatch", DefaultTolerancePaise, 1799899, true},
		{"a zero tolerance demands the exact paisa", 0, 1800001, true},
		{"a zero tolerance accepts the exact paisa", 0, 1800000, false},
		{"a wide tolerance absorbs a rupee", 500, 1800400, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			books := base
			books.Source = SourceBooks
			books.IGST = c.booksIGST
			res := Reconcile([]Invoice{base}, []Invoice{books}, Options{TolerancePaise: c.tolerance})
			gotMismatch := len(res.MatchedWithValueMismatch) == 1
			if gotMismatch != c.wantMismatch {
				t.Fatalf("mismatch = %v, want %v (tolerance %d paise, books IGST %d paise)", gotMismatch, c.wantMismatch, c.tolerance, c.booksIGST)
			}
			if !gotMismatch && len(res.Matched) != 1 {
				t.Fatalf("the invoice fell out of both matched categories: %+v", res)
			}
			if res.TolerancePaise != c.tolerance {
				t.Errorf("result records a tolerance of %d, want %d", res.TolerancePaise, c.tolerance)
			}
		})
	}
}

func TestANegativeToleranceIsTreatedAsExact(t *testing.T) {
	d := day(2025, time.June, 1)
	a := inv(Source2B, "27AAPFU0939F1ZV", "T-1", d, 100000, 18000, 0, 0, 0)
	b := a
	b.Source = SourceBooks
	b.IGST += 1
	res := Reconcile([]Invoice{a}, []Invoice{b}, Options{TolerancePaise: -500})
	if len(res.MatchedWithValueMismatch) != 1 {
		t.Errorf("a negative tolerance should mean no allowance at all, got %+v", res.Summary)
	}
	if res.TolerancePaise != 0 {
		t.Errorf("recorded tolerance = %d, want 0", res.TolerancePaise)
	}
}

func TestEveryComparedHeadIsChecked(t *testing.T) {
	d := day(2025, time.September, 9)
	cases := []struct {
		head  string
		apply func(*Invoice)
	}{
		{"Taxable value", func(i *Invoice) { i.TaxableValue += 50000 }},
		{"IGST", func(i *Invoice) { i.IGST += 50000 }},
		{"CGST", func(i *Invoice) { i.CGST += 50000 }},
		{"SGST", func(i *Invoice) { i.SGST += 50000 }},
		{"Cess", func(i *Invoice) { i.Cess += 50000 }},
	}
	for _, c := range cases {
		t.Run(c.head, func(t *testing.T) {
			a := inv(Source2B, "27AAPFU0939F1ZV", "H-1", d, 100000, 9000, 4500, 4500, 100)
			b := a
			b.Source = SourceBooks
			c.apply(&b)
			res := Reconcile([]Invoice{a}, []Invoice{b}, DefaultOptions())
			if len(res.MatchedWithValueMismatch) != 1 {
				t.Fatalf("a difference in %s was not reported", c.head)
			}
			ds := res.MatchedWithValueMismatch[0].Deltas
			if len(ds) != 1 || ds[0].Head != c.head {
				t.Fatalf("deltas = %+v, want one for %s", ds, c.head)
			}
			if ds[0].Delta != 50000 {
				t.Errorf("delta = %d paise, want 50000", ds[0].Delta)
			}
			if want := headValue(c.head, a); ds[0].TwoB != want {
				t.Errorf("delta records the GSTR-2B figure as %d, want %d", ds[0].TwoB, want)
			}
			if want := headValue(c.head, b); ds[0].Books != want {
				t.Errorf("delta records the books figure as %d, want %d", ds[0].Books, want)
			}
		})
	}
}

// headValue reads one compared head off an invoice by name.
func headValue(name string, i Invoice) int64 {
	for _, h := range comparedHeads {
		if h.Name == name {
			return h.Get(i)
		}
	}
	return 0
}

func TestInvoiceNumbersAreMatchedAcrossFinancialYears(t *testing.T) {
	// The same invoice number in two financial years: each must match its own
	// year, and neither must match the other.
	twoB := []Invoice{
		inv(Source2B, "27AAPFU0939F1ZV", "1", day(2025, time.March, 31), 10000, 1800, 0, 0, 0),
		inv(Source2B, "27AAPFU0939F1ZV", "1", day(2025, time.April, 1), 20000, 3600, 0, 0, 0),
	}
	books := []Invoice{
		inv(SourceBooks, "27AAPFU0939F1ZV", "001", day(2025, time.April, 1), 20000, 3600, 0, 0, 0),
	}
	got := Reconcile(twoB, books, DefaultOptions())
	if len(got.Matched) != 1 {
		t.Fatalf("Matched = %d, want 1", len(got.Matched))
	}
	if got.Matched[0].TwoB.TaxableValue != 2000000 {
		t.Errorf("matched the invoice from the wrong financial year (taxable %d paise)", got.Matched[0].TwoB.TaxableValue)
	}
	if len(got.In2BNotInBooks) != 1 || got.In2BNotInBooks[0].InvoiceDate.Month() != time.March {
		t.Errorf("the 31 March invoice should be left unmatched, got %+v", got.In2BNotInBooks)
	}
	if len(got.InBooksNotIn2B) != 0 {
		t.Errorf("nothing should be at risk here, got %+v", got.InBooksNotIn2B)
	}
}

func TestSupplierRollupIsSortedByAmountAtRisk(t *testing.T) {
	d := day(2025, time.July, 7)
	books := []Invoice{
		inv(SourceBooks, "27AAPFU0939F1ZV", "S-1", d, 10000, 1800, 0, 0, 0),
		inv(SourceBooks, "29AABCU9603R1ZM", "B-1", d, 100000, 18000, 0, 0, 0),
		inv(SourceBooks, "27AAPFU0939F1ZV", "S-2", d, 20000, 3600, 0, 0, 0),
		inv(SourceBooks, "33AACCM1234K1Z8", "M-1", d, 5000, 900, 0, 0, 0),
	}
	books[1].SupplierName = "Nandi Hardware"
	got := Reconcile(nil, books, DefaultOptions())

	if len(got.Suppliers) != 3 {
		t.Fatalf("suppliers = %d, want 3", len(got.Suppliers))
	}
	wantOrder := []string{"29AABCU9603R1ZM", "27AAPFU0939F1ZV", "33AACCM1234K1Z8"}
	wantRisk := []int64{1800000, 540000, 90000}
	for i, s := range got.Suppliers {
		if s.GSTIN != wantOrder[i] {
			t.Errorf("supplier %d = %s, want %s (largest amount at risk first)", i, s.GSTIN, wantOrder[i])
		}
		if s.TaxAtRisk != wantRisk[i] {
			t.Errorf("%s tax at risk = %d paise, want %d", s.GSTIN, s.TaxAtRisk, wantRisk[i])
		}
	}
	if got.Suppliers[0].Name != "Nandi Hardware" {
		t.Errorf("supplier name = %q, want Nandi Hardware", got.Suppliers[0].Name)
	}
	if n := len(got.Suppliers[1].Missing); n != 2 {
		t.Errorf("the two-invoice supplier lists %d invoices, want 2", n)
	}
	var sum int64
	for _, s := range got.Suppliers {
		sum += s.TaxAtRisk
	}
	if sum != got.TotalAtRisk {
		t.Errorf("the supplier rollup totals %d paise but TotalAtRisk is %d", sum, got.TotalAtRisk)
	}
}

func TestDuplicatesInBooksAreGroupedAndCountedOnce(t *testing.T) {
	d := day(2025, time.July, 7)
	a := inv(SourceBooks, "27AAPFU0939F1ZV", "D-1", d, 10000, 1800, 0, 0, 0)
	b := inv(SourceBooks, "27AAPFU0939F1ZV", "D/001", d, 10000, 1800, 0, 0, 0)
	c := inv(SourceBooks, "27AAPFU0939F1ZV", "d 1", d, 10000, 1800, 0, 0, 0)
	twoB := []Invoice{inv(Source2B, "27AAPFU0939F1ZV", "D-1", d, 10000, 1800, 0, 0, 0)}

	got := Reconcile(twoB, []Invoice{a, b, c}, DefaultOptions())
	if len(got.DuplicateInBooks) != 1 {
		t.Fatalf("duplicate groups = %d, want 1", len(got.DuplicateInBooks))
	}
	g := got.DuplicateInBooks[0]
	if len(g.Invoices) != 3 {
		t.Errorf("copies = %d, want 3", len(g.Invoices))
	}
	if g.ExtraTax != 360000 {
		t.Errorf("ExtraTax = %d paise, want 360000 (two extra copies at ₹1,800)", g.ExtraTax)
	}
	if len(got.Matched) != 1 {
		t.Errorf("the invoice itself should still match GSTR-2B once, got %d matches", len(got.Matched))
	}
	if got.TotalAtRisk != 0 {
		t.Errorf("TotalAtRisk = %d, want 0: the invoice is in GSTR-2B", got.TotalAtRisk)
	}
	if got.Summary.Duplicates.Count != 1 || got.Summary.Duplicates.Tax != 360000 {
		t.Errorf("duplicate summary = %+v, want 1 group and 360000 paise", got.Summary.Duplicates)
	}
}

func TestDuplicatesInGSTR2BAreNotedAndUsedOnce(t *testing.T) {
	d := day(2025, time.July, 7)
	a := inv(Source2B, "27AAPFU0939F1ZV", "X-1", d, 10000, 1800, 0, 0, 0)
	got := Reconcile([]Invoice{a, a}, nil, DefaultOptions())
	if len(got.In2BNotInBooks) != 1 {
		t.Errorf("a repeated GSTR-2B row should be counted once, got %d", len(got.In2BNotInBooks))
	}
	if got.Summary.TwoBInvoices != 1 {
		t.Errorf("GSTR-2B count = %d, want 1", got.Summary.TwoBInvoices)
	}
	if len(got.Notes) == 0 || !strings.Contains(strings.ToLower(got.Notes[0]), "repeated") {
		t.Errorf("a repeated GSTR-2B row should be explained in the notes, got %v", got.Notes)
	}
}

func TestReconcileWithNoInputAtAll(t *testing.T) {
	got := Reconcile(nil, nil, DefaultOptions())
	if got.TotalAtRisk != 0 || len(got.Matched) != 0 || len(got.Suppliers) != 0 {
		t.Errorf("empty input should give an empty result, got %+v", got)
	}
	if got.TolerancePaise != DefaultTolerancePaise {
		t.Errorf("tolerance = %d, want the default %d", got.TolerancePaise, DefaultTolerancePaise)
	}
}

func TestZeroValueInvoicesStillMatch(t *testing.T) {
	d := day(2025, time.July, 7)
	a := inv(Source2B, "27AAPFU0939F1ZV", "Z-1", d, 0, 0, 0, 0, 0)
	b := a
	b.Source = SourceBooks
	got := Reconcile([]Invoice{a}, []Invoice{b}, DefaultOptions())
	if len(got.Matched) != 1 {
		t.Errorf("an invoice with no tax should still match, got %+v", got.Summary)
	}
	if got.TotalAtRisk != 0 {
		t.Errorf("TotalAtRisk = %d, want 0", got.TotalAtRisk)
	}
}

func TestResultIsStableWhateverTheInputOrder(t *testing.T) {
	d := day(2025, time.July, 7)
	twoB := []Invoice{
		inv(Source2B, "29AABCU9603R1ZM", "N-2", d, 1000, 180, 0, 0, 0),
		inv(Source2B, "27AAPFU0939F1ZV", "S-1", d, 1000, 180, 0, 0, 0),
	}
	books := []Invoice{
		inv(SourceBooks, "33AACCM1234K1Z8", "M-9", d, 1000, 180, 0, 0, 0),
		inv(SourceBooks, "27AAPFU0939F1ZV", "S-3", d, 2000, 360, 0, 0, 0),
	}
	first := Reconcile(twoB, books, DefaultOptions())
	reversed := Reconcile([]Invoice{twoB[1], twoB[0]}, []Invoice{books[1], books[0]}, DefaultOptions())

	if len(first.In2BNotInBooks) != 2 || len(reversed.In2BNotInBooks) != 2 {
		t.Fatalf("expected two unmatched GSTR-2B invoices either way")
	}
	for i := range first.In2BNotInBooks {
		if first.In2BNotInBooks[i].InvoiceNo != reversed.In2BNotInBooks[i].InvoiceNo {
			t.Errorf("row %d differs with the input order: %q then %q", i, first.In2BNotInBooks[i].InvoiceNo, reversed.In2BNotInBooks[i].InvoiceNo)
		}
	}
	if first.In2BNotInBooks[0].SupplierGSTIN != "27AAPFU0939F1ZV" {
		t.Errorf("rows should come out ordered by supplier, got %s first", first.In2BNotInBooks[0].SupplierGSTIN)
	}
	for i := range first.Suppliers {
		if first.Suppliers[i].GSTIN != reversed.Suppliers[i].GSTIN {
			t.Errorf("supplier %d differs with the input order", i)
		}
	}
}

func TestCategoryTitles(t *testing.T) {
	for _, c := range []Category{CatMatched, CatValueMismatch, CatOnly2B, CatOnlyBooks, CatDuplicates, CatSuppliers} {
		if c.Title() == "" || c.Title() == string(c) {
			t.Errorf("category %q has no readable title", string(c))
		}
		if !strings.HasSuffix(c.FileName(), ".csv") {
			t.Errorf("category %q has no CSV file name", string(c))
		}
	}
}
