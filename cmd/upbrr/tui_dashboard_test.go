// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestRootTUIPathsStartWithoutLauncher(t *testing.T) {
	const source = "Synthetic.Film.2026-GRP.mkv"
	for _, test := range []struct {
		name string
		args []string
	}{
		{
			name: "single path before flags",
			args: []string{source, "--ui=tui"},
		},
		{
			name: "multiple paths after separator",
			args: []string{"--ui=tui", "--", source, "Synthetic.Second.Film.2026-GRP.mkv"},
		},
		{
			name: "queue root",
			args: []string{"--ui=tui", "--queue=synthetic", source},
		},
		{
			name: "unattended confirm",
			args: []string{"--ui=tui", "--uac", source},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output cliSynchronizedOutput
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			args := append([]string{"--config", filepath.Join(t.TempDir(), "missing.yaml")}, test.args...)
			err := executeCLI(ctx, args, cliIO{
				in:     strings.NewReader(""),
				out:    &output,
				errOut: io.Discard,
				capabilities: &cliTerminalCapabilities{
					Input:   true,
					Output:  true,
					Width:   240,
					Height:  60,
					NoColor: true,
				},
			})
			if ctx.Err() != nil || !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("supplied path did not reach the config boundary without a Start action: %v", err)
			}
			text := output.String()
			if !strings.Contains(text, "\x1b[?1049h") || !strings.Contains(text, "\x1b[?1049l") {
				t.Fatal("immediate processing did not enter and restore the TUI")
			}
			if strings.Contains(text, "Edit source paths and CLI arguments") || strings.Contains(text, "[ Start ]") {
				t.Fatal("supplied path opened the interactive launcher")
			}
		})
	}
}

func TestUnattendedRootDashboardNeverReadsInputOrLaunches(t *testing.T) {
	var output cliSynchronizedOutput
	err := executeCLI(t.Context(), []string{
		"--ui=tui", "--ua", "--config", filepath.Join(t.TempDir(), "missing.yaml"), "Synthetic.Film.2026-GRP",
	}, cliIO{
		in:  cliNoInput{t},
		out: &output,
		capabilities: &cliTerminalCapabilities{
			Output: true,
			Width:  120,
			Height: 40,
		},
	})
	if err == nil || strings.Contains(err.Error(), "requires usable terminal") || !strings.Contains(output.String(), "\x1b[?1049h") {
		t.Fatalf("dashboard did not reach the existing config boundary: %v", err)
	}
	if strings.Contains(output.String(), "Edit source paths and CLI arguments") {
		t.Fatal("unattended dashboard opened the interactive launcher")
	}
}

func TestRetainedDashboardDrainsFinalPublicationAndKeepsNavigation(t *testing.T) {
	bridge := newCLITUIBridge()
	model := newCLITUIModel(t.Context(), bridge, func() { t.Error("completed dashboard canceled work") }, nil, nil, false, false, true)
	model.keepOpen = true
	model.Update(tea.WindowSizeMsg{Width: 240, Height: 60})
	bridge.Publish(cliView{
		WorkflowID: "finished-workflow",
		Revision:   4,
		Result:     "Item completed.",
		Lanes: []cliLaneView{{
			ID:     "ALPHA",
			State:  "Submission: completed / Client: completed",
			URL:    "https://tracker.example/torrents/42",
			Status: cliLaneReady,
		}},
	})
	bridge.AppendLog("INFO", "Final upload outcome retained")
	model.Update(cliCompletion{})
	frame := ansi.Strip(model.View().Content)
	for _, expected := range []string{"Workflow completed.", "https://tracker.example/torrents/42", "Final upload outcome retained"} {
		if !strings.Contains(frame, expected) {
			t.Fatalf("final dashboard omitted %q", expected)
		}
	}
	model.Update(modelKey(tea.KeyTab))
	if model.focus != cliPanelTrackers {
		t.Fatal("retained interactive dashboard lost navigation")
	}
	_, closeCommand := model.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if closeCommand == nil {
		t.Fatal("q did not close the completed dashboard")
	}
	if _, ok := closeCommand().(tea.QuitMsg); !ok {
		t.Fatal("closing retained dashboard did not quit presentation")
	}

	bridge.dashboard = true
	model.dashboard = true
	focus := model.focus
	model.Update(modelKey(tea.KeyTab))
	model.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if model.focus != focus {
		t.Fatal("unattended dashboard handled keyboard navigation")
	}
	if _, err := bridge.Ask(t.Context(), cliPrompt{Kind: cliPromptConfirm}); err == nil {
		t.Fatal("unattended dashboard accepted a prompt")
	}
}

type cliCompletedDashboardOutput struct {
	cliSynchronizedOutput
	rendered chan struct{}
	once     sync.Once
}

func (output *cliCompletedDashboardOutput) Write(data []byte) (int, error) {
	n, err := output.cliSynchronizedOutput.Write(data)
	if strings.Contains(output.String(), "Workflow completed.") || strings.Contains(output.String(), "Workflow failed.") {
		output.once.Do(func() { close(output.rendered) })
	}
	return n, err
}

func TestKeepOpenRuntimeRetainsSuccessAndFailureWithoutReadingDashboardInput(t *testing.T) {
	failure := errors.New("synthetic workflow failure")
	for _, dashboard := range []bool{false, true} {
		for _, workflowErr := range []error{nil, failure} {
			name := "interactive-success"
			if dashboard {
				name = "unattended-success"
			}
			if workflowErr != nil {
				name = strings.ReplaceAll(name, "success", "failure")
			}
			t.Run(name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				output := &cliCompletedDashboardOutput{rendered: make(chan struct{})}
				var input io.Reader = strings.NewReader("")
				if dashboard {
					input = cliNoInput{t}
				}
				done := make(chan error, 1)
				go func() {
					done <- runCLIPresentation(ctx, cliIO{
						in:        input,
						out:       output,
						dashboard: dashboard,
						keepOpen:  true,
					}, cliTerminalCapabilities{Width: 120, Height: 40}, true, false, true, nil, nil, false, false,
						func(_ context.Context, _, _ []string, streams cliIO) error {
							streams.presenter.Publish(cliView{Result: "Retained terminal outcome."})
							return workflowErr
						})
				}()
				select {
				case <-output.rendered:
				case err := <-done:
					t.Fatalf("dashboard exited before retained completion: %v", err)
				case <-time.After(5 * time.Second):
					t.Fatal("dashboard never rendered completion")
				}
				select {
				case err := <-done:
					t.Fatalf("keep-open exited without dismissal: %v", err)
				default:
				}
				cancel()
				select {
				case err := <-done:
					if !errors.Is(err, workflowErr) {
						t.Fatalf("dashboard dismissal changed workflow result: %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("completed dashboard did not close after parent cancellation")
				}
				if strings.LastIndex(output.String(), "\x1b[?1049l") < 0 {
					t.Fatal("retained dashboard failed to restore the terminal")
				}
			})
		}
	}
}

func TestTrackerUploadURLsRequireConfirmedSubmission(t *testing.T) {
	for _, status := range []api.StageStatus{api.StageStatusCompleted, api.StageStatusFailed, api.StageStatusInterrupted} {
		t.Run(string(status), func(t *testing.T) {
			bridge := newCLITUIBridge()
			session := &cliWorkflowSession{
				streams: cliIO{out: &cliPresentationWriter{Writer: io.Discard, presenter: bridge}, presenter: bridge},
				current: releaseworkflow.CommandResult{
					Workflow: api.ReleaseWorkflow{ID: "uploaded-url"},
					UploadResult: &api.UploadResult{Results: []api.UploadTrackerResult{{
						TrackerID:             "ALPHA",
						SubmissionStatus:      status,
						ClientInjectionStatus: api.StageStatusFailed,
						RemoteURL:             "https://tracker.example/details.php?id=42&passkey=SECRET_SENTINEL",
					}}},
				},
				uploadRequest: api.Request{Trackers: []string{"ALPHA"}},
			}
			session.publishPresentation(t.Context())
			lane := bridge.latest.Lanes[0]
			if status == api.StageStatusCompleted {
				if lane.Status != cliLaneReady || !strings.Contains(lane.URL, "id=42") {
					t.Fatal("confirmed upload URL was lost after client injection failure")
				}
			} else if lane.URL != "" {
				t.Fatal("unconfirmed submission displayed an upload URL")
			}
			model := newCLITUIModel(t.Context(), bridge, func() {}, nil, nil, false, false, true)
			model.Update(tea.WindowSizeMsg{Width: 240, Height: 60})
			model.Update(bridge.drain())
			frame := ansi.Strip(model.View().Content)
			if strings.Contains(frame+bridge.summary(nil), "SECRET_SENTINEL") {
				t.Fatal("upload URL exposed a secret in terminal output")
			}
			if status == api.StageStatusCompleted && !strings.Contains(frame, "ALPHA: https://tracker.example/details.php?id=42") {
				t.Fatal("upload URL was not beside its tracker name")
			}
		})
	}
}
func TestPageURLIdentitySurvivesTerminalProjection(t *testing.T) {
	value := "https://tracker.example/details.php?hash=" + strings.Repeat("a", 40)
	bridge := newCLITUIBridge()
	session := &cliWorkflowSession{
		streams: cliIO{out: &cliPresentationWriter{Writer: io.Discard, presenter: bridge}, presenter: bridge},
		current: releaseworkflow.CommandResult{
			Workflow: api.ReleaseWorkflow{ID: "uploaded-opaque-url"},
			UploadResult: &api.UploadResult{Results: []api.UploadTrackerResult{{
				TrackerID:        "ALPHA",
				SubmissionStatus: api.StageStatusCompleted,
				RemoteURL:        value,
			}}},
		},
		uploadRequest: api.Request{Trackers: []string{"ALPHA"}},
	}
	session.publishPresentation(t.Context())
	model := newCLITUIModel(t.Context(), bridge, func() {}, nil, nil, false, false, true)
	model.Update(tea.WindowSizeMsg{Width: 240, Height: 60})
	model.Update(bridge.drain())
	if frame := ansi.Strip(model.View().Content); !strings.Contains(frame, "ALPHA: "+value) || !strings.Contains(bridge.summary(nil), value) {
		t.Fatal("public page identity did not survive the terminal projection and durable summary")
	}
}

type cliHeldWorkflowShutdown struct {
	entered chan struct{}
	release chan struct{}
	closed  atomic.Bool
}

func (core *cliHeldWorkflowShutdown) ShutdownWorkflowCoordinator(context.Context) error {
	close(core.entered)
	<-core.release
	return nil
}

func (core *cliHeldWorkflowShutdown) Close() error {
	core.closed.Store(true)
	return nil
}

func TestNormalWorkflowCleanupHasProductionWatchdog(t *testing.T) {
	for _, mode := range []string{"interactive", "unattended", "plain"} {
		t.Run(mode, func(t *testing.T) {
			core := &cliHeldWorkflowShutdown{entered: make(chan struct{}), release: make(chan struct{})}
			var output cliSynchronizedOutput
			ctx, cancel := context.WithCancel(context.WithValue(t.Context(), cliProductionContextKey{}, true))
			defer cancel()
			workerDone := make(chan struct{})
			done := make(chan error, 1)
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(core.release) }) }
			t.Cleanup(release)
			go func() {
				done <- runCLIPresentation(ctx, cliIO{
					in:        strings.NewReader(""),
					out:       &output,
					errOut:    io.Discard,
					dashboard: mode == "unattended",
					keepOpen:  mode != "plain",
				}, cliTerminalCapabilities{Width: 120, Height: 40}, mode != "plain", false, true, nil, nil, false, false,
					func(ctx context.Context, _, _ []string, _ cliIO) error {
						defer close(workerDone)
						return closeCLIWorkflowCore(ctx, core)
					})
			}()
			select {
			case <-core.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("normal cleanup did not begin")
			}
			returned := false
			select {
			case err := <-done:
				returned = true
				if !errors.Is(err, errCLIShutdownUnconfirmed) {
					t.Errorf("unconfirmed shutdown lost operational failure: %v", err)
				}
				if err != nil && strings.Contains(err.Error(), "terminal presentation failed") {
					t.Error("cleanup timeout was misreported as terminal failure")
				}
			case <-time.After(cliShutdownWatchdog + 500*time.Millisecond):
				t.Error("normal shutdown exceeded watchdog with live run context")
			}
			if core.closed.Load() {
				t.Error("repository closed before worker termination was confirmed")
			}
			release()
			cancel()
			if !returned {
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("failed to release presentation")
				}
			}
			select {
			case <-workerDone:
			case <-time.After(5 * time.Second):
				t.Fatal("released workflow worker did not join")
			}
			if !core.closed.Load() {
				t.Error("confirmed worker termination did not close repository")
			}
			if mode != "plain" && (!strings.Contains(output.String(), "\x1b[?1049l") ||
				!strings.Contains(output.String(), "Workflow stopped.")) {
				t.Error("terminal restoration omitted the stopped workflow summary")
			}
		})
	}
}

func TestCompletedDashboardDisarmsCleanupWatchdog(t *testing.T) {
	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), cliProductionContextKey{}, true))
	defer cancel()
	output := &cliCompletedDashboardOutput{rendered: make(chan struct{})}
	core := &cliWorkflowCoreLifecycleFake{}
	done := make(chan error, 1)
	go func() {
		done <- runCLIPresentation(ctx, cliIO{
			in:        cliNoInput{t},
			out:       output,
			errOut:    io.Discard,
			dashboard: true,
			keepOpen:  true,
		},
			cliTerminalCapabilities{Width: 120, Height: 40}, true, false, true, nil, nil, false, false,
			func(ctx context.Context, _, _ []string, _ cliIO) error { return closeCLIWorkflowCore(ctx, core) })
	}()
	select {
	case <-output.rendered:
	case err := <-done:
		t.Fatalf("completed dashboard exited without retention: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("dashboard did not complete")
	}
	select {
	case err := <-done:
		t.Fatalf("cleanup watchdog closed completed dashboard: %v", err)
	case <-time.After(cliShutdownWatchdog + 500*time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("dashboard dismissal changed completed result: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("completed dashboard did not close after cancellation")
	}
	if !slices.Contains(core.order, "close") || !strings.Contains(output.String(), "\x1b[?1049l") {
		t.Fatal("completed dashboard did not close repository and restore terminal")
	}
}

func TestKeepOpenRuntimeDistinguishesUnconfirmedCleanup(t *testing.T) {
	workerError := errors.New("synthetic coordinator worker remains unjoined")
	closeError := errors.New("synthetic repository close failed")
	for _, dashboard := range []bool{false, true} {
		for _, scenario := range []struct {
			name                                string
			shutdownErr, closeErr, operationErr error
			wantHold, wantClosed                bool
		}{
			{name: "unjoined-worker", shutdownErr: workerError},
			{name: "unjoined-worker-deadline", shutdownErr: context.DeadlineExceeded},
			{
				name:       "confirmed-close-failure",
				closeErr:   closeError,
				wantHold:   true,
				wantClosed: true,
			},
			{
				name:         "ordinary-operation-deadline",
				operationErr: context.DeadlineExceeded,
				wantHold:     true,
				wantClosed:   true,
			},
		} {
			name := "interactive/" + scenario.name
			if dashboard {
				name = "unattended/" + scenario.name
			}
			t.Run(name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				output := &cliCompletedDashboardOutput{rendered: make(chan struct{})}
				core := &cliWorkflowCoreLifecycleFake{shutdownErr: scenario.shutdownErr, closeErr: scenario.closeErr}
				done := make(chan error, 1)
				go func() {
					done <- runCLIPresentation(ctx, cliIO{
						in:        strings.NewReader(""),
						out:       output,
						dashboard: dashboard,
						keepOpen:  true,
					},
						cliTerminalCapabilities{Width: 120, Height: 40}, true, false, true, nil, nil, false, false,
						func(ctx context.Context, _, _ []string, _ cliIO) error {
							return errors.Join(scenario.operationErr, closeCLIWorkflowCore(ctx, core))
						})
				}()
				var result error
				returned := false
				select {
				case <-output.rendered:
				case result = <-done:
					returned = true
				case <-time.After(5 * time.Second):
					t.Fatal("proof did not reach completion")
				}
				if scenario.wantHold {
					if returned {
						t.Errorf("ordinary failure exited without requested retention: %v", result)
					}
					select {
					case result = <-done:
						returned = true
						t.Errorf("ordinary failure exited without requested retention: %v", result)
					default:
					}
				} else if !returned {
					select {
					case result = <-done:
						returned = true
					case <-time.After(time.Second):
						t.Error("keep-open retained Failed dashboard after cleanup could not confirm worker termination")
					}
				}
				cancel()
				if !returned {
					select {
					case result = <-done:
					case <-time.After(5 * time.Second):
						t.Fatal("proof could not dismiss")
					}
				}
				wantErr := scenario.shutdownErr
				if wantErr == nil {
					wantErr = scenario.closeErr
				}
				if wantErr == nil {
					wantErr = scenario.operationErr
				}
				if !errors.Is(result, wantErr) || errors.Is(result, errCLIShutdownUnconfirmed) != (scenario.shutdownErr != nil) ||
					slices.Contains(core.order, "close") != scenario.wantClosed {
					t.Errorf("result/repository safety mismatch: result=%v closed=%t", result, slices.Contains(core.order, "close"))
				}
				if strings.LastIndex(output.String(), "\x1b[?1049l") < 0 {
					t.Error("terminal was not restored")
				}
			})
		}
	}
}

type cliLateCleanupCore struct {
	entered chan struct{}
	gate    <-chan struct{}
}

func (*cliLateCleanupCore) ShutdownWorkflowCoordinator(context.Context) error { return nil }

func (core *cliLateCleanupCore) Close() error {
	close(core.entered)
	<-core.gate
	return nil
}

type cliLateCleanupOutput struct {
	cliSynchronizedOutput
	release  func()
	returned <-chan struct{}
}

func (output *cliLateCleanupOutput) Write(data []byte) (int, error) {
	if strings.Contains(string(data), "\x1b[?1049l") {
		output.release()
		<-output.returned
	}
	return output.cliSynchronizedOutput.Write(data)
}

func TestWatchdogFailureSurvivesLateCleanupAcknowledgement(t *testing.T) {
	for _, mode := range []string{"dashboard", "plain", "parent-canceled-dashboard"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.WithValue(t.Context(), cliProductionContextKey{}, true))
			defer cancel()
			entered, gate, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(gate) }) }
			t.Cleanup(release)
			output := &cliLateCleanupOutput{release: release, returned: returned}
			done := make(chan error, 1)
			go func() {
				done <- runCLIPresentation(ctx, cliIO{
					in:        strings.NewReader(""),
					out:       output,
					errOut:    io.Discard,
					dashboard: mode != "plain",
					keepOpen:  mode != "plain",
				}, cliTerminalCapabilities{Width: 120, Height: 40}, mode != "plain", false, true, nil, nil, false, false,
					func(ctx context.Context, _, _ []string, _ cliIO) error {
						defer close(returned)
						var closeGate <-chan struct{} = gate
						if mode == "plain" {
							closeGate = ctx.Done()
						}
						return closeCLIWorkflowCore(ctx, &cliLateCleanupCore{entered: entered, gate: closeGate})
					})
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("cleanup did not reach Close")
			}
			if mode == "parent-canceled-dashboard" {
				cancel()
			}
			select {
			case result := <-done:
				if !errors.Is(result, errCLIShutdownUnconfirmed) {
					t.Errorf("late cleanup acknowledgement hid forced shutdown: %v", result)
				}
				if result != nil && strings.Contains(result.Error(), "terminal presentation failed") {
					t.Error("forced shutdown was mislabeled as terminal failure")
				}
			case <-time.After(cliShutdownWatchdog + 5*time.Second):
				release()
				t.Fatal("late cleanup did not restore the presentation")
			}
			select {
			case <-returned:
			case <-time.After(5 * time.Second):
				t.Fatal("late cleanup worker did not join")
			}
			if mode != "plain" && (!strings.Contains(output.String(), "\x1b[?1049l") ||
				!strings.Contains(output.String(), "Workflow stopped.")) {
				t.Error("terminal restoration omitted the stopped workflow summary")
			}
		})
	}
}
