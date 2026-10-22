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

package github

import "testing"

func TestRefPath(t *testing.T) {
	for in, want := range map[string]string{"develop": "develop", "release/v0.1.0": "release/v0.1.0", "a b/c?d": "a%20b/c%3Fd"} {
		if got := refPath(in); got != want {
			t.Errorf("refPath(%q) = %q, want %q", in, got, want)
		}
	}
}
