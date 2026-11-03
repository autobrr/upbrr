// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

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
	media := api.MediaFacts{Tracks: tracks, LanguageFacts: api.LanguageFacts{OriginalLanguagesKnown: true, OriginalLanguages: []string{"German"}}}
	media.Tracks[0].Title = "Main surround"
	media.Tracks[0].Codec = "AC-3"
	media.Tracks[0].DetectedLanguages = []string{"und"}
	for _, mode := range []api.InteractionMode{api.InteractionModeInteractive, api.InteractionModeUnattendedConfirm} {
		for _, terminal := range []bool{false, true} {
			var output bytes.Buffer
			reader := bufio.NewReader(strings.NewReader("jpn\nJapanese, English\n"))
			var presenter cliPresenter = &cliPlainPresenter{reader: reader, output: &output}
			var model *modelPresenter
			if terminal {
				tui := newCLITUIModel(t.Context(), newCLITUIBridge(), func() {}, nil, nil, false, false, true)
				tui.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
				model = &modelPresenter{model: tui, answers: []string{"jpn", "Japanese, English"}}
				presenter = model
			}
			writer := &cliPresentationWriter{
				Writer:    &output,
				ctx:       t.Context(),
				presenter: presenter,
				terminal:  terminal,
			}
			bindCLIQuestion(t.Context(), writer, "source-review", 8, "", "Current source generation")
			corrections, err := collectCLIInputLanguages(reader, writer, mode, fields, "Example.Release.mkv", media)
			if err != nil || len(corrections) != 2 || corrections[0].ManifestFingerprint != "current" || corrections[0].Languages[0] != "Japanese" || len(corrections[1].Languages) != 2 {
				t.Fatalf("corrections: %#v, %v", corrections, err)
			}
			text := output.String()
			if terminal {
				if len(model.prompts) != 2 || model.prompts[0].WorkflowID != "source-review" || model.prompts[0].Revision != 8 || model.prompts[0].ActionID != "" {
					t.Fatal("source review lost deduplication or current authority")
				}
				text = cliPlainPromptText(model.prompts[0])
			} else if strings.Count(text, "Programme audio track") != 2 {
				t.Fatal("source review repeated or omitted a language question")
			}
			for _, fact := range []string{"Example.Release.mkv", "Main surround", "AC-3", "German", "und", "ONE, TWO", "one=LANGUAGE", "--input-only"} {
				if !strings.Contains(text, fact) {
					t.Fatalf("language review lost prepared fact or recovery %q", fact)
				}
			}
		}
	}
	var output bytes.Buffer
	corrections, err := collectCLIInputLanguages(bufio.NewReader(strings.NewReader("")), &output, api.InteractionModeUnattended, fields, "Example.Release.mkv", api.MediaFacts{Tracks: tracks})
	if err != nil || len(corrections) != 0 || !strings.Contains(output.String(), "--track-languages") || strings.Contains(output.String(), "Programme audio track") {
		t.Fatalf("unattended prompted or lost guidance: %v %s", err, output.String())
	}
	for _, value := range []string{"und", "mul", "unknown-language"} {
		if _, err := collectCLIInputLanguages(bufio.NewReader(strings.NewReader(value+"\n")), &bytes.Buffer{}, api.InteractionModeInteractive, fields[:1], "Example.Release.mkv", api.MediaFacts{Tracks: tracks}); err == nil {
			t.Fatalf("accepted unresolved answer %q", value)
		}
	}
	if _, err := collectCLIInputLanguages(bufio.NewReader(strings.NewReader("Japanese\n")), &bytes.Buffer{}, api.InteractionModeInteractive, fields[:1], "Example.Release.mkv", api.MediaFacts{}); err == nil {
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
		originReview                bool
		terminal                    bool
	}{
		{name: "explicit", selected: []string{"AITHER"}},
		{
			name:         "combined source facts",
			selected:     []string{"AITHER"},
			originReview: true,
		},
		{
			name:         "combined source facts TUI",
			selected:     []string{"AITHER"},
			originReview: true,
			terminal:     true,
		},
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
			answers := "Japanese\n"
			if test.originReview {
				current.Release.Release.Media.Tracks[0].Kind = api.MediaTrackAudio
				current.Release.Release.Media.Tracks[0].Role = api.AudioRoleProgramme
				current.Release.Release.Media.Tracks[0].Languages = []string{"und"}
				current.Release.Release.Media.Tracks[0].DetectedLanguages = []string{"und"}
				current.InputReadiness.Fields = append(current.InputReadiness.Fields, api.InputReadinessFieldOutcome{
					TrackID:         "track",
					CorrectionField: new(api.CorrectionFieldMetadataTrackAudioOrigins),
					Status:          api.InputReadinessFieldMissing,
					TrackerIDs:      []api.TrackerID{"AITHER"},
				})
				answers += "original\n"
			}
			before := current.Release.Release.Media
			before.Tracks = slices.Clone(before.Tracks)
			for i := range before.Tracks {
				before.Tracks[i].Languages = slices.Clone(before.Tracks[i].Languages)
				before.Tracks[i].DetectedLanguages = slices.Clone(before.Tracks[i].DetectedLanguages)
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
			reader := bufio.NewReader(strings.NewReader(answers))
			var writer io.Writer = &output
			mode := api.InteractionModeInteractive
			var terminalPresenter *modelPresenter
			if test.terminal {
				mode = api.InteractionModeUnattendedConfirm
				model := newCLITUIModel(t.Context(), newCLITUIBridge(), func() {}, nil, nil, false, false, true)
				model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
				terminalPresenter = &modelPresenter{model: model, answers: strings.Split(strings.TrimSpace(answers), "\n")}
				writer = &cliPresentationWriter{
					Writer:    &output,
					presenter: terminalPresenter,
					ctx:       t.Context(),
					terminal:  true,
				}
			}
			session := &cliWorkflowSession{
				core:          core,
				current:       current,
				logger:        api.NopLogger{},
				intent:        cliWorkflowIntent{interaction: mode},
				uploadRequest: api.Request{Trackers: test.selected, TrackersRemove: test.removed},
				streams:       cliIO{out: writer},
			}
			if err := session.reviewInputLanguages(t.Context(), reader, false, test.defaults); err != nil {
				t.Fatal(err)
			}
			if len(core.continuations) != 2 {
				t.Fatalf("continuations: %d", len(core.continuations))
			}
			patch := core.continuations[1].Intent.CorrectionPatch
			want := &api.ReleaseCorrectionPatch{ExpectedRevision: new(uint64(2))}
			want.Values.Metadata.TrackLanguages = []api.TrackLanguageCorrection{{
				TrackID:             "track",
				Languages:           []string{"Japanese"},
				ManifestFingerprint: "current",
			}}
			if test.originReview {
				want.Values.Metadata.TrackAudioOrigins = []api.TrackAudioOriginCorrection{{
					TrackID:             "track",
					Origin:              api.TrackAudioOriginal,
					ManifestFingerprint: "current",
				}}
				text := output.String()
				if terminalPresenter != nil {
					if len(terminalPresenter.prompts) != 2 {
						t.Fatal("language and origin review did not each ask once")
					}
					for i, prompt := range terminalPresenter.prompts {
						if prompt.WorkflowID != "workflow" || prompt.Revision != 4 || prompt.ActionID != "" || prompt.ID != uint64(i+1) {
							t.Fatal("prompt lost its workflow binding")
						}
					}
					terminalPresenter.model.Update(cliPromptRequest{prompt: terminalPresenter.prompts[1], reply: make(chan cliAnswer, 1)})
					text = terminalPresenter.model.evidence.GetContent()
				}
				if !strings.Contains(text, "Effective track languages: Japanese") || !strings.Contains(text, "Inspected track languages: und") {
					t.Fatalf("origin prompt did not distinguish accepted and inspected languages: %s", text)
				}
			}
			if !reflect.DeepEqual(patch, want) {
				t.Fatalf("combined correction changed: got %#v, want %#v", patch, want)
			}
			if !reflect.DeepEqual(current.Release.Release.Media, before) {
				t.Fatal("prompt generation mutated the original media snapshot")
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
