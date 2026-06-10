// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

type cliProgressLogState struct {
	lastPercent int
	lastLog     time.Time
	lastLineLen int
}

var cliProgressOutput io.Writer = os.Stdout

func withCLIUploadProgressLogger(ctx context.Context, logger api.Logger) context.Context {
	if logger == nil {
		return ctx
	}

	var mu sync.Mutex
	states := make(map[string]cliProgressLogState)

	return api.WithUploadProgressReporter(ctx, func(update api.UploadProgressUpdate) {
		if strings.TrimSpace(update.Task) != "torrent" {
			return
		}
		renderCLIProgress(update, states, &mu)
	})
}

func renderCLIProgress(update api.UploadProgressUpdate, states map[string]cliProgressLogState, mu *sync.Mutex) {
	if cliProgressOutput == nil {
		return
	}

	key := update.SourcePath + "\x00" + update.Tracker + "\x00" + update.Task
	now := time.Now()

	mu.Lock()
	defer mu.Unlock()

	state := states[key]
	if !shouldRenderCLIProgress(update, state, now) {
		return
	}

	line := cliProgressLine(update)
	padding := ""
	if len(line) < state.lastLineLen {
		padding = strings.Repeat(" ", state.lastLineLen-len(line))
	}
	if progressStatusFinal(update.Status) {
		fmt.Fprintf(cliProgressOutput, "\r%s%s\n", line, padding)
		state.lastLineLen = 0
	} else {
		fmt.Fprintf(cliProgressOutput, "\r%s%s", line, padding)
		state.lastLineLen = len(line)
	}
	state.lastPercent = update.Percent
	state.lastLog = now
	states[key] = state
}

func shouldRenderCLIProgress(update api.UploadProgressUpdate, state cliProgressLogState, now time.Time) bool {
	status := strings.ToLower(strings.TrimSpace(update.Status))
	if progressStatusFinal(status) {
		return true
	}
	if update.TotalPieces <= 0 {
		return true
	}

	if state.lastLog.IsZero() {
		return true
	}
	if update.Percent >= state.lastPercent+5 || now.Sub(state.lastLog) >= 10*time.Second {
		return true
	}
	return false
}

func progressStatusFinal(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "failed":
		return true
	default:
		return false
	}
}

func cliProgressLine(update api.UploadProgressUpdate) string {
	message := strings.TrimSpace(update.Message)
	if message == "" {
		message = strings.TrimSpace(update.Status)
	}
	if update.TotalPieces <= 0 {
		return "torrent: " + message
	}
	if update.HashRateMiB > 0 {
		return "torrent: " + message
	}
	return "torrent: " + message
}
