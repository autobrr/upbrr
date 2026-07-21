// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveCategoryUsesCanonicalIdentityDespiteEmptyTVMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		metadata api.SourceScopedMetadata
	}{
		{name: "TVDB", metadata: api.SourceScopedMetadata{TVDB: &api.TVDBMetadata{}}},
		{name: "TVmaze", metadata: api.SourceScopedMetadata{TVmaze: &api.TVmazeMetadata{}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			meta := api.RuleSubject{
				Identity:         api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
				ProviderMetadata: tt.metadata,
			}
			if got := resolveCategory(meta); got != "movie" {
				t.Fatalf("expected canonical movie category, got %q", got)
			}
		})
	}
}

func TestConfiguredStrictRuleDispositions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		rule    string
		rules   RuleSet
		subject api.RuleSubject
	}{
		{
			name:  "unique ID",
			rule:  "require_unique_id",
			rules: RuleSet{RequireUniqueID: true},
			subject: api.RuleSubject{Assessments: api.ReleaseAssessments{
				MediaInfoUniqueID: api.UniqueIDStatusMissing,
			}},
		},
		{
			name:  "MediaInfo settings",
			rule:  "require_valid_mi_setting",
			rules: RuleSet{RequireValidMISetting: true},
			subject: api.RuleSubject{Assessments: api.ReleaseAssessments{
				MediaInfoEncodeSettings: api.EncodeSettingsStatusMissing,
			}},
		},
		{
			name:    "disc only",
			rule:    "require_disc_only",
			rules:   RuleSet{RequireDiscOnly: true},
			subject: api.RuleSubject{},
		},
		{
			name:  "movie only",
			rule:  "require_movie_only",
			rules: RuleSet{RequireMovieOnly: true},
			subject: api.RuleSubject{Identity: api.ExternalIdentity{
				Category: api.CanonicalCategoryTV,
			}},
		},
		{
			name:  "movie unless TV pack",
			rule:  "require_movie_only",
			rules: RuleSet{RequireMovieUnlessTVPack: true},
			subject: api.RuleSubject{Identity: api.ExternalIdentity{
				Category: api.CanonicalCategoryTV,
			}},
		},
		{
			name:  "TV only",
			rule:  "require_tv_only",
			rules: RuleSet{RequireTVOnly: true},
			subject: api.RuleSubject{Identity: api.ExternalIdentity{
				Category: api.CanonicalCategoryMovie,
			}},
		},
		{
			name:  "HEVC",
			rule:  "require_hevc",
			rules: RuleSet{RequireHEVCForTypes: []string{"ENCODE"}},
			subject: api.RuleSubject{
				Type:       "ENCODE",
				VideoCodec: "AVC",
			},
		},
		{
			name:    "DVDRip",
			rule:    "block_dvdrip",
			rules:   RuleSet{BlockDVDRip: true},
			subject: api.RuleSubject{Type: "DVDRIP"},
		},
		{
			name:    "hardcoded subtitles",
			rule:    "block_hardcoded_subs",
			rules:   RuleSet{BlockHardcodedSubs: true},
			subject: api.RuleSubject{ReleaseName: "Example.Release.2026.HARDSUB-GRP"},
		},
		{
			name:  "single-file folder",
			rule:  "block_single_file_folder",
			rules: RuleSet{BlockSingleFileFolder: true},
			subject: api.RuleSubject{
				SourcePath: "Example.Release.2026",
				FileList:   []string{"Example.Release.2026/video.mkv"},
			},
		},
		{
			name:  "scene NFO",
			rule:  "require_scene_nfo",
			rules: RuleSet{RequireSceneNFO: true},
			subject: api.RuleSubject{
				Scene: true,
			},
		},
		{
			name:    "audio language metadata",
			rule:    "require_audio_languages",
			rules:   RuleSet{RequireAudioLanguages: true},
			subject: api.RuleSubject{},
		},
		{
			name: "language requirement",
			rule: "language_rule",
			rules: RuleSet{Language: &LanguageRule{
				Languages:    []string{"english"},
				RequireAudio: true,
			}},
			subject: api.RuleSubject{AudioLanguages: []string{"french"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			registry := NewRegistry()
			if err := registry.RegisterDescriptor(Descriptor{
				Name:       "STRICT",
				Definition: stubDefinition{name: "STRICT"},
				Rules:      &test.rules,
			}); err != nil {
				t.Fatalf("register rule: %v", err)
			}
			failures, err := evaluateRules(context.Background(), registry, "STRICT", test.subject, nil)
			if err != nil {
				t.Fatalf("evaluate rule: %v", err)
			}
			for _, failure := range failures {
				if failure.Rule == test.rule {
					if failure.Disposition != api.RuleDispositionStrict {
						t.Fatalf("%s disposition = %q, want strict", test.rule, failure.Disposition)
					}
					return
				}
			}
			t.Fatalf("missing %s failure: %#v", test.rule, failures)
		})
	}
}

func TestMetadataCategoryDispositionUsesStrictestRequirement(t *testing.T) {
	t.Parallel()

	policy := TrackerMetadataPolicy{Requirements: []MetadataRequirement{
		{
			Scope:       MetadataScopeAny,
			AnyOf:       []MetadataField{MetadataFieldPoster},
			Disposition: api.RuleDispositionWaivable,
		},
		{
			Scope:       MetadataScopeMovie,
			AnyOf:       []MetadataField{MetadataFieldTMDB},
			Disposition: api.RuleDispositionStrict,
		},
	}}
	if got := metadataCategoryDisposition(policy); got != api.RuleDispositionStrict {
		t.Fatalf("category disposition = %q, want strict", got)
	}
}
