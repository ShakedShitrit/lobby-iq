// Package discord publishes a Rich Presence activity to a Discord client
// running on this machine, over Discord's local IPC socket.
//
// The wire protocol is deliberately small, so it's spoken directly rather
// than pulled in as a dependency: frames are a little-endian uint32 opcode,
// a little-endian uint32 payload length, then that many bytes of JSON.
//
// The activity's *name* ("Playing <something>") is not sent by us - Discord
// takes it from the name of the application the client_id belongs to. To
// show "Playing Rocket League", the Discord application must itself be named
// "Rocket League"; see DISCORD.md.
package discord

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Frame opcodes.
const (
	opHandshake = 0
	opFrame     = 1
	opClose     = 2
	opPing      = 3
	opPong      = 4
)

// maxPayload bounds an inbound frame so a desynced or hostile socket can't
// make us allocate arbitrarily. Real frames are a few hundred bytes.
const maxPayload = 64 << 10

// maxTextLen is Discord's limit on the details/state strings. Longer values
// are rejected outright rather than truncated by Discord, so clamp locally.
const maxTextLen = 128

// ErrUnavailable is returned when Discord isn't reachable - not running, or
// a previous connection failed and the retry backoff hasn't elapsed. It is
// expected during normal operation and isn't worth surfacing to the user.
var ErrUnavailable = errors.New("discord is not available")

// ErrClosed means the client has shut down and will not reconnect, either
// because Close was called or because the client ID was rejected outright.
var ErrClosed = errors.New("discord client is closed")

// ErrRejected means Discord accepted the connection but refused this
// particular activity - an over-long field, or an image URL it wouldn't load.
// The connection stays usable, so a corrected activity can be sent after it.
var ErrRejected = errors.New("discord rejected the activity")

type Timestamps struct {
	// Start makes Discord render an "elapsed" counter, End a "remaining"
	// one. Both are Unix seconds.
	Start int64 `json:"start,omitempty"`
	End   int64 `json:"end,omitempty"`
}

type Assets struct {
	// LargeImage/SmallImage are the *keys* of art assets uploaded to the
	// Discord application (or https URLs), not local file paths.
	LargeImage string `json:"large_image,omitempty"`
	LargeText  string `json:"large_text,omitempty"`
	SmallImage string `json:"small_image,omitempty"`
	SmallText  string `json:"small_text,omitempty"`
}

type Party struct {
	ID string `json:"id,omitempty"`
	// Size is [current, max]; Discord renders it as "(2 of 4)".
	Size []int `json:"size,omitempty"`
}

type Button struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// Activity is the presence payload. Every field is optional; zero values are
// omitted so a sparse Activity is valid.
type Activity struct {
	Details    string      `json:"details,omitempty"`
	State      string      `json:"state,omitempty"`
	Timestamps *Timestamps `json:"timestamps,omitempty"`
	Assets     *Assets     `json:"assets,omitempty"`
	Party      *Party      `json:"party,omitempty"`
	Buttons    []Button    `json:"buttons,omitempty"`
}

// Client maintains a lazily-established connection to Discord. It is safe for
// concurrent use, and every method is a no-op returning ErrUnavailable while
// Discord is unreachable - callers can keep pushing activities and the
// connection re-establishes itself once Discord comes back.
type Client struct {
	clientID string

	mu      sync.Mutex
	conn    io.ReadWriteCloser
	retryAt time.Time
	backoff time.Duration
	nonce   int
	closed  bool
}

// readTimeout bounds the wait for Discord's reply to a command. The pipe
// handle has no read deadline, so the only way to unblock a stuck read is to
// close the handle out from under it.
const readTimeout = 10 * time.Second

func New(clientID string) *Client {
	return &Client{clientID: clientID, backoff: time.Second}
}

// SetActivity publishes a, replacing any activity previously set by this
// application.
func (c *Client) SetActivity(a Activity) error {
	a.Details = clamp(a.Details)
	a.State = clamp(a.State)
	return c.send(map[string]any{"pid": os.Getpid(), "activity": a})
}

// ClearActivity removes this application's presence, leaving the user with no
// "Playing" line at all.
func (c *Client) ClearActivity() error {
	return c.send(map[string]any{"pid": os.Getpid(), "activity": nil})
}

// Close clears the presence and shuts the connection down. Further calls are
// no-ops.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	live := c.conn != nil
	c.mu.Unlock()

	if live {
		// Best effort: Discord clears the presence itself when the socket
		// drops, so a failure here isn't worth reporting.
		_ = c.ClearActivity()
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return c.dropLocked(nil)
}

func (c *Client) send(args any) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrClosed
	}
	if err := c.connectLocked(); err != nil {
		return err
	}

	c.nonce++
	payload := struct {
		Cmd   string `json:"cmd"`
		Args  any    `json:"args"`
		Nonce string `json:"nonce"`
	}{Cmd: "SET_ACTIVITY", Args: args, Nonce: strconv.Itoa(c.nonce)}

	if err := writeJSON(c.conn, opFrame, payload); err != nil {
		_ = c.dropLocked(err)
		return err
	}

	// Read the reply before returning, rather than draining the socket from a
	// second goroutine. On Windows the pipe is a synchronous handle, and Go
	// serializes reads and writes to one of those behind a single lock - so a
	// reader parked in a blocking read would deadlock every later write.
	if err := c.awaitReplyLocked(); err != nil {
		// A refused activity says nothing about the connection, so keep it.
		if !errors.Is(err, ErrRejected) {
			_ = c.dropLocked(err)
		}
		return err
	}
	return nil
}

// awaitReplyLocked reads frames until Discord answers the command just sent.
func (c *Client) awaitReplyLocked() error {
	conn := c.conn

	// Closing the handle is what aborts a blocking read; the read then returns
	// an error and the connection is torn down by the caller.
	watchdog := time.AfterFunc(readTimeout, func() { _ = conn.Close() })
	defer watchdog.Stop()

	for {
		op, data, err := readFrame(conn)
		if err != nil {
			return err
		}
		switch op {
		case opFrame:
			var reply struct {
				Evt  string `json:"evt"`
				Data struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"data"`
			}
			if err := json.Unmarshal(data, &reply); err != nil {
				return fmt.Errorf("parsing reply: %w", err)
			}
			if reply.Evt == "ERROR" {
				// The connection is fine; only this activity was refused, so
				// report it without tearing anything down.
				return fmt.Errorf("%w: %s (code %d)", ErrRejected, reply.Data.Message, reply.Data.Code)
			}
			return nil
		case opClose:
			return closeReason(data)
		case opPing:
			if err := writeFrame(conn, opPong, data); err != nil {
				return err
			}
		}
	}
}

// connectLocked establishes and hands-shakes a connection if there isn't one,
// respecting the retry backoff so a Discord-less machine isn't hammered with
// a dial on every update.
func (c *Client) connectLocked() error {
	if c.conn != nil {
		return nil
	}
	if time.Now().Before(c.retryAt) {
		return ErrUnavailable
	}

	conn, err := dialIPC()
	if err != nil {
		c.backoffLocked()
		zap.L().Debug("discord: dial failed", zap.Error(err), zap.Duration("retry_in", c.backoff))
		return ErrUnavailable
	}

	if err := handshake(conn, c.clientID); err != nil {
		conn.Close()

		// A client ID Discord doesn't know will never start working, so give
		// up rather than logging the same warning every minute for the rest
		// of the session.
		if errors.Is(err, ErrInvalidClientID) {
			c.closed = true
			zap.L().Warn("discord: rich presence disabled",
				zap.String("client_id", c.clientID),
				zap.Error(err),
				zap.String("hint", "check discord_client_id against your application's ID; see DISCORD.md"))
			return err
		}

		c.backoffLocked()
		zap.L().Warn("discord: handshake failed", zap.Error(err), zap.Duration("retry_in", c.backoff))
		return ErrUnavailable
	}

	c.conn = conn
	c.backoff = time.Second
	c.retryAt = time.Time{}
	zap.L().Info("discord: connected", zap.String("client_id", c.clientID))
	return nil
}

func (c *Client) backoffLocked() {
	c.retryAt = time.Now().Add(c.backoff)
	if c.backoff < time.Minute {
		c.backoff *= 2
	}
}

func (c *Client) dropLocked(cause error) error {
	if c.conn == nil {
		return nil
	}
	if cause != nil {
		zap.L().Info("discord: disconnected", zap.Error(cause))
		c.backoffLocked()
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

// handshake performs the op-0 exchange that binds the socket to our
// application, and waits for Discord's READY dispatch before the connection
// is considered usable.
func handshake(conn io.ReadWriteCloser, clientID string) error {
	req := struct {
		V        int    `json:"v"`
		ClientID string `json:"client_id"`
	}{V: 1, ClientID: clientID}

	if err := writeJSON(conn, opHandshake, req); err != nil {
		return fmt.Errorf("writing handshake: %w", err)
	}

	// Discord answers immediately, so this read is bounded in practice. A
	// hung read unblocks when Close shuts the handle down.
	for {
		op, data, err := readFrame(conn)
		if err != nil {
			return fmt.Errorf("reading handshake reply: %w", err)
		}
		switch op {
		case opFrame:
			var reply struct {
				Cmd  string `json:"cmd"`
				Evt  string `json:"evt"`
				Data struct {
					Message string `json:"message"`
				} `json:"data"`
			}
			if err := json.Unmarshal(data, &reply); err != nil {
				return fmt.Errorf("parsing handshake reply: %w", err)
			}
			if reply.Evt == "ERROR" {
				return fmt.Errorf("discord rejected the client id: %s", reply.Data.Message)
			}
			if reply.Evt == "READY" {
				return nil
			}
		case opClose:
			return closeReason(data)
		case opPing:
			if err := writeFrame(conn, opPong, data); err != nil {
				return err
			}
		}
	}
}

// closeCodeInvalidClientID is what Discord answers a handshake with when it
// doesn't recognise the client ID - the one failure that retrying can never
// fix.
const closeCodeInvalidClientID = 4000

// ErrInvalidClientID means the configured client ID isn't a Discord
// application. Retrying is pointless; the configuration has to change.
var ErrInvalidClientID = errors.New("discord did not recognise the client id")

// closeReason decodes Discord's op-2 payload, which carries a code and a
// human-readable message, e.g. {"code":4000,"message":"Invalid Client ID"}.
func closeReason(payload []byte) error {
	var body struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(payload, &body); err != nil || body.Message == "" {
		return fmt.Errorf("discord closed the connection: %s", payload)
	}
	if body.Code == closeCodeInvalidClientID {
		return fmt.Errorf("%w: %s", ErrInvalidClientID, body.Message)
	}
	return fmt.Errorf("discord closed the connection: %s (code %d)", body.Message, body.Code)
}

func writeJSON(w io.Writer, op uint32, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return writeFrame(w, op, b)
}

func writeFrame(w io.Writer, op uint32, payload []byte) error {
	buf := make([]byte, 8+len(payload))
	binary.LittleEndian.PutUint32(buf[0:4], op)
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(payload)))
	copy(buf[8:], payload)

	// One Write for the whole frame: a partial header followed by an error
	// would leave the socket desynced with no way to recover.
	_, err := w.Write(buf)
	return err
}

func readFrame(r io.Reader) (op uint32, payload []byte, err error) {
	var header [8]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, err
	}

	op = binary.LittleEndian.Uint32(header[0:4])
	length := binary.LittleEndian.Uint32(header[4:8])
	if length > maxPayload {
		return 0, nil, fmt.Errorf("frame payload too large: %d bytes", length)
	}

	payload = make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return op, payload, nil
}

// clamp trims a string to Discord's field limit on a rune boundary, so a
// multi-byte character can't be cut in half into invalid UTF-8.
func clamp(s string) string {
	if len(s) <= maxTextLen {
		return s
	}
	runes := []rune(s)
	for len(string(runes)) > maxTextLen {
		runes = runes[:len(runes)-1]
	}
	return string(runes)
}
