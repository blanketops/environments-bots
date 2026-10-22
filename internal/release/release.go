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

// Package release keeps every release of a repository attached to its
// Release issue ("Release: vX.Y.Z", label release) on the project board.
//
// Prepare runs before the release branch is cut, Verify before it is
// finalized, and Complete after the GitHub release is published. Each
// records its checks as a comment on the Release issue; a failed check
// fails the command and leaves the issue open.
package release

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/blanketops/environments-bots/internal/github"
	"github.com/blanketops/environments-bots/internal/semver"
)

// Label marks Release issues.
const Label = "release"

const labelColor = "5319e7"

// Board statuses of a Release issue before and after its release.
const (
	StatusPlanned = "Release"
	StatusDone    = "Done"
)

// MainBranch is where releases are merged; release branches are
// release/<version>.
const MainBranch = "main"

// API is the part of the GitHub client releases use.
type API interface {
	ListIssues(ctx context.Context, label string) ([]github.Issue, error)
	CreateIssue(ctx context.Context, title, body string, labels []string) (github.Issue, error)
	Comment(ctx context.Context, number int, body string) error
	CloseIssue(ctx context.Context, number int) error
	EnsureLabel(ctx context.Context, name, color, description string) error
	FindProject(ctx context.Context, org string, number int) (github.Project, error)
	AddToProject(ctx context.Context, p github.Project, issueNodeID, status string) error
	ListTags(ctx context.Context) ([]string, error)
	Compare(ctx context.Context, base, head string) ([]github.Commit, error)
	ListCommits(ctx context.Context, ref string) ([]github.Commit, error)
	TagSHA(ctx context.Context, tag string) (string, error)
	ReleaseByTag(ctx context.Context, tag string) (github.Release, error)
	ListMilestones(ctx context.Context) ([]github.Milestone, error)
	OpenMilestoneItems(ctx context.Context, number int) ([]github.Issue, error)
	CloseMilestone(ctx context.Context, number int) error
	ListPulls(ctx context.Context, state, base, head string) ([]github.Pull, error)
}

// Title is the title of the Release issue of v.
func Title(v semver.Version) string { return "Release: " + v.String() }

// Branch is the release branch of v.
func Branch(v semver.Version) string { return "release/" + v.String() }

// ProjectRef names the organization's Projects (v2) board.
type ProjectRef struct {
	Org    string
	Number int
}

// Options control every command.
type Options struct {
	// Version is the release, vX.Y.Z.
	Version string
	// Ref is the branch the release is cut from (Prepare only).
	Ref string
	// Project, if set, is the board the Release issue is kept on.
	Project *ProjectRef
	// RunURL links the workflow run in comments.
	RunURL string
	// DryRun only reads: nothing is created, commented or closed.
	DryRun bool
}

// Check is one named check and its outcome.
type Check struct {
	Name   string
	OK     bool
	Detail string
}

// Stage names a command in comments and summaries.
type Stage string

// The three stages.
const (
	StagePrepare  Stage = "prepare"
	StageVerify   Stage = "verify"
	StageComplete Stage = "complete"
)

// Result is what a command found and did.
type Result struct {
	Stage   Stage
	Version semver.Version
	// Previous is the latest release other than Version (Prepare); nil
	// for a first release.
	Previous *semver.Version
	// Commits are the non-merge commits of the release (Prepare).
	Commits  []semver.Commit
	Required semver.Bump
	// Issue is the Release issue; Number 0 when there is none (or a dry
	// run would create it).
	Issue        github.Issue
	IssueCreated bool
	Milestone    *github.Milestone
	Checks       []Check
	// Warnings do not fail the command.
	Warnings []string
	// Set by Complete.
	Release   *github.Release
	TagSHA    string
	ReleasePR *github.Pull
	Closed    bool
	RunURL    string
	DryRun    bool
}

// Failed reports whether any check failed.
func (r *Result) Failed() bool {
	for _, c := range r.Checks {
		if !c.OK {
			return true
		}
	}
	return false
}

func (r *Result) check(name string, ok bool, format string, args ...any) {
	r.Checks = append(r.Checks, Check{Name: name, OK: ok, Detail: fmt.Sprintf(format, args...)})
}

func (r *Result) warn(format string, args ...any) {
	r.Warnings = append(r.Warnings, fmt.Sprintf(format, args...))
}

// releaseIssues returns the Release issues (not pull requests) of the
// repository, oldest first, keyed by the version in their title. Titles
// that carry no release version are returned under "".
func releaseIssues(ctx context.Context, api API) (map[string][]github.Issue, error) {
	all, err := api.ListIssues(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("list issues: %w", err)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Number < all[j].Number })
	out := map[string][]github.Issue{}
	for _, is := range all {
		rest, ok := strings.CutPrefix(is.Title, "Release: ")
		if !ok || is.PullRequest != nil {
			continue
		}
		key := ""
		if v, err := semver.Parse(strings.TrimSpace(rest)); err == nil {
			key = v.String()
		}
		out[key] = append(out[key], is)
	}
	return out, nil
}

func numbers(issues []github.Issue) string {
	s := make([]string, len(issues))
	for i, is := range issues {
		s[i] = fmt.Sprintf("#%d", is.Number)
	}
	return strings.Join(s, ", ")
}

// latest returns the highest release tag other than v, nil when there is
// none, and whether v itself is tagged.
func latest(ctx context.Context, api API, v semver.Version) (*semver.Version, bool, error) {
	tags, err := api.ListTags(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("list tags: %w", err)
	}
	tagged := false
	var others []string
	for _, t := range tags {
		if t == v.String() {
			tagged = true
		} else {
			others = append(others, t)
		}
	}
	prev, ok := semver.Latest(others)
	if !ok {
		return nil, tagged, nil
	}
	return &prev, tagged, nil
}

// milestone finds the open or closed milestone titled v (with or without
// the leading v) and its open items other than the Release issue.
func milestone(ctx context.Context, api API, v semver.Version, issue int) (*github.Milestone, []github.Issue, error) {
	ms, err := api.ListMilestones(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list milestones: %w", err)
	}
	for _, m := range ms {
		t := strings.TrimSpace(m.Title)
		if t != v.String() && t != strings.TrimPrefix(v.String(), "v") {
			continue
		}
		items, err := api.OpenMilestoneItems(ctx, m.Number)
		if err != nil {
			return nil, nil, fmt.Errorf("milestone %q: %w", m.Title, err)
		}
		var open []github.Issue
		for _, is := range items {
			if is.Number != issue {
				open = append(open, is)
			}
		}
		return &m, open, nil
	}
	return nil, nil, nil
}

func (r *Result) checkMilestone(ctx context.Context, api API) error {
	m, open, err := milestone(ctx, api, r.Version, r.Issue.Number)
	if err != nil {
		return err
	}
	r.Milestone = m
	switch {
	case m == nil:
		r.check("Milestone", true, "no milestone %s; nothing else is planned for this release", r.Version)
	case len(open) > 0:
		r.check("Milestone", false, "%d item(s) of milestone %s still open: %s", len(open), code(m.Title), numbers(open))
	default:
		r.check("Milestone", true, "every item of milestone %s is closed", code(m.Title))
	}
	return nil
}

// board puts the Release issue on the project with status; a failure is a
// warning, as the release itself is unaffected.
func (r *Result) board(ctx context.Context, api API, p *ProjectRef, status string) {
	if p == nil || r.Issue.Number == 0 || r.DryRun {
		return
	}
	proj, err := api.FindProject(ctx, p.Org, p.Number)
	if err == nil {
		err = api.AddToProject(ctx, proj, r.Issue.NodeID, status)
	}
	if err != nil {
		r.warn("could not set #%d to %s on project %s/%d: %v", r.Issue.Number, status, p.Org, p.Number, err)
	}
}

func (r *Result) comment(ctx context.Context, api API) error {
	if r.Issue.Number == 0 || r.DryRun {
		return nil
	}
	if err := api.Comment(ctx, r.Issue.Number, Comment(r)); err != nil {
		return fmt.Errorf("comment on #%d: %w", r.Issue.Number, err)
	}
	return nil
}

func start(stage Stage, opts Options) (*Result, error) {
	v, err := semver.Parse(opts.Version)
	if err != nil {
		return nil, err
	}
	return &Result{Stage: stage, Version: v, RunURL: opts.RunURL, DryRun: opts.DryRun}, nil
}

// Prepare checks that Version can be released from Ref and opens (or
// reuses) its Release issue on the board with status Release.
//
// It fails when the version is already tagged or is not the next version
// after the latest release, when the bump is smaller than the commits
// need, when the Release issue is duplicated or closed, or when the
// version's milestone still has open items.
func Prepare(ctx context.Context, api API, opts Options) (*Result, error) {
	r, err := start(StagePrepare, opts)
	if err != nil {
		return nil, err
	}
	if opts.Ref == "" {
		return nil, fmt.Errorf("no ref to release from")
	}
	prev, tagged, err := latest(ctx, api, r.Version)
	if err != nil {
		return nil, err
	}
	r.Previous = prev

	versionOK := false
	switch {
	case tagged:
		r.check("Version", false, "%s is already tagged", r.Version)
	case prev == nil:
		versionOK = true
		r.check("Version", true, "%s is the first release", r.Version)
	default:
		if bump, ok := r.Version.StepFrom(*prev); ok {
			versionOK = true
			r.check("Version", true, "%s is the %s release after %s", r.Version, bump, prev)
		} else {
			r.check("Version", false, "%s does not follow the latest release %s; the next version is %s, %s or %s",
				r.Version, prev, prev.Next(semver.Patch), prev.Next(semver.Minor), prev.Next(semver.Major))
		}
	}

	var raw []github.Commit
	if prev != nil {
		raw, err = api.Compare(ctx, prev.String(), opts.Ref)
	} else {
		raw, err = api.ListCommits(ctx, opts.Ref)
		for i, j := 0, len(raw)-1; i < j; i, j = i+1, j-1 {
			raw[i], raw[j] = raw[j], raw[i]
		}
	}
	if err != nil {
		return nil, fmt.Errorf("commits of %s: %w", opts.Ref, err)
	}
	var commits []semver.Commit
	for _, c := range raw {
		pc := semver.ParseCommit(c.SHA, c.Message)
		if pc.Merge {
			continue
		}
		commits = append(commits, pc)
		if !pc.Conventional() {
			r.warn("%s does not follow Conventional Commits and is left out of the changelog: %s", short(pc.SHA), code(pc.Subject))
		}
	}
	r.Commits = commits
	if len(commits) == 0 {
		r.check("Changes", false, "%s has no commits since %s; there is nothing to release", opts.Ref, prevName(prev))
	} else {
		r.check("Changes", true, "%d commit(s) on %s since %s", len(commits), opts.Ref, prevName(prev))
	}
	if prev != nil && versionOK {
		r.Required = semver.Required(*prev, commits)
		chosen, _ := r.Version.StepFrom(*prev)
		if chosen < r.Required {
			r.check("Semantic version", false, "the commits need a %s release (%s), not %s: %s",
				r.Required, prev.Next(r.Required), r.Version, needing(*prev, commits, r.Required))
		} else {
			r.check("Semantic version", true, "a %s release; the commits need at least %s", chosen, r.Required)
		}
	}

	issues, err := releaseIssues(ctx, api)
	if err != nil {
		return nil, err
	}
	r.staleIssues(issues, prev)
	mine := issues[r.Version.String()]
	switch {
	case len(mine) > 1:
		r.check("Release issue", false, "%s has %d Release issues (%s); close the duplicates first", r.Version, len(mine), numbers(mine))
	case len(mine) == 1 && mine[0].State != "open":
		r.check("Release issue", false, "#%d is closed; reopen it if %s is still to be released", mine[0].Number, r.Version)
	case len(mine) == 1:
		r.Issue = mine[0]
		r.check("Release issue", true, "#%d", r.Issue.Number)
	case !versionOK:
		r.check("Release issue", false, "none, and none is opened for a version that cannot be released")
	case opts.DryRun:
		r.check("Release issue", true, "would open %q", Title(r.Version))
	default:
		if err := api.EnsureLabel(ctx, Label, labelColor, "Tracks one release"); err != nil {
			return nil, fmt.Errorf("label %s: %w", Label, err)
		}
		is, err := api.CreateIssue(ctx, Title(r.Version), IssueBody(r.Version, opts.Ref), []string{Label})
		if err != nil {
			return nil, fmt.Errorf("open %q: %w", Title(r.Version), err)
		}
		r.Issue, r.IssueCreated = is, true
		r.check("Release issue", true, "opened #%d", is.Number)
	}
	if err := r.checkMilestone(ctx, api); err != nil {
		return nil, err
	}
	r.board(ctx, api, opts.Project, StatusPlanned)
	return r, r.comment(ctx, api)
}

// staleIssues warns about open Release issues no release will close: their
// version is already released or below the latest release.
func (r *Result) staleIssues(issues map[string][]github.Issue, prev *semver.Version) {
	var keys []string
	for k := range issues {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if k == r.Version.String() {
			continue
		}
		v, err := semver.Parse(k)
		for _, is := range issues[k] {
			switch {
			case is.State != "open":
			case err != nil:
				r.warn("#%d is open but its title %s names no release version", is.Number, code(is.Title))
			case prev != nil && v.Compare(*prev) <= 0:
				r.warn("#%d (%s) is open but %s is not above the latest release %s: close it, or attach it to that release", is.Number, k, k, prev)
			}
		}
	}
}

func prevName(prev *semver.Version) string {
	if prev == nil {
		return "the start of the history"
	}
	return prev.String()
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// needing lists the commits that make the release need bump.
func needing(prev semver.Version, commits []semver.Commit, bump semver.Bump) string {
	var out []string
	for _, c := range commits {
		if semver.Required(prev, []semver.Commit{c}) == bump {
			out = append(out, code(short(c.SHA))+" "+code(c.Subject))
		}
	}
	return strings.Join(out, "; ")
}

// Verify checks, before the release branch is finalized, that Version is
// not tagged yet, has one open Release issue, no pull request still open
// into its release branch, and no open milestone item.
func Verify(ctx context.Context, api API, opts Options) (*Result, error) {
	r, err := start(StageVerify, opts)
	if err != nil {
		return nil, err
	}
	if _, err := api.TagSHA(ctx, r.Version.String()); err == nil {
		r.check("Version", false, "%s is already tagged", r.Version)
	} else if github.IsNotFound(err) {
		r.check("Version", true, "%s is not tagged yet", r.Version)
	} else {
		return nil, fmt.Errorf("tag %s: %w", r.Version, err)
	}
	issues, err := releaseIssues(ctx, api)
	if err != nil {
		return nil, err
	}
	r.oneOpenIssue(issues[r.Version.String()])

	branch := Branch(r.Version)
	into, err := api.ListPulls(ctx, "open", branch, "")
	if err != nil {
		return nil, fmt.Errorf("pull requests into %s: %w", branch, err)
	}
	if len(into) > 0 {
		var ns []string
		for _, p := range into {
			ns = append(ns, fmt.Sprintf("#%d", p.Number))
		}
		r.check("Release branch", false, "%d pull request(s) still open into %s: %s", len(into), branch, strings.Join(ns, ", "))
	} else {
		r.check("Release branch", true, "no pull request open into %s", branch)
	}
	rp, err := api.ListPulls(ctx, "open", MainBranch, branch)
	if err != nil {
		return nil, fmt.Errorf("release pull request: %w", err)
	}
	if len(rp) == 0 {
		r.warn("no open pull request from %s into %s; finalize opens one", branch, MainBranch)
	} else {
		r.ReleasePR = &rp[0]
	}
	if err := r.checkMilestone(ctx, api); err != nil {
		return nil, err
	}
	return r, r.comment(ctx, api)
}

func (r *Result) oneOpenIssue(mine []github.Issue) {
	var open []github.Issue
	for _, is := range mine {
		if is.State == "open" {
			open = append(open, is)
		}
	}
	switch {
	case len(mine) > 1:
		r.check("Release issue", false, "%s has %d Release issues (%s); close the duplicates first", r.Version, len(mine), numbers(mine))
	case len(open) == 1:
		r.Issue = open[0]
		r.check("Release issue", true, "#%d", r.Issue.Number)
	case len(mine) == 1:
		r.check("Release issue", false, "#%d is closed", mine[0].Number)
	default:
		r.check("Release issue", false, "%s has no Release issue; run prepare first", r.Version)
	}
}

// Complete attaches the published release of Version to its Release
// issue: it checks that the tag and the GitHub release exist, comments
// both on the issue, closes it, sets it to Done on the board and closes
// the version's milestone. A missing Release issue is opened first, so
// every release has one. When a check fails the issue stays open with a
// comment saying what is missing.
func Complete(ctx context.Context, api API, opts Options) (*Result, error) {
	r, err := start(StageComplete, opts)
	if err != nil {
		return nil, err
	}
	sha, err := api.TagSHA(ctx, r.Version.String())
	switch {
	case err == nil:
		r.TagSHA = sha
		r.check("Tag", true, "%s at %s", r.Version, short(sha))
	case github.IsNotFound(err):
		r.check("Tag", false, "%s is not tagged", r.Version)
	default:
		return nil, fmt.Errorf("tag %s: %w", r.Version, err)
	}
	rel, err := api.ReleaseByTag(ctx, r.Version.String())
	switch {
	case err == nil:
		r.Release = &rel
		r.check("GitHub release", true, "%s", rel.HTMLURL)
		if rel.Prerelease {
			r.warn("the release is marked as a pre-release")
		}
	case github.IsNotFound(err):
		r.check("GitHub release", false, "no published release for %s", r.Version)
	default:
		return nil, fmt.Errorf("release %s: %w", r.Version, err)
	}
	merged, err := api.ListPulls(ctx, "closed", MainBranch, Branch(r.Version))
	if err != nil {
		return nil, fmt.Errorf("release pull request: %w", err)
	}
	for i := range merged {
		if merged[i].MergedAt != nil {
			r.ReleasePR = &merged[i]
			break
		}
	}
	if r.ReleasePR == nil {
		r.warn("found no merged pull request from %s into %s", Branch(r.Version), MainBranch)
	}

	issues, err := releaseIssues(ctx, api)
	if err != nil {
		return nil, err
	}
	mine := issues[r.Version.String()]
	switch {
	case len(mine) > 1:
		r.check("Release issue", false, "%s has %d Release issues (%s); close the duplicates, then rerun", r.Version, len(mine), numbers(mine))
	case len(mine) == 1:
		r.Issue = mine[0]
		r.check("Release issue", true, "#%d", r.Issue.Number)
	case opts.DryRun:
		r.check("Release issue", true, "would open %q", Title(r.Version))
	default:
		if err := api.EnsureLabel(ctx, Label, labelColor, "Tracks one release"); err != nil {
			return nil, fmt.Errorf("label %s: %w", Label, err)
		}
		is, err := api.CreateIssue(ctx, Title(r.Version), IssueBody(r.Version, MainBranch), []string{Label})
		if err != nil {
			return nil, fmt.Errorf("open %q: %w", Title(r.Version), err)
		}
		r.Issue, r.IssueCreated = is, true
		r.check("Release issue", true, "opened #%d; prepare did not run for this release", is.Number)
	}
	m, open, err := milestone(ctx, api, r.Version, r.Issue.Number)
	if err != nil {
		return nil, err
	}
	r.Milestone = m
	if m != nil && len(open) > 0 {
		r.warn("milestone %s still has %d open item(s) (%s) and is left open", code(m.Title), len(open), numbers(open))
	}

	if r.Failed() || r.DryRun {
		return r, r.comment(ctx, api)
	}
	// The comment says the issue is closed; it is closed right after.
	alreadyClosed := r.Issue.State == "closed"
	r.Closed = true
	if err := r.comment(ctx, api); err != nil {
		return nil, err
	}
	if !alreadyClosed {
		if err := api.CloseIssue(ctx, r.Issue.Number); err != nil {
			return nil, fmt.Errorf("close #%d: %w", r.Issue.Number, err)
		}
	}
	r.board(ctx, api, opts.Project, StatusDone)
	if m != nil && len(open) == 0 && m.State == "open" {
		if err := api.CloseMilestone(ctx, m.Number); err != nil {
			r.warn("could not close milestone %s: %v", code(m.Title), err)
		}
	}
	return r, nil
}
