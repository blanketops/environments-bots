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

// Command releaser drives every release of a repository through its
// Release issue ("Release: vX.Y.Z", label release) on the project board.
//
//	releaser plan       on every push to develop: plan the next release
//	releaser cut        cut the release branch and its pull request
//	releaser prepare    the checks of cut, without cutting
//	releaser verify     before the release branch is finalized
//	releaser complete   after the GitHub release is published
//
// Each command comments its checks on the Release issue and exits 1 when
// one fails. GitHub is reached with the token in GH_TOKEN (or
// GITHUB_TOKEN), normally an installation token of the BlanketOps
// Environments Releaser App.
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
	"github.com/blanketops/environments-bots/internal/release"
)

// version is set at release time with
// -ldflags "-X main.version=<tag>".
var version = "dev"

const usage = `releaser drives every release through its Release issue on the project board.

Usage:
  releaser <command> [-release vX.Y.Z] [flags]

Commands:
  plan      When develop has changes no release carries yet, open the
            Release issue for the version they need (or rename the planned
            one when they need more), on the board as Release
  cut       Check the release like prepare, then create release/<version>
            from develop and open its pull request into main. -release is
            optional: the planned release, else the version the commits need
  prepare   The checks of cut without cutting: the version against the
            latest tag and the commits (semantic versioning), one open
            Release issue (opened if missing), the version's milestone
  verify    Before finalizing: nothing still open into the release branch
            or in the version's milestone
  complete  After the GitHub release is published: comment the tag and the
            release on the issue, close it, set it to Done
  version   Print the version
  help      Print this help

Each command comments its checks on the Release issue and exits 1 when one
fails. Run "releaser <command> -h" for its flags. GitHub is reached with the
token in GH_TOKEN (or GITHUB_TOKEN).
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}

type command func(context.Context, release.API, release.Options) (*release.Result, error)

// run executes the command in args and returns the process exit code:
// 0 when every check passed, 1 when one failed or the command could not
// run, 2 on a usage error.
func run(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
	var cmd command
	switch args[0] {
	case "version", "--version", "-v":
		_, _ = fmt.Fprintln(stdout, version)
		return 0
	case "help", "--help", "-h":
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	case "plan":
		cmd = release.Plan
	case "cut":
		cmd = release.Cut
	case "prepare":
		cmd = release.Prepare
	case "verify":
		cmd = release.Verify
	case "complete":
		cmd = release.Complete
	default:
		_, _ = fmt.Fprintf(stderr, "releaser: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	return runCommand(args[0], cmd, args[1:], stdout, stderr, getenv)
}

func runCommand(name string, cmd command, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var opts release.Options
	var repo, apiURL, summary, project string
	switch name {
	case "plan":
	case "cut":
		fs.StringVar(&opts.Version, "release", "", "the release, vX.Y.Z (default: the planned release, else the version the commits need)")
	default:
		fs.StringVar(&opts.Version, "release", "", "the release, vX.Y.Z (required)")
	}
	if name == "plan" || name == "cut" || name == "prepare" {
		fs.StringVar(&opts.Ref, "ref", "develop", "branch releases are cut from")
	}
	fs.StringVar(&project, "project", "", "keep the Release issue on this organization project, as org/number (e.g. blanketops/1)")
	fs.StringVar(&repo, "repo", getenv("GITHUB_REPOSITORY"), "repository as owner/name (default $GITHUB_REPOSITORY)")
	fs.StringVar(&apiURL, "api-url", getenv("GITHUB_API_URL"), "GitHub API URL (default $GITHUB_API_URL, else https://api.github.com)")
	fs.StringVar(&summary, "summary", getenv("GITHUB_STEP_SUMMARY"), "file to append the markdown summary to (default $GITHUB_STEP_SUMMARY)")
	fs.StringVar(&opts.RunURL, "run-url", runURL(getenv), "link to the workflow run (default from $GITHUB_SERVER_URL, $GITHUB_REPOSITORY, $GITHUB_RUN_ID)")
	fs.StringVar(&opts.Bot, "bot", release.DefaultBot, "the releaser's own login: only Release issues it opened are planned, renamed or flagged")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "read only: show the checks without commenting, opening or closing anything")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "releaser %s: unexpected argument %q\n", name, fs.Arg(0))
		return 2
	}
	if opts.Version == "" && name != "plan" && name != "cut" {
		_, _ = fmt.Fprintf(stderr, "releaser %s: -release is required\n", name)
		return 2
	}
	if project != "" {
		ref, err := parseProject(project)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "releaser %s: %v\n", name, err)
			return 2
		}
		opts.Project = &ref
	}

	token := getenv("GH_TOKEN")
	if token == "" {
		token = getenv("GITHUB_TOKEN")
	}
	if token == "" && !opts.DryRun {
		return fail(stderr, name, errors.New("no token: set GH_TOKEN (or GITHUB_TOKEN), or use -dry-run"))
	}
	api, err := github.New(apiURL, token, repo)
	if err != nil {
		return fail(stderr, name, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	res, err := cmd(ctx, api, opts)
	if err != nil {
		return fail(stderr, name, err)
	}
	if err := emit(stdout, summary, release.Summary(res)); err != nil {
		return fail(stderr, name, err)
	}
	for _, w := range res.Warnings {
		_, _ = fmt.Fprintf(stderr, "releaser %s: warning: %s\n", name, w)
	}
	if res.Failed() {
		for _, c := range res.Checks {
			if !c.OK {
				_, _ = fmt.Fprintf(stderr, "releaser %s: %s: %s\n", name, c.Name, c.Detail)
			}
		}
		return 1
	}
	return 0
}

func fail(stderr io.Writer, cmd string, err error) int {
	_, _ = fmt.Fprintf(stderr, "releaser %s: %v\n", cmd, err)
	return 1
}

// emit prints the summary and appends it to the summary file, if any.
func emit(stdout io.Writer, path, md string) error {
	_, _ = fmt.Fprint(stdout, md)
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, md); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func parseProject(s string) (release.ProjectRef, error) {
	org, num, ok := strings.Cut(s, "/")
	n, err := strconv.Atoi(num)
	if !ok || org == "" || err != nil || n <= 0 {
		return release.ProjectRef{}, fmt.Errorf("-project %q is not org/number", s)
	}
	return release.ProjectRef{Org: org, Number: n}, nil
}

func runURL(getenv func(string) string) string {
	server, repo, id := getenv("GITHUB_SERVER_URL"), getenv("GITHUB_REPOSITORY"), getenv("GITHUB_RUN_ID")
	if server == "" || repo == "" || id == "" {
		return ""
	}
	return server + "/" + repo + "/actions/runs/" + id
}
