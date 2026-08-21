// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package acm

import (
	"context"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestProfileParity(t *testing.T) {
	profile := Profile().Site
	if profile.BuildNameVersion != "v2" {
		t.Fatalf("ACM build-name version = %q", profile.BuildNameVersion)
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
	meta := api.UploadSubject{
		ReleaseName:       "Example.2024.1080p.BluRay.REMUX.H.265.DD+ 5.1 Atmos-GRP",
		Audio:             "DD+ 5.1",
		ProviderMetadata:  api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Title: "Example", OriginalTitle: "Original Example"}},
		SubtitleLanguages: []string{"Japanese"},
	}
	name := profile.BuildName(meta, config.TrackerConfig{})
	if !strings.Contains(name, "Example / Original Example") || strings.Contains(name, "H.265") || !strings.Contains(name, "[Jpn subs only]") {
		t.Fatalf("name = %q", name)
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

func TestBuildNameDoesNotAddSubtitleTagsToDiscs(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		DiscType:          "DVD",
		ReleaseName:       "Example Release 2026 480p NTSC DVD DD 2.0-GRP",
		SubtitleLanguages: []string{"Japanese"},
	}
	got := Profile().Site.BuildName(meta, config.TrackerConfig{})
	if strings.Contains(got, "[Jpn subs only]") {
		t.Fatalf("disc name added subtitle tag: %q", got)
	}
}

func TestBuildNameHonorsOmitAlternateTitle(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		ReleaseName:      "Example Release 2026 1080p WEB-DL-GRP",
		Release:          api.ReleaseInfo{Title: "Example Release"},
		ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{OriginalTitle: "Example Original"}},
		NamePresentation: api.ReleaseNamePresentation{
			Version:            api.ReleaseNamePresentationVersionV1,
			OmitAlternateTitle: true,
		},
	}
	if got := buildACMName(meta); strings.Contains(got, "Example Original") {
		t.Fatalf("ACM name retained optional alternate title: %q", got)
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
