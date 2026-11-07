package email

import (
	"strings"
	"testing"
)

func TestJoinLocale(t *testing.T) {
	for lang, want := range map[string]string{"nn": "nn", "nb": "nb", "": "nb", "en": "nb"} {
		if got := JoinLocale(lang); got != want {
			t.Errorf("JoinLocale(%q) = %q, want %q", lang, got, want)
		}
	}
}

func TestJoinEmails(t *testing.T) {
	const confirmURL = "https://club.example.test/join/confirm?token=abc_DEF-123"
	const loginURL = "https://club.example.test/api/v1/auth/verify?token=xyz"

	cases := []struct {
		name, subject, body, wantSubject, wantText, url string
	}{
		{"confirm nb", JoinConfirmSubject("nb", "Testklubben"), JoinConfirmBody("nb", "Testklubben", "club.example.test", confirmURL),
			"Bekreft innmeldingen i Testklubben", "24 timer", confirmURL},
		{"confirm nn", JoinConfirmSubject("nn", "Testklubben"), JoinConfirmBody("nn", "Testklubben", "club.example.test", confirmURL),
			"Stadfest innmeldinga i Testklubben", "24 timar", confirmURL},
		{"member nb", JoinAlreadyMemberSubject("nb", "Testklubben"), JoinAlreadyMemberBody("nb", "Testklubben", "club.example.test", loginURL),
			"Du er allerede registrert i Testklubben", "Logg inn", loginURL},
		{"member nn", JoinAlreadyMemberSubject("nn", ""), JoinAlreadyMemberBody("nn", "", "", loginURL),
			"Du er allereie registrert i klubben", "allereie", loginURL},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.subject != c.wantSubject {
				t.Errorf("subject = %q, want %q", c.subject, c.wantSubject)
			}
			if !strings.Contains(c.body, c.wantText) {
				t.Errorf("body lacks %q", c.wantText)
			}
			if !strings.Contains(c.body, `href="`+c.url+`"`) {
				t.Errorf("body has no button to %s", c.url)
			}
		})
	}
}

func TestJoinContactBoardEmail(t *testing.T) {
	body := JoinContactBoardBody("nn", "Testklubben", "club.example.test", "styret@example.com")
	if !strings.Contains(body, `href="mailto:styret@example.com"`) || !strings.Contains(body, "ta kontakt med styret") {
		t.Errorf("nn body lacks the board contact: %s", body)
	}
	if got := JoinContactBoardSubject("nb", "Testklubben"); got != "Innmelding i Testklubben" {
		t.Errorf("subject = %q", got)
	}
	noContact := JoinContactBoardBody("nb", "Testklubben", "club.example.test", "")
	if strings.Contains(noContact, "mailto:") || !strings.Contains(noContact, "Svar på denne e-posten") {
		t.Error("without a contact address the email should rely on the reply hint")
	}
}
