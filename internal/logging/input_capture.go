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
	"time"

	"github.com/autobrr/upbrr/internal/services/db"
)

type inputCapture struct {
	root     *Logger
	id       string
	mu       sync.Mutex
	sequence uint64
	path     string
	closed   bool
}

// InputLogRepository saves only input log associations and outcomes.
type InputLogRepository interface {
	BeginInputLog(context.Context, db.InputLogAttempt) error
	FinishInputLog(context.Context, string, string, string) error
}

// BeginInputLog scopes a run to an opaque ID. A nested phase inherits an
// existing capture, so a CLI invocation can export one log for the whole input.
func (l *Logger) BeginInputLog(ctx context.Context, repo InputLogRepository, source, kind string) (context.Context, string, func(path, outcome string)) {
	noFinish := func(string, string) {}
	if l == nil || repo == nil || strings.TrimSpace(source) == "" {
		return ctx, "", noFinish
	}
	if view, ok := ctx.Value(operationLoggerKey{}).(*OperationLogger); ok && view.root == l && view.capture != nil {
		return ctx, view.capture.id, func(path, _ string) { view.capture.bindPath(path) }
	}
	path, err := filepath.Abs(strings.TrimSpace(source))
	if err != nil {
		return ctx, "", noFinish
	}
	var token [12]byte
	rand.Read(token[:])
	id := hex.EncodeToString(token[:])
	if err := repo.BeginInputLog(ctx, db.InputLogAttempt{
		ID:         id,
		SourcePath: path,
		Kind:       kind,
		StartedAt:  time.Now().UTC(),
	}); err != nil {
		l.Warnf("logging: could not associate input attempt")
		return ctx, "", noFinish
	}
	c := &inputCapture{root: l, id: id}
	level := l.level
	if view, ok := ctx.Value(operationLoggerKey{}).(*OperationLogger); ok && view.root == l {
		level = view.level
	}
	c.write("INFO", "begin", "Input processing started.")
	return WithOperationLogger(ctx, &OperationLogger{
			root:    l,
			level:   level,
			capture: c,
		}), id, func(path, outcome string) {
			c.bindPath(path)
			c.write("INFO", "end", "Input processing finished.")
			c.mu.Lock()
			path = c.path
			c.mu.Unlock()
			saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			if err := repo.FinishInputLog(saveCtx, id, path, outcome); err != nil {
				l.Warnf("logging: could not save input attempt outcome")
			}
		}
}

func (c *inputCapture) bindPath(path string) {
	if path != "" {
		c.mu.Lock()
		if !c.closed {
			c.path = path
		}
		c.mu.Unlock()
	}
}

func (c *inputCapture) write(label, kind, message string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	if kind == "end" {
		c.closed = true
	}
	if c.root.file == nil {
		return
	}
	c.sequence++
	c.root.file.Printf("%s: [input-log %s %d %s %d] %s",
		label, c.id, c.sequence, kind, time.Now().UnixNano(), diskLogMessage(message))
}

func diskLogMessage(message string) string {
	return strings.ReplaceAll(message, "\n", "\n\t")
}
