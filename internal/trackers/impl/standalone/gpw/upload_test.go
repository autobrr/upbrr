// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gpw

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildFields(req trackers.PreparationInput, cfg config.TrackerConfig, groupID string, answers map[string]string) map[string]string {
	req.TrackerConfig = cfg
	return previewFields(buildForm(req, "description", groupID, answers))
}

func TestBuildFieldsPersonalReleaseAndExclusiveFlags(t *testing.T) {
	t.Parallel()

	base := api.UploadSubject{PersonalRelease: true}

	nonDisc := buildFields(trackers.PreparationInput{Meta: base}, config.TrackerConfig{Exclusive: true}, "group", nil)
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
	disc := buildFields(trackers.PreparationInput{Meta: discMeta, Runtime: trackers.PreparationRuntime{DBPath: filepath.Join(t.TempDir(), "db.sqlite")}}, config.TrackerConfig{}, "group", nil)
	if disc["buy"] != "on" {
		t.Fatalf("expected disc personal release to use buy flag, got %#v", disc)
	}
	if _, ok := disc["diy"]; ok {
		t.Fatalf("did not expect diy for disc personal release, got %#v", disc)
	}
}

func TestBuildFieldsPreservesStructuredEditionParts(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		legacy     string
		structured api.UploadSubject
	}{
		{"mixed", "Extended Collector's Open Matte", api.UploadSubject{
			Cut:          "Extended",
			Edition:      "Collector's",
			Presentation: "Open Matte",
		}},
		{"cut only", "Extended", api.UploadSubject{Cut: "Extended"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fields := buildFields(trackers.PreparationInput{Meta: test.structured}, config.TrackerConfig{}, "group", nil)
			if fields["movie_edition_information"] != "on" || fields["remaster_title"] != "extended_edition" {
				t.Fatal("canonical cut was not mapped")
			}
			wantCustom := ""
			if test.name == "mixed" {
				wantCustom = "Collector's / Open Matte"
			}
			if fields["remaster_custom_title"] != wantCustom {
				t.Fatalf("custom edition title = %q, want %q", fields["remaster_custom_title"], wantCustom)
			}
			legacy := buildFields(trackers.PreparationInput{Meta: api.UploadSubject{Edition: test.legacy}}, config.TrackerConfig{}, "group", nil)
			if legacy["remaster_title"] != "" || legacy["remaster_custom_title"] != test.legacy {
				t.Fatal("opaque edition was reclassified or lost")
			}
		})
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

			fields := buildFields(trackers.PreparationInput{Meta: tc.meta}, config.TrackerConfig{}, "", nil)
			if got, ok := fields["year"]; !ok || got != tc.want {
				t.Fatalf("year = %q, present = %t, want %q", got, ok, tc.want)
			}
		})
	}

	existingGroup := buildFields(trackers.PreparationInput{Meta: api.UploadSubject{ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Year: 2026}}}}, config.TrackerConfig{}, "group", nil)
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

func TestUnanswerableMetadataPrerequisitesDoNotRequestQuestionnaire(t *testing.T) {
	for _, test := range []struct {
		name string
		meta api.UploadSubject
	}{
		{"missing year", api.UploadSubject{}},
		{"cleared year", api.UploadSubject{EffectiveMetadata: api.EffectiveMetadata{YearProvenance: api.FactProvenanceManualEmpty}, ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Year: 2026}}}},
		{"unmapped subtitles", api.UploadSubject{EffectiveMetadata: api.EffectiveMetadata{Year: 2026, YearProvenance: api.FactProvenanceManual}, SubtitleLanguages: []string{"zh"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := test.meta
			meta.TorrentPath = filepath.Join(t.TempDir(), "example.torrent")
			if err := os.WriteFile(meta.TorrentPath, []byte("synthetic torrent"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := prepareUploadState(t.Context(), trackers.PreparationInput{
				Meta:          meta,
				TrackerConfig: config.TrackerConfig{APIKey: "synthetic-key"},
				Logger:        api.NopLogger{},
			})
			var failure *trackers.PreparationFailure
			if !errors.As(err, &failure) || failure.Code() != "missing_metadata" {
				t.Fatalf("unanswerable prerequisite was routed to questionnaire: %v", err)
			}
		})
	}
}
