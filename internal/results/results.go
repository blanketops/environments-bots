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

// Package results reads test results and assigns them to test files.
//
// Two sources are read: the event stream of `go test -json`, and Ginkgo's
// JSON report (-ginkgo.json-report). A Go test belongs to the file that
// declares its function; a Ginkgo spec belongs to the file of its It. A
// package that failed to build gives every test file in it a failed
// "(build)" result.
package results

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/blanketops/environments-bots/internal/discover"
)

// Outcome is a test's result.
type Outcome string

// Outcomes.
const (
	Passed  Outcome = "passed"
	Failed  Outcome = "failed"
	Skipped Outcome = "skipped"
)

// maxOutput caps the output kept for one failed test, in bytes. The tail is
// kept: a failure is usually explained at the end.
const maxOutput = 4000

// Result is one test's result.
type Result struct {
	// Name is the Go test function, or a Ginkgo spec's container texts and
	// leaf text joined with " / ".
	Name     string
	Outcome  Outcome
	Duration time.Duration
	// Output is the test's output (Go) or failure or skip message (Ginkgo),
	// kept for failed and skipped tests only.
	Output string
}

// Run holds the results of one test run.
type Run struct {
	// tests: package import path -> top-level test name -> result.
	tests map[string]map[string]*Result
	// output: package -> test -> collected output lines.
	output map[string]map[string]*bytes.Buffer
	// buildFailed: package -> build output, for packages that did not build.
	buildFailed map[string]string
	// buildOutput: ImportPath of a build-output event -> collected output.
	buildOutput map[string]*bytes.Buffer
	// specs are the Ginkgo specs, with absolute or root-relative file names.
	specs []spec
}

type spec struct {
	file   string
	result Result
}

// NewRun returns an empty Run.
func NewRun() *Run {
	return &Run{
		tests:       map[string]map[string]*Result{},
		output:      map[string]map[string]*bytes.Buffer{},
		buildFailed: map[string]string{},
		buildOutput: map[string]*bytes.Buffer{},
	}
}

// testEvent is one line of `go test -json` (see `go doc test2json`).
type testEvent struct {
	Action      string
	Package     string
	Test        string
	Elapsed     float64
	Output      string
	ImportPath  string
	FailedBuild string
}

// AddGoTestJSON reads a `go test -json` stream. Lines that are not JSON
// (stderr mixed in by `2>&1`) are ignored.
func (r *Run) AddGoTestJSON(in io.Reader) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev testEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		r.addEvent(ev)
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read go test -json: %w", err)
	}
	return nil
}

func (r *Run) addEvent(ev testEvent) {
	switch {
	case ev.Action == "build-output":
		buf := r.buildOutput[ev.ImportPath]
		if buf == nil {
			buf = &bytes.Buffer{}
			r.buildOutput[ev.ImportPath] = buf
		}
		buf.WriteString(ev.Output)
		return
	case ev.Test == "":
		if ev.Action == "fail" && ev.FailedBuild != "" {
			out := ""
			if buf := r.buildOutput[ev.FailedBuild]; buf != nil {
				out = buf.String()
			}
			r.buildFailed[ev.Package] = tail(out)
		}
		return
	}
	top, _, _ := strings.Cut(ev.Test, "/")
	sub := top != ev.Test
	switch ev.Action {
	case "output":
		byTest := r.output[ev.Package]
		if byTest == nil {
			byTest = map[string]*bytes.Buffer{}
			r.output[ev.Package] = byTest
		}
		buf := byTest[top]
		if buf == nil {
			buf = &bytes.Buffer{}
			byTest[top] = buf
		}
		buf.WriteString(ev.Output)
	case "pass", "fail", "skip":
		if sub {
			return
		}
		byTest := r.tests[ev.Package]
		if byTest == nil {
			byTest = map[string]*Result{}
			r.tests[ev.Package] = byTest
		}
		res := &Result{
			Name:     top,
			Outcome:  map[string]Outcome{"pass": Passed, "fail": Failed, "skip": Skipped}[ev.Action],
			Duration: time.Duration(ev.Elapsed * float64(time.Second)),
		}
		if res.Outcome != Passed {
			if buf := r.output[ev.Package][top]; buf != nil {
				res.Output = tail(buf.String())
			}
		}
		byTest[top] = res
	}
}

// ginkgoReport is the part of Ginkgo's JSON report this package reads.
type ginkgoReport struct {
	SpecReports []struct {
		ContainerHierarchyTexts []string
		LeafNodeType            string
		LeafNodeText            string
		LeafNodeLocation        struct{ FileName string }
		State                   string
		RunTime                 int64
		Failure                 struct {
			Message  string
			Location struct {
				FileName   string
				LineNumber int
			}
		}
	}
}

// AddGinkgoJSON reads a Ginkgo JSON report. Only It specs are kept; suite
// setup nodes such as BeforeSuite fail their suite's Go test, which the
// `go test -json` stream already reports.
func (r *Run) AddGinkgoJSON(in io.Reader) error {
	var reports []ginkgoReport
	if err := json.NewDecoder(in).Decode(&reports); err != nil {
		return fmt.Errorf("read Ginkgo JSON report: %w", err)
	}
	for _, rep := range reports {
		for _, s := range rep.SpecReports {
			if s.LeafNodeType != "It" {
				continue
			}
			res := Result{
				Name:     strings.Join(append(append([]string{}, s.ContainerHierarchyTexts...), s.LeafNodeText), " / "),
				Outcome:  ginkgoOutcome(s.State),
				Duration: time.Duration(s.RunTime),
			}
			if res.Outcome != Passed && s.Failure.Message != "" {
				msg := s.Failure.Message
				if loc := s.Failure.Location; loc.FileName != "" {
					msg = fmt.Sprintf("%s:%d\n%s", path.Base(filepath.ToSlash(loc.FileName)), loc.LineNumber, msg)
				}
				res.Output = tail(msg)
			}
			r.specs = append(r.specs, spec{file: s.LeafNodeLocation.FileName, result: res})
		}
	}
	return nil
}

func ginkgoOutcome(state string) Outcome {
	switch state {
	case "passed":
		return Passed
	case "skipped", "pending":
		return Skipped
	default: // failed, panicked, interrupted, aborted, timedout
		return Failed
	}
}

// FileResults are the results of one test file.
type FileResults struct {
	File    discover.File
	Results []Result
}

// Counts returns how many results passed, failed and were skipped.
func (f FileResults) Counts() (passed, failed, skipped int) {
	for _, r := range f.Results {
		switch r.Outcome {
		case Passed:
			passed++
		case Failed:
			failed++
		case Skipped:
			skipped++
		}
	}
	return passed, failed, skipped
}

// Assign groups the run's results by test file. module is the Go module
// path (to turn package import paths into directories) and root the
// absolute repository root (to make Ginkgo's file names relative). Files
// with no result in the run are left out. Results are in name order.
func (r *Run) Assign(files []discover.File, module, root string) []FileResults {
	byPath := map[string]*FileResults{}
	var order []string
	add := func(f discover.File, res Result) {
		fr := byPath[f.Path]
		if fr == nil {
			fr = &FileResults{File: f}
			byPath[f.Path] = fr
			order = append(order, f.Path)
		}
		fr.Results = append(fr.Results, res)
	}

	for pkg, byTest := range r.tests {
		dir := pkgDir(pkg, module)
		for name, res := range byTest {
			for _, f := range files {
				if f.Dir == dir && f.Declares(name) {
					add(f, *res)
					break
				}
			}
		}
	}
	for pkg, out := range r.buildFailed {
		dir := pkgDir(pkg, module)
		for _, f := range files {
			if f.Dir == dir {
				add(f, Result{Name: "(build)", Outcome: Failed, Output: out})
			}
		}
	}
	for _, s := range r.specs {
		rel := relPath(s.file, root)
		for _, f := range files {
			if f.Path == rel {
				add(f, s.result)
				break
			}
		}
	}

	sort.Strings(order)
	out := make([]FileResults, 0, len(order))
	for _, p := range order {
		fr := byPath[p]
		sort.SliceStable(fr.Results, func(i, j int) bool { return fr.Results[i].Name < fr.Results[j].Name })
		out = append(out, *fr)
	}
	return out
}

// pkgDir turns an import path into a directory relative to the module root.
func pkgDir(pkg, module string) string {
	if pkg == module {
		return "."
	}
	return strings.TrimPrefix(pkg, module+"/")
}

// relPath makes a Ginkgo file name relative to root. A file name from
// another checkout location (a report produced elsewhere) is matched from
// its last "/tests/" segment.
func relPath(name, root string) string {
	name = filepath.ToSlash(name)
	root = strings.TrimSuffix(filepath.ToSlash(root), "/")
	if strings.HasPrefix(name, root+"/") {
		return strings.TrimPrefix(name, root+"/")
	}
	if i := strings.LastIndex(name, "/tests/"); i >= 0 {
		return name[i+1:]
	}
	return name
}

func tail(s string) string {
	s = strings.TrimRight(s, "\n")
	if len(s) <= maxOutput {
		return s
	}
	cut := s[len(s)-maxOutput:]
	if i := strings.IndexByte(cut, '\n'); i >= 0 && i < len(cut)-1 {
		cut = cut[i+1:]
	}
	return "...\n" + cut
}
