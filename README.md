# environments-bots

Bots and helpers that automate the BlanketOps Environments repositories: the
bookkeeping around tests, releases and the project board that workflows
otherwise do in inline shell.

| Part | What it does | Status |
|---|---|---|
| `test-helper` | Keeps one GitHub issue per test file under `tests/` and posts every test run's results to it, with a link to the run | `sync` and `report` work; not released yet |

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
| Create Release (`create-release.yml`) | manual, input `version` | Cuts `release/<version>` from `develop` and opens its pull request into `main` |
| Finalize Release (`finalize-release.yml`) | manual, input `version` | Merges the release, commits code docs and `CHANGELOG.md` to `main`, tags, merges `main` back into `develop` |
| Release (`release.yml`) | `v*` tag | Builds every tool under `cmd/` for linux/amd64 and linux/arm64, signs each with Cosign, attests build provenance, publishes the GitHub Release |

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

## Releases

Each tag `vX.Y.Z` publishes `test-helper-linux-amd64` and
`test-helper-linux-arm64`, a `SHA256SUMS` file, and a Sigstore bundle per
file. Verify a download:

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
