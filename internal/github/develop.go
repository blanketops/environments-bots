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

import (
	"context"
	"errors"
	"net/http"
)

// This file holds the calls developer makes beyond those releaser shares:
// a server-side merge (so the merge commit is signed by GitHub and passes
// the signature ruleset with no bypass) and deleting a branch.

// IsConflict reports whether err is an HTTP 409, which a merge returns when
// the two branches cannot be merged without resolving a conflict.
func IsConflict(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusConflict
}

// Merge merges head into base with a server-side merge commit and returns
// the merge commit's SHA. GitHub signs the commit, so it satisfies the
// branch's required-signatures rule without a ruleset bypass. The SHA is
// empty when base already contains head (HTTP 204, nothing to merge).
// IsConflict(err) reports a merge conflict (HTTP 409); the caller resolves
// it, as a bot cannot.
func (c *Client) Merge(ctx context.Context, base, head, message string) (string, error) {
	in := map[string]string{"base": base, "head": head, "commit_message": message}
	var out struct {
		SHA string `json:"sha"`
	}
	err := c.do(ctx, http.MethodPost, c.repoPath("/merges"), in, &out)
	return out.SHA, err
}

// DeleteBranch deletes a branch. IsNotFound(err) when it is already gone.
func (c *Client) DeleteBranch(ctx context.Context, branch string) error {
	return c.do(ctx, http.MethodDelete, c.repoPath("/git/refs/heads/"+refPath(branch)), nil, nil)
}
