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

// Package develop keeps a repository's develop branch in step with main.
//
// Sync opens one pull request that merges main into develop whenever main
// is ahead: it branches off develop, merges main in with a server-side
// (GitHub-signed) merge commit so the signature ruleset needs no bypass,
// and opens the pull request into develop for the user to merge. It is
// idempotent — an open sync pull request, or a develop already level with
// main, makes it do nothing. A merge conflict is reported, not forced, as
// resolving it needs a human.
package develop

import (
	"context"
	"fmt"
	"strings"

	"github.com/blanketops/environments-bots/internal/github"
)

// Default branches Sync keeps in step.
const (
	MainBranch    = "main"
	DevelopBranch = "develop"
)

// syncTitle is the title of the sync pull request, and branchPrefix the
// prefix of the branch it is opened from; Sync treats any open pull request
// into the base whose head carries this prefix as an existing sync.
const (
	syncTitle    = "chore: sync develop with main"
	branchPrefix = "sync/main-into-develop-"
)

// API is the part of the GitHub client develop uses.
type API interface {
	BranchSHA(ctx context.Context, branch string) (string, error)
	Compare(ctx context.Context, base, head string) ([]github.Commit, error)
	ListPulls(ctx context.Context, state, base, head string) ([]github.Pull, error)
	CreateBranch(ctx context.Context, branch, sha string) error
	Merge(ctx context.Context, base, head, message string) (string, error)
	DeleteBranch(ctx context.Context, branch string) error
	CreatePull(ctx context.Context, title, head, base, body string) (github.Pull, error)
}

// SyncOptions configures Sync.
type SyncOptions struct {
	// Base is the branch kept up to date (default develop), From the branch
	// merged into it (default main).
	Base, From string
	// DryRun only reports what it would do; it makes no branch, merge or
	// pull request.
	DryRun bool
}

func (o SyncOptions) base() string {
	if o.Base != "" {
		return o.Base
	}
	return DevelopBranch
}

func (o SyncOptions) from() string {
	if o.From != "" {
		return o.From
	}
	return MainBranch
}

// SyncResult is the outcome of a Sync.
type SyncResult struct {
	Base, From string
	// Behind are the commits From has that Base lacks, oldest first.
	Behind []github.Commit
	// UpToDate is set when Base already contains From (nothing to do).
	UpToDate bool
	// Existing is an open sync pull request that was already there.
	Existing *github.Pull
	// Conflict is set when From cannot be merged into Base without
	// resolving a conflict; no pull request is opened.
	Conflict bool
	// Branch is the branch the sync pull request was opened from.
	Branch string
	// Opened is the pull request Sync opened.
	Opened *github.Pull
}

// Sync opens a pull request merging From into Base when From is ahead. See
// the package comment for the guarantees.
func Sync(ctx context.Context, api API, opts SyncOptions) (SyncResult, error) {
	base, from := opts.base(), opts.from()
	res := SyncResult{Base: base, From: from}

	fromSHA, err := api.BranchSHA(ctx, from)
	if err != nil {
		return res, fmt.Errorf("read %s: %w", from, err)
	}
	baseSHA, err := api.BranchSHA(ctx, base)
	if err != nil {
		return res, fmt.Errorf("read %s: %w", base, err)
	}

	// Commits From has that Base does not. Empty means Base is level with
	// (or ahead of) From and there is nothing to merge.
	behind, err := api.Compare(ctx, base, from)
	if err != nil {
		return res, fmt.Errorf("compare %s...%s: %w", base, from, err)
	}
	res.Behind = behind
	if len(behind) == 0 {
		res.UpToDate = true
		return res, nil
	}

	// An open sync pull request into Base already covers this.
	open, err := api.ListPulls(ctx, "open", base, "")
	if err != nil {
		return res, fmt.Errorf("list pull requests into %s: %w", base, err)
	}
	for i := range open {
		if strings.HasPrefix(open[i].Title, syncTitle) {
			res.Existing = &open[i]
			return res, nil
		}
	}

	if opts.DryRun {
		return res, nil
	}

	branch := branchPrefix + short(fromSHA)
	res.Branch = branch
	// Branch off Base; reuse it if a previous run left it behind.
	if _, err := api.BranchSHA(ctx, branch); err != nil {
		if !github.IsNotFound(err) {
			return res, fmt.Errorf("read %s: %w", branch, err)
		}
		if err := api.CreateBranch(ctx, branch, baseSHA); err != nil {
			return res, fmt.Errorf("create %s: %w", branch, err)
		}
	}

	// Merge From into the branch server-side so GitHub signs the commit.
	if _, err := api.Merge(ctx, branch, from, syncTitle); err != nil {
		if github.IsConflict(err) {
			// A bot cannot resolve a conflict: drop the branch and report.
			_ = api.DeleteBranch(ctx, branch)
			res.Conflict = true
			res.Branch = ""
			return res, nil
		}
		return res, fmt.Errorf("merge %s into %s: %w", from, branch, err)
	}

	pr, err := api.CreatePull(ctx, syncTitle, branch, base, syncBody(from, base, behind))
	if err != nil {
		return res, fmt.Errorf("open pull request: %w", err)
	}
	res.Opened = &pr
	return res, nil
}

// short is the first 7 characters of a commit SHA, or all of a shorter one.
func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// syncBody is the body of the sync pull request. Commit subjects are
// untrusted text, so each goes through a code span.
func syncBody(from, base string, behind []github.Commit) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Merges %s into %s.\n\n", code(from), code(base))
	fmt.Fprintf(&b, "%s is %d commit(s) behind %s:\n\n", code(base), len(behind), code(from))
	for _, c := range behind {
		fmt.Fprintf(&b, "- %s %s\n", code(short(c.SHA)), code(subject(c.Message)))
	}
	return b.String()
}

// SyncSummary is the markdown summary of a Sync.
func SyncSummary(res SyncResult, dryRun bool) string {
	switch {
	case res.UpToDate:
		return fmt.Sprintf("### Sync %s ← %s\n\n%s is up to date; nothing to merge.\n", res.Base, res.From, code(res.Base))
	case res.Existing != nil:
		return fmt.Sprintf("### Sync %s ← %s\n\nAn open sync pull request already covers this: [#%d](%s).\n",
			res.Base, res.From, res.Existing.Number, res.Existing.HTMLURL)
	case res.Conflict:
		return fmt.Sprintf("### Sync %s ← %s\n\n%s cannot be merged into %s without resolving a conflict. Sync it by hand.\n",
			res.Base, res.From, code(res.From), code(res.Base))
	case dryRun:
		return fmt.Sprintf("### Sync %s ← %s (dry run)\n\nWould open a pull request merging %d commit(s) from %s into %s.\n",
			res.Base, res.From, len(res.Behind), code(res.From), code(res.Base))
	case res.Opened != nil:
		return fmt.Sprintf("### Sync %s ← %s\n\nOpened [#%d](%s) merging %d commit(s) from %s.\n",
			res.Base, res.From, res.Opened.Number, res.Opened.HTMLURL, len(res.Behind), code(res.From))
	default:
		return fmt.Sprintf("### Sync %s ← %s\n\nNothing to do.\n", res.Base, res.From)
	}
}

// subject is the first line of a commit message.
func subject(message string) string {
	if i := strings.IndexByte(message, '\n'); i >= 0 {
		return message[:i]
	}
	return message
}

// code renders s as an inline code span safe in a table or list: one line,
// pipes escaped, fenced with more backticks than s contains, so untrusted
// text can neither mention anyone nor break the surrounding markdown.
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
