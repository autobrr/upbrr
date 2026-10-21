// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPrepareRecollectsResourceEvidenceAfterRestart(t *testing.T) {
	path := writePreparedTestFile(t, "Example.Movie.2026.mkv", "synthetic media")
	store := newMemoryStore()
	input := api.PrepareInput{SourcePath: path}
	media := api.MediaFacts{
		OriginalLanguage:      "English",
		TrackCoverageComplete: true,
		PrimaryAudioTrackID:   "main",
		Tracks: []api.MediaTrackFacts{{
			ID:        "main",
			Kind:      api.MediaTrackAudio,
			Role:      api.AudioRoleProgramme,
			Languages: []string{"English"},
		}},
	}
	media.LanguageFacts = mediafacts.ResolveLanguages(media)
	previous, err := newTestModule(t, store, &recordingCollector{facts: &CollectedFacts{Media: media}}).Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	stale := previous.Release
	stale.Compatibility.ContractVersion = "prepared-release-v42"
	store.mu.Lock()
	store.current[canonicalSourceKey(path)] = stale
	store.mu.Unlock()

	media.AnimeKnown = true
	media.VideoEncoder = "x264"
	media.MediaFileFacts.Files = []api.MediaFileFact{{
		ResourceID:          "resource-a",
		VideoEncoder:        "x264",
		Resolution:          "1080p",
		EncodeSettingsKnown: true,
		HasEncodeSettings:   true,
	}}
	media.Tracks[0].BitDepth = 24
	media.Tracks[0].ChannelsKnown = true
	media.LanguageFacts = mediafacts.ResolveLanguages(media)
	collector := &clientEvidenceTestCollector{base: recordingCollector{facts: &CollectedFacts{Media: media}}}
	restarted := newTestModule(t, store, collector)
	current, err := restarted.Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if collector.collectCount() != 1 || current.Release.Generation != previous.Release.Generation+1 || current.Release.Compatibility.ContractVersion != ContractVersion {
		t.Fatalf("pre-depth facts reused: generation=%d calls=%d contract=%s", current.Release.Generation, collector.collectCount(), current.Release.Compatibility.ContractVersion)
	}
	persisted, err := store.LoadPreparedRelease(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Media.Tracks) != 1 || persisted.Media.Tracks[0].BitDepth != 24 || persisted.Media.LanguageFacts.Tracks[0].BitDepth != 24 ||
		!persisted.Media.Tracks[0].ChannelsKnown || !persisted.Media.LanguageFacts.Tracks[0].ChannelsKnown || !persisted.Media.AnimeKnown || persisted.Media.VideoEncoder != "x264" || len(persisted.Media.MediaFileFacts.Files) != 1 || persisted.Media.MediaFileFacts.Files[0].ResourceID != "resource-a" {
		t.Fatalf("audio sample depth was not persisted: %+v", persisted.Media)
	}
	reused, err := restarted.Prepare(t.Context(), input)
	if err != nil || collector.collectCount() != 1 || reused.Release.Generation != current.Release.Generation {
		t.Fatalf("fresh depth facts not reused: generation=%d calls=%d err=%v", reused.Release.Generation, collector.collectCount(), err)
	}
}
