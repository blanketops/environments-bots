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

// Package release drives every release of a repository through its
// Release issue ("Release: vX.Y.Z", label release) on the project board.
//
// Plan notices that develop has changes no release carries yet and opens
// the issue for the next version. Cut checks the release (Prepare) and
// cuts its branch and pull request into main. A person finalizes the
// release (merge and tag). Verify runs before that merge, and Complete,
// after the GitHub release is published, attaches it to the issue and
// closes it. Each records its checks as a comment on the Release issue; a
// failed check fails the command and leaves the issue open.
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

// DefaultBot is the login the Releaser App's installation token acts as.
// Release issues it opened are its own to plan, rename and flag; issues a
// person opened are only reused, never renamed or flagged.
const DefaultBot = "blanketops-environments-releaser[bot]"

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
	BranchSHA(ctx context.Context, branch string) (string, error)
	CreateBranch(ctx context.Context, branch, sha string) error
	CreatePull(ctx context.Context, title, head, base, body string) (github.Pull, error)
	RenameIssue(ctx context.Context, number int, title string) error
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
	// Version is the release, vX.Y.Z. Cut works it out when it is empty;
	// Plan ignores it.
	Version string
	// Ref is the branch releases are cut from (Plan, Prepare, Cut).
	Ref string
	// Project, if set, is the board the Release issue is kept on.
	Project *ProjectRef
	// RunURL links the workflow run in comments.
	RunURL string
	// DryRun only reads: nothing is created, commented or closed.
	DryRun bool
	// Bot is the releaser's own login (default DefaultBot).
	Bot string
}

func (o Options) bot() string {
	if o.Bot != "" {
		return o.Bot
	}
	return DefaultBot
}

// Check is one named check and its outcome.
type Check struct {
	Name   string
	OK     bool
	Detail string
}

// Stage names a command in comments and summaries.
type Stage string

// The stages, in the order a release goes through them.
const (
	StagePlan     Stage = "plan"
	StagePrepare  Stage = "prepare"
	StageCut      Stage = "cut"
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
	// Note says why Plan did nothing (nothing to release, a release in
	// progress).
	Note string
	// RenamedFrom is the version Plan moved the planned issue away from.
	RenamedFrom *semver.Version
	// CutSHA is the commit Cut created the release branch at.
	CutSHA string
	// Ref is the branch releases are cut from.
	Ref string
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
	return &Result{Stage: stage, Version: v, Ref: opts.Ref, RunURL: opts.RunURL, DryRun: opts.DryRun}, nil
}

// Prepare checks that Version can be released from Ref and opens (or
// reuses) its Release issue on the board with status Release.
//
// It fails when the version is already tagged or is not the next version
// after the latest release, when the bump is smaller than the commits
// need, when the Release issue is duplicated or closed, or when the
// version's milestone still has open items.
func Prepare(ctx context.Context, api API, opts Options) (*Result, error) {
	r, err := prepare(ctx, api, opts)
	if err != nil {
		return nil, err
	}
	return r, r.comment(ctx, api)
}

// prepare is Prepare without the comment.
func prepare(ctx context.Context, api API, opts Options) (*Result, error) {
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
			r.check("Version", false, "%s does not follow the latest release %s; the next version is %s",
				r.Version, prev, successors(*prev))
		}
	}

	commits, err := r.commitsSince(ctx, api, prev, opts.Ref)
	if err != nil {
		return nil, err
	}
	if len(commits) == 0 {
		r.check("Changes", false, "%s has no commits since %s; there is nothing to release", opts.Ref, prevName(prev))
	} else {
		r.check("Changes", true, "%d commit(s) on %s since %s", len(commits), opts.Ref, prevName(prev))
	}
	if prev != nil && versionOK {
		r.Required = semver.Required(*prev, commits)
		chosen, _ := r.Version.StepFrom(*prev)
		// By version, not by bump name: with carries, v0.0.9 to v0.1.0 is
		// both the next patch and the next minor.
		if r.Version.Compare(prev.Next(r.Required)) < 0 {
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
	r.staleIssues(issues, prev, opts.bot())
	// Releases go out in version order: a lower release the releaser
	// planned comes first.
	for _, is := range planned(issues, prev, opts.bot()) {
		if v := issueVersion(is); v.Compare(r.Version) < 0 {
			versionOK = false
			r.check("Release order", false, "#%d plans %s, which comes first: release it, or close it, before %s", is.Number, v, r.Version)
			break
		}
	}
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
	return r, nil
}

// commitsSince returns the non-merge commits on ref since prev (all of
// ref's history when prev is nil), oldest first, records them on r and
// warns about subjects that are not Conventional Commits.
func (r *Result) commitsSince(ctx context.Context, api API, prev *semver.Version, ref string) ([]semver.Commit, error) {
	var raw []github.Commit
	var err error
	if prev != nil {
		raw, err = api.Compare(ctx, prev.String(), ref)
	} else {
		raw, err = api.ListCommits(ctx, ref)
		for i, j := 0, len(raw)-1; i < j; i, j = i+1, j-1 {
			raw[i], raw[j] = raw[j], raw[i]
		}
	}
	if err != nil {
		return nil, fmt.Errorf("commits of %s: %w", ref, err)
	}
	var commits []semver.Commit
	for _, c := range raw {
		pc := semver.ParseCommit(c.SHA, c.Message)
		if pc.Merge || c.Parents > 1 {
			continue
		}
		commits = append(commits, pc)
		if !pc.Conventional() {
			r.warn("%s does not follow Conventional Commits and is left out of the changelog: %s", short(pc.SHA), code(pc.Subject))
		}
	}
	r.Commits = commits
	return commits, nil
}

// successors lists the distinct single-digit versions that may follow prev,
// smallest first: "v0.1.0 or v1.0.0" after v0.0.9.
func successors(prev semver.Version) string {
	var out []string
	seen := map[semver.Version]bool{}
	for _, b := range []semver.Bump{semver.Patch, semver.Minor, semver.Major} {
		if v := prev.Next(b); v.Valid() && !seen[v] {
			seen[v] = true
			out = append(out, v.String())
		}
	}
	switch len(out) {
	case 0:
		return "none: no single-digit version follows it"
	case 1:
		return out[0]
	default:
		return strings.Join(out[:len(out)-1], ", ") + " or " + out[len(out)-1]
	}
}

// next is the version the commits need after prev: v0.0.1 for a first
// release.
func next(prev *semver.Version, commits []semver.Commit) semver.Version {
	if prev == nil {
		return semver.Version{Patch: 1}
	}
	return prev.Next(semver.Required(*prev, commits))
}

// planned returns the open Release issues the releaser (bot) opened for
// versions above prev (every version when prev is nil), lowest version
// first.
func planned(issues map[string][]github.Issue, prev *semver.Version, bot string) []github.Issue {
	type entry struct {
		v  semver.Version
		is github.Issue
	}
	var es []entry
	for k, list := range issues {
		v, err := semver.Parse(k)
		if err != nil || prev != nil && v.Compare(*prev) <= 0 {
			continue
		}
		for _, is := range list {
			if is.State == "open" && is.User.Login == bot {
				es = append(es, entry{v, is})
			}
		}
	}
	sort.Slice(es, func(i, j int) bool {
		if c := es[i].v.Compare(es[j].v); c != 0 {
			return c < 0
		}
		return es[i].is.Number < es[j].is.Number
	})
	out := make([]github.Issue, len(es))
	for i, e := range es {
		out[i] = e.is
	}
	return out
}

func issueVersion(is github.Issue) semver.Version {
	v, _ := semver.Parse(strings.TrimSpace(strings.TrimPrefix(is.Title, "Release: ")))
	return v
}

// Plan notices that Ref has changes no release carries yet and keeps one
// Release issue planned for them, on the board as Release.
//
// With nothing to release, or a planned release whose branch is already
// cut, it does nothing. With no planned release it opens the issue for the
// version the commits need. When the planned version no longer covers the
// commits (a breaking change landed before its branch was cut), it renames
// the issue to the version they need and says why.
func Plan(ctx context.Context, api API, opts Options) (*Result, error) {
	if opts.Ref == "" {
		return nil, fmt.Errorf("no ref to release from")
	}
	r := &Result{Stage: StagePlan, Ref: opts.Ref, RunURL: opts.RunURL, DryRun: opts.DryRun}
	tags, err := api.ListTags(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	if v, ok := semver.Latest(tags); ok {
		r.Previous = &v
	}
	commits, err := r.commitsSince(ctx, api, r.Previous, opts.Ref)
	if err != nil {
		return nil, err
	}
	if len(commits) == 0 {
		r.Note = fmt.Sprintf("nothing to release: %s has no commits since %s", opts.Ref, prevName(r.Previous))
		return r, nil
	}
	need := next(r.Previous, commits)
	if !need.Valid() {
		r.check("Version", false, "the commits need a release after %s, and no single-digit version follows it", r.Previous)
		return r, nil
	}
	if r.Previous != nil {
		r.Required = semver.Required(*r.Previous, commits)
	}
	issues, err := releaseIssues(ctx, api)
	if err != nil {
		return nil, err
	}
	r.staleIssues(issues, r.Previous, opts.bot())
	open := planned(issues, r.Previous, opts.bot())
	if len(open) == 0 {
		// A person may already plan this version (an issue the releaser
		// did not open): it is adopted as it is, never duplicated.
		for _, is := range issues[need.String()] {
			if is.State == "open" {
				r.Issue, r.Version = is, need
				r.Note = fmt.Sprintf("#%d plans %s, which covers the %d commit(s) since %s", is.Number, need, len(commits), prevName(r.Previous))
				return r, nil
			}
		}
		return r, r.openPlanned(ctx, api, opts, need)
	}
	r.Issue, r.Version = open[0], issueVersion(open[0])
	for _, other := range open[1:] {
		r.warn("#%d (%s) is also planned; only %s is released next", other.Number, issueVersion(other), r.Version)
	}
	if _, err := api.BranchSHA(ctx, Branch(r.Version)); err == nil {
		r.Note = fmt.Sprintf("%s is in progress on %s (#%d); finalize it before the next release is planned", r.Version, Branch(r.Version), r.Issue.Number)
		return r, nil
	} else if !github.IsNotFound(err) {
		return nil, fmt.Errorf("branch %s: %w", Branch(r.Version), err)
	}
	if r.Version.Compare(need) >= 0 {
		r.Note = fmt.Sprintf("#%d plans %s, which covers the %d commit(s) since %s", r.Issue.Number, r.Version, len(commits), prevName(r.Previous))
		return r, nil
	}
	if len(issues[need.String()]) > 0 {
		r.check("Version", false, "#%d plans %s but the commits need %s, and %s already has a Release issue (%s)",
			r.Issue.Number, r.Version, need, need, numbers(issues[need.String()]))
		return r, r.comment(ctx, api)
	}
	old := r.Version
	r.RenamedFrom, r.Version = &old, need
	r.check("Version", true, "moved from %s to %s: the commits need a %s release: %s", old, need, r.Required, needing(*r.Previous, commits, r.Required))
	if !opts.DryRun {
		if err := api.RenameIssue(ctx, r.Issue.Number, Title(need)); err != nil {
			return nil, fmt.Errorf("rename #%d: %w", r.Issue.Number, err)
		}
		r.Issue.Title = Title(need)
	}
	return r, r.comment(ctx, api)
}

// openPlanned opens the Release issue of v and puts it on the board.
func (r *Result) openPlanned(ctx context.Context, api API, opts Options, v semver.Version) error {
	r.Version = v
	if r.Previous != nil {
		r.check("Version", true, "%s, the %s release the commits need after %s", v, r.Required, r.Previous)
	} else {
		r.check("Version", true, "%s, the first release", v)
	}
	if opts.DryRun {
		r.check("Release issue", true, "would open %q", Title(v))
		return nil
	}
	if err := api.EnsureLabel(ctx, Label, labelColor, "Tracks one release"); err != nil {
		return fmt.Errorf("label %s: %w", Label, err)
	}
	is, err := api.CreateIssue(ctx, Title(v), IssueBody(v, opts.Ref), []string{Label})
	if err != nil {
		return fmt.Errorf("open %q: %w", Title(v), err)
	}
	r.Issue, r.IssueCreated = is, true
	r.check("Release issue", true, "opened #%d", is.Number)
	r.board(ctx, api, opts.Project, StatusPlanned)
	return r.comment(ctx, api)
}

// Cut cuts the release: it runs Prepare's checks and, when they pass,
// creates the release branch at Ref's head and opens its pull request into
// main (both left as they are when they already exist, so a rerun is
// safe). Without a Version it releases the planned Release issue, or the
// version the commits need. Finalizing (merge and tag) stays with a
// person.
func Cut(ctx context.Context, api API, opts Options) (*Result, error) {
	if opts.Ref == "" {
		return nil, fmt.Errorf("no ref to release from")
	}
	if opts.Version == "" {
		v, err := resolve(ctx, api, opts.Ref, opts.bot())
		if err != nil {
			return nil, err
		}
		opts.Version = v.String()
	}
	r, err := prepare(ctx, api, opts)
	if err != nil {
		return nil, err
	}
	r.Stage = StageCut
	if r.Failed() {
		return r, r.comment(ctx, api)
	}
	branch := Branch(r.Version)
	_, err = api.BranchSHA(ctx, branch)
	switch {
	case err == nil:
		r.check("Release branch", true, "%s already exists", code(branch))
	case !github.IsNotFound(err):
		return nil, fmt.Errorf("branch %s: %w", branch, err)
	case opts.DryRun:
		r.check("Release branch", true, "would create %s from %s", code(branch), code(opts.Ref))
	default:
		sha, err := api.BranchSHA(ctx, opts.Ref)
		if err != nil {
			return nil, fmt.Errorf("branch %s: %w", opts.Ref, err)
		}
		if err := api.CreateBranch(ctx, branch, sha); err != nil {
			return nil, fmt.Errorf("create %s: %w", branch, err)
		}
		r.CutSHA = sha
		r.check("Release branch", true, "created %s at %s from %s", code(branch), code(short(sha)), code(opts.Ref))
	}
	pulls, err := api.ListPulls(ctx, "open", MainBranch, branch)
	if err != nil {
		return nil, fmt.Errorf("release pull request: %w", err)
	}
	switch {
	case len(pulls) > 0:
		r.ReleasePR = &pulls[0]
		r.check("Release pull request", true, "#%d is open", pulls[0].Number)
	case opts.DryRun:
		r.check("Release pull request", true, "would open one from %s into %s", code(branch), MainBranch)
	default:
		pr, err := api.CreatePull(ctx, "release: "+r.Version.String(), branch, MainBranch, PullBody(r))
		if err != nil {
			return nil, fmt.Errorf("open the release pull request: %w", err)
		}
		r.ReleasePR = &pr
		r.check("Release pull request", true, "opened #%d", pr.Number)
	}
	return r, r.comment(ctx, api)
}

// resolve picks the version Cut releases when none is given: the planned
// Release issue's, else the version the commits need.
func resolve(ctx context.Context, api API, ref, bot string) (semver.Version, error) {
	tags, err := api.ListTags(ctx)
	if err != nil {
		return semver.Version{}, fmt.Errorf("list tags: %w", err)
	}
	var prev *semver.Version
	if v, ok := semver.Latest(tags); ok {
		prev = &v
	}
	issues, err := releaseIssues(ctx, api)
	if err != nil {
		return semver.Version{}, err
	}
	if open := planned(issues, prev, bot); len(open) > 0 {
		return issueVersion(open[0]), nil
	}
	var scratch Result
	commits, err := scratch.commitsSince(ctx, api, prev, ref)
	if err != nil {
		return semver.Version{}, err
	}
	return next(prev, commits), nil
}

// staleIssues warns about open Release issues the releaser (bot) opened
// that no release will close: their version is already released or below
// the latest release. Issues a person opened are theirs to keep.
func (r *Result) staleIssues(issues map[string][]github.Issue, prev *semver.Version, bot string) {
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
			case is.State != "open" || is.User.Login != bot:
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
		merged, err := api.ListPulls(ctx, "closed", MainBranch, branch)
		if err != nil {
			return nil, fmt.Errorf("release pull request: %w", err)
		}
		for i := range merged {
			if merged[i].MergedAt != nil {
				r.ReleasePR = &merged[i]
				break
			}
		}
		if r.ReleasePR != nil {
			r.warn("the release pull request #%d is already merged into %s; finalize continues from %s", r.ReleasePR.Number, MainBranch, MainBranch)
		} else {
			r.warn("no open pull request from %s into %s; finalize opens one", branch, MainBranch)
		}
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
