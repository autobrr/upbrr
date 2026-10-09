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
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/autobrr/upbrr/internal/logging"
)

const cliShutdownWatchdog = 15 * time.Second

var errCLIShutdownUnconfirmed = errors.New("shutdown could not confirm all work stopped; inspect retained state before retrying")

type cliProductionContextKey struct{}
type cliShutdownStartedContextKey struct{}
type cliPresentationDriver func(context.Context, []string, []string, cliIO) error

func cliStartupFallbackAllowed(parent, invocation context.Context, forced bool) bool {
	return !forced && parent.Err() == nil && !errors.Is(context.Cause(invocation), errCLIUserInterrupt) &&
		!errors.Is(context.Cause(invocation), errCLIShutdownUnconfirmed)
}

// runCLIPresentation owns one serial driver, its presenter, and terminal cleanup.
// TUI dispatch waits for model readiness and optional launch validation; automatic
// fallback is allowed only before driver dispatch. Production signals cancel work
// and bound cleanup, while summaries and artifact reports wait for shell restoration.
func runCLIPresentation(
	ctx context.Context,
	streams cliIO,
	caps cliTerminalCapabilities,
	tui, launch, forced bool,
	args, paths []string,
	debug, audio bool,
	driver cliPresentationDriver,
) error {
	streams = streams.normalized()
	originalStreams := streams
	runCtx, cancelDriver := context.WithCancelCause(ctx)
	defer cancelDriver(nil)
	shutdownStarted := make(chan struct{}, 1)
	runCtx = context.WithValue(runCtx, cliShutdownStartedContextKey{}, shutdownStarted)
	uiCtx, cancelUI := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelUI()
	production, _ := ctx.Value(cliProductionContextKey{}).(bool)
	// Only production owns this adapter. Finite injected readers retain EOF and
	// ownership; no goroutine per read is created or caller stream closed.
	if production && !tui && isCLITerminal(streams.in) {
		file, _ := streams.in.(*os.File)
		input, closeInput, err := newCLIPlainInput(runCtx, file, cancelDriver)
		if err != nil {
			return fmt.Errorf("initialize cancelable terminal input: %w", err)
		}
		defer func() { _ = closeInput() }()
		streams.in = input
	}
	force := make(chan struct{})
	var forceOnce sync.Once
	driverStarted := make(chan struct{})
	driverDone := make(chan struct{})
	var bridge *cliTUIBridge
	var program *tea.Program
	var presenter cliPresenter
	var render io.Writer
	terminalResult := make(chan func(io.Writer) error, 1)
	if tui {
		streams.terminalResult = terminalResult
		bridge = newCLITUIBridge()
		bridge.dashboard = streams.dashboard
		presenter = bridge
		render = streams.out
		if audio {
			render = streams.errOut
		}
		model := newCLITUIModel(uiCtx, bridge, func() {
			if production && errors.Is(context.Cause(runCtx), errCLIUserInterrupt) {
				forceOnce.Do(func() { close(force) })
			}
			cancelDriver(errCLIUserInterrupt)
		}, args, paths, debug, launch, caps.NoColor)
		model.dashboard, model.keepOpen = streams.dashboard, streams.keepOpen
		terminalWriter := &cliTerminalWriter{Writer: render, failure: func() { cancelDriver(errors.New("terminal output failed")); cancelUI() }}
		var terminalOutput io.Writer = terminalWriter
		if file, ok := render.(*os.File); ok {
			terminalOutput = &cliTerminalFileWriter{cliTerminalWriter: terminalWriter, file: file}
		}
		input := streams.in
		if streams.dashboard {
			input = nil
		}
		options := []tea.ProgramOption{
			tea.WithContext(uiCtx),
			tea.WithInput(input),
			tea.WithOutput(terminalOutput),
			tea.WithEnvironment(cliTerminalEnvironment()),
			tea.WithoutSignalHandler(),
			tea.WithFPS(20),
			tea.WithWindowSize(caps.Width, caps.Height),
		}
		if caps.NoColor {
			options = append(options, tea.WithColorProfile(colorprofile.Ascii))
		}
		program = tea.NewProgram(model, options...)
		stopDashboardClose := context.AfterFunc(runCtx, func() {
			select {
			case <-driverDone:
				program.Quit()
			default:
			}
		})
		defer stopDashboardClose()
		logWriter := &cliTUILogWriter{bridge: bridge}
		if !audio {
			streams.out = logWriter
		}
		if caps.Error || audio {
			streams.errOut = logWriter
		}
	} else {
		presenter = &cliPlainPresenter{reader: bufio.NewReader(streams.in), output: streams.out}
	}
	if streams.presenter != nil {
		presenter = streams.presenter
	}
	streams.presenter = presenter
	if !audio {
		streams.out = &cliPresentationWriter{
			Writer:    streams.out,
			presenter: presenter,
			ctx:       runCtx,
			terminal:  tui,
		}
	}
	var restoreConsole func()
	if production {
		restoreConsole = logging.SetDefaultConsoleOutput(streams.out, streams.errOut)
		defer func() {
			if restoreConsole != nil {
				restoreConsole()
			}
		}()
	}
	if bridge != nil {
		bridge.Publish(cliView{
			Source:    strings.Join(paths, "\n"),
			Arguments: safeCLIInvocation(args),
			Stage:     "Starting",
			Debug:     debug,
		})
	}
	finished := make(chan error, 1)
	stopSignals := make(chan struct{})
	var signalWorkers sync.WaitGroup
	if production {
		interrupts := make(chan os.Signal, 2)
		signal.Notify(interrupts, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(interrupts)
		signalWorkers.Go(func() {
			var deadline <-chan time.Time
			var timer *time.Timer
			defer func() {
				if timer != nil {
					timer.Stop()
				}
			}()
			canceled := false
			cancellation := runCtx.Done()
			shutdown := (<-chan struct{})(shutdownStarted)
			var cleanupComplete <-chan struct{}
			for {
				select {
				case <-stopSignals:
					return
				case <-interrupts:
					select {
					case <-driverDone:
						if program != nil && streams.keepOpen {
							program.Quit()
							return
						}
					default:
					}
					if canceled {
						forceOnce.Do(func() { close(force) })
						return
					}
					cancelDriver(errCLIUserInterrupt)
				case <-cancellation:
					canceled = true
					cancellation = nil
					if timer == nil {
						timer = time.NewTimer(cliShutdownWatchdog)
						deadline = timer.C
					}
				case <-shutdown:
					shutdown = nil
					cleanupComplete = driverDone
					if timer == nil {
						timer = time.NewTimer(cliShutdownWatchdog)
						deadline = timer.C
					}
				case <-cleanupComplete:
					cleanupComplete = nil
					if !canceled {
						timer.Stop()
						timer = nil
						deadline = nil
					}
				case <-deadline:
					cancelDriver(errCLIShutdownUnconfirmed)
					forceOnce.Do(func() { close(force) })
					return
				}
			}
		})
	}
	var stopOnce sync.Once
	stopSignalWorkers := func() { stopOnce.Do(func() { close(stopSignals); signalWorkers.Wait() }) }
	defer stopSignalWorkers()
	startDriver := func() {
		go func() {
			var driverErr error
			defer func() {
				finished <- driverErr
				close(driverDone)
				if bridge != nil {
					bridge.completion <- cliCompletion{err: driverErr, canceled: runCtx.Err() != nil}
				}
			}()
			nextArgs, nextPaths := args, paths
			if bridge != nil {
				select {
				case <-bridge.ready:
				case <-runCtx.Done():
					driverErr = runCtx.Err()
					return
				case <-uiCtx.Done():
					driverErr = uiCtx.Err()
					return
				}
				if launch {
					for {
						select {
						case <-runCtx.Done():
							driverErr = runCtx.Err()
							return
						case request := <-bridge.launch:
							request.Sources = resolveCLILaunchSources(paths, request)
							validated, err := validateCLILaunch(args, request)
							request.Reply <- cliLaunchResult{err: err}
							if err != nil {
								continue
							}
							nextArgs, nextPaths = validated, request.Sources
						}
						break
					}
				}
			}
			if driverErr = runCtx.Err(); driverErr != nil {
				return
			}
			if driverErr = uiCtx.Err(); driverErr != nil {
				return
			}
			if bridge != nil {
				bridge.mu.Lock()
				view := bridge.latest
				bridge.mu.Unlock()
				view.Arguments, view.Source = safeCLIInvocation(nextArgs), strings.Join(nextPaths, "\n")
				bridge.Publish(view)
			}
			close(driverStarted)
			driverErr = driver(runCtx, nextArgs, nextPaths, streams)
		}()
	}
	startDriver()
	var result error
	var uiErr error
	if program != nil {
		// One monitor restores the terminal if a production worker cannot join.
		monitorDone := make(chan struct{})
		var monitor sync.WaitGroup
		monitor.Go(func() {
			select {
			case <-force:
				program.Kill()
			case <-monitorDone:
			}
		})
		_, uiErr = program.Run()
		close(monitorDone)
		monitor.Wait()
		if uiErr != nil {
			cancelDriver(errors.New("terminal presentation failed"))
			cancelUI()
			program.Kill()
		}
	}
	select {
	case result = <-finished:
	case <-force:
		result = errCLIShutdownUnconfirmed
	}
	// Windows console Ctrl+C can finish ReadConsole with zero characters before
	// the registered signal handler runs. Preserve real EOF, but let an already
	// delivered interrupt settle before joining the controller and reporting.
	if production && !tui && caps.Input && errors.Is(result, io.EOF) {
		select {
		case <-runCtx.Done():
		case <-time.After(250 * time.Millisecond):
		}
	}
	stopSignalWorkers()
	select {
	case <-force:
		if !errors.Is(result, errCLIShutdownUnconfirmed) {
			result = errors.Join(result, errCLIShutdownUnconfirmed)
		}
	default:
	}
	if program != nil {
		if uiErr != nil && cliStartupFallbackAllowed(ctx, runCtx, forced) {
			select {
			case <-driverStarted:
			default:
				stopSignalWorkers()
				if restoreConsole != nil {
					restoreConsole()
					restoreConsole = nil
				}
				return runCLIPresentation(ctx, originalStreams, caps, false, false, false, args, paths, debug, audio, driver)
			}
		}
		if uiErr != nil && !errors.Is(context.Cause(runCtx), errCLIUserInterrupt) &&
			!errors.Is(result, errCLIShutdownUnconfirmed) {
			result = errors.Join(result, errors.New("terminal presentation failed; use --ui=plain on the next invocation"))
		}
		// All terminal writes resume only after Run has restored the shell.
		if bridge != nil && !audio {
			_, _ = io.WriteString(render, bridge.summary(result))
		}
		select {
		case report := <-terminalResult:
			result = errors.Join(result, report(originalStreams.out))
		default:
		}
	}
	if errors.Is(context.Cause(runCtx), errCLIUserInterrupt) {
		return exitError(130, errCLIUserInterrupt)
	}
	return result
}

// cliTerminalWriter cancels presentation on the first failed or short frame write.
type cliTerminalWriter struct {
	io.Writer
	failure func()
	failed  atomic.Bool
}

func (w *cliTerminalWriter) Write(data []byte) (int, error) {
	n, err := w.Writer.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil && w.failed.CompareAndSwap(false, true) {
		w.failure()
	}
	if err != nil {
		return n, fmt.Errorf("write terminal frame: %w", err)
	}
	return n, nil
}

type cliTerminalFileWriter struct {
	*cliTerminalWriter
	file *os.File
}

func (w *cliTerminalFileWriter) Fd() uintptr { return w.file.Fd() }

func (w *cliTerminalFileWriter) Read(data []byte) (int, error) {
	n, err := w.file.Read(data)
	if err != nil {
		return n, fmt.Errorf("read borrowed terminal file: %w", err)
	}
	return n, nil
}

// Bubble Tea's terminal-file detection requires ReadWriteCloser. This wrapper
// borrows the command's file; closing the presentation never closes that file.
func (*cliTerminalFileWriter) Close() error { return nil }

func safeCLIInvocation(args []string) string {
	text, opaque, _ := cliDraftArguments(args)
	return safeTerminalText(text) + cliProtectedArgumentNotice(opaque)
}

func cliTerminalEnvironment() []string {
	environment := make([]string, 0, len(os.Environ()))
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		if strings.HasPrefix(strings.ToUpper(name), "BUBBLETEA_") {
			continue
		}
		environment = append(environment, value)
	}
	return environment
}
