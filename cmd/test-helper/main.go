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

// Command test-helper keeps one GitHub issue per test file of a repository
// and posts each test run's results to it.
//
// This is the entry point only: it reports its version and usage. The
// issue and run-result commands arrive in the next changes.
package main

import (
	"fmt"
	"io"
	"os"
)

// version is set at release time with
// -ldflags "-X main.version=<tag>".
var version = "dev"

const usage = `test-helper keeps one GitHub issue per test file and posts each run's results to it.

Usage:
  test-helper <command>

Commands:
  version   Print the version
  help      Print this help
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes the command in args and returns the process exit code:
// 0 on success, 2 on a usage error.
func run(args []string, stdout, stderr io.Writer) int {
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
	default:
		_, _ = fmt.Fprintf(stderr, "test-helper: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}
