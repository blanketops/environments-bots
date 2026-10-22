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

// Package tracker keeps one GitHub issue per test file (Sync) and posts each
// run's results to it (Report).
package tracker

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/blanketops/environments-bots/internal/discover"
	"github.com/blanketops/environments-bots/internal/github"
	"github.com/blanketops/environments-bots/internal/results"
)

// Label marks tracking issues. Issues are matched on it plus their title.
const Label = "test-tracking"

const labelColor = "0075ca"

// API is the part of the GitHub client the tracker uses.
type API interface {
	ListIssues(ctx context.Context, label string) ([]github.Issue, error)
	CreateIssue(ctx context.Context, title, body string, labels []string) (github.Issue, error)
	Comment(ctx context.Context, number int, body string) error
	EnsureLabel(ctx context.Context, name, color, description string) error
	FindProject(ctx context.Context, org string, number int) (github.Project, error)
	AddToProject(ctx context.Context, p github.Project, issueNodeID, status string) error
}

// ProjectRef names a Projects (v2) board and the Status new issues get.
type ProjectRef struct {
	Org    string
	Number int
	Status string
}

// Tracked is a test file and its issue. Issue.Number is 0 for an issue a
// dry run would create.
type Tracked struct {
	File  discover.File
	Issue github.Issue
}

// issuesByTitle maps titles to tracking issues; with duplicates, the oldest
// (lowest number) wins.
func issuesByTitle(issues []github.Issue) map[string]github.Issue {
	sort.Slice(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })
	m := map[string]github.Issue{}
	for _, is := range issues {
		if _, ok := m[is.Title]; !ok {
			m[is.Title] = is
		}
	}
	return m
}

// SyncOptions control Sync.
type SyncOptions struct {
	Files []discover.File
	// Project, if set, receives every issue Sync creates.
	Project *ProjectRef
	// Source is shown in new issues as where they were opened from (e.g. a
	// pull request URL).
	Source string
	DryRun bool
}

// SyncResult is what Sync found and did.
type SyncResult struct {
	Existing []Tracked
	Created  []Tracked
	// Warnings are failures that do not stop the sync (project updates).
	Warnings []string
}

// Sync opens an issue for every test file that has none. Existing issues,
// open or closed, are left as they are, so a closed issue is never
// duplicated.
func Sync(ctx context.Context, api API, opts SyncOptions) (SyncResult, error) {
	var res SyncResult
	issues, err := api.ListIssues(ctx, Label)
	if err != nil {
		return res, fmt.Errorf("list issues: %w", err)
	}
	byTitle := issuesByTitle(issues)

	var missing []discover.File
	for _, f := range opts.Files {
		if is, ok := byTitle[f.Title()]; ok {
			res.Existing = append(res.Existing, Tracked{File: f, Issue: is})
		} else {
			missing = append(missing, f)
		}
	}
	if opts.DryRun {
		for _, f := range missing {
			res.Created = append(res.Created, Tracked{File: f})
		}
		return res, nil
	}
	if len(missing) == 0 {
		return res, nil
	}

	labels := map[string]string{Label: "Tracks a test file and its test runs"}
	for _, f := range missing {
		if f.Layer != "" {
			labels[f.Layer] = "Test layer " + f.Layer
		}
	}
	for _, name := range sortedKeys(labels) {
		if err := api.EnsureLabel(ctx, name, labelColor, labels[name]); err != nil {
			return res, fmt.Errorf("label %s: %w", name, err)
		}
	}

	for _, f := range missing {
		issueLabels := []string{Label}
		if f.Layer != "" {
			issueLabels = append(issueLabels, f.Layer)
		}
		is, err := api.CreateIssue(ctx, f.Title(), IssueBody(f, opts.Source), issueLabels)
		if err != nil {
			return res, fmt.Errorf("create issue for %s: %w", f.Path, err)
		}
		res.Created = append(res.Created, Tracked{File: f, Issue: is})
	}

	if opts.Project != nil {
		p, err := api.FindProject(ctx, opts.Project.Org, opts.Project.Number)
		if err != nil {
			res.Warnings = append(res.Warnings, err.Error())
			return res, nil
		}
		for _, t := range res.Created {
			if err := api.AddToProject(ctx, p, t.Issue.NodeID, opts.Project.Status); err != nil {
				res.Warnings = append(res.Warnings, fmt.Sprintf("#%d: %v", t.Issue.Number, err))
			}
		}
	}
	return res, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ReportOptions control Report.
type ReportOptions struct {
	Results []results.FileResults
	Run     RunInfo
	DryRun  bool
}

// Posted is a file whose results were posted (or would be, in a dry run).
type Posted struct {
	Tracked
	Passed, Failed, Skipped int
}

// ReportResult is what Report did.
type ReportResult struct {
	Posted []Posted
	// Untracked files have results but no issue; Sync opens theirs.
	Untracked []discover.File
}

// Report comments each file's results on its issue.
func Report(ctx context.Context, api API, opts ReportOptions) (ReportResult, error) {
	var res ReportResult
	issues, err := api.ListIssues(ctx, Label)
	if err != nil {
		return res, fmt.Errorf("list issues: %w", err)
	}
	byTitle := issuesByTitle(issues)
	for _, fr := range opts.Results {
		is, ok := byTitle[fr.File.Title()]
		if !ok {
			res.Untracked = append(res.Untracked, fr.File)
			continue
		}
		if !opts.DryRun {
			if err := api.Comment(ctx, is.Number, RunComment(fr, opts.Run)); err != nil {
				return res, fmt.Errorf("comment on #%d (%s): %w", is.Number, fr.File.Path, err)
			}
		}
		p, f, s := fr.Counts()
		res.Posted = append(res.Posted, Posted{Tracked: Tracked{File: fr.File, Issue: is}, Passed: p, Failed: f, Skipped: s})
	}
	return res, nil
}

func issueRef(is github.Issue) string {
	switch {
	case is.Number == 0:
		return "(new)"
	case is.HTMLURL != "":
		return fmt.Sprintf("[#%d](%s)", is.Number, is.HTMLURL)
	default:
		return fmt.Sprintf("#%d", is.Number)
	}
}

// SyncSummary renders a sync as markdown for the job summary.
func SyncSummary(res SyncResult, dryRun bool) string {
	var b strings.Builder
	b.WriteString("## Test tracking issues\n\n")
	if dryRun {
		b.WriteString("Dry run: nothing was created.\n\n")
	}
	fmt.Fprintf(&b, "%d test files: %d already tracked, %d %s.\n\n", len(res.Existing)+len(res.Created), len(res.Existing), len(res.Created), map[bool]string{true: "to create", false: "created"}[dryRun])
	if len(res.Created) > 0 || len(res.Existing) > 0 {
		b.WriteString("| File | Issue | |\n| --- | --- | --- |\n")
		for _, t := range res.Created {
			fmt.Fprintf(&b, "| %s | %s | new |\n", code(t.File.Path), issueRef(t.Issue))
		}
		for _, t := range res.Existing {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", code(t.File.Path), issueRef(t.Issue), t.Issue.State)
		}
	}
	if len(res.Warnings) > 0 {
		b.WriteString("\nWarnings:\n\n")
		for _, w := range res.Warnings {
			fmt.Fprintf(&b, "- %s\n", code(w))
		}
	}
	return b.String()
}

// ReportSummary renders a report as markdown for the job summary.
func ReportSummary(res ReportResult, dryRun bool) string {
	var b strings.Builder
	b.WriteString("## Test results posted to issues\n\n")
	if dryRun {
		b.WriteString("Dry run: nothing was posted.\n\n")
	}
	if len(res.Posted) > 0 {
		b.WriteString("| File | Issue | Passed | Failed | Skipped |\n| --- | --- | --- | --- | --- |\n")
		for _, p := range res.Posted {
			fmt.Fprintf(&b, "| %s | %s | %d | %d | %d |\n", code(p.File.Path), issueRef(p.Issue), p.Passed, p.Failed, p.Skipped)
		}
	} else {
		b.WriteString("No test file had results in this run.\n")
	}
	if len(res.Untracked) > 0 {
		b.WriteString("\nNo tracking issue yet (`test-helper sync` opens them):\n\n")
		for _, f := range res.Untracked {
			fmt.Fprintf(&b, "- %s\n", code(f.Path))
		}
	}
	return b.String()
}
