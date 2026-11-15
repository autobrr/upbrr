// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestComparisonFrameSelectionInvalidatesOnlyChangedMainMeasurements(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Example.Video.mkv")
	if err := os.WriteFile(path, []byte("synthetic comparison source"), 0o600); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := comparisonVideoFingerprint(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	identity := api.ComparisonFrameIdentity{
		Version:             1,
		InputFingerprint:    fingerprint,
		PTS:                 90000,
		TimeBaseNumerator:   1,
		TimeBaseDenominator: 90000,
	}
	snapshot := api.MediaArtifactSet{Comparisons: &api.ComparisonPlan{
		MainSourceID: "main",
		Sources: []api.ComparisonSource{
			{ID: "main", Fingerprint: fingerprint},
			{ID: "other", Fingerprint: fingerprint},
		},
		Rows: []api.ComparisonRow{{
			ID:    "row",
			Scene: "dark,motion",
			Cells: []api.ComparisonCell{
				{
					SourceID:         "main",
					TimestampSeconds: new(1.0),
					ObservedIdentity: &identity,
				},
				{
					SourceID:         "other",
					TimestampSeconds: new(1.0),
					Confidence:       new(.99),
					ArtifactID:       "retained-other",
				},
			},
		}},
		SceneMix: &api.ComparisonSceneMix{
			RequestedDark:   1,
			RequestedMotion: 1,
			AchievedDark:    1,
			AchievedMotion:  1,
		},
	}}
	retained := workflowMediaPrivateArtifacts{ComparisonSources: map[string]workflowComparisonSource{}}
	for _, id := range []string{"main", "other"} {
		retained.ComparisonSources[id] = workflowComparisonSource{
			Source: screenshots.ComparisonSource{Path: path}, Fingerprint: fingerprint,
		}
	}
	for _, tc := range []struct {
		name, source string
		seconds      float64
		invalidated  bool
	}{
		{"changed main", "main", 2, true},
		{"same main", "main", 1, false},
		{"changed secondary", "other", 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observed := identity
			observed.PTS = int64(tc.seconds * 90000)
			witness, err := json.Marshal(screenshots.ComparisonFrameReplay{Identity: observed})
			if err != nil {
				t.Fatal(err)
			}
			b := workflowMediaBuilder{}
			selected, _, err := b.ApplyComparisonFrames(t.Context(), snapshot, retained,
				[]api.ComparisonFrameSelection{{RowID: "row", SourceID: tc.source}},
				[]releaseworkflow.MediaPreviewContent{{
					TimestampSeconds:  tc.seconds,
					ObservedIdentity:  &observed,
					ComparisonWitness: witness,
				}})
			if err != nil {
				t.Fatal(err)
			}
			plan := selected.Comparisons
			if tc.invalidated {
				if plan.Rows[0].Scene != "" || plan.SceneMix != nil || plan.Rows[0].Cells[1].Confidence != nil {
					t.Fatalf("new main picture retained old measurements: %+v", plan)
				}
			} else if plan.Rows[0].Scene != "dark,motion" || plan.SceneMix == nil || plan.SceneMix.AchievedDark != 1 || plan.SceneMix.AchievedMotion != 1 {
				t.Fatalf("unchanged main picture lost scene measurements: %+v", plan)
			}
			if tc.source == "main" && (plan.Rows[0].Cells[1].ArtifactID != "retained-other" ||
				!tc.invalidated && plan.Rows[0].Cells[1].Confidence == nil) {
				t.Fatal("main selection discarded unaffected secondary pixels or confidence")
			}
			if snapshot.Comparisons.Rows[0].Scene == "" || snapshot.Comparisons.Rows[0].Cells[1].Confidence == nil {
				t.Fatal("selection mutated the previous snapshot")
			}
		})
	}
}

type comparisonProgressRenderer struct {
	comparisonFrameRenderer
}

func (comparisonProgressRenderer) SampleComparisonSource(ctx context.Context, request screenshots.ComparisonSampleRequest) ([]screenshots.ComparisonSample, error) {
	for _, completed := range []int{0, 500, 1000} {
		api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{Completed: completed, Total: 1000})
	}
	return []screenshots.ComparisonSample{{
		PTS:                      int64(request.StartSeconds),
		TimeBaseNumerator:        1,
		TimeBaseDenominator:      1,
		RelativeTimestampSeconds: request.StartSeconds,
	}}, nil
}

func TestComparisonFullScanScalesWindowProgressAcrossSource(t *testing.T) {
	var completed []int
	ctx := api.WithWorkflowProgressReporter(t.Context(), func(update api.WorkflowProgressUpdate) {
		completed = append(completed, update.Completed)
		if update.Total != 1000 {
			t.Fatalf("unexpected source progress total: %+v", update)
		}
	})
	frames, err := sampleWholeComparisonSource(ctx, comparisonProgressRenderer{}, screenshots.ComparisonSampleRequest{
		StartSeconds: 10, DurationSeconds: 160,
	})
	if err != nil || len(frames) != 3 {
		t.Fatalf("sampled windows=%d err=%v", len(frames), err)
	}
	if want := []int{0, 200, 400, 400, 600, 800, 800, 900, 1000}; !slices.Equal(completed, want) {
		t.Fatalf("source progress=%v want=%v", completed, want)
	}
}
