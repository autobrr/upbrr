// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package languageutil

import (
	"slices"
	"testing"
)

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

func TestNormalizeLanguageListSplitsNormalizesAndPreservesUnknowns(t *testing.T) {
	t.Parallel()

	got := NormalizeLanguageList([]string{" en, fra ", "English", " Unknown Label ", ",unknown label,,"})
	want := []string{"English", "French", "Unknown Label"}
	if !slices.Equal(got, want) {
		t.Fatalf("language list = %#v, want %#v", got, want)
	}
}

func TestNormalizeLanguageListPreservesCompleteBaseNames(t *testing.T) {
	t.Parallel()

	got := NormalizeLanguageList([]string{"gd", "Scottish Gaelic", "gd-GB", "Unknown Label"})
	want := []string{"Scottish Gaelic", "Unknown Label"}
	if !slices.Equal(got, want) {
		t.Fatalf("language list = %#v, want %#v", got, want)
	}
}

func TestNormalizeLanguageCodeResolvesCodesAndCompleteNames(t *testing.T) {
	t.Parallel()

	for input, want := range map[string]string{
		"fra":             "fr",
		"French":          "fr",
		"gd-GB":           "gd",
		"Scottish Gaelic": "gd",
		"":                "",
		"Unknown Label":   "",
	} {
		if got := NormalizeLanguageCode(input); got != want {
			t.Fatalf("NormalizeLanguageCode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestLanguageCorrectionsPreserveUnresolvedTags(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"x-custom", "und-US", "Unknown language"} {
		if got := NormalizeLanguageList([]string{value}); !slices.Equal(got, []string{value}) {
			t.Errorf("language list changed unresolved value %q to %v", value, got)
		}
		if got := NormalizeLanguageCode(value); got != "" {
			t.Errorf("language code inferred %q from unresolved value %q", got, value)
		}
		if got := NormalizeLanguageLabel(value); got != "" {
			t.Errorf("language label inferred %q from unresolved value %q", got, value)
		}
	}
	for _, value := range []string{"en-US", "gd-GB", "mul", "zxx"} {
		if NormalizeLanguageCode(value) == "" || NormalizeLanguageLabel(value) == "" {
			t.Errorf("explicit language tag %q lost its code or label", value)
		}
	}
}
