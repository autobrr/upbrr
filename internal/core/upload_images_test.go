// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

type stubImageHosting struct {
	candidates []api.ScreenshotImage
	uploaded   []api.UploadedImageLink
	err        error
	lastMeta   api.PreparedMetadata
}

func (s *stubImageHosting) ListCandidates(ctx context.Context, meta api.PreparedMetadata) ([]api.ScreenshotImage, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.lastMeta = meta
	return s.candidates, nil
}

func (s *stubImageHosting) Upload(ctx context.Context, meta api.PreparedMetadata, host string, usageScope string, images []api.ScreenshotImage) ([]api.UploadedImageLink, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.lastMeta = meta
	return s.uploaded, nil
}

func TestUploadImagesWithoutCache(t *testing.T) {
	t.Parallel()

	images := []api.ScreenshotImage{{Path: "/tmp/img1.png"}}
	uploaded := []api.UploadedImageLink{{ImagePath: "/tmp/img1.png", Host: "imgbox"}}

	core := &Core{
		logger: api.NopLogger{},
		cfg:    config.Config{},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{"/tmp/source"}},
			Images:     &stubImageHosting{uploaded: uploaded},
		},
		dupeCache: make(map[string]dupeCacheEntry),
	}

	result, err := core.UploadImages(context.Background(), api.Request{
		Paths: []string{"/tmp/source"},
		Mode:  api.ModeGUI,
	}, "imgbox", images)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	if result[0].Host != "imgbox" {
		t.Fatalf("expected host imgbox, got %s", result[0].Host)
	}
}

func TestListUploadCandidatesWithoutCache(t *testing.T) {
	t.Parallel()

	candidates := []api.ScreenshotImage{{Path: "/tmp/img1.png"}}

	core := &Core{
		logger: api.NopLogger{},
		cfg:    config.Config{},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{"/tmp/source"}},
			Images:     &stubImageHosting{candidates: candidates},
		},
		dupeCache: make(map[string]dupeCacheEntry),
	}

	result, err := core.ListUploadCandidates(context.Background(), api.Request{
		Paths: []string{"/tmp/source"},
		Mode:  api.ModeGUI,
	})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(result))
	}
	if result[0].Path != "/tmp/img1.png" {
		t.Fatalf("expected path /tmp/img1.png, got %s", result[0].Path)
	}
}

func TestUploadImagesGUIFallbackReappliesReleaseOverrides(t *testing.T) {
	t.Parallel()

	images := []api.ScreenshotImage{{Path: "/tmp/img1.png"}}
	imageService := &stubImageHosting{uploaded: []api.UploadedImageLink{{ImagePath: "/tmp/img1.png", Host: "imgbox"}}}
	core := &Core{
		logger: api.NopLogger{},
		cfg:    config.Config{},
		services: api.ServiceSet{
			Filesystem: stubFilesystem{paths: []string{"/tmp/source"}},
			Metadata:   &stubMeta{},
			Images:     imageService,
		},
		dupeCache: make(map[string]dupeCacheEntry),
	}
	core.storeDupeCache("/tmp/source", "", api.PreparedMetadata{
		SourcePath: "/tmp/source",
		Release: api.ReleaseInfo{
			Title: "Example",
		},
	})

	edition := "Director's Cut"
	_, err := core.UploadImages(context.Background(), api.Request{
		Paths: []string{"/tmp/source"},
		Mode:  api.ModeGUI,
		ReleaseNameOverrides: api.ReleaseNameOverrides{
			Edition: &edition,
		},
	}, "imgbox", images)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if imageService.lastMeta.ReleaseNameOverrides.Edition == nil || *imageService.lastMeta.ReleaseNameOverrides.Edition != edition {
		t.Fatalf("expected upload images to receive edition override, got %#v", imageService.lastMeta.ReleaseNameOverrides)
	}
}
