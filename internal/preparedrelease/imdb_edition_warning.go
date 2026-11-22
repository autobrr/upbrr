// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"fmt"
	"math"
	"strings"
	"unicode"

	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/pkg/api"
)

// imdbEditionDiagnostics compares a recognized, explicitly selected movie cut
// to labeled runtime evidence for the effective title and exact generation.
func imdbEditionDiagnostics(release api.PreparedRelease, instructions api.ReleaseFactInstructions) []api.PreparationDiagnostic {
	overrides := instructions.ReleaseName
	imdb := release.ProviderMetadata.IMDB
	if overrides.Edition == nil || (overrides.NoEdition != nil && *overrides.NoEdition) ||
		release.Identity.Category != "MOVIE" || imdb == nil || release.Identity.IMDBID <= 0 ||
		imdb.IMDBID != release.Identity.IMDBID {
		return nil
	}
	selected := imdbEditorialCut(*overrides.Edition)
	duration := release.Media.EditionRuntimeSeconds
	if selected == "" || duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
		return nil
	}

	// Require an essentially exact labeled match. A nearby different or
	// unlabeled runtime is ambiguous, including alternatives in the same minute.
	var observed string
	var exact, selectedListed bool
	for _, row := range imdb.EditionRuntimes {
		if row.Seconds <= 0 {
			continue
		}
		cut := ""
		if len(row.Attributes) == 1 {
			cut = imdbEditorialCut(row.Attributes[0])
		}
		delta := math.Abs(duration - float64(row.Seconds))
		if delta > 50 {
			selectedListed = selectedListed || cut == selected
			continue
		}
		if cut == "" || cut == selected || observed != "" && cut != observed {
			return nil
		}
		observed = cut
		exact = exact || delta <= 2
	}
	if !exact || !selectedListed {
		return nil
	}
	base := "https://www.imdb.com/title/" + providerid.IMDb(release.Identity.IMDBID).Prefixed()
	return []api.PreparationDiagnostic{{
		Code:     "imdb_edition_mismatch",
		Severity: api.DiagnosticSeverityWarning,
		Message: fmt.Sprintf("Selected edition %q differs from IMDb's %q at this runtime. Your selection is unchanged.\n%s/technical/\n%s/alternateversions/",
			strings.TrimSpace(*overrides.Edition), observed, base, base),
	}}
}

// imdbEditorialCut matches only complete editorial labels. Presentation,
// restoration, region and other attributes may coexist with any cut.
func imdbEditorialCut(label string) string {
	key := strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) || unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, label)
	switch key {
	case "theatrical", "theatricalcut", "theatricaledition", "theatricalversion":
		return "Theatrical Cut"
	case "extended", "extendedcut", "extendededition", "extendedversion":
		return "Extended Cut"
	case "directorscut":
		return "Director's Cut"
	case "finalcut":
		return "Final Cut"
	default:
		return ""
	}
}
