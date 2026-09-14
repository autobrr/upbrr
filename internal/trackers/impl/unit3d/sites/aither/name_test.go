// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package aither

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestAitherStructuredReleaseNamePolicy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		request   api.ReleaseNameRequest
		languages []string
		configure func(*api.UploadSubject)
		want      string
	}{
		{
			name: "language marker has its own role when title collides",
			request: api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "ENCODE",
				Title:       "JAPANESE 1080p Cut Tales",
				Year:        2026,
				Resolution:  "1080p",
				Source:      "BluRay",
				Edition:     "Collector's",
				Audio:       "DD 5.1",
				VideoEncode: "x264",
				Tag:         "-GRP",
			},
			languages: []string{"Japanese"},
			want:      "JAPANESE 1080p Cut Tales 2026 JAPANESE 1080p BluRay DD 5.1 x264-GRP",
		},
		{
			name: "DVD rip moves structured resolution and video encode",
			request: api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "DVDRIP",
				Title:       "Example Release",
				Year:        2026,
				Resolution:  "480p",
				Source:      "DVD",
				Audio:       "DD 1.0",
				VideoEncode: "x264",
				Tag:         "-GRP",
			},
			want: "Example Release 2026 480p DVDRip DD 1.0 x264-GRP",
		},
		{
			name: "DVD rip keeps video encode after dual audio marker",
			request: api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "DVDRIP",
				Title:       "Example Release",
				Year:        2026,
				Resolution:  "480p",
				Source:      "DVD",
				Audio:       "DD 1.0 Dual-Audio",
				VideoEncode: "x264",
				Tag:         "-GRP",
			},
			want: "Example Release 2026 480p DVDRip DD 1.0 Dual-Audio x264-GRP",
		},
		{
			name: "DVD rip omits video encode when audio marker is absent",
			request: api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "DVDRIP",
				Title:       "Example Release",
				Year:        2026,
				Resolution:  "480p",
				Source:      "DVD",
				VideoEncode: "x264",
				Tag:         "-GRP",
			},
			want: "Example Release 2026 480p DVDRip-GRP",
		},
		{
			name: "DVD disc anchors resolution on DVD system when source is absent",
			request: api.ReleaseNameRequest{
				Category:   "MOVIE",
				Type:       "DISC",
				DiscType:   "DVD",
				Title:      "Example Release",
				Year:       2026,
				Resolution: "480p",
				Source:     "PAL DVD",
				DVDSize:    "DVD5",
				Audio:      "DD 1.0",
				VideoCodec: "MPEG-2",
				Tag:        "-GRP",
			},
			want: "Example Release 2026 480p PAL DVD5 MPEG-2 DD 1.0-GRP",
		},
		{
			name: "TVDB locale and year are independent structured components",
			request: api.ReleaseNameRequest{
				Category:    "TV",
				Type:        "WEBDL",
				Title:       "Canonical Name",
				AltTitle:    "AKA Original Name",
				Year:        2024,
				Season:      "S01",
				Episode:     "E01",
				Resolution:  "1080p",
				Audio:       "DD+ 5.1",
				VideoEncode: "H.264",
				Tag:         "-GRP",
			},
			languages: []string{"English"},
			configure: func(subject *api.UploadSubject) {
				subject.ProviderMetadata.TVDB = &api.TVDBMetadata{NameDisambiguation: api.TVDBNameDisambiguation{
					CanonicalName: "Canonical Name",
					SeriesYear:    2026,
					Locale:        "US",
					IncludeYear:   true,
					IncludeLocale: true,
				}}
			},
			want: "Canonical Name AKA Original Name US 2026 S01E01 1080p WEB-DL DD+ 5.1 H.264-GRP",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			subject := aitherGeneratedSubject(t, test.request, test.languages)
			if test.configure != nil {
				test.configure(&subject)
			}
			if got := aitherReviewedName(t, subject, nil); got != test.want {
				t.Fatalf("reviewed name = %q, want %q", got, test.want)
			}
		})
	}
}

func TestAitherStructuredPolicyPreservesManualAndOpaqueNames(t *testing.T) {
	t.Parallel()
	subject := aitherGeneratedSubject(t, api.ReleaseNameRequest{
		Category:    "MOVIE",
		Type:        "DVDRIP",
		Title:       "Example Release",
		Year:        2026,
		Resolution:  "480p",
		Source:      "PAL DVD",
		Audio:       "DD 1.0",
		VideoEncode: "x264",
		Tag:         "-GRP",
	}, []string{"Japanese"})
	override := "Exact Manual AITHER Name-GRP"
	if got := aitherReviewedName(t, subject, &override); got != override {
		t.Fatalf("opaque override = %q, want %q", got, override)
	}
	manual := subject
	manual.GeneratedName = manual.GeneratedName.Clone()
	markAitherManual(t, manual.GeneratedName, api.NameRoleSource)
	manual.ReleaseName = manual.GeneratedName.Render().Name
	if got := aitherReviewedName(t, manual, nil); !strings.Contains(got, "PAL DVD") {
		t.Fatalf("manual source was changed: %q", got)
	}
	manualDualAudio := aitherGeneratedSubject(t, api.ReleaseNameRequest{
		Category:    "MOVIE",
		Type:        "DVDRIP",
		Title:       "Example Release",
		Year:        2026,
		Resolution:  "480p",
		Source:      "DVD",
		Audio:       "DD 1.0 Dual-Audio",
		VideoEncode: "x264",
		Tag:         "-GRP",
	}, nil)
	manualDualAudio.GeneratedName = manualDualAudio.GeneratedName.Clone()
	markAitherManual(t, manualDualAudio.GeneratedName, api.NameRoleDualAudio)
	manualDualAudio.ReleaseName = manualDualAudio.GeneratedName.Render().Name
	if got, want := aitherReviewedName(t, manualDualAudio, nil), "Example Release 2026 480p DVDRip DD 1.0 Dual-Audio x264-GRP"; got != want {
		t.Fatalf("manual dual-audio name = %q, want %q", got, want)
	}
}

func TestAitherTVDBDisambiguationRequiresCurrentMatchingAutomaticTitle(t *testing.T) {
	t.Parallel()
	request := api.ReleaseNameRequest{
		Category:    "TV",
		Type:        "WEBDL",
		Title:       "Example Series",
		AltTitle:    "AKA Original",
		Year:        2024,
		Season:      "S01",
		Episode:     "E01",
		Resolution:  "1080p",
		Audio:       "DD+ 5.1",
		VideoEncode: "H.264",
		Tag:         "-GRP",
	}
	tests := []struct {
		name      string
		configure func(*api.UploadSubject)
		want      string
	}{
		{name: "missing evidence", configure: func(*api.UploadSubject) {}},
		{name: "stale snapshot", configure: func(subject *api.UploadSubject) {
			subject.SourcePath, subject.Identity.SourcePath, subject.ProviderMetadata.SourcePath = "current", "current", "stale"
			subject.ProviderMetadata.TVDB = aitherTVDBEvidence()
		}},
		{name: "conflicting canonical title", configure: func(subject *api.UploadSubject) {
			subject.ProviderMetadata.TVDB = &api.TVDBMetadata{NameDisambiguation: api.TVDBNameDisambiguation{
				CanonicalName: "Other Series",
				SeriesYear:    2030,
				Locale:        "US",
				IncludeYear:   true,
				IncludeLocale: true,
			}}
		}},
		{
			name: "manual title",
			configure: func(subject *api.UploadSubject) {
				markAitherManual(t, subject.GeneratedName, api.NameRoleTitle)
				subject.ReleaseName = subject.GeneratedName.Render().Name
				subject.ProviderMetadata.TVDB = aitherTVDBEvidence()
			},
			want: "Example Series AKA Original US 2030 S01E01 1080p WEB-DL DD+ 5.1 H.264-GRP",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			subject := aitherGeneratedSubject(t, request, []string{"English"})
			want := test.want
			if want == "" {
				want = subject.ReleaseName
			}
			test.configure(&subject)
			if got := aitherReviewedName(t, subject, nil); got != want {
				t.Fatalf("reviewed name = %q, want unchanged %q", got, want)
			}
		})
	}
}

func aitherTVDBEvidence() *api.TVDBMetadata {
	return &api.TVDBMetadata{NameDisambiguation: api.TVDBNameDisambiguation{
		CanonicalName: "Example Series",
		SeriesYear:    2030,
		Locale:        "US",
		IncludeYear:   true,
		IncludeLocale: true,
	}}
}

func TestAitherProfileUsesStructuredPolicy(t *testing.T) {
	t.Parallel()
	policy := unit3d.NewWithProfile(Profile()).ReleaseNamePolicy()
	if policy.ID != "unit3d/aither/v3" || policy.Structured == nil || policy.Resolver != nil {
		t.Fatalf("AITHER policy = %#v", policy)
	}
}

func aitherGeneratedSubject(t *testing.T, request api.ReleaseNameRequest, languages []string) api.UploadSubject {
	t.Helper()
	generated := metadata.BuildReleaseName(request, api.NopLogger{})
	if generated.GeneratedName == nil {
		t.Fatal("BuildReleaseName did not produce a structured document")
	}
	category, _ := api.NormalizeCanonicalCategory(request.Category)
	return api.UploadSubject{
		ReleaseName:      generated.Name,
		ReleaseNameNoTag: generated.NameNoTag,
		GeneratedName:    generated.GeneratedName,
		Identity:         api.ExternalIdentity{Category: category},
		Release: api.ReleaseInfo{
			Category:   request.Category,
			Title:      request.Title,
			Year:       request.Year,
			Resolution: request.Resolution,
			Size:       request.DVDSize,
		},
		AlternateTitle: request.AltTitle,
		Type:           request.Type,
		DiscType:       request.DiscType,
		Source:         request.Source,
		Audio:          request.Audio,
		VideoCodec:     request.VideoCodec,
		VideoEncode:    request.VideoEncode,
		AudioLanguages: languages,
		Edition:        request.Edition,
		Repack:         request.Repack,
		Tag:            request.Tag,
		SeasonStr:      request.Season,
		EpisodeStr:     request.Episode,
		Region:         request.Region,
	}
}

func aitherReviewedName(t *testing.T, subject api.UploadSubject, requested *string) string {
	t.Helper()
	prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker:             "AITHER",
		Meta:                subject,
		RequestedUploadName: requested,
	}, unit3d.NewWithProfile(Profile()).ReleaseNamePolicy())
	if failure != nil {
		t.Fatal(failure)
	}
	name, err := prepared.ReviewedUploadName()
	if err != nil {
		t.Fatal(err)
	}
	return name
}

func markAitherManual(t *testing.T, document *api.ReleaseNameDocument, role api.ReleaseNameRole) {
	t.Helper()
	for index := range document.Components {
		if document.Components[index].Role == role {
			document.Components[index].Manual = true
			return
		}
	}
	t.Fatalf("generated name is missing %s", role)
}
