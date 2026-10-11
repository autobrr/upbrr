// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"reflect"
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPreparedMediaFileFactsSurvivePersistenceAndExactProjection(t *testing.T) {
	path := writePreparedTestFile(t, "Example.Show.S01.1080p.WEB-DL-GRP.mkv", "synthetic media")
	raw := api.MediaFileFact{
		FileName:          "episode.mkv",
		Primary:           true,
		Source:            "old-source",
		Resolution:        "720p",
		VideoCodec:        "AVC",
		VideoTrackCount:   1,
		AudioLanguages:    []string{"Japanese"},
		SubtitleLanguages: []string{"English"},
		AudioStatus:       api.MetadataEvidenceStatusComplete,
		SubtitleStatus:    api.MetadataEvidenceStatusComplete,
	}
	state := preparationstate.State{
		MediaFileFacts:              api.MediaFileFacts{ExpectedFileCount: 1, Files: []api.MediaFileFact{raw}},
		SourcePath:                  path,
		VideoPath:                   path,
		AudioLanguages:              []string{"German"},
		AudioLanguagesProvenance:    api.FactProvenanceManual,
		SubtitleLanguagesProvenance: api.FactProvenanceManualEmpty,
		VideoCodec:                  "HEVC",
		ResolvedNaming:              preparationstate.ResolvedNaming{Source: "Web", Resolution: "1080p"},
	}
	facts := mapCollectedFacts(state)
	store := newMemoryStore()
	collector := &clientEvidenceTestCollector{base: recordingCollector{facts: &facts}}
	module := newTestModule(t, store, collector)
	input := api.PrepareInput{SourcePath: path}
	first, err := module.Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	restarted := newTestModule(t, store, collector)
	repeated, err := restarted.Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if collector.base.callCount() != 1 || repeated.Release.Generation != first.Release.Generation {
		t.Fatalf("unchanged generation=%d calls=%d", repeated.Release.Generation, collector.base.callCount())
	}
	subject, err := restarted.ResolveUploadSubject(t.Context(), api.UploadSubjectInput{Release: api.ReleaseRef{SourcePath: path, Generation: repeated.Release.Generation}})
	if err != nil {
		t.Fatal(err)
	}
	validation := api.NewTrackerValidationSubject(subject, "SP")
	rule := api.NewRuleSubject(subject)
	if !reflect.DeepEqual(validation.MediaFileFacts, rule.MediaFileFacts) || !reflect.DeepEqual(validation.MediaFileFacts, repeated.Release.Media.MediaFileFacts) {
		t.Fatal("persisted file facts lost across projection")
	}
	primary := validation.MediaFileFacts.Files[0]
	if primary.Source != "Web" || primary.Resolution != "1080p" || primary.VideoCodec != "HEVC" || !reflect.DeepEqual(primary.AudioLanguages, []string{"German"}) || len(primary.SubtitleLanguages) != 0 || primary.SubtitleStatus != api.MetadataEvidenceStatusPartial {
		t.Fatalf("primary corrections lost=%+v", primary)
	}
	validation.MediaFileFacts.Files[0].AudioLanguages[0] = "mutated"
	if subject.MediaFileFacts.Files[0].AudioLanguages[0] != "German" || repeated.Release.Media.MediaFileFacts.Files[0].AudioLanguages[0] != "German" {
		t.Fatal("file facts aliased prepared generation")
	}
	old := repeated.Release
	old.Compatibility.ContractVersion = "prepared-release-v32"
	old.Naming.Type = "REMUX"
	old.Media.MediaFileFacts = api.MediaFileFacts{}
	store.mu.Lock()
	store.current[canonicalSourceKey(path)] = old
	store.mu.Unlock()
	upgraded, err := newTestModule(t, store, collector).Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if upgraded.Release.Generation != old.Generation+1 || collector.base.callCount() != 2 || len(upgraded.Release.Media.MediaFileFacts.Files) != 1 {
		t.Fatalf("contract failed to invalidate generation=%d calls=%d", upgraded.Release.Generation, collector.base.callCount())
	}
}
