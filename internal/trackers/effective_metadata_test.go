// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestPreferredEffectiveMetadataPreservesManualEmptyValues(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		Release: api.ReleaseInfo{
			Title: "Canonical",
			Year:  2026,
			Genre: "Drama",
		},
		EffectiveMetadata: api.EffectiveMetadata{
			Title:                      "",
			TitleProvenance:            api.FactProvenanceManualEmpty,
			OriginalLanguage:           "",
			OriginalLanguageProvenance: api.FactProvenanceManualEmpty,
			Genres:                     []string{},
			GenresProvenance:           api.FactProvenanceManualEmpty,
		},
	}
	if got := PreferredTitle(meta, "Provider"); got != "" {
		t.Fatalf("title=%q", got)
	}
	if got := PreferredOriginalLanguage(meta, "English"); got != "" {
		t.Fatalf("language=%q", got)
	}
	if got := PreferredGenreText(meta, "Drama"); got != "" {
		t.Fatalf("genres=%q", got)
	}
}
