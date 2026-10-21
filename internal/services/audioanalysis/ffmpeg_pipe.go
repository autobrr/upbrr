// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package audioanalysis

import (
	"context"
	"fmt"
	"io"
	"os/exec"

	"github.com/autobrr/upbrr/internal/services/ffmpegstream"
)

type pcmOutput struct{ pipe ffmpegstream.Pipe }

func newPCMOutputs(count int) ([]pcmOutput, error) {
	pipes, err := ffmpegstream.NewPipes(count)
	if err != nil {
		return nil, fmt.Errorf("create PCM outputs: %w", err)
	}
	outputs := make([]pcmOutput, len(pipes))
	for i := range pipes {
		outputs[i].pipe = pipes[i]
	}
	return outputs, nil
}

func attachPCMOutputs(cmd *exec.Cmd, outputs []pcmOutput) {
	pipes := make([]ffmpegstream.Pipe, len(outputs))
	for i := range outputs {
		pipes[i] = outputs[i].pipe
	}
	ffmpegstream.AttachPipes(cmd, pipes)
}

func (p *pcmOutput) destination() string { return p.pipe.Destination() }
func (p *pcmOutput) started()            { p.pipe.Started() }
func (p *pcmOutput) close()              { p.pipe.Close() }
func (p *pcmOutput) reader(ctx context.Context, pid int) (io.ReadCloser, error) {
	reader, err := p.pipe.Reader(ctx, pid)
	if err != nil {
		return nil, fmt.Errorf("connect PCM output: %w", err)
	}
	return reader, nil
}
