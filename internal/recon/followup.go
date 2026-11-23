package recon

import (
	"fmt"
	"strings"
)

// MessageContext is what the business wants to say about itself in a follow-up
// message. Everything in it is optional.
type MessageContext struct {
	// BusinessName signs the message off.
	BusinessName string `json:"businessName"`
	// ContactLine is a phone number or email address under the sign-off.
	ContactLine string `json:"contactLine"`
	// PeriodText names the return period, e.g. "September 2025".
	PeriodText string `json:"periodText"`
}

// FollowUpMessage writes the plain-text message to send to one supplier about
// the invoices of theirs that are missing from GSTR-2B.
//
// The message is only text. Nothing is sent anywhere: the business copies it
// into its own email or messaging app and sends it itself.
func FollowUpMessage(s SupplierFollowUp, ctx MessageContext) string {
	period := strings.TrimSpace(ctx.PeriodText)
	forPeriod := ""
	if period != "" {
		forPeriod = " for " + period
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Subject: %s not showing in our GSTR-2B%s - please check your GSTR-1\n\n",
		plural(len(s.Missing), "invoice", "invoices"), forPeriod)

	if name := strings.TrimSpace(s.Name); name != "" {
		fmt.Fprintf(&b, "Dear %s,\n", name)
	} else {
		b.WriteString("Dear Sir or Madam,\n")
	}
	fmt.Fprintf(&b, "GSTIN: %s\n\n", s.GSTIN)

	many := len(s.Missing) > 1
	subject := "The invoice below"
	if many {
		subject = fmt.Sprintf("The %d invoices below", len(s.Missing))
	}
	fmt.Fprintf(&b, "%s %s in our GSTR-2B%s, although %s recorded in our purchase register. Until %s reported, we cannot take the input tax credit of ₹%s on %s.\n\n",
		subject,
		pick1(many, "do not appear", "does not appear"), forPeriod,
		pick1(many, "they are", "it is"),
		pick1(many, "they are", "it is"),
		Rupees(s.TaxAtRisk),
		pick1(many, "them", "it"))

	for n, i := range s.Missing {
		fmt.Fprintf(&b, "%d. Invoice %s dated %s - taxable ₹%s, tax ₹%s\n",
			n+1, i.InvoiceNo, i.DateText(), Rupees(i.TaxableValue), Rupees(i.TotalTax()))
	}

	fmt.Fprintf(&b, "\nTotal tax we cannot claim at present: ₹%s\n\n", Rupees(s.TaxAtRisk))
	if many {
		b.WriteString("Please check whether these were reported in your GSTR-1. If any were missed, kindly include them in your next GSTR-1 so the credit reaches our GSTR-2B.")
	} else {
		b.WriteString("Please check whether it was reported in your GSTR-1. If it was missed, kindly include it in your next GSTR-1 so the credit reaches our GSTR-2B.")
	}
	b.WriteString(" If our GSTIN or an invoice number is recorded differently at your end, please tell us and we will correct our records.\n\n")
	b.WriteString("Thank you,\n")
	if n := strings.TrimSpace(ctx.BusinessName); n != "" {
		b.WriteString(n + "\n")
	}
	if c := strings.TrimSpace(ctx.ContactLine); c != "" {
		b.WriteString(c + "\n")
	}
	return b.String()
}

// AllFollowUpMessages joins one message per supplier, largest amount first, for
// a single download.
func AllFollowUpMessages(res Result, ctx MessageContext) string {
	if len(res.Suppliers) == 0 {
		return "Every invoice in the purchase register appears in GSTR-2B. There is nothing to follow up.\n"
	}
	parts := make([]string, 0, len(res.Suppliers))
	for _, s := range res.Suppliers {
		parts = append(parts, FollowUpMessage(s, ctx))
	}
	return strings.Join(parts, "\n----------------------------------------\n\n")
}

func pick1(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
}
