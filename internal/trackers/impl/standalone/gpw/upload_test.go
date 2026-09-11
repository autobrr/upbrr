// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gpw

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildFieldsPersonalReleaseAndExclusiveFlags(t *testing.T) {
	t.Parallel()

	base := api.UploadSubject{PersonalRelease: true}

	nonDisc := buildFields(trackers.PreparationInput{Meta: base}, config.TrackerConfig{Exclusive: true}, "description", "group", nil)
	if nonDisc["diy"] != "on" {
		t.Fatalf("expected non-disc personal release to use diy flag, got %#v", nonDisc)
	}
	if _, ok := nonDisc["self_rip"]; ok {
		t.Fatalf("did not expect legacy self_rip flag, got %#v", nonDisc)
	}
	if nonDisc["jinzhuan"] != "on" {
		t.Fatalf("expected exclusive flag to set jinzhuan, got %#v", nonDisc)
	}

	discMeta := base
	discMeta.DiscType = "BDMV"
	disc := buildFields(trackers.PreparationInput{Meta: discMeta}, config.TrackerConfig{}, "description", "group", nil)
	if disc["buy"] != "on" {
		t.Fatalf("expected disc personal release to use buy flag, got %#v", disc)
	}
	if _, ok := disc["diy"]; ok {
		t.Fatalf("did not expect diy for disc personal release, got %#v", disc)
	}
}

func TestBuildFieldsNewGroupYearPreservesManualAuthority(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		meta api.UploadSubject
		want string
	}{
		{
			name: "manual empty suppresses provider year",
			meta: api.UploadSubject{
				EffectiveMetadata: api.EffectiveMetadata{YearProvenance: api.FactProvenanceManualEmpty},
				ProviderMetadata:  api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Year: 2026}},
			},
			want: "",
		},
		{
			name: "manual year overrides provider year",
			meta: api.UploadSubject{
				EffectiveMetadata: api.EffectiveMetadata{Year: 2024, YearProvenance: api.FactProvenanceManual},
				ProviderMetadata:  api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Year: 2026}},
			},
			want: "2024",
		},
		{
			name: "provider year remains available",
			meta: api.UploadSubject{ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Year: 2026}}},
			want: "2026",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fields := buildFields(trackers.PreparationInput{Meta: tc.meta}, config.TrackerConfig{}, "description", "", nil)
			if got, ok := fields["year"]; !ok || got != tc.want {
				t.Fatalf("year = %q, present = %t, want %q", got, ok, tc.want)
			}
		})
	}

	existingGroup := buildFields(trackers.PreparationInput{Meta: api.UploadSubject{ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Year: 2026}}}}, config.TrackerConfig{}, "description", "group", nil)
	if _, ok := existingGroup["year"]; ok {
		t.Fatalf("existing group must omit year, got %#v", existingGroup)
	}
}

func TestResolveTagsPreservesAutomaticTMDBPresenceAndManualFacts(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{Release: api.ReleaseInfo{Genre: "Release"}, ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Genres: ""}}}
	if got := resolveTags(meta); got != "" {
		t.Fatalf("blank TMDB tags = %q", got)
	}
	meta.ProviderMetadata.TMDB = nil
	if got := resolveTags(meta); got != "release" {
		t.Fatalf("release tags = %q", got)
	}
	meta.EffectiveMetadata = api.EffectiveMetadata{Genres: []string{"Manual Genre"}, GenresProvenance: api.FactProvenanceManual}
	if got := resolveTags(meta); got != "manual genre" {
		t.Fatalf("manual tags = %q", got)
	}
}
