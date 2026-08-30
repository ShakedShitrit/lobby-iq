package rlstats

import "testing"

func TestPrettyArena(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Labs_4v4_Arena15_Blackout_P", "Blackout"},
		{"EuroStadium_Night_P", "Euro Stadium Night"},
		{"Stadium_P", "Stadium"},
		{"Park_Night_P", "Night"},
		{"HoopsStadium_P", "Hoops Stadium"},
		{"", ""},
		{"Mystery", "Mystery"},
	}

	for _, c := range cases {
		if got := PrettyArena(c.in); got != c.want {
			t.Errorf("PrettyArena(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
