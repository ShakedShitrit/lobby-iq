package rankicon

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// The two art sets cover different ranges, so picking the wrong one for a tier
// yields a 404 rather than the wrong picture.
func TestURLPicksTheRightSet(t *testing.T) {
	for tier := 0; tier <= MaxTier; tier++ {
		url := URL(tier)
		wantSet := "s4-"
		if tier >= TopTier {
			wantSet = "s15rank"
		}
		if !strings.Contains(url, wantSet) {
			t.Errorf("tier %d: %q, want it from the %q set", tier, url, wantSet)
		}
	}
}

// The whole point of embedding is that every rank draws without a network, so
// a missing file has to fail the build's tests rather than show up as one rank
// silently rendering as text months later.
func TestEveryTierHasArt(t *testing.T) {
	for tier := 1; tier <= MaxTier; tier++ {
		b, ok := Get(tier)
		if !ok {
			t.Errorf("tier %d: no embedded badge", tier)
			continue
		}
		if len(b) < 8 || string(b[1:4]) != "PNG" {
			t.Errorf("tier %d: embedded badge is not a PNG", tier)
		}
	}
}

// Unranked has no badge worth showing, and nothing exists past Supersonic
// Legend. Both must report absent so the caller renders text.
func TestTiersWithoutArt(t *testing.T) {
	for _, tier := range []int{-1, 0, MaxTier + 1, 99} {
		if _, ok := Get(tier); ok {
			t.Errorf("tier %d reported art", tier)
		}
		if Has(tier) {
			t.Errorf("Has(%d) = true", tier)
		}
	}
}

// TestURLsResolve checks the embedded art still matches what the CDN serves,
// which is how a set being moved or renamed gets noticed - Discord fetches
// those URLs itself and would start showing nothing. Opt-in, since the rest of
// the suite doesn't touch the network.
func TestURLsResolve(t *testing.T) {
	if os.Getenv("LOBBYIQ_TEST_NET") != "1" {
		t.Skip("set LOBBYIQ_TEST_NET=1 to check badge URLs against the CDN")
	}

	client := &http.Client{Timeout: 20 * time.Second}
	for tier := 0; tier <= MaxTier; tier++ {
		url := URL(tier)
		resp, err := client.Head(url)
		if err != nil {
			t.Errorf("tier %d: %v", tier, err)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("tier %d: %s returned %d", tier, url, resp.StatusCode)
		}
	}
}
