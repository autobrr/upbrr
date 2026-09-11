// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestBHDTVTitlePolicyPrefersManualTitle(t *testing.T) {
	t.Parallel()
	meta := api.UploadSubject{
		SeasonStr: "S01",
		ProviderMetadata: api.SourceScopedMetadata{TVDB: &api.TVDBMetadata{
			NameEnglish: "Provider Series",
		}},
		EffectiveMetadata: api.EffectiveMetadata{
			Title: "Manual Series", TitleProvenance: api.FactProvenanceManual,
		},
	}
	if got := applyBHDTVTitlePolicy("Provider Series S01 1080p WEB-DL-GRP", meta); got != "Manual Series S01 1080p WEB-DL-GRP" {
		t.Fatalf("manual title name = %q", got)
	}
	meta.EffectiveMetadata.Title = ""
	meta.EffectiveMetadata.TitleProvenance = api.FactProvenanceManualEmpty
	if got := applyBHDTVTitlePolicy("Provider Series S01 1080p WEB-DL-GRP", meta); got != "Provider Series S01 1080p WEB-DL-GRP" {
		t.Fatalf("manual-empty title name = %q", got)
	}
}
