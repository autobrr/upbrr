// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"

	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/pkg/api"
)

type cliPromptKind uint8

const (
	cliPromptText cliPromptKind = iota
	cliPromptConfirm
	cliPromptSelect
	cliPromptMultiSelect
	cliPromptSecret
)

// cliPrompt carries detached evidence and the identities needed to correlate an
// answer. The workflow driver retains authority and validates returned identities.
type cliPrompt struct {
	ID         uint64
	WorkflowID api.WorkflowID
	Revision   api.WorkflowRevision
	ActionID   api.RequiredActionID
	Kind       cliPromptKind
	Question   string
	Evidence   string
	Help       string
	DefaultYes bool
	Required   bool
	Options    []cliPromptOption
}

// cliPromptOption separates a display label from the exact value returned to
// the existing workflow answer parser.
type cliPromptOption struct{ Label, Value string }

type cliAnswer struct {
	ID         uint64
	WorkflowID api.WorkflowID
	Revision   api.WorkflowRevision
	ActionID   api.RequiredActionID
	Text       string
	Confirmed  bool
	Stale      bool
}

type cliLaneStatus uint8

const (
	cliLanePending cliLaneStatus = iota
	cliLaneReady
	cliLaneBlocked
)

type cliLaneView struct {
	ID, State, Reason, Detail, URL string
	Status                         cliLaneStatus
}

// Raw password input disables OS signal generation, so own Ctrl+C explicitly.
type cliPasswordIO struct {
	io.Reader
	io.Writer
	cancel context.CancelCauseFunc
}

func (input cliPasswordIO) Read(data []byte) (int, error) {
	n, err := input.Reader.Read(data)
	if slices.Contains(data[:n], byte(3)) {
		input.cancel(errCLIUserInterrupt)
		return 0, errCLIUserInterrupt
	}
	if err != nil {
		return n, fmt.Errorf("read masked terminal input: %w", err)
	}
	return n, nil
}

// cliView is a detached workflow projection. Item identifies the queue entry and
// Epoch identifies its progress-reporting lifetime; neither grants upload authority.
type cliView struct {
	Item                              uint64
	Epoch                             uint64
	WorkflowID                        api.WorkflowID
	Revision                          api.WorkflowRevision
	OperationID                       api.WorkflowOperationID
	Source, Arguments, Summary, Stage string
	Result                            string
	Lanes                             []cliLaneView
	Completed, Total                  int
	ItemsTotal                        int
	Debug                             bool
	ItemFailed                        bool
}

type cliPresenter interface {
	Publish(cliView)
	Ask(context.Context, cliPrompt) (cliAnswer, error)
}

// cliPresentationWriter keeps legacy formatting as a compatibility sink while
// typed snapshots and questions supply the panels. It never parses stdout.
// Question binding and answering belong to the serial workflow driver.
type cliPresentationWriter struct {
	io.Writer
	presenter  cliPresenter
	ctx        context.Context
	terminal   bool
	nextPrompt uint64
	question   cliPrompt
}

func (w *cliPresentationWriter) WriteConsoleLog(label, message string) {
	if console, ok := w.Writer.(interface{ WriteConsoleLog(string, string) }); ok {
		console.WriteConsoleLog(label, message)
		return
	}
	fmt.Fprintf(w.Writer, "%s %s: %s\n", time.Now().Format("2006/01/02 15:04:05"), label, safeDiagnosticText(message))
}

type cliStyledConsoleWriter struct {
	io.Writer
	noColor bool
}

func (w *cliStyledConsoleWriter) WriteConsoleLog(label, message string) {
	fmt.Fprintf(w.Writer, "%s %s: %s\n", time.Now().Format("2006/01/02 15:04:05"), cliLogLevelStyle(label, w.noColor), safeDiagnosticText(message))
}

// ask binds a unique question to the current workflow action and rejects stale
// or mismatched replies before the caller constructs typed workflow feedback.
func (w *cliPresentationWriter) ask(prompt cliPrompt) (cliAnswer, error) {
	w.nextPrompt++
	prompt.ID = w.nextPrompt
	prompt.WorkflowID, prompt.Revision, prompt.ActionID = w.question.WorkflowID, w.question.Revision, w.question.ActionID
	if prompt.Evidence == "" {
		prompt.Evidence = w.question.Evidence
	}
	answer, err := w.presenter.Ask(w.ctx, prompt)
	if err != nil {
		return cliAnswer{}, fmt.Errorf("answer terminal question: %w", err)
	}
	if answer.Stale {
		return cliAnswer{}, errors.New("upbrr: question authority changed; start a fresh review")
	}
	if answer.ID != prompt.ID || answer.WorkflowID != prompt.WorkflowID || answer.Revision != prompt.Revision || answer.ActionID != prompt.ActionID {
		return cliAnswer{}, errors.New("upbrr: question authority changed; start a fresh review")
	}
	return answer, nil
}

func bindCLIQuestion(
	ctx context.Context,
	output io.Writer,
	workflow api.WorkflowID,
	revision api.WorkflowRevision,
	action api.RequiredActionID,
	evidence string,
) {
	if writer, ok := output.(*cliPresentationWriter); ok {
		writer.ctx = ctx
		writer.question = cliPrompt{
			WorkflowID: workflow,
			Revision:   revision,
			ActionID:   action,
			Evidence:   safeTerminalText(evidence),
		}
	}
}

func askCLIField(reader *bufio.Reader, output io.Writer, prompt cliPrompt) (string, error) {
	if writer, ok := output.(*cliPresentationWriter); ok {
		answer, err := writer.ask(prompt)
		return answer.Text, err
	}
	return promptLine(reader, output, prompt.Question)
}

type cliPlainPresenter struct {
	reader *bufio.Reader
	output io.Writer
}

func (*cliPlainPresenter) Publish(cliView) {}

func (p *cliPlainPresenter) Ask(ctx context.Context, prompt cliPrompt) (cliAnswer, error) {
	answer := cliPromptAnswer(prompt, "", false)
	if err := ctx.Err(); err != nil {
		return answer, fmt.Errorf("answer plain question: %w", err)
	}
	// Original line protocol and EOF behavior remain owned by promptLine.
	line, err := promptLine(p.reader, p.output, prompt.Question)
	if err != nil {
		return answer, err
	}
	answer.Text = line
	if prompt.Kind == cliPromptConfirm {
		trimmed := strings.ToLower(strings.TrimSpace(line))
		answer.Confirmed = trimmed == "y" || trimmed == "yes" || (trimmed == "" && prompt.DefaultYes)
	}
	return answer, nil
}

// Strip control sequences before redaction so an escape cannot split a secret
// key. Apply redaction again after filtering; only renderer-owned styles follow.
func safeTerminalText(value string) string {
	value = redaction.RedactValue(value, nil)
	return redaction.RedactValue(stripTerminalControls(value), nil)
}

func stripTerminalControls(value string) string {
	value = ansi.Strip(value)
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) || r == '\u202a' || r == '\u202b' || r == '\u202d' || r == '\u202e' || r == '\u2066' || r == '\u2067' || r == '\u2068' ||
			r == '\u2069' {
			return -1
		}
		return r
	}, value)
	return value
}

func safeDiagnosticText(value string) string {
	return safeTerminalText(logging.SanitizeMessage(safeTerminalText(value)))
}

func safeCLIView(view cliView) cliView {
	view.Source = safeTerminalText(view.Source)
	view.Arguments = safeTerminalText(view.Arguments)
	view.Summary = safeTerminalText(view.Summary)
	view.Stage = safeDiagnosticText(view.Stage)
	view.Result = safeDiagnosticText(view.Result)
	view.Lanes = slices.Clone(view.Lanes)
	for i := range view.Lanes {
		lane := &view.Lanes[i]
		lane.ID, lane.State = safeTerminalText(lane.ID), safeTerminalText(lane.State)
		lane.Reason, lane.Detail = safeDiagnosticText(lane.Reason), safeDiagnosticText(lane.Detail)
		lane.URL = stripTerminalControls(redaction.TrackerPageURL(stripTerminalControls(lane.URL)))
	}
	return view
}

// safeCLIPrompt sanitizes visible text and detaches options while preserving
// their values for exact answer mapping.
func safeCLIPrompt(prompt cliPrompt) cliPrompt {
	prompt.Question, prompt.Evidence, prompt.Help = safeTerminalText(prompt.Question), safeTerminalText(prompt.Evidence), safeTerminalText(prompt.Help)
	prompt.Options = slices.Clone(prompt.Options)
	for i := range prompt.Options {
		prompt.Options[i].Label = safeTerminalText(prompt.Options[i].Label)
	}
	return prompt
}

func validateCLIUI(value string) error {
	switch value {
	case "", "auto", "plain", "tui":
		return nil
	}
	return errors.New("--ui must be auto, plain or tui")
}

func cliPromptAnswer(prompt cliPrompt, text string, confirmed bool) cliAnswer {
	return cliAnswer{
		ID:         prompt.ID,
		WorkflowID: prompt.WorkflowID,
		Revision:   prompt.Revision,
		ActionID:   prompt.ActionID,
		Text:       text,
		Confirmed:  confirmed,
	}
}

func cliLaneSummary(lanes []cliLaneView) string {
	var output strings.Builder
	for _, lane := range lanes {
		fmt.Fprintf(&output, "%s: %s", lane.ID, lane.State)
		if lane.URL != "" {
			fmt.Fprintf(&output, " %s", lane.URL)
		}
		if lane.Reason != "" {
			fmt.Fprintf(&output, " (%s)", lane.Reason)
		}
		output.WriteByte('\n')
		if lane.Detail != "" {
			fmt.Fprintf(&output, "  %s\n", lane.Detail)
		}
	}
	return output.String()
}
