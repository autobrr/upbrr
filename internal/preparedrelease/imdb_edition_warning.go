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

const imdbRuntimeMatchSeconds = 30

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
	reference, preferAdjusted := imdbEditionReferenceRate(release)
	comparison, adjusted := duration, duration
	if reference > 0 {
		adjusted = duration * release.Media.EditionFrameRate / reference
	}
	runtime := imdbRuntimeText(duration)
	candidates := make([]imdbRuntimeCandidate, 0, len(imdb.EditionRuntimes))
	seen := make(map[imdbRuntimeCandidate]bool, len(imdb.EditionRuntimes))
	expectedKey := imdbEditionKey(expected)
	rawDistance, adjustedDistance := math.Inf(1), math.Inf(1)
	rawMatch, adjustedMatch := false, false
	var rawNearest imdbRuntimeCandidate
	var rawUnlabeledMatch, adjustedUnlabeledMatch bool
	for _, row := range imdb.EditionRuntimes {
		if row.Seconds <= 0 {
			continue
		}
		candidate := imdbRuntimeCandidate{label: imdbRuntimeLabel(row.Attributes), seconds: row.Seconds}
		rawDelta := math.Abs(duration - float64(row.Seconds))
		adjustedDelta := math.Abs(adjusted - float64(row.Seconds))
		if candidate.label == "" {
			rawUnlabeledMatch = rawUnlabeledMatch || rawDelta <= imdbRuntimeMatchSeconds
			adjustedUnlabeledMatch = adjustedUnlabeledMatch || adjustedDelta <= imdbRuntimeMatchSeconds
			continue
		}
		if rawDelta < rawDistance {
			rawDistance, rawNearest = rawDelta, candidate
		}
		adjustedDistance = min(adjustedDistance, adjustedDelta)
		if imdbEditionKey(candidate.label) == expectedKey {
			rawMatch = rawMatch || rawDelta <= imdbRuntimeMatchSeconds
			adjustedMatch = adjustedMatch || adjustedDelta <= imdbRuntimeMatchSeconds
		}
		if !seen[candidate] {
			seen[candidate] = true
			candidates = append(candidates, candidate)
		}
	}
	if len(candidates) == 0 || preferAdjusted && (adjustedMatch || adjustedUnlabeledMatch) ||
		!preferAdjusted && (rawMatch || rawUnlabeledMatch) {
		return nil
	}
	useAdjusted := reference > 0 && (preferAdjusted || adjustedMatch || !adjustedUnlabeledMatch && adjustedDistance < rawDistance)
	if useAdjusted {
		comparison = adjusted
		if preferAdjusted {
			runtime += fmt.Sprintf(" PAL (about %s at %.3f fps)", imdbRuntimeText(adjusted), reference)
		} else {
			runtime += fmt.Sprintf(" at %.3f fps (about %s at %.3f fps if the cadence difference is a speed change)",
				release.Media.EditionFrameRate, imdbRuntimeText(adjusted), reference)
		}
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
	if useAdjusted && adjustedMatch {
		reason = "The frame-rate-adjusted runtime is consistent with this edition; the measured runtime does not confirm it."
	}
	nearCuts := make(map[string]struct{})
	for _, candidate := range candidates {
		if math.Abs(comparison-float64(candidate.seconds)) > imdbRuntimeMatchSeconds {
			break
		}
		nearCuts[imdbEditionKey(candidate.label)] = struct{}{}
	}
	if len(nearCuts) > 1 {
		reason = "Multiple IMDb editions have this runtime; duration alone cannot distinguish them."
	} else if useAdjusted && !preferAdjusted && rawDistance <= imdbRuntimeMatchSeconds &&
		imdbEditionKey(rawNearest.label) != imdbEditionKey(candidates[0].label) {
		reason += fmt.Sprintf(" At the measured runtime, IMDb is also close to %s (%s).", rawNearest.label, imdbRuntimeText(float64(rawNearest.seconds)))
	}
	if len(nearCuts) == 0 {
		reason = fmt.Sprintf("No listed IMDb runtime is within %d seconds.", imdbRuntimeMatchSeconds)
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

// imdbEditionReferenceRate chooses a reported original cadence, or a common
// film cadence as an explicitly labelled hypothesis. NTSC DVD pulldown is
// cadence conversion, not a change in playback duration.
func imdbEditionReferenceRate(release api.PreparedRelease) (reference float64, preferAdjusted bool) {
	const filmNTSC = 24000.0 / 1001
	fps := release.Media.EditionFrameRate
	if fps <= 0 || math.IsNaN(fps) || math.IsInf(fps, 0) {
		return 0, false
	}
	dvd := strings.EqualFold(release.Disc.Type, "DVD")
	if dvd && (math.Abs(fps-30000.0/1001) < 0.001 || math.Abs(fps-filmNTSC) < 0.001) {
		return 0, false
	}
	if dvd && fps == 25 {
		return filmNTSC, true
	}
	reference = release.Media.EditionOriginalFrameRate
	if reference <= 0 || math.IsNaN(reference) || math.IsInf(reference, 0) {
		reference = filmNTSC
		if math.Abs(fps-filmNTSC) < 0.001 {
			reference = 24
		}
	}
	if math.Abs(reference-fps) < 0.001 {
		return 0, false
	}
	return reference, false
}

func imdbRuntimeText(seconds float64) string {
	value := int(math.Round(seconds))
	return fmt.Sprintf("%d:%02d:%02d", value/3600, value/60%60, value%60)
}

// imdbRuntimeLabel treats a plain row as IMDb's default theatrical candidate.
// Presentation-only or mastering-only rows do not identify another cut.
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
	return ""
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
