// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/externalidentity"
	"github.com/autobrr/upbrr/internal/metadata/evidence"
	"github.com/autobrr/upbrr/internal/metadata/imdb"
	"github.com/autobrr/upbrr/internal/metadata/mediainfo"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	"github.com/autobrr/upbrr/internal/metadata/tvdb"
	"github.com/autobrr/upbrr/internal/metadata/tvmaze"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/services/db/dbfixture"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPreparedTypeCorrectionSurvivesSourceEditAndRestart(t *testing.T) {
	t.Parallel()
	fixture := newCorrectionEvidenceFixture(t)
	module, repo := fixture.open(t)
	initial := prepareCorrectionEvidence(t, module, fixture.input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
	calls := fixture.requests()
	input := api.PrepareInput{SourcePath: fixture.input.SourcePath, ExternalFreshness: api.ExternalFreshnessReuse}
	previous := initial.Release.Generation
	for _, step := range []struct {
		name, wantType, wantSource string
		patch                      *api.ReleaseCorrectionPatch
		restart, manual            bool
	}{
		{
			name:       "encode",
			wantType:   "ENCODE",
			wantSource: "Web",
			manual:     true,
			patch:      &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{ReleaseName: api.ReleaseNameOverrides{Type: new("ENCODE")}}},
		},
		{
			name:       "source only",
			wantType:   "ENCODE",
			wantSource: "BluRay",
			manual:     true,
			patch:      &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{ReleaseName: api.ReleaseNameOverrides{Source: new("BluRay")}}},
		},
		{
			name:       "restart",
			wantType:   "ENCODE",
			wantSource: "BluRay",
			manual:     true,
			restart:    true,
		},
		{
			name:       "Auto",
			wantType:   "WEBDL",
			wantSource: "BluRay",
			patch:      &api.ReleaseCorrectionPatch{ResetFields: []api.CorrectionFieldRef{{Field: api.CorrectionFieldReleaseNameType}}},
		},
	} {
		if step.restart {
			if err := repo.Close(); err != nil {
				t.Fatal(err)
			}
			module, repo = fixture.open(t)
		}
		t.Run(step.name, func(t *testing.T) {
			update := api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit}
			if step.patch != nil {
				update = api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: step.patch}
			}
			result := prepareCorrectionEvidence(t, module, input, update)
			if result.Release.Naming.Type != step.wantType || result.Release.Media.Type != step.wantType || result.Release.Media.Source != step.wantSource {
				t.Fatalf("naming/media type/source = %q/%q/%q", result.Release.Naming.Type, result.Release.Media.Type, result.Release.Media.Source)
			}
			for _, override := range []*string{result.Corrections.Corrections.ReleaseName.Type, result.EffectiveInstructions.ReleaseName.Type} {
				if step.manual && (override == nil || *override != "ENCODE") || !step.manual && override != nil {
					t.Fatalf("type authority = %v, manual=%t", override, step.manual)
				}
			}
			if step.patch != nil && result.Release.Generation <= previous {
				t.Fatal("correction reused the previous generation")
			}
			previous = result.Release.Generation
			name := result.Release.Naming.ReleaseName
			if result.Release.Naming.GeneratedName == nil || result.Release.Naming.GeneratedName.Render().Name != name ||
				step.manual && strings.Contains(name, "WEB-DL") {
				t.Fatalf("generated name disagrees with effective type: %q", name)
			}
			ref := api.ReleaseRef{SourcePath: input.SourcePath, Generation: result.Release.Generation}
			upload, err := module.ResolveUploadSubject(t.Context(), api.UploadSubjectInput{Release: ref})
			if err != nil {
				t.Fatal(err)
			}
			duplicate, err := module.ResolveDuplicateSubject(t.Context(), api.DuplicateCheckInput{Release: ref})
			if err != nil {
				t.Fatal(err)
			}
			if upload.Type != step.wantType || upload.ReleaseName != name || duplicate.ReleaseName != name {
				t.Fatal("downstream subjects lost effective type or name")
			}
			if !reflect.DeepEqual(fixture.requests(), calls) {
				t.Fatal("local correction repeated metadata requests")
			}
		})
	}
}

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
			value:  new("2"),
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

func TestPreparedPlainSeasonEpisodeCorrections(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name            string
		values          api.ReleaseNameOverrides
		season, episode int
	}{
		{"season only", api.ReleaseNameOverrides{Season: new("5")}, 5, 1},
		{"episode only", api.ReleaseNameOverrides{Episode: new("5")}, 1, 5},
		{"both", api.ReleaseNameOverrides{Season: new("5"), Episode: new("5")}, 5, 5},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newCorrectionEvidenceFixture(t)
			module, _ := fixture.open(t)
			initial := prepareCorrectionEvidence(t, module, fixture.input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
			result := prepareCorrectionEvidence(t, module, api.PrepareInput{SourcePath: fixture.input.SourcePath}, api.ReleaseCorrectionUpdate{
				Mode:  api.ReleaseCorrectionUpdatePatch,
				Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{ReleaseName: test.values}},
			})
			if result.Release.Generation <= initial.Release.Generation || result.Release.Episode.Season != test.season || result.Release.Episode.Episode != test.episode ||
				result.Release.Episode.Title != "Corrected Signal" {
				t.Fatalf("plain corrections did not reach prepared/provider facts: identity=%+v episode=%+v", result.Release.Identity, result.Release.Episode)
			}
			if !strings.Contains(result.Release.Naming.ReleaseName, fmt.Sprintf("S%02dE%02d", test.season, test.episode)) {
				t.Fatalf("plain corrections did not reach generated name: %q", result.Release.Naming.ReleaseName)
			}
			endpoint := fmt.Sprintf("/3/tv/555001/season/%d/episode/%d", test.season, test.episode)
			saved := result.Corrections.Corrections.ReleaseName
			if fixture.requests()[endpoint] != 1 || !reflect.DeepEqual(saved.Season, test.values.Season) || !reflect.DeepEqual(saved.Episode, test.values.Episode) {
				t.Fatalf("plain corrections did not reach provider lookup and durable instructions: requests=%v saved=%+v", fixture.requests(), saved)
			}
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

func TestPreparedUnresolvedCategoryDefaultsSurviveReuseAndRestart(t *testing.T) {
	t.Parallel()
	fixture := newCorrectionEvidenceFixtureForSource(t, "content.mkv")
	fixture.noTMDBResults = true
	dir := filepath.Join(filepath.Dir(fixture.input.SourcePath), "Example Release")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "content.mkv"), []byte("synthetic media"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.input.SourcePath = dir
	fixture.input.Instructions = api.ReleaseFactInstructions{}
	if category := ParseReleaseInfo(dir).Category; category != "" {
		t.Fatalf("expected unresolved source category, got %q", category)
	}
	module, repo := fixture.open(t)
	input := fixture.input
	var generation api.PreparedGeneration
	for _, phase := range []string{"initial", "reuse", "restart", "refresh", "old contract"} {
		switch phase {
		case "reuse":
			input.ExternalFreshness = api.ExternalFreshnessReuse
		case "refresh":
			input.ExternalFreshness = api.ExternalFreshnessRefresh
		case "restart", "old contract":
			if phase == "old contract" {
				stale, err := repo.LoadPreparedRelease(t.Context(), dir)
				if err != nil {
					t.Fatal(err)
				}
				stale.Compatibility.ContractVersion = "prepared-release-v77"
				stale.Identity.Resolution.ContractVersion = "external-identity-v12"
				stale.Identity.Category = api.CanonicalCategoryUnknown
				if err := repo.CommitPreparedRelease(t.Context(), stale); err != nil {
					t.Fatal(err)
				}
			}
			if err := repo.Close(); err != nil {
				t.Fatal(err)
			}
			module, repo = fixture.open(t)
			input.ExternalFreshness = api.ExternalFreshnessReuse
		}
		result := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
		release := result.Release
		if release.Identity.Category != api.CanonicalCategoryMovie || release.Identity.TMDBID != 0 || release.Identity.IMDBID != 0 ||
			release.Identity.TVDBID != 0 || release.Identity.TVmazeID != 0 || release.Identity.MALID != 0 {
			t.Fatalf("%s: unresolved identity = %+v", phase, release.Identity)
		}
		if (phase == "reuse" || phase == "restart") && release.Generation != generation || phase == "old contract" && release.Generation <= generation {
			t.Fatalf("%s: generation = %d, previous %d", phase, release.Generation, generation)
		}
		generation = release.Generation
		subject, err := module.ResolveUploadSubject(t.Context(), api.UploadSubjectInput{Release: api.ReleaseRef{SourcePath: dir, Generation: generation}})
		if err != nil {
			t.Fatal(err)
		}
		if subject.Release.Category != "MOVIE" {
			t.Fatalf("%s: inconsistent upload category: %q", phase, subject.Release.Category)
		}
		if _, err := release.Identity.RequireProviderID(api.IdentityProviderTMDB); err == nil {
			t.Fatalf("%s: category fallback bypassed missing TMDB requirement", phase)
		}
	}
}

func TestPreparedTMDBCategoryFallbackAndMovieCorrectionSurviveRestart(t *testing.T) {
	t.Parallel()
	fixture := newCorrectionEvidenceFixtureForSource(t, "Example.Series.2026.1080p.WEB-DL.H264-GRP.mkv")
	fixture.input.Instructions = api.ReleaseFactInstructions{}
	fixture.imdb = &stubIMDB{
		searchFn: func(input imdb.SearchInput) (imdb.SearchResult, error) {
			if input.Category == "tv" {
				return imdb.SearchResult{IMDbID: 555002}, nil
			}
			return imdb.SearchResult{IMDbID: 555099}, nil
		},
		infoFn: func(id string) imdb.Info {
			if id == "tt0555002" {
				return imdb.Info{
					IMDbID: id,
					Title:  "Example Series",
					Type:   "tvSeries",
				}
			}
			return imdb.Info{
				IMDbID: id,
				Title:  "Example Movie",
				Type:   "movie",
			}
		},
	}
	fixture.tvdb = &stubTVDB{id: 555003, name: "Example Series"}
	fixture.tvmaze = &stubTVmaze{result: tvmaze.SearchResult{
		SelectedID: 555004,
		TVDBID:     555003,
		Candidates: []tvmaze.Candidate{{ID: 555004, Name: "Example Series"}},
	}}
	module, repo := fixture.open(t)
	input := fixture.input
	update := api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit}
	for _, phase := range []string{"fallback", "restart", "Movie correction", "Movie restart"} {
		switch phase {
		case "restart", "Movie restart":
			if err := repo.Close(); err != nil {
				t.Fatal(err)
			}
			module, repo = fixture.open(t)
			input = api.PrepareInput{SourcePath: input.SourcePath, ExternalFreshness: api.ExternalFreshnessLoad}
			update = api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit}
		case "Movie correction":
			input.ExternalFreshness = api.ExternalFreshnessReuse
			update = api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{
				Values: api.ReleaseCorrectionValues{ReleaseName: api.ReleaseNameOverrides{Category: new("MOVIE")}},
			}}
		}
		result := prepareCorrectionEvidence(t, module, input, update)
		release := result.Release
		wantCategory, wantIMDb := api.CanonicalCategoryTV, 555002
		if strings.HasPrefix(phase, "Movie") {
			wantCategory, wantIMDb = api.CanonicalCategoryMovie, 555099
		}
		if release.Identity.Category != wantCategory || release.Identity.IMDBID != wantIMDb {
			t.Fatalf("%s: category/IMDb = %s/%d, want %s/%d", phase, release.Identity.Category, release.Identity.IMDBID, wantCategory, wantIMDb)
		}
		if release.ProviderMetadata.IMDB == nil {
			t.Fatalf("%s: metadata previews unavailable", phase)
		}
		if wantCategory == api.CanonicalCategoryTV {
			if release.Identity.TMDBID != 555001 || release.Identity.TVDBID != 555003 || release.Identity.TVmazeID != 555004 ||
				release.ProviderMetadata.TMDB == nil || release.ProviderMetadata.TVDB == nil || release.ProviderMetadata.TVmaze == nil {
				t.Fatalf("%s: TV fallback lost provider IDs or metadata: %+v", phase, release.Identity)
			}
		} else if release.Identity.TMDBID != 0 || release.Identity.TVDBID != 0 || release.Identity.TVmazeID != 0 ||
			release.ProviderMetadata.TMDB != nil || release.ProviderMetadata.TVDB != nil || release.ProviderMetadata.TVmaze != nil {
			t.Fatalf("%s: Movie retained TV provider IDs or metadata: %+v", phase, release.Identity)
		}
		display, err := preparedrelease.ProjectDisplay(release)
		if err != nil {
			t.Fatal(err)
		}
		if len(display.Providers) == 0 {
			t.Fatalf("%s: metadata previews absent from display", phase)
		}
	}
	if calls := fixture.requests(); calls["/3/search/movie"] == 0 || calls["/3/search/tv"] == 0 || calls["/3/tv/555001"] == 0 {
		t.Fatalf("fallback did not search and fetch the TV namespace: %v", calls)
	}
}

func TestPreparedProviderCorrectionsRestoreMALAcrossRestart(t *testing.T) {
	for _, test := range []struct {
		name string
		mal  *int
		want int
	}{
		{name: "automatic", want: 444},
		{
			name: "manual",
			mal:  new(999),
			want: 999,
		},
		{name: "cleared", mal: new(0)},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newCorrectionEvidenceFixture(t)
			fixture.tmdbAnime = true
			fixture.tmdbExternalIDs = `{"imdb_id":"tt0555002"}`
			fixture.input.Instructions.Identity = api.ExternalIDOverrides{MALID: test.mal}
			fixture.imdb = &stubIMDB{
				searchResult: imdb.SearchResult{IMDbID: 555002},
				infoFn: func(id string) imdb.Info {
					return imdb.Info{
						IMDbID: id,
						Title:  "Example Series",
						Type:   "tvSeries",
					}
				},
			}
			module, repo := fixture.open(t)
			input := fixture.input
			update := api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit}
			for _, phase := range []string{"initial", "IMDb correction", "TMDB correction", "restart", "refresh", "old contract"} {
				want := test.want
				switch phase {
				case "IMDb correction":
					input = api.PrepareInput{SourcePath: input.SourcePath, ExternalFreshness: api.ExternalFreshnessReuse}
					update = api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{
						Values: api.ReleaseCorrectionValues{Identity: api.ExternalIDOverrides{IMDBID: new(555099)}},
					}}
					if test.mal == nil {
						want = 0 // The corrected IMDb has no TMDB link until TMDB is selected manually.
					}
				case "TMDB correction":
					update = api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{
						Values: api.ReleaseCorrectionValues{Identity: api.ExternalIDOverrides{TMDBID: new(555001)}},
					}}
				case "refresh":
					input.ExternalFreshness = api.ExternalFreshnessRefresh
					update = api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit}
				case "restart", "old contract":
					if phase == "old contract" {
						stale, err := repo.LoadPreparedRelease(t.Context(), input.SourcePath)
						if err != nil {
							t.Fatal(err)
						}
						stale.Compatibility.ContractVersion = "prepared-release-v64"
						stale.Identity.Resolution.ContractVersion = "external-identity-v7"
						if test.mal == nil {
							stale.Identity.MALID = 0
							stale.Identity.Provenance.MAL = api.IdentityProvenanceUnknown
							stale.Identity.Dependencies.MAL = api.IdentityDependency{}
							stale.ProviderMetadata.AniList = nil
						}
						if err := repo.CommitPreparedRelease(t.Context(), stale); err != nil {
							t.Fatal(err)
						}
					}
					if err := repo.Close(); err != nil {
						t.Fatal(err)
					}
					module, repo = fixture.open(t)
					input.ExternalFreshness = api.ExternalFreshnessLoad
					update = api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit}
				}
				result := prepareCorrectionEvidence(t, module, input, update)
				ids, metadata := result.Release.Identity, result.Release.ProviderMetadata
				if ids.MALID != want || want == 0 && metadata.AniList != nil ||
					want != 0 && (metadata.AniList == nil || metadata.AniList.MALID != want) {
					t.Fatalf("%s: MAL identity/preview = %d / %#v, want %d", phase, ids.MALID, metadata.AniList, want)
				}
				if test.mal != nil {
					correction := result.Corrections.Corrections.Identity.MALID
					if correction == nil || *correction != *test.mal || ids.Provenance.MAL != api.IdentityProvenanceExplicit {
						t.Fatalf("%s: manual MAL correction lost: %#v", phase, ids)
					}
				}
				if phase != "initial" && (ids.IMDBID != 555099 || metadata.IMDB == nil) {
					t.Fatalf("%s: IMDb correction lost: %#v", phase, ids)
				}
				if phase != "initial" && phase != "IMDb correction" && (ids.TMDBID != 555001 || metadata.TMDB == nil) {
					t.Fatalf("%s: TMDB correction lost: %#v", phase, ids)
				}
			}
		})
	}
}

func TestPreparedProviderRetentionSurvivesCorrectionsAndRestart(t *testing.T) {
	t.Parallel()
	fixture := newCorrectionEvidenceFixtureForSource(t, "Example.Series.2026.1080p.WEB-DL.H264-GRP.mkv")
	fixture.input.Instructions = api.ReleaseFactInstructions{}
	fixture.tmdbAnime = true
	fixture.tmdbExternalIDs = `{"imdb_id":"tt0555002","tvdb_id":555003}`
	fixture.imdb = &stubIMDB{
		searchResult: imdb.SearchResult{IMDbID: 555002},
		infoFn: func(id string) imdb.Info {
			return imdb.Info{
				IMDbID: id,
				Title:  "Example Series",
				Year:   2026,
				Type:   "tvSeries",
			}
		},
	}
	fixture.tvdb = &stubTVDB{id: 555003, name: "Example Series"}
	fixture.tvmaze = &stubTVmaze{result: tvmaze.SearchResult{
		SelectedID: 555004,
		Candidates: []tvmaze.Candidate{{ID: 555004, Name: "Example Series"}},
	}}
	module, repo := fixture.open(t)
	input := fixture.input
	update := api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit}
	var initialRequests map[string]int
	for _, phase := range []string{"initial", "Keep and IMDb correction", "restart", "refresh", "Keep off", "clear TVDB", "Movie"} {
		switch phase {
		case "Keep and IMDb correction":
			input = api.PrepareInput{SourcePath: input.SourcePath, ExternalFreshness: api.ExternalFreshnessReuse}
			update = api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{
				Values: api.ReleaseCorrectionValues{Identity: api.ExternalIDOverrides{
					IMDBID:     new(555099),
					KeepTMDB:   new(true),
					KeepIMDB:   new(true),
					KeepTVDB:   new(true),
					KeepTVmaze: new(true),
					KeepMAL:    new(true),
				}},
			}}
		case "restart":
			if err := repo.Close(); err != nil {
				t.Fatal(err)
			}
			module, repo = fixture.open(t)
			input.ExternalFreshness = api.ExternalFreshnessLoad
			update = api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit}
		case "refresh":
			input.ExternalFreshness = api.ExternalFreshnessRefresh
		case "Keep off":
			input.ExternalFreshness = api.ExternalFreshnessReuse
			update = api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{
				Values: api.ReleaseCorrectionValues{Identity: api.ExternalIDOverrides{KeepIMDB: new(false)}},
			}}
		case "clear TVDB":
			update = api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{
				Values: api.ReleaseCorrectionValues{Identity: api.ExternalIDOverrides{TVDBID: new(0)}},
			}}
		case "Movie":
			update = api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{
				Values: api.ReleaseCorrectionValues{ReleaseName: api.ReleaseNameOverrides{Category: new("MOVIE")}},
			}}
		}
		result := prepareCorrectionEvidence(t, module, input, update)
		ids, metadata := result.Release.Identity, result.Release.ProviderMetadata
		wantIMDb, wantTVDB := 555099, 555003
		if phase == "initial" {
			wantIMDb = 555002
			initialRequests = fixture.requests()
		}
		if phase == "clear TVDB" || phase == "Movie" {
			wantTVDB = 0
		}
		if ids.IMDBID != wantIMDb || ids.TVDBID != wantTVDB || metadata.IMDB == nil {
			t.Fatalf("%s: provider correction or clear lost: %#v", phase, ids)
		}
		if phase == "Movie" {
			if ids.TMDBID != 0 || ids.TVmazeID != 0 || metadata.TMDB != nil || metadata.TVDB != nil || metadata.TVmaze != nil {
				t.Fatalf("category change retained incompatible automatic providers: %#v", ids)
			}
			continue
		}
		if ids.TMDBID != 555001 || ids.TVmazeID != 555004 || ids.MALID != 444 || metadata.TMDB == nil || metadata.TVmaze == nil || metadata.AniList == nil ||
			wantTVDB > 0 && metadata.TVDB == nil || wantTVDB == 0 && metadata.TVDB != nil {
			t.Fatalf("%s: kept identities or metadata were lost: %#v", phase, ids)
		}
		if phase == "Keep and IMDb correction" || phase == "restart" {
			for endpoint, count := range initialRequests {
				if strings.HasPrefix(endpoint, "/3/tv/555001") && fixture.requests()[endpoint] != count {
					t.Fatalf("%s fetched kept TMDB data again at %s", phase, endpoint)
				}
			}
		}
		if phase != "initial" {
			corrections := result.Corrections.Corrections.Identity
			if corrections.KeepTMDB == nil || !*corrections.KeepTMDB || ids.Provenance.TMDB != api.IdentityProvenanceRetained ||
				ids.Overrides.TMDB != api.OverrideStateUnset || ids.Provenance.IMDB != api.IdentityProvenanceExplicit {
				t.Fatalf("%s: Keep choices were not saved independently of manual IDs: %#v", phase, ids)
			}
		}
	}
}

func TestPreparedExplicitTVAcquiresTVDBDespiteParsedMovie(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		aliasYear bool
		edit      string
	}{
		{edit: "automatic"},
		{aliasYear: true, edit: "automatic"},
		{aliasYear: true, edit: "before refresh"},
		{edit: "after refresh"},
		{aliasYear: true, edit: "with category correction"},
	} {
		aliasYear, edit := test.aliasYear, test.edit
		t.Run(fmt.Sprintf("alias=%t/%s", aliasYear, edit), func(t *testing.T) {
			t.Parallel()
			fixture := newCorrectionEvidenceFixtureForSource(t, "Example.Movie.2026.1080p.WEB-DL.H264-GRP.mkv")
			fixture.input.Instructions.Category = new(api.CanonicalCategoryMovie)
			fixture.tvdb = tvdb.NewClient(fixture.http, nil, "synthetic-api-key", "")
			fixture.tvdbAliasYear = aliasYear
			module, repo := fixture.open(t)
			movie := prepareCorrectionEvidence(t, module, fixture.input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
			if movie.Release.Naming.Year != 2026 {
				t.Fatalf("automatic Movie year = %d, want 2026", movie.Release.Naming.Year)
			}
			input := api.PrepareInput{SourcePath: fixture.input.SourcePath}
			for _, phase := range []string{"before refresh", "refresh", "after refresh"} {
				update := api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit}
				input.ExternalFreshness = api.ExternalFreshnessReuse
				switch phase {
				case "refresh":
					input.ExternalFreshness = api.ExternalFreshnessRefresh
				case edit:
					update = api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{
						Values: api.ReleaseCorrectionValues{ReleaseName: api.ReleaseNameOverrides{ManualYear: new(2030)}},
					}}
				}
				movie = prepareCorrectionEvidence(t, module, input, update)
			}
			wantMovieYear := 2026
			if edit == "before refresh" || edit == "after refresh" {
				wantMovieYear = 2030
			}
			if movie.Release.Identity.Category != api.CanonicalCategoryMovie || movie.Release.Naming.Year != wantMovieYear ||
				fixture.requests()["/v4/series/555003/extended"] != 0 || fixture.requests()["/v4/search"] != 0 {
				t.Fatal("explicit Movie lost year authority or fetched TVDB")
			}
			input.ExternalFreshness = api.ExternalFreshnessReuse
			update := api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{
				Values: api.ReleaseCorrectionValues{
					Identity: api.ExternalIDOverrides{TVDBID: new(555003)},
					ReleaseName: api.ReleaseNameOverrides{
						Category: new("TV"),
						Season:   new("01"),
						Episode:  new("01"),
					},
				},
			}}
			if edit == "with category correction" {
				update.Patch.Values.ReleaseName.ManualYear = new(2030)
			}
			for _, step := range []string{"correct to TV", "refresh", "SQLite reopen"} {
				switch step {
				case "refresh":
					input.ExternalFreshness = api.ExternalFreshnessRefresh
					update = api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit}
				case "SQLite reopen":
					if err := repo.Close(); err != nil {
						t.Fatal(err)
					}
					module, repo = fixture.open(t)
					input.ExternalFreshness = api.ExternalFreshnessLoad
				}
				before := fixture.requests()
				result := prepareCorrectionEvidence(t, module, input, update)
				after := fixture.requests()
				for _, endpoint := range []string{"/v4/series/555003/extended", "/v4/search"} {
					if step == "refresh" && after[endpoint] <= before[endpoint] {
						t.Errorf("refresh reused stale TVDB response for %s", endpoint)
					}
					if step == "SQLite reopen" && after[endpoint] != before[endpoint] {
						t.Errorf("SQLite reopen did not reuse TVDB response for %s", endpoint)
					}
				}
				if after["/v4/series/555003/extended"] == 0 || after["/v4/search"] == 0 {
					t.Errorf("%s: explicit TV correction skipped TVDB series/disambiguation acquisition", step)
				}
				provider := result.Release.ProviderMetadata.TVDB
				if provider == nil || provider.NameEnglish != "Example Series" || provider.Year != 2024 || provider.YearFromAlias != aliasYear || !provider.PosterThumbnailLookupAttempted ||
					provider.NameDisambiguation.Status != api.MetadataEvidenceStatusPartial ||
					provider.NameDisambiguation.CanonicalName != "Example Series" || provider.NameDisambiguation.SeriesYear != 2024 ||
					provider.NameDisambiguation.Source != "tvdb_v4_search_unpaged" {
					t.Errorf("%s: prepared TVDB series/alias/disambiguation evidence missing", step)
				}
				wantYear := 0
				if aliasYear {
					wantYear = 2024
				}
				if result.Release.Identity.Category != api.CanonicalCategoryTV || result.Release.Naming.Year != wantYear ||
					result.Release.Naming.YearProvenance != api.FactProvenanceAutomatic {
					t.Errorf("%s: prepared category/year = %s/%d, want TV/%d", step, result.Release.Identity.Category, result.Release.Naming.Year, wantYear)
				}
				if result.Corrections.Corrections.ReleaseName.ManualYear != nil || result.EffectiveInstructions.ReleaseName.ManualYear != nil {
					t.Errorf("%s: manual Movie year survived TV correction", step)
				}
				if _, bound := result.Corrections.Corrections.ContentBindings[api.CorrectionFieldReleaseNameManualYear]; bound {
					t.Errorf("%s: manual-year content binding survived TV correction", step)
				}
				display, err := preparedrelease.ProjectDisplay(result.Release)
				if err != nil {
					t.Fatal(err)
				}
				if display.ReleaseName != result.Release.Naming.ReleaseName || strings.Contains(display.ReleaseName, "2030") ||
					strings.Contains(display.ReleaseName, "2026") || strings.Contains(display.ReleaseName, "2024") != aliasYear {
					t.Errorf("%s: display name violates TVDB year authority: %q", step, display.ReleaseName)
				}
			}
			if aliasYear && edit == "automatic" {
				old, err := repo.LoadPreparedRelease(t.Context(), input.SourcePath)
				if err != nil {
					t.Fatal(err)
				}
				old.Compatibility.ContractVersion = "prepared-release-v26"
				old.Naming.Year = 0
				old.ProviderMetadata.TVDB = &api.TVDBMetadata{TVDBID: 555003}
				if err := repo.CommitPreparedRelease(t.Context(), old); err != nil {
					t.Fatal(err)
				}
				if err := repo.Close(); err != nil {
					t.Fatal(err)
				}
				module, _ = fixture.open(t)
				input.ExternalFreshness = api.ExternalFreshnessReuse
				updated := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
				if updated.Release.Generation != old.Generation+1 || updated.Release.Naming.Year != 2024 {
					t.Errorf("pre-TVDB-correction generation reused: generation=%d year=%d", updated.Release.Generation, updated.Release.Naming.Year)
				}
			}
		})
	}
}

func TestPreparedIndependentManualProviderIDsSurviveRefreshAndRestart(t *testing.T) {
	t.Parallel()
	for _, sequential := range []bool{false, true} {
		t.Run(fmt.Sprintf("sequential=%t", sequential), func(t *testing.T) {
			t.Parallel()
			fixture := newCorrectionEvidenceFixture(t)
			fixture.tmdbExternalIDs = `{"imdb_id":"tt7654321","tvdb_id":999888}`
			imdbProvider := &stubIMDB{info: imdb.Info{
				IMDbID:          "tt1234567",
				Title:           "Example Selected Series",
				Type:            "tvSeries",
				SpokenLanguages: []string{"French", "German"},
			}}
			fixture.imdb = imdbProvider
			tvdbProvider := &stubTVDB{seriesMetadata: tvdb.SeriesMetadata{TVDBID: 222333, Name: "Example Selected Series"}}
			fixture.tvdb = tvdbProvider
			tvmazeProvider := &stubTVmaze{result: tvmaze.SearchResult{
				SelectedID: 55,
				IMDBID:     8887777,
				TVDBID:     888777,
				Candidates: []tvmaze.Candidate{{
					ID:        55,
					Name:      "Example TVmaze Series",
					Externals: tvmaze.Externals{IMDB: "tt8887777", TVDB: 888777},
				}},
			}}
			fixture.tvmaze = tvmazeProvider
			module, repo := fixture.open(t)
			prepareCorrectionEvidence(t, module, fixture.input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
			input := api.PrepareInput{SourcePath: fixture.input.SourcePath, ExternalFreshness: api.ExternalFreshnessReuse}
			patches := []api.ExternalIDOverrides{{
				TMDBID:   new(555002),
				IMDBID:   new(1234567),
				TVDBID:   new(222333),
				TVmazeID: new(55),
			}}
			if sequential {
				patches = []api.ExternalIDOverrides{{
					IMDBID:   new(1234567),
					TVDBID:   new(222333),
					TVmazeID: new(55),
				}, {TMDBID: new(555002)}}
			}
			for _, patch := range patches {
				result := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{
					Mode:  api.ReleaseCorrectionUpdatePatch,
					Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{Identity: patch}},
				})
				if result.Release.ProviderMetadata.TMDB == nil || result.Release.ProviderMetadata.IMDB == nil || result.Release.ProviderMetadata.TVDB == nil || result.Release.ProviderMetadata.TVmaze == nil {
					t.Fatal("manual selection discarded successfully fetched provider metadata")
				}
			}
			parentT := t
			for _, phase := range []struct {
				name, preparedVersion, identityVersion string
			}{
				{name: "reuse"},
				{name: "refresh"},
				{name: "restart"},
				{
					name:            "old contract",
					preparedVersion: "prepared-release-v56",
					identityVersion: "external-identity-v3",
				},
				{
					name:            "main before merge",
					preparedVersion: "prepared-release-v61",
					identityVersion: "external-identity-v4",
				},
				{
					name:            "PR before merge",
					preparedVersion: "prepared-release-v57",
					identityVersion: "external-identity-v5",
				},
				{
					name:            "owner merge",
					preparedVersion: "prepared-release-v62",
					identityVersion: "external-identity-v5",
				},
			} {
				t.Run(phase.name, func(t *testing.T) {
					input.ExternalFreshness = api.ExternalFreshnessReuse
					var staleGeneration api.PreparedGeneration
					if phase.name == "refresh" {
						input.ExternalFreshness = api.ExternalFreshnessRefresh
					}
					if phase.preparedVersion != "" {
						stale, err := repo.LoadPreparedRelease(t.Context(), input.SourcePath)
						if err != nil {
							t.Fatal(err)
						}
						staleGeneration = stale.Generation
						stale.Compatibility.ContractVersion = phase.preparedVersion
						stale.Identity.Resolution.ContractVersion = phase.identityVersion
						stale.ProviderMetadata.TMDB = nil
						if err := repo.CommitPreparedRelease(t.Context(), stale); err != nil {
							t.Fatal(err)
						}
					}
					if phase.name == "restart" || phase.preparedVersion != "" {
						if err := repo.Close(); err != nil {
							t.Fatal(err)
						}
						module, repo = fixture.open(parentT)
					}
					before := fixture.requests()
					beforeIMDB, beforeTVDB, beforeTVmaze := imdbProvider.infoCalls, tvdbProvider.seriesMetadataCalls, tvmazeProvider.calls
					result := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
					ids, providers := result.Release.Identity, result.Release.ProviderMetadata
					if ids.TMDBID != 555002 || ids.IMDBID != 1234567 || ids.TVDBID != 222333 ||
						providers.TMDB == nil || providers.TMDB.TMDBID != 555002 || providers.TMDB.Title != "Alternate Series" ||
						providers.IMDB == nil || providers.TVDB == nil || providers.TVmaze == nil || providers.TVmaze.TVmazeID != 55 || ids.TVmazeID != 55 {
						t.Fatalf("manual provider authority lost: IDs=%#v", ids)
					}
					if result.Corrections.Corrections.Identity.TMDBID == nil || *result.Corrections.Corrections.Identity.TMDBID != 555002 ||
						result.Corrections.Corrections.Identity.IMDBID == nil || *result.Corrections.Corrections.Identity.IMDBID != 1234567 ||
						result.Corrections.Corrections.Identity.TVDBID == nil || *result.Corrections.Corrections.Identity.TVDBID != 222333 ||
						result.Corrections.Corrections.Identity.TVmazeID == nil || *result.Corrections.Corrections.Identity.TVmazeID != 55 {
						t.Fatal("saved manual corrections lost")
					}
					if result.Release.Episode.Title != "The Other Signal" || !strings.Contains(result.Release.Naming.ReleaseName, "The Other Signal") {
						t.Fatalf("accepted provider did not reach episode naming: %q", result.Release.Naming.ReleaseName)
					}
					display, err := preparedrelease.ProjectDisplay(result.Release)
					if err != nil {
						t.Fatal(err)
					}
					if len(display.Providers) != 4 {
						t.Fatalf("provider display count = %d", len(display.Providers))
					}
					for _, provider := range display.Providers {
						if provider.Provider == api.IdentityProviderIMDB && !slices.Equal(provider.Details.IMDB.SpokenLanguages, []string{"French", "German"}) {
							t.Fatal("IMDb spoken languages lost during preparation, refresh or restart")
						}
					}
					ref := api.ReleaseRef{SourcePath: input.SourcePath, Generation: result.Release.Generation}
					upload, err := module.ResolveUploadSubject(t.Context(), api.UploadSubjectInput{Release: ref})
					if err != nil {
						t.Fatal(err)
					}
					duplicate, err := module.ResolveDuplicateSubject(t.Context(), api.DuplicateCheckInput{Release: ref})
					if err != nil {
						t.Fatal(err)
					}
					for _, subject := range []struct {
						ids      api.ExternalIdentity
						metadata api.SourceScopedMetadata
						name     string
					}{
						{upload.Identity, upload.ProviderMetadata, upload.ReleaseName},
						{duplicate.Identity, duplicate.ProviderMetadata, duplicate.ReleaseName},
					} {
						if subject.ids.TMDBID != 555002 || subject.ids.IMDBID != 1234567 || subject.ids.TVDBID != 222333 ||
							subject.metadata.TMDB == nil || subject.metadata.IMDB == nil || subject.metadata.TVDB == nil || subject.metadata.TVmaze == nil || subject.ids.TVmazeID != 55 || subject.name != display.ReleaseName {
							t.Fatal("downstream subject lost selected IDs, metadata or naming")
						}
					}
					if phase.name == "refresh" {
						after := fixture.requests()
						for _, endpoint := range []string{"/3/tv/555002", "/3/tv/555002/external_ids"} {
							if after[endpoint] <= before[endpoint] {
								t.Fatalf("refresh reused old TMDB evidence at %s", endpoint)
							}
						}
						if imdbProvider.infoCalls <= beforeIMDB || tvdbProvider.seriesMetadataCalls <= beforeTVDB || tvmazeProvider.calls <= beforeTVmaze {
							t.Fatal("refresh reused old IMDb, TVDB or TVmaze evidence")
						}
					} else {
						fixture.assertRequests(t, before, nil)
						if phase.name == "reuse" || phase.name == "restart" {
							if imdbProvider.infoCalls != beforeIMDB || tvdbProvider.seriesMetadataCalls != beforeTVDB || tvmazeProvider.calls != beforeTVmaze {
								t.Fatal("cached IMDb, TVDB or TVmaze evidence was re-fetched")
							}
						}
					}
					if phase.preparedVersion != "" && result.Release.Generation <= staleGeneration {
						t.Fatal("old rejection semantics reused")
					}
				})
			}
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
	dbPath                  string
	mediaInfo               mediainfo.Exporter
	input                   api.PrepareInput
	http                    *http.Client
	tvdb                    TVDBClient
	imdb                    IMDBClient
	tmdbExternalIDs         string
	tmdbFindIMDb            bool
	tmdbSpokenLanguages     []string
	tmdbOriginalLanguage    string
	tmdbAnime               bool
	anilistSeasonTitles     bool
	anilistSeasonTwoNoMAL   bool
	anilistSeasonThreeNoMAL bool
	anilistSearchDown       bool
	tmdbSeriesDown          bool
	noTMDBResults           bool
	tmdbSearchID            int
	tvmaze                  TVmazeClient
	tvdbAliasYear           bool
	tvdbRemoteIDs           string
	mu                      sync.Mutex
	calls                   map[string]int
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
		calls:  make(map[string]int),
		tvdb:   &stubTVDB{},
		imdb:   &stubIMDB{},
		tvmaze: &stubTVmaze{},
	}
	fixture.http = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		key := request.URL.Path
		if request.URL.Host == "api4.thetvdb.com" && key == "/v4/series/555003/extended" && request.URL.RawQuery != "" {
			key += "?" + request.URL.RawQuery
		}
		fixture.mu.Lock()
		fixture.calls[key]++
		fixture.mu.Unlock()
		body := ""
		switch {
		case request.URL.Host == "scene.example.invalid":
			body = `{"resultsCount":0,"results":[]}`
		case request.URL.Host == "graphql.anilist.co":
			var payload struct {
				Variables struct {
					IDMal int `json:"idMal"`
				} `json:"variables"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				return nil, fmt.Errorf("decode AniList fixture request: %w", err)
			}
			if payload.Variables.IDMal == 0 {
				if fixture.anilistSearchDown {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(`{"data":null,"errors":[{"message":"synthetic upstream failure"}]}`)),
					}, nil
				}
				body = `{"data":{"Page":{"media":[{"id":445,"idMal":444,"title":{"romaji":"Example Series"}}]}}}`
				if fixture.anilistSeasonTitles {
					seasonTwoMAL := `"idMal":445,`
					if fixture.anilistSeasonTwoNoMAL {
						seasonTwoMAL = ""
					}
					seasonThreeMAL := `"idMal":446,`
					if fixture.anilistSeasonThreeNoMAL {
						seasonThreeMAL = ""
					}
					body = `{"data":{"Page":{"media":[
						{"id":445,"idMal":444,"title":{"english":"Example Series","romaji":"Aoi Hoshi"},"tags":[{"name":"Shounen"}]},
						{"id":446,` + seasonTwoMAL + `"title":{"english":"Example Series 2nd Season","romaji":"Akai Hoshi"}},
						{"id":447,` + seasonThreeMAL + `"title":{"english":"Example Series 3rd Season","romaji":"Midori Hoshi"}},
						{"id":448,"idMal":447,"title":{"english":"Example Series 4th Season","romaji":"Murasaki Hoshi"}}
					]}}}`
				}
			} else {
				body = fmt.Sprintf(`{"data":{"Media":{"id":445,"idMal":%d,"title":{"romaji":"Example Series"}}}}`, payload.Variables.IDMal)
			}
		case request.URL.Host == "api4.thetvdb.com":
			switch request.URL.Path {
			case "/v4/login":
				body = `{"data":{"token":"synthetic-token"}}`
			case "/v4/series/555003/extended":
				body = `{"data":{"id":555003,"name":"Example Series","year":"2024","firstAired":"2024-01-01","originalLanguage":"eng"}}`
				if fixture.tvdbRemoteIDs != "" {
					body = strings.Replace(body, `"id":555003`, `"id":555003,"remoteIds":`+fixture.tvdbRemoteIDs, 1)
				}
			case "/v4/series/555003/translations/eng":
				body = `{"data":{"name":"Example Series","aliases":[]}}`
				if fixture.tvdbAliasYear {
					body = `{"data":{"name":"Example Series","aliases":["Example Series (2024)"]}}`
				}
			case "/v4/search":
				if query := request.URL.Query(); query.Get("query") != "Example Series" || query.Get("type") != "series" || len(query) != 2 {
					t.Error("TVDB disambiguation did not search the unfiltered canonical series name")
				}
				body = `{"data":[{"tvdb_id":"555003","name":"Example Series","primary_language":"eng","year":"2024"}]}`
			case "/v4/search/remoteid/555001":
				body = `{"data":[]}`
			case "/v4/series/555003/episodes/default":
				body = `{"data":{"episodes":[{"id":555301,"name":"The First Signal","seasonNumber":1,"number":1,"aired":"2024-01-01"}],"slug":"example-series"}}`
			case "/v4/episodes/555301/translations/eng":
				body = `{"data":{"name":"The First Signal"}}`
			}
		case request.URL.Host != "api.themoviedb.org":
			t.Errorf("unexpected HTTP destination: %s", request.URL.Host)
		case key == "/3/tv/555001":
			if fixture.tmdbSeriesDown {
				return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("{}"))}, nil
			}
			body = `{"id":555001,"name":"Example Series","original_name":"Example Series","first_air_date":"2026-01-01","original_language":"en","type":"Scripted","seasons":[{"season_number":1,"air_date":"2026-01-01"}]}`
			if fixture.tmdbAnime {
				body = strings.Replace(body, `"original_language":"en"`, `"original_language":"ja","genres":[{"id":16,"name":"Animation"}]`, 1)
				body = strings.Replace(body, `"seasons":[`, `"seasons":[{"season_number":2,"air_date":"2026-02-01"},`, 1)
			}
		case key == "/3/search/tv":
			body = `{"results":[{"id":555001,"name":"Example Series","original_name":"Example Series","first_air_date":"2026-01-01","original_language":"en"}]}`
			if fixture.noTMDBResults {
				body = `{"results":[]}`
			} else if fixture.tmdbSearchID != 0 {
				body = strings.Replace(body, "\"id\":555001", "\"id\":"+strconv.Itoa(fixture.tmdbSearchID), 1)
			}
		case key == "/3/search/movie":
			body = `{"results":[]}`
			if fixture.tmdbAnime && strings.EqualFold(request.URL.Query().Get("query"), "Example Movie") {
				body = `{"results":[{"id":555001,"title":"Example Movie","original_title":"Example Movie","release_date":"2026-01-01"}]}`
			}
		case key == "/3/find/555003":
			body = `{"tv_results":[{"id":555001,"name":"Example Series","first_air_date":"2026-01-01"}]}`
		case key == "/3/find/tt0555002", key == "/3/find/tt0555099":
			body = `{"movie_results":[],"tv_results":[]}`
			if fixture.tmdbFindIMDb && key == "/3/find/tt0555002" {
				body = `{"tv_results":[{"id":555001,"name":"Example Series","first_air_date":"2026-01-01"}]}`
			}
		case key == "/3/tv/555002":
			body = `{"id":555002,"name":"Alternate Series","original_name":"Alternate Series","first_air_date":"2026-01-01","original_language":"en","type":"Scripted"}`
		case key == "/3/tv/555002/season/1/episode/1":
			body = `{"id":555201,"name":"The Other Signal","season_number":1,"episode_number":1,"air_date":"2026-01-01"}`
		case key == "/3/tv/555001/season/1":
			body = `{"id":555010,"season_number":1,"episodes":[{"episode_number":1,"air_date":"2026-01-01"},{"episode_number":2,"air_date":"2026-01-02"},{"episode_number":3,"air_date":"2026-01-03"}]}`
		case key == "/3/tv/555001/season/2":
			body = `{"id":555020,"season_number":2,"episodes":[{"episode_number":1,"air_date":"2026-02-01"}]}`
		case key == "/3/tv/555001/season/1/episode/3":
			body = `{"id":555103,"name":"The Third Signal","season_number":1,"episode_number":3,"air_date":"2026-01-03"}`
		case key == "/3/tv/555001/season/2/episode/1":
			body = `{"id":555211,"name":"The Returning Signal","season_number":2,"episode_number":1,"air_date":"2026-02-01"}`
		case key == "/3/tv/555001/season/3/episode/1":
			body = `{"id":555311,"name":"The Final Signal","season_number":3,"episode_number":1,"air_date":"2026-03-01"}`
		case key == "/3/tv/555001/season/4/episode/1":
			body = `{"id":555411,"name":"The Next Signal","season_number":4,"episode_number":1,"air_date":"2026-04-01"}`
		case key == "/3/tv/555001/season/5/episode/1", key == "/3/tv/555001/season/1/episode/5", key == "/3/tv/555001/season/5/episode/5":
			parts := strings.Split(key, "/")
			body = fmt.Sprintf(`{"id":555505,"name":"Corrected Signal","season_number":%s,"episode_number":%s,"air_date":"2026-05-05"}`, parts[5], parts[7])
		case key == "/3/movie/555001":
			body = `{"id":555001,"title":"Example Movie","original_title":"Example Movie","release_date":"2026-01-01","original_language":"en"}`
			if fixture.tmdbAnime {
				body = strings.Replace(body, `"original_language":"en"`, `"original_language":"ja","genres":[{"id":16,"name":"Animation"}]`, 1)
			}
		case key == "/3/tv/555001/season/1/episode/1":
			body = `{"id":555101,"name":"The First Signal","season_number":1,"episode_number":1,"air_date":"2026-01-01"}`
		case key == "/3/tv/555001/season/1/episode/2":
			body = `{"id":555102,"name":"The Second Signal","season_number":1,"episode_number":2,"air_date":"2026-01-02"}`
		default:
			for _, namespace := range []string{"tv/555001", "tv/555002", "movie/555001"} {
				prefix := "/3/" + namespace + "/"
				switch key {
				case prefix + "external_ids":
					body = fixture.tmdbExternalIDs
					if body == "" {
						body = `{}`
					}
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
		if (key == "/3/movie/555001" || key == "/3/tv/555001") && fixture.tmdbOriginalLanguage != "" {
			body = strings.Replace(body, `"original_language":"en"`, `"original_language":"`+fixture.tmdbOriginalLanguage+`"`, 1)
		}
		if (key == "/3/movie/555001" || key == "/3/tv/555001") && fixture.tmdbSpokenLanguages != nil {
			spoken := make([]map[string]string, 0, len(fixture.tmdbSpokenLanguages))
			for _, language := range fixture.tmdbSpokenLanguages {
				spoken = append(spoken, map[string]string{"iso_639_1": language})
			}
			encoded, err := json.Marshal(spoken)
			if err != nil {
				return nil, fmt.Errorf("encode spoken-language fixture: %w", err)
			}
			body = strings.TrimSuffix(body, "}") + `,"spoken_languages":` + string(encoded) + "}"
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

func (f *correctionEvidenceFixture) open(t *testing.T, overrides ...Option) (*preparedrelease.Module, *db.SQLiteRepository) {
	t.Helper()
	if _, err := os.Stat(f.dbPath); os.IsNotExist(err) {
		dbfixture.WriteMigrated(t, f.dbPath)
	}
	repo, err := db.OpenContext(t.Context(), f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	mediaInfo := f.mediaInfo
	if mediaInfo == nil {
		mediaInfo = &stubMediaInfo{}
	}
	options := []Option{
		WithConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: f.dbPath}}),
		WithMediaInfoExporter(mediaInfo),
		WithSceneDetector(newSRRDBDetector(f.http, "https://scene.example.invalid", "", "")),
		WithTMDBClient(tmdb.NewClient(f.http, nil, "synthetic-api-key")),
		WithIMDBClient(f.imdb), WithTVDBClient(f.tvdb), WithTVmazeClient(f.tvmaze),
	}
	service := newTestService(t, repo, slices.Concat(options, overrides)...)
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

func TestPrepareAnimeSeasonCorrectionRebindsMALAndNamingEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name           string
		overrides      api.ExternalIDOverrides
		wantMAL        int
		noSeasonTwoMAL bool
		dateMapped     bool
		resetMAL       bool
		refresh        bool
	}{
		{name: "automatic and Auto reset", wantMAL: 445},
		{name: "automatic season without a MAL ID", noSeasonTwoMAL: true},
		{
			name:           "refresh season without a MAL ID",
			noSeasonTwoMAL: true,
			refresh:        true,
		},
		{
			name:       "date maps to S02 before S02 is used for ranking",
			dateMapped: true,
			wantMAL:    445,
		},
		{
			name:           "date maps to S02 without a MAL ID",
			dateMapped:     true,
			noSeasonTwoMAL: true,
		},
		{
			name:      "Keep TMDB protects only the TMDB record",
			overrides: api.ExternalIDOverrides{KeepTMDB: new(true)},
			wantMAL:   445,
		},
		{
			name:           "kept TMDB season without a MAL ID",
			overrides:      api.ExternalIDOverrides{KeepTMDB: new(true)},
			noSeasonTwoMAL: true,
		},
		{
			name:      "MAL Auto after changing season with TMDB kept",
			overrides: api.ExternalIDOverrides{KeepTMDB: new(true), MALID: new(0)},
			resetMAL:  true,
		},
		{
			name:      "Keep MAL",
			overrides: api.ExternalIDOverrides{KeepMAL: new(true)},
			wantMAL:   444,
		},
		{
			name:      "manual MAL",
			overrides: api.ExternalIDOverrides{MALID: new(999)},
			wantMAL:   999,
		},
		{name: "clear MAL", overrides: api.ExternalIDOverrides{MALID: new(0)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			filename := "Example.Series.S01E01.1080p.WEB-DL.H264-GRP.mkv"
			if test.dateMapped {
				filename = "Example.Series.2026.02.01.1080p.WEB-DL.H264-GRP.mkv"
			}
			fixture := newCorrectionEvidenceFixtureForSource(t, filename)
			fixture.tmdbAnime = true
			fixture.anilistSeasonTitles = true
			fixture.anilistSeasonTwoNoMAL = test.noSeasonTwoMAL
			fixture.input.Instructions.Identity = api.ExternalIDOverrides{}
			input := fixture.input
			verified, err := preparedrelease.VerifyInputSource(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			input.VerifiedSource = &verified
			module, repo := fixture.open(t)
			initial := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
			input.ExternalFreshness = api.ExternalFreshnessReuse
			aka, demographic := "AKA Akai Hoshi", "Mina"
			if test.noSeasonTwoMAL {
				aka, demographic = "", ""
			}
			if test.dateMapped {
				assertPreparedAnime(t, initial.Release, "S02", test.wantMAL, aka, demographic)
				calls := fixture.requests()
				input.Force = true
				same := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
				assertPreparedAnime(t, same.Release, "S02", test.wantMAL, aka, demographic)
				fixture.assertRequests(t, calls, nil)
				return
			}
			assertPreparedAnime(t, initial.Release, "S01", 444, "AKA Aoi Hoshi", "Shounen")
			if test.refresh {
				input.ExternalFreshness = api.ExternalFreshnessRefresh
			}
			before := fixture.requests()["/3/tv/555001"]
			corrected := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{
				Mode: api.ReleaseCorrectionUpdatePatch,
				Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{
					ReleaseName: api.ReleaseNameOverrides{Season: new("S02")},
					Identity:    test.overrides,
				}},
			})
			release := corrected.Release
			if release.Episode.SeasonLabel != "S02" || release.Identity.MALID != test.wantMAL ||
				test.wantMAL != 0 && (release.ProviderMetadata.AniList == nil || release.ProviderMetadata.AniList.MALID != test.wantMAL) ||
				test.wantMAL == 0 && release.ProviderMetadata.AniList != nil {
				t.Fatalf("corrected episode, MAL and AniList evidence = %+v / %d / %+v, want MAL %d", release.Episode, release.Identity.MALID,
					release.ProviderMetadata.AniList, test.wantMAL)
			}
			if test.wantMAL == 445 || test.noSeasonTwoMAL {
				assertPreparedAnime(t, release, "S02", test.wantMAL, aka, demographic)
			}
			if test.refresh {
				if err := repo.Close(); err != nil {
					t.Fatal(err)
				}
				module, repo = fixture.open(t)
				stored, err := repo.LoadPreparedRelease(t.Context(), input.SourcePath)
				if err != nil {
					t.Fatal(err)
				}
				assertPreparedAnime(t, stored, "S02", 0, "", "")
				input.ExternalFreshness = api.ExternalFreshnessLoad
				reopened := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
				assertPreparedAnime(t, reopened.Release, "S02", 0, "", "")
				return
			}
			if test.overrides.KeepTMDB != nil && fixture.requests()["/3/tv/555001"] != before {
				t.Fatal("season change fetched a kept TMDB record")
			}
			if test.resetMAL {
				if release.ProviderMetadata.TMDB.AnimeSeason != "S01" {
					t.Fatalf("clearing MAL changed the ranking season: %+v", release.ProviderMetadata.TMDB)
				}
				auto := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{
					Mode:  api.ReleaseCorrectionUpdatePatch,
					Patch: &api.ReleaseCorrectionPatch{ResetFields: []api.CorrectionFieldRef{{Field: api.CorrectionFieldIdentityMAL}}},
				})
				assertPreparedAnime(t, auto.Release, "S02", 445, "AKA Akai Hoshi", "Mina")
				if fixture.requests()["/3/tv/555001"] != before {
					t.Fatal("MAL Auto fetched the kept TMDB record")
				}
			}
			if test.overrides == (api.ExternalIDOverrides{}) {
				calls := fixture.requests()
				input.Force = true
				same := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
				if same.Release.Identity.MALID != test.wantMAL || !reflect.DeepEqual(fixture.requests(), calls) {
					t.Fatalf("same-season reprepare changed MAL or repeated requests: MAL=%d", same.Release.Identity.MALID)
				}
				auto := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{
					Mode:  api.ReleaseCorrectionUpdatePatch,
					Patch: &api.ReleaseCorrectionPatch{ResetFields: []api.CorrectionFieldRef{{Field: api.CorrectionFieldReleaseNameSeason}}},
				})
				if auto.Release.Episode.SeasonLabel != "S01" || auto.Release.Identity.MALID != 444 ||
					auto.Release.ProviderMetadata.TMDB.RetrievedAKA != "AKA Aoi Hoshi" {
					t.Fatalf("Auto season reset = %q / MAL %d / AKA %q", auto.Release.Episode.SeasonLabel, auto.Release.Identity.MALID,
						auto.Release.ProviderMetadata.TMDB.RetrievedAKA)
				}
			}
		})
	}
}

func assertPreparedAnime(t *testing.T, release api.PreparedRelease, season string, malID int, aka, demographic string) {
	t.Helper()
	tmdb, anilist := release.ProviderMetadata.TMDB, release.ProviderMetadata.AniList
	scope := season
	if release.Identity.Category == api.CanonicalCategoryMovie {
		scope = "movie"
	}
	if release.Episode.SeasonLabel != season || release.Identity.MALID != malID ||
		tmdb == nil || tmdb.MALID != malID || tmdb.AnimeSeason != scope || tmdb.RetrievedAKA != aka || tmdb.Demographic != demographic ||
		malID == 0 && anilist != nil || malID > 0 && (anilist == nil || anilist.MALID != malID) {
		t.Fatalf("season=%q MAL=%d TMDB=%+v AniList=%+v, want season=%q MAL=%d AKA=%q demographic=%q",
			release.Episode.SeasonLabel, release.Identity.MALID, tmdb, anilist, season, malID, aka, demographic)
	}
}

func TestPrepareSeasonCorrectionWithdrawsTVDBMALAcrossRestart(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name         string
		freshness    api.ExternalFreshness
		manualMAL    *int
		restoreLinks bool
		refreshLinks string
	}{
		{name: "reuse", freshness: api.ExternalFreshnessReuse},
		{name: "refresh", freshness: api.ExternalFreshnessRefresh},
		{name: "manual TVmaze", freshness: api.ExternalFreshnessReuse},
		{name: "independent TVmaze", freshness: api.ExternalFreshnessRefresh},
		{name: "IMDb-derived TVmaze", freshness: api.ExternalFreshnessReuse},
		{name: "refresh IMDb-derived TVmaze", freshness: api.ExternalFreshnessRefresh},
		{name: "kept IMDb", freshness: api.ExternalFreshnessReuse},
		{name: "kept IMDb-derived TVmaze", freshness: api.ExternalFreshnessReuse},
		{name: "kept IMDb with derived TMDB", freshness: api.ExternalFreshnessReuse},
		{
			name:         "agreeing manual MAL",
			freshness:    api.ExternalFreshnessReuse,
			manualMAL:    new(444),
			restoreLinks: true,
		},
		{
			name:         "agreeing manual MAL after extended refresh removes IMDb and MAL",
			freshness:    api.ExternalFreshnessReuse,
			manualMAL:    new(444),
			restoreLinks: true,
			refreshLinks: "extended",
		},
		{
			name:         "agreeing manual MAL after links-only refresh removes IMDb",
			freshness:    api.ExternalFreshnessReuse,
			manualMAL:    new(444),
			restoreLinks: true,
			refreshLinks: "links-only",
		},
		{
			name:         "explicit MAL clear",
			freshness:    api.ExternalFreshnessReuse,
			manualMAL:    new(0),
			restoreLinks: true,
		},
		{
			name:      "disagreeing manual MAL",
			freshness: api.ExternalFreshnessReuse,
			manualMAL: new(445),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newCorrectionEvidenceFixture(t)
			fixture.tmdbAnime = true
			fixture.anilistSeasonTitles = true
			fixture.anilistSeasonThreeNoMAL = true
			fixture.tvdb = tvdb.NewClient(fixture.http, nil, "synthetic-api-key", "")
			fixture.tmdbExternalIDs = `{"tvdb_id":555003}`
			fixture.tvdbRemoteIDs = `[{"id":"444","sourceName":"MyAnimeList"},{"id":"555004","sourceName":"TVmaze"},{"id":"tt0555002","sourceName":"IMDB"}]`
			imdbDerivedTVmaze := strings.HasSuffix(test.name, "IMDb-derived TVmaze")
			if imdbDerivedTVmaze {
				fixture.tvdbRemoteIDs = `[{"id":"444","sourceName":"MyAnimeList"},{"id":"tt0555002","sourceName":"IMDB"}]`
			}
			if test.name == "kept IMDb" {
				fixture.tvdbRemoteIDs = strings.TrimSuffix(fixture.tvdbRemoteIDs, "]") + `,{"id":"555001","sourceName":"TheMovieDB.com","type":12}]`
			}
			imdbDerivedTMDB := test.name == "kept IMDb with derived TMDB"
			fixture.tmdbFindIMDb = imdbDerivedTMDB
			fixture.noTMDBResults = imdbDerivedTMDB
			fixture.tvmaze = &stubTVmaze{searchFn: func(input tvmaze.SearchInput) tvmaze.SearchResult {
				linkedIMDb := imdbDerivedTVmaze && input.ImdbID == "tt0555002"
				if input.ManualID != 555004 && !linkedIMDb {
					return tvmaze.SearchResult{}
				}
				result := tvmaze.SearchResult{SelectedID: 555004, Candidates: []tvmaze.Candidate{{ID: 555004, Name: "Example Series"}}}
				if test.name == "manual TVmaze" || test.manualMAL != nil {
					result.TVDBID = 555003
					result.Candidates[0].Externals.TVDB = 555003
				}
				if imdbDerivedTVmaze && input.ManualID == 0 {
					result.LookupDependency.IMDBID = 555002
				}
				return result
			}}
			fixture.imdb = &stubIMDB{info: imdb.Info{
				IMDbID: "tt0555002",
				Title:  "Example Series",
				Type:   "tvSeries",
			}}
			var options []Option
			if test.name == "independent TVmaze" {
				options = []Option{
					WithConfig(config.Config{
						MainSettings:   config.MainSettingsConfig{DBPath: fixture.dbPath},
						ArrIntegration: config.ArrIntegrationConfig{UseSonarr: true},
					}),
					WithSonarrClient(&stubArrClient{result: ArrLookupResult{Source: "sonarr", TVmazeID: 555004}}),
				}
			}
			input := fixture.input
			input.Instructions.Identity = api.ExternalIDOverrides{}
			verified, err := preparedrelease.VerifyInputSource(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			input.VerifiedSource = &verified
			module, repo := fixture.open(t, options...)
			inherit := api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit}
			initial := prepareCorrectionEvidence(t, module, input, inherit).Release
			assertPreparedAnime(t, initial, "S01", 444, "AKA Aoi Hoshi", "Shounen")
			if initial.Identity.Dependencies.MAL.TVDBID != 555003 {
				t.Fatalf("initial MAL does not depend on TVDB: %+v", initial.Identity.Dependencies.MAL)
			}
			wantTVmazeDependency := api.IdentityDependency{ID: 555004}
			switch {
			case imdbDerivedTVmaze:
				wantTVmazeDependency.IMDBID = 555002
			case test.name != "independent TVmaze":
				wantTVmazeDependency.TVDBID = 555003
			}
			if initial.Identity.IMDBID != 555002 || initial.ProviderMetadata.IMDB == nil || initial.Identity.Dependencies.IMDB.TVDBID != 555003 ||
				initial.Identity.TVmazeID != 555004 || initial.ProviderMetadata.TVmaze == nil || initial.Identity.Dependencies.TVmaze != wantTVmazeDependency {
				t.Fatalf("initial TVDB siblings have unexpected lineage: %+v", initial.Identity)
			}
			calls := fixture.requests()
			input.ExternalFreshness = api.ExternalFreshnessReuse
			input.Force = true
			same := prepareCorrectionEvidence(t, module, input, inherit).Release
			assertPreparedAnime(t, same, "S01", 444, "AKA Aoi Hoshi", "Shounen")
			fixture.assertRequests(t, calls, nil)

			input.ExternalFreshness = test.freshness
			var overrides api.ExternalIDOverrides
			switch test.name {
			case "manual TVmaze":
				overrides.TVmazeID = new(555004)
			case "kept IMDb", "kept IMDb with derived TMDB":
				overrides.KeepIMDB = new(true)
				overrides.KeepTVDB = new(true)
			case "kept IMDb-derived TVmaze":
				overrides.KeepTVmaze = new(true)
				overrides.KeepTVDB = new(true)
			}
			corrected := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{
				Mode: api.ReleaseCorrectionUpdatePatch,
				Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{
					Identity:    overrides,
					ReleaseName: api.ReleaseNameOverrides{Season: new("S02")},
				}},
			})
			assertConflict := func(release api.PreparedRelease, season string, disputedID int, kept bool) {
				t.Helper()
				metadata := release.ProviderMetadata
				if release.Episode.SeasonLabel != season || release.Identity.MALID != 0 || metadata.AniList != nil {
					t.Fatalf("correction retained a disputed MAL ID or AKA: identity=%+v TMDB=%+v", release.Identity, metadata.TMDB)
				}
				if imdbDerivedTMDB && !kept {
					if release.Identity.TMDBID != 0 || release.Identity.Dependencies.TMDB != (api.IdentityDependency{}) || metadata.TMDB != nil {
						t.Fatalf("TMDB survived withdrawal of its IMDb input: identity=%+v TMDB=%+v", release.Identity, metadata.TMDB)
					}
				} else if metadata.TMDB == nil || metadata.TMDB.MALID != disputedID || metadata.TMDB.AnimeSeason != season ||
					metadata.TMDB.RetrievedAKA != "" || release.Identity.TMDBID != 555001 {
					t.Fatalf("TMDB conflict evidence was lost: identity=%+v TMDB=%+v", release.Identity, metadata.TMDB)
				}
				if imdbDerivedTMDB && kept && release.Identity.Dependencies.TMDB != (api.IdentityDependency{
					ID:     555001,
					IMDBID: 555002,
					TVDBID: 555003,
				}) {
					t.Fatalf("TMDB at the Keep boundary does not depend on IMDb: %+v", release.Identity.Dependencies.TMDB)
				}
				if metadata.TVDB == nil || metadata.TVDB.RemoteLinks.Status != api.MetadataEvidenceStatusContradictory ||
					!slices.Contains(metadata.TVDB.RemoteLinks.Links, api.TVDBRemoteLink{Provider: api.IdentityProviderMAL, State: "conflicting"}) {
					t.Fatalf("TVDB did not retain the MAL conflict: %+v", metadata.TVDB)
				}
				var wantIMDb, wantTVmaze api.IdentityDependency
				switch {
				case test.name == "manual TVmaze" || test.name == "independent TVmaze":
					wantTVmaze.ID = 555004
				case kept && test.name == "kept IMDb-derived TVmaze":
					wantTVmaze = wantTVmazeDependency
				}
				if kept && overrides.KeepIMDB != nil {
					wantIMDb = api.IdentityDependency{ID: 555002, TVDBID: 555003}
				}
				if release.Identity.TVmazeID != wantTVmaze.ID || (metadata.TVmaze != nil) != (wantTVmaze.ID > 0) ||
					release.Identity.Dependencies.TVmaze != wantTVmaze || release.Identity.IMDBID != wantIMDb.ID ||
					(metadata.IMDB != nil) != (wantIMDb.ID > 0) || release.Identity.Dependencies.IMDB != wantIMDb {
					t.Fatalf("unexpected TVDB sibling authority after MAL conflict: identity=%+v", release.Identity)
				}
				if kept && (overrides.KeepIMDB != nil && release.Identity.Provenance.IMDB != api.IdentityProvenanceRetained ||
					overrides.KeepTVmaze != nil && release.Identity.Provenance.TVmaze != api.IdentityProvenanceRetained) {
					t.Fatalf("Keep authority was lost: identity=%+v", release.Identity)
				}
			}
			assertConflict(corrected.Release, "S02", 445, true)
			const conflict = "TMDB and TVDB reference different MAL IDs"
			if !hasPreparationWarning(corrected, conflict) {
				t.Fatalf("missing MAL conflict warning: %+v", corrected.Diagnostics)
			}
			if test.freshness == api.ExternalFreshnessReuse && fixture.requests()["/3/tv/555001"] != calls["/3/tv/555001"] {
				t.Fatal("reuse repeated the TMDB series lookup")
			}
			if err := repo.Close(); err != nil {
				t.Fatal(err)
			}
			module, repo = fixture.open(t, options...)
			stored, err := repo.LoadPreparedRelease(t.Context(), input.SourcePath)
			if err != nil {
				t.Fatal(err)
			}
			assertConflict(stored, "S02", 445, true)

			input.ExternalFreshness = api.ExternalFreshnessReuse
			if test.refreshLinks != "" {
				fingerprint := stored.Compatibility.SourceFingerprint + ":" + input.VerifiedSource.Identity.Digest
				ctx, scope := evidence.WithScope(t.Context(), repo, input.SourcePath, fingerprint, api.ExternalFreshnessReuse, nil)
				earlier, err := fixture.tvdb.GetSeriesRemoteLinks(ctx, 555003)
				if err != nil || scope.Err() != nil || !slices.Contains(earlier.Links, api.TVDBRemoteLink{
					Provider: api.IdentityProviderIMDB,
					ID:       555002,
					State:    "valid",
				}) {
					t.Fatalf("missing original TVDB IMDb link: links=%+v error=%v persistence=%v", earlier, err, scope.Err())
				}
				// Provider evidence can advance before a new preparation is published.
				ctx, scope = evidence.WithScope(t.Context(), repo, input.SourcePath, fingerprint, api.ExternalFreshnessRefresh, nil)
				var refreshed api.TVDBRemoteLinks
				if test.refreshLinks == "extended" {
					fixture.tvdbRemoteIDs = `[{"id":"555004","sourceName":"TVmaze"}]`
					var series tvdb.SeriesMetadata
					series, err = fixture.tvdb.GetSeriesMetadata(ctx, 555003)
					refreshed = series.RemoteLinks
				} else {
					fixture.tvdbRemoteIDs = `[{"id":"444","sourceName":"MyAnimeList"},{"id":"555004","sourceName":"TVmaze"}]`
					refreshed, err = fixture.tvdb.GetSeriesRemoteLinks(ctx, 555003)
				}
				if err != nil || scope.Err() != nil || refreshed.Status != api.MetadataEvidenceStatusComplete ||
					slices.ContainsFunc(refreshed.Links, func(link api.TVDBRemoteLink) bool { return link.Provider == api.IdentityProviderIMDB }) {
					t.Fatalf("TVDB refresh did not remove the old IMDb link: links=%+v error=%v persistence=%v", refreshed, err, scope.Err())
				}
			}
			before := fixture.requests()["/v4/series/555003/extended"]
			if test.manualMAL != nil {
				fixed := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{
					Mode: api.ReleaseCorrectionUpdatePatch,
					Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{
						Identity:    api.ExternalIDOverrides{KeepTVDB: new(true), MALID: test.manualMAL},
						ReleaseName: api.ReleaseNameOverrides{Season: new("S02")},
					}},
				})
				stored, err := repo.LoadPreparedRelease(t.Context(), input.SourcePath)
				if err != nil {
					t.Fatal(err)
				}
				wantIMDb := 555002
				if test.refreshLinks != "" {
					wantIMDb = 0
				}
				if fixed.Release.Generation != stored.Generation || fixed.Release.Generation <= corrected.Release.Generation {
					t.Fatalf("MAL correction did not publish a new generation: returned=%d stored=%d previous=%d",
						fixed.Release.Generation, stored.Generation, corrected.Release.Generation)
				}
				for _, release := range []api.PreparedRelease{fixed.Release, stored} {
					ids, metadata := release.Identity, release.ProviderMetadata
					if ids.TVDBID != 555003 || ids.MALID != *test.manualMAL || metadata.TVDB == nil ||
						(*test.manualMAL == 0 && (ids.Overrides.MAL != api.OverrideStateClear || metadata.AniList != nil)) ||
						(*test.manualMAL > 0 && (metadata.AniList == nil || metadata.AniList.MALID != *test.manualMAL)) {
						t.Fatalf("MAL correction lost user authority: identity=%+v AniList=%+v", ids, metadata.AniList)
					}
					if test.restoreLinks {
						if ids.IMDBID != wantIMDb || ids.TVmazeID != 555004 || (metadata.IMDB != nil) != (wantIMDb > 0) || metadata.TVmaze == nil ||
							metadata.TVDB.RemoteLinks.Status != api.MetadataEvidenceStatusComplete {
							t.Fatalf("MAL correction did not restore TVDB siblings: identity=%+v links=%+v", ids, metadata.TVDB.RemoteLinks)
						}
					} else if ids.IMDBID != 0 || ids.TVmazeID != 0 || metadata.IMDB != nil || metadata.TVmaze != nil ||
						metadata.TVDB.RemoteLinks.Status != api.MetadataEvidenceStatusContradictory {
						t.Fatalf("conflicting MAL admitted TVDB siblings: identity=%+v links=%+v", ids, metadata.TVDB.RemoteLinks)
					}
				}
				if hasPreparationWarning(fixed, conflict) || hasPreparationWarning(fixed, "TVDB series remote IDs conflict") == test.restoreLinks ||
					fixture.requests()["/v4/series/555003/extended"] != before {
					t.Fatalf("MAL correction used unexpected diagnostics or repeated TVDB evidence: %+v", fixed.Diagnostics)
				}
				return
			}
			seasons := []string{"S04", "S03", "S01"}
			if imdbDerivedTMDB {
				seasons = seasons[:2]
			}
			for _, season := range seasons {
				var retention api.ExternalIDOverrides
				if season == "S04" && overrides.KeepTVDB != nil {
					retention.KeepTVDB = new(false)
					if overrides.KeepIMDB != nil {
						retention.KeepIMDB = new(false)
					}
					if overrides.KeepTVmaze != nil {
						retention.KeepTVmaze = new(false)
					}
				}
				next := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{
					Mode: api.ReleaseCorrectionUpdatePatch,
					Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{
						Identity:    retention,
						ReleaseName: api.ReleaseNameOverrides{Season: new(season)},
					}},
				})
				switch season {
				case "S04":
					assertConflict(next.Release, "S04", 447, false)
					if !hasPreparationWarning(next, conflict) {
						t.Fatalf("missing S04 conflict warning: %+v", next.Diagnostics)
					}
					if imdbDerivedTMDB {
						stored, err := repo.LoadPreparedRelease(t.Context(), input.SourcePath)
						if err != nil {
							t.Fatal(err)
						}
						assertConflict(stored, "S04", 447, false)
					}
				case "S03":
					release := next.Release
					tmdb := release.ProviderMetadata.TMDB
					if imdbDerivedTMDB {
						if release.Identity.TMDBID != 0 || tmdb != nil || release.Identity.IMDBID != 0 ||
							!hasPreparationWarning(next, "TVDB series remote IDs conflict") {
							t.Fatalf("reuse restored withdrawn links without independent evidence: identity=%+v diagnostics=%+v", release.Identity, next.Diagnostics)
						}
						break
					}
					if tmdb == nil || tmdb.AnimeSeason != season || tmdb.MALID != 0 || release.Identity.MALID != 444 ||
						release.Identity.Dependencies.MAL.TVDBID != 555003 || release.Identity.IMDBID != 555002 || release.Identity.TVmazeID != 555004 ||
						release.ProviderMetadata.TVDB.RemoteLinks.Status != api.MetadataEvidenceStatusComplete || hasPreparationWarning(next, conflict) {
						t.Fatalf("completed empty AniList search did not restore raw TVDB links: identity=%+v diagnostics=%+v", release.Identity, next.Diagnostics)
					}
				default:
					assertPreparedAnime(t, next.Release, "S01", 444, "AKA Aoi Hoshi", "Shounen")
					if hasPreparationWarning(next, conflict) || next.Release.ProviderMetadata.TVDB.RemoteLinks.Status != api.MetadataEvidenceStatusComplete {
						t.Fatalf("returning to S01 did not restore TVDB agreement: %+v", next.Diagnostics)
					}
				}
				if fixture.requests()["/v4/series/555003/extended"] != before {
					t.Fatalf("%s repeated the TVDB extended request instead of replaying stored evidence", season)
				}
			}
		})
	}
}

func hasPreparationWarning(result api.PrepareResult, phrase string) bool {
	return slices.ContainsFunc(result.Diagnostics, func(d api.PreparationDiagnostic) bool {
		return d.Severity == api.DiagnosticSeverityWarning && strings.Contains(d.Message, phrase)
	})
}

func TestPrepareAnimeEpisodeMappingDisqualifiesTVDBIMDbVote(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, filename string
		independent    bool
		directLink     bool
		restart        bool
	}{
		{name: "TVDB absolute fallback", filename: "Example.Series.S01E13.1080p.WEB-DL.H264-GRP.mkv"},
		{name: "IMDb air date", filename: "Example.Series.2026.02.11.1080p.WEB-DL.H264-GRP.mkv"},
		{
			name:       "TVDB IMDb link and air date",
			filename:   "Example.Series.2026.02.11.1080p.WEB-DL.H264-GRP.mkv",
			directLink: true,
		},
		{
			name:     "IMDb confirmed by TVDB before restart",
			filename: "Example.Series.S01E01.1080p.WEB-DL.H264-GRP.mkv",
			restart:  true,
		},
		{
			name:        "independent IMDb confirmation before restart",
			filename:    "Example.Series.S01E01.1080p.WEB-DL.H264-GRP.mkv",
			independent: true,
			restart:     true,
		},
		{
			name:        "independent IMDb confirmation",
			filename:    "Example.Series.2026.02.11.1080p.WEB-DL.H264-GRP.mkv",
			independent: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newCorrectionEvidenceFixtureForSource(t, test.filename)
			fixture.tmdbAnime, fixture.anilistSeasonTitles = true, true
			tvdbClient := &stubTVDB{
				searchResults: []tvdb.SeriesSearchResult{{TVDBID: 555003, Name: "Example Series"}},
				seriesMetadata: tvdb.SeriesMetadata{
					TVDBID: 555003,
					Name:   "Example Series",
					RemoteLinks: api.TVDBRemoteLinks{
						Status: api.MetadataEvidenceStatusComplete,
						Links: []api.TVDBRemoteLink{
							{
								Provider: api.IdentityProviderMAL,
								ID:       444,
								State:    "valid",
							},
							{
								Provider: api.IdentityProviderIMDB,
								ID:       555002,
								State:    "valid",
							},
						},
					},
				},
			}
			if test.name == "TVDB absolute fallback" {
				tvdbClient.episodes.Episodes = []tvdb.Episode{{
					SeasonNumber:   2,
					Number:         1,
					AbsoluteNumber: 13,
					Name:           "TVDB Second Season",
				}}
			}
			fixture.tvdb = tvdbClient
			imdbClient := &stubIMDB{
				searchResult: imdb.SearchResult{Tentative: true, Candidates: []imdb.Candidate{{IMDbID: 555002, Confidence: .3}}},
				info: imdb.Info{
					IMDbID: "tt0555002",
					Title:  "Example Series",
					Type:   "tvSeries",
					Episodes: []imdb.Episode{{
						Title:       "IMDb Second Season",
						Season:      2,
						EpisodeText: "1",
						ReleaseDate: imdb.ReleaseDate{
							Year:  2026,
							Month: 2,
							Day:   11,
						},
					}},
				},
			}
			if test.directLink {
				imdbClient.searchResult = imdb.SearchResult{}
			}
			fixture.imdb = imdbClient
			if test.independent {
				fixture.tmdbExternalIDs = `{"imdb_id":"tt0555002"}`
			}
			input := fixture.input
			input.Instructions.Identity = api.ExternalIDOverrides{}
			module, repo := fixture.open(t)
			result := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
			if test.restart {
				assertPreparedAnime(t, result.Release, "S01", 444, "AKA Aoi Hoshi", "Shounen")
				if result.Release.Identity.IMDBID != 555002 || result.Release.ProviderMetadata.IMDB == nil {
					t.Fatalf("initial IMDb match was not confirmed: %+v", result.Release.Identity)
				}
				if err := repo.Close(); err != nil {
					t.Fatal(err)
				}
				module, repo = fixture.open(t)
				input.ExternalFreshness = api.ExternalFreshnessReuse
				result = prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{
					Mode: api.ReleaseCorrectionUpdatePatch,
					Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{
						ReleaseName: api.ReleaseNameOverrides{Season: new("S02")},
					}},
				})
			}
			wantTentative := !test.independent && !test.directLink && !test.restart
			if !hasPreparationWarning(result, "TMDB and TVDB reference different MAL IDs") ||
				hasPreparationWarning(result, "IMDb match remains tentative") != wantTentative {
				t.Fatalf("unexpected IMDb/TVDB diagnostics: %+v", result.Diagnostics)
			}
			if err := repo.Close(); err != nil {
				t.Fatal(err)
			}
			_, repo = fixture.open(t)
			stored, err := repo.LoadPreparedRelease(t.Context(), input.SourcePath)
			if err != nil {
				t.Fatal(err)
			}
			wantIMDb, wantSeason, wantInfoCalls := 0, "", 1
			if test.name == "TVDB absolute fallback" || test.restart {
				wantSeason = "S02"
			}
			if test.name == "TVDB absolute fallback" {
				wantInfoCalls = 0
			}
			if test.independent {
				wantIMDb, wantSeason = 555002, "S02"
			}
			for _, release := range []api.PreparedRelease{result.Release, stored} {
				if release.Identity.IMDBID != wantIMDb || (release.ProviderMetadata.IMDB != nil) != (wantIMDb > 0) ||
					release.Episode.SeasonLabel != wantSeason || (!test.independent && release.Episode.Title == "IMDb Second Season") ||
					release.ProviderMetadata.TMDB.AnimeSeason != "S02" {
					t.Fatalf("incorrect authority after episode mapping: identity=%+v episode=%+v", release.Identity, release.Episode)
				}
			}
			if !test.restart && imdbClient.infoCalls != wantInfoCalls {
				t.Fatalf("IMDb detail calls = %d, want %d", imdbClient.infoCalls, wantInfoCalls)
			}
		})
	}
}

func TestPrepareSelectedSeriesMALDisagreementBlocksSiblings(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		tvdbMAL  int
		wantIMDb int
	}{
		{
name: "providers agree",
 tvdbMAL: 444,
 wantIMDb: 555002,
},
		{name: "providers disagree", tvdbMAL: 445},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newCorrectionEvidenceFixture(t)
			fixture.tmdbAnime, fixture.anilistSeasonTitles = true, true
			fixture.tvdb = tvdb.NewClient(fixture.http, nil, "synthetic-api-key", "")
			fixture.tvdbRemoteIDs = fmt.Sprintf(`[{"id":"%d","sourceName":"MyAnimeList"},{"id":"tt0555002","sourceName":"IMDB"}]`, test.tvdbMAL)
			fixture.imdb = &stubIMDB{info: imdb.Info{
IMDbID: "tt0555002",
 Title: "Example Series",
 Type: "tvSeries",
}}
			input := fixture.input
			input.Instructions.Identity = api.ExternalIDOverrides{
				TMDBID:   new(555001),
				TVDBID:   new(555003),
				TVmazeID: new(0),
			}
			module, repo := fixture.open(t)
			for _, freshness := range []api.ExternalFreshness{api.ExternalFreshnessLoad, api.ExternalFreshnessRefresh} {
				input.ExternalFreshness = freshness
				result := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
				ids, metadata := result.Release.Identity, result.Release.ProviderMetadata
				if ids.TMDBID != 555001 || ids.TVDBID != 555003 || ids.MALID != 444 ||
					metadata.TMDB == nil || metadata.TVDB == nil || metadata.AniList == nil || metadata.AniList.MALID != 444 ||
					ids.IMDBID != test.wantIMDb || (metadata.IMDB != nil) != (test.wantIMDb > 0) {
					t.Fatalf("%s: selected identities or sibling inference differ: %+v", freshness, ids)
				}
				if freshness == api.ExternalFreshnessLoad {
					if err := repo.Close(); err != nil {
						t.Fatal(err)
					}
					module, _ = fixture.open(t)
				}
			}
		})
	}
}

func TestPrepareTVDBDerivedTMDBCannotCorroborateMALWithDailyMapping(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		manualTMDB *int
		wantMAL    int
		wantRank   string
	}{
		{name: "TMDB supplied by TVDB", wantRank: "none"},
		{
name: "manual TMDB",
 manualTMDB: new(555001),
 wantMAL: 445,
 wantRank: "S02",
},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newCorrectionEvidenceFixtureForSource(t, "Example.Series.2026.02.01.1080p.WEB-DL.H264-GRP.mkv")
			fixture.tmdbAnime, fixture.anilistSeasonTitles, fixture.noTMDBResults = true, true, true
			fixture.tvdb = tvdb.NewClient(fixture.http, nil, "synthetic-api-key", "")
			fixture.tvdbRemoteIDs = `[{"id":"445","sourceName":"MyAnimeList"},{"id":"555001","sourceName":"TheMovieDB.com","type":12}]`
			input := fixture.input
			input.Instructions.Identity = api.ExternalIDOverrides{
				TMDBID:   test.manualTMDB,
				TVDBID:   new(555003),
				IMDBID:   new(0),
				TVmazeID: new(0),
			}
			module, _ := fixture.open(t)
			result := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
			ids, metadata := result.Release.Identity, result.Release.ProviderMetadata
			if ids.TMDBID != 555001 || (ids.Dependencies.TMDB.TVDBID == 555003) != (test.manualTMDB == nil) ||
				result.Release.Episode.SeasonLabel != "S02" || !result.Release.Episode.DateMatched ||
				ids.MALID != test.wantMAL || metadata.TMDB == nil || metadata.TMDB.AnimeSeason != test.wantRank ||
				(metadata.AniList != nil) != (test.wantMAL > 0) {
				t.Fatalf("unexpected daily mapping or MAL evidence: identity=%+v episode=%+v TMDB=%+v", ids, result.Release.Episode, metadata.TMDB)
			}
		})
	}
}

func TestPrepareTVmazeIMDbFallbackSurvivesTVDBMALConflict(t *testing.T) {
	t.Parallel()
	fixture := newCorrectionEvidenceFixture(t)
	fixture.tmdbAnime, fixture.anilistSeasonTitles = true, true
	fixture.tmdbExternalIDs = `{"imdb_id":"tt0555002","tvdb_id":555003}`
	fixture.tmdbFindIMDb = true
	fixture.tvdb = tvdb.NewClient(fixture.http, nil, "synthetic-api-key", "")
	fixture.tvdbRemoteIDs = `[{"id":"444","sourceName":"MyAnimeList"}]`
	fixture.imdb = &stubIMDB{info: imdb.Info{
		IMDbID: "tt0555002",
		Title:  "Example Series",
		Type:   "tvSeries",
	}}
	var lookups []string
	fixture.tvmaze = tvmaze.NewClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		status, body := http.StatusOK, `[]`
		switch request.URL.Path {
		case "/lookup/shows":
			query := request.URL.Query()
			switch {
			case query.Get("thetvdb") == "555003":
				lookups = append(lookups, "TVDB")
				status = http.StatusNotFound
			case query.Get("imdb") == "tt0555002":
				lookups = append(lookups, "IMDb")
				body = `{"id":555004,"name":"Example Series","externals":{"imdb":"tt0555002","thetvdb":555003}}`
			default:
				t.Errorf("unexpected TVmaze lookup: %v", query)
			}
		case "/shows/555004/images":
		default:
			status = http.StatusNotFound
		}
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    request,
		}, nil
	})}, nil)
	input := fixture.input
	input.Instructions.Identity = api.ExternalIDOverrides{}
	module, _ := fixture.open(t)
	result := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{
		Mode: api.ReleaseCorrectionUpdatePatch,
		Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{
			Identity:    api.ExternalIDOverrides{IMDBID: new(555002), TVDBID: new(555003)},
			ReleaseName: api.ReleaseNameOverrides{Season: new("S02")},
		}},
	})
	identity := result.Release.Identity
	if !slices.Equal(lookups, []string{"TVDB", "IMDb"}) || !hasPreparationWarning(result, "TMDB and TVDB reference different MAL IDs") ||
		identity.TVmazeID != 555004 || result.Release.ProviderMetadata.TVmaze == nil ||
		identity.Dependencies.TVmaze != (api.IdentityDependency{ID: 555004, IMDBID: 555002}) {
		t.Fatalf("TVmaze fallback was lost: lookups=%v identity=%+v diagnostics=%+v", lookups, result.Release.Identity, result.Diagnostics)
	}
}

func TestPrepareRefreshWithdrawsStoredIMDbWhenTVDBCannotCorroborate(t *testing.T) {
	t.Parallel()
	fixture := newCorrectionEvidenceFixture(t)
	fixture.tmdbAnime, fixture.anilistSeasonTitles = true, true
	fixture.tvdb = &stubTVDB{
		searchResults: []tvdb.SeriesSearchResult{{TVDBID: 555003, Name: "Example Series"}},
		seriesMetadata: tvdb.SeriesMetadata{
			TVDBID: 555003,
			Name:   "Example Series",
			RemoteLinks: api.TVDBRemoteLinks{
				Status: api.MetadataEvidenceStatusComplete,
				Links: []api.TVDBRemoteLink{
					{
						Provider: api.IdentityProviderIMDB,
						ID:       555002,
						State:    "valid",
					},
					{
						Provider: api.IdentityProviderMAL,
						ID:       444,
						State:    "valid",
					},
				},
			},
		},
	}
	imdbClient := &stubIMDB{
		searchResult: imdb.SearchResult{IMDbID: 555002},
		info: imdb.Info{
			IMDbID: "tt0555002",
			Title:  "Example Series",
			Type:   "tvSeries",
		},
	}
	fixture.imdb = imdbClient
	fixture.tvmaze = &stubTVmaze{searchFn: func(input tvmaze.SearchInput) tvmaze.SearchResult {
		if input.ImdbID != "tt0555002" {
			return tvmaze.SearchResult{}
		}
		return tvmaze.SearchResult{
			SelectedID:       555004,
			IMDBID:           555002,
			Candidates:       []tvmaze.Candidate{{ID: 555004, Name: "Example Series"}},
			LookupDependency: api.IdentityDependency{IMDBID: 555002},
		}
	}}
	input := fixture.input
	input.Instructions.Identity = api.ExternalIDOverrides{}
	verified, err := preparedrelease.VerifyInputSource(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	input.VerifiedSource = &verified
	module, repo := fixture.open(t)
	initial := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
	assertPreparedAnime(t, initial.Release, "S01", 444, "AKA Aoi Hoshi", "Shounen")
	if initial.Release.Identity.IMDBID != 555002 || initial.Release.ProviderMetadata.IMDB == nil ||
		initial.Release.Identity.TVmazeID != 555004 || initial.Release.ProviderMetadata.TVmaze == nil ||
		initial.Release.Identity.Dependencies.TVmaze.IMDBID != 555002 {
		t.Fatalf("setup did not confirm IMDb and the dependent TVmaze match: %+v", initial.Release.Identity)
	}
	input.ExternalFreshness = api.ExternalFreshnessReuse
	kept := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{
		Mode: api.ReleaseCorrectionUpdatePatch,
		Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{
			Identity: api.ExternalIDOverrides{KeepIMDB: new(true)},
		}},
	})
	if kept.Release.Identity.IMDBID != 555002 || kept.Release.Identity.Provenance.IMDB != api.IdentityProvenanceRetained {
		t.Fatalf("setup did not retain IMDb: %+v", kept.Release.Identity)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	module, repo = fixture.open(t)
	imdbClient.searchResult = imdb.SearchResult{Tentative: true, Candidates: []imdb.Candidate{{IMDbID: 555002, Confidence: .3}}}
	input.ExternalFreshness = api.ExternalFreshnessRefresh
	kept = prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{
		Mode: api.ReleaseCorrectionUpdatePatch,
		Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{
			ReleaseName: api.ReleaseNameOverrides{Season: new("S02")},
		}},
	})
	if kept.Release.Identity.IMDBID != 555002 || kept.Release.Identity.Provenance.IMDB != api.IdentityProvenanceRetained ||
		kept.Release.ProviderMetadata.IMDB == nil || hasPreparationWarning(kept, "IMDb match remains tentative") {
		t.Fatalf("refresh with Keep IMDb still enabled lost retained authority: identity=%+v diagnostics=%+v", kept.Release.Identity, kept.Diagnostics)
	}
	result := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{
		Mode: api.ReleaseCorrectionUpdatePatch,
		Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{
			Identity:    api.ExternalIDOverrides{KeepIMDB: new(false)},
			ReleaseName: api.ReleaseNameOverrides{Season: new("S02")},
		}},
	})
	if !hasPreparationWarning(result, "IMDb match remains tentative") || !hasPreparationWarning(result, "TMDB and TVDB reference different MAL IDs") {
		t.Fatalf("refresh did not report tentative IMDb and conflicting MAL: %+v", result.Diagnostics)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	_, repo = fixture.open(t)
	stored, err := repo.LoadPreparedRelease(t.Context(), input.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, release := range []api.PreparedRelease{result.Release, stored} {
		if release.Identity.IMDBID != 0 || release.ProviderMetadata.IMDB != nil || release.Identity.TVmazeID != 0 || release.ProviderMetadata.TVmaze != nil ||
			release.ProviderMetadata.TVDB == nil || release.ProviderMetadata.TVDB.RemoteLinks.Status != api.MetadataEvidenceStatusContradictory {
			t.Fatalf("refresh published withdrawn IMDb or TVmaze evidence: identity=%+v", release.Identity)
		}
	}
}

func TestPrepareAnimeRefreshPreservesMALDuringAniListOutage(t *testing.T) {
	t.Parallel()
	for _, season := range []string{"S01", "S02"} {
		t.Run(season, func(t *testing.T) {
			t.Parallel()
			fixture := newCorrectionEvidenceFixture(t)
			fixture.tmdbAnime = true
			fixture.anilistSeasonTitles = true
			input := fixture.input
			input.Instructions.Identity = api.ExternalIDOverrides{}
			verified, err := preparedrelease.VerifyInputSource(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			input.VerifiedSource = &verified
			module, repo := fixture.open(t)
			inherit := api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit}
			initial := prepareCorrectionEvidence(t, module, input, inherit).Release
			assertPreparedAnime(t, initial, "S01", 444, "AKA Aoi Hoshi", "Shounen")
			if err := repo.Close(); err != nil {
				t.Fatal(err)
			}
			module, repo = fixture.open(t)
			before := fixture.requests()[""] // AniList has no URL path.
			fixture.anilistSearchDown = true
			input.ExternalFreshness = api.ExternalFreshnessRefresh
			update := inherit
			if season == "S02" {
				update = api.ReleaseCorrectionUpdate{
					Mode: api.ReleaseCorrectionUpdatePatch,
					Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{
						ReleaseName: api.ReleaseNameOverrides{Season: new(season)},
					}},
				}
			}
			refreshed := prepareCorrectionEvidence(t, module, input, update)
			release := refreshed.Release
			const outage = "AniList title search failed"
			if release.Episode.SeasonLabel != season || fixture.requests()[""] != before+1 || !hasPreparationWarning(refreshed, outage) {
				t.Fatalf("failed lookup for %s: episode=%+v requests=%+v diagnostics=%+v", season, release.Episode, fixture.requests(), refreshed.Diagnostics)
			}
			if tmdb := release.ProviderMetadata.TMDB; tmdb == nil || !tmdb.AnimeLookupPending {
				t.Fatalf("failed AniList search was not marked pending: %+v", tmdb)
			}
			if season == "S01" {
				assertPreparedAnime(t, release, "S01", 444, "AKA Aoi Hoshi", "Shounen")
			} else {
				stored, err := repo.LoadPreparedRelease(t.Context(), input.SourcePath)
				if err != nil {
					t.Fatal(err)
				}
				if got := stored.ProviderMetadata.TMDB; stored.Identity.MALID != 0 || got == nil || got.MALID != 0 ||
					got.AnimeSeason != "" || got.RetrievedAKA != "" || got.Demographic != "" || stored.ProviderMetadata.AniList != nil {
					t.Fatalf("changed season retained prior anime evidence: identity=%+v TMDB=%+v AniList=%+v", stored.Identity, got, stored.ProviderMetadata.AniList)
				}
				subject, err := module.ResolveUploadSubject(t.Context(), api.UploadSubjectInput{Release: api.ReleaseRef{
					SourcePath: input.SourcePath, Generation: release.Generation,
				}})
				if err != nil || subject.Identity.MALID != 0 || subject.ProviderMetadata.AniList != nil {
					t.Fatalf("changed season published stale upload evidence: identity=%+v error=%v", subject.Identity, err)
				}
				artifacts := t.TempDir()
				subject.MediaInfoTextPath = filepath.Join(artifacts, "mediainfo.txt")
				if err := os.WriteFile(subject.MediaInfoTextPath, []byte("Synthetic MediaInfo report"), 0o600); err != nil {
					t.Fatal(err)
				}
				subject.TorrentPath = filepath.Join(artifacts, "example.torrent")
				if err := os.WriteFile(subject.TorrentPath, []byte("synthetic torrent"), 0o600); err != nil {
					t.Fatal(err)
				}
				def := unit3d.NewWithProfile(unit3d.Profile{Name: "TEST", BaseURL: "https://tracker.example.invalid"})
				plan, failure := def.Prepare(t.Context(), trackers.PreparationInput{
					Intent:        trackers.PreparationIntentDryRun,
					Tracker:       "TEST",
					Meta:          subject,
					TrackerConfig: config.TrackerConfig{APIKey: "synthetic-api-key"},
					Runtime:       trackers.PreparationRuntime{DBPath: fixture.dbPath},
					Assets:        &trackers.DescriptionAssets{Description: "Example description", Final: true},
				})
				if failure != nil {
					t.Fatal(failure)
				}
				if value := plan.DryRun().Payload["mal"]; value != "0" {
					t.Fatalf("Unit3D payload MAL ID = %q, want zero", value)
				}
			}
			fixture.anilistSearchDown = false
			input.ExternalFreshness = api.ExternalFreshnessReuse
			// The correction is explicitly reapplied because the recovered MAL changes its content binding.
			recovered := prepareCorrectionEvidence(t, module, input, update)
			if season == "S02" {
				assertPreparedAnime(t, recovered.Release, "S02", 445, "AKA Akai Hoshi", "Mina")
			} else {
				assertPreparedAnime(t, recovered.Release, "S01", 444, "AKA Aoi Hoshi", "Shounen")
			}
			if fixture.requests()[""] <= before+1 || recovered.Release.ProviderMetadata.TMDB.AnimeLookupPending || hasPreparationWarning(recovered, outage) {
				t.Fatalf("failed GraphQL search did not retry cleanly: requests=%+v diagnostics=%+v", fixture.requests(), recovered.Diagnostics)
			}
		})
	}
}

func TestPrepareRefreshWithdrawsMALWhenTMDBStopsClassifyingAnime(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		change  api.ExternalIDOverrides
		wantMAL int
		tmdbID  int
	}{
		{name: "automatic", tmdbID: 555001},
		{name: "automatic TMDB replacement", tmdbID: 555002},
		{
			name:    "kept",
			change:  api.ExternalIDOverrides{KeepMAL: new(true), TMDBID: new(555001)},
			wantMAL: 444,
			tmdbID:  555001,
		},
		{
			name:    "manual",
			change:  api.ExternalIDOverrides{MALID: new(446), TMDBID: new(555001)},
			wantMAL: 446,
			tmdbID:  555001,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newCorrectionEvidenceFixture(t)
			fixture.tmdbAnime, fixture.anilistSeasonTitles = true, true
			input := fixture.input
			input.Instructions.Identity = api.ExternalIDOverrides{}
			module, repo := fixture.open(t)
			inherit := api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit}
			initial := prepareCorrectionEvidence(t, module, input, inherit).Release
			assertPreparedAnime(t, initial, "S01", 444, "AKA Aoi Hoshi", "Shounen")
			if !initial.Media.Anime || initial.Identity.Dependencies.MAL.TMDBID != 555001 {
				t.Fatalf("initial MAL did not come from TMDB's anime evidence: %+v", initial.Identity)
			}

			fixture.tmdbAnime = false
			fixture.tmdbSearchID = test.tmdbID
			input.ExternalFreshness = api.ExternalFreshnessRefresh
			update := inherit
			if test.wantMAL != 0 {
				update = api.ReleaseCorrectionUpdate{
					Mode:  api.ReleaseCorrectionUpdatePatch,
					Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{Identity: test.change}},
				}
			}
			result := prepareCorrectionEvidence(t, module, input, update)
			subject, err := module.ResolveUploadSubject(t.Context(), api.UploadSubjectInput{Release: api.ReleaseRef{
				SourcePath: input.SourcePath, Generation: result.Release.Generation,
			}})
			if err != nil || subject.Identity.MALID != test.wantMAL || (subject.ProviderMetadata.AniList != nil) != (test.wantMAL != 0) {
				t.Fatalf("upload subject published mismatched MAL evidence: identity=%+v error=%v", subject.Identity, err)
			}
			if err := repo.Close(); err != nil {
				t.Fatal(err)
			}
			_, repo = fixture.open(t)
			stored, err := repo.LoadPreparedRelease(t.Context(), input.SourcePath)
			if err != nil {
				t.Fatal(err)
			}
			for _, release := range []api.PreparedRelease{result.Release, stored} {
				tmdbMetadata, anilist := release.ProviderMetadata.TMDB, release.ProviderMetadata.AniList
				if tmdbMetadata == nil || tmdbMetadata.TMDBID != test.tmdbID || tmdbMetadata.Anime || tmdbMetadata.MALID != 0 || tmdbMetadata.AnimeSeason != "" ||
					release.Identity.MALID != test.wantMAL || test.wantMAL == 0 && (anilist != nil || release.Media.Anime) ||
					test.wantMAL > 0 && (anilist == nil || anilist.MALID != test.wantMAL) ||
					hasPreparationWarning(result, "AniList title search failed") {
					t.Fatalf("refresh published mismatched anime evidence: identity=%+v TMDB=%+v AniList=%+v diagnostics=%+v",
						release.Identity, tmdbMetadata, anilist, result.Diagnostics)
				}
			}
		})
	}
}

func TestPrepareAnimeReuseRejectsOutageFallbackAfterSourceReplacement(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		sameStat bool
		force    bool
		tmdbID   int
		tmdbDown bool
	}{
		{name: "changed inventory", tmdbID: 555001},
		{
			name:     "same size and mtime",
			sameStat: true,
			tmdbID:   555001,
		},
		{
			name:     "same size and mtime with forced collection",
			sameStat: true,
			force:    true,
			tmdbID:   555001,
		},
		{
			name:     "same size and mtime with new TMDB work",
			sameStat: true,
			tmdbID:   555002,
		},
		{name: "changed inventory with TMDB unavailable", tmdbDown: true},
		{
			name:     "same size and mtime with TMDB unavailable",
			sameStat: true,
			tmdbDown: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertAnimeOutageFallbackRejectsReplacement(t, test.sameStat, test.force, test.tmdbID, test.tmdbDown)
		})
	}
}

func assertAnimeOutageFallbackRejectsReplacement(t *testing.T, sameStat, force bool, tmdbID int, tmdbDown bool) {
	t.Helper()
	fixture := newCorrectionEvidenceFixture(t)
	fixture.tmdbAnime, fixture.anilistSeasonTitles = true, true
	input := fixture.input
	input.Instructions.Identity = api.ExternalIDOverrides{}
	if sameStat {
		verified, err := preparedrelease.VerifyInputSource(t.Context(), input)
		if err != nil {
			t.Fatal(err)
		}
		input.VerifiedSource = &verified
	}
	module, repo := fixture.open(t)
	inherit := api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit}
	initial := prepareCorrectionEvidence(t, module, input, inherit).Release
	assertPreparedAnime(t, initial, "S01", 444, "AKA Aoi Hoshi", "Shounen")
	original, err := os.Stat(input.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("replacement synthetic media")
	if sameStat {
		data = []byte("synthetic MEDIA")
	}
	if err := os.WriteFile(input.SourcePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.WriteFile(input.SourcePath, []byte("synthetic media"), 0o600); err != nil {
			t.Error(err)
		}
	})
	if sameStat {
		if err := os.Chtimes(input.SourcePath, original.ModTime(), original.ModTime()); err != nil {
			t.Fatal(err)
		}
		replacement, err := preparedrelease.VerifyInputSource(t.Context(), input)
		if err != nil {
			t.Fatal(err)
		}
		if replacement.Identity.Digest == input.VerifiedSource.Identity.Digest {
			t.Fatal("replacement did not change the verified content identity")
		}
		input.VerifiedSource = &replacement
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	fixture.anilistSearchDown = tmdbID == 555001
	fixture.tmdbSearchID = tmdbID
	fixture.tmdbSeriesDown = tmdbDown
	if tmdbDown {
		fixture.imdb = &stubIMDB{
			searchResult: imdb.SearchResult{IMDbID: 555002, AutoSelected: true},
			info: imdb.Info{
				IMDbID: "tt0555002",
				Title:  "Example IMDb Selection",
				Type:   "tvSeries",
				Year:   2026,
			},
		}
	}
	sonarr := &stubArrClient{}
	module, repo = fixture.open(t,
		WithConfig(config.Config{
			MainSettings:   config.MainSettingsConfig{DBPath: fixture.dbPath},
			ArrIntegration: config.ArrIntegrationConfig{UseSonarr: true},
		}),
		WithSonarrClient(sonarr),
	)
	input.ExternalFreshness = api.ExternalFreshnessReuse
	if tmdbDown {
		input.ExternalFreshness = api.ExternalFreshnessRefresh
	}
	input.Force = force
	result := prepareCorrectionEvidence(t, module, input, inherit)
	if sonarr.calls != 1 || sonarr.last.Identity.TMDBID != 0 || sonarr.last.Identity.MALID != 0 {
		t.Fatalf("Arr received pre-replacement provider evidence: calls=%d TMDB=%d MAL=%d",
			sonarr.calls, sonarr.last.Identity.TMDBID, sonarr.last.Identity.MALID)
	}
	stored, err := repo.LoadPreparedRelease(t.Context(), input.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, release := range []api.PreparedRelease{result.Release, stored} {
		tmdb := release.ProviderMetadata.TMDB
		if (release.Compatibility.SourceFingerprint == initial.Compatibility.SourceFingerprint) != sameStat || release.Episode.SeasonLabel != "S01" ||
			release.Identity.Resolution.SourceFingerprint == initial.Identity.Resolution.SourceFingerprint ||
			release.Identity.TMDBID != tmdbID || release.Identity.MALID != 0 || release.ProviderMetadata.AniList != nil {
			t.Fatalf("replacement reused old identity evidence: identity=%+v TMDB=%+v diagnostics=%+v", release.Identity, tmdb, result.Diagnostics)
		}
		switch {
		case tmdbDown:
			imdb := release.ProviderMetadata.IMDB
			if tmdb != nil || release.Identity.IMDBID != 555002 || imdb == nil || imdb.Title != "Example IMDb Selection" ||
				!hasPreparationWarning(result, "TMDB refresh failed") || hasPreparationWarning(result, "kept the previous anime metadata") {
				t.Fatalf("replacement outage published the wrong evidence: identity=%+v IMDb=%+v TMDB=%+v diagnostics=%+v",
					release.Identity, imdb, tmdb, result.Diagnostics)
			}
		case tmdbID == 555002:
			if tmdb == nil || tmdb.Title != "Alternate Series" || release.Naming.Title != "Alternate Series" {
				t.Fatalf("replacement did not publish the new work: naming=%+v TMDB=%+v", release.Naming, tmdb)
			}
		default:
			if tmdb == nil || !tmdb.AnimeLookupPending || tmdb.MALID != 0 || tmdb.RetrievedAKA != "" || tmdb.Demographic != "" ||
				!hasPreparationWarning(result, "automatic anime metadata is unavailable") {
				t.Fatalf("replacement reused old anime evidence: identity=%+v TMDB=%+v diagnostics=%+v", release.Identity, tmdb, result.Diagnostics)
			}
		}
	}
}

func TestPrepareAnimeMovieRetriesInitialAniListOutage(t *testing.T) {
	t.Parallel()
	for _, freshness := range []api.ExternalFreshness{api.ExternalFreshnessReuse, api.ExternalFreshnessLoad} {
		t.Run(string(freshness), func(t *testing.T) {
			t.Parallel()
			fixture := newCorrectionEvidenceFixtureForSource(t, "Example.Movie.2026.1080p.WEB-DL.H264-GRP.mkv")
			fixture.tmdbAnime, fixture.anilistSeasonTitles, fixture.anilistSearchDown = true, true, true
			input := fixture.input
			input.Instructions.Category = new(api.CanonicalCategoryMovie)
			input.Instructions.Identity = api.ExternalIDOverrides{KeepTMDB: new(true)}
			module, repo := fixture.open(t)
			inherit := api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit}
			initial := prepareCorrectionEvidence(t, module, input, inherit)
			const outage = "AniList title search failed"
			if tmdbMeta := initial.Release.ProviderMetadata.TMDB; tmdbMeta == nil || tmdbMeta.AnimeSeason != "" ||
				initial.Release.Identity.MALID != 0 || initial.Release.ProviderMetadata.AniList != nil || !hasPreparationWarning(initial, outage) {
				t.Fatalf("initial movie outage did not leave ranking pending: %+v / %+v", tmdbMeta, initial.Diagnostics)
			}
			calls := fixture.requests()
			fixture.anilistSearchDown = false
			if freshness == api.ExternalFreshnessLoad {
				if err := repo.Close(); err != nil {
					t.Fatal(err)
				}
				module, _ = fixture.open(t)
			}
			input.ExternalFreshness = freshness
			recovered := prepareCorrectionEvidence(t, module, input, inherit)
			assertPreparedAnime(t, recovered.Release, "", 444, "AKA Aoi Hoshi", "Shounen")
			if hasPreparationWarning(recovered, outage) {
				t.Fatalf("recovered movie retained an outage warning: %+v", recovered.Diagnostics)
			}
			if got := fixture.requests(); got["/3/movie/555001"] != calls["/3/movie/555001"] || got[""] != calls[""]+2 {
				t.Fatalf("expected one search and one detail lookup with no TMDB refetch: before=%+v after=%+v", calls, got)
			}
		})
	}
}

func TestPrepareTurningKeepOffWithdrawsDependentTMDBAfterSourceReplacement(t *testing.T) {
	t.Parallel()
	for _, freshness := range []api.ExternalFreshness{api.ExternalFreshnessLoad, api.ExternalFreshnessReuse, api.ExternalFreshnessRefresh} {
		t.Run(cmp.Or(string(freshness), "reuse"), func(t *testing.T) {
			t.Parallel()
			fixture := newCorrectionEvidenceFixture(t)
			fixture.noTMDBResults, fixture.tmdbFindIMDb = true, true
			imdbClient := &stubIMDB{
				searchResult: imdb.SearchResult{IMDbID: 555002, AutoSelected: true},
				info: imdb.Info{
					IMDbID: "tt0555002",
					Title:  "Example Original Series",
					Type:   "tvSeries",
				},
			}
			fixture.imdb = imdbClient
			input := fixture.input
			input.Instructions.Identity = api.ExternalIDOverrides{}
			verified, err := preparedrelease.VerifyInputSource(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			input.VerifiedSource = &verified
			module, repo := fixture.open(t)
			initial := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit}).Release
			dependency := api.IdentityDependency{ID: 555001, IMDBID: 555002}
			if initial.Identity.TMDBID != dependency.ID || initial.Identity.IMDBID != dependency.IMDBID ||
				initial.Identity.Dependencies.TMDB != dependency || initial.ProviderMetadata.TMDB == nil {
				t.Fatalf("setup did not use the original IMDb-derived TMDB: %+v", initial.Identity)
			}

			if err := os.WriteFile(input.SourcePath, []byte("synthetic MEDIA"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.WriteFile(input.SourcePath, []byte("synthetic media"), 0o600); err != nil {
					t.Error(err)
				}
			})
			replacement, err := preparedrelease.VerifyInputSource(t.Context(), input)
			if err != nil || replacement.Identity.Digest == verified.Identity.Digest {
				t.Fatalf("source replacement was not verified: %v", err)
			}
			input.VerifiedSource = &replacement
			input.ExternalFreshness = api.ExternalFreshnessReuse
			imdbClient.searchResult = imdb.SearchResult{IMDbID: 555099, AutoSelected: true}
			imdbClient.info = imdb.Info{
				IMDbID: "tt0555099",
				Title:  "Example Replacement Series",
				Type:   "tvSeries",
			}
			keep := func(enabled bool) api.ReleaseCorrectionUpdate {
				return api.ReleaseCorrectionUpdate{
					Mode: api.ReleaseCorrectionUpdatePatch,
					Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{Identity: api.ExternalIDOverrides{
						KeepTMDB: new(enabled),
					}}},
				}
			}
			kept := prepareCorrectionEvidence(t, module, input, keep(true)).Release
			if kept.Identity.TMDBID != dependency.ID || kept.Identity.Dependencies.TMDB != dependency ||
				kept.Identity.Provenance.TMDB != api.IdentityProvenanceRetained || kept.Identity.IMDBID != 0 {
				t.Fatalf("replacement did not retain the kept TMDB and its lineage: %+v", kept.Identity)
			}
			input.ExternalFreshness = freshness
			final := prepareCorrectionEvidence(t, module, input, keep(false)).Release
			stored, err := repo.LoadPreparedRelease(t.Context(), input.SourcePath)
			if err != nil {
				t.Fatal(err)
			}
			if final.Generation != stored.Generation || final.Generation <= kept.Generation {
				t.Fatalf("Keep off did not publish a new generation: returned=%d stored=%d previous=%d", final.Generation, stored.Generation, kept.Generation)
			}
			for _, release := range []api.PreparedRelease{final, stored} {
				identity := release.Identity
				metadata := release.ProviderMetadata
				if identity.TMDBID != 0 || identity.Dependencies.TMDB != (api.IdentityDependency{}) || metadata.TMDB != nil ||
					identity.IMDBID != 555099 || metadata.IMDB == nil || metadata.IMDB.IMDBID != 555099 {
					t.Fatalf("unexpected identity after Keep off: %+v TMDB=%+v IMDb=%+v", identity, metadata.TMDB, metadata.IMDB)
				}
			}
		})
	}
}

func TestPrepareAnimeMALResetRestoresAutomaticEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		movie    bool
		initial  bool
		keepTMDB bool
		malID    int
	}{
		{name: "TV cleared after preparation"},
		{name: "TV overridden after preparation", malID: 999},
		{
			name:     "TV initially cleared with Keep TMDB",
			initial:  true,
			keepTMDB: true,
		},
		{
			name:    "movie initially cleared",
			movie:   true,
			initial: true,
		},
		{
			name:     "movie initially cleared with Keep TMDB",
			movie:    true,
			initial:  true,
			keepTMDB: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			filename, season, endpoint := "Example.Series.S01E01.1080p.WEB-DL.H264-GRP.mkv", "S01", "/3/tv/555001"
			if test.movie {
				filename, season, endpoint = "Example.Movie.2026.1080p.WEB-DL.H264-GRP.mkv", "", "/3/movie/555001"
			}
			fixture := newCorrectionEvidenceFixtureForSource(t, filename)
			fixture.tmdbAnime = true
			fixture.anilistSeasonTitles = true
			input := fixture.input
			input.Instructions.Identity = api.ExternalIDOverrides{}
			if test.movie {
				input.Instructions.Category = new(api.CanonicalCategoryMovie)
			}
			if test.malID > 0 {
				input.Instructions.Identity.TMDBID = new(555001)
			}
			if test.initial {
				input.Instructions.Identity.MALID = new(test.malID)
			}
			module, _ := fixture.open(t)
			result := prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
			input = api.PrepareInput{SourcePath: input.SourcePath, ExternalFreshness: api.ExternalFreshnessReuse}
			if !test.initial {
				assertPreparedAnime(t, result.Release, season, 444, "AKA Aoi Hoshi", "Shounen")
			}
			if !test.initial || test.keepTMDB {
				result = prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{
					Mode: api.ReleaseCorrectionUpdatePatch,
					Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{Identity: api.ExternalIDOverrides{
						MALID: new(test.malID), KeepTMDB: new(test.keepTMDB),
					}}},
				})
			}
			if result.Release.Identity.MALID != test.malID || result.Release.ProviderMetadata.TMDB == nil ||
				result.Release.ProviderMetadata.TMDB.RetrievedAKA != "" {
				t.Fatalf("MAL correction was lost before the reset: %+v / %+v", result.Release.Identity, result.Release.ProviderMetadata.TMDB)
			}
			before := fixture.requests()[endpoint]
			result = prepareCorrectionEvidence(t, module, input, api.ReleaseCorrectionUpdate{
				Mode:  api.ReleaseCorrectionUpdatePatch,
				Patch: &api.ReleaseCorrectionPatch{ResetFields: []api.CorrectionFieldRef{{Field: api.CorrectionFieldIdentityMAL}}},
			})
			assertPreparedAnime(t, result.Release, season, 444, "AKA Aoi Hoshi", "Shounen")
			if test.keepTMDB && fixture.requests()[endpoint] != before {
				t.Fatal("MAL Auto fetched the kept TMDB record")
			}
		})
	}
}
