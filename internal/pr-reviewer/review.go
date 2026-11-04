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

// Package pr-reviewer reviews pull requests against the repository's pull
// request template and issue templates, posting review comments in the style
// of PR Agent.
//
// Review reads a pull request's diff, the repository's PULL_REQUEST_TEMPLATE.md
// and issue templates, checks the PR body against the template checklist,
// flags common issues (hardcoded secrets, large files, missing tests, ...),
// and posts a review comment on the pull request plus per-file comments.
//
// It labels the pull request with its area (from the files it touches) and
// its type (from its title or branch name, when they follow Conventional
// Commits).
package prreviewer

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/blanketops/environments-bots/internal/github"
)

// ReviewAPI is the part of the GitHub client review needs.
type ReviewAPI interface {
	GetPull(ctx context.Context, number int) (github.PullDetail, error)
	GetPullFiles(ctx context.Context, number int) ([]github.PullFile, error)
	GetContents(ctx context.Context, path string) (string, error)
	CreateReview(ctx context.Context, number int, body, event string, comments []github.ReviewComment) (github.Review, error)
	EnsureLabel(ctx context.Context, name, color, description string) error
	AddLabels(ctx context.Context, number int, labels []string) error
	Comment(ctx context.Context, number int, body string) error
}

// ReviewOptions configures Review.
type ReviewOptions struct {
	// Repo is "owner/name", used to read the repo's templates.
	Repo string
	// PR is the pull request number to review.
	PR int
	// DryRun only reads: nothing is posted or labelled.
	DryRun bool
}

// ReviewResult is the outcome of a Review.
type ReviewResult struct {
	// PR is the pull request that was reviewed.
	PR int
	// Title is the pull request title.
	Title string
	// Base and Head are the pull request's base and head branches.
	Base, Head string
	// Files are the files changed in the pull request.
	Files []FileReview
	// Summary is a one-paragraph assessment of the pull request.
	Summary string
	// Checklist is the template checklist with each item marked done or not.
	Checklist []ChecklistItem
	// Issues are problems found in the diff (severity, message).
	Issues []Issue
	// Labels are the labels applied to the pull request.
	Labels []string
	// Comment is the review comment posted on the pull request.
	Comment string
}

// FileReview is the review of one changed file.
type FileReview struct {
	Path      string
	Status    string // added, modified, removed, renamed
	Additions int
	Deletions int
	Comments  []FileComment
}

// FileComment is a comment on a specific line or hunk of a file.
type FileComment struct {
	Line int // line in the new file (0 = file-level comment)
	Body string
}

// ChecklistItem is one checklist item from the PR template.
type ChecklistItem struct {
	Text string
	Done bool
}

// Issue is a problem found in the diff.
type Issue struct {
	Severity string // error, warning, info
	Message  string
	File     string // file the issue is in, or "" for pull-request-level issues
}

// Review reviews pull request pr. It fetches the pull request, its changed
// files, and the repository's pull request and issue templates, then posts
// a review comment and labels. A dry run makes no changes.
func Review(ctx context.Context, api ReviewAPI, opts ReviewOptions) (*ReviewResult, error) {
	res := &ReviewResult{PR: opts.PR}

	pull, err := api.GetPull(ctx, opts.PR)
	if err != nil {
		return nil, fmt.Errorf("get pull request #%d: %w", opts.PR, err)
	}
	res.Title = pull.Title
	res.Base = pull.Base
	res.Head = pull.Head

	files, err := api.GetPullFiles(ctx, opts.PR)
	if err != nil {
		return nil, fmt.Errorf("list pull request #%d files: %w", opts.PR, err)
	}

	// Read the pull request template and issue templates.
	prTemplate, err := api.GetContents(ctx, ".github/PULL_REQUEST_TEMPLATE.md")
	if err != nil {
		prTemplate = ""
	}
	issueTemplates := readIssueTemplates(ctx, api)

	// Parse the PR template checklist.
	res.Checklist = parseChecklist(prTemplate)

	// Review each changed file.
	for _, f := range files {
		fr := FileReview{
			Path:      f.Filename,
			Status:    f.Status,
			Additions: f.Additions,
			Deletions: f.Deletions,
		}
		fr.Comments = reviewFile(f, prTemplate, issueTemplates, files)
		res.Files = append(res.Files, fr)
	}

	// Checklist: mark items done if they appear in the PR body (where the
	// author ticks them), not just the title.
	res.Checklist = markChecklistDone(res.Checklist, pull.Body)

	// Find issues in the diff.
	res.Issues = findIssues(files)

	// Labels: area from files, type from title/branch.
	res.Labels = labelsFor(files, pull.Title, pull.Head)

	// Build the review comment.
	res.Comment = buildComment(res)
	res.Summary = summarize(res)

	if opts.DryRun {
		return res, nil
	}

	// Apply labels.
	for _, l := range res.Labels {
		if err := api.EnsureLabel(ctx, l, labelColor(l), labelDesc(l)); err != nil {
			return res, fmt.Errorf("ensure label %q: %w", l, err)
		}
	}
	if len(res.Labels) > 0 {
		if err := api.AddLabels(ctx, opts.PR, res.Labels); err != nil {
			return res, fmt.Errorf("label pull request #%d: %w", opts.PR, err)
		}
	}

	// Post the review: the summary as the body, line-specific findings as
	// inline comments on the diff.
	if _, err := api.CreateReview(ctx, opts.PR, res.Comment, "COMMENT", inlineComments(res.Files)); err != nil {
		return res, fmt.Errorf("post review on pull request #%d: %w", opts.PR, err)
	}

	return res, nil
}

// ReviewSummary is the markdown summary of a Review.
func ReviewSummary(res *ReviewResult, dryRun bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### PR #%d review\n\n%s\n\n", res.PR, res.Summary)

	if len(res.Checklist) > 0 {
		b.WriteString("Template checklist:\n\n")
		for _, item := range res.Checklist {
			mark := "- [ ]"
			if item.Done {
				mark = "- [x]"
			}
			fmt.Fprintf(&b, "%s %s\n", mark, item.Text)
		}
		b.WriteString("\n")
	}

	if len(res.Issues) > 0 {
		b.WriteString("Issues:\n\n")
		for _, iss := range res.Issues {
			fmt.Fprintf(&b, "- [%s] %s", strings.ToUpper(iss.Severity), iss.Message)
			if iss.File != "" {
				fmt.Fprintf(&b, " in %s", code(iss.File))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if len(res.Labels) > 0 {
		fmt.Fprintf(&b, "Labels: %s\n\n", labelList(res.Labels))
	}

	if dryRun {
		b.WriteString("Dry run: nothing was posted or labelled.\n")
	}
	return b.String()
}

// readIssueTemplates reads issue templates from .github/ISSUE_TEMPLATE/ or
// .github/ISSUE_TEMPLATE.md.
func readIssueTemplates(ctx context.Context, api ReviewAPI) []string {
	var out []string
	contents, err := api.GetContents(ctx, ".github/ISSUE_TEMPLATE.md")
	if err == nil && contents != "" {
		out = append(out, contents)
	}
	// Also try the ISSUE_TEMPLATE directory by listing common names.
	for _, name := range []string{"bug_report.md", "feature_request.md", "config.yml", "template.md"} {
		contents, err := api.GetContents(ctx, ".github/ISSUE_TEMPLATE/"+name)
		if err == nil && contents != "" {
			out = append(out, contents)
		}
	}
	return out
}

// parseChecklist parses "- [ ]" and "- [x]" items from markdown text.
func parseChecklist(text string) []ChecklistItem {
	var items []ChecklistItem
	re := regexp.MustCompile(`(?m)^\s*-\s*\[(x|)\]\s*(.*)$`)
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		items = append(items, ChecklistItem{
			Text: strings.TrimSpace(m[2]),
			Done: m[1] == "x",
		})
	}
	return items
}

// markChecklistDone marks checklist items as done when they appear in text.
func markChecklistDone(items []ChecklistItem, texts ...string) []ChecklistItem {
	joined := strings.Join(texts, " ")
	for i := range items {
		if strings.Contains(strings.ToLower(joined), strings.ToLower(items[i].Text)) {
			items[i].Done = true
		}
	}
	return items
}

// reviewFile reviews one changed file and returns file-level comments.
func reviewFile(f github.PullFile, prTemplate string, issueTemplates []string, allFiles []github.PullFile) []FileComment {
	var comments []FileComment

	// Large file check.
	if f.Additions+f.Deletions > 500 {
		comments = append(comments, FileComment{
			Line: 0,
			Body: fmt.Sprintf("Large change: %d additions, %d deletions. Consider splitting into smaller pull requests.",
				f.Additions, f.Deletions),
		})
	}

	// Line-specific scans on the patch's added lines: hardcoded secrets and
	// untrusted input interpolated straight into a shell or query.
	if f.Patch != "" {
		if line := matchAdded(f.Patch, secretRes); line > 0 {
			comments = append(comments, FileComment{
				Line: line,
				Body: "Possible hardcoded secret on this line. Never commit credentials; use a secret store or an Actions secret.",
			})
		}
		if line := matchAdded(f.Patch, untrustedRes); line > 0 {
			comments = append(comments, FileComment{
				Line: line,
				Body: "Untrusted input is interpolated directly here. Pass it through an env var (quoted), never inline into a shell command or query.",
			})
		}
	}

	// Test coverage check: if source code changed, check for a matching test file
	// elsewhere in the pull request.
	if isSourceFile(f.Filename) && f.Status != "removed" {
		testFile := testFileFor(f.Filename)
		testFound := false
		for _, other := range allFiles {
			if other.Filename == testFile {
				testFound = true
				break
			}
		}
		if !testFound {
			comments = append(comments, FileComment{
				Line: 0,
				Body: fmt.Sprintf("No test file found for %s. Consider adding tests (%s).",
					f.Filename, code(testFile)),
			})
		}
	}

	return comments
}

// findIssues scans all changed files for pull-request-level issues.
func findIssues(files []github.PullFile) []Issue {
	var issues []Issue
	for _, f := range files {
		if f.Additions+f.Deletions > 1000 {
			issues = append(issues, Issue{
				Severity: "warning",
				Message:  fmt.Sprintf("Very large change (%d lines). Consider splitting into smaller pull requests.", f.Additions+f.Deletions),
				File:     f.Filename,
			})
		}
		if f.Patch != "" {
			for _, pattern := range secretPatterns {
				if re := regexp.MustCompile(pattern); re.FindStringIndex(f.Patch) != nil {
					issues = append(issues, Issue{
						Severity: "error",
						Message:  fmt.Sprintf("Possible hardcoded secret detected (%s). Do not commit credentials; use a secret store instead.", pattern),
						File:     f.Filename,
					})
					break
				}
			}
		}
	}
	return issues
}

// isSourceFile reports whether path looks like source code (not config, docs, tests).
func isSourceFile(path string) bool {
	if strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, ".md") ||
		strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml") ||
		strings.HasSuffix(path, ".json") || strings.HasSuffix(path, ".toml") ||
		strings.HasSuffix(path, ".lock") || strings.HasPrefix(path, ".github/") ||
		strings.Contains(path, "/fixtures/") || strings.Contains(path, "/testdata/") {
		return false
	}
	return strings.HasSuffix(path, ".go") || strings.HasSuffix(path, ".ts") ||
		strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".py") ||
		strings.HasSuffix(path, ".rs") || strings.HasSuffix(path, ".java")
}

// testFileFor returns the likely test file path for a source file.
func testFileFor(path string) string {
	if strings.HasSuffix(path, ".go") {
		return strings.TrimSuffix(path, ".go") + "_test.go"
	}
	return ""
}

// labelsFor computes area and type labels for a pull request.
func labelsFor(files []github.PullFile, title, head string) []string {
	var labels []string
	area := areaFor(files)
	if area != "" {
		labels = append(labels, area)
	}
	t := typeFor(title, head)
	if t != "" {
		labels = append(labels, t)
	}
	return labels
}

// areaFor infers the area label from the files changed.
func areaFor(files []github.PullFile) string {
	areaCounts := map[string]int{}
	for _, f := range files {
		p := f.Filename
		switch {
		case strings.HasPrefix(p, "resolution/") || strings.HasPrefix(p, "core/") ||
			strings.HasPrefix(p, "pkg/apis/") || strings.HasPrefix(p, "pkg/intent/"):
			areaCounts["domain"]++
		case strings.HasPrefix(p, "pkg/secrets/") || strings.HasPrefix(p, "pkg/providerconfig/"):
			areaCounts["secrets"]++
		case strings.HasPrefix(p, "pkg/cache/") || strings.HasPrefix(p, "core/cache/"):
			areaCounts["cache"]++
		case strings.HasPrefix(p, "cmd/") || strings.HasPrefix(p, "hack/"):
			areaCounts["cli"]++
		case strings.HasPrefix(p, ".github/"):
			areaCounts["ci"]++
		case strings.HasSuffix(pathExt(p), ".md"):
			areaCounts["docs"]++
		}
	}
	best := ""
	bestN := 0
	for area, n := range areaCounts {
		if n > bestN {
			best, bestN = area, n
		}
	}
	return best
}

// pathExt returns the file extension including the dot.
func pathExt(path string) string {
	dot := strings.LastIndex(path, ".")
	if dot < 0 {
		return ""
	}
	return path[dot:]
}

// typeFor infers the type label from the pull request title or branch name.
func typeFor(title, head string) string {
	text := title + " " + head
	lower := strings.ToLower(text)
	for _, t := range []string{"feat", "fix", "docs", "chore", "ci", "refactor", "test", "perf", "style", "revert", "build"} {
		if strings.HasPrefix(lower, t+":") || strings.HasPrefix(lower, t+"(") ||
			strings.Contains(lower, "/"+t+"/") || strings.Contains(lower, "/"+t+"-") {
			return t
		}
	}
	return ""
}

func labelColor(name string) string {
	switch name {
	case "domain":
		return "1d76db"
	case "secrets":
		return "b60205"
	case "cache":
		return "0e8a16"
	case "cli":
		return "fbca04"
	case "ci":
		return "0052cc"
	case "docs":
		return "0075ca"
	case "feat":
		return "0e8a16"
	case "fix":
		return "d73a4a"
	case "chore":
		return "fbca04"
	case "refactor":
		return "a2eeef"
	case "test":
		return "1d76db"
	case "perf":
		return "ffdf00"
	case "style":
		return "a2eeef"
	case "revert":
		return "fbca04"
	case "build":
		return "0366d6"
	}
	return "ced8d6"
}

func labelDesc(name string) string {
	switch name {
	case "domain":
		return "Domain logic change"
	case "secrets":
		return "Secrets or credential handling"
	case "cache":
		return "Caching layer change"
	case "cli":
		return "CLI change"
	case "ci":
		return "CI or workflow change"
	case "docs":
		return "Documentation change"
	case "feat":
		return "New feature"
	case "fix":
		return "Bug fix"
	case "chore":
		return "Chore or maintenance"
	case "refactor":
		return "Code refactor"
	case "test":
		return "Test change"
	case "perf":
		return "Performance improvement"
	case "style":
		return "Code style change"
	case "revert":
		return "Revert"
	case "build":
		return "Build system change"
	}
	return ""
}

func labelList(labels []string) string {
	out := make([]string, len(labels))
	for i, l := range labels {
		out[i] = code(l)
	}
	return strings.Join(out, ", ")
}

var secretPatterns = []string{
	`(?i)(api[_-]?key|apikey)\s*[:=]\s*["']?[A-Za-z0-9_-]{16,}["']?`,
	`(?i)(secret|password|passwd|token)\s*[:=]\s*["']?[A-Za-z0-9_-]{16,}["']?`,
	`(?i)(private[_-]?key|privatekey)\s*[:=]\s*["']?[A-Za-z0-9+/=]{32,}["']?`,
	`(?i)sk-[A-Za-z0-9]{20,}`,  // OpenAI-style keys
	`(?i)ghp_[A-Za-z0-9]{36,}`, // GitHub personal access tokens
	`(?i)github[_-]?token\s*[:=]\s*["']?[A-Za-z0-9_-]{20,}["']?`,
}

// secretRes is the precompiled secret scanners.
var secretRes = compileAll(secretPatterns)

// untrustedPatterns match GitHub Actions script-injection vectors: a
// user-controlled field interpolated where a workflow can execute it.
var untrustedPatterns = []string{
	`\$\{\{\s*github\.event\.[A-Za-z0-9_.]*\.(title|body|ref|name|email|login)\s*\}\}`,
	`\$\{\{\s*github\.(head_ref|event\.comment\.body|event\.issue\.title|event\.issue\.body|event\.pull_request\.title|event\.pull_request\.body)\s*\}\}`,
}

// untrustedRes is the precompiled untrusted-input scanners.
var untrustedRes = compileAll(untrustedPatterns)

func compileAll(pats []string) []*regexp.Regexp {
	res := make([]*regexp.Regexp, len(pats))
	for i, p := range pats {
		res[i] = regexp.MustCompile(p)
	}
	return res
}

var hunkRe = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)`)

// matchAdded returns the new-file line number of the first added ("+") line in
// the unified-diff patch that matches any of res, or 0 when none does. It
// walks hunk headers to keep the new-file line count accurate.
func matchAdded(patch string, res []*regexp.Regexp) int {
	newLine := 0
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "@@"):
			if m := hunkRe.FindStringSubmatch(line); m != nil {
				n, _ := strconv.Atoi(m[1])
				newLine = n - 1
			}
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			newLine++
			for _, re := range res {
				if re.MatchString(line[1:]) {
					return newLine
				}
			}
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			// a deletion does not advance the new-file line
		default:
			newLine++
		}
	}
	return 0
}

// inlineComments collects the line-specific file comments as review comments.
func inlineComments(files []FileReview) []github.ReviewComment {
	var out []github.ReviewComment
	for _, f := range files {
		for _, c := range f.Comments {
			if c.Line > 0 {
				out = append(out, github.ReviewComment{Path: f.Path, Line: c.Line, Body: c.Body})
			}
		}
	}
	return out
}

// buildComment builds the review comment body.
func buildComment(res *ReviewResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### PR Review for #%d\n\n", res.PR)
	fmt.Fprintf(&b, "%s\n\n", res.Summary)

	if len(res.Checklist) > 0 {
		b.WriteString("### Pull Request Template Checklist\n\n")
		for _, item := range res.Checklist {
			mark := "- [ ]"
			if item.Done {
				mark = "- [x]"
			}
			fmt.Fprintf(&b, "%s %s\n", mark, item.Text)
		}
		b.WriteString("\n")
	}

	if len(res.Issues) > 0 {
		b.WriteString("### Issues Found\n\n")
		for _, iss := range res.Issues {
			fmt.Fprintf(&b, "- [%s] %s", strings.ToUpper(iss.Severity), iss.Message)
			if iss.File != "" {
				fmt.Fprintf(&b, " in %s", code(iss.File))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if len(res.Files) > 0 {
		b.WriteString("### Files Changed\n\n")
		for _, f := range res.Files {
			fmt.Fprintf(&b, "- %s (%s, %d additions, %d deletions)",
				code(f.Path), f.Status, f.Additions, f.Deletions)
			if len(f.Comments) > 0 {
				b.WriteString(":\n")
				for _, c := range f.Comments {
					fmt.Fprintf(&b, "  - %s\n", c.Body)
				}
			} else {
				b.WriteString("\n")
			}
		}
		b.WriteString("\n")
	}

	if len(res.Labels) > 0 {
		fmt.Fprintf(&b, "Labels: %s\n", labelList(res.Labels))
	}

	b.WriteString("\n---\n*Reviewed by environments-pr-reviewer*")
	return b.String()
}

// summarize writes a one-paragraph assessment of the pull request.
func summarize(res *ReviewResult) string {
	totalAdds, totalDels := 0, 0
	for _, f := range res.Files {
		totalAdds += f.Additions
		totalDels += f.Deletions
	}
	var parts []string
	parts = append(parts, fmt.Sprintf("This pull request changes %d file(s) with %d additions and %d deletions.",
		len(res.Files), totalAdds, totalDels))

	doneCount := 0
	for _, item := range res.Checklist {
		if item.Done {
			doneCount++
		}
	}
	if len(res.Checklist) > 0 {
		parts = append(parts, fmt.Sprintf("It addresses %d of %d items in the pull request template checklist.",
			doneCount, len(res.Checklist)))
	}

	errorCount := 0
	warningCount := 0
	for _, iss := range res.Issues {
		if iss.Severity == "error" {
			errorCount++
		} else if iss.Severity == "warning" {
			warningCount++
		}
	}
	if errorCount > 0 {
		parts = append(parts, fmt.Sprintf("⚠️ %d error(s) need attention before this can be merged.", errorCount))
	}
	if warningCount > 0 {
		parts = append(parts, fmt.Sprintf("⚠️ %d warning(s) to consider.", warningCount))
	}

	return strings.Join(parts, " ")
}

// code renders s as an inline code span safe in markdown tables and lists.
func code(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, "|", "\\|")
	if s == "" {
		return "` `"
	}
	ticks := strings.Repeat("`", longestRun(s, '`')+1)
	pad := ""
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		pad = " "
	}
	return ticks + pad + s + pad + ticks
}

func longestRun(s string, c byte) int {
	longest, cur := 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			cur++
			if cur > longest {
				longest = cur
			}
		} else {
			cur = 0
		}
	}
	return longest
}
