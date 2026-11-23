package recon

import (
	"encoding/csv"
	"strings"
	"testing"
	"time"
)

// sampleResult builds a result holding one invoice in every category.
func sampleResult(t *testing.T) Result {
	t.Helper()
	d := day(2025, time.July, 15)
	twoB := []Invoice{
		inv(Source2B, "27AAPFU0939F1ZV", "M-1", d, 100000, 18000, 0, 0, 0),  // matches
		inv(Source2B, "27AAPFU0939F1ZV", "V-1", d, 100000, 18000, 0, 0, 0),  // value mismatch
		inv(Source2B, "29AABCU9603R1ZM", "O-1", d, 10000, 0, 900, 900, 100), // only in 2B
	}
	books := []Invoice{
		inv(SourceBooks, "27AAPFU0939F1ZV", "M-1", d, 100000, 18000, 0, 0, 0),
		inv(SourceBooks, "27AAPFU0939F1ZV", "V-1", d, 100000, 17000, 0, 0, 0),
		inv(SourceBooks, "27AAPFU0939F1ZV", "R-1", d, 50000, 9000, 0, 0, 0), // at risk
		inv(SourceBooks, "29AABCU9603R1ZM", "D-1", d, 20000, 0, 1800, 1800, 0),
		inv(SourceBooks, "29AABCU9603R1ZM", "D-1", d, 20000, 0, 1800, 1800, 0), // duplicate
	}
	for i := range books {
		books[i].SupplierName = "Sundar Metals"
		books[i].Line = i + 2
	}
	res := Reconcile(twoB, books, DefaultOptions())
	if len(res.Matched) != 1 || len(res.MatchedWithValueMismatch) != 1 || len(res.In2BNotInBooks) != 1 ||
		len(res.InBooksNotIn2B) != 2 || len(res.DuplicateInBooks) != 1 {
		t.Fatalf("the sample does not cover every category: %+v", res.Summary)
	}
	return res
}

func readCSV(t *testing.T, b []byte) [][]string {
	t.Helper()
	rows, err := csv.NewReader(strings.NewReader(string(b))).ReadAll()
	if err != nil {
		t.Fatalf("the exported CSV does not parse: %v", err)
	}
	return rows
}

func TestCategoryCSVCoversEveryCategory(t *testing.T) {
	res := sampleResult(t)
	cases := []struct {
		cat      Category
		wantRows int // heading row included
		wantHead string
	}{
		{CatMatched, 2, "Supplier GSTIN"},
		{CatValueMismatch, 2, "Tax difference (books - GSTR-2B)"},
		{CatOnly2B, 2, "Supplier GSTIN"},
		{CatOnlyBooks, 3, "ITC at risk"},
		{CatDuplicates, 3, "Copies in register"},
		{CatSuppliers, 3, "Invoice numbers"},
	}
	for _, c := range cases {
		t.Run(string(c.cat), func(t *testing.T) {
			b, err := CategoryCSV(res, c.cat)
			if err != nil {
				t.Fatalf("CategoryCSV: %v", err)
			}
			rows := readCSV(t, b)
			if len(rows) != c.wantRows {
				t.Fatalf("got %d rows (heading included), want %d:\n%s", len(rows), c.wantRows, b)
			}
			if !strings.Contains(strings.Join(rows[0], ","), c.wantHead) {
				t.Errorf("heading row %v does not hold %q", rows[0], c.wantHead)
			}
			width := len(rows[0])
			for i, r := range rows {
				if len(r) != width {
					t.Errorf("row %d has %d cells, heading has %d", i, len(r), width)
				}
			}
		})
	}
}

func TestCategoryCSVValuesAreMachineReadable(t *testing.T) {
	res := sampleResult(t)
	b, err := CategoryCSV(res, CatOnlyBooks)
	if err != nil {
		t.Fatal(err)
	}
	rows := readCSV(t, b)
	head := map[string]int{}
	for i, h := range rows[0] {
		head[h] = i
	}
	row := rows[1]
	if got := row[head["Supplier GSTIN"]]; got != "27AAPFU0939F1ZV" {
		t.Errorf("GSTIN cell = %q", got)
	}
	if got := row[head["Invoice no"]]; got != "R-1" {
		t.Errorf("invoice cell = %q, want R-1", got)
	}
	if got := row[head["Invoice date"]]; got != "15-07-2025" {
		t.Errorf("date cell = %q, want 15-07-2025", got)
	}
	// Plain decimals: no grouping, no currency sign, so a spreadsheet reads
	// them back as numbers.
	if got := row[head["IGST"]]; got != "9000.00" {
		t.Errorf("IGST cell = %q, want 9000.00", got)
	}
	if got := row[head["ITC at risk"]]; got != "9000.00" {
		t.Errorf("at-risk cell = %q, want 9000.00", got)
	}
	for _, cell := range row {
		if strings.ContainsAny(cell, "\u20b9,") {
			t.Errorf("cell %q carries a currency sign or grouping comma", cell)
		}
	}
}

func TestCategoryCSVMismatchShowsBothSides(t *testing.T) {
	res := sampleResult(t)
	b, err := CategoryCSV(res, CatValueMismatch)
	if err != nil {
		t.Fatal(err)
	}
	rows := readCSV(t, b)
	head := map[string]int{}
	for i, h := range rows[0] {
		head[h] = i
	}
	row := rows[1]
	if got := row[head["IGST (GSTR-2B)"]]; got != "18000.00" {
		t.Errorf("IGST from GSTR-2B = %q, want 18000.00", got)
	}
	if got := row[head["IGST (books)"]]; got != "17000.00" {
		t.Errorf("IGST from the books = %q, want 17000.00", got)
	}
	if got := row[head["Tax difference (books - GSTR-2B)"]]; got != "-1000.00" {
		t.Errorf("difference = %q, want -1000.00", got)
	}
	if got := row[head["Heads outside tolerance"]]; got != "IGST" {
		t.Errorf("heads outside tolerance = %q, want IGST", got)
	}
}

func TestCategoryCSVDuplicateRowsAreNumbered(t *testing.T) {
	res := sampleResult(t)
	b, err := CategoryCSV(res, CatDuplicates)
	if err != nil {
		t.Fatal(err)
	}
	rows := readCSV(t, b)
	if rows[1][4] != "1" || rows[2][4] != "2" {
		t.Errorf("copies are numbered %q and %q, want 1 and 2", rows[1][4], rows[2][4])
	}
	if rows[1][9] != "3600.00" {
		t.Errorf("tax on the extra copies = %q, want 3600.00", rows[1][9])
	}
	if rows[2][9] != "" {
		t.Errorf("the extra-copy total should appear once per group, got %q on the second row", rows[2][9])
	}
}

func TestCategoryCSVOfAnEmptyResultIsJustTheHeading(t *testing.T) {
	res := Reconcile(nil, nil, DefaultOptions())
	for _, cat := range []Category{CatMatched, CatValueMismatch, CatOnly2B, CatOnlyBooks, CatDuplicates, CatSuppliers} {
		b, err := CategoryCSV(res, cat)
		if err != nil {
			t.Fatalf("CategoryCSV(%s): %v", cat, err)
		}
		rows := readCSV(t, b)
		if len(rows) != 1 {
			t.Errorf("%s exported %d rows for an empty result, want just the heading", cat, len(rows))
		}
	}
}

func TestCategoryCSVRejectsAnUnknownCategory(t *testing.T) {
	if _, err := CategoryCSV(Result{}, Category("everything")); err == nil {
		t.Error("an unknown category should be an error")
	}
}

func TestCategoryFileName(t *testing.T) {
	if got := CatOnlyBooks.FileName(); got != "gstr2b-in-books-not-in-2b.csv" {
		t.Errorf("FileName = %q", got)
	}
}
