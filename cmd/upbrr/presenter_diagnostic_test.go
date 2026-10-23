// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/logging"
)

func TestSafeDiagnosticTextPreservesSanitization(t *testing.T) {
	t.Parallel()
	for name, input := range map[string]string{
		"ordinary":       "tracker=SYN state=ready count=3",
		"whitespace":     "first\nsecond\tready",
		"styled":         "state=\x1b[32mready\x1b[0m",
		"controls":       "state=rea\bdy\u202eready",
		"password":       "password=synthetic-credential state=ready",
		"split_password": "pass\x1b[31mword=synthetic-credential state=ready",
		"json":           `{"password":"synthetic-credential","message":"ready"}`,
		"encoded_key":    `{"pass\u0077ord":"synthetic-credential","message":"ready"}`,
		"url":            "https://tracker.example/path?token=synthetic-credential&state=ready",
	} {
		t.Run(name, func(t *testing.T) {
			want := safeTerminalText(logging.SanitizeMessage(safeTerminalText(input)))
			got := safeDiagnosticText(input)
			if got != want {
				t.Fatal("diagnostic sanitization changed")
			}
			if strings.Contains(got, "synthetic-credential") {
				t.Fatal("diagnostic retained the synthetic credential")
			}
			if strings.ContainsAny(got, "\x1b\b\u202e") {
				t.Fatal("diagnostic retained terminal controls")
			}
		})
	}
}

func BenchmarkSafeDiagnosticText(b *testing.B) {
	for name, input := range map[string]string{
		"ordinary":   "tracker=SYN state=ready count=3",
		"styled":     "state=\x1b[32mready\x1b[0m",
		"credential": "password=synthetic-credential state=ready",
	} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = safeDiagnosticText(input)
			}
		})
	}
}
