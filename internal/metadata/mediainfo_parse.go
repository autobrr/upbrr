// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/pkg/api"
)

type mediaInfoDoc struct {
	Media struct {
		Track []map[string]any `json:"track"`
	} `json:"media"`
}

var canonicalResolutionWidths = []int{3840, 2560, 1920, 1280, 1024, 854, 720, 15360, 7680, 0}
var canonicalResolutionHeights = []int{2160, 1440, 1080, 720, 576, 540, 480, 8640, 4320, 0}

func loadMediaInfoDoc(path string) (mediaInfoDoc, error) {
	var doc mediaInfoDoc
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return doc, nil
	}
	payload, err := os.ReadFile(trimmed)
	if err != nil {
		return doc, fmt.Errorf("metadata: read mediainfo json: %w", err)
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		return doc, fmt.Errorf("metadata: parse mediainfo json: %w", err)
	}
	return doc, nil
}

func splitMediaInfoTracks(doc mediaInfoDoc) (general []map[string]any, video []map[string]any, audio []map[string]any) {
	if len(doc.Media.Track) == 0 {
		return nil, nil, nil
	}
	for _, track := range doc.Media.Track {
		trackType := strings.ToLower(trackString(track, "@type"))
		switch trackType {
		case "general":
			general = append(general, track)
		case "video":
			video = append(video, track)
		case "audio":
			audio = append(audio, track)
		}
	}
	return general, video, audio
}

func trackString(track map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := track[key]; ok {
			trimmed := strings.TrimSpace(fmtSprint(value))
			if trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func fmtSprint(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprintf("%v", value)
}

var widthRegex = regexp.MustCompile(`(?i)Width\s*:\s*(\d+)`)
var heightRegex = regexp.MustCompile(`(?i)Height\s*:\s*(\d+)`)
var frameRateRegex = regexp.MustCompile(`(?i)Frame\s*rate\s*:\s*([\d.]+)`)
var scanTypeRegex = regexp.MustCompile(`(?i)Scan\s*type\s*:\s*([^\r\n]+)`)
var interlacedResolutionHintRegex = regexp.MustCompile(`(?i)\b(1080i|576i|480i)\b`)
var numericRegex = regexp.MustCompile(`\d+(?:\.\d+)?`)

func extractDVDMediaInfo(meta preparationstate.State) api.DVDMediaInfo {
	if !strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		return api.DVDMediaInfo{}
	}

	var doc mediaInfoDoc
	if strings.TrimSpace(meta.DVDVOBMediaInfoJSON) != "" {
		if parsed, err := loadMediaInfoDocFromJSONPayload(meta.DVDVOBMediaInfoJSON); err == nil {
			doc = parsed
		}
	}
	if len(doc.Media.Track) == 0 {
		if parsed, err := loadMediaInfoDoc(meta.MediaInfoJSONPath); err == nil {
			doc = parsed
		}
	}

	_, videoTracks, _ := splitMediaInfoTracks(doc)
	videoTrack := map[string]any{}
	if len(videoTracks) > 0 {
		videoTrack = videoTracks[0]
	}

	width := trackNumericInt(videoTrack, "Width")
	height := trackNumericInt(videoTrack, "Height")
	mediaInfoText := metautil.FirstNonEmptyTrimmed(strings.TrimSpace(meta.DVDVOBMediaInfoText), readFileIfExists(meta.MediaInfoTextPath))
	if (width == 0 || height == 0) && mediaInfoText != "" {
		if width == 0 {
			width = regexInt(widthRegex, mediaInfoText)
		}
		if height == 0 {
			height = regexInt(heightRegex, mediaInfoText)
		}
	}

	frameRate := strings.TrimSpace(trackString(videoTrack, "FrameRate", "FrameRate_Original", "FrameRate_Num"))
	if frameRate == "" || frameRate == "0" {
		if frameRateText := regexString(frameRateRegex, mediaInfoText); frameRateText != "" {
			frameRate = frameRateText
		}
	}
	if frameRate == "" {
		frameRate = "24.000"
	}

	hfr := false
	if parsedRate, err := strconv.ParseFloat(firstNumericTokenString(frameRate), 64); err == nil && int(parsedRate) > 30 {
		hfr = true
	}

	scanType := strings.TrimSpace(trackString(videoTrack, "ScanType"))
	if scanType == "" && mediaInfoText != "" {
		if scanText := regexString(scanTypeRegex, mediaInfoText); scanText != "" {
			scanType = strings.TrimSpace(scanText)
		}
	}
	scan := normalizeDVDScan(scanType, meta.SourcePath)

	closestWidth, closestHeight, resolution := snapAndMapResolution(width, height, scan, meta.Release.Resolution)

	return api.DVDMediaInfo{
		Width:         closestWidth,
		Height:        closestHeight,
		FrameRate:     frameRate,
		ScanType:      scan,
		Resolution:    resolution,
		HighFrameRate: hfr,
	}
}

func loadMediaInfoDocFromJSONPayload(payload string) (mediaInfoDoc, error) {
	var doc mediaInfoDoc
	if strings.TrimSpace(payload) == "" {
		return doc, nil
	}
	if err := json.Unmarshal([]byte(payload), &doc); err != nil {
		return doc, fmt.Errorf("metadata: parse mediainfo payload: %w", err)
	}
	return doc, nil
}

func readFileIfExists(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	payload, err := os.ReadFile(trimmed)
	if err != nil {
		return ""
	}
	return string(payload)
}

func trackNumericInt(track map[string]any, keys ...string) int {
	value := strings.TrimSpace(trackString(track, keys...))
	if value == "" {
		return 0
	}
	numeric := firstNumericTokenString(value)
	if numeric == "" {
		return 0
	}
	parsed, err := strconv.ParseFloat(numeric, 64)
	if err != nil {
		return 0
	}
	return int(parsed)
}

func firstNumericTokenString(value string) string {
	match := numericRegex.FindString(strings.TrimSpace(value))
	return strings.TrimSpace(match)
}

func regexString(pattern *regexp.Regexp, value string) string {
	matches := pattern.FindStringSubmatch(value)
	if len(matches) < 2 {
		return ""
	}
	return strings.TrimSpace(matches[1])
}

func regexInt(pattern *regexp.Regexp, value string) int {
	numeric := regexString(pattern, value)
	if numeric == "" {
		return 0
	}
	parsed, err := strconv.Atoi(numeric)
	if err != nil {
		return 0
	}
	return parsed
}

func normalizeDVDScan(scanType string, sourceHint string) string {
	scan := strings.TrimSpace(scanType)
	if strings.EqualFold(scan, "Progressive") {
		return "p"
	}
	if strings.EqualFold(scan, "Interlaced") {
		return "i"
	}
	if interlacedResolutionHintRegex.MatchString(sourceHint) {
		return "i"
	}
	return "p"
}

func closestDVDValue(values []int, input int) int {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int{}, values...)
	for i := range sorted {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j] < sorted[i] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	result := 0
	for _, value := range sorted {
		if input > value {
			continue
		}
		result = value
		break
	}
	return result
}

func mapDVDResolution(res string, guessed string, width int, scan string) string {
	resMap := map[string]string{
		"3840x2160p":  "2160p",
		"2160p":       "2160p",
		"2560x1440p":  "1440p",
		"1440p":       "1440p",
		"1920x1080p":  "1080p",
		"1080p":       "1080p",
		"1920x1080i":  "1080i",
		"1080i":       "1080i",
		"1280x720p":   "720p",
		"720p":        "720p",
		"1280x540p":   "720p",
		"1280x576p":   "720p",
		"1024x576p":   "576p",
		"576p":        "576p",
		"1024x576i":   "576i",
		"576i":        "576i",
		"960x540p":    "540p",
		"540p":        "540p",
		"960x540i":    "540i",
		"540i":        "540i",
		"854x480p":    "480p",
		"480p":        "480p",
		"854x480i":    "480i",
		"480i":        "480i",
		"720x576p":    "576p",
		"720x576i":    "576i",
		"720x480p":    "480p",
		"720x480i":    "480i",
		"15360x8640p": "8640p",
		"8640p":       "8640p",
		"7680x4320p":  "4320p",
		"4320p":       "4320p",
		"OTHER":       "OTHER",
	}

	if resolution, ok := resMap[res]; ok {
		return resolution
	}

	widthMap := map[string]string{
		"3840p":  "2160p",
		"2560p":  "1550p",
		"1920p":  "1080p",
		"1920i":  "1080i",
		"1280p":  "720p",
		"1024p":  "576p",
		"1024i":  "576i",
		"960p":   "540p",
		"960i":   "540i",
		"854p":   "480p",
		"854i":   "480i",
		"720p":   "576p",
		"720i":   "576i",
		"15360p": "4320p",
		"OTHERp": "OTHER",
	}

	resolution := strings.TrimSpace(guessed)
	if _, ok := resMap[resolution]; !ok {
		if mapped, ok := widthMap[fmt.Sprintf("%d%s", width, scan)]; ok {
			resolution = mapped
		} else {
			resolution = "OTHER"
		}
	}

	if _, ok := resMap[resolution]; !ok {
		return "OTHER"
	}
	return resolution
}

func snapAndMapResolution(width int, height int, scan string, guessed string) (int, int, string) {
	closestWidth := closestDVDValue(canonicalResolutionWidths, width)
	closestHeight := closestDVDValue(canonicalResolutionHeights, height)
	resKey := fmt.Sprintf("%dx%d%s", closestWidth, closestHeight, scan)
	resolution := mapDVDResolution(resKey, guessed, closestWidth, scan)
	return closestWidth, closestHeight, resolution
}

// resolutionFromMediaInfo derives a standard resolution string from a MediaInfo
// document for non-DVD content. It mirrors the Python get_resolution() logic:
// read Width/Height from the video track, snap each using the same Python
// closest() helper semantics, compose a WxHscan key, and map it through the
// same resolution table used for DVD content. Returns "" when
// the video track is absent or either dimension is zero.
func resolutionFromMediaInfo(doc mediaInfoDoc, sourcePath string) string {
	_, videoTracks, _ := splitMediaInfoTracks(doc)
	if len(videoTracks) == 0 {
		return ""
	}
	track := videoTracks[0]

	width := trackNumericInt(track, "Width")
	height := trackNumericInt(track, "Height")
	if width == 0 || height == 0 {
		return ""
	}

	scanType := strings.TrimSpace(trackString(track, "ScanType"))
	scan := normalizeDVDScan(scanType, sourcePath)

	_, _, resolution := snapAndMapResolution(width, height, scan, "")
	if resolution == "OTHER" {
		return ""
	}
	return resolution
}
