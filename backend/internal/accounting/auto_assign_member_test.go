package accounting

import "testing"

func synthCand(id, first, last string) memberCand {
	return memberCand{
		id:         id,
		fullN:      normalizeName(first + " " + last),
		firstLastN: normalizeName(first + " " + last),
		lastN:      normalizeName(last),
	}
}

func TestMatchMemberNameNeedsAUniqueExactName(t *testing.T) {
	unique := []memberCand{synthCand("m1", "Synthetic", "Ankerfeste"), synthCand("m2", "Synthetic", "Baugspyd")}
	if id, pass := matchMemberName(unique, "Synthetic Ankerfeste"); id != "m1" || pass != memberPassFullName {
		t.Errorf("unique name = (%q, %d), want (m1, full name)", id, pass)
	}

	// Two members share the normalised name: neither is a strong match,
	// and no weak pass can pick one either.
	shared := []memberCand{synthCand("m1", "Synthetic", "Ankerfeste"), synthCand("m2", "Synthétic", "Ankerfeste")}
	if id, pass := matchMemberName(shared, "Synthetic Ankerfeste"); id != "" || pass != memberPassNone {
		t.Errorf("shared name = (%q, %d), want no match", id, pass)
	}

	// A full_name match on one member and a first+last match on another
	// is the same ambiguity.
	split := []memberCand{
		{id: "m1", fullN: normalizeName("Synthetic Ankerfeste"), firstLastN: normalizeName("Syn Ankerfeste")},
		{id: "m2", fullN: normalizeName("Synthetic J. Ankerfeste"), firstLastN: normalizeName("Synthetic Ankerfeste")},
	}
	if id, pass := matchMemberName(split, "Synthetic Ankerfeste"); pass <= memberPassFirstLast {
		t.Errorf("full name vs first+last on two members = (%q, %d), want no strong match", id, pass)
	}
}

func TestMatchMemberNameSharedNameFallsToWeakPasses(t *testing.T) {
	// The shared full name is ambiguous, but the email local part names one
	// member: that is a weak (suggest-only) match, never a strong one.
	cands := []memberCand{synthCand("m1", "Synthetic", "Ankerfeste"), synthCand("m2", "Synthetic", "Ankerfeste")}
	cands[1].emailLocal = normalizeName("syntheticankerfeste")
	id, pass := matchMemberName(cands, "Synthetic Ankerfeste")
	if id != "m2" || pass != memberPassEmail {
		t.Errorf("got (%q, %d), want (m2, email)", id, pass)
	}
}
