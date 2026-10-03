// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata/evidence"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/services/db/dbfixture"
	"github.com/autobrr/upbrr/pkg/api"
)

func openSceneEvidenceRepo(t *testing.T, databasePath string) *db.SQLiteRepository {
	t.Helper()
	repo, err := db.OpenContext(t.Context(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}

func TestSceneEvidenceReusesQueriesAndNFOAcrossRestart(t *testing.T) {
	t.Parallel()
	databasePath := filepath.Join(t.TempDir(), "evidence.sqlite")
	dbfixture.WriteMigrated(t, databasePath)
	repo := openSceneEvidenceRepo(t, databasePath)
	const release = "Example.Movie.2026.1080p.WEB.H264-GRP"
	var requests atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		var body string
		switch {
		case strings.Contains(request.URL.Path, "/search/r:"):
			body = `{"resultsCount":1,"results":[{"release":"` + release + `","hasNFO":"yes"}]}`
		case strings.Contains(request.URL.Path, "/details/"):
			body = `{"files":[{"name":"example.nfo"}],"archived-files":[{"name":"` + release + `.mkv"}]}`
		case strings.Contains(request.URL.Path, "/imdb/"):
			body = `{"releases":[{"imdb":"tt1234567"}]}`
		case strings.Contains(request.URL.Path, "/download/file/"):
			body = "https://www.imdb.com/title/tt1234567/\nhttps://www.themoviedb.org/movie/42"
		default:
			t.Errorf("unexpected scene request: %s", request.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
			Request:    request,
		}, nil
	})}
	meta := preparationstate.State{VideoPath: filepath.Join(t.TempDir(), release+".mkv")}
	for _, freshness := range []api.ExternalFreshness{api.ExternalFreshnessLoad, api.ExternalFreshnessReuse, api.ExternalFreshnessLoad} {
		ctx, scope := evidence.WithScope(t.Context(), repo, meta.VideoPath, "fingerprint", freshness, nil)
		// New artifact directories ensure the persisted raw NFO, not an old file, is reused.
		detector := newSRRDBDetector(client, "https://scene.example.invalid", t.TempDir(), t.TempDir())
		for attempt := range 2 {
			result, err := detector.Detect(ctx, meta)
			if err != nil || scope.Err() != nil {
				t.Fatalf("detect: %v; evidence: %v", err, scope.Err())
			}
			if !result.IsScene || result.SceneName != release || result.IMDBID != 1234567 || result.TMDBID != 42 || result.Renamed {
				t.Fatalf("unexpected scene result: %#v", result)
			}
			if result.NFONew != (attempt == 0) {
				t.Fatalf("NFO new = %t on attempt %d", result.NFONew, attempt)
			}
			if data, readErr := os.ReadFile(result.NFOPath); readErr != nil || !strings.Contains(string(data), "tt1234567") {
				t.Fatalf("replayed NFO = %q, %v", data, readErr)
			}
			if got := requests.Load(); got != 4 {
				t.Fatalf("freshness %q: requests = %d, want 4 initial raw lookups", freshness, got)
			}
		}
		if err := repo.Close(); err != nil {
			t.Fatal(err)
		}
		repo = openSceneEvidenceRepo(t, databasePath)
	}
}

func TestSceneEvidenceRescoresResolutionAndYearWithoutRequests(t *testing.T) {
	t.Parallel()
	databasePath := filepath.Join(t.TempDir(), "evidence.sqlite")
	dbfixture.WriteMigrated(t, databasePath)
	repo := openSceneEvidenceRepo(t, databasePath)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if !strings.Contains(request.URL.Path, "/search/imdb:tt1234567/") {
			t.Errorf("unexpected scene request: %s", request.URL.Path)
		}
		_, _ = io.WriteString(w, `{"resultsCount":3,"results":[
			{"release":"Example.Movie.2026.1080p.WEB.H264-GRP","imdbId":"1234567"},
			{"release":"Example.Movie.2026.2160p.WEB.H265-GRP","imdbId":"1234567"},
			{"release":"Example.Movie.2025.2160p.WEB.H265-GRP","imdbId":"1234567"}]}`)
	}))
	defer server.Close()
	meta := preparationstate.State{
		SourcePath: filepath.Join(t.TempDir(), "Example Movie"),
		Identity:   api.ExternalIdentity{IMDBID: 1234567},
		Release:    api.ReleaseInfo{Year: 2026, Resolution: "1080p"},
	}
	for _, correction := range []struct {
		resolution string
		year       int
		want       string
	}{
		{
			resolution: "1080p",
			year:       2026,
			want:       "Example.Movie.2026.1080p.WEB.H264-GRP",
		},
		{
			resolution: "2160p",
			year:       2026,
			want:       "Example.Movie.2026.2160p.WEB.H265-GRP",
		},
		{
			resolution: "2160p",
			year:       2025,
			want:       "Example.Movie.2025.2160p.WEB.H265-GRP",
		},
		{resolution: "720p", year: 2025},
	} {
		meta.ReleaseNameOverrides.Resolution = &correction.resolution
		meta.ReleaseNameOverrides.ManualYear = &correction.year
		ctx, scope := evidence.WithScope(t.Context(), repo, meta.SourcePath, "fingerprint", api.ExternalFreshnessReuse, nil)
		detector := newSRRDBDetector(server.Client(), server.URL, "", "")
		result, err := detector.detectViaIMDB(ctx, meta, sceneLocalCandidates(meta), 1234567)
		if err != nil || scope.Err() != nil || result.SceneName != correction.want || result.IsScene != (correction.want != "") {
			t.Fatalf("correction %s/%d = %#v, %v; evidence: %v", correction.resolution, correction.year, result, err, scope.Err())
		}
		if got := requests.Load(); got != 1 {
			t.Fatalf("local scoring refetched raw candidates: requests = %d", got)
		}
	}
}

func TestSceneEvidenceRetainsOutcomesUntilFreshLoadOrHistoryPurge(t *testing.T) {
	t.Parallel()
	lookups := []struct {
		name  string
		fetch func(context.Context, *srrdbDetector) error
	}{
		{name: "exact", fetch: func(ctx context.Context, detector *srrdbDetector) error {
			_, _, err := detector.searchExactR(ctx, "Example.Release")
			return err
		}},
		{name: "word", fetch: func(ctx context.Context, detector *srrdbDetector) error {
			_, err := detector.searchWord(ctx, "Example Show S01E01")
			return err
		}},
		{name: "IMDb search", fetch: func(ctx context.Context, detector *srrdbDetector) error {
			_, err := detector.fetchIMDBReleases(ctx, preparationstate.State{}, 1234567)
			return err
		}},
		{name: "details", fetch: func(ctx context.Context, detector *srrdbDetector) error {
			_, err := detector.fetchDetails(ctx, "Example.Release")
			return err
		}},
		{name: "IMDb ID", fetch: func(ctx context.Context, detector *srrdbDetector) error {
			_, err := detector.fetchIMDB(ctx, "Example.Release")
			return err
		}},
	}
	for _, lookup := range lookups {
		for _, outcome := range []string{"empty", "HTTP 500", "invalid JSON"} {
			t.Run(lookup.name+"/"+outcome, func(t *testing.T) {
				databasePath := filepath.Join(t.TempDir(), "evidence.sqlite")
				dbfixture.WriteMigrated(t, databasePath)
				repo := openSceneEvidenceRepo(t, databasePath)
				source := filepath.Join(t.TempDir(), "Example.Release.mkv")
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					requests.Add(1)
					switch outcome {
					case "HTTP 500":
						w.WriteHeader(http.StatusInternalServerError)
					case "invalid JSON":
						_, _ = io.WriteString(w, "{")
					default:
						_, _ = io.WriteString(w, "{}")
					}
				}))
				defer server.Close()
				detector := newSRRDBDetector(server.Client(), server.URL, t.TempDir(), t.TempDir())
				for pass, freshness := range []api.ExternalFreshness{api.ExternalFreshnessLoad, api.ExternalFreshnessReuse, api.ExternalFreshnessLoad} {
					ctx, scope := evidence.WithScope(t.Context(), repo, source, "fingerprint", freshness, nil)
					for attempt := range 2 {
						err := lookup.fetch(ctx, detector)
						if outcome == "empty" && err != nil || outcome != "empty" && err == nil {
							t.Fatalf("pass %d, attempt %d: lookup error = %v", pass, attempt, err)
						}
						if outcome != "empty" && (pass > 0 || attempt > 0) && !errors.Is(err, evidence.ErrSuppressed) {
							t.Fatalf("retained failure = %v, want suppressed", err)
						}
						if scope.Err() != nil {
							t.Fatal(scope.Err())
						}
					}
					want := int32(1)
					if outcome == "empty" && pass == 2 {
						want = 2
					}
					if got := requests.Load(); got != want {
						t.Fatalf("freshness %q: requests = %d, want %d", freshness, got, want)
					}
				}
				before := requests.Load()
				if err := repo.PurgeContentData(t.Context(), source); err != nil {
					t.Fatal(err)
				}
				ctx, _ := evidence.WithScope(t.Context(), repo, source, "fingerprint", api.ExternalFreshnessLoad, nil)
				err := lookup.fetch(ctx, detector)
				if errors.Is(err, evidence.ErrSuppressed) || requests.Load() != before+1 {
					t.Fatalf("History purge did not re-enable request: %v; requests = %d, before = %d", err, requests.Load(), before)
				}
			})
		}
	}
}

func TestSceneEvidenceSeparatesActualQueries(t *testing.T) {
	t.Parallel()
	databasePath := filepath.Join(t.TempDir(), "evidence.sqlite")
	dbfixture.WriteMigrated(t, databasePath)
	repo := openSceneEvidenceRepo(t, databasePath)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, `{"resultsCount":1,"results":[{"release":"Example.Release","imdbId":"1234567"}]}`)
	}))
	defer server.Close()
	ctx, scope := evidence.WithScope(t.Context(), repo, filepath.Join(t.TempDir(), "Example.Release.mkv"), "fingerprint", api.ExternalFreshnessReuse, nil)
	detector := newSRRDBDetector(server.Client(), server.URL, "", "")
	for range 2 {
		for _, imdbID := range []int{1234567, 2345678} {
			if _, err := detector.fetchIMDBReleases(ctx, preparationstate.State{}, imdbID); err != nil {
				t.Fatal(err)
			}
		}
		for _, query := range []string{"Example Show S01E01", "Example Show S01E02"} {
			if _, err := detector.searchWord(ctx, query); err != nil {
				t.Fatal(err)
			}
		}
	}
	if requests.Load() != 4 || scope.Err() != nil {
		t.Fatalf("query requests = %d, want 4; evidence: %v", requests.Load(), scope.Err())
	}
}

func TestSceneEvidenceCancellationDoesNotRetainNonScene(t *testing.T) {
	t.Parallel()
	databasePath := filepath.Join(t.TempDir(), "evidence.sqlite")
	dbfixture.WriteMigrated(t, databasePath)
	repo := openSceneEvidenceRepo(t, databasePath)
	var requests atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if requests.Add(1) == 1 {
			return nil, fmt.Errorf("interrupted request: %w", context.Canceled)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"resultsCount":1,"results":[{"release":"Example.Release","imdbId":"1234567"}]}`)),
			Request:    request,
		}, nil
	})}
	meta := preparationstate.State{SourcePath: filepath.Join(t.TempDir(), "Example.Release")}
	detector := newSRRDBDetector(client, "https://scene.example.invalid", "", "")
	ctx, _ := evidence.WithScope(t.Context(), repo, meta.SourcePath, "fingerprint", api.ExternalFreshnessReuse, nil)
	if _, err := detector.Detect(ctx, meta); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled detection = %v", err)
	}
	ctx, scope := evidence.WithScope(t.Context(), repo, meta.SourcePath, "fingerprint", api.ExternalFreshnessReuse, nil)
	result, err := detector.Detect(ctx, meta)
	if err != nil || !result.IsScene || requests.Load() != 2 || scope.Err() != nil {
		t.Fatalf("retry = %#v, %v; requests = %d; evidence: %v", result, err, requests.Load(), scope.Err())
	}
}
