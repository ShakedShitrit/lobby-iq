package presence

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ShakedShitrit/lobby-iq/internal/rankicon"
)

func TestParseRank(t *testing.T) {
	cases := []struct {
		in   string
		tier int
		name string
	}{
		{"Champion I", 16, "Champion I"},
		{"champion 1", 16, "Champion I"},
		{"champ1", 16, "Champion I"},
		{"c1", 16, "Champion I"},
		{"C III", 18, "Champion III"},
		{"Grand Champion I", 19, "Grand Champion I"},
		{"gc2", 20, "Grand Champion II"},
		{"grandchamp3", 21, "Grand Champion III"},
		{"Supersonic Legend", 22, "Supersonic Legend"},
		{"ssl", 22, "Supersonic Legend"},
		{"Bronze I", 1, "Bronze I"},
		{"s3", 6, "Silver III"},
		{"Gold II", 8, "Gold II"},
		{"plat 3", 12, "Platinum III"},
		{"Diamond II", 14, "Diamond II"},
		{"d1", 13, "Diamond I"},
		{"Unranked", 0, "Unranked"},
		{"  champion   ii  ", 17, "Champion II"},
		{"Champion Div 2", 17, "Champion II"},
	}

	for _, tc := range cases {
		tier, name, ok := parseRank(tc.in)
		if !ok {
			t.Errorf("parseRank(%q) failed, want %q", tc.in, tc.name)
			continue
		}
		if tier != tc.tier || name != tc.name {
			t.Errorf("parseRank(%q) = (%d, %q), want (%d, %q)", tc.in, tier, name, tc.tier, tc.name)
		}
	}
}

// The short aliases overlap by prefix, and picking the wrong one would show a
// wildly wrong badge - "gc2" as a Gold, or "ssl" as a Silver.
func TestParseRankPrefersLongerAlias(t *testing.T) {
	if tier, _, _ := parseRank("gc1"); tier != 19 {
		t.Errorf("gc1 = tier %d, want 19 (Grand Champion I, not Gold)", tier)
	}
	if tier, _, _ := parseRank("ssl"); tier != 22 {
		t.Errorf("ssl = tier %d, want 22 (Supersonic Legend, not Silver)", tier)
	}
}

func TestParseRankRejectsNonsense(t *testing.T) {
	for _, in := range []string{"", "   ", "Wood III", "champion IV", "champion 4", "ssl2", "zzz"} {
		if tier, name, ok := parseRank(in); ok {
			t.Errorf("parseRank(%q) = (%d, %q), want a rejection", in, tier, name)
		}
	}
}

// Every rank must have its own art, since the URL is built from the slice
// index. It takes two CDN sets to cover the range, and picking the wrong one
// for a tier yields a 404 that shows up as a blank icon on the card.
func TestRankIconCoversEveryRank(t *testing.T) {
	seen := map[string]string{}
	for tier, name := range rankNames {
		icon := rankIcon(tier)
		if !strings.HasPrefix(icon, "https://") {
			t.Errorf("%s: icon %q is not a URL", name, icon)
		}
		if other, dup := seen[icon]; dup {
			t.Errorf("%s and %s share the icon %q", other, name, icon)
		}
		seen[icon] = name

		wantSet := "s4-"
		if tier >= rankicon.TopTier {
			wantSet = "s15rank"
		}
		if !strings.Contains(icon, wantSet) {
			t.Errorf("%s (tier %d): icon %q, want it from the %q set", name, tier, icon, wantSet)
		}
	}
}

// TestRankIconsResolve checks the URLs against the CDN, which is the only way
// to catch tracker.gg moving or renaming the art. Opt-in, since the rest of
// the suite doesn't touch the network.
func TestRankIconsResolve(t *testing.T) {
	if os.Getenv("LOBBYIQ_TEST_NET") != "1" {
		t.Skip("set LOBBYIQ_TEST_NET=1 to check rank icon URLs against the CDN")
	}

	client := &http.Client{Timeout: 20 * time.Second}
	for tier, name := range rankNames {
		icon := rankIcon(tier)
		resp, err := client.Head(icon)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: %s returned %d", name, icon, resp.StatusCode)
		}
	}
}

func TestRankBadgeReplacesLogo(t *testing.T) {
	p := newTestPresence(nil)
	p.ranks = NewConfigRanks(map[string]string{"2v2": "Champion I"})
	p.Observe(tick(1, 0))

	p.mu.Lock()
	a := p.buildLocked()
	p.mu.Unlock()

	if want := rankIcon(16); a.Assets.LargeImage != want {
		t.Errorf("large image = %q, want the rank badge %q", a.Assets.LargeImage, want)
	}
	// The arena must survive the takeover, since it has nowhere else to go.
	if !strings.Contains(a.Assets.LargeText, "Champion I") || !strings.Contains(a.Assets.LargeText, "Blackout") {
		t.Errorf("large text = %q, want both the rank and the arena", a.Assets.LargeText)
	}
}

// A mode with no configured rank keeps the logo, so a 1v1 rank doesn't leak
// onto a 3v3 card.
func TestRankBadgeOnlyAppliesToItsMode(t *testing.T) {
	p := newTestPresence(nil)
	p.ranks = NewConfigRanks(map[string]string{"3v3": "Supersonic Legend"})
	p.Observe(tick(1, 0)) // tick() is a 2v2

	p.mu.Lock()
	a := p.buildLocked()
	p.mu.Unlock()

	if a.Assets.LargeImage != DefaultAssets().Logo {
		t.Errorf("large image = %q, want the default logo", a.Assets.LargeImage)
	}
}

func TestConfigRanksDropsBadEntries(t *testing.T) {
	ranks := NewConfigRanks(map[string]string{
		"2v2":  "Champion I",
		"3v3":  "Wood V", // not a rank
		"1v1":  "",       // no rank given
		"":     "ssl",    // no mode given
		"4V4 ": "gc3",    // case and spacing are forgiven
	})

	for _, tc := range []struct {
		mode string
		tier int
		want bool
	}{
		{"2v2", 16, true},
		{"4v4", 21, true},
		{"3v3", 0, false},
		{"1v1", 0, false},
		{"", 0, false},
	} {
		tier, label, ok := ranks.TierFor(tc.mode)
		if ok != tc.want {
			t.Errorf("TierFor(%q) ok = %v, want %v", tc.mode, ok, tc.want)
			continue
		}
		if !ok {
			continue
		}
		if tier != tc.tier {
			t.Errorf("TierFor(%q) tier = %d, want %d", tc.mode, tier, tc.tier)
		}
		if label != rankNames[tc.tier] {
			t.Errorf("TierFor(%q) label = %q, want %q", tc.mode, label, rankNames[tc.tier])
		}
	}
}

// A configured rank cannot change, so the match-end hint must be a harmless
// no-op rather than anything that could fail.
func TestConfigRanksMatchEndedIsInert(t *testing.T) {
	ranks := NewConfigRanks(map[string]string{"2v2": "Champion I"})
	ranks.MatchEnded()
	if err := ranks.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
	if _, _, ok := ranks.TierFor("2v2"); !ok {
		t.Error("rank lost after MatchEnded")
	}
}
