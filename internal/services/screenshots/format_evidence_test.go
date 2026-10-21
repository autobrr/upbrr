// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestScreenshotFormatUsesBytesRatherThanExtension(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic.jpg")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err = png.Encode(file, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	got, ok := buildScreenshotImage(path, 1)
	if !ok || got.Format != "png" {
		t.Fatalf("format=%q found=%t", got.Format, ok)
	}
	if err = os.WriteFile(path, []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _ = buildScreenshotImage(path, 1)
	if got.Format != "" {
		t.Fatal("invalid content gained format evidence")
	}
}
