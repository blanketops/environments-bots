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

// Command pr-reviewer reviews pull requests against the repository's pull
// request template and issue templates, in the style of PR Agent.
//
// \tpr-reviewer review   review a pull request and post review comments
//
// GitHub is reached with the token in GH_TOKEN (or GITHUB_TOKEN), normally
// an installation token of the BlanketOps Environments PR Reviewer App.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/blanketops/environments-bots/internal/github"
	"github.com/blanketops/environments-bots/internal/llm"
	prreviewer "github.com/blanketops/environments-bots/internal/pr-reviewer"
)

var version = "dev"

const usage = `pr-reviewer reviews pull requests against the repository's pull
request template and issue templates, posting review comments.

Usage:
  pr-reviewer <command> [flags]

Commands:
  review   Review a pull request and post review comments.
  version  Print the version.
  help     Print this help.

Run "pr-reviewer <command> -h" for a command's flags. GitHub is reached with
the token in GH_TOKEN (or GITHUB_TOKEN).
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}

func run(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version", "--version", "-v":
		_, _ = fmt.Fprintln(stdout, version)
		return 0
	case "help", "--help", "-h":
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	case "review":
		return runReview(args[1:], stdout, stderr, getenv)
	default:
		_, _ = fmt.Fprintf(stderr, "pr-reviewer: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

type common struct {
	repo, apiURL, summary string
	dryRun                bool
}

func (c *common) register(fs *flag.FlagSet, getenv func(string) string) {
	fs.StringVar(&c.repo, "repo", getenv("GITHUB_REPOSITORY"), "repository as owner/name (default $GITHUB_REPOSITORY)")
	fs.StringVar(&c.apiURL, "api-url", getenv("GITHUB_API_URL"), "GitHub API URL (default $GITHUB_API_URL, else https://api.github.com)")
	fs.StringVar(&c.summary, "summary", getenv("GITHUB_STEP_SUMMARY"), "file to append the markdown summary to (default $GITHUB_STEP_SUMMARY)")
	fs.BoolVar(&c.dryRun, "dry-run", false, "read only: show what would be reviewed")
}

func (c *common) client(getenv func(string) string) (*github.Client, error) {
	token := getenv("GH_TOKEN")
	if token == "" {
		token = getenv("GITHUB_TOKEN")
	}
	if token == "" && !c.dryRun {
		return nil, errors.New("no token: set GH_TOKEN (or GITHUB_TOKEN), or use -dry-run")
	}
	return github.New(c.apiURL, token, c.repo)
}

func (c *common) emit(stdout io.Writer, md string) error {
	_, _ = fmt.Fprint(stdout, md)
	if c.summary == "" {
		return nil
	}
	f, err := os.OpenFile(c.summary, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, md); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func parse(fs *flag.FlagSet, args []string, stderr io.Writer) (int, bool) {
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, false
		}
		return 2, false
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "pr-reviewer %s: unexpected argument %q\n", fs.Name(), fs.Arg(0))
		return 2, false
	}
	return 0, true
}

func fail(stderr io.Writer, cmd string, err error) int {
	_, _ = fmt.Fprintf(stderr, "pr-reviewer %s: %v\n", cmd, err)
	return 1
}

func commandContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Minute)
}

func runReview(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	var c common
	c.register(fs, getenv)
	pr := fs.Int("pr", prFromRef(getenv("GITHUB_REF")), "the pull request to review (default parsed from $GITHUB_REF)")
	if code, ok := parse(fs, args, stderr); !ok {
		return code
	}
	if *pr <= 0 {
		_, _ = fmt.Fprintln(stderr, "pr-reviewer review: no pull request: set -pr or run on a pull_request event")
		return 2
	}

	api, err := c.client(getenv)
	if err != nil {
		return fail(stderr, "review", err)
	}
	ctx, cancel := commandContext()
	defer cancel()

	res, err := prreviewer.Review(ctx, api, prreviewer.ReviewOptions{
		Repo:   c.repo,
		PR:     *pr,
		DryRun: c.dryRun,
		LLM:    llm.FromEnv(getenv),
	})
	if err != nil {
		return fail(stderr, "review", err)
	}
	if err := c.emit(stdout, prreviewer.ReviewSummary(res, c.dryRun)); err != nil {
		return fail(stderr, "review", err)
	}
	return 0
}

func prFromRef(ref string) int {
	rest, ok := strings.CutPrefix(ref, "refs/pull/")
	if !ok {
		return 0
	}
	num, _, _ := strings.Cut(rest, "/")
	n, _ := strconv.Atoi(num)
	return n
}
