// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestBHDTVTitlePolicyPrefersManualTitle(t *testing.T) {
	t.Parallel()
	meta := bhdGeneratedSubject(t, api.ReleaseNameRequest{
		Category:   "TV",
		Type:       "WEBDL",
		Title:      "Provider Series",
		Season:     "S01",
		Resolution: "1080p",
		Source:     "Web",
		Tag:        "-GRP",
	})
	meta.Identity.Category = api.CanonicalCategoryTV
	meta.ProviderMetadata = api.SourceScopedMetadata{TVDB: &api.TVDBMetadata{NameEnglish: "Provider Series"}}
	meta.EffectiveMetadata = api.EffectiveMetadata{Title: "Manual Series", TitleProvenance: api.FactProvenanceManual}
	if got := bhdReviewedName(t, meta, nil); got != "Manual Series S01 1080p WEB-DL-GRP" {
		t.Fatalf("manual title name = %q", got)
	}
	meta.EffectiveMetadata.Title = ""
	meta.EffectiveMetadata.TitleProvenance = api.FactProvenanceManualEmpty
	if got := bhdReviewedName(t, meta, nil); got != "Provider Series S01 1080p WEB-DL-GRP" {
		t.Fatalf("manual-empty title name = %q", got)
	}
}
