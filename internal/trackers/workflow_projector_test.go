// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestWorkflowProjectorUsesContextLogger(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.RegisterDescriptor(Descriptor{
		Name:              "EXAMPLE",
		Definition:        stubDefinition{name: "EXAMPLE"},
		UploadContentMode: UploadContentModeDescription,
		Validation: ValidationPolicyBinding{
			ID: "example-validation-v1",
			Check: func(context.Context, api.TrackerValidationSubject, api.Logger) ([]api.RuleFailure, error) {
				return []api.RuleFailure{NewRuleFailure("example_rule", "example reason", api.RuleDispositionStrict)}, nil
			},
		},
	}); err != nil {
		t.Fatalf("register tracker: %v", err)
	}
	rootLogger := &warningLogger{}
	contextLogger := &warningLogger{}
	projector, err := NewWorkflowProjector(registry, config.Config{}, rootLogger)
	if err != nil {
		t.Fatalf("new workflow projector: %v", err)
	}

	ctx := logging.WithOperationLogger(context.Background(), contextLogger)
	if _, _, _, _, err := projector.Build(
		ctx,
		api.ReleaseSnapshot{},
		api.UploadSubject{ReleaseName: "Example.Release.2026.1080p-GRP"},
		[]api.TrackerID{"EXAMPLE"},
		nil,
		nil,
		api.WorkflowExecutionModeNormal,
	); err != nil {
		t.Fatalf("build projections: %v", err)
	}
	if len(rootLogger.warnings) != 0 || len(contextLogger.warnings) != 1 {
		t.Fatalf("projection warnings: root=%#v context=%#v", rootLogger.warnings, contextLogger.warnings)
	}
}

func TestResolveTrackerIDsIgnoresUnsupportedConfiguredDefaults(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(stubDefinition{name: "SUPPORTED"}); err != nil {
		t.Fatalf("register tracker: %v", err)
	}
	logger := &warningLogger{}
	projector, err := NewWorkflowProjector(registry, config.Config{
		Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"SUPPORTED", "RETIRED"}},
	}, logger)
	if err != nil {
		t.Fatalf("new workflow projector: %v", err)
	}

	selected, err := projector.resolveTrackerIDs(nil)
	if err != nil {
		t.Fatalf("resolve configured defaults: %v", err)
	}
	if len(selected) != 1 || selected[0] != "SUPPORTED" {
		t.Fatalf("selected trackers = %v, want [SUPPORTED]", selected)
	}
	if len(logger.warnings) != 1 || logger.warnings[0] != "trackers: projection tracker=RETIRED state=unregistered decision=skip count=1" {
		t.Fatalf("configured default warnings = %#v", logger.warnings)
	}

	if _, err := projector.resolveTrackerIDs([]api.TrackerID{"RETIRED"}); err == nil {
		t.Fatal("expected explicit unsupported tracker to fail")
	}
}

func TestApplyWorkflowProjectionRequirementsDoesNotInferDVDMenuMinimumFromCaptureCap(t *testing.T) {
	t.Parallel()

	cfg := config.Config{}
	cfg.ScreenshotHandling.Screens = 4
	cfg.ScreenshotHandling.MaxMenuItems = 6
	projection := api.TrackerReleaseProjection{}

	applyWorkflowProjectionRequirements(
		&projection,
		Descriptor{Name: "EXAMPLE", UploadContentMode: UploadContentModeDescription},
		api.UploadSubject{DiscType: "DVD"},
		cfg,
	)

	if projection.Artifacts.ScreenshotCount != 4 || projection.Artifacts.DVDMenuCount != 0 {
		t.Fatalf("projected media requirements = %#v", projection.Artifacts)
	}
}

func TestApplyWorkflowProjectionRequirementsKeepsExplicitDVDMenuMinimum(t *testing.T) {
	t.Parallel()

	projection := api.TrackerReleaseProjection{}
	applyWorkflowProjectionRequirements(
		&projection,
		Descriptor{
			Name:              "EXAMPLE",
			UploadContentMode: UploadContentModeDescription,
			WorkflowMedia:     &WorkflowMediaRequirements{DVDMenuCount: 2},
		},
		api.UploadSubject{DiscType: "DVD"},
		config.Config{},
	)

	if projection.Artifacts.DVDMenuCount != 2 {
		t.Fatalf("projected DVD menu minimum = %d, want 2", projection.Artifacts.DVDMenuCount)
	}
}
