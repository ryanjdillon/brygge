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
	body := MagicLinkBody("nb", "Klokkarvik Båtlag", "klokkarvikbaatlag.no", "https://klokkarvikbaatlag.no/login?token=abc123")
	mustNoRawTemplate(t, "magic-link", body)
	if !strings.Contains(body, `href="https://klokkarvikbaatlag.no/login?token=abc123"`) {
		t.Error("magic-link: login URL missing from button href")
	}
	// Login CTA uses the dark brand colour via the shared button partial.
	if !strings.Contains(body, "background:#0f172a") {
		t.Error("magic-link: CTA button colour missing")
	}
	if !strings.Contains(body, "Klokkarvik Båtlag") {
		t.Error("magic-link: club header missing")
	}
}

func TestInvoiceBodyWithVipps(t *testing.T) {
	url := "https://pay.vipps.no/v2/?token=xyz&foo=bar"
	body := InvoiceBody("nb", "Ryan Dillon", "Klokkarvik Båtlag", 285,
		time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC), 1.0, "000002850028", "3470.05.02793",
		url)
	mustNoRawTemplate(t, "invoice", body)
	// The button href must carry the exact URL (template.URL, not filtered).
	if !strings.Contains(body, `href="https://pay.vipps.no/v2/?token=xyz&amp;foo=bar"`) {
		t.Errorf("invoice: Vipps URL missing/mangled in button href")
	}
	if !strings.Contains(body, ">Betal med Vipps</a>") {
		t.Error("invoice: Vipps button label missing")
	}
	if !strings.Contains(body, "background:#ff5b24") {
		t.Error("invoice: Vipps brand colour missing")
	}
	if !strings.Contains(body, "000002850028") {
		t.Error("invoice: KID missing from details box")
	}
}

func TestInvoiceBodyWithoutVipps(t *testing.T) {
	body := InvoiceBody("nb", "Ryan Dillon", "Klokkarvik Båtlag", 285,
		time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC), 1.0, "000002850028", "3470.05.02793",
		"")
	mustNoRawTemplate(t, "invoice-no-vipps", body)
	if strings.Contains(body, "Betal med Vipps") {
		t.Error("invoice: Vipps button rendered despite empty URL")
	}
}

func TestReminderBodyWithVipps(t *testing.T) {
	url := "https://pay.vipps.no/v2/?token=abc"
	body := InvoiceReminderBody("nb", "Ryan Dillon", "Klokkarvik Båtlag", 285,
		time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), 1.0, "000002850028", "3470.05.02793",
		url)
	mustNoRawTemplate(t, "reminder", body)
	if !strings.Contains(body, ">Betal med Vipps</a>") || !strings.Contains(body, "background:#ff5b24") {
		t.Error("reminder: Vipps button missing")
	}
	// Past due date → overdue copy.
	if !strings.Contains(body, "er forfalt") {
		t.Error("reminder: overdue copy missing for past-due invoice")
	}
}
