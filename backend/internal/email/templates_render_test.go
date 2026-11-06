package email

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Rendering the shared-partial email templates (BRY-258). These also exercise
// the package-init template parsing (a bad partial would panic at init).

func mustNoRawTemplate(t *testing.T, name, body string) {
	t.Helper()
	if strings.Contains(body, "{{") || strings.Contains(body, "ZgotmplZ") {
		t.Fatalf("%s: unrendered template token or filtered value in output", name)
	}
	// Shared card chrome must be present in every email.
	if !strings.Contains(body, "max-width:560px;background:#ffffff") {
		t.Errorf("%s: shared card chrome missing", name)
	}
}

func TestMagicLinkBodyRenders(t *testing.T) {
	body := MagicLinkBody("nb", "Test Båtlag", "club.example.invalid", "https://club.example.invalid/login?token=abc123")
	mustNoRawTemplate(t, "magic-link", body)
	if !strings.Contains(body, `href="https://club.example.invalid/login?token=abc123"`) {
		t.Error("magic-link: login URL missing from button href")
	}
	// Login CTA uses the dark brand colour via the shared button partial.
	if !strings.Contains(body, "background:#0f172a") {
		t.Error("magic-link: CTA button colour missing")
	}
	if !strings.Contains(body, "Test Båtlag") {
		t.Error("magic-link: club header missing")
	}
}

func TestInvoiceBodyWithPayLink(t *testing.T) {
	url := "https://club.example.invalid/betal/xyz?a=1&b=2"
	body := InvoiceBody("nb", "Test Member", "Test Båtlag", 285,
		time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC), 1.0, "000002850028", "1234.56.78903",
		url, "Vipps eller kort", nil)
	mustNoRawTemplate(t, "invoice", body)
	// The button href must carry the exact URL, only HTML-escaped.
	if !strings.Contains(body, `href="https://club.example.invalid/betal/xyz?a=1&amp;b=2"`) {
		t.Errorf("invoice: pay URL missing/mangled in button href")
	}
	if !strings.Contains(body, ">Betal nå</a>") {
		t.Error("invoice: pay button label missing")
	}
	// The pay CTA uses the same dark colour as the login button, not a
	// provider's brand colour.
	if !strings.Contains(body, "background:#0f172a") || strings.Contains(body, "#ff5b24") {
		t.Error("invoice: pay button colour wrong")
	}
	if strings.Contains(body, "Betal med Vipps") {
		t.Error("invoice: Vipps-only button label still rendered")
	}
	if !strings.Contains(body, "000002850028") {
		t.Error("invoice: KID missing from details box")
	}
}

func TestInvoiceBodyPayMethods(t *testing.T) {
	due := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	url := "https://club.example.invalid/betal/xyz"
	tests := []struct {
		name         string
		vipps, card  bool
		url          string
		wantMethods  string
		wantButton   bool
		absentPhrase string
	}{
		{"vipps only", true, false, url, "Vipps", true, "kort"},
		{"card only", false, true, url, "Kort", true, "Vipps"},
		{"both", true, true, url, "Vipps eller kort", true, ""},
		{"none", false, false, "", "", false, "Vipps"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			methods := PayMethodsLabel(tc.vipps, tc.card)
			if methods != tc.wantMethods {
				t.Fatalf("PayMethodsLabel = %q, want %q", methods, tc.wantMethods)
			}
			for _, b := range []struct{ name, body string }{
				{"invoice", InvoiceBody("nb", "Test Member", "Test Båtlag", 285, due, 1.0, "000002850028", "1234.56.78903", tc.url, methods, nil)},
				{"reminder", InvoiceReminderBody("nb", "Test Member", "Test Båtlag", 285, due, 1.0, "000002850028", "1234.56.78903", tc.url, methods, nil)},
			} {
				mustNoRawTemplate(t, b.name, b.body)
				if got := strings.Contains(b.body, ">Betal nå</a>"); got != tc.wantButton {
					t.Errorf("%s: pay button shown = %v, want %v", b.name, got, tc.wantButton)
				}
				line := `color:#64748b;">` + tc.wantMethods + `</p>`
				if tc.wantMethods != "" && !strings.Contains(b.body, line) {
					t.Errorf("%s: methods line %q missing", b.name, tc.wantMethods)
				}
				if tc.absentPhrase != "" && strings.Contains(b.body, tc.absentPhrase) {
					t.Errorf("%s: unexpected %q in body", b.name, tc.absentPhrase)
				}
			}
		})
	}
}

func TestInvoiceBodyWithoutPayLink(t *testing.T) {
	body := InvoiceBody("nb", "Test Member", "Test Båtlag", 285,
		time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC), 1.0, "000002850028", "1234.56.78903",
		"", "", nil)
	mustNoRawTemplate(t, "invoice-no-pay-link", body)
	if strings.Contains(body, "Betal nå") || strings.Contains(body, "på nett") {
		t.Error("invoice: pay button rendered despite empty URL")
	}
}

func TestReminderBodyWithPayLink(t *testing.T) {
	url := "https://club.example.invalid/betal/abc"
	body := InvoiceReminderBody("nb", "Test Member", "Test Båtlag", 285,
		time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), 1.0, "000002850028", "1234.56.78903",
		url, "Kort", nil)
	mustNoRawTemplate(t, "reminder", body)
	if !strings.Contains(body, ">Betal nå</a>") || !strings.Contains(body, "background:#0f172a") {
		t.Error("reminder: pay button missing")
	}
	// Past due date → overdue copy.
	if !strings.Contains(body, "er forfalt") {
		t.Error("reminder: overdue copy missing for past-due invoice")
	}
}

func TestFallbackPayLine(t *testing.T) {
	if got := fallbackPayLine(""); got != "" {
		t.Errorf("fallbackPayLine(\"\") = %q, want empty", got)
	}
	if got := fallbackPayLine("https://club.example.invalid/betal/x"); !strings.HasPrefix(got, "<p>Betal nå: ") {
		t.Errorf("fallbackPayLine = %q", got)
	}
	// The flat fallback is built without html/template, so it escapes itself.
	got := fallbackPayLine(`https://club.example.invalid/betal/x?a=1&b="2"`)
	if !strings.Contains(got, `href="https://club.example.invalid/betal/x?a=1&amp;b=&#34;2&#34;"`) {
		t.Errorf("fallbackPayLine did not escape the URL: %q", got)
	}
}

func TestInvoiceEmailsListLines(t *testing.T) {
	due := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	lines := []InvoiceLine{
		{Description: "Båtplassleie 2026", SubDescription: "Plass A1 · 3,0 m bredde", Amount: 2500},
		{Description: "Medlemskontingent", Amount: 1234.5},
		{Description: "Fritak styreverv", Amount: -650},
		{Description: "Dugnad utført", Amount: 0},
	}
	for _, b := range []struct{ name, body string }{
		{"invoice", InvoiceBody("nb", "Kari Testesen", "Eksempel Båtlag", 285, due, 3084.5, "000002850028", "1234.56.78903", "", "", lines)},
		{"reminder", InvoiceReminderBody("nb", "Kari Testesen", "Eksempel Båtlag", 285, due, 3084.5, "000002850028", "1234.56.78903", "", "", lines)},
	} {
		t.Run(b.name, func(t *testing.T) {
			mustNoRawTemplate(t, b.name, b.body)
			for _, want := range []string{
				">Gjelder</p>",
				"Båtplassleie 2026", "Plass A1 · 3,0 m bredde", ">kr 2 500,00</td>",
				"Medlemskontingent", ">kr 1 234,50</td>",
				"Fritak styreverv", ">kr -650,00</td>",
				"Dugnad utført", ">kr 0,00</td>",
			} {
				if !strings.Contains(b.body, want) {
					t.Errorf("%q missing", want)
				}
			}
			if strings.Contains(b.body, "til (se PDF-vedlegget)") {
				t.Error("overflow note shown for four lines")
			}
			// The list precedes the payment box.
			if strings.Index(b.body, ">Gjelder</p>") > strings.Index(b.body, "Fakturanummer") {
				t.Error("Gjelder list rendered after the payment details")
			}
		})
	}
}

func TestInvoiceEmailOmitsGjelderWithoutLines(t *testing.T) {
	body := InvoiceBody("nb", "Kari Testesen", "Eksempel Båtlag", 285,
		time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC), 1.0, "000002850028", "1234.56.78903", "", "", nil)
	if strings.Contains(body, "Gjelder") {
		t.Error("Gjelder heading rendered without lines")
	}
}

func TestInvoiceEmailEscapesLineText(t *testing.T) {
	lines := []InvoiceLine{{Description: `<script>alert("x")</script> Kran & slipp`, SubDescription: "<b>A&B</b>", Amount: 100}}
	body := InvoiceBody("nb", "Kari Testesen", "Eksempel Båtlag", 285,
		time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC), 100, "000002850028", "1234.56.78903", "", "", lines)
	if strings.Contains(body, "<script>") || strings.Contains(body, "<b>A&B</b>") {
		t.Fatal("line text reached the email unescaped")
	}
	for _, want := range []string{"&lt;script&gt;", "Kran &amp; slipp", "&lt;b&gt;A&amp;B&lt;/b&gt;"} {
		if !strings.Contains(body, want) {
			t.Errorf("escaped %q missing", want)
		}
	}
}

func TestInvoiceEmailCapsLines(t *testing.T) {
	lines := make([]InvoiceLine, 13)
	for i := range lines {
		lines[i] = InvoiceLine{Description: fmt.Sprintf("Linje %02d", i+1), Amount: 10}
	}
	due := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	for name, body := range map[string]string{
		"invoice":  InvoiceBody("nb", "Kari Testesen", "Eksempel Båtlag", 285, due, 130, "000002850028", "1234.56.78903", "", "", lines),
		"reminder": InvoiceReminderBody("nb", "Kari Testesen", "Eksempel Båtlag", 285, due, 130, "000002850028", "1234.56.78903", "", "", lines),
	} {
		if !strings.Contains(body, "Linje 10") || strings.Contains(body, "Linje 11") {
			t.Errorf("%s: list not cut after ten lines", name)
		}
		if !strings.Contains(body, "+ 3 til (se PDF-vedlegget)") {
			t.Errorf("%s: overflow note missing or miscounted", name)
		}
	}

	exact := InvoiceBody("nb", "Kari Testesen", "Eksempel Båtlag", 285,
		time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC), 100, "000002850028", "1234.56.78903", "", "", lines[:10])
	if !strings.Contains(exact, "Linje 10") || strings.Contains(exact, "til (se PDF-vedlegget)") {
		t.Error("exactly ten lines must all show, with no overflow note")
	}
}

// A faktura that nets to zero lists its lines too, exemption included.
func TestInvoiceNoPaymentEmailListsLines(t *testing.T) {
	lines := []InvoiceLine{{Description: "Båtplassleie 2026", Amount: 2500}, {Description: "Fritak styreverv", Amount: -2500}}
	body := InvoiceBody("nb", "Kari Testesen", "Eksempel Båtlag", 285,
		time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC), 0, "000002850028", "1234.56.78903", "", "", lines)
	if !strings.Contains(body, "ikke betale noe") || !strings.Contains(body, ">kr -2 500,00</td>") {
		t.Error("zero-total email lacks the no-payment copy or the exemption line")
	}
	if strings.Contains(body, "000002850028") {
		t.Error("zero-total email shows the KID")
	}
}

// The flat fallbacks are built without html/template; they escape the values
// themselves and leave the lines to the PDF.
func TestInvoiceFallbackBodiesEscape(t *testing.T) {
	name, club := `<img src=x onerror=alert(1)> & Co`, "Båt & <Lag>"
	for _, b := range []struct{ name, body string }{
		{"no-payment", invoiceNoPaymentFallback(name, club, 285)},
		{"invoice", invoicePaymentFallback(false, name, club, 285, "05.09.2026", "kr 1,00", "000002850028", "1234.56.78903", "")},
		{"all values", invoicePaymentFallback(false, name, club, 285, "<due>", "<amount>", "<kid>", "<account>", "")},
		{"reminder", invoicePaymentFallback(true, name, club, 285, "05.09.2026", "kr 1,00", "000002850028", "1234.56.78903", "")},
	} {
		if strings.Contains(b.body, "<img") || strings.Contains(b.body, "<Lag>") ||
			strings.Contains(b.body, "<due>") || strings.Contains(b.body, "<amount>") || strings.Contains(b.body, "<kid>") || strings.Contains(b.body, "<account>") {
			t.Errorf("%s: name or club unescaped: %q", b.name, b.body)
		}
		if !strings.Contains(b.body, "&lt;img src=x onerror=alert(1)&gt; &amp; Co") || !strings.Contains(b.body, "Båt &amp; &lt;Lag&gt;") {
			t.Errorf("%s: escaped name or club missing: %q", b.name, b.body)
		}
	}
	if got := invoicePaymentFallback(true, "Kari", "Lag", 1, "", "", "", "", ""); !strings.Contains(got, "Påminnelse om ubetalt faktura (#1)") {
		t.Errorf("reminder fallback intro = %q", got)
	}
}

func TestFormatNOK(t *testing.T) {
	tests := []struct {
		in   float64
		want string
	}{
		{0, "kr 0,00"},
		{1234.5, "kr 1 234,50"},
		{-650, "kr -650,00"},
		{-150.5, "kr -150,50"},
		{-0.5, "kr -0,50"},
		{0.999, "kr 1,00"},
		{1234567.891, "kr 1 234 567,89"},
	}
	for _, tc := range tests {
		if got := formatNOK(tc.in); got != tc.want {
			t.Errorf("formatNOK(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The shared layout pins the light colour scheme, so auto-darkening clients
// leave the card as designed; the magic-link email shares it.
func TestEmailLayoutDeclaresLightScheme(t *testing.T) {
	for name, body := range map[string]string{
		"magic-link": MagicLinkBody("nb", "Eksempel Båtlag", "club.example.invalid", "https://club.example.invalid/login?token=abc"),
		"invoice": InvoiceBody("nb", "Kari", "Eksempel Båtlag", 1,
			time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC), 1, "000002850028", "1234.56.78903", "", "", nil),
	} {
		for _, want := range []string{
			`<html style="color-scheme:light;">`,
			`<meta name="color-scheme" content="light">`,
			`<meta name="supported-color-schemes" content="light">`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: %q missing", name, want)
			}
		}
	}
}

// Payment numbers render in the body font with tabular figures, not a
// monospace stack.
func TestInvoiceDetailsUseBodyFont(t *testing.T) {
	body := InvoiceBody("nb", "Kari", "Eksempel Båtlag", 1,
		time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC), 1, "000002850028", "1234.56.78903", "", "", nil)
	if strings.Contains(body, "monospace") {
		t.Error("details box still uses a monospace stack")
	}
	if !strings.Contains(body, "tabular-nums;\">000002850028</td>") {
		t.Error("KID cell lacks tabular figures")
	}
}
