// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type unknownOriginalPipeline struct{}

func (unknownOriginalPipeline) CollectPreparationEvidence(_ context.Context, request preparationstate.Request) (preparationstate.State, error) {
	state := preparationstate.State{
		SourcePath: request.Manifest.SourcePath,
		Paths:      []string{request.Manifest.SourcePath},
		VideoPath:  request.Manifest.SourcePath,
		Release: api.ReleaseInfo{
			Title:      "Example Release",
			Year:       2026,
			Category:   "MOVIE",
			Resolution: "1080p",
		},
		Type:                  "WEBDL",
		Source:                "WEB-DL",
		Container:             "mkv",
		MetadataOverrides:     request.Input.Instructions.Metadata,
		AudioLanguages:        []string{"German"},
		TrackCoverageComplete: true,
		PrimaryAudioTrackID:   "programme",
		MediaTracks: []api.MediaTrackFacts{{
			ID:                  "programme",
			Kind:                api.MediaTrackAudio,
			Role:                api.AudioRoleProgramme,
			Languages:           []string{"German"},
			ResourceID:          "resource",
			ManifestFingerprint: "report",
		}},
	}
	metadata.RebuildReleaseName(&state, api.NopLogger{})
	return state, nil
}

type languageUploadBoundary struct{ submitted *int }

func (languageUploadBoundary) Name() string { return "EXAMPLE" }
func (languageUploadBoundary) UploadContentMode() trackers.UploadContentMode {
	return trackers.UploadContentModeNone
}
func (languageUploadBoundary) DefaultBaseURL() string { return "https://tracker.example.invalid" }
func (d languageUploadBoundary) Prepare(_ context.Context, _ trackers.PreparationInput) (trackers.TrackerPlan, *trackers.PreparationFailure) {
	return trackers.NewUploadPlan(d.Name(), api.TrackerDryRunEntry{Tracker: d.Name(), Status: "ready"}, func(context.Context) (api.UploadSummary, error) {
		*d.submitted++
		return api.UploadSummary{Uploaded: 1}, nil
	}, nil), nil
}

func TestUnknownOriginalRepreparationReachesSyntheticUpload(t *testing.T) {
	source := writePreparedTestFile(t, "Example.Release.2026.mkv", "synthetic media")
	store := newMemoryStore()
	collector, err := NewEvidenceCollector(unknownOriginalPipeline{})
	if err != nil {
		t.Fatal(err)
	}
	module := newTestModule(t, store, collector)
	registry := trackers.NewRegistry()
	submitted := 0
	if err := registry.RegisterDescriptor(trackers.Descriptor{
		Name:       "EXAMPLE",
		Definition: languageUploadBoundary{&submitted},
		Validation: trackers.WithLanguagePolicy(trackers.NoExtraValidationPolicy("example"), trackers.LanguagePolicy{
			OriginalAudio: trackers.LanguageProhibited, ExtraDubs: trackers.LanguageProhibited,
		}),
	}); err != nil {
		t.Fatal(err)
	}
	service := trackers.NewServiceWithRegistry(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "db.sqlite")}}, api.NopLogger{}, nil, registry)
	var previousGeneration api.PreparedGeneration
	for _, step := range []struct {
		name           string
		language       *string
		reset, blocked bool
	}{
		{name: "unknown"},
		{
			name:     "supplied mismatch",
			language: new("Japanese"),
			blocked:  true,
		},
		{name: "explicit clear", language: new("")},
		{name: "reset", reset: true},
		{name: "matching original", language: new("German")},
	} {
		t.Run(step.name, func(t *testing.T) {
			if step.language != nil || step.reset {
				patch := api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{Metadata: api.MetadataOverrides{OriginalLanguage: step.language}}}
				if step.reset {
					patch.ResetFields = []api.CorrectionFieldRef{{Field: api.CorrectionFieldMetadataOriginalLanguage}}
				}
				if _, err := module.ResolveInput(t.Context(), api.PrepareInput{SourcePath: source}, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &patch}); err != nil {
					t.Fatal(err)
				}
			}
			module = newTestModule(t, store, collector)
			result, err := module.Prepare(t.Context(), api.PrepareInput{SourcePath: source})
			if err != nil {
				t.Fatal(err)
			}
			if result.Release.Generation <= previousGeneration {
				t.Fatal("metadata change reused old generation")
			}
			previousGeneration = result.Release.Generation
			subject, err := module.ResolveUploadSubject(t.Context(), api.UploadSubjectInput{Release: api.ReleaseRef{SourcePath: source, Generation: result.Release.Generation}})
			if err != nil {
				t.Fatal(err)
			}
			readiness, err := trackers.EvaluateInputReadiness(registry, []api.TrackerID{"EXAMPLE"}, subject)
			if err != nil {
				t.Fatal(err)
			}
			if (len(readiness.Fields) > 0) != step.blocked {
				t.Fatalf("source review = %#v", readiness)
			}
			projection, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{Tracker: "EXAMPLE", Meta: subject}, "input", "catalog", "config")
			if failure != nil {
				t.Fatal(failure)
			}
			if projection.UploadReady == step.blocked {
				t.Fatalf("upload eligibility = %#v", projection)
			}
			before := submitted
			plan, err := service.PrepareRetainedUploadPlan(t.Context(), subject, []api.TrackerReleaseProjection{projection})
			if step.blocked {
				if err == nil || submitted != before {
					t.Fatal("ambiguous known-original source crossed upload boundary")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := plan.Release(); err != nil {
					t.Error(err)
				}
			}()
			if _, err := plan.Execute(t.Context()); err != nil {
				t.Fatal(err)
			}
			if submitted != before+1 {
				t.Fatal("eligible source did not reach synthetic upload")
			}
		})
	}
}
