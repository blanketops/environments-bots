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

// Command cleaner keeps a repository's Actions footprint small — the
// environments-cleaner bot. Its first task prunes old workflow runs, which
// otherwise fill the self-hosted runner node's disk.
//
//	cleaner runs    delete completed workflow runs beyond the newest N per
//	                workflow
//
// GitHub is reached with the token in GH_TOKEN (or GITHUB_TOKEN), normally an
// installation token of the BlanketOps Environments Cleaner App (Actions
// read and write).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/blanketops/environments-bots/internal/clean"
	"github.com/blanketops/environments-bots/internal/github"
)

// version is set at release time with -ldflags "-X main.version=<tag>".
var version = "dev"

const usage = `cleaner keeps a repository's Actions footprint small.

Usage:
  cleaner <command> [flags]

Commands:
  runs      Delete completed workflow runs beyond the newest N per workflow
  version   Print the version
  help      Print this help

Run "cleaner <command> -h" for a command's flags. GitHub is reached with the
token in GH_TOKEN (or GITHUB_TOKEN).
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}

// run executes the command in args and returns the process exit code:
// 0 on success, 1 when the command failed, 2 on a usage error.
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
	case "runs":
		return runRuns(args[1:], stdout, stderr, getenv)
	default:
		_, _ = fmt.Fprintf(stderr, "cleaner: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

// common are the flags every command shares.
type common struct {
	repo, apiURL, summary string
	dryRun                bool
}

func (c *common) register(fs *flag.FlagSet, getenv func(string) string) {
	fs.StringVar(&c.repo, "repo", getenv("GITHUB_REPOSITORY"), "repository as owner/name (default $GITHUB_REPOSITORY)")
	fs.StringVar(&c.apiURL, "api-url", getenv("GITHUB_API_URL"), "GitHub API URL (default $GITHUB_API_URL, else https://api.github.com)")
	fs.StringVar(&c.summary, "summary", getenv("GITHUB_STEP_SUMMARY"), "file to append the markdown summary to (default $GITHUB_STEP_SUMMARY)")
	fs.BoolVar(&c.dryRun, "dry-run", false, "read only: show what would be deleted")
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
		_, _ = fmt.Fprintf(stderr, "cleaner %s: unexpected argument %q\n", fs.Name(), fs.Arg(0))
		return 2, false
	}
	return 0, true
}

func fail(stderr io.Writer, cmd string, err error) int {
	_, _ = fmt.Fprintf(stderr, "cleaner %s: %v\n", cmd, err)
	return 1
}

func runRuns(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("runs", flag.ContinueOnError)
	var c common
	c.register(fs, getenv)
	keep := fs.Int("keep", 20, "how many of the newest runs to keep per workflow")
	if code, ok := parse(fs, args, stderr); !ok {
		return code
	}

	api, err := c.client(getenv)
	if err != nil {
		return fail(stderr, "runs", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	res, err := clean.PruneRuns(ctx, api, clean.PruneOptions{Keep: *keep, DryRun: c.dryRun})
	if err != nil {
		return fail(stderr, "runs", err)
	}
	if err := c.emit(stdout, clean.PruneSummary(res, *keep, c.dryRun)); err != nil {
		return fail(stderr, "runs", err)
	}
	return 0
}
