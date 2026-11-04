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
	"strings"

	"github.com/blanketops/environments-bots/internal/github"
)

// TrackLabel marks the tracking issue and the pull request the developer bot
// opens for a branch.
const TrackLabel = "pr-tracking"

const (
	trackLabelColor = "1d76db"
	typeLabelColor  = "c5def5"
)

// issuePrefix titles a branch's tracking issue: one issue per branch.
const issuePrefix = "[pr] "

// conventionalTypes are the Conventional Commit types a branch prefix
// (feat/, fix/, ...) is turned into a label.
var conventionalTypes = map[string]bool{
	"feat": true, "fix": true, "docs": true, "chore": true, "ci": true,
	"refactor": true, "test": true, "perf": true, "build": true,
	"style": true, "revert": true,
}

// OpenAPI is the part of the GitHub client Open uses.
type OpenAPI interface {
	ListPulls(ctx context.Context, state, base, head string) ([]github.Pull, error)
	Compare(ctx context.Context, base, head string) ([]github.Commit, error)
	CreatePull(ctx context.Context, title, head, base, body string) (github.Pull, error)
	EnsureLabel(ctx context.Context, name, color, description string) error
	ListIssues(ctx context.Context, label string) ([]github.Issue, error)
	CreateIssue(ctx context.Context, title, body string, labels []string) (github.Issue, error)
	Comment(ctx context.Context, number int, body string) error
	AddLabels(ctx context.Context, number int, labels []string) error
}

// OpenOptions configures Open.
type OpenOptions struct {
	// Branch is the pushed branch; Base is the pull request's base (default
	// develop).
	Branch, Base string
	// DryRun only reports what it would do.
	DryRun bool
}

func (o OpenOptions) base() string {
	if o.Base != "" {
		return o.Base
	}
	return DevelopBranch
}

// OpenResult is the outcome of Open.
type OpenResult struct {
	Branch, Base string
	// Skipped is non-empty when Branch is long-lived and gets no pull request.
	Skipped string
	// Existing is a pull request from Branch that was already open.
	Existing *github.Pull
	// Title is the pull request title (the branch's latest commit subject).
	Title string
	// Labels are applied to both the pull request and the tracking issue.
	Labels []string
	// Pull and Issue are what Open created (Issue may be reused).
	Pull  *github.Pull
	Issue *github.Issue
}

// Open opens a pull request for Branch into Base on its first push (when no
// pull request from it is open yet) and a tracking issue mapped one-to-one to
// that pull request, both labelled with pr-tracking and the branch's
// Conventional Commit type. It is idempotent — a branch that already has an
// open pull request, or a long-lived branch, does nothing — and reuses an
// open tracking issue a closed pull request left behind.
func Open(ctx context.Context, api OpenAPI, opts OpenOptions) (OpenResult, error) {
	base := opts.base()
	branch := opts.Branch
	res := OpenResult{Branch: branch, Base: base}
	if branch == "" {
		return res, fmt.Errorf("no branch given")
	}
	if reason := longLived(branch, base); reason != "" {
		res.Skipped = reason
		return res, nil
	}

	// First push: no open pull request from this branch yet.
	open, err := api.ListPulls(ctx, "open", base, branch)
	if err != nil {
		return res, fmt.Errorf("list pull requests from %s: %w", branch, err)
	}
	if len(open) > 0 {
		res.Existing = &open[0]
		return res, nil
	}

	// Nothing to open when the branch is at or behind base: GitHub rejects a
	// pull request with no commits between them (HTTP 422). Check before
	// creating the tracking issue so a no-op push leaves nothing behind.
	ahead, err := api.Compare(ctx, base, branch)
	if err != nil {
		return res, fmt.Errorf("compare %s...%s: %w", base, branch, err)
	}
	if len(ahead) == 0 {
		res.Skipped = "at or behind " + base
		return res, nil
	}

	// The pull request title is the branch's newest commit subject (Compare
	// returns commits oldest first).
	title := branch
	if s := subject(ahead[len(ahead)-1].Message); s != "" {
		title = s
	}
	res.Title = title
	res.Labels = branchLabels(branch)

	if opts.DryRun {
		return res, nil
	}

	for _, l := range res.Labels {
		if err := api.EnsureLabel(ctx, l, labelColor(l), labelDesc(l)); err != nil {
			return res, fmt.Errorf("ensure label %q: %w", l, err)
		}
	}

	// One tracking issue per branch; reuse an open one a closed pull request
	// left behind.
	issueTitle := issuePrefix + branch
	issue, err := openIssue(ctx, api, issueTitle)
	if err != nil {
		return res, err
	}
	if issue == nil {
		created, err := api.CreateIssue(ctx, issueTitle, issueBody(branch, base, title), res.Labels)
		if err != nil {
			return res, fmt.Errorf("open tracking issue: %w", err)
		}
		issue = &created
	}
	res.Issue = issue

	pull, err := api.CreatePull(ctx, title, branch, base, pullBody(issue.Number))
	if err != nil {
		return res, fmt.Errorf("open pull request: %w", err)
	}
	res.Pull = &pull

	if err := api.AddLabels(ctx, pull.Number, res.Labels); err != nil {
		return res, fmt.Errorf("label pull request #%d: %w", pull.Number, err)
	}
	if err := api.Comment(ctx, issue.Number, fmt.Sprintf("Pull request: #%d", pull.Number)); err != nil {
		return res, fmt.Errorf("link pull request on issue #%d: %w", issue.Number, err)
	}
	return res, nil
}

// longLived returns why a branch gets no pull request, or "" for a normal
// feature branch.
func longLived(branch, base string) string {
	switch branch {
	case MainBranch, DevelopBranch, base:
		return "a long-lived branch"
	}
	switch {
	case strings.HasPrefix(branch, "release/"):
		return "a release branch"
	case strings.HasPrefix(branch, branchPrefix):
		return "a sync branch the developer bot opens itself"
	}
	return ""
}

// branchLabels is the pr-tracking label plus, when the branch is prefixed with
// a Conventional Commit type (feat/, fix/, ...), that type.
func branchLabels(branch string) []string {
	labels := []string{TrackLabel}
	if t, _, ok := strings.Cut(branch, "/"); ok && conventionalTypes[t] {
		labels = append(labels, t)
	}
	return labels
}

func labelColor(name string) string {
	if name == TrackLabel {
		return trackLabelColor
	}
	return typeLabelColor
}

func labelDesc(name string) string {
	if name == TrackLabel {
		return "A pull request the developer bot opened for a branch"
	}
	return "Conventional Commit type: " + name
}

// issueLister is the lookup capability openIssue needs, shared by open and
// resolve.
type issueLister interface {
	ListIssues(ctx context.Context, label string) ([]github.Issue, error)
}

// openIssue returns the open tracking issue titled title, or nil.
func openIssue(ctx context.Context, api issueLister, title string) (*github.Issue, error) {
	issues, err := api.ListIssues(ctx, TrackLabel)
	if err != nil {
		return nil, fmt.Errorf("list tracking issues: %w", err)
	}
	for i := range issues {
		if issues[i].Title == title && issues[i].State == "open" {
			return &issues[i], nil
		}
	}
	return nil, nil
}

func issueBody(branch, base, title string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Tracks the pull request the developer bot opened for %s into %s.\n\n", code(branch), code(base))
	b.WriteString("| | |\n| --- | --- |\n")
	fmt.Fprintf(&b, "| Branch | %s |\n", code(branch))
	fmt.Fprintf(&b, "| Base | %s |\n", code(base))
	fmt.Fprintf(&b, "| Title | %s |\n", code(title))
	return b.String()
}

func pullBody(issue int) string {
	return fmt.Sprintf("Opened by the developer bot on the first push to this branch.\n\nTracks #%d.\n", issue)
}

// OpenSummary is the markdown summary of an Open.
func OpenSummary(res OpenResult, dryRun bool) string {
	switch {
	case res.Skipped != "":
		return fmt.Sprintf("### Open %s\n\nSkipped: %s is %s.\n", res.Branch, code(res.Branch), res.Skipped)
	case res.Existing != nil:
		return fmt.Sprintf("### Open %s\n\n%s already has an open pull request: [#%d](%s).\n",
			res.Branch, code(res.Branch), res.Existing.Number, res.Existing.HTMLURL)
	case dryRun:
		return fmt.Sprintf("### Open %s ← (dry run)\n\nWould open a pull request into %s titled %s and a tracking issue, labelled %s.\n",
			res.Branch, code(res.Base), code(res.Title), labelList(res.Labels))
	case res.Pull != nil && res.Issue != nil:
		return fmt.Sprintf("### Open %s\n\nOpened [#%d](%s) into %s and tracking issue [#%d](%s), labelled %s.\n",
			res.Branch, res.Pull.Number, res.Pull.HTMLURL, code(res.Base),
			res.Issue.Number, res.Issue.HTMLURL, labelList(res.Labels))
	default:
		return fmt.Sprintf("### Open %s\n\nNothing to do.\n", res.Branch)
	}
}

func labelList(labels []string) string {
	out := make([]string, len(labels))
	for i, l := range labels {
		out[i] = code(l)
	}
	return strings.Join(out, ", ")
}
