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
	"fmt"
	"net/http"
	"strings"
)

// This file holds the calls the admin bot makes beyond those the other bots
// share: merging a pull request server-side, so the merge commit is signed by
// GitHub and passes the signature ruleset with no bypass.

// IsAlreadyExists reports whether err is a 422 from creating a git ref that
// already exists (GitHub answers POST /git/refs on an existing ref with
// "Reference already exists"). Note the asymmetry: DELETE of a ref that is NOT
// there is also a 422 ("Reference does not exist"), not a 404 — so never treat
// a missing ref as IsNotFound on a delete.
func IsAlreadyExists(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusUnprocessableEntity &&
		strings.Contains(strings.ToLower(e.Message), "already exists")
}

// MergePull merges pull request number with a merge commit (merge_method
// "merge") and returns the merge commit SHA. GitHub signs the commit, so it
// satisfies a branch's required-signatures rule without a ruleset bypass. An
// error whose IsConflict is true means the branch could not be merged (HTTP
// 405/409); the caller leaves the pull request open for a human.
func (c *Client) MergePull(ctx context.Context, number int) (string, error) {
	in := map[string]string{"merge_method": "merge"}
	var out struct {
		SHA    string `json:"sha"`
		Merged bool   `json:"merged"`
	}
	if err := c.do(ctx, http.MethodPut, c.repoPath(fmt.Sprintf("/pulls/%d/merge", number)), in, &out); err != nil {
		return "", err
	}
	if !out.Merged {
		return "", fmt.Errorf("pull request #%d was not merged", number)
	}
	return out.SHA, nil
}
