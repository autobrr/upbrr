// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	logger := &screenshotRecordingLogger{}
	info, err := resolveVideoInfo(context.Background(), meta, "", logger)
	if err != nil {
		t.Fatalf("resolve video info: %v", err)
	}
	if info.SourcePath != large {
		t.Fatalf("expected largest playlist m2ts %q, got %q", large, info.SourcePath)
	}
	if !logger.Contains("kind=disc_bdmv") || !logger.Contains("method=metadata") {
		t.Fatal("expected BDMV metadata source-selection trace")
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

	logger := &screenshotRecordingLogger{}
	got, err := resolveVideoSource(context.Background(), meta, "", logger)
	if err != nil {
		t.Fatalf("resolve video source: %v", err)
	}
	if got != large {
		t.Fatalf("expected largest playlist m2ts %q, got %q", large, got)
	}
	if !logger.Contains("kind=disc_bdmv") || !logger.Contains("method=bdmv_metadata") {
		t.Fatal("expected BDMV preview source-selection trace")
	}
}

func TestResolveVideoInfoLogsSeasonPackSourceKind(t *testing.T) {
	tmpDir := t.TempDir()
	mediaInfoPath := filepath.Join(tmpDir, "mediainfo.json")
	payload := []byte(`{"media":{"track":[{"@type":"General","Duration":"1800"},{"@type":"Video","FrameRate":"24.000"}]}}`)
	if err := os.WriteFile(mediaInfoPath, payload, 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}

	logger := &screenshotRecordingLogger{}
	meta := api.PreparedMetadata{
		SourcePath:        filepath.Join(tmpDir, "Example.Release.2026.S01"),
		MediaInfoJSONPath: mediaInfoPath,
		MediaInfoCategory: "TV",
		TVPack:            true,
	}

	info, err := resolveVideoInfo(context.Background(), meta, "", logger)
	if err != nil {
		t.Fatalf("resolve video info: %v", err)
	}
	if info.SourcePath != meta.SourcePath {
		t.Fatal("expected base source path")
	}
	if !logger.Contains("kind=season_pack") || !logger.Contains("method=base") {
		t.Fatal("expected season-pack base source trace")
	}
}

func TestSelectDVDVOBIncludesZeroVOBAndUsesLargestTitleSet(t *testing.T) {
	root := t.TempDir()
	videoTS := filepath.Join(root, "VIDEO_TS")
	if err := os.MkdirAll(videoTS, 0o700); err != nil {
		t.Fatalf("mkdir VIDEO_TS: %v", err)
	}
	files := map[string]int{
		"VTS_01_0.VOB": 200,
		"VTS_01_1.VOB": 10,
		"VTS_02_0.VOB": 1,
		"VTS_02_1.VOB": 30,
		"VTS_02_2.VOB": 20,
	}
	for name, size := range files {
		if err := os.WriteFile(filepath.Join(videoTS, name), make([]byte, size), 0o600); err != nil {
			t.Fatalf("write VOB fixture: %v", err)
		}
	}

	got, err := selectDVDVOB(context.Background(), root)
	if err != nil {
		t.Fatalf("select DVD VOB: %v", err)
	}
	want := filepath.Join(videoTS, "VTS_02_0.VOB")
	if got != want {
		t.Fatalf("expected first numeric VOB from largest set %q, got %q", want, got)
	}
}

func TestResolveVideoInfoBuildsOrderedDVDSegments(t *testing.T) {
	root := t.TempDir()
	videoTS := filepath.Join(root, "VIDEO_TS")
	if err := os.MkdirAll(videoTS, 0o700); err != nil {
		t.Fatalf("mkdir VIDEO_TS: %v", err)
	}
	files := map[string]int{
		"VTS_02_0.VOB": 1,
		"VTS_02_2.VOB": 20,
		"VTS_02_1.VOB": 30,
	}
	for name, size := range files {
		if err := os.WriteFile(filepath.Join(videoTS, name), make([]byte, size), 0o600); err != nil {
			t.Fatalf("write VOB fixture: %v", err)
		}
	}
	mediaInfoPath := filepath.Join(root, "mediainfo.json")
	payload := []byte(`{"media":{"track":[{"@type":"General","Duration":"100"},{"@type":"Video","FrameRate":"25.000"}]}}`)
	if err := os.WriteFile(mediaInfoPath, payload, 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}

	info, err := resolveVideoInfo(context.Background(), api.PreparedMetadata{
		SourcePath:        root,
		DiscType:          "DVD",
		MediaInfoJSONPath: mediaInfoPath,
	}, "", api.NopLogger{})
	if err != nil {
		t.Fatalf("resolve video info: %v", err)
	}
	if len(info.Segments) != 3 {
		t.Fatalf("expected 3 DVD segments, got %#v", info.Segments)
	}
	if info.Segments[0].SourcePath != filepath.Join(videoTS, "VTS_02_0.VOB") {
		t.Fatalf("expected first segment VTS_02_0.VOB, got %#v", info.Segments)
	}
	if info.Segments[1].SourcePath != filepath.Join(videoTS, "VTS_02_1.VOB") {
		t.Fatalf("expected second segment VTS_02_1.VOB, got %#v", info.Segments)
	}
	if info.Segments[2].SourcePath != filepath.Join(videoTS, "VTS_02_2.VOB") {
		t.Fatalf("expected third segment VTS_02_2.VOB, got %#v", info.Segments)
	}

	path, timestamp := resolveSegmentTimestamp(info, 70)
	if path != filepath.Join(videoTS, "VTS_02_2.VOB") {
		t.Fatalf("expected timestamp in final segment to use VTS_02_2.VOB, got %q", path)
	}
	if timestamp < 9.20 || timestamp > 9.23 {
		t.Fatalf("expected local timestamp near 9.22s, got %f", timestamp)
	}
}

func TestResolveSegmentCandidatesFallsForwardFromZeroVOB(t *testing.T) {
	info := videoInfo{
		SourcePath: "VTS_01_0.VOB",
		Segments: []videoSegment{
			{SourcePath: "VTS_01_0.VOB", StartSeconds: 0, DurationSeconds: 2},
			{SourcePath: "VTS_01_1.VOB", StartSeconds: 2, DurationSeconds: 98},
		},
	}

	candidates := resolveSegmentCandidates(info, 1)
	if len(candidates) != 2 {
		t.Fatalf("expected primary plus fallback candidate, got %#v", candidates)
	}
	if candidates[0].SourcePath != "VTS_01_0.VOB" || candidates[0].Timestamp != 1 {
		t.Fatalf("expected primary zero VOB candidate, got %#v", candidates[0])
	}
	if candidates[1].SourcePath != "VTS_01_1.VOB" || candidates[1].Timestamp != 0.5 {
		t.Fatalf("expected fallback content VOB candidate, got %#v", candidates[1])
	}
}

func TestResolveSegmentCandidatesFallsForwardWithoutSegmentDurations(t *testing.T) {
	info := videoInfo{
		SourcePath: "VTS_01_0.VOB",
		Segments: []videoSegment{
			{SourcePath: "VTS_01_0.VOB"},
			{SourcePath: "VTS_01_1.VOB"},
		},
	}

	candidates := resolveSegmentCandidates(info, 12)
	if len(candidates) != 2 {
		t.Fatalf("expected durationless DVD segment fallbacks, got %#v", candidates)
	}
	if candidates[0].SourcePath != "VTS_01_0.VOB" || candidates[0].Timestamp != 12 {
		t.Fatalf("expected primary durationless candidate, got %#v", candidates[0])
	}
	if candidates[1].SourcePath != "VTS_01_1.VOB" || candidates[1].Timestamp != 0.5 {
		t.Fatalf("expected fallback durationless candidate, got %#v", candidates[1])
	}
}

func TestSelectDVDVOBAllowsZeroOnlyDisc(t *testing.T) {
	root := t.TempDir()
	videoTS := filepath.Join(root, "VIDEO_TS")
	if err := os.MkdirAll(videoTS, 0o700); err != nil {
		t.Fatalf("mkdir VIDEO_TS: %v", err)
	}
	if err := os.WriteFile(filepath.Join(videoTS, "VTS_01_0.VOB"), []byte("menu"), 0o600); err != nil {
		t.Fatalf("write menu VOB: %v", err)
	}

	got, err := selectDVDVOB(context.Background(), root)
	if err != nil {
		t.Fatalf("select DVD VOB: %v", err)
	}
	want := filepath.Join(videoTS, "VTS_01_0.VOB")
	if got != want {
		t.Fatalf("expected zero-only DVD VOB %q, got %q", want, got)
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

type screenshotRecordingLogger struct {
	entries []string
}

func (l *screenshotRecordingLogger) Tracef(format string, args ...any) {
	l.entries = append(l.entries, fmt.Sprintf(format, args...))
}

func (l *screenshotRecordingLogger) Debugf(format string, args ...any) {
	l.entries = append(l.entries, fmt.Sprintf(format, args...))
}

func (l *screenshotRecordingLogger) Infof(string, ...any) {
	// Intentionally no-op.
}

func (l *screenshotRecordingLogger) Warnf(format string, args ...any) {
	l.entries = append(l.entries, fmt.Sprintf(format, args...))
}

func (l *screenshotRecordingLogger) Errorf(format string, args ...any) {
	l.entries = append(l.entries, fmt.Sprintf(format, args...))
}

func (l *screenshotRecordingLogger) Contains(needle string) bool {
	for _, entry := range l.entries {
		if strings.Contains(entry, needle) {
			return true
		}
	}
	return false
}
