package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// APIError is an error response from the herdr socket API. It proves the
// server received and rejected the request: nothing was executed.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string { return "herdr " + e.Code + ": " + e.Message }

// SendError is a transport failure. Sent reports whether the request bytes
// were written before the failure: an unsent request definitely did not run,
// a sent one may have (lost acknowledgement).
type SendError struct {
	Sent bool
	Err  error
}

func (e *SendError) Error() string {
	if e.Sent {
		return "herdr response lost: " + e.Err.Error()
	}
	return "herdr not reached: " + e.Err.Error()
}

func (e *SendError) Unwrap() error { return e.Err }

// IsAPIError reports whether err is an API error with one of the codes.
func IsAPIError(err error, codes ...string) bool {
	var ae *APIError
	if !errors.As(err, &ae) {
		return false
	}
	for _, c := range codes {
		if ae.Code == c {
			return true
		}
	}
	return len(codes) == 0
}

// wasSent reports whether a failed call may have been executed.
func wasSent(err error) bool {
	var se *SendError
	return errors.As(err, &se) && se.Sent
}

// DefaultSocketPath is the socket of the herdr server the current user
// runs: HERDR_SOCKET_PATH when set, else $XDG_CONFIG_HOME/herdr/herdr.sock.
func DefaultSocketPath() string {
	if p := os.Getenv("HERDR_SOCKET_PATH"); p != "" {
		return p
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "herdr", "herdr.sock")
}

// Client talks to the herdr socket API: one JSON line request and one JSON
// line response per connection (protocol 22).
type Client struct {
	Socket  string
	Timeout time.Duration
}

type request struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params"`
}

type response struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *APIError       `json:"error"`
}

func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", c.Socket)
}

// Call sends one request and decodes the result into out (may be nil).
func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	if params == nil {
		params = struct{}{}
	}
	t := c.Timeout
	if t == 0 {
		t = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, t)
	defer cancel()
	conn, err := c.dial(ctx)
	if err != nil {
		return &SendError{Err: err}
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	b, err := json.Marshal(request{ID: "samsara:" + method, Method: method, Params: params})
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return &SendError{Err: err}
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return &SendError{Sent: true, Err: err}
	}
	var r response
	if err := json.Unmarshal(line, &r); err != nil {
		return &SendError{Sent: true, Err: fmt.Errorf("decode response: %w", err)}
	}
	if r.Error != nil {
		return r.Error
	}
	if out != nil {
		if err := json.Unmarshal(r.Result, out); err != nil {
			return &SendError{Sent: true, Err: fmt.Errorf("decode result: %w", err)}
		}
	}
	return nil
}

// Event is one subscription stream message.
type Event struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

// Subscribe opens a subscription and calls fn for each event until the
// stream ends or fn returns an error; the returned error says why it ended
// (never nil).
func (c *Client) Subscribe(ctx context.Context, subs []map[string]string, fn func(Event) error) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return &SendError{Err: err}
	}
	defer conn.Close()
	ended := make(chan struct{})
	defer close(ended)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-ended:
		}
	}()
	// The acknowledgement must arrive promptly; the stream itself may idle.
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	b, _ := json.Marshal(request{ID: "samsara:subscribe", Method: "events.subscribe", Params: map[string]any{"subscriptions": subs}})
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return &SendError{Err: err}
	}
	rd := bufio.NewReader(conn)
	line, err := rd.ReadBytes('\n')
	if err != nil {
		return &SendError{Sent: true, Err: err}
	}
	var r response
	if err := json.Unmarshal(line, &r); err != nil {
		return fmt.Errorf("decode subscribe ack: %w", err)
	}
	if r.Error != nil {
		return r.Error
	}
	var ack struct {
		Type string `json:"type"`
	}
	if _ = json.Unmarshal(r.Result, &ack); ack.Type != "subscription_started" {
		return fmt.Errorf("unexpected subscribe ack %q", ack.Type)
	}
	_ = conn.SetReadDeadline(time.Time{})
	if err := fn(Event{Event: "subscription_started"}); err != nil {
		return err
	}
	for {
		line, err := rd.ReadBytes('\n')
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("subscription closed: %w", err)
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil || ev.Event == "" {
			return fmt.Errorf("undecodable event: %s", line)
		}
		if err := fn(ev); err != nil {
			return err
		}
	}
}
