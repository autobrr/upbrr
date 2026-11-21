// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"slices"
	"testing"
)

func TestParseLogoLanguagesPreservesCanonicalPreferenceOrder(t *testing.T) {
	got, unsupported := ParseLogoLanguages(" PT-BR ; JA, pt; Pt-PT, en-US; ZZ; not-a-tag ")
	if !slices.Equal(got, []string{"pt", "ja", "en"}) || !slices.Equal(unsupported, []string{"ZZ", "not-a-tag"}) {
		t.Fatalf("preferences = %v, unsupported = %v", got, unsupported)
	}
	names := LogoLanguageNames()
	if names["pt"] == "" || names["br"] == names["pt"] || names["zz"] != "" {
		t.Fatalf("wrong artwork language catalog: pt=%q br=%q zz=%q", names["pt"], names["br"], names["zz"])
	}
}
