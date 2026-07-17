// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package logging

import (
	"context"
	"errors"

	"github.com/autobrr/upbrr/pkg/api"
)

type operationLoggerKey struct{}

// OperationLogger is an immutable non-owning verbosity view over one active
// logger. Entries use the root logger's sanitized outputs, buffer, and
// subscribers without changing its configured threshold.
type OperationLogger struct {
	root  *Logger
	level Level
}

// NewOperationLogger creates a non-owning logger view. An empty override uses
// the root logger's configured level.
func NewOperationLogger(root *Logger, override string) (*OperationLogger, error) {
	if root == nil {
		return nil, errors.New("logging: root logger is required")
	}
	level := root.level
	if override != "" {
		parsed, err := ParseLevel(override)
		if err != nil {
			return nil, err
		}
		level = parsed
	}
	return &OperationLogger{root: root, level: level}, nil
}

// WithOperationLogger attaches one operation-local logger view to ctx.
func WithOperationLogger(ctx context.Context, logger api.Logger) context.Context {
	if ctx == nil || logger == nil {
		return ctx
	}
	return context.WithValue(ctx, operationLoggerKey{}, logger)
}

// FromContext returns the operation logger attached to ctx or fallback.
func FromContext(ctx context.Context, fallback api.Logger) api.Logger {
	if ctx != nil {
		if logger, ok := ctx.Value(operationLoggerKey{}).(api.Logger); ok && logger != nil {
			return logger
		}
	}
	if fallback == nil {
		return api.NopLogger{}
	}
	return fallback
}

// Tracef logs a trace entry when enabled by this operation view.
func (l *OperationLogger) Tracef(format string, args ...any) {
	l.logf(LevelTrace, "TRACE", format, args...)
}

// Debugf logs a debug entry when enabled by this operation view.
func (l *OperationLogger) Debugf(format string, args ...any) {
	l.logf(LevelDebug, "DEBUG", format, args...)
}

// Infof logs an info entry when enabled by this operation view.
func (l *OperationLogger) Infof(format string, args ...any) {
	l.logf(LevelInfo, "INFO", format, args...)
}

// Warnf logs a warning entry when enabled by this operation view.
func (l *OperationLogger) Warnf(format string, args ...any) {
	l.logf(LevelWarn, "WARN", format, args...)
}

// Errorf logs an error entry when enabled by this operation view.
func (l *OperationLogger) Errorf(format string, args ...any) {
	l.logf(LevelError, "ERROR", format, args...)
}

func (l *OperationLogger) logf(level Level, label string, format string, args ...any) {
	if l == nil || l.root == nil || level > l.level {
		return
	}
	l.root.writef(level, label, format, args...)
}

var _ api.Logger = (*OperationLogger)(nil)
