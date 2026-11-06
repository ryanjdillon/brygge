package email

import (
	"strings"
	"testing"
	"time"
)

func TestMembershipCancelEmails(t *testing.T) {
	end := time.Date(2026, time.December, 31, 0, 0, 0, 0, time.UTC)

	if got := MembershipCancelReceiptSubject("nn"); got != "Utmelding stadfesta" {
		t.Errorf("nn receipt subject = %q", got)
	}
	if got := MembershipCancelReceiptSubject("nb"); got != "Utmelding bekreftet" {
		t.Errorf("nb receipt subject = %q", got)
	}
	// The receipt is the written notice: it names the end date and says
	// nothing is refunded.
	receipt := MembershipCancelReceiptBody("nb", "Testklubben", "Kari", end, true, "https://club.example.test/portal/profile")
	for _, want := range []string{"Hei Kari,", "31.12.2026", "refunderes ikke", "skriftlige bekreftelsen", "/portal/profile", "ventelisten"} {
		if !strings.Contains(receipt, want) {
			t.Errorf("receipt lacks %q", want)
		}
	}
	// The waiting list is mentioned only to a member on it.
	if strings.Contains(MembershipCancelReceiptBody("nn", "Testklubben", "Kari", end, false, "u"), "ventelista") {
		t.Error("receipt warns about a waiting list the member is not on")
	}

	if got := MembershipCancelBoardSubject("nn", "Kari Testesen", end); got != "Utmelding: Kari Testesen (til og med 31.12)" {
		t.Errorf("nn board subject = %q", got)
	}
	if got := MembershipCancelBoardSubject("nb", "", end); got != "Utmelding: Et medlem (til og med 31.12)" {
		t.Errorf("nb board subject = %q", got)
	}
	// The member's name and reason are theirs to type, so they reach the
	// board escaped.
	board := MembershipCancelBoardBody("nb", "Testklubben", "<b>x</b>", "<script>y</script>", end, "https://club.example.test/admin/users?user=1")
	if strings.Contains(board, "<script>") || strings.Contains(board, "<b>x") || !strings.Contains(board, "&lt;script&gt;") {
		t.Error("name or reason not escaped in the board notice")
	}
	if strings.Contains(MembershipCancelBoardBody("nn", "K", "Kari", "", end, "u"), "Grunngjeving") {
		t.Error("board notice introduces a reason that was not given")
	}

	if got := MembershipCancelUndoneSubject("nn", "Kari Testesen"); got != "Utmelding angra: Kari Testesen" {
		t.Errorf("undo subject = %q", got)
	}
	refused := MembershipEndRefusedBody("nn", "Testklubben", "Kari Testesen", end, "https://club.example.test/admin/users?user=1")
	if !strings.Contains(refused, "einaste aktive administratoren") || !strings.Contains(refused, "31.12.2026") {
		t.Error("end-refused notice lacks the reason or the date")
	}
}
