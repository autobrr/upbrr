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

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
)

var (
	seasonEpisodePattern           = regexp.MustCompile(`(?i)\bS(\d{2}|\d{4})[ ._-]*E(\d{1,3}(?:v\d+)?(?:[ ._-]*E\d{1,3}(?:v\d+)?)*)\b`)
	episodeTokenPattern            = regexp.MustCompile(`(?i)E(\d{1,3})`)
	multiEpisodePattern            = regexp.MustCompile(`(?i)E\d{1,3}(?:v\d+)?\s*[-+&]\s*(?:E)?\d{1,3}`)
	multipleEpisodeEvidencePattern = regexp.MustCompile(
		`(?i)(?:\b|_)(?:S\d+[ ._-]*)?E\d+(?:v\d+)?((?:[ ._-]*(?:S\d+[ ._-]*)?E\d+(?:v\d+)?|\s*[-+&]\s*(?:(?:S\d+[ ._-]*)?E)?\d+(?:v\d+)?(?:[.-]\d{2}[.-]\d{2})?)+)(?:\b|_)`,
	)
	sourceEpisodeEvidencePattern = regexp.MustCompile(`(?i)(?:\b|_)(?:S(\d+)[ ._-]*)?E(\d+)(?:v\d+)?(?:\b|_)`)
	seasonOnlyPattern            = regexp.MustCompile(`(?i)\bS(\d{2}|\d{4})\b`)
	seasonWordPattern            = regexp.MustCompile(`(?i)\b(?:season|series)\s*(\d{2}|\d{4})\b`)
	episodeOnlyPattern           = regexp.MustCompile(`(?i)\bE(\d{2,3})(?:v\d+)?\b`)
	dailyPattern                 = regexp.MustCompile(`\b(19\d{2}|20\d{2})[.-](\d{2})[.-](\d{2})\b`)
	animeResolutionPattern       = regexp.MustCompile(`(?i)(?:\s-\s)?(\d{1,4})(?:v\d+)?\s*\((?:\d+[pi])\)`)
	animeEpisodePattern          = regexp.MustCompile(`(?i)\b(?:ep|episode)\s*([0-9]{1,4})\b`)
	animeGenericPattern          = regexp.MustCompile(`(?:^|[\s._-])(\d{1,4})(?:$|[\s._-])`)
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

// Result contains normalized episodic signals. DailyDate uses YYYY-MM-DD;
// MultiEpisode includes the first episode; AbsoluteEpisode is retained even
// when it also supplies Episode. MultipleEpisodes also retains ranges and
// contrary source-member coordinates that are not represented by MultiEpisode.
type Result struct {
	Season int
	// SeasonKnown distinguishes an explicit S00 token from missing season evidence.
	SeasonKnown      bool
	Episode          int
	MultipleEpisodes bool
	TVPack           bool
	DailyDate        string
	AbsoluteEpisode  int
	MultiEpisode     []int
}

// Extract parses the source basename before the selected video basename, then
// fills missing values from parsed release metadata. Season-only or multi-video
// sources become TV packs unless the primary basename names one explicit
// episode. Directory inspection is best-effort and filesystem errors are
// treated as no multi-video evidence.
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
	explicitSpecialEpisode := false

	for _, candidate := range candidates {
		if result.DailyDate == "" {
			result.DailyDate = parseDailyDate(candidate)
		}

		if !result.SeasonKnown && result.Season == 0 && result.Episode == 0 {
			if season, episode, multi, ok := parseSeasonEpisode(candidate); ok {
				result.Season = season
				result.SeasonKnown = true
				result.Episode = episode
				explicitSpecialEpisode = season == 0
				if len(multi) > 0 {
					result.MultiEpisode = append(result.MultiEpisode[:0], multi...)
				}
			}
		}

		if !result.SeasonKnown && result.Season == 0 {
			if season, ok := parseSeasonOnly(candidate); ok {
				result.Season = season
				result.SeasonKnown = true
				seasonOnly = true
			}
		}
		if result.Episode == 0 && (result.Season > 0 || result.SeasonKnown) && !explicitSpecialEpisode {
			if episode, ok := parseEpisodeOnly(candidate); ok {
				result.Episode = episode
			}
		}

		if result.AbsoluteEpisode == 0 {
			result.AbsoluteEpisode = parseAnimeAbsolute(candidate)
		}
	}

	if !result.SeasonKnown && result.Season == 0 && meta.Release.Season > 0 {
		result.Season = meta.Release.Season
	}
	if result.Episode == 0 && meta.Release.Episode > 0 && !explicitSpecialEpisode {
		result.Episode = meta.Release.Episode
	}

	// Retain absolute evidence without repairing an explicitly invalid S00E00.
	if result.AbsoluteEpisode > 0 && result.Episode == 0 && !explicitSpecialEpisode {
		result.Episode = result.AbsoluteEpisode
	}
	result.MultipleEpisodes = multipleEpisodeEvidence(candidates, meta.FileList, result)

	if (seasonOnly || multipleVideos) && !primaryHasSingleEpisode {
		result.TVPack = true
		result.Episode = 0
		result.MultiEpisode = nil
	}

	return result
}

// ParseSeasonInstruction parses one explicit caller-supplied season token: a
// bare or S-prefixed number with exactly two or four digits ("05", "S05",
// "2026", "S2026"). "00"/"S00" identifies specials; an empty value explicitly
// clears the season. Both return zero, so callers must preserve token presence. Combined,
// ranged, overflowing, or otherwise malformed values are rejected with a
// typed invalid-input error.
func ParseSeasonInstruction(value string) (int, error) {
	return parseInstructionToken(value, "S", 4, "season")
}

// ParseEpisodeInstruction parses one explicit caller-supplied episode token: a
// bare number or an E-prefixed number of at most three digits ("7", "07",
// "E07"). An empty value means an explicit clear and returns zero. Combined,
// ranged, zero, overflowing, or otherwise malformed values are rejected with a
// typed invalid-input error.
func ParseEpisodeInstruction(value string) (int, error) {
	return parseInstructionToken(value, "E", 3, "episode")
}

func parseInstructionToken(value string, prefix string, maxDigits int, label string) (int, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, nil
	}
	digits := trimmed
	if len(digits) > 1 && strings.EqualFold(digits[:1], prefix) {
		digits = digits[1:]
	}
	if len(digits) > maxDigits || prefix == "S" && len(digits) != 2 && len(digits) != 4 {
		return 0, instructionTokenError(label, value, prefix)
	}
	for _, char := range digits {
		if char < '0' || char > '9' {
			return 0, instructionTokenError(label, value, prefix)
		}
	}
	number, err := strconv.Atoi(digits)
	if err != nil || number < 0 || number == 0 && prefix != "S" {
		return 0, instructionTokenError(label, value, prefix)
	}
	return number, nil
}

func instructionTokenError(label string, value string, prefix string) error {
	return fmt.Errorf(
		"%s instruction %q: expected a single valid token such as %q or %q: %w",
		label,
		value,
		"05",
		prefix+"05",
		internalerrors.ErrInvalidInput,
	)
}

// FormatSeason pads positive seasons to two digits, or at least four above 99.
// Nonpositive values return an empty string.
func FormatSeason(value int) string {
	if value <= 0 {
		return ""
	}
	if value >= 100 {
		return fmt.Sprintf("S%04d", value)
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

func parseSeasonOnly(value string) (int, bool) {
	if match := seasonOnlyPattern.FindStringSubmatch(value); len(match) > 1 {
		return parseInt(match[1]), true
	}
	if match := seasonWordPattern.FindStringSubmatch(value); len(match) > 1 {
		return parseInt(match[1]), true
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
	if _, episode, multi, ok := parseSeasonEpisode(trimmed); ok && episode > 0 {
		return len(multi) == 0
	}
	_, ok := parseEpisodeOnly(trimmed)
	return ok
}

// multipleEpisodeEvidence retains contrary source membership even when the
// primary folder names one episode. Unknown filenames do not establish a pack.
func multipleEpisodeEvidence(candidates, files []string, result Result) bool {
	if len(result.MultiEpisode) > 1 {
		return true
	}
	for group, names := range [][]string{candidates, files} {
		knownSeason := strings.TrimLeft(strconv.Itoa(result.Season), "0")
		knownEpisode := strconv.Itoa(result.Episode)
		episodeKnown := result.Episode > 0
		for _, name := range names {
			base := filepath.Base(name)
			if group == 1 {
				if _, video := videoExtensions[strings.ToLower(filepath.Ext(base))]; !video {
					continue
				}
			}
			for _, match := range multipleEpisodeEvidencePattern.FindAllStringSubmatch(base, -1) {
				// Only a complete hyphen-prefixed date is an annotation. A date
				// after another member cannot erase that member's evidence.
				suffix := strings.TrimSpace(match[1])
				if strings.HasPrefix(suffix, "-") {
					date := strings.ReplaceAll(strings.TrimSpace(suffix[1:]), ".", "-")
					if date == parseDailyDate(date) {
						continue
					}
				}
				return true
			}
			if group == 0 && result.Episode <= 0 {
				// A provisional folder label does not contradict one selected
				// media file; members within each source name still must agree.
				knownSeason = strings.TrimLeft(strconv.Itoa(result.Season), "0")
				episodeKnown = false
			}
			// Unsupported widths still retain contrary membership; they do not
			// become canonical coordinates or truncate to a valid episode prefix.
			for _, match := range sourceEpisodeEvidencePattern.FindAllStringSubmatch(base, -1) {
				season := knownSeason
				if match[1] != "" {
					season = strings.TrimLeft(match[1], "0")
				}
				episode := strings.TrimLeft(match[2], "0")
				if !episodeKnown {
					knownSeason, knownEpisode, episodeKnown = season, episode, true
				} else if season != knownSeason || episode != knownEpisode {
					return true
				}
			}
		}
	}
	return false
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
