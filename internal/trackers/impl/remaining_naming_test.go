// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl

import (
	"reflect"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestGeneratedDVDRipNamesOmitDVDSourceAcrossTrackers(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, tracker := range registry.Names() {
		// These policies intentionally select an exact source name, not a generated name.
		if tracker == "AR" || tracker == "SP" {
			continue
		}
		t.Run(tracker, func(t *testing.T) {
			generated := metadata.BuildReleaseName(api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "DVDRIP",
				Title:       "Example Movie",
				Year:        2026,
				Source:      "DVD",
				Resolution:  "576p",
				Audio:       "DTS 2.0",
				VideoEncode: "x264",
				Tag:         "-GRP",
			}, nil)
			subject := api.UploadSubject{
				ReleaseName:           generated.Name,
				ReleaseNameNoTag:      generated.NameNoTag,
				GeneratedName:         generated.GeneratedName,
				GeneratedReleaseNames: generated.GeneratedVariants,
				Type:                  "DVDRIP",
				Source:                "DVD",
				Audio:                 "DTS 2.0",
				VideoEncode:           "x264",
				Tag:                   "-GRP",
				Identity: api.ExternalIdentity{
					Category: api.CanonicalCategoryMovie,
					IMDBID:   4242,
					TMDBID:   4242,
				},
				ProviderMetadata: api.SourceScopedMetadata{
					IMDB: &api.IMDBMetadata{
						IMDBID: 4242,
						Title:  "Example Movie",
						AKA:    "Example Movie",
						Year:   2026,
					},
					TMDB: &api.TMDBMetadata{
						TMDBID: 4242,
						Title:  "Example Movie",
						Year:   2026,
					},
				},
				Release: api.ReleaseInfo{
					Title:      "Example Movie",
					Year:       2026,
					Category:   "MOVIE",
					Type:       "DVDRIP",
					Source:     "DVD",
					Resolution: "576p",
				},
			}
			descriptor, _ := registry.LookupDescriptor(tracker)
			prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{Tracker: tracker, Meta: subject}, descriptor.ReleaseNamePolicy)
			if failure != nil {
				t.Fatal(failure)
			}
			name, err := prepared.ReviewedUploadName()
			if err != nil || name == "" {
				t.Fatalf("name=%q err=%v", name, err)
			}
			if tracker == "HDB" || tracker == "OTW" {
				const want = "Example Movie 2026 576p DVDRip DTS 2.0 x264-GRP"
				if name != want {
					t.Fatalf("custom generated name=%q, want %q", name, want)
				}
			}
			tokens := strings.FieldsSeq(strings.ToUpper(strings.ReplaceAll(name, ".", " ")))
			for token := range tokens {
				if token == "DVD" || token == "PAL" || token == "NTSC" {
					t.Fatalf("tracker restored DVD source in %q", name)
				}
			}
			audio, video := strings.Index(name, "DTS"), strings.Index(name, "x264")
			if audio >= 0 && video >= 0 && video < audio {
				t.Fatalf("video precedes audio in %q", name)
			}
		})
	}
}

func TestRemainingStandaloneNamesTargetComponents(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ tracker, want string }{
		{"FL", "DV.DD+.HDR10+.Tales.2001.1080p.WEB-DL.DDP.5.1.DoVi.HDR.H.264-GRP"},
		{"HDT", "DV DD+ HDR10+ Tales 2001 1080p WEB-DL DD+5.1 DoVi HDR10+ H.264-GRP"},
		{"BHDTV", "DV.DD+.HDR10+.Tales.2001.1080p.WEB-DL.DDP.5.1.DV.HDR10+.H.264-GRP"},
		{"THR", "DV DD HDR10 Tales 2001 1080p WEB-DL DDP 5.1 DV HDR10 H.264-GRP"},
	} {
		t.Run(tc.tracker, func(t *testing.T) {
			generated := metadata.BuildReleaseName(api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "WEBDL",
				Title:       "DV DD+ HDR10+ Tales",
				Year:        2001,
				Resolution:  "1080p",
				Source:      "WEB",
				Audio:       "DD+ 5.1",
				HDR:         "DV HDR10+",
				VideoEncode: "H.264",
				Tag:         "-GRP",
			}, nil)
			subject := api.UploadSubject{
				ReleaseName:   generated.Name,
				GeneratedName: generated.GeneratedName,
				Type:          "WEBDL",
				Source:        "WEB",
				Audio:         "DD+ 5.1",
				HDR:           "DV HDR10+",
				VideoEncode:   "H.264",
				Identity:      api.ExternalIdentity{Category: api.CanonicalCategoryMovie, IMDBID: 4242},
				Release: api.ReleaseInfo{
					Title:      "DV DD+ HDR10+ Tales",
					Year:       2001,
					Resolution: "1080p",
				},
			}
			descriptor, ok := registry.LookupDescriptor(tc.tracker)
			if !ok || descriptor.ReleaseNamePolicy.Structured == nil {
				t.Fatal("structured policy not registered")
			}
			original := subject.GeneratedName.Clone()
			prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{Tracker: tc.tracker, Meta: subject}, descriptor.ReleaseNamePolicy)
			if failure != nil {
				t.Fatal(failure)
			}
			name, err := prepared.ReviewedUploadName()
			if err != nil || name != tc.want {
				t.Fatalf("name=%q want=%q err=%v", name, tc.want, err)
			}
			if !reflect.DeepEqual(original, subject.GeneratedName) {
				t.Fatal("canonical document mutated")
			}
			requested := "Manual DV DD+ HDR10+ Name"
			prepared, failure = trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
				Tracker:             tc.tracker,
				Meta:                subject,
				RequestedUploadName: &requested,
			}, descriptor.ReleaseNamePolicy)
			if failure != nil {
				t.Fatal(failure)
			}
			name, err = prepared.ReviewedUploadName()
			if err != nil || name != requested {
				t.Fatalf("opaque name=%q err=%v", name, err)
			}
		})
	}
}

func TestFileListExactUploadKeepsGeneratedDuplicateName(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	descriptor, _ := registry.LookupDescriptor("FL")
	generated := metadata.BuildReleaseName(api.ReleaseNameRequest{
		Category:    "MOVIE",
		Type:        "WEBDL",
		Title:       "Example Movie",
		Year:        2001,
		Resolution:  "1080p",
		Source:      "WEB",
		Audio:       "DD+ 5.1",
		VideoEncode: "H.264",
		Tag:         "-GRP",
	}, nil)
	subject := api.UploadSubject{
		ReleaseName:   generated.Name,
		GeneratedName: generated.GeneratedName,
		Type:          "WEBDL",
		Source:        "WEB",
		Identity:      api.ExternalIdentity{Category: api.CanonicalCategoryMovie, IMDBID: 4242},
		Release: api.ReleaseInfo{
			Title:      "Example Movie",
			Year:       2001,
			Resolution: "1080p",
		},
		TrackerQuestionnaireAnswers: map[string]map[string]string{"FL": {"name": "Exact Questionnaire Name"}},
		Scene:                       true,
		SceneName:                   "Distinct Scene Name-GRP",
	}
	for _, requested := range []*string{nil, new("Requested Upload Name")} {
		input := trackers.PreparationInput{
			Tracker:             "FL",
			Meta:                subject,
			RequestedUploadName: requested,
		}
		projection, failure := registry.ProjectRelease(t.Context(), input, "", "", "")
		if failure != nil {
			t.Fatal(failure)
		}
		want := "Exact Questionnaire Name"
		if requested != nil {
			want = *requested
		}
		if projection.UploadReleaseName != want || projection.DuplicateCriteria.Name != "Example.Movie.2001.1080p.WEB-DL.DDP.5.1.H.264-GRP" {
			t.Fatalf("upload=%q duplicate=%q", projection.UploadReleaseName, projection.DuplicateCriteria.Name)
		}
		prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(input, descriptor.ReleaseNamePolicy)
		if failure != nil {
			t.Fatal(failure)
		}
		prepared, failure = trackers.PrepareInputWithReleaseNamePolicy(prepared, descriptor.ReleaseNamePolicy)
		if failure != nil {
			t.Fatal(failure)
		}
		if name, err := prepared.ReviewedUploadName(); err != nil || name != want {
			t.Fatalf("reviewed=%q err=%v", name, err)
		}
	}
	subject.Identity.IMDBID = 0
	projection, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{Tracker: "FL", Meta: subject}, "", "", "")
	if failure != nil || projection.DuplicateCriteria.Name != "Example Movie" {
		t.Fatalf("title search=%q failure=%v", projection.DuplicateCriteria.Name, failure)
	}
	subject.Identity.IMDBID = 4242
	subject.Release.Title = ""
	projection, failure = registry.ProjectRelease(t.Context(), trackers.PreparationInput{Tracker: "FL", Meta: subject}, "", "", "")
	if failure != nil || projection.DuplicateCriteria.Name != "Example.Movie.2001.1080p.WEB-DL.DDP.5.1.H.264-GRP" {
		t.Fatalf("missing title search=%q failure=%v", projection.DuplicateCriteria.Name, failure)
	}
	subject.TrackerQuestionnaireAnswers = map[string]map[string]string{"THR": {"name_override": "THR Exact Name"}}
	thr, _ := registry.LookupDescriptor("THR")
	prepared, thrFailure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{Tracker: "THR", Meta: subject}, thr.ReleaseNamePolicy)
	if thrFailure != nil || prepared.Projection.DuplicateCriteria.Name != "THR Exact Name" {
		t.Fatalf("THR exact search changed: %v", thrFailure)
	}
	subject.TrackerQuestionnaireAnswers = map[string]map[string]string{"FL": {"name": "Exact Questionnaire Name"}}
	subject.GeneratedName = nil
	if _, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{Tracker: "FL", Meta: subject}, descriptor.ReleaseNamePolicy); failure == nil {
		t.Fatal("missing search document accepted")
	}
}
