package email

import (
	"strings"
	"testing"
)

func TestContactSubjectPrefixAndLimit(t *testing.T) {
	if got := ContactSubject("Slip"); got != "[Kontaktskjema] Slip" {
		t.Errorf("got %q", got)
	}
	got := ContactSubject(strings.Repeat("å", 400))
	if n := len([]rune(got)); n > len("[Kontaktskjema] ")+61 {
		t.Errorf("subject not limited, %d runes", n)
	}
}

func TestContactBodyEscapesAndKeepsLineBreaks(t *testing.T) {
	body := ContactBody(ContactMessage{
		Name:    `<b>Ola</b>`,
		Email:   "ola@example.com",
		Subject: `A & B`,
		Message: "one\n<script>x</script>",
	})
	for _, bad := range []string{"<b>Ola", "<script>"} {
		if strings.Contains(body, bad) {
			t.Errorf("unescaped %q in %s", bad, body)
		}
	}
	for _, want := range []string{"&lt;b&gt;Ola", "A &amp; B", "one<br>\n&lt;script&gt;"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in %s", want, body)
		}
	}
}
