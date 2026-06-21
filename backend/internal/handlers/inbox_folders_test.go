package handlers

import (
	"sort"
	"testing"

	"github.com/brygge-klubb/brygge/internal/mail"
)

func folderFixtures() []mail.Mailbox {
	return []mail.Mailbox{
		{ID: "m-inbox", Name: "Inbox", Role: "inbox"},
		{ID: "m-archive", Name: "Archive", Role: "archive"},
		{ID: "m-sent", Name: "Sent", Role: "sent"},
		{ID: "m-custom", Name: "Fakturaer", Role: ""},
	}
}

func TestPickFolder(t *testing.T) {
	mboxes := folderFixtures()
	cases := []struct {
		name     string
		selector string
		wantID   string
		wantOK   bool
	}{
		{"empty falls back to inbox", "", "m-inbox", true},
		{"inbox selector", "inbox", "m-inbox", true},
		{"inbox selector is case-insensitive", "INBOX", "m-inbox", true},
		{"match by role", "archive", "m-archive", true},
		{"match by role case-insensitive", "Sent", "m-sent", true},
		{"match custom folder by name", "Fakturaer", "m-custom", true},
		{"match by name case-insensitive", "fakturaer", "m-custom", true},
		{"unknown selector falls back to inbox", "Nonexistent", "m-inbox", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, ok := pickFolder(mboxes, tc.selector)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if m.ID != tc.wantID {
				t.Errorf("id = %q, want %q", m.ID, tc.wantID)
			}
		})
	}
}

func TestPickFolderNoInbox(t *testing.T) {
	// A selector that matches nothing, with no Inbox present, must report
	// not-ok so the handler creates the Inbox on demand.
	mboxes := []mail.Mailbox{{ID: "m-sent", Name: "Sent", Role: "sent"}}
	if m, ok := pickFolder(mboxes, "archive"); ok {
		t.Errorf("expected not-ok with no inbox, got %q", m.ID)
	}
}

func TestFolderSortRank(t *testing.T) {
	got := []mail.Mailbox{
		{Name: "Zeta", Role: ""},
		{Name: "Sent", Role: "sent"},
		{Name: "Inbox", Role: "inbox"},
		{Name: "Alfa", Role: ""},
		{Name: "Archive", Role: "archive"},
	}
	sort.SliceStable(got, func(i, j int) bool {
		ri, rj := folderSortRank(got[i].Role), folderSortRank(got[j].Role)
		if ri != rj {
			return ri < rj
		}
		return got[i].Name < got[j].Name
	})
	want := []string{"Inbox", "Archive", "Sent", "Alfa", "Zeta"}
	for i, w := range want {
		if got[i].Name != w {
			t.Errorf("position %d: got %q, want %q", i, got[i].Name, w)
		}
	}
}
