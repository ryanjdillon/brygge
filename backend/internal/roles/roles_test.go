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

// Pins the board-document readers to the set that predates roles.Board,
// so widening it is a deliberate change, not a side effect of a refactor.
func TestBoardDocumentReadersArePinned(t *testing.T) {
	for _, r := range []string{"board", "admin", "harbor_master", "treasurer"} {
		if !CanReadBoardDocuments([]string{"member", r}) {
			t.Errorf("%s must read board documents", r)
		}
	}
	for _, r := range []string{"chair", "vice_chair", "secretary", "deputy", "member", "slip_holder", "applicant"} {
		if CanReadBoardDocuments([]string{r}) {
			t.Errorf("%s must not read board documents until the owner decides otherwise", r)
		}
	}
}
