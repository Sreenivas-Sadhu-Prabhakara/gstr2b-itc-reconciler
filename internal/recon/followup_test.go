package recon

import (
	"strings"
	"testing"
	"time"
)

func TestFollowUpMessage(t *testing.T) {
	d := day(2025, time.July, 15)
	books := []Invoice{
		inv(SourceBooks, "27AAPFU0939F1ZV", "INV/003/25-26", d, 80000, 14400, 0, 0, 0),
		inv(SourceBooks, "27AAPFU0939F1ZV", "INV/004/25-26", day(2025, time.July, 28), 100000, 18000, 0, 0, 0),
	}
	for i := range books {
		books[i].SupplierName = "Sundar Metals"
	}
	res := Reconcile(nil, books, DefaultOptions())
	msg := FollowUpMessage(res.Suppliers[0], MessageContext{
		BusinessName: "Nagarjuna Fabricators",
		ContactLine:  "accounts@example.in",
		PeriodText:   "September 2025",
	})

	for _, want := range []string{
		"Sundar Metals",
		"27AAPFU0939F1ZV",
		"INV/003/25-26",
		"INV/004/25-26",
		"15-07-2025",
		"28-07-2025",
		"32,400.00", // the tax at risk, grouped the Indian way
		"September 2025",
		"GSTR-1",
		"GSTR-2B",
		"Nagarjuna Fabricators",
		"accounts@example.in",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message does not mention %q:\n%s", want, msg)
		}
	}
	if !strings.Contains(msg, "1. Invoice INV/003/25-26") || !strings.Contains(msg, "2. Invoice INV/004/25-26") {
		t.Errorf("the invoices should be listed and numbered:\n%s", msg)
	}
	if !strings.Contains(msg, "The 2 invoices below do not appear") {
		t.Errorf("the plural reading is wrong:\n%s", msg)
	}
}

func TestFollowUpMessageForOneInvoiceReadsAsSingular(t *testing.T) {
	d := day(2025, time.July, 15)
	res := Reconcile(nil, []Invoice{inv(SourceBooks, "27AAPFU0939F1ZV", "A-1", d, 10000, 1800, 0, 0, 0)}, DefaultOptions())
	msg := FollowUpMessage(res.Suppliers[0], MessageContext{})

	if !strings.Contains(msg, "The invoice below does not appear") {
		t.Errorf("the singular reading is wrong:\n%s", msg)
	}
	if strings.Contains(msg, "invoices below") {
		t.Errorf("one invoice should not be written as several:\n%s", msg)
	}
	if !strings.Contains(msg, "Dear Sir or Madam") {
		t.Errorf("with no supplier name the greeting should still work:\n%s", msg)
	}
	if strings.Contains(msg, " for \n") || strings.Contains(msg, "for ,") {
		t.Errorf("with no period given the sentence should close cleanly:\n%s", msg)
	}
	if strings.Contains(msg, "1,800.00") == false {
		t.Errorf("the amount at risk is missing:\n%s", msg)
	}
	if !strings.Contains(msg, "whether it was reported") {
		t.Errorf("the request should read as singular too:\n%s", msg)
	}
}

// The tool writes the message; the business sends it. Nothing in the text may
// look like an automatic notice from a system the supplier does not know.
func TestFollowUpMessageIsPlainTextWithNoLinks(t *testing.T) {
	d := day(2025, time.July, 15)
	res := Reconcile(nil, []Invoice{inv(SourceBooks, "27AAPFU0939F1ZV", "A-1", d, 10000, 1800, 0, 0, 0)}, DefaultOptions())
	msg := FollowUpMessage(res.Suppliers[0], MessageContext{BusinessName: "Acme"})

	for _, forbidden := range []string{"http://", "https://", "<", ">", "unsubscribe"} {
		if strings.Contains(strings.ToLower(msg), forbidden) {
			t.Errorf("the message should be plain text with no %q:\n%s", forbidden, msg)
		}
	}
	if !strings.HasPrefix(msg, "Subject:") {
		t.Errorf("the message should open with a subject line the user can paste into email:\n%s", msg)
	}
	if !strings.HasSuffix(msg, "Acme\n") {
		t.Errorf("the message should end with the sender's own name:\n%s", msg)
	}
}

func TestFollowUpMessageOmitsEmptyContext(t *testing.T) {
	d := day(2025, time.July, 15)
	res := Reconcile(nil, []Invoice{inv(SourceBooks, "27AAPFU0939F1ZV", "A-1", d, 10000, 1800, 0, 0, 0)}, DefaultOptions())
	msg := FollowUpMessage(res.Suppliers[0], MessageContext{BusinessName: "  ", ContactLine: "  ", PeriodText: " "})

	if strings.Contains(msg, "for  ") {
		t.Errorf("an empty period left a gap in the sentence:\n%s", msg)
	}
	if !strings.HasSuffix(msg, "Thank you,\n") {
		t.Errorf("with nothing to sign off with the message should simply end:\n%s", msg)
	}
}

func TestAllFollowUpMessages(t *testing.T) {
	d := day(2025, time.July, 15)
	books := []Invoice{
		inv(SourceBooks, "27AAPFU0939F1ZV", "A-1", d, 100000, 18000, 0, 0, 0),
		inv(SourceBooks, "29AABCU9603R1ZM", "B-1", d, 10000, 0, 900, 900, 0),
	}
	res := Reconcile(nil, books, DefaultOptions())
	all := AllFollowUpMessages(res, MessageContext{BusinessName: "Acme"})

	if strings.Count(all, "Subject:") != 2 {
		t.Errorf("want one message per supplier, got %d:\n%s", strings.Count(all, "Subject:"), all)
	}
	if !strings.Contains(all, "27AAPFU0939F1ZV") || !strings.Contains(all, "29AABCU9603R1ZM") {
		t.Error("both suppliers should appear")
	}
	// Largest amount first, as in the rollup.
	if strings.Index(all, "27AAPFU0939F1ZV") > strings.Index(all, "29AABCU9603R1ZM") {
		t.Error("the supplier with more at risk should come first")
	}
}

func TestAllFollowUpMessagesWhenNothingIsMissing(t *testing.T) {
	got := AllFollowUpMessages(Reconcile(nil, nil, DefaultOptions()), MessageContext{})
	if !strings.Contains(got, "nothing to follow up") {
		t.Errorf("with nothing at risk the text should say so, got %q", got)
	}
	if strings.Contains(got, "Subject:") {
		t.Error("there should be no message to send")
	}
}
