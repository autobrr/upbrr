// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestLauncherEditsReplaceEffectiveSuppressionMode(t *testing.T) {
	for _, flag := range []string{"debug", "site-check", "live-test"} {
		for _, enabled := range []bool{false, true} {
			t.Run(flag+"/"+strconv.FormatBool(enabled), func(t *testing.T) {
				args := []string{"--ui=tui", "--" + flag + "=" + strconv.FormatBool(!enabled),
					"--config=" + filepath.Join(t.TempDir(), strings.Repeat("a", 32), "missing.yaml")}
				opts, visited, paths, err := parseCLIOptions(args)
				if err != nil {
					t.Fatal(err)
				}
				draft, _, _ := cliDraftArguments(args)
				draft = strings.Replace(draft, "--"+flag+"="+strconv.FormatBool(!enabled), "--"+flag+"="+strconv.FormatBool(enabled), 1)
				input, keys := io.Pipe()
				defer input.Close()
				defer keys.Close()
				output := &cliScriptedTerminal{input: keys, keys: "Synthetic.Film.2026-GRP.mkv\t\x01\x0b" + draft + "\t\r"}
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				err = runUpload(ctx, args, opts, visited, paths, cliIO{
					in:     input,
					out:    output,
					errOut: io.Discard,
					capabilities: &cliTerminalCapabilities{
						Input:   true,
						Output:  true,
						Width:   240,
						Height:  60,
						NoColor: true,
					},
				})
				if err == nil || ctx.Err() != nil {
					t.Fatal("accepted launcher did not reach the invalid-config driver boundary")
				}
				text := output.String()
				restored := strings.LastIndex(text, "\x1b[?1049l")
				if restored < 0 || strings.Contains(text[restored:], "submission suppressed") != enabled {
					t.Fatal("durable result retained the original invocation mode after editing")
				}
			})
		}
	}
}

func TestMetadataEditsReplaceModeBeforeRepreparation(t *testing.T) {
	for _, test := range []struct {
		name, initial, edit string
		live, suppressed    bool
	}{
		{
			name:    "debug off",
			initial: "--debug=true",
			edit:    "--debug=false",
		},
		{
			name:       "debug on",
			initial:    "--debug=false",
			edit:       "--debug=true",
			suppressed: true,
		},
		{
			name:       "site check on",
			initial:    "--debug=false",
			edit:       "--site-check=true",
			suppressed: true,
		},
		{
			name:       "live runtime retained",
			initial:    "--live-test=true",
			edit:       "--live-test=false",
			live:       true,
			suppressed: true,
		},
		{
			name:    "normal runtime retained",
			initial: "--live-test=false",
			edit:    "--live-test=true",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			const source = "Synthetic.Film.2026-GRP.mkv"
			args := []string{"--ui=tui", test.initial, source}
			opts, visited, _, err := parseCLIOptions(args)
			if err != nil {
				t.Fatal(err)
			}
			bridge := newCLITUIBridge()
			bridge.Publish(cliView{
				Source:    source,
				Arguments: safeCLIInvocation(args),
				Debug:     opts.Debug || opts.LiveTest,
			})
			current := releaseworkflow.CommandResult{
				Workflow: api.ReleaseWorkflow{ID: "edited-mode-workflow", Revision: 1},
				Release: &api.ReleaseSnapshot{Release: api.PreparedRelease{
					Generation: 1,
					Source:     api.SourceManifest{SourcePath: source},
					Naming:     api.NamingFacts{Title: "Synthetic Film"},
				}},
			}
			core := &cliWorkflowCoreFake{current: current, liveTest: test.live}
			var beforePreparation []cliView
			core.continueFn = func(request api.ContinueReleaseWorkflowRequest) (releaseworkflow.CommandResult, error) {
				if request.Authority == nil && request.Intent.Preparation != nil {
					bridge.mu.Lock()
					beforePreparation = append(beforePreparation, bridge.latest)
					bridge.mu.Unlock()
				}
				return core.current, nil
			}
			core.startUploadFn = func(api.CreateReleaseWorkflowUploadRequest) (releaseworkflow.CommandResult, error) {
				core.current.DryRun = &api.UploadDryRunResult{Reports: []api.TrackerDryRunReport{{TrackerID: "BETA", Status: api.StageStatusCompleted}}}
				if !test.suppressed {
					core.current.UploadResult = &api.UploadResult{Results: []api.UploadTrackerResult{{TrackerID: "BETA", SubmissionStatus: api.StageStatusCompleted}}}
				}
				return core.current, nil
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			streams := cliIO{
				in: strings.NewReader(""),
				out: &cliPresentationWriter{
					Writer:    io.Discard,
					presenter: bridge,
					ctx:       ctx,
					terminal:  true,
				},
				presenter: bridge,
				errOut:    io.Discard,
			}
			finished := make(chan error, 1)
			go func() {
				finished <- runCLIWorkflowInteractive(ctx, core, args, opts, visited, source, api.PlaylistInstruction{}, 0, config.Config{}, streams, api.NopLogger{})
			}()
			for index := range 3 {
				select {
				case request := <-bridge.questions:
					want := cliPromptConfirm
					text := ""
					if index == 1 {
						want, text = cliPromptText, test.edit
					}
					if request.prompt.Kind != want {
						cancel()
						<-finished
						t.Fatal("unexpected metadata correction prompt")
					}
					request.reply <- cliPromptAnswer(request.prompt, text, index == 2)
				case err := <-finished:
					t.Fatalf("metadata editing ended before confirmation: %v", err)
				case <-ctx.Done():
					<-finished
					t.Fatal("metadata editing timed out")
				}
			}
			if err := <-finished; err != nil {
				t.Fatal(err)
			}
			if len(beforePreparation) != 2 || beforePreparation[0].Debug != (opts.Debug || test.live) ||
				beforePreparation[1].Debug != test.suppressed || !strings.Contains(beforePreparation[1].Arguments, test.edit) {
				t.Fatal("accepted mode and arguments were not published before re-preparation")
			}
			if strings.Contains(bridge.summary(nil), "submission suppressed") != test.suppressed || len(core.uploadRequests) != 1 {
				t.Fatal("composite result differs from the effective execution mode")
			}
			want := api.ReleaseWorkflowUploadModeUpload
			if test.suppressed && !test.live {
				want = api.ReleaseWorkflowUploadModeDebug
			}
			if core.uploadRequests[0].Execution.Mode != want || (core.liveStarts > 0) != test.live {
				t.Fatal("editing the presentation changed the actual submission mode")
			}
		})
	}
}
