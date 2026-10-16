// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestPrepareRecollectsPreAudioAliasFactsAfterRestart(t *testing.T) {
	path := writePreparedTestFile(t, "Example.Movie.2026.mkv", "synthetic media")
	store := newMemoryStore()
	input := api.PrepareInput{SourcePath: path}
	oldFacts := &CollectedFacts{Media: api.MediaFacts{
		Audio: "DDP 5.1",
		Tracks: []api.MediaTrackFacts{{
			ID:    "audio",
			Kind:  api.MediaTrackAudio,
			Codec: "DDP",
		}},
	}}
	previous, err := newTestModule(t, store, &recordingCollector{facts: oldFacts}).Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	stale := previous.Release
	stale.Compatibility.ContractVersion = "prepared-release-v33"
	store.mu.Lock()
	store.current[canonicalSourceKey(path)] = stale
	store.mu.Unlock()

	collector := &clientEvidenceTestCollector{base: recordingCollector{facts: &CollectedFacts{Media: api.MediaFacts{
		Audio: "DD+ 5.1",
		Tracks: []api.MediaTrackFacts{{
			ID:    "audio",
			Kind:  api.MediaTrackAudio,
			Codec: "DD+",
		}},
	}}}}
	restarted := newTestModule(t, store, collector)
	current, err := restarted.Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if collector.collectCount() != 1 || current.Release.Generation != previous.Release.Generation+1 ||
		current.Release.Compatibility.ContractVersion != ContractVersion || current.Release.Media.Audio != "DD+ 5.1" ||
		len(current.Release.Media.Tracks) != 1 || current.Release.Media.Tracks[0].Codec != "DD+" {
		t.Fatalf("cached pre-alias facts reused: generation=%d calls=%d media=%+v", current.Release.Generation, collector.collectCount(), current.Release.Media)
	}
	reused, err := restarted.Prepare(t.Context(), input)
	if err != nil || collector.collectCount() != 1 || reused.Release.Generation != current.Release.Generation {
		t.Fatalf("normalized facts not reused: generation=%d calls=%d err=%v", reused.Release.Generation, collector.collectCount(), err)
	}
}
