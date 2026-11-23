# GSTR-2B vs Purchase Register ITC Reconciler

A single-binary tool that matches the GST portal's **GSTR-2B** against your own **purchase register**, invoice by invoice, and tells you how much input tax credit you cannot yet claim.

It serves a small web interface on `127.0.0.1`, keeps its data in one JSON file on the same machine, and makes no network calls of any kind. Nothing is uploaded, and nothing is sent to any supplier.

## What it does

Give it the GSTR-2B **JSON** downloaded from the portal and a **CSV** of your purchase register. Every invoice lands in exactly one of five categories:

| Category | Meaning |
| --- | --- |
| **In books, not in GSTR-2B** | The credit at risk — your supplier has not reported the invoice |
| **In GSTR-2B, not in books** | A purchase you have not recorded, or one not yours |
| **Matched, values differ** | Same invoice, different figures, broken down head by head |
| **Duplicate in books** | The same invoice entered twice, which would over-claim credit |
| **Matched** | Present on both sides and agreeing |

It then:

- totals the tax on everything in the first category as the **ITC at risk**;
- rolls that up per supplier, largest amount first;
- exports any category as a CSV with plain decimal amounts;
- writes a plain-text follow-up message per supplier listing their missing invoices — which **you** send, from your own email or messaging app. The tool has no way to send anything.

### How invoices are matched

An invoice is the same invoice on both sides when the **supplier's GSTIN**, the **invoice number** and the **financial year** of the invoice date agree.

Invoice numbers are first reduced to a common form: upper-cased, with spaces and the separators `/ - \ .` removed, and the leading zeros of each run of digits dropped. So `inv-001/23`, `INV1/23` and `Inv 001 / 23` are all read as `INV123`. Leading zeros are dropped run by run, before the separators go, so two genuinely different numbers do not collapse into one: `INV-10/023` gives `INV1023` while `INV-100/23` gives `INV10023`.

The financial year is part of the key because invoice numbering restarts every 1 April — `INV-1` of 2025-26 is a different document from `INV-1` of 2024-25.

For invoices found on both sides, the taxable value and each of IGST, CGST, SGST and cess are compared separately, with a tolerance of **₹1.00 per tax head** by default to absorb line-level rounding. The tolerance is a single named constant and can be changed for each run; set it to zero to demand the exact paisa.

## Who it's for

Small businesses in India that claim input tax credit, and the bookkeepers and accountants who do their monthly GST work. Beyond a handful of purchase invoices the comparison is too long to do by eye and too important to skip.

## The rule it implements

- **Section 16(2)(aa), CGST Act, 2017** — input tax credit on an invoice or debit note may be taken only once the supplier has furnished the document in their statement of outward supplies and the details have been *communicated to the recipient*. GSTR-2B is that communication.
- **Rule 36(4), CGST Rules, 2017** — no credit shall be availed in respect of invoices or debit notes whose details the supplier has not furnished. The earlier provisional-credit cushion for unreported invoices no longer applies, so your claim is capped by what GSTR-2B shows.
- **GSTR-2B** — the static, auto-drafted ITC statement the portal generates for each return period.
- **Section 16(4), CGST Act, 2017** — sets the last date for taking credit on an invoice, which is why a missing invoice is worth chasing now rather than later.

Hence the arithmetic: anything in your books but missing from GSTR-2B is cash at risk. Statutory provisions are cited as they stood when this was written; confirm the current position with the GST portal or a qualified practitioner.

## Install & run

One Go program, standard library only — nothing to install, nothing to configure.

```sh
go build -o gstr2b-itc-reconciler .
./gstr2b-itc-reconciler
```

Or without building:

```sh
go run .
```

Then open `http://127.0.0.1:8080/`. Two flags change the defaults:

```sh
./gstr2b-itc-reconciler -addr 127.0.0.1:9000 -data ~/gst/reconciler-data.json
```

### The purchase register CSV

Headings are read flexibly — the usual spellings used by spreadsheets and accounting packages are understood, title rows above the headings are skipped, a total row at the foot is ignored, and comma, semicolon or tab separated files all read. This works:

```csv
Supplier GSTIN,Supplier name,Invoice no,Invoice date,Taxable value,IGST,CGST,SGST,Cess
27AAPFU0939F1ZV,Sundar Metals,INV/001/25-26,15-07-2025,100000.00,18000.00,0,0,0
29AABCU9603R1ZM,Nandi Hardware,NH-0045,02-08-2025,20000.00,0,1800.00,1800.00,0
```

A supplier GSTIN, an invoice number and an invoice date are needed on every row, because an invoice cannot be matched without all three. Every other figure may be blank and counts as zero. Amounts may carry a `₹`, `Rs` or `INR` marker and Indian or Western digit grouping.

## Where data is stored

One JSON file on your own computer — `./gstr2b-itc-reconciler-data.json` unless `-data` says otherwise — holding your settings and the last 25 comparisons so you can reopen them. It is written atomically (temporary file, then rename) so an interrupted write cannot corrupt it, and it is created readable only by you. Delete that file and the tool knows nothing.

The tool binds to `127.0.0.1`, makes no outbound network calls, has no accounts, analytics or telemetry, and loads no fonts, scripts or styles from anywhere but itself.

## Scope & accuracy

Only the **B2B section** of GSTR-2B (`docdata.b2b`) is reconciled. Amendments, credit and debit notes, imports and ISD credit are reported as present in the file but not compared — check those by hand.

This tool compares two files you give it. It does not decide eligibility: a credit also depends on receipt of the goods or services, the supplier having paid the tax, the time limit for claiming, blocked credits and reversal rules. It is not tax, accounting or legal advice. Check the figures against your own invoices and confirm the law with the GST portal or a qualified practitioner before filing GSTR-3B.

## Tests

The matching rules live in `internal/recon` as pure functions with table-driven tests, including the invoice-number normalisation, the 31 March / 1 April financial-year boundary, leap-year dates, rounding to the paisa, the tolerance boundary, both parsers against fixtures, and malformed input.

```sh
gofmt -l .
go vet ./...
go test ./... -count=1
go build -o gstr2b-itc-reconciler .
```

MIT licensed.
