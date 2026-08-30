package discord

import (
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestFrameRoundTrip(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	go func() {
		_ = writeFrame(client, opFrame, []byte(`{"cmd":"SET_ACTIVITY"}`))
	}()

	op, payload, err := readFrame(server)
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	if op != opFrame {
		t.Errorf("op = %d, want %d", op, opFrame)
	}
	if got := string(payload); got != `{"cmd":"SET_ACTIVITY"}` {
		t.Errorf("payload = %q", got)
	}
}

func TestReadFrameRejectsOversizedPayload(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	go func() {
		// A length header far beyond maxPayload, with no body behind it.
		header := make([]byte, 8)
		header[4], header[5], header[6], header[7] = 0xff, 0xff, 0xff, 0xff
		_, _ = client.Write(header)
	}()

	if _, _, err := readFrame(server); err == nil {
		t.Fatal("readFrame accepted an oversized payload, want an error")
	}
}

// fakeDiscord answers a handshake with evt, as Discord's IPC socket would.
func fakeDiscord(t *testing.T, server net.Conn, evt string) chan string {
	t.Helper()
	gotClientID := make(chan string, 1)

	go func() {
		op, payload, err := readFrame(server)
		if err != nil || op != opHandshake {
			gotClientID <- ""
			return
		}
		var req struct {
			ClientID string `json:"client_id"`
		}
		_ = json.Unmarshal(payload, &req)
		gotClientID <- req.ClientID

		_ = writeJSON(server, opFrame, map[string]any{
			"cmd":  "DISPATCH",
			"evt":  evt,
			"data": map[string]any{"message": "bad client id"},
		})
	}()

	return gotClientID
}

func TestHandshakeSucceedsOnReady(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	gotClientID := fakeDiscord(t, server, "READY")

	done := make(chan error, 1)
	go func() { done <- handshake(client, "123456") }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("handshake: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handshake timed out")
	}

	if id := <-gotClientID; id != "123456" {
		t.Errorf("client_id = %q, want %q", id, "123456")
	}
}

func TestHandshakeFailsOnError(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	fakeDiscord(t, server, "ERROR")

	done := make(chan error, 1)
	go func() { done <- handshake(client, "nope") }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("handshake accepted an ERROR reply, want an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handshake timed out")
	}
}

// Discord rejects an over-long details/state outright rather than truncating,
// so the clamp has to happen here - and must not split a multi-byte rune.
func TestClampKeepsValidUTF8(t *testing.T) {
	long := strings.Repeat("é", maxTextLen)

	got := clamp(long)
	if len(got) > maxTextLen {
		t.Errorf("clamped length = %d bytes, want <= %d", len(got), maxTextLen)
	}
	if strings.ContainsRune(got, '�') {
		t.Error("clamp split a multi-byte rune")
	}
	if short := clamp("hello"); short != "hello" {
		t.Errorf("clamp(%q) = %q, want it untouched", "hello", short)
	}
}

// Real Discord answers an unknown client ID with an op-2 close frame, not an
// op-1 ERROR dispatch, and that case has to be told apart from a transient
// failure because retrying it is pointless.
func TestHandshakeFailsOnInvalidClientIDCloseFrame(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	go func() {
		if _, _, err := readFrame(server); err != nil {
			return
		}
		_ = writeFrame(server, opClose, []byte(`{"code":4000,"message":"Invalid Client ID"}`))
	}()

	done := make(chan error, 1)
	go func() { done <- handshake(client, "nope") }()

	select {
	case err := <-done:
		if !errors.Is(err, ErrInvalidClientID) {
			t.Fatalf("got %v, want ErrInvalidClientID", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handshake timed out")
	}
}

// The app has to run fine without Discord: failures are soft, and repeated
// ones back off instead of dialing on every update.
func TestSetActivityFailsSoftly(t *testing.T) {
	c := New("000000000000000000")
	defer c.Close()

	var last error
	for i := 0; i < 3; i++ {
		last = c.SetActivity(Activity{Details: "test"})
		if last == nil {
			t.Fatal("a bogus client id was accepted, want a failure")
		}
	}

	c.mu.Lock()
	backoff, closed := c.backoff, c.closed
	c.mu.Unlock()

	// Which failure occurs depends on whether Discord is running on the test
	// machine: no Discord means dial failures and a growing backoff, while a
	// running Discord rejects the bogus ID and the client stands down.
	switch {
	case errors.Is(last, ErrClosed):
		if !closed {
			t.Error("client reported ErrClosed but is not marked closed")
		}
	case errors.Is(last, ErrUnavailable):
		if backoff <= time.Second {
			t.Errorf("backoff = %v, want it to have grown after repeated failures", backoff)
		}
	default:
		t.Errorf("got %v, want ErrUnavailable or ErrClosed", last)
	}
}
