package email

import (
	"strings"
	"testing"
)

// A credit note's email documents a credit and asks for nothing: no KID,
// account, due date or pay button.
func TestCreditNoteEmailAsksForNothing(t *testing.T) {
	subject := CreditNoteSubject("Test Båtlag", 289)
	if subject != "Kreditnota fra Test Båtlag [Kreditnota 289]" {
		t.Errorf("subject = %q", subject)
	}
	body := CreditNoteBody("Test <Member>", "Test Båtlag", 289, -5)
	for _, want := range []string{"kreditnota #289", "Test Båtlag", "kr 5,00", "Du skal ikke betale noe", "Test &lt;Member&gt;"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q: %s", want, body)
		}
	}
	for _, unwanted := range []string{"KID", "Forfall", "Konto", "href", "-5", "faktura"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("body shows %q: %s", unwanted, body)
		}
	}
}

func TestRefundDocumentSubjectUsesCreditNoteSubject(t *testing.T) {
	if got, want := RefundDocumentSubject("Test Båtlag", 289, -5), CreditNoteSubject("Test Båtlag", 289); got != want {
		t.Errorf("credit note subject = %q, want %q", got, want)
	}
	if got := RefundDocumentSubject("Test Båtlag", 290, 5); got != "Ny faktura fra Test Båtlag [Faktura 290]" {
		t.Errorf("faktura subject = %q", got)
	}
}

// The new faktura after a failed refund is told as already paid, with
// nothing to act on.
func TestRefundDocumentBodyForSettledFaktura(t *testing.T) {
	body := RefundDocumentBody("Test Medlem", "Test Båtlag", 290, 5)
	for _, want := range []string{"Hei Test Medlem", "faktura #290", "Den er allerede betalt, og du skal ikke betale noe."} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q: %s", want, body)
		}
	}
	for _, unwanted := range []string{"KID", "href", "Forfall"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("body shows %q: %s", unwanted, body)
		}
	}
}
