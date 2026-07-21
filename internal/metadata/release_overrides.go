// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/seasonep"
	"github.com/autobrr/upbrr/pkg/api"
)

func mergeReleaseNameOverrides(base api.ReleaseNameOverrides, incoming api.ReleaseNameOverrides) api.ReleaseNameOverrides {
	result := base
	if incoming.Category != nil {
		result.Category = incoming.Category
	}
	if incoming.Type != nil {
		result.Type = incoming.Type
	}
	if incoming.Source != nil {
		result.Source = incoming.Source
	}
	if incoming.Resolution != nil {
		result.Resolution = incoming.Resolution
	}
	if incoming.Tag != nil {
		result.Tag = incoming.Tag
	}
	if incoming.Service != nil {
		result.Service = incoming.Service
	}
	if incoming.Edition != nil {
		result.Edition = incoming.Edition
	}
	if incoming.Season != nil {
		result.Season = incoming.Season
	}
	if incoming.Episode != nil {
		result.Episode = incoming.Episode
	}
	if incoming.EpisodeTitle != nil {
		result.EpisodeTitle = incoming.EpisodeTitle
	}
	if incoming.ManualYear != nil {
		result.ManualYear = incoming.ManualYear
	}
	if incoming.ManualDate != nil {
		result.ManualDate = incoming.ManualDate
	}
	if incoming.UseSeasonEpisode != nil {
		result.UseSeasonEpisode = incoming.UseSeasonEpisode
	}
	if incoming.NoSeason != nil {
		result.NoSeason = incoming.NoSeason
	}
	if incoming.NoYear != nil {
		result.NoYear = incoming.NoYear
	}
	if incoming.NoAKA != nil {
		result.NoAKA = incoming.NoAKA
	}
	if incoming.NoTag != nil {
		result.NoTag = incoming.NoTag
	}
	if incoming.NoEdition != nil {
		result.NoEdition = incoming.NoEdition
	}
	if incoming.NoDub != nil {
		result.NoDub = incoming.NoDub
	}
	if incoming.NoDual != nil {
		result.NoDual = incoming.NoDual
	}
	if incoming.DualAudio != nil {
		result.DualAudio = incoming.DualAudio
	}
	if incoming.Region != nil {
		result.Region = incoming.Region
	}
	return result
}

func hasReleaseNameOverrides(overrides api.ReleaseNameOverrides) bool {
	return overrides.Category != nil ||
		overrides.Type != nil ||
		overrides.Source != nil ||
		overrides.Resolution != nil ||
		overrides.Tag != nil ||
		overrides.Service != nil ||
		overrides.Edition != nil ||
		overrides.Season != nil ||
		overrides.Episode != nil ||
		overrides.EpisodeTitle != nil ||
		overrides.ManualYear != nil ||
		overrides.ManualDate != nil ||
		overrides.UseSeasonEpisode != nil ||
		overrides.NoSeason != nil ||
		overrides.NoYear != nil ||
		overrides.NoAKA != nil ||
		overrides.NoTag != nil ||
		overrides.NoEdition != nil ||
		overrides.NoDub != nil ||
		overrides.NoDual != nil ||
		overrides.DualAudio != nil ||
		overrides.Region != nil
}

// applyReleaseNameValueOverrides folds the naming value overrides into the
// metadata itself. They otherwise reach only the release-name string, so every
// consumer that reads the component field keeps the parsed value the user just
// corrected: the UNIT3D payload ids (category_id, type_id, resolution_id,
// season_number, episode_number), the tracker rules, the dupe checks, and the
// trackers that rebuild the name from components instead of ReleaseName (UTP,
// RHD, TVC, ASC).
//
// Only value overrides belong here. The name-suppression toggles (NoSeason,
// NoYear, NoAKA) stay naming-only — dropping season_number from the payload
// because the user hid the season in the name would be a different change.
func applyReleaseNameValueOverrides(meta *api.PreparedMetadata) {
	if meta == nil {
		return
	}
	overrides := meta.ReleaseNameOverrides

	if overrides.Category != nil {
		if category := normalizeNamingCategory(*overrides.Category); category != "" {
			meta.ExternalIDs.Category = category
		}
	}
	if overrides.Type != nil {
		meta.Type = strings.TrimSpace(*overrides.Type)
	}
	if overrides.Source != nil {
		meta.Source = strings.TrimSpace(*overrides.Source)
	}
	if overrides.Resolution != nil {
		meta.Release.Resolution = strings.TrimSpace(*overrides.Resolution)
	}
	if overrides.Service != nil {
		meta.Service = strings.TrimSpace(*overrides.Service)
	}
	if overrides.Region != nil {
		meta.Region = strings.TrimSpace(*overrides.Region)
	}
	if overrides.EpisodeTitle != nil {
		meta.EpisodeTitle = strings.TrimSpace(*overrides.EpisodeTitle)
	}
	if overrides.ManualYear != nil && *overrides.ManualYear > 0 {
		meta.Release.Year = *overrides.ManualYear
	}

	if overrides.Edition != nil {
		meta.Edition = strings.TrimSpace(*overrides.Edition)
	}
	if overrides.NoEdition != nil && *overrides.NoEdition {
		meta.Edition = ""
	}

	if overrides.Season != nil {
		meta.SeasonInt = parseSeasonEpisodeNumber(*overrides.Season)
		meta.SeasonStr = seasonep.FormatSeason(meta.SeasonInt)
	}
	if overrides.Episode != nil {
		meta.EpisodeInt = parseSeasonEpisodeNumber(*overrides.Episode)
		meta.EpisodeStr = seasonep.FormatEpisode(meta.EpisodeInt)
	}

	meta.Audio = applyAudioOverrides(meta.Audio, overrides)

	// Release.Group moves with the tag: resolveGroup (trackers/rules.go,
	// unit3d/additional) prefers it over meta.Tag, so leaving it behind keeps the
	// parsed group in the banned-group and internal-group checks.
	if overrides.Tag != nil {
		tag := strings.TrimSpace(*overrides.Tag)
		if tag != "" && !strings.HasPrefix(tag, "-") {
			tag = "-" + tag
		}
		meta.Tag = tag
		meta.Release.Group = strings.TrimPrefix(tag, "-")
	}
	if overrides.NoTag != nil && *overrides.NoTag {
		meta.Tag = ""
		meta.Release.Group = ""
	}
}

// parseSeasonEpisodeNumber reads the number out of a manual season/episode entry,
// accepting both the "S01"/"E05" tokens the GUI shows and a bare number.
func parseSeasonEpisodeNumber(value string) int {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, strings.TrimSpace(value))
	if digits == "" {
		return 0
	}
	number, err := strconv.Atoi(digits)
	if err != nil {
		return 0
	}
	return number
}

func applyReleaseNameOverrides(req api.ReleaseNameRequest, overrides api.ReleaseNameOverrides, logger api.Logger) api.ReleaseNameRequest {
	if logger == nil {
		logger = api.NopLogger{}
	}

	if overrides.Category != nil {
		req.Category = strings.TrimSpace(*overrides.Category)
	}
	if overrides.Type != nil {
		req.Type = strings.TrimSpace(*overrides.Type)
	}
	if overrides.Source != nil {
		req.Source = strings.TrimSpace(*overrides.Source)
	}
	if overrides.Resolution != nil {
		req.Resolution = strings.TrimSpace(*overrides.Resolution)
	}
	if overrides.Tag != nil {
		tag := strings.TrimSpace(*overrides.Tag)
		if tag != "" && !strings.HasPrefix(tag, "-") {
			tag = "-" + tag
		}
		req.Tag = tag
	}
	if overrides.Service != nil {
		req.Service = strings.TrimSpace(*overrides.Service)
	}
	if overrides.Edition != nil {
		req.Edition = strings.TrimSpace(*overrides.Edition)
	}
	if overrides.Season != nil {
		req.Season = strings.TrimSpace(*overrides.Season)
	}
	if overrides.Episode != nil {
		req.Episode = strings.TrimSpace(*overrides.Episode)
	}
	if overrides.EpisodeTitle != nil {
		req.EpisodeTitle = strings.TrimSpace(*overrides.EpisodeTitle)
	}
	if overrides.ManualYear != nil {
		req.ManualYear = *overrides.ManualYear
	}
	if overrides.ManualDate != nil {
		trimmed := strings.TrimSpace(*overrides.ManualDate)
		req.ManualDate = trimmed != ""
		if trimmed != "" {
			req.DailyDate = trimmed
		}
	}
	if overrides.UseSeasonEpisode != nil {
		if *overrides.UseSeasonEpisode {
			hasManualSeason := overrides.Season != nil && strings.TrimSpace(*overrides.Season) != ""
			hasManualEpisode := overrides.Episode != nil && strings.TrimSpace(*overrides.Episode) != ""
			switch {
			case hasManualSeason || hasManualEpisode:
				req.ManualDate = false
			case req.TMDBDateMatch:
				req.ManualDate = false
			case strings.TrimSpace(req.DailyDate) == "":
				req.ManualDate = false
			default:
				logger.Warnf("metadata: season/episode naming requested but TMDB season/episode not available; keeping daily-date naming")
				req.ManualDate = true
			}
		} else if strings.TrimSpace(req.DailyDate) != "" {
			req.ManualDate = true
		}
	}
	if overrides.NoSeason != nil {
		req.NoSeason = *overrides.NoSeason
	}
	if overrides.NoYear != nil {
		req.NoYear = *overrides.NoYear
	}
	if overrides.NoAKA != nil {
		req.NoAKA = *overrides.NoAKA
	}
	if overrides.NoTag != nil && *overrides.NoTag {
		req.Tag = ""
	}
	if overrides.NoEdition != nil && *overrides.NoEdition {
		req.Edition = ""
	}
	if overrides.Region != nil {
		req.Region = strings.TrimSpace(*overrides.Region)
	}
	req.Audio = applyAudioOverrides(req.Audio, overrides)
	return req
}

func applyAudioOverrides(value string, overrides api.ReleaseNameOverrides) string {
	result := value
	if overrides.NoDub != nil && *overrides.NoDub {
		result = strings.ReplaceAll(result, "Dubbed", "")
		result = strings.ReplaceAll(result, "Dub", "")
	}
	if overrides.NoDual != nil && *overrides.NoDual {
		result = strings.ReplaceAll(result, "Dual-Audio", "")
		result = strings.ReplaceAll(result, "Dual Audio", "")
	}
	if overrides.DualAudio != nil && *overrides.DualAudio {
		lower := strings.ToLower(result)
		if !strings.Contains(lower, "dual") {
			result = strings.TrimSpace(result + " Dual-Audio")
		}
	}
	return strings.TrimSpace(strings.Join(strings.Fields(result), " "))
}
