// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The pseudoconsole attribute takes an opaque HPCON value, not an address.
//
//go:nocheckptr
func setCLITestConsole(attributes *windows.ProcThreadAttributeListContainer, console windows.Handle) error {
	if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, unsafe.Pointer(uintptr(console)), unsafe.Sizeof(console)); err != nil {
		return fmt.Errorf("attach pseudoconsole: %w", err)
	}
	return nil
}

func TestNativeTerminalChild(t *testing.T) {
	scenario := os.Getenv("UPBRR_TERMINAL_TEST_CHILD")
	if scenario == "" {
		return
	}
	input, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal("child has no attached pseudoconsole input")
	}
	defer input.Close()
	output, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal("child has no attached pseudoconsole output")
	}
	defer output.Close()
	os.Stdin, os.Stdout, os.Stderr = input, output, output
	var before uint32
	if err := windows.GetConsoleMode(windows.Handle(os.Stdin.Fd()), &before); err != nil {
		t.Fatal("child input is not a console")
	}
	var outputBefore uint32
	if err := windows.GetConsoleMode(windows.Handle(os.Stdout.Fd()), &outputBefore); err != nil {
		t.Fatal("child output is not a console")
	}
	caps := detectCLITerminal(cliIO{
		in:     os.Stdin,
		out:    os.Stdout,
		errOut: os.Stderr,
	}, os.Stdout)
	if !caps.Input || !caps.Output {
		t.Fatal("child did not inherit terminal streams")
	}
	caps.NoColor = scenario != "normal"
	ctx := context.WithValue(t.Context(), cliProductionContextKey{}, true)
	err = runCLIPresentation(ctx, cliIO{
		in:        os.Stdin,
		out:       os.Stdout,
		errOut:    os.Stderr,
		dashboard: strings.HasPrefix(scenario, "dashboard"),
		keepOpen:  scenario == "dashboard" || scenario == "keep-open" || scenario == "dashboard-cancel",
	}, caps, scenario != "plain", false, true, nil, nil, false, false,
		func(runCtx context.Context, _, _ []string, streams cliIO) error {
			if isCLITerminal(streams.out) != (scenario == "plain") {
				return errors.New("progress output does not match the selected terminal presentation")
			}
			if scenario == "dashboard" || scenario == "keep-open" {
				streams.presenter.Publish(cliView{
					Result: "Synthetic upload completed.",
					Lanes: []cliLaneView{{
						ID:     "ALPHA",
						State:  "Uploaded",
						URL:    "https://tracker.example/torrents/42",
						Status: cliLaneReady,
					}},
				})
				return nil
			}
			if scenario == "dashboard-cancel" {
				streams.presenter.Publish(cliView{Stage: "Synthetic dashboard running"})
				<-runCtx.Done()
				return fmt.Errorf("synthetic dashboard: %w", runCtx.Err())
			}
			if scenario == "watchdog" {
				streams.presenter.Publish(cliView{Stage: "Synthetic blocked worker"})
				select {} // Deliberately uncooperative; only the child process owns this worker.
			}
			if scenario == "normal" {
				writer, ok := streams.out.(*cliPresentationWriter)
				if !ok {
					return errors.New("missing terminal writer")
				}
				answer, askErr := writer.ask(cliPrompt{
					Kind:     cliPromptSecret,
					Question: "Private value",
					Required: true,
				})
				if askErr != nil || answer.Text != "terminal-private-sentinel" {
					return errors.New("private response mismatch")
				}
			}
			_, askErr := streams.presenter.Ask(runCtx, cliPrompt{
				ID:       2,
				Kind:     cliPromptConfirm,
				Question: "Confirm synthetic decision [y/N]: ",
			})
			if askErr != nil {
				return fmt.Errorf("synthetic decision: %w", askErr)
			}
			return nil
		})
	var after uint32
	if modeErr := windows.GetConsoleMode(windows.Handle(os.Stdin.Fd()), &after); modeErr != nil || before != after {
		t.Fatal("terminal input mode was not restored")
	}
	var outputAfter uint32
	if modeErr := windows.GetConsoleMode(windows.Handle(os.Stdout.Fd()), &outputAfter); modeErr != nil || outputBefore != outputAfter {
		t.Fatal("terminal output mode was not restored")
	}
	completed := scenario == "normal" || scenario == "dashboard" || scenario == "keep-open"
	if completed && err != nil {
		t.Fatal("terminal workflow failed")
	}
	if !completed {
		if exit, ok := errors.AsType[*cliExitError](err); !ok || exit.code != 130 {
			t.Fatalf("interruption did not return 130: %T %v", err, err)
		}
	}
	fmt.Fprintln(os.Stdout, "TERMINAL_RESTORED")
}

func TestWindowsConPTYLifecycle(t *testing.T) {
	for _, scenario := range []string{"normal", "cancel", "plain", "watchdog", "dashboard", "dashboard-cancel", "keep-open"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("UPBRR_TERMINAL_TEST_CHILD", scenario)
			t.Setenv("NO_COLOR", "")
			t.Setenv("TERM", "")
			inputRead, inputWrite, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer inputRead.Close()
			defer inputWrite.Close()
			outputRead, outputWrite, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer outputRead.Close()
			defer outputWrite.Close()
			var console windows.Handle
			if err := windows.CreatePseudoConsole(windows.Coord{X: 120, Y: 40}, windows.Handle(inputRead.Fd()), windows.Handle(outputWrite.Fd()), 0, &console); err != nil {
				t.Fatal(err)
			}
			var consoleClose sync.Once
			closeConsole := func() { consoleClose.Do(func() { windows.ClosePseudoConsole(console) }) }
			defer closeConsole()
			attributes, err := windows.NewProcThreadAttributeList(1)
			if err != nil {
				t.Fatal(err)
			}
			defer attributes.Delete()
			if err := setCLITestConsole(attributes, console); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			command, err := windows.UTF16PtrFromString(windows.ComposeCommandLine([]string{executable, "-test.run=^TestNativeTerminalChild$", "-test.timeout=25s"}))
			if err != nil {
				t.Fatal(err)
			}
			startup := windows.StartupInfoEx{Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{})), ProcThreadAttributeList: attributes.List()}
			var process windows.ProcessInformation
			if err := windows.CreateProcess(nil, command, nil, nil, false, windows.EXTENDED_STARTUPINFO_PRESENT, nil, nil, &startup.StartupInfo, &process); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = windows.CloseHandle(process.Process); _ = windows.CloseHandle(process.Thread) }()
			var output cliSynchronizedOutput
			updates := make(chan struct{}, 1)
			var readers sync.WaitGroup
			readers.Go(func() {
				data := make([]byte, 8192)
				for {
					n, readErr := outputRead.Read(data)
					if n > 0 {
						_, _ = output.Write(data[:n])
						select {
						case updates <- struct{}{}:
						default:
						}
					}
					if readErr != nil {
						return
					}
				}
			})
			defer func() {
				_ = windows.TerminateProcess(process.Process, 1)
				closeConsole()
				_ = outputWrite.Close()
				readers.Wait()
				_ = outputRead.Close()
			}()
			deadline := time.NewTimer(30 * time.Second)
			defer deadline.Stop()
			waitFor := func(text string) {
				for !strings.Contains(output.String(), text) {
					select {
					case <-updates:
					case <-deadline.C:
						if scenario == "watchdog" || scenario == "dashboard-cancel" {
							t.Logf("synthetic status output: %q", output.String())
						}
						t.Fatalf("native terminal did not reach %q (output bytes %d)", text, len(output.String()))
					}
				}
			}
			switch scenario {
			case "dashboard", "keep-open":
				waitFor("Workflow completed.")
				waitFor("https://tracker.example/torrents/42")
				status, err := windows.WaitForSingleObject(process.Process, 0)
				if err != nil || status != uint32(windows.WAIT_TIMEOUT) {
					t.Fatal("keep-open dashboard exited before dismissal")
				}
				if scenario == "keep-open" {
					_, _ = io.WriteString(inputWrite, "q")
				} else {
					_, _ = inputWrite.Write([]byte{3})
				}
			case "normal":
				waitFor("Private value")
				if err := windows.ResizePseudoConsole(console, windows.Coord{X: 80, Y: 24}); err != nil {
					t.Fatal(err)
				}
				_, _ = io.WriteString(inputWrite, "terminal-private-sentinel\r")
				waitFor("Confirm synthetic")
				// Inspect another panel, then return directly to the answer.
				_, _ = io.WriteString(inputWrite, "\t\t\x1b[17~\r")
			default:
				switch scenario {
				case "dashboard-cancel":
					// Incremental redraws can reuse the S already shown in Starting.
					waitFor("dashboard running")
				case "watchdog":
					waitFor("blocked worker")
				default:
					waitFor("Confirm synthetic")
				}
				_, _ = inputWrite.Write([]byte{3})
			}
			waitFor("TERMINAL_RESTORED")
			status, err := windows.WaitForSingleObject(process.Process, 5000)
			if err != nil || status != windows.WAIT_OBJECT_0 {
				t.Fatal("terminal child did not exit")
			}
			var exit uint32
			if err := windows.GetExitCodeProcess(process.Process, &exit); err != nil || exit != 0 {
				t.Fatal("native terminal child failed")
			}
			if strings.Contains(output.String(), "terminal-private-sentinel") {
				t.Fatal("private response reached terminal output")
			}
			if scenario == "normal" && !strings.Contains(output.String(), "\x1b[36") && !strings.Contains(output.String(), "\x1b[38;") {
				t.Fatal("terminal file wrapper lost the native color profile")
			}
		})
	}
}
