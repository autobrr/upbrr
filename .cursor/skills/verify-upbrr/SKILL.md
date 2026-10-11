---
name: verify-upbrr
description: Project-local verification skill for upbrr (Go CLI + WebUI). Launches the Go binary, drives tracker-related commands, and captures terminal evidence.
---

# verify-upbrr

Surface: Go CLI (`cmd/upbrr`) and WebUI (`webui`).

Launch: `make backend` builds binary; run `go test -race -v -timeout 20m ./...` for verification.
Doctor: `go version` and `go build ./cmd/upbrr` succeed.
Drive: `make test-go` and `make lint`.
Evidence: test output saved to `.cursor/skills/verify-upbrr/evidence/test-output.log`.
Cleanup: `find .cursor/skills/verify-upbrr/evidence -type f -delete` (preserve directories).
Helpers: `make backend`, `make lint`.
