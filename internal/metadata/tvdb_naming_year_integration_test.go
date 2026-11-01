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
			name:        "collision without title year",
			translation: `{"name":"Example Series","aliases":[]}`,
			collision:   true,
			wantYear:    true,
		},
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
			var keptDependencies api.IdentityDependency
			for _, step := range []string{
				"prepare", "refresh", "SQLite reopen", "No year", "refresh No year", "reopen No year", "Auto", "Manual year", "Empty year",
				"prepared-release-v56", "prepared-release-v57", "prepared-release-v58", "prepared-release-v61", "prepared-release-v66", "prepared-release-v67",
				"Keep TVDB", "kept v66", "kept SQLite reopen", "kept No year", "kept Auto",
			} {
				update := api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit}
				beforeRequests := fixture.requests()
				switch step {
				case "refresh", "refresh No year":
					input.ExternalFreshness = api.ExternalFreshnessRefresh
				case "SQLite reopen", "reopen No year", "kept SQLite reopen":
					if err := repo.Close(); err != nil {
						t.Fatal(err)
					}
					module, repo = fixture.open(t)
					input.ExternalFreshness = api.ExternalFreshnessLoad
				case "No year", "kept No year":
					update = api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{ReleaseName: api.ReleaseNameOverrides{NoYear: new(true)}}}}
				case "Auto", "kept Auto":
					update = api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{ResetFields: []api.CorrectionFieldRef{{Field: api.CorrectionFieldReleaseNameNoYear}}}}
				case "Manual year", "Empty year":
					year := 2030
					if step == "Empty year" {
						year = 0
					}
					update = api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{
						Values: api.ReleaseCorrectionValues{ReleaseName: api.ReleaseNameOverrides{ManualYear: new(year)}},
					}}
				case "Keep TVDB":
					update = api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{
						Values: api.ReleaseCorrectionValues{Identity: api.ExternalIDOverrides{KeepTVDB: new(true)}},
					}}
				case "prepared-release-v56", "prepared-release-v57", "prepared-release-v58", "prepared-release-v61", "prepared-release-v66", "prepared-release-v67", "kept v66":
					old, err := repo.LoadPreparedRelease(t.Context(), input.SourcePath)
					if err != nil {
						t.Fatal(err)
					}
					old.Compatibility.ContractVersion = step
					if step == "kept v66" {
						old.Compatibility.ContractVersion = "prepared-release-v66"
					}
					if scenario.name != "collision without title year" {
						old.ProviderMetadata.TVDB.NameDisambiguation.IncludeYear = false
					}
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
					t.Fatalf("%s: linked provider identity was not acquired with TMDB enabled: %#v", step, result.Release.Identity)
				}
				if (strings.HasPrefix(step, "prepared-release-v") || step == "kept v66") && result.Release.Generation <= previous {
					t.Fatal("reused a pre-fix prepared decision")
				}
				if step == "Keep TVDB" {
					keptDependencies = result.Release.Identity.Dependencies.TVDB
				}
				if strings.HasPrefix(step, "kept") {
					keep := result.Corrections.Corrections.Identity.KeepTVDB
					if keep == nil || !*keep || result.Release.Identity.Dependencies.TVDB != keptDependencies {
						t.Fatal("kept TVDB choice or its original dependencies changed")
					}
					for endpoint, count := range fixture.requests() {
						if strings.HasPrefix(endpoint, "/v4/") && count != beforeRequests[endpoint] {
							t.Fatalf("%s fetched kept TVDB data at %s", step, endpoint)
						}
					}
				}
				previous = result.Release.Generation
				if result.Corrections.Corrections.ReleaseName.ManualYear != nil || result.Corrections.Corrections.ReleaseName.NoYear != nil {
					t.Errorf("%s: TV year controls survived preparation", step)
				}
				wantYear := scenario.wantYear
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

func TestPreparedTVYearAvailabilityTransitions(t *testing.T) {
	ownerT := t
	fixture := newCorrectionEvidenceFixture(t)
	fixture.input.Instructions.Identity.TVDBID = new(555003)
	provider := &stubTVDB{}
	fixture.tvdb = provider
	module, repo := fixture.open(t)
	input := fixture.input
	for _, step := range []struct {
		name, data         string
		year               int
		omit, reuse, clear bool
		wantFact, wantName int
		owned              bool
	}{
		{
			name:     "missing positive",
			year:     2030,
			wantFact: 2030,
			wantName: 2030,
		},
		{name: "missing zero", year: 0},
		{
			name:     "missing omission",
			year:     2030,
			omit:     true,
			wantFact: 2030,
		},
		{
			name:     "available explicit",
			data:     "explicit",
			year:     2030,
			omit:     true,
			wantFact: 2024,
			wantName: 2024,
			owned:    true,
		},
		{
			name:     "kept despite missing upstream",
			year:     2035,
			omit:     true,
			reuse:    true,
			wantFact: 2024,
			wantName: 2024,
			owned:    true,
		},
		{
			name:     "refresh loses TVDB",
			year:     2031,
			wantFact: 2031,
			wantName: 2031,
		},
		{
			name:  "available no qualifier",
			data:  "none",
			year:  2032,
			omit:  true,
			owned: true,
		},
		{
			name:     "available collision",
			data:     "collision",
			year:     2033,
			omit:     true,
			wantFact: 2024,
			wantName: 2024,
			owned:    true,
		},
		{
			name:     "explicit provider clear",
			data:     "explicit",
			year:     2034,
			omit:     true,
			clear:    true,
			wantFact: 2034,
		},
	} {
		t.Run(step.name, func(t *testing.T) {
			provider.seriesMetadata = tvdb.SeriesMetadata{}
			if step.data != "" {
				provider.seriesMetadata = tvdb.SeriesMetadata{
					TVDBID:      555003,
					Name:        "Example Series",
					NameEnglish: "Example Series",
					Year:        2024,
					NameDisambiguation: tvdb.NameDisambiguation{
						CanonicalName: "Example Series",
						SeriesYear:    2024,
						IncludeYear:   step.data != "none",
						Status:        api.MetadataEvidenceStatusPartial,
						Source:        "synthetic",
					},
				}
				if step.data == "explicit" {
					provider.seriesMetadata.SeriesYear = 2024
					provider.seriesMetadata.SeriesYearSource = "translation_name"
				}
			}
			input.ExternalFreshness = api.ExternalFreshnessRefresh
			if step.reuse {
				input.ExternalFreshness = api.ExternalFreshnessReuse
			}
			id := 555003
			if step.clear {
				id = 0
			}
			update := api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{
				Values: api.ReleaseCorrectionValues{
					Identity:    api.ExternalIDOverrides{TVDBID: new(id), KeepTVDB: new(true)},
					ReleaseName: api.ReleaseNameOverrides{ManualYear: new(step.year), NoYear: new(step.omit)},
				},
			}}
			beforeFetches := provider.seriesMetadataCalls
			for _, phase := range []string{"prepare", "SQLite reopen"} {
				if phase == "SQLite reopen" {
					if err := repo.Close(); err != nil {
						t.Fatal(err)
					}
					module, repo = fixture.open(ownerT)
					input = api.PrepareInput{SourcePath: input.SourcePath, ExternalFreshness: api.ExternalFreshnessLoad}
					update = api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit}
				}
				result := prepareCorrectionEvidence(t, module, input, update)
				if result.Release.Naming.Year != step.wantFact {
					t.Fatalf("%s: year=%d want=%d", phase, result.Release.Naming.Year, step.wantFact)
				}
				for _, year := range []int{2024, 2030, 2031, 2032, 2033, 2034, 2035} {
					if strings.Contains(result.Release.Naming.ReleaseName, fmt.Sprintf(" %d ", year)) != (year == step.wantName) {
						t.Fatalf("%s: name=%q want year=%d", phase, result.Release.Naming.ReleaseName, step.wantName)
					}
				}
				display, err := module.ResolveDisplay(t.Context(), api.ReleaseRef{SourcePath: input.SourcePath, Generation: result.Release.Generation})
				if err != nil || display.TVDBNamingAvailable != step.owned {
					t.Fatalf("%s: TVDB naming available=%t want=%t err=%v", phase, display.TVDBNamingAvailable, step.owned, err)
				}
				controls := result.Corrections.Corrections.ReleaseName
				if step.owned {
					if controls.ManualYear != nil || controls.NoYear != nil || result.Release.Naming.YearProvenance != api.FactProvenanceAutomatic {
						t.Fatal("manual TV year controls overrode available TVDB data")
					}
				} else if controls.ManualYear == nil || *controls.ManualYear != step.year || controls.NoYear == nil || *controls.NoYear != step.omit {
					t.Fatal("manual TV year fallback was not persisted")
				}
			}
			if step.reuse && provider.seriesMetadataCalls != beforeFetches {
				t.Fatal("kept TVDB naming data was fetched again")
			}
		})
	}
}
