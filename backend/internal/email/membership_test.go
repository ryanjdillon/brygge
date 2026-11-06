package email

import (
	"strings"
	"testing"
)

func TestMembershipEmails(t *testing.T) {
	if got := MembershipWelcomeSubject("nn", "Testklubben"); got != "Velkomen som medlem i Testklubben" {
		t.Errorf("nn welcome subject = %q", got)
	}
	if got := MembershipWelcomeSubject("nb", ""); got != "Velkommen som medlem i klubben" {
		t.Errorf("nb welcome subject = %q", got)
	}
	body := MembershipWelcomeBody("nn", "Testklubben", "Kari", "https://club.example.test/portal", "https://club.example.test/portal/waiting-list")
	for _, want := range []string{"Hei Kari,", "https://club.example.test/portal", "/portal/waiting-list", "ventelista"} {
		if !strings.Contains(body, want) {
			t.Errorf("welcome body lacks %q", want)
		}
	}

	if got := MembershipBoardNoticeSubject("nb", "Kari Testesen"); got != "Nytt medlem: Kari Testesen" {
		t.Errorf("board subject = %q", got)
	}
	if got := MembershipJoinRefundedSubject("nn", ""); got != "Innmeldingsbetaling refundert: eit medlem" {
		t.Errorf("refund subject = %q", got)
	}
	// A member's name is theirs to type, so it reaches the board's email
	// escaped.
	board := MembershipBoardNoticeBody("nb", "Testklubben", `<script>x</script>`, "https://club.example.test/admin/users?user=1")
	if strings.Contains(board, "<script>") || !strings.Contains(board, "&lt;script&gt;") {
		t.Error("member name not escaped in the board notice")
	}
	refund := MembershipJoinRefundedBody("nn", "Testklubben", "Kari Testesen", "https://club.example.test/admin/users?user=1")
	if !strings.Contains(refund, "ikkje trekt tilbake") || !strings.Contains(refund, "/admin/users?user=1") {
		t.Error("refund notice lacks the no-revoke note or the member link")
	}

	if got := MembershipGrantWithheldSubject("nb", "Kari Testesen"); got != "Innmelding må kontrolleres: Kari Testesen" {
		t.Errorf("withheld subject = %q", got)
	}
	withheld := MembershipGrantWithheldBody("nn", "Testklubben", "Kari Testesen", "total below the join line", "https://club.example.test/admin/users?user=1")
	if !strings.Contains(withheld, "total below the join line") || !strings.Contains(withheld, "/admin/users?user=1") {
		t.Error("withheld notice lacks the reason or the member link")
	}
}
