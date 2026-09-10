// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"errors"
	"slices"
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

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
	tracks, audio, subtitles, err := mediaTrackFacts(preparationstate.State{SourcePath: "Example.2026.mkv", VideoPath: "Example.2026.mkv"}, doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 3 || tracks[0].ID == "" || tracks[0].ID == "Example.2026.mkv" {
		t.Fatalf("tracks = %#v", tracks)
	}
	if !slices.Equal(audio, []string{"English"}) || !slices.Equal(subtitles, []string{"English", "French"}) {
		t.Fatalf("aggregate languages = %#v/%#v", audio, subtitles)
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
	first, _, _, err := mediaTrackFacts(meta, doc)
	if err != nil {
		t.Fatal(err)
	}
	doc.Media.Track[0], doc.Media.Track[1] = doc.Media.Track[1], doc.Media.Track[0]
	second, _, _, err := mediaTrackFacts(meta, doc)
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
