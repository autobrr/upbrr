// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPrepareReusesV37PartialPackLanguageFactsAfterRestart(t *testing.T) {
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
	if previous.Release.Compatibility.ContractVersion != "prepared-release-v37" ||
		previous.Release.Media.LanguageFacts.ProgrammeStatus != api.MetadataEvidenceStatusPartial ||
		previous.Release.Media.TrackCoverageComplete || previous.Release.Media.LanguageFacts.TrackCoverageComplete {
		t.Fatalf("fixture lost existing v37 partial pack evidence: %+v", previous.Release)
	}
	restartedCollector := &clientEvidenceTestCollector{}
	restarted := newTestModule(t, store, restartedCollector)
	reused, err := restarted.Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if restartedCollector.collectCount() != 0 || reused.Release.Generation != previous.Release.Generation ||
		reused.Release.Compatibility != previous.Release.Compatibility || !reflect.DeepEqual(reused.Release.Media, previous.Release.Media) {
		t.Fatalf("tracker eligibility upgrade recollected or changed v37 facts: calls=%d release=%+v", restartedCollector.collectCount(), reused.Release)
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
}
