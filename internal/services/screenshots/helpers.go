// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata/discparse"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/pkg/api"
)

type videoInfo struct {
	SourcePath      string
	DurationSeconds float64
	FrameRate       float64
	Width           int
	Height          int
	WidthScale      float64
	HeightScale     float64
}

type mediaInfoDoc struct {
	Media struct {
		Track []map[string]any `json:"track"`
	} `json:"media"`
}

func resolveVideoInfo(ctx context.Context, meta api.PreparedMetadata, tmpRoot string) (videoInfo, error) {
	info := videoInfo{}

	basePath := strings.TrimSpace(meta.VideoPath)
	if basePath == "" {
		basePath = strings.TrimSpace(meta.SourcePath)
	}
	if basePath == "" {
		return info, errors.New("screenshots: source path required")
	}

	doc, _ := loadMediaInfoDoc(meta.MediaInfoJSONPath)
	info.DurationSeconds = mediaInfoDurationSeconds(doc)
	info.FrameRate = mediaInfoFrameRate(doc)
	info.Width, info.Height, info.WidthScale, info.HeightScale = mediaInfoVideoGeometry(doc)
	if info.FrameRate <= 0 {
		info.FrameRate = 24.0
	}

	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		bdinfo, err := loadBDInfo(tmpRoot, meta)
		if err != nil {
			return info, err
		}
		if bdinfo != nil {
			if info.DurationSeconds <= 0 {
				info.DurationSeconds = parseDurationSeconds(bdinfo.Length)
			}
			if info.FrameRate <= 0 {
				info.FrameRate = parseFPS(bdinfo)
			}
			if filePath, err := selectBDMVFile(ctx, meta.SourcePath, bdinfo); err == nil {
				info.SourcePath = filePath
			}
		}
	}

	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		vob, err := selectDVDVOB(ctx, meta.SourcePath)
		if err != nil {
			return info, err
		}
		info.SourcePath = vob
	}

	if info.SourcePath == "" {
		info.SourcePath = basePath
	}

	return info, nil
}

func resolveVideoSource(ctx context.Context, meta api.PreparedMetadata, tmpRoot string) (string, error) {
	basePath := strings.TrimSpace(meta.VideoPath)
	if basePath == "" {
		basePath = strings.TrimSpace(meta.SourcePath)
	}
	if basePath == "" {
		return "", errors.New("screenshots: source path required")
	}

	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		bdinfo, err := loadBDInfo(tmpRoot, meta)
		if err != nil {
			return "", err
		}
		if bdinfo != nil {
			filePath, err := selectBDMVFile(ctx, meta.SourcePath, bdinfo)
			if err != nil {
				return "", err
			}
			return filePath, nil
		}
	}

	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		vob, err := selectDVDVOB(ctx, meta.SourcePath)
		if err != nil {
			return "", err
		}
		return vob, nil
	}

	return basePath, nil
}

func loadMediaInfoDoc(path string) (mediaInfoDoc, error) {
	var doc mediaInfoDoc
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return doc, nil
	}
	payload, err := os.ReadFile(trimmed)
	if err != nil {
		return doc, fmt.Errorf("screenshots: read mediainfo document: %w", err)
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		return doc, fmt.Errorf("screenshots: unmarshal mediainfo document: %w", err)
	}
	return doc, nil
}

func mediaInfoDurationSeconds(doc mediaInfoDoc) float64 {
	for _, track := range doc.Media.Track {
		trackType := strings.ToLower(trackString(track, "@type"))
		if trackType != "general" && trackType != "video" {
			continue
		}
		if value := trackString(track, "Duration"); value != "" {
			if seconds := parseDurationValue(value); seconds > 0 {
				return seconds
			}
		}
		if value := trackString(track, "Duration/String3", "Duration/String2", "Duration/String"); value != "" {
			if seconds := parseDurationValue(value); seconds > 0 {
				return seconds
			}
		}
	}
	return 0
}

func mediaInfoFrameRate(doc mediaInfoDoc) float64 {
	for _, track := range doc.Media.Track {
		trackType := strings.ToLower(trackString(track, "@type"))
		if trackType != "video" {
			continue
		}
		value := trackString(track, "FrameRate", "FrameRate_Original", "FrameRate_Num")
		if value == "" {
			continue
		}
		if rate := parseFloat(value); rate > 0 {
			return rate
		}
	}
	return 0
}

func mediaInfoVideoGeometry(doc mediaInfoDoc) (int, int, float64, float64) {
	widthScale := 1.0
	heightScale := 1.0
	for _, track := range doc.Media.Track {
		trackType := strings.ToLower(trackString(track, "@type"))
		if trackType != "video" {
			continue
		}
		width := parseDimensionInt(trackString(track, "Width"))
		height := parseDimensionInt(trackString(track, "Height"))
		if width <= 0 || height <= 0 {
			return width, height, widthScale, heightScale
		}
		par := parseAspectFloat(trackString(track, "PixelAspectRatio"))
		if par <= 0 {
			par = 1.0
		}
		dar := parseAspectFloat(trackString(track, "DisplayAspectRatio"))
		if dar <= 0 {
			dar = 16.0 / 9.0
		}

		switch {
		case par == 1:
			return width, height, widthScale, heightScale
		case par < 1:
			scaledHeight := dar * float64(height)
			if scaledHeight > 0 {
				heightScale = float64(width) / scaledHeight
			}
		default:
			widthScale = par
		}
		return width, height, widthScale, heightScale
	}
	return 0, 0, widthScale, heightScale
}

func parseAspectFloat(value string) float64 {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}
	if strings.Contains(trimmed, ":") {
		parts := strings.SplitN(trimmed, ":", 2)
		left := parseFloat(parts[0])
		right := parseFloat(parts[1])
		if left > 0 && right > 0 {
			return left / right
		}
		return 0
	}
	return parseFloat(trimmed)
}

func parseDimensionInt(value string) int {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}
	digits := strings.Builder{}
	for _, r := range trimmed {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	if digits.Len() == 0 {
		return 0
	}
	return int(parseFloat(digits.String()))
}

func parseDurationValue(value string) float64 {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}
	if strings.Contains(trimmed, ":") {
		return parseDurationSeconds(trimmed)
	}
	seconds := parseFloat(trimmed)
	if seconds <= 0 {
		return 0
	}
	if seconds > 10000 {
		return seconds / 1000
	}
	return seconds
}

func parseDurationSeconds(value string) float64 {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}
	parts := strings.Split(trimmed, ":")
	if len(parts) < 2 {
		return parseFloat(trimmed)
	}
	var seconds float64
	multiplier := 1.0
	for i := len(parts) - 1; i >= 0; i-- {
		part := strings.TrimSpace(parts[i])
		if part == "" {
			continue
		}
		seconds += parseFloat(part) * multiplier
		multiplier *= 60
	}
	return seconds
}

func parseFloat(value string) float64 {
	trimmed := strings.TrimSpace(strings.ReplaceAll(value, ",", ""))
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return 0
	}
	trimmed = fields[0]
	parsed, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func trackString(track map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := track[key]
		if !ok || value == nil {
			continue
		}
		if text, ok := value.(string); ok {
			if strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
			continue
		}
		asString := fmt.Sprintf("%v", value)
		if strings.TrimSpace(asString) != "" {
			return strings.TrimSpace(asString)
		}
	}
	return ""
}

func loadBDInfo(tmpRoot string, meta api.PreparedMetadata) (*discparse.BDInfo, error) {
	if !strings.EqualFold(meta.DiscType, "BDMV") && !strings.EqualFold(meta.DiscType, "DVD") {
		return nil, nil
	}
	if strings.TrimSpace(tmpRoot) == "" {
		return nil, nil
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return nil, fmt.Errorf("screenshots: %w", err)
	}
	path := paths.BDMVSummaryPath(tmpDir, paths.PrimaryBDMVPlaylist(meta))
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("screenshots: read BDMV summary: %w", err)
	}
	summary, files, _ := discparse.SplitBDInfoReport(string(payload))
	return discparse.ParseBDInfoSummary(summary, files, meta.SourcePath), nil
}

func parseFPS(info *discparse.BDInfo) float64 {
	if info == nil || len(info.Video) == 0 {
		return 0
	}
	fps := strings.TrimSpace(info.Video[0].FPS)
	fps = strings.TrimSuffix(fps, "fps")
	return parseFloat(fps)
}

func selectBDMVFile(ctx context.Context, root string, info *discparse.BDInfo) (string, error) {
	if info == nil || len(info.Files) == 0 {
		return "", errors.New("screenshots: bdinfo files missing")
	}

	longest := ""
	longestSeconds := -1.0
	for _, file := range info.Files {
		seconds := parseDurationSeconds(file.Length)
		if seconds > longestSeconds {
			longestSeconds = seconds
			longest = file.File
		}
	}
	if longest == "" {
		return "", errors.New("screenshots: no bdinfo file selected")
	}

	var found string
	walkErr := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("context canceled: %w", ctx.Err())
		default:
		}
		if entry.IsDir() {
			return nil
		}
		if strings.EqualFold(entry.Name(), longest) {
			found = path
			return errFound
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, context.Canceled) && !errors.Is(walkErr, errFound) {
		return "", fmt.Errorf("screenshots: scan bdinfo files: %w", walkErr)
	}
	if found == "" {
		return "", errors.New("screenshots: bdinfo file not found")
	}
	return found, nil
}

func selectDVDVOB(ctx context.Context, root string) (string, error) {
	videoTS, err := findVideoTS(ctx, root)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(videoTS)
	if err != nil {
		return "", fmt.Errorf("screenshots: read VIDEO_TS directory: %w", err)
	}

	vobSizes := map[string]int64{}
	vobPaths := map[string][]string{}
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("context canceled: %w", ctx.Err())
		default:
		}
		name := entry.Name()
		upper := strings.ToUpper(name)
		if !strings.HasPrefix(upper, "VTS_") || !strings.HasSuffix(upper, ".VOB") {
			continue
		}
		set := strings.TrimPrefix(strings.TrimSuffix(upper, ".VOB"), "VTS_")
		set = strings.SplitN(set, "_", 2)[0]
		info, err := entry.Info()
		if err != nil {
			continue
		}
		vobSizes[set] += info.Size()
		vobPaths[set] = append(vobPaths[set], filepath.Join(videoTS, name))
	}

	bestSet := ""
	var bestSize int64
	for set, size := range vobSizes {
		if size > bestSize {
			bestSize = size
			bestSet = set
		}
	}
	if bestSet == "" {
		return "", errors.New("screenshots: no dvd vob found")
	}

	paths := vobPaths[bestSet]
	if len(paths) == 0 {
		return "", errors.New("screenshots: no dvd vob files for set")
	}
	return paths[0], nil
}

func findVideoTS(ctx context.Context, root string) (string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("screenshots: stat DVD root: %w", err)
	}
	if info.IsDir() {
		if strings.EqualFold(filepath.Base(root), "VIDEO_TS") {
			return root, nil
		}
		candidate := filepath.Join(root, "VIDEO_TS")
		if stat, err := os.Stat(candidate); err == nil && stat.IsDir() {
			return candidate, nil
		}
	}

	var found string
	walkErr := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("context canceled: %w", ctx.Err())
		default:
		}
		if entry.IsDir() && strings.EqualFold(entry.Name(), "VIDEO_TS") {
			found = path
			return errFound
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, context.Canceled) && !errors.Is(walkErr, errFound) {
		return "", fmt.Errorf("screenshots: scan VIDEO_TS: %w", walkErr)
	}
	if found == "" {
		return "", errors.New("screenshots: VIDEO_TS not found")
	}
	return found, nil
}

var errFound = errors.New("found")

func buildScreenshotSelections(count int, durationSeconds float64, frameRate float64, meta api.PreparedMetadata) []api.ScreenshotSelection {
	if count <= 0 || durationSeconds <= 0 || frameRate <= 0 {
		return nil
	}
	totalFrames := int(durationSeconds * frameRate)
	startFrame := int(float64(totalFrames) * 0.05)
	if strings.EqualFold(meta.MediaInfoCategory, "TV") {
		startFrame = int(float64(totalFrames) * 0.10)
	}
	endFrame := int(float64(totalFrames) * 0.90)
	maxStart := int(float64(totalFrames) * 0.40)
	if startFrame > maxStart {
		startFrame = maxStart
	}
	usable := endFrame - startFrame
	interval := usable
	if count > 1 {
		interval = usable / count
	}

	selections := make([]api.ScreenshotSelection, 0, count)
	for i := 0; i < count; i++ {
		frame := startFrame + (i * interval)
		timestamp := float64(frame) / frameRate
		selections = append(selections, api.ScreenshotSelection{
			Index:            i,
			TimestampSeconds: timestamp,
			Frame:            frame,
			Source:           "auto",
		})
	}
	return selections
}

func buildManualFrameSelections(frames []int, frameRate float64) []api.ScreenshotSelection {
	if len(frames) == 0 || frameRate <= 0 {
		return nil
	}
	selections := make([]api.ScreenshotSelection, 0, len(frames))
	for _, frame := range frames {
		if frame <= 0 {
			continue
		}
		selections = append(selections, api.ScreenshotSelection{
			Index:            len(selections),
			TimestampSeconds: float64(frame) / frameRate,
			Frame:            frame,
			Source:           "manual",
		})
	}
	return selections
}

func sanitizeFilename(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "screens"
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_' || r == '.':
			return r
		default:
			return '_'
		}
	}, trimmed)
}

func shouldTonemap(meta api.PreparedMetadata, cfg config.Config) bool {
	if !cfg.ScreenshotHandling.ToneMap {
		return false
	}
	return strings.Contains(strings.ToUpper(meta.HDR), "HDR") || strings.Contains(strings.ToUpper(meta.HDR), "DV")
}

func shouldUseLibplacebo(meta api.PreparedMetadata, cfg config.Config) bool {
	if !cfg.ScreenshotHandling.UseLibplacebo {
		return false
	}
	if strings.TrimSpace(meta.DiscType) != "" {
		return false
	}
	return true
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
