package discord

import (
	"errors"
	"os"
	"testing"
)

// TestLiveHandshake talks to the Discord client actually running on this
// machine, to check the framing against the real thing rather than a fake.
// It deliberately hands over a bogus client ID: a rejection proves the frames
// were written and read correctly, without setting any presence.
//
// Opt in with LOBBYIQ_TEST_DISCORD=1, since an ordinary `go test` shouldn't
// reach out to whatever is running on the developer's desktop.
func TestLiveHandshake(t *testing.T) {
	if os.Getenv("LOBBYIQ_TEST_DISCORD") != "1" {
		t.Skip("set LOBBYIQ_TEST_DISCORD=1 to test against a running discord client")
	}

	conn, err := dialIPC()
	if err != nil {
		t.Skipf("discord is not running: %v", err)
	}
	defer conn.Close()

	err = handshake(conn, "000000000000000000")
	if err == nil {
		t.Fatal("discord accepted a bogus client id, want a rejection")
	}
	// Getting this specific error back means the handshake frame was written
	// correctly and Discord's reply frame was read and decoded correctly.
	if !errors.Is(err, ErrInvalidClientID) {
		t.Fatalf("got %v, want ErrInvalidClientID", err)
	}
	t.Logf("discord rejected the bogus id as expected: %v", err)
}
