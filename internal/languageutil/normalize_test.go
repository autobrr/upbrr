// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package languageutil

import (
	"slices"
	"testing"

	"golang.org/x/text/language"
)

func TestNormalizeLanguageDisplayCodes(t *testing.T) {
	cases := map[string]string{
		"en":    "English",
		"en-US": "English",
		"eng":   "English",
		"mul":   "Multiple Languages",
		"zxx":   "ZXX",
		"phi":   "Philippine languages",
		"gd":    "Scottish Gaelic",
		"nb":    "Norwegian Bokmål",
	}
	for input, expected := range cases {
		if got := NormalizeLanguageDisplay(input); got != expected {
			t.Fatalf("expected %q for %q, got %q", expected, input, got)
		}
	}
}

func TestNormalizeLanguageDisplayNames(t *testing.T) {
	cases := map[string]string{
		"English":          "English",
		"english":          "English",
		"Scottish Gaelic":  "Scottish Gaelic",
		"Norwegian Bokmal": "Norwegian Bokmål",
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
	want := []string{"Scottish Gaelic", "Scottish Gaelic [gd-GB]", "Unknown Label"}
	if !slices.Equal(got, want) {
		t.Fatalf("language list = %#v, want %#v", got, want)
	}
}

func TestNormalizeLanguageCodeResolvesCodesAndCompleteNames(t *testing.T) {
	t.Parallel()

	for input, want := range map[string]string{
		"fra":                  "fr",
		"French":               "fr",
		"gd-GB":                "gd",
		"Scottish Gaelic":      "gd",
		"phi":                  "phi",
		"Philippine languages": "phi",
		"fil":                  "fil",
		"Filipino":             "fil",
		"tgl":                  "tl",
		"tl":                   "tl",
		"Tagalog":              "tl",
		"Norwegian Bokmål":     "nb",
		"Norwegian":            "no",
		"Mandarin":             "cmn",
		"Mandarin Chinese":     "cmn",
		"und":                  "und",
		"Undetermined":         "und",
		"Uncoded languages":    "mis",
		"ger":                  "de",
		"Malay":                "ms",
		"Nepali":               "ne",
		"Swahili":              "sw",
		"Dogri":                "doi",
		"Konkani":              "kok",
		"Mari":                 "chm",
		"Gbaya":                "gba",
		"":                     "",
		"Unknown Label":        "",
	} {
		if got := NormalizeLanguageCode(input); got != want {
			t.Fatalf("NormalizeLanguageCode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestLanguageCorrectionsPreserveUnresolvedTags(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"x-custom", "und-US", "Unknown language", "qau", "i-default", "zh-min"} {
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

func TestProducerNamesDoNotBecomeConflictingCodes(t *testing.T) {
	for _, tt := range []struct{ name, want string }{{"Ga", "gaa"}, {"ga", "ga"}, {"Kru", "kro"}, {"kru", "kru"}, {"Malay", "ms"}} {
		labels := NormalizeLanguageNames([]string{tt.name})
		if len(labels) != 1 || NormalizeLanguageCode(labels[0]) != tt.want {
			t.Errorf("producer name %q became %q, want %q", tt.name, labels, tt.want)
		}
	}
	if NormalizeLanguageCode("ga") != "ga" || NormalizeLanguageCode("kru") != "kru" {
		t.Fatal("explicit language codes were reinterpreted as names")
	}
}

func TestLanguageCoveragePreservesExplicitFullAndForced(t *testing.T) {
	for input, want := range map[string]string{
		"English Forced":   "English (Forced)",
		"eng Full":         "English (Full)",
		"English - Forced": "English (Forced)",
		"English (Full)":   "English (Full)",
		"Spanish":          "Spanish",
		"phi (Forced)":     "Philippine languages (Forced)",
		"phi Full":         "Philippine languages (Full)",
	} {
		got := NormalizeLanguageList([]string{input})
		if len(got) != 1 || got[0] != want {
			t.Fatalf("%q=%v want %q", input, got, want)
		}
		if NormalizeLanguageCode(got[0]) == "" {
			t.Fatalf("coverage lost base-language matching for %q", got[0])
		}
	}
	if got := NormalizeLanguageList([]string{"Custom Dialect"}); len(got) != 1 || got[0] != "Custom Dialect" {
		t.Fatalf("custom=%v", got)
	}
}

func TestLanguageIdentityPreservesVariantsAndStableListOrder(t *testing.T) {
	want := []string{"Tagalog", "Filipino", "Philippine languages", "Portuguese [pt-BR]", "Portuguese [pt-PT]", "Chinese [zh-Hans]", "Chinese [zh-Hant]", "Norwegian Bokmål", "Norwegian", "Mandarin Chinese", "Chinese", "Custom Dialect (Forced)"}
	got := NormalizeLanguageList([]string{"tgl", "fil", "phi", "pt-BR", "pt-PT", "zh-Hans", "zh-Hant", "nb", "no", "cmn", "zh", "Tagalog", "Brazilian Portuguese", "Custom Dialect (Forced)"})
	if !slices.Equal(got, want) {
		t.Fatalf("language identities = %q, want %q", got, want)
	}
	if again := NormalizeLanguageList(got); !slices.Equal(again, want) {
		t.Fatalf("normalizing retained labels changed identities: %q", again)
	}
	if got := NormalizeLanguageList([]string{"Greek, Modern (1453-)", "English,French"}); !slices.Equal(got, []string{"Greek", "English", "French"}) {
		t.Fatalf("complete names or CSV entries changed: %q", got)
	}
}

func TestSupportedLanguageLabelsRoundTripWithoutGrouping(t *testing.T) {
	for _, base := range language.Supported.BaseLanguages() {
		if _, bibliographic := bibliographicAliases[base.String()]; bibliographic {
			continue
		}
		label := NormalizeLanguageLabel(base.String())
		if label == "" {
			continue
		}
		expected := base.String()
		if preferred := registryLanguages[expected].preferred; preferred != "" {
			expected = preferred
		}
		code, _, _ := language.Deprecated.Make(expected).Raw()
		if got := NormalizeLanguageCode(label); got != code.String() {
			t.Errorf("code %q became %q through label %q", code, got, label)
		}
	}
}

func TestSubtitleLanguagePartsPreservesCustomBareSuffixes(t *testing.T) {
	for _, test := range []struct {
		input, base, coverage string
	}{
		{"Custom Dialect Full", "Custom Dialect Full", ""},
		{"Custom Dialect Forced", "Custom Dialect Forced", ""},
		{"Custom Dialect (Full)", "Custom Dialect", "Full"},
		{"Custom Dialect - Forced", "Custom Dialect", "Forced"},
		{"English Full", "English", "Full"},
		{"en Forced", "en", "Forced"},
	} {
		base, coverage := SubtitleLanguageParts(test.input)
		if base != test.base || coverage != test.coverage {
			t.Errorf("%q = (%q, %q), want (%q, %q)", test.input, base, coverage, test.base, test.coverage)
		}
	}
}
