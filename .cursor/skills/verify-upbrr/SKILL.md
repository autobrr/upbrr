---
name: verify-upbrr
description: Verify upbrr CLI/workflow locally
---
# Verify upbrr
Launch: go build ./cmd/upbrr; ./upbrr --help
Doctor: ./upbrr --version
Drive: build and run basic CLI
Evidence: build log
Cleanup: rm upbrr
