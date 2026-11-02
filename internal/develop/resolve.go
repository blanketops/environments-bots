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

package develop

import (
	"context"
	"fmt"

	"github.com/blanketops/environments-bots/internal/github"
)

// ResolveAPI is the part of the GitHub client Resolve uses.
type ResolveAPI interface {
	GetPull(ctx context.Context, number int) (github.PullDetail, error)
	ListIssues(ctx context.Context, label string) ([]github.Issue, error)
	Comment(ctx context.Context, number int, body string) error
	CloseIssue(ctx context.Context, number int) error
}

// ResolveOptions configures Resolve.
type ResolveOptions struct {
	// Pull is the pull request whose tracking issue to close.
	Pull int
	// DryRun only reports what it would do.
	DryRun bool
}

// ResolveResult is the outcome of a Resolve.
type ResolveResult struct {
	Pull   int
	Branch string
	// NotMerged is set when the pull request was closed without merging; the
	// tracking issue is left open.
	NotMerged bool
	// Issue is the tracking issue found for the branch.
	Issue *github.Issue
	// NotFound is set when the branch has no tracking issue.
	NotFound bool
	// AlreadyClosed is set when the tracking issue was already closed.
	AlreadyClosed bool
	// Closed is set when Resolve closed the tracking issue.
	Closed bool
}

// Resolve closes the one-to-one tracking issue of a merged pull request (the
// issue developer open created for the pull request's head branch), commenting
// the merge on it. It acts only when the pull request merged — a pull request
// closed without merging leaves the issue open — and is idempotent: an issue
// already closed, or a branch with none, does nothing.
func Resolve(ctx context.Context, api ResolveAPI, opts ResolveOptions) (ResolveResult, error) {
	res := ResolveResult{Pull: opts.Pull}
	if opts.Pull <= 0 {
		return res, fmt.Errorf("no pull request number given")
	}

	pr, err := api.GetPull(ctx, opts.Pull)
	if err != nil {
		return res, fmt.Errorf("read pull request #%d: %w", opts.Pull, err)
	}
	res.Branch = pr.Head
	if !pr.Merged {
		res.NotMerged = true
		return res, nil
	}

	issue, err := trackingIssue(ctx, api, issuePrefix+pr.Head)
	if err != nil {
		return res, err
	}
	if issue == nil {
		res.NotFound = true
		return res, nil
	}
	res.Issue = issue
	if issue.State != "open" {
		res.AlreadyClosed = true
		return res, nil
	}
	if opts.DryRun {
		return res, nil
	}

	if err := api.Comment(ctx, issue.Number, resolveComment(pr)); err != nil {
		return res, fmt.Errorf("comment on issue #%d: %w", issue.Number, err)
	}
	if err := api.CloseIssue(ctx, issue.Number); err != nil {
		return res, fmt.Errorf("close issue #%d: %w", issue.Number, err)
	}
	res.Closed = true
	return res, nil
}

// trackingIssue returns the tracking issue titled title whatever its state, or
// nil. Unlike openIssue it also finds a closed one, so resolve can tell an
// already-closed issue from a missing one.
func trackingIssue(ctx context.Context, api issueLister, title string) (*github.Issue, error) {
	issues, err := api.ListIssues(ctx, TrackLabel)
	if err != nil {
		return nil, fmt.Errorf("list tracking issues: %w", err)
	}
	for i := range issues {
		if issues[i].Title == title {
			return &issues[i], nil
		}
	}
	return nil, nil
}

func resolveComment(pr github.PullDetail) string {
	if sha := short(pr.MergeCommitSHA); sha != "" {
		return fmt.Sprintf("Closed by merged pull request #%d (%s).", pr.Number, code(sha))
	}
	return fmt.Sprintf("Closed by merged pull request #%d.", pr.Number)
}

// ResolveSummary is the markdown summary of a Resolve.
func ResolveSummary(res ResolveResult, dryRun bool) string {
	switch {
	case res.NotMerged:
		return fmt.Sprintf("### Resolve #%d\n\nPull request #%d was closed without merging; its tracking issue stays open.\n", res.Pull, res.Pull)
	case res.NotFound:
		return fmt.Sprintf("### Resolve #%d\n\nNo tracking issue for %s; nothing to close.\n", res.Pull, code(res.Branch))
	case res.AlreadyClosed:
		return fmt.Sprintf("### Resolve #%d\n\nTracking issue [#%d](%s) is already closed.\n", res.Pull, res.Issue.Number, res.Issue.HTMLURL)
	case dryRun && res.Issue != nil:
		return fmt.Sprintf("### Resolve #%d (dry run)\n\nWould close tracking issue [#%d](%s) for %s.\n", res.Pull, res.Issue.Number, res.Issue.HTMLURL, code(res.Branch))
	case res.Closed:
		return fmt.Sprintf("### Resolve #%d\n\nClosed tracking issue [#%d](%s) for %s.\n", res.Pull, res.Issue.Number, res.Issue.HTMLURL, code(res.Branch))
	default:
		return fmt.Sprintf("### Resolve #%d\n\nNothing to do.\n", res.Pull)
	}
}
