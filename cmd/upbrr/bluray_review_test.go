// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"bytes"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestCLIReviewBlurayNumberAllSkipAndUnattended(t *testing.T) {
	for _, tc := range []struct {
		name, answers, wantMode, wantID string
		mode                            api.InteractionMode
		scans                           int
	}{
		{
			name:     "number",
			answers:  "2\n",
			wantMode: "manual",
			wantID:   "102",
			mode:     api.InteractionModeInteractive,
		},
		{
			name:     "all then confirmation",
			answers:  "a\n1\n",
			wantMode: "manual",
			wantID:   "101",
			mode:     api.InteractionModeUnattendedConfirm,
			scans:    1,
		},
		{
			name:     "skip",
			answers:  "n\n",
			wantMode: "skip",
			mode:     api.InteractionModeInteractive,
		},
		{
			name:     "invalid then skip",
			answers:  "3\nn\n",
			wantMode: "skip",
			mode:     api.InteractionModeInteractive,
		},
		{
			name:    "unattended",
			answers: "",
			mode:    api.InteractionModeUnattended,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := releaseworkflow.CommandResult{
				Workflow: api.ReleaseWorkflow{ID: "workflow", Revision: 4},
				Release: &api.ReleaseSnapshot{Release: api.PreparedRelease{ProviderMetadata: api.SourceScopedMetadata{Bluray: &api.BlurayMetadata{Candidates: []api.BlurayReleaseCandidate{
					{
						ReleaseID: "101",
						Title:     "Example A",
						Country:   "United States",
						Publisher: "Example Publisher",
					},
					{
						ReleaseID: "102",
						Title:     "Example B",
						Country:   "United States",
						Publisher: "Example Publisher",
					},
				}}}}},
				FactInstructions: &api.ReleaseFactInstructionSnapshot{Instructions: api.ReleaseFactInstructions{Metadata: api.MetadataOverrides{Title: new("Manual title")}}},
			}
			core := &cliWorkflowCoreFake{current: current}
			scans := 0
			core.continueFn = func(request api.ContinueReleaseWorkflowRequest) (releaseworkflow.CommandResult, error) {
				input := request.Intent.Preparation
				if request.Authority == nil || request.Authority.ExpectedRevision != 4 || input == nil || !input.Force || input.Instructions.Metadata.Title == nil || *input.Instructions.Metadata.Title != "Manual title" {
					t.Fatal("selection lost exact workflow/correction authority")
				}
				if details := input.Controls.BlurayDetails; details != nil {
					if !details.All || details.ReleaseID != "" {
						t.Fatalf("unexpected detail request: %#v", details)
					}
					scans++
					for i := range current.Release.Release.ProviderMetadata.Bluray.Candidates {
						current.Release.Release.ProviderMetadata.Bluray.Candidates[i].DetailsStatus = "complete"
					}
				}
				return current, nil
			}
			var output bytes.Buffer
			session := &cliWorkflowSession{
				core:    core,
				current: current,
				logger:  api.NopLogger{},
				intent:  cliWorkflowIntent{sourcePath: "Example.Disc", interaction: tc.mode},
				streams: cliIO{out: &output},
			}
			if err := session.reviewBlurayReleases(t.Context(), bufio.NewReader(strings.NewReader(tc.answers))); err != nil {
				t.Fatal(err)
			}
			if scans != tc.scans {
				t.Fatalf("scans=%d want=%d", scans, tc.scans)
			}
			if tc.mode == api.InteractionModeUnattended {
				if len(core.continuations) != 0 || output.Len() != 0 {
					t.Fatal("unattended prompted or fetched details")
				}
				return
			}
			if session.uploadRequest.Bluray == nil || session.uploadRequest.Bluray.Mode != tc.wantMode || session.uploadRequest.Bluray.Release != tc.wantID {
				t.Fatalf("selection=%#v", session.uploadRequest.Bluray)
			}
			if !strings.Contains(output.String(), "a. Fetch details") || !strings.Contains(output.String(), "n. Skip") || !strings.Contains(output.String(), "2. Example B") {
				t.Fatalf("initial choices missing: %s", output.String())
			}
		})
	}
}

func TestCLIBlurayReleaseFlagKeepsBlutopiaArgument(t *testing.T) {
	for _, tc := range []struct{ value, mode string }{{"123", "manual"}, {"https://www.blu-ray.com/movies/Example/123/", "manual"}, {"", "skip"}, {"auto", "auto"}} {
		options, visited, paths, err := parseCLIOptions([]string{"Example.Release.2026", "--bluray-release=" + tc.value, "--blu", "456"})
		if err != nil {
			t.Fatal(err)
		}
		request, err := buildCLIRequest(options, visited, paths, 0)
		if err != nil {
			t.Fatal(err)
		}
		input, err := api.MapPreparationRequest(request, api.PreparationIntentPreview)
		if err != nil || input.Instructions.Bluray == nil || input.Instructions.Bluray.Mode != tc.mode || request.TrackerIDOverrides["blu"] != "456" {
			t.Fatalf("flag mapping lost selection or --blu: %#v %v", request, err)
		}
	}
	options, visited, paths, err := parseCLIOptions([]string{"Example.Release.2026", "--bluray-release", "https://evil.invalid/movies/Example/123/"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := buildCLIRequest(options, visited, paths, 0); err == nil {
		t.Fatal("accepted invalid release override")
	}
}
