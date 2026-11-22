// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestIMDbEditionWarningUsesCurrentSelectionAndCertainRuntimeEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, selected string
		rows           []api.IMDBEditionDetail
		changedIMDbID  int
		want           bool
	}{
		{"mismatch survives restore", "Extended", nil, 0, true},
		{"equivalent punctuation", "DIRECTOR’S-CUT", nil, 0, false},
		{"selection cleared", "", nil, 0, false},
		{"effective title changed", "Extended", nil, 1234568, false},
		{"unknown qualifier is not another cut", "IMAX", nil, 0, false},
		{"another cut in same minute", "Extended", []api.IMDBEditionDetail{{Seconds: 6006, Attributes: []string{"Theatrical"}}}, 0, false},
		{"unlabeled near match is ambiguous", "Extended", []api.IMDBEditionDetail{{Seconds: 6000}}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := api.PreparedRelease{
				Identity: api.ExternalIdentity{Category: "MOVIE", IMDBID: 1234567},
				Media:    api.MediaFacts{EditionRuntimeSeconds: 6000.5},
				ProviderMetadata: api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{
					IMDBID: 1234567,
					EditionRuntimes: append([]api.IMDBEditionDetail{
						{Seconds: 6000, Attributes: []string{"Director's Cut"}},
						{Seconds: 6350, Attributes: []string{"extended version"}},
					}, tc.rows...),
				}},
			}
			if tc.changedIMDbID > 0 {
				release.Identity.IMDBID = tc.changedIMDbID
			}
			data, err := json.Marshal(release)
			if err != nil {
				t.Fatal(err)
			}
			var restored api.PreparedRelease
			if err := json.Unmarshal(data, &restored); err != nil {
				t.Fatal(err)
			}
			input := api.PrepareInput{}
			input.Instructions.ReleaseName.Edition = new(tc.selected)
			warnings := envelopeFromPersisted(restored, input).result.Diagnostics
			if !tc.want {
				if len(warnings) != 0 {
					t.Fatalf("unexpected warning: %+v", warnings)
				}
				return
			}
			if len(warnings) != 1 || warnings[0].Code != "imdb_edition_mismatch" || warnings[0].Severity != api.DiagnosticSeverityWarning {
				t.Fatalf("missing advisory: %+v", warnings)
			}
			for _, value := range []string{"Extended", "Director's Cut", "https://www.imdb.com/title/tt1234567/technical/", "https://www.imdb.com/title/tt1234567/alternateversions/"} {
				if !strings.Contains(warnings[0].Message, value) {
					t.Fatalf("warning missing %q: %s", value, warnings[0].Message)
				}
			}
		})
	}
}
