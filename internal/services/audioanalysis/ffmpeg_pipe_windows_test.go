// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build windows

package audioanalysis

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestPrivatePCMOutputRejectsOtherProcess(t *testing.T) {
	outputs, err := newPCMOutputs(1)
	if err != nil {
		t.Fatal(err)
	}
	defer closePCMOutputs(outputs)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	type result struct {
		reader io.ReadCloser
		err    error
	}
	accepted := make(chan result, 1)
	go func() {
		reader, acceptErr := outputs[0].reader(ctx, os.Getpid())
		accepted <- result{reader: reader, err: acceptErr}
	}()

	rogue := exec.CommandContext(ctx, os.Args[0], "-test.run=TestFFmpegDecodeHelperProcess", "--", "rogue-pipe", outputs[0].destination())
	if output, err := rogue.CombinedOutput(); err != nil {
		t.Fatalf("connect other process: %v: %s", err, output)
	}

	var authentic io.WriteCloser
	for ctx.Err() == nil {
		authentic, err = openHelperPCM(outputs[0].destination())
		if err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("connect expected process: %v", err)
	}
	if _, err := authentic.Write([]byte("good")); err != nil {
		t.Fatal(err)
	}
	if err := authentic.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-accepted:
		if got.err != nil {
			t.Fatal(got.err)
		}
		defer got.reader.Close()
		content, err := io.ReadAll(got.reader)
		if err != nil || string(content) != "good" {
			t.Fatalf("accepted PCM = %q, err = %v", content, err)
		}
	case <-ctx.Done():
		t.Fatal("private PCM reader did not accept expected process")
	}
}

func TestPrivatePCMOutputCancelsWithoutWriter(t *testing.T) {
	outputs, err := newPCMOutputs(1)
	if err != nil {
		t.Fatal(err)
	}
	defer closePCMOutputs(outputs)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	accepted := make(chan error, 1)
	go func() {
		reader, acceptErr := outputs[0].reader(ctx, os.Getpid())
		if reader != nil {
			_ = reader.Close()
		}
		accepted <- acceptErr
	}()
	select {
	case err := <-accepted:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("accept error = %v, want canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("private PCM accept did not stop after cancellation")
	}
}
