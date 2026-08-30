package discord

import (
	"os"
	"testing"
	"time"
)

// TestLiveSetActivity publishes a real presence card to the Discord client on
// this machine and then clears it, which is the only way to check the parts
// the fakes can't: that the client ID is accepted, that SET_ACTIVITY is
// well-formed enough for Discord to render, and - the reason this test exists
// - whether an https URL works in place of an uploaded asset key.
//
// Needs a real application client ID, so it's opt-in twice over:
//
//	LOBBYIQ_TEST_DISCORD=1 LOBBYIQ_TEST_CLIENT_ID=<id> go test ./internal/discord -run LiveSetActivity -v
func TestLiveSetActivity(t *testing.T) {
	if os.Getenv("LOBBYIQ_TEST_DISCORD") != "1" {
		t.Skip("set LOBBYIQ_TEST_DISCORD=1 to test against a running discord client")
	}
	clientID := os.Getenv("LOBBYIQ_TEST_CLIENT_ID")
	if clientID == "" {
		t.Skip("set LOBBYIQ_TEST_CLIENT_ID to a real discord application id")
	}

	c := New(clientID)
	defer c.Close()

	err := c.SetActivity(Activity{
		Details: "2v2 · Blue 3 - 1 Orange",
		State:   "540 pts · 2G 1A 3S 4Sh · Session +2",
		Assets: &Assets{
			// An external URL rather than an uploaded key, on purpose - and
			// the same rank-badge URL the presence package builds.
			LargeImage: "https://trackercdn.com/cdn/tracker.gg/rocket-league/ranks/s4-16.png",
			LargeText:  "Champion I · Blackout",
			SmallImage: "team_blue",
			SmallText:  "Blue",
		},
		Timestamps: &Timestamps{Start: time.Now().Add(-2 * time.Minute).Unix()},
		Party:      &Party{ID: "test-guid", Size: []int{4, 4}},
	})
	if err != nil {
		t.Fatalf("SetActivity: %v", err)
	}

	// Long enough to eyeball the card in Discord before it disappears.
	time.Sleep(20 * time.Second)

	if err := c.ClearActivity(); err != nil {
		t.Fatalf("ClearActivity: %v", err)
	}
	t.Log("published and cleared a presence card; check Discord to confirm the images rendered")
}
