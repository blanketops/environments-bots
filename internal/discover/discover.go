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

// Package discover finds the test files of a repository.
//
// A test file is a *_test.go file under the tests directory (an excluded
// subtree such as tests/data aside) that declares tests: a top-level Go test
// function (func TestXxx(t *testing.T)) or a top-level Ginkgo container or
// spec (var _ = Describe(...)). Helper files such as consts_test.go declare
// neither and are not test files.
package discover

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// File is one test file.
type File struct {
	// Path is the file's slash-separated path relative to the repository
	// root, e.g. "tests/fake/networks/route_test.go".
	Path string
	// Dir is Path's directory, e.g. "tests/fake/networks".
	Dir string
	// Key identifies the file under the tests directory, without the .go
	// extension, e.g. "fake/networks/route_test". Issue titles derive from it.
	Key string
	// Layer is the first directory under the tests directory, e.g. "fake";
	// empty for a file directly in it.
	Layer string
	// Tests are the Go test functions the file declares, in source order.
	Tests []string
	// Containers are the texts of the file's top-level Ginkgo containers and
	// specs, in source order. A container whose text is not a string literal
	// is listed as "(dynamic)".
	Containers []string
}

// Title is the tracking issue's title for f, e.g.
// "[test] fake/networks/route_test".
func (f File) Title() string { return "[test] " + f.Key }

// Declares reports whether the file declares the Go test function name.
func (f File) Declares(name string) bool {
	for _, t := range f.Tests {
		if t == name {
			return true
		}
	}
	return false
}

// Options control Discover.
type Options struct {
	// TestsDir is the tests directory relative to the root. Default "tests".
	TestsDir string
	// Exclude lists subtrees, relative to the root, whose files are never
	// test files. Default ["tests/data"].
	Exclude []string
}

func (o Options) withDefaults() Options {
	if o.TestsDir == "" {
		o.TestsDir = "tests"
	}
	if o.Exclude == nil {
		o.Exclude = []string{path.Join(o.TestsDir, "data")}
	}
	o.TestsDir = path.Clean(filepath.ToSlash(o.TestsDir))
	exclude := make([]string, len(o.Exclude))
	for i, e := range o.Exclude {
		exclude[i] = path.Clean(filepath.ToSlash(e))
	}
	o.Exclude = exclude
	return o
}

// ginkgoNodes are the Ginkgo functions that register a container or spec
// at the top level of a file, including their focused (F), pending (P) and
// excluded (X) forms.
var ginkgoNodes = func() map[string]bool {
	m := map[string]bool{}
	for _, base := range []string{"Describe", "Context", "When", "It", "Specify", "DescribeTable"} {
		m[base] = true
		for _, prefix := range []string{"F", "P", "X"} {
			m[prefix+base] = true
		}
	}
	return m
}()

// Discover returns the test files under root, sorted by path. A file that
// does not parse is an error: a test file the tool cannot read would
// otherwise silently lose its issue.
func Discover(root string, opts Options) ([]File, error) {
	opts = opts.withDefaults()
	testsRoot := filepath.Join(root, filepath.FromSlash(opts.TestsDir))
	var files []File
	fset := token.NewFileSet()
	err := filepath.WalkDir(testsRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if excluded(rel, opts.Exclude) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, "_test.go") || excluded(rel, opts.Exclude) {
			return nil
		}
		f, err := parseFile(fset, p, rel, opts.TestsDir)
		if err != nil {
			return err
		}
		if len(f.Tests) > 0 || len(f.Containers) > 0 {
			files = append(files, f)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover %s: %w", testsRoot, err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func excluded(rel string, exclude []string) bool {
	for _, e := range exclude {
		if rel == e || strings.HasPrefix(rel, e+"/") {
			return true
		}
	}
	return false
}

func parseFile(fset *token.FileSet, p, rel, testsDir string) (File, error) {
	src, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
	if err != nil {
		return File{}, err
	}
	key := strings.TrimSuffix(strings.TrimPrefix(rel, testsDir+"/"), ".go")
	f := File{Path: rel, Dir: path.Dir(rel), Key: key}
	if i := strings.Index(key, "/"); i > 0 {
		f.Layer = key[:i]
	}
	for _, decl := range src.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if isTestFunc(d) {
				f.Tests = append(f.Tests, d.Name.Name)
			}
		case *ast.GenDecl:
			if d.Tok != token.VAR {
				continue
			}
			for _, spec := range d.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, v := range vs.Values {
					if text, ok := ginkgoNode(v); ok {
						f.Containers = append(f.Containers, text)
					}
				}
			}
		}
	}
	return f, nil
}

// isTestFunc reports whether d is a Go test function: no receiver, a name
// that is "Test" or "Test" followed by a non-lowercase rune (as go test
// requires), and exactly one parameter.
func isTestFunc(d *ast.FuncDecl) bool {
	if d.Recv != nil || !strings.HasPrefix(d.Name.Name, "Test") {
		return false
	}
	if rest := d.Name.Name[len("Test"):]; rest != "" {
		r, _ := utf8.DecodeRuneInString(rest)
		if unicode.IsLower(r) {
			return false
		}
	}
	params := d.Type.Params.List
	return len(params) == 1 && len(params[0].Names) <= 1
}

// ginkgoNode reports whether e is a call to a Ginkgo container or spec
// function (Describe or ginkgo.Describe) and returns its text.
func ginkgoNode(e ast.Expr) (string, bool) {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	var name string
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		name = fn.Name
	case *ast.SelectorExpr:
		name = fn.Sel.Name
	default:
		return "", false
	}
	if !ginkgoNodes[name] {
		return "", false
	}
	if len(call.Args) > 0 {
		if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if s, err := strconv.Unquote(lit.Value); err == nil {
				return s, true
			}
		}
	}
	return "(dynamic)", true
}
