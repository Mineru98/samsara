package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/paths"
)

// AppCredentialFile is the file under the runtime state directory that
// points at the Samsara GitHub App credential. Only the daemon (the target
// controller) reads it; it never reaches the DB, an event or a worker
// prompt (04 §10.8).
const AppCredentialFile = "github-app.json"

// AppCredential identifies the App installation that publishes the policy
// check. The private key stays in its own file; this struct holds its path.
type AppCredential struct {
	AppID          int64  `json:"app_id"`
	InstallationID int64  `json:"installation_id"`
	PrivateKeyPath string `json:"private_key_path"`
	APIURL         string `json:"api_url,omitempty"`
}

// PolicyCheck is one publication of samsara/integration-policy on a merge
// group head (04 §10.7). Status is in_progress or completed; Conclusion is
// set only when completed. ExternalID binds the check to the exact group.
type PolicyCheck struct {
	Repository, HeadSHA, ExternalID, Status, Conclusion, Summary string
}

// AppPublisher publishes the policy check with a GitHub App installation
// token. The token lives only in memory and is never logged.
type AppPublisher struct {
	cred AppCredential
	key  *rsa.PrivateKey
	HTTP *http.Client
	Now  func() time.Time

	mu       sync.Mutex
	token    string
	tokenExp time.Time
}

// LoadAppPublisher reads the credential file of a state directory. It
// returns (nil, nil) when no credential is configured; a present but
// unusable credential is an error, never a silent publisher.
func LoadAppPublisher(stateDir string) (*AppPublisher, error) {
	path := filepath.Join(stateDir, AppCredentialFile)
	b, err := readPrivateFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cred AppCredential
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cred); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	if cred.AppID <= 0 || cred.InstallationID <= 0 {
		return nil, fmt.Errorf("%s: app_id and installation_id must be positive", path)
	}
	if !filepath.IsAbs(cred.PrivateKeyPath) {
		return nil, fmt.Errorf("%s: private_key_path must be absolute", path)
	}
	pemBytes, err := readPrivateFile(cred.PrivateKeyPath)
	if err != nil {
		return nil, err
	}
	key, err := parseRSAKey(pemBytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", cred.PrivateKeyPath, err)
	}
	return NewAppPublisher(cred, key), nil
}

// NewAppPublisher builds a publisher from an already loaded key.
func NewAppPublisher(cred AppCredential, key *rsa.PrivateKey) *AppPublisher {
	if cred.APIURL == "" {
		cred.APIURL = "https://api.github.com"
	}
	cred.APIURL = strings.TrimRight(cred.APIURL, "/")
	return &AppPublisher{cred: cred, key: key, HTTP: &http.Client{Timeout: 30 * time.Second}, Now: time.Now}
}

// readPrivateFile refuses a credential file another user could read or
// replace.
// The file is opened without following a symlink and checked through the
// open descriptor, so it cannot be swapped between check and read.
func readPrivateFile(path string) ([]byte, error) {
	if _, err := os.Lstat(path); err != nil {
		return nil, err
	}
	if err := paths.CheckPrivate(filepath.Dir(path)); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("%s: %v (symlinks are refused)", path, err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return nil, fmt.Errorf("%s is owned by uid %d, not the current user", path, st.Uid)
	}
	if !fi.Mode().IsRegular() || fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s must be a regular file not accessible by group or others (mode %o, want 0600)", path, fi.Mode().Perm())
	}
	return io.ReadAll(io.LimitReader(f, 1<<20))
}

func parseRSAKey(b []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, errors.New("no PEM private key")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("unreadable RSA private key")
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not RSA")
	}
	return rk, nil
}

// AppID is the App the published checks come from.
func (p *AppPublisher) AppID() int64 { return p.cred.AppID }

// InstallationID is the installation whose token is used.
func (p *AppPublisher) InstallationID() int64 { return p.cred.InstallationID }

// Verify obtains an installation token, proving the credential works.
func (p *AppPublisher) Verify(ctx context.Context) error {
	_, err := p.installationToken(ctx)
	return err
}

// jwt is the short-lived App assertion used only to obtain an
// installation token.
func (p *AppPublisher) jwt() (string, error) {
	now := p.Now()
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(9 * time.Minute).Unix(), "iss": strconv.FormatInt(p.cred.AppID, 10)})
	signing := header + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + enc.EncodeToString(sig), nil
}

func (p *AppPublisher) installationToken(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.token != "" && p.Now().Before(p.tokenExp.Add(-time.Minute)) {
		return p.token, nil
	}
	assertion, err := p.jwt()
	if err != nil {
		return "", err
	}
	// The token is narrowed to the one permission the controller needs.
	r, err := p.do(ctx, "POST", fmt.Sprintf("app/installations/%d/access_tokens", p.cred.InstallationID), "Bearer "+assertion,
		map[string]any{"permissions": map[string]string{"checks": "write"}})
	if err != nil {
		return "", err
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(r.Body, &out); err != nil || out.Token == "" {
		return "", &Error{Status: StatusProviderError, Message: "unreadable installation token response"}
	}
	p.token, p.tokenExp = out.Token, out.ExpiresAt
	return p.token, nil
}

func (p *AppPublisher) do(ctx context.Context, method, path, auth string, body any) (*Response, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.cred.APIURL+"/"+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", auth)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.HTTP.Do(req)
	if err != nil {
		// Transport errors carry the URL, never the Authorization header.
		return nil, &Error{Status: StatusProviderError, Message: Redact(err.Error())}
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, &Error{Status: StatusProviderError, Message: Redact(err.Error())}
	}
	r := &Response{Status: resp.StatusCode, Header: resp.Header, Body: b}
	if err := Classify(r, p.Now()); err != nil {
		return nil, err
	}
	return r, nil
}

// PublishPolicyCheck creates the check run of one group, or updates the run
// this App already created for the same external_id: a publication whose
// response was lost is recovered by reading instead of duplicating (04
// §10.8 step 7).
func (p *AppPublisher) PublishPolicyCheck(ctx context.Context, c PolicyCheck) (string, error) {
	tok, err := p.installationToken(ctx)
	if err != nil {
		return "", err
	}
	auth := "token " + tok
	q := url.Values{"check_name": {PolicyCheckName}, "app_id": {strconv.FormatInt(p.cred.AppID, 10)}, "filter": {"all"}, "per_page": {"100"}}
	r, err := p.do(ctx, "GET", "repos/"+c.Repository+"/commits/"+c.HeadSHA+"/check-runs?"+q.Encode(), auth, nil)
	if err != nil {
		return "", err
	}
	var list struct {
		Runs []struct {
			ID         int64  `json:"id"`
			ExternalID string `json:"external_id"`
			App        struct {
				ID int64 `json:"id"`
			} `json:"app"`
		} `json:"check_runs"`
	}
	if err := json.Unmarshal(r.Body, &list); err != nil {
		return "", &Error{Status: StatusProviderError, Message: "unreadable check runs: " + err.Error()}
	}
	body := map[string]any{"status": c.Status, "external_id": c.ExternalID,
		"output": map[string]string{"title": PolicyCheckName, "summary": c.Summary}}
	if c.Conclusion != "" {
		body["conclusion"] = c.Conclusion
	}
	for _, run := range list.Runs {
		if run.ExternalID == c.ExternalID && run.App.ID == p.cred.AppID {
			if _, err := p.do(ctx, "PATCH", fmt.Sprintf("repos/%s/check-runs/%d", c.Repository, run.ID), auth, body); err != nil {
				return "", err
			}
			return strconv.FormatInt(run.ID, 10), nil
		}
	}
	body["name"], body["head_sha"] = PolicyCheckName, c.HeadSHA
	r, err = p.do(ctx, "POST", "repos/"+c.Repository+"/check-runs", auth, body)
	if err != nil {
		return "", err
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(r.Body, &created); err != nil || created.ID == 0 {
		return "", &Error{Status: StatusProviderError, Message: "unreadable created check run"}
	}
	return strconv.FormatInt(created.ID, 10), nil
}
