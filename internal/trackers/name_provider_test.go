// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestTitleProviderRequiresExactPreparedIdentity(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*api.UploadSubject)
	}{
		{name: "missing provider", change: func(s *api.UploadSubject) { s.ProviderMetadata.TMDB = nil }},
		{name: "empty title", change: func(s *api.UploadSubject) { s.ProviderMetadata.TMDB.Title = " " }},
		{name: "wrong source", change: func(s *api.UploadSubject) { s.ProviderMetadata.SourcePath += "-other" }},
		{name: "unscoped provider", change: func(s *api.UploadSubject) { s.ProviderMetadata.SourcePath = "" }},
		{name: "unscoped identity", change: func(s *api.UploadSubject) { s.Identity.SourcePath = "" }},
		{name: "old generation", change: func(s *api.UploadSubject) { s.ProviderMetadata.Generation-- }},
		{name: "legacy generation", change: func(s *api.UploadSubject) { s.Identity.Generation = 0; s.ProviderMetadata.Generation = 0 }},
		{name: "wrong provider ID", change: func(s *api.UploadSubject) { s.ProviderMetadata.TMDB.TMDBID++ }},
		{name: "missing canonical ID", change: func(s *api.UploadSubject) { s.Identity.TMDBID = 0 }},
		{name: "same ID wrong category", change: func(s *api.UploadSubject) { s.ProviderMetadata.TMDB.Category = "TV" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := titleProviderSubject(t)
			test.change(&subject)
			binding := WithTitleProvider(StructuredReleaseNamePolicy("test/provider/v1", StructuredNamePolicy{}), api.IdentityProviderTMDB)
			_, err := resolveReleaseNames(PreparationInput{Meta: subject}, binding)
			var rule *NameRuleError
			if !errors.As(err, &rule) || rule.Role != api.NameRoleTitle {
				t.Fatalf("missing current title did not fail explicitly: %v", err)
			}
		})
	}
}

func TestLegacyTitleProviderUsesDetachedFinalizedSubject(t *testing.T) {
	subject := titleProviderSubject(t)
	before := subject.GeneratedName.Clone()
	var seen api.UploadSubject
	binding := WithTitleProvider(SubjectReleaseNamePolicy("test/legacy-provider/v1", func(meta api.UploadSubject, _ config.TrackerConfig) string {
		seen = meta
		return meta.ReleaseName
	}), api.IdentityProviderTMDB)
	binding = WithEpisodeTitleMode(binding, api.EpisodeTitleModeOmit)
	resolved, err := resolveReleaseNames(PreparationInput{Meta: subject}, binding)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(resolved.Upload, "TMDB Primary ") || strings.Contains(resolved.Upload, "The Uncut Version") ||
		seen.Release.Title != "TMDB Primary" || seen.EffectiveMetadata.Title != "TMDB Primary" {
		t.Fatalf("legacy resolver received inconsistent provider projection: %q / %q / %q", resolved.Upload, seen.Release.Title, seen.EffectiveMetadata.Title)
	}
	if !reflect.DeepEqual(subject.GeneratedName, before) || subject.Release.Title != "Finalized Primary" {
		t.Fatal("legacy provider changed canonical facts or document")
	}
	if seen.GeneratedName == subject.GeneratedName || !strings.Contains(seen.GeneratedName.Render().Name, "The Uncut Version") {
		t.Fatal("projection document aliased canonical data or was mutated by variant rendering")
	}
}

func TestTitleProviderPreservesManualFactsWithoutProvider(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		subject := titleProviderSubject(t)
		subject.ProviderMetadata = api.SourceScopedMetadata{}
		subject.EffectiveMetadata.Title = "Manual Primary"
		subject.EffectiveMetadata.TitleProvenance = api.FactProvenanceManual
		binding := StructuredReleaseNamePolicy("test/manual-provider/v1", StructuredNamePolicy{})
		if legacy {
			binding = SubjectReleaseNamePolicy("test/manual-provider/v1", func(s api.UploadSubject, _ config.TrackerConfig) string { return s.ReleaseName })
		}
		resolved, err := resolveReleaseNames(PreparationInput{Meta: subject}, WithTitleProvider(binding, api.IdentityProviderTMDB))
		if err != nil || !strings.HasPrefix(resolved.Upload, "Manual Primary ") {
			t.Fatalf("manual fact authority lost (legacy=%t): %q, %v", legacy, resolved.Upload, err)
		}
	}
}

func TestTitleProviderChangeInvalidatesEqualReviewedNames(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		subject := titleProviderSubject(t)
		subject.ProviderMetadata.IMDB.Title = subject.ProviderMetadata.TMDB.Title
		binding := StructuredReleaseNamePolicy("test/provider-review/v1", StructuredNamePolicy{})
		if legacy {
			binding = CanonicalReleaseNamePolicy()
		}
		binding = WithTitleProvider(binding, api.IdentityProviderTMDB)
		input, failure := PrepareInputWithReleaseNamePolicy(PreparationInput{Tracker: "EXAMPLE", Meta: subject}, binding)
		if failure != nil {
			t.Fatal(failure)
		}
		before, err := descriptorPolicyFingerprint(Descriptor{ReleaseNamePolicy: binding})
		if err != nil {
			t.Fatal(err)
		}
		binding.TitleProvider = api.IdentityProviderIMDB
		after, err := descriptorPolicyFingerprint(Descriptor{ReleaseNamePolicy: binding})
		if err != nil {
			t.Fatal(err)
		}
		if before == after {
			t.Fatal("provider policy change reused the descriptor fingerprint")
		}
		if _, failure := PrepareInputWithReleaseNamePolicy(input, binding); failure == nil {
			t.Fatalf("equal title output retained review after provider change (legacy=%t)", legacy)
		}
	}
}

func titleProviderSubject(t *testing.T) api.UploadSubject {
	t.Helper()
	subject := structuredSubject()
	subject.SourcePath = filepath.Join(t.TempDir(), "Example.mkv")
	subject.Identity = api.ExternalIdentity{
		SourcePath: subject.SourcePath,
		Generation: 2,
		Category:   api.CanonicalCategoryMovie,
		TMDBID:     123,
		IMDBID:     456,
	}
	subject.Release.Title = "Finalized Primary"
	subject.ProviderMetadata = api.SourceScopedMetadata{
		SourcePath: subject.SourcePath,
		Generation: subject.Identity.Generation,
		TMDB: &api.TMDBMetadata{
			TMDBID:   123,
			Category: "MOVIE",
			Title:    "TMDB Primary",
			Year:     2025,
		},
		IMDB: &api.IMDBMetadata{
			IMDBID: 456,
			Title:  "IMDb Primary",
			Year:   2024,
		},
	}
	return subject
}

func TestTitleProviderTVParentheticalYear(t *testing.T) {
	for _, test := range []struct {
		name        string
		title       string
		category    api.CanonicalCategory
		hideYear    bool
		omitYear    bool
		manual      bool
		defaultYear string
		want        string
	}{
		{
			name:     "matching known year",
			title:    "Provider Show (2026)",
			category: api.CanonicalCategoryTV,
			want:     "Provider Show",
		},
		{
			name:     "different year",
			title:    "Provider Show (2000)",
			category: api.CanonicalCategoryTV,
			want:     "Provider Show (2000)",
		},
		{
			name:     "hidden known year",
			title:    "Provider Show (2026)",
			category: api.CanonicalCategoryTV,
			hideYear: true,
			want:     "Provider Show",
		},
		{
			name:     "missing year",
			title:    "Provider Show (2026)",
			category: api.CanonicalCategoryTV,
			omitYear: true,
			want:     "Provider Show (2026)",
		},
		{
			name:     "movie title preserved",
			title:    "Provider Movie (2026)",
			category: api.CanonicalCategoryMovie,
			want:     "Provider Movie (2026)",
		},
		{
			name:     "manual title preserved",
			title:    "Provider Show (2026)",
			category: api.CanonicalCategoryTV,
			manual:   true,
			want:     "Provider Show (2026)",
		},
		{
			name:        "tracker year qualifier",
			title:       "Provider Show (2027)",
			category:    api.CanonicalCategoryTV,
			defaultYear: "2027",
			want:        "Provider Show",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := titleProviderSubject(t)
			subject.Identity.Category = test.category
			subject.ProviderMetadata.TMDB.Category = string(test.category)
			subject.ProviderMetadata.TMDB.Title = test.title
			for i := range subject.GeneratedName.Components {
				component := &subject.GeneratedName.Components[i]
				if component.Role == api.NameRoleTitle && test.manual {
					component.Value = test.title
					component.Manual = true
				}
				if component.Role == api.NameRoleYear {
					if test.hideYear {
						component.Present = false
					}
					if test.omitYear {
						component.Value = ""
						component.AvailableValue = ""
						component.Present = false
					}
				}
			}
			subject.ReleaseName = subject.GeneratedName.Render().Name
			policy := StructuredNamePolicy{}
			if test.defaultYear != "" {
				policy.Defaults = func(e *NameEditor, _ api.UploadSubject, _ config.TrackerConfig) error {
					return e.Set(api.NameRoleYear, test.defaultYear)
				}
			}
			binding := WithTitleProvider(StructuredReleaseNamePolicy("test/provider-format/v1", policy), api.IdentityProviderTMDB)
			resolved, err := resolveReleaseNames(PreparationInput{Meta: subject}, binding)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(resolved.Upload, test.want+" ") || test.want == "Provider Show" && strings.HasPrefix(resolved.Upload, "Provider Show (") {
				t.Fatalf("title formatting = %q, want title %q", resolved.Upload, test.want)
			}
		})
	}
}
