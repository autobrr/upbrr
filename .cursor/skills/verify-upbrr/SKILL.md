---
name: verify-upbrr
description: Verify upbrr CLI and configuration-import changes with repository-native commands.
---

# Verify upbrr

## Launch

For a build check run `make backend`. The CLI is short-lived; invoke commands individually rather than keeping a shared process running. No server or credentials are needed for legacy configuration conversion tests.

## Doctor

Run `git status --short --branch` and `go version`; confirm the checkout is the intended branch and Go is installed.

## Drive

For legacy config import behavior run `go test ./internal/config/legacy`. This drives the production import/conversion entry point through the package tests.

## Evidence

Record command output and exit status in the task report. For a broader backend change use `make test-go`, `make lint`, and `make backend`; use the repo's frontend and documentation commands for changes in those areas.

## Cleanup

These checks do not start persistent processes or write repository artifacts. Remove only scratch data created during verification; do not remove test evidence.

## Helpers

No helper scripts are required.
