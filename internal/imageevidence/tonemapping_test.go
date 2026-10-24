// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imageevidence

import (
	"github.com/autobrr/upbrr/pkg/api"
	"os"
	"path/filepath"
	"testing"
)

func TestTonemappingRequiresSameBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image.png")
	if err := os.WriteFile(path, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, err := Digest(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []api.ScreenshotTonemapping{api.ScreenshotTonemapped, api.ScreenshotNotTonemapped} {
		if got := Tonemapping(path, digest, status); got != status {
			t.Fatalf("same bytes = %q", got)
		}
	}
	if got := Tonemapping(path, digest, ""); got != "" {
		t.Fatalf("legacy unknown = %q", got)
	}
	if err := os.WriteFile(path, []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Tonemapping(path, digest, api.ScreenshotTonemapped); got != "" {
		t.Fatalf("replacement inherited %q", got)
	}
	if got := Tonemapping(path, "", api.ScreenshotNotTonemapped); got != "" {
		t.Fatalf("missing digest = %q", got)
	}
}
