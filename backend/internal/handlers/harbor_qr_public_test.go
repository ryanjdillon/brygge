package handlers

import "testing"

func TestResolveQRUnits(t *testing.T) {
	cases := []struct {
		name      string
		min, max  int
		fixed     bool
		requested int
		want      int
		wantOK    bool
	}{
		{"fixed ignores requested", 1, 14, true, 99, 1, true},
		{"fixed with zero requested", 1, 14, true, 0, 1, true},
		{"in range", 1, 14, false, 3, 3, true},
		{"at min", 1, 14, false, 1, 1, true},
		{"at max", 1, 14, false, 14, 14, true},
		{"below min rejected", 2, 14, false, 1, 0, false},
		{"above max rejected", 1, 5, false, 6, 0, false},
		{"zero rejected", 1, 14, false, 0, 0, false},
		{"negative rejected", 1, 14, false, -3, 0, false},
		{"degenerate min normalises to 1", 0, 14, false, 1, 1, true},
		{"max below min clamps to min", 5, 2, false, 5, 5, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := resolveQRUnits(c.min, c.max, c.fixed, c.requested)
			if got != c.want || ok != c.wantOK {
				t.Fatalf("resolveQRUnits(%d,%d,%v,%d) = (%d,%v), want (%d,%v)",
					c.min, c.max, c.fixed, c.requested, got, ok, c.want, c.wantOK)
			}
		})
	}
}
