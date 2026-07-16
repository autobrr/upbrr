// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package seasonep

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	pathutil "github.com/autobrr/upbrr/internal/pathing"
)

var (
	seasonEpisodePattern   = regexp.MustCompile(`(?i)\bS(\d{1,2})[ ._-]*E(\d{1,3}(?:[ ._-]*E\d{1,3})*)\b`)
	episodeTokenPattern    = regexp.MustCompile(`(?i)E(\d{1,3})`)
	multiEpisodePattern    = regexp.MustCompile(`(?i)E\d{1,3}\s*[-+&]\s*(?:E)?\d{1,3}`)
	altSeasonEpisode       = regexp.MustCompile(`(?i)\b(\d{1,2})x(\d{2,3})\b`)
	seasonOnlyPattern      = regexp.MustCompile(`(?i)\bS(\d{1,2})\b`)
	seasonWordPattern      = regexp.MustCompile(`(?i)\b(?:season|series)\s*(\d+)\b`)
	episodeOnlyPattern     = regexp.MustCompile(`(?i)\bE(\d{2,3})\b`)
	dailyPattern           = regexp.MustCompile(`\b(19\d{2}|20\d{2})[.-](\d{2})[.-](\d{2})\b`)
	animeResolutionPattern = regexp.MustCompile(`(?i)(?:\s-\s)?(\d{1,4})(?:v\d+)?\s*\((?:\d+[pi])\)`)
	animeEpisodePattern    = regexp.MustCompile(`(?i)\b(?:ep|episode)\s*([0-9]{1,4})\b`)
	animeGenericPattern    = regexp.MustCompile(`(?:^|[\s._-])(\d{1,4})(?:$|[\s._-])`)
)

var videoExtensions = map[string]struct{}{
	".mkv":  {},
	".mp4":  {},
	".avi":  {},
	".mov":  {},
	".wmv":  {},
	".webm": {},
	".ts":   {},
	".m2ts": {},
	".m2v":  {},
	".mpg":  {},
	".mpeg": {},
}

type Result struct {
	Season          int
	Episode         int
	TVPack          bool
	DailyDate       string
	AbsoluteEpisode int
	MultiEpisode    []int
}

func Extract(path string, meta preparationstate.State) Result {
	candidates := buildCandidates(path, meta)
	primaryCandidate := ""
	if len(candidates) > 0 {
		primaryCandidate = candidates[0]
	}
	primaryHasSingleEpisode := hasExplicitSingleEpisodeToken(primaryCandidate)
	multipleVideos := hasMultipleVideos(path, meta.FileList)

	result := Result{}
	seasonOnly := false

	for _, candidate := range candidates {
		if result.DailyDate == "" {
			result.DailyDate = parseDailyDate(candidate)
		}

		if result.Season == 0 && result.Episode == 0 {
			if season, episode, multi, ok := parseSeasonEpisode(candidate); ok {
				result.Season = season
				result.Episode = episode
				if len(multi) > 0 {
					result.MultiEpisode = append(result.MultiEpisode[:0], multi...)
				}
			}
		}

		if result.Season == 0 && result.Episode == 0 {
			if season, episode, ok := parseAltSeasonEpisode(candidate); ok {
				result.Season = season
				result.Episode = episode
			}
		}

		if result.Season == 0 {
			if season, ok := parseSeasonOnly(candidate); ok {
				result.Season = season
				seasonOnly = true
			}
		}
		if result.Episode == 0 && result.Season > 0 {
			if episode, ok := parseEpisodeOnly(candidate); ok {
				result.Episode = episode
			}
		}

		if result.AbsoluteEpisode == 0 {
			result.AbsoluteEpisode = parseAnimeAbsolute(candidate)
		}
	}

	if result.Season == 0 && meta.Release.Season > 0 {
		result.Season = meta.Release.Season
	}
	if result.Episode == 0 && meta.Release.Episode > 0 {
		result.Episode = meta.Release.Episode
	}

	// Keep the parsed absolute value available for anime remapping later.
	if result.AbsoluteEpisode > 0 && result.Episode == 0 {
		result.Episode = result.AbsoluteEpisode
	}

	if (seasonOnly || multipleVideos) && !primaryHasSingleEpisode {
		result.TVPack = true
		result.Episode = 0
		result.MultiEpisode = nil
	}

	return result
}

func FormatSeason(value int) string {
	if value <= 0 {
		return ""
	}
	return fmt.Sprintf("S%02d", value)
}

func FormatEpisode(value int) string {
	if value <= 0 {
		return ""
	}
	return fmt.Sprintf("E%02d", value)
}

func buildCandidates(path string, meta preparationstate.State) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 2)
	add := func(value string) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return
		}
		base := pathutil.Base(trimmed)
		if base == "." || base == string(filepath.Separator) {
			return
		}
		if _, exists := seen[base]; exists {
			return
		}
		seen[base] = struct{}{}
		out = append(out, base)
	}

	add(path)
	add(meta.VideoPath)
	return out
}

func parseSeasonEpisode(value string) (int, int, []int, bool) {
	match := seasonEpisodePattern.FindStringSubmatch(value)
	if len(match) < 2 {
		return 0, 0, nil, false
	}
	season := parseInt(match[1])
	if season == 0 {
		return 0, 0, nil, false
	}
	episodes := make([]int, 0, 4)
	for _, episodeMatch := range episodeTokenPattern.FindAllStringSubmatch(match[0], -1) {
		if len(episodeMatch) < 2 {
			continue
		}
		if parsed := parseInt(episodeMatch[1]); parsed > 0 {
			episodes = append(episodes, parsed)
		}
	}
	if len(episodes) == 0 {
		return season, 0, nil, true
	}
	multi := []int(nil)
	if len(episodes) > 1 {
		multi = append(multi, episodes...)
	}
	return season, episodes[0], multi, true
}

func parseAltSeasonEpisode(value string) (int, int, bool) {
	match := altSeasonEpisode.FindStringSubmatch(value)
	if len(match) < 3 {
		return 0, 0, false
	}
	season := parseInt(match[1])
	episode := parseInt(match[2])
	if season == 0 || episode == 0 {
		return 0, 0, false
	}
	return season, episode, true
}

func parseSeasonOnly(value string) (int, bool) {
	if match := seasonOnlyPattern.FindStringSubmatch(value); len(match) > 1 {
		if season := parseInt(match[1]); season > 0 {
			return season, true
		}
	}
	if match := seasonWordPattern.FindStringSubmatch(value); len(match) > 1 {
		if season := parseInt(match[1]); season > 0 {
			return season, true
		}
	}
	return 0, false
}

func parseEpisodeOnly(value string) (int, bool) {
	match := episodeOnlyPattern.FindStringSubmatch(value)
	if len(match) < 2 {
		return 0, false
	}
	episode := parseInt(match[1])
	return episode, episode > 0
}

func hasExplicitSingleEpisodeToken(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || multiEpisodePattern.MatchString(trimmed) {
		return false
	}
	if season, episode, multi, ok := parseSeasonEpisode(trimmed); ok && season > 0 && episode > 0 {
		return len(multi) == 0
	}
	if _, _, ok := parseAltSeasonEpisode(trimmed); ok {
		return true
	}
	_, ok := parseEpisodeOnly(trimmed)
	return ok
}

func parseDailyDate(value string) string {
	match := dailyPattern.FindStringSubmatch(value)
	if len(match) < 4 {
		return ""
	}
	date := fmt.Sprintf("%s-%s-%s", match[1], match[2], match[3])
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return ""
	}
	return date
}

func parseAnimeAbsolute(value string) int {
	if match := animeResolutionPattern.FindStringSubmatch(value); len(match) > 1 {
		if episode := parseInt(match[1]); episode > 0 {
			return episode
		}
	}
	if match := animeEpisodePattern.FindStringSubmatch(value); len(match) > 1 {
		if episode := parseInt(match[1]); validAbsolute(episode) {
			return episode
		}
	}
	// Keep this generic fallback limited to common fansub naming patterns.
	if !strings.Contains(value, "[") || !strings.Contains(value, "]") {
		return 0
	}
	for _, match := range animeGenericPattern.FindAllStringSubmatch(value, -1) {
		if len(match) < 2 {
			continue
		}
		episode := parseInt(match[1])
		if validAbsolute(episode) {
			return episode
		}
	}
	return 0
}

func validAbsolute(value int) bool {
	if value <= 0 || value > 500 {
		return false
	}
	switch value {
	case 360, 480, 540, 576, 720, 1080, 2160, 4320:
		return false
	}
	return true
}

func hasMultipleVideos(path string, fileList []string) bool {
	if len(fileList) > 1 {
		return true
	}

	info, err := os.Stat(strings.TrimSpace(path))
	if err != nil || !info.IsDir() {
		return false
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	videoCount := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if _, ok := videoExtensions[ext]; ok {
			videoCount++
		}
		if videoCount > 1 {
			return true
		}
	}
	return false
}

func parseInt(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return parsed
}
