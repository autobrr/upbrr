# Verification Skill for upbrr

**Surface:** CLI tool for usenet/usenet release management. Primary surface is the CLI command `upbrr`, with web UI as secondary.

**Run:** `make backend` or `go build -o upbrr ./cmd/upbrr`. The CLI takes a torrent file or magnet link as input. Uses seedbox/local torrent client for processing.

**Drive:** The verification harness runs the CLI via PTY or test mode. Key commands:
- `upbrr upload <torrent>` - upload a release
- `upbrr debug --info <torrent>` - show parsed metadata
- `upbrr release-workflow <torrent>` - show release workflow

**Observe:** Evidence captured from:
- CLI exit codes and output lines
- Metadata facts parsed from MediaInfo
- Duplicate evaluation results
- SAM validation outcomes

**Isolate:** Each verification run uses isolated data directories. Multiple concurrent runs may conflict on port/session state.

**Launch:** Build once with `make backend`, then start verification drives in isolated PTY sessions. The CLI prompts for required answers interactively; use `--unattended` or provide answers via environment/flags.

**Doctor:** Check that the binary is built, tests pass (`make test-go`), and lint passes (`make lint`). Verify Go version compatibility with go.mod.

**Drive:** Run the test suite for SAM-related changes:
- `go test -race -v -timeout 20m ./internal/trackers/impl/unit3d/sites/sam/...`
- `go test -race -v -timeout 20m ./internal/mediafacts/...`
- Verify build: `go build ./...`

**Evidence:** Test results (pass/fail), build output, lint reports. Capture test output files and any SAM validation error messages.

**Cleanup:** `go clean -testcache`, remove any temporary test data. Does not remove evidence - test results survive cleanup.

**Helpers:**
- `make backend` - fast CLI build sanity
- `make test-go` - full Go race tests
- `make lint` - architecture/path/literal/contracts checks

**Feature map:** See `.cursor/skills/verify-upbrr/features/` for per-feature verification guides.