// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveTrackerIDsIgnoresUnsupportedConfiguredDefaults(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(stubDefinition{name: "SUPPORTED"}); err != nil {
		t.Fatalf("register tracker: %v", err)
	}
	projector, err := NewWorkflowProjector(registry, config.Config{
		Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"SUPPORTED", "RETIRED"}},
	}, nil)
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
