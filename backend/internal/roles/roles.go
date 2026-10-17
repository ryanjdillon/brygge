// Package roles holds the role vocabulary that more than one subsystem
// has to agree on. The user_role enum in Postgres is the storage; this is
// the one place that says which of those roles make up the club board.
package roles

// Board lists the board (styre) roles in display order. Board-only
// visibility, the officer pickers and the billing exemptions all read it,
// so adding an officer role here is enough for all of them to see it.
var Board = []string{"chair", "vice_chair", "treasurer", "secretary", "harbor_master", "deputy", "board"}

// boardLabelsNB are the Norwegian labels printed on fakturas (the PDF is
// always Norwegian). The SPA localises the same keys itself.
var boardLabelsNB = map[string]string{
	"chair":         "Styreleder",
	"vice_chair":    "Nestleder",
	"treasurer":     "Kasserer",
	"secretary":     "Sekretær",
	"harbor_master": "Havnesjef",
	"deputy":        "Varamedlem",
	"board":         "Styremedlem",
}

// IsBoard reports whether role is one of the board roles.
func IsBoard(role string) bool {
	_, ok := boardLabelsNB[role]
	return ok
}

// BoardLabelNB returns the Norwegian label for a board role, or the role
// key itself for anything else.
func BoardLabelNB(role string) string {
	if l, ok := boardLabelsNB[role]; ok {
		return l
	}
	return role
}
