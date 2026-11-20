// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/comparisons"
	"github.com/autobrr/upbrr/pkg/api"
)

type comparisonFrameMediaFake struct {
	comparisonMediaFake
	applied  int
	previews int
}

func (b *comparisonFrameMediaFake) PreviewComparisonFrame(_ context.Context, _ api.ReleaseRef, _ api.MediaArtifactSet, _ any, source string, timestamp float64, step int, starting *MediaPreviewContent) (MediaPreviewContent, error) {
	b.previews++
	if step != 0 && (starting == nil || starting.ObservedIdentity == nil || starting.TimestampSeconds != timestamp) {
		return MediaPreviewContent{}, ErrPrivateResourceIntegrity
	}
	return MediaPreviewContent{
		ComparisonSourceID: source,
		TimestampSeconds:   timestamp,
		ObservedIdentity: &api.ComparisonFrameIdentity{
			Version:             1,
			InputFingerprint:    "source-version",
			Occurrence:          "title-cell-1",
			StreamID:            "0xe0",
			PTS:                 900000,
			TimeBaseNumerator:   1,
			TimeBaseDenominator: 90000,
		},
	}, nil
}

func (b *comparisonFrameMediaFake) ApplyComparisonFrames(_ context.Context, media api.MediaArtifactSet, private RetainedMediaResource, selections []api.ComparisonFrameSelection, previews []MediaPreviewContent) (api.MediaArtifactSet, RetainedMediaResource, error) {
	b.applied++
	for i, selection := range selections {
		for r := range media.Comparisons.Rows {
			row := &media.Comparisons.Rows[r]
			if row.ID != selection.RowID {
				continue
			}
			for c := range row.Cells {
				if row.Cells[c].SourceID == selection.SourceID {
					row.Cells[c].ObservedIdentity = previews[i].ObservedIdentity
				}
			}
		}
	}
	fingerprint, err := comparisons.ReviewFingerprint(*media.Comparisons)
	if err != nil {
		return media, private, fmt.Errorf("fingerprint selected preview fixture: %w", err)
	}
	media.Comparisons.Fingerprint = fingerprint
	media.CaptureFingerprint = fingerprint
	return media, private, nil
}

func TestComparisonPreviewSelectionRequiresCurrentScopedWitness(t *testing.T) {
	for _, scenario := range []string{"current", "expired", "other owner", "other workflow", "old revision", "other media", "other column"} {
		t.Run(scenario, func(t *testing.T) {
			module, repository, _, current := newDuplicateReuseWorkflow(t)
			builder := &comparisonFrameMediaFake{}
			module.mediaBuilder = builder
			current = executeCommand(t, module, EditComparisonsCommand{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				Instructions:     api.ComparisonInstructions{Action: "configure", Plan: reviewedComparisonFixture(t)},
			})
			plan := *current.Media.Comparisons
			selection := api.ComparisonFrameSelection{RowID: plan.Rows[0].ID, SourceID: plan.Sources[0].ID}
			preview, err := module.PreviewComparisonFrame(t.Context(), testOwnerID, current.Workflow.ID, current.Workflow.Revision, selection.SourceID, 10, 0, "")
			if err != nil {
				t.Fatal(err)
			}
			selection.PreviewID = preview.ID
			if scenario == "current" {
				for _, invalid := range []struct {
					timestamp float64
					step      int
				}{{timestamp: 11, step: 1}, {timestamp: 10, step: 0}} {
					_, err := module.PreviewComparisonFrame(t.Context(), testOwnerID, current.Workflow.ID, current.Workflow.Revision, selection.SourceID, invalid.timestamp, invalid.step, preview.ID)
					if err == nil || builder.previews != 1 {
						t.Fatalf("invalid preview continuation reached the renderer: calls=%d err=%v", builder.previews, err)
					}
				}
			}
			if scenario != "current" {
				resourceID := previewPrivateResourceID(preview.ID)
				value, err := module.private.Get(testOwnerID, current.Workflow.ID, resourceID, module.clock.Now())
				if err != nil {
					t.Fatal(err)
				}
				content, ok := value.(MediaPreviewContent)
				if !ok {
					t.Fatal("preview-private resource type changed")
				}
				owner, expiry := testOwnerID, preview.ExpiresAt
				switch scenario {
				case "expired":
					expiry = module.clock.Now().Add(-time.Second)
				case "other owner":
					module.private.Delete(owner, current.Workflow.ID, resourceID)
					owner = "other-owner"
				case "other workflow":
					content.WorkflowID = "other-workflow"
				case "old revision":
					content.WorkflowRevision--
				case "other media":
					content.Media = &api.MediaArtifactSetRef{ID: "other-media", Revision: content.Media.Revision}
				case "other column":
					content.ComparisonSourceID = plan.Sources[1].ID
				}
				if err := module.private.Put(owner, current.Workflow.ID, resourceID, content, expiry); err != nil {
					t.Fatal(err)
				}
			}
			_, stepErr := module.PreviewComparisonFrame(t.Context(), testOwnerID, current.Workflow.ID, current.Workflow.Revision, selection.SourceID, 10, 1, preview.ID)
			if (stepErr == nil) != (scenario == "current") {
				t.Fatalf("%s step bound the wrong preview: %v", scenario, stepErr)
			}
			result, err := module.execute(t.Context(), testOwnerID, EditComparisonsCommand{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				Media:            current.Workflow.Media,
				Instructions: api.ComparisonInstructions{
					Action:          "configure",
					Plan:            plan,
					FrameSelections: []api.ComparisonFrameSelection{selection},
				},
			})
			if scenario == "current" {
				if err != nil || builder.applied != 1 || result.Media.Comparisons.Rows[0].Cells[0].ObservedIdentity == nil || *result.Media.Comparisons.Rows[0].Cells[0].ObservedIdentity != *preview.ObservedIdentity {
					t.Fatalf("current preview identity was not retained: %+v %v", result.Media, err)
				}
				return
			}
			if err == nil || builder.applied != 0 {
				t.Fatalf("%s preview was applied: %v", scenario, err)
			}
			state, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
			if err != nil || state.Workflow.Revision != current.Workflow.Revision || *state.Workflow.Media != *current.Workflow.Media {
				t.Fatalf("rejected preview changed applied authority: %+v %v", state.Workflow.Media, err)
			}
		})
	}
}
