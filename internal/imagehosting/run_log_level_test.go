// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imagehosting

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestUploadUsesOperationLogLevel(t *testing.T) {
	t.Parallel()
	for _, level := range []string{"trace", "warn"} {
		t.Run(level, func(t *testing.T) {
			t.Parallel()
			root, err := logging.New(config.LoggingConfig{Level: "info"}, "")
			if err != nil {
				t.Fatal(err)
			}
			root.SetConsoleOutput(io.Discard, io.Discard)
			t.Cleanup(func() { _ = root.Close() })
			scoped, err := logging.NewOperationLogger(root, level)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "example.png")
			if err := os.WriteFile(path, []byte("synthetic image"), 0o600); err != nil {
				t.Fatal(err)
			}
			service := &Service{
				cfg:    config.Config{ScreenshotHandling: config.ScreenshotHandlingConfig{MaxConcurrentUploads: 1}},
				logger: root,
				repo:   &recordingRepo{},
				uploaders: map[string]uploader{"test": &fakeUploader{result: uploadResult{
					ImgURL: "https://images.example.invalid/image",
					RawURL: "https://images.example.invalid/raw",
					WebURL: "https://images.example.invalid/page",
				}}},
			}
			if _, err := service.Upload(logging.WithOperationLogger(t.Context(), scoped), imageHostingTestSubject("example"), "test", "", []api.ScreenshotImage{{Path: path}}); err != nil {
				t.Fatal(err)
			}
			entries := root.Recent(100)
			if level == "warn" && len(entries) != 0 {
				t.Fatal("quiet image upload retained progress logs")
			}
			if level == "trace" && !slices.ContainsFunc(entries, func(entry logging.Entry) bool { return entry.Level == "trace" }) {
				t.Fatal("image upload dropped requested trace diagnostics")
			}
			root.Debugf("synthetic unrelated debug")
			root.Infof("synthetic unrelated info")
			if got := root.Recent(100); len(got) != len(entries)+1 || got[len(got)-1].Message != "synthetic unrelated info" {
				t.Fatal("image upload changed root verbosity")
			}
		})
	}
}

func TestImageHostingRejectsNilService(t *testing.T) {
	t.Parallel()
	var service *Service
	if _, err := service.ListCandidates(t.Context(), api.ImageHostingSubject{}); err == nil {
		t.Fatal("nil image service did not return an error")
	}
	if _, err := service.Upload(t.Context(), api.ImageHostingSubject{}, "test", "", nil); err == nil {
		t.Fatal("nil image service did not return an error")
	}
}
