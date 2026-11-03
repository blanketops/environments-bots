/*
Copyright 2026 The BlanketOps Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// This file holds the calls developer makes beyond those releaser shares:
// a server-side merge (so the merge commit is signed by GitHub and passes
// the signature ruleset with no bypass) and deleting a branch.

// IsConflict reports whether err is an HTTP 409, which a merge returns when
// the two branches cannot be merged without resolving a conflict.
func IsConflict(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusConflict
}

// Merge merges head into base with a server-side merge commit and returns
// the merge commit's SHA. GitHub signs the commit, so it satisfies the
// branch's required-signatures rule without a ruleset bypass. The SHA is
// empty when base already contains head (HTTP 204, nothing to merge).
// IsConflict(err) reports a merge conflict (HTTP 409); the caller resolves
// it, as a bot cannot.
func (c *Client) Merge(ctx context.Context, base, head, message string) (string, error) {
	in := map[string]string{"base": base, "head": head, "commit_message": message}
	var out struct {
		SHA string `json:"sha"`
	}
	err := c.do(ctx, http.MethodPost, c.repoPath("/merges"), in, &out)
	return out.SHA, err
}

// DeleteBranch deletes a branch. IsNotFound(err) when it is already gone.
func (c *Client) DeleteBranch(ctx context.Context, branch string) error {
	return c.do(ctx, http.MethodDelete, c.repoPath("/git/refs/heads/"+refPath(branch)), nil, nil)
}

// AddLabels adds labels to an issue or pull request (pull requests are
// labelled through the issues API). Adding a label that is already there is a
// no-op, so this is safe to repeat.
func (c *Client) AddLabels(ctx context.Context, number int, labels []string) error {
	if len(labels) == 0 {
		return nil
	}
	in := map[string][]string{"labels": labels}
	return c.do(ctx, http.MethodPost, c.repoPath(fmt.Sprintf("/issues/%d/labels", number)), in, nil)
}

// PullDetail is one pull request's detail (GET /pulls/{number}): its head and
// base branches and, once merged, whether it merged and the merge commit.
type PullDetail struct {
	Number         int
	Title          string
	State          string
	HTMLURL        string
	Head           string
	Base           string
	Merged         bool
	MergeCommitSHA string
}

// GetPull returns a pull request's detail.
func (c *Client) GetPull(ctx context.Context, number int) (PullDetail, error) {
	var out struct {
		Number         int    `json:"number"`
		Title          string `json:"title"`
		State          string `json:"state"`
		HTMLURL        string `json:"html_url"`
		Merged         bool   `json:"merged"`
		MergeCommitSHA string `json:"merge_commit_sha"`
		Head           struct {
			Ref string `json:"ref"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
	}
	err := c.do(ctx, http.MethodGet, c.repoPath(fmt.Sprintf("/pulls/%d", number)), nil, &out)
	return PullDetail{
		Number: out.Number, Title: out.Title, State: out.State, HTMLURL: out.HTMLURL,
		Head: out.Head.Ref, Base: out.Base.Ref, Merged: out.Merged, MergeCommitSHA: out.MergeCommitSHA,
	}, err
}

// FileAddition is a file written by CreateCommitOnBranch; Contents is
// base64-encoded.
type FileAddition struct {
	Path     string
	Contents string
}

// CreateCommitOnBranch makes a commit on branch with the given file additions
// and deletions through the GraphQL createCommitOnBranch mutation, and returns
// the new commit's OID. A commit made this way with an App installation token
// is signed by GitHub and shows as verified — a git commit in a workflow is
// not, so a branch requiring verified signatures refuses it. expectedHead is
// the commit the branch must still point at, so a racing update fails the
// mutation instead of being clobbered.
func (c *Client) CreateCommitOnBranch(ctx context.Context, branch, headline, body, expectedHead string, additions []FileAddition, deletions []string) (string, error) {
	const q = `mutation($input: CreateCommitOnBranchInput!) {
  createCommitOnBranch(input: $input) { commit { oid } }
}`
	adds := make([]map[string]string, len(additions))
	for i, a := range additions {
		adds[i] = map[string]string{"path": a.Path, "contents": a.Contents}
	}
	dels := make([]map[string]string, len(deletions))
	for i, d := range deletions {
		dels[i] = map[string]string{"path": d}
	}
	input := map[string]any{
		"branch":          map[string]string{"repositoryNameWithOwner": c.Owner + "/" + c.Repo, "branchName": branch},
		"message":         map[string]string{"headline": headline, "body": body},
		"expectedHeadOid": expectedHead,
		"fileChanges":     map[string]any{"additions": adds, "deletions": dels},
	}
	var out struct {
		CreateCommitOnBranch struct {
			Commit struct {
				OID string `json:"oid"`
			} `json:"commit"`
		} `json:"createCommitOnBranch"`
	}
	if err := c.graphql(ctx, q, map[string]any{"input": input}, &out); err != nil {
		return "", err
	}
	return out.CreateCommitOnBranch.Commit.OID, nil
}
