package github

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"
)

const wf = ".github/workflows/samsara-merge-group.yml"

// manifestAPI is a scripted provider for one merge group.
type manifestAPI struct {
	runStatus, runConclusion, runHead string
	headBlob, baseBlob                string
	manifest                          string
	digest                            string // "" computes the real digest
	files                             map[string]string
	transient                         bool
}

func goodManifestAPI() *manifestAPI {
	return &manifestAPI{runStatus: "completed", runConclusion: "success", runHead: "g1", headBlob: "wf1", baseBlob: "wf1",
		manifest: `{"repository":"o/r","run_id":901,"run_attempt":1,"event":"merge_group","head_sha":"g1","base_sha":"b1","head_ref":"refs/heads/gh-readonly-queue/main/pr-7-g1"}`}
}

func (m *manifestAPI) zip() []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := m.files
	if files == nil {
		files = map[string]string{ManifestFile: m.manifest}
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names) // listing and download must see the same bytes
	for _, name := range names {
		w, _ := zw.Create(name)
		w.Write([]byte(files[name]))
	}
	_ = zw.Close()
	return buf.Bytes()
}

func (m *manifestAPI) Do(_ context.Context, method, path string, _ map[string]string) (*Response, error) {
	ok := func(body string) (*Response, error) {
		return &Response{Status: 200, Header: http.Header{}, Body: []byte(body)}, nil
	}
	if m.transient {
		return nil, &Error{Status: StatusProviderError, Message: "connection reset"}
	}
	p, raw, _ := strings.Cut(path, "?")
	q, _ := url.ParseQuery(raw)
	switch p {
	case "repos/o/r/actions/runs":
		if q.Get("event") != "merge_group" || q.Get("head_sha") != "g1" || m.runHead == "" {
			return ok(`{"workflow_runs":[]}`)
		}
		return ok(fmt.Sprintf(`{"workflow_runs":[
			{"id":7,"path":".github/workflows/ci.yml","event":"merge_group","head_sha":"g1","status":"completed","conclusion":"success","repository":{"full_name":"o/r"}},
			{"id":901,"path":"%s@refs/heads/gh-readonly-queue/main/pr-7-g1","event":"merge_group","head_sha":%q,"status":%q,"conclusion":%q,"run_attempt":1,"repository":{"full_name":"o/r"}}]}`,
			wf, m.runHead, m.runStatus, m.runConclusion))
	case "repos/o/r/contents/" + wf:
		sha := map[string]string{"g1": m.headBlob, "b1": m.baseBlob}[q.Get("ref")]
		if sha == "" {
			return &Response{Status: 404, Header: http.Header{}, Body: []byte(`{"message":"Not Found"}`)}, nil
		}
		return ok(fmt.Sprintf(`{"sha":%q}`, sha))
	case "repos/o/r/actions/runs/901/artifacts":
		if q.Get("name") != DefaultManifestArtifact {
			return ok(`{"artifacts":[]}`)
		}
		z := m.zip()
		d := m.digest
		if d == "" {
			sum := sha256.Sum256(z)
			d = "sha256:" + hex.EncodeToString(sum[:])
		}
		return ok(fmt.Sprintf(`{"artifacts":[{"id":501,"name":%q,"size_in_bytes":%d,"expired":false,"digest":%q}]}`, DefaultManifestArtifact, len(z), d))
	case "repos/o/r/actions/artifacts/501/zip":
		return &Response{Status: 200, Header: http.Header{}, Body: m.zip()}, nil
	}
	return &Response{Status: 404, Header: http.Header{}, Body: []byte(`{"message":"Not Found"}`)}, nil
}

func verify(t *testing.T, m *manifestAPI) (*ManifestResult, error) {
	t.Helper()
	spec := ManifestSpec{Repository: "o/r", Branch: "main", Workflow: wf, Artifact: DefaultManifestArtifact}
	return VerifyMergeGroupManifest(context.Background(), m, spec, "g1", "b1", time.Now())
}

// 04 §10.8 steps 1–3: the trusted collector run of exactly this group,
// with an unmodified definition and an intact artifact, verifies it.
func TestVerifyMergeGroupManifest(t *testing.T) {
	r, err := verify(t, goodManifestAPI())
	if err != nil || r.State != ManifestVerified || r.RunID != "901" || r.WorkflowBlob != "wf1" {
		t.Fatalf("verified: %+v %v", r, err)
	}
	cases := map[string]struct {
		mut   func(m *manifestAPI)
		state string
		why   string
	}{
		"no run yet":         {func(m *manifestAPI) { m.runHead = "" }, ManifestPending, "MANIFEST_RUN_NOT_FOUND"},
		"run in progress":    {func(m *manifestAPI) { m.runStatus, m.runConclusion = "in_progress", "" }, ManifestPending, "MANIFEST_RUN_IN_PROGRESS"},
		"run failed":         {func(m *manifestAPI) { m.runConclusion = "failure" }, ManifestFailed, "MANIFEST_RUN_FAILURE"},
		"workflow rewritten": {func(m *manifestAPI) { m.headBlob = "wf-evil" }, ManifestFailed, "MANIFEST_WORKFLOW_MODIFIED"},
		"workflow absent":    {func(m *manifestAPI) { m.baseBlob = "" }, ManifestPending, "MANIFEST_WORKFLOW_UNPINNED"},
		"stale attempt": {func(m *manifestAPI) {
			m.manifest = strings.Replace(m.manifest, `"run_attempt":1`, `"run_attempt":2`, 1)
		}, ManifestFailed, "MANIFEST_MISMATCH"},
		"digest mismatch": {func(m *manifestAPI) { m.digest = "sha256:00" }, ManifestFailed, "MANIFEST_ARTIFACT_DIGEST_MISMATCH"},
		"digest missing":  {func(m *manifestAPI) { m.digest = "-" }, ManifestFailed, "MANIFEST_ARTIFACT_DIGEST_MISSING"},
		"other head": {func(m *manifestAPI) {
			m.manifest = strings.Replace(m.manifest, `"head_sha":"g1"`, `"head_sha":"g0"`, 1)
		}, ManifestFailed, "MANIFEST_MISMATCH"},
		"other base": {func(m *manifestAPI) {
			m.manifest = strings.Replace(m.manifest, `"base_sha":"b1"`, `"base_sha":"b0"`, 1)
		}, ManifestFailed, "MANIFEST_MISMATCH"},
		"other run":          {func(m *manifestAPI) { m.manifest = strings.Replace(m.manifest, `"run_id":901`, `"run_id":900`, 1) }, ManifestFailed, "MANIFEST_MISMATCH"},
		"other branch queue": {func(m *manifestAPI) { m.manifest = strings.Replace(m.manifest, "queue/main/", "queue/dev/", 1) }, ManifestFailed, "MANIFEST_MISMATCH"},
		"unknown field":      {func(m *manifestAPI) { m.manifest = strings.Replace(m.manifest, `{`, `{"approved":true,`, 1) }, ManifestFailed, "MANIFEST_UNREADABLE"},
		"extra file":         {func(m *manifestAPI) { m.files = map[string]string{ManifestFile: m.manifest, "run.sh": "x"} }, ManifestFailed, "MANIFEST_ARTIFACT_CONTENT"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			m := goodManifestAPI()
			tc.mut(m)
			r, err := verify(t, m)
			if err != nil || r.State != tc.state || r.Reason != tc.why {
				t.Fatalf("want %s(%s), got %+v %v", tc.state, tc.why, r, err)
			}
		})
	}
	m := goodManifestAPI()
	m.transient = true
	if r, err := verify(t, m); err == nil || r != nil {
		t.Fatalf("a transport failure is not a verdict: %+v %v", r, err)
	}
}
