// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"fmt"
	"strings"
	"time"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
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
	if incoming.NoEpisodeTitle != nil {
		result.NoEpisodeTitle = incoming.NoEpisodeTitle
	}
	if incoming.NoDistributor != nil {
		result.NoDistributor = incoming.NoDistributor
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
		overrides.NoEpisodeTitle != nil ||
		overrides.NoDistributor != nil ||
		overrides.NoEdition != nil ||
		overrides.NoDub != nil ||
		overrides.NoDual != nil ||
		overrides.DualAudio != nil ||
		overrides.Region != nil
}

// validateReleaseNameFactInstructions rejects malformed season, episode, and
// daily-date instruction values with a typed invalid-input error before any
// instruction becomes an effective fact or is persisted for reuse.
func validateReleaseNameFactInstructions(overrides api.ReleaseNameOverrides) error {
	if overrides.Season != nil {
		if _, err := seasonep.ParseSeasonInstruction(*overrides.Season); err != nil {
			return fmt.Errorf("metadata: %w", err)
		}
	}
	if overrides.Episode != nil {
		if _, err := seasonep.ParseEpisodeInstruction(*overrides.Episode); err != nil {
			return fmt.Errorf("metadata: %w", err)
		}
	}
	if overrides.ManualDate != nil {
		if date := strings.TrimSpace(*overrides.ManualDate); date != "" {
			if _, err := time.Parse("2006-01-02", date); err != nil {
				return fmt.Errorf("metadata: daily date instruction %q: expected YYYY-MM-DD: %w", date, internalerrors.ErrInvalidInput)
			}
		}
	}
	return nil
}

// manualSeasonEpisodeInstructionValues returns explicit valid season and
// episode instruction values; zero means no explicit value was supplied.
func manualSeasonEpisodeInstructionValues(overrides api.ReleaseNameOverrides) (int, int) {
	season, episode := 0, 0
	if overrides.Season != nil {
		if value, err := seasonep.ParseSeasonInstruction(*overrides.Season); err == nil {
			season = value
		}
	}
	if overrides.Episode != nil {
		if value, err := seasonep.ParseEpisodeInstruction(*overrides.Episode); err == nil {
			episode = value
		}
	}
	return season, episode
}

// applyReleaseNameValueOverrides folds fact-producing release-name
// instructions into canonical prepared state exactly once, after provider,
// media, and scene evidence resolution and before the final release-name
// rebuild, so the rebuilt name and every downstream fact projection consume
// the same effective values. Naming-only controls (NoSeason, NoYear, NoAKA,
// and the daily-date/season naming mode) stay in applyReleaseNameOverrides;
// category stays on the typed instruction path into canonical identity.
func applyReleaseNameValueOverrides(meta *preparationstate.State) {
	if meta == nil {
		return
	}
	overrides := meta.ReleaseNameOverrides

	if overrides.Type != nil {
		value := strings.TrimSpace(*overrides.Type)
		meta.Type = value
		meta.Release.Type = value
	}
	if overrides.Source != nil {
		value := strings.TrimSpace(*overrides.Source)
		meta.Source = value
		meta.Release.Source = value
	}
	if overrides.Resolution != nil {
		meta.Release.Resolution = strings.TrimSpace(*overrides.Resolution)
	}
	if overrides.Service != nil {
		value := strings.TrimSpace(*overrides.Service)
		service, longName := resolveServiceValue(value)
		if service == "" {
			service = value
		}
		meta.Service = service
		meta.ServiceLongName = longName
	}
	if overrides.Region != nil {
		value := strings.TrimSpace(*overrides.Region)
		meta.Region = value
		meta.Release.Region = value
	}
	if overrides.EpisodeTitle != nil {
		meta.EpisodeTitle = strings.TrimSpace(*overrides.EpisodeTitle)
	}
	if overrides.NoEpisodeTitle != nil && *overrides.NoEpisodeTitle {
		meta.EpisodeTitle = ""
	}
	if overrides.NoDistributor != nil && *overrides.NoDistributor {
		meta.Distributor = ""
	}
	if overrides.ManualYear != nil && *overrides.ManualYear > 0 {
		meta.Release.Year = *overrides.ManualYear
	}
	if overrides.ManualDate != nil {
		if date := strings.TrimSpace(*overrides.ManualDate); date != "" {
			meta.DailyEpisodeDate = date
		}
	}

	if overrides.Edition != nil {
		meta.Edition = strings.TrimSpace(*overrides.Edition)
		meta.Release.Edition = nil
		if meta.Edition != "" {
			meta.Release.Edition = []string{meta.Edition}
		}
	}
	if overrides.NoEdition != nil && *overrides.NoEdition {
		meta.Edition = ""
		meta.Release.Edition = nil
	}

	// Malformed values cannot reach this point: the merged instructions were
	// validated when collection state was built.
	if overrides.Season != nil {
		if season, err := seasonep.ParseSeasonInstruction(*overrides.Season); err == nil {
			meta.SeasonInt = season
			meta.SeasonStr = seasonep.FormatSeason(season)
		}
	}
	if overrides.Episode != nil {
		if episode, err := seasonep.ParseEpisodeInstruction(*overrides.Episode); err == nil {
			meta.EpisodeInt = episode
			meta.EpisodeStr = seasonep.FormatEpisode(episode)
		}
	}

	meta.Audio = applyAudioOverrides(meta.Audio, overrides)

	// Release.Group moves with the tag: group consumers such as banned-group
	// and internal-group checks prefer it over meta.Tag, so leaving it behind
	// would keep the parsed group in effect.
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

// applyReleaseNameOverrides applies naming-only controls to one release-name
// request. Fact-producing values are folded into canonical prepared state by
// applyReleaseNameValueOverrides before the final rebuild, so the request
// already carries them through the regular fact fields.
func applyReleaseNameOverrides(req api.ReleaseNameRequest, overrides api.ReleaseNameOverrides, logger api.Logger) api.ReleaseNameRequest {
	if logger == nil {
		logger = api.NopLogger{}
	}

	if overrides.Category != nil {
		req.Category = strings.TrimSpace(*overrides.Category)
	}
	if overrides.EpisodeTitle != nil {
		req.EpisodeTitle = strings.TrimSpace(*overrides.EpisodeTitle)
		req.ManualEpisodeTitle = true
	}
	if overrides.ManualDate != nil {
		req.ManualDate = strings.TrimSpace(*overrides.ManualDate) != ""
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
	if overrides.NoEpisodeTitle != nil && *overrides.NoEpisodeTitle {
		req.EpisodeTitle = ""
	}
	if overrides.NoEdition != nil && *overrides.NoEdition {
		req.Edition = ""
	}
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
