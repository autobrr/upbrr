// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package additional

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

var resolutionOrder = map[string]int{
	"480i":  1,
	"480p":  2,
	"576i":  3,
	"576p":  4,
	"720p":  5,
	"1080i": 6,
	"1080p": 7,
	"1440p": 8,
	"2160p": 9,
	"4320p": 10,
	"8640p": 11,
}

var a4kWebRipRegex = regexp.MustCompile(`(?i)(^|[^[:alnum:]])web-?rip([^[:alnum:]]|$)`)

const (
	a4kMovieMinBitrateMbps = 10.0
	a4kTVMinBitrateMbps    = 6.0
)

// checkA4KRequirements rejects WEBRips outright and enforces A4K's minimum
// encode bitrate for movies and TV episodes. Full-disc uploads are exempt
// from the bitrate floor since raw discs are never encode-bitrate limited.
func checkA4KRequirements(ctx context.Context, meta api.PreparedMetadata, _ api.Logger) Result {
	select {
	case <-ctx.Done():
		return Fail(fmt.Errorf("context canceled: %w", ctx.Err()).Error())
	default:
	}

	if isA4KWebRip(meta) {
		return Fail("WEBRips are not allowed at A4K.")
	}

	if reason, ok := checkA4KBitrate(meta); !ok {
		return Fail(reason)
	}

	return Pass()
}

func isA4KWebRip(meta api.PreparedMetadata) bool {
	if resolveType(meta) == "WEBRIP" {
		return true
	}
	if a4kWebRipRegex.MatchString(meta.Source) {
		return true
	}
	return a4kWebRipRegex.MatchString(meta.ReleaseName)
}

func checkA4KBitrate(meta api.PreparedMetadata) (string, bool) {
	if isDiscType(meta.DiscType) {
		return "", true
	}

	bitrateMbps, ok := mediaInfoVideoBitrateMbps(meta.MediaInfoJSONPath)
	if !ok {
		return "", true
	}

	minBitrate := a4kMovieMinBitrateMbps
	label := "Movie"
	if resolveCategory(meta) == "tv" {
		minBitrate = a4kTVMinBitrateMbps
		label = "TV Episode"
	}

	if bitrateMbps < minBitrate {
		return fmt.Sprintf("%s bitrate %.1f Mbps is below A4K's %.0f Mbps minimum.", label, bitrateMbps, minBitrate), false
	}
	return "", true
}

// mediaInfoVideoBitrateMbps reads the video track's bitrate from a MediaInfo
// JSON report. When MediaInfo didn't report a video track bitrate directly,
// it's approximated as the General track's OverallBitRate minus the summed
// bitrate of all audio tracks. Returns ok=false when neither is available or
// parseable, rather than failing the caller's rule.
func mediaInfoVideoBitrateMbps(path string) (float64, bool) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return 0, false
	}
	payload, err := os.ReadFile(trimmed)
	if err != nil {
		return 0, false
	}

	var doc struct {
		Media struct {
			Track []map[string]any `json:"track"`
		} `json:"media"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		return 0, false
	}

	var overallBitRate, videoBitRate string
	var audioBitRateSum float64
	for _, track := range doc.Media.Track {
		trackType := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", track["@type"])))
		switch trackType {
		case "general":
			if overallBitRate == "" {
				overallBitRate = fmt.Sprintf("%v", track["OverallBitRate"])
			}
		case "video":
			if videoBitRate == "" {
				videoBitRate = fmt.Sprintf("%v", track["BitRate"])
			}
		case "audio":
			if bps, ok := firstNumericFloat(fmt.Sprintf("%v", track["BitRate"])); ok {
				audioBitRateSum += bps
			}
		}
	}

	if bps, ok := firstNumericFloat(videoBitRate); ok {
		return bps / 1_000_000, true
	}

	overallBps, ok := firstNumericFloat(overallBitRate)
	if !ok {
		return 0, false
	}
	videoBps := overallBps - audioBitRateSum
	if videoBps <= 0 {
		return 0, false
	}
	return videoBps / 1_000_000, true
}

var mediaInfoNumericRegex = regexp.MustCompile(`\d+(?:\.\d+)?`)

func firstNumericFloat(value string) (float64, bool) {
	match := mediaInfoNumericRegex.FindString(strings.TrimSpace(value))
	if match == "" {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(match, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func resolveCategory(meta api.PreparedMetadata) string {
	if value := strings.ToLower(strings.TrimSpace(meta.ExternalIDs.Category)); value != "" {
		return value
	}
	if value := strings.ToLower(strings.TrimSpace(meta.MediaInfoCategory)); value != "" {
		return value
	}
	if meta.ExternalMetadata.TMDB != nil {
		if value := strings.ToLower(strings.TrimSpace(meta.ExternalMetadata.TMDB.Category)); value != "" {
			return value
		}
	}
	if value := strings.ToLower(strings.TrimSpace(meta.Release.Category)); value != "" {
		return value
	}
	return ""
}

func checkLUMEResolution(ctx context.Context, meta api.PreparedMetadata, _ api.Logger) Result {
	select {
	case <-ctx.Done():
		return Fail(fmt.Errorf("context canceled: %w", ctx.Err()).Error())
	default:
	}

	if isDiscType(meta.DiscType) {
		return Pass()
	}

	resolution := resolveResolution(meta)
	if resolution == "" {
		return Fail("LUME requires a known resolution")
	}
	if resolutionOrder[resolution] < resolutionOrder["720p"] {
		return Fail("LUME only allows SD releases when the content does not have a higher resolution release.")
	}
	return Pass()
}

func checkLUMERequirements(ctx context.Context, meta api.PreparedMetadata, logger api.Logger) Result {
	select {
	case <-ctx.Done():
		return Fail(fmt.Errorf("context canceled: %w", ctx.Err()).Error())
	default:
	}

	if !isDiscType(meta.DiscType) && !strings.EqualFold(strings.TrimSpace(meta.Container), "mkv") {
		return Fail("LUME only allows MKV containers for non-disc uploads.")
	}
	return checkLUMEResolution(ctx, meta, logger)
}

func checkBHDRequirements(ctx context.Context, meta api.PreparedMetadata, _ api.Logger) Result {
	select {
	case <-ctx.Done():
		return Fail(fmt.Errorf("context canceled: %w", ctx.Err()).Error())
	default:
	}

	switch resolveType(meta) {
	case "REMUX", "ENCODE", "WEBDL", "WEBRIP":
		container := strings.ToLower(strings.TrimSpace(meta.Container))
		if container != "" && container != "mkv" && container != "mp4" {
			return Fail(fmt.Sprintf("Container %q is not allowed for %s. Only MKV and MP4 are permitted.", meta.Container, resolveType(meta)))
		}
	}
	return Pass()
}

func checkBLUContainer(ctx context.Context, meta api.PreparedMetadata, _ api.Logger) Result {
	select {
	case <-ctx.Done():
		return Fail(fmt.Errorf("context canceled: %w", ctx.Err()).Error())
	default:
	}

	if isDiscType(meta.DiscType) {
		return Pass()
	}

	container := strings.ToLower(strings.TrimSpace(meta.Container))
	if container == "" {
		return Pass()
	}

	allowed := []string{"mkv"}
	typeValue := resolveType(meta)
	if typeValue == "HDTV" {
		allowed = append(allowed, "ts")
	}
	if (typeValue == "WEBDL" || typeValue == "HDTV") && isDolbyVisionOnly(meta) {
		allowed = append(allowed, "mp4")
	}
	if containsAny([]string{container}, allowed) {
		return Pass()
	}
	return Fail("BLU requires one of the following containers for this release: " + strings.ToUpper(strings.Join(allowed, ", ")))
}

func isDolbyVisionOnly(meta api.PreparedMetadata) bool {
	if meta.WebDV {
		return true
	}
	hdr := strings.ToUpper(strings.TrimSpace(meta.HDR))
	return strings.Contains(hdr, "DV") && !strings.Contains(hdr, "HDR")
}

func checkOTWGenres(ctx context.Context, meta api.PreparedMetadata, _ api.Logger) Result {
	select {
	case <-ctx.Done():
		return Fail(fmt.Errorf("context canceled: %w", ctx.Err()).Error())
	default:
	}

	genres := collectGenres(meta)
	if !containsAny(genres, []string{"animation", "family"}) {
		return Fail("Genre does not match Animation or Family for OTW.")
	}

	if isAdultContent(meta) {
		return Fail("Adult animation not allowed at OTW.")
	}

	if containsAny(genres, []string{"reality", "game show", "game-show", "reality tv", "reality television"}) {
		return Fail("Reality / Game Show content not allowed at OTW.")
	}

	typeValue := resolveType(meta)
	group := resolveGroup(meta)
	if group != "" && typeValue != "WEBDL" && !isDiscType(meta.DiscType) {
		restricted := map[string]bool{"CMRG": true, "EVO": true, "TERMINAL": true, "VISION": true}
		if restricted[strings.ToUpper(group)] {
			return Fail(fmt.Sprintf("Group %s is only allowed for raw type content at OTW", group))
		}
	}

	return Pass()
}

func checkSHRIRegion(ctx context.Context, meta api.PreparedMetadata, _ api.Logger) Result {
	select {
	case <-ctx.Done():
		return Fail(fmt.Errorf("context canceled: %w", ctx.Err()).Error())
	default:
	}

	if !strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") && !strings.EqualFold(strings.TrimSpace(meta.DiscType), "HDDVD") {
		return Pass()
	}
	if strings.TrimSpace(meta.Region) == "" {
		return Fail("Region required; skipping SHRI.")
	}
	return Pass()
}

func checkTTRSubtitleOnly(ctx context.Context, meta api.PreparedMetadata, _ api.Logger) Result {
	select {
	case <-ctx.Done():
		return Fail(fmt.Errorf("context canceled: %w", ctx.Err()).Error())
	default:
	}

	if !containsAny(normalizeStrings(meta.Release.Language), []string{"spanish", "es", "spa"}) {
		return Fail("TTR requires at least one Spanish audio or subtitle track.")
	}
	return Pass()
}

func checkULCXRules(ctx context.Context, meta api.PreparedMetadata, _ api.Logger) Result {
	select {
	case <-ctx.Done():
		return Fail(fmt.Errorf("context canceled: %w", ctx.Err()).Error())
	default:
	}

	keywords := collectKeywords(meta)
	if containsAny(keywords, []string{"concert"}) {
		return Fail("Concerts not allowed at ULCX.")
	}

	resolution := resolveResolution(meta)
	if strings.EqualFold(strings.TrimSpace(meta.VideoCodec), "HEVC") && resolution != "2160p" && !isAnimation(meta) && !isAnime(meta) {
		return Fail("This content might not fit HEVC rules for ULCX.")
	}

	typeValue := resolveType(meta)
	if (typeValue == "ENCODE" || typeValue == "HDTV") && resolutionOrder[resolution] < resolutionOrder["720p"] {
		return Fail("Encodes must be at least 720p resolution for ULCX.")
	}

	if typeValue == "DVDRIP" {
		return Fail("DVDRIPs are not allowed for ULCX.")
	}

	return Pass()
}

func resolveResolution(meta api.PreparedMetadata) string {
	resolution := strings.TrimSpace(meta.Release.Resolution)
	if resolution == "" {
		resolution = detectResolution(meta.ReleaseName)
	}
	return resolution
}

func detectResolution(value string) string {
	clean := strings.ToLower(value)
	for _, candidate := range []string{"8640p", "4320p", "2160p", "1440p", "1080p", "1080i", "720p", "576p", "576i", "480p", "480i"} {
		if strings.Contains(clean, candidate) {
			return candidate
		}
	}
	return ""
}

func resolveType(meta api.PreparedMetadata) string {
	typeValue := strings.ToUpper(strings.TrimSpace(meta.Type))
	if typeValue == "" {
		typeValue = strings.ToUpper(strings.TrimSpace(meta.Release.Type))
	}
	return typeValue
}

func resolveGroup(meta api.PreparedMetadata) string {
	if group := strings.TrimSpace(meta.Release.Group); group != "" {
		return group
	}
	return strings.TrimPrefix(strings.TrimSpace(meta.Tag), "-")
}

func isDiscType(value string) bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "BDMV", "DVD", "HDDVD":
		return true
	default:
		return false
	}
}

func isAdultContent(meta api.PreparedMetadata) bool {
	candidates := append([]string{}, collectGenres(meta)...)
	candidates = append(candidates, collectKeywords(meta)...)
	for _, token := range candidates {
		switch strings.ToLower(strings.TrimSpace(token)) {
		case "adult", "porn", "pornography", "xxx", "erotic":
			return true
		}
	}
	return false
}

func isAnime(meta api.PreparedMetadata) bool {
	if meta.ExternalMetadata.TMDB != nil && externalMetadataMatchesCurrentSource(meta) && meta.ExternalMetadata.TMDB.Anime {
		return true
	}
	return containsAny(collectKeywords(meta), []string{"anime"})
}

func isAnimation(meta api.PreparedMetadata) bool {
	return containsAny(collectGenres(meta), []string{"animation"})
}

func collectGenres(meta api.PreparedMetadata) []string {
	values := []string{}
	values = append(values, splitList(meta.Release.Genre)...)
	if meta.ExternalMetadata.TMDB != nil && externalMetadataMatchesCurrentSource(meta) {
		values = append(values, splitList(meta.ExternalMetadata.TMDB.Genres)...)
	}
	if meta.ExternalMetadata.IMDB != nil && externalMetadataMatchesCurrentSource(meta) {
		values = append(values, splitList(meta.ExternalMetadata.IMDB.Genres)...)
	}
	return normalizeStrings(values)
}

func collectKeywords(meta api.PreparedMetadata) []string {
	values := []string{}
	if meta.ExternalMetadata.TMDB != nil && externalMetadataMatchesCurrentSource(meta) {
		values = append(values, splitList(meta.ExternalMetadata.TMDB.Keywords)...)
	}
	return normalizeStrings(values)
}

func externalMetadataMatchesCurrentSource(meta api.PreparedMetadata) bool {
	storedSource := strings.TrimSpace(meta.ExternalMetadata.SourcePath)
	return storedSource == "" || strings.EqualFold(storedSource, strings.TrimSpace(meta.SourcePath))
}

func splitList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func normalizeStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		trimmed := strings.ToLower(strings.TrimSpace(value))
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, trimmed)
	}
	return out
}

func containsAny(values []string, targets []string) bool {
	if len(values) == 0 || len(targets) == 0 {
		return false
	}
	targetSet := make(map[string]bool, len(targets))
	for _, target := range targets {
		targetSet[strings.ToLower(strings.TrimSpace(target))] = true
	}
	for _, value := range values {
		if targetSet[strings.ToLower(strings.TrimSpace(value))] {
			return true
		}
	}
	return false
}
