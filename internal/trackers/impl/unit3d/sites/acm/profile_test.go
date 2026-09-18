// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package acm

import (
	"context"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestACMStructuredReleaseNamePolicy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		request   api.ReleaseNameRequest
		subtitles []string
		configure func(*api.UploadSubject)
		want      string
	}{
		{
			name: "remux normalizes semantic technical roles and subtitle marker",
			request: api.ReleaseNameRequest{
				Category:   "MOVIE",
				Type:       "REMUX",
				Title:      "Example",
				AltTitle:   "Original Example",
				Year:       2024,
				Resolution: "1080p",
				Source:     "BluRay",
				UHD:        "UHD",
				Audio:      "DD+ 5.1 Atmos",
				VideoCodec: "H.265",
				Tag:        "-GRP",
			},
			subtitles: []string{"Japanese"},
			configure: func(subject *api.UploadSubject) { subject.EffectiveMetadata.OriginalTitle = "Original Example" },
			want:      "Example / Original Example \u202A 2024 1080p Remux HEVC DD+5.1-GRP [Jpn subs only]",
		},
		{
			name: "English subtitles omit the marker",
			request: api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "ENCODE",
				Title:       "Example",
				Year:        2024,
				Resolution:  "1080p",
				Source:      "BluRay",
				Audio:       "AAC 2.0",
				VideoEncode: "x264",
				Tag:         "-GRP",
			},
			subtitles: []string{"English", "Japanese"},
			want:      "Example 2024 1080p BluRay AAC2.0 x264-GRP",
		},
		{
			name: "subtitles follow the last generated component when technical roles are absent",
			request: api.ReleaseNameRequest{
				Category: "MOVIE",
				Type:     "ENCODE",
				Title:    "Example",
				Year:     2024,
			},
			subtitles: []string{"Japanese"},
			want:      "Example 2024 [Jpn subs only]",
		},
		{
			name: "H.265 video encode normalizes to HEVC",
			request: api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "ENCODE",
				Title:       "Example",
				Year:        2024,
				Resolution:  "1080p",
				Source:      "BluRay",
				Audio:       "DD 5.1",
				VideoEncode: "H.265",
				Tag:         "-GRP",
			},
			subtitles: []string{"Japanese"},
			want:      "Example 2024 1080p BluRay DD 5.1 HEVC-GRP [Jpn subs only]",
		},
		{
			name: "compound H.265 video encode preserves the other generated token",
			request: api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "ENCODE",
				Title:       "Example",
				Year:        2024,
				Resolution:  "1080p",
				Source:      "BluRay",
				Audio:       "DD 5.1",
				VideoEncode: "Hi10P H.265",
				Tag:         "-GRP",
			},
			subtitles: []string{"Japanese"},
			want:      "Example 2024 1080p BluRay DD 5.1 Hi10P HEVC-GRP [Jpn subs only]",
		},
		{
			name: "DVD remux preserves its source and format",
			request: api.ReleaseNameRequest{
				Category:   "MOVIE",
				Type:       "REMUX",
				Title:      "Example",
				Year:       2024,
				Resolution: "480p",
				Source:     "DVD",
				Audio:      "DD 5.1",
				VideoCodec: "H.265",
				Tag:        "-GRP",
			},
			subtitles: []string{"Japanese"},
			want:      "Example 2024 DVD REMUX DD 5.1-GRP [Jpn subs only]",
		},
		{
			name: "provider original title takes precedence over alternate and IMDb AKA",
			request: api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "ENCODE",
				Title:       "Example",
				AltTitle:    "Fallback Original",
				Year:        2024,
				Resolution:  "1080p",
				Source:      "BluRay",
				Audio:       "DD 5.1",
				VideoEncode: "x264",
				Tag:         "-GRP",
			},
			configure: func(subject *api.UploadSubject) {
				subject.ProviderMetadata.TMDB = &api.TMDBMetadata{OriginalTitle: "TMDB Original", RetrievedAKA: "TMDB AKA"}
				subject.ProviderMetadata.IMDB = &api.IMDBMetadata{AKA: "IMDb AKA"}
			},
			want: "Example / TMDB Original \u202A 2024 1080p BluRay DD 5.1 x264-GRP [No subs]",
		},
		{
			name: "provider retrieved AKA takes precedence over IMDb AKA",
			request: api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "ENCODE",
				Title:       "Example",
				AltTitle:    "Fallback Original",
				Year:        2024,
				Resolution:  "1080p",
				Source:      "BluRay",
				Audio:       "DD 5.1",
				VideoEncode: "x264",
				Tag:         "-GRP",
			},
			configure: func(subject *api.UploadSubject) {
				subject.ProviderMetadata.TMDB = &api.TMDBMetadata{RetrievedAKA: "AKA TMDB AKA"}
				subject.ProviderMetadata.IMDB = &api.IMDBMetadata{AKA: "IMDb AKA"}
			},
			want: "Example / TMDB AKA \u202A 2024 1080p BluRay DD 5.1 x264-GRP [No subs]",
		},
		{
			name: "DVD keeps its no subtitle suffix off disc names",
			request: api.ReleaseNameRequest{
				Category:   "MOVIE",
				Type:       "DISC",
				DiscType:   "DVD",
				Title:      "Example",
				Year:       2024,
				Resolution: "480p",
				Source:     "NTSC DVD",
				DVDSize:    "DVD5",
				Audio:      "DD 2.0",
			},
			subtitles: []string{"Japanese"},
			configure: func(subject *api.UploadSubject) { subject.Channels = "DD 2.0" },
			want:      "Example 2024 480p DVD NTSC DVD MPEG DD 2.0",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			subject := acmGeneratedSubject(t, test.request, test.subtitles)
			if test.configure != nil {
				test.configure(&subject)
			}
			if got := acmReviewedName(t, subject, nil); got != test.want {
				t.Fatalf("reviewed name = %q, want %q", got, test.want)
			}
		})
	}
}

func TestACMStructuredPolicyPreservesManualAndOpaqueNames(t *testing.T) {
	t.Parallel()
	subject := acmGeneratedSubject(t, api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "REMUX",
		Title:      "Example",
		Year:       2024,
		Resolution: "1080p",
		Source:     "BluRay",
		Audio:      "DD 5.1",
		VideoCodec: "H.265",
		Tag:        "-GRP",
	}, []string{"Japanese"})
	override := "Exact Manual ACM Name-GRP"
	if got := acmReviewedName(t, subject, &override); got != override {
		t.Fatalf("opaque override = %q, want %q", got, override)
	}
	manual := subject
	manual.GeneratedName = manual.GeneratedName.Clone()
	markACMManual(t, manual.GeneratedName, api.NameRoleSource)
	manual.ReleaseName = manual.GeneratedName.Render().Name
	if got := acmReviewedName(t, manual, nil); !strings.Contains(got, "BluRay") {
		t.Fatalf("manual source was changed: %q", got)
	}
	manualAlternate := acmGeneratedSubject(t, api.ReleaseNameRequest{
		Category:    "MOVIE",
		Type:        "ENCODE",
		Title:       "Example",
		AltTitle:    "Manual Original",
		Year:        2024,
		Resolution:  "1080p",
		Source:      "BluRay",
		Audio:       "DD 5.1",
		VideoEncode: "x264",
		Tag:         "-GRP",
	}, nil)
	manualAlternate.GeneratedName = manualAlternate.GeneratedName.Clone()
	markACMManual(t, manualAlternate.GeneratedName, api.NameRoleAlternateTitle)
	manualAlternate.ReleaseName = manualAlternate.GeneratedName.Render().Name
	manualAlternate.ProviderMetadata.TMDB = &api.TMDBMetadata{OriginalTitle: "Provider Original"}
	if got, want := acmReviewedName(t, manualAlternate, nil), "Example Manual Original 2024 1080p BluRay DD 5.1 x264-GRP [No subs]"; got != want {
		t.Fatalf("manual alternate name = %q, want %q", got, want)
	}
	manualOriginal := acmGeneratedSubject(t, api.ReleaseNameRequest{
		Category:    "MOVIE",
		Type:        "ENCODE",
		Title:       "Example",
		AltTitle:    "Fallback Original",
		Year:        2024,
		Resolution:  "1080p",
		Source:      "BluRay",
		Audio:       "DD 5.1",
		VideoEncode: "x264",
		Tag:         "-GRP",
	}, nil)
	manualOriginal.EffectiveMetadata = api.EffectiveMetadata{OriginalTitle: "Manual Original", OriginalTitleProvenance: api.FactProvenanceManual}
	manualOriginal.ProviderMetadata.TMDB = &api.TMDBMetadata{OriginalTitle: "Provider Original"}
	if got, want := acmReviewedName(t, manualOriginal, nil), "Example / Manual Original \u202A 2024 1080p BluRay DD 5.1 x264-GRP [No subs]"; got != want {
		t.Fatalf("manual original name = %q, want %q", got, want)
	}
}

func TestProfileParity(t *testing.T) {
	profile := Profile().Site
	if got := unit3d.NewWithProfile(Profile()).ReleaseNamePolicy(); got.ID != "unit3d/acm/v4" || got.Structured == nil || got.Resolver != nil {
		t.Fatalf("ACM policy = %#v", got)
	}
	if got := profile.ResolveTypeID(api.UploadSubject{
		DiscType:   "BDMV",
		UHD:        "UHD",
		SourceSize: 60 * (1 << 30),
	}); got != "2" {
		t.Fatalf("type = %q", got)
	}
	if got := profile.ResolveResolutionID(api.UploadSubject{Release: api.ReleaseInfo{Resolution: "1080i"}}); got != "2" {
		t.Fatalf("resolution = %q", got)
	}
	if got := profile.ResolveTypeID(api.UploadSubject{DiscType: "DVD", Release: api.ReleaseInfo{Size: "DVD5"}}); got != "14" {
		t.Fatalf("DVD5 type = %q", got)
	}
	if got := profile.ResolveTypeID(api.UploadSubject{DiscType: "DVD", Release: api.ReleaseInfo{Size: "DVD9"}}); got != "16" {
		t.Fatalf("DVD9 type = %q", got)
	}
	keywordsMeta := api.UploadSubject{
		Region:           "3",
		Distributor:      "42",
		ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Keywords: "one, two words, three,four,five,six,seven,eight,nine,ten,eleven,twelve"}},
	}
	if got := profile.ResolveKeywords(keywordsMeta); got != "one, three, four, five, six, seven, eight, nine, ten, eleven" {
		t.Fatalf("keywords = %q", got)
	}
	data := map[string]string{}
	profile.ApplyAdditionalPayload(trackers.PreparationInput{Meta: keywordsMeta}, data)
	if data["region_id"] != "3" || data["distributor_id"] != "42" {
		t.Fatalf("payload = %#v", data)
	}
}

func TestDescriptionParity(t *testing.T) {
	meta := api.UploadSubject{Type: "WEBDL", ServiceLongName: "Example Stream"}
	result, err := Profile().Site.BuildDescription(context.Background(), meta, config.Config{}, config.TrackerConfig{}, api.NopLogger{}, "[pre]x[/pre]\n[hide=test]y[/hide]\n[img]https://img.example/z.png[/img]", nil, nil)
	if err != nil {
		t.Fatalf("description: %v", err)
	}
	for _, want := range []string{"[code]x[/code]", "[spoiler=test]y[/spoiler]", "not transcoded, just remuxed from the direct Example Stream stream", "[img=300]https://img.example/z.png[/img]"} {
		if !strings.Contains(result, want) {
			t.Fatalf("missing %q in %q", want, result)
		}
	}
}

func acmGeneratedSubject(t *testing.T, request api.ReleaseNameRequest, subtitles []string) api.UploadSubject {
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
		AlternateTitle:    request.AltTitle,
		Type:              request.Type,
		DiscType:          request.DiscType,
		Source:            request.Source,
		Audio:             request.Audio,
		VideoCodec:        request.VideoCodec,
		VideoEncode:       request.VideoEncode,
		SubtitleLanguages: subtitles,
		Tag:               request.Tag,
	}
}

func acmReviewedName(t *testing.T, subject api.UploadSubject, requested *string) string {
	t.Helper()
	prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker:             "ACM",
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

func markACMManual(t *testing.T, document *api.ReleaseNameDocument, role api.ReleaseNameRole) {
	t.Helper()
	for index := range document.Components {
		if document.Components[index].Role == role {
			document.Components[index].Manual = true
			return
		}
	}
	t.Fatalf("generated name is missing %s", role)
}
