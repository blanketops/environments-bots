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

package release

import (
	"fmt"
	"strings"

	"github.com/blanketops/environments-bots/internal/semver"
)

// maxCommits caps the commit list of a comment; GitHub limits a comment to
// 65536 characters.
const maxCommits = 200

// code renders s as an inline code span that is safe in a table cell or a
// list: one line, pipes escaped, fenced with more backticks than s
// contains. Commit subjects and issue titles always go through it, so they
// can neither mention anyone nor break the markdown around them.
func code(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, "|", `\|`)
	if s == "" {
		return "` `"
	}
	longest, cur := 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] == '`' {
			cur++
			longest = max(longest, cur)
		} else {
			cur = 0
		}
	}
	ticks := strings.Repeat("`", longest+1)
	pad := ""
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		pad = " "
	}
	return ticks + pad + s + pad + ticks
}

// IssueBody is the body of a new Release issue.
func IssueBody(v semver.Version, ref string) string {
	return fmt.Sprintf("Tracks release %s, cut from %s.\n\n"+
		"releaser comments here at each stage: prepare (before the release branch is cut), "+
		"verify (before it is finalized) and complete (after the GitHub release is published). "+
		"complete attaches the tag and the release to this issue, closes it and moves it to %s.\n",
		code(v.String()), code(ref), StatusDone)
}

func (r *Result) headline() string {
	if r.Failed() {
		return fmt.Sprintf("%s found problems; this issue stays open until they are fixed and %s runs again", r.Stage, r.Stage)
	}
	switch r.Stage {
	case StagePrepare:
		return "ready to cut " + code(Branch(r.Version))
	case StageVerify:
		return "ready to finalize"
	default:
		if r.Closed {
			return "released; this issue is closed"
		}
		return "released"
	}
}

// Comment is the comment a command posts on the Release issue.
func Comment(r *Result) string {
	var b strings.Builder
	state := "passed"
	if r.Failed() {
		state = "failed"
	}
	fmt.Fprintf(&b, "### %s %s: %s\n\n%s.\n\n", r.Version, r.Stage, state, upperFirst(r.headline()))
	if r.Stage == StageComplete && r.Release != nil {
		b.WriteString("| Release | Tag | Release pull request |\n| --- | --- | --- |\n")
		pr := "-"
		if r.ReleasePR != nil {
			pr = fmt.Sprintf("#%d", r.ReleasePR.Number)
		}
		fmt.Fprintf(&b, "| [%s](%s) | %s at %s | %s |\n\n", r.Version, r.Release.HTMLURL, code(r.Version.String()), code(short(r.TagSHA)), pr)
	}
	b.WriteString("| Check | Result | Detail |\n| --- | --- | --- |\n")
	for _, c := range r.Checks {
		res := "ok"
		if !c.OK {
			res = "failed"
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n", c.Name, res, c.Detail)
	}
	if len(r.Warnings) > 0 {
		b.WriteString("\nWarnings:\n\n")
		for _, w := range r.Warnings {
			fmt.Fprintf(&b, "- %s\n", w)
		}
	}
	if r.Stage == StagePrepare && len(r.Commits) > 0 {
		b.WriteString(commitList(r.Commits))
	}
	if r.RunURL != "" {
		fmt.Fprintf(&b, "\n[Workflow run](%s)\n", r.RunURL)
	}
	return b.String()
}

// commitList groups the release's commits the way the changelog does.
func commitList(commits []semver.Commit) string {
	groups := []struct {
		name string
		keep func(semver.Commit) bool
	}{
		{"Breaking changes", func(c semver.Commit) bool { return c.Breaking }},
		{"Features", func(c semver.Commit) bool { return !c.Breaking && c.Type == "feat" }},
		{"Fixes", func(c semver.Commit) bool { return !c.Breaking && c.Type == "fix" }},
		{"Other", func(c semver.Commit) bool { return !c.Breaking && c.Type != "feat" && c.Type != "fix" && c.Type != "" }},
		{"Not Conventional Commits", func(c semver.Commit) bool { return c.Type == "" }},
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nIn this release (%d commits):\n", len(commits))
	shown := 0
	for _, g := range groups {
		var lines []string
		for _, c := range commits {
			if g.keep(c) {
				lines = append(lines, fmt.Sprintf("- %s %s\n", code(short(c.SHA)), code(c.Subject)))
			}
		}
		if len(lines) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n%s:\n\n", g.name)
		for _, l := range lines {
			if shown == maxCommits {
				fmt.Fprintf(&b, "- and %d more\n", len(commits)-shown)
				return b.String()
			}
			b.WriteString(l)
			shown++
		}
	}
	return b.String()
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Summary is the step summary of a command.
func Summary(r *Result) string {
	var b strings.Builder
	b.WriteString(Comment(r))
	switch {
	case r.DryRun:
		b.WriteString("\nDry run: nothing was created, commented or closed.\n")
	case r.Issue.Number == 0:
		b.WriteString("\nNo Release issue was commented.\n")
	default:
		created := ""
		if r.IssueCreated {
			created = " (opened by this run)"
		}
		fmt.Fprintf(&b, "\nCommented on Release issue %s%s.\n", r.Issue.HTMLURL, created)
	}
	return b.String()
}
