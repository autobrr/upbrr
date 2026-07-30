// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

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
