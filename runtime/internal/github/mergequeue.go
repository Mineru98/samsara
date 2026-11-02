package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// PolicyCheckName is the required check that carries Samsara's MERGE
// approval to the provider (04 §10.7).
const PolicyCheckName = "samsara/integration-policy"

// Reasons a target cannot use native integration. Each one keeps the
// candidate WAITING with that reason (04 §10.2, §10.9).
const (
	ReasonQueueDisabled       = "MERGE_QUEUE_DISABLED"
	ReasonHeadGreen           = "MERGE_QUEUE_HEADGREEN_UNSUPPORTED"
	ReasonGroupSize           = "MERGE_QUEUE_GROUP_SIZE_UNSUPPORTED"
	ReasonQueueAmbiguous      = "MERGE_QUEUE_RULES_AMBIGUOUS"
	ReasonPolicyNotRequired   = "POLICY_CHECK_NOT_REQUIRED"
	ReasonPolicyUnpinned      = "POLICY_CHECK_SOURCE_UNPINNED"
	ReasonPublisherMissing    = "POLICY_PUBLISHER_UNAVAILABLE"
	ReasonProviderUnreachable = "PROVIDER_UNREACHABLE"
	// ReasonManifestUnconfigured: no trusted merge_group collector Workflow
	// is configured, so no group can be verified (04 §10.8).
	ReasonManifestUnconfigured = "MERGE_GROUP_MANIFEST_UNCONFIGURED"
)

// QueueCapability is the result of one compatibility probe of a target.
type QueueCapability struct {
	Supported         bool   `json:"supported"`
	Reason            string `json:"reason,omitempty"`
	Repository        string `json:"repository"`
	Branch            string `json:"branch"`
	MergeQueue        bool   `json:"merge_queue"`
	Grouping          string `json:"grouping_strategy,omitempty"`
	MaxEntriesToBuild int    `json:"max_entries_to_build,omitempty"`
	MaxEntriesToMerge int    `json:"max_entries_to_merge,omitempty"`
	PolicyRequired    bool   `json:"policy_check_required"`
	PolicyAppID       int64  `json:"policy_check_app_id,omitempty"`
}

type rule struct {
	Type       string `json:"type"`
	Parameters struct {
		Grouping          string `json:"grouping_strategy"`
		MaxEntriesToBuild int    `json:"max_entries_to_build"`
		MaxEntriesToMerge int    `json:"max_entries_to_merge"`
		Checks            []struct {
			Context       string `json:"context"`
			IntegrationID int64  `json:"integration_id"`
		} `json:"required_status_checks"`
	} `json:"parameters"`
}

// ProbeMergeQueue reads the rules active on the target branch and decides
// whether native integration can be offered. The MVP only accepts a queue
// that builds and merges one entry at a time with ALLGREEN grouping and a
// required policy check pinned to the expected App (04 §10.7). Without a
// check publisher the gate can never turn green, so the target is
// unsupported as well.
func ProbeMergeQueue(ctx context.Context, c Client, repo, branch string, expectedAppID int64, publisher bool, now time.Time) (*QueueCapability, error) {
	r, err := c.Do(ctx, "GET", "repos/"+repo+"/rules/branches/"+branch+"?per_page=100", nil)
	if err != nil {
		return nil, err
	}
	if err := Classify(r, now); err != nil {
		return nil, err
	}
	var rules []rule
	if err := json.Unmarshal(r.Body, &rules); err != nil {
		return nil, &Error{Status: StatusProviderError, Message: "unreadable branch rules: " + err.Error()}
	}
	capa := &QueueCapability{Repository: repo, Branch: branch}
	queues, policyApps := 0, map[int64]bool{}
	for _, ru := range rules {
		switch ru.Type {
		case "merge_queue":
			queues++
			capa.MergeQueue = true
			capa.Grouping = ru.Parameters.Grouping
			capa.MaxEntriesToBuild, capa.MaxEntriesToMerge = ru.Parameters.MaxEntriesToBuild, ru.Parameters.MaxEntriesToMerge
		case "required_status_checks":
			for _, ch := range ru.Parameters.Checks {
				if ch.Context == PolicyCheckName {
					capa.PolicyRequired, capa.PolicyAppID = true, ch.IntegrationID
					policyApps[ch.IntegrationID] = true
				}
			}
		}
	}
	switch {
	case !capa.MergeQueue:
		capa.Reason = ReasonQueueDisabled
	case queues > 1:
		// Several rulesets configure the queue: fail closed.
		capa.Reason = ReasonQueueAmbiguous
	case capa.Grouping != "ALLGREEN":
		capa.Reason = ReasonHeadGreen
	case capa.MaxEntriesToBuild != 1 || capa.MaxEntriesToMerge != 1:
		capa.Reason = ReasonGroupSize
	case !capa.PolicyRequired:
		capa.Reason = ReasonPolicyNotRequired
	case expectedAppID == 0 || len(policyApps) != 1 || capa.PolicyAppID != expectedAppID:
		capa.Reason = ReasonPolicyUnpinned
	case !publisher:
		capa.Reason = ReasonPublisherMissing
	default:
		capa.Supported = true
	}
	return capa, nil
}

// PullRequest is the open pull request that delivers a source to the queue.
type PullRequest struct {
	Number int64
	NodeID string
}

// FindSourcePullRequest returns the open pull request whose head is exactly
// the approved source and whose base is the target branch, or nil.
func FindSourcePullRequest(ctx context.Context, c Client, repo, branch, sourceSHA string, now time.Time) (*PullRequest, error) {
	r, err := c.Do(ctx, "GET", "repos/"+repo+"/commits/"+sourceSHA+"/pulls", nil)
	if err != nil {
		return nil, err
	}
	if err := Classify(r, now); err != nil {
		return nil, err
	}
	var prs []struct {
		Number int64  `json:"number"`
		NodeID string `json:"node_id"`
		State  string `json:"state"`
		Head   struct {
			SHA string `json:"sha"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
	}
	if err := json.Unmarshal(r.Body, &prs); err != nil {
		return nil, &Error{Status: StatusProviderError, Message: "unreadable pull requests: " + err.Error()}
	}
	for _, p := range prs {
		if p.State == "open" && p.Head.SHA == sourceSHA && p.Base.Ref == branch {
			return &PullRequest{Number: p.Number, NodeID: p.NodeID}, nil
		}
	}
	return nil, nil
}

type graphQLError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// RejectedError is a GraphQL answer that refused the request; it is a
// definite outcome, unlike a transport failure.
type RejectedError struct{ Message string }

func (e *RejectedError) Error() string { return "github rejected: " + e.Message }

func graphQL(ctx context.Context, c Client, fields map[string]string, out any, now time.Time) error {
	r, err := c.Do(ctx, "POST", "graphql", fields)
	if err != nil {
		return err
	}
	if err := Classify(r, now); err != nil {
		return err
	}
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []graphQLError  `json:"errors"`
	}
	if err := json.Unmarshal(r.Body, &env); err != nil {
		return &Error{Status: StatusProviderError, Message: "unreadable graphql response: " + err.Error()}
	}
	if len(env.Errors) > 0 {
		var msgs []string
		for _, e := range env.Errors {
			msgs = append(msgs, e.Message)
		}
		return &RejectedError{Message: Redact(strings.Join(msgs, "; "))}
	}
	return json.Unmarshal(env.Data, out)
}

// EnqueuePullRequest adds the pull request to the merge queue only while
// its head is still the approved source (expectedHeadOid). It never asks
// for a direct merge or a rule bypass (04 §10.3).
func EnqueuePullRequest(ctx context.Context, c Client, pr *PullRequest, sourceSHA string, now time.Time) (string, error) {
	var out struct {
		Enqueue struct {
			Entry *struct {
				ID string `json:"id"`
			} `json:"mergeQueueEntry"`
		} `json:"enqueuePullRequest"`
	}
	err := graphQL(ctx, c, map[string]string{
		"query":           `mutation($pullRequestId:ID!,$expectedHeadOid:GitObjectID){enqueuePullRequest(input:{pullRequestId:$pullRequestId,expectedHeadOid:$expectedHeadOid}){mergeQueueEntry{id state}}}`,
		"pullRequestId":   pr.NodeID,
		"expectedHeadOid": sourceSHA,
	}, &out, now)
	if err != nil {
		return "", err
	}
	if out.Enqueue.Entry == nil {
		return "", &RejectedError{Message: "no merge queue entry returned"}
	}
	return out.Enqueue.Entry.ID, nil
}

// DequeuePullRequest removes the pull request from the merge queue. It is
// used when the approval subject became permanently invalid, so the entry
// does not block the queue behind a check that never turns green.
func DequeuePullRequest(ctx context.Context, c Client, prNode string, now time.Time) error {
	var out struct {
		Dequeue struct {
			Entry *struct {
				ID string `json:"id"`
			} `json:"mergeQueueEntry"`
		} `json:"dequeuePullRequest"`
	}
	return graphQL(ctx, c, map[string]string{
		"query": `mutation($id:ID!){dequeuePullRequest(input:{id:$id}){mergeQueueEntry{id}}}`,
		"id":    prNode,
	}, &out, now)
}

// QueueObservation is the provider state of a delivered pull request.
type QueueObservation struct {
	State      string // OPEN, CLOSED, MERGED
	HeadOID    string
	MergeOID   string
	EntryID    string
	EntryState string
	GroupHead  string // empty until GitHub built the group
	GroupBase  string
}

// ObservePullRequest reads the pull request and its current queue entry.
func ObservePullRequest(ctx context.Context, c Client, repo string, number int64, now time.Time) (*QueueObservation, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return nil, &Error{Status: StatusNotConfigured, Message: "repository must be owner/name"}
	}
	type oid struct {
		OID string `json:"oid"`
	}
	var out struct {
		Repository struct {
			PR *struct {
				State       string `json:"state"`
				HeadRefOid  string `json:"headRefOid"`
				MergeCommit *oid   `json:"mergeCommit"`
				Entry       *struct {
					ID    string `json:"id"`
					State string `json:"state"`
					Head  *oid   `json:"headCommit"`
					Base  *oid   `json:"baseCommit"`
				} `json:"mergeQueueEntry"`
			} `json:"pullRequest"`
		} `json:"repository"`
	}
	err := graphQL(ctx, c, map[string]string{
		"query": fmt.Sprintf(`query($owner:String!,$name:String!){repository(owner:$owner,name:$name){pullRequest(number:%d){number state headRefOid mergeCommit{oid} mergeQueueEntry{id state headCommit{oid} baseCommit{oid}}}}}`, number),
		"owner": owner,
		"name":  name,
	}, &out, now)
	if err != nil {
		return nil, err
	}
	pr := out.Repository.PR
	if pr == nil {
		return nil, &Error{Status: StatusRepoNotFound, Message: fmt.Sprintf("pull request %d not found", number)}
	}
	o := &QueueObservation{State: pr.State, HeadOID: pr.HeadRefOid}
	if pr.MergeCommit != nil {
		o.MergeOID = pr.MergeCommit.OID
	}
	if e := pr.Entry; e != nil {
		o.EntryID, o.EntryState = e.ID, e.State
		if e.Head != nil && e.Base != nil {
			o.GroupHead, o.GroupBase = e.Head.OID, e.Base.OID
		}
	}
	return o, nil
}
