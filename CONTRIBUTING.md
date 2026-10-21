# Contributing

## Branches

Branch from `develop` and open pull requests against it. `main` only
receives releases (create-release / finalize-release). Fill in the pull
request template.

## Commits

- Conventional Commits (`feat:`, `fix:`, `docs:`, `chore:`, `ci:`), with a
  scope where it helps (`feat(test-helper): ...`). The changelog is
  generated from them.
- Sign off every commit (`git commit -s`) and sign it; the branches require
  verified signatures.

## Before opening a pull request

`go build ./...`, `go vet ./...`, `gofmt -l .` (prints nothing),
`golangci-lint run`, `go test ./...`.
