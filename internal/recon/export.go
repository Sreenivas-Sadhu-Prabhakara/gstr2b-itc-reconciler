package recon

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
)

// CategoryCSV renders one category of a result as a CSV file: one heading row
// and one row per invoice, with amounts as plain decimals so a spreadsheet or
// an accounting package reads them back as numbers.
func CategoryCSV(res Result, cat Category) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	write := func(row []string) {
		_ = w.Write(row)
	}

	invoiceHead := []string{"Supplier GSTIN", "Supplier name", "Invoice no", "Invoice date", "Invoice value", "Taxable value", "IGST", "CGST", "SGST", "Cess", "Total tax"}
	invoiceRow := func(i Invoice) []string {
		return []string{
			i.SupplierGSTIN, i.SupplierName, i.InvoiceNo, i.DateText(),
			Decimal(i.InvoiceValue), Decimal(i.TaxableValue),
			Decimal(i.IGST), Decimal(i.CGST), Decimal(i.SGST), Decimal(i.Cess),
			Decimal(i.TotalTax()),
		}
	}

	switch cat {
	case CatMatched:
		write(invoiceHead)
		for _, p := range res.Matched {
			write(invoiceRow(p.Books))
		}

	case CatValueMismatch:
		write([]string{
			"Supplier GSTIN", "Supplier name", "Invoice no", "Invoice date",
			"Taxable value (GSTR-2B)", "Taxable value (books)",
			"IGST (GSTR-2B)", "IGST (books)",
			"CGST (GSTR-2B)", "CGST (books)",
			"SGST (GSTR-2B)", "SGST (books)",
			"Cess (GSTR-2B)", "Cess (books)",
			"Total tax (GSTR-2B)", "Total tax (books)",
			"Tax difference (books - GSTR-2B)", "Heads outside tolerance",
		})
		for _, m := range res.MatchedWithValueMismatch {
			heads := make([]string, 0, len(m.Deltas))
			for _, d := range m.Deltas {
				heads = append(heads, d.Head)
			}
			write([]string{
				m.Books.SupplierGSTIN, m.Books.SupplierName, m.Books.InvoiceNo, m.Books.DateText(),
				Decimal(m.TwoB.TaxableValue), Decimal(m.Books.TaxableValue),
				Decimal(m.TwoB.IGST), Decimal(m.Books.IGST),
				Decimal(m.TwoB.CGST), Decimal(m.Books.CGST),
				Decimal(m.TwoB.SGST), Decimal(m.Books.SGST),
				Decimal(m.TwoB.Cess), Decimal(m.Books.Cess),
				Decimal(m.TwoB.TotalTax()), Decimal(m.Books.TotalTax()),
				Decimal(m.TaxDelta), strings.Join(heads, "; "),
			})
		}

	case CatOnly2B:
		write(invoiceHead)
		for _, i := range res.In2BNotInBooks {
			write(invoiceRow(i))
		}

	case CatOnlyBooks:
		write(append(append([]string{}, invoiceHead...), "ITC at risk", "Line in register"))
		for _, i := range res.InBooksNotIn2B {
			write(append(invoiceRow(i), Decimal(i.TotalTax()), strconv.Itoa(i.Line)))
		}

	case CatDuplicates:
		write([]string{"Supplier GSTIN", "Supplier name", "Invoice no", "Invoice date", "Copy", "Copies in register", "Line in register", "Taxable value", "Total tax", "Tax on the extra copies"})
		for _, g := range res.DuplicateInBooks {
			for n, i := range g.Invoices {
				extra := ""
				if n == 0 {
					extra = Decimal(g.ExtraTax)
				}
				write([]string{
					i.SupplierGSTIN, i.SupplierName, i.InvoiceNo, i.DateText(),
					strconv.Itoa(n + 1), strconv.Itoa(len(g.Invoices)), strconv.Itoa(i.Line),
					Decimal(i.TaxableValue), Decimal(i.TotalTax()), extra,
				})
			}
		}

	case CatSuppliers:
		write([]string{"Supplier GSTIN", "Supplier name", "Invoices missing from GSTR-2B", "ITC at risk", "Invoice numbers"})
		for _, s := range res.Suppliers {
			nos := make([]string, 0, len(s.Missing))
			for _, i := range s.Missing {
				nos = append(nos, i.InvoiceNo)
			}
			write([]string{s.GSTIN, s.Name, strconv.Itoa(len(s.Missing)), Decimal(s.TaxAtRisk), strings.Join(nos, "; ")})
		}

	default:
		return nil, fmt.Errorf("unknown category %q", string(cat))
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// FileName is the suggested download name for a category's CSV.
func (c Category) FileName() string {
	return "gstr2b-" + string(c) + ".csv"
}
