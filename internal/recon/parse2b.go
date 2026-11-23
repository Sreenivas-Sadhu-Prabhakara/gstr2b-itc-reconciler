package recon

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// maxFileBytes is the largest input this package will read. A year of B2B
// invoices for a small business is a few megabytes; the limit is here so a
// wrong file cannot exhaust memory.
const maxFileBytes = 64 << 20 // 64 MiB

// TwoBFile is what a parsed GSTR-2B download contains.
type TwoBFile struct {
	// GSTIN is the recipient's own GSTIN as stated in the file.
	GSTIN string `json:"gstin,omitempty"`
	// Period is the return period in the file, e.g. "092025".
	Period string `json:"period,omitempty"`
	// PeriodText is that period written out, e.g. "September 2025".
	PeriodText string    `json:"periodText,omitempty"`
	Invoices   []Invoice `json:"invoices"`
	Notes      []string  `json:"notes,omitempty"`
}

// flexString accepts a JSON string, number or boolean, so that an invoice
// number written as 1001 instead of "1001" still reads.
type flexString struct {
	s   string
	set bool
}

func (f *flexString) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		return nil
	}
	if s[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		f.s, f.set = strings.TrimSpace(str), true
		return nil
	}
	if s[0] == '{' || s[0] == '[' {
		return fmt.Errorf("expected a value, found %s", jsonKind(s))
	}
	f.s, f.set = s, true
	return nil
}

func (f flexString) String() string { return f.s }

// amount accepts a JSON number, a quoted number, or null, and holds whole
// paise. A field that is absent or null counts as zero; a field that holds
// something that is not a number is an error the user can act on.
type amount struct {
	paise int64
	set   bool
}

func (a *amount) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		return nil
	}
	if s[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		s = strings.TrimSpace(str)
		if s == "" {
			return nil
		}
	} else if s[0] == '{' || s[0] == '[' {
		return fmt.Errorf("expected an amount, found %s", jsonKind(s))
	}
	p, err := ParseAmount(s)
	if err != nil {
		return err
	}
	a.paise, a.set = p, true
	return nil
}

func jsonKind(s string) string {
	switch {
	case strings.HasPrefix(s, "{"):
		return "an object"
	case strings.HasPrefix(s, "["):
		return "a list"
	default:
		return "a value"
	}
}

type twoBSupplier struct {
	CTIN  flexString        `json:"ctin"`
	TrdNm flexString        `json:"trdnm"`
	Inv   []json.RawMessage `json:"inv"`
}

// twoBInvoiceID is read before the rest of an invoice, so that a problem
// anywhere in the invoice can be reported against its number.
type twoBInvoiceID struct {
	INum flexString `json:"inum"`
}

type twoBInvoice struct {
	INum flexString `json:"inum"`
	IDt  flexString `json:"idt"`
	Val  amount     `json:"val"`
	// Invoice-level heads: not in the portal's own shape, but some exports
	// flatten the item detail up to the invoice.
	TxVal amount        `json:"txval"`
	IAmt  amount        `json:"iamt"`
	CAmt  amount        `json:"camt"`
	SAmt  amount        `json:"samt"`
	CsAmt amount        `json:"csamt"`
	Itms  []twoBItemRow `json:"itms"`
}

type twoBItemRow struct {
	ItmDet twoBItemDet `json:"itm_det"`
	// Some exports drop the itm_det wrapper and put the heads on the row.
	TxVal amount `json:"txval"`
	IAmt  amount `json:"iamt"`
	CAmt  amount `json:"camt"`
	SAmt  amount `json:"samt"`
	CsAmt amount `json:"csamt"`
}

type twoBItemDet struct {
	TxVal amount `json:"txval"`
	IAmt  amount `json:"iamt"`
	CAmt  amount `json:"camt"`
	SAmt  amount `json:"samt"`
	CsAmt amount `json:"csamt"`
}

func (d twoBItemDet) any() bool {
	return d.TxVal.set || d.IAmt.set || d.CAmt.set || d.SAmt.set || d.CsAmt.set
}

func (r twoBItemRow) flattened() twoBItemDet {
	return twoBItemDet{TxVal: r.TxVal, IAmt: r.IAmt, CAmt: r.CAmt, SAmt: r.SAmt, CsAmt: r.CsAmt}
}

// ParseGSTR2B reads the GSTR-2B JSON download from the GST portal and returns
// its B2B invoices.
//
// It reads the portal's own shape — docdata.b2b is a list of suppliers, each
// with a ctin and an inv list, each invoice carrying inum, idt (dd-mm-yyyy),
// val and an itms list of itm_det holding iamt, camt, samt and csamt — and
// also the common wrapper in which that sits under a top-level "data" key.
// Key casing is ignored. Missing figures count as zero; a missing supplier
// GSTIN, invoice number or invoice date is reported as an error naming the
// invoice, because none of the three can be guessed.
func ParseGSTR2B(r io.Reader) (TwoBFile, error) {
	var out TwoBFile
	raw, err := readLimited(r)
	if err != nil {
		return out, err
	}
	top, err := objectOf(raw)
	if err != nil {
		return out, fmt.Errorf("could not read the GSTR-2B JSON: %w", err)
	}

	// The portal download wraps the statement in {"data": {...}}; some tools
	// hand over the inner object directly.
	body := top
	if _, ok := pick(top, "docdata"); !ok {
		if d, ok := pick(top, "data"); ok {
			if inner, err := objectOf(d); err == nil {
				body = inner
			}
		}
	}
	if g, ok := pick(body, "gstin"); ok {
		out.GSTIN = NormaliseGSTIN(textOf(g))
	}
	if p, ok := pick(body, "rtnprd"); ok {
		out.Period = strings.TrimSpace(textOf(p))
		out.PeriodText = PeriodText(out.Period)
	}

	var doc map[string]json.RawMessage
	switch docRaw, ok := pick(body, "docdata"); {
	case ok:
		if doc, err = objectOf(docRaw); err != nil {
			return out, fmt.Errorf("could not read docdata: %w", err)
		}
	default:
		// Last resort: a bare {"b2b": [...]} document.
		if _, hasB2B := pick(body, "b2b"); !hasB2B {
			return out, fmt.Errorf("this file has no docdata section — open the GSTR-2B JSON downloaded from the GST portal (the file whose name starts with GSTR2B_)")
		}
		doc = body
	}

	for _, section := range []struct{ key, label string }{
		{"b2ba", "amended invoices (b2ba)"},
		{"cdnr", "credit and debit notes (cdnr)"},
		{"cdnra", "amended credit and debit notes (cdnra)"},
		{"impg", "imports of goods (impg)"},
		{"impgsez", "imports from SEZ units (impgsez)"},
	} {
		if v, ok := pick(doc, section.key); ok {
			if n := countArray(v); n > 0 {
				out.Notes = append(out.Notes, fmt.Sprintf("This GSTR-2B also lists %s under %s, which this tool does not reconcile — check those by hand.", plural(n, "entry", "entries"), section.label))
			}
		}
	}

	b2bRaw, ok := pick(doc, "b2b")
	if !ok {
		out.Notes = append(out.Notes, "This GSTR-2B has no B2B section, so there are no supplier invoices in it to compare.")
		return out, nil
	}
	var suppliers []json.RawMessage
	if err := json.Unmarshal(b2bRaw, &suppliers); err != nil {
		return out, fmt.Errorf("docdata.b2b is not a list of suppliers: %w", err)
	}

	missingTaxable := 0
	for si, sRaw := range suppliers {
		var sup twoBSupplier
		if err := json.Unmarshal(sRaw, &sup); err != nil {
			return out, fmt.Errorf("supplier %d in docdata.b2b could not be read: %w", si+1, err)
		}
		gstin := NormaliseGSTIN(sup.CTIN.String())
		if gstin == "" {
			return out, fmt.Errorf("supplier %d in docdata.b2b has no GSTIN (ctin)", si+1)
		}
		for ii, iRaw := range sup.Inv {
			// Read the invoice number on its own first, so that a problem
			// further down the invoice can be reported against it.
			where := fmt.Sprintf("invoice %d in the list", ii+1)
			var id twoBInvoiceID
			if err := json.Unmarshal(iRaw, &id); err == nil {
				if label := strings.TrimSpace(id.INum.String()); label != "" {
					where = "invoice " + label
				}
			}
			var in twoBInvoice
			if err := json.Unmarshal(iRaw, &in); err != nil {
				return out, fmt.Errorf("supplier %s, %s: %w", gstin, where, err)
			}
			no := strings.TrimSpace(in.INum.String())
			if no == "" {
				return out, fmt.Errorf("supplier %s, %s has no invoice number (inum)", gstin, where)
			}
			dateText := strings.TrimSpace(in.IDt.String())
			if dateText == "" {
				return out, fmt.Errorf("supplier %s, invoice %s has no invoice date (idt)", gstin, no)
			}
			date, err := ParseInvoiceDate(dateText)
			if err != nil {
				return out, fmt.Errorf("supplier %s, invoice %s: %w", gstin, no, err)
			}

			inv := Invoice{
				SupplierGSTIN: gstin,
				SupplierName:  strings.TrimSpace(sup.TrdNm.String()),
				InvoiceNo:     no,
				InvoiceDate:   date,
				InvoiceValue:  in.Val.paise,
				Source:        Source2B,
			}
			stated := false
			for _, row := range in.Itms {
				d := row.ItmDet
				if !d.any() {
					d = row.flattened()
				}
				if !d.any() {
					continue
				}
				stated = true
				inv.TaxableValue += d.TxVal.paise
				inv.IGST += d.IAmt.paise
				inv.CGST += d.CAmt.paise
				inv.SGST += d.SAmt.paise
				inv.Cess += d.CsAmt.paise
			}
			if !stated {
				// No item detail at all: use whatever the invoice level states.
				inv.TaxableValue = in.TxVal.paise
				inv.IGST = in.IAmt.paise
				inv.CGST = in.CAmt.paise
				inv.SGST = in.SAmt.paise
				inv.Cess = in.CsAmt.paise
			}
			if inv.TaxableValue == 0 && inv.TotalTax() != 0 {
				missingTaxable++
			}
			out.Invoices = append(out.Invoices, inv)
		}
	}
	if missingTaxable > 0 {
		out.Notes = append(out.Notes, fmt.Sprintf("%s in the GSTR-2B state tax but no taxable value, so only the tax heads were compared for those.", plural(missingTaxable, "invoice", "invoices")))
	}
	return out, nil
}

// PeriodText turns the GSTR-2B return period "092025" into "September 2025".
// Anything else is returned unchanged, so an unexpected value is shown as-is
// rather than invented.
func PeriodText(p string) string {
	p = strings.TrimSpace(p)
	if len(p) != 6 {
		return p
	}
	for i := 0; i < 6; i++ {
		if p[i] < '0' || p[i] > '9' {
			return p
		}
	}
	m := int(p[0]-'0')*10 + int(p[1]-'0')
	if m < 1 || m > 12 {
		return p
	}
	return fmt.Sprintf("%s %s", time.Month(m).String(), p[2:])
}

// readLimited reads all of r, refusing a file too large to be a return.
func readLimited(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, fmt.Errorf("no file was given")
	}
	b, err := io.ReadAll(io.LimitReader(r, maxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("could not read the file: %w", err)
	}
	if len(b) > maxFileBytes {
		return nil, fmt.Errorf("the file is larger than %d MB, which is too large to be a GSTR-2B statement or a purchase register", maxFileBytes>>20)
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return nil, fmt.Errorf("the file is empty")
	}
	return b, nil
}

// objectOf decodes a JSON object into its raw members, with a message aimed at
// someone who has handed over the wrong file.
func objectOf(b []byte) (map[string]json.RawMessage, error) {
	t := strings.TrimLeft(string(b), "\ufeff \t\r\n")
	if t == "" {
		return nil, fmt.Errorf("the file is empty")
	}
	if t[0] != '{' {
		return nil, fmt.Errorf("expected a JSON object but the file starts with %q — is this the GSTR-2B JSON download?", firstRunes(t, 12))
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(t), &m); err != nil {
		return nil, fmt.Errorf("the file is not valid JSON: %w", err)
	}
	return m, nil
}

// pick looks a key up ignoring case, so docdata, docData and DOCDATA all read.
func pick(m map[string]json.RawMessage, key string) (json.RawMessage, bool) {
	if v, ok := m[key]; ok {
		return v, true
	}
	for k, v := range m {
		if strings.EqualFold(k, key) {
			return v, true
		}
	}
	return nil, false
}

func textOf(v json.RawMessage) string {
	var f flexString
	if err := f.UnmarshalJSON(v); err != nil {
		return ""
	}
	return f.String()
}

func countArray(v json.RawMessage) int {
	var a []json.RawMessage
	if err := json.Unmarshal(v, &a); err != nil {
		return 0
	}
	return len(a)
}

func firstRunes(s string, n int) string {
	i := 0
	for j := range s {
		if i == n {
			return s[:j]
		}
		i++
	}
	return s
}
