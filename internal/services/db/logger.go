// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

// Logger receives repository lifecycle, retry, and maintenance diagnostics.
// Repository operations do not require an error-level method because returned
// errors remain caller-owned.
type Logger interface {
	Debugf(format string, args ...any)
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Debugf(string, ...any) {
	// Intentionally no-op.
}

func (nopLogger) Infof(string, ...any) {
	// Intentionally no-op.
}

func (nopLogger) Warnf(string, ...any) {
	// Intentionally no-op.
}
