package vipps

import "testing"

func TestUserDetailsPayerName(t *testing.T) {
	cases := []struct {
		name string
		ud   *UserDetails
		want string
	}{
		{"nil receiver", nil, ""},
		{"full name preferred", &UserDetails{Name: "Ola Nordmann", GivenName: "Ola", FamilyName: "Nordmann"}, "Ola Nordmann"},
		{"given + family fallback", &UserDetails{GivenName: "Kari", FamilyName: "Nordmann"}, "Kari Nordmann"},
		{"given only", &UserDetails{GivenName: "Kari"}, "Kari"},
		{"family only", &UserDetails{FamilyName: "Nordmann"}, "Nordmann"},
		{"nothing consented", &UserDetails{}, ""},
		{"trims whitespace", &UserDetails{Name: "  Ola  "}, "Ola"},
	}
	for _, c := range cases {
		if got := c.ud.PayerName(); got != c.want {
			t.Errorf("%s: PayerName() = %q, want %q", c.name, got, c.want)
		}
	}
}
