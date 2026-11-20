// Copyright (c) 2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"github.com/autobrr/upbrr/internal/config"
	"testing"
)

func TestCaptureProcessConcurrency(t *testing.T) {
	for _, test := range []struct {
		limited              bool
		limit, pending, want int
	}{
		{false, 1, 0, 0}, {false, 1, 9, 9}, {true, 0, 9, 1}, {true, -1, 9, 1}, {true, 3, 9, 3}, {true, 20, 9, 9}, {true, 3, 0, 0},
	} {
		got := CaptureProcessConcurrency(config.ScreenshotHandlingConfig{FFmpegLimit: test.limited, ProcessLimit: test.limit}, test.pending)
		if got != test.want {
			t.Fatalf("limited=%t limit=%d pending=%d concurrency=%d want=%d", test.limited, test.limit, test.pending, got, test.want)
		}
	}
}
