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
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/blanketops/environments-bots/internal/github"
)

// TidyMessage is the commit the developer bot makes for formatting fixes.
const TidyMessage = "chore: gofmt and go mod tidy"

// TidyAPI is the part of the GitHub client Tidy uses.
type TidyAPI interface {
	BranchSHA(ctx context.Context, branch string) (string, error)
	CreateCommitOnBranch(ctx context.Context, branch, headline, body, expectedHead string, additions []github.FileAddition, deletions []string) (string, error)
	ListPulls(ctx context.Context, state, base, head string) ([]github.Pull, error)
	Comment(ctx context.Context, number int, body string) error
}

// TidyChange is one file the tidy tools changed: its content to write, or a
// deletion.
type TidyChange struct {
	Path    string
	Content []byte
	Deleted bool
}

// TidyOptions configures Tidy.
type TidyOptions struct {
	// Branch is the pull request's head branch to commit the fixes on; Base is
	// the branch its pull request targets (default develop), used to find the
	// pull request to comment on.
	Branch, Base string
	// Changes are the files gofmt and go mod tidy changed, computed by the
	// caller.
	Changes []TidyChange
	// DryRun only reports what it would commit.
	DryRun bool
}

func (o TidyOptions) base() string {
	if o.Base != "" {
		return o.Base
	}
	return DevelopBranch
}

// TidyResult is the outcome of a Tidy.
type TidyResult struct {
	Branch string
	// Clean is set when nothing needed fixing.
	Clean bool
	// Added and Deleted are the paths committed.
	Added   []string
	Deleted []string
	// CommitOID is the signed commit Tidy made.
	CommitOID string
	// Pull is the pull request Tidy commented on, if one was open.
	Pull *github.Pull
}

// Tidy commits the formatting fixes in opts.Changes to the branch as one
// GitHub-signed commit (so a branch requiring verified signatures accepts it)
// and comments the fixes on the branch's open pull request. With no changes it
// does nothing. The caller runs gofmt and go mod tidy and passes the resulting
// changes in.
func Tidy(ctx context.Context, api TidyAPI, opts TidyOptions) (TidyResult, error) {
	res := TidyResult{Branch: opts.Branch}
	if opts.Branch == "" {
		return res, fmt.Errorf("no branch given")
	}
	if len(opts.Changes) == 0 {
		res.Clean = true
		return res, nil
	}

	var additions []github.FileAddition
	var deletions []string
	for _, ch := range opts.Changes {
		if ch.Deleted {
			deletions = append(deletions, ch.Path)
			res.Deleted = append(res.Deleted, ch.Path)
			continue
		}
		additions = append(additions, github.FileAddition{
			Path:     ch.Path,
			Contents: base64.StdEncoding.EncodeToString(ch.Content),
		})
		res.Added = append(res.Added, ch.Path)
	}

	if opts.DryRun {
		return res, nil
	}

	head, err := api.BranchSHA(ctx, opts.Branch)
	if err != nil {
		return res, fmt.Errorf("read %s: %w", opts.Branch, err)
	}
	oid, err := api.CreateCommitOnBranch(ctx, opts.Branch, TidyMessage, "", head, additions, deletions)
	if err != nil {
		return res, fmt.Errorf("commit tidy fixes to %s: %w", opts.Branch, err)
	}
	res.CommitOID = oid

	// Comment the fixes on the branch's open pull request, if there is one.
	open, err := api.ListPulls(ctx, "open", opts.base(), opts.Branch)
	if err != nil {
		return res, fmt.Errorf("list pull requests from %s: %w", opts.Branch, err)
	}
	if len(open) > 0 {
		res.Pull = &open[0]
		if err := api.Comment(ctx, open[0].Number, tidyComment(res)); err != nil {
			return res, fmt.Errorf("comment on pull request #%d: %w", open[0].Number, err)
		}
	}
	return res, nil
}

func tidyComment(res TidyResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Tidied %d file(s) with gofmt and go mod tidy.\n\n", len(res.Added)+len(res.Deleted))
	for _, p := range res.Added {
		fmt.Fprintf(&b, "- %s\n", code(p))
	}
	for _, p := range res.Deleted {
		fmt.Fprintf(&b, "- %s (removed)\n", code(p))
	}
	return b.String()
}

// TidySummary is the markdown summary of a Tidy.
func TidySummary(res TidyResult, dryRun bool) string {
	n := len(res.Added) + len(res.Deleted)
	switch {
	case res.Clean:
		return fmt.Sprintf("### Tidy %s\n\nAlready tidy; nothing to fix.\n", res.Branch)
	case dryRun:
		return fmt.Sprintf("### Tidy %s (dry run)\n\nWould commit %d gofmt / go mod tidy fix(es) to %s.\n", res.Branch, n, code(res.Branch))
	case res.Pull != nil:
		return fmt.Sprintf("### Tidy %s\n\nCommitted %d fix(es) (`%s`) to %s and commented on [#%d](%s).\n",
			res.Branch, n, short(res.CommitOID), code(res.Branch), res.Pull.Number, res.Pull.HTMLURL)
	default:
		return fmt.Sprintf("### Tidy %s\n\nCommitted %d fix(es) (`%s`) to %s.\n", res.Branch, n, short(res.CommitOID), code(res.Branch))
	}
}
