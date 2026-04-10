// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

var seasonPattern = regexp.MustCompile(`(?i)[s](\d{1,2})`)
var episodePattern = regexp.MustCompile(`(?i)[e](\d{1,3})`)
var dailyEpisodePattern = regexp.MustCompile(`(?i)\b((?:19|20)\d{2})[.\-_/\s](\d{1,2})[.\-_/\s](\d{1,2})\b`)

func FilterDupes(dupes []api.DupeEntry, meta api.PreparedMetadata, tracker string, cfg config.Config, logger api.Logger) ([]api.DupeEntry, api.DupeMatch) {
	match := api.DupeMatch{}
	if len(dupes) == 0 {
		return nil, match
	}

	hasRepackInName := strings.Contains(strings.ToLower(meta.ReleaseName), "repack")
	videoEncode := strings.ToLower(strings.TrimSpace(meta.VideoEncode))
	videoEncodeNormalized := normalizeFilename(meta.VideoEncode)

	fileSize := resolvePrimaryFileSize(meta)

	targetHDR := refineHDRTerms(meta.HDR)
	targetSeason, targetEpisode := resolveSeasonEpisode(meta)
	targetResolution := strings.TrimSpace(meta.Release.Resolution)
	tag := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(strings.TrimPrefix(meta.Tag, "-")), "-", " "))
	isDVD := strings.EqualFold(meta.DiscType, "DVD")
	isDVDRIP := strings.EqualFold(meta.Type, "DVDRIP")
	webDL := strings.EqualFold(meta.Type, "WEBDL")
	isHDTV := strings.EqualFold(meta.Type, "HDTV")
	targetSource := strings.TrimSpace(meta.Source)
	isSD := isSDResolution(targetResolution)
	hasDisc := strings.TrimSpace(meta.DiscType) != ""

	filenames, filelist := resolveFileNames(meta)

	attributeChecks := []attributeCheck{
		{
			key:       "remux",
			uuidFlag:  strings.Contains(strings.ToLower(meta.ReleaseName), "remux"),
			condition: func(value string) bool { return strings.Contains(strings.ToLower(value), "remux") },
		},
		{
			key:       "uhd",
			uuidFlag:  strings.Contains(strings.ToLower(meta.ReleaseName), "uhd"),
			condition: func(value string) bool { return strings.Contains(strings.ToLower(value), "uhd") },
		},
	}

	processEntry := func(entry api.DupeEntry) bool {
		each := strings.TrimSpace(entry.Name)
		normalized := normalizeFilename(each)
		fileHDR := refineHDRFromEntry(entry, normalized)
		fileCount := entry.FileCount

		rememberMatch := func(reason string) {
			if match.MatchedName == "" {
				match.MatchedName = entry.Name
			}
			if match.MatchedLink == "" {
				match.MatchedLink = entry.Link
			}
			if match.MatchedDownload == "" {
				match.MatchedDownload = entry.Download
			}
			if match.MatchedReason == "" {
				match.MatchedReason = reason
			}
			if match.FileCountMatch == 0 && fileCount > 0 {
				match.FileCountMatch = fileCount
			}
			if match.MatchedID == "" {
				match.MatchedID = entry.ID
			}
		}

		if (tracker == "AITHER" || tracker == "LST") && entry.Trumpable && entry.Res != "" && targetResolution == entry.Res {
			match.TrumpableID = entry.ID
			rememberMatch("trumpable_id")
		}

		if !hasDisc {
			for _, file := range filenames {
				if tracker == "MTV" || tracker == "AR" || tracker == "RTF" {
					for _, dupeFile := range entry.Files {
						if strings.Contains(strings.ToLower(file), strings.ToLower(dupeFile)) {
							match.FilenameMatch = entry.Name
							rememberMatch("filename")
							if fileCount > 0 && fileCount == len(filelist) {
								match.FileCountMatch = fileCount
								rememberMatch("file_count")
								return false
							}
						}
					}
					if entry.SizeKnown && meta.SourceSize > 0 && entry.SizeBytes == meta.SourceSize {
						match.SizeMatch = entry.Name
						rememberMatch("size")
						return false
					}
				} else {
					for _, dupeFile := range entry.Files {
						if strings.EqualFold(file, dupeFile) {
							match.FilenameMatch = entry.Name
							rememberMatch("filename")
							rememberMatch("id")
							if fileCount > 0 && fileCount == len(filelist) {
								match.FileCountMatch = fileCount
								rememberMatch("file_count")
								return false
							}
						}
					}
				}
			}
			if tracker == "BHD" {
				if entry.SizeKnown && meta.SourceSize > 0 && entry.SizeBytes == meta.SourceSize {
					match.SizeMatch = entry.Name
					rememberMatch("size")
					return false
				}
			}
		} else if entry.SizeKnown && meta.SourceSize > 0 && entry.SizeBytes == meta.SourceSize {
			match.SizeMatch = entry.Name
			rememberMatch("size")
			return false
		}

		if hasDisc && fileCount > 0 && fileCount < 2 {
			return true
		}
		if hasRepackInName && !strings.Contains(normalized, "repack") && tag != "" && strings.Contains(normalized, tag) {
			return true
		}

		if tracker == "MTV" {
			targetName := strings.ReplaceAll(meta.ReleaseName, " ", ".")
			targetName = strings.ReplaceAll(targetName, "DD+", "DDP")
			dupeName := entry.Name
			normalizedTarget := normalizeMTVName(targetName)
			if normalizedTarget == dupeName {
				match.FilenameMatch = entry.Name
				return false
			}
		}

		if tracker == "BHD" {
			targetName := strings.ReplaceAll(meta.ReleaseName, "DD+", "DDP")
			if entry.Name == targetName {
				match.FilenameMatch = entry.Name
				return false
			}
		}

		if hasDisc && strings.HasSuffix(strings.ToLower(each), ".m2ts") {
			return false
		}
		if hasDisc && regexp.MustCompile(`\.\w{2,4}$`).MatchString(each) {
			return true
		}

		if isSD && (tracker == "BHD" || tracker == "AITHER") && containsResolution(each, []int{1080, 720, 2160}) {
			return false
		}

		if len(targetHDR) > 0 && strings.Contains(strings.ToLower(targetResolution), "1080") && strings.Contains(strings.ToLower(each), "2160p") {
			return false
		}

		if (tracker == "AITHER" || tracker == "LST") && isDVD {
			if tag == "" {
				return false
			}
			return !strings.Contains(normalized, tag)
		}

		if webDL {
			if strings.Contains(normalized, "hdtv") && !containsAny(normalized, []string{"web-dl", "web -dl", "webdl", "web dl"}) {
				return true
			}
			if containsAny(normalized, []string{"blu-ray", "blu ray", "bluray", "blu -ray"}) && !containsAny(normalized, []string{"web-dl", "web -dl", "webdl", "web dl"}) {
				return true
			}
		}
		if !webDL && containsAny(normalized, []string{"web-dl", "web -dl", "webdl", "web dl"}) {
			return true
		}

		skipResolutionCheck := isDVD || strings.Contains(strings.ToUpper(targetSource), "DVD") || isDVDRIP
		if !skipResolutionCheck {
			if targetResolution != "" && !strings.Contains(each, targetResolution) {
				return true
			}
			if !hasMatchingHDR(fileHDR, targetHDR, meta, tracker) {
				return true
			}
		}

		if isDVD && tracker != "BHD" && containsResolution(each, []int{1080, 720, 2160}) {
			return false
		}

		for _, check := range attributeChecks {
			if check.key == "repack" {
				if hasRepackInName && !strings.Contains(normalized, "repack") && tag != "" && strings.Contains(normalized, tag) {
					return true
				}
				continue
			}
			if check.key == "remux" {
				uuidHas := check.uuidFlag
				dupeHas := check.condition(normalized)
				if uuidHas && !dupeHas {
					return true
				}
				if !uuidHas && dupeHas {
					return true
				}
			}
		}

		if strings.EqualFold(meta.ExternalIDs.Category, "TV") || strings.EqualFold(meta.MediaInfoCategory, "TV") {
			seasonMatch, isSeason := isSeasonEpisodeMatch(normalized, targetSeason, targetEpisode)
			if !seasonMatch {
				return true
			}
			if isSeason && targetEpisode != "" {
				match.SeasonPackExists = true
				match.SeasonPackName = each
				match.SeasonPackLink = entry.Link
				match.SeasonPackID = entry.ID
				match.SeasonPackContainsEpisode = true
				return false
			}
		}

		if isHDTV && containsAny(normalized, []string{"web-dl", "web -dl", "webdl", "web dl"}) {
			return false
		}

		if len(dupes) == 1 && !strings.EqualFold(meta.DiscType, "BDMV") && (tracker == "AITHER" || tracker == "BHD" || tracker == "OE" || tracker == "ULCX") && fileSize > 0 && strings.Contains(targetResolution, "1080") && strings.Contains(videoEncode, "x264") {
			if entry.SizeKnown && entry.SizeBytes > 0 {
				sizeDiff := float64(fileSize-entry.SizeBytes) / float64(entry.SizeBytes)
				if sizeDiff >= 0.20 {
					return true
				}
			}
		}
		if len(dupes) == 1 && !strings.EqualFold(meta.DiscType, "BDMV") && tracker == "RF" {
			if tag != "" && strings.Contains(normalized, tag) {
				return false
			}
			if tag != "" && !strings.Contains(normalized, tag) {
				return true
			}
		}

		_ = videoEncodeNormalized
		return false
	}

	filtered := make([]api.DupeEntry, 0, len(dupes))
	for _, entry := range dupes {
		if exclude := processEntry(entry); !exclude {
			filtered = append(filtered, entry)
		}
	}

	return filtered, match
}

type attributeCheck struct {
	key       string
	uuidFlag  bool
	condition func(string) bool
}

func normalizeFilename(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	lower = strings.ReplaceAll(lower, "-", " -")
	lower = strings.ReplaceAll(lower, ".", " ")
	return lower
}

func normalizeMTVName(value string) string {
	normalized := value
	normalized = regexp.MustCompile(`\.DDP\.(\d)`).ReplaceAllString(normalized, `.DDP$1`)
	normalized = regexp.MustCompile(`\.DD\.(\d)`).ReplaceAllString(normalized, `.DD$1`)
	normalized = regexp.MustCompile(`\.AC3\.(\d)`).ReplaceAllString(normalized, `.AC3$1`)
	normalized = regexp.MustCompile(`\.DTS\.(\d)`).ReplaceAllString(normalized, `.DTS$1`)
	return normalized
}

func resolveFileNames(meta api.PreparedMetadata) ([]string, []string) {
	if strings.TrimSpace(meta.DiscType) != "" {
		return nil, nil
	}
	files := make([]string, 0, len(meta.FileList))
	for _, path := range meta.FileList {
		files = append(files, filepath.Base(path))
	}
	return files, meta.FileList
}

func resolvePrimaryFileSize(meta api.PreparedMetadata) int64 {
	if strings.TrimSpace(meta.VideoPath) != "" {
		if info, err := os.Stat(meta.VideoPath); err == nil && !info.IsDir() {
			return info.Size()
		}
	}
	if strings.TrimSpace(meta.DiscType) == "" {
		return meta.SourceSize
	}
	return 0
}

func refineHDRTerms(hdr string) map[string]struct{} {
	terms := make(map[string]struct{})
	if strings.TrimSpace(hdr) == "" {
		return terms
	}
	upper := strings.ToUpper(hdr)
	if strings.Contains(upper, "DV") || strings.Contains(upper, "DOVI") {
		terms["DV"] = struct{}{}
	}
	if strings.Contains(upper, "HDR") {
		terms["HDR"] = struct{}{}
	}
	return terms
}

func refineHDRFromEntry(entry api.DupeEntry, normalized string) map[string]struct{} {
	if len(entry.Flags) == 0 {
		return refineHDRTerms(normalized)
	}
	terms := make(map[string]struct{})
	for _, flag := range entry.Flags {
		upper := strings.ToUpper(strings.TrimSpace(flag))
		switch upper {
		case "DV":
			terms["DV"] = struct{}{}
		case "HDR", "HDR10", "HDR10+":
			terms["HDR"] = struct{}{}
		}
	}
	return terms
}

func hasMatchingHDR(fileHDR, targetHDR map[string]struct{}, meta api.PreparedMetadata, tracker string) bool {
	simplify := func(hdr map[string]struct{}, trackerName string) map[string]struct{} {
		out := make(map[string]struct{})
		if _, ok := hdr["HDR"]; ok {
			out["HDR"] = struct{}{}
		}
		if _, ok := hdr["HDR10"]; ok {
			out["HDR"] = struct{}{}
		}
		if _, ok := hdr["HDR10+"]; ok {
			out["HDR"] = struct{}{}
		}
		if _, ok := hdr["DV"]; ok {
			out["DV"] = struct{}{}
			metaType := strings.ToLower(strings.TrimSpace(meta.Type))
			if !strings.Contains(metaType, "web") {
				out["HDR"] = struct{}{}
			}
			if trackerName == "ANT" {
				out["HDR"] = struct{}{}
			}
		}
		return out
	}

	fileSimple := simplify(fileHDR, tracker)
	targetSimple := simplify(targetHDR, tracker)
	if len(fileSimple) == 2 {
		if _, hasHDR := fileSimple["HDR"]; hasHDR {
			if _, hasDV := fileSimple["DV"]; hasDV {
				fileSimple = map[string]struct{}{"HDR": {}}
			}
		}
	}
	if len(targetSimple) == 2 {
		if _, hasHDR := targetSimple["HDR"]; hasHDR {
			if _, hasDV := targetSimple["DV"]; hasDV {
				targetSimple = map[string]struct{}{"HDR": {}}
			}
		}
	}

	if len(fileSimple) != len(targetSimple) {
		return false
	}
	for key := range fileSimple {
		if _, ok := targetSimple[key]; !ok {
			return false
		}
	}
	return true
}

func parseSeasonEpisode(value string) (string, string) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", ""
	}
	season := ""
	episode := ""
	if match := seasonPattern.FindStringSubmatch(trimmed); len(match) > 1 {
		season = "S" + match[1]
	}
	if match := episodePattern.FindStringSubmatch(trimmed); len(match) > 1 {
		episode = "E" + match[1]
	}
	return season, episode
}

func resolveSeasonEpisode(meta api.PreparedMetadata) (string, string) {
	season := ""
	episode := ""
	if meta.ReleaseNameOverrides.Season != nil {
		season = normalizeSeasonEpisode(*meta.ReleaseNameOverrides.Season)
	}
	if meta.ReleaseNameOverrides.Episode != nil {
		episode = normalizeEpisodeValue(*meta.ReleaseNameOverrides.Episode)
	}
	if season == "" {
		season = normalizeSeasonEpisode(meta.SeasonStr)
		if season == "" && meta.SeasonInt > 0 {
			season = normalizeSeasonEpisode(strconv.Itoa(meta.SeasonInt))
		}
	}
	if episode == "" {
		episode = normalizeEpisodeValue(meta.EpisodeStr)
		if episode == "" && meta.EpisodeInt > 0 {
			episode = normalizeEpisodeValue(strconv.Itoa(meta.EpisodeInt))
		}
	}
	if season == "" || episode == "" {
		parsedSeason, parsedEpisode := parseSeasonEpisode(meta.ReleaseName)
		if season == "" {
			season = parsedSeason
		}
		if episode == "" {
			episode = parsedEpisode
		}
	}
	return season, episode
}

func isSeasonEpisodeMatch(filename string, targetSeason string, targetEpisode string) (bool, bool) {
	seasonMatch := seasonPattern.FindStringSubmatch(targetSeason)
	var targetSeasonValue int
	if len(seasonMatch) > 1 {
		if value, err := strconv.Atoi(seasonMatch[1]); err == nil {
			targetSeasonValue = value
		}
	}

	if targetEpisode != "" {
		if match := dailyEpisodePattern.FindStringSubmatch(targetEpisode); len(match) == 4 {
			year, yearErr := strconv.Atoi(match[1])
			month, monthErr := strconv.Atoi(match[2])
			day, dayErr := strconv.Atoi(match[3])
			if yearErr == nil && monthErr == nil && dayErr == nil {
				pattern := regexp.MustCompile(`(?i)\b` + strconv.Itoa(year) + `[.\-_/\s]?` + leftPad(month, 2) + `[.\-_/\s]?` + leftPad(day, 2) + `\b`)
				if pattern.MatchString(filename) {
					return true, false
				}
				return false, false
			}
		}
	}

	targetEpisodes := []int{}
	if targetEpisode != "" {
		for _, match := range episodePattern.FindAllStringSubmatch(targetEpisode, -1) {
			if len(match) > 1 {
				if value, err := strconv.Atoi(match[1]); err == nil {
					targetEpisodes = append(targetEpisodes, value)
				}
			}
		}
	}

	seasonPatternValue := ""
	if targetSeasonValue > 0 {
		seasonPatternValue = "S" + strconv.FormatInt(int64(targetSeasonValue), 10)
	}
	isSeasonPack := !regexp.MustCompile(`(?i)e\d{2}`).MatchString(filename)

	if len(targetEpisodes) == 0 {
		seasonMatches := seasonPatternValue != "" && regexp.MustCompile(`(?i)`+seasonPatternValue).MatchString(filename)
		return seasonMatches && isSeasonPack, seasonMatches
	}

	if seasonPatternValue != "" {
		if isSeasonPack {
			return regexp.MustCompile(`(?i)` + seasonPatternValue).MatchString(filename), true
		}
		for _, ep := range targetEpisodes {
			pattern := regexp.MustCompile(`(?i)E` + leftPad(ep, 2))
			if regexp.MustCompile(`(?i)`+seasonPatternValue).MatchString(filename) && pattern.MatchString(filename) {
				return true, false
			}
		}
	}
	return false, false
}

func leftPad(value int, width int) string {
	text := strconv.Itoa(value)
	for len(text) < width {
		text = "0" + text
	}
	return text
}

func normalizeEpisodeValue(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	upper := strings.ToUpper(trimmed)
	if strings.HasPrefix(upper, "E") {
		return upper
	}
	if num, err := strconv.Atoi(trimmed); err == nil {
		return "E" + strconv.Itoa(num)
	}
	return upper
}

func containsResolution(value string, resolutions []int) bool {
	lower := strings.ToLower(value)
	for _, res := range resolutions {
		if strings.Contains(lower, strconv.Itoa(res)) {
			return true
		}
	}
	return false
}

func containsAny(value string, terms []string) bool {
	for _, term := range terms {
		if strings.Contains(value, term) {
			return true
		}
	}
	return false
}

func isSDResolution(resolution string) bool {
	lower := strings.ToLower(strings.TrimSpace(resolution))
	return strings.Contains(lower, "480") || strings.Contains(lower, "576")
}
