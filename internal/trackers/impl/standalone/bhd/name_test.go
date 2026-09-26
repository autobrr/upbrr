// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
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
	subject.ProviderMetadata = api.SourceScopedMetadata{
		TMDB: &api.TMDBMetadata{Title: "Example Release", Year: 2026},
		IMDB: &api.IMDBMetadata{Year: 2024},
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
	subject.ProviderMetadata = api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Title: "Provider Title", Year: 2027}}
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

func TestBHDStructuredNamePolicyUsesComponentAudioAndCurrentProviders(t *testing.T) {
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
	if got, want := bhdReviewedName(t, subject, nil), "Example Release 2026 1080p WEB-DL Dubbed DD2.0-GRP"; got != want {
		t.Fatalf("component audio markers = %q, want %q", got, want)
	}

	stale := bhdGeneratedSubject(t, api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "WEBDL",
		Title:      "Prepared Title",
		Year:       2026,
		Resolution: "1080p",
		Source:     "Web",
		Tag:        "-GRP",
	})
	stale.Identity.Category = api.CanonicalCategoryMovie
	stale.SourcePath = "current.mkv"
	stale.ProviderMetadata = api.SourceScopedMetadata{SourcePath: "stale.mkv", TMDB: &api.TMDBMetadata{Title: "Stale Title", Year: 2027}}
	if got, want := bhdReviewedName(t, stale, nil), "Prepared Title 2026 1080p WEB-DL-GRP"; got != want {
		t.Fatalf("stale provider title/year = %q, want %q", got, want)
	}
}

func TestBHDMovieTitlesFallsBackFromBlankTMDBToIMDb(t *testing.T) {
	t.Parallel()
	meta := api.UploadSubject{ProviderMetadata: api.SourceScopedMetadata{
		TMDB: &api.TMDBMetadata{Year: 2026},
		IMDB: &api.IMDBMetadata{Title: "IMDb Title", Year: 2024},
	}}
	title, _, year := bhdMovieTitles(meta)
	if title != "IMDb Title" || year != 2024 {
		t.Fatalf("provider fallback = title %q year %d, want IMDb Title 2024", title, year)
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
	subject.ProviderMetadata = api.SourceScopedMetadata{TVDB: &api.TVDBMetadata{NameEnglish: "Example Series"}}
	if got, want := bhdReviewedName(t, subject, nil), "Example Series 2026 S01E02 1080p WEB-DL-GRP"; got != want {
		t.Fatalf("TV year without disambiguation = %q, want %q", got, want)
	}
}

func TestBHDNamingPolicyVersion(t *testing.T) {
	t.Parallel()
	if got := New().ReleaseNamePolicy().ID; got != "standalone/bhd/v7" {
		t.Fatalf("BHD naming policy ID = %q", got)
	}
}

func bhdGeneratedSubject(t *testing.T, request api.ReleaseNameRequest) api.UploadSubject {
	t.Helper()
	generated := metadata.BuildReleaseName(request, api.NopLogger{})
	if generated.GeneratedName == nil {
		t.Fatal("BuildReleaseName did not produce a structured document")
	}
	return api.UploadSubject{
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
