// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

type Logger interface {
	Debugf(format string, args ...any)
	Infof(format string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Debugf(string, ...any) {
	// Intentionally no-op.
}

func (nopLogger) Infof(string, ...any) {
	// Intentionally no-op.
}
