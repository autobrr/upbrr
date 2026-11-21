// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata/imdb"
	"github.com/autobrr/upbrr/internal/metadata/tvdb"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPreparedTentativeIMDbKeepsIndependentEvidenceAcrossRestart(t *testing.T) {
	for _, tc := range []struct {
		name, sceneID, imdbType string
		wantID                  int
		linkTVDB                bool
	}{
		{
			name:     "independent agreement",
			sceneID:  "tt0555002",
			imdbType: "tvSeries",
			wantID:   555002,
		},
		{
			name:     "derived TVDB link does not count twice",
			imdbType: "tvSeries",
			linkTVDB: true,
		},
		{
			name:     "conflicting links",
			sceneID:  "tt0555099",
			imdbType: "tvSeries",
		},
		{
			name:     "linked IMDb title is not a series",
			sceneID:  "tt0555002",
			imdbType: "tvEpisode",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fixture := newCorrectionEvidenceFixtureForSource(t, "Example.Series.2026.S01E01.1080p.WEB-DL.H264-GRP.mkv")
			fixture.input.Instructions = api.ReleaseFactInstructions{}
			fixture.input.Controls.Interaction = api.InteractionModeUnattended
			fixture.tmdbExternalIDs = `{"imdb_id":"tt0555002"}`
			var reads atomic.Int32
			base := fixture.http.Transport
			fixture.http.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				var body string
				switch req.URL.Host {
				case "caching.graphql.imdb.com":
					var payload struct {
						OperationName string         `json:"operationName"`
						Variables     map[string]any `json:"variables"`
					}
					if req.Method == http.MethodGet {
						payload.OperationName = req.URL.Query().Get("operationName")
						if err := json.Unmarshal([]byte(req.URL.Query().Get("variables")), &payload.Variables); err != nil {
							return nil, fmt.Errorf("decode fixture IMDb request: %w", err)
						}
					} else if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
						return nil, fmt.Errorf("decode fixture IMDb request: %w", err)
					}
					switch payload.OperationName {
					case "SearchTitles":
						body = `{"data":{"advancedTitleSearch":{"edges":[{"node":{"title":{"id":"tt0555001","titleText":{"text":"Unrelated Programme"},"titleType":{"text":"TV Series"},"releaseYear":{"year":2026}}}}]}}}`
					case "GetTitleInfo":
						id, _ := payload.Variables["id"].(string)
						if id == "tt0555001" {
							t.Error("tentative IMDb suggestion was fetched as canonical metadata")
						}
						if id == "tt0555002" {
							reads.Add(1)
						}
						body = fmt.Sprintf(`{"data":{"title":{"id":%q,"titleText":{"text":"Example Series"},"titleType":{"id":%q},"releaseYear":{"year":2026}}}}`, id, tc.imdbType)
					default:
						return nil, fmt.Errorf("unexpected IMDb operation %s", payload.OperationName)
					}
				case "scene.example.invalid":
					switch {
					case strings.HasPrefix(req.URL.Path, "/v1/search/imdb:"):
						t.Error("tentative IMDb suggestion was passed into scene lookup")
					case tc.sceneID != "" && strings.Contains(req.URL.Path, "/v1/search/"):
						release := strings.TrimSuffix(filepath.Base(fixture.input.SourcePath), ".mkv")
						body = fmt.Sprintf(`{"resultsCount":1,"results":[{"release":%q,"imdbId":%q}]}`, release, tc.sceneID)
					}
					if body == "" {
						return base.RoundTrip(req)
					}
				case "api.themoviedb.org":
					if req.URL.Path != "/3/find/tt0555009" {
						return base.RoundTrip(req)
					}
					body = `{"movie_results":[],"tv_results":[]}`
				default:
					return base.RoundTrip(req)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(body)),
					Request:    req,
				}, nil
			})
			fixture.imdb = imdb.NewClient(fixture.http, nil)
			if tc.linkTVDB {
				fixture.tmdbExternalIDs = `{"imdb_id":"tt0555002","tvdb_id":555003}`
				fixture.tvdbRemoteIDs = `[{"id":"tt0555002","sourceName":"IMDB","type":2}]`
				fixture.tvdb = tvdb.NewClient(fixture.http, nil, "synthetic-key", "")
			}
			module, repo := fixture.open(t)
			input := fixture.input
			inherit := api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit}
			var generation api.PreparedGeneration
			for _, phase := range []string{"initial", "restart"} {
				if phase == "restart" {
					if err := repo.Close(); err != nil {
						t.Fatal(err)
					}
					module, _ = fixture.open(t)
					input.ExternalFreshness = api.ExternalFreshnessReuse
				}
				got := prepareCorrectionEvidence(t, module, input, inherit).Release
				if got.Identity.IMDBID != tc.wantID || phase == "restart" && got.Generation != generation {
					t.Fatalf("%s: IMDb %d, generation %d (previous %d)", phase, got.Identity.IMDBID, got.Generation, generation)
				}
				generation = got.Generation
				if tc.linkTVDB && (got.ProviderMetadata.TVDB == nil || len(got.ProviderMetadata.TVDB.RemoteLinks.Links) == 0) {
					t.Fatalf("%s: derived TVDB link was not collected: %+v", phase, got.ProviderMetadata.TVDB)
				}
				candidates := got.ProviderMetadata.IdentityCandidates
				index := slices.IndexFunc(candidates, func(candidate api.ExternalIdentityCandidate) bool {
					return candidate.Provider == api.IdentityProviderIMDB && candidate.ID == 555001
				})
				if index < 0 || candidates[index].TentativeConfidence == nil || *candidates[index].TentativeConfidence >= 0.85 {
					t.Fatalf("%s: retained tentative evidence %+v", phase, candidates)
				}
				ref := api.ReleaseRef{SourcePath: input.SourcePath, Generation: generation}
				upload, err := module.ResolveUploadSubject(t.Context(), api.UploadSubjectInput{Release: ref})
				if err != nil {
					t.Fatal(err)
				}
				if upload.Identity.IMDBID != tc.wantID || strings.Contains(upload.ReleaseName, "Unrelated") {
					t.Fatalf("%s: upload projection got IMDb %d and name %q", phase, upload.Identity.IMDBID, upload.ReleaseName)
				}
				if tc.wantID == 0 && upload.ProviderMetadata.IMDB != nil ||
					tc.wantID != 0 && (upload.ProviderMetadata.IMDB == nil || upload.ProviderMetadata.IMDB.IMDBID != tc.wantID) {
					t.Fatalf("%s: upload IMDb metadata %+v", phase, upload.ProviderMetadata.IMDB)
				}
			}
			wantReads := int32(0)
			if tc.sceneID == "tt0555002" {
				wantReads = 1
			}
			if reads.Load() != wantReads {
				t.Fatalf("IMDb title fetched %d times, want %d", reads.Load(), wantReads)
			}
			if tc.wantID != 0 {
				for _, step := range []struct {
					want  int
					patch *api.ReleaseCorrectionPatch
				}{
					{555009, &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{Identity: api.ExternalIDOverrides{IMDBID: new(555009)}}}},
					{0, &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{Identity: api.ExternalIDOverrides{IMDBID: new(0)}}}},
					{555002, &api.ReleaseCorrectionPatch{ResetFields: []api.CorrectionFieldRef{{Field: api.CorrectionFieldIdentityIMDB}}}},
				} {
					got := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: step.patch}).Release
					if got.Identity.IMDBID != step.want {
						t.Fatalf("after correction %+v: IMDb %d, want %d", step.patch, got.Identity.IMDBID, step.want)
					}
				}
			}
		})
	}
}
