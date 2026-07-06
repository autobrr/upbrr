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
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata/discparse"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/internal/redaction"
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
	Segments        []videoSegment
}

// videoSegment maps a global disc timestamp window onto one concrete ffmpeg
// input file. DVD title sets use one segment per ordered VOB part.
type videoSegment struct {
	SourcePath      string
	StartSeconds    float64
	DurationSeconds float64
}

// segmentCandidate records one concrete ffmpeg input attempt for a whole-title
// timestamp. DVD candidates can include later VOB parts as fallbacks when an
// early menu segment produces no decodable frame.
type segmentCandidate struct {
	SourcePath    string
	Timestamp     float64
	SegmentIndex  int
	FallbackIndex int
}

type mediaInfoDoc struct {
	Media struct {
		Track []map[string]any `json:"track"`
	} `json:"media"`
}

var durationTokenPattern = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(milliseconds?|msecs?|ms|hours?|hrs?|h|minutes?|mins?|min|seconds?|secs?|sec|s)\b`)

// resolveVideoInfo selects timing metadata and the ffmpeg inputs used for
// screenshot planning/capture. DVD releases keep ordered title-set segments so
// global screenshot times can be captured from the matching VOB part.
func resolveVideoInfo(ctx context.Context, meta api.PreparedMetadata, tmpRoot string, logger api.Logger) (videoInfo, error) {
	logger = screenshotLogger(logger)
	info := videoInfo{}

	basePath := strings.TrimSpace(meta.VideoPath)
	if basePath == "" {
		basePath = strings.TrimSpace(meta.SourcePath)
	}
	if basePath == "" {
		return info, errors.New("screenshots: source path required")
	}

	logger.Tracef(
		"screenshots: video info input kind=%s disc=%s source_path=%s video_path=%s mediainfo_present=%t selected_playlists=%d tvpack=%t",
		screenshotSourceKind(meta),
		screenshotLogField(meta.DiscType),
		meta.SourcePath,
		meta.VideoPath,
		strings.TrimSpace(meta.MediaInfoJSONPath) != "",
		len(meta.SelectedBDMVPlaylists),
		meta.TVPack,
	)
	doc, mediaInfoErr := loadMediaInfoDoc(meta.MediaInfoJSONPath)
	if mediaInfoErr != nil {
		logger.Debugf("screenshots: mediainfo timing unavailable err=%s", redaction.RedactValue(mediaInfoErr.Error(), nil))
	}
	info.DurationSeconds = mediaInfoDurationSeconds(doc)
	info.FrameRate = mediaInfoFrameRate(doc)
	info.Width, info.Height, info.WidthScale, info.HeightScale = mediaInfoVideoGeometry(doc)
	if info.FrameRate <= 0 {
		info.FrameRate = 24.0
	}
	logger.Tracef(
		"screenshots: mediainfo timing duration_seconds=%.3f frame_rate=%.3f width=%d height=%d width_scale=%.3f height_scale=%.3f",
		info.DurationSeconds,
		info.FrameRate,
		info.Width,
		info.Height,
		info.WidthScale,
		info.HeightScale,
	)

	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		if filePath, ok, err := selectBDMVFileFromMetadata(ctx, meta); err != nil {
			return info, err
		} else if ok {
			info.SourcePath = filePath
			logger.Tracef("screenshots: BDMV source selected method=metadata path=%s", filePath)
		}

		logger.Tracef("screenshots: BDMV summary lookup tmp_root_present=%t playlist=%s", strings.TrimSpace(tmpRoot) != "", paths.PrimaryBDMVPlaylist(meta))
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
			if info.SourcePath == "" {
				filePath, err := selectBDMVFile(ctx, meta.SourcePath, bdinfo)
				if err == nil {
					info.SourcePath = filePath
					logger.Tracef("screenshots: BDMV source selected method=bdinfo path=%s", filePath)
				} else {
					logger.Debugf("screenshots: BDMV source selection unavailable err=%s", redaction.RedactValue(err.Error(), nil))
				}
			}
			logger.Tracef("screenshots: BDMV summary applied duration_seconds=%.3f frame_rate=%.3f files=%d", info.DurationSeconds, info.FrameRate, len(bdinfo.Files))
		} else {
			logger.Tracef("screenshots: BDMV summary not available")
		}
	}

	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		logger.Tracef("screenshots: DVD source selection root=%s", meta.SourcePath)
		vobs, err := selectDVDVOBs(ctx, meta.SourcePath)
		if err != nil {
			return info, err
		}
		info.SourcePath = vobs[0].path
		info.Segments = buildVideoSegments(vobs, info.DurationSeconds)
		logger.Tracef("screenshots: DVD source selected path=%s segments=%d", info.SourcePath, len(info.Segments))
	}

	if info.SourcePath == "" {
		info.SourcePath = basePath
		logger.Tracef("screenshots: video source selected method=base path=%s", info.SourcePath)
	}
	logger.Debugf(
		"screenshots: video info resolved kind=%s disc=%s duration_seconds=%.3f frame_rate=%.3f selected_path=%s",
		screenshotSourceKind(meta),
		screenshotLogField(meta.DiscType),
		info.DurationSeconds,
		info.FrameRate,
		info.SourcePath,
	)

	return info, nil
}

// resolveSegmentTimestamp converts a whole-title timestamp into the concrete
// segment file and local timestamp ffmpeg should seek within that file.
func resolveSegmentTimestamp(info videoInfo, timestamp float64) (string, float64) {
	candidates := resolveSegmentCandidates(info, timestamp)
	if len(candidates) == 0 {
		return info.SourcePath, timestamp
	}
	return candidates[0].SourcePath, candidates[0].Timestamp
}

// resolveSegmentCandidates returns the primary input segment for a whole-title
// timestamp plus later ordered segments that can be tried if ffmpeg emits no
// frame from the primary segment.
func resolveSegmentCandidates(info videoInfo, timestamp float64) []segmentCandidate {
	if len(info.Segments) == 0 {
		return []segmentCandidate{{SourcePath: info.SourcePath, Timestamp: timestamp, SegmentIndex: -1}}
	}

	primaryIndex := 0
	localTimestamp := timestamp
	for idx, segment := range info.Segments {
		if segment.SourcePath == "" || segment.DurationSeconds <= 0 {
			continue
		}
		if timestamp <= 0 {
			primaryIndex = idx
			localTimestamp = 0
			break
		}
		if timestamp < segment.StartSeconds+segment.DurationSeconds {
			primaryIndex = idx
			localTimestamp = timestamp - segment.StartSeconds
			if localTimestamp < 0 {
				localTimestamp = 0
			}
			break
		}
		primaryIndex = idx + 1
	}
	if primaryIndex >= len(info.Segments) {
		primaryIndex = len(info.Segments) - 1
		last := info.Segments[primaryIndex]
		localTimestamp = last.DurationSeconds
		if localTimestamp > 0.5 {
			localTimestamp -= 0.5
		}
	}
	if localTimestamp < 0 {
		localTimestamp = 0
	}

	candidates := make([]segmentCandidate, 0, len(info.Segments)-primaryIndex)
	for idx := primaryIndex; idx < len(info.Segments); idx++ {
		segment := info.Segments[idx]
		if segment.SourcePath == "" {
			continue
		}
		candidateTimestamp := localTimestamp
		if idx != primaryIndex {
			candidateTimestamp = segmentFallbackTimestamp(segment)
		}
		candidates = append(candidates, segmentCandidate{
			SourcePath:    segment.SourcePath,
			Timestamp:     candidateTimestamp,
			SegmentIndex:  idx,
			FallbackIndex: len(candidates),
		})
	}
	if len(candidates) == 0 {
		return []segmentCandidate{{SourcePath: info.SourcePath, Timestamp: timestamp, SegmentIndex: -1}}
	}
	return candidates
}

func segmentFallbackTimestamp(segment videoSegment) float64 {
	switch {
	case segment.DurationSeconds <= 0:
		return 0.5
	case segment.DurationSeconds <= 1:
		return segment.DurationSeconds / 2
	default:
		return 0.5
	}
}

// resolveVideoSource applies the same input-file preference as resolveVideoInfo
// when callers only need the ffmpeg source path, such as preview generation.
func resolveVideoSource(ctx context.Context, meta api.PreparedMetadata, tmpRoot string, logger api.Logger) (string, error) {
	logger = screenshotLogger(logger)
	basePath := strings.TrimSpace(meta.VideoPath)
	if basePath == "" {
		basePath = strings.TrimSpace(meta.SourcePath)
	}
	if basePath == "" {
		return "", errors.New("screenshots: source path required")
	}
	logger.Tracef(
		"screenshots: video source input kind=%s disc=%s source_path=%s video_path=%s selected_playlists=%d",
		screenshotSourceKind(meta),
		screenshotLogField(meta.DiscType),
		meta.SourcePath,
		meta.VideoPath,
		len(meta.SelectedBDMVPlaylists),
	)

	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		if filePath, ok, err := selectBDMVFileFromMetadata(ctx, meta); err != nil {
			return "", err
		} else if ok {
			logger.Tracef("screenshots: video source selected method=bdmv_metadata path=%s", filePath)
			return filePath, nil
		}

		logger.Tracef("screenshots: video source BDMV summary lookup tmp_root_present=%t playlist=%s", strings.TrimSpace(tmpRoot) != "", paths.PrimaryBDMVPlaylist(meta))
		bdinfo, err := loadBDInfo(tmpRoot, meta)
		if err != nil {
			return "", err
		}
		if bdinfo != nil {
			filePath, err := selectBDMVFile(ctx, meta.SourcePath, bdinfo)
			if err != nil {
				return "", err
			}
			logger.Tracef("screenshots: video source selected method=bdmv_bdinfo path=%s", filePath)
			return filePath, nil
		}
	}

	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		logger.Tracef("screenshots: video source DVD selection root=%s", meta.SourcePath)
		vob, err := selectDVDVOB(ctx, meta.SourcePath)
		if err != nil {
			return "", err
		}
		logger.Tracef("screenshots: video source selected method=dvd_vob path=%s", vob)
		return vob, nil
	}

	logger.Tracef("screenshots: video source selected method=base path=%s", basePath)
	return basePath, nil
}

// selectBDMVFileFromMetadata returns a concrete stream path when the prepared
// metadata already identifies the selected BDMV item or resolved video file.
func selectBDMVFileFromMetadata(ctx context.Context, meta api.PreparedMetadata) (string, bool, error) {
	if fileName := largestSelectedBDMVPlaylistItem(meta.SelectedBDMVPlaylists); fileName != "" {
		if videoPath := strings.TrimSpace(meta.VideoPath); videoPath != "" && strings.EqualFold(filepath.Base(videoPath), fileName) {
			return videoPath, true, nil
		}

		filePath, err := findBDMVFile(ctx, meta.SourcePath, fileName)
		if err != nil {
			return "", false, err
		}
		return filePath, true, nil
	}

	if videoPath := strings.TrimSpace(meta.VideoPath); videoPath != "" {
		return videoPath, true, nil
	}

	return "", false, nil
}

func largestSelectedBDMVPlaylistItem(playlists []api.PlaylistInfo) string {
	largestFile := ""
	largestSize := int64(-1)
	for _, playlist := range playlists {
		for _, item := range playlist.Items {
			fileName := strings.TrimSpace(item.File)
			if fileName == "" || item.Size <= largestSize {
				continue
			}
			largestFile = fileName
			largestSize = item.Size
		}
	}
	if largestSize <= 0 {
		return ""
	}
	return largestFile
}

func findBDMVFile(ctx context.Context, root string, fileName string) (string, error) {
	trimmedRoot := strings.TrimSpace(root)
	if trimmedRoot == "" {
		return "", errors.New("screenshots: BDMV root required")
	}
	trimmedFile := strings.TrimSpace(fileName)
	if trimmedFile == "" {
		return "", errors.New("screenshots: BDMV file required")
	}

	var found string
	walkErr := filepath.WalkDir(trimmedRoot, func(path string, entry os.DirEntry, err error) error {
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
		if strings.EqualFold(entry.Name(), trimmedFile) {
			found = path
			return errFound
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, context.Canceled) && !errors.Is(walkErr, errFound) {
		return "", fmt.Errorf("screenshots: scan BDMV files: %w", walkErr)
	}
	if found == "" {
		return "", errors.New("screenshots: BDMV file not found")
	}
	return found, nil
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
	if seconds := parseDurationTokens(trimmed); seconds > 0 {
		return seconds
	}
	seconds := parseFloat(trimmed)
	if seconds <= 0 {
		return 0
	}
	return seconds
}

func parseDurationTokens(value string) float64 {
	var total float64
	for _, match := range durationTokenPattern.FindAllStringSubmatch(value, -1) {
		if len(match) != 3 {
			continue
		}
		amount := parseFloat(match[1])
		if amount <= 0 {
			continue
		}
		switch strings.ToLower(match[2]) {
		case "h", "hr", "hrs", "hour", "hours":
			total += amount * 3600
		case "m", "min", "mins", "minute", "minutes":
			total += amount * 60
		case "s", "sec", "secs", "second", "seconds":
			total += amount
		case "ms", "msec", "msecs", "millisecond", "milliseconds":
			total += amount / 1000
		}
	}
	return total
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

	return findBDMVFile(ctx, root, longest)
}

func selectDVDVOB(ctx context.Context, root string) (string, error) {
	vobs, err := selectDVDVOBs(ctx, root)
	if err != nil {
		return "", err
	}
	return vobs[0].path, nil
}

// selectDVDVOBs returns the largest DVD title set's VOBs in numeric playback
// order, including VTS_nn_0.VOB when present.
func selectDVDVOBs(ctx context.Context, root string) ([]dvdTitleVOB, error) {
	videoTS, err := findVideoTS(ctx, root)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(videoTS)
	if err != nil {
		return nil, fmt.Errorf("screenshots: read VIDEO_TS directory: %w", err)
	}

	contentSizes := map[string]int64{}
	vobPaths := map[string][]dvdTitleVOB{}
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context canceled: %w", ctx.Err())
		default:
		}
		name := entry.Name()
		upper := strings.ToUpper(name)
		if !strings.HasPrefix(upper, "VTS_") || !strings.HasSuffix(upper, ".VOB") {
			continue
		}
		set, index, ok := parseDVDVOBName(upper)
		if !ok || index < 0 {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if index > 0 {
			contentSizes[set] += info.Size()
		}
		vobPaths[set] = append(vobPaths[set], dvdTitleVOB{
			path:  filepath.Join(videoTS, name),
			index: index,
			size:  info.Size(),
		})
	}

	bestSet := ""
	var bestSize int64
	for set, size := range contentSizes {
		if size > bestSize {
			bestSize = size
			bestSet = set
		}
	}
	if bestSet == "" {
		for set, paths := range vobPaths {
			var size int64
			for _, path := range paths {
				size += path.size
			}
			if size > bestSize {
				bestSize = size
				bestSet = set
			}
		}
	}
	if bestSet == "" {
		return nil, errors.New("screenshots: no dvd vob found")
	}

	paths := vobPaths[bestSet]
	if len(paths) == 0 {
		return nil, errors.New("screenshots: no dvd vob files for set")
	}
	sort.Slice(paths, func(i, j int) bool {
		if paths[i].index != paths[j].index {
			return paths[i].index < paths[j].index
		}
		if paths[i].size != paths[j].size {
			return paths[i].size > paths[j].size
		}
		return paths[i].path < paths[j].path
	})
	return paths, nil
}

func parseDVDVOBName(name string) (string, int, bool) {
	trimmed := strings.TrimSuffix(strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(name)), "VTS_"), ".VOB")
	parts := strings.Split(trimmed, "_")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", 0, false
	}
	index, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, false
	}
	return parts[0], index, true
}

// dvdTitleVOB records the parsed VTS set/index and size used to select and
// order DVD title segments.
type dvdTitleVOB struct {
	path  string
	index int
	size  int64
}

// buildVideoSegments assigns a whole-title runtime window to each ordered DVD
// VOB, using file size as the best available proxy for per-part duration.
func buildVideoSegments(vobs []dvdTitleVOB, totalDuration float64) []videoSegment {
	if len(vobs) == 0 {
		return nil
	}
	segments := make([]videoSegment, 0, len(vobs))
	if totalDuration <= 0 {
		for _, vob := range vobs {
			segments = append(segments, videoSegment{SourcePath: vob.path})
		}
		return segments
	}
	var totalSize int64
	for _, vob := range vobs {
		if vob.size > 0 {
			totalSize += vob.size
		}
	}
	if totalSize <= 0 {
		each := totalDuration / float64(len(vobs))
		start := 0.0
		for _, vob := range vobs {
			segments = append(segments, videoSegment{SourcePath: vob.path, StartSeconds: start, DurationSeconds: each})
			start += each
		}
		return segments
	}
	start := 0.0
	for _, vob := range vobs {
		duration := totalDuration * (float64(vob.size) / float64(totalSize))
		segments = append(segments, videoSegment{SourcePath: vob.path, StartSeconds: start, DurationSeconds: duration})
		start += duration
	}
	return segments
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
	for i := range count {
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

func screenshotLogger(logger api.Logger) api.Logger {
	if logger == nil {
		return api.NopLogger{}
	}
	return logger
}

// screenshotSourceKind classifies the prepared metadata for diagnostic logs
// without changing screenshot selection behavior.
func screenshotSourceKind(meta api.PreparedMetadata) string {
	discType := strings.TrimSpace(meta.DiscType)
	if discType != "" {
		return "disc_" + strings.ToLower(sanitizeFilename(discType))
	}
	if meta.TVPack {
		return "season_pack"
	}
	if strings.EqualFold(strings.TrimSpace(meta.MediaInfoCategory), "TV") {
		if meta.EpisodeInt <= 0 && meta.Release.Episode <= 0 {
			return "season_pack"
		}
		return "episode"
	}
	videoPath := strings.TrimSpace(meta.VideoPath)
	sourcePath := strings.TrimSpace(meta.SourcePath)
	if videoPath != "" && sourcePath != "" && !pathutil.SamePath(videoPath, sourcePath) {
		return "pack_item"
	}
	return "single_file"
}

func screenshotLogField(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "none"
	}
	return sanitizeFilename(trimmed)
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
