// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rtf

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const minimumContentAgeReason = "content must be at least 10 years and 1 month old"

// validationPolicy strictly blocks content newer than RTF's ten-year-and-one-month
// eligibility cutoff and adult-classified releases.
func validationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{ID: "standalone-rtf-constructibility-v3", Check: checkRules}
}

func checkRules(ctx context.Context, meta api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context canceled: %w", err)
	}
	failures := make([]api.RuleFailure, 0, 2)
	if trackers.AdultContent(trackers.RuleSubjectFromValidation(meta)) {
		failures = append(failures, trackers.NewRuleFailure(
			"block_adult",
			"adult content is not allowed",
			api.RuleDispositionStrict,
		))
	}
	if minimumContentAgeViolation(meta, time.Now().UTC()) {
		failures = append(failures, trackers.NewRuleFailure(
			"minimum_content_age",
			minimumContentAgeReason,
			api.RuleDispositionStrict,
		))
	}
	return failures, nil
}

func minimumContentAgeViolation(meta api.TrackerValidationSubject, now time.Time) bool {
	return rtfContentAgeEligibility(meta.Release, meta.ProviderMetadata, meta.EffectiveMetadata, now) != rtfAgeEligible
}

type rtfAgeEligibilityVerdict string

const (
	rtfAgeEligible                  rtfAgeEligibilityVerdict = "eligible"
	rtfAgeIneligibleMissingEvidence rtfAgeEligibilityVerdict = "missing_evidence"
	rtfAgeIneligibleBoundaryYear    rtfAgeEligibilityVerdict = "boundary_year"
	rtfAgeIneligibleTooNew          rtfAgeEligibilityVerdict = "too_new"
)

// rtfContentAgeEligibility retains exact provider dates but not partial provider years when year is manual.
func rtfContentAgeEligibility(
	release api.ReleaseInfo,
	metadata api.SourceScopedMetadata,
	effective api.EffectiveMetadata,
	now time.Time,
) rtfAgeEligibilityVerdict {
	cutoff := now.UTC().AddDate(-10, -1, 0)
	evidence := youngestRTFReleaseEvidence(release, metadata, effective)
	if releaseDate, ok := evidence.exactDate(); ok {
		if releaseDate.After(cutoff) {
			return rtfAgeIneligibleTooNew
		}
		return rtfAgeEligible
	}

	switch {
	case evidence.year == 0:
		return rtfAgeIneligibleMissingEvidence
	case evidence.year < cutoff.Year():
		return rtfAgeEligible
	case evidence.year == cutoff.Year():
		return rtfAgeIneligibleBoundaryYear
	default:
		return rtfAgeIneligibleTooNew
	}
}

type rtfReleaseAgeEvidence struct {
	date time.Time
	year int
}

func (e *rtfReleaseAgeEvidence) addDate(value time.Time) {
	if value.IsZero() {
		return
	}
	value = value.UTC()
	date := time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
	if e.date.IsZero() || date.After(e.date) {
		e.date = date
	}
	e.addYear(date.Year())
}

func (e *rtfReleaseAgeEvidence) addYear(value int) {
	if value > e.year {
		e.year = value
	}
}

func (e *rtfReleaseAgeEvidence) exactDate() (time.Time, bool) {
	if e.date.IsZero() || e.year > e.date.Year() {
		return time.Time{}, false
	}
	return e.date, true
}

func youngestRTFReleaseEvidence(
	release api.ReleaseInfo,
	metadata api.SourceScopedMetadata,
	effective api.EffectiveMetadata,
) rtfReleaseAgeEvidence {
	var evidence rtfReleaseAgeEvidence
	includePartialYears := !effective.YearProvenance.IsManual()
	if !includePartialYears {
		evidence.addYear(effective.Year)
	}
	addRTFDateParts(&evidence, release.Year, release.Month, release.Day, includePartialYears)

	if value := metadata.TMDB; value != nil {
		if includePartialYears {
			evidence.addYear(value.Year)
		}
		addRTFDateText(&evidence, value.ReleaseDate, includePartialYears)
		addRTFDateText(&evidence, value.FirstAirDate, includePartialYears)
		addRTFDateText(&evidence, value.LastAirDate, includePartialYears)
	}
	if value := metadata.IMDB; value != nil {
		if includePartialYears {
			evidence.addYear(value.Year)
			evidence.addYear(value.EndYear)
			evidence.addYear(value.TVYear)
		}
		for _, episode := range value.Episodes {
			addRTFDateParts(
				&evidence,
				episode.ReleaseDate.Year,
				episode.ReleaseDate.Month,
				episode.ReleaseDate.Day,
				includePartialYears,
			)
			if includePartialYears {
				evidence.addYear(episode.ReleaseYear)
			}
		}
	}
	if value := metadata.TVDB; value != nil {
		if includePartialYears {
			evidence.addYear(value.Year)
		}
		addRTFDateText(&evidence, value.FirstAired, includePartialYears)
		addRTFDateText(&evidence, value.EpisodeAired, includePartialYears)
		for _, episode := range value.Episodes {
			addRTFDateText(&evidence, episode.EpisodeAired, includePartialYears)
		}
	}
	if value := metadata.TVmaze; value != nil {
		addRTFDateText(&evidence, value.Premiered, includePartialYears)
		addRTFDateText(&evidence, value.Ended, includePartialYears)
	}
	if value := metadata.AniList; value != nil {
		if includePartialYears {
			evidence.addYear(value.SeasonYear)
		}
		addRTFDateText(&evidence, value.StartDate, includePartialYears)
		addRTFDateText(&evidence, value.EndDate, includePartialYears)
		if value.NextAiringEpisode.AiringAt > 0 {
			evidence.addDate(time.Unix(int64(value.NextAiringEpisode.AiringAt), 0))
		}
	}

	return evidence
}

func addRTFDateParts(evidence *rtfReleaseAgeEvidence, year, month, day int, includePartialYear bool) {
	if year <= 0 || month <= 0 || day <= 0 {
		if includePartialYear {
			evidence.addYear(year)
		}
		return
	}
	value := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if value.Year() != year || int(value.Month()) != month || value.Day() != day {
		return
	}
	evidence.addDate(value)
}

func addRTFDateText(evidence *rtfReleaseAgeEvidence, raw string, includePartialYear bool) {
	if value, ok := parseRTFDate(raw); ok {
		evidence.addDate(value)
		return
	}
	if includePartialYear {
		evidence.addYear(parseRTFYear(raw))
	}
}

func parseRTFDate(raw string) (time.Time, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339} {
		parsed, err := time.Parse(layout, value)
		if err != nil {
			continue
		}
		return parsed, true
	}
	return time.Time{}, false
}

func parseRTFYear(raw string) int {
	value := strings.TrimSpace(raw)
	if len(value) < 4 || (len(value) > 4 && value[4] != '-') {
		return 0
	}
	year, err := strconv.Atoi(value[:4])
	if err != nil || year <= 0 {
		return 0
	}
	return year
}
