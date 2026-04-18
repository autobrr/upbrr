// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestMergeTrackerImagesIntoFinalSelectionsReindexesSparseIndices(t *testing.T) {
	tmpDir := t.TempDir()
	for _, name := range []string{"a.png", "b.png", "c.png", "d.png"} {
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte("png"), 0o600); err != nil {
			t.Fatalf("write temp image: %v", err)
		}
	}

	finalSelections := []api.ScreenshotImage{
		{Index: 0, Path: filepath.Join(tmpDir, "a.png")},
		{Index: 2, Path: filepath.Join(tmpDir, "b.png")},
		{Index: 5, Path: filepath.Join(tmpDir, "c.png")},
	}
	trackerLinks := []api.ScreenshotLinkedImage{
		{Path: filepath.Join(tmpDir, "c.png")},
		{Path: filepath.Join(tmpDir, "d.png")},
	}

	merged := mergeTrackerImagesIntoFinalSelections(finalSelections, trackerLinks)
	if len(merged) != 4 {
		t.Fatalf("expected 4 merged screenshots, got %d", len(merged))
	}
	for idx, image := range merged {
		if image.Index != idx {
			t.Fatalf("expected image %d to be reindexed to %d, got %d (%#v)", idx, idx, image.Index, merged)
		}
	}
	if merged[3].Path != filepath.Join(tmpDir, "d.png") {
		t.Fatalf("expected new tracker image appended after existing selections, got %#v", merged)
	}
}

func TestPlanUsesManualFrameOverridesWithoutDuration(t *testing.T) {
	tmpDir := t.TempDir()
	mediaInfoPath := filepath.Join(tmpDir, "mediainfo.json")
	payload := map[string]any{
		"media": map[string]any{
			"track": []map[string]any{
				{
					"@type":     "Video",
					"FrameRate": "24.000",
				},
			},
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal mediainfo: %v", err)
	}
	if err := os.WriteFile(mediaInfoPath, encoded, 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}

	service := NewService(config.Config{}, api.NopLogger{}, tmpDir, nil)
	meta := api.PreparedMetadata{
		SourcePath:        filepath.Join(tmpDir, "movie.mkv"),
		MediaInfoJSONPath: mediaInfoPath,
		ScreenshotOverrides: api.ScreenshotOverrides{
			ManualFrames: []int{120, 360, 600},
		},
	}

	plan, err := service.Plan(context.Background(), meta, 4)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.RequiresManualFrames {
		t.Fatalf("expected manual frame override to satisfy screenshot plan, got %#v", plan)
	}
	if len(plan.SuggestedSelections) != 3 {
		t.Fatalf("expected 3 manual selections, got %#v", plan.SuggestedSelections)
	}
	if plan.SuggestedSelections[0].Frame != 120 || plan.SuggestedSelections[0].Source != "manual" {
		t.Fatalf("expected first manual selection, got %#v", plan.SuggestedSelections[0])
	}
}
