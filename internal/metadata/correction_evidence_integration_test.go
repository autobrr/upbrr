// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/externalidentity"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPreparedCorrectionReusesEpisodeAndSceneEvidenceAcrossRestart(t *testing.T) {
	t.Parallel()
	fixture := newCorrectionEvidenceFixture(t)
	module, repo := fixture.open(t)
	initial := prepareCorrectionEvidence(t, module, fixture.input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
	if initial.Release.Episode.Title != "The First Signal" || !strings.Contains(initial.Release.Naming.ReleaseName, "The First Signal") {
		t.Fatalf("initial episode/name = %q / %q", initial.Release.Episode.Title, initial.Release.Naming.ReleaseName)
	}
	original, err := json.Marshal(initial.Release)
	if err != nil {
		t.Fatal(err)
	}
	initialCalls := fixture.requests()
	if initialCalls["/3/tv/555001/season/1/episode/1"] != 1 || initialCalls["/3/tv/555001"] == 0 || initialCalls["/v1/search/word:Example Series S01E01"] != 1 {
		t.Fatalf("initial preparation did not collect provider, episode, and scene evidence: %v", initialCalls)
	}

	input := api.PrepareInput{SourcePath: fixture.input.SourcePath, ExternalFreshness: api.ExternalFreshnessReuse}
	omitted := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{
		Mode: api.ReleaseCorrectionUpdatePatch,
		Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{
			ReleaseName: api.ReleaseNameOverrides{NoEpisodeTitle: new(true)},
		}},
	})
	if omitted.Release.Generation <= initial.Release.Generation || omitted.Release.Episode.Title != "" {
		t.Fatalf("omission did not create a cleared generation: generation=%d title=%q", omitted.Release.Generation, omitted.Release.Episode.Title)
	}
	names := omitted.Release.Naming
	for _, name := range []string{
		names.ReleaseName, names.NameWithoutTag, names.CleanName,
		names.GeneratedReleaseNames.IncludeEpisodeTitle.Name, names.GeneratedReleaseNames.IncludeEpisodeTitle.NameNoTag,
		names.GeneratedReleaseNames.IncludeEpisodeTitle.CleanName, names.GeneratedReleaseNames.OmitEpisodeTitle.Name,
		names.GeneratedReleaseNames.OmitEpisodeTitle.NameNoTag, names.GeneratedReleaseNames.OmitEpisodeTitle.CleanName,
	} {
		if name == "" || strings.Contains(name, "The First Signal") {
			t.Errorf("invalid omitted-title name projection: %q", name)
		}
	}
	if names.GeneratedName == nil {
		t.Fatal("omission discarded the generated name document")
	}
	for _, component := range names.GeneratedName.Components {
		if component.Role == api.NameRoleEpisodeTitle && (component.Present || component.Value != "") {
			t.Errorf("generated name retained an effective episode-title component: %#v", component)
		}
	}
	if got := fixture.requests(); !reflect.DeepEqual(got, initialCalls) {
		t.Fatalf("local omission made HTTP requests: before=%v after=%v", initialCalls, got)
	}
	persisted, err := repo.LoadPreparedRelease(t.Context(), input.SourcePath)
	if err != nil || !reflect.DeepEqual(persisted, omitted.Release) {
		t.Fatalf("omitted generation was not persisted: error=%v generation=%d", err, persisted.Generation)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen both the repository and clients so only persisted raw evidence can restore Auto.
	module, _ = fixture.open(t)
	restored := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{
		Mode: api.ReleaseCorrectionUpdatePatch,
		Patch: &api.ReleaseCorrectionPatch{ResetFields: []api.CorrectionFieldRef{
			{Field: api.CorrectionFieldReleaseNameNoEpisodeTitle},
		}},
	})
	if restored.Release.Generation <= omitted.Release.Generation || restored.Release.Episode.Title != "The First Signal" ||
		!reflect.DeepEqual(restored.Release.Naming, initial.Release.Naming) {
		t.Fatalf("Auto did not restore the automatic title/name in a new generation: %#v", restored.Release.Naming)
	}
	if restored.Corrections.Corrections.ReleaseName.NoEpisodeTitle != nil {
		t.Fatal("Auto retained the omission correction")
	}
	if got := fixture.requests(); !reflect.DeepEqual(got, initialCalls) {
		t.Fatalf("Auto after restart made HTTP requests: before=%v after=%v", initialCalls, got)
	}
	unchanged, err := json.Marshal(initial.Release)
	if err != nil || string(unchanged) != string(original) {
		t.Fatalf("later preparations mutated the original generation: %v", err)
	}

	changed := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{
		Mode: api.ReleaseCorrectionUpdatePatch,
		Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{
			ReleaseName: api.ReleaseNameOverrides{Episode: new("E02")},
		}},
	})
	if changed.Release.Episode.Episode != 2 || changed.Release.Episode.Title != "The Second Signal" ||
		!strings.Contains(changed.Release.Naming.ReleaseName, "The Second Signal") {
		t.Fatalf("episode dependency change reused the wrong title: episode=%#v name=%q", changed.Release.Episode, changed.Release.Naming.ReleaseName)
	}
	wantCalls := maps.Clone(initialCalls)
	wantCalls["/3/tv/555001/season/1/episode/2"] = 1
	wantCalls["/v1/search/word:Example Series S01E02"] = 1
	if got := fixture.requests(); !reflect.DeepEqual(got, wantCalls) {
		t.Fatalf("episode edit repeated unchanged provider or scene queries: want=%v got=%v", wantCalls, got)
	}
}

func TestPreparedCorrectionSeparatesTMDBMovieAndTVEvidence(t *testing.T) {
	t.Parallel()
	fixture := newCorrectionEvidenceFixture(t)
	module, _ := fixture.open(t)
	initial := prepareCorrectionEvidence(t, module, fixture.input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
	initialCalls := fixture.requests()
	input := api.PrepareInput{SourcePath: fixture.input.SourcePath, ExternalFreshness: api.ExternalFreshnessReuse}
	movie := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{
		Mode:  api.ReleaseCorrectionUpdatePatch,
		Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{ReleaseName: api.ReleaseNameOverrides{Category: new("MOVIE")}}},
	})
	if movie.Release.Generation <= initial.Release.Generation || movie.Release.Identity.TMDBID != 555001 || movie.Release.Identity.Category != api.CanonicalCategoryMovie ||
		movie.Release.ProviderMetadata.TMDB == nil || movie.Release.ProviderMetadata.TMDB.Title != "Example Movie" || movie.Release.Naming.Title != "Example Movie" {
		t.Fatalf("same numeric TMDB ID reused TV facts as movie facts: identity=%#v naming=%#v", movie.Release.Identity, movie.Release.Naming)
	}
	movieCalls := fixture.requests()
	for endpoint, count := range initialCalls {
		if movieCalls[endpoint] != count {
			t.Errorf("movie correction repeated unrelated query %q: before=%d after=%d", endpoint, count, movieCalls[endpoint])
		}
	}
	if movieCalls["/3/movie/555001"] == 0 {
		t.Fatalf("movie identity did not query its own TMDB namespace: %v", movieCalls)
	}
	restored := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{
		Mode:  api.ReleaseCorrectionUpdatePatch,
		Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{ReleaseName: api.ReleaseNameOverrides{Category: new("TV")}}},
	})
	if restored.Release.Identity.Category != api.CanonicalCategoryTV || restored.Release.Naming.Title != initial.Release.Naming.Title ||
		restored.Release.Episode.Title != initial.Release.Episode.Title {
		t.Fatalf("TV restoration reused movie facts: identity=%#v naming=%#v episode=%#v", restored.Release.Identity, restored.Release.Naming, restored.Release.Episode)
	}
	if got := fixture.requests(); !reflect.DeepEqual(got, movieCalls) {
		t.Fatalf("TV restoration did not reuse its own retained facts: before=%v after=%v", movieCalls, got)
	}
}

type correctionEvidenceFixture struct {
	dbPath string
	input  api.PrepareInput
	http   *http.Client
	mu     sync.Mutex
	calls  map[string]int
}

func newCorrectionEvidenceFixture(t *testing.T) *correctionEvidenceFixture {
	t.Helper()
	base := t.TempDir()
	source := filepath.Join(base, "Example.Series.S01E01.1080p.WEB-DL.H264-GRP.mkv")
	if err := os.WriteFile(source, []byte("synthetic media"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := &correctionEvidenceFixture{
		dbPath: filepath.Join(base, "metadata.sqlite"),
		input: api.PrepareInput{
			SourcePath:        source,
			ExternalFreshness: api.ExternalFreshnessLoad,
			Instructions: api.ReleaseFactInstructions{
				Category: new(api.CanonicalCategoryTV),
				Identity: api.ExternalIDOverrides{
					TMDBID:   new(555001),
					IMDBID:   new(0),
					TVDBID:   new(0),
					TVmazeID: new(0),
					MALID:    new(0),
				},
			},
		},
		calls: make(map[string]int),
	}
	fixture.http = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		key := request.URL.Path
		fixture.mu.Lock()
		fixture.calls[key]++
		fixture.mu.Unlock()
		body := ""
		switch {
		case request.URL.Host == "scene.example.invalid":
			body = `{"resultsCount":0,"results":[]}`
		case request.URL.Host != "api.themoviedb.org":
			t.Errorf("unexpected HTTP destination: %s", request.URL.Host)
		case key == "/3/tv/555001":
			body = `{"id":555001,"name":"Example Series","original_name":"Example Series","first_air_date":"2026-01-01","original_language":"en","type":"Scripted"}`
		case key == "/3/movie/555001":
			body = `{"id":555001,"title":"Example Movie","original_title":"Example Movie","release_date":"2026-01-01","original_language":"en"}`
		case key == "/3/tv/555001/season/1/episode/1":
			body = `{"id":555101,"name":"The First Signal","season_number":1,"episode_number":1,"air_date":"2026-01-01"}`
		case key == "/3/tv/555001/season/1/episode/2":
			body = `{"id":555102,"name":"The Second Signal","season_number":1,"episode_number":2,"air_date":"2026-01-02"}`
		default:
			for _, category := range []string{"tv", "movie"} {
				prefix := fmt.Sprintf("/3/%s/555001/", category)
				switch key {
				case prefix + "external_ids":
					body = `{}`
				case prefix + "translations":
					body = `{"translations":[]}`
				case prefix + "videos", prefix + "keywords":
					body = `{"results":[],"keywords":[]}`
				case prefix + "credits":
					body = `{"cast":[],"crew":[]}`
				case prefix + "images":
					body = `{"logos":[]}`
				}
			}
		}
		if body == "" {
			t.Errorf("unexpected HTTP query: %s", request.URL.Path)
			body = `{}`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    request,
		}, nil
	})}
	return fixture
}

func (f *correctionEvidenceFixture) open(t *testing.T) (*preparedrelease.Module, *db.SQLiteRepository) {
	t.Helper()
	repo, err := db.OpenContext(t.Context(), f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.MigrateContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	service := NewService(repo,
		WithConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: f.dbPath}}),
		WithMediaInfoExporter(&stubMediaInfo{}),
		WithSceneDetector(newSRRDBDetector(f.http, "https://scene.example.invalid", "", "")),
		WithTMDBClient(tmdb.NewClient(f.http, nil, "synthetic-api-key")),
		WithIMDBClient(&stubIMDB{}), WithTVDBClient(&stubTVDB{}), WithTVmazeClient(&stubTVmaze{}))
	collector, err := preparedrelease.NewEvidenceCollector(service)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := externalidentity.NewWithCandidateSource(repo, collector)
	if err != nil {
		t.Fatal(err)
	}
	module, err := preparedrelease.New(repo, identity, collector)
	if err != nil {
		t.Fatal(err)
	}
	return module, repo
}

func (f *correctionEvidenceFixture) requests() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return maps.Clone(f.calls)
}

func prepareCorrectionEvidence(t *testing.T, module *preparedrelease.Module, input api.PrepareInput, update api.ReleaseCorrectionUpdate) api.PrepareResult {
	t.Helper()
	resolved, err := module.ResolveInput(t.Context(), input, update)
	if err != nil {
		t.Fatalf("resolve corrections: %v", err)
	}
	result, err := module.PrepareResolved(t.Context(), resolved)
	if err != nil {
		t.Fatalf("prepare corrections: %v", err)
	}
	return result
}
