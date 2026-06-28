// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"io"
	"os"
	"testing"

	"github.com/autobrr/upbrr/internal/logging"
)

func TestMain(m *testing.M) {
	restore := logging.SetDefaultConsoleOutput(io.Discard, io.Discard)
	code := m.Run()
	restore()
	os.Exit(code)
}
