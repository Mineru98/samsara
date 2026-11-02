package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Cursors are the incremental positions of one repository: the newest
// `updated_at` of issues/PRs and the newest commit date already read.
type Cursors struct {
	Issues  string
	Commits string
}

// Item is one external object for the history projection.
type Item struct {
	Kind   string // issue | pull_request | commit | check | review
	Key    string
	URL    string
	Title  string
	State  string
	Detail map[string]any
}

// Result of one sync pass.
type Result struct {
	Items   []Item
	Cursors Cursors
	// Warnings name PRs whose checks or reviews could not be read; the
	// rest of the pass is still valid.
	Warnings []string
}

// Limits bound the calls made by one pass.
type Limits struct {
	IssuePages  int // pages of 100 issues/PRs
	CommitPages int // pages of 100 commits
	PRDetails   int // PRs whose checks and reviews are read
}

// DefaultLimits keep one pass well under the hourly API budget.
var DefaultLimits = Limits{IssuePages: 3, CommitPages: 3, PRDetails: 20}

// commitOverlap re-reads commits slightly older than the cursor: `since`
// filters on committer date, and merged branches can carry commits dated
// before the previous pass.
const commitOverlap = 24 * time.Hour

// Sync reads issues, PRs, commits and, for recently updated PRs plus the
// open PRs in openPRs, their check runs and reviews (a finished check does
// not bump the PR's updated_at). A failure of a repository-level call or a
// rate limit aborts the pass without a partial result, so cursors only
// advance over fully read data; a failure on one PR's details only skips
// that PR and is reported in Warnings.
func Sync(ctx context.Context, c Client, repo string, cur Cursors, openPRs []int, lim Limits, now time.Time) (*Result, error) {
	res := &Result{Cursors: cur}
	get := func(path string, v any) error {
		r, err := c.Do(ctx, "GET", path, nil)
		if err != nil {
			return err
		}
		if err := Classify(r, now); err != nil {
			return err
		}
		if err := json.Unmarshal(r.Body, v); err != nil {
			return &Error{Status: StatusProviderError, Message: "decode " + strings.SplitN(path, "?", 2)[0] + ": " + err.Error()}
		}
		return nil
	}

	type ghIssue struct {
		Number      int    `json:"number"`
		Title       string `json:"title"`
		State       string `json:"state"`
		HTMLURL     string `json:"html_url"`
		UpdatedAt   string `json:"updated_at"`
		PullRequest *struct {
			MergedAt *string `json:"merged_at"`
		} `json:"pull_request"`
	}
	var prs []int
	for page := 1; page <= lim.IssuePages; page++ {
		q := url.Values{"state": {"all"}, "sort": {"updated"}, "direction": {"asc"}, "per_page": {"100"}, "page": {fmt.Sprint(page)}}
		if cur.Issues != "" {
			q.Set("since", cur.Issues)
		}
		var batch []ghIssue
		if err := get("repos/"+repo+"/issues?"+q.Encode(), &batch); err != nil {
			return nil, err
		}
		for _, is := range batch {
			it := Item{Kind: "issue", Key: fmt.Sprintf("%s#%d", repo, is.Number), URL: is.HTMLURL, Title: is.Title, State: is.State,
				Detail: map[string]any{"number": is.Number, "updated_at": is.UpdatedAt}}
			if is.PullRequest != nil {
				it.Kind = "pull_request"
				if is.PullRequest.MergedAt != nil {
					it.State = "merged"
				}
				prs = append(prs, is.Number)
			}
			res.Items = append(res.Items, it)
			if is.UpdatedAt > res.Cursors.Issues {
				res.Cursors.Issues = is.UpdatedAt
			}
		}
		if len(batch) < 100 {
			break
		}
	}

	type ghCommit struct {
		SHA     string `json:"sha"`
		HTMLURL string `json:"html_url"`
		Commit  struct {
			Message   string `json:"message"`
			Committer struct {
				Date string `json:"date"`
			} `json:"committer"`
			Author struct {
				Name string `json:"name"`
			} `json:"author"`
		} `json:"commit"`
	}
	since := ""
	if t, err := time.Parse(time.RFC3339, cur.Commits); err == nil {
		since = t.Add(-commitOverlap).UTC().Format(time.RFC3339)
	}
	var commits []ghCommit
	for page := 1; page <= lim.CommitPages; page++ {
		q := url.Values{"per_page": {"100"}, "page": {fmt.Sprint(page)}}
		if since != "" {
			q.Set("since", since)
		}
		var batch []ghCommit
		if err := get("repos/"+repo+"/commits?"+q.Encode(), &batch); err != nil {
			return nil, err
		}
		commits = append(commits, batch...)
		if len(batch) < 100 {
			break
		}
	}
	for _, cm := range commits {
		res.Items = append(res.Items, Item{Kind: "commit", Key: cm.SHA, URL: cm.HTMLURL, Title: strings.SplitN(cm.Commit.Message, "\n", 2)[0],
			Detail: map[string]any{"repository": repo, "committed_at": cm.Commit.Committer.Date, "author": cm.Commit.Author.Name}})
		if cm.Commit.Committer.Date > res.Cursors.Commits {
			res.Cursors.Commits = cm.Commit.Committer.Date
		}
	}

	// Issues come oldest-first; the newest PRs are at the end. Open PRs
	// already known follow, so pending checks reach a final state.
	seen := map[int]bool{}
	var detail []int
	for i := len(prs) - 1; i >= 0; i-- {
		if !seen[prs[i]] {
			seen[prs[i]] = true
			detail = append(detail, prs[i])
		}
	}
	for _, n := range openPRs {
		if !seen[n] {
			seen[n] = true
			detail = append(detail, n)
		}
	}
	if len(detail) > lim.PRDetails {
		detail = detail[:lim.PRDetails]
	}
	// soft keeps a per-PR failure from failing the pass; a rate limit still
	// aborts so the backoff applies.
	soft := func(n int, err error) error {
		if e := AsError(err); e.Status == StatusRateLimited {
			return e
		}
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s#%d: %s", repo, n, AsError(err).Error()))
		return nil
	}
PRS:
	for _, n := range detail {
		var pr struct {
			Draft bool `json:"draft"`
			Head  struct {
				SHA string `json:"sha"`
				Ref string `json:"ref"`
			} `json:"head"`
		}
		if err := get(fmt.Sprintf("repos/%s/pulls/%d", repo, n), &pr); err != nil {
			if err := soft(n, err); err != nil {
				return nil, err
			}
			continue
		}
		key := fmt.Sprintf("%s#%d", repo, n)
		for i := range res.Items {
			if res.Items[i].Kind == "pull_request" && res.Items[i].Key == key {
				res.Items[i].Detail["head_sha"], res.Items[i].Detail["head_ref"], res.Items[i].Detail["draft"] = pr.Head.SHA, pr.Head.Ref, pr.Draft
			}
		}
		var checks struct {
			CheckRuns []struct {
				ID         int64   `json:"id"`
				Name       string  `json:"name"`
				Status     string  `json:"status"`
				Conclusion *string `json:"conclusion"`
				HTMLURL    string  `json:"html_url"`
			} `json:"check_runs"`
		}
		if pr.Head.SHA != "" {
			if err := get(fmt.Sprintf("repos/%s/commits/%s/check-runs?per_page=100", repo, pr.Head.SHA), &checks); err != nil {
				if err := soft(n, err); err != nil {
					return nil, err
				}
				continue PRS
			}
		}
		for _, cr := range checks.CheckRuns {
			state := cr.Status
			if cr.Conclusion != nil && *cr.Conclusion != "" {
				state = *cr.Conclusion
			}
			res.Items = append(res.Items, Item{Kind: "check", Key: fmt.Sprintf("%s@%s/check/%d", repo, pr.Head.SHA, cr.ID), URL: cr.HTMLURL, Title: cr.Name, State: state,
				Detail: map[string]any{"pull_request": key, "head_sha": pr.Head.SHA, "status": cr.Status}})
		}
		var reviews []struct {
			ID          int64  `json:"id"`
			State       string `json:"state"`
			HTMLURL     string `json:"html_url"`
			CommitID    string `json:"commit_id"`
			SubmittedAt string `json:"submitted_at"`
			User        struct {
				Login string `json:"login"`
			} `json:"user"`
		}
		if err := get(fmt.Sprintf("repos/%s/pulls/%d/reviews?per_page=100", repo, n), &reviews); err != nil {
			if err := soft(n, err); err != nil {
				return nil, err
			}
			continue
		}
		for _, rv := range reviews {
			res.Items = append(res.Items, Item{Kind: "review", Key: fmt.Sprintf("%s/review/%d", key, rv.ID), URL: rv.HTMLURL, Title: "review by " + rv.User.Login, State: rv.State,
				Detail: map[string]any{"pull_request": key, "commit_id": rv.CommitID, "submitted_at": rv.SubmittedAt}})
		}
	}
	return res, nil
}

// CloseIssue closes "owner/name#N" as completed. It reads the issue first so
// a resend after an unknown outcome does not repeat the write.
func CloseIssue(ctx context.Context, c Client, key string, now time.Time) (alreadyClosed bool, err error) {
	repo, n, ok := ParseIssueKey(key)
	if !ok {
		return false, &Error{Status: StatusRepoNotFound, Message: "not an issue key: " + key}
	}
	path := "repos/" + repo + "/issues/" + n
	r, err := c.Do(ctx, "GET", path, nil)
	if err != nil {
		return false, err
	}
	if err := Classify(r, now); err != nil {
		return false, err
	}
	var is struct {
		State       string    `json:"state"`
		PullRequest *struct{} `json:"pull_request"`
	}
	if err := json.Unmarshal(r.Body, &is); err != nil {
		return false, &Error{Status: StatusProviderError, Message: "decode issue: " + err.Error()}
	}
	if is.PullRequest != nil {
		return false, &Error{Status: StatusRepoNotFound, Message: key + " is a pull request"}
	}
	if is.State == "closed" {
		return true, nil
	}
	r, err = c.Do(ctx, "PATCH", path, map[string]string{"state": "closed", "state_reason": "completed"})
	if err != nil {
		return false, err
	}
	return false, Classify(r, now)
}
