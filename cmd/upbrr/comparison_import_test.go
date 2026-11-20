// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

func cliComparisonFolder(t *testing.T) string {
	t.Helper()
	folder := t.TempDir()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewGray(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	for row := 1; row <= 2; row++ {
		for group, label := range []string{"Source", "Encode", "Filtered"} {
			if err := os.WriteFile(filepath.Join(folder, fmt.Sprintf("%02d-%02d-%s.png", row, group+1, label)), data.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return folder
}

func TestCLIComparisonMainChoicePrecedesUpload(t *testing.T) {
	folder := cliComparisonFolder(t)
	request := api.CreateReleaseWorkflowUploadRequest{Media: api.ReleaseWorkflowUploadMedia{Screenshots: api.ReleaseWorkflowUploadScreenshots{ComparisonPaths: []string{folder}, ComparisonReuseMain: true}}}
	for _, mode := range []api.InteractionMode{api.InteractionModeInteractive, api.InteractionModeUnattendedConfirm, api.InteractionModeUnattended} {
		t.Run(string(mode), func(t *testing.T) {
			var output bytes.Buffer
			input := strings.NewReader("2\n")
			session := &cliWorkflowSession{
				intent:  cliWorkflowIntent{interaction: mode},
				streams: cliIO{out: &output},
			}
			_, resolved, err := session.resolveCompositeUploadInputs(t.Context(), bufio.NewReader(input), request)
			if mode == api.InteractionModeUnattended {
				if err == nil || !strings.Contains(err.Error(), "--comparison_index") || input.Len() != 2 {
					t.Fatalf("unattended import: err=%v input remaining=%d", err, input.Len())
				}
			} else if err != nil || resolved.Media.Screenshots.ComparisonPrimaryIndex == nil || *resolved.Media.Screenshots.ComparisonPrimaryIndex != 2 || !resolved.Media.Screenshots.ComparisonReuseMain {
				t.Fatalf("main source group was not retained: %+v %v", resolved, err)
			}
		})
	}
}

func TestCLIComparisonReviewRequiresExplicitRetainedImageApproval(t *testing.T) {
	for _, test := range []struct {
		mode    api.InteractionMode
		answer  string
		approve bool
	}{
		{api.InteractionModeInteractive, "y\n", true},
		{api.InteractionModeUnattendedConfirm, "y\n", true},
		{api.InteractionModeInteractive, "\n", false},
		{api.InteractionModeUnattendedConfirm, "n\n", false},
		{api.InteractionModeUnattended, "y\n", false},
	} {
		t.Run(string(test.mode)+fmt.Sprintf("-%q", test.answer), func(t *testing.T) {
			var output bytes.Buffer
			input := strings.NewReader(test.answer)
			session := &cliWorkflowSession{
				core:    &cliWorkflowCoreFake{},
				intent:  cliWorkflowIntent{interaction: test.mode},
				streams: cliIO{out: &output},
				current: releaseworkflow.CommandResult{Workflow: api.ReleaseWorkflow{ID: "workflow", Revision: 3}, Media: &api.MediaArtifactSet{
					ID:       "media",
					Revision: 3,
					Comparisons: &api.ComparisonPlan{
						MainSourceID:         "2",
						ReuseMainScreenshots: true,
						Fingerprint:          "retained-pixels",
						Sources:              []api.ComparisonSource{{ID: "1", Label: "Source"}, {ID: "2", Label: "Encode"}},
						Rows:                 []api.ComparisonRow{{ID: "1", Cells: []api.ComparisonCell{{SourceID: "1", ArtifactID: "source-image"}, {SourceID: "2", ArtifactID: "main-image"}}}},
					},
				},
				},
			}
			action := api.RequiredAction{
				ID:               "review",
				Kind:             api.RequiredActionReviewComparisons,
				WorkflowRevision: 3,
				Prompt:           "Review comparisons",
			}
			feedback, declined, err := session.collectCompositeUploadFeedback(t.Context(), bufio.NewReader(input), config.Config{}, api.NopLogger{}, action)
			switch {
			case test.mode == api.InteractionModeUnattended:
				if err == nil || !strings.Contains(err.Error(), "--uac") || input.Len() != 2 || feedback.Response.ComparisonApproval != nil {
					t.Fatalf("unattended approved or prompted: %+v %v", feedback, err)
				}
			case !test.approve:
				if err != nil || !declined || feedback.Response.ComparisonApproval != nil {
					t.Fatalf("unconfirmed pixels approved: %+v declined=%t err=%v", feedback, declined, err)
				}
			default:
				if err != nil || declined || feedback.Response.Kind != api.ReleaseWorkflowUploadFeedbackComparisonApproval || feedback.Response.ComparisonApproval.ExpectedFingerprint != "retained-pixels" ||
					!strings.Contains(output.String(), filepath.Join("test-output", "main-image.png")) || !strings.Contains(output.String(), "normal screenshots: true") {
					t.Fatalf("review failed: %+v declined=%t err=%v output=%s", feedback, declined, err, output.String())
				}
			}
		})
	}
}
