// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata/imdb"
	"github.com/autobrr/upbrr/internal/metadata/tvdb"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPreparedTVDBExplicitNamingYear(t *testing.T) {
	registry := trackerimpl.MustNewRegistry()
	for _, scenario := range []struct {
		name, translation                string
		collision, unavailable, wantYear bool
	}{
		{
			name:        "title",
			translation: `{"name":"Example Series (2024)","aliases":[]}`,
			wantYear:    true,
		},
		{
			name:        "alias",
			translation: `{"name":"Example Series","aliases":["Example Series (2024)"]}`,
			wantYear:    true,
		},
		{name: "premiere only", translation: `{"name":"Example Series","aliases":[]}`},
		{
			name:        "collision",
			translation: `{"name":"Example Series (2024)","aliases":[]}`,
			collision:   true,
			wantYear:    true,
		},
		{
			name:        "unavailable",
			translation: `{"name":"Example Series (2024)","aliases":[]}`,
			unavailable: true,
			wantYear:    true,
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fixture := newCorrectionEvidenceFixture(t)
			fixture.input.Instructions.Identity.TMDBID = nil
			fixture.input.Instructions.Identity.TVDBID = nil
			fixture.input.Instructions.Identity.IMDBID = nil
			fixture.imdb = &stubIMDB{info: imdb.Info{
				IMDbID: "tt0555004",
				Title:  "Example Series",
				Type:   "tvSeries",
			}}
			transport := fixture.http.Transport
			fixture.http.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
				response, err := transport.RoundTrip(request)
				if err != nil {
					return response, fmt.Errorf("fixture HTTP: %w", err)
				}
				body := ""
				switch request.URL.Path {
				case "/3/tv/555001/external_ids":
					body = `{"tvdb_id":555003,"imdb_id":"tt0555004"}`
				case "/v4/series/555003/translations/eng":
					body = `{"data":` + scenario.translation + `}`
				case "/v4/search":
					if scenario.unavailable {
						_ = response.Body.Close()
						return nil, io.ErrUnexpectedEOF
					}
					if scenario.collision {
						body = `{"data":[{"tvdb_id":"555009","name":"Example Series","primary_language":"eng","year":"2020"}]}`
					}
				}
				if body != "" {
					_ = response.Body.Close()
					response.Body = io.NopCloser(strings.NewReader(body))
				}
				return response, nil
			})
			fixture.tvdb = tvdb.NewClient(fixture.http, nil, "synthetic-key", "")
			module, repo := fixture.open(t)
			input := fixture.input
			var previous api.PreparedGeneration
			for _, step := range []string{"prepare", "refresh", "SQLite reopen", "No year", "refresh No year", "reopen No year", "Auto", "prepared-release-v56", "prepared-release-v57", "prepared-release-v58"} {
				update := api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit}
				switch step {
				case "refresh", "refresh No year":
					input.ExternalFreshness = api.ExternalFreshnessRefresh
				case "SQLite reopen", "reopen No year":
					if err := repo.Close(); err != nil {
						t.Fatal(err)
					}
					module, repo = fixture.open(t)
					input.ExternalFreshness = api.ExternalFreshnessLoad
				case "No year":
					update = api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{ReleaseName: api.ReleaseNameOverrides{NoYear: new(true)}}}}
				case "Auto":
					update = api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{ResetFields: []api.CorrectionFieldRef{{Field: api.CorrectionFieldReleaseNameNoYear}}}}
				case "prepared-release-v56", "prepared-release-v57", "prepared-release-v58":
					old, err := repo.LoadPreparedRelease(t.Context(), input.SourcePath)
					if err != nil {
						t.Fatal(err)
					}
					old.Compatibility.ContractVersion = step
					old.ProviderMetadata.TVDB.NameDisambiguation.IncludeYear = false
					if err := repo.CommitPreparedRelease(t.Context(), old); err != nil {
						t.Fatal(err)
					}
					if err := repo.Close(); err != nil {
						t.Fatal(err)
					}
					module, repo = fixture.open(t)
				}
				result := prepareCorrectionEvidence(t, module, input, update)
				input = api.PrepareInput{SourcePath: input.SourcePath, ExternalFreshness: api.ExternalFreshnessReuse}
				if result.Release.Identity.TVDBID != 555003 || result.Release.Identity.IMDBID != 555004 {
					t.Fatal("linked provider identity was not acquired with TMDB enabled")
				}
				if strings.HasPrefix(step, "prepared-release-v") && result.Release.Generation <= previous {
					t.Fatal("reused a pre-fix prepared decision")
				}
				previous = result.Release.Generation
				wantYear := scenario.wantYear && !strings.Contains(step, "No year")
				if strings.Contains(result.Release.Naming.ReleaseName, " 2024 ") != wantYear {
					t.Errorf("%s: canonical name=%q", step, result.Release.Naming.ReleaseName)
				}
				subject, err := module.ResolveUploadSubject(t.Context(), api.UploadSubjectInput{Release: api.ReleaseRef{SourcePath: input.SourcePath, Generation: result.Release.Generation}})
				if err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"AITHER", "DP", "HHD", "LUME", "ULCX", "YUS", "BHD", "LST"} {
					descriptor, _ := registry.LookupDescriptor(name)
					prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{Tracker: name, Meta: subject}, descriptor.ReleaseNamePolicy)
					if failure != nil {
						t.Fatalf("%s/%s: %v: %v", step, name, failure, failure.Unwrap())
					}
					reviewed, err := prepared.ReviewedUploadName()
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(reviewed, " 2024 ") != wantYear || strings.Contains(reviewed, " 2026 ") {
						t.Errorf("%s/%s: reviewed=%q, want year=%t", step, name, reviewed, wantYear)
					}
					if prepared.Projection.DuplicateCriteria.Name != reviewed {
						t.Errorf("%s/%s: search and reviewed names differ", step, name)
					}
				}
				if result.Release.Compatibility.ContractVersion != preparedrelease.ContractVersion {
					t.Fatal("incompatible prepared contract")
				}
			}
		})
	}
}
