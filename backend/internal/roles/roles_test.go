package roles

import "testing"

func TestBoardListAndLabelsAgree(t *testing.T) {
	if len(Board) != len(boardLabelsNB) {
		t.Fatalf("Board has %d roles but %d labels", len(Board), len(boardLabelsNB))
	}
	for _, r := range Board {
		if !IsBoard(r) {
			t.Errorf("%s is in Board but IsBoard is false", r)
		}
		if BoardLabelNB(r) == r {
			t.Errorf("%s has no Norwegian label", r)
		}
	}
	for _, r := range []string{"admin", "member", "slip_holder", "applicant", ""} {
		if IsBoard(r) {
			t.Errorf("%q must not be a board role", r)
		}
	}
}
