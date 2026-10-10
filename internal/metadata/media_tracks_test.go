// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestMediaTrackFactsMeasuredStreamOrder(t *testing.T) {
	t.Parallel()
	doc := mediaInfoDoc{}
	doc.Media.Track = []map[string]any{
		{
			"@type":       "Audio",
			"StreamOrder": "3",
			"Language":    "eng",
			"Title":       "Dub",
		},
		{
			"@type":       "Text",
			"StreamOrder": "2",
			"Language":    "eng",
		},
		{
			"@type":       "Audio",
			"StreamOrder": "1",
			"Language":    "jpn",
			"Title":       "Main",
		},
	}
	tracks, _, _, _, err := mediaTrackFacts(preparationstate.State{}, doc)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []int{3, 2, 1} {
		if !tracks[i].StreamOrderKnown || tracks[i].StreamOrder != want {
			t.Fatalf("track %d measured order = %+v, want %d", i, tracks[i], want)
		}
	}
	if tracks[0].Ordinal != 1 || tracks[1].Ordinal != 1 || tracks[2].Ordinal != 2 {
		t.Fatalf("per-kind identity ordinals changed: %+v", tracks)
	}
}

func TestMediaTrackFactsStreamOrderEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		value any
		want  int
		known bool
	}{
		{
			name:  "zero",
			value: "0",
			known: true,
		},
		{
			name:  "integer",
			value: 3,
			want:  3,
			known: true,
		},
		{
			name:  "JSON number",
			value: float64(4),
			want:  4,
			known: true,
		},
		{
			name:  "number token",
			value: json.Number("5"),
			want:  5,
			known: true,
		},
		{
			name:  "trimmed",
			value: " 6 ",
			want:  6,
			known: true,
		},
		{name: "missing"},
		{name: "empty", value: ""},
		{name: "negative", value: "-1"},
		{name: "fraction", value: "1.5"},
		{name: "fractional number", value: float64(1.5)},
		{name: "multiple streams", value: "1 / 2"},
		{name: "compound stream", value: "0-1"},
		{name: "labelled value", value: "1 (0x1)"},
		{name: "overflow", value: "99999999999999999999999999999999"},
	} {
		t.Run(test.name, func(t *testing.T) {
			doc := mediaInfoDoc{}
			track := map[string]any{
				"@type":    "Audio",
				"ID":       "1",
				"UniqueID": "2",
			}
			if test.value != nil {
				track["StreamOrder"] = test.value
			}
			doc.Media.Track = []map[string]any{track}
			tracks, _, _, _, err := mediaTrackFacts(preparationstate.State{}, doc)
			if err != nil {
				t.Fatal(err)
			}
			if tracks[0].StreamOrderKnown != test.known || tracks[0].StreamOrder != test.want {
				t.Fatalf("measured order = %+v, want %d known=%t", tracks[0], test.want, test.known)
			}
		})
	}
}

func TestMediaTrackFactsDuplicateStreamOrderIsUnknown(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"Audio", "Text", "Video"} {
		t.Run(kind, func(t *testing.T) {
			doc := mediaInfoDoc{}
			doc.Media.Track = []map[string]any{
				{
					"@type":       "Audio",
					"StreamOrder": "1",
					"ID":          "2",
				},
				{
					"@type":       kind,
					"StreamOrder": "1",
					"ID":          "3",
				},
			}
			tracks, _, _, _, err := mediaTrackFacts(preparationstate.State{}, doc)
			if err != nil {
				t.Fatal(err)
			}
			for _, track := range tracks {
				if track.StreamOrderKnown {
					t.Fatalf("duplicate container order trusted: %+v", tracks)
				}
			}
		})
	}
}

func TestMediaTrackFactsExcludesCommentaryFromAudioAggregate(t *testing.T) {
	t.Parallel()

	doc := mediaInfoDoc{}
	doc.Media.Track = []map[string]any{
		{
			"@type":       "Audio",
			"StreamOrder": "1",
			"Language":    "eng",
		},
		{
			"@type":       "Audio",
			"StreamOrder": "2",
			"Language":    "jpn",
			"Title":       "Commentary",
		},
		{
			"@type":       "Text",
			"StreamOrder": "3",
			"Language":    "en, fra",
		},
	}
	tracks, primaryAudioTrackID, audio, subtitles, err := mediaTrackFacts(
		preparationstate.State{SourcePath: "Example.2026.mkv", VideoPath: "Example.2026.mkv"},
		doc,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 3 || tracks[0].ID == "" || tracks[0].ID == "Example.2026.mkv" {
		t.Fatalf("tracks = %#v", tracks)
	}
	if primaryAudioTrackID != tracks[0].ID {
		t.Fatalf("primary audio track ID = %q, want %q", primaryAudioTrackID, tracks[0].ID)
	}
	if !slices.Equal(audio, []string{"English"}) || !slices.Equal(subtitles, []string{"English", "French"}) {
		t.Fatalf("aggregate languages = %#v/%#v", audio, subtitles)
	}
}

func TestMediaTrackFactsPrimaryIdentityUsesExistingPolicyWithDuplicateNativeIDs(t *testing.T) {
	t.Parallel()

	doc := mediaInfoDoc{}
	doc.Media.Track = []map[string]any{
		{
			"@type":        "Audio",
			"StreamOrder":  "0",
			"ID":           "7",
			"Title":        "Director Commentary",
			"Format":       "AAC",
			"Channels":     "2",
			"SamplingRate": "48000",
		},
		{
			"@type":         "Audio",
			"StreamOrder":   "2",
			"ID":            "7",
			"Title":         "Main",
			"Format":        "FLAC",
			"ChannelLayout": "L R",
			"Channels":      "2",
			"SamplingRate":  "96000",
		},
		{
			"@type":        "Audio",
			"StreamOrder":  "1",
			"ID":           "7",
			"Title":        "Main Alternate",
			"Format":       "AC-3",
			"Channels":     "6",
			"SamplingRate": "48000",
		},
	}
	tracks, primaryID, _, _, err := mediaTrackFacts(
		preparationstate.State{SourcePath: "Example.2026.mkv", VideoPath: "Example.2026.mkv"},
		doc,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 3 || primaryID != tracks[2].ID || tracks[0].ID == tracks[1].ID || tracks[1].ID == tracks[2].ID {
		t.Fatalf("tracks=%#v primary=%q", tracks, primaryID)
	}
	if tracks[1].Title != "Main" || tracks[1].Codec != "FLAC" || tracks[1].ChannelLayout != "L R" ||
		tracks[1].Channels != 2 || tracks[1].SampleRate != 96_000 {
		t.Fatalf("audio facts = %#v", tracks[1])
	}
}

func TestMediaTrackFactsNormalizesDolbyCodecsForAnalysisBinding(t *testing.T) {
	t.Parallel()

	doc := mediaInfoDoc{Media: struct {
		Track []map[string]any `json:"track"`
	}{Track: []map[string]any{
		{
			"@type":       "Audio",
			"Format":      "AC-3",
			"StreamOrder": "1",
		},
		{
			"@type":       "Audio",
			"Format":      "E-AC-3",
			"StreamOrder": "2",
		},
	}}}
	tracks, _, _, _, err := mediaTrackFacts(
		preparationstate.State{SourcePath: "Example.2026.mkv", VideoPath: "Example.2026.mkv"},
		doc,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 2 || tracks[0].Codec != "DD" || tracks[1].Codec != "DD+" {
		t.Fatalf("Dolby track facts = %#v", tracks)
	}
}

func TestSelectPrimaryAudioTrackFallsBackToNumericIDThenSourceOrder(t *testing.T) {
	t.Parallel()

	byID := []map[string]any{
		{
			"@type": "Audio",
			"ID":    "9",
			"Title": "Compatibility",
		},
		{
			"@type": "Audio",
			"ID":    "3",
			"Title": "Commentary",
		},
	}
	if index := selectPrimaryAudioTrackIndex(byID); index != 1 {
		t.Fatalf("numeric ID primary index = %d, want 1", index)
	}
	bySourceOrder := []map[string]any{
		{"@type": "Audio", "Title": "Main A"},
		{"@type": "Audio", "Title": "Main B"},
	}
	if index := selectPrimaryAudioTrackIndex(bySourceOrder); index != 0 {
		t.Fatalf("source-order primary index = %d, want 0", index)
	}
}

func TestApplyTrackLanguageOverridesRequiresCurrentManifest(t *testing.T) {
	t.Parallel()

	meta := preparationstate.State{MediaTracks: []api.MediaTrackFacts{{
		ID:                  "track_1",
		Kind:                api.MediaTrackAudio,
		ManifestFingerprint: "manifest_1",
		Languages:           []string{"English"},
	}}}
	err := applyTrackLanguageOverrides(&meta, []api.TrackLanguageCorrection{{
		TrackID:             "track_1",
		ManifestFingerprint: "other",
		Languages:           []string{"fra"},
	}})
	if _, ok := errors.AsType[*api.CorrectionConflictError](err); !ok {
		t.Fatalf("manifest error = %v, want correction conflict", err)
	}
	if err := applyTrackLanguageOverrides(&meta, []api.TrackLanguageCorrection{{
		TrackID:             "track_1",
		ManifestFingerprint: "manifest_1",
		Languages:           []string{"fra"},
	}}); err != nil {
		t.Fatalf("apply override: %v", err)
	}
	if !slices.Equal(meta.MediaTracks[0].Languages, []string{"French"}) || meta.MediaTracks[0].LanguageProvenance != api.FactProvenanceManual {
		t.Fatalf("track = %#v", meta.MediaTracks[0])
	}
}

func TestTrackOrdinalBindingChangesWithOrderedScanEvidence(t *testing.T) {
	t.Parallel()
	meta := preparationstate.State{
		SourcePath:        "Example.mkv",
		VideoPath:         "Example.mkv",
		SourceFingerprint: "source-one",
	}
	doc := mediaInfoDoc{}
	doc.Media.Track = []map[string]any{{"@type": "Audio", "Language": "eng"}, {"@type": "Audio", "Language": "fra"}}
	first, _, _, _, err := mediaTrackFacts(meta, doc)
	if err != nil {
		t.Fatal(err)
	}
	doc.Media.Track[0], doc.Media.Track[1] = doc.Media.Track[1], doc.Media.Track[0]
	second, _, _, _, err := mediaTrackFacts(meta, doc)
	if err != nil {
		t.Fatal(err)
	}
	if first[0].ID == second[0].ID || first[0].ManifestFingerprint == second[0].ManifestFingerprint {
		t.Fatal("reordered stream inherited an ordinal correction")
	}
	meta.MediaTracks = second
	if err := applyTrackLanguageOverrides(&meta, []api.TrackLanguageCorrection{{
		TrackID:             first[0].ID,
		ManifestFingerprint: first[0].ManifestFingerprint,
		Languages:           []string{"Spanish"},
	}}); err == nil {
		t.Fatal("old scan correction applied")
	}
}

func TestEmptyTrackCorrectionClearsDetectedAggregate(t *testing.T) {
	t.Parallel()
	meta := preparationstate.State{
		AudioLanguages: []string{"English"},
		MediaTracks: []api.MediaTrackFacts{{
			ID:                  "audio-one",
			Kind:                api.MediaTrackAudio,
			ManifestFingerprint: "scan",
			Languages:           []string{"English"},
		}},
		MetadataOverrides: api.MetadataOverrides{TrackLanguages: []api.TrackLanguageCorrection{{
			TrackID:             "audio-one",
			ManifestFingerprint: "scan",
			Languages:           []string{},
		}}},
	}
	if err := applyMetadataOverrides(&meta); err != nil {
		t.Fatal(err)
	}
	if len(meta.AudioLanguages) != 0 {
		t.Fatal("empty track correction restored detected aggregate")
	}
}

func TestUntaggedMediaInfoTracksPreserveBDInfoLanguages(t *testing.T) {
	meta := preparationstate.State{
		DiscType:          "BDMV",
		AudioLanguages:    []string{"Japanese"},
		SubtitleLanguages: []string{"English"},
		MediaTracks: []api.MediaTrackFacts{
			{Kind: api.MediaTrackAudio, LanguageProvenance: api.FactProvenanceAutomatic},
			{Kind: api.MediaTrackSubtitle, LanguageProvenance: api.FactProvenanceAutomatic},
		},
	}
	if err := applyMetadataOverrides(&meta); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(meta.AudioLanguages, []string{"Japanese"}) || !slices.Equal(meta.SubtitleLanguages, []string{"English"}) {
		t.Fatalf("BDInfo fallback lost: audio=%v subtitles=%v", meta.AudioLanguages, meta.SubtitleLanguages)
	}
}

func TestOrdinaryAudioLanguageDoesNotRequireRecognizedTitle(t *testing.T) {
	var doc mediaInfoDoc
	doc.Media.Track = []map[string]any{
		{
			"@type":    "Audio",
			"ID":       "1",
			"Language": "ja",
			"Default":  "Yes",
		},
		{
			"@type":    "Audio",
			"ID":       "2",
			"Language": "en",
			"Title":    "Unclassified supplementary recording",
		},
		{
			"@type":    "Audio",
			"ID":       "3",
			"Language": "de",
			"Title":    "German Dub",
		},
	}
	tracks, _, _, _, err := mediaTrackFacts(preparationstate.State{}, doc)
	if err != nil {
		t.Fatal(err)
	}
	if tracks[0].Role != api.AudioRoleProgramme || tracks[1].Role != api.AudioRoleProgramme || tracks[2].Role != api.AudioRoleProgramme {
		t.Fatalf("track roles=%#v", tracks)
	}
}

func TestDefaultAudioNamingRejectsMissingInspectedCodec(t *testing.T) {
	for _, channels := range []string{"", "2"} {
		t.Run(channels, func(t *testing.T) {
			doc := mediaInfoDoc{}
			doc.Media.Track = []map[string]any{{
"@type": "Audio",
 "Default": "Yes",
 "Title": "Main",
 "Language": "eng",
 "Channels": channels,
}}
			facts, _, _, _, err := mediaTrackFacts(preparationstate.State{SourcePath: "source", VideoPath: "source"}, doc)
			if err != nil {
				t.Fatal(err)
			}
			if len(facts) != 1 || facts[0].Codec != "" || facts[0].AudioLabel == "" {
				t.Fatalf("unexpected producer evidence: %+v", facts)
			}
			name := BuildReleaseName(api.ReleaseNameRequest{
Category: "MOVIE",
 Type: "WEBDL",
 Source: "WEB",
 Resolution: "1080p",
 VideoEncode: "H.265",
 Tag: "-GRP",
 Title: "Example",
 Year: 2026,
 Audio: "AAC 2.0",
}, api.NopLogger{})
			subject := api.UploadSubject{
ReleaseName: name.Name,
 GeneratedName: name.GeneratedName,
 LanguageFacts: api.LanguageFacts{Tracks: facts},
}
			binding := trackers.StructuredReleaseNamePolicy("test/default-audio/v1", trackers.StructuredNamePolicy{Defaults: func(editor *trackers.NameEditor, meta api.UploadSubject, _ config.TrackerConfig) error {
				return trackers.ApplyDefaultAudioName(editor, meta)
			}})
			_, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{Tracker: "DP", Meta: subject}, binding)
			var rule *trackers.NameRuleError
			if failure == nil || !errors.As(failure, &rule) {
				t.Fatalf("missing codec was accepted for default naming: %+v %v cause=%v", facts, failure, failure.Unwrap())
			}
		})
	}
}
