// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildScreenshotSelections(t *testing.T) {
	meta := api.PreparedMetadata{}
	selections := buildScreenshotSelections(4, 600, 24, meta)
	if len(selections) != 4 {
		t.Fatalf("expected 4 selections, got %d", len(selections))
	}
	prev := -1.0
	for _, sel := range selections {
		if sel.TimestampSeconds <= 0 {
			t.Fatalf("expected positive timestamp, got %f", sel.TimestampSeconds)
		}
		if sel.TimestampSeconds <= prev {
			t.Fatalf("timestamps not increasing: %f <= %f", sel.TimestampSeconds, prev)
		}
		prev = sel.TimestampSeconds
	}
}

func TestSanitizeFilename(t *testing.T) {
	got := sanitizeFilename("My:File/Name")
	if got == "" || got == "My:File/Name" {
		t.Fatalf("expected sanitized filename, got %q", got)
	}
}

func TestBuildManualFrameSelections(t *testing.T) {
	selections := buildManualFrameSelections([]int{240, 480, 720}, 24)
	if len(selections) != 3 {
		t.Fatalf("expected 3 selections, got %d", len(selections))
	}
	for idx, selection := range selections {
		if selection.Index != idx {
			t.Fatalf("expected index %d, got %d", idx, selection.Index)
		}
		if selection.Frame != (idx+1)*240 {
			t.Fatalf("unexpected frame at %d: %#v", idx, selection)
		}
		if selection.Source != "manual" {
			t.Fatalf("expected manual source, got %#v", selection)
		}
	}
	if selections[1].TimestampSeconds != 20 {
		t.Fatalf("expected second timestamp 20, got %f", selections[1].TimestampSeconds)
	}
}

func TestParseDurationValueKeepsLargeMediaInfoSeconds(t *testing.T) {
	got := parseDurationValue("10571.090286852")
	if got < 10571.09 || got > 10571.10 {
		t.Fatalf("expected MediaInfo seconds to stay near 10571.09, got %f", got)
	}
}

func TestParseDurationValueParsesMediaInfoText(t *testing.T) {
	got := parseDurationValue("2 h 56 min 11 s")
	want := 10571.0
	if got != want {
		t.Fatalf("expected %f seconds, got %f", want, got)
	}
}

func TestBuildScreenshotSelectionsUsesLongMediaInfoDuration(t *testing.T) {
	duration := parseDurationValue("10571.090286852")
	selections := buildScreenshotSelections(5, duration, 23.976, api.PreparedMetadata{})
	if len(selections) != 5 {
		t.Fatalf("expected 5 selections, got %d", len(selections))
	}
	if selections[0].Frame <= 300 {
		t.Fatalf("expected first frame above 300 for long runtime, got %#v", selections[0])
	}
}

func TestFilterScreenshotsMatchingSelectionsRejectsStaleTimestamps(t *testing.T) {
	selections := []api.ScreenshotSelection{
		{Index: 0, TimestampSeconds: 528.5, Frame: 12671},
		{Index: 1, TimestampSeconds: 2322.0, Frame: 55671},
	}
	images := []api.ScreenshotImage{
		{Index: 0, TimestampSeconds: 0.5, Path: "stale.png"},
		{Index: 1, TimestampSeconds: 2322.1, Path: "current.png"},
	}

	filtered := filterScreenshotsMatchingSelections(images, selections, 23.976)
	if len(filtered) != 1 {
		t.Fatalf("expected only one matching screenshot, got %#v", filtered)
	}
	if filtered[0].Path != "current.png" {
		t.Fatalf("expected current screenshot, got %#v", filtered[0])
	}
}

func TestResolveVideoInfoPrefersLargestSelectedBDMVPlaylistFile(t *testing.T) {
	root := t.TempDir()
	streamDir := filepath.Join(root, "BDMV", "STREAM")
	if err := os.MkdirAll(streamDir, 0o700); err != nil {
		t.Fatalf("mkdir stream dir: %v", err)
	}
	small := filepath.Join(streamDir, "00001.m2ts")
	large := filepath.Join(streamDir, "00002.m2ts")
	for _, path := range []string{small, large} {
		if err := os.WriteFile(path, []byte("m2ts"), 0o600); err != nil {
			t.Fatalf("write stream file: %v", err)
		}
	}

	meta := api.PreparedMetadata{
		SourcePath: root,
		DiscType:   "BDMV",
		VideoPath:  small,
		SelectedBDMVPlaylists: []api.PlaylistInfo{
			{
				File: "00001.MPLS",
				Items: []api.PlaylistItem{
					{File: "00001.m2ts", Size: 100},
					{File: "00002.m2ts", Size: 200},
				},
			},
		},
	}

	info, err := resolveVideoInfo(context.Background(), meta, "")
	if err != nil {
		t.Fatalf("resolve video info: %v", err)
	}
	if info.SourcePath != large {
		t.Fatalf("expected largest playlist m2ts %q, got %q", large, info.SourcePath)
	}
}

func TestResolveVideoSourcePrefersLargestSelectedBDMVPlaylistFile(t *testing.T) {
	root := t.TempDir()
	streamDir := filepath.Join(root, "BDMV", "STREAM")
	if err := os.MkdirAll(streamDir, 0o700); err != nil {
		t.Fatalf("mkdir stream dir: %v", err)
	}
	small := filepath.Join(streamDir, "00001.m2ts")
	large := filepath.Join(streamDir, "00002.m2ts")
	for _, path := range []string{small, large} {
		if err := os.WriteFile(path, []byte("m2ts"), 0o600); err != nil {
			t.Fatalf("write stream file: %v", err)
		}
	}

	meta := api.PreparedMetadata{
		SourcePath: root,
		DiscType:   "BDMV",
		VideoPath:  small,
		SelectedBDMVPlaylists: []api.PlaylistInfo{
			{
				File: "00001.MPLS",
				Items: []api.PlaylistItem{
					{File: "00001.m2ts", Size: 100},
					{File: "00002.m2ts", Size: 200},
				},
			},
		},
	}

	got, err := resolveVideoSource(context.Background(), meta, "")
	if err != nil {
		t.Fatalf("resolve video source: %v", err)
	}
	if got != large {
		t.Fatalf("expected largest playlist m2ts %q, got %q", large, got)
	}
}

func TestMediaInfoVideoGeometryBuildsPARScaleFactors(t *testing.T) {
	var doc mediaInfoDoc
	payload := []byte(`{"media":{"track":[{"@type":"Video","Width":"720 pixels","Height":"576 pixels","PixelAspectRatio":"0.750","DisplayAspectRatio":"4:3"}]}}`)
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatalf("unmarshal mediainfo: %v", err)
	}

	width, height, widthScale, heightScale := mediaInfoVideoGeometry(doc)
	if width != 720 || height != 576 {
		t.Fatalf("expected source dimensions 720x576, got %dx%d", width, height)
	}
	if widthScale != 1 {
		t.Fatalf("expected width scale 1, got %f", widthScale)
	}
	if heightScale < 0.9374 || heightScale > 0.9376 {
		t.Fatalf("expected height scale near 0.9375, got %f", heightScale)
	}
}

func TestMediaInfoVideoGeometryScalesWidthForWidePixels(t *testing.T) {
	var doc mediaInfoDoc
	payload := []byte(`{"media":{"track":[{"@type":"Video","Width":"720","Height":"480","PixelAspectRatio":"1.185","DisplayAspectRatio":"16:9"}]}}`)
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatalf("unmarshal mediainfo: %v", err)
	}

	width, height, widthScale, heightScale := mediaInfoVideoGeometry(doc)
	if width != 720 || height != 480 {
		t.Fatalf("expected source dimensions 720x480, got %dx%d", width, height)
	}
	if widthScale != 1.185 || heightScale != 1 {
		t.Fatalf("expected width scale 1.185 and height scale 1, got %f x %f", widthScale, heightScale)
	}
}
