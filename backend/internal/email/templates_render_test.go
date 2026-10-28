package email

import (
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
		url, "Vipps eller kort")
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
				{"invoice", InvoiceBody("nb", "Test Member", "Test Båtlag", 285, due, 1.0, "000002850028", "1234.56.78903", tc.url, methods)},
				{"reminder", InvoiceReminderBody("nb", "Test Member", "Test Båtlag", 285, due, 1.0, "000002850028", "1234.56.78903", tc.url, methods)},
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
		"", "")
	mustNoRawTemplate(t, "invoice-no-pay-link", body)
	if strings.Contains(body, "Betal nå") || strings.Contains(body, "på nett") {
		t.Error("invoice: pay button rendered despite empty URL")
	}
}

func TestReminderBodyWithPayLink(t *testing.T) {
	url := "https://club.example.invalid/betal/abc"
	body := InvoiceReminderBody("nb", "Test Member", "Test Båtlag", 285,
		time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), 1.0, "000002850028", "1234.56.78903",
		url, "Kort")
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
