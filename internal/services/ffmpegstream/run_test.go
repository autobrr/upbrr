// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ffmpegstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestSharedOutputsDrainFailureAndCancel(t *testing.T) {
	if os.Getenv("UPBRR_HDR_DECODE_CALIBRATION") != "1" {
		t.Skip("requires actual FFmpeg")
	}
	executable, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "color=s=16x16:r=10:d=1"}
	outputArgs := []string{"-map", "0:v:0", "-c:v", "rawvideo", "-threads:v", "1", "-pix_fmt", "gray", "-f", "rawvideo"}
	failed := errors.New("synthetic consumer failure")
	var received int64
	result, err := Run(t.Context(), exec.CommandContext(t.Context(), executable, args...), []Output{
		{Arguments: outputArgs, Consume: func(io.Reader) error { return failed }},
		{Arguments: outputArgs, Consume: func(reader io.Reader) error {
			var err error
			received, err = io.Copy(io.Discard, reader)
			if err != nil {
				return fmt.Errorf("read sibling output: %w", err)
			}
			return nil
		}},
	})
	if err != nil || result.ProcessError != nil || !errors.Is(result.Errors[0], failed) || result.Errors[1] != nil || received != 2560 {
		t.Fatalf("sibling output lost: %+v %v bytes=%d", result, err, received)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	args = []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-y", "-re", "-f", "lavfi", "-i", "color=s=16x16:r=10:d=60"}
	_, err = Run(ctx, exec.CommandContext(ctx, executable, args...), []Output{
		{Arguments: outputArgs, Consume: func(reader io.Reader) error {
			_, err := io.ReadFull(reader, make([]byte, 256))
			cancel()
			if err != nil {
				return fmt.Errorf("read canceled output: %w", err)
			}
			return nil
		}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("shared cancellation: %v", err)
	}
}
