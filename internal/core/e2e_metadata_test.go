// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build e2e

package core

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/preparedrelease"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestE2EMetadataCollectsCompleteEnglishProgrammeEvidence(t *testing.T) {
	for _, test := range []struct {
		name          string
		mediaKind     string
		audioAnalysis string
		codec         string
		channels      int
	}{
		{
			name:      "movie",
			mediaKind: "movie",
			codec:     "AC-3",
			channels:  6,
		},
		{
			name:      "tv",
			mediaKind: "tv",
			codec:     "AC-3",
			channels:  6,
		},
		{
			name:          "audio analysis",
			mediaKind:     "movie",
			audioAnalysis: "1",
			codec:         "PCM",
			channels:      2,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(e2eMediaKindEnv, test.mediaKind)
			t.Setenv(e2eAudioAnalysisEnv, test.audioAnalysis)
			t.Setenv(e2eNamingModeEnv, "")
			collector, err := preparedrelease.NewEvidenceCollector(e2eMetadataService{})
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(t.TempDir(), "Example.2026.mkv")
			facts, err := collector.Collect(t.Context(), preparationstate.Request{
				Input:    api.PrepareInput{SourcePath: source},
				Manifest: api.SourceManifest{SourcePath: source},
			})
			if err != nil {
				t.Fatal(err)
			}
			language := facts.Media.LanguageFacts
			if language.AudioStatus != api.MetadataEvidenceStatusComplete || !language.TrackCoverageComplete ||
				!slices.Equal(language.ProgrammeLanguages, []string{"English"}) || !language.HasOriginalAudio() {
				t.Errorf("fixture programme evidence = %#v", language)
			}
			if !slices.Equal(language.OriginalLanguages, []string{"English"}) ||
				facts.Media.OriginalLanguageProvenance != api.FactProvenanceAutomatic {
				t.Errorf("fixture original language = %v, provenance = %s", language.OriginalLanguages, facts.Media.OriginalLanguageProvenance)
			}
			if len(language.Tracks) != 1 {
				t.Fatalf("fixture tracks = %#v", language.Tracks)
			}
			track := language.Tracks[0]
			if track.Kind != api.MediaTrackAudio || track.Role != api.AudioRoleProgramme || !track.Default ||
				track.ID != language.PrimaryAudioTrackID || track.Codec != test.codec || track.Channels != test.channels {
				t.Errorf("fixture primary audio = %#v", track)
			}
		})
	}
}
