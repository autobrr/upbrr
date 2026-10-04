// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

// sourceSeasonDiagnostics uses the same file list and season parser as tracker
// validation. Known release seasons guide the sample, never the warning itself.
// Otherwise the most represented season is the baseline; ties prefer regular
// seasons over specials. A zero season remains unknown.
func sourceSeasonDiagnostics(owned envelope) []api.PreparationDiagnostic {
	files := owned.resources.fileList
	if len(files) == 0 {
		files = manifestFiles(owned.result.Release.Source)
	}
	bySeason := make(map[int][]string)
	seen := make(map[string]bool)
	for _, file := range files {
		file = filepath.Clean(strings.TrimSpace(file))
		if seen[file] {
			continue
		}
		seen[file] = true
		if season, ok := api.DetectedFileSeason(file); ok {
			bySeason[season] = append(bySeason[season], file)
		}
	}
	if len(bySeason) < 2 {
		return nil
	}
	seasons := make([]int, 0, len(bySeason))
	for season := range bySeason {
		seasons = append(seasons, season)
	}
	slices.Sort(seasons)
	baseline := seasons[0]
	labels := make([]string, 0, len(seasons))
	for _, season := range seasons {
		labels = append(labels, fmt.Sprintf("S%02d", season))
		if len(bySeason[season]) > len(bySeason[baseline]) || (baseline == 0 && len(bySeason[season]) == len(bySeason[baseline])) {
			baseline = season
		}
	}
	episode := owned.result.Release.Episode
	if episode.Season > 0 && len(bySeason[episode.Season]) > 0 {
		baseline = episode.Season
	}
	message := fmt.Appendf(nil,
		"Multiple seasons detected: %s. Trackers that do not allow multiple seasons may reject this release during duplicate checking.",
		strings.Join(labels, ", "),
	)
	const sampleLimit = 5
	shown, total := 0, 0
	source := owned.result.Release.Source.SourcePath
	for _, season := range seasons {
		if season == baseline {
			continue
		}
		candidates := bySeason[season]
		slices.Sort(candidates)
		total += len(candidates)
		for _, file := range candidates {
			if shown == sampleLimit {
				break
			}
			label := filepath.Base(file)
			if pathing.IsWithinRoot(source, file) {
				if relative, err := filepath.Rel(source, file); err == nil {
					label = relative
				}
			}
			message = fmt.Appendf(message, "\nAdditional season file: %q — S%02d", label, season)
			shown++
		}
	}
	if omitted := total - shown; omitted > 0 {
		message = fmt.Appendf(message, "\n%d additional season files omitted.", omitted)
	}
	return []api.PreparationDiagnostic{{
		Code:     "multiple_source_seasons",
		Severity: api.DiagnosticSeverityWarning,
		Message:  string(message),
	}}
}
