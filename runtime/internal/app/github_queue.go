package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Mineru98/samsara/runtime/internal/config"
	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/github"
	"github.com/Mineru98/samsara/runtime/internal/store/sqlite"
)

// PolicyCheck is one publication of samsara/integration-policy on a merge
// group head (04 §10.7).
type PolicyCheck = github.PolicyCheck

// PolicyCheckPublisher writes the required policy check with the verified
// GitHub App credential. Only the target controller holds it; workers never
// do. Without one, native integration stays unsupported (04 §10.9).
type PolicyCheckPublisher interface {
	PublishPolicyCheck(ctx context.Context, c PolicyCheck) (checkRunID string, err error)
}

// Native group check states.
const (
	groupInProgressRequested = "IN_PROGRESS_REQUESTED"
	groupInProgress          = "IN_PROGRESS"
	groupSuccessRequested    = "SUCCESS_REQUESTED"
	groupSuccess             = "SUCCESS"
	groupFailureRequested    = "FAILURE_REQUESTED"
	groupFailed              = "FAILED"
	groupSuperseded          = "SUPERSEDED"
)

// nativePollInterval bounds provider reads per delivered pull request.
const nativePollInterval = 10 * time.Second

// nativeRequestTimeout is how long an enqueue intent may stay REQUESTED
// before its outcome is treated as unknown and read from the provider.
const nativeRequestTimeout = time.Minute

// capabilityReasons are the WAITING reasons a probe may overwrite; other
// reasons record a provider decision about this candidate.
var capabilityReasons = map[string]bool{
	"GITHUB_QUEUE_NOT_VERIFIED": true, "AWAITING_ENQUEUE": true, "GITHUB_REPOSITORY_UNKNOWN": true,
	github.ReasonQueueDisabled: true, github.ReasonQueueAmbiguous: true, github.ReasonHeadGreen: true, github.ReasonGroupSize: true,
	github.ReasonPolicyNotRequired: true, github.ReasonPolicyUnpinned: true, github.ReasonPublisherMissing: true,
	github.ReasonProviderUnreachable: true, github.ReasonManifestUnconfigured: true,
}

// retryableNativeFailures are FAILED reasons that only lost the delivering
// pull request; the approved source is unchanged, so the candidate may be
// enqueued again through an open pull request of the same head. A FAILED
// caused by a permanently invalid approval subject is not listed.
var retryableNativeFailures = map[string]bool{"PR_CLOSED": true, "PR_HEAD_CHANGED": true}

// nativeEnqueueable is a candidate state EnqueueCandidate may deliver.
func nativeEnqueueable(c *sqlite.Candidate) bool {
	return c.State == domain.CandidateWaiting || (c.State == domain.CandidateFailed && retryableNativeFailures[c.StateReason])
}

// projectGitHubRepo is the configured repository or the github.com origin.
func (a *App) projectGitHubRepo(ctx context.Context, p *sqlite.Project) string {
	repo := projectConfig(p).GitHub.Repository
	if repo == "" {
		if r, err := a.Git.Run(ctx, p.RootPath, nil, "remote", "get-url", "origin"); err == nil {
			repo, _ = github.RepoFromRemote(r.Stdout)
		}
	}
	return repo
}

func targetBranch(ref string) string { return strings.TrimPrefix(ref, "refs/heads/") }

func targetCapability(t *sqlite.Target) *github.QueueCapability {
	if t.ProviderCheckedAt == 0 {
		return nil
	}
	c := &github.QueueCapability{}
	if json.Unmarshal([]byte(t.ProviderCapability), c) != nil {
		return nil
	}
	return c
}

// nativeEnqueue is what EnqueueCandidate learned from the provider before
// opening its write transaction.
type nativeEnqueue struct {
	repo string
	pr   *github.PullRequest
}

// prepareNativeEnqueue checks the target probe and finds the open pull
// request of the approved source. It returns nil for other authorities.
func (a *App) prepareNativeEnqueue(ctx context.Context, candidateID string) (*nativeEnqueue, error) {
	var c *sqlite.Candidate
	var t *sqlite.Target
	var p *sqlite.Project
	if err := a.DB.Read(ctx, func(q sqlite.Q) error {
		var err error
		if c, err = sqlite.GetCandidate(ctx, q, candidateID); err != nil {
			return err
		}
		if t, err = sqlite.GetTarget(ctx, q, c.TargetID); err != nil {
			return err
		}
		p, err = sqlite.GetProject(ctx, q, c.ProjectID)
		return err
	}); err != nil {
		return nil, err
	}
	if t.Authority != domain.AuthorityGitHubQueue {
		return nil, nil
	}
	if !nativeEnqueueable(c) {
		return nil, domain.Errf(domain.CodeConflict, "candidate is %s(%s); only WAITING candidates and ones that lost their pull request (PR_CLOSED, PR_HEAD_CHANGED) are delivered to the merge queue", c.State, c.StateReason)
	}
	// Enqueue decides on a fresh probe, never on a cached one.
	repo := a.projectGitHubRepo(ctx, p)
	if err := a.probeTarget(ctx, t, repo, projectConfig(p).GitHub.MergeQueue); err != nil {
		return nil, err
	}
	if err := nativeSupported(t); err != nil {
		return nil, err
	}
	pr, err := github.FindSourcePullRequest(ctx, a.GitHub, repo, targetBranch(t.TargetRef), c.SourceSHA, a.Clock.Now())
	if err != nil {
		return nil, domain.Errf(domain.CodeConflict, "cannot read pull requests of %s: %v", repo, github.AsError(err))
	}
	if pr == nil {
		return nil, domain.Errf(domain.CodeConflict, "no open pull request of %s has head %s and base %s; push the source and open one", repo, c.SourceSHA, targetBranch(t.TargetRef))
	}
	return &nativeEnqueue{repo: repo, pr: pr}, nil
}

func nativeSupported(t *sqlite.Target) error {
	capa := targetCapability(t)
	if capa == nil {
		return domain.Errf(domain.CodeUnsupportedCapability, "github_queue target %s is not probed yet (GITHUB_QUEUE_NOT_VERIFIED)", t.TargetRef)
	}
	if !capa.Supported {
		return domain.Errf(domain.CodeUnsupportedCapability, "github_queue target %s is not supported: %s", t.TargetRef, capa.Reason).
			WithDetails("reason", capa.Reason)
	}
	return nil
}

// enqueueNativeTx moves a RESULT-approved, eligible candidate WAITING (or
// FAILED that only lost its pull request) → PUBLISHING and records the enqueue intent (03 §3.4). PUBLISHING here means
// "in the provider queue", never "allowed to merge".
func (a *App) enqueueNativeTx(ctx context.Context, tx *sqlite.Tx, c *sqlite.Candidate, t *sqlite.Target, n *nativeEnqueue) error {
	if n == nil {
		return domain.Errf(domain.CodeConflict, "target authority changed; retry")
	}
	if err := nativeSupported(t); err != nil {
		return err
	}
	if !nativeEnqueueable(c) {
		return domain.Errf(domain.CodeConflict, "candidate is %s", c.State)
	}
	results, err := sqlite.ApprovalsWhere(ctx, tx, "kind='RESULT' AND output_id=? AND state='APPROVED'", c.OutputID)
	if err != nil {
		return err
	}
	valid := false
	for _, ap := range results {
		if stale, _, err := a.approvalStale(ctx, tx, ap); err != nil {
			return err
		} else if !stale {
			valid = true
		}
	}
	if !valid {
		return domain.Errf(domain.CodeApprovalRequired, "the source has no current RESULT approval")
	}
	if bl, err := sqlite.IntegrationBlockers(ctx, tx, c.RunID, c.IssueID); err != nil {
		return err
	} else if len(bl) > 0 {
		return domain.Errf(domain.CodeDependencyUnmet, "integration is blocked: %s", bl[0].Code)
	}
	// The MVP delivers one candidate per target at a time (04 §10.7).
	if busy, err := sqlite.CandidatesWhere(ctx, tx, "target_id=? AND id<>? AND state='PUBLISHING'", t.ID, c.ID); err != nil {
		return err
	} else if len(busy) > 0 {
		return domain.Errf(domain.CodeConflict, "candidate %s is already in the merge queue of %s", busy[0].ID, t.TargetRef)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE merge_candidates SET state='PUBLISHING', state_reason='NATIVE_ENQUEUE_REQUESTED', native_queue_state='REQUESTED',
		publication_observation='REQUESTED', enqueued=1, provider_pr=?, provider_pr_node=?, provider_validation_id=NULL, version=version+1, updated_at=? WHERE id=?`,
		n.pr.Number, n.pr.NodeID, tx.Now, c.ID); err != nil {
		return err
	}
	_, err = tx.Enqueue(ctx, sqlite.OutboxItem{ProjectID: c.ProjectID, Kind: "github.queue.enqueue",
		Payload:   map[string]string{"candidate_id": c.ID, "pr_node": n.pr.NodeID, "source_sha": c.SourceSHA, "repository": n.repo},
		DedupeKey: fmt.Sprintf("github.queue.enqueue:%s:%d", c.ID, c.Version), ResourceKey: "target:" + t.ID})
	return err
}

// handleNativeEnqueue asks GitHub to queue the pull request while its head
// is still the approved source. A refusal returns the candidate to WAITING;
// an unknown outcome is resolved by polling the pull request (04 §10.3).
func (a *App) handleNativeEnqueue(ctx context.Context, p map[string]string) outcome {
	var c *sqlite.Candidate
	if err := a.DB.Read(ctx, func(q sqlite.Q) error {
		var err error
		c, err = sqlite.GetCandidate(ctx, q, p["candidate_id"])
		return err
	}); err != nil {
		return failed(err.Error(), nil)
	}
	if c.State != domain.CandidatePublishing || c.NativeQueueState != domain.NativeRequested {
		return done(map[string]string{"skipped": c.State}, nil)
	}
	entry, err := github.EnqueuePullRequest(ctx, a.GitHub, &github.PullRequest{Number: c.ProviderPR, NodeID: p["pr_node"]}, p["source_sha"], a.Clock.Now())
	set := func(state, native, reason string, enqueued bool) func(tx *sqlite.Tx) error {
		return func(tx *sqlite.Tx) error {
			cur, err := sqlite.GetCandidate(ctx, tx, c.ID)
			if err != nil || cur.State != domain.CandidatePublishing || cur.NativeQueueState != domain.NativeRequested {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE merge_candidates SET state=?, native_queue_state=?, state_reason=?, enqueued=?, version=version+1, updated_at=? WHERE id=?",
				state, native, reason, enqueued, tx.Now, c.ID); err != nil {
				return err
			}
			tx.Touch(c.ProjectID, sqlite.KindCandidate, c.ID)
			tx.Touch(c.ProjectID, sqlite.KindIssue, c.IssueID)
			tx.Event(c.ProjectID, "merge_candidate.native_"+strings.ToLower(native), "merge_candidate", c.ID, cur.Version+1)
			return nil
		}
	}
	var rej *github.RejectedError
	switch {
	case err == nil:
		return done(map[string]string{"merge_queue_entry": entry}, set(domain.CandidatePublishing, domain.NativeQueued, "NATIVE_QUEUED", true))
	case errors.As(err, &rej), definiteRefusal(err):
		return done(map[string]string{"rejected": err.Error()}, set(domain.CandidateWaiting, domain.NativeNone, "NATIVE_ENQUEUE_REJECTED", false))
	default:
		return done(map[string]string{"unknown": err.Error()}, set(domain.CandidatePublishing, domain.NativeUnknown, "NATIVE_ENQUEUE_UNKNOWN", true))
	}
}

// handleNativeDequeue takes a pull request whose approval subject became
// permanently invalid out of the merge queue. It reads the provider first,
// so a re-run after a restart or a refusal never claims a removal it did
// not observe: the candidate is marked REMOVED only once the pull request
// has no queue entry. Errors are retried; when the retries run out the
// queue state is UNKNOWN, not REMOVED, so the stuck entry stays visible.
func (a *App) handleNativeDequeue(ctx context.Context, p map[string]string) outcome {
	var c *sqlite.Candidate
	var holder []*sqlite.Candidate
	if err := a.DB.Read(ctx, func(q sqlite.Q) error {
		var err error
		if c, err = sqlite.GetCandidate(ctx, q, p["candidate_id"]); err != nil {
			return err
		}
		holder, err = sqlite.CandidatesWhere(ctx, q, "provider_pr_node=? AND id<>? AND state='PUBLISHING'", p["pr_node"], c.ID)
		return err
	}); err != nil {
		return failed(err.Error(), nil)
	}
	if c.State != domain.CandidateFailed || c.NativeQueueState == domain.NativeRemoved {
		return done(map[string]string{"skipped": c.State}, nil)
	}
	mark := func(native, action string) func(tx *sqlite.Tx) error {
		return func(tx *sqlite.Tx) error {
			cur, err := sqlite.GetCandidate(ctx, tx, c.ID)
			if err != nil || cur.State != domain.CandidateFailed || cur.NativeQueueState == domain.NativeRemoved {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE merge_candidates SET native_queue_state=?, enqueued=?, version=version+1, updated_at=? WHERE id=?",
				native, native != domain.NativeRemoved, tx.Now, c.ID); err != nil {
				return err
			}
			tx.Touch(c.ProjectID, sqlite.KindCandidate, c.ID)
			tx.Touch(c.ProjectID, sqlite.KindIssue, c.IssueID)
			tx.Event(c.ProjectID, "merge_candidate.native_"+strings.ToLower(native), "merge_candidate", c.ID, cur.Version+1)
			return tx.Audit(ctx, c.ProjectID, "integration-controller", action, "merge_candidate", c.ID, "", p["reason"],
				map[string]string{"repository": p["repository"], "pull_request_node": p["pr_node"]})
		}
	}
	removed := mark(domain.NativeRemoved, "merge.native_dequeue")
	// A newer candidate delivered through the same pull request owns its
	// queue entry now; this candidate's entry is already gone.
	if len(holder) > 0 {
		return done(map[string]string{"superseded_by": holder[0].ID}, removed)
	}
	again := func(err error) outcome {
		o := retry(15*time.Second, err)
		o.finish = mark(domain.NativeUnknown, "merge.native_dequeue_failed")
		return o
	}
	o, err := github.ObservePullRequest(ctx, a.GitHub, p["repository"], c.ProviderPR, a.Clock.Now())
	if err != nil {
		return again(err)
	}
	if o.EntryID == "" {
		return done(map[string]string{"not_queued": p["pr_node"]}, removed)
	}
	if err := github.DequeuePullRequest(ctx, a.GitHub, p["pr_node"], a.Clock.Now()); err != nil {
		return again(err)
	}
	return done(map[string]string{"dequeued": p["pr_node"]}, removed)
}

// definiteRefusal is a provider answer that certainly did not queue.
func definiteRefusal(err error) bool {
	e := github.AsError(err)
	return e.HTTP >= 400 && e.HTTP < 500 && e.Status != github.StatusRateLimited
}

// observeGitHubQueueTarget refreshes the target probe and follows every
// delivered pull request by polling; no public webhook is needed (04 §10.8).
func (a *App) observeGitHubQueueTarget(ctx context.Context, t *sqlite.Target) error {
	var cands []*sqlite.Candidate
	var p *sqlite.Project
	if err := a.DB.Read(ctx, func(q sqlite.Q) error {
		var err error
		if cands, err = sqlite.CandidatesWhere(ctx, q, "target_id=? AND state IN ('WAITING','PUBLISHING')", t.ID); err != nil || len(cands) == 0 {
			return err
		}
		p, err = sqlite.GetProject(ctx, q, cands[0].ProjectID)
		return err
	}); err != nil || len(cands) == 0 {
		return err
	}
	cfg := projectConfig(p)
	repo := a.projectGitHubRepo(ctx, p)
	active := false
	for _, c := range cands {
		active = active || c.State == domain.CandidatePublishing
	}
	// While a candidate is in the queue, its success intent must rest on a
	// recent probe (04 §10.2), so the target is re-probed every poll.
	age := a.nowMillis() - t.ProviderCheckedAt
	if t.ProviderCheckedAt == 0 || age >= int64(cfg.GitHub.SyncIntervalSeconds)*1000 || (active && age >= nativePollInterval.Milliseconds()) {
		if err := a.probeTarget(ctx, t, repo, cfg.GitHub.MergeQueue); err != nil {
			return err
		}
	}
	for _, c := range cands {
		if c.State != domain.CandidatePublishing || c.ProviderPR == 0 {
			continue
		}
		// A REQUESTED intent normally resolves through its outbox item; one
		// left behind by a restart is resolved by reading the provider.
		if c.NativeQueueState == domain.NativeRequested && a.nowMillis()-c.UpdatedAt < nativeRequestTimeout.Milliseconds() {
			continue
		}
		now := a.Clock.Now()
		if last, ok := a.nativePolled.Load(c.ID); ok && now.Sub(last.(time.Time)) < nativePollInterval {
			continue
		}
		a.nativePolled.Store(c.ID, now)
		o, err := github.ObservePullRequest(ctx, a.GitHub, repo, c.ProviderPR, now)
		if err != nil {
			a.Log.Warn("merge queue poll", "candidate", c.ID, "err", github.AsError(err))
			continue
		}
		mr := a.verifyGroupManifest(ctx, c, t, repo, cfg.GitHub.MergeQueue, o)
		cid := c.ID
		if err := a.DB.Write(ctx, func(tx *sqlite.Tx) error { return a.applyQueueObservation(ctx, tx, cid, t, repo, o, mr) }); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) probeTarget(ctx context.Context, t *sqlite.Target, repo string, mq config.MergeQueue) error {
	var capa *github.QueueCapability
	if repo == "" {
		capa = &github.QueueCapability{Reason: "GITHUB_REPOSITORY_UNKNOWN", Branch: targetBranch(t.TargetRef)}
	} else {
		var err error
		capa, err = github.ProbeMergeQueue(ctx, a.GitHub, repo, targetBranch(t.TargetRef), mq.PolicyAppID, a.publishesAs(mq.PolicyAppID), a.Clock.Now())
		if err != nil {
			a.Log.Warn("merge queue probe", "target", t.ID, "err", github.AsError(err))
			capa = &github.QueueCapability{Reason: github.ReasonProviderUnreachable, Repository: repo, Branch: targetBranch(t.TargetRef)}
		}
	}
	if capa.Supported && mq.ManifestWorkflow == "" {
		capa.Supported, capa.Reason = false, github.ReasonManifestUnconfigured
	}
	reason := capa.Reason
	if capa.Supported {
		reason = "AWAITING_ENQUEUE"
	}
	b, _ := json.Marshal(capa)
	return a.DB.Write(ctx, func(tx *sqlite.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE integration_targets SET provider_capability=?, provider_checked_at=? WHERE id=?", string(b), tx.Now, t.ID); err != nil {
			return err
		}
		t.ProviderCapability, t.ProviderCheckedAt = string(b), tx.Now
		ws, err := sqlite.CandidatesWhere(ctx, tx, "target_id=? AND state='WAITING' AND enqueued=0", t.ID)
		if err != nil {
			return err
		}
		for _, c := range ws {
			if !capabilityReasons[c.StateReason] || c.StateReason == reason {
				continue
			}
			if _, err := tx.ExecContext(ctx, "UPDATE merge_candidates SET state_reason=?, version=version+1, updated_at=? WHERE id=?", reason, tx.Now, c.ID); err != nil {
				return err
			}
			tx.Touch(c.ProjectID, sqlite.KindCandidate, c.ID)
			tx.Touch(c.ProjectID, sqlite.KindIssue, c.IssueID)
			tx.Event(c.ProjectID, "merge_candidate.waiting", "merge_candidate", c.ID, c.Version+1)
		}
		return nil
	})
}

// publishesAs reports whether a check publisher exists and, when it names
// its App, whether that is the App the ruleset pins. A credential of
// another App could never satisfy the required check.
func (a *App) publishesAs(appID int64) bool {
	if a.PolicyChecks == nil {
		return false
	}
	if p, ok := a.PolicyChecks.(interface{ AppID() int64 }); ok {
		return p.AppID() == appID
	}
	return true
}

// groupManifest is a manifest verdict read for one provider group.
type groupManifest struct {
	groupID string
	*github.ManifestResult
}

// verifyGroupManifest reads the collector manifest of the candidate's
// current group outside any transaction (R-06). It returns nil when there
// is nothing to verify, the run is not finished or the read failed; the
// group then stays unverified and its check never turns green.
func (a *App) verifyGroupManifest(ctx context.Context, c *sqlite.Candidate, t *sqlite.Target, repo string, mq config.MergeQueue, o *github.QueueObservation) *groupManifest {
	if c.ProviderValidationID == "" || o.GroupHead == "" || mq.ManifestWorkflow == "" {
		return nil
	}
	var g *sqlite.ProviderGroup
	if err := a.DB.Read(ctx, func(q sqlite.Q) error {
		var err error
		g, err = sqlite.GetProviderGroup(ctx, q, c.ProviderValidationID)
		return err
	}); err != nil || g.ManifestState != github.ManifestPending || g.EntryID != o.EntryID || g.HeadSHA != o.GroupHead || g.BaseSHA != o.GroupBase {
		return nil
	}
	spec := github.ManifestSpec{Repository: repo, Branch: targetBranch(t.TargetRef), Workflow: mq.ManifestWorkflow, Artifact: mq.ManifestArtifact}
	mr, err := github.VerifyMergeGroupManifest(ctx, a.GitHub, spec, g.HeadSHA, g.BaseSHA, a.Clock.Now())
	if err != nil {
		a.Log.Warn("merge group manifest", "candidate", c.ID, "group", g.ID, "err", github.AsError(err))
		return nil
	}
	if mr.State == github.ManifestPending {
		return nil
	}
	return &groupManifest{groupID: g.ID, ManifestResult: mr}
}

// applyQueueObservation maps one provider observation onto the candidate.
// A group tuple GitHub has not shown before is a new candidate revision
// with a fresh in_progress policy check and a new MERGE approval (INV-09,
// 04 §10.9). The success intent is the linearization point (04 §10.10):
// it is written only when, in this transaction, the observed group is still
// the candidate's group and its MERGE approval is current.
func (a *App) applyQueueObservation(ctx context.Context, tx *sqlite.Tx, candidateID string, t *sqlite.Target, repo string, o *github.QueueObservation, mr *groupManifest) error {
	c, err := sqlite.GetCandidate(ctx, tx, candidateID)
	if err != nil || c.State != domain.CandidatePublishing {
		return err
	}
	var g *sqlite.ProviderGroup
	if c.ProviderValidationID != "" {
		if g, err = sqlite.GetProviderGroup(ctx, tx, c.ProviderValidationID); err != nil {
			return err
		}
	}
	update := func(sets string, args ...any) error {
		args = append(args, tx.Now, c.ID)
		if _, err := tx.ExecContext(ctx, "UPDATE merge_candidates SET "+sets+", version=version+1, updated_at=? WHERE id=?", args...); err != nil {
			return err
		}
		tx.Touch(c.ProjectID, sqlite.KindCandidate, c.ID)
		tx.Touch(c.ProjectID, sqlite.KindIssue, c.IssueID)
		tx.Event(c.ProjectID, "merge_candidate.native_observed", "merge_candidate", c.ID, c.Version+1)
		return nil
	}
	dropGroup := func(reason string) error {
		if g != nil && g.CheckState != groupSuperseded && g.CheckState != groupFailed {
			if _, err := tx.ExecContext(ctx, "UPDATE provider_validation_groups SET check_state='SUPERSEDED', updated_at=? WHERE id=?", tx.Now, g.ID); err != nil {
				return err
			}
		}
		aps, err := sqlite.ApprovalsWhere(ctx, tx, "kind='MERGE' AND merge_candidate_id=? AND state IN ('PENDING','APPROVED')", c.ID)
		if err != nil {
			return err
		}
		for _, ap := range aps {
			if err := a.invalidateApproval(ctx, tx, ap, reason); err != nil {
				return err
			}
		}
		return nil
	}
	switch {
	case o.State == "MERGED" && o.HeadOID == c.SourceSHA:
		if err := update("native_queue_state='MERGED'"); err != nil {
			return err
		}
		cur, err := sqlite.GetCandidate(ctx, tx, c.ID)
		if err != nil {
			return err
		}
		// GitHub merging without a published success (rule bypass, ruleset
		// change) is still a fact and is kept (04 §10.10), but it is never
		// recorded as an authorized integration.
		authorized := g != nil && g.CheckState == groupSuccess
		ev := map[string]any{"repository": repo, "pull_request": c.ProviderPR, "merge_commit": o.MergeOID, "source_sha": c.SourceSHA, "policy_authorized": authorized}
		if g != nil {
			ev["provider_validation_id"], ev["merge_group_head"], ev["merge_group_base"], ev["merge_queue_entry"], ev["policy_check_run"] =
				g.ID, g.HeadSHA, g.BaseSHA, g.EntryID, g.CheckRunID
		}
		if err := a.recordMerged(ctx, tx, cur, c.BaseSHA, o.MergeOID, domain.AuthorityGitHubQueue, ev); err != nil || authorized {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE merge_candidates SET state_reason='MERGED_WITHOUT_POLICY_SUCCESS' WHERE id=?", c.ID); err != nil {
			return err
		}
		return tx.Audit(ctx, c.ProjectID, "integration-controller", "merge.native_unauthorized", "merge_candidate", c.ID, "", "MERGED_WITHOUT_POLICY_SUCCESS", ev)
	case o.State == "MERGED" || o.HeadOID != c.SourceSHA:
		// The pull request no longer carries the approved source.
		if err := dropGroup("PR_HEAD_CHANGED"); err != nil {
			return err
		}
		return update("state='FAILED', state_reason='PR_HEAD_CHANGED', native_queue_state='REMOVED', enqueued=0")
	case o.State == "CLOSED":
		if err := dropGroup("PR_CLOSED"); err != nil {
			return err
		}
		return update("state='FAILED', state_reason='PR_CLOSED', native_queue_state='REMOVED', enqueued=0")
	case o.EntryID == "":
		if err := dropGroup("NATIVE_QUEUE_REMOVED"); err != nil {
			return err
		}
		return update("state='WAITING', state_reason='NATIVE_QUEUE_REMOVED', native_queue_state='REMOVED', enqueued=0, provider_validation_id=NULL")
	case o.GroupHead == "":
		if c.NativeQueueState == domain.NativeQueued {
			return nil
		}
		return update("native_queue_state='QUEUED', state_reason='NATIVE_QUEUED'")
	}
	if g == nil || g.EntryID != o.EntryID || g.HeadSHA != o.GroupHead || g.BaseSHA != o.GroupBase {
		return a.startProviderGroup(ctx, tx, c, t, repo, o, dropGroup, update)
	}
	if mr != nil && mr.groupID == g.ID && g.ManifestState == github.ManifestPending {
		if _, err := tx.ExecContext(ctx, "UPDATE provider_validation_groups SET manifest_state=?, manifest_run_id=?, manifest_reason=?, updated_at=? WHERE id=? AND manifest_state='PENDING'",
			mr.State, mr.RunID, mr.Reason, tx.Now, g.ID); err != nil {
			return err
		}
		g.ManifestState, g.ManifestRunID, g.ManifestReason = mr.State, mr.RunID, mr.Reason
		if err := tx.Audit(ctx, c.ProjectID, "integration-controller", "merge.native_manifest", "merge_candidate", c.ID, "", mr.State,
			map[string]string{"provider_validation_id": g.ID, "merge_group_head": g.HeadSHA, "workflow_run": mr.RunID, "workflow_blob": mr.WorkflowBlob, "reason": mr.Reason}); err != nil {
			return err
		}
	}
	if g.CheckState != groupInProgress {
		return nil
	}
	if g.ManifestState == github.ManifestFailed {
		// The provider gets a definite failure for this exact group; GitHub
		// then removes the entry and the candidate returns to WAITING.
		if _, err := tx.ExecContext(ctx, "UPDATE provider_validation_groups SET check_state='FAILURE_REQUESTED', updated_at=? WHERE id=? AND check_state='IN_PROGRESS'", tx.Now, g.ID); err != nil {
			return err
		}
		aps, err := sqlite.ApprovalsWhere(ctx, tx, "kind='MERGE' AND merge_candidate_id=? AND state IN ('PENDING','APPROVED')", c.ID)
		if err != nil {
			return err
		}
		for _, ap := range aps {
			if err := a.invalidateApproval(ctx, tx, ap, "MERGE_GROUP_MANIFEST_REJECTED"); err != nil {
				return err
			}
		}
		if err := update("state_reason='MERGE_GROUP_MANIFEST_REJECTED'"); err != nil {
			return err
		}
		return a.enqueuePolicyCheck(ctx, tx, c, t, repo, g.ID, "failure")
	}
	if cur, err := sqlite.GetTarget(ctx, tx, t.ID); err != nil {
		return err
	} else if nativeSupported(cur) != nil {
		return nil
	}
	p, err := sqlite.GetProject(ctx, tx, c.ProjectID)
	if err != nil {
		return err
	}
	if projectConfig(p).Approval.Integration != "auto" {
		ap, err := a.requestApproval(ctx, tx, nativeMergeSpec(ctx, tx, p, c, t, g))
		if err != nil {
			return err
		}
		if stale, reason, err := a.approvalStale(ctx, tx, ap); err != nil {
			return err
		} else if stale {
			// Run or output changes are permanent for this candidate: stop
			// asking for approvals instead of re-requesting every poll, and
			// take the pull request out of the queue: its policy check would
			// never turn green and would block the queue.
			if err := a.invalidateApproval(ctx, tx, ap, reason); err != nil {
				return err
			}
			if err := dropGroup(reason); err != nil {
				return err
			}
			if err := update("state='FAILED', state_reason=?, provider_validation_id=NULL", reason); err != nil {
				return err
			}
			_, err = tx.Enqueue(ctx, sqlite.OutboxItem{ProjectID: c.ProjectID, Kind: "github.queue.dequeue",
				Payload:   map[string]string{"candidate_id": c.ID, "pr_node": c.ProviderPRNode, "repository": repo, "reason": reason},
				DedupeKey: fmt.Sprintf("github.queue.dequeue:%s:%d", c.ID, c.Version), ResourceKey: "target:" + t.ID})
			return err
		}
		if ap.State != domain.ApprovalApproved {
			return nil
		}
	}
	// Success is authorized only for a group whose manifest was verified.
	if g.ManifestState != github.ManifestVerified {
		return nil
	}
	if _, err := tx.ExecContext(ctx, "UPDATE provider_validation_groups SET check_state='SUCCESS_REQUESTED', updated_at=? WHERE id=? AND check_state='IN_PROGRESS'", tx.Now, g.ID); err != nil {
		return err
	}
	if err := a.enqueuePolicyCheck(ctx, tx, c, t, repo, g.ID, "success"); err != nil {
		return err
	}
	return tx.Audit(ctx, c.ProjectID, "integration-controller", "merge.native_authorize", "merge_candidate", c.ID, "", "",
		map[string]any{"provider_validation_id": g.ID, "merge_group_head": g.HeadSHA, "merge_group_base": g.BaseSHA, "policy_digest": g.PolicyDigest})
}

func (a *App) startProviderGroup(ctx context.Context, tx *sqlite.Tx, c *sqlite.Candidate, t *sqlite.Target, repo string, o *github.QueueObservation,
	dropGroup func(string) error, update func(string, ...any) error) error {
	if err := dropGroup("PROVIDER_GROUP_CHANGED"); err != nil {
		return err
	}
	r, err := sqlite.GetRun(ctx, tx, c.RunID)
	if err != nil {
		return err
	}
	rev := c.Revision + 1
	members, _ := json.Marshal([]map[string]any{{"pull_request": c.ProviderPR, "source_sha": c.SourceSHA, "output_id": c.OutputID, "candidate_revision": rev}})
	digest := domain.Digest(map[string]any{"repository": repo, "target_ref": t.TargetRef, "target_policy": t.PolicyVersion,
		"merge_queue_entry": o.EntryID, "head_sha": o.GroupHead, "base_sha": o.GroupBase, "members": string(members), "run_generation": r.Generation})
	gid := domain.NewID("pvg")
	if _, err := tx.ExecContext(ctx, `INSERT INTO provider_validation_groups(id,project_id,candidate_id,candidate_revision,entry_id,head_sha,base_sha,members,policy_digest,check_state,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,'IN_PROGRESS_REQUESTED',?,?)`, gid, c.ProjectID, c.ID, rev, o.EntryID, o.GroupHead, o.GroupBase, string(members), digest, tx.Now, tx.Now); err != nil {
		return err
	}
	if err := update("revision=?, base_sha=?, candidate_sha=?, provider_validation_id=?, native_queue_state='VALIDATING', state_reason='PROVIDER_GROUP_VALIDATING'",
		rev, o.GroupBase, o.GroupHead, gid); err != nil {
		return err
	}
	// A head SHA seen before may still carry an old success: reset it to
	// in_progress before this group is evaluated (04 §10.9).
	if err := a.enqueuePolicyCheck(ctx, tx, c, t, repo, gid, "in_progress"); err != nil {
		return err
	}
	p, err := sqlite.GetProject(ctx, tx, c.ProjectID)
	if err != nil {
		return err
	}
	if projectConfig(p).Approval.Integration == "auto" {
		return nil
	}
	cur, err := sqlite.GetCandidate(ctx, tx, c.ID)
	if err != nil {
		return err
	}
	g, err := sqlite.GetProviderGroup(ctx, tx, gid)
	if err != nil {
		return err
	}
	_, err = a.requestApproval(ctx, tx, nativeMergeSpec(ctx, tx, p, cur, t, g))
	return err
}

// nativeMergeSpec binds a MERGE approval to one provider group: its head,
// base, queue entry and members (04 §10.7).
func nativeMergeSpec(ctx context.Context, q sqlite.Q, p *sqlite.Project, c *sqlite.Candidate, t *sqlite.Target, g *sqlite.ProviderGroup) approvalSpec {
	gen := int64(0)
	if r, err := sqlite.GetRun(ctx, q, c.RunID); err == nil {
		gen = r.Generation
	}
	return approvalSpec{Kind: domain.ApprovalMerge, ProjectID: p.ID, RunID: c.RunID, RunGeneration: gen, IssueID: c.IssueID, OutputID: c.OutputID,
		CandidateID: c.ID, CandidateRev: c.Revision, PolicyVersion: p.PolicyVersion,
		Subject: map[string]any{"candidate_sha": c.CandidateSHA, "base_sha": c.BaseSHA, "source_sha": c.SourceSHA, "target_ref": t.TargetRef, "target_policy": t.PolicyVersion,
			"provider_validation_id": g.ID, "merge_queue_entry": g.EntryID, "members": g.Members, "policy_digest": g.PolicyDigest}}
}

func (a *App) enqueuePolicyCheck(ctx context.Context, tx *sqlite.Tx, c *sqlite.Candidate, t *sqlite.Target, repo, groupID, status string) error {
	_, err := tx.Enqueue(ctx, sqlite.OutboxItem{ProjectID: c.ProjectID, Kind: "github.policy_check",
		Payload:   map[string]string{"group_id": groupID, "status": status, "repository": repo},
		DedupeKey: fmt.Sprintf("github.policy_check:%s:%s:%d", groupID, status, tx.Now), ResourceKey: "target:" + t.ID})
	return err
}

// handlePolicyCheck publishes the policy check of one group. A success
// intent whose group was replaced or whose approval was revoked before it
// was sent is cancelled; one already sent is not claimed to be recalled
// (04 §10.10).
func (a *App) handlePolicyCheck(ctx context.Context, p map[string]string) outcome {
	var g *sqlite.ProviderGroup
	var c *sqlite.Candidate
	var proj *sqlite.Project
	if err := a.DB.Read(ctx, func(q sqlite.Q) error {
		var err error
		if g, err = sqlite.GetProviderGroup(ctx, q, p["group_id"]); err != nil {
			return err
		}
		if c, err = sqlite.GetCandidate(ctx, q, g.CandidateID); err != nil {
			return err
		}
		proj, err = sqlite.GetProject(ctx, q, c.ProjectID)
		return err
	}); err != nil {
		return failed(err.Error(), nil)
	}
	success := p["status"] == "success"
	want := map[string]string{"in_progress": groupInProgressRequested, "success": groupSuccessRequested, "failure": groupFailureRequested}[p["status"]]
	current := c.State == domain.CandidatePublishing && c.ProviderValidationID == g.ID
	if !current || g.CheckState != want {
		return done(map[string]string{"skipped": g.CheckState}, nil)
	}
	cancel := ""
	if success && g.ManifestState != github.ManifestVerified {
		cancel = "MERGE_GROUP_MANIFEST_NOT_VERIFIED"
	} else if success && projectConfig(proj).Approval.Integration != "auto" {
		approved, err := a.groupApproved(ctx, c)
		if err != nil {
			return retry(15*time.Second, err)
		}
		if !approved {
			cancel = "MERGE_APPROVAL_NOT_CURRENT"
		}
	}
	if cancel != "" {
		return done(map[string]string{"cancelled": cancel}, func(tx *sqlite.Tx) error {
			_, err := tx.ExecContext(ctx, "UPDATE provider_validation_groups SET check_state='IN_PROGRESS', updated_at=? WHERE id=? AND check_state='SUCCESS_REQUESTED'", tx.Now, g.ID)
			return err
		})
	}
	if a.PolicyChecks == nil {
		// The credential went away while a publication was pending: keep it
		// retryable rather than stranding the group in a *_REQUESTED state.
		return retry(15*time.Second, errors.New(github.ReasonPublisherMissing))
	}
	check := PolicyCheck{Repository: p["repository"], HeadSHA: g.HeadSHA, ExternalID: g.ID + ":" + g.PolicyDigest, Status: "in_progress",
		Summary: "Samsara MERGE approval pending for this merge group"}
	next := groupInProgress
	switch p["status"] {
	case "success":
		check.Status, check.Conclusion, check.Summary, next = "completed", "success", "Samsara MERGE approval granted for this exact merge group", groupSuccess
	case "failure":
		check.Status, check.Conclusion, check.Summary, next = "completed", "failure", "Samsara rejected this merge group: "+g.ManifestReason, groupFailed
	}
	id, err := a.PolicyChecks.PublishPolicyCheck(ctx, check)
	if err != nil {
		return retry(15*time.Second, err)
	}
	return done(map[string]string{"check_run_id": id}, func(tx *sqlite.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE provider_validation_groups SET check_state=?, check_run_id=?, updated_at=? WHERE id=? AND check_state=?", next, id, tx.Now, g.ID, want); err != nil {
			return err
		}
		return tx.Audit(ctx, c.ProjectID, "integration-controller", "merge.policy_check", "merge_candidate", c.ID, "", next,
			map[string]string{"provider_validation_id": g.ID, "head_sha": g.HeadSHA, "check_run_id": id})
	})
}

func (a *App) groupApproved(ctx context.Context, c *sqlite.Candidate) (bool, error) {
	ok := false
	err := a.DB.Read(ctx, func(q sqlite.Q) error {
		aps, err := sqlite.ApprovalsWhere(ctx, q, "kind='MERGE' AND merge_candidate_id=? AND candidate_revision=? AND state='APPROVED'", c.ID, c.Revision)
		if err != nil {
			return err
		}
		for _, ap := range aps {
			stale, _, err := a.approvalStale(ctx, q, ap)
			if err != nil {
				return err
			}
			ok = ok || !stale
		}
		return nil
	})
	return ok, err
}
