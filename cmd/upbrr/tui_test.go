// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestTerminalSelectionUsesEffectiveInteractionAndRenderingStream(t *testing.T) {
	terminal := cliTerminalCapabilities{
		Input:  true,
		Output: true,
		Error:  true,
	}
	for _, args := range [][]string{nil, {"--unattended"}, {"--audio-analysis-only"}} {
		opts, _, _, err := parseCLIOptions(args)
		if err != nil || opts.UI != "plain" {
			t.Fatalf("default presentation for %v = %q, %v", args, opts.UI, err)
		}
		if tui, err := selectCLITUI(opts.UI, opts.interactionMode(), terminal); err != nil || tui {
			t.Fatalf("default presentation entered the TUI for %v", args)
		}
	}
	password, _, err := newRootCommand(cliIO{}, nil).Find([]string{"auth", "password"})
	if err != nil {
		t.Fatal(err)
	}
	if ui, err := password.Flags().GetString("ui"); err != nil || ui != "plain" {
		t.Fatalf("password form default presentation = %q, %v", ui, err)
	}
	for _, test := range []struct {
		name, ui string
		mode     api.InteractionMode
		caps     cliTerminalCapabilities
		want     bool
		fail     bool
	}{
		{"auto", "auto", api.InteractionModeInteractive, terminal, true, false},
		{"plain", "plain", api.InteractionModeInteractive, terminal, false, false},
		{"strict", "auto", api.InteractionModeUnattended, terminal, true, false},
		{"strict forced", "tui", api.InteractionModeUnattended, terminal, true, false},
		{"strict no input", "tui", api.InteractionModeUnattended, cliTerminalCapabilities{Output: true}, true, false},
		{"strict redirected", "tui", api.InteractionModeUnattended, cliTerminalCapabilities{}, false, true},
		{"confirm", "auto", api.InteractionModeUnattendedConfirm, terminal, true, false},
		{"piped", "auto", api.InteractionModeInteractive, cliTerminalCapabilities{Output: true}, false, false},
		{"redirected", "tui", api.InteractionModeInteractive, cliTerminalCapabilities{Input: true}, false, true},
		{"dumb", "auto", api.InteractionModeInteractive, cliTerminalCapabilities{
			Input:  true,
			Output: true,
			Term:   "dumb",
		}, false, false},
		{"CI", "auto", api.InteractionModeInteractive, cliTerminalCapabilities{
			Input:  true,
			Output: true,
			CI:     "true",
		}, false, false},
		{"explicit CI", "tui", api.InteractionModeInteractive, cliTerminalCapabilities{
			Input:  true,
			Output: true,
			CI:     "1",
		}, true, false},
		{"CI false", "auto", api.InteractionModeInteractive, cliTerminalCapabilities{
			Input:   true,
			Output:  true,
			CI:      "false",
			NoColor: true,
		}, true, false},
		{"invalid", "bad", api.InteractionModeInteractive, terminal, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := selectCLITUI(test.ui, test.mode, test.caps)
			if got != test.want || (err != nil) != test.fail {
				t.Fatalf("selection = %t %v", got, err)
			}
		})
	}
	opts, _, _, err := parseCLIOptions([]string{"--ua", "--uac", "example.mkv"})
	if err != nil || opts.interactionMode() != api.InteractionModeUnattendedConfirm {
		t.Fatalf("combined mode = %v %v", opts.interactionMode(), err)
	}
}

type cliNoInput struct{ t *testing.T }

func (r cliNoInput) Read([]byte) (int, error) {
	r.t.Error("unattended/static operation read input")
	return 0, io.EOF
}

func TestUIModeAndStrictAuthRejectBeforeSideEffects(t *testing.T) {
	for _, arguments := range [][]string{{"--ui=bad"}, {"--ui=tui"}, {"--ui=plain", "--ua", "--create-auth"}, {"--ua", "--create-auth"}} {
		configPath := filepath.Join(t.TempDir(), "must-not-create", "config.yaml")
		args := append(slices.Clone(arguments), "--config", configPath)
		err := executeCLI(t.Context(), args, cliIO{in: cliNoInput{t}})
		if exit, ok := errors.AsType[*cliExitError](err); !ok || exit.code != 2 {
			t.Fatalf("expected usage error: %v", err)
		}
		if _, err := os.Stat(filepath.Dir(configPath)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("invalid invocation created config/auth state")
		}
	}
	var output strings.Builder
	if err := executeCLI(t.Context(), []string{"--ui=tui", "--version"}, cliIO{in: cliNoInput{t}, out: &output}); err != nil || strings.Contains(output.String(), "\x1b") {
		t.Fatalf("static version = %v", err)
	}
	for _, args := range [][]string{{"serve", "--ui=plain"}, {"api-token", "list", "--ui=plain"}, {"auth", "browse-roots", "--ui=plain", "example"}, {"live-test", "init", "--ui=plain"}} {
		if err := executeCLI(t.Context(), args, cliIO{}); err == nil {
			t.Fatalf("static command accepted ui: %v", args)
		}
	}
}

func TestAudioUIFlagKeepsConfigurationFreeRoute(t *testing.T) {
	opts, visited, paths, err := parseCLIOptions([]string{"--audio-analysis-only", "--audio-output", t.TempDir(), "--ui=plain", "missing.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	err = runAudioAnalysisOnly(t.Context(), opts, visited, paths, cliIO{})
	if err == nil || strings.Contains(err.Error(), "cannot be used") {
		t.Fatalf("audio ui allowlist = %v", err)
	}
	err = executeCLI(t.Context(), []string{"--audio-analysis-only", "--audio-output", t.TempDir(), "--ui=tui", "missing.mkv"}, cliIO{})
	if exit, ok := errors.AsType[*cliExitError](err); !ok || exit.code != 2 {
		t.Fatalf("audio forced selection = %v", err)
	}
}

func TestDraftRetainsLiteralSourcesAndOpaqueOriginalTokens(t *testing.T) {
	args := []string{"--ui=tui", "-tk", "ALPHA,BETA", "-debug=false", "--infohash", "0123456789abcdef0123456789abcdef01234567", "--hc", "English", "--", "original.mkv"}
	draft, opaque, _ := cliDraftArguments(args)
	if strings.Contains(draft, "0123456789") || len(opaque) != 2 {
		t.Fatal("opaque tokens entered editor")
	}
	paths := []string{filepath.Join(t.TempDir(), "Example Film 日本語.mkv")}
	frozen, err := validateCLILaunch(args, cliLaunchRequest{Sources: paths, Arguments: draft})
	if err != nil {
		t.Fatal(err)
	}
	opts, visited, parsed, err := parseCLIOptions(frozen)
	if err != nil || !slices.Equal(parsed, paths) || opts.InfoHash != "0123456789abcdef0123456789abcdef01234567" || !visited["debug"] || opts.Debug {
		t.Fatal("unchanged vector lost parser intent")
	}
	frozen, err = validateCLILaunch(args, cliLaunchRequest{
		Sources:   paths,
		Arguments: draft + " --screens=0",
		Changed:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	opts, _, _, err = parseCLIOptions(frozen)
	if err != nil || opts.Screens != 0 || opts.InfoHash != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatal("edited vector lost protected intent")
	}
	for _, text := range []string{"--ui=plain", "--ua", "--version", "--cleanup", "--audio-analysis-only", "--create-auth", "--export-config config.yaml", "--screens 1 another.mkv", "--title=\"unfinished", "--password=SECRET_SENTINEL"} {
		_, err := validateCLILaunch([]string{"--ui=tui", "original.mkv"}, cliLaunchRequest{
			Sources:   paths,
			Arguments: text,
			Changed:   true,
		})
		if err == nil || strings.Contains(err.Error(), "SECRET_SENTINEL") {
			t.Fatalf("draft accepted unsafe edit or echoed a value: %v", err)
		}
	}
	for _, sources := range [][]string{nil, {""}, {"one", "two"}} {
		if _, err := validateCLILaunch([]string{"--ui=tui", "--queue=q"}, cliLaunchRequest{Sources: sources}); err == nil {
			t.Fatal("invalid queue admitted")
		}
	}
	model := newCLITUIModel(t.Context(), newCLITUIBridge(), func() {}, args, paths, false, true, true)
	for _, size := range [][2]int{{160, 50}, {100, 30}, {40, 12}} {
		model.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		frame := model.View().Content
		if model.focus != cliPanelArguments || strings.Contains(frame, "Input [") || !strings.Contains(frame, "Source 1/1:") {
			t.Fatalf("draft source at %v is not inside the arguments panel", size)
		}
		if lipgloss.Width(frame) > size[0] || lipgloss.Height(frame) > size[1] {
			t.Fatalf("draft layout at %v overflows with combined source and argument editors", size)
		}
	}
	model.Update(modelKey(tea.KeyTab))
	if model.focus != cliPanelArguments || !model.arguments.Focused() {
		t.Fatal("Tab did not reach the arguments editor in the combined panel")
	}
	model.Update(modelKey(tea.KeyTab))
	model.Update(modelKey(tea.KeyTab))
	if model.focus != cliPanelArguments || !model.sources[0].Focused() {
		t.Fatal("Tab did not return to the source editor in the combined panel")
	}
}

func modelKey(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code} }

func TestDraftEditsPreserveMixedQuotesAndEmptyOriginalValues(t *testing.T) {
	for _, title := range []string{`A 'Quoted' "Film"`, ""} {
		for _, attached := range []bool{false, true} {
			args := []string{"--ui=tui", "--infohash", "0123456789abcdef0123456789abcdef01234567"}
			if attached {
				args = append(args, "--title="+title)
			} else {
				args = append(args, "--title", title)
			}
			args = append(args, "--", "original.mkv")
			draft, _, _ := cliDraftArguments(args)
			if strings.Contains(draft, "0123456789") || strings.Contains(draft, "[unchanged quoted value]") {
				t.Fatal("draft exposed protected data or substituted an unparseable placeholder")
			}
			paths := []string{filepath.Join(t.TempDir(), "Example Film 日本語.mkv")}
			validated, err := validateCLILaunch(args, cliLaunchRequest{
				Sources:   paths,
				Arguments: draft + " --screens=0",
				Changed:   true,
			})
			if err != nil {
				t.Fatalf("unrelated edit rejected original quoted/empty value: %v", err)
			}
			opts, _, sources, err := parseCLIOptions(validated)
			if err != nil || opts.Title != title || opts.InfoHash != "0123456789abcdef0123456789abcdef01234567" || opts.Screens != 0 || !slices.Equal(sources, paths) {
				t.Fatal("unrelated edit changed original values or source authority")
			}
		}
	}
}

func TestDraftEditsPreserveProtectedOptionOrderAndAliases(t *testing.T) {
	for _, protectedFirst := range []bool{false, true} {
		for _, names := range [][2]string{{"--qbit-tag", "--qbt"}, {"--qbt", "--qbit-tag"}} {
			quoted := `A 'Quoted' "Tag"`
			first, last := "Normal", quoted
			firstID, lastID := "222", "https://tracker.example/torrents/synthetic.111"
			if protectedFirst {
				first, last = last, first
				firstID, lastID = lastID, firstID
			}
			paths := []string{filepath.Join(t.TempDir(), "Example Film 日本語.mkv")}
			args := []string{"--ui=tui", names[0], first, names[1], last, "--bhd", firstID, "--bhd", lastID, "--", paths[0]}
			priorOpts, priorVisited, priorPaths, err := parseCLIOptions(args)
			if err != nil {
				t.Fatal(err)
			}
			prior, err := buildCLIRequest(priorOpts, priorVisited, priorPaths, 4)
			if err != nil {
				t.Fatal(err)
			}
			draft, _, _ := cliDraftArguments(args)
			validated, err := validateCLILaunch(args, cliLaunchRequest{
				Sources:   paths,
				Arguments: draft + " --screens=0",
				Changed:   true,
			})
			if err != nil {
				t.Fatal(err)
			}
			opts, visited, sources, err := parseCLIOptions(validated)
			if err != nil {
				t.Fatal(err)
			}
			next, err := buildCLIRequest(opts, visited, sources, 0)
			if err != nil {
				t.Fatal(err)
			}
			if opts.QbitTag != priorOpts.QbitTag || !reflect.DeepEqual(next.ClientOverrides, prior.ClientOverrides) ||
				!reflect.DeepEqual(next.TrackerIDOverrides, prior.TrackerIDOverrides) || !slices.Equal(sources, paths) || opts.Screens != 0 {
				t.Fatal("unrelated edit changed effective client or tracker authority")
			}
			for _, edit := range []string{" --qbit-tag=New", " --qbt=New", " --bhd=333"} {
				if _, err := validateCLILaunch(args, cliLaunchRequest{
					Sources:   paths,
					Arguments: draft + edit,
					Changed:   true,
				}); err == nil || !strings.Contains(err.Error(), "protected") {
					t.Fatal("editing a protected canonical option or alias silently changed intent")
				}
			}
		}
	}
}

func TestTUIAnswersRequireFocusedCurrentQuestionAndNeverReplay(t *testing.T) {
	b := newCLITUIBridge()
	m := newCLITUIModel(t.Context(), b, func() {}, nil, nil, false, false, true)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	prompt := cliPrompt{
		ID:         1,
		WorkflowID: "workflow",
		Revision:   2,
		ActionID:   "approve",
		Kind:       cliPromptConfirm,
		Question:   "Approve ALPHA?",
		Evidence:   "Upload name: Example.Film.2024-GRP",
	}
	reply := make(chan cliAnswer, 1)
	m.Update(cliPromptRequest{prompt: prompt, reply: reply})
	m.focus = cliPanelTrackers
	m.Update(modelKey(tea.KeyEnter))
	select {
	case <-reply:
		t.Fatal("inspection approved")
	default:
	}
	m.focus = cliPanelFlow
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 10})
	m.Update(modelKey(tea.KeyEnter))
	select {
	case <-reply:
		t.Fatal("tiny terminal approved")
	default:
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.Update(modelKey(tea.KeyEnter))
	answer := <-reply
	if answer.Confirmed || answer.ActionID != prompt.ActionID || answer.Revision != 2 {
		t.Fatal("default was not explicit No")
	}
	m.Update(modelKey(tea.KeyEnter))
	select {
	case <-reply:
		t.Fatal("answer replayed")
	default:
	}
	m.Update(cliPromptRequest{prompt: prompt, reply: reply})
	m.Update(cliBridgeUpdate{view: &cliView{WorkflowID: "workflow", Revision: 3}})
	m.Update(modelKey(tea.KeyEnter))
	select {
	case answer := <-reply:
		if !answer.Stale || answer.Confirmed {
			t.Fatal("stale question approved")
		}
	default:
		t.Fatal("invalidated question left driver waiting")
	}
}

func TestTUITextKeysRemainLiteralAndSecretsAreReleased(t *testing.T) {
	b := newCLITUIBridge()
	m := newCLITUIModel(t.Context(), b, func() {}, nil, nil, false, false, true)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	reply := make(chan cliAnswer, 1)
	m.Update(cliPromptRequest{prompt: cliPrompt{
		ID:       1,
		Kind:     cliPromptText,
		Question: "Name",
	}, reply: reply})
	for _, r := range "q?yn" {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m.Update(modelKey(tea.KeyEnter))
	if answer := <-reply; answer.Text != "q?yn" {
		t.Fatal("literal text input changed")
	}
	m.Update(cliPromptRequest{prompt: cliPrompt{
		ID:       2,
		Kind:     cliPromptSecret,
		Question: "Password",
		Required: true,
	}, reply: reply})
	m.field.SetValue("SECRET_SENTINEL")
	if strings.Contains(m.View().Content, "SECRET_SENTINEL") || strings.Contains(fmt.Sprintf("%#v", m), "SECRET_SENTINEL") {
		t.Fatal("private field entered render/dump")
	}
	m.Update(modelKey(tea.KeyEnter))
	answer := <-reply
	if answer.Text != "SECRET_SENTINEL" || m.field.Value() != "" || m.question != nil {
		t.Fatal("private answer path or clearing failed")
	}
}

func TestTUIResponsiveViewsFitAndRetainAllPanels(t *testing.T) {
	b := newCLITUIBridge()
	m := newCLITUIModel(t.Context(), b, func() {}, nil, nil, false, false, true)
	m.view = cliView{
		Source:    strings.Repeat("日本語é ", 70),
		Arguments: "--trackers ALPHA,BETA",
		Stage:     "Preparing",
		Summary:   "Upload name: Synthetic.Film.2026-GRP",
		Lanes: []cliLaneView{{ID: "ALPHA", State: "Unchecked"}, {
			ID:     "BETA",
			State:  "Blocked",
			Reason: "auth_required",
		}},
	}
	for _, size := range [][2]int{{160, 50}, {120, 40}, {100, 30}, {80, 24}, {60, 20}, {40, 12}, {20, 8}, {0, 0}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for focus := range cliPanelCount {
			m.focus = focus
			view := m.View()
			if !view.AltScreen {
				t.Fatal("active frame left alternate screen")
			}
			if size[0] >= 40 && size[1] >= 12 {
				if lipgloss.Width(view.Content) > size[0] || lipgloss.Height(view.Content) > size[1] {
					t.Fatalf("layout %v focus %d = %dx%d", size, focus, lipgloss.Width(view.Content), lipgloss.Height(view.Content))
				}
				label := cliPanelNames[focus]
				if focus == cliPanelRelease {
					label = "Upload name:"
				}
				if !strings.Contains(ansi.Strip(view.Content), label) {
					t.Fatalf("panel inaccessible: %d at %v", focus, size)
				}
			}
		}
	}
}

func TestBridgeFloodIsBoundedAndQuestionsCompletionStayReliable(t *testing.T) {
	b := newCLITUIBridge()
	b.Publish(cliView{
		Item:       2,
		WorkflowID: "new",
		Revision:   3,
		Lanes: []cliLaneView{{
			ID:     "ALPHA",
			State:  "Blocked",
			Reason: "auth_required",
		}},
	})
	b.Publish(cliView{
		Item:       1,
		WorkflowID: "old",
		Lanes:      []cliLaneView{{ID: "STALE", State: "valid"}},
	})
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			for range 400 {
				b.AppendLog("INFO", strings.Repeat("a", 5000))
			}
		})
	}
	workers.Wait()
	if len(b.logs) != cliPendingLogLimit || b.dropped != 600 || b.latest.WorkflowID != "new" {
		t.Fatal("mailbox bounds or stale filtering failed")
	}
	for _, line := range b.logs {
		if len(line.Text) > cliLogLineLimit {
			t.Fatal("log bytes unbounded")
		}
	}
	m := newCLITUIModel(t.Context(), b, func() {}, nil, nil, false, false, true)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.Update(b.receive(t.Context())())
	m.focus = cliPanelLogs
	m.Update(modelKey(tea.KeyPgUp))
	offset := m.panels[cliPanelLogs].YOffset()
	m.Update(cliBridgeUpdate{logs: []cliLogLine{{Text: "new line"}}})
	if m.follow || m.panels[cliPanelLogs].YOffset() != offset || m.unseen != 1 {
		t.Fatal("log refresh stole paused position")
	}
	m.Update(modelKey(tea.KeyEnd))
	if !m.follow || m.unseen != 0 {
		t.Fatal("End did not resume following")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := b.Ask(ctx, cliPrompt{ID: 1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Ask = %v", err)
	}
	b.completion <- cliCompletion{}
	if _, ok := b.receive(t.Context())().(cliCompletion); !ok {
		t.Fatal("completion dropped")
	}
}

func TestTerminalTextAndLogStyleExcludeInjectedControls(t *testing.T) {
	text := safeDiagnosticText("INFO inside message password=SECRET_SENTINEL \x1b]52;c;CLIPBOARD\a\x1b[2J日本語")
	if strings.ContainsAny(text, "\x1b\a") || strings.Contains(text, "SECRET_SENTINEL") || strings.Contains(text, "CLIPBOARD") || !strings.Contains(text, "日本語") {
		t.Fatal("terminal text filter failed")
	}
	for _, level := range []string{"TRACE", "DEBUG", "INFO", "WARN", "ERROR"} {
		styled := cliLogLevelStyle(level, false) + ": INFO ordinary message\nERROR continuation"
		if ansi.Strip(styled) != level+": INFO ordinary message\nERROR continuation" || !strings.HasSuffix(styled, ": INFO ordinary message\nERROR continuation") {
			t.Fatal("color escaped the actual level label")
		}
		if strings.Contains(cliLogLevelStyle(level, true), "\x1b") {
			t.Fatal("NO_COLOR styled level")
		}
	}
}

func TestPlainPresenterPreservesEOFAndConfirmationDefaults(t *testing.T) {
	for _, input := range []string{"\n", "yes\n", "n\n", "yes"} {
		var output strings.Builder
		presenter := &cliPlainPresenter{reader: bufio.NewReader(strings.NewReader(input)), output: &output}
		answer, err := presenter.Ask(t.Context(), cliPrompt{
			ID:         3,
			Kind:       cliPromptConfirm,
			Question:   "Confirm? [Y/n]: ",
			DefaultYes: true,
		})
		if err != nil || answer.Confirmed != (input != "n\n") || answer.ID != 3 || output.String() != "Confirm? [Y/n]: " {
			t.Fatal("plain prompt protocol changed")
		}
	}
}

type modelPresenter struct {
	model   *cliTUIModel
	answers []string
	prompts []cliPrompt
}

func (p *modelPresenter) Publish(view cliView) {
	p.model.Update(cliBridgeUpdate{view: new(safeCLIView(view))})
}
func (p *modelPresenter) Ask(ctx context.Context, prompt cliPrompt) (cliAnswer, error) {
	p.prompts = append(p.prompts, prompt)
	reply := make(chan cliAnswer, 1)
	p.model.Update(cliPromptRequest{
		prompt: prompt,
		reply:  reply,
		done:   ctx.Done(),
	})
	text := p.answers[0]
	p.answers = p.answers[1:]
	switch prompt.Kind {
	case cliPromptConfirm:
		if text == "y" {
			p.model.Update(modelKey(tea.KeyRight))
		}
	case cliPromptSelect, cliPromptMultiSelect:
		for value := range strings.SplitSeq(text, ",") {
			if value == "y" {
				value = "yes"
			}
			if value == "n" {
				value = "no"
			}
			for index, option := range prompt.Options {
				if option.Value == value {
					p.model.choices.Select(index)
					if prompt.Kind == cliPromptMultiSelect {
						p.model.Update(modelKey(' '))
					}
					break
				}
			}
		}
	case cliPromptText, cliPromptSecret:
		p.model.field.SetValue(text)
	}
	p.model.Update(modelKey(tea.KeyEnter))
	select {
	case answer := <-reply:
		return answer, nil
	default:
		return cliAnswer{}, errors.New("model did not answer")
	}
}

func TestPlainAndTUIBuildSameExplicitTrackerApproval(t *testing.T) {
	action := api.RequiredAction{
		ID:               "approval",
		WorkflowRevision: 3,
		Kind:             api.RequiredActionApproveTrackers,
		Status:           api.RequiredActionStatusPending,
		Options:          []api.RequiredActionOption{{Value: "ALPHA", Label: "Alpha"}, {Value: "BETA", Label: "Beta"}},
	}
	current := releaseworkflow.CommandResult{
		Workflow:    api.ReleaseWorkflow{ID: "workflow", Revision: 3},
		Projections: &api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{TrackerID: "ALPHA", UploadReleaseName: "Example.Film.2024.ALPHA-GRP"}, {TrackerID: "BETA", UploadReleaseName: "Example.Film.2024.BETA-GRP"}}},
		Dupes:       &api.DupeAssessment{Results: []api.TrackerDupeAssessment{{TrackerID: "ALPHA", Search: api.DupeSearchEvidence{Complete: true}}, {TrackerID: "BETA"}}},
	}
	feedbacks := make([]api.ReleaseWorkflowUploadFeedback, 0, 2)
	current.Projections.Projections[1].EditionFeatures = []api.TrackerEditionFeature{{Label: "Remastered", Selected: true}, {Label: "Extended"}}
	for _, terminal := range []bool{false, true} {
		var presenter cliPresenter
		var output strings.Builder
		if terminal {
			model := newCLITUIModel(t.Context(), newCLITUIBridge(), func() {}, nil, nil, false, false, true)
			model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
			presenter = &modelPresenter{model: model, answers: []string{"y", "n"}}
		} else {
			presenter = &cliPlainPresenter{reader: bufio.NewReader(strings.NewReader("y\nn\n")), output: &output}
		}
		writer := &cliPresentationWriter{
			Writer:    &output,
			presenter: presenter,
			ctx:       t.Context(),
			terminal:  terminal,
		}
		session := &cliWorkflowSession{
			current:        current,
			idempotencyRun: "parity",
			intent:         cliWorkflowIntent{interaction: api.InteractionModeUnattendedConfirm},
			streams:        cliIO{out: writer, presenter: presenter},
		}
		feedback, declined, err := session.collectCompositeUploadFeedback(t.Context(), bufio.NewReader(strings.NewReader("")), config.Config{}, api.NopLogger{}, action)
		if err != nil || declined || !slices.Equal(feedback.Response.TrackerApproval.TrackerIDs, []api.TrackerID{"ALPHA"}) {
			t.Fatalf("approval path failed: %v", err)
		}
		name := current.Projections.Projections[1].UploadReleaseName
		if !strings.Contains(output.String(), fmt.Sprintf("Upload name: %q\n", name)) ||
			!strings.Contains(writer.question.Evidence, "Upload name: "+name+"\n") ||
			strings.Contains(writer.question.Evidence, "Example.Film.2024.ALPHA-GRP") {
			t.Fatal("tracker approval lost console quoting or current tracker evidence")
		}
		if !strings.Contains(output.String(), "BETA edition/features: [Remastered]\n") ||
			!strings.Contains(writer.question.Evidence, "BETA edition/features: [Remastered]\n") ||
			strings.Contains(writer.question.Evidence, "Extended") {
			t.Fatal("tracker approval lost selected edition evidence or included an unselected feature")
		}
		feedbacks = append(feedbacks, feedback)
	}
	if !reflect.DeepEqual(feedbacks[0], feedbacks[1]) {
		t.Fatal("presentation changed typed feedback or idempotency")
	}
}

func TestPresentersPreserveRequiredActionFeedback(t *testing.T) {
	for _, test := range []struct {
		name    string
		kind    api.RequiredActionKind
		answers []string
	}{
		{"playlist", api.RequiredActionSelectPlaylist, []string{"one,two"}},
		{"metadata", api.RequiredActionSelectMetadata, []string{"one"}},
		{"rescan yes", api.RequiredActionConfirmRescan, []string{"y"}},
		{"rescan no", api.RequiredActionConfirmRescan, []string{"n"}},
		{"tracker name", api.RequiredActionProvideTrackerInput, []string{"Example.Film.2024-GRP"}},
		{"questionnaire", api.RequiredActionAnswerQuestionnaire, []string{"drama"}},
		{"rules yes", api.RequiredActionAuthorizeRules, []string{"y"}},
		{"rules no", api.RequiredActionAuthorizeRules, []string{"n"}},
		{"preparation yes", api.RequiredActionResolveTrackerPreparation, []string{"y"}},
		{"preparation no", api.RequiredActionResolveTrackerPreparation, []string{"n"}},
		{"duplicates yes", api.RequiredActionReviewDuplicates, []string{"y"}},
		{"duplicates no", api.RequiredActionReviewDuplicates, []string{"n"}},
		{"reprepare", api.RequiredActionReprepare, []string{"y"}},
		{"reconcile yes", api.RequiredActionReconcileSubmission, []string{"y"}},
		{"reconcile no", api.RequiredActionReconcileSubmission, []string{"n"}},
		{"saved correction gate", api.RequiredActionConfirmCorrections, nil},
		{"legacy auth", legacyTrackerAuthActionKind, nil},
		{"legacy two factor", legacyTrackerTwoFactorActionKind, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			var previous api.ReleaseWorkflowUploadFeedback
			var previousDeclined bool
			var previousError string
			for _, terminal := range []bool{false, true} {
				var output strings.Builder
				var presenter cliPresenter = &cliPlainPresenter{reader: bufio.NewReader(strings.NewReader(strings.Join(test.answers, "\n") + "\n")), output: &output}
				if terminal {
					model := newCLITUIModel(t.Context(), newCLITUIBridge(), func() {}, nil, nil, false, false, true)
					model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
					presenter = &modelPresenter{model: model, answers: slices.Clone(test.answers)}
				}
				action := api.RequiredAction{
					ID:               "action",
					WorkflowRevision: 3,
					Kind:             test.kind,
					TrackerID:        "ALPHA",
					Prompt:           "Synthetic required decision",
					Options:          []api.RequiredActionOption{{Value: "one", Label: "First"}, {Value: "two", Label: "Second"}},
				}
				projection := api.TrackerReleaseProjection{TrackerID: "ALPHA"}
				if test.kind == api.RequiredActionProvideTrackerInput {
					action.AllowsFreeText = true
				}
				if test.kind == api.RequiredActionAnswerQuestionnaire {
					projection.Questionnaire = []api.TrackerQuestionnaireRequirement{{
						Key:      "genre",
						Kind:     "text",
						Label:    "Genre",
						Required: true,
						Help:     "Synthetic genre help",
					}}
				}
				writer := &cliPresentationWriter{
					Writer:    &output,
					presenter: presenter,
					ctx:       t.Context(),
					terminal:  terminal,
				}
				session := &cliWorkflowSession{
					current: releaseworkflow.CommandResult{
						Workflow:    api.ReleaseWorkflow{ID: "workflow", Revision: 3},
						Projections: &api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{projection}},
						Dupes:       &api.DupeAssessment{Results: []api.TrackerDupeAssessment{{TrackerID: "ALPHA", Search: api.DupeSearchEvidence{Complete: true}}}},
					},
					uploadRequest:  api.Request{Trackers: []string{"ALPHA", "BETA"}},
					intent:         cliWorkflowIntent{interaction: api.InteractionModeUnattendedConfirm},
					idempotencyRun: "parity",
					streams:        cliIO{out: writer, presenter: presenter},
				}
				feedback, declined, err := session.collectCompositeUploadFeedback(t.Context(), bufio.NewReader(strings.NewReader("")), config.Config{}, api.NopLogger{}, action)
				errorText := ""
				if err != nil {
					errorText = err.Error()
				}
				if terminal {
					if !reflect.DeepEqual(feedback, previous) || declined != previousDeclined || errorText != previousError {
						t.Fatal("presentation changed typed feedback, decline, or rejection")
					}
				} else {
					previous, previousDeclined, previousError = feedback, declined, errorText
				}
			}
		})
	}
}

func TestQuestionnairePresentationPreservesKindsHelpAndExactAnswers(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		var output strings.Builder
		var presenter cliPresenter = &cliPlainPresenter{reader: bufio.NewReader(strings.NewReader("y\n2,1\nnotes\n")), output: &output}
		var terminalPresenter *modelPresenter
		if terminal {
			model := newCLITUIModel(t.Context(), newCLITUIBridge(), func() {}, nil, nil, false, false, true)
			model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
			terminalPresenter = &modelPresenter{model: model, answers: []string{"y", "blue,red", "notes"}}
			presenter = terminalPresenter
		}
		writer := &cliPresentationWriter{
			Writer:    &output,
			presenter: presenter,
			ctx:       t.Context(),
			terminal:  terminal,
		}
		projections := &api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{TrackerID: "ALPHA", Questionnaire: []api.TrackerQuestionnaireRequirement{
			{
				Key:      "boolean",
				Kind:     "select",
				Options:  []string{"yes", "no"},
				Help:     "Current boolean help",
				Required: true,
			},
			{
				Key:      "multiple",
				Kind:     "multiselect",
				Options:  []string{"red", "blue"},
				Required: true,
			},
			{
				Key:      "notes",
				Kind:     "text",
				Required: true,
			},
			{
				Key:     "default",
				Kind:    "select",
				Options: []string{"yes", "no"},
				Value:   "yes",
			},
		}}}}
		instructions := map[api.TrackerID]api.TrackerProjectionInstructions{"ALPHA": {Questionnaire: map[string]*string{"reset": nil, "empty": new("")}}}
		_, err := collectCLIWorkflowQuestionnaires(bufio.NewReader(strings.NewReader("")), writer, api.InteractionModeUnattendedConfirm, projections, instructions)
		if err != nil {
			t.Fatal(err)
		}
		answers := instructions["ALPHA"].Questionnaire
		if answers["reset"] != nil || answers["empty"] == nil || *answers["empty"] != "" || *answers["boolean"] != "yes" || answers["default"] != nil || *answers["notes"] != "notes" || *answers["multiple"] != "blue,red" {
			t.Fatal("exact answer map or default intent changed")
		}
		if terminal && (terminalPresenter.prompts[0].Help != "Current boolean help" || terminalPresenter.prompts[1].Kind != cliPromptMultiSelect) {
			t.Fatal("current help or questionnaire kinds lost")
		}
	}
}
