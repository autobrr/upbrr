// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPrepareRecollectsPreProgrammeRoleFactsAfterRestart(t *testing.T) {
	for _, version := range []string{"prepared-release-v34", "prepared-release-v35", "prepared-release-v36"} {
		t.Run(version, func(t *testing.T) {
			path := writePreparedTestFile(t, "Example.Movie.2026.mkv", "synthetic media")
			store := newMemoryStore()
			input := api.PrepareInput{SourcePath: path}
			media := api.MediaFacts{
				OriginalLanguage:      "Japanese",
				TrackCoverageComplete: true,
				PrimaryAudioTrackID:   "english",
				Tracks: []api.MediaTrackFacts{
					{
						ID:        "english",
						Kind:      api.MediaTrackAudio,
						Role:      api.AudioRoleProgramme,
						Languages: []string{"English"},
					},
					{
						ID:        "japanese",
						Kind:      api.MediaTrackAudio,
						Title:     "Original",
						Languages: []string{"Japanese"},
					},
					{
						ID:        "commentary",
						Kind:      api.MediaTrackAudio,
						Role:      api.AudioRoleCommentary,
						Languages: []string{"English"},
					},
				},
			}
			media.LanguageFacts = mediafacts.ResolveLanguages(media)
			previous, err := newTestModule(t, store, &recordingCollector{facts: &CollectedFacts{Media: media}}).Prepare(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			stale := previous.Release
			stale.Compatibility.ContractVersion = version
			store.mu.Lock()
			store.current[canonicalSourceKey(path)] = stale
			store.mu.Unlock()

			media.Tracks[1].Role = api.AudioRoleProgramme
			media.LanguageFacts = mediafacts.ResolveLanguages(media)
			collector := &clientEvidenceTestCollector{base: recordingCollector{facts: &CollectedFacts{Media: media}}}
			restarted := newTestModule(t, store, collector)
			current, err := restarted.Prepare(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			if collector.collectCount() != 1 || current.Release.Generation != previous.Release.Generation+1 ||
				current.Release.Compatibility.ContractVersion != ContractVersion {
				t.Fatalf("cached pre-role facts reused: generation=%d calls=%d contract=%s",
					current.Release.Generation, collector.collectCount(), current.Release.Compatibility.ContractVersion)
			}
			persisted, err := store.LoadPreparedRelease(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			facts := persisted.Media.LanguageFacts
			if len(persisted.Media.Tracks) != 3 || persisted.Media.Tracks[1].Role != api.AudioRoleProgramme ||
				persisted.Media.Tracks[2].Role != api.AudioRoleCommentary || facts.ProgrammeStatus != api.MetadataEvidenceStatusComplete ||
				!slices.Equal(facts.ProgrammeLanguages, []string{"English", "Japanese"}) || !facts.HasOriginalAudio() || !facts.HasEnglishDub() {
				t.Fatalf("recollected programme facts not persisted: media=%+v", persisted.Media)
			}
			reused, err := restarted.Prepare(t.Context(), input)
			if err != nil || collector.collectCount() != 1 || reused.Release.Generation != current.Release.Generation {
				t.Fatalf("fresh role facts not reused: generation=%d calls=%d err=%v", reused.Release.Generation, collector.collectCount(), err)
			}
		})
	}
}
