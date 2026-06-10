// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestCLIUploadProgressRendersTorrentOnSingleLine(t *testing.T) {
	var output bytes.Buffer
	originalOutput := cliProgressOutput
	cliProgressOutput = &output
	t.Cleanup(func() {
		cliProgressOutput = originalOutput
	})

	ctx := withCLIUploadProgressLogger(context.Background(), api.NopLogger{})
	api.EmitUploadProgress(ctx, api.UploadProgressUpdate{
		SourcePath:      "movie.mkv",
		Task:            "torrent",
		Status:          "running",
		Message:         "Hashing pieces... 10% (10/100 pieces)",
		CompletedPieces: 10,
		TotalPieces:     100,
		Percent:         10,
	})
	api.EmitUploadProgress(ctx, api.UploadProgressUpdate{
		SourcePath:      "movie.mkv",
		Task:            "torrent",
		Status:          "running",
		Message:         "Hashing pieces... 15% (15/100 pieces)",
		CompletedPieces: 15,
		TotalPieces:     100,
		Percent:         15,
	})

	rendered := output.String()
	if strings.Count(rendered, "\r") != 2 {
		t.Fatalf("expected two carriage-return renders, got %q", rendered)
	}
	if strings.Contains(rendered, "\n") {
		t.Fatalf("expected running progress not to add newlines, got %q", rendered)
	}
	if !strings.HasSuffix(rendered, "torrent: Hashing pieces... 15% (15/100 pieces)") {
		t.Fatalf("expected latest progress at end, got %q", rendered)
	}
}

func TestCLIUploadProgressFinalStatusEndsLine(t *testing.T) {
	var output bytes.Buffer
	originalOutput := cliProgressOutput
	cliProgressOutput = &output
	t.Cleanup(func() {
		cliProgressOutput = originalOutput
	})

	ctx := withCLIUploadProgressLogger(context.Background(), api.NopLogger{})
	api.EmitUploadProgress(ctx, api.UploadProgressUpdate{
		SourcePath: "movie.mkv",
		Task:       "torrent",
		Status:     "running",
		Message:    "Creating torrent with mkbrr",
	})
	api.EmitUploadProgress(ctx, api.UploadProgressUpdate{
		SourcePath: "movie.mkv",
		Task:       "torrent",
		Status:     "completed",
		Message:    "Torrent ready",
	})

	rendered := output.String()
	if !strings.HasSuffix(rendered, "\n") {
		t.Fatalf("expected final progress to end with newline, got %q", rendered)
	}
	if !strings.Contains(rendered, "\rtorrent: Torrent ready") {
		t.Fatalf("expected final progress render, got %q", rendered)
	}
}

func TestCLIUploadProgressIgnoresNonTorrentTasks(t *testing.T) {
	var output bytes.Buffer
	originalOutput := cliProgressOutput
	cliProgressOutput = &output
	t.Cleanup(func() {
		cliProgressOutput = originalOutput
	})

	ctx := withCLIUploadProgressLogger(context.Background(), api.NopLogger{})
	api.EmitUploadProgress(ctx, api.UploadProgressUpdate{
		SourcePath: "movie.mkv",
		Task:       "tracker_upload",
		Status:     "running",
		Message:    "Uploading",
	})

	if output.Len() != 0 {
		t.Fatalf("expected no output for non-torrent task, got %q", output.String())
	}
}
