package accounting

import (
	"reflect"
	"testing"
)

func TestPriceItemAccountSuggestions(t *testing.T) {
	accounts := map[string]suggestableAccount{
		"3120": {code: "3120", name: "Plassleie", kind: "revenue"},
		"3200": {code: "3200", name: "Gjestehavninntekter", kind: "revenue"},
		"3210": {code: "3210", name: "Bobil/campingplass", kind: "revenue"},
		"3300": {code: "3300", name: "Salgs- og tjenesteinntekter", kind: "revenue"},
	}
	type want struct{ code, label, why string }
	got := func(cands []priceArticleCandidate, knownMember bool) []want {
		var out []want
		for _, s := range priceItemAccountSuggestions(cands, knownMember, accounts) {
			out = append(out, want{s.Code, s.ConfidenceLabel, s.WhyTooltip})
		}
		return out
	}

	cases := []struct {
		name        string
		cands       []priceArticleCandidate
		knownMember bool
		want        []want
	}{
		{
			name: "every item on one account is likely",
			cands: []priceArticleCandidate{
				{name: "Slipping liten båt", category: "service", audience: "all", multiplier: 1},
				{name: "Spyling", category: "service", audience: "all", multiplier: 1},
			},
			want: []want{{"3300", "sannsynleg", "Slipping liten båt, Spyling"}},
		},
		{
			name: "items on several accounts are weak, in candidate order",
			cands: []priceArticleCandidate{
				{name: "Gjesteplass", category: "guest", audience: "all", multiplier: 3, perUnit: true},
				{name: "Bobilplass", category: "motorhome", audience: "all", multiplier: 3, perUnit: true},
			},
			want: []want{{"3200", "svak", "3x Gjesteplass"}, {"3210", "svak", "3x Bobilplass"}},
		},
		{
			name: "a multiple on one account is still only weak",
			cands: []priceArticleCandidate{
				{name: "Bobilplass per døgn", category: "motorhome", audience: "all", multiplier: 9, perUnit: true},
			},
			want: []want{{"3210", "svak", "9x Bobilplass per døgn"}},
		},
		{
			name: "one single-unit item makes its account likely",
			cands: []priceArticleCandidate{
				{name: "Bobilplass per døgn", category: "motorhome", audience: "all", multiplier: 1, perUnit: true},
				{name: "Bobil strøm per døgn", category: "motorhome", audience: "all", multiplier: 2, perUnit: true},
			},
			want: []want{{"3210", "sannsynleg", "Bobilplass per døgn, 2x Bobil strøm per døgn"}},
		},
		{
			name: "members-only items need a known member",
			cands: []priceArticleCandidate{
				{name: "Sesongplass medlem", category: "seasonal_rental", audience: "member", multiplier: 1},
				{name: "Slipping liten båt", category: "service", audience: "all", multiplier: 1},
			},
			want: []want{{"3300", "sannsynleg", "Slipping liten båt"}},
		},
		{
			name: "members-only items stay out of the tooltip for an unknown payer",
			cands: []priceArticleCandidate{
				{name: "Slipping medlem", category: "service", audience: "member", multiplier: 1},
				{name: "Slipping", category: "service", audience: "all", multiplier: 1},
			},
			want: []want{{"3300", "sannsynleg", "Slipping"}},
		},
		{
			name: "a known member keeps members-only items",
			cands: []priceArticleCandidate{
				{name: "Sesongplass medlem", category: "seasonal_rental", audience: "member", multiplier: 1},
				{name: "Slipping liten båt", category: "service", audience: "all", multiplier: 1},
			},
			knownMember: true,
			want:        []want{{"3120", "svak", "Sesongplass medlem"}, {"3300", "svak", "Slipping liten båt"}},
		},
		{
			name: "unmapped categories and missing accounts are skipped",
			cands: []priceArticleCandidate{
				{name: "T-skjorte", category: "merchandise", audience: "all", multiplier: 1},
				{name: "Strøm", category: "electricity", audience: "all", multiplier: 1},
			},
			want: nil,
		},
		{
			name: "the tooltip names at most three items",
			cands: []priceArticleCandidate{
				{name: "A", category: "service", audience: "all", multiplier: 1},
				{name: "B", category: "service", audience: "all", multiplier: 1},
				{name: "C", category: "service", audience: "all", multiplier: 1},
				{name: "D", category: "service", audience: "all", multiplier: 1},
			},
			want: []want{{"3300", "sannsynleg", "A, B, C, …"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if g := got(tc.cands, tc.knownMember); !reflect.DeepEqual(g, tc.want) {
				t.Errorf("got %+v, want %+v", g, tc.want)
			}
		})
	}
}
