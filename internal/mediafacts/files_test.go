// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediafacts

import (
	"reflect"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveFilesKeepsPrimaryCorrectionsAndClears(t *testing.T) {
	raw := api.MediaFileFact{
		FileName:          "episode1.mkv",
		Primary:           true,
		Container:         "mkv",
		Source:            "Web",
		Resolution:        "1080p",
		VideoCodec:        "AVC",
		VideoEncode:       "H.264",
		BitDepth:          "8",
		VideoTrackCount:   1,
		AudioLanguages:    []string{"Japanese"},
		SubtitleLanguages: []string{"English"},
		AudioStatus:       api.MetadataEvidenceStatusComplete,
		SubtitleStatus:    api.MetadataEvidenceStatusComplete,
	}
	second := raw
	second.FileName, second.Primary = "episode2.mkv", false
	for _, clear := range []bool{false, true} {
		t.Run(map[bool]string{false: "corrected", true: "cleared"}[clear], func(t *testing.T) {
			media := api.MediaFacts{
				MediaFileFacts:              api.MediaFileFacts{ExpectedFileCount: 2, Files: []api.MediaFileFact{raw, second}},
				Container:                   "mp4",
				Source:                      "BluRay",
				VideoCodec:                  "HEVC",
				BitDepth:                    "10",
				AudioLanguages:              []string{"German"},
				SubtitleLanguages:           []string{"French"},
				OriginalLanguage:            "German",
				AudioLanguagesProvenance:    api.FactProvenanceManual,
				SubtitleLanguagesProvenance: api.FactProvenanceManual,
			}
			resolution := "2160p"
			if clear {
				media.Container, media.Source, media.VideoCodec, media.BitDepth, resolution = "", "", "", "", ""
				media.OriginalLanguage = ""
				media.AudioLanguages, media.SubtitleLanguages = nil, nil
				media.AudioLanguagesProvenance, media.SubtitleLanguagesProvenance = api.FactProvenanceManualEmpty, api.FactProvenanceManualEmpty
			}
			got := ResolveFiles(media, resolution)
			primary := got.Files[0]
			if got.OriginalLanguage != media.OriginalLanguage || primary.Container != media.Container || primary.Source != media.Source || primary.Resolution != resolution || primary.VideoCodec != media.VideoCodec || primary.BitDepth != media.BitDepth || !reflect.DeepEqual(primary.AudioLanguages, media.AudioLanguages) || !reflect.DeepEqual(primary.SubtitleLanguages, media.SubtitleLanguages) {
				t.Fatalf("stale primary=%+v", primary)
			}
			if !reflect.DeepEqual(got.Files[1], second) {
				t.Fatalf("primary correction changed other file=%+v", got.Files[1])
			}
			if clear && got.Status != api.MetadataEvidenceStatusPartial {
				t.Fatalf("clear was complete=%+v", got)
			}
			got.Files[1].AudioLanguages[0] = "mutated"
			if raw.AudioLanguages[0] != "Japanese" {
				t.Fatal("projection aliased raw evidence")
			}
		})
	}
}

func TestResolveFilesKeepsUnknownAndClearedTrackEvidence(t *testing.T) {
	for _, languages := range [][]string{nil, {"Undetermined"}, {"German"}} {
		media := api.MediaFacts{
			MediaFileFacts: api.MediaFileFacts{ExpectedFileCount: 1, Files: []api.MediaFileFact{{Primary: true, VideoTrackCount: 1}}},
			Tracks:         []api.MediaTrackFacts{{Kind: api.MediaTrackAudio, Languages: languages}},
			AudioLanguages: languages,
		}
		got := ResolveFiles(media, "1080p")
		want := api.MetadataEvidenceStatusPartial
		if len(languages) > 0 && languages[0] == "German" {
			want = api.MetadataEvidenceStatusComplete
		}
		if got.Files[0].AudioStatus != want {
			t.Fatalf("languages=%v status=%s", languages, got.Files[0].AudioStatus)
		}
	}
}

func TestResolveFilesCannotRepairUninspectedTracksWithAggregateCorrections(t *testing.T) {
	media := api.MediaFacts{
		MediaFileFacts: api.MediaFileFacts{ExpectedFileCount: 1, Files: []api.MediaFileFact{{
			Primary:         true,
			VideoTrackCount: 1,
			AudioStatus:     api.MetadataEvidenceStatusUnavailable,
		}}},
		AudioLanguages:           []string{"German"},
		AudioLanguagesProvenance: api.FactProvenanceManual,
	}
	if got := ResolveFiles(media, "1080p"); got.Files[0].AudioStatus != api.MetadataEvidenceStatusUnavailable {
		t.Fatalf("uninspected track was made complete: %+v", got.Files[0])
	}
}

func TestResolveFilesNormalizesPrimaryTransportStreamContainer(t *testing.T) {
	media := api.MediaFacts{Container: "m2ts", MediaFileFacts: api.MediaFileFacts{ExpectedFileCount: 2, Files: []api.MediaFileFact{
		{
FileName: "episode1.m2ts",
 Primary: true,
 Container: "ts",
 VideoTrackCount: 1,
},
		{
FileName: "episode2.m2ts",
 Container: "ts",
 VideoTrackCount: 1,
},
	}}}
	got := ResolveFiles(media, "1080p")
	if got.Files[0].Container != "ts" || got.Files[1].Container != "ts" {
		t.Fatalf("same transport-stream format differs: %+v", got.Files)
	}
	if media.Container != "m2ts" {
		t.Fatal("pack comparison normalization changed canonical naming container")
	}
}
