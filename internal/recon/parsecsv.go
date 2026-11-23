package recon

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Register is a parsed purchase register.
type Register struct {
	Invoices []Invoice `json:"invoices"`
	Notes    []string  `json:"notes,omitempty"`
}

// column identifiers inside the purchase register.
const (
	colGSTIN   = "gstin"
	colName    = "name"
	colInvNo   = "invno"
	colDate    = "date"
	colTaxable = "taxable"
	colIGST    = "igst"
	colCGST    = "cgst"
	colSGST    = "sgst"
	colCess    = "cess"
	colValue   = "value"
)

// columnAliases lists the headings a purchase register exported from a
// spreadsheet or an accounting package actually uses. Headings are compared
// with case, spaces, punctuation and currency marks removed, and a heading
// that merely starts with an alias also matches, so "Invoice date (dd-mm-yyyy)"
// and "IGST Amount (₹)" are understood.
var columnAliases = map[string][]string{
	colGSTIN: {"gstin", "gstinuin", "gstno", "gstinno", "gstinofsupplier", "suppliergstin", "suppliergst", "suppliergstinuin", "vendorgstin", "partygstin", "ctin", "gstinofthesupplier"},
	colName:  {"suppliername", "supplier", "partyname", "party", "vendorname", "vendor", "tradename", "trdnm", "supplierpartyname", "nameofsupplier"},
	colInvNo: {"invoiceno", "invoicenumber", "invno", "inum", "billno", "billnumber", "documentno", "docno", "voucherno", "supplierinvoiceno", "invoiceid", "invoicenum"},
	colDate:  {"invoicedate", "invdate", "billdate", "documentdate", "docdate", "voucherdate", "dateofinvoice", "idt", "date"},
	colTaxable: {"taxablevalue", "taxableamount", "taxableamt", "taxable", "txval", "assessablevalue", "basevalue", "netvalue",
		"taxablevalueofsupply"},
	colIGST:  {"igstamount", "igstamt", "igstpaid", "igsttax", "igst", "iamt"},
	colCGST:  {"cgstamount", "cgstamt", "cgsttax", "cgst", "camt"},
	colSGST:  {"sgstutgstamount", "sgstutgst", "sgstamount", "sgstamt", "sgsttax", "utgst", "sgst", "samt"},
	colCess:  {"cessamount", "cessamt", "cess", "csamt"},
	colValue: {"invoicevalue", "totalinvoicevalue", "invoiceamount", "billamount", "totalvalue", "totalamount", "grandtotal", "val", "value", "total"},
}

// amountColumns are the columns read as money.
var amountColumns = []string{colTaxable, colIGST, colCGST, colSGST, colCess, colValue}

// colTitles are the names used when talking to the user about a column.
var colTitles = map[string]string{
	colGSTIN:   "Supplier GSTIN",
	colName:    "Supplier name",
	colInvNo:   "Invoice no",
	colDate:    "Invoice date",
	colTaxable: "Taxable value",
	colIGST:    "IGST",
	colCGST:    "CGST",
	colSGST:    "SGST",
	colCess:    "Cess",
	colValue:   "Invoice value",
}

// ParsePurchaseCSV reads the business's own purchase register.
//
// It finds the heading row itself (title rows above it are skipped), accepts
// comma, semicolon or tab separated files, understands the usual spellings of
// each heading, and treats blank, "-" and "0" cells as zero. A row without a
// supplier GSTIN, invoice number or invoice date is reported as an error naming
// the line, because an invoice cannot be matched without all three.
func ParsePurchaseCSV(r io.Reader) (Register, error) {
	var reg Register
	raw, err := readLimited(r)
	if err != nil {
		return reg, err
	}
	raw = bytes.TrimPrefix(raw, []byte("\ufeff"))

	rd := csv.NewReader(bytes.NewReader(raw))
	rd.Comma = detectDelimiter(raw)
	rd.FieldsPerRecord = -1
	rd.LazyQuotes = true
	rd.TrimLeadingSpace = true
	rd.ReuseRecord = false

	type row struct {
		line  int
		cells []string
	}
	var rows []row
	for {
		rec, err := rd.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var pe *csv.ParseError
			if errors.As(err, &pe) {
				return reg, fmt.Errorf("line %d of the purchase register could not be read: %s — check the quotation marks on that line", pe.Line, pe.Err)
			}
			return reg, fmt.Errorf("could not read the purchase register: %w", err)
		}
		if len(rec) == 0 {
			continue
		}
		line, _ := rd.FieldPos(0)
		rows = append(rows, row{line: line, cells: rec})
	}
	if len(rows) == 0 {
		return reg, fmt.Errorf("the purchase register has no rows")
	}

	// Find the heading row. Registers often carry a title or a date range
	// above the headings, so scan the first rows rather than assuming row 1.
	headerAt, cols := -1, map[string]int(nil)
	for i, rw := range rows {
		if i >= 25 {
			break
		}
		m := mapColumns(rw.cells)
		if _, hasG := m[colGSTIN]; hasG {
			if _, hasI := m[colInvNo]; hasI {
				headerAt, cols = i, m
				break
			}
		}
	}
	if headerAt < 0 {
		return reg, fmt.Errorf("could not find the heading row in the purchase register — it needs a column for the supplier GSTIN and one for the invoice number, for example: Supplier GSTIN, Invoice no, Invoice date, Taxable value, IGST, CGST, SGST, Cess")
	}
	if _, ok := cols[colDate]; !ok {
		return reg, fmt.Errorf("the purchase register has no invoice date column — add one headed \"Invoice date\" with dates written as dd-mm-yyyy; the date is needed because invoice numbers restart every financial year")
	}
	var missingHeads []string
	for _, c := range []string{colTaxable, colIGST, colCGST, colSGST, colCess} {
		if _, ok := cols[c]; !ok {
			missingHeads = append(missingHeads, colTitles[c])
		}
	}
	if len(missingHeads) > 0 {
		tail := "so that figure was read as zero"
		if len(missingHeads) > 1 {
			tail = "so those figures were read as zero"
		}
		reg.Notes = append(reg.Notes, fmt.Sprintf("The purchase register has no column for %s, %s.", strings.Join(missingHeads, ", "), tail))
	}

	cell := func(cells []string, col string) string {
		i, ok := cols[col]
		if !ok || i >= len(cells) {
			return ""
		}
		return strings.TrimSpace(cells[i])
	}

	skippedBlank, skippedTotal, badGSTINs := 0, 0, []int(nil)
	for _, rw := range rows[headerAt+1:] {
		if allBlank(rw.cells) {
			continue
		}
		gstinCell := cell(rw.cells, colGSTIN)
		gstin := NormaliseGSTIN(gstinCell)
		no := cell(rw.cells, colInvNo)
		// The summary line at the foot of a register is not an invoice.
		if isTotalLabel(gstinCell) || isTotalLabel(no) ||
			(gstin == "" && isTotalLabel(firstNonBlank(rw.cells))) {
			skippedTotal++
			continue
		}
		if gstin == "" && no == "" {
			skippedBlank++
			continue
		}
		if gstin == "" {
			return reg, fmt.Errorf("line %d: invoice %q has no supplier GSTIN — the supplier's GSTIN is what links the invoice to GSTR-2B", rw.line, no)
		}
		if no == "" {
			return reg, fmt.Errorf("line %d: the row for supplier %s has no invoice number", rw.line, gstin)
		}
		dateText := cell(rw.cells, colDate)
		if dateText == "" {
			return reg, fmt.Errorf("line %d: invoice %s of supplier %s has no invoice date", rw.line, no, gstin)
		}
		date, err := ParseInvoiceDate(dateText)
		if err != nil {
			return reg, fmt.Errorf("line %d: invoice %s of supplier %s: %w", rw.line, no, gstin, err)
		}

		amounts := map[string]int64{}
		for _, c := range amountColumns {
			v, err := ParseAmount(cell(rw.cells, c))
			if err != nil {
				return reg, fmt.Errorf("line %d: invoice %s of supplier %s: column %s: %w", rw.line, no, gstin, colTitles[c], err)
			}
			amounts[c] = v
		}
		if !ValidGSTINFormat(gstin) {
			badGSTINs = append(badGSTINs, rw.line)
		}
		reg.Invoices = append(reg.Invoices, Invoice{
			SupplierGSTIN: gstin,
			SupplierName:  cell(rw.cells, colName),
			InvoiceNo:     no,
			InvoiceDate:   date,
			TaxableValue:  amounts[colTaxable],
			IGST:          amounts[colIGST],
			CGST:          amounts[colCGST],
			SGST:          amounts[colSGST],
			Cess:          amounts[colCess],
			InvoiceValue:  amounts[colValue],
			Source:        SourceBooks,
			Line:          rw.line,
		})
	}

	if len(reg.Invoices) == 0 {
		return reg, fmt.Errorf("the purchase register has a heading row but no invoice rows under it")
	}
	if skippedTotal > 0 {
		reg.Notes = append(reg.Notes, fmt.Sprintf("Skipped %s that looked like a total rather than an invoice.", plural(skippedTotal, "row", "rows")))
	}
	if skippedBlank > 0 {
		reg.Notes = append(reg.Notes, fmt.Sprintf("Skipped %s with no GSTIN and no invoice number.", plural(skippedBlank, "row", "rows")))
	}
	if len(badGSTINs) > 0 {
		reg.Notes = append(reg.Notes, fmt.Sprintf("%s a supplier GSTIN that is not in the standard 15-character format (%s). Check these first: a mistyped GSTIN makes an invoice look missing from GSTR-2B.",
			plural(len(badGSTINs), "line has", "lines have"), linesList(badGSTINs)))
	}
	return reg, nil
}

// mapColumns works out which column holds which field.
func mapColumns(cells []string) map[string]int {
	out := map[string]int{}
	type aliasRef struct {
		col, alias string
	}
	var byLength []aliasRef
	for col, aliases := range columnAliases {
		for _, a := range aliases {
			byLength = append(byLength, aliasRef{col: col, alias: a})
		}
	}
	sort.Slice(byLength, func(i, j int) bool {
		if len(byLength[i].alias) != len(byLength[j].alias) {
			return len(byLength[i].alias) > len(byLength[j].alias)
		}
		return byLength[i].alias < byLength[j].alias
	})

	for i, c := range cells {
		h := normaliseHeading(c)
		if h == "" {
			continue
		}
		// A rate or percentage column is never an amount column.
		if strings.Contains(h, "rate") || strings.Contains(h, "percent") {
			continue
		}
		for _, ref := range byLength {
			if h != ref.alias && !strings.HasPrefix(h, ref.alias) {
				continue
			}
			if _, taken := out[ref.col]; !taken {
				out[ref.col] = i
			}
			break
		}
	}
	return out
}

// normaliseHeading reduces a heading to lower-case letters and digits.
func normaliseHeading(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// detectDelimiter picks the separator from the first non-empty line, so a
// semicolon or tab separated export also reads.
func detectDelimiter(raw []byte) rune {
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		commas := strings.Count(line, ",")
		semis := strings.Count(line, ";")
		tabs := strings.Count(line, "\t")
		switch {
		case semis > commas && semis >= tabs:
			return ';'
		case tabs > commas && tabs > semis:
			return '\t'
		default:
			return ','
		}
	}
	return ','
}

func allBlank(cells []string) bool {
	for _, c := range cells {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

func firstNonBlank(cells []string) string {
	for _, c := range cells {
		if t := strings.TrimSpace(c); t != "" {
			return t
		}
	}
	return ""
}

// isTotalLabel spots the summary line at the foot of a register.
func isTotalLabel(s string) bool {
	t := normaliseHeading(s)
	switch t {
	case "total", "totals", "grandtotal", "subtotal", "sumtotal", "granttotal":
		return true
	}
	return false
}

func linesList(lines []int) string {
	const show = 8
	parts := make([]string, 0, show+1)
	for i, l := range lines {
		if i == show {
			parts = append(parts, fmt.Sprintf("and %d more", len(lines)-show))
			break
		}
		parts = append(parts, fmt.Sprintf("line %d", l))
	}
	return strings.Join(parts, ", ")
}
