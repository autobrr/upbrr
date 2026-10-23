// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
)

func TestDiagnosticEntriesRedactEncodedControlSecrets(t *testing.T) {
	const secret = "synthetic-credential"
	input := `{"message":"pass\u001b[31mword=` + secret + `"}`
	var output strings.Builder
	printCLIError(&output, errors.New(input))
	bridge := newCLITUIBridge()
	writer := &cliTUILogWriter{bridge: bridge}
	logger, err := logging.New(config.LoggingConfig{Level: "info"}, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := logger.Close(); err != nil {
			t.Fatal(err)
		}
	})
	logger.SetConsoleOutput(writer, writer)
	logger.Infof("%s", input)
	if len(bridge.logs) != 1 || len(logger.Recent(1)) != 1 {
		t.Fatal("diagnostic did not reach expected sinks")
	}
	for _, actual := range []string{output.String(), bridge.logs[0].Text, logger.Recent(1)[0].Message} {
		if strings.Contains(actual, secret) || !strings.Contains(actual, "[REDACTED]") {
			t.Fatal("diagnostic retained a control-split credential")
		}
	}
}

func TestRawTUILogWriterOmitsOversizedSecretLine(t *testing.T) {
	const secret = "synthetic-credential"
	input := `{"password":"` + secret + `","message":"` + strings.Repeat("z", cliLogLineLimit) + `"}`
	for _, split := range []int{0, 12, cliLogLineLimit, len(input)} {
		bridge := newCLITUIBridge()
		writer := &cliTUILogWriter{bridge: bridge}
		for _, part := range []string{input[:split], input[split:], "\r\nready\n"} {
			if n, err := writer.Write([]byte(part)); err != nil || n != len(part) {
				t.Fatal("raw writer did not consume its input")
			}
			if len(writer.fragment) > cliLogLineLimit {
				t.Fatal("fragment exceeded its byte bound")
			}
		}
		if len(bridge.logs) != 2 || bridge.logs[0].Text != cliOversizedLogText || bridge.logs[1].Text != "ready" {
			t.Fatal("oversized line omission or following-line recovery failed")
		}
	}
}

func TestTUILogLineBoundaryAndSanitizedTruncation(t *testing.T) {
	bridge := newCLITUIBridge()
	writer := &cliTUILogWriter{bridge: bridge}
	boundary := strings.Repeat("x", cliLogLineLimit)
	if _, err := writer.Write([]byte(boundary + "\n")); err != nil {
		t.Fatal(err)
	}
	if len(bridge.logs) != 1 || bridge.logs[0].Text != boundary {
		t.Fatal("exact-boundary raw line was not preserved")
	}
	bridge.AppendLog("", `{"password":"synthetic-credential","message":"`+boundary+`"}`)
	if bridge.logs[1].Text != cliOversizedLogText {
		t.Fatal("oversized untrusted bridge line was not omitted")
	}
	writer.WriteConsoleLog("INFO", "status=ready "+boundary)
	if len(bridge.logs) != 3 || !strings.HasPrefix(bridge.logs[2].Text, "status=ready ") ||
		!strings.HasSuffix(bridge.logs[2].Text, " [truncated]") || len(bridge.logs[2].Text) != cliLogLineLimit {
		t.Fatal("already-sanitized logger line lost its bounded diagnostic prefix")
	}
}
