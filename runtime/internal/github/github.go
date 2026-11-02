// Package github reads GitHub state through the user's own credential
// provider (the `gh` CLI) for the history projection (01 §17). The runtime
// never reads, stores or forwards a token: `gh` owns authentication, and
// every error text leaving this package is redacted.
package github

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/textproto"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Sync statuses. They describe the provider connection, never the project:
// a missing or unreachable GitHub repository is not "project not found"
// (03 §7.3).
const (
	StatusOK            = "OK"
	StatusDisabled      = "DISABLED"
	StatusNotConfigured = "NOT_CONFIGURED"
	StatusAuthRequired  = "AUTH_REQUIRED"
	StatusRepoNotFound  = "REPO_NOT_FOUND"
	StatusRateLimited   = "RATE_LIMITED"
	StatusProviderError = "PROVIDER_ERROR"
)

// Response is one HTTP response relayed by the provider.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Client performs one GitHub REST call. fields are sent as string body
// parameters (write calls only).
type Client interface {
	Do(ctx context.Context, method, path string, fields map[string]string) (*Response, error)
}

// Error is a classified provider failure.
type Error struct {
	Status  string
	HTTP    int
	RetryAt time.Time // set for RATE_LIMITED
	Message string
}

func (e *Error) Error() string {
	if e.HTTP != 0 {
		return fmt.Sprintf("github %s (HTTP %d): %s", e.Status, e.HTTP, e.Message)
	}
	return fmt.Sprintf("github %s: %s", e.Status, e.Message)
}

// AsError extracts a classified error; unclassified errors become
// PROVIDER_ERROR so callers never mistake them for success.
func AsError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Status: StatusProviderError, Message: Redact(err.Error())}
}

// Classify turns a non-2xx response into an Error (nil for 2xx).
func Classify(r *Response, now time.Time) error {
	if r.Status >= 200 && r.Status < 300 {
		return nil
	}
	msg := Redact(strings.TrimSpace(string(firstBytes(r.Body, 300))))
	e := &Error{HTTP: r.Status, Message: msg}
	limited := r.Header.Get("Retry-After") != "" || r.Header.Get("X-RateLimit-Remaining") == "0"
	switch {
	case r.Status == http.StatusTooManyRequests || (r.Status == http.StatusForbidden && limited):
		e.Status, e.RetryAt = StatusRateLimited, retryAt(r.Header, now)
	case r.Status == http.StatusUnauthorized || r.Status == http.StatusForbidden:
		e.Status = StatusAuthRequired
	case r.Status == http.StatusNotFound || r.Status == http.StatusGone:
		e.Status = StatusRepoNotFound
	default:
		e.Status = StatusProviderError
	}
	return e
}

func retryAt(h http.Header, now time.Time) time.Time {
	if s, err := strconv.Atoi(h.Get("Retry-After")); err == nil && s >= 0 {
		return now.Add(time.Duration(s) * time.Second)
	}
	if s, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil && s > 0 {
		if t := time.Unix(s, 0); t.After(now) {
			return t
		}
	}
	return now.Add(time.Minute)
}

func firstBytes(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}

var secretRe = regexp.MustCompile(`(gh[pousr]_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{16,}|(?i:bearer|token)\s+[A-Za-z0-9_.\-]{16,})`)

// Redact removes anything shaped like a GitHub credential and bounds length.
func Redact(s string) string {
	s = secretRe.ReplaceAllString(s, "[REDACTED]")
	if len(s) > 500 {
		s = s[:500]
	}
	return s
}

// CLI calls the REST API through `gh api`, so authentication stays with the
// credential provider the user already configured.
type CLI struct {
	Binary  string
	Timeout time.Duration
}

// NewCLI returns a client using `gh` from PATH.
func NewCLI() *CLI { return &CLI{Binary: "gh", Timeout: 30 * time.Second} }

// Do runs `gh api --include` and parses the relayed HTTP response.
func (c *CLI) Do(ctx context.Context, method, path string, fields map[string]string) (*Response, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	args := []string{"api", "--include", "--method", method,
		"-H", "Accept: application/vnd.github+json", "-H", "X-GitHub-Api-Version: 2022-11-28", path}
	for k, v := range fields {
		args = append(args, "-f", k+"="+v)
	}
	cmd := exec.CommandContext(ctx, c.Binary, args...)
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "NO_COLOR=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	if errors.Is(runErr, exec.ErrNotFound) {
		return nil, &Error{Status: StatusNotConfigured, Message: "gh CLI not installed"}
	}
	if r, ok := parseIncluded(stdout.Bytes()); ok {
		return r, nil
	}
	msg := Redact(strings.TrimSpace(stderr.String()))
	var ee *exec.ExitError
	if errors.As(runErr, &ee) && ee.ExitCode() == 4 {
		// gh exits 4 when no credential is configured.
		return nil, &Error{Status: StatusAuthRequired, Message: msg}
	}
	if runErr == nil {
		runErr = errors.New("unparseable gh output")
	}
	return nil, &Error{Status: StatusProviderError, Message: Redact(runErr.Error() + ": " + msg)}
}

// parseIncluded splits `gh api --include` output into status, headers, body.
func parseIncluded(out []byte) (*Response, bool) {
	br := bufio.NewReader(bytes.NewReader(out))
	line, err := br.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "HTTP/") {
		return nil, false
	}
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return nil, false
	}
	code, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, false
	}
	mh, err := textproto.NewReader(br).ReadMIMEHeader()
	if err != nil && len(mh) == 0 {
		return nil, false
	}
	rest := new(bytes.Buffer)
	_, _ = rest.ReadFrom(br)
	return &Response{Status: code, Header: http.Header(mh), Body: rest.Bytes()}, true
}

var (
	remoteRe = regexp.MustCompile(`^(?:https?://(?:[^@/]+@)?github\.com/|ssh://git@github\.com(?::22)?/|git@github\.com:)([A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+?)(?:\.git)?/?$`)
	issueKey = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+)#([0-9]+)$`)
)

// RepoFromRemote extracts owner/name from a github.com remote URL.
func RepoFromRemote(url string) (string, bool) {
	m := remoteRe.FindStringSubmatch(strings.TrimSpace(url))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// ParseIssueKey splits "owner/name#N".
func ParseIssueKey(key string) (repo string, number string, ok bool) {
	m := issueKey.FindStringSubmatch(key)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}
