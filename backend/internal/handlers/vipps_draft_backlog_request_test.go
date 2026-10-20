package handlers

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/brygge-klubb/brygge/internal/accounting"
)

// An absent or null reclassify_entry_ids means "every reclassification";
// an empty list means "none of them". Mixing those up would either post
// rebookings nobody ticked or silently skip all of them.
func TestVippsDraftBacklogRequestChoices(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []accounting.VippsBacklogChoice
	}{
		{"absent applies all", `{"period_id":"p"}`, nil},
		{"null applies all", `{"period_id":"p","reclassify_entry_ids":null}`, nil},
		{"empty applies none", `{"period_id":"p","reclassify_entry_ids":[]}`, []accounting.VippsBacklogChoice{}},
		{
			"ids carry their previewed target, once each; a missing target is empty",
			`{"period_id":"p","reclassify_entry_ids":["a","b","a"],"reclassify_targets":{"a":"3300"}}`,
			[]accounting.VippsBacklogChoice{{EntryID: "a", To: "3300"}, {EntryID: "b", To: ""}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var req vippsDraftBacklogRequest
			if err := json.Unmarshal([]byte(tc.body), &req); err != nil {
				t.Fatalf("decode: %v", err)
			}
			got := req.choices()
			if (got == nil) != (tc.want == nil) || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("choices() = %#v, want %#v", got, tc.want)
			}
		})
	}
}
