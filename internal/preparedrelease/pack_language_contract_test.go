// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/autobrr/upbrr/internal/languageutil"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPrepareReusesCurrentPartialPackLanguageFactsAfterRestart(t *testing.T) {
	path := t.TempDir()
	files := []string{filepath.Join(path, "Example.S01E01.mkv"), filepath.Join(path, "Example.S01E02.mkv")}
	for _, file := range files {
		if err := os.WriteFile(file, []byte("synthetic media"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	media := api.MediaFacts{
		OriginalLanguage:    "Japanese",
		PrimaryAudioTrackID: "english",
		Tracks: []api.MediaTrackFacts{
			{
				ID:                  "english",
				Kind:                api.MediaTrackAudio,
				Role:                api.AudioRoleProgramme,
				Languages:           []string{"English"},
				ResourceID:          "selected-resource",
				ManifestFingerprint: "selected-report",
				LanguageProvenance:  api.FactProvenanceManual,
			},
			{
				ID:                  "japanese",
				Kind:                api.MediaTrackAudio,
				Role:                api.AudioRoleProgramme,
				Languages:           []string{"Japanese"},
				ResourceID:          "selected-resource",
				ManifestFingerprint: "selected-report",
			},
		},
	}
	media.LanguageFacts = mediafacts.ResolveLanguages(media)
	store := newMemoryStore()
	collector := &recordingCollector{facts: &CollectedFacts{
		Media: media,
		Resources: CollectedResources{
			SourcePath: path,
			VideoPath:  files[0],
			FileList:   files,
		},
	}}
	input := api.PrepareInput{SourcePath: path}
	previous, err := newTestModule(t, store, collector).Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if previous.Release.Compatibility.ContractVersion != ContractVersion ||
		previous.Release.Media.LanguageFacts.ProgrammeStatus != api.MetadataEvidenceStatusPartial ||
		previous.Release.Media.TrackCoverageComplete || previous.Release.Media.LanguageFacts.TrackCoverageComplete {
		t.Fatalf("fixture lost current partial pack evidence: %+v", previous.Release)
	}
	restartedCollector := &clientEvidenceTestCollector{}
	restarted := newTestModule(t, store, restartedCollector)
	reused, err := restarted.Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if restartedCollector.collectCount() != 0 || reused.Release.Generation != previous.Release.Generation ||
		reused.Release.Compatibility != previous.Release.Compatibility || !reflect.DeepEqual(reused.Release.Media, previous.Release.Media) {
		t.Fatalf("restart recollected or changed current facts: calls=%d release=%+v", restartedCollector.collectCount(), reused.Release)
	}
	subject, err := restarted.ResolveUploadSubject(t.Context(), api.UploadSubjectInput{
		Release: api.ReleaseRef{SourcePath: path, Generation: reused.Release.Generation},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(subject.LanguageFacts, previous.Release.Media.LanguageFacts) || restartedCollector.collectCount() != 0 {
		t.Fatal("projection changed partial canonical language evidence or recollected it")
	}

	stale := reused.Release
	stale.Compatibility.ContractVersion = "prepared-release-v56"
	stale.Media.SubtitleLanguages = []string{"phi"}
	store.mu.Lock()
	store.current[canonicalSourceKey(path)] = stale
	store.mu.Unlock()
	media.SubtitleLanguages = languageutil.NormalizeLanguageList([]string{"phi"})
	media.LanguageFacts = mediafacts.ResolveLanguages(media)
	updatedCollector := &clientEvidenceTestCollector{base: recordingCollector{facts: &CollectedFacts{
		Media: media,
		Resources: CollectedResources{
			SourcePath: path,
			VideoPath:  files[0],
			FileList:   files,
		},
	}}}
	updated := newTestModule(t, store, updatedCollector)
	current, err := updated.Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if updatedCollector.collectCount() != 1 || current.Release.Generation != previous.Release.Generation+1 ||
		current.Release.Compatibility.ContractVersion != ContractVersion ||
		!reflect.DeepEqual(current.Release.Media.SubtitleLanguages, []string{"Philippine languages"}) ||
		!reflect.DeepEqual(current.Release.Media.Tracks, previous.Release.Media.Tracks) {
		t.Fatalf("old language semantics reused or manual/source binding changed: generation=%d calls=%d media=%+v", current.Release.Generation, updatedCollector.collectCount(), current.Release.Media)
	}
	if _, err := updated.Prepare(t.Context(), input); err != nil || updatedCollector.collectCount() != 1 {
		t.Fatalf("current language generation was not reused: calls=%d err=%v", updatedCollector.collectCount(), err)
	}
}
