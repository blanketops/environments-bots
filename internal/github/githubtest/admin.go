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

package githubtest

import (
	"net/http"
	"strconv"
	"strings"
)

// handleAdmin serves the endpoints the admin bot uses beyond those the other
// bots share: merging a pull request. It reports whether it handled the
// request. rest is the path after /repos/owner/repo. Inject a failed merge
// with FailPaths["PUT /repos/owner/repo/pulls/<n>/merge"].
func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request, rest string) bool {
	if r.Method == http.MethodPut && strings.HasPrefix(rest, "/pulls/") && strings.HasSuffix(rest, "/merge") {
		n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rest, "/pulls/"), "/merge"))
		for i := range s.Pulls {
			if s.Pulls[i].Number == n {
				s.Pulls[i].Merged = true
				s.Pulls[i].State = "closed"
				write(w, http.StatusOK, map[string]any{"merged": true, "sha": "merge" + strconv.Itoa(n)})
				return true
			}
		}
		write(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return true
	}
	return false
}
