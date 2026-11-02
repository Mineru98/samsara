package github

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

// Merge group manifest verdicts (04 §10.8 steps 1–3).
const (
	ManifestPending  = "PENDING"
	ManifestVerified = "VERIFIED"
	ManifestFailed   = "FAILED"
)

// DefaultManifestArtifact is the artifact name the collector Workflow
// uploads when the project does not configure another one.
const DefaultManifestArtifact = "samsara-merge-group-manifest"

// ManifestFile is the only file the manifest artifact may contain.
const ManifestFile = "manifest.json"

// maxManifestArtifactBytes bounds the downloaded artifact and the manifest
// inside it; a manifest is a few hundred bytes.
const maxManifestArtifactBytes = 64 << 10

// ManifestSpec pins where the trusted manifest comes from.
type ManifestSpec struct {
	Repository string // owner/name
	Branch     string // target branch the queue merges into
	Workflow   string // collector workflow path, e.g. .github/workflows/samsara-merge-group.yml
	Artifact   string // artifact name
}

// ManifestResult is the verdict for one merge group. RunID and
// WorkflowBlob identify the collector run and definition that were checked.
type ManifestResult struct {
	State        string `json:"state"`
	Reason       string `json:"reason,omitempty"`
	RunID        string `json:"run_id,omitempty"`
	WorkflowBlob string `json:"workflow_blob,omitempty"`
}

// mergeGroupManifest is what the collector writes from the merge_group
// event payload; it never runs repository code (04 §10.8 step 1).
type mergeGroupManifest struct {
	Repository string `json:"repository"`
	RunID      int64  `json:"run_id"`
	RunAttempt int64  `json:"run_attempt"`
	Event      string `json:"event"`
	HeadSHA    string `json:"head_sha"`
	BaseSHA    string `json:"base_sha"`
	HeadRef    string `json:"head_ref"`
}

// VerifyMergeGroupManifest checks the collector run of one merge group head
// against the provider API (04 §10.8 steps 1–3). A definite mismatch is
// FAILED with a reason; a run or artifact that does not exist yet is
// PENDING. A returned error is a transient read failure, never a verdict.
func VerifyMergeGroupManifest(ctx context.Context, c Client, s ManifestSpec, head, base string, now time.Time) (*ManifestResult, error) {
	get := func(path string) (*Response, error) {
		r, err := c.Do(ctx, "GET", path, nil)
		if err != nil {
			return nil, err
		}
		return r, Classify(r, now)
	}
	runID := ""
	fail := func(reason string) (*ManifestResult, error) {
		return &ManifestResult{State: ManifestFailed, Reason: reason, RunID: runID}, nil
	}
	repo := "repos/" + s.Repository

	q := url.Values{"event": {"merge_group"}, "head_sha": {head}, "per_page": {"100"}}
	r, err := get(repo + "/actions/runs?" + q.Encode())
	if err != nil {
		return nil, err
	}
	var runs struct {
		Runs []struct {
			ID         int64  `json:"id"`
			Path       string `json:"path"`
			Event      string `json:"event"`
			HeadSHA    string `json:"head_sha"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			RunAttempt int64  `json:"run_attempt"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
		} `json:"workflow_runs"`
	}
	if err := json.Unmarshal(r.Body, &runs); err != nil {
		return nil, &Error{Status: StatusProviderError, Message: "unreadable workflow runs: " + err.Error()}
	}
	idx := -1
	for i, run := range runs.Runs {
		path, _, _ := strings.Cut(run.Path, "@")
		if path == s.Workflow && (idx < 0 || run.ID > runs.Runs[idx].ID) {
			idx = i
		}
	}
	if idx < 0 {
		return &ManifestResult{State: ManifestPending, Reason: "MANIFEST_RUN_NOT_FOUND"}, nil
	}
	run := runs.Runs[idx]
	runID = fmt.Sprint(run.ID)
	if run.Event != "merge_group" || run.HeadSHA != head || !strings.EqualFold(run.Repository.FullName, s.Repository) {
		return fail("MANIFEST_RUN_MISMATCH")
	}
	if run.Status != "completed" {
		return &ManifestResult{State: ManifestPending, Reason: "MANIFEST_RUN_IN_PROGRESS", RunID: runID}, nil
	}
	if run.Conclusion != "success" {
		return fail("MANIFEST_RUN_" + strings.ToUpper(run.Conclusion))
	}

	// The merge_group run executes the Workflow as it exists in the group
	// head. Only a definition identical to the target base is trusted, so a
	// pull request cannot rewrite the collector (04 §10.8 step 3). The
	// collector must not reference other repository files (local actions,
	// scripts, reusable workflows); only this file is pinned.
	blob := func(ref string) (string, error) {
		r, err := c.Do(ctx, "GET", repo+"/contents/"+s.Workflow+"?ref="+url.QueryEscape(ref), nil)
		if err != nil {
			return "", err
		}
		if r.Status == 404 {
			return "", nil
		}
		if err := Classify(r, now); err != nil {
			return "", err
		}
		var f struct {
			SHA string `json:"sha"`
		}
		if err := json.Unmarshal(r.Body, &f); err != nil {
			return "", &Error{Status: StatusProviderError, Message: "unreadable workflow contents: " + err.Error()}
		}
		return f.SHA, nil
	}
	headBlob, err := blob(head)
	if err != nil {
		return nil, err
	}
	baseBlob, err := blob(base)
	if err != nil {
		return nil, err
	}
	if headBlob == "" || baseBlob == "" {
		// A missing definition may be a permission or lookup problem; it
		// never verifies, but it is not a final verdict either.
		return &ManifestResult{State: ManifestPending, Reason: "MANIFEST_WORKFLOW_UNPINNED", RunID: runID}, nil
	}
	if headBlob != baseBlob {
		return fail("MANIFEST_WORKFLOW_MODIFIED")
	}

	r, err = get(fmt.Sprintf("%s/actions/runs/%d/artifacts?name=%s&per_page=100", repo, run.ID, url.QueryEscape(s.Artifact)))
	if err != nil {
		return nil, err
	}
	var arts struct {
		Artifacts []struct {
			ID      int64  `json:"id"`
			Name    string `json:"name"`
			Size    int64  `json:"size_in_bytes"`
			Expired bool   `json:"expired"`
			Digest  string `json:"digest"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(r.Body, &arts); err != nil {
		return nil, &Error{Status: StatusProviderError, Message: "unreadable artifacts: " + err.Error()}
	}
	// A re-run attempt uploads again under the same name: the newest
	// artifact is read and its manifest must name the current attempt.
	ai := -1
	for i, a := range arts.Artifacts {
		if a.Name == s.Artifact && (ai < 0 || a.ID > arts.Artifacts[ai].ID) {
			ai = i
		}
	}
	if ai < 0 {
		return fail("MANIFEST_ARTIFACT_MISSING")
	}
	art := arts.Artifacts[ai]
	switch {
	case art.Expired:
		return fail("MANIFEST_ARTIFACT_EXPIRED")
	case art.Size <= 0 || art.Size > maxManifestArtifactBytes:
		return fail("MANIFEST_ARTIFACT_SIZE")
	case !strings.HasPrefix(art.Digest, "sha256:"):
		return fail("MANIFEST_ARTIFACT_DIGEST_MISSING")
	}
	r, err = get(fmt.Sprintf("%s/actions/artifacts/%d/zip", repo, art.ID))
	if err != nil {
		return nil, err
	}
	if len(r.Body) > maxManifestArtifactBytes {
		return fail("MANIFEST_ARTIFACT_SIZE")
	}
	sum := sha256.Sum256(r.Body)
	if hex.EncodeToString(sum[:]) != strings.ToLower(strings.TrimPrefix(art.Digest, "sha256:")) {
		return fail("MANIFEST_ARTIFACT_DIGEST_MISMATCH")
	}
	m, reason := readManifest(r.Body)
	if reason != "" {
		return fail(reason)
	}
	if !strings.EqualFold(m.Repository, s.Repository) || m.RunID != run.ID || m.RunAttempt != run.RunAttempt || m.Event != "merge_group" ||
		m.HeadSHA != head || m.BaseSHA != base || !strings.HasPrefix(m.HeadRef, "refs/heads/gh-readonly-queue/"+s.Branch+"/") {
		return fail("MANIFEST_MISMATCH")
	}
	return &ManifestResult{State: ManifestVerified, RunID: runID, WorkflowBlob: headBlob}, nil
}

// readManifest opens the artifact zip without extracting anything to disk.
func readManifest(b []byte) (*mergeGroupManifest, string) {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, "MANIFEST_ARTIFACT_UNREADABLE"
	}
	if len(zr.File) != 1 || zr.File[0].Name != ManifestFile || zr.File[0].UncompressedSize64 > maxManifestArtifactBytes {
		return nil, "MANIFEST_ARTIFACT_CONTENT"
	}
	f, err := zr.File[0].Open()
	if err != nil {
		return nil, "MANIFEST_ARTIFACT_UNREADABLE"
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxManifestArtifactBytes+1))
	if err != nil || len(raw) > maxManifestArtifactBytes {
		return nil, "MANIFEST_ARTIFACT_UNREADABLE"
	}
	m := &mergeGroupManifest{}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(m); err != nil {
		return nil, "MANIFEST_UNREADABLE"
	}
	return m, ""
}
