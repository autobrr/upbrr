// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"context"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestCinemaZMissingTitleExplainsProjectionFailure(t *testing.T) {
	t.Parallel()
	subject := azFamilyGeneratedSubject(t, api.ReleaseNameRequest{
		Category: "MOVIE",
		Type:     "ENCODE",
		Title:    "Example Film",
	})
	subject.EffectiveMetadata.OriginalTitleProvenance = api.FactProvenanceManualEmpty
	registry := trackers.NewRegistry()
	if err := registry.Register(New("CZ")); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint("missing-title")
	if err != nil {
		t.Fatal(err)
	}
	projection, failure := registry.ProjectRelease(context.Background(), trackers.PreparationInput{
		Tracker: "CZ",
		Meta:    subject,
	}, fingerprint, fingerprint, fingerprint)
	if failure == nil || failure.Code() != "name_rule_unsatisfied" ||
		!strings.Contains(failure.Message(), "Latin-safe manual original title") || projection.UploadReady {
		t.Fatalf("missing CinemaZ title projection=%+v failure=%v", projection, failure)
	}
}

func TestAZFamilyStructuredReleaseNamePolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		site      string
		request   api.ReleaseNameRequest
		configure func(*api.UploadSubject)
		want      string
	}{
		{
			name: "AZ uses provider title and episode ordering",
			site: "AZ",
			request: api.ReleaseNameRequest{
				Category:    "TV",
				Type:        "WEBDL",
				Title:       "Localized Show",
				AltTitle:    "Original Show",
				Year:        2026,
				SearchYear:  "2026",
				Season:      "S01",
				Episode:     "E02",
				Resolution:  "1080p",
				Audio:       "Dubbed DD 5.1 Dual-Audio",
				VideoEncode: "H.265",
				Tag:         "-GRP",
			},
			configure: func(subject *api.UploadSubject) {
				subject.ProviderMetadata.TMDB = &api.TMDBMetadata{Title: "English Show"}
			},
			want: "English Show S01E02 1080p WEB-DL DD 5.1 H.265-GRP",
		},
		{
			name: "CinemaZ uses English AKA and BDMV facts",
			site: "CZ",
			request: api.ReleaseNameRequest{
				Category:   "MOVIE",
				Type:       "DISC",
				DiscType:   "BDMV",
				Title:      "Localized Film",
				AltTitle:   "Original Film",
				Year:       2026,
				Resolution: "2160p",
				Region:     "USA",
				UHD:        "UHD",
				Source:     "BluRay",
				HDR:        "HDR10+",
				VideoCodec: "HEVC",
				Audio:      "DTS-HD MA 2.0",
				Tag:        "-NOGRP",
			},
			configure: func(subject *api.UploadSubject) {
				subject.ProviderMetadata.IMDB = &api.IMDBMetadata{AKA: "Original Film", Akas: []api.IMDBAKA{{
					Title:    "English Film",
					Country:  "Otherland",
					Language: "English",
				}}}
			},
			want: "English Film 2026 2160p USA UHD Blu-ray RAW HDR10+ HEVC DTS-HD MA 2.0-NoGroup",
		},
		{
			name: "CinemaZ DVD uses structured resolution size and codec",
			site: "CZ",
			request: api.ReleaseNameRequest{
				Category:   "MOVIE",
				Type:       "DISC",
				DiscType:   "DVD",
				Title:      "Localized Film",
				AltTitle:   "Original Film",
				Year:       2026,
				Resolution: "480p",
				Region:     "R1",
				Source:     "DVD",
				DVDSize:    "DVD9",
				VideoCodec: "MPEG-2",
				Audio:      "DD 5.1",
				Tag:        "-GRP",
			},
			configure: func(subject *api.UploadSubject) {
				subject.ProviderMetadata.IMDB = &api.IMDBMetadata{AKA: "Original Film"}
			},
			want: "Original Film 2026 480p DVD9 DD 5.1 MPEG2-GRP",
		},
		{
			name: "PHD DVD uses structured resolution and codec ordering",
			site: "PHD",
			request: api.ReleaseNameRequest{
				Category:   "MOVIE",
				Type:       "DISC",
				DiscType:   "DVD",
				Title:      "Example Film",
				AltTitle:   "Original Film",
				Year:       2026,
				Resolution: "480p",
				Region:     "R1",
				Source:     "DVD",
				DVDSize:    "DVD9",
				VideoCodec: "MPEG-2",
				Audio:      "DD 5.1",
				Tag:        "-NOGRP",
			},
			want: "Example Film 2026 DVD9 480p DD 5.1 MPEG-2-NOGROUP",
		},
		{
			name: "PHD encode uses semantic encoder role",
			site: "PHD",
			request: api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "ENCODE",
				Title:       "Example Film",
				Year:        2026,
				Resolution:  "1080p",
				Source:      "BluRay",
				VideoEncode: "H.265",
				Audio:       "DD 5.1",
				Tag:         "-GRP",
			},
			configure: func(subject *api.UploadSubject) { subject.HasEncodeSettings = true },
			want:      "Example Film 2026 1080p BluRay DD 5.1 x265-GRP",
		},
		{
			name: "PHD normalizes H264 inside generated codec compounds",
			site: "PHD",
			request: api.ReleaseNameRequest{
				Category:   "MOVIE",
				Type:       "WEBDL",
				Title:      "Example Film",
				Year:       2026,
				Resolution: "1080p",
				Source:     "WEB-DL",
				VideoCodec: "Hi10P H.264",
				Audio:      "DD 5.1",
				Tag:        "-GRP",
			},
			configure: func(subject *api.UploadSubject) { subject.HasEncodeSettings = true },
			want:      "Example Film 2026 1080p WEB-DL DD 5.1 Hi10P x264-GRP",
		},
		{
			name: "PHD normalizes H265 inside generated encode compounds",
			site: "PHD",
			request: api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "ENCODE",
				Title:       "Example Film",
				Year:        2026,
				Resolution:  "1080p",
				Source:      "BluRay",
				VideoEncode: "Hi10P H.265",
				Audio:       "DD 5.1",
				Tag:         "-GRP",
			},
			configure: func(subject *api.UploadSubject) { subject.HasEncodeSettings = true },
			want:      "Example Film 2026 1080p BluRay DD 5.1 Hi10P x265-GRP",
		},
		{
			name: "CinemaZ omits an emptied automatic edition",
			site: "CZ",
			request: api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "WEBDL",
				Title:       "Localized Film",
				AltTitle:    "Original Film",
				Year:        2026,
				Edition:     "LIMITED Criterion Collection 25th Anniversary Edition 4K",
				Resolution:  "1080p",
				Source:      "WEB-DL",
				Audio:       "DD 5.1",
				VideoEncode: "H.265",
				Tag:         "-GRP",
			},
			want: "Localized Film 2026 1080p WEB-DL DD 5.1 H.265-GRP",
		},
		{
			name: "PHD omits an emptied automatic edition",
			site: "PHD",
			request: api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "WEBDL",
				Title:       "Example Film",
				Year:        2026,
				Edition:     "LIMITED Criterion Collection 25th Anniversary Edition",
				Resolution:  "1080p",
				Source:      "WEB-DL",
				Audio:       "DD 5.1",
				VideoEncode: "H.265",
				Tag:         "-GRP",
			},
			want: "Example Film 2026 1080p WEB-DL DD 5.1 H.265-GRP",
		},
		{
			name: "CinemaZ normalizes only the edition component",
			site: "CZ",
			request: api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "WEBDL",
				Title:       "Director's Cut Limited Story",
				AltTitle:    "Original",
				Year:        2026,
				Edition:     "LIMITED Criterion Collection 25th Anniversary Edition Extended Cut Director's Cut Theatrical Cut 4K restored",
				Resolution:  "1080p",
				Source:      "WEB-DL",
				Audio:       "DD 5.1",
				VideoEncode: "H.265",
				Tag:         "-GRP",
			},
			configure: func(subject *api.UploadSubject) {
				subject.ProviderMetadata.IMDB = &api.IMDBMetadata{AKA: "Director's Cut Limited Story"}
			},
			want: "Director's Cut Limited Story 2026 EXT DC TC RESTORED 1080p WEB-DL DD 5.1 H.265-GRP",
		},
		{
			name: "CinemaZ does not invent DVD tokens without facts",
			site: "CZ",
			request: api.ReleaseNameRequest{
				Category:   "MOVIE",
				Type:       "DISC",
				DiscType:   "DVD",
				Title:      "Localized Film",
				AltTitle:   "Original Film",
				Year:       2026,
				Resolution: "480p",
				DVDSize:    "DVD9",
				Audio:      "DD 5.1",
				Tag:        "-GRP",
			},
			configure: func(subject *api.UploadSubject) {
				subject.ProviderMetadata.IMDB = &api.IMDBMetadata{AKA: "Original Film"}
			},
			want: "Original Film 2026 480p DVD9 DD 5.1-GRP",
		},
		{
			name: "CinemaZ supplies no-group suffix for an untagged generated name",
			site: "CZ",
			request: api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "WEBDL",
				Title:       "Localized Film",
				AltTitle:    "Original Film",
				Year:        2026,
				Resolution:  "1080p",
				Source:      "WEB-DL",
				Audio:       "DD 5.1",
				VideoEncode: "H.265",
			},
			configure: func(subject *api.UploadSubject) {
				subject.ProviderMetadata.IMDB = &api.IMDBMetadata{AKA: "Original Film"}
			},
			want: "Original Film 2026 1080p WEB-DL DD 5.1 H.265-NoGroup",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			subject := azFamilyGeneratedSubject(t, test.request)
			if test.configure != nil {
				test.configure(&subject)
			}
			if got := azFamilyReviewedName(t, test.site, subject, nil); got != test.want {
				t.Fatalf("reviewed name = %q, want %q", got, test.want)
			}
		})
	}
}

func TestAZFamilyStructuredPolicyPreservesRequestedOpaqueAndManualNames(t *testing.T) {
	t.Parallel()
	request := api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "DISC",
		DiscType:   "DVD",
		Title:      "Example Film",
		Year:       2026,
		Resolution: "480p",
		Source:     "DVD",
		DVDSize:    "DVD9",
		VideoCodec: "MPEG-2",
		Audio:      "DD 5.1",
		Tag:        "-GRP",
	}
	subject := azFamilyGeneratedSubject(t, request)
	requested := "Manual AZ Name-GRP"
	if got := azFamilyReviewedName(t, "AZ", subject, &requested); got != requested {
		t.Fatalf("requested name = %q, want %q", got, requested)
	}

	opaque := subject
	opaque.GeneratedName = nil
	opaque.ReleaseName = "Opaque PHD Name-GRP"
	if got := azFamilyReviewedName(t, "PHD", opaque, nil); got != opaque.ReleaseName {
		t.Fatalf("opaque name = %q, want %q", got, opaque.ReleaseName)
	}

	manual := subject
	manual.GeneratedName = manual.GeneratedName.Clone()
	markAZFamilyComponentManual(t, manual.GeneratedName, api.NameRoleSource, true)
	manual.ReleaseName = manual.GeneratedName.Render().Name
	if got := azFamilyReviewedName(t, "PHD", manual, nil); !strings.Contains(got, " DVD ") {
		t.Fatalf("manual DVD source changed: %q", got)
	}

	manualEdition := azFamilyGeneratedSubject(t, api.ReleaseNameRequest{
		Category:    "MOVIE",
		Type:        "WEBDL",
		Title:       "Localized Film",
		AltTitle:    "Original Film",
		Year:        2026,
		Edition:     "LIMITED Criterion Collection Extended Cut",
		Resolution:  "1080p",
		Source:      "WEB-DL",
		Audio:       "DD 5.1",
		VideoEncode: "H.265",
		Tag:         "-GRP",
	})
	manualEdition.GeneratedName = manualEdition.GeneratedName.Clone()
	markAZFamilyComponentManual(t, manualEdition.GeneratedName, api.NameRoleEdition, true)
	manualEdition.ReleaseName = manualEdition.GeneratedName.Render().Name
	manualEdition.ProviderMetadata.IMDB = &api.IMDBMetadata{AKA: "Original Film"}
	if got, want := azFamilyReviewedName(t, "CZ", manualEdition, nil), "Original Film 2026 LIMITED Criterion Collection Extended Cut 1080p WEB-DL DD 5.1 H.265-GRP"; got != want {
		t.Fatalf("manual edition = %q, want %q", got, want)
	}

	manualEmptyEdition := azFamilyGeneratedSubject(t, api.ReleaseNameRequest{
		Category:    "MOVIE",
		Type:        "WEBDL",
		Title:       "Localized Film",
		AltTitle:    "Original Film",
		Year:        2026,
		Edition:     "LIMITED Criterion Collection",
		Resolution:  "1080p",
		Source:      "WEB-DL",
		Audio:       "DD 5.1",
		VideoEncode: "H.265",
		Tag:         "-GRP",
	})
	manualEmptyEdition.GeneratedName = manualEmptyEdition.GeneratedName.Clone()
	markAZFamilyComponentManual(t, manualEmptyEdition.GeneratedName, api.NameRoleEdition, true)
	manualEmptyEdition.ReleaseName = manualEmptyEdition.GeneratedName.Render().Name
	if got, want := azFamilyReviewedName(t, "PHD", manualEmptyEdition, nil), "Localized Film 2026 LIMITED Criterion Collection 1080p WEB-DL DD 5.1 H.265-GRP"; got != want {
		t.Fatalf("manual empty edition = %q, want %q", got, want)
	}
}

func TestAZFamilySearchNameUsesFactsNotUploadName(t *testing.T) {
	t.Parallel()
	subject := azFamilyGeneratedSubject(t, api.ReleaseNameRequest{
		Category:    "MOVIE",
		Type:        "WEBDL",
		Title:       "Fact Title",
		Year:        2026,
		Resolution:  "1080p",
		Audio:       "DD 5.1",
		VideoEncode: "H.265",
		Tag:         "-GRP",
	})
	requested := "Manual Upload Title-GRP"
	prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker:             "AZ",
		Meta:                subject,
		RequestedUploadName: &requested,
	}, New("AZ").ReleaseNamePolicy())
	if failure != nil {
		t.Fatal(failure)
	}
	if got, want := prepared.Projection.DuplicateCriteria.Name, "Fact Title"; got != want {
		t.Fatalf("duplicate search = %q, want %q", got, want)
	}
}

func TestCinemaZStructuredPolicyUsesCurrentIMDbYear(t *testing.T) {
	t.Parallel()
	subject := azFamilyGeneratedSubject(t, api.ReleaseNameRequest{
		Category:    "MOVIE",
		Type:        "WEBDL",
		Title:       "Localized Film",
		AltTitle:    "Original Film",
		Year:        2025,
		Resolution:  "1080p",
		Audio:       "DD 5.1",
		VideoEncode: "H.265",
		Tag:         "-GRP",
	})
	subject.SourcePath = "prepared/source"
	subject.Identity.SourcePath = subject.SourcePath
	subject.Identity.IMDBID = 123
	subject.ProviderMetadata = api.SourceScopedMetadata{
		SourcePath: subject.SourcePath,
		IMDB: &api.IMDBMetadata{
			IMDBID: 123,
			AKA:    "Original Film",
			Year:   2024,
			Akas: []api.IMDBAKA{{
				Title:    "English Film",
				Country:  "Otherland",
				Language: "English",
			}},
		},
	}
	if got, want := azFamilyReviewedName(t, "CZ", subject, nil), "English Film 2024 1080p WEB-DL DD 5.1 H.265-GRP"; got != want {
		t.Fatalf("current IMDb name = %q, want %q", got, want)
	}
}

func TestAZFamilyNamingPolicyVersions(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		site, want string
		provider   api.IdentityProvider
	}{
		{"AZ", "azfamily/az/v3", api.IdentityProviderTMDB},
		{"CZ", "azfamily/cz/v5", api.IdentityProviderIMDB},
		{"PHD", "azfamily/phd/v3", api.IdentityProviderTMDB},
	} {
		t.Run(test.site, func(t *testing.T) {
			policy := New(test.site).ReleaseNamePolicy()
			if policy.ID != test.want || policy.MovieYearProvider != test.provider || policy.Structured == nil {
				t.Fatalf("policy = %#v", policy)
			}
		})
	}
}

func TestCinemaZEnglishCountryAKA(t *testing.T) {
	t.Parallel()
	metadata := &api.IMDBMetadata{Akas: []api.IMDBAKA{
		{
			Title:      "Working English",
			Country:    "Otherland",
			Language:   "English",
			Attributes: []string{"working title"},
		},
		{
			Title:    "English Title",
			Country:  "Otherland",
			Language: "English",
		},
	}}
	if got, want := cinemaZEnglishCountryAKA(metadata, false), "English Title"; got != want {
		t.Fatalf("AKA = %q, want %q", got, want)
	}
}

func azFamilyGeneratedSubject(t *testing.T, request api.ReleaseNameRequest) api.UploadSubject {
	t.Helper()
	result := metadata.BuildReleaseName(request, api.NopLogger{})
	if result.GeneratedName == nil {
		t.Fatal("BuildReleaseName did not produce a document")
	}
	subject := api.UploadSubject{
		Identity:         api.ExternalIdentity{Category: api.CanonicalCategory(request.Category)},
		ReleaseName:      result.Name,
		ReleaseNameNoTag: result.NameNoTag,
		GeneratedName:    result.GeneratedName,
		Release: api.ReleaseInfo{
			Category:   request.Category,
			Title:      request.Title,
			Alt:        request.AltTitle,
			Year:       request.Year,
			Resolution: request.Resolution,
			Size:       request.DVDSize,
		},
		Type:        request.Type,
		DiscType:    request.DiscType,
		Source:      request.Source,
		Region:      request.Region,
		UHD:         request.UHD,
		HDR:         request.HDR,
		Audio:       request.Audio,
		VideoCodec:  request.VideoCodec,
		VideoEncode: request.VideoEncode,
		Tag:         request.Tag,
	}
	if request.Category == "TV" {
		subject.SeasonStr, subject.EpisodeStr, subject.SeasonInt, subject.EpisodeInt = request.Season, request.Episode, 1, 2
	}
	return subject
}

func azFamilyReviewedName(t *testing.T, site string, subject api.UploadSubject, requested *string) string {
	t.Helper()
	prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker:             site,
		Meta:                subject,
		RequestedUploadName: requested,
	}, New(site).ReleaseNamePolicy())
	if failure != nil {
		t.Fatal(failure)
	}
	name, err := prepared.ReviewedUploadName()
	if err != nil {
		t.Fatal(err)
	}
	return name
}

func markAZFamilyComponentManual(t *testing.T, document *api.ReleaseNameDocument, role api.ReleaseNameRole, present bool) {
	t.Helper()
	for index := range document.Components {
		component := &document.Components[index]
		if component.Role == role {
			component.Manual, component.Present = true, present
			return
		}
	}
	t.Fatalf("generated name is missing %s", role)
}
