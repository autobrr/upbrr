// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestANTTVUsesSupportedContentType(t *testing.T) {
	t.Parallel()
	profile := Profile()
	if profile.Rules != nil && profile.Rules.RequireMovieOnly {
		t.Fatal("blanket movie gate rejects supported TV content types")
	}
	for _, test := range []struct {
		name, answer, providerType string
		wantType                   string
	}{
		{
			name:     "selected miniseries",
			answer:   "Miniseries",
			wantType: "Miniseries",
		},
		{
			name:         "known miniseries",
			providerType: "tv mini series",
			wantType:     "Miniseries",
		},
		{
			name:         "known television movie",
			providerType: "tv movie",
			wantType:     "Feature Film",
		},
		{name: "ordinary series still needs a supported type", providerType: "tv series"},
		{name: "unanswered type"},
		{name: "unsupported answer", answer: "ordinary series"},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := api.UploadSubject{
				Identity: api.ExternalIdentity{Category: api.CanonicalCategoryTV},
				ProviderMetadata: api.SourceScopedMetadata{
					IMDB: &api.IMDBMetadata{Type: test.providerType},
				},
				TrackerQuestionnaireAnswers: map[string]map[string]string{"ANT": {"type": test.answer, "tags": "drama"}},
			}
			typeName, _ := resolveType(meta, meta.TrackerQuestionnaireAnswers["ANT"])
			if typeName != test.wantType {
				t.Fatalf("resolved type %q, want %q", typeName, test.wantType)
			}
			subject := api.NewTrackerValidationSubject(meta, "ANT")
			subject.MediaInfoTextReady = true
			failures, err := profile.ValidationPolicy.Check(t.Context(), subject, nil)
			if err != nil {
				t.Fatal(err)
			}
			blocked := slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
				return trackers.RuleFailureBlocksExecution(f, api.WorkflowExecutionModeNormal, false)
			})
			if blocked != (test.wantType == "") {
				t.Fatalf("unexpected type eligibility: %+v", failures)
			}
		})
	}
}

func TestANTTVRequiresTMDBBeforePreparation(t *testing.T) {
	t.Parallel()
	registry := trackers.NewRegistry()
	if err := registry.Register(New()); err != nil {
		t.Fatal(err)
	}
	for _, withTMDB := range []bool{false, true} {
		subject := api.RuleSubject{Identity: api.ExternalIdentity{Category: api.CanonicalCategoryTV}}
		if withTMDB {
			subject.Identity.TMDBID = 100001
			subject.ProviderMetadata.TMDB = &api.TMDBMetadata{TMDBID: 100001}
		}
		failures, err := trackers.EvaluateRulesWithRegistry(t.Context(), registry, "ANT", subject, nil)
		if err != nil {
			t.Fatal(err)
		}
		blocked := slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
			return f.Rule == "require_metadata_id" && f.Disposition == api.RuleDispositionStrict
		})
		if blocked == withTMDB {
			t.Fatalf("TV metadata readiness with TMDB=%v: %+v", withTMDB, failures)
		}
	}
}
