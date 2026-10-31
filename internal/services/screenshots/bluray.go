// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"context"
	"errors"
	"fmt"

	"github.com/autobrr/upbrr/internal/dvdvideo/render"
)

type protocolRunner struct{ runner Runner }

func (r protocolRunner) Run(ctx context.Context, executable string, args []string, limit int) (render.Output, error) {
	result, err := r.runner.Run(ctx, executable, args, "")
	if len(result.Stdout) > limit || len(result.Stderr) > render.MaxDiagnosticBytes {
		return render.Output{}, errors.New("FFmpeg protocol output exceeded diagnostic limit")
	}
	if err == nil && result.ExitCode != 0 {
		err = fmt.Errorf("FFmpeg protocol probe exited with status %d", result.ExitCode)
	}
	return render.Output{Stdout: result.Stdout, Stderr: result.Stderr}, err
}

func requireBlurayProtocol(ctx context.Context, runner Runner, executable string, info videoInfo) error {
	if info.Playlist == "" {
		return nil
	}
	available, err := render.ProbeBluray(ctx, protocolRunner{runner}, executable)
	if err != nil {
		return fmt.Errorf("screenshots: cannot verify FFmpeg libbluray input support: %w", err)
	}
	if !available {
		return errors.New("screenshots: FFmpeg lacks libbluray input support; install an FFmpeg build with the bluray protocol to capture this ISO")
	}
	return nil
}
