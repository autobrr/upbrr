// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/pkg/api"
)

type preparedGenerationStub struct{}

func (preparedGenerationStub) PrepareRelease(_ context.Context, input api.PrepareInput) (api.PrepareResult, error) {
	return api.PrepareResult{Release: api.PreparedRelease{
		Generation: 1,
		Source:     api.SourceManifest{SourcePath: input.SourcePath},
	}}, nil
}

func (preparedGenerationStub) ExportReleaseSeed(context.Context, api.ReleaseRef) (preparedrelease.Seed, error) {
	return preparedrelease.Seed{}, nil
}

func (preparedGenerationStub) ImportReleaseSeed(context.Context, preparedrelease.Seed) (api.ReleaseRef, error) {
	return api.ReleaseRef{}, nil
}

type preparedUploadStub struct{}

func (preparedUploadStub) RunAcceptedUpload(context.Context, api.UploadExecutionPlan) (api.Result, error) {
	return api.Result{}, nil
}

type preparedDupeStub struct{}

func (preparedDupeStub) CheckAcceptedDupes(context.Context, api.DuplicateCheckInput) (api.DupeCheckSummary, error) {
	return api.DupeCheckSummary{}, nil
}

type preparedDryRunStub struct{}

func (preparedDryRunStub) FetchTrackerDryRunPreview(context.Context, api.Request) (api.TrackerDryRunPreview, error) {
	return api.TrackerDryRunPreview{}, nil
}

func (preparedDryRunStub) FetchAcceptedTrackerDryRun(context.Context, api.TrackerDryRunInput) (api.TrackerDryRunPreview, error) {
	return api.TrackerDryRunPreview{}, nil
}

type preparedDVDStub struct{}

func (preparedDVDStub) CaptureAcceptedDVDMenus(context.Context, api.MediaPlanInput) (api.DVDMenuCaptureResult, error) {
	return api.DVDMenuCaptureResult{}, nil
}

func (preparedDVDStub) ListAcceptedDVDMenuScreenshots(context.Context, api.MediaPlanInput) ([]api.ScreenshotImage, error) {
	return nil, nil
}

func (preparedDVDStub) DeleteAcceptedDVDMenuScreenshot(context.Context, api.MediaPlanInput, string) error {
	return nil
}

func TestCoreCapabilitiesPreparedOperationReadinessRejectsPartialBundles(t *testing.T) {
	t.Parallel()

	generationOnly := CoreCapabilities{ReleasePreparation: preparedGenerationStub{}, PreparedGenerationTransfer: preparedGenerationStub{}}
	if !generationOnly.Available() {
		t.Fatal("expected generation-only test bundle to remain available")
	}
	if generationOnly.PreparedUploadReady() || generationOnly.PreparedDupeReady() || generationOnly.PreparedDVDReady() || generationOnly.PreparedDryRunReady() {
		t.Fatal("expected generation-only bundle to reject prepared operations")
	}

	withoutGeneration := CoreCapabilities{
		UploadExecution:    preparedUploadStub{},
		DuplicateExecution: preparedDupeStub{},
		DVD:                preparedDVDStub{},
		DryRun:             preparedDryRunStub{},
	}
	if !withoutGeneration.Available() {
		t.Fatal("expected workflow-only test bundle to remain available")
	}
	if withoutGeneration.PreparedUploadReady() || withoutGeneration.PreparedDupeReady() || withoutGeneration.PreparedDVDReady() || withoutGeneration.PreparedDryRunReady() {
		t.Fatal("expected workflows without generation support to reject prepared operations")
	}

	complete := withoutGeneration
	complete.ReleasePreparation = preparedGenerationStub{}
	complete.PreparedGenerationTransfer = preparedGenerationStub{}
	if !complete.PreparedUploadReady() || !complete.PreparedDupeReady() || !complete.PreparedDVDReady() || !complete.PreparedDryRunReady() {
		t.Fatal("expected complete prepared-operation bundle to be ready")
	}
}

func TestCoreCapabilitiesPreparedOperationReadinessRejectsTypedNilCapabilities(t *testing.T) {
	t.Parallel()

	var typedNilGeneration *preparedGenerationStub
	var typedNilUpload *preparedUploadStub
	var typedNilDupe *preparedDupeStub
	var typedNilDVD *preparedDVDStub
	var typedNilDryRun *preparedDryRunStub

	typedNilOnly := CoreCapabilities{
		ReleasePreparation:         typedNilGeneration,
		PreparedGenerationTransfer: typedNilGeneration,
		DryRun:                     typedNilDryRun,
		DuplicateExecution:         typedNilDupe,
		UploadExecution:            typedNilUpload,
		DVD:                        typedNilDVD,
	}
	if typedNilOnly.Available() {
		t.Fatal("expected typed-nil-only bundle to be unavailable")
	}
	if typedNilOnly.PreparedGenerationReady() || typedNilOnly.PreparedUploadReady() || typedNilOnly.PreparedDupeReady() ||
		typedNilOnly.PreparedDVDReady() || typedNilOnly.PreparedDryRunReady() {
		t.Fatal("expected typed-nil generation to make every prepared operation unavailable")
	}

	complete := CoreCapabilities{
		ReleasePreparation:         preparedGenerationStub{},
		DryRun:                     preparedDryRunStub{},
		DuplicateExecution:         preparedDupeStub{},
		UploadExecution:            preparedUploadStub{},
		DVD:                        preparedDVDStub{},
		PreparedGenerationTransfer: preparedGenerationStub{},
	}
	tests := []struct {
		name       string
		bundle     CoreCapabilities
		ready      func(CoreCapabilities) bool
		otherReady []func(CoreCapabilities) bool
	}{
		{
			name: "upload",
			bundle: func() CoreCapabilities {
				bundle := complete
				bundle.UploadExecution = typedNilUpload
				return bundle
			}(),
			ready: CoreCapabilities.PreparedUploadReady,
			otherReady: []func(CoreCapabilities) bool{
				CoreCapabilities.PreparedDupeReady,
				CoreCapabilities.PreparedDVDReady,
				CoreCapabilities.PreparedDryRunReady,
			},
		},
		{
			name: "dupe",
			bundle: func() CoreCapabilities {
				bundle := complete
				bundle.DuplicateExecution = typedNilDupe
				return bundle
			}(),
			ready: CoreCapabilities.PreparedDupeReady,
			otherReady: []func(CoreCapabilities) bool{
				CoreCapabilities.PreparedUploadReady,
				CoreCapabilities.PreparedDVDReady,
				CoreCapabilities.PreparedDryRunReady,
			},
		},
		{
			name: "DVD",
			bundle: func() CoreCapabilities {
				bundle := complete
				bundle.DVD = typedNilDVD
				return bundle
			}(),
			ready: CoreCapabilities.PreparedDVDReady,
			otherReady: []func(CoreCapabilities) bool{
				CoreCapabilities.PreparedUploadReady,
				CoreCapabilities.PreparedDupeReady,
				CoreCapabilities.PreparedDryRunReady,
			},
		},
		{
			name: "dry run",
			bundle: func() CoreCapabilities {
				bundle := complete
				bundle.DryRun = typedNilDryRun
				return bundle
			}(),
			ready: CoreCapabilities.PreparedDryRunReady,
			otherReady: []func(CoreCapabilities) bool{
				CoreCapabilities.PreparedUploadReady,
				CoreCapabilities.PreparedDupeReady,
				CoreCapabilities.PreparedDVDReady,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.ready(tt.bundle) {
				t.Fatal("expected typed-nil operation capability to be unavailable")
			}
			for _, ready := range tt.otherReady {
				if !ready(tt.bundle) {
					t.Fatal("expected valid sibling operation capability to remain ready")
				}
			}
			if !tt.bundle.Available() {
				t.Fatal("expected valid partial bundle capabilities to remain available")
			}
		})
	}
}
