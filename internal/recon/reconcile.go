package recon

import (
	"fmt"
	"sort"
)

// DefaultTolerancePaise is how far a tax head may differ between GSTR-2B and
// the purchase register before the invoice is reported as a value mismatch:
// ₹1.00 per head, which absorbs ordinary rounding on an invoice line without
// hiding a real difference. The user can change it per run.
const DefaultTolerancePaise int64 = 100

// Options configures one reconciliation run.
type Options struct {
	// TolerancePaise is the allowance per tax head. Zero demands an exact
	// match to the paisa; a negative value is treated as zero.
	TolerancePaise int64
}

// DefaultOptions returns the options used when the user has not chosen any.
func DefaultOptions() Options { return Options{TolerancePaise: DefaultTolerancePaise} }

func (o Options) tolerance() int64 {
	if o.TolerancePaise < 0 {
		return 0
	}
	return o.TolerancePaise
}

// Category names the five outcomes of the comparison. They are also the
// identifiers used for CSV export.
type Category string

const (
	// CatMatched: present on both sides, every compared head within tolerance.
	CatMatched Category = "matched"
	// CatValueMismatch: present on both sides, but at least one head differs.
	CatValueMismatch Category = "value-mismatch"
	// CatOnly2B: in GSTR-2B but not in the purchase register — an unrecorded
	// purchase, or credit the supplier reported against the wrong buyer.
	CatOnly2B Category = "in-2b-not-in-books"
	// CatOnlyBooks: in the purchase register but not in GSTR-2B — the input
	// tax credit at risk.
	CatOnlyBooks Category = "in-books-not-in-2b"
	// CatDuplicates: the same invoice recorded more than once in the register.
	CatDuplicates Category = "duplicate-in-books"
	// CatSuppliers: the supplier-wise follow-up rollup.
	CatSuppliers Category = "suppliers"
)

// Title is the human name of a category.
func (c Category) Title() string {
	switch c {
	case CatMatched:
		return "Matched"
	case CatValueMismatch:
		return "Matched with value mismatch"
	case CatOnly2B:
		return "In GSTR-2B, not in books"
	case CatOnlyBooks:
		return "In books, not in GSTR-2B"
	case CatDuplicates:
		return "Duplicate in books"
	case CatSuppliers:
		return "Supplier follow-up"
	}
	return string(c)
}

// Pair is one invoice seen from both sides.
type Pair struct {
	Key   MatchKey `json:"key"`
	TwoB  Invoice  `json:"twoB"`
	Books Invoice  `json:"books"`
}

// HeadDelta is the difference in one tax head, from the books' point of view:
// a positive delta means the register claims more than GSTR-2B allows.
type HeadDelta struct {
	Head  string `json:"head"`
	TwoB  int64  `json:"twoB"`
	Books int64  `json:"books"`
	Delta int64  `json:"delta"`
}

// Mismatch is an invoice that exists on both sides with different figures.
type Mismatch struct {
	Key   MatchKey `json:"key"`
	TwoB  Invoice  `json:"twoB"`
	Books Invoice  `json:"books"`
	// Deltas holds only the heads that fall outside the tolerance.
	Deltas []HeadDelta `json:"deltas"`
	// TaxDelta is books tax minus GSTR-2B tax over all four tax heads. When it
	// is positive, that much of the claim is not supported by GSTR-2B.
	TaxDelta int64 `json:"taxDelta"`
}

// DuplicateGroup is one invoice recorded more than once in the register.
type DuplicateGroup struct {
	Key      MatchKey  `json:"key"`
	Invoices []Invoice `json:"invoices"`
	// ExtraTax is the tax on the second and later copies: the amount that
	// would be over-claimed if every copy were posted.
	ExtraTax int64 `json:"extraTax"`
}

// SupplierFollowUp is the per-supplier rollup of invoices missing from GSTR-2B.
type SupplierFollowUp struct {
	GSTIN     string    `json:"gstin"`
	Name      string    `json:"name,omitempty"`
	Missing   []Invoice `json:"missing"`
	TaxAtRisk int64     `json:"taxAtRisk"`
}

// CategoryTotal is the count and tax value of one category.
type CategoryTotal struct {
	Count int   `json:"count"`
	Tax   int64 `json:"tax"`
}

// Summary is the headline counts and totals of a run.
type Summary struct {
	TwoBInvoices  int           `json:"twoBInvoices"`
	BooksInvoices int           `json:"booksInvoices"`
	TwoBTax       int64         `json:"twoBTax"`
	BooksTax      int64         `json:"booksTax"`
	Matched       CategoryTotal `json:"matched"`
	ValueMismatch CategoryTotal `json:"valueMismatch"`
	Only2B        CategoryTotal `json:"only2B"`
	OnlyBooks     CategoryTotal `json:"onlyBooks"`
	Duplicates    CategoryTotal `json:"duplicates"`
}

// Result is the outcome of one reconciliation.
type Result struct {
	Matched                  []Pair             `json:"matched"`
	MatchedWithValueMismatch []Mismatch         `json:"matchedWithValueMismatch"`
	In2BNotInBooks           []Invoice          `json:"in2BNotInBooks"`
	InBooksNotIn2B           []Invoice          `json:"inBooksNotIn2B"`
	DuplicateInBooks         []DuplicateGroup   `json:"duplicateInBooks"`
	Suppliers                []SupplierFollowUp `json:"suppliers"`
	// TotalAtRisk is the input tax credit on invoices that are in the books
	// but not in GSTR-2B.
	TotalAtRisk    int64    `json:"totalAtRisk"`
	Summary        Summary  `json:"summary"`
	TolerancePaise int64    `json:"tolerancePaise"`
	Notes          []string `json:"notes,omitempty"`
}

// comparedHeads are the figures checked on an invoice that appears on both
// sides. The four tax heads decide the credit; the taxable value is compared
// too, because a taxable value that disagrees usually means the invoice was
// posted wrongly even when the tax happens to tie.
var comparedHeads = []struct {
	Name string
	Get  func(Invoice) int64
}{
	{"Taxable value", func(i Invoice) int64 { return i.TaxableValue }},
	{"IGST", func(i Invoice) int64 { return i.IGST }},
	{"CGST", func(i Invoice) int64 { return i.CGST }},
	{"SGST", func(i Invoice) int64 { return i.SGST }},
	{"Cess", func(i Invoice) int64 { return i.Cess }},
}

// Reconcile compares the GSTR-2B statement against the purchase register,
// invoice by invoice, and sorts every invoice into exactly one of the five
// categories.
//
// Input tax credit may only be taken on an invoice that appears in GSTR-2B, so
// the invoices in the register that are absent from GSTR-2B are the ones whose
// credit is at risk; they are totalled in TotalAtRisk and rolled up by supplier
// for follow-up.
func Reconcile(twoB, books []Invoice, opts Options) Result {
	tol := opts.tolerance()
	res := Result{TolerancePaise: tol}

	// Index GSTR-2B by match key, keeping the first record for each key.
	type indexed struct {
		inv  Invoice
		used bool
	}
	twoBIdx := make(map[string]*indexed, len(twoB))
	twoBOrder := make([]string, 0, len(twoB))
	dupIn2B := 0
	for _, inv := range twoB {
		k := inv.Key().String()
		if _, seen := twoBIdx[k]; seen {
			dupIn2B++
			continue
		}
		twoBIdx[k] = &indexed{inv: inv}
		twoBOrder = append(twoBOrder, k)
		res.Summary.TwoBInvoices++
		res.Summary.TwoBTax += inv.TotalTax()
	}
	if dupIn2B > 0 {
		res.Notes = append(res.Notes, fmt.Sprintf("GSTR-2B contained %s with a repeated supplier and invoice number; the first of each was used.", plural(dupIn2B, "row", "rows")))
	}

	// Group the register by match key, keeping input order within a key.
	booksIdx := make(map[string][]Invoice, len(books))
	booksOrder := make([]string, 0, len(books))
	for _, inv := range books {
		k := inv.Key().String()
		if _, seen := booksIdx[k]; !seen {
			booksOrder = append(booksOrder, k)
		}
		booksIdx[k] = append(booksIdx[k], inv)
		res.Summary.BooksInvoices++
		res.Summary.BooksTax += inv.TotalTax()
	}

	for _, k := range booksOrder {
		copies := booksIdx[k]
		first := copies[0]
		if len(copies) > 1 {
			var extra int64
			for _, c := range copies[1:] {
				extra += c.TotalTax()
			}
			res.DuplicateInBooks = append(res.DuplicateInBooks, DuplicateGroup{
				Key:      first.Key(),
				Invoices: copies,
				ExtraTax: extra,
			})
		}
		// The first copy carries the comparison; the duplicates are reported
		// separately so the same invoice is never counted twice.
		if entry, ok := twoBIdx[k]; ok {
			entry.used = true
			deltas := compareHeads(entry.inv, first, tol)
			if len(deltas) == 0 {
				res.Matched = append(res.Matched, Pair{Key: first.Key(), TwoB: entry.inv, Books: first})
			} else {
				res.MatchedWithValueMismatch = append(res.MatchedWithValueMismatch, Mismatch{
					Key:      first.Key(),
					TwoB:     entry.inv,
					Books:    first,
					Deltas:   deltas,
					TaxDelta: first.TotalTax() - entry.inv.TotalTax(),
				})
			}
			continue
		}
		res.InBooksNotIn2B = append(res.InBooksNotIn2B, first)
	}

	for _, k := range twoBOrder {
		if entry := twoBIdx[k]; !entry.used {
			res.In2BNotInBooks = append(res.In2BNotInBooks, entry.inv)
		}
	}

	sortInvoices(res.In2BNotInBooks)
	sortInvoices(res.InBooksNotIn2B)
	sort.SliceStable(res.Matched, func(a, b int) bool { return lessInvoice(res.Matched[a].Books, res.Matched[b].Books) })
	sort.SliceStable(res.MatchedWithValueMismatch, func(a, b int) bool {
		x, y := res.MatchedWithValueMismatch[a], res.MatchedWithValueMismatch[b]
		if x.TaxDelta != y.TaxDelta {
			return abs64(x.TaxDelta) > abs64(y.TaxDelta) // biggest difference first
		}
		return lessInvoice(x.Books, y.Books)
	})
	sort.SliceStable(res.DuplicateInBooks, func(a, b int) bool {
		x, y := res.DuplicateInBooks[a], res.DuplicateInBooks[b]
		if x.ExtraTax != y.ExtraTax {
			return x.ExtraTax > y.ExtraTax
		}
		return x.Key.String() < y.Key.String()
	})

	for _, inv := range res.InBooksNotIn2B {
		res.TotalAtRisk += inv.TotalTax()
	}
	res.Suppliers = rollupSuppliers(res.InBooksNotIn2B)

	res.Summary.Matched = total(invoicesOfPairs(res.Matched))
	res.Summary.ValueMismatch = CategoryTotal{Count: len(res.MatchedWithValueMismatch)}
	for _, m := range res.MatchedWithValueMismatch {
		res.Summary.ValueMismatch.Tax += m.Books.TotalTax()
	}
	res.Summary.Only2B = total(res.In2BNotInBooks)
	res.Summary.OnlyBooks = total(res.InBooksNotIn2B)
	res.Summary.Duplicates = CategoryTotal{Count: len(res.DuplicateInBooks)}
	for _, d := range res.DuplicateInBooks {
		res.Summary.Duplicates.Tax += d.ExtraTax
	}
	return res
}

// compareHeads returns the heads that differ by more than the tolerance.
func compareHeads(twoB, books Invoice, tol int64) []HeadDelta {
	var out []HeadDelta
	for _, h := range comparedHeads {
		a, b := h.Get(twoB), h.Get(books)
		d := b - a
		if abs64(d) > tol {
			out = append(out, HeadDelta{Head: h.Name, TwoB: a, Books: b, Delta: d})
		}
	}
	return out
}

// rollupSuppliers groups the invoices missing from GSTR-2B by supplier and
// sorts the suppliers by the tax at stake, largest first.
func rollupSuppliers(missing []Invoice) []SupplierFollowUp {
	idx := map[string]*SupplierFollowUp{}
	var order []string
	for _, inv := range missing {
		g := NormaliseGSTIN(inv.SupplierGSTIN)
		s, ok := idx[g]
		if !ok {
			s = &SupplierFollowUp{GSTIN: g, Name: inv.SupplierName}
			idx[g] = s
			order = append(order, g)
		}
		if s.Name == "" {
			s.Name = inv.SupplierName
		}
		s.Missing = append(s.Missing, inv)
		s.TaxAtRisk += inv.TotalTax()
	}
	out := make([]SupplierFollowUp, 0, len(order))
	for _, g := range order {
		s := idx[g]
		sortInvoices(s.Missing)
		out = append(out, *s)
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].TaxAtRisk != out[b].TaxAtRisk {
			return out[a].TaxAtRisk > out[b].TaxAtRisk
		}
		return out[a].GSTIN < out[b].GSTIN
	})
	return out
}

func invoicesOfPairs(pairs []Pair) []Invoice {
	out := make([]Invoice, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, p.Books)
	}
	return out
}

func total(invs []Invoice) CategoryTotal {
	t := CategoryTotal{Count: len(invs)}
	for _, i := range invs {
		t.Tax += i.TotalTax()
	}
	return t
}

func sortInvoices(invs []Invoice) {
	sort.SliceStable(invs, func(a, b int) bool { return lessInvoice(invs[a], invs[b]) })
}

func lessInvoice(a, b Invoice) bool {
	ka, kb := a.Key(), b.Key()
	if ka.GSTIN != kb.GSTIN {
		return ka.GSTIN < kb.GSTIN
	}
	if !a.InvoiceDate.Equal(b.InvoiceDate) {
		return a.InvoiceDate.Before(b.InvoiceDate)
	}
	return ka.InvoiceNo < kb.InvoiceNo
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
