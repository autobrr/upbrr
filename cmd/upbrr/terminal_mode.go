// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/autobrr/upbrr/pkg/api"
)

type cliTerminalCapabilities struct {
	Input, Output, Error bool
	Term, CI             string
	NoColor              bool
	Width, Height        int
}

func isCLITerminal(stream any) bool {
	if writer, ok := stream.(*cliPresentationWriter); ok && !writer.terminal {
		stream = writer.Writer
	}
	file, ok := stream.(*os.File)
	if !ok {
		return false
	}
	fd, valid := terminalFileDescriptor(file)
	return valid && term.IsTerminal(fd)
}

func detectCLITerminal(streams cliIO, render io.Writer) cliTerminalCapabilities {
	if streams.capabilities != nil {
		return *streams.capabilities
	}
	caps := cliTerminalCapabilities{
		Input:   isCLITerminal(streams.in),
		Output:  isCLITerminal(render),
		Error:   isCLITerminal(streams.errOut),
		Term:    os.Getenv("TERM"),
		CI:      os.Getenv("CI"),
		NoColor: os.Getenv("NO_COLOR") != "",
	}
	if file, ok := render.(*os.File); ok {
		if fd, valid := terminalFileDescriptor(file); valid {
			caps.Width, caps.Height, _ = term.GetSize(fd)
		}
	}
	return caps
}

// selectCLITUI evaluates the effective interaction mode and the rendering stream.
// Strict unattended mode needs only terminal output; forced TUI fails when
// ineligible, while auto falls back to plain output and respects CI detection.
func selectCLITUI(ui string, mode api.InteractionMode, caps cliTerminalCapabilities) (bool, error) {
	if err := validateCLIUI(ui); err != nil {
		return false, exitError(2, err)
	}
	if ui == "plain" {
		return false, nil
	}
	eligible := caps.Output && !strings.EqualFold(caps.Term, "dumb") && (caps.Input || mode == api.InteractionModeUnattended)
	if ui == "tui" {
		if !eligible {
			return false, exitError(2, errors.New("--ui=tui requires usable terminal output and, for interactive mode, terminal input; use --ui=plain"))
		}
		return true, nil
	}
	ci := strings.ToLower(strings.TrimSpace(caps.CI))
	return eligible && (ci == "" || ci == "false" || ci == "0" || ci == "no"), nil
}

func runUpload(ctx context.Context, args []string, opts cliOptions, visited map[string]bool, paths []string, streams cliIO) error {
	if err := validateCLIUI(opts.UI); err != nil {
		return exitError(2, err)
	}
	if opts.CreateAuth && opts.interactionMode() == api.InteractionModeUnattended {
		return exitError(2, errors.New("--create-auth cannot prompt in unattended mode; use --uac to provide required input"))
	}
	// Static paths never probe a terminal or start an interactive lifetime.
	if opts.ShowVersion || opts.Cleanup || opts.ExportConfigPath != "" || opts.ImportConfigPath != "" {
		return runUploadDriver(ctx, args, opts, visited, paths, streams)
	}
	render := streams.out
	if opts.AudioAnalysisOnly {
		render = streams.errOut
	}
	caps := detectCLITerminal(streams, render)
	useTUI, err := selectCLITUI(opts.UI, opts.interactionMode(), caps)
	if err != nil {
		return err
	}
	launch := len(paths) == 0 && useTUI && opts.UI == "tui" &&
		opts.interactionMode() == api.InteractionModeInteractive && !opts.CreateAuth && !opts.AudioAnalysisOnly
	streams.dashboard = opts.interactionMode() == api.InteractionModeUnattended
	streams.keepOpen = opts.UIKeepOpen
	return runCLIPresentation(
		ctx,
		streams,
		caps,
		useTUI,
		launch,
		opts.UI == "tui",
		args,
		paths,
		opts.Debug || opts.SiteCheck || opts.LiveTest,
		opts.AudioAnalysisOnly,
		func(runCtx context.Context, nextArgs, nextPaths []string, nextStreams cliIO) error {
			if launch {
				nextOpts, nextVisited, parsedPaths, parseErr := parseCLIOptions(nextArgs)
				if parseErr != nil {
					return exitError(2, parseErr)
				}
				publishCLIInvocation(nextStreams, nextArgs, nextOpts.Debug || nextOpts.SiteCheck || nextOpts.LiveTest)
				return runUploadDriver(runCtx, nextArgs, nextOpts, nextVisited, parsedPaths, nextStreams)
			}
			result := runUploadDriver(runCtx, args, opts, visited, nextPaths, nextStreams)
			if result == nil && opts.CreateAuth && nextStreams.presenter != nil {
				nextStreams.presenter.Publish(cliView{Result: "Created local WebUI authentication file."})
			}
			return result
		},
	)
}

func runAuthPasswordPresentation(ctx context.Context, opts authPasswordOptions, configProvided bool, streams cliIO) error {
	if err := validateCLIUI(opts.ui); err != nil {
		return exitError(2, err)
	}
	caps := detectCLITerminal(streams, streams.out)
	tui, err := selectCLITUI(opts.ui, api.InteractionModeInteractive, caps)
	if err != nil {
		return err
	}
	streams.keepOpen = opts.uiKeepOpen
	return runCLIPresentation(ctx, streams, caps, tui, false, opts.ui == "tui", nil, nil, false, false,
		func(runCtx context.Context, _, _ []string, nextStreams cliIO) error {
			result := runChangeAuthPasswordCommand(runCtx, opts, configProvided, nextStreams)
			if result == nil && nextStreams.presenter != nil {
				nextStreams.presenter.Publish(cliView{Result: "Password changed. Retained browser sessions were revoked."})
			}
			return result
		})
}
