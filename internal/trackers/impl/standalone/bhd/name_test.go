// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"errors"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBHDStructuredNamePolicyProjectsGeneratedFacts(t *testing.T) {
	t.Parallel()
	subject := bhdGeneratedSubject(t, api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "REMUX",
		Title:      "Parsed Release",
		AltTitle:   "Rei no Sakuhin",
		Year:       2025,
		Edition:    "Director's Cut",
		Resolution: "2160p",
		Source:     "BluRay",
		UHD:        "UHD",
		VideoCodec: "HEVC",
		Audio:      "TrueHD 7.1 Atmos",
		Tag:        "-GRP",
	})
	subject.Identity.Category = api.CanonicalCategoryMovie
	subject.Type, subject.Source, subject.VideoCodec, subject.Audio = "REMUX", "BluRay", "HEVC", "TrueHD 7.1 Atmos"
	subject.AlternateTitle = "Rei no Sakuhin"
	subject.ProviderMetadata.TMDB = &api.TMDBMetadata{
		TMDBID: 2,
		Title:  "Other Provider Title",
		Year:   2026,
	}
	subject.ProviderMetadata.IMDB = &api.IMDBMetadata{
		IMDBID: 1,
		Title:  "Example Release",
		Year:   2024,
	}
	subject.HDRFacts = api.HDRFacts{Status: api.HDREvidenceComplete, Formats: []api.HDRFormat{api.HDRFormatSDR}}
	if got, want := bhdReviewedName(t, subject, nil), "Example Release AKA Rei no Sakuhin 2024 Director's Cut 2160p UHD BluRay REMUX SDR HEVC TrueHD Atmos 7.1-GRP"; got != want {
		t.Fatalf("BHD movie name = %q, want %q", got, want)
	}
}

func TestBHDStructuredNamePolicyHonorsManualAndOpaqueAuthority(t *testing.T) {
	t.Parallel()
	subject := bhdGeneratedSubject(t, api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "WEBDL",
		Title:      "Example Release",
		AltTitle:   "Provider Original",
		Year:       2026,
		Resolution: "1080p",
		Source:     "Web",
		Tag:        "-GRP",
	})
	subject.Identity.Category = api.CanonicalCategoryMovie
	subject.EffectiveMetadata = api.EffectiveMetadata{
		Title:                    "Manual Title",
		TitleProvenance:          api.FactProvenanceManual,
		AlternateTitleProvenance: api.FactProvenanceManualEmpty,
	}
	subject.ProviderMetadata.IMDB = &api.IMDBMetadata{
		IMDBID: 1,
		Title:  "Provider Title",
		Year:   2027,
	}
	if got, want := bhdReviewedName(t, subject, nil), "Manual Title 2027 1080p WEB-DL-GRP"; got != want {
		t.Fatalf("manual facts = %q, want %q", got, want)
	}
	override := "Manual BHD Name-GRP"
	if got := bhdReviewedName(t, subject, &override); got != override {
		t.Fatalf("requested opaque name = %q, want %q", got, override)
	}
	opaque := subject
	opaque.ReleaseName = "Exact.P2P.Source.Name.2026.1080p.WEB-DL-GRP"
	if got := bhdReviewedName(t, opaque, nil); got != opaque.ReleaseName {
		t.Fatalf("opaque source name = %q, want %q", got, opaque.ReleaseName)
	}
}

func TestBHDSceneUsesGeneratedNamePolicy(t *testing.T) {
	t.Parallel()
	subject := bhdGeneratedSubject(t, api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "WEBDL",
		Title:      "Example Release",
		Year:       2026,
		Resolution: "1080p",
		Source:     "Web",
		Tag:        "-GRP",
	})
	subject.Identity.Category = api.CanonicalCategoryMovie
	subject.Scene = true
	subject.SceneName = "Different.Scene.Name.2026.1080p.WEB-DL-GRP"
	if got, want := bhdReviewedName(t, subject, nil), "Example Release 2026 1080p WEB-DL-GRP"; got != want {
		t.Fatalf("BHD scene upload name = %q, want %q", got, want)
	}
}

func TestBHDStructuredNamePolicyDVDGroupAndOrder(t *testing.T) {
	t.Parallel()
	subject := bhdGeneratedSubject(t, api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "DISC",
		DiscType:   "DVD",
		Title:      "Example Release",
		Year:       2026,
		Source:     "PAL DVD",
		VideoCodec: "MPEG-2",
		Audio:      "DD 2.0",
	})
	subject.Identity.Category = api.CanonicalCategoryMovie
	subject.Type, subject.DiscType, subject.Source, subject.VideoCodec, subject.Audio = "DISC", "DVD", "PAL DVD", "MPEG-2", "DD 2.0"
	if got, want := bhdReviewedName(t, subject, nil), "Example Release 2026 PAL DVD MPEG-2 DD2.0"; got != want {
		t.Fatalf("BHD DVD name = %q, want %q", got, want)
	}
}

func TestBHDStructuredNamePolicyUsesComponentAudio(t *testing.T) {
	t.Parallel()
	subject := bhdGeneratedSubject(t, api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "WEBDL",
		Title:      "Example Release",
		Year:       2026,
		Resolution: "1080p",
		Source:     "Web",
		Audio:      "Dubbed DD 2.0",
		Tag:        "-GRP",
	})
	subject.Identity.Category = api.CanonicalCategoryMovie
	subject.Audio = "Dubbed DD 2.0"
	subject.LanguageFacts = bhdTestLanguageFacts("Japanese", []string{"English"}, []string{"English"})
	if got, want := bhdReviewedName(t, subject, nil), "Example Release 2026 1080p WEB-DL Dubbed DD2.0-GRP"; got != want {
		t.Fatalf("component audio markers = %q, want %q", got, want)
	}
}

func TestBHDStructuredNamePolicyKeepsTVYearWithoutDisambiguationEvidence(t *testing.T) {
	t.Parallel()
	subject := bhdGeneratedSubject(t, api.ReleaseNameRequest{
		Category:   "TV",
		Type:       "WEBDL",
		Title:      "Example Series",
		Year:       2026,
		SearchYear: "2026",
		Season:     "S01",
		Episode:    "E02",
		Resolution: "1080p",
		Source:     "Web",
		Tag:        "-GRP",
	})
	subject.Identity.Category = api.CanonicalCategoryTV
	subject.ProviderMetadata.TVDB = &api.TVDBMetadata{NameEnglish: "Example Series"}
	if got, want := bhdReviewedName(t, subject, nil), "Example Series 2026 S01E02 1080p WEB-DL-GRP"; got != want {
		t.Fatalf("TV year without disambiguation = %q, want %q", got, want)
	}
}

func TestBHDNamingPolicyVersion(t *testing.T) {
	t.Parallel()
	policy := New().ReleaseNamePolicy()
	if policy.ID != "standalone/bhd/v8" || policy.TitleProvider != api.IdentityProviderIMDB || policy.MovieYearProvider != api.IdentityProviderIMDB {
		t.Fatalf("BHD naming policy = %+v", policy)
	}
}

func bhdGeneratedSubject(t *testing.T, request api.ReleaseNameRequest) api.UploadSubject {
	t.Helper()
	generated := metadata.BuildReleaseName(request, api.NopLogger{})
	if generated.GeneratedName == nil {
		t.Fatal("BuildReleaseName did not produce a structured document")
	}
	return api.UploadSubject{
		SourcePath: "source",
		Identity: api.ExternalIdentity{
			SourcePath: "source",
			Generation: 1,
			Category:   api.CanonicalCategory(request.Category),
			IMDBID:     1,
			TMDBID:     2,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: "source",
			Generation: 1,
			IMDB: &api.IMDBMetadata{
				IMDBID: 1,
				Title:  request.Title,
				Year:   request.Year,
			},
		},
		LanguageFacts:    bhdTestLanguageFacts("English", []string{"English"}, []string{"English"}),
		ReleaseName:      generated.Name,
		ReleaseNameNoTag: generated.NameNoTag,
		GeneratedName:    generated.GeneratedName,
		Tag:              request.Tag,
		Release: api.ReleaseInfo{
			Title:      request.Title,
			Alt:        request.AltTitle,
			Year:       request.Year,
			Resolution: request.Resolution,
			Group:      request.Tag,
		},
	}
}

func bhdReviewedName(t *testing.T, subject api.UploadSubject, requested *string) string {
	t.Helper()
	prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker:             "BHD",
		Meta:                subject,
		RequestedUploadName: requested,
	}, New().ReleaseNamePolicy())
	if failure != nil {
		t.Fatal(failure)
	}
	name, err := prepared.ReviewedUploadName()
	if err != nil {
		t.Fatal(err)
	}
	return name
}

func TestBHDIMDbTitlesAndTVDBYears(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		category api.CanonicalCategory
		include  bool
		want     string
	}{
		{"movie", api.CanonicalCategoryMovie, false, "IMDb Signal 2002 1080p WEB-DL-GRP"},
		{"TV without collision", api.CanonicalCategoryTV, false, "IMDb Signal S01E02 1080p WEB-DL-GRP"},
		{"TV with collision", api.CanonicalCategoryTV, true, "IMDb Signal 1999 S01E02 1080p WEB-DL-GRP"},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := bhdProviderSubject(t, test.category)
			subject.ProviderMetadata.TVDB.NameDisambiguation.IncludeYear = test.include
			if got := bhdReviewedName(t, subject, nil); got != test.want {
				t.Fatalf("name = %q, want %q", got, test.want)
			}
		})
	}
}

func bhdProviderSubject(t *testing.T, category api.CanonicalCategory) api.UploadSubject {
	t.Helper()
	request := api.ReleaseNameRequest{
		Category:   string(category),
		Type:       "WEBDL",
		Title:      "Canonical Signal",
		Year:       1998,
		Resolution: "1080p",
		Source:     "Web",
		Tag:        "-GRP",
	}
	if category == api.CanonicalCategoryTV {
		request.SearchYear, request.Season, request.Episode = "1998", "S01", "E02"
	}
	subject := bhdGeneratedSubject(t, request)
	subject.SourcePath = "source"
	subject.Identity = api.ExternalIdentity{
		SourcePath: "source",
		Generation: 1,
		Category:   category,
		IMDBID:     1,
		TMDBID:     2,
		TVDBID:     3,
	}
	subject.ProviderMetadata = api.SourceScopedMetadata{
		SourcePath: "source",
		Generation: 1,
		IMDB: &api.IMDBMetadata{
			IMDBID: 1,
			Title:  "IMDb Signal",
			Year:   2002,
		},
		TMDB: &api.TMDBMetadata{
			TMDBID:   2,
			Category: string(category),
			Title:    "TMDB Signal",
			Year:     2001,
		},
		TVDB: &api.TVDBMetadata{
			TVDBID:      3,
			NameEnglish: "TVDB Signal",
			NameDisambiguation: api.TVDBNameDisambiguation{
				CanonicalName: "TVDB Signal",
				SeriesYear:    1999,
				Status:        api.MetadataEvidenceStatusComplete,
				Source:        "tvdb-name/v1",
			},
		},
	}
	return subject
}

func TestBHDIMDbTitleRequiresCurrentEvidence(t *testing.T) {
	t.Parallel()
	for _, category := range []api.CanonicalCategory{api.CanonicalCategoryMovie, api.CanonicalCategoryTV} {
		for _, test := range []struct {
			name   string
			mutate func(*api.UploadSubject)
		}{
			{"missing IMDb", func(meta *api.UploadSubject) { meta.ProviderMetadata.IMDB = nil }},
			{"blank IMDb title", func(meta *api.UploadSubject) { meta.ProviderMetadata.IMDB.Title = " " }},
			{"mismatched IMDb ID", func(meta *api.UploadSubject) { meta.ProviderMetadata.IMDB.IMDBID++ }},
			{"missing IMDb ID", func(meta *api.UploadSubject) { meta.Identity.IMDBID = 0 }},
			{"missing provider ID", func(meta *api.UploadSubject) { meta.ProviderMetadata.IMDB.IMDBID = 0 }},
			{"stale source", func(meta *api.UploadSubject) { meta.ProviderMetadata.SourcePath = "old-source" }},
			{"stale identity source", func(meta *api.UploadSubject) { meta.Identity.SourcePath = "old-source" }},
			{"stale generation", func(meta *api.UploadSubject) { meta.ProviderMetadata.Generation++ }},
			{"missing generation", func(meta *api.UploadSubject) { meta.Identity.Generation = 0; meta.ProviderMetadata.Generation = 0 }},
			{"unscoped provider", func(meta *api.UploadSubject) { meta.ProviderMetadata.SourcePath = "" }},
		} {
			t.Run(string(category)+"/"+test.name, func(t *testing.T) {
				subject := bhdProviderSubject(t, category)
				test.mutate(&subject)
				_, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
					Tracker: "BHD",
					Meta:    subject,
				}, New().ReleaseNamePolicy())
				var rule *trackers.NameRuleError
				if !errors.As(failure, &rule) || rule.Role != api.NameRoleTitle || !strings.Contains(rule.Reason, "current matching imdb title") {
					t.Fatalf("invalid IMDb evidence failure = %v", failure)
				}
			})
		}
	}
}

func TestBHDTVYearRequiresEstablishedEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*api.UploadSubject)
	}{
		{"missing TVDB", func(meta *api.UploadSubject) { meta.ProviderMetadata.TVDB = nil }},
		{"missing TVDB ID", func(meta *api.UploadSubject) { meta.Identity.TVDBID = 0 }},
		{"mismatched TVDB ID", func(meta *api.UploadSubject) { meta.ProviderMetadata.TVDB.TVDBID++ }},
		{"missing status", func(meta *api.UploadSubject) { meta.ProviderMetadata.TVDB.NameDisambiguation.Status = "" }},
		{"unavailable evidence", func(meta *api.UploadSubject) {
			meta.ProviderMetadata.TVDB.NameDisambiguation.Status = api.MetadataEvidenceStatusUnavailable
		}},
		{"contradictory evidence", func(meta *api.UploadSubject) {
			meta.ProviderMetadata.TVDB.NameDisambiguation.Status = api.MetadataEvidenceStatusContradictory
		}},
		{"missing evidence source", func(meta *api.UploadSubject) { meta.ProviderMetadata.TVDB.NameDisambiguation.Source = "" }},
		{"missing canonical name", func(meta *api.UploadSubject) { meta.ProviderMetadata.TVDB.NameDisambiguation.CanonicalName = "" }},
		{"unknown required year", func(meta *api.UploadSubject) {
			meta.ProviderMetadata.TVDB.NameDisambiguation.IncludeYear = true
			meta.ProviderMetadata.TVDB.NameDisambiguation.SeriesYear = 0
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := bhdProviderSubject(t, api.CanonicalCategoryTV)
			test.mutate(&subject)
			if got, want := bhdReviewedName(t, subject, nil), "IMDb Signal 1998 S01E02 1080p WEB-DL-GRP"; got != want {
				t.Fatalf("name = %q, want %q", got, want)
			}
		})
	}
}

func TestBHDProviderNamingPreservesManualAuthority(t *testing.T) {
	t.Parallel()
	for _, category := range []api.CanonicalCategory{api.CanonicalCategoryMovie, api.CanonicalCategoryTV} {
		for _, authority := range []string{
			"manual title", "manual year", "manual cleared year", "manual title component",
			"manual year component", "manually omitted year", "opaque requested", "opaque existing",
		} {
			t.Run(string(category)+"/"+authority, func(t *testing.T) {
				subject := bhdProviderSubject(t, category)
				title, year := "IMDb Signal", "2002"
				if category == api.CanonicalCategoryTV {
					year = ""
				}
				var requested *string
				switch authority {
				case "manual title":
					subject.EffectiveMetadata.Title = "Manual Signal"
					subject.EffectiveMetadata.TitleProvenance = api.FactProvenanceManual
					subject.ProviderMetadata.IMDB = nil
					title, year = "Manual Signal", "1998"
				case "manual year":
					subject.EffectiveMetadata.Year = 1998
					subject.EffectiveMetadata.YearProvenance = api.FactProvenanceManual
					year = "1998"
				case "manual cleared year":
					subject.EffectiveMetadata.YearProvenance = api.FactProvenanceManualEmpty
					subject.ProviderMetadata.TVDB.NameDisambiguation.IncludeYear = true
					year = ""
				case "manual title component":
					subject.ProviderMetadata.IMDB = nil
					title, year = "Canonical Signal", "1998"
				case "manual year component":
					year = "1998"
				case "manually omitted year":
					subject.ProviderMetadata.TVDB.NameDisambiguation.IncludeYear = true
					year = ""
				case "opaque requested":
					name := "Exact Manual Name-GRP"
					requested = &name
				case "opaque existing":
					subject.ProviderMetadata = api.SourceScopedMetadata{}
				}
				for index := range subject.GeneratedName.Components {
					component := &subject.GeneratedName.Components[index]
					if component.Role == api.NameRoleTitle && authority == "manual title component" {
						component.Manual = true
					}
					if component.Role == api.NameRoleYear {
						if authority == "manual year component" || authority == "manually omitted year" {
							component.Manual = true
						}
						if authority == "manual cleared year" || authority == "manually omitted year" {
							component.Present = false
						}
					}
				}
				subject.ReleaseName = subject.GeneratedName.Render().Name
				want := title
				if year != "" {
					want += " " + year
				}
				if category == api.CanonicalCategoryTV {
					want += " S01E02"
				}
				want += " 1080p WEB-DL-GRP"
				if requested != nil {
					want = *requested
				}
				if authority == "opaque existing" {
					want = "Exact.Source.Name-GRP"
					subject.ReleaseName = want
				}
				if got := bhdReviewedName(t, subject, requested); got != want {
					t.Fatalf("name = %q, want %q", got, want)
				}
			})
		}
	}
}

func TestBHDProviderTitleNormalizesAlternateTitle(t *testing.T) {
	t.Parallel()
	for _, manual := range []bool{false, true} {
		subject := bhdProviderSubject(t, api.CanonicalCategoryMovie)
		subject.AlternateTitle = "AKA IMDb Signal"
		for index := range subject.GeneratedName.Components {
			component := &subject.GeneratedName.Components[index]
			if component.Role == api.NameRoleAlternateTitle {
				component.Value, component.Present, component.Manual = subject.AlternateTitle, true, manual
			}
		}
		subject.ReleaseName = subject.GeneratedName.Render().Name
		if manual {
			subject.EffectiveMetadata.AlternateTitle = subject.AlternateTitle
			subject.EffectiveMetadata.AlternateTitleProvenance = api.FactProvenanceManual
		}
		want := "IMDb Signal 2002 1080p WEB-DL-GRP"
		if manual {
			want = "IMDb Signal AKA IMDb Signal 2002 1080p WEB-DL-GRP"
		}
		if got := bhdReviewedName(t, subject, nil); got != want {
			t.Fatalf("manual=%t: name = %q, want %q", manual, got, want)
		}
	}
}

func TestBHDMissingIMDbMovieYearKeepsCanonicalYear(t *testing.T) {
	t.Parallel()
	subject := bhdProviderSubject(t, api.CanonicalCategoryMovie)
	subject.ProviderMetadata.IMDB.Year = 0
	if got, want := bhdReviewedName(t, subject, nil), "IMDb Signal 1998 1080p WEB-DL-GRP"; got != want {
		t.Fatalf("name = %q, want %q", got, want)
	}
}
