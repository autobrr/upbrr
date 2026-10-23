// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/releaseworkflow"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestDebugInputLanguagesDoNotRequireCorrection(t *testing.T) {
	// No backend or input is available: diagnostic execution must not evaluate
	// readiness, prompt, or write canonical corrections through this hook.
	session := &cliWorkflowSession{}
	if err := session.reviewInputLanguages(context.Background(), nil, true, nil); err != nil {
		t.Fatal(err)
	}
}

func TestCollectCLIInputLanguagesBatchesCanonicalCorrections(t *testing.T) {
	fields := []api.InputReadinessFieldOutcome{
		{
			TrackID:         "one",
			CorrectionField: new(api.CorrectionFieldMetadataTrackLanguages),
			Status:          api.InputReadinessFieldMissing,
			TrackerIDs:      []api.TrackerID{"ONE", "TWO"},
		},
		{
			TrackID:         "one",
			CorrectionField: new(api.CorrectionFieldMetadataTrackLanguages),
			Status:          api.InputReadinessFieldMissing,
		},
		{
			TrackID:         "two",
			CorrectionField: new(api.CorrectionFieldMetadataTrackLanguages),
			Status:          api.InputReadinessFieldMissing,
		},
	}
	tracks := []api.MediaTrackFacts{{
		ID:                  "one",
		Ordinal:             1,
		ManifestFingerprint: "current",
	}, {
		ID:                  "two",
		Ordinal:             2,
		ManifestFingerprint: "current",
	}}
	for _, mode := range []api.InteractionMode{api.InteractionModeInteractive, api.InteractionModeUnattendedConfirm} {
		var output bytes.Buffer
		corrections, err := collectCLIInputLanguages(bufio.NewReader(strings.NewReader("jpn\nJapanese, English\n")), &output, mode, fields, tracks)
		if err != nil {
			t.Fatal(err)
		}
		if len(corrections) != 2 || corrections[0].ManifestFingerprint != "current" || corrections[0].Languages[0] != "Japanese" || len(corrections[1].Languages) != 2 {
			t.Fatalf("corrections: %#v", corrections)
		}
		if strings.Count(output.String(), "Programme audio track") != 2 {
			t.Fatalf("duplicate prompts: %s", output.String())
		}
	}
	var output bytes.Buffer
	corrections, err := collectCLIInputLanguages(bufio.NewReader(strings.NewReader("")), &output, api.InteractionModeUnattended, fields, tracks)
	if err != nil || len(corrections) != 0 || !strings.Contains(output.String(), "--track-languages") || strings.Contains(output.String(), "Programme audio track") {
		t.Fatalf("unattended prompted or lost guidance: %v %s", err, output.String())
	}
	for _, value := range []string{"und", "mul", "unknown-language"} {
		if _, err := collectCLIInputLanguages(bufio.NewReader(strings.NewReader(value+"\n")), &bytes.Buffer{}, api.InteractionModeInteractive, fields[:1], tracks); err == nil {
			t.Fatalf("accepted unresolved answer %q", value)
		}
	}
	if _, err := collectCLIInputLanguages(bufio.NewReader(strings.NewReader("Japanese\n")), &bytes.Buffer{}, api.InteractionModeInteractive, fields[:1], nil); err == nil {
		t.Fatal("accepted stale track")
	}
}

func TestLanguageReviewTrackerScopeExcludesRemovedSelections(t *testing.T) {
	selected := []api.TrackerID{"ONE", "TWO"}
	for _, removed := range [][]string{{"two"}, {"TWO"}} {
		got := languageReviewTrackerScope(selected, removed)
		if len(got) != 1 || got[0] != "ONE" {
			t.Fatalf("scope: %v", got)
		}
	}
	if selected[1] != "TWO" {
		t.Fatal("selection was mutated")
	}
	if got := languageReviewTrackerScope(selected, []string{"ONE", "TWO"}); len(got) != 0 {
		t.Fatalf("removed scope: %v", got)
	}
}

func TestInputLanguageReviewUsesReadinessBeforeProjection(t *testing.T) {
	for _, test := range []struct {
		name                        string
		selected, defaults, removed []string
	}{
		{name: "explicit", selected: []string{"AITHER"}},
		{
			name:     "configured default",
			defaults: []string{"AITHER", "BHD", "UNREGISTERED"},
			removed:  []string{"bhd"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := releaseworkflow.CommandResult{
				Workflow:    api.ReleaseWorkflow{ID: "workflow", Revision: 4},
				Release:     &api.ReleaseSnapshot{Release: api.PreparedRelease{Media: api.MediaFacts{Tracks: []api.MediaTrackFacts{{ID: "track", ManifestFingerprint: "current"}}}}},
				Corrections: &api.ReleaseCorrectionsSnapshot{Revision: 2},
				InputReadiness: &api.InputReadinessSnapshot{Fields: []api.InputReadinessFieldOutcome{{
					TrackID:         "track",
					CorrectionField: new(api.CorrectionFieldMetadataTrackLanguages),
					Status:          api.InputReadinessFieldMissing,
					TrackerIDs:      []api.TrackerID{"AITHER"},
				}}},
			}
			core := &cliWorkflowCoreFake{current: current}
			core.continueFn = func(request api.ContinueReleaseWorkflowRequest) (releaseworkflow.CommandResult, error) {
				if request.Goal != api.WorkflowGoalInputReady {
					t.Fatalf("projected before source correction: %s", request.Goal)
				}
				if len(request.Intent.TrackerIDs) != 1 || request.Intent.TrackerIDs[0] != "AITHER" {
					t.Fatalf("scope: %v", request.Intent.TrackerIDs)
				}
				if request.Authority == nil || request.Authority.ExpectedRevision != 4 {
					t.Fatal("lost workflow authority")
				}
				return current, nil
			}
			var output bytes.Buffer
			session := &cliWorkflowSession{
				core:          core,
				current:       current,
				logger:        api.NopLogger{},
				intent:        cliWorkflowIntent{interaction: api.InteractionModeInteractive},
				uploadRequest: api.Request{Trackers: test.selected, TrackersRemove: test.removed},
				streams:       cliIO{out: &output},
			}
			if err := session.reviewInputLanguages(t.Context(), bufio.NewReader(strings.NewReader("Japanese\n")), false, test.defaults); err != nil {
				t.Fatal(err)
			}
			if len(core.continuations) != 2 {
				t.Fatalf("continuations: %d", len(core.continuations))
			}
			patch := core.continuations[1].Intent.CorrectionPatch
			if patch == nil || patch.ExpectedRevision == nil || *patch.ExpectedRevision != 2 || len(patch.Values.Metadata.TrackLanguages) != 1 {
				t.Fatalf("missing bound correction: %#v", patch)
			}
		})
	}
}

func TestInputLanguageReviewPreservesSourceOnlyAndHistoryCompletion(t *testing.T) {
	session := &cliWorkflowSession{}
	if err := session.reviewInputLanguages(t.Context(), nil, false, nil); err != nil {
		t.Fatal(err)
	}
	session.current.Workflow = api.ReleaseWorkflow{Status: api.WorkflowStatusCompleted, SubmissionExclusions: []api.SubmissionExclusion{{}}}
	if err := session.reviewInputLanguages(t.Context(), nil, false, []string{"AITHER"}); err != nil {
		t.Fatal(err)
	}
}
