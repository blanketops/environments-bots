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

package discover

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeTree creates files (slash paths relative to the returned root).
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, content := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const ginkgoFile = `package networks

import (
	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2"
)

var _ = Describe("Route Controller", func() {
	It("reconciles", func() {})
})

var _ = ginkgo.Context("selector form", func() {})

var _, _ = FDescribe("focused", func() {}), PIt("pending")

var _ = XDescribe(name, func() {})

var notANode = fmt.Sprintf("x")

var _ = Entry("an Entry is not top-level registration", 1)
`

const goTestFile = `package grpc

import "testing"

func TestBuildService_CreateBuild(t *testing.T) {}
func TestA(t *testing.T) {}
func Test(t *testing.T) {}
func TestMain(m *testing.M) {}
func TestDot(t *T) {}
func TestValue(t testing.T) {}
func TestOther(t *testing.B) {}
func TestFunc(t func()) {}
func Testlowercase(t *testing.T) {}
func TestTwoParams(t *testing.T, x int) {}
func TestNoParams() {}
func (s *suite) TestMethod(t *testing.T) {}
func helper(t *testing.T) {}
`

func TestDiscover(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tests/fake/networks/route_test.go":  ginkgoFile,
		"tests/fake/networks/consts_test.go": "package networks\n\nconst x = 1\n",
		"tests/fake/networks/helper.go":      "package networks\n\nfunc TestNotATestFile(t *testing.T) {}\n",
		"tests/grpc/build_grpc_test.go":      goTestFile,
		"tests/data/networks/route_test.go":  goTestFile,
		"tests/top_test.go":                  "package tests\n\nimport \"testing\"\n\nfunc TestTop(t *testing.T) {}\n",
		"other/outside_test.go":              goTestFile,
	})

	files, err := Discover(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := []File{
		{
			Path:       "tests/fake/networks/route_test.go",
			Dir:        "tests/fake/networks",
			Key:        "fake/networks/route_test",
			Layer:      "fake",
			Containers: []string{"Route Controller", "selector form", "focused", "pending", "(dynamic)"},
		},
		{
			Path:  "tests/grpc/build_grpc_test.go",
			Dir:   "tests/grpc",
			Key:   "grpc/build_grpc_test",
			Layer: "grpc",
			Tests: []string{"TestBuildService_CreateBuild", "TestA", "Test", "TestDot"},
		},
		{
			Path:  "tests/top_test.go",
			Dir:   "tests",
			Key:   "top_test",
			Tests: []string{"TestTop"},
		},
	}
	if !reflect.DeepEqual(files, want) {
		t.Errorf("Discover() =\n%#v\nwant\n%#v", files, want)
	}
}

func TestDiscoverOptions(t *testing.T) {
	root := writeTree(t, map[string]string{
		"e2e/a/x_test.go":    "package a\n\nfunc TestX(t *testing.T) {}\n",
		"e2e/skip/y_test.go": "package skip\n\nfunc TestY(t *testing.T) {}\n",
		"e2e/data/z_test.go": "package data\n\nfunc TestZ(t *testing.T) {}\n",
	})
	exclude := []string{"e2e/skip/"}
	files, err := Discover(root, Options{TestsDir: "e2e/", Exclude: exclude})
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, f := range files {
		keys = append(keys, f.Key)
	}
	if want := []string{"a/x_test", "data/z_test"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}
	if exclude[0] != "e2e/skip/" {
		t.Errorf("Discover modified the caller's Exclude slice: %v", exclude)
	}
}

func TestDiscoverErrors(t *testing.T) {
	t.Run("missing tests directory", func(t *testing.T) {
		if _, err := Discover(t.TempDir(), Options{}); err == nil {
			t.Error("want an error")
		}
	})
	t.Run("unparsable test file", func(t *testing.T) {
		root := writeTree(t, map[string]string{"tests/a/bad_test.go": "package a\n\nfunc {"})
		_, err := Discover(root, Options{})
		if err == nil || !strings.Contains(err.Error(), "bad_test.go") {
			t.Errorf("err = %v, want it to name bad_test.go", err)
		}
	})
}

func TestFile(t *testing.T) {
	f := File{Key: "grpc/build_grpc_test", Tests: []string{"TestA", "TestB"}}
	if got, want := f.Title(), "[test] grpc/build_grpc_test"; got != want {
		t.Errorf("Title() = %q, want %q", got, want)
	}
	if !f.Declares("TestB") || f.Declares("TestC") {
		t.Errorf("Declares() wrong for %v", f.Tests)
	}
}
