// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/autobrr/upbrr/internal/services/db"
)

type inputCapture struct {
	root     *Logger
	id       string
	mu       sync.Mutex
	sequence uint64
	path     string
	closed   atomic.Bool
}

// InputLogRepository saves only input log associations and outcomes.
type InputLogRepository interface {
	BeginInputLog(context.Context, db.InputLogAttempt) error
	FinishInputLog(context.Context, db.InputLogAttempt) error
}

// BeginInputLog scopes a run to an opaque ID. A nested phase inherits an
// existing capture, so a CLI invocation can export one log for the whole input.
func (l *Logger) BeginInputLog(ctx context.Context, repo InputLogRepository, source, kind string) (context.Context, string, func(path, outcome string)) {
	noFinish := func(string, string) {}
	if l == nil || repo == nil || strings.TrimSpace(source) == "" {
		return ctx, "", noFinish
	}
	if view, ok := ctx.Value(operationLoggerKey{}).(*OperationLogger); ok && view.root == l && view.capture != nil && !view.capture.isClosed() {
		return ctx, view.capture.id, func(path, _ string) { view.capture.bindPath(path) }
	}
	path, err := filepath.Abs(strings.TrimSpace(source))
	if err != nil {
		return ctx, "", noFinish
	}
	var token [12]byte
	rand.Read(token[:])
	id := hex.EncodeToString(token[:])
	attempt := db.InputLogAttempt{
		ID:            id,
		SourcePath:    path,
		RequestedPath: path,
		Kind:          kind,
		StartedAt:     time.Now().UTC(),
	}
	if err := repo.BeginInputLog(ctx, attempt); err != nil {
		l.Warnf("logging: could not associate input attempt")
		return ctx, "", noFinish
	}
	c := &inputCapture{root: l, id: id}
	level := l.level
	if view, ok := ctx.Value(operationLoggerKey{}).(*OperationLogger); ok && view.root == l {
		level = view.level
	}
	c.begin()
	return WithOperationLogger(ctx, &OperationLogger{
			root:    l,
			level:   level,
			capture: c,
		}), id, func(path, outcome string) {
			boundPath, ok := c.finish(path)
			if !ok {
				return
			}
			completed := attempt
			completed.SourcePath = boundPath
			completed.Outcome = outcome
			saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			if err := repo.FinishInputLog(saveCtx, completed); err != nil {
				l.Warnf("logging: could not save input attempt outcome")
			}
		}
}

func (c *inputCapture) isClosed() bool {
	return c == nil || c.closed.Load()
}

func (c *inputCapture) bindPath(path string) {
	if path != "" {
		c.mu.Lock()
		if !c.closed.Load() {
			c.path = path
		}
		c.mu.Unlock()
	}
}

func (c *inputCapture) begin() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() || c.root.file == nil {
		return
	}
	c.sequence++
	c.root.file.Printf("INFO: [input-log %s %d begin %d] Input processing started.",
		c.id, c.sequence, time.Now().UnixNano())
}

func (c *inputCapture) writeRecord(label, message string) (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() {
		return time.Time{}, false
	}
	at := time.Now()
	if c.root.file != nil {
		c.sequence++
		c.root.file.Printf("%s: [input-log %s %d record %d] %s",
			label, c.id, c.sequence, at.UnixNano(), diskLogMessage(message))
	}
	c.root.recordInputLog(Entry{
		Time:     at,
		Level:    strings.ToLower(label),
		Message:  message,
		attempt:  c.id,
		sequence: c.sequence,
	})
	return at, true
}

func (c *inputCapture) finish(path string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() {
		return "", false
	}
	if path != "" {
		c.path = path
	}
	c.closed.Store(true)
	if c.root.file != nil {
		c.sequence++
		c.root.file.Printf("INFO: [input-log %s %d end %d] Input processing finished.",
			c.id, c.sequence, time.Now().UnixNano())
	}
	return c.path, true
}

func diskLogMessage(message string) string {
	return strings.ReplaceAll(message, "\n", "\n\t")
}
