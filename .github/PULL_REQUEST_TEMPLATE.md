## What

<!-- One or two sentences. What does this PR change? -->

## Why

<!-- The intent. Link the issue if one exists: Closes #123 -->

## Area

* [ ] test-helper
* [ ] CI / release tooling / docs

## Checklist

* [ ] `go build ./...` and `go vet ./...` pass
* [ ] `gofmt -l .` prints nothing
* [ ] `golangci-lint run` clean
* [ ] `go test ./...` passes
* [ ] Untrusted input (titles, test names, output) is never interpolated into shell or queries
* [ ] Commit messages follow Conventional Commits and are signed off (`git commit -s`)

## Notes for reviewer

<!-- Anything non-obvious: design trade-offs, deferred follow-ups -->
