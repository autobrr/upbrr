// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestIMDbEditionWarningUsesEffectiveEditionAndNearestRuntimeEvidence(t *testing.T) {
	row := func(seconds int, attributes ...string) api.IMDBEditionDetail {
		return api.IMDBEditionDetail{
			Seconds:    seconds,
			Attributes: attributes,
		}
	}
	filmRows := []api.IMDBEditionDetail{row(7920), row(8640, "extended")}
	for _, tc := range []struct {
		name, cut, edition string
		seconds            float64
		dvd, changedTitle  bool
		fps, originalFPS   float64
		rows               []api.IMDBEditionDetail
		want               []string
	}{
		{
			name:    "unmarked theatrical matches IMDb default",
			seconds: 7920,
			rows:    filmRows,
		},
		{
			name:    "unmarked theatrical still checks extended runtime",
			seconds: 8640,
			rows:    filmRows,
			want:    []string{"Theatrical Cut", "Extended Cut (2:24:00)"},
		},
		{
			name:    "filename cut matches equivalent provider wording",
			cut:     "DIRECTOR’S-CUT",
			seconds: 6001,
			rows:    []api.IMDBEditionDetail{row(6000, "Director's Cut", "4K Restoration", "IMAX")},
		},
		{
			name:    "filename edition absent from IMDb still warns",
			edition: "Unrated",
			seconds: 7920,
			rows:    filmRows,
			want:    []string{"Unrated", "Theatrical Cut (2:12:00)"},
		},
		{
			name:    "prefer nearest shorter and longer",
			seconds: 8010,
			rows:    []api.IMDBEditionDetail{row(7920), row(7800, "TV Cut"), row(8640, "extended")},
			want:    []string{"No listed IMDb runtime is within 2 seconds", "Theatrical Cut (2:12:00); Extended Cut (2:24:00)"},
		},
		{
			name:    "when all longer use two closest and discard duplicate",
			seconds: 7500,
			rows:    []api.IMDBEditionDetail{row(8640, "extended"), row(7920), row(7920), row(8040, "Director's Cut")},
			want:    []string{"Theatrical Cut (2:12:00); Director's Cut (2:14:00)"},
		},
		{
			name:    "when all shorter use two closest",
			seconds: 9000,
			rows:    []api.IMDBEditionDetail{row(7920), row(8040, "Director's Cut"), row(8640, "extended")},
			want:    []string{"Extended Cut (2:24:00); Director's Cut (2:14:00)"},
		},
		{
			name:    "PAL DVD presentation normalizes to film timing",
			cut:     "Extended",
			seconds: 8269.760,
			dvd:     true,
			fps:     25,
			rows:    filmRows,
			want:    []string{"2:17:50 PAL (about 2:23:43 at 23.976 fps)", "Extended Cut (2:24:00); Theatrical Cut (2:12:00)"},
		},
		{
			name:    "same title with two runtimes can use a true edition match",
			cut:     "Extended",
			seconds: 827.172,
			dvd:     true,
			fps:     25,
			rows:    []api.IMDBEditionDetail{row(860), row(862, "Extended")},
		},
		{
			name:    "25 fps file speed-up alternative",
			cut:     "Extended",
			seconds: 8286.113,
			fps:     25,
			rows:    filmRows,
			want:    []string{"at 25.000 fps", "at 23.976 fps", "if the cadence difference is a speed change", "consistent with this edition", "Extended Cut (2:24:00)"},
		},
		{
			name:        "23.976 fps file slowdown alternative",
			cut:         "Extended",
			seconds:     8648.64,
			fps:         24000.0 / 1001,
			originalFPS: 24,
			rows:        filmRows,
			want:        []string{"at 23.976 fps", "at 24.000 fps", "consistent with this edition"},
		},
		{
			name:    "raw matching cadence takes precedence for files",
			cut:     "Extended",
			seconds: 8640,
			fps:     25,
			rows:    filmRows,
		},
		{
			name:        "NTSC pulldown must not suggest a speed change",
			seconds:     7920,
			fps:         30000.0 / 1001,
			originalFPS: 24000.0 / 1001,
			dvd:         true,
			rows:        filmRows,
		},
		{
			name:         "effective title changed",
			cut:          "Extended",
			seconds:      7920,
			rows:         filmRows,
			changedTitle: true,
		},
		{
			name:    "near restoration-only evidence is not another cut",
			cut:     "Extended",
			seconds: 6000,
			rows:    []api.IMDBEditionDetail{row(6000, "IMAX", "4K Restoration"), row(7200)},
		},
		{
			name:    "equal runtimes are explicitly ambiguous",
			cut:     "Unrated",
			seconds: 6000,
			rows:    []api.IMDBEditionDetail{row(6000, "extended"), row(6000, "Director's Cut"), row(6000, "extended")},
			want:    []string{"Multiple IMDb editions have this runtime", "Director's Cut (1:40:00); Extended Cut (1:40:00)"},
		},
		{
			name:    "measured and adjusted timings can point to different cuts",
			cut:     "Extended",
			seconds: 8286.113,
			fps:     25,
			rows:    []api.IMDBEditionDetail{row(8286), row(8640, "Extended")},
			want:    []string{"consistent with this edition", "At the measured runtime, IMDb is also close to Theatrical Cut"},
		},
		{
			name: "runtime unavailable",
			cut:  "Extended",
			rows: filmRows,
		},
		{
			name:    "IMDb data unavailable",
			cut:     "Extended",
			seconds: 7920,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := 1234567
			if tc.changedTitle {
				id++
			}
			release := api.PreparedRelease{
				Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie, IMDBID: id},
				Media: api.MediaFacts{
					Cut:                      tc.cut,
					Edition:                  tc.edition,
					EditionRuntimeSeconds:    tc.seconds,
					EditionFrameRate:         tc.fps,
					EditionOriginalFrameRate: tc.originalFPS,
				},
				ProviderMetadata: api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{
					IMDBID:          1234567,
					EditionRuntimes: tc.rows,
				}},
			}
			if tc.dvd {
				release.Disc.Type = "DVD"
			}
			data, err := json.Marshal(release)
			if err != nil {
				t.Fatal(err)
			}
			var restored api.PreparedRelease
			if err := json.Unmarshal(data, &restored); err != nil {
				t.Fatal(err)
			}
			warnings := envelopeFromPersisted(restored, api.PrepareInput{}).result.Diagnostics
			if len(tc.want) == 0 {
				if len(warnings) != 0 {
					t.Fatalf("unexpected warning: %+v", warnings)
				}
				return
			}
			if len(warnings) != 1 || warnings[0].Code != "imdb_edition_mismatch" || warnings[0].Severity != api.DiagnosticSeverityWarning {
				t.Fatalf("missing advisory: %+v", warnings)
			}
			for _, value := range append(tc.want, "https://www.imdb.com/title/tt1234567/technical/", "https://www.imdb.com/title/tt1234567/alternateversions/") {
				if !strings.Contains(warnings[0].Message, value) {
					t.Fatalf("warning missing %q: %s", value, warnings[0].Message)
				}
			}
		})
	}
}
