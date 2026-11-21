// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package seasonep

import (
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

var (
	completeSeasonPattern = regexp.MustCompile(`(?i)(?:^|[ ._-])(?:S(\d+)[ ._-]+COMPLETE|COMPLETE[ ._-]+S(\d+))(?:$|[ ._-])`)
	partPattern           = regexp.MustCompile(`(?i)(?:^|[ ._-])(?:part|pt)[ ._-]*(\d+)\b`)
	coveragePattern       = regexp.MustCompile(
		`(?i)(?:^|[^a-z0-9])(?:S(\d+)[ ._-]*)?E(\d+)(?:v\d+)?((?:[ ._-]*E\d+(?:v\d+)?|\s*[-+&]\s*(?:S\d+[ ._-]*)?E?\d+(?:v\d+)?)*)(?:\b|_)`,
	)
	coverageMemberPattern = regexp.MustCompile(`(?i)([ ._-]*E|\s*[-+&]\s*(?:S(\d+)[ ._-]*)?E?)(\d+)(?:v\d+)?`)
	seasonLabelSeparators = strings.NewReplacer(".", " ", "-", " ")
	seasonLabelWords      = regexp.MustCompile(`(?i)\b(?:seasons?|series)\s*(\d{1,4})\b`)
	seasonLabelChain      = regexp.MustCompile(`(?i)\b(?:S(?:\d{2}|\d{4})){2,}(?:E\d+(?:v\d+)?)*\b`)
	seasonLabelRange      = regexp.MustCompile(
		`(?i)(?:^|[^a-z0-9])(seasons?|series|s)[ .-]*(\d{1,4})([ .]*(?:[-–—,&+]|\b(?:to|through|and)\b)[ .]*)(s?)(\d{1,4})\b(?:\s*\(\d+[pi]\))?`,
	)
)

// SeasonLabelStatus checks explicit season labels in a filename against one
// selected season. A season range must contain only that season. A known
// individual episode can disambiguate a resolution-suffixed absolute label.
// Unlabeled filenames can still supply episode-only evidence.
func SeasonLabelStatus(file string, season, episode int) api.MetadataEvidenceStatus {
	base := strings.ReplaceAll(filepath.Base(file), "_", ".")
	name := seasonLabelSeparators.Replace(base)
	name = seasonLabelChain.ReplaceAllStringFunc(name, func(chain string) string {
		return strings.ReplaceAll(strings.ToUpper(chain), "S", " S")
	})
	status := api.MetadataEvidenceStatusUnavailable
	compact := false
	for _, pattern := range []*regexp.Regexp{seasonEpisodePattern, seasonOnlyPattern, seasonLabelWords} {
		for _, match := range pattern.FindAllStringSubmatch(name, -1) {
			value := parseInt(match[1])
			if pattern == seasonLabelWords && compact && value >= 1900 && value <= 2099 {
				// With an explicit compact season, a title such as "Example Series 2025"
				// supplies no independent evidence of a year-numbered season.
				continue
			}
			if value != season {
				return api.MetadataEvidenceStatusContradictory
			}
			compact = compact || pattern != seasonLabelWords
			status = api.MetadataEvidenceStatusComplete
		}
	}
	for _, match := range seasonLabelRange.FindAllStringSubmatch(withoutDailyDates(base), -1) {
		compactRange := strings.EqualFold(match[1], "s")
		if compactRange && match[4] == "" {
			// A bare separator after S01 can introduce an episode or an air date.
			switch strings.Trim(strings.ToLower(match[3]), " .") {
			case "to", "through", "and":
			default:
				continue
			}
		}
		start, end := parseInt(match[2]), parseInt(match[5])
		first, last := min(start, end), max(start, end)
		if compact && !compactRange && first >= 1900 && last <= 2099 && (season < first || season > last) {
			// A year range outside the compact season can belong to the title.
			continue
		}
		if episode > 0 && season == start && episode == end && strings.EqualFold(match[1], "season") && match[4] == "" &&
			strings.TrimSpace(match[3]) == "-" && strings.Contains(match[3], " - ") && animeResolutionPattern.MatchString(match[0]) {
			continue
		}
		if start != season || end != season {
			return api.MetadataEvidenceStatusContradictory
		}
		status = api.MetadataEvidenceStatusComplete
	}
	return status
}

// Coverage reads explicit part and episode membership without inspecting media.
// seasonKnown distinguishes a selected S00 from an unspecified season value.
// inventoryStatus describes enumeration of files, never completeness of a season.
// Unknown members and conflicting season/range evidence remain unverified.
func Coverage(season int, seasonKnown bool, source string, files []string, inventoryStatus api.MetadataEvidenceStatus) api.EpisodeCoverage {
	facts := api.EpisodeCoverage{
		Status:      api.MetadataEvidenceStatusUnavailable,
		Episodes:    []int{},
		SeasonKnown: seasonKnown,
	}
	observedSeason, observed := season, seasonKnown
	reconcileSeason := func(name string) {
		detected, known := api.DetectedFileSeason(name)
		if !known {
			return
		}
		if observed && detected != observedSeason {
			facts.Status = api.MetadataEvidenceStatusContradictory
		}
		if !observed {
			observedSeason, observed = detected, true
		}
		if detected == season {
			facts.SeasonKnown = true
		}
	}
	reconcileSeason(source)
	for _, match := range partPattern.FindAllStringSubmatch(strings.ReplaceAll(filepath.Base(source), "_", "."), -1) {
		value, err := strconv.Atoi(match[1])
		if err != nil || value <= 0 {
			continue
		}
		part := "Part." + strconv.Itoa(value)
		if facts.Part != "" && facts.Part != part {
			facts.Status = api.MetadataEvidenceStatusContradictory
		}
		facts.Part = part
	}
	sourceMembers, sourceStatus := coverageMembers(observedSeason, filepath.Base(source))
	if facts.Part == "" && len(sourceMembers) > 1 && sourceStatus != api.MetadataEvidenceStatusContradictory {
		labels := make([]string, 0, len(sourceMembers))
		for _, episode := range sourceMembers {
			labels = append(labels, FormatEpisode(episode))
		}
		facts.Part = strings.Join(labels, "+")
		if sourceMembers[len(sourceMembers)-1]-sourceMembers[0]+1 == len(sourceMembers) {
			facts.Part = labels[0] + "-" + labels[len(labels)-1]
		}
	}
	members := []int(nil)
	complete := inventoryStatus == api.MetadataEvidenceStatusComplete
	count := 0
	for _, file := range files {
		if !IsVideoFile(file) {
			continue
		}
		reconcileSeason(file)
		count++
		episodes, status := coverageMembers(observedSeason, filepath.Base(file))
		members = append(members, episodes...)
		if status != api.MetadataEvidenceStatusComplete {
			complete = false
		}
		if status == api.MetadataEvidenceStatusContradictory {
			facts.Status = status
		}
	}
	slices.Sort(members)
	members = slices.Compact(members)
	if sourceStatus == api.MetadataEvidenceStatusContradictory {
		facts.Status = sourceStatus
	}
	if len(sourceMembers) > 0 && count > 0 {
		for _, episode := range members {
			if !slices.Contains(sourceMembers, episode) {
				facts.Status = api.MetadataEvidenceStatusContradictory
			}
		}
		if complete && !slices.Equal(sourceMembers, members) {
			facts.Status = api.MetadataEvidenceStatusContradictory
		}
	}
	if len(members) == 0 {
		members = sourceMembers
		complete = count == 0 && sourceStatus == api.MetadataEvidenceStatusComplete
	}
	facts.Episodes = append(facts.Episodes, members...)
	if match := completeSeasonPattern.FindStringSubmatch(filepath.Base(source)); match != nil {
		facts.CompleteSeason = true
		if parseInt(match[1]+match[2]) != observedSeason {
			facts.Status = api.MetadataEvidenceStatusContradictory
		}
	}
	if facts.CompleteSeason && facts.Part != "" {
		facts.Status = api.MetadataEvidenceStatusContradictory
	}
	if facts.Status != api.MetadataEvidenceStatusContradictory {
		switch {
		case len(members) == 0:
			facts.Status = api.MetadataEvidenceStatusUnavailable
		case complete:
			facts.Status = api.MetadataEvidenceStatusComplete
		default:
			facts.Status = api.MetadataEvidenceStatusPartial
		}
	}
	return facts
}

func withoutDailyDates(name string) string {
	for _, date := range dailyPattern.FindAllString(name, -1) {
		if parseDailyDate(date) != "" {
			name = strings.ReplaceAll(name, date, "")
		}
	}
	return name
}

func coverageMembers(season int, name string) ([]int, api.MetadataEvidenceStatus) {
	name = withoutDailyDates(strings.ReplaceAll(name, "_", "."))
	matches := coveragePattern.FindAllStringSubmatch(name, -1)
	if len(matches) == 0 {
		return nil, api.MetadataEvidenceStatusUnavailable
	}
	status := api.MetadataEvidenceStatusComplete
	remainder := coveragePattern.ReplaceAllString(strings.ReplaceAll(name, "_", "."), " ")
	if len(matches) > 1 || sourceEpisodeEvidencePattern.MatchString(remainder) {
		status = api.MetadataEvidenceStatusContradictory
	}
	episodes := []int(nil)
	for _, match := range matches {
		if match[1] != "" && parseInt(match[1]) != season || len(match[1]) > 4 || len(match[2]) > 3 {
			status = api.MetadataEvidenceStatusContradictory
			continue
		}
		first := parseInt(match[2])
		if first <= 0 {
			status = api.MetadataEvidenceStatusContradictory
			continue
		}
		episodes = append(episodes, first)
		previous := first
		for _, member := range coverageMemberPattern.FindAllStringSubmatch(match[3], -1) {
			next := parseInt(member[3])
			if next <= 0 || len(member[3]) > 3 || member[2] != "" && parseInt(member[2]) != season {
				status = api.MetadataEvidenceStatusContradictory
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(member[1]), "-") {
				if next < previous {
					status = api.MetadataEvidenceStatusContradictory
					continue
				}
				for episode := previous + 1; episode <= next; episode++ {
					episodes = append(episodes, episode)
				}
			} else {
				episodes = append(episodes, next)
			}
			previous = next
		}
	}
	slices.Sort(episodes)
	return slices.Compact(episodes), status
}
