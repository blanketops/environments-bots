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
