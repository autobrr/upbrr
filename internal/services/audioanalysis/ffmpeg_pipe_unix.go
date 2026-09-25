// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build !windows

package audioanalysis

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
)

type pcmOutput struct {
	read  *os.File
	write *os.File
	index int
}

func newPCMOutputs(count int) ([]pcmOutput, error) {
	outputs := make([]pcmOutput, 0, count)
	for index := range count {
		read, write, err := os.Pipe()
		if err != nil {
			closePCMOutputs(outputs)
			return nil, fmt.Errorf("create PCM pipe: %w", err)
		}
		outputs = append(outputs, pcmOutput{
			read:  read,
			write: write,
			index: index,
		})
	}
	return outputs, nil
}

func attachPCMOutputs(cmd *exec.Cmd, outputs []pcmOutput) {
	// ExtraFiles gives the child private write ends at descriptors 3, 4, and so on.
	for index := range outputs {
		cmd.ExtraFiles = append(cmd.ExtraFiles, outputs[index].write)
	}
}

func (p *pcmOutput) destination() string { return fmt.Sprintf("pipe:%d", p.index+3) }

func (p *pcmOutput) started() { _ = p.write.Close() }

func (p *pcmOutput) reader(context.Context, int) (io.ReadCloser, error) { return p.read, nil }

func (p *pcmOutput) close() {
	_ = p.read.Close()
	_ = p.write.Close()
}
