// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"reflect"
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPreparedDefaultFactsSurvivePersistenceAndExactProjection(t *testing.T) {
	t.Parallel()
	path := writePreparedTestFile(t, "Example.Film.2026.1080p.BluRay.REMUX-GRP.mkv", "synthetic media")
	tracks := []api.MediaTrackFacts{
		{
			ID:           "audio-yes",
			Kind:         api.MediaTrackAudio,
			Default:      true,
			DefaultKnown: true,
		},
		{
			ID:           "subtitle-no",
			Kind:         api.MediaTrackSubtitle,
			DefaultKnown: true,
		},
		{ID: "audio-missing", Kind: api.MediaTrackAudio},
		{
			ID:      "subtitle-legacy",
			Kind:    api.MediaTrackSubtitle,
			Default: true,
		},
	}
	facts := mapCollectedFacts(preparationstate.State{
		SourcePath:  path,
		VideoPath:   path,
		MediaTracks: tracks,
	})
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
	subject, err := restarted.ResolveUploadSubject(t.Context(), api.UploadSubjectInput{Release: api.ReleaseRef{
		SourcePath: path,
		Generation: repeated.Release.Generation,
	}})
	if err != nil {
		t.Fatal(err)
	}
	validation := api.NewTrackerValidationSubject(subject, "PTP")
	rule := api.NewRuleSubject(subject)
	for name, projected := range map[string][]api.MediaTrackFacts{
		"prepared media":     repeated.Release.Media.Tracks,
		"prepared languages": repeated.Release.Media.LanguageFacts.Tracks,
		"upload":             subject.LanguageFacts.Tracks,
		"validation":         validation.LanguageFacts.Tracks,
		"rule":               rule.LanguageFacts.Tracks,
	} {
		if !reflect.DeepEqual(projected, tracks) {
			t.Fatalf("%s default evidence changed: got %+v, want %+v", name, projected, tracks)
		}
	}
	validation.LanguageFacts.Tracks[0].DefaultKnown = false
	if !subject.LanguageFacts.Tracks[0].DefaultKnown || !repeated.Release.Media.Tracks[0].DefaultKnown {
		t.Fatal("projected default evidence aliased prepared tracks")
	}
}
