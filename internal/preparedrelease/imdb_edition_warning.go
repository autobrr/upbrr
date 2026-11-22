// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/pkg/api"
)

const imdbRuntimeMatchSeconds = 2

type imdbRuntimeCandidate struct {
	label   string
	seconds int
}

// imdbEditionDiagnostics compares effective movie naming facts with IMDb
// runtime rows without changing the name, selection or upload eligibility.
func imdbEditionDiagnostics(release api.PreparedRelease) []api.PreparationDiagnostic {
	imdb := release.ProviderMetadata.IMDB
	duration := release.Media.EditionRuntimeSeconds
	if release.Identity.Category != api.CanonicalCategoryMovie || imdb == nil || release.Identity.IMDBID <= 0 ||
		imdb.IMDBID != release.Identity.IMDBID || duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
		return nil
	}

	expected := strings.TrimSpace(release.Media.Cut)
	if expected == "" {
		expected = strings.TrimSpace(release.Media.Edition)
	}
	if expected == "" {
		expected = "Theatrical Cut"
	}
	comparison := duration
	runtime := imdbRuntimeText(duration)
	if release.Media.EditionRuntimePAL {
		comparison *= 25 * 1001.0 / 24000
		runtime += " PAL (about " + imdbRuntimeText(comparison) + " at 23.976 fps)"
	}

	candidates := make([]imdbRuntimeCandidate, 0, len(imdb.EditionRuntimes))
	seen := make(map[imdbRuntimeCandidate]bool, len(imdb.EditionRuntimes))
	for _, row := range imdb.EditionRuntimes {
		if row.Seconds <= 0 {
			continue
		}
		candidate := imdbRuntimeCandidate{label: imdbRuntimeLabel(row.Attributes), seconds: row.Seconds}
		if imdbEditionKey(candidate.label) == imdbEditionKey(expected) &&
			math.Abs(comparison-float64(candidate.seconds)) <= imdbRuntimeMatchSeconds {
			return nil
		}
		if !seen[candidate] {
			seen[candidate] = true
			candidates = append(candidates, candidate)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		da, db := math.Abs(comparison-float64(a.seconds)), math.Abs(comparison-float64(b.seconds))
		if da != db {
			return da < db
		}
		if a.seconds != b.seconds {
			return a.seconds < b.seconds
		}
		return a.label < b.label
	})
	closest := candidates[:min(2, len(candidates))]
	reason := "The closest IMDb runtime does not confirm this edition."
	if math.Abs(comparison-float64(candidates[0].seconds)) > imdbRuntimeMatchSeconds {
		reason = "No listed IMDb runtime is within 2 seconds."
		var shorter, longer imdbRuntimeCandidate
		for _, candidate := range candidates {
			if float64(candidate.seconds) < comparison && shorter.seconds == 0 {
				shorter = candidate
			}
			if float64(candidate.seconds) > comparison && longer.seconds == 0 {
				longer = candidate
			}
		}
		if shorter.seconds != 0 && longer.seconds != 0 {
			closest = []imdbRuntimeCandidate{shorter, longer}
			if candidates[0] == longer {
				closest[0], closest[1] = closest[1], closest[0]
			}
		}
	}

	summaries := make([]string, 0, len(closest))
	for _, candidate := range closest {
		summaries = append(summaries, fmt.Sprintf("%s (%s)", candidate.label, imdbRuntimeText(float64(candidate.seconds))))
	}
	base := "https://www.imdb.com/title/" + providerid.IMDb(release.Identity.IMDBID).Prefixed()
	return []api.PreparationDiagnostic{{
		Code:     "imdb_edition_mismatch",
		Severity: api.DiagnosticSeverityWarning,
		Message: fmt.Sprintf("Edition %q, source runtime %s. %s Closest IMDb options: %s. Your selection is unchanged.\n%s/technical/\n%s/alternateversions/",
			expected, runtime, reason, strings.Join(summaries, "; "), base, base),
	}}
}

func imdbRuntimeText(seconds float64) string {
	value := int(math.Round(seconds))
	return fmt.Sprintf("%d:%02d:%02d", value/3600, value/60%60, value%60)
}

// Treat a plain runtime as IMDb's default theatrical candidate. Preserve
// unknown attributes as display evidence without interpreting them as cuts.
func imdbRuntimeLabel(attributes []string) string {
	var label string
	for _, attribute := range attributes {
		if cut := imdbEditorialCut(attribute); cut != "" {
			if label == "" || label == cut {
				label = cut
				continue
			}
			return strings.Join(attributes, ", ")
		}
		separated := api.SeparateEditionOverride(attribute)
		if *separated.Edition != "" || (separated.Presentation == nil && separated.ReleaseFeatures == nil) {
			return strings.Join(attributes, ", ")
		}
	}
	if label != "" {
		return label
	}
	if len(attributes) == 0 {
		return "Theatrical Cut"
	}
	return strings.Join(attributes, ", ")
}

func imdbEditionKey(label string) string {
	if cut := imdbEditorialCut(label); cut != "" {
		label = cut
	}
	return imdbLabelKey(label)
}

func imdbLabelKey(label string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) || unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, label)
}

// imdbEditorialCut matches only complete editorial labels. Presentation,
// restoration, region and other attributes may coexist with any cut.
func imdbEditorialCut(label string) string {
	switch imdbLabelKey(label) {
	case "theatrical", "theatricalcut", "theatricaledition", "theatricalversion":
		return "Theatrical Cut"
	case "extended", "extendedcut", "extendededition", "extendedversion":
		return "Extended Cut"
	case "directorscut":
		return "Director's Cut"
	case "finalcut":
		return "Final Cut"
	case "uncut":
		return "Uncut"
	case "unrated":
		return "Unrated"
	default:
		return ""
	}
}
