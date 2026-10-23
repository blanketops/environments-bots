# environments-bots

Bots and helpers that automate the BlanketOps Environments repositories: the
bookkeeping around tests, releases and the project board that workflows
otherwise do in inline shell.

| Part | What it does | Status |
|---|---|---|
| `test-helper` | Keeps one GitHub issue per test file under `tests/` and posts every test run's results to it, with a link to the run | `sync` and `report` work; not released yet |
| `releaser` | Drives every release through its Release issue on the project board: plans the next release when develop has unreleased changes, cuts the release branch and its pull request, and attaches the tag and the GitHub release after a person finalizes | `plan`, `cut`, `prepare`, `verify` and `complete` work and run in this repository's release workflows; not released yet |

## Why

The test bookkeeping in `environments-tests` lived in workflow shell
(`create-test.yml`): hard to test, tied to one repository, and run with a
personal token. A small Go tool in its own repository can be tested,
released like the rest of the family, used by any repository from one
workflow step, and run as the BlanketOps-Environments GitHub App.

## Status

Nothing is released yet. CI and the release workflows are in place.

| Workflow | Runs on | What it does |
|---|---|---|
| CI (`ci.yml`) | push, pull request | Build; gofmt, vet, golangci-lint and tests with coverage; code docs to `docs/code/` on `main` |
| Security (`security.yml`) | weekly schedule, manual | CodeQL, OpenSSF Scorecard, govulncheck (report-only) |
| Plan Release (`plan-release.yml`) | push to `develop`, manual | `releaser plan`: keeps the next release planned as a Release issue |
| Create Release (`create-release.yml`) | manual, optional input `version` | `releaser cut`: checks the release, cuts `release/<version>` from `develop` and opens its pull request into `main` |
| Finalize Release (`finalize-release.yml`) | manual, input `version` | Runs `releaser verify`, then merges the release, commits code docs and `CHANGELOG.md` to `main`, tags, merges `main` back into `develop` |
| Release (`release.yml`) | `v*` tag | Builds every tool under `cmd/` for linux/amd64 and linux/arm64, signs each with Cosign, attests build provenance, publishes the GitHub Release, then runs `releaser complete` (also when a step before it failed) |

Jobs run on the repository's self-hosted runner pool (`runs-on:
self-hosted`).

## test-helper

A test file is a `*_test.go` file under `tests/` (`tests/data` excluded) that
declares a Go test function (`func TestXxx(t *testing.T)`) or a top-level
Ginkgo container or spec (`var _ = Describe(...)`). Helper files such as
`consts_test.go` declare neither. Each test file has one issue, titled
`[test] <path under tests/ without .go>` (e.g. `[test] fake/networks/route_test`)
and labelled `test-tracking` plus its layer (the first directory under
`tests/`, e.g. `fake`).

| Command | What it does |
|---|---|
| `test-helper sync` | Opens an issue for every test file that has none, and with `-project blanketops/1` adds each new one to the project with Status `Test` (`-status`). Existing issues, open or closed, are left alone. |
| `test-helper report -go-json <file> [-ginkgo-json <file>]...` | Comments each test file's results on its issue: a verdict, one row per test (name, result, time), the failure or skip output, and a link to the run. |

`report` assigns a Go test to the file that declares its function, and a
Ginkgo spec to the file of its `It`, read from Ginkgo's JSON report
(`-ginkgo.json-report=ginkgo-report.json`, written into each package's
directory). A package that does not compile gives each of its test files a
failed `(build)` row. Both commands append a markdown summary to
`$GITHUB_STEP_SUMMARY`, take the repository from `$GITHUB_REPOSITORY`, and
take the run link, name and commit from the workflow's environment. `-dry-run`
reads only and needs no token.

The token comes from `GH_TOKEN` (or `GITHUB_TOKEN`). In a workflow, mint one
for the BlanketOps-Environments App with `actions/create-github-app-token`;
the App needs Issues read and write on the repository and, for `-project`,
Projects read and write on the organization.

Everything taken from test files and test output (names, messages) is
written inside code spans or code blocks, so it cannot mention users or break
the markdown around it.

## releaser

A Release issue is titled `Release: vX.Y.Z` and labelled `release`. On the
project board it has Status `Release` until the release is published, then
`Done`. Each command comments its checks on the issue and exits 1 when one
fails; a failed release leaves its issue open with the reason.

A release goes: `plan` opens the issue when develop has changes no release
carries, `cut` cuts the branch when someone runs Create Release, a person
runs Finalize Release (merge and tag), and `complete` attaches the release
and closes the issue. Releases go out in version order.

Release issues a person opened (for example to plan a go-live) are theirs:
the releaser reuses one when its version is released, and `complete`
closes it then, but it never renames, flags or orders releases by them.
`plan` adopts a person's issue when it names the version the commits need,
instead of opening a second one. The releaser's own issues are those
opened by `-bot` (default `blanketops-environments-releaser[bot]`).

| Command | When | What it checks and does |
|---|---|---|
| `releaser plan [-ref develop]` | Every push to `develop` | With commits since the latest tag (merge commits do not count) and no planned release, opens the Release issue for the version they need, on the board as `Release`. When the planned version no longer covers the commits and its branch is not cut yet, renames the issue to the version they need and says why. Otherwise does nothing. |
| `releaser cut [-release vX.Y.Z] [-ref develop]` | Create Release | The checks of `prepare`; then creates `release/<version>` at the head of `develop` (no new commit) and opens its pull request into `main`, both kept when they already exist. Without `-release` it releases the planned Release issue, else the version the commits need. |
| `releaser prepare -release vX.Y.Z [-ref develop]` | Run by `cut`; alone, checks without cutting | The version is not tagged and is the next patch, minor or major after the latest tag; there are commits since that tag; the bump is at least what the commits need; there is at most one Release issue and it is open; every item of a milestone named after the version is closed; no lower version is still planned. Opens the issue if there is none (never for a version that fails the version check), puts it on the board as `Release`, and lists the release's commits. Warns about other open Release issues at or below the latest tag and about commits that are not Conventional Commits. |
| `releaser verify -release vX.Y.Z` | Before the release branch is finalized | The version is not tagged yet; exactly one open Release issue; no pull request still open into `release/<version>`; no open item in the version's milestone. |
| `releaser complete -release vX.Y.Z` | After the GitHub release is published | The tag exists and a published GitHub release exists for it. Comments the release, the tagged commit and the merged release pull request on the issue, closes it, sets it to `Done`, and closes the milestone when it has no open item. Opens the issue first if the release has none. |

The bump a release needs follows Conventional Commits and Semantic
Versioning. From v1.0.0 on, a breaking change (`type!:` or a
`BREAKING CHANGE:` footer) needs a major release, a `feat` a minor one, and
anything else a patch. Before v1.0.0 a breaking change needs a minor release
and everything else a patch. A larger bump than needed is accepted; a skipped
version (v0.8.7 to v0.8.9) is not.

Every part of a BlanketOps version is a single digit, 0 to 9, and a 9
carries into the part above: the release after v0.0.9 is v0.1.0, after
v0.1.9 v0.2.0, after v0.9.9 v1.0.0. A version with a larger part (v0.0.10)
is refused, and tags that are not `vX.Y.Z` in this form are not releases.

`-project blanketops/1` keeps the issue on the board; `-dry-run` reads only.
The token comes from `GH_TOKEN` (or `GITHUB_TOKEN`); the workflows mint one
for the BlanketOps Environments Releaser App (secrets `RELEASER_APP_ID` and
`RELEASER_APP_PRIVATE_KEY`), which has Contents, Pull requests, Issues and
organization Projects read and write, and Actions read. It creates release
branches and their pull requests; merging and tagging stay with Finalize
Release. Commit subjects and issue and milestone titles are written inside
code spans.

### Using it from another repository

Every BlanketOps repository runs the releaser through the composite action
`blanketops/environments-bots/.github/actions/releaser@vX.Y.Z`. It mints a
Releaser App token for the calling repository, downloads the `releaser`
binary of that same environments-bots release, verifies its Sigstore bundle
against `release.yml@refs/tags/vX.Y.Z` of this repository with cosign (a
binary that does not verify is never run), and runs one command:

```yaml
- uses: blanketops/environments-bots/.github/actions/releaser@vX.Y.Z
  with:
    command: cut                       # plan | cut | prepare | verify | complete
    release: ${{ inputs.version }}     # optional for plan and cut
    app-id: ${{ secrets.RELEASER_APP_ID }}
    private-key: ${{ secrets.RELEASER_APP_PRIVATE_KEY }}
```

`ref` (default `develop`), `project` (default `blanketops/1`) and
`releaser-version` (default: the tag the action is used at) are optional.
Each repository needs the two secrets and cosign on its runners (the
zenith-runner-toolchain image has it). This repository's own workflows build
the releaser from source instead, so a change is exercised before it is
released.

## Security

The tools act on GitHub through an App installation token passed in by the
calling workflow. They never need a personal token. Report vulnerabilities
as described in [SECURITY.md](SECURITY.md).

## Development

| Command | What it does |
|---|---|
| `go build ./...` | Build every tool |
| `go vet ./...` | Static checks |
| `go test ./...` | Unit tests |
| `golangci-lint run` | Lint (the runner image ships the version CI uses) |
| `go run ./cmd/test-helper sync -dry-run -root <checkout> -repo <owner/name>` | Run test-helper from source against a checkout, read-only |
| `go run ./cmd/releaser cut -dry-run -repo <owner/name>` | Show what the next release would be and whether it can be cut, read-only |

## Releases

Each tag `vX.Y.Z` publishes `<tool>-linux-amd64` and `<tool>-linux-arm64`
for every tool (`test-helper`, `releaser`), a `SHA256SUMS` file, and a
Sigstore bundle per file. Verify a download:

```
cosign verify-blob --bundle test-helper-linux-amd64.sigstore.json \
  --certificate-identity https://github.com/blanketops/environments-bots/.github/workflows/release.yml@refs/tags/vX.Y.Z \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  test-helper-linux-amd64
gh attestation verify test-helper-linux-amd64 --repo blanketops/environments-bots
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Work branches merge into `develop`;
`main` receives releases. Commits follow Conventional Commits and are signed
off (`git commit -s`) and signed.

## License

Apache License 2.0. See [LICENSE](LICENSE).
