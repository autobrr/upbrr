// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/audioanalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

type audioAnalysisResolverFake struct {
	subject api.AudioAnalysisSubject
}

func (f audioAnalysisResolverFake) ResolveAudioAnalysisSubject(
	context.Context,
	api.AudioAnalysisInstructions,
) (api.AudioAnalysisSubject, error) {
	return f.subject, nil
}

type changingAudioAnalysisResolverFake struct {
	subject api.AudioAnalysisSubject
	calls   int
	failAt  int
}

func (f *changingAudioAnalysisResolverFake) ResolveAudioAnalysisSubject(
	context.Context,
	api.AudioAnalysisInstructions,
) (api.AudioAnalysisSubject, error) {
	f.calls++
	if f.calls == f.failAt {
		return api.AudioAnalysisSubject{}, fmt.Errorf("change audio analysis subject: %w", api.NewAudioAnalysisError(api.AudioAnalysisFailure{
			Code:    api.AudioAnalysisFailureStaleSource,
			Message: "the prepared audio source changed and must be refreshed",
		}, errors.New("synthetic source mutation")))
	}
	return f.subject, nil
}

type audioAnalysisServiceFake struct {
	validations []api.AudioAnalysisInstructions
	calls       []api.AudioAnalysisInstructions
	path        string
	prepare     func(string)
	validate    func(api.AudioAnalysisInstructions) error
	analyze     func(context.Context, api.AudioAnalysisInstructions, string) (audioanalysis.TrackResult, error)
	build       func(api.AudioAnalysisInstructions) (audioanalysis.TrackResult, error)
}

func (f *audioAnalysisServiceFake) ValidateSelection(
	_ context.Context,
	_ api.AudioAnalysisSubject,
	instructions api.AudioAnalysisInstructions,
) error {
	f.validations = append(f.validations, instructions)
	if f.validate != nil {
		return f.validate(instructions)
	}
	return nil
}

func (f *audioAnalysisServiceFake) Analyze(
	ctx context.Context,
	_ api.AudioAnalysisSubject,
	instructions api.AudioAnalysisInstructions,
	_ string,
	attemptRoot string,
) (audioanalysis.TrackResult, error) {
	f.calls = append(f.calls, instructions)
	if f.prepare != nil {
		f.prepare(attemptRoot)
	}
	if f.analyze != nil {
		return f.analyze(ctx, instructions, attemptRoot)
	}
	if f.build != nil {
		return f.build(instructions)
	}
	artifact := api.AudioAnalysisArtifact{
		ID:      "spectrogram-retry",
		Variant: api.AudioAnalysisSpectrogram,
		Status:  api.StageStatusCompleted,
		Width:   12,
		Height:  8,
	}
	return audioanalysis.TrackResult{
		Public: api.AudioAnalysisTrackResult{
			TrackID:      "track-1",
			Ordinal:      1,
			Channels:     2,
			SampleRate:   48_000,
			SampleFrames: 96_000,
			Duration:     2,
			Status:       api.StageStatusCompleted,
			Artifacts:    []api.AudioAnalysisArtifact{artifact},
		},
		Artifacts: []audioanalysis.Artifact{{Public: artifact, Path: f.path}},
	}, nil
}

func TestWorkflowAudioAnalysisBuilderRetriesOnlyMissingVariant(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	release := api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 3}
	subject := api.AudioAnalysisSubject{
		Release:             release,
		SourcePath:          release.SourcePath,
		VideoPath:           "source.mkv",
		ResourceID:          "resource-1",
		ManifestFingerprint: "manifest-1",
		PrimaryTrackID:      "track-1",
		Tracks: []api.MediaTrackFacts{{
			ID:                  "track-1",
			Kind:                api.MediaTrackAudio,
			ResourceID:          "resource-1",
			ManifestFingerprint: "manifest-1",
			Ordinal:             1,
			Channels:            2,
			SampleRate:          48_000,
		}},
	}
	priorAttemptRoot := filepath.Join(root, "prior-attempt")
	if err := os.MkdirAll(priorAttemptRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	waveformPath := writeAudioAnalysisPNGForTest(t, priorAttemptRoot, "waveform.png", 10, 6)
	attemptRoot, err := audioAnalysisAttemptRoot(root, subject, "attempt-retry")
	if err != nil {
		t.Fatal(err)
	}
	var spectrogramPath string
	service := &audioAnalysisServiceFake{prepare: func(root string) {
		trackDigest := sha256.Sum256([]byte("track-1"))
		trackRoot := filepath.Join(root, "track_"+hex.EncodeToString(trackDigest[:12]))
		if err := os.MkdirAll(trackRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		spectrogramPath = writeAudioAnalysisPNGForTest(t, trackRoot, "spectrogram.png", 12, 8)
	}}
	service.build = func(api.AudioAnalysisInstructions) (audioanalysis.TrackResult, error) {
		artifact := api.AudioAnalysisArtifact{
			ID:      "spectrogram-retry",
			Variant: api.AudioAnalysisSpectrogram,
			Status:  api.StageStatusCompleted,
			Width:   12,
			Height:  8,
		}
		return audioanalysis.TrackResult{
			Public: api.AudioAnalysisTrackResult{
				TrackID:      "track-1",
				Ordinal:      1,
				Channels:     2,
				SampleRate:   48_000,
				SampleFrames: 96_000,
				Duration:     2,
				Status:       api.StageStatusCompleted,
				Artifacts:    []api.AudioAnalysisArtifact{artifact},
			},
			Artifacts: []audioanalysis.Artifact{{Public: artifact, Path: spectrogramPath}},
		}, nil
	}
	builder := workflowAudioAnalysisBuilder{
		resolver: audioAnalysisResolverFake{subject: subject},
		service:  service,
		root:     root,
	}
	instructions := api.AudioAnalysisInstructions{
		Release:        release,
		ResourceID:     "resource-1",
		Selection:      api.AudioAnalysisSelectionPrimary,
		TrackIDs:       []string{"track-1"},
		Variants:       []api.AudioAnalysisVariant{api.AudioAnalysisWaveform, api.AudioAnalysisSpectrogram},
		ProfileVersion: api.AudioAnalysisProfileVersion,
	}
	now := time.Date(2026, time.September, 21, 2, 0, 0, 0, time.UTC)
	failure := api.AudioAnalysisFailure{Code: api.AudioAnalysisFailureOutput, Message: "could not publish analysis image"}
	prior := &api.AudioAnalysisResult{
		Release:             release,
		ResourceID:          "resource-1",
		ManifestFingerprint: "manifest-1",
		Selection:           instructions.Selection,
		TrackIDs:            []string{"track-1"},
		Variants:            append([]api.AudioAnalysisVariant(nil), instructions.Variants...),
		ProfileVersion:      api.AudioAnalysisProfileVersion,
		Status:              api.StageStatusPartial,
		Tracks: []api.AudioAnalysisTrackResult{{
			TrackID:      "track-1",
			Ordinal:      1,
			Channels:     2,
			SampleRate:   48_000,
			SampleFrames: 96_000,
			Duration:     2,
			Status:       api.StageStatusPartial,
			Artifacts: []api.AudioAnalysisArtifact{
				{
					ID:      "waveform-first",
					Variant: api.AudioAnalysisWaveform,
					Status:  api.StageStatusCompleted,
					Width:   10,
					Height:  6,
				},
				{
					Variant: api.AudioAnalysisSpectrogram,
					Status:  api.StageStatusFailed,
					Failure: &failure,
				},
			},
		}},
	}
	priorResource := retainWorkflowAudioAnalysisResourceForTest(
		t,
		root,
		priorAttemptRoot,
		map[api.PublicResourceID]string{"waveform-first": waveformPath},
	)

	result, retained, err := builder.Build(t.Context(), release, instructions, "attempt-retry", now, prior, priorResource)
	if err != nil {
		t.Fatal(err)
	}
	if len(service.calls) != 1 || !slices.Equal(service.calls[0].TrackIDs, []string{"track-1"}) ||
		!slices.Equal(service.calls[0].Variants, []api.AudioAnalysisVariant{api.AudioAnalysisSpectrogram}) {
		t.Fatalf("service calls = %#v", service.calls)
	}
	if result.Status != api.StageStatusCompleted || len(result.Tracks) != 1 ||
		result.Tracks[0].Artifacts[0].ID == "waveform-first" || result.Tracks[0].Artifacts[1].ID != "spectrogram-retry" {
		t.Fatalf("incremental result = %#v", result)
	}
	resource, ok := retained.(workflowAudioAnalysisResource)
	copiedWaveformID := result.Tracks[0].Artifacts[0].ID
	if !ok || resource.attemptRoot != attemptRoot || resource.paths[copiedWaveformID] == waveformPath ||
		resource.paths["spectrogram-retry"] != spectrogramPath {
		t.Fatalf("retained resource = %#v", retained)
	}
	if err := resource.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(attemptRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("released attempt stat error = %v", err)
	}
	if _, err := os.Stat(waveformPath); err != nil {
		t.Fatalf("prior attempt was removed: %v", err)
	}
}

func TestWorkflowAudioAnalysisBuilderResetsUncommittedAttemptBeforeReplay(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	release := api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 3}
	subject := audioAnalysisSubjectForTest(release, "track-1")
	attemptRoot, err := audioAnalysisAttemptRoot(root, subject, "attempt-replay")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(attemptRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	orphanPath := filepath.Join(attemptRoot, "orphan.png")
	if err := os.WriteFile(orphanPath, []byte("unfinished"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := &audioAnalysisServiceFake{}
	service.prepare = func(currentRoot string) {
		if currentRoot != attemptRoot {
			t.Fatalf("attempt root = %q, want %q", currentRoot, attemptRoot)
		}
		if _, statErr := os.Stat(orphanPath); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("orphaned output survived replay reset: %v", statErr)
		}
		trackRoot := filepath.Join(currentRoot, "track-replayed")
		if mkdirErr := os.MkdirAll(trackRoot, 0o700); mkdirErr != nil {
			t.Fatal(mkdirErr)
		}
		service.path = writeAudioAnalysisPNGForTest(t, trackRoot, "waveform.png", 12, 8)
	}
	builder := workflowAudioAnalysisBuilder{
		resolver: audioAnalysisResolverFake{subject: subject},
		service:  service,
		root:     root,
	}
	instructions := audioAnalysisInstructionsForBuilderTest(release, []string{"track-1"}, api.AudioAnalysisSpectrogram)

	result, retained, err := builder.Build(t.Context(), release, instructions, "attempt-replay", time.Now(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != api.StageStatusCompleted || retained == nil {
		t.Fatalf("replayed result = %#v retained=%#v", result, retained)
	}
}

func TestWorkflowAudioAnalysisBuilderStopsBeforeDecodeWhenSourceChangesAtTrackBoundary(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	release := api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 3}
	subject := audioAnalysisSubjectForTest(release, "track-1", "track-2")
	resolver := &changingAudioAnalysisResolverFake{subject: subject, failAt: 2}
	service := &audioAnalysisServiceFake{}
	builder := workflowAudioAnalysisBuilder{
		resolver: resolver,
		service:  service,
		root:     root,
	}
	instructions := audioAnalysisInstructionsForBuilderTest(
		release,
		[]string{"track-1", "track-2"},
		api.AudioAnalysisWaveform,
	)

	result, retained, err := builder.Build(t.Context(), release, instructions, "attempt-stale-boundary", time.Now(), nil, nil)
	failure, typed := api.AsAudioAnalysisFailure(err)
	if !typed || failure.Code != api.AudioAnalysisFailureStaleSource {
		t.Fatalf("error = %v, failure = %#v", err, failure)
	}
	if resolver.calls != 2 || len(service.calls) != 0 || retained != nil || result.Status != "" {
		t.Fatalf("resolver calls = %d, service calls = %#v, retained = %#v, result = %#v",
			resolver.calls, service.calls, retained, result)
	}
}

func TestWorkflowAudioAnalysisBuilderPreflightsCompleteSelectionBeforeDecode(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	release := api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 3}
	subject := audioAnalysisSubjectForTest(release, "track-1", "track-2")
	service := &audioAnalysisServiceFake{validate: func(instructions api.AudioAnalysisInstructions) error {
		if !slices.Equal(instructions.TrackIDs, []string{"track-1", "track-2"}) {
			t.Fatalf("preflight track IDs = %v", instructions.TrackIDs)
		}
		return api.NewAudioAnalysisError(api.AudioAnalysisFailure{
			Code:    api.AudioAnalysisFailureUnsupportedLayout,
			Message: "the selected audio track has an unsupported channel layout",
		}, errors.New("synthetic later selected track is unsupported"))
	}}
	builder := workflowAudioAnalysisBuilder{
		resolver: audioAnalysisResolverFake{subject: subject},
		service:  service,
		root:     root,
	}
	instructions := audioAnalysisInstructionsForBuilderTest(
		release,
		[]string{"track-1", "track-2"},
		api.AudioAnalysisWaveform,
	)

	result, retained, err := builder.Build(t.Context(), release, instructions, "attempt-invalid-selection", time.Now(), nil, nil)
	failure, typed := api.AsAudioAnalysisFailure(err)
	if !typed || failure.Code != api.AudioAnalysisFailureUnsupportedLayout {
		t.Fatalf("error = %v, failure = %#v", err, failure)
	}
	if len(service.validations) != 1 || len(service.calls) != 0 || retained != nil || result.Status != "" {
		t.Fatalf("validations = %#v, calls = %#v, retained = %#v, result = %#v",
			service.validations, service.calls, retained, result)
	}
}

func TestWorkflowAudioAnalysisBuilderContinuesAfterTrackLocalFailure(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	release := api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 3}
	subject := audioAnalysisSubjectForTest(release, "track-1", "track-2")
	var secondPath string
	service := &audioAnalysisServiceFake{prepare: func(attemptRoot string) {
		if secondPath != "" {
			return
		}
		trackRoot := filepath.Join(attemptRoot, "track-second")
		if err := os.MkdirAll(trackRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		secondPath = writeAudioAnalysisPNGForTest(t, trackRoot, "waveform.png", 12, 8)
	}, build: func(instructions api.AudioAnalysisInstructions) (audioanalysis.TrackResult, error) {
		trackID := instructions.TrackIDs[0]
		if trackID == "track-1" {
			failure := api.AudioAnalysisFailure{Code: api.AudioAnalysisFailureDecode, Message: "could not decode the selected audio track"}
			return audioanalysis.TrackResult{Public: api.AudioAnalysisTrackResult{
				TrackID:    trackID,
				Ordinal:    1,
				Channels:   2,
				SampleRate: 48_000,
				Status:     api.StageStatusFailed,
				Failure:    &failure,
			}}, nil
		}
		artifact := api.AudioAnalysisArtifact{
			ID:      "waveform-second",
			Variant: api.AudioAnalysisWaveform,
			Status:  api.StageStatusCompleted,
			Width:   12,
			Height:  8,
		}
		return audioanalysis.TrackResult{
			Public: api.AudioAnalysisTrackResult{
				TrackID:      trackID,
				Ordinal:      2,
				Channels:     2,
				SampleRate:   48_000,
				SampleFrames: 96_000,
				Duration:     2,
				Status:       api.StageStatusCompleted,
				Artifacts:    []api.AudioAnalysisArtifact{artifact},
			},
			Artifacts: []audioanalysis.Artifact{{Public: artifact, Path: secondPath}},
		}, nil
	}}
	builder := workflowAudioAnalysisBuilder{
		resolver: audioAnalysisResolverFake{subject: subject},
		service:  service,
		root:     root,
	}
	instructions := audioAnalysisInstructionsForBuilderTest(
		release,
		[]string{"track-1", "track-2"},
		api.AudioAnalysisWaveform,
	)

	result, retained, err := builder.Build(t.Context(), release, instructions, "attempt-track-failure", time.Now(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != api.StageStatusPartial || len(result.Tracks) != 2 || len(service.calls) != 2 ||
		result.Tracks[0].Status != api.StageStatusFailed || result.Tracks[1].Status != api.StageStatusCompleted {
		t.Fatalf("calls = %#v, result = %#v", service.calls, result)
	}
	resource, ok := retained.(workflowAudioAnalysisResource)
	if !ok || resource.paths["waveform-second"] != secondPath {
		t.Fatalf("retained partial resource = %#v", retained)
	}
}

func TestWorkflowAudioAnalysisBuilderAbortsAttemptAfterTypedStaleDecode(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	release := api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 3}
	subject := audioAnalysisSubjectForTest(release, "track-1", "track-2", "track-3")
	attemptRoot, err := audioAnalysisAttemptRoot(root, subject, "attempt-stale-decode")
	if err != nil {
		t.Fatal(err)
	}
	var firstPath string
	service := &audioAnalysisServiceFake{prepare: func(attemptRoot string) {
		if firstPath != "" {
			return
		}
		firstTrackRoot := filepath.Join(attemptRoot, "track-first")
		if err := os.MkdirAll(firstTrackRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		firstPath = writeAudioAnalysisPNGForTest(t, firstTrackRoot, "waveform.png", 12, 8)
	}, build: func(instructions api.AudioAnalysisInstructions) (audioanalysis.TrackResult, error) {
		trackID := instructions.TrackIDs[0]
		if trackID == "track-1" {
			artifact := api.AudioAnalysisArtifact{
				ID:      "waveform-first",
				Variant: api.AudioAnalysisWaveform,
				Status:  api.StageStatusCompleted,
				Width:   12,
				Height:  8,
			}
			return audioanalysis.TrackResult{
				Public: api.AudioAnalysisTrackResult{
					TrackID:      trackID,
					Ordinal:      1,
					Channels:     2,
					SampleRate:   48_000,
					SampleFrames: 96_000,
					Duration:     2,
					Status:       api.StageStatusCompleted,
					Artifacts:    []api.AudioAnalysisArtifact{artifact},
				},
				Artifacts: []audioanalysis.Artifact{{Public: artifact, Path: firstPath}},
			}, nil
		}
		failure := api.AudioAnalysisFailure{
			Code:    api.AudioAnalysisFailureStaleSource,
			Message: "decoded audio changed between analysis passes",
		}
		return audioanalysis.TrackResult{}, api.NewAudioAnalysisError(failure, errors.New("synthetic PCM drift"))
	}}
	builder := workflowAudioAnalysisBuilder{
		resolver: audioAnalysisResolverFake{subject: subject},
		service:  service,
		root:     root,
	}
	instructions := audioAnalysisInstructionsForBuilderTest(
		release,
		[]string{"track-1", "track-2", "track-3"},
		api.AudioAnalysisWaveform,
	)

	result, retained, err := builder.Build(t.Context(), release, instructions, "attempt-stale-decode", time.Now(), nil, nil)
	failure, typed := api.AsAudioAnalysisFailure(err)
	if !typed || failure.Code != api.AudioAnalysisFailureStaleSource {
		t.Fatalf("error = %v, failure = %#v", err, failure)
	}
	if len(service.calls) != 2 || retained != nil || result.Status != "" {
		t.Fatalf("service calls = %#v, retained = %#v, result = %#v", service.calls, retained, result)
	}
	if _, statErr := os.Stat(attemptRoot); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("stale attempt still exists: %v", statErr)
	}
}

func TestWorkflowAudioAnalysisBuilderAbortsAttemptAfterAmbiguousBinding(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	release := api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 3}
	subject := audioAnalysisSubjectForTest(release, "track-1", "track-2", "track-3")
	attemptRoot, err := audioAnalysisAttemptRoot(root, subject, "attempt-ambiguous-binding")
	if err != nil {
		t.Fatal(err)
	}
	var firstPath string
	service := &audioAnalysisServiceFake{prepare: func(attemptRoot string) {
		if firstPath != "" {
			return
		}
		firstTrackRoot := filepath.Join(attemptRoot, "track-first")
		if err := os.MkdirAll(firstTrackRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		firstPath = writeAudioAnalysisPNGForTest(t, firstTrackRoot, "waveform.png", 12, 8)
	}, build: func(instructions api.AudioAnalysisInstructions) (audioanalysis.TrackResult, error) {
		trackID := instructions.TrackIDs[0]
		if trackID == "track-1" {
			artifact := api.AudioAnalysisArtifact{
				ID:      "waveform-first",
				Variant: api.AudioAnalysisWaveform,
				Status:  api.StageStatusCompleted,
				Width:   12,
				Height:  8,
			}
			return audioanalysis.TrackResult{
				Public: api.AudioAnalysisTrackResult{
					TrackID:      trackID,
					Ordinal:      1,
					Channels:     2,
					SampleRate:   48_000,
					SampleFrames: 96_000,
					Duration:     2,
					Status:       api.StageStatusCompleted,
					Artifacts:    []api.AudioAnalysisArtifact{artifact},
				},
				Artifacts: []audioanalysis.Artifact{{Public: artifact, Path: firstPath}},
			}, nil
		}
		failure := api.AudioAnalysisFailure{
			Code:    api.AudioAnalysisFailureAmbiguousBinding,
			Message: "prepared audio facts no longer match the decoder stream inventory",
		}
		return audioanalysis.TrackResult{}, api.NewAudioAnalysisError(failure, errors.New("synthetic binding mutation"))
	}}
	builder := workflowAudioAnalysisBuilder{
		resolver: audioAnalysisResolverFake{subject: subject},
		service:  service,
		root:     root,
	}
	instructions := audioAnalysisInstructionsForBuilderTest(
		release,
		[]string{"track-1", "track-2", "track-3"},
		api.AudioAnalysisWaveform,
	)

	result, retained, err := builder.Build(t.Context(), release, instructions, "attempt-ambiguous-binding", time.Now(), nil, nil)
	failure, typed := api.AsAudioAnalysisFailure(err)
	if !typed || failure.Code != api.AudioAnalysisFailureAmbiguousBinding {
		t.Fatalf("error = %v, failure = %#v", err, failure)
	}
	if len(service.calls) != 2 || retained != nil || result.Status != "" {
		t.Fatalf("service calls = %#v, retained = %#v, result = %#v", service.calls, retained, result)
	}
	if _, statErr := os.Stat(attemptRoot); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("invalidated attempt still exists: %v", statErr)
	}
}

func TestWorkflowAudioAnalysisBuilderRetainsCompletedTrackOnCancellation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	release := api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 3}
	subject := audioAnalysisSubjectForTest(release, "track-1", "track-2")
	var firstPath string
	secondTrackStarted := make(chan struct{})
	service := &audioAnalysisServiceFake{prepare: func(attemptRoot string) {
		if firstPath != "" {
			return
		}
		firstTrackRoot := filepath.Join(attemptRoot, "track-first")
		if err := os.MkdirAll(firstTrackRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		firstPath = writeAudioAnalysisPNGForTest(t, firstTrackRoot, "waveform.png", 12, 8)
	}, analyze: func(ctx context.Context, instructions api.AudioAnalysisInstructions, _ string) (audioanalysis.TrackResult, error) {
		if instructions.TrackIDs[0] == "track-1" {
			artifact := api.AudioAnalysisArtifact{
				ID:      "waveform-first",
				Variant: api.AudioAnalysisWaveform,
				Status:  api.StageStatusCompleted,
				Width:   12,
				Height:  8,
			}
			return audioanalysis.TrackResult{
				Public: api.AudioAnalysisTrackResult{
					TrackID:      "track-1",
					Ordinal:      1,
					Channels:     2,
					SampleRate:   48_000,
					SampleFrames: 96_000,
					Duration:     2,
					Status:       api.StageStatusCompleted,
					Artifacts:    []api.AudioAnalysisArtifact{artifact},
				},
				Artifacts: []audioanalysis.Artifact{{Public: artifact, Path: firstPath}},
			}, nil
		}
		close(secondTrackStarted)
		<-ctx.Done()
		failure := api.AudioAnalysisFailure{Code: api.AudioAnalysisFailureCanceled, Message: "audio analysis was canceled"}
		return audioanalysis.TrackResult{Public: api.AudioAnalysisTrackResult{
			TrackID: "track-2",
			Ordinal: 2,
			Status:  api.StageStatusFailed,
			Failure: &failure,
		}}, api.NewAudioAnalysisError(failure, ctx.Err())
	}}
	builder := workflowAudioAnalysisBuilder{
		resolver: audioAnalysisResolverFake{subject: subject},
		service:  service,
		root:     root,
	}
	instructions := audioAnalysisInstructionsForBuilderTest(release, []string{"track-1", "track-2"}, api.AudioAnalysisWaveform)
	var progress []api.WorkflowProgressUpdate
	ctx, cancel := context.WithCancel(api.WithWorkflowProgressReporter(t.Context(), func(update api.WorkflowProgressUpdate) {
		progress = append(progress, update)
	}))
	type outcome struct {
		result   api.AudioAnalysisResult
		retained releaseworkflow.RetainedAudioAnalysisResource
		err      error
	}
	done := make(chan outcome, 1)
	go func() {
		result, retained, err := builder.Build(ctx, release, instructions, "attempt-cancel", time.Now(), nil, nil)
		done <- outcome{
			result:   result,
			retained: retained,
			err:      err,
		}
	}()
	<-secondTrackStarted
	cancel()

	var got outcome
	select {
	case got = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("builder did not stop after canceling the final track count pass")
	}
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.result.Status != api.StageStatusCanceled || len(got.result.Tracks) != 2 || len(service.calls) != 2 ||
		got.result.Tracks[0].Artifacts[0].Status != api.StageStatusCompleted ||
		got.result.Tracks[1].Artifacts[0].Failure == nil ||
		got.result.Tracks[1].Artifacts[0].Failure.Code != api.AudioAnalysisFailureCanceled {
		t.Fatalf("canceled result = %#v", got.result)
	}
	resource, ok := got.retained.(workflowAudioAnalysisResource)
	if !ok || resource.paths["waveform-first"] != firstPath {
		t.Fatalf("retained canceled resource = %#v", got.retained)
	}
	if _, err := os.Stat(resource.attemptRoot); err != nil {
		t.Fatalf("retained canceled attempt root: %v", err)
	}
	if !slices.ContainsFunc(progress, func(update api.WorkflowProgressUpdate) bool {
		return update.Kind == "audio_output" && update.ItemID == "track-1:waveform" && update.Status == api.StageStatusCompleted
	}) || !slices.ContainsFunc(progress, func(update api.WorkflowProgressUpdate) bool {
		return update.Kind == "audio_track" && update.ItemID == "track-2" && update.Status == api.StageStatusFailed
	}) {
		t.Fatalf("progress = %#v", progress)
	}
}

func TestWorkflowAudioAnalysisBuilderMapsDeadlineToInterruptedResult(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	release := api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 3}
	subject := audioAnalysisSubjectForTest(release, "track-1")
	failure := api.AudioAnalysisFailure{Code: api.AudioAnalysisFailureInterrupted, Message: "audio analysis timed out"}
	service := &audioAnalysisServiceFake{build: func(api.AudioAnalysisInstructions) (audioanalysis.TrackResult, error) {
		return audioanalysis.TrackResult{}, api.NewAudioAnalysisError(failure, context.DeadlineExceeded)
	}}
	builder := workflowAudioAnalysisBuilder{
		resolver: audioAnalysisResolverFake{subject: subject},
		service:  service,
		root:     root,
	}
	instructions := audioAnalysisInstructionsForBuilderTest(release, []string{"track-1"}, api.AudioAnalysisWaveform)

	result, _, err := builder.Build(t.Context(), release, instructions, "attempt-deadline", time.Now(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != api.StageStatusInterrupted || len(result.Tracks) != 1 || result.Tracks[0].Failure == nil ||
		result.Tracks[0].Failure.Code != api.AudioAnalysisFailureInterrupted {
		t.Fatalf("interrupted result = %#v", result)
	}
}

func TestWorkflowAudioAnalysisBuilderCompletesMissingVariantsAfterStorageFailure(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	release := api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 4}
	subject := audioAnalysisSubjectForTest(release, "track-1")
	var waveformPath string
	service := &audioAnalysisServiceFake{prepare: func(attemptRoot string) {
		trackRoot := filepath.Join(attemptRoot, "track-storage")
		if err := os.MkdirAll(trackRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		waveformPath = writeAudioAnalysisPNGForTest(t, trackRoot, "waveform.png", 12, 8)
	}, build: func(api.AudioAnalysisInstructions) (audioanalysis.TrackResult, error) {
		artifact := api.AudioAnalysisArtifact{
			ID:      "waveform-storage",
			Variant: api.AudioAnalysisWaveform,
			Status:  api.StageStatusCompleted,
			Width:   12,
			Height:  8,
		}
		track := audioanalysis.TrackResult{
			Public: api.AudioAnalysisTrackResult{
				TrackID:      "track-1",
				Ordinal:      1,
				Channels:     2,
				SampleRate:   48_000,
				SampleFrames: 96_000,
				Duration:     2,
				Status:       api.StageStatusPartial,
				Artifacts:    []api.AudioAnalysisArtifact{artifact},
			},
			Artifacts: []audioanalysis.Artifact{{Public: artifact, Path: waveformPath}},
		}
		failure := api.AudioAnalysisFailure{
			Code: api.AudioAnalysisFailureResourceUnavailable, Message: "the audio-analysis output root became unavailable",
		}
		return track,
			api.NewAudioAnalysisError(failure, errors.New("disk full"))
	}}
	builder := workflowAudioAnalysisBuilder{
		resolver: audioAnalysisResolverFake{subject: subject},
		service:  service,
		root:     root,
	}
	instructions := audioAnalysisInstructionsForBuilderTest(
		release,
		[]string{"track-1"},
		api.AudioAnalysisWaveform,
		api.AudioAnalysisSpectrogram,
	)

	result, retained, err := builder.Build(t.Context(), release, instructions, "attempt-storage", time.Now(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != api.StageStatusPartial || len(result.Tracks[0].Artifacts) != 2 ||
		result.Tracks[0].Artifacts[0].Status != api.StageStatusCompleted ||
		result.Tracks[0].Artifacts[1].Failure == nil ||
		result.Tracks[0].Artifacts[1].Failure.Code != api.AudioAnalysisFailureResourceUnavailable {
		t.Fatalf("storage failure result = %#v", result)
	}
	if retained == nil {
		t.Fatal("storage failure discarded completed artifact")
	}
}

func TestWorkflowAudioAnalysisVaultReleasesDurableAttemptsAfterRestart(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	codec := workflowAudioAnalysisCodecForTest(root)
	vault, err := releaseworkflow.NewPrivateArtifactVault(filepath.Join(t.TempDir(), "vault"), codec)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	newResource := func(name string) (workflowAudioAnalysisResource, string) {
		attemptRoot := filepath.Join(root, "release", "audio-analysis", name)
		if err := os.MkdirAll(attemptRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		pathValue := writeAudioAnalysisPNGForTest(t, attemptRoot, "waveform.png", 12, 8)
		return retainWorkflowAudioAnalysisResourceForTest(
			t,
			root,
			attemptRoot,
			map[api.PublicResourceID]string{"waveform-" + api.PublicResourceID(name): pathValue},
		), attemptRoot
	}

	deletedResource, deletedRoot := newResource("deleted")
	if err := vault.Put("owner", "workflow", "audio-analysis:deleted", deletedResource, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	vault.InvalidateAll()
	if _, err := os.Stat(deletedRoot); err != nil {
		t.Fatalf("restart removed durable attempt: %v", err)
	}
	vault.Delete("owner", "workflow", "audio-analysis:deleted")
	if _, err := os.Stat(deletedRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted attempt stat error = %v", err)
	}

	expiredResource, expiredRoot := newResource("expired")
	if err := vault.Put("owner", "workflow", "audio-analysis:expired", expiredResource, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	vault.InvalidateAll()
	if err := vault.CleanupExpired(now.Add(2 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(expiredRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired attempt stat error = %v", err)
	}
}

func TestWorkflowAudioAnalysisStartupCleanupReleasesCorruptExpiredAttemptAndContinues(t *testing.T) {
	for _, test := range []struct {
		name    string
		corrupt func(*testing.T, string)
	}{
		{
			name: "missing artifact",
			corrupt: func(t *testing.T, pathValue string) {
				t.Helper()
				if err := os.Remove(pathValue); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "tampered artifact",
			corrupt: func(t *testing.T, pathValue string) {
				t.Helper()
				writeAudioAnalysisPNGColorForTest(t, filepath.Dir(pathValue), filepath.Base(pathValue), 12, 8, color.RGBA{B: 255, A: 255})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			vault, err := releaseworkflow.NewPrivateArtifactVault(
				filepath.Join(t.TempDir(), "vault"),
				workflowAudioAnalysisCodecForTest(root),
			)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			newResource := func(name string) (workflowAudioAnalysisResource, string, string) {
				attemptRoot := filepath.Join(root, "release", "audio-analysis", name)
				if err := os.MkdirAll(attemptRoot, 0o700); err != nil {
					t.Fatal(err)
				}
				pathValue := writeAudioAnalysisPNGForTest(t, attemptRoot, "waveform.png", 12, 8)
				resource := retainWorkflowAudioAnalysisResourceForTest(
					t,
					root,
					attemptRoot,
					map[api.PublicResourceID]string{"waveform-" + api.PublicResourceID(name): pathValue},
				)
				return resource, attemptRoot, pathValue
			}

			corruptResource, corruptRoot, corruptPath := newResource("corrupt")
			validResource, validRoot, _ := newResource("valid")
			if err := vault.Put("owner", "workflow", "audio-analysis:corrupt", corruptResource, now.Add(-time.Minute)); err != nil {
				t.Fatal(err)
			}
			if err := vault.Put("owner", "workflow", "audio-analysis:valid", validResource, now.Add(-time.Minute)); err != nil {
				t.Fatal(err)
			}
			vault.InvalidateAll()
			test.corrupt(t, corruptPath)

			stop, done, err := startWorkflowPrivateVaultCleanup(t.Context(), vault, time.Hour, api.NopLogger{})
			if err != nil {
				t.Fatal(err)
			}
			stop()
			<-done
			for _, attemptRoot := range []string{corruptRoot, validRoot} {
				if _, statErr := os.Stat(attemptRoot); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("expired attempt %q stat error = %v", attemptRoot, statErr)
				}
			}
		})
	}
}

func TestWorkflowAudioAnalysisStartupCleanupPreservesAttemptWhenVaultAuthorityIsCorrupt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	vaultRoot := filepath.Join(t.TempDir(), "vault")
	vault, err := releaseworkflow.NewPrivateArtifactVault(vaultRoot, workflowAudioAnalysisCodecForTest(root))
	if err != nil {
		t.Fatal(err)
	}
	attemptRoot := filepath.Join(root, "release", "audio-analysis", "corrupt-authority")
	if err := os.MkdirAll(attemptRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	pathValue := writeAudioAnalysisPNGForTest(t, attemptRoot, "waveform.png", 12, 8)
	resource := retainWorkflowAudioAnalysisResourceForTest(
		t,
		root,
		attemptRoot,
		map[api.PublicResourceID]string{"waveform-corrupt-authority": pathValue},
	)
	if err := vault.Put("owner", "workflow", "audio-analysis:corrupt-authority", resource, time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	vault.InvalidateAll()
	blobPaths, err := filepath.Glob(filepath.Join(vaultRoot, "*", "*.blob"))
	if err != nil || len(blobPaths) != 1 {
		t.Fatalf("vault blob paths = %v, error = %v", blobPaths, err)
	}
	metadataPaths, err := filepath.Glob(filepath.Join(vaultRoot, "*", "*.json"))
	if err != nil || len(metadataPaths) != 1 {
		t.Fatalf("vault metadata paths = %v, error = %v", metadataPaths, err)
	}
	if err := os.WriteFile(blobPaths[0], []byte("tampered authority"), 0o600); err != nil {
		t.Fatal(err)
	}

	stop, done, err := startWorkflowPrivateVaultCleanup(t.Context(), vault, time.Hour, api.NopLogger{})
	if stop != nil || done != nil || !errors.Is(err, releaseworkflow.ErrPrivateResourceIntegrity) {
		t.Fatalf("startup cleanup stop=%v done=%v error=%v", stop, done, err)
	}
	for _, pathValue := range []string{attemptRoot, blobPaths[0], metadataPaths[0]} {
		if _, statErr := os.Stat(pathValue); statErr != nil {
			t.Fatalf("preserved corrupt authority path %q stat error = %v", pathValue, statErr)
		}
	}
}

func TestWorkflowPrivateVaultCleanupLifecycleExpiresAudioAttempt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	codec := workflowAudioAnalysisCodecForTest(root)
	vault, err := releaseworkflow.NewPrivateArtifactVault(filepath.Join(t.TempDir(), "vault"), codec)
	if err != nil {
		t.Fatal(err)
	}
	attemptRoot := filepath.Join(root, "release", "audio-analysis", "expiring")
	if err := os.MkdirAll(attemptRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	pathValue := writeAudioAnalysisPNGForTest(t, attemptRoot, "waveform.png", 12, 8)
	resource := retainWorkflowAudioAnalysisResourceForTest(
		t,
		root,
		attemptRoot,
		map[api.PublicResourceID]string{"waveform-expiring": pathValue},
	)
	if err := vault.Put("owner", "workflow", "audio-analysis:expiring", resource, time.Now().UTC().Add(500*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	stop, done, err := startWorkflowPrivateVaultCleanup(t.Context(), vault, 20*time.Millisecond, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		stop()
		<-done
	}()
	if _, err := os.Stat(attemptRoot); err != nil {
		t.Fatalf("startup cleanup removed an unexpired attempt: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		_, statErr := os.Stat(attemptRoot)
		if errors.Is(statErr, os.ErrNotExist) {
			break
		}
		if statErr != nil {
			t.Fatalf("stat expiring attempt: %v", statErr)
		}
		if time.Now().After(deadline) {
			t.Fatal("periodic private-vault cleanup did not remove the expired audio attempt")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestWorkflowAudioAnalysisResourceCodecRejectsEscapeAndServesAuthorizedPNG(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	attemptRoot := filepath.Join(root, "attempt")
	if err := os.MkdirAll(attemptRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	pathValue := writeAudioAnalysisPNGForTest(t, attemptRoot, "artifact.png", 9, 7)
	resource := retainWorkflowAudioAnalysisResourceForTest(
		t,
		root,
		attemptRoot,
		map[api.PublicResourceID]string{"artifact-1": pathValue},
	)
	kind, payload, err := resource.MarshalPrivateResource()
	if err != nil {
		t.Fatal(err)
	}
	if kind != workflowPrivateResourceKindAudioAnalysis {
		t.Fatalf("resource kind = %q", kind)
	}
	decodedValue, err := decodeWorkflowAudioAnalysisResource(root, payload)
	if err != nil {
		t.Fatal(err)
	}
	decoded, ok := decodedValue.(workflowAudioAnalysisResource)
	if !ok {
		t.Fatalf("decoded resource type = %T", decodedValue)
	}
	snapshot := api.AudioAnalysisResult{Tracks: []api.AudioAnalysisTrackResult{{Artifacts: []api.AudioAnalysisArtifact{{
		ID:      "artifact-1",
		Variant: api.AudioAnalysisWaveform,
		Status:  api.StageStatusCompleted,
		Width:   9,
		Height:  7,
	}}}}}
	content, err := decoded.OpenArtifact(t.Context(), snapshot, "artifact-1")
	if err != nil {
		t.Fatal(err)
	}
	if content.ContentType != "image/png" {
		t.Fatalf("content type = %q", content.ContentType)
	}
	_ = content.Body.Close()
	if _, err := decoded.OpenArtifact(t.Context(), snapshot, "other"); !errors.Is(err, releaseworkflow.ErrPrivateResourceUnavailable) {
		t.Fatalf("unauthorized artifact error = %v", err)
	}
	writeAudioAnalysisPNGColorForTest(t, attemptRoot, "artifact.png", 9, 7, color.RGBA{B: 255, A: 255})
	if _, err := decodeWorkflowAudioAnalysisResource(root, payload); err == nil {
		t.Fatal("same-dimension tampered artifact decoded")
	}
	if _, err := decoded.OpenArtifact(t.Context(), snapshot, "artifact-1"); !errors.Is(err, releaseworkflow.ErrPrivateResourceUnavailable) {
		t.Fatalf("tampered artifact error = %v", err)
	}
	if _, err := decoded.LocalArtifactPath(snapshot, "artifact-1"); !errors.Is(err, releaseworkflow.ErrPrivateResourceUnavailable) {
		t.Fatalf("tampered local artifact error = %v", err)
	}

	escapePayload, err := json.Marshal(persistedWorkflowAudioAnalysisResource{
		AttemptRoot: attemptRoot,
		Paths:       map[api.PublicResourceID]string{"artifact-1": filepath.Join(root, "..", "escape.png")},
		Integrity:   map[api.PublicResourceID]workflowAudioAnalysisArtifactIntegrity{"artifact-1": resource.integrity["artifact-1"]},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeWorkflowAudioAnalysisResource(root, escapePayload); err == nil {
		t.Fatal("escaped retained path decoded")
	}
	releaseEscapePayload, err := json.Marshal(persistedWorkflowAudioAnalysisResource{
		AttemptRoot: filepath.Join(root, "..", "escape-attempt"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeWorkflowAudioAnalysisResourceForRelease(root, releaseEscapePayload); err == nil {
		t.Fatal("escaped attempt root decoded for release")
	}
}

func workflowAudioAnalysisCodecForTest(root string) releaseworkflow.PrivateResourceCodec {
	return releaseworkflow.PrivateResourceCodec{
		Kind: workflowPrivateResourceKindAudioAnalysis,
		Decode: func(payload []byte) (any, error) {
			return decodeWorkflowAudioAnalysisResource(root, payload)
		},
		DecodeForRelease: func(payload []byte) (any, error) {
			return decodeWorkflowAudioAnalysisResourceForRelease(root, payload)
		},
	}
}

func writeAudioAnalysisPNGForTest(t *testing.T, root string, name string, width int, height int) string {
	t.Helper()
	return writeAudioAnalysisPNGColorForTest(t, root, name, width, height, color.RGBA{R: 255, A: 255})
}

func writeAudioAnalysisPNGColorForTest(
	t *testing.T,
	root string,
	name string,
	width int,
	height int,
	pixel color.RGBA,
) string {
	t.Helper()
	pathValue := filepath.Join(root, name)
	file, err := os.OpenFile(pathValue, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	canvas.SetRGBA(0, 0, pixel)
	if err := png.Encode(file, canvas); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return pathValue
}

func retainWorkflowAudioAnalysisResourceForTest(
	t *testing.T,
	root string,
	attemptRoot string,
	paths map[api.PublicResourceID]string,
) workflowAudioAnalysisResource {
	t.Helper()
	resource, err := retainWorkflowAudioAnalysisResource(root, attemptRoot, paths)
	if err != nil {
		t.Fatal(err)
	}
	return resource
}

func audioAnalysisSubjectForTest(release api.ReleaseRef, trackIDs ...string) api.AudioAnalysisSubject {
	tracks := make([]api.MediaTrackFacts, 0, len(trackIDs))
	for index, trackID := range trackIDs {
		tracks = append(tracks, api.MediaTrackFacts{
			ID:                  trackID,
			Kind:                api.MediaTrackAudio,
			ResourceID:          "resource-1",
			ManifestFingerprint: "manifest-1",
			Ordinal:             index + 1,
			Channels:            2,
			SampleRate:          48_000,
		})
	}
	return api.AudioAnalysisSubject{
		Release:             release,
		SourcePath:          release.SourcePath,
		VideoPath:           "source.mkv",
		ResourceID:          "resource-1",
		ManifestFingerprint: "manifest-1",
		PrimaryTrackID:      trackIDs[0],
		Tracks:              tracks,
	}
}

func audioAnalysisInstructionsForBuilderTest(
	release api.ReleaseRef,
	trackIDs []string,
	variants ...api.AudioAnalysisVariant,
) api.AudioAnalysisInstructions {
	return api.AudioAnalysisInstructions{
		Release:        release,
		ResourceID:     "resource-1",
		Selection:      api.AudioAnalysisSelectionSelected,
		TrackIDs:       trackIDs,
		Variants:       variants,
		ProfileVersion: api.AudioAnalysisProfileVersion,
	}
}
