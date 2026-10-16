// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"reflect"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/aither"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestAitherNamesFromInspectedAudio(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, tracks, original, releaseType, source, resolution, audio, want string
	}{
		{
			name: "automatic dual audio DVD rip",
			tracks: `,{"@type":"Audio","ID":"1","StreamOrder":"1","Format":"AC-3","Channels":"6","ChannelLayout":"L R C LFE Ls Rs","Language":"ja","Title":"Original Audio","Default":"Yes"},` +
				`{"@type":"Audio","ID":"2","StreamOrder":"2","Format":"AC-3","Channels":"6","ChannelLayout":"L R C LFE Ls Rs","Language":"en","Title":"English Dub","Default":"No"}`,
			original:    "Japanese",
			releaseType: "DVDRIP",
			source:      "DVD",
			resolution:  "480p",
			audio:       "Dual-Audio DD 5.1",
			want:        "Example Film 2026 480p DVDRip Dual-Audio DD 5.1 x264-GRP",
		},
		{
			name:        "absent audio WEB",
			releaseType: "WEBDL",
			resolution:  "576p",
			want:        "Example Film 2026 576p WEB-DL None x264-GRP",
		},
		{
			name:        "absent audio DVD rip",
			releaseType: "DVDRIP",
			source:      "DVD",
			resolution:  "480p",
			want:        "Example Film 2026 480p DVDRip None x264-GRP",
		},
		{
			name:        "non-linguistic score",
			tracks:      `,{"@type":"Audio","ID":"1","StreamOrder":"1","Format":"FLAC","Channels":"2","ChannelLayout":"L R","Language":"zxx","Default":"Yes"}`,
			original:    "ZXX",
			releaseType: "REMUX",
			source:      "BluRay",
			resolution:  "1080p",
			audio:       "FLAC 2.0",
			want:        "Example Film 2026 ZXX 1080p BluRay REMUX AVC FLAC 2.0-GRP",
		},
		{
			name:        "unknown track language",
			tracks:      `,{"@type":"Audio","ID":"1","StreamOrder":"1","Format":"AC-3","Channels":"6","ChannelLayout":"L R C LFE Ls Rs","Default":"Yes"}`,
			original:    "Japanese",
			releaseType: "WEBDL",
			resolution:  "1080p",
			audio:       "DD 5.1",
			want:        "Example Film 2026 1080p WEB-DL DD 5.1 x264-GRP",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			doc := mustParseMediaInfoDoc(`{"media":{"track":[{"@type":"General"}` + test.tracks + `]}}`)
			state := preparationstate.State{
				SourcePath:       "Example.Film.2026.mkv",
				VideoPath:        "Example.Film.2026.mkv",
				ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{OriginalLanguage: test.original}},
			}
			audio, _, _ := audioFromMedia(state, doc, nil)
			if audio != test.audio {
				t.Fatalf("derived audio = %q, want %q", audio, test.audio)
			}
			tracks, primaryID, _, _, err := mediaTrackFacts(state, doc)
			if err != nil {
				t.Fatal(err)
			}
			generated := BuildReleaseName(api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        test.releaseType,
				Title:       "Canonical Film",
				Year:        2026,
				Source:      test.source,
				Resolution:  test.resolution,
				Audio:       audio,
				VideoCodec:  "AVC",
				VideoEncode: "x264",
				Tag:         "-GRP",
			}, api.NopLogger{})
			subject := api.UploadSubject{
				SourcePath:    state.SourcePath,
				ReleaseName:   generated.Name,
				GeneratedName: generated.GeneratedName,
				Type:          test.releaseType,
				Source:        test.source,
				Audio:         audio,
				VideoCodec:    "AVC",
				VideoEncode:   "x264",
				Tag:           "-GRP",
				Release: api.ReleaseInfo{
					Category:   "MOVIE",
					Title:      "Canonical Film",
					Resolution: test.resolution,
					Year:       2026,
				},
				Identity: api.ExternalIdentity{
					SourcePath: state.SourcePath,
					Generation: 1,
					Category:   api.CanonicalCategoryMovie,
					TMDBID:     1,
				},
				ProviderMetadata: api.SourceScopedMetadata{
					SourcePath: state.SourcePath,
					Generation: 1,
					TMDB: &api.TMDBMetadata{
						TMDBID:   1,
						Category: "MOVIE",
						Title:    "Example Film",
						Year:     2026,
					},
				},
				LanguageFacts: mediafacts.ResolveLanguages(api.MediaFacts{
					OriginalLanguage:      test.original,
					AudioAbsent:           test.tracks == "",
					TrackCoverageComplete: true,
					Tracks:                tracks,
					PrimaryAudioTrackID:   primaryID,
				}),
			}
			if test.name == "unknown track language" && subject.LanguageFacts.ProgrammeStatus != api.MetadataEvidenceStatusPartial {
				t.Fatalf("unknown language became resolved: %+v", subject.LanguageFacts)
			}
			before := subject.GeneratedName.Clone()
			registry := trackers.NewRegistry()
			if err := registry.Register(unit3d.NewWithProfile(aither.Profile())); err != nil {
				t.Fatal(err)
			}
			descriptor, ok := registry.LookupDescriptor("AITHER")
			if !ok {
				t.Fatal("AITHER descriptor missing")
			}
			prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
				Tracker: "AITHER", Meta: subject,
			}, descriptor.ReleaseNamePolicy)
			if failure != nil {
				t.Fatal(failure)
			}
			got, err := prepared.ReviewedUploadName()
			if err != nil || got != test.want {
				t.Fatalf("reviewed name = %q, %v; want %q", got, err, test.want)
			}
			if !reflect.DeepEqual(before, subject.GeneratedName) {
				t.Fatal("AITHER projection changed canonical components")
			}
		})
	}
}
