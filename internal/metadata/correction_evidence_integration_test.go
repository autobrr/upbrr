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
	"strconv"
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
	preserveCorrectionGeneration(t, &initial.Release)
	initialCalls := fixture.requests()
	input := api.PrepareInput{SourcePath: fixture.input.SourcePath, ExternalFreshness: api.ExternalFreshnessReuse}
	movie := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{
		Mode:  api.ReleaseCorrectionUpdatePatch,
		Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{ReleaseName: api.ReleaseNameOverrides{Category: new("MOVIE")}}},
	})
	if movie.Release.Generation <= initial.Release.Generation || movie.Release.Identity.TMDBID != 555001 || movie.Release.Identity.Category != api.CanonicalCategoryMovie ||
		movie.Release.ProviderMetadata.TMDB == nil ||
		movie.Release.ProviderMetadata.TMDB.Title != "Example Movie" ||
		movie.Release.Naming.Title != "Example Movie" {
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
		t.Fatalf(
			"TV restoration reused movie facts: identity=%#v naming=%#v episode=%#v",
			restored.Release.Identity,
			restored.Release.Naming,
			restored.Release.Episode,
		)
	}
	if got := fixture.requests(); !reflect.DeepEqual(got, movieCalls) {
		t.Fatalf("TV restoration did not reuse its own retained facts: before=%v after=%v", movieCalls, got)
	}
}

func TestPreparedSeasonCorrectionScopesEpisodeEvidence(t *testing.T) {
	t.Parallel()
	fixture := newCorrectionEvidenceFixture(t)
	module, repo := fixture.open(t)
	initial := prepareCorrectionEvidence(t, module, fixture.input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
	preserveCorrectionGeneration(t, &initial.Release)
	input := api.PrepareInput{SourcePath: fixture.input.SourcePath, ExternalFreshness: api.ExternalFreshnessReuse}
	previous := initial.Release.Generation
	parentT := t
	for _, test := range []struct {
		name   string
		value  *string
		season int
		title  string
		delta  map[string]int
	}{
		{
			name:   "set",
			value:  new("S02"),
			season: 2,
			title:  "The Returning Signal",
			delta:  map[string]int{"/3/tv/555001/season/2/episode/1": 1, "/v1/search/word:Example Series S02E01": 1},
		},
		{
			name:   "change",
			value:  new("S03"),
			season: 3,
			title:  "The Final Signal",
			delta:  map[string]int{"/3/tv/555001/season/3/episode/1": 1, "/v1/search/word:Example Series S03E01": 1},
		},
		{
			name:  "clear",
			value: new(""),
			title: "The First Signal",
		},
		{
			name:   "Auto after restart",
			season: 1,
			title:  "The First Signal",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := fixture.requests()
			patch := &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{ReleaseName: api.ReleaseNameOverrides{Season: test.value}}}
			if test.value == nil {
				if err := repo.Close(); err != nil {
					t.Fatal(err)
				}
				module, repo = fixture.open(parentT)
				patch.ResetFields = []api.CorrectionFieldRef{{Field: api.CorrectionFieldReleaseNameSeason}}
			}
			result := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: patch})
			if result.Release.Generation <= previous || result.Release.Episode.Season != test.season || result.Release.Episode.Title != test.title {
				t.Fatalf("season correction = generation %d episode %#v", result.Release.Generation, result.Release.Episode)
			}
			if test.season > 0 && !strings.Contains(result.Release.Naming.ReleaseName, fmt.Sprintf("S%02dE01", test.season)) ||
				test.season == 0 &&
					(result.Release.Episode.SeasonLabel != "" || strings.Contains(result.Release.Naming.ReleaseName, "S01E01") || strings.Contains(result.Release.Naming.ReleaseName, "S03E01")) {
				t.Fatalf("season correction did not reach generated name: %q", result.Release.Naming.ReleaseName)
			}
			if !reflect.DeepEqual(result.Corrections.Corrections.ReleaseName.Season, test.value) {
				t.Fatalf("stored season correction = %v, want %v", result.Corrections.Corrections.ReleaseName.Season, test.value)
			}
			if test.value == nil && !reflect.DeepEqual(result.Release.Naming, initial.Release.Naming) {
				t.Fatalf("Auto did not restore original naming: %#v", result.Release.Naming)
			}
			fixture.assertRequests(t, before, test.delta)
			previous = result.Release.Generation
		})
	}
}

func TestPreparedTMDBIDCorrectionRestoresAutomaticEvidence(t *testing.T) {
	t.Parallel()
	fixture := newCorrectionEvidenceFixture(t)
	fixture.input.Instructions.Identity.TMDBID = nil
	module, repo := fixture.open(t)
	initial := prepareCorrectionEvidence(t, module, fixture.input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
	preserveCorrectionGeneration(t, &initial.Release)
	if initial.Release.Identity.TMDBID != 555001 || fixture.requests()["/3/search/tv"] != 1 {
		t.Fatalf("automatic TV identity did not use the candidate fixture: %#v; calls=%v", initial.Release.Identity, fixture.requests())
	}
	input := api.PrepareInput{SourcePath: fixture.input.SourcePath, ExternalFreshness: api.ExternalFreshnessReuse}
	before := fixture.requests()
	changed := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{
		Mode:  api.ReleaseCorrectionUpdatePatch,
		Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{Identity: api.ExternalIDOverrides{TMDBID: new(555002)}}},
	})
	if changed.Release.Generation <= initial.Release.Generation || changed.Release.Identity.TMDBID != 555002 ||
		changed.Release.ProviderMetadata.TMDB == nil || changed.Release.ProviderMetadata.TMDB.TMDBID != 555002 ||
		changed.Release.Naming.Title != "Alternate Series" || changed.Release.Episode.Title != "The Other Signal" {
		t.Fatalf(
			"TMDB correction reused old identity facts: identity=%#v provider=%#v episode=%#v",
			changed.Release.Identity,
			changed.Release.ProviderMetadata.TMDB,
			changed.Release.Episode,
		)
	}
	fixture.assertRequests(t, before, map[string]int{
		"/3/tv/555002":                    2,
		"/3/tv/555002/external_ids":       1,
		"/3/tv/555002/translations":       1,
		"/3/tv/555002/videos":             1,
		"/3/tv/555002/keywords":           1,
		"/3/tv/555002/credits":            1,
		"/3/tv/555002/season/1/episode/1": 1,
		"/3/tv/555002/images":             1,
	})
	before = fixture.requests()
	cleared := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{
		Mode:  api.ReleaseCorrectionUpdatePatch,
		Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{Identity: api.ExternalIDOverrides{TMDBID: new(0)}}},
	})
	if cleared.Release.Generation <= changed.Release.Generation || cleared.Release.Identity.TMDBID != 0 ||
		cleared.Release.ProviderMetadata.TMDB != nil || cleared.Corrections.Corrections.Identity.TMDBID == nil || *cleared.Corrections.Corrections.Identity.TMDBID != 0 {
		t.Fatalf("explicit zero did not clear TMDB identity/provider facts: %#v", cleared)
	}
	fixture.assertRequests(t, before, nil)
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	module, _ = fixture.open(t)
	restored := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{
		Mode:  api.ReleaseCorrectionUpdatePatch,
		Patch: &api.ReleaseCorrectionPatch{ResetFields: []api.CorrectionFieldRef{{Field: api.CorrectionFieldIdentityTMDB}}},
	})
	if restored.Release.Generation <= cleared.Release.Generation || restored.Release.Identity.TMDBID != 555001 ||
		restored.Corrections.Corrections.Identity.TMDBID != nil || restored.Release.Identity.Overrides.TMDB == api.OverrideStateValue ||
		!reflect.DeepEqual(restored.Release.Episode, initial.Release.Episode) || !reflect.DeepEqual(restored.Release.Naming, initial.Release.Naming) {
		t.Fatalf("Auto did not restore automatic TMDB identity and naming: %#v", restored)
	}
	fixture.assertRequests(t, before, nil)
}

func TestPreparedDailyDateCorrectionPreservesParsedDateEvidence(t *testing.T) {
	t.Parallel()
	fixture := newCorrectionEvidenceFixtureForSource(t, "Example.Series.2026.01.01.1080p.WEB-DL.H264-GRP.mkv")
	module, repo := fixture.open(t)
	initial := prepareCorrectionEvidence(t, module, fixture.input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
	preserveCorrectionGeneration(t, &initial.Release)
	if initial.Release.Episode.DailyDate != "2026-01-01" || initial.Release.Episode.Episode != 1 || !initial.Release.Episode.DateMatched {
		t.Fatalf("daily fixture did not retain its parsed date and exact mapping: %#v", initial.Release.Episode)
	}
	input := api.PrepareInput{SourcePath: fixture.input.SourcePath, ExternalFreshness: api.ExternalFreshnessReuse}
	previous := initial.Release.Generation
	parentT := t
	for _, test := range []struct {
		name      string
		date      *string
		mode      *bool
		reset     api.CorrectionField
		wantDate  string
		episode   int
		dailyName bool
		delta     map[string]int
	}{
		{
			name:      "set date",
			date:      new("2026-01-02"),
			wantDate:  "2026-01-02",
			episode:   2,
			dailyName: true,
			delta:     map[string]int{"/3/tv/555001/season/1/episode/2": 1, "/v1/search/word:Example Series S01E02": 1},
		},
		{
			name:      "change date",
			date:      new("2026-01-03"),
			wantDate:  "2026-01-03",
			episode:   3,
			dailyName: true,
			delta:     map[string]int{"/3/tv/555001/season/1/episode/3": 1, "/v1/search/word:Example Series S01E03": 1},
		},
		{
			name:     "blank keeps parsed date",
			date:     new(""),
			wantDate: "2026-01-01",
			episode:  1,
		},
		{
			name:      "date Auto after restart",
			reset:     api.CorrectionFieldReleaseNameManualDate,
			wantDate:  "2026-01-01",
			episode:   1,
			dailyName: true,
		},
		{
			name:     "season episode true",
			mode:     new(true),
			wantDate: "2026-01-01",
			episode:  1,
		},
		{
			name:      "season episode false",
			mode:      new(false),
			wantDate:  "2026-01-01",
			episode:   1,
			dailyName: true,
		},
		{
			name:      "naming mode Auto",
			reset:     api.CorrectionFieldReleaseNameUseSeasonEpisode,
			wantDate:  "2026-01-01",
			episode:   1,
			dailyName: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := fixture.requests()
			patch := &api.ReleaseCorrectionPatch{
				Values: api.ReleaseCorrectionValues{ReleaseName: api.ReleaseNameOverrides{ManualDate: test.date, UseSeasonEpisode: test.mode}},
			}
			if test.reset != "" {
				patch.ResetFields = []api.CorrectionFieldRef{{Field: test.reset}}
			}
			if test.reset == api.CorrectionFieldReleaseNameManualDate {
				if err := repo.Close(); err != nil {
					t.Fatal(err)
				}
				module, repo = fixture.open(parentT)
			}
			result := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: patch})
			if result.Release.Generation <= previous || result.Release.Episode.DailyDate != test.wantDate || result.Release.Episode.Episode != test.episode ||
				!result.Release.Episode.DateMatched || result.Release.Episode.Season != 1 {
				t.Fatalf("date correction reused incorrect episode facts: generation=%d episode=%#v", result.Release.Generation, result.Release.Episode)
			}
			wantTitle := []string{"The First Signal", "The Second Signal", "The Third Signal"}[test.episode-1]
			if result.Release.Episode.Title != wantTitle {
				t.Fatalf("date correction retained wrong episode title: %q, want %q", result.Release.Episode.Title, wantTitle)
			}
			if !reflect.DeepEqual(result.Corrections.Corrections.ReleaseName.ManualDate, test.date) ||
				!reflect.DeepEqual(result.Corrections.Corrections.ReleaseName.UseSeasonEpisode, test.mode) {
				t.Fatalf("date/naming corrections lost explicit blank, boolean, or Auto: %#v", result.Corrections.Corrections.ReleaseName)
			}
			name := result.Release.Naming.ReleaseName
			if strings.Contains(name, test.wantDate) != test.dailyName ||
				strings.Contains(name, fmt.Sprintf("S01E%02d", test.episode)) == test.dailyName {
				t.Fatalf("naming mode mismatch: daily=%t name=%q", test.dailyName, name)
			}
			if test.reset == api.CorrectionFieldReleaseNameManualDate && !reflect.DeepEqual(result.Release.Naming, initial.Release.Naming) {
				t.Fatalf("date Auto did not restore original naming: %#v", result.Release.Naming)
			}
			fixture.assertRequests(t, before, test.delta)
			previous = result.Release.Generation
		})
	}
}

func TestPreparedMovieYearCorrectionReusesProviderEvidence(t *testing.T) {
	t.Parallel()
	fixture := newCorrectionEvidenceFixtureForSource(t, "Example.Movie.2026.1080p.WEB-DL.H264-GRP.mkv")
	fixture.input.Instructions.Category = new(api.CanonicalCategoryMovie)
	module, repo := fixture.open(t)
	initial := prepareCorrectionEvidence(t, module, fixture.input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
	preserveCorrectionGeneration(t, &initial.Release)
	input := api.PrepareInput{SourcePath: fixture.input.SourcePath, ExternalFreshness: api.ExternalFreshnessReuse}
	previous := initial.Release.Generation
	parentT := t
	before := fixture.requests()
	for _, test := range []struct {
		name   string
		value  *int
		year   int
		noYear *bool
		reset  api.CorrectionField
	}{
		{
			name:  "set",
			value: new(2027),
			year:  2027,
		},
		{
			name:  "change",
			value: new(2028),
			year:  2028,
		},
		{name: "zero", value: new(0)},
		{
			name:  "Auto after restart",
			year:  2026,
			reset: api.CorrectionFieldReleaseNameManualYear,
		},
		{
			name:   "omit year",
			year:   2026,
			noYear: new(true),
		},
		{
			name:   "show year",
			year:   2026,
			noYear: new(false),
		},
		{
			name:  "year omission Auto",
			year:  2026,
			reset: api.CorrectionFieldReleaseNameNoYear,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			patch := &api.ReleaseCorrectionPatch{
				Values: api.ReleaseCorrectionValues{ReleaseName: api.ReleaseNameOverrides{ManualYear: test.value, NoYear: test.noYear}},
			}
			if test.reset != "" {
				if err := repo.Close(); err != nil {
					t.Fatal(err)
				}
				module, repo = fixture.open(parentT)
				patch.ResetFields = []api.CorrectionFieldRef{{Field: test.reset}}
			}
			result := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: patch})
			if result.Release.Generation <= previous || result.Release.Naming.Year != test.year ||
				result.Release.ProviderMetadata.TMDB == nil || result.Release.ProviderMetadata.TMDB.Year != 2026 ||
				!reflect.DeepEqual(result.Corrections.Corrections.ReleaseName.ManualYear, test.value) ||
				!reflect.DeepEqual(result.Corrections.Corrections.ReleaseName.NoYear, test.noYear) {
				t.Fatalf(
					"movie year correction did not preserve raw provider year: naming=%#v provider=%#v correction=%v",
					result.Release.Naming,
					result.Release.ProviderMetadata.TMDB,
					result.Corrections.Corrections.ReleaseName.ManualYear,
				)
			}
			omitYear := test.noYear != nil && *test.noYear
			if test.year > 0 && strings.Contains(result.Release.Naming.ReleaseName, strconv.Itoa(test.year)) == omitYear ||
				test.year == 0 && (strings.Contains(result.Release.Naming.ReleaseName, "2026") || strings.Contains(result.Release.Naming.ReleaseName, "2028")) {
				t.Fatalf("movie name retained stale year: %q", result.Release.Naming.ReleaseName)
			}
			if test.reset != "" && !reflect.DeepEqual(result.Release.Naming, initial.Release.Naming) {
				t.Fatalf("year Auto did not restore original naming: %#v", result.Release.Naming)
			}
			fixture.assertRequests(t, before, nil)
			previous = result.Release.Generation
		})
	}
}

func preserveCorrectionGeneration(t *testing.T, release *api.PreparedRelease) {
	t.Helper()
	original, err := release.Clone()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !reflect.DeepEqual(*release, original) {
			t.Error("later corrections mutated the original prepared generation")
		}
	})
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
	return newCorrectionEvidenceFixtureForSource(t, "Example.Series.S01E01.1080p.WEB-DL.H264-GRP.mkv")
}

func newCorrectionEvidenceFixtureForSource(t *testing.T, filename string) *correctionEvidenceFixture {
	t.Helper()
	base := t.TempDir()
	source := filepath.Join(base, filename)
	if err := os.WriteFile(source, []byte("synthetic media"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if data, err := os.ReadFile(source); err != nil || string(data) != "synthetic media" {
			t.Errorf("corrections changed source bytes: %q, %v", data, err)
		}
	})
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
			body = `{"id":555001,"name":"Example Series","original_name":"Example Series","first_air_date":"2026-01-01","original_language":"en","type":"Scripted","seasons":[{"season_number":1,"air_date":"2026-01-01"}]}`
		case key == "/3/search/tv":
			body = `{"results":[{"id":555001,"name":"Example Series","original_name":"Example Series","first_air_date":"2026-01-01","original_language":"en"}]}`
		case key == "/3/tv/555002":
			body = `{"id":555002,"name":"Alternate Series","original_name":"Alternate Series","first_air_date":"2026-01-01","original_language":"en","type":"Scripted"}`
		case key == "/3/tv/555002/season/1/episode/1":
			body = `{"id":555201,"name":"The Other Signal","season_number":1,"episode_number":1,"air_date":"2026-01-01"}`
		case key == "/3/tv/555001/season/1":
			body = `{"id":555010,"season_number":1,"episodes":[{"episode_number":1,"air_date":"2026-01-01"},{"episode_number":2,"air_date":"2026-01-02"},{"episode_number":3,"air_date":"2026-01-03"}]}`
		case key == "/3/tv/555001/season/1/episode/3":
			body = `{"id":555103,"name":"The Third Signal","season_number":1,"episode_number":3,"air_date":"2026-01-03"}`
		case key == "/3/tv/555001/season/2/episode/1":
			body = `{"id":555211,"name":"The Returning Signal","season_number":2,"episode_number":1,"air_date":"2026-02-01"}`
		case key == "/3/tv/555001/season/3/episode/1":
			body = `{"id":555311,"name":"The Final Signal","season_number":3,"episode_number":1,"air_date":"2026-03-01"}`
		case key == "/3/movie/555001":
			body = `{"id":555001,"title":"Example Movie","original_title":"Example Movie","release_date":"2026-01-01","original_language":"en"}`
		case key == "/3/tv/555001/season/1/episode/1":
			body = `{"id":555101,"name":"The First Signal","season_number":1,"episode_number":1,"air_date":"2026-01-01"}`
		case key == "/3/tv/555001/season/1/episode/2":
			body = `{"id":555102,"name":"The Second Signal","season_number":1,"episode_number":2,"air_date":"2026-01-02"}`
		default:
			for _, namespace := range []string{"tv/555001", "tv/555002", "movie/555001"} {
				prefix := "/3/" + namespace + "/"
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

func (f *correctionEvidenceFixture) assertRequests(t *testing.T, before, delta map[string]int) {
	t.Helper()
	want := maps.Clone(before)
	for endpoint, count := range delta {
		want[endpoint] += count
	}
	if got := f.requests(); !reflect.DeepEqual(got, want) {
		t.Fatalf("correction HTTP dependency delta: want=%v got=%v", want, got)
	}
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
