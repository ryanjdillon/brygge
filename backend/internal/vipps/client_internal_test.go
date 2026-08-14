package vipps

import (
	"encoding/json"
	"testing"
)

// TestVippsIntUnmarshal covers both representations Vipps may send for numeric
// fields: the quoted string form the Access Token API actually returns, and a
// bare JSON number (defensive).
func TestVippsIntUnmarshal(t *testing.T) {
	cases := map[string]struct {
		in   string
		want vippsInt
	}{
		"quoted string": {`"3599"`, 3599},
		"bare number":   {`3600`, 3600},
		"empty string":  {`""`, 0},
		"json null":     {`null`, 0},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var v vippsInt
			if err := json.Unmarshal([]byte(c.in), &v); err != nil {
				t.Fatalf("unmarshal %s: %v", c.in, err)
			}
			if v != c.want {
				t.Errorf("got %d, want %d", v, c.want)
			}
		})
	}
}

func TestVippsIntUnmarshalRejectsGarbage(t *testing.T) {
	var v vippsInt
	if err := json.Unmarshal([]byte(`"not-a-number"`), &v); err == nil {
		t.Fatal("expected error for non-numeric string, got nil")
	}
}
