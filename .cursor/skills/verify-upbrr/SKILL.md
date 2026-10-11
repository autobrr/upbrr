name: verify-upbrr
description: Verify upbrr CLI/build via make/test

Launch: make backend / go test
Doctor: go build ./cmd/upbrr
Drive: go test -race ./...
Evidence: test output
Cleanup: none needed
