// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package redaction

import (
	"strings"
	"testing"
)

func TestRedactValueRecognizesControlSplitSecrets(t *testing.T) {
	t.Parallel()
	for name, input := range map[string]string{
		"literal_ansi":        "pass\x1b[31mword=synthetic-credential",
		"literal_control":     "pass\bword=synthetic-credential",
		"json_string_ansi":    `{"message":"pass\u001b[31mword=synthetic-credential"}`,
		"json_string_control": `{"message":"pass\bword=synthetic-credential"}`,
		"json_key_ansi":       `{"pass\u001b[31mword":"synthetic-credential","message":"ready"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if got := RedactValue(input, nil); strings.Contains(got, "synthetic-credential") || !strings.Contains(got, "[REDACTED]") {
				t.Fatal("control-split credential was not redacted")
			}
		})
	}
}

func TestRedactValuePreservesHarmlessControls(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		"status=\x1b[31mready\x1b[0m",
		`{"message":"status=\u001b[31mready\u001b[0m"}`,
		"first\nsecond\tready",
	} {
		if got := RedactValue(input, nil); got != input {
			t.Fatal("harmless diagnostic content changed")
		}
	}
}

func TestRedactValueNormalizesMixedSecretDiagnostic(t *testing.T) {
	t.Parallel()
	input := "status=\x1b[31mready\x1b[0m password=synthetic-credential"
	if got := RedactValue(input, nil); got != "status=ready password=[REDACTED]" {
		t.Fatal("mixed diagnostic did not retain ordinary text while redacting the credential")
	}
}
