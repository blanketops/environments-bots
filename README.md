# environments-bots

Bots and helpers that automate the BlanketOps Environments repositories: the
bookkeeping around tests, releases and the project board that workflows
otherwise do in inline shell.

| Part | What it does | Status |
|---|---|---|
| `test-helper` | Keeps one GitHub issue per test under `tests/` (a `_test.go` file that declares tests) and posts every test run's result to it, with a link to the run | Entry point only (`version`, `help`); the issue and run-result commands are next |

## Why

The test bookkeeping in `environments-tests` lived in workflow shell
(`create-test.yml`): hard to test, tied to one repository, and run with a
personal token. A small Go tool in its own repository can be tested,
released like the rest of the family, used by any repository from one
workflow step, and run as the BlanketOps-Environments GitHub App.

## Status

Nothing is released yet. `test-helper` has its entry point; CI and the
release workflows are in place.

| Workflow | Runs on | What it does |
|---|---|---|
| CI (`ci.yml`) | push, pull request | Build; gofmt, vet, golangci-lint and tests with coverage; code docs to `docs/code/` on `main` |
| Security (`security.yml`) | weekly schedule, manual | CodeQL, OpenSSF Scorecard, govulncheck (report-only) |
| Create Release (`create-release.yml`) | manual, input `version` | Cuts `release/<version>` from `develop` and opens its pull request into `main` |
| Finalize Release (`finalize-release.yml`) | manual, input `version` | Merges the release, commits code docs and `CHANGELOG.md` to `main`, tags, merges `main` back into `develop` |
| Release (`release.yml`) | `v*` tag | Builds every tool under `cmd/` for linux/amd64 and linux/arm64, signs each with Cosign, attests build provenance, publishes the GitHub Release |

Jobs run on the repository's self-hosted runner pool (`runs-on:
self-hosted`).

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
| `go run ./cmd/test-helper version` | Run test-helper from source |

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
