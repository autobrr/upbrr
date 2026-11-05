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
		`(?i)(?:^|[ ._-])(?:S(\d+)[ ._-]*)?E(\d+)(?:v\d+)?((?:[ ._-]*E\d+(?:v\d+)?|\s*[-+&]\s*(?:S\d+[ ._-]*)?E?\d+(?:v\d+)?)*)(?:\b|_)`,
	)
	coverageMemberPattern = regexp.MustCompile(`(?i)([ ._-]*E|\s*[-+&]\s*(?:S(\d+)[ ._-]*)?E?)(\d+)(?:v\d+)?`)
)

// Coverage reads explicit part and episode membership without inspecting media.
// inventoryStatus describes enumeration of files, never completeness of a season.
// Unknown members and conflicting season/range evidence remain unverified.
func Coverage(season int, source string, files []string, inventoryStatus api.MetadataEvidenceStatus) api.EpisodeCoverage {
	facts := api.EpisodeCoverage{
		Status:      api.MetadataEvidenceStatusUnavailable,
		Episodes:    []int{},
		SeasonKnown: season > 0,
	}
	if sourceSeason, known := api.DetectedFileSeason(source); known && sourceSeason == season {
		facts.SeasonKnown = true
	}
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
	sourceMembers, sourceStatus := coverageMembers(season, filepath.Base(source))
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
		if fileSeason, known := api.DetectedFileSeason(file); known && fileSeason == season {
			facts.SeasonKnown = true
		}
		count++
		episodes, status := coverageMembers(season, filepath.Base(file))
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
		if parseInt(match[1]+match[2]) != season {
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

func coverageMembers(season int, name string) ([]int, api.MetadataEvidenceStatus) {
	name = strings.ReplaceAll(name, "_", ".")
	for _, date := range dailyPattern.FindAllString(name, -1) {
		if parseDailyDate(date) != "" {
			name = strings.ReplaceAll(name, date, "")
		}
	}
	matches := coveragePattern.FindAllStringSubmatch(name, -1)
	if len(matches) == 0 {
		return nil, api.MetadataEvidenceStatusUnavailable
	}
	status := api.MetadataEvidenceStatusComplete
	remainder := coveragePattern.ReplaceAllString(strings.ReplaceAll(name, "_", "."), " ")
	if sourceEpisodeEvidencePattern.MatchString(remainder) {
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
