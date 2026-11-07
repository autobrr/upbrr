// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/autobrr/upbrr/pkg/api"
)

type selectionIndexPresenter struct {
	modelPresenter
	index int
}

func (p *selectionIndexPresenter) Ask(ctx context.Context, prompt cliPrompt) (cliAnswer, error) {
	p.answers = []string{prompt.Options[p.index].Value}
	return p.modelPresenter.Ask(ctx, prompt)
}

func TestSelectionPresentationPreservesNumericValues(t *testing.T) {
	for _, kind := range []string{"metadata", "playlist", "multiselect", "select"} {
		for _, terminal := range []bool{false, true} {
			for index, want := range []string{"2", "1"} {
				t.Run(kind+"/terminal="+strconv.FormatBool(terminal)+"/row="+strconv.Itoa(index+1), func(t *testing.T) {
					answer := strconv.Itoa(index + 1)
					if kind == "select" {
						answer = want
					}
					reader := bufio.NewReader(strings.NewReader(answer + "\n"))
					var transcript strings.Builder
					var presenter cliPresenter = &cliPlainPresenter{reader: reader, output: &transcript}
					var terminalPresenter *selectionIndexPresenter
					if terminal {
						model := newCLITUIModel(t.Context(), newCLITUIBridge(), func() {}, nil, nil, false, false, true)
						model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
						terminalPresenter = &selectionIndexPresenter{
							model: model,
							index: index,
						}
						presenter = terminalPresenter
					}
					writer := &cliPresentationWriter{
						Writer:    &transcript,
						presenter: presenter,
						ctx:       t.Context(),
						terminal:  terminal,
					}
					action := api.RequiredAction{
						ID: "candidate",
 WorkflowRevision: 7,
 Prompt: "Choose Example Release from prepared candidates",
						Kind:    api.RequiredActionSelectMetadata,
						Options: []api.RequiredActionOption{{Label: "First", Value: "2"}, {Label: "Second", Value: "1"}},
					}
					bindCLIQuestion(t.Context(), writer, "workflow", action.WorkflowRevision, action.ID, action.Prompt)
					switch kind {
					case "metadata":
						got, err := selectCLICompositeOption(reader, writer, action, "Metadata")
						if err != nil || got != want {
							t.Fatalf("selected = %q, %v; want %q", got, err, want)
						}
					case "playlist":
						got, err := selectCLIWorkflowPlaylists(reader, writer, action, false)
						if err != nil || !slices.Equal(got, []string{want}) {
							t.Fatalf("selected = %v, %v; want %q", got, err, want)
						}
					default:
						projections := &api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{
							TrackerID: "ALPHA",
							Questionnaire: []api.TrackerQuestionnaireRequirement{{
								Key:      "choice",
								Kind:     kind,
								Options:  []string{"2", "1"},
								Required: true,
							}},
						}}}
						instructions := make(map[api.TrackerID]api.TrackerProjectionInstructions)
						changed, err := collectCLIWorkflowQuestionnaires(reader, writer, api.InteractionModeUnattendedConfirm, projections, instructions)
						if err != nil || !changed {
							t.Fatalf("collect questionnaire = %t, %v", changed, err)
						}
						got := instructions["ALPHA"].Questionnaire["choice"]
						if got == nil || *got != want {
							t.Fatalf("questionnaire did not preserve selected value %q", want)
						}
					}
					if terminal {
						p := terminalPresenter.prompts[0]
						if p.ActionID != action.ID || p.WorkflowID != "workflow" || p.Revision != 7 || !strings.Contains(p.Evidence, action.Prompt) || p.Help == "" {
							t.Fatal("selection lost current context or action binding")
						}
					} else {
						for _, label := range []string{"First", "Second"} {
							if kind == "metadata" || kind == "playlist" {
								if strings.Count(transcript.String(), label) != 1 {
									t.Fatal("plain selection omitted or repeated a candidate")
								}
							}
						}
					}
				})
			}
		}
	}
}
