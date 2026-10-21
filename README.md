# environments-bots

Bots and helpers that automate the BlanketOps Environments repositories: the
bookkeeping around tests, releases and the project board that workflows
otherwise do in inline shell.

| Part | What it does | Status |
|---|---|---|
| `test-helper` | Keeps one GitHub issue per test under `tests/` (a `_test.go` file that declares tests) and posts every test run's result to it, with a link to the run | Planned; first tool |

## Why

The test bookkeeping in `environments-tests` lived in workflow shell
(`create-test.yml`): hard to test, tied to one repository, and run with a
personal token. A small Go tool in its own repository can be tested,
released like the rest of the family, used by any repository from one
workflow step, and run as the BlanketOps-Environments GitHub App.

## Status

Nothing is released yet. The repository holds the project's standard files;
`test-helper`'s code, CI and release workflows arrive with its first pull
request.

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

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Work branches merge into `develop`;
`main` receives releases. Commits follow Conventional Commits and are signed
off (`git commit -s`) and signed.

## License

Apache License 2.0. See [LICENSE](LICENSE).
