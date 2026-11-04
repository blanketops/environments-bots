## [0.1.0] - 2026-11-04

### 🚀 Features

- *(pr-reviewer)* Optional LLM review pass

### 🐛 Bug Fixes

- *(llm)* Check resp.Body.Close error (errcheck)
- *(developer)* Auto-populate the tracking issue from the repo template

### 📚 Documentation

- Auto-generate code documentation [skip ci] (#83)

### ⚙️ Miscellaneous Tasks

- Sync develop with main after release/v0.0.9
- *(pr-review)* Authenticate with client-id from a variable
- *(clean)* Run the cleaner bot on a daily schedule
## [0.0.9] - 2026-11-04

### 🚀 Features

- *(developer)* Honour the repo's PR and issue templates
- Add pr-reviewer bot for pull request review
- *(developer)* Honour the repo's PR and issue templates
- *(developer)* Fill the PR template's What and Why
- *(pr-reviewer)* Inline advisory, body-based checklist, untrusted-input scan
- *(pr-reviewer)* Add inline advisory and untrusted-input scan
- *(developer)* Restore the PR template What/Why fill
- *(pr-reviewer)* Flag leftover debug/panic and TODO/FIXME inline

### 💼 Other

- Merge release/v0.0.9 into main

### 📚 Documentation

- Auto-generate code documentation [skip ci] (#71)

### 🎨 Styling

- Gofmt the githubtest Files field alignment
- Gofmt the pr-reviewer code
- *(pr-reviewer)* Tagged switch on issue severity (QF1003)

### ⚙️ Miscellaneous Tasks

- Sync develop with main after release/v0.0.8
- *(developer)* Sanitize resolve-issue input
- *(release)* Update changelog for v0.0.9 (#72)
## [0.0.8] - 2026-11-04

### 🐛 Bug Fixes

- *(developer)* Skip open when branch has no commits ahead of base

### 📚 Documentation

- Auto-generate code documentation [skip ci] (#51)

### 🧪 Testing

- Dogfood the developer bot's open and resolve

### ⚙️ Miscellaneous Tasks

- Sync develop with main after release/v0.0.7
- Dogfood the developer bot in environments-bots
- *(release)* Update changelog for v0.0.8 (#52)
## [0.0.7] - 2026-11-03

### 🚀 Features

- *(cleaner)* Stub the environments-cleaner bot with run pruning

### 📚 Documentation

- Document the developer bot's full command set and the cleaner bot
- Auto-generate code documentation [skip ci] (#44)

### ⚙️ Miscellaneous Tasks

- Sync develop with main after release/v0.0.6
- *(release)* Update changelog for v0.0.7 (#45)
## [0.0.6] - 2026-11-03

### 🚀 Features

- *(developer)* Close a branch's tracking issue when its PR merges
- *(developer)* Commit gofmt and go mod tidy fixes to a branch

### 📚 Documentation

- Auto-generate code documentation [skip ci] (#38)

### ⚙️ Miscellaneous Tasks

- Sync develop with main after release/v0.0.5
- *(release)* Update changelog for v0.0.6 (#39)
## [0.0.5] - 2026-11-02

### 🚀 Features

- *(developer)* Open a pull request and tracking issue for a branch

### 📚 Documentation

- Tidy the README, add the developer bot and per-bot icons
- Auto-generate code documentation [skip ci] (#32)

### ⚙️ Miscellaneous Tasks

- Sync develop with main after release/v0.0.4
- *(release)* Update changelog for v0.0.5 (#33)
## [0.0.4] - 2026-11-02

### 🚀 Features

- *(test-helper)* Add layer filter for sync and report
- *(developer)* Sync develop with main

### 📚 Documentation

- Auto-generate code documentation [skip ci] (#24)
- Auto-generate code documentation [skip ci] (#25)

### ⚙️ Miscellaneous Tasks

- Sync develop with main after release/v0.0.3
- Sync develop with main
- *(release)* Update changelog for v0.0.4 (#26)
## [0.0.3] - 2026-10-23

### 🐛 Bug Fixes

- *(releaser)* Keep every version part a single digit

### 💼 Other

- Merge release/v0.0.3 into main

### 📚 Documentation

- Auto-generate code documentation [skip ci] (#17)

### ⚙️ Miscellaneous Tasks

- Sync develop with main after release/v0.0.2
- *(release)* Update changelog for v0.0.3 (#18)
## [0.0.2] - 2026-10-23

### 🚀 Features

- *(releaser)* Run the releaser from any repository with one action

### 🐛 Bug Fixes

- *(releaser)* Say so when the release pull request is already merged

### ⚙️ Miscellaneous Tasks

- Sync develop with main after release/v0.0.1
- *(release)* Update changelog for v0.0.2 (#13)
## [0.0.1] - 2026-10-22

### 🚀 Features

- *(test-helper)* Add the entry point
- *(test-helper)* Open an issue per test file and post each run's results
- *(releaser)* Keep every release attached to its Release issue
- *(releaser)* Plan and cut releases
- *(releaser)* Leave people's Release issues to them

### 🐛 Bug Fixes

- *(test-helper)* Count only func TestXxx(t *testing.T) as a test; show <1ms

### 📚 Documentation

- Auto-generate code documentation [skip ci] (#7)

### 🎨 Styling

- Gofmt the GitHub test fake

### ⚙️ Miscellaneous Tasks

- Start environments-bots with the family's standard files
- Put the workflows on main so they can be dispatched
- Add CI, security and release workflows
- Run the releaser in the release workflows
- Plan on develop pushes and cut with the releaser
- *(finalize)* Continue when the release pull request is already merged
- *(release)* Update changelog for v0.0.1 (#9)
