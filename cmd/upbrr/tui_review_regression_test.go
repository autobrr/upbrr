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
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/audioanalysis"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

type cliScriptedTerminal struct {
	cliSynchronizedOutput
	input *io.PipeWriter
	keys  string
	once  sync.Once
}

func (w *cliScriptedTerminal) Write(data []byte) (int, error) {
	n, err := w.cliSynchronizedOutput.Write(data)
	if strings.Contains(string(data), "[ Start ]") {
		w.once.Do(func() { go func() { defer w.input.Close(); _, _ = io.WriteString(w.input, w.keys) }() })
	}
	return n, err
}

func TestLauncherDispatchPreservesOriginalSourceAuthority(t *testing.T) {
	root := t.TempDir()
	sources := []string{
		filepath.Join(root, strings.Repeat("a", 32), "Synthetic.Film.2026.mkv"),
		filepath.Join(root, strings.Repeat("b", 32), "Synthetic.Film.2026.mkv"),
	}
	if safeTerminalText(sources[0]) != safeTerminalText(sources[1]) {
		t.Fatal("fixture projections must collide")
	}
	for _, removeFirst := range []bool{false, true} {
		t.Run(strconv.FormatBool(removeFirst), func(t *testing.T) {
			input, keys := io.Pipe()
			defer input.Close()
			defer keys.Close()
			script := "\t\t\t\r"
			want := sources
			if removeFirst {
				script, want = "\x04\t\t\r", sources[1:]
			}
			output := &cliScriptedTerminal{input: keys, keys: script}
			var dispatched []string
			err := runCLIPresentation(t.Context(), cliIO{
				in:     input,
				out:    output,
				errOut: io.Discard,
			},
				cliTerminalCapabilities{Width: 120, Height: 40}, true, true, true, []string{"--ui=tui"}, sources, false, false,
				func(_ context.Context, args, paths []string, _ cliIO) error {
					dispatched = slices.Clone(paths)
					_, _, parsed, err := parseCLIOptions(args)
					if err != nil || !slices.Equal(parsed, paths) {
						return errors.New("source dispatch differs from parsed authority")
					}
					return nil
				})
			if err != nil || !slices.Equal(dispatched, want) {
				t.Fatal("launcher changed unchanged source authority")
			}
		})
	}
	reordered := resolveCLILaunchSources(sources, cliLaunchRequest{
		Sources: []string{safeTerminalText(sources[1]), safeTerminalText(sources[0])}, SourceIndexes: []int{1, 0},
	})
	if !slices.Equal(reordered, []string{sources[1], sources[0]}) {
		t.Fatal("source identities depend on redacted text equality")
	}
	changed := resolveCLILaunchSources(sources, cliLaunchRequest{Sources: []string{"edited.mkv"}, SourceIndexes: []int{0}})
	if changed[0] != "edited.mkv" {
		t.Fatal("edited source lost its new authority")
	}
}

func TestAudioTUIUsesRealTrackProgressAndRestoresBeforeArtifactOutput(t *testing.T) {
	ffmpeg, err := screenshots.ResolveFFmpegExecutable()
	if err != nil {
		t.Skipf("FFmpeg unavailable: %v", err)
	}
	input := filepath.Join(t.TempDir(), "Synthetic.Audio.2026.wav")
	command := exec.CommandContext(t.Context(), ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i",
		"sine=frequency=440:duration=0.1", "-c:a", "pcm_s16le", input)
	if _, err := command.CombinedOutput(); err != nil {
		t.Fatal("generate synthetic audio")
	}
	var terminal cliSynchronizedOutput
	var progressed bool
	opts := cliOptions{
		AudioAnalysisOnly: true,
		AudioOutput:       t.TempDir(),
		AudioImages:       "waveform",
		AudioTracks:       "primary",
	}
	err = runCLIPresentation(t.Context(), cliIO{
		in:     strings.NewReader(""),
		out:    &terminal,
		errOut: &terminal,
	},
		cliTerminalCapabilities{Width: 120, Height: 40}, true, false, true, nil, []string{input}, false, true,
		func(ctx context.Context, _, paths []string, streams cliIO) error {
			err := runAudioAnalysisOnly(ctx, opts, map[string]bool{"ui": true}, paths, streams)
			bridge, ok := streams.presenter.(*cliTUIBridge)
			if !ok {
				return errors.New("missing audio presenter")
			}
			bridge.mu.Lock()
			for _, row := range bridge.telemetry {
				progressed = progressed || row.ItemOnly && row.Lane == "audio-1" && row.Completed > 0 && row.Total == 100
			}
			bridge.mu.Unlock()
			return err
		})
	text := terminal.String()
	exit, artifact := strings.LastIndex(text, "\x1b[?1049l"), strings.Index(text, "Audio track 1 waveform:")
	if err != nil || !progressed || exit < 0 || artifact <= exit || strings.Count(text, "Audio track 1 waveform:") != 1 {
		t.Fatal("real audio progress or durable artifact output failed")
	}
}

func TestAuthTUIDurableBackupOnSuccessAndPartialFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(strconv.FormatBool(fail), func(t *testing.T) {
			dbPath, configPath := writeAuthCLIConfig(t)
			if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "very-secure-password"); err != nil {
				t.Fatal("bootstrap synthetic auth")
			}
			if fail {
				blocker := filepath.Join(filepath.Dir(dbPath), "web-sessions.json")
				if err := os.Mkdir(blocker, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(blocker, "blocker"), []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			model := newCLITUIModel(t.Context(), newCLITUIBridge(), func() {}, nil, nil, false, false, true)
			model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
			presenter := &modelPresenter{model: model, answers: []string{"very-secure-password", "replacement-secure-password", "replacement-secure-password"}}
			var terminal cliSynchronizedOutput
			err := executeCLI(t.Context(), []string{"auth", "password", "--config", configPath, "--ui=tui"}, cliIO{
				in:        strings.NewReader(""),
				out:       &terminal,
				errOut:    io.Discard,
				presenter: presenter,
				capabilities: &cliTerminalCapabilities{
					Input:  true,
					Output: true,
					Width:  120,
					Height: 40,
				},
			})
			backups, globErr := filepath.Glob(filepath.Join(filepath.Dir(dbPath), authmaterial.WebAuthFileName+".backup-*"))
			if (err != nil) != fail || globErr != nil || len(backups) != 1 {
				t.Fatal("auth operation did not produce expected backup outcome")
			}
			text := terminal.String()
			exit, backup := strings.LastIndex(text, "\x1b[?1049l"), strings.Index(text, "Web auth backup created: "+backups[0])
			if exit < 0 || backup <= exit || strings.Count(text, "Web auth backup created:") != 1 || strings.Contains(text, "secure-password") {
				t.Fatal("backup path not durable or private auth field leaked")
			}
		})
	}
}

func TestAudioPartialFailureRetainsArtifactsAfterTerminalRestoration(t *testing.T) {
	var terminal cliSynchronizedOutput
	failure := errors.New("synthetic partial audio failure")
	artifact := filepath.Join(t.TempDir(), "stats.txt")
	err := runCLIPresentation(t.Context(), cliIO{
		in:     strings.NewReader(""),
		out:    &terminal,
		errOut: &terminal,
	},
		cliTerminalCapabilities{Width: 120, Height: 40}, true, false, true, nil, nil, false, true,
		func(_ context.Context, _, _ []string, streams cliIO) error {
			results := []audioanalysis.TrackResult{{Public: api.AudioAnalysisTrackResult{Ordinal: 1, Status: api.StageStatusFailed},
				Artifacts: []audioanalysis.Artifact{{Path: artifact, Public: api.AudioAnalysisArtifact{Variant: api.AudioAnalysisStats}}}}}
			streams.terminalResult <- func(output io.Writer) error { return writeAudioAnalysisArtifacts(results, output) }
			return failure
		})
	text := terminal.String()
	exit, result := strings.LastIndex(text, "\x1b[?1049l"), strings.Index(text, "Audio track 1 stats: "+artifact)
	if !errors.Is(err, failure) || exit < 0 || result <= exit || strings.Count(text, "Audio track 1 stats:") != 1 {
		t.Fatal("partial failure lost its error or completed artifact output")
	}
}

func TestInitialSourceProjectionNeverRetainsUnsafeTerminalText(t *testing.T) {
	source := "Synthetic.\x1b]0;PRIVATE_SOURCE\a." + strings.Repeat("a", 32) + ".mkv"
	model := newCLITUIModel(t.Context(), newCLITUIBridge(), func() {}, nil, []string{source}, false, false, true)
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if model.view.Source != safeTerminalText(source) || strings.Contains(model.View().Content, "PRIVATE_SOURCE") || strings.Contains(model.view.Source, strings.Repeat("a", 32)) {
		t.Fatal("initial source bypassed safe display projection")
	}
}

func TestRawPasswordInputDistinguishesInterruptFromEOF(t *testing.T) {
	for _, input := range []string{"\x03", "\x04", ""} {
		ctx, cancel := context.WithCancelCause(t.Context())
		terminal := term.NewTerminal(cliPasswordIO{
			Reader: strings.NewReader(input),
			Writer: io.Discard,
			cancel: cancel,
		}, "")
		_, err := terminal.ReadPassword("")
		if input == "\x03" {
			if !errors.Is(err, errCLIUserInterrupt) || !errors.Is(context.Cause(ctx), errCLIUserInterrupt) {
				t.Fatal("raw password Ctrl+C was treated as EOF")
			}
		} else if !errors.Is(err, io.EOF) || ctx.Err() != nil {
			t.Fatal("ordinary password EOF became an interrupt")
		}
		cancel(nil)
	}
}

func TestQueueTerminalResultsBoundRecordsBytesAndRetainCounts(t *testing.T) {
	bridge := newCLITUIBridge()
	const items = cliQueueResultLimit + 3
	for item := range uint64(items) {
		bridge.Publish(cliView{
			Item:       item + 1,
			ItemsTotal: items,
			Result:     "Item failed.",
			ItemFailed: true,
			Lanes:      []cliLaneView{{ID: "ALPHA", State: strings.Repeat("x", cliQueueResultBytes+64)}},
		})
	}
	if len(bridge.results) != cliQueueResultLimit || bridge.resultsDropped != items-1-cliQueueResultLimit {
		t.Fatal("queue record retention exceeds bound")
	}
	for _, result := range bridge.results {
		if len(result) > cliQueueResultBytes || !strings.Contains(result, "Item failed.") {
			t.Fatal("queue byte bound or reliable item outcome lost")
		}
	}
	summary := bridge.summary(errors.New("synthetic failure"))
	if !strings.Contains(summary, fmt.Sprintf("%d/%d items finished; %d failed", items, items, items)) || !strings.Contains(summary, "earlier item results omitted") {
		t.Fatal("queue aggregate/omission evidence absent")
	}
}

func TestLocalDeclinesPublishTruthfulTerminalOutcomes(t *testing.T) {
	for _, kind := range []api.RequiredActionKind{api.RequiredActionApproveTrackers, api.RequiredActionAuthorizeRules,
		api.RequiredActionConfirmRescan, api.RequiredActionReprepare, api.RequiredActionReconcileSubmission} {
		t.Run(string(kind), func(t *testing.T) {
			bridge := newCLITUIBridge()
			model := newCLITUIModel(t.Context(), bridge, func() {}, nil, nil, false, false, true)
			model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var answer sync.WaitGroup
			answer.Go(func() {
				select {
				case question := <-bridge.questions:
					model.Update(question)
					model.Update(modelKey(tea.KeyEnter))
				case <-ctx.Done():
				}
			})
			writer := &cliPresentationWriter{
				Writer:    io.Discard,
				presenter: bridge,
				ctx:       ctx,
				terminal:  true,
			}
			action := api.RequiredAction{
				ID:               "decline",
				Kind:             kind,
				WorkflowRevision: 1,
				Status:           api.RequiredActionStatusPending,
				Options:          []api.RequiredActionOption{{Value: "ALPHA"}},
			}
			if kind == api.RequiredActionAuthorizeRules {
				action.TrackerID = "ALPHA"
			}
			current := releaseworkflow.CommandResult{
				Workflow:     api.ReleaseWorkflow{ID: "workflow", Revision: 1},
				Continuation: api.WorkflowContinuation{RequiredActions: []api.RequiredAction{action}},
				Projections: &api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{
					{TrackerID: "ALPHA"},
					{
						TrackerID: "BETA",
						Readiness: api.ReadinessStatusIneligible,
						Failures:  []api.WorkflowFailure{{Failure: api.OperationFailure{Code: "authentication_required"}}},
					},
				}},
				Dupes: &api.DupeAssessment{Results: []api.TrackerDupeAssessment{{TrackerID: "ALPHA"}}},
			}
			coreSvc := &cliWorkflowCoreFake{startUploadFn: func(api.CreateReleaseWorkflowUploadRequest) (releaseworkflow.CommandResult, error) {
				return current, nil
			}}
			source := filepath.Join(t.TempDir(), "Synthetic.Film.2026.mkv")
			session := &cliWorkflowSession{
				streams:        cliIO{out: writer, presenter: bridge},
				core:           coreSvc,
				idempotencyRun: "decline",
				intent:         cliWorkflowIntent{sourcePath: source, interaction: api.InteractionModeUnattendedConfirm},
				uploadRequest: api.Request{
					SourcePath: source,
					Trackers:   []string{"ALPHA", "BETA"},
					Options:    api.UploadOptions{Screens: 0, InteractionMode: api.InteractionModeUnattendedConfirm},
				},
			}
			_, err := session.completeComposite(ctx, false, bufio.NewReader(strings.NewReader("")), config.Config{}, api.NopLogger{})
			cancel()
			answer.Wait()
			if err != nil || len(coreSvc.uploadFeedback) != 0 {
				t.Fatal("declining a required action submitted feedback or failed")
			}
			publishCLIItemResult(session.streams, nil)
			summary := bridge.summary(nil)
			if strings.Contains(summary, "Awaiting decision") || strings.Contains(summary, "Item completed.") {
				t.Fatal("decline was relabeled completed or left awaiting decision")
			}
			if kind == api.RequiredActionReconcileSubmission && !strings.Contains(summary, "External outcome remains unresolved") {
				t.Fatal("declined reconciliation lost remote uncertainty")
			}
			if !strings.Contains(summary, "BETA: ineligible (authentication_required)") {
				t.Fatal("global decline overwrote the blocked sibling outcome")
			}
		})
	}
}

func TestAllNoApprovalPublishesDeclinedLanesWithoutFeedback(t *testing.T) {
	bridge := newCLITUIBridge()
	model := newCLITUIModel(t.Context(), bridge, func() {}, nil, nil, false, false, true)
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var answers sync.WaitGroup
	answers.Go(func() {
		for range 2 {
			select {
			case question := <-bridge.questions:
				model.Update(question)
				model.Update(modelKey(tea.KeyEnter))
			case <-ctx.Done():
				return
			}
		}
	})
	writer := &cliPresentationWriter{
		Writer:    io.Discard,
		presenter: bridge,
		ctx:       ctx,
		terminal:  true,
	}
	session := &cliWorkflowSession{
		streams:        cliIO{out: writer, presenter: bridge},
		idempotencyRun: "decline",
		current: releaseworkflow.CommandResult{
			Workflow:    api.ReleaseWorkflow{ID: "workflow", Revision: 1},
			Projections: &api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{TrackerID: "ALPHA"}, {TrackerID: "BETA"}}},
			Dupes:       &api.DupeAssessment{Results: []api.TrackerDupeAssessment{{TrackerID: "ALPHA"}, {TrackerID: "BETA"}}},
		},
	}
	action := api.RequiredAction{
		ID:               "approval",
		Kind:             api.RequiredActionApproveTrackers,
		WorkflowRevision: 1,
		Options:          []api.RequiredActionOption{{Value: "ALPHA"}, {Value: "BETA"}},
	}
	feedback, declined, err := session.collectCompositeUploadFeedback(ctx, bufio.NewReader(strings.NewReader("")), config.Config{}, api.NopLogger{}, action)
	cancel()
	answers.Wait()
	if err != nil || !declined || feedback.Response.TrackerApproval != nil {
		t.Fatal("all-No review produced upload approval")
	}
	for _, lane := range bridge.latest.Lanes {
		if lane.State != "Declined" {
			t.Fatal("No did not publish the candidate's declined state")
		}
	}
}
