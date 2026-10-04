// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

type metadataEvidenceTransport func(*http.Request) (*http.Response, error)

func (f metadataEvidenceTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type coreEvidenceFixture struct {
	core     *Core
	repo     *db.SQLiteRepository
	input    api.PrepareInput
	initial  api.PrepareResult
	requests func() map[string]int
}

// Both production repository ownership paths must retain provider evidence.
func newCoreEvidenceFixture(t *testing.T, borrowed bool) coreEvidenceFixture {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "Example.Series.S01E04.1080p.WEB-DL.H264-GRP.mkv")
	if err := os.WriteFile(source, []byte("synthetic video"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.MainSettings.DBPath = filepath.Join(root, "metadata.sqlite")
	cfg.MainSettings.SceneDetection = true
	cfg.Metadata.SkipAutoTorrent = true
	cfg.Trackers.Trackers["AITHER"] = config.TrackerConfig{APIKey: "synthetic-key"}
	for name, client := range cfg.TorrentClients {
		client.URL, client.Username, client.Password = "http://127.0.0.1:1", "test", "test"
		cfg.TorrentClients[name] = client
	}
	tmpRoot, err := db.Subdir(cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		t.Fatal(err)
	}
	temp, _, err := paths.ReleaseTempDirFor(tmpRoot, source, metadata.ParseReleaseInfo(source))
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"mediainfo.txt": "General\nFormat : Matroska\n", "MediaInfo.json": `{"media":{"track":[{"@type":"General","Format":"Matroska"},{"@type":"Video","Format":"AVC","Width":"1920","Height":"1080"},{"@type":"Audio","ID":"2","Format":"AC-3","Channels":"6","Language":"en"},{"@type":"Text","ID":"3","Format":"UTF-8","Language":"en"}]}}`} {
		if err := os.WriteFile(filepath.Join(temp, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	calls := make(map[string]int)
	originalTransport := http.DefaultTransport
	http.DefaultTransport = metadataEvidenceTransport(func(request *http.Request) (*http.Response, error) {
		key := request.URL.Host + request.URL.RequestURI()
		mu.Lock()
		calls[key]++
		mu.Unlock()
		var body string
		status := http.StatusOK
		switch {
		case request.URL.Host == "aither.cc" && request.URL.Path == "/api/torrents/42":
			body = `{"data":[{"id":42,"attributes":{"category":"TV","description":"Release notes [img]https://93.184.216.34/failed.png[/img]"}}]}`
		case request.URL.Host == "93.184.216.34" && request.URL.Path == "/failed.png":
			status, body = http.StatusNotFound, "missing"
		case request.URL.Host == "api.srrdb.com":
			body = `{"resultsCount":0,"results":[]}`
		case request.URL.Host == "api.tvmaze.com" && request.URL.Path == "/shows/555001":
			body = `{"id":555001,"name":"Example Series","premiered":"2026-01-01","language":"English","type":"Scripted","externals":{}}`
		case request.URL.Host == "api.tvmaze.com" && request.URL.Path == "/shows/555001/images":
			body = `[]`
		case request.URL.Host == "api.tvmaze.com" && request.URL.Path == "/shows/555001/episodebynumber":
			body = `{"id":555104,"name":"The Fourth Signal","season":1,"number":4,"airdate":"2026-01-04"}`
		default:
			t.Errorf("unexpected metadata HTTP request: %s", key)
			body = `{}`
		}
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    request,
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	deps := api.CoreDependencies{Config: cfg, SkipCookieMigration: true}
	if borrowed {
		repo, err := db.OpenContext(t.Context(), cfg.MainSettings.DBPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = repo.Close() })
		if err := repo.MigrateContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		deps.Repository, deps.RepositoryOwner = repo.RepositoryCapabilities(), repo
	}
	var c *Core
	if borrowed {
		c, err = NewWithContextAndCoordinator(t.Context(), deps, nil)
	} else {
		c, err = NewWithContext(t.Context(), deps)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.ShutdownWorkflowCoordinator(context.Background()); _ = c.Close() })
	input := api.PrepareInput{
		SourcePath:        source,
		ExternalFreshness: api.ExternalFreshnessLoad,
		Policy:            api.PreparationPolicy{KeepImages: true},
		Instructions: api.ReleaseFactInstructions{
			Category:     new(api.CanonicalCategoryTV),
			SourceLookup: "https://aither.cc/torrents/42",
			Identity: api.ExternalIDOverrides{
				TMDBID:   new(0),
				IMDBID:   new(0),
				TVDBID:   new(0),
				TVmazeID: new(555001),
				MALID:    new(0),
			},
		},
	}
	initial, err := c.preparedFacts.Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	firstCalls := maps.Clone(calls)
	mu.Unlock()
	if firstCalls["api.srrdb.com/v1/search/word:Example%20Series%20S01E04"] != 1 || firstCalls["api.tvmaze.com/shows/555001/episodebynumber?number=4&season=1"] != 1 || firstCalls["aither.cc/api/torrents/42"] != 1 || firstCalls["93.184.216.34/failed.png"] != 1 {
		t.Fatalf("initial provider/tracker/image lookup missing: %v", firstCalls)
	}
	// Expire the tracker cooldown so only retained evidence can suppress the failed image request.
	repo, ok := c.repoOwner.(*db.SQLiteRepository)
	if !ok {
		t.Fatal("default Core did not retain its SQLite owner")
	}
	for _, key := range []string{"AITHER", "AITHER:assets"} {
		if err := repo.SaveTrackerTimestamp(t.Context(), api.TrackerTimestamp{Tracker: key, UpdatedAt: time.Now().Add(-time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	input.ExternalFreshness = api.ExternalFreshnessReuse
	input.Instructions = api.ReleaseFactInstructions{SourceLookup: "https://aither.cc/torrents/42"}
	return coreEvidenceFixture{
		core:     c,
		repo:     repo,
		input:    input,
		initial:  initial,
		requests: func() map[string]int { mu.Lock(); defer mu.Unlock(); return maps.Clone(calls) },
	}
}

func (f coreEvidenceFixture) prepare(t *testing.T, patch api.ReleaseCorrectionPatch) api.PrepareResult {
	t.Helper()
	resolved, err := f.core.preparedFacts.ResolveInput(t.Context(), f.input, api.ReleaseCorrectionUpdate{
		Mode: api.ReleaseCorrectionUpdatePatch, Patch: &patch,
	})
	if err != nil {
		t.Fatalf("resolve correction: %v", err)
	}
	result, err := f.core.preparedFacts.PrepareResolved(t.Context(), resolved)
	if err != nil {
		t.Fatalf("prepare correction: %v", err)
	}
	return result
}

func TestDefaultCoreMetadataEvidenceSurvivesServiceCorrection(t *testing.T) {
	for _, borrowed := range []bool{false, true} {
		name := "CLI-owned repository"
		if borrowed {
			name = "WebUI-borrowed repository"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newCoreEvidenceFixture(t, borrowed)
			before := fixture.requests()
			refreshed := fixture.prepare(t, api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{ReleaseName: api.ReleaseNameOverrides{Service: new("NF")}}})
			if refreshed.Release.Generation <= fixture.initial.Release.Generation || !strings.Contains(refreshed.Release.Naming.ReleaseName, "NF") {
				t.Fatalf("service correction missing from new generation: %s", refreshed.Release.Naming.ReleaseName)
			}
			if after := fixture.requests(); !reflect.DeepEqual(before, after) {
				t.Fatalf("service correction repeated metadata HTTP: before=%v after=%v", before, after)
			}
			ref := api.ReleaseRef{SourcePath: fixture.input.SourcePath, Generation: refreshed.Release.Generation}
			upload, err := fixture.core.preparedFacts.ResolveUploadSubject(t.Context(), api.UploadSubjectInput{Release: ref})
			if err != nil {
				t.Fatal(err)
			}
			duplicate, err := fixture.core.preparedFacts.ResolveDuplicateSubject(t.Context(), api.DuplicateCheckInput{Release: ref})
			if err != nil {
				t.Fatal(err)
			}
			if upload.Service != "NF" || upload.ReleaseName != refreshed.Release.Naming.ReleaseName || duplicate.ReleaseName != upload.ReleaseName {
				t.Fatalf("downstream subjects lost corrected generation name: upload=%q duplicate=%q", upload.ReleaseName, duplicate.ReleaseName)
			}
			fingerprint, err := api.CanonicalWorkflowFingerprint(ref)
			if err != nil {
				t.Fatal(err)
			}
			projection, failure := trackerimpl.MustNewRegistry().ProjectRelease(t.Context(), trackers.PreparationInput{Tracker: "AITHER", Meta: upload}, fingerprint, fingerprint, fingerprint)
			if failure != nil {
				t.Fatal(failure)
			}
			if !strings.Contains(projection.UploadReleaseName, "NF") || !strings.Contains(projection.DuplicateCriteria.Name, "NF") {
				t.Fatalf("tracker projection lost corrected service: upload=%q search=%q", projection.UploadReleaseName, projection.DuplicateCriteria.Name)
			}
			if after := fixture.requests(); !reflect.DeepEqual(before, after) {
				t.Fatalf("downstream projection made HTTP calls: %v", after)
			}
		})
	}
}

type coreCorrectionCase struct {
	field  api.CorrectionField
	before api.ReleaseCorrectionValues
	run    func(*testing.T, coreEvidenceFixture)
}

// Explicit empty values and false remain saved intent until a field-specific Auto reset.
func coreCorrectionLifecycle[T any](field api.CorrectionField, values, expected []T,
	target func(*api.ReleaseCorrectionValues) **T, fact func(api.PreparedRelease) T,
) coreCorrectionCase {
	return coreCorrectionCase{field: field, run: func(t *testing.T, fixture coreEvidenceFixture) {
		t.Helper()
		initial := fixture.initial.Release
		original, err := json.Marshal(initial)
		if err != nil {
			t.Fatal(err)
		}
		calls := fixture.requests()
		previous := initial.Generation
		previousRelease := initial
		for index, value := range values {
			t.Run([]string{"set", "change", "clear"}[index], func(t *testing.T) {
				correction := api.ReleaseCorrectionValues{}
				*target(&correction) = new(value)
				result := fixture.prepare(t, api.ReleaseCorrectionPatch{Values: correction})
				if got := fact(result.Release); !reflect.DeepEqual(got, expected[index]) {
					t.Errorf("canonical fact = %#v, want %#v (name %q)", got, expected[index], result.Release.Naming.ReleaseName)
				}
				if field == api.CorrectionFieldReleaseNameNoSeason && !reflect.DeepEqual(result.Release.Episode, initial.Episode) {
					t.Error("NoSeason changed canonical episode facts")
				}
				if field == api.CorrectionFieldReleaseNameNoYear && result.Release.Naming.Year != initial.Naming.Year {
					t.Error("NoYear changed canonical year")
				}
				if field == api.CorrectionFieldReleaseNameNoAKA && result.Release.Naming.AlternateTitle != initial.Naming.AlternateTitle {
					t.Error("NoAKA changed canonical alternate title")
				}
				saved := api.ReleaseCorrectionValues{
					Identity:    result.Corrections.Corrections.Identity,
					ReleaseName: result.Corrections.Corrections.ReleaseName,
					Metadata:    result.Corrections.Corrections.Metadata,
				}
				if got := *target(&saved); got == nil || !reflect.DeepEqual(*got, value) {
					t.Errorf("saved intent = %v, want %#v", got, value)
				}
				assertCoreCorrectionGeneration(t, fixture, result, previous, calls)
				assertCoreCorrectionName(t, correction, initial, previousRelease, result.Release)
				previousRelease = result.Release
				previous = result.Release.Generation
			})
		}
		t.Run("Auto", func(t *testing.T) {
			result := fixture.prepare(t, api.ReleaseCorrectionPatch{ResetFields: []api.CorrectionFieldRef{{Field: field}}})
			saved := api.ReleaseCorrectionValues{
				Identity:    result.Corrections.Corrections.Identity,
				ReleaseName: result.Corrections.Corrections.ReleaseName,
				Metadata:    result.Corrections.Corrections.Metadata,
			}
			if *target(&saved) != nil {
				t.Error("Auto retained explicit correction")
			}
			if !reflect.DeepEqual(result.Release.Naming, initial.Naming) || !reflect.DeepEqual(result.Release.Episode, initial.Episode) || !reflect.DeepEqual(result.Release.Media, initial.Media) {
				t.Errorf("Auto did not restore automatic naming, episode and media: name %q, want %q", result.Release.Naming.ReleaseName, initial.Naming.ReleaseName)
			}
			assertCoreCorrectionGeneration(t, fixture, result, previous, calls)
		})
		unchanged, err := json.Marshal(initial)
		if err != nil || string(unchanged) != string(original) {
			t.Fatalf("later preparation mutated original generation: %v", err)
		}
	}}
}

func assertCoreCorrectionGeneration(t *testing.T, fixture coreEvidenceFixture, result api.PrepareResult, previous api.PreparedGeneration, calls map[string]int) {
	t.Helper()
	if result.Release.Generation <= previous {
		t.Errorf("generation = %d, must be newer than %d", result.Release.Generation, previous)
	}
	if result.Release.Naming.GeneratedName == nil || result.Release.Naming.ReleaseName == "" {
		t.Fatal("correction lost generated name")
	}
	rendered := result.Release.Naming.GeneratedName.Render()
	if rendered.Name != result.Release.Naming.ReleaseName || rendered.NameNoTag != result.Release.Naming.NameWithoutTag || rendered.CleanName != result.Release.Naming.CleanName {
		t.Error("generated name document and rendered projections disagree")
	}
	persisted, err := fixture.repo.LoadPreparedRelease(t.Context(), fixture.input.SourcePath)
	if err != nil || !reflect.DeepEqual(persisted, result.Release) {
		t.Errorf("prepared generation not persisted: %v", err)
	}
	if got := fixture.requests(); !reflect.DeepEqual(got, calls) {
		t.Errorf("local edit repeated HTTP requests: before=%v after=%v", calls, got)
	}
}

// Check visible tokens against correction intent as well as the structured document.
func assertCoreCorrectionName(t *testing.T, correction api.ReleaseCorrectionValues, initial, before, after api.PreparedRelease) {
	t.Helper()
	name := after.Naming.ReleaseName
	for _, token := range []struct {
		value    *string
		previous string
	}{
		{correction.ReleaseName.Service, before.Media.Service},
		{correction.ReleaseName.Resolution, before.Naming.Resolution},
		{correction.ReleaseName.Tag, before.Naming.Group},
		{correction.ReleaseName.Edition, before.Media.Edition},
		{correction.ReleaseName.EpisodeTitle, before.Episode.Title},
		{correction.Metadata.AlternateTitle, before.Naming.AlternateTitle},
	} {
		if token.value == nil {
			continue
		}
		if *token.value != "" && !strings.Contains(name, *token.value) {
			t.Errorf("generated name %q lacks corrected token %q", name, *token.value)
		}
		if token.previous != "" && token.previous != *token.value && strings.Contains(name, token.previous) {
			t.Errorf("generated name %q retains old token %q", name, token.previous)
		}
	}
	for _, omission := range []struct {
		value     *bool
		component string
	}{
		{correction.ReleaseName.NoSeason, initial.Episode.SeasonLabel + initial.Episode.EpisodeLabel},
		{correction.ReleaseName.NoAKA, initial.Naming.AlternateTitle},
		{correction.ReleaseName.NoTag, initial.Naming.Tag},
		{correction.ReleaseName.NoEpisodeTitle, initial.Episode.Title},
		{correction.ReleaseName.NoEdition, initial.Media.Edition},
		{correction.ReleaseName.NoDub, "Dubbed"},
		{correction.ReleaseName.NoDual, "Dual-Audio"},
	} {
		if omission.value != nil && omission.component != "" && strings.Contains(name, omission.component) == *omission.value {
			t.Errorf("generated name %q: component %q omission=%t", name, omission.component, *omission.value)
		}
	}
	if forced := correction.ReleaseName.DualAudio; forced != nil && strings.Contains(name, "Dual-Audio") != *forced {
		t.Errorf("generated name %q: force dual audio=%t", name, *forced)
	}
}

func TestDefaultCoreLocalCorrectionMatrix(t *testing.T) {
	cases := []coreCorrectionCase{
		coreCorrectionLifecycle(api.CorrectionFieldReleaseNameType, []string{"ENCODE", "WEBRIP", ""}, []string{"ENCODE", "WEBRIP", "WEBDL"},
			func(v *api.ReleaseCorrectionValues) **string { return &v.ReleaseName.Type },
			func(r api.PreparedRelease) string { return r.Media.Type }),
		coreCorrectionLifecycle(api.CorrectionFieldReleaseNameSource, []string{"HDTV", "BluRay", ""}, []string{"HDTV", "BluRay", "Web"},
			func(v *api.ReleaseCorrectionValues) **string { return &v.ReleaseName.Source },
			func(r api.PreparedRelease) string { return r.Media.Source }),
		coreCorrectionLifecycle(api.CorrectionFieldReleaseNameResolution, []string{"720p", "2160p", ""}, []string{"720p", "2160p", ""},
			func(v *api.ReleaseCorrectionValues) **string { return &v.ReleaseName.Resolution },
			func(r api.PreparedRelease) string { return r.Naming.Resolution }),
		coreCorrectionLifecycle(api.CorrectionFieldReleaseNameTag, []string{"ALT", "EDIT", ""}, []string{"ALT", "EDIT", ""},
			func(v *api.ReleaseCorrectionValues) **string { return &v.ReleaseName.Tag },
			func(r api.PreparedRelease) string { return r.Naming.Group }),
		coreCorrectionLifecycle(api.CorrectionFieldReleaseNameService, []string{"NF", "AMZN", ""}, []string{"NF", "AMZN", ""},
			func(v *api.ReleaseCorrectionValues) **string { return &v.ReleaseName.Service },
			func(r api.PreparedRelease) string { return r.Media.Service }),
		coreCorrectionLifecycle(api.CorrectionFieldReleaseNameEdition, []string{"Extended", "Uncut", ""}, []string{"Extended", "Uncut", ""},
			func(v *api.ReleaseCorrectionValues) **string { return &v.ReleaseName.Edition },
			func(r api.PreparedRelease) string { return r.Media.Edition }),
		coreCorrectionLifecycle(api.CorrectionFieldReleaseNameEpisodeTitle, []string{"A New Signal", "A Later Signal", ""}, []string{"A New Signal", "A Later Signal", ""},
			func(v *api.ReleaseCorrectionValues) **string { return &v.ReleaseName.EpisodeTitle },
			func(r api.PreparedRelease) string { return r.Episode.Title }),
		coreCorrectionLifecycle(api.CorrectionFieldReleaseNameRegion, []string{"A", "B", ""}, []string{"A", "B", ""},
			func(v *api.ReleaseCorrectionValues) **string { return &v.ReleaseName.Region },
			func(r api.PreparedRelease) string { return r.Media.Region }),
		coreCorrectionLifecycle(api.CorrectionFieldMetadataDistributor, []string{"Criterion", "StudioCanal", ""}, []string{"Criterion", "StudioCanal", ""},
			func(v *api.ReleaseCorrectionValues) **string { return &v.Metadata.Distributor },
			func(r api.PreparedRelease) string { return r.Media.Distributor }),
		coreCorrectionLifecycle(api.CorrectionFieldMetadataOriginalLanguage, []string{"French", "German", ""}, []string{"French", "German", ""},
			func(v *api.ReleaseCorrectionValues) **string { return &v.Metadata.OriginalLanguage },
			func(r api.PreparedRelease) string { return r.Media.OriginalLanguage }),
		coreCorrectionLifecycle(api.CorrectionFieldMetadataAlternateTitle, []string{"Other Signal", "Further Signal", ""}, []string{"Other Signal", "Further Signal", ""},
			func(v *api.ReleaseCorrectionValues) **string { return &v.Metadata.AlternateTitle },
			func(r api.PreparedRelease) string { return r.Naming.AlternateTitle }),
		coreCorrectionLifecycle(api.CorrectionFieldMetadataGenres, [][]string{{"Drama", "Mystery"}, {"Comedy"}, {}}, [][]string{{"Drama", "Mystery"}, {"Comedy"}, nil},
			func(v *api.ReleaseCorrectionValues) **[]string { return &v.Metadata.Genres },
			func(r api.PreparedRelease) []string { return r.Naming.Genres }),
		coreCorrectionLifecycle(api.CorrectionFieldMetadataAudioLanguages, [][]string{{"French", "German"}, {"Spanish"}, {}}, [][]string{{"French", "German"}, {"Spanish"}, nil},
			func(v *api.ReleaseCorrectionValues) **[]string { return &v.Metadata.AudioLanguages },
			func(r api.PreparedRelease) []string { return r.Media.AudioLanguages }),
		coreCorrectionLifecycle(api.CorrectionFieldMetadataSubtitleLanguages, [][]string{{"French", "German"}, {"Spanish"}, {}}, [][]string{{"French", "German"}, {"Spanish"}, nil},
			func(v *api.ReleaseCorrectionValues) **[]string { return &v.Metadata.SubtitleLanguages },
			func(r api.PreparedRelease) []string { return r.Media.SubtitleLanguages }),
		coreCorrectionLifecycle(api.CorrectionFieldMetadataPersonalRelease, []bool{true, false}, []bool{true, false},
			func(v *api.ReleaseCorrectionValues) **bool { return &v.Metadata.PersonalRelease },
			func(r api.PreparedRelease) bool { return r.Naming.Personal }),
		coreCorrectionLifecycle(api.CorrectionFieldMetadataCommentary, []bool{true, false}, []bool{true, false},
			func(v *api.ReleaseCorrectionValues) **bool { return &v.Metadata.Commentary },
			func(r api.PreparedRelease) bool { return r.Media.Commentary }),
		coreCorrectionLifecycle(api.CorrectionFieldMetadataWebDV, []bool{true, false}, []bool{true, false},
			func(v *api.ReleaseCorrectionValues) **bool { return &v.Metadata.WebDV },
			func(r api.PreparedRelease) bool { return r.Media.WebDV }),
		coreCorrectionLifecycle(api.CorrectionFieldMetadataStreamOptimized, []bool{true, false}, []bool{true, false},
			func(v *api.ReleaseCorrectionValues) **bool { return &v.Metadata.StreamOptimized },
			func(r api.PreparedRelease) bool { return r.Media.StreamOptimized == 1 }),
		coreCorrectionLifecycle(api.CorrectionFieldMetadataAnime, []bool{true, false}, []bool{true, false},
			func(v *api.ReleaseCorrectionValues) **bool { return &v.Metadata.Anime },
			func(r api.PreparedRelease) bool { return r.Media.Anime }),
		coreCorrectionLifecycle(api.CorrectionFieldMetadataHardcodedSubs, []bool{true, false}, []bool{true, false},
			func(v *api.ReleaseCorrectionValues) **bool { return &v.Metadata.HardcodedSubs },
			func(r api.PreparedRelease) bool { return r.Media.HardcodedSubs }),
		coreCorrectionLifecycle(api.CorrectionFieldReleaseNameNoSeason, []bool{true, false}, []bool{true, false},
			func(v *api.ReleaseCorrectionValues) **bool { return &v.ReleaseName.NoSeason },
			func(r api.PreparedRelease) bool { return r.Naming.NamePresentation.OmitSeasonEpisode }),
		coreCorrectionLifecycle(api.CorrectionFieldReleaseNameNoYear, []bool{true, false}, []bool{true, false},
			func(v *api.ReleaseCorrectionValues) **bool { return &v.ReleaseName.NoYear },
			func(r api.PreparedRelease) bool { return r.Naming.NamePresentation.OmitYear }),
		coreCorrectionLifecycle(api.CorrectionFieldReleaseNameNoAKA, []bool{true, false}, []bool{true, false},
			func(v *api.ReleaseCorrectionValues) **bool { return &v.ReleaseName.NoAKA },
			func(r api.PreparedRelease) bool { return r.Naming.NamePresentation.OmitAlternateTitle }),
		coreCorrectionLifecycle(api.CorrectionFieldReleaseNameNoTag, []bool{true, false}, []bool{true, false},
			func(v *api.ReleaseCorrectionValues) **bool { return &v.ReleaseName.NoTag },
			func(r api.PreparedRelease) bool { return r.Naming.Tag == "" }),
		coreCorrectionLifecycle(api.CorrectionFieldReleaseNameNoEpisodeTitle, []bool{true, false}, []bool{true, false},
			func(v *api.ReleaseCorrectionValues) **bool { return &v.ReleaseName.NoEpisodeTitle },
			func(r api.PreparedRelease) bool { return r.Episode.Title == "" }),
		coreCorrectionLifecycle(api.CorrectionFieldReleaseNameDualAudio, []bool{true, false}, []bool{true, false},
			func(v *api.ReleaseCorrectionValues) **bool { return &v.ReleaseName.DualAudio },
			func(r api.PreparedRelease) bool { return strings.Contains(r.Media.Audio, "Dual-Audio") }),
		func() coreCorrectionCase {
			test := coreCorrectionLifecycle(api.CorrectionFieldReleaseNameNoDistributor, []bool{true, false}, []bool{true, false},
				func(v *api.ReleaseCorrectionValues) **bool { return &v.ReleaseName.NoDistributor },
				func(r api.PreparedRelease) bool { return r.Media.Distributor == "" })
			test.before = api.ReleaseCorrectionValues{Metadata: api.MetadataOverrides{Distributor: new("Criterion")}}
			return test
		}(),
		func() coreCorrectionCase {
			test := coreCorrectionLifecycle(api.CorrectionFieldReleaseNameNoEdition, []bool{true, false}, []bool{true, false},
				func(v *api.ReleaseCorrectionValues) **bool { return &v.ReleaseName.NoEdition },
				func(r api.PreparedRelease) bool { return r.Media.Edition == "" })
			test.before = api.ReleaseCorrectionValues{ReleaseName: api.ReleaseNameOverrides{Edition: new("Extended")}}
			return test
		}(),
		func() coreCorrectionCase {
			test := coreCorrectionLifecycle(api.CorrectionFieldReleaseNameNoDub, []bool{true, false}, []bool{true, false},
				func(v *api.ReleaseCorrectionValues) **bool { return &v.ReleaseName.NoDub },
				func(r api.PreparedRelease) bool { return !strings.Contains(r.Media.Audio, "Dubbed") })
			test.before = api.ReleaseCorrectionValues{Metadata: api.MetadataOverrides{OriginalLanguage: new("French"), AudioLanguages: new([]string{"English"})}}
			return test
		}(),
		func() coreCorrectionCase {
			test := coreCorrectionLifecycle(api.CorrectionFieldReleaseNameNoDual, []bool{true, false}, []bool{true, false},
				func(v *api.ReleaseCorrectionValues) **bool { return &v.ReleaseName.NoDual },
				func(r api.PreparedRelease) bool { return !strings.Contains(r.Media.Audio, "Dual-Audio") })
			test.before = api.ReleaseCorrectionValues{Metadata: api.MetadataOverrides{AudioLanguages: new([]string{"English", "French"})}}
			return test
		}(),
		func() coreCorrectionCase {
			test := coreCorrectionLifecycle(api.CorrectionFieldMetadataHardcodedSubtitleLanguages, [][]string{{"French", "German"}, {"Spanish"}, {}}, [][]string{{"French", "German"}, {"Spanish"}, nil},
				func(v *api.ReleaseCorrectionValues) **[]string { return &v.Metadata.HardcodedSubtitleLanguages },
				func(r api.PreparedRelease) []string { return r.Media.HardcodedSubtitleLanguages })
			test.before = api.ReleaseCorrectionValues{Metadata: api.MetadataOverrides{HardcodedSubs: new(true)}}
			return test
		}(),
	}
	for _, borrowed := range []bool{false, true} {
		ownership := "CLI-owned repository"
		if borrowed {
			ownership = "WebUI-borrowed repository"
		}
		t.Run(ownership, func(t *testing.T) {
			for _, test := range cases {
				t.Run(string(test.field), func(t *testing.T) {
					fixture := newCoreEvidenceFixture(t, borrowed)
					if !reflect.DeepEqual(test.before, api.ReleaseCorrectionValues{}) {
						fixture.initial = fixture.prepare(t, api.ReleaseCorrectionPatch{Values: test.before})
					}
					if test.field == api.CorrectionFieldReleaseNameNoAKA {
						fixture.initial = fixture.prepare(t, api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{Metadata: api.MetadataOverrides{AlternateTitle: new("Other Signal")}}})
					}
					test.run(t, fixture)
				})
			}
		})
	}
}

func TestDefaultCoreTrackLanguageCorrectionMatrix(t *testing.T) {
	for _, borrowed := range []bool{false, true} {
		ownership := "CLI-owned repository"
		if borrowed {
			ownership = "WebUI-borrowed repository"
		}
		t.Run(ownership, func(t *testing.T) {
			fixture := newCoreEvidenceFixture(t, borrowed)
			initial := fixture.initial.Release
			if len(initial.Media.Tracks) != 2 {
				t.Fatalf("fixture needs inspected audio and subtitle tracks: %#v", initial.Media.Tracks)
			}
			original, err := json.Marshal(initial)
			if err != nil {
				t.Fatal(err)
			}
			calls := fixture.requests()
			previous := initial.Generation
			for trackIndex, track := range initial.Media.Tracks {
				t.Run(string(track.Kind), func(t *testing.T) {
					for index, languages := range [][]string{{"French"}, {"German", "Spanish"}, {}} {
						t.Run([]string{"set", "change", "clear"}[index], func(t *testing.T) {
							result := fixture.prepare(t, api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{
								Metadata: api.MetadataOverrides{TrackLanguages: []api.TrackLanguageCorrection{{
									TrackID:             track.ID,
									Languages:           languages,
									ManifestFingerprint: track.ManifestFingerprint,
								}}},
							}})
							corrected := result.Release.Media.Tracks[trackIndex]
							if !slices.Equal(corrected.Languages, languages) || !corrected.LanguageProvenance.IsManual() ||
								!slices.Equal(corrected.DetectedLanguages, track.DetectedLanguages) {
								t.Errorf("incorrect resolved track correction: %#v", corrected)
							}
							if !reflect.DeepEqual(result.Release.Media.Tracks[1-trackIndex], initial.Media.Tracks[1-trackIndex]) {
								t.Error("language correction changed the other inspected track")
							}
							manual := result.Release.Media.ManualLanguages()
							aggregate, projected := result.Release.Media.AudioLanguages, manual.Audio
							if track.Kind == api.MediaTrackSubtitle {
								aggregate, projected = result.Release.Media.SubtitleLanguages, manual.Subtitles
							}
							if !slices.Equal(aggregate, languages) || !slices.Equal(projected, languages) {
								t.Errorf("track correction not reflected in aggregate/description languages: %v / %v", aggregate, projected)
							}
							saved := result.Corrections.Corrections.Metadata.TrackLanguages
							if len(saved) != 1 || saved[0].TrackID != track.ID || saved[0].ManifestFingerprint != track.ManifestFingerprint ||
								!slices.Equal(saved[0].Languages, languages) {
								t.Errorf("track correction not retained: %#v", saved)
							}
							assertCoreCorrectionGeneration(t, fixture, result, previous, calls)
							previous = result.Release.Generation
						})
					}
					t.Run("Auto", func(t *testing.T) {
						result := fixture.prepare(t, api.ReleaseCorrectionPatch{ResetFields: []api.CorrectionFieldRef{{
							Field: api.CorrectionFieldMetadataTrackLanguages, TrackID: track.ID,
						}}})
						if len(result.Corrections.Corrections.Metadata.TrackLanguages) != 0 ||
							!reflect.DeepEqual(result.Release.Media, initial.Media) || !reflect.DeepEqual(result.Release.Naming, initial.Naming) {
							t.Error("track Auto did not restore inspected media and automatic naming")
						}
						assertCoreCorrectionGeneration(t, fixture, result, previous, calls)
						previous = result.Release.Generation
					})
				})
			}
			unchanged, err := json.Marshal(initial)
			if err != nil || string(unchanged) != string(original) {
				t.Fatalf("track corrections mutated original generation: %v", err)
			}
		})
	}
}
