// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPrepareRecollectsBDInfoAudioLabelsAfterRestart(t *testing.T) {
	source := filepath.Join(t.TempDir(), "Example.Movie.2026.BluRay-GRP")
	playlistDir := filepath.Join(source, "BDMV", "PLAYLIST")
	if err := os.MkdirAll(playlistDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, "BDMV", "STREAM"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(playlistDir, "00001.MPLS"), []byte("synthetic playlist"), 0o600); err != nil {
		t.Fatal(err)
	}
	input := api.PrepareInput{SourcePath: source, Instructions: api.ReleaseFactInstructions{Playlist: api.PlaylistInstruction{Set: true, Selected: []string{"00001.MPLS"}}}}
	media := api.MediaFacts{Tracks: []api.MediaTrackFacts{{
		ID:                    "audio",
		Kind:                  api.MediaTrackAudio,
		ResourceID:            "resource",
		ManifestFingerprint:   "manifest",
		Codec:                 "TrueHD",
		Languages:             []string{"English"},
		LanguageProvenance:    api.FactProvenanceManual,
		AudioOrigin:           api.TrackAudioOriginal,
		AudioOriginProvenance: api.FactProvenanceManual,
	}}}
	media.LanguageFacts = mediafacts.ResolveLanguages(media)
	store := newMemoryStore()
	previous, err := newTestModule(t, store, &recordingCollector{}).Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	stale := previous.Release
	stale.Media = media
	stale.Compatibility.ContractVersion = "prepared-release-v71-bluray-listing-details"
	store.mu.Lock()
	store.current[canonicalSourceKey(source)] = stale
	store.mu.Unlock()

	media.Tracks[0].AudioLabel = "TrueHD 7.1 Atmos"
	media.LanguageFacts = mediafacts.ResolveLanguages(media)
	collector := &clientEvidenceTestCollector{base: recordingCollector{facts: &CollectedFacts{
		Media:       media,
		Disc:        stale.Disc,
		Naming:      stale.Naming,
		Assessments: stale.Assessments,
	}}}
	restarted := newTestModule(t, store, collector)
	required := input
	required.RequirePrepared, required.ExternalFreshness = true, api.ExternalFreshnessLoad
	if _, err := restarted.Prepare(t.Context(), required); err == nil {
		t.Fatal("old BDInfo generation remained reusable")
	}
	if collector.collectCount() != 0 {
		t.Fatal("required reuse recollected metadata")
	}
	current, err := restarted.Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if collector.collectCount() != 1 || current.Release.Generation != previous.Release.Generation+1 || current.Release.Compatibility.ContractVersion != ContractVersion {
		t.Fatal("old BDInfo facts were reused")
	}
	persisted, err := store.LoadPreparedRelease(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Media.LanguageFacts.Tracks) != 1 || persisted.Media.Tracks[0].AudioLabel != "TrueHD 7.1 Atmos" || persisted.Media.LanguageFacts.Tracks[0].AudioLabel != "TrueHD 7.1 Atmos" {
		t.Fatal("per-track labels were not persisted")
	}
	track := persisted.Media.Tracks[0]
	if track.ID != "audio" || track.ManifestFingerprint != "manifest" || track.LanguageProvenance != api.FactProvenanceManual || track.AudioOrigin != api.TrackAudioOriginal || track.AudioOriginProvenance != api.FactProvenanceManual {
		t.Fatal("refresh changed track identity or finalized corrections")
	}
	// A second restart must hydrate the repaired generation, not recollect it.
	restarted = newTestModule(t, store, collector)
	reused, err := restarted.Prepare(t.Context(), input)
	if err != nil || collector.collectCount() != 1 || reused.Release.Generation != current.Release.Generation || reused.Release.Media.Tracks[0].AudioLabel != "TrueHD 7.1 Atmos" {
		t.Fatalf("repaired facts not reused: err=%v", err)
	}
}
