// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

type cliProgressLogState struct {
	lastPercent int
	lastLog     time.Time
}

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
		if !shouldLogCLIProgress(update, states, &mu) {
			return
		}
		logger.Debugf("torrent: %s", cliProgressMessage(update))
	})
}

func shouldLogCLIProgress(update api.UploadProgressUpdate, states map[string]cliProgressLogState, mu *sync.Mutex) bool {
	status := strings.ToLower(strings.TrimSpace(update.Status))
	if status == "completed" || status == "failed" {
		return true
	}
	if update.TotalPieces <= 0 {
		return true
	}

	key := update.SourcePath + "\x00" + update.Tracker + "\x00" + update.Task
	now := time.Now()

	mu.Lock()
	defer mu.Unlock()

	state := states[key]
	if state.lastLog.IsZero() {
		states[key] = cliProgressLogState{lastPercent: update.Percent, lastLog: now}
		return true
	}
	if update.Percent >= state.lastPercent+5 || now.Sub(state.lastLog) >= 10*time.Second {
		states[key] = cliProgressLogState{lastPercent: update.Percent, lastLog: now}
		return true
	}
	return false
}

func cliProgressMessage(update api.UploadProgressUpdate) string {
	message := strings.TrimSpace(update.Message)
	if message == "" {
		message = strings.TrimSpace(update.Status)
	}
	if update.TotalPieces <= 0 {
		return fmt.Sprintf("progress source=%s status=%s message=%q", update.SourcePath, update.Status, message)
	}
	if update.HashRateMiB > 0 {
		return fmt.Sprintf("progress source=%s status=%s percent=%d pieces=%d/%d rate=%.0fMiB/s message=%q", update.SourcePath, update.Status, update.Percent, update.CompletedPieces, update.TotalPieces, update.HashRateMiB, message)
	}
	return fmt.Sprintf("progress source=%s status=%s percent=%d pieces=%d/%d message=%q", update.SourcePath, update.Status, update.Percent, update.CompletedPieces, update.TotalPieces, message)
}
