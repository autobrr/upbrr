// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestCLILanguageReviewUsesBackendDecisions(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name              string
		outcome           trackers.LanguageOutcome
		mode              api.WorkflowExecutionMode
		disc              bool
		independentStrict bool
		wantReady         bool
		wantWaiver        bool
	}{
		{name: "prohibited", outcome: trackers.LanguageProhibited},
		{name: "staff exception", outcome: trackers.LanguageStaffException},
		{
			name:       "trumpable",
			outcome:    trackers.LanguageTrumpable,
			wantWaiver: true,
		},
		{
			name:              "independent strict",
			outcome:           trackers.LanguageTrumpable,
			independentStrict: true,
		},
		{
			name:      "full disc exempt",
			outcome:   trackers.LanguageProhibited,
			disc:      true,
			wantReady: true,
		},
		{
			name:      "debug bypass",
			outcome:   trackers.LanguageProhibited,
			mode:      api.WorkflowExecutionModeDebug,
			wantReady: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := api.TrackerValidationSubject{Tracker: "EXAMPLE", LanguageFacts: api.LanguageFacts{
				OriginalLanguages:  []string{"Japanese"},
				ProgrammeLanguages: []string{"Japanese", "English", "German"},
				ProgrammeStatus:    api.MetadataEvidenceStatusComplete,
				AudioStatus:        api.MetadataEvidenceStatusComplete,
				SubtitleStatus:     api.MetadataEvidenceStatusComplete,
			}}
			if test.disc {
				subject.DiscType = "BDMV"
			}
			failures := trackers.EvaluateLanguagePolicy(subject, trackers.LanguagePolicy{ExtraDubs: test.outcome})
			if test.independentStrict {
				failures = append(failures, trackers.NewRuleFailure("required_resource", "Required resource is missing.", api.RuleDispositionStrict))
			}
			projection := api.TrackerReleaseProjection{
				TrackerID:   "EXAMPLE",
				DisplayName: "EXAMPLE",
				Readiness:   api.ReadinessStatusReady,
				DupeReady:   true,
				UploadReady: true,
			}
			if err := trackers.ApplyProjectionRuleFailures(&projection, failures, test.mode, "", nil); err != nil {
				t.Fatal(err)
			}
			if projection.DupeReady != test.wantReady || (len(projection.RequiredActions) > 0) != test.wantWaiver {
				t.Fatalf("backend state=%+v", projection)
			}
			printed := captureWriter(func(output io.Writer) {
				printCLIWorkflowProjections(output, &api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{projection}}, nil, true)
			})
			if !test.disc {
				for _, detail := range []string{string(test.outcome), "EXAMPLE", "Original: Japanese", "programme audio: Japanese, English, German", "German"} {
					if !strings.Contains(printed, detail) {
						t.Fatalf("CLI omitted %q: %s", detail, printed)
					}
				}
			} else if strings.Contains(printed, "language_") {
				t.Fatalf("full-disc language review appeared: %s", printed)
			}
			if test.mode == api.WorkflowExecutionModeDebug && !strings.Contains(printed, "decision=bypassed blocking=false") {
				t.Fatalf("debug bypass hidden: %s", printed)
			}
			for _, interaction := range []api.InteractionMode{api.InteractionModeUnattended, api.InteractionModeUnattendedConfirm} {
				var output bytes.Buffer
				session := cliWorkflowSession{
					intent:  cliWorkflowIntent{interaction: interaction},
					streams: cliIO{out: &output},
					current: releaseworkflow.CommandResult{Workflow: api.ReleaseWorkflow{Revision: 7}, Continuation: api.WorkflowContinuation{RequiredActions: projection.RequiredActions}},
				}
				var reader *bufio.Reader
				if interaction == api.InteractionModeUnattendedConfirm {
					reader = bufio.NewReader(strings.NewReader("y\n"))
				}
				answers, declined, err := session.collectContinuationActionAnswers(t.Context(), reader, config.Config{}, api.NopLogger{})
				if err != nil || declined {
					t.Fatalf("review %s: %v declined=%t", interaction, err, declined)
				}
				wantAnswer := test.wantWaiver && interaction == api.InteractionModeUnattendedConfirm
				if (len(answers) > 0) != wantAnswer || (output.Len() > 0) != wantAnswer {
					t.Fatalf("review %s answers=%+v output=%q", interaction, answers, output.String())
				}
				if wantAnswer && (!strings.Contains(output.String(), "Trumpable release") || !strings.Contains(output.String(), "A compliant replacement may supersede") || answers[0].Confirmed == nil || !*answers[0].Confirmed) {
					t.Fatalf("trumpable acknowledgement missing consequence/confirmation: %+v %s", answers, output.String())
				}
			}
		})
	}
}
