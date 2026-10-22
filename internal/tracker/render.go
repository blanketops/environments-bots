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

package tracker

import (
	"fmt"
	"strings"
	"time"

	"github.com/blanketops/environments-bots/internal/discover"
	"github.com/blanketops/environments-bots/internal/results"
)

// maxComment keeps a run comment under GitHub's 65536-character limit, with
// room for the closing note.
const maxComment = 60000

// code renders s as an inline code span that is safe in a table cell: one
// line, pipes escaped, fenced with more backticks than s contains. Text from
// test files and test output always goes through code or fence, so it can
// neither mention anyone nor break the markdown around it.
func code(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, "|", `\|`)
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

// fence renders s as a fenced code block whose fence is longer than any
// backtick run in s.
func fence(s string) string {
	n := longestRun(s, '`') + 1
	if n < 3 {
		n = 3
	}
	f := strings.Repeat("`", n)
	return f + "text\n" + strings.TrimRight(s, "\n") + "\n" + f + "\n"
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

func duration(d time.Duration) string {
	switch {
	case d <= 0:
		return "-"
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	default:
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
}

// IssueBody is the body of a new tracking issue.
func IssueBody(f discover.File, source string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Tracks %s. test-helper posts the results of every test run of this file here.\n\n", code(f.Path))
	b.WriteString("| | |\n| --- | --- |\n")
	fmt.Fprintf(&b, "| File | %s |\n", code(f.Path))
	if f.Layer != "" {
		fmt.Fprintf(&b, "| Layer | %s |\n", code(f.Layer))
	}
	if len(f.Tests) > 0 {
		fmt.Fprintf(&b, "| Go tests | %d |\n", len(f.Tests))
	}
	if len(f.Containers) > 0 {
		names := make([]string, len(f.Containers))
		for i, c := range f.Containers {
			names[i] = code(c)
		}
		fmt.Fprintf(&b, "| Ginkgo | %s |\n", strings.Join(names, ", "))
	}
	if source != "" {
		fmt.Fprintf(&b, "| Opened from | %s |\n", source)
	}
	return b.String()
}

// RunInfo describes the workflow run whose results are reported.
type RunInfo struct {
	// URL links to the run.
	URL string
	// Name labels the link, e.g. "Suite Tests #42".
	Name string
	// SHA is the tested commit.
	SHA string
	// Ref is the tested branch or pull request, e.g. "PR #53".
	Ref string
}

func (r RunInfo) link() string {
	name := r.Name
	if name == "" {
		name = "run"
	}
	label := strings.NewReplacer("[", "(", "]", ")").Replace(name)
	if r.URL == "" {
		return label
	}
	return "[" + label + "](" + r.URL + ")"
}

func verdict(passed, failed, skipped int) string {
	switch {
	case failed > 0:
		return "Failed"
	case passed > 0:
		return "Passed"
	case skipped > 0:
		return "Skipped"
	default:
		return "No results"
	}
}

// RunComment is the comment posted on a file's issue for one run.
func RunComment(fr results.FileResults, run RunInfo) string {
	passed, failed, skipped := fr.Counts()
	var total time.Duration
	for _, r := range fr.Results {
		total += r.Duration
	}

	var b strings.Builder
	fmt.Fprintf(&b, "**%s**: %d passed, %d failed, %d skipped (%s)\n\n", verdict(passed, failed, skipped), passed, failed, skipped, duration(total))
	b.WriteString(run.link())
	if run.SHA != "" {
		sha := run.SHA
		if len(sha) > 12 {
			sha = sha[:12]
		}
		fmt.Fprintf(&b, " on %s", code(sha))
	}
	if run.Ref != "" {
		fmt.Fprintf(&b, " (%s)", code(run.Ref))
	}
	b.WriteString("\n\n| Test | Result | Time |\n| --- | --- | --- |\n")
	for _, r := range fr.Results {
		fmt.Fprintf(&b, "| %s | %s | %s |\n", code(r.Name), r.Outcome, duration(r.Duration))
	}

	var details []results.Result
	for _, r := range fr.Results {
		if r.Output != "" {
			details = append(details, r)
		}
	}
	// Failures before skips, so the limit below cuts skip reasons first.
	ordered := make([]results.Result, 0, len(details))
	for _, want := range []results.Outcome{results.Failed, results.Skipped} {
		for _, r := range details {
			if r.Outcome == want {
				ordered = append(ordered, r)
			}
		}
	}
	for i, r := range ordered {
		section := fmt.Sprintf("\n<details><summary>%s %s</summary>\n\n%s\n</details>\n", code(r.Name), r.Outcome, fence(r.Output))
		if b.Len()+len(section) > maxComment {
			fmt.Fprintf(&b, "\nOutput of %d more tests is left out to fit GitHub's comment limit; see the run.\n", len(ordered)-i)
			break
		}
		b.WriteString(section)
	}
	return b.String()
}
