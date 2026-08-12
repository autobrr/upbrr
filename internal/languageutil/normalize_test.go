// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package languageutil

import "testing"

func TestNormalizeLanguageDisplayCodes(t *testing.T) {
	cases := map[string]string{
		"en":    "English",
		"en-US": "English",
		"eng":   "English",
		"mul":   "Multiple Languages",
		"zxx":   "ZXX",
	}
	for input, expected := range cases {
		if got := NormalizeLanguageDisplay(input); got != expected {
			t.Fatalf("expected %q for %q, got %q", expected, input, got)
		}
	}
}

func TestNormalizeLanguageDisplayNames(t *testing.T) {
	cases := map[string]string{
		"English": "English",
		"english": "English",
	}
	for input, expected := range cases {
		if got := NormalizeLanguageDisplay(input); got != expected {
			t.Fatalf("expected %q for %q, got %q", expected, input, got)
		}
	}
}

func TestNormalizeLanguageDisplayInvalid(t *testing.T) {
	if got := NormalizeLanguageDisplay("zzzz"); got != "" {
		t.Fatalf("expected empty string for invalid language, got %q", got)
	}
}
