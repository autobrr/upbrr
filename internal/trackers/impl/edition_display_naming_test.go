// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestEditionDisplayPreservesTrackerNamingPolicies(t *testing.T) {
	t.Parallel()
	registry := MustNewRegistry()
	const wording = "LIMITED Criterion Collection Collector's 4K restored version"
	for _, test := range []struct {
		name, tracker, releaseType, discType, source, resolution, edition string
	}{
		{"CinemaZ normalization", "CZ", "ENCODE", "", "BluRay", "1080p", wording},
		{"PHD normalization", "PHD", "ENCODE", "", "BluRay", "1080p", wording},
		{"CinemaZ empty normalization", "CZ", "ENCODE", "", "BluRay", "1080p", "LIMITED Criterion Collection 10th Anniversary Edition"},
		{"PHD empty normalization", "PHD", "ENCODE", "", "BluRay", "1080p", "LIMITED Criterion Collection 10th Anniversary Edition"},
		{"AITHER omission", "AITHER", "ENCODE", "", "BluRay", "1080p", wording},
		{"HHD omission", "HHD", "ENCODE", "", "BluRay", "1080p", wording},
		{"YUS omission", "YUS", "ENCODE", "", "BluRay", "1080p", wording},
		{"ULCX omission", "ULCX", "ENCODE", "", "BluRay", "1080p", wording},
		{"LUME encode omission", "LUME", "ENCODE", "", "BluRay", "1080p", wording},
		{"LUME full disc retention", "LUME", "DISC", "BDMV", "BluRay", "1080p", wording},
		{"DVL DVD encode omission", "DVL", "ENCODE", "", "DVD", "480p", wording},
		{"DVL missing resolution retention", "DVL", "ENCODE", "", "DVD", "", wording},
		{"DVL BluRay retention", "DVL", "ENCODE", "", "BluRay", "1080p", wording},
	} {
		t.Run(test.name, func(t *testing.T) {
			descriptor, ok := registry.LookupDescriptor(test.tracker)
			if !ok {
				t.Fatalf("missing tracker %s", test.tracker)
			}
			request := api.ReleaseNameRequest{
				Category:    "MOVIE",
				Title:       "Example Movie",
				Year:        2026,
				Type:        test.releaseType,
				DiscType:    test.discType,
				Source:      test.source,
				Resolution:  test.resolution,
				Edition:     test.edition,
				Audio:       "DD 5.1",
				VideoCodec:  "AVC",
				VideoEncode: "x264",
				Tag:         "-GRP",
			}
			legacy := editionDisplayNamingSubject(t, request)
			want := editionDisplayReviewedName(t, descriptor.ReleaseNamePolicy, test.tracker, legacy, nil)
			request.Edition, request.EditionDisplay = "", test.edition
			if strings.Contains(test.edition, "Collector's") {
				request.Edition = "Collector's"
			}
			display := editionDisplayNamingSubject(t, request)
			if component, exists := display.GeneratedName.Component(api.NameRoleEditionDisplay); !exists || !component.Present {
				t.Fatal("fixture did not render edition display")
			}
			if got := editionDisplayReviewedName(t, descriptor.ReleaseNamePolicy, test.tracker, display, nil); got != want {
				t.Fatalf("display name = %q, legacy edition name = %q", got, want)
			}
			if display.GeneratedName.Render().Name != display.ReleaseName || display.Edition != request.Edition {
				t.Fatal("tracker naming changed canonical facts")
			}
			for index := range legacy.GeneratedName.Components {
				if legacy.GeneratedName.Components[index].Role == api.NameRoleEdition {
					legacy.GeneratedName.Components[index].Manual = true
				}
			}
			if got := editionDisplayReviewedName(t, descriptor.ReleaseNamePolicy, test.tracker, legacy, nil); !strings.Contains(got, test.edition) {
				t.Fatalf("manual edition changed: %q", got)
			}
			requested := "Manual Release Name-GRP"
			if got := editionDisplayReviewedName(t, descriptor.ReleaseNamePolicy, test.tracker, display, &requested); got != requested {
				t.Fatalf("manual full name = %q, want %q", got, requested)
			}
		})
	}
}

func editionDisplayNamingSubject(t *testing.T, request api.ReleaseNameRequest) api.UploadSubject {
	t.Helper()
	named := metadata.BuildReleaseName(request, api.NopLogger{})
	if named.GeneratedName == nil {
		t.Fatal("generated name is missing")
	}
	return api.UploadSubject{
		SourcePath: "edition-display",
		Identity: api.ExternalIdentity{
			SourcePath: "edition-display",
			Generation: 1,
			Category:   api.CanonicalCategoryMovie,
			TMDBID:     123,
			IMDBID:     456,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: "edition-display",
			Generation: 1,
			TMDB: &api.TMDBMetadata{
				TMDBID:   123,
				Category: "MOVIE",
				Title:    request.Title,
				Year:     request.Year,
			},
			IMDB: &api.IMDBMetadata{
				IMDBID: 456,
				Title:  request.Title,
				AKA:    request.Title,
				Year:   request.Year,
			},
		},
		Release: api.ReleaseInfo{
			Category:   request.Category,
			Title:      request.Title,
			Year:       request.Year,
			Resolution: request.Resolution,
		},
		LanguageFacts: api.LanguageFacts{Tracks: []api.MediaTrackFacts{{
			Kind:         api.MediaTrackAudio,
			Role:         api.AudioRoleProgramme,
			Default:      true,
			DefaultKnown: true,
			Codec:        "DD",
			AudioLabel:   request.Audio,
		}}},
		Type:             request.Type,
		DiscType:         request.DiscType,
		Source:           request.Source,
		Audio:            request.Audio,
		VideoCodec:       request.VideoCodec,
		VideoEncode:      request.VideoEncode,
		Tag:              request.Tag,
		Edition:          request.Edition,
		EditionDisplay:   []api.EditionDisplayPart{{Text: request.EditionDisplay}},
		ReleaseName:      named.Name,
		ReleaseNameNoTag: named.NameNoTag,
		GeneratedName:    named.GeneratedName,
	}
}

func editionDisplayReviewedName(t *testing.T, policy trackers.ReleaseNamePolicyBinding, tracker string, subject api.UploadSubject, requested *string) string {
	t.Helper()
	prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker:             tracker,
		Meta:                subject,
		RequestedUploadName: requested,
	}, policy)
	if failure != nil {
		t.Fatal(failure)
	}
	name, err := prepared.ReviewedUploadName()
	if err != nil {
		t.Fatal(err)
	}
	return name
}
