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
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/autobrr/upbrr/pkg/api"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type sceneLogRecorder struct {
	api.NopLogger
	lines []string
}

func (r *sceneLogRecorder) Debugf(format string, args ...any) {
	r.lines = append(r.lines, fmt.Sprintf(format, args...))
}

func (r *sceneLogRecorder) Tracef(format string, args ...any) {
	r.lines = append(r.lines, fmt.Sprintf(format, args...))
}

func (r *sceneLogRecorder) join() string {
	return strings.Join(r.lines, "\n")
}

func TestSceneDetectorSRRDB(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("/v1/search/r:Example.Release", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resultsCount":1,"results":[{"release":"Example.Release.2024.1080p-WEB","imdbId":"1234567"}]}`))
	})

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	cacheDir := t.TempDir()
	nfoDir := t.TempDir()
	detector := newSRRDBDetector(server.Client(), server.URL, cacheDir, nfoDir)

	meta := preparationstate.State{VideoPath: "/data/Example.Release.mkv"}
	result, err := detector.Detect(context.Background(), meta)
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if !result.IsScene {
		t.Fatalf("expected scene match")
	}
	if !strings.HasPrefix(result.SceneName, "Example.Release") {
		t.Fatalf("unexpected scene name: %q", result.SceneName)
	}
	if result.IMDBID != 1234567 {
		t.Fatalf("unexpected imdb id: %d", result.IMDBID)
	}
}

func TestSceneDetectorSRRDBFetchesIMDbWhenSearchOmitsIt(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("/v1/search/r:Example.Release", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resultsCount":1,"results":[{"release":"Example.Release.2024.1080p-WEB","hasNFO":"no"}]}`))
	})
	handler.HandleFunc("/v1/imdb/Example.Release.2024.1080p-WEB", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"releases":[{"imdb":"tt7654321","title":"Example Release"}]}`))
	})

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	cacheDir := t.TempDir()
	nfoDir := t.TempDir()
	detector := newSRRDBDetector(server.Client(), server.URL, cacheDir, nfoDir)

	meta := preparationstate.State{VideoPath: "/data/Example.Release.mkv"}
	result, err := detector.Detect(context.Background(), meta)
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if !result.IsScene {
		t.Fatalf("expected scene match")
	}
	if result.IMDBID != 7654321 {
		t.Fatalf("unexpected imdb id: %d", result.IMDBID)
	}
}

func TestSceneDetectorSRRDBLogsNFODownloadLifecycle(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("/v1/search/r:Example.Release", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resultsCount":1,"results":[{"release":"Example.Release.2024.1080p-WEB","imdbId":"1234567","hasNFO":"yes"}]}`))
	})
	handler.HandleFunc("/v1/details/Example.Release.2024.1080p-WEB", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"files":[{"name":"Example.Release.Custom.NFO"}]}`))
	})

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	client := server.Client()
	baseTransport := client.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.EqualFold(req.URL.Host, "www.srrdb.com") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("URL: https://www.imdb.com/title/tt1234567/")),
				Request:    req,
			}, nil
		}
		return baseTransport.RoundTrip(req)
	})

	logger := &sceneLogRecorder{}
	detector := newSRRDBDetector(client, server.URL, t.TempDir(), t.TempDir())
	detector.logger = logger

	result, err := detector.Detect(context.Background(), preparationstate.State{VideoPath: "/data/Example.Release.mkv"})
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if result.NFOPath == "" || !result.NFONew {
		t.Fatalf("expected downloaded NFO, got path=%q new=%t", result.NFOPath, result.NFONew)
	}
	logs := logger.join()
	for _, want := range []string{
		"metadata: scene nfo lookup start",
		"metadata: scene nfo details selected",
		"metadata: scene nfo downloading",
		"metadata: scene nfo downloaded",
		"metadata: scene nfo attached downloaded=true",
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("expected log %q in:\n%s", want, logs)
		}
	}
}

func TestSceneDetectorSRRDBNFOFetchFailurePreservesMatch(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("/v1/search/r:Example.Release", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resultsCount":1,"results":[{"release":"Example.Release.2024.1080p-WEB","imdbId":"1234567","hasNFO":"yes"}]}`))
	})

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	client := server.Client()
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.EqualFold(req.URL.Host, "www.srrdb.com") {
			return nil, errors.New("nfo unavailable")
		}
		return http.DefaultTransport.RoundTrip(req)
	})

	detector := newSRRDBDetector(client, server.URL, t.TempDir(), t.TempDir())

	result, err := detector.Detect(context.Background(), preparationstate.State{VideoPath: "/data/Example.Release.mkv"})
	if err == nil {
		t.Fatalf("expected NFO fetch failure to be returned")
	}
	if !isSceneNFOError(err) {
		t.Fatalf("expected scene NFO error, got %v", err)
	}
	if !result.IsScene || result.SceneName != "Example.Release.2024.1080p-WEB" || result.IMDBID != 1234567 {
		t.Fatalf("expected scene match to survive NFO fetch failure, got %#v", result)
	}
	if result.NFOPath != "" || result.NFONew {
		t.Fatalf("expected no NFO attachment on fetch failure, got path=%q new=%t", result.NFOPath, result.NFONew)
	}
}

func TestSceneDetectorSRRDBDetailsFailureDirectNFOSuccessPreservesWarning(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("/v1/search/r:Example.Release", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resultsCount":1,"results":[{"release":"Example.Release.2024.1080p-WEB","imdbId":"1234567","hasNFO":"yes"}]}`))
	})
	handler.HandleFunc("/v1/details/Example.Release.2024.1080p-WEB", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"files":[`))
	})

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	client := server.Client()
	baseTransport := client.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.EqualFold(req.URL.Host, "www.srrdb.com") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("TMDB: https://www.themoviedb.org/movie/42")),
				Request:    req,
			}, nil
		}
		return baseTransport.RoundTrip(req)
	})

	nfoDir := t.TempDir()
	detector := newSRRDBDetector(client, server.URL, t.TempDir(), nfoDir)

	result, err := detector.Detect(context.Background(), preparationstate.State{VideoPath: "/data/Example.Release.mkv"})
	if err == nil {
		t.Fatalf("expected details failure to remain visible")
	}
	if !isSceneNFOError(err) || !strings.Contains(err.Error(), "scene: decode details") {
		t.Fatalf("expected scene NFO details decode error, got %v", err)
	}
	expectedPath := filepath.Join(nfoDir, "example.release.2024.1080p-web.nfo")
	if result.NFOPath != expectedPath || !result.NFONew {
		t.Fatalf("expected direct NFO attachment path=%q new=true, got path=%q new=%t", expectedPath, result.NFOPath, result.NFONew)
	}
	if result.TMDBID != 42 {
		t.Fatalf("expected external ID parsed from direct NFO, got %d", result.TMDBID)
	}
}

func TestSceneDetectorSRRDBDetailsAndNFOFailuresPreserveBothCauses(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("/v1/search/r:Example.Release", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resultsCount":1,"results":[{"release":"Example.Release.2024.1080p-WEB","imdbId":"1234567","hasNFO":"yes"}]}`))
	})
	handler.HandleFunc("/v1/details/Example.Release.2024.1080p-WEB", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"files":[`))
	})

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	client := server.Client()
	baseTransport := client.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.EqualFold(req.URL.Host, "www.srrdb.com") {
			return nil, errors.New("nfo unavailable")
		}
		return baseTransport.RoundTrip(req)
	})

	detector := newSRRDBDetector(client, server.URL, t.TempDir(), t.TempDir())

	result, err := detector.Detect(context.Background(), preparationstate.State{VideoPath: "/data/Example.Release.mkv"})
	if err == nil {
		t.Fatalf("expected details and NFO failures to be returned")
	}
	if !isSceneNFOError(err) {
		t.Fatalf("expected scene NFO error, got %v", err)
	}
	if !strings.Contains(err.Error(), "scene: decode details") || !strings.Contains(err.Error(), "scene: nfo request") {
		t.Fatalf("expected details and NFO causes, got %v", err)
	}
	if !result.IsScene || result.SceneName != "Example.Release.2024.1080p-WEB" || result.IMDBID != 1234567 {
		t.Fatalf("expected scene match to survive NFO side-effect failures, got %#v", result)
	}
	if result.NFOPath != "" || result.NFONew {
		t.Fatalf("expected no NFO attachment on NFO failure, got path=%q new=%t", result.NFOPath, result.NFONew)
	}
}

func TestSceneDetectorSRRDBNFOFailurePreservesMatch(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("/v1/search/r:Example.Release", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resultsCount":1,"results":[{"release":"Example.Release.2024.1080p-WEB","imdbId":"1234567","hasNFO":"yes"}]}`))
	})

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	cacheDir := t.TempDir()
	nfoDir := filepath.Join(t.TempDir(), "nfo-file")
	if err := os.WriteFile(nfoDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write nfo dir placeholder: %v", err)
	}
	detector := newSRRDBDetector(server.Client(), server.URL, cacheDir, nfoDir)

	result, err := detector.Detect(context.Background(), preparationstate.State{VideoPath: "/data/Example.Release.mkv"})
	if err == nil {
		t.Fatalf("expected NFO save failure to be returned")
	} else if !strings.Contains(err.Error(), "scene: nfo dir") {
		t.Fatalf("expected NFO dir error, got %v", err)
	}
	if !isSceneNFOError(err) {
		t.Fatalf("expected scene NFO error, got %v", err)
	}
	if !result.IsScene || result.SceneName != "Example.Release.2024.1080p-WEB" || result.IMDBID != 1234567 {
		t.Fatalf("expected scene match to survive NFO save failure, got %#v", result)
	}
}

func TestSceneDetectorSRRDBDetailsFailureCachedNFOPreservesWarning(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("/v1/search/r:Example.Release", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resultsCount":1,"results":[{"release":"Example.Release.2024.1080p-WEB","imdbId":"1234567","hasNFO":"yes"}]}`))
	})
	handler.HandleFunc("/v1/details/Example.Release.2024.1080p-WEB", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"files":[`))
	})

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	cacheDir := t.TempDir()
	nfoDir := t.TempDir()
	nfoPath := filepath.Join(nfoDir, "example.release.2024.1080p-web.nfo")
	if err := os.WriteFile(nfoPath, []byte("URL: https://www.imdb.com/title/tt7654321/"), 0o600); err != nil {
		t.Fatalf("write cached nfo: %v", err)
	}
	detector := newSRRDBDetector(server.Client(), server.URL, cacheDir, nfoDir)

	result, err := detector.Detect(context.Background(), preparationstate.State{VideoPath: "/data/Example.Release.mkv"})
	if err == nil {
		t.Fatalf("expected details failure to remain visible")
	}
	if !isSceneNFOError(err) || !strings.Contains(err.Error(), "scene: decode details") {
		t.Fatalf("expected scene NFO details decode error, got %v", err)
	}
	if result.NFOPath != nfoPath || result.NFONew {
		t.Fatalf("expected cached NFO path %q with NFONew=false, got path=%q new=%t", nfoPath, result.NFOPath, result.NFONew)
	}
	if result.IMDBID != 1234567 {
		t.Fatalf("expected search imdb id preserved, got %d", result.IMDBID)
	}
}

func TestSceneDetectorSRRDBCachedNFOPreservesAttachment(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("/v1/search/r:Example.Release", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resultsCount":1,"results":[{"release":"Example.Release.2024.1080p-WEB","imdbId":"1234567","hasNFO":"yes"}]}`))
	})

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	cacheDir := t.TempDir()
	nfoDir := t.TempDir()
	nfoPath := filepath.Join(nfoDir, "example.release.2024.1080p-web.nfo")
	if err := os.WriteFile(nfoPath, []byte("URL: https://www.imdb.com/title/tt7654321/"), 0o600); err != nil {
		t.Fatalf("write cached nfo: %v", err)
	}
	detector := newSRRDBDetector(server.Client(), server.URL, cacheDir, nfoDir)

	result, err := detector.Detect(context.Background(), preparationstate.State{VideoPath: "/data/Example.Release.mkv"})
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if result.NFOPath != nfoPath {
		t.Fatalf("expected cached NFO path %q, got %q", nfoPath, result.NFOPath)
	}
	if result.NFONew {
		t.Fatalf("expected cached NFO to report NFONew=false")
	}
	if result.IMDBID != 1234567 {
		t.Fatalf("expected search imdb id preserved, got %d", result.IMDBID)
	}
}

func TestSceneDetectorSRRDBNFOContextCancellationIsFatal(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("/v1/search/r:Example.Release", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resultsCount":1,"results":[{"release":"Example.Release.2024.1080p-WEB","imdbId":"1234567","hasNFO":"yes"}]}`))
	})

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	client := server.Client()
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.EqualFold(req.URL.Host, "www.srrdb.com") {
			cancel()
			return nil, req.Context().Err()
		}
		return http.DefaultTransport.RoundTrip(req)
	})

	detector := newSRRDBDetector(client, server.URL, t.TempDir(), t.TempDir())

	result, err := detector.Detect(ctx, preparationstate.State{VideoPath: "/data/Example.Release.mkv"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got result=%#v err=%v", result, err)
	}
	if result.IsScene {
		t.Fatalf("expected cancellation to abort scene match, got %#v", result)
	}
}

// srrdbFallbackHandler routes the srrdb endpoints used by the imdb: fallback so
// tests can drive scene/rename detection without touching the live service.
type srrdbFallbackHandler struct {
	imdbPages   map[int]string // page -> JSON body for /v1/search/imdb:<id>/...
	details     map[string]string
	rResults    map[string]string // r:<name> -> JSON body (name is the unescaped search term)
	wordResults map[string]string // word:<query> -> JSON body (query is the unescaped search term)
	imdbStat    int               // non-zero overrides the imdb: search status code
	imdbHits    *atomic.Int32     // when set, counts /v1/search/imdb: requests
	imdbPaths   *[]string         // when set, captures /v1/search/imdb: request paths
	wordHits    *atomic.Int32     // when set, counts /v1/search/word: requests
	wordPaths   *[]string         // when set, captures escaped /v1/search/word: request paths
}

func (h srrdbFallbackHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Path
	switch {
	case strings.Contains(path, "/v1/search/word:"):
		if h.wordHits != nil {
			h.wordHits.Add(1)
		}
		if h.wordPaths != nil {
			*h.wordPaths = append(*h.wordPaths, r.URL.EscapedPath())
		}
		query := path[strings.Index(path, "/v1/search/word:")+len("/v1/search/word:"):]
		if body, ok := h.wordResults[query]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		_, _ = w.Write([]byte(`{"resultsCount":0,"results":[]}`))
	case strings.Contains(path, "/v1/search/r:"):
		name := path[strings.Index(path, "/v1/search/r:")+len("/v1/search/r:"):]
		if body, ok := h.rResults[name]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		_, _ = w.Write([]byte(`{"resultsCount":0,"results":[]}`))
	case strings.Contains(path, "/v1/search/imdb:"):
		if h.imdbHits != nil {
			h.imdbHits.Add(1)
		}
		if h.imdbPaths != nil {
			*h.imdbPaths = append(*h.imdbPaths, path)
		}
		if h.imdbStat != 0 {
			w.WriteHeader(h.imdbStat)
			return
		}
		page := 1
		if _, after, ok := strings.Cut(path, "page:"); ok {
			if p, err := strconv.Atoi(after); err == nil {
				page = p
			}
		}
		if body, ok := h.imdbPages[page]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		_, _ = w.Write([]byte(`{"resultsCount":0,"results":[]}`))
	case strings.HasPrefix(path, "/v1/details/"):
		release := strings.TrimPrefix(path, "/v1/details/")
		if body, ok := h.details[release]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		_, _ = w.Write([]byte(`{"files":[],"archived-files":[]}`))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func renamedSceneMeta(videoPath string) preparationstate.State {
	return preparationstate.State{
		VideoPath: videoPath,
		Identity:  api.ExternalIdentity{IMDBID: 1234567},
		Release: api.ReleaseInfo{
			Resolution: "1080p",
			Year:       2026,
			Group:      "GRP",
			Source:     "BluRay",
			Codec:      []string{"x264"},
		},
	}
}

func TestSceneDetectorIMDBFallbackDetectsRename(t *testing.T) {
	handler := srrdbFallbackHandler{
		imdbPages: map[int]string{
			1: `{"resultsCount":1,"results":[{"release":"Example.Movie.2026.1080p.BluRay.x264-GRP","imdbId":"1234567","hasNFO":"no","isForeign":"no"}]}`,
		},
		details: map[string]string{
			"Example.Movie.2026.1080p.BluRay.x264-GRP": `{"files":[],"archived-files":[{"name":"example.movie.2026.1080p.bluray.x264-grp.mkv","crc":"AABBCCDD","size":8000000000}]}`,
		},
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	detector := newSRRDBDetector(server.Client(), server.URL, t.TempDir(), t.TempDir())
	result, err := detector.Detect(context.Background(), renamedSceneMeta("/data/Example Movie 2026 1080p BluRay x264 GRP.mkv"))
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if !result.IsScene {
		t.Fatalf("expected scene via imdb fallback")
	}
	if !result.Renamed {
		t.Fatalf("expected renamed verdict")
	}
	if result.SceneName != "Example.Movie.2026.1080p.BluRay.x264-GRP" {
		t.Fatalf("unexpected scene name: %q", result.SceneName)
	}
	if result.IMDBID != 1234567 {
		t.Fatalf("unexpected imdb id: %d", result.IMDBID)
	}
	if strings.TrimSpace(result.RenamedReason) == "" {
		t.Fatalf("expected a rename reason")
	}
}

func TestSceneDetectorIMDBFallbackUnmodifiedNotRenamed(t *testing.T) {
	handler := srrdbFallbackHandler{
		imdbPages: map[int]string{
			1: `{"resultsCount":1,"results":[{"release":"Example.Movie.2026.1080p.BluRay.x264-GRP","imdbId":"1234567","hasNFO":"no"}]}`,
		},
		details: map[string]string{
			"Example.Movie.2026.1080p.BluRay.x264-GRP": `{"archived-files":[{"name":"Example.Movie.2026.1080p.BluRay.x264-GRP.mkv","size":8000000000}]}`,
		},
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	detector := newSRRDBDetector(server.Client(), server.URL, t.TempDir(), t.TempDir())
	// On-disk media filename exactly matches the archived scene media filename, so
	// this must not be flagged as renamed.
	result, err := detector.Detect(context.Background(), renamedSceneMeta("/data/Example.Movie.2026.1080p.BluRay.x264-GRP.mkv"))
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if !result.IsScene {
		t.Fatalf("expected scene match")
	}
	if result.Renamed {
		t.Fatalf("did not expect a rename verdict, reason=%q", result.RenamedReason)
	}
}

func TestSceneDetectorIMDBFallbackCaseOnlyDiffIsRenamed(t *testing.T) {
	handler := srrdbFallbackHandler{
		imdbPages: map[int]string{
			1: `{"resultsCount":1,"results":[{"release":"Example.Movie.2026.1080p.BluRay.x264-GRP","imdbId":"1234567","hasNFO":"no"}]}`,
		},
		details: map[string]string{
			"Example.Movie.2026.1080p.BluRay.x264-GRP": `{"archived-files":[{"name":"example.movie.2026.1080p.bluray.x264-grp.mkv","size":8000000000}]}`,
		},
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	detector := newSRRDBDetector(server.Client(), server.URL, t.TempDir(), t.TempDir())
	// Local media filename matches the archived one except for casing; srrdb is
	// authoritative, so a casing-only difference is a rename.
	result, err := detector.Detect(context.Background(), renamedSceneMeta("/data/Example.Movie.2026.1080p.BluRay.x264-GRP.mkv"))
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if !result.IsScene || !result.Renamed {
		t.Fatalf("expected scene + renamed on casing-only difference, got %#v", result)
	}
}

func TestSceneDetectorFolderMatchDetectsRenamedFile(t *testing.T) {
	// Folder is the canonical scene name; the media file inside is renamed.
	// IMDb tt0012345 is resolved; the imdb: search returns the
	// release set; the folder candidate selects the exact release; the media file
	// differs from the archived scene filename ⇒ scene + renamed.
	const release = "Example.Driver.2026.1080p.BluRay.x264-GRP"
	handler := srrdbFallbackHandler{
		imdbPages: map[int]string{
			1: `{"resultsCount":3,"results":[` +
				`{"release":"Example.Driver.2026.German.DL.1080p.BluRay.x264-ALT","imdbId":"0012345","isForeign":"yes"},` +
				`{"release":"Example.Driver.2026.1080p.BluRay.x264-GRP","imdbId":"0012345","isForeign":"no"},` +
				`{"release":"Example.Driver.2026.720p.BluRay.x264-ALT2","imdbId":"0012345","isForeign":"no"}]}`,
		},
		details: map[string]string{
			release: `{"archived-files":[{"name":"example.driver.2026.1080p.bluray.x264-grp.mkv","crc":"9CDDBFCD","size":4695029966}]}`,
		},
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	meta := preparationstate.State{
		SourcePath: "/data/Example.Driver.2026.1080p.BluRay.x264-GRP",
		VideoPath:  "/data/Example.Driver.2026.1080p.BluRay.x264-GRP/example-driver-renamed.mkv",
		Identity:   api.ExternalIdentity{IMDBID: 12345},
		Release: api.ReleaseInfo{
			Resolution: "1080p",
			Year:       2026,
			Group:      "GRP",
			Source:     "BluRay",
			Codec:      []string{"x264"},
			Language:   []string{"English"},
		},
	}
	detector := newSRRDBDetector(server.Client(), server.URL, t.TempDir(), t.TempDir())
	result, err := detector.Detect(context.Background(), meta)
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if !result.IsScene {
		t.Fatalf("expected scene match via folder candidate")
	}
	if result.SceneName != release {
		t.Fatalf("expected release %q, got %q", release, result.SceneName)
	}
	if result.IMDBID != 12345 {
		t.Fatalf("expected imdb 12345, got %d", result.IMDBID)
	}
	if !result.Renamed || strings.TrimSpace(result.RenamedReason) == "" {
		t.Fatalf("expected renamed verdict for renamed media file, got %#v", result)
	}
}

func TestSceneDetectorIMDBFallbackPaginates(t *testing.T) {
	// Page 1 is full (40 wrong-resolution entries); the match is only on page 2.
	var page1 strings.Builder
	page1.WriteString(`{"resultsCount":41,"results":[`)
	for i := range 40 {
		if i > 0 {
			page1.WriteString(",")
		}
		page1.WriteString(`{"release":"Example.Movie.2026.720p.BluRay.x264-GRP","imdbId":"1234567"}`)
	}
	page1.WriteString(`]}`)

	handler := srrdbFallbackHandler{
		imdbPages: map[int]string{
			1: page1.String(),
			2: `{"resultsCount":41,"results":[{"release":"Example.Movie.2026.1080p.BluRay.x264-GRP","imdbId":"1234567"}]}`,
		},
		details: map[string]string{
			"Example.Movie.2026.1080p.BluRay.x264-GRP": `{"archived-files":[{"name":"example.movie.2026.1080p.bluray.x264-grp.mkv","size":8000000000}]}`,
		},
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	detector := newSRRDBDetector(server.Client(), server.URL, t.TempDir(), t.TempDir())
	result, err := detector.Detect(context.Background(), renamedSceneMeta("/data/Example Movie 2026 1080p BluRay x264 GRP.mkv"))
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if !result.IsScene || result.SceneName != "Example.Movie.2026.1080p.BluRay.x264-GRP" {
		t.Fatalf("expected paginated match, got %#v", result)
	}
}

func TestSceneDetectorTVWordSearchRunsBeforeIMDBFallback(t *testing.T) {
	imdbHits := &atomic.Int32{}
	wordHits := &atomic.Int32{}
	var wordPaths []string
	const release = "Example.Show.S02E03.1080p.WEB.h264-GRP"
	handler := srrdbFallbackHandler{
		imdbHits:  imdbHits,
		wordHits:  wordHits,
		wordPaths: &wordPaths,
		wordResults: map[string]string{
			"Example Show S02E03": `{"resultsCount":1,"results":[{"release":"` + release + `","imdbId":"1234567","hasNFO":"no","isForeign":"no"}]}`,
		},
		imdbPages: map[int]string{
			1: `{"resultsCount":1,"results":[{"release":"Example.Show.S02E03.720p.WEB.h264-ALT","imdbId":"1234567"}]}`,
		},
		details: map[string]string{
			release: `{"archived-files":[{"name":"example.show.s02e03.1080p.web.h264-grp.mkv","size":1000000000}]}`,
		},
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	meta := preparationstate.State{
		VideoPath: "/data/" + release + ".mkv",
		Identity:  api.ExternalIdentity{IMDBID: 1234567},
		Release: api.ReleaseInfo{
			Category:   "TV",
			Title:      "Example Show",
			Season:     2,
			Episode:    3,
			Resolution: "1080p",
			Source:     "Web",
			Codec:      []string{"H.264"},
			Group:      "GRP",
		},
	}
	detector := newSRRDBDetector(server.Client(), server.URL, t.TempDir(), t.TempDir())
	result, err := detector.Detect(context.Background(), meta)
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if !result.IsScene || result.SceneName != release {
		t.Fatalf("expected word search match %q, got %#v", release, result)
	}
	if got := wordHits.Load(); got != 1 {
		t.Fatalf("expected one word search, got %d", got)
	}
	if got := imdbHits.Load(); got != 0 {
		t.Fatalf("expected imdb fallback skipped after word match, got %d", got)
	}
	if len(wordPaths) != 1 || !strings.Contains(wordPaths[0], "/v1/search/word:Example%20Show%20S02E03") {
		t.Fatalf("expected word search path for title and episode")
	}
}

func TestSceneDetectorTVWordSearchAllowsSeasonOnly(t *testing.T) {
	imdbHits := &atomic.Int32{}
	wordHits := &atomic.Int32{}
	var wordPaths []string
	const release = "Example.Show.S02.1080p.WEB.h264-GRP"
	handler := srrdbFallbackHandler{
		imdbHits:  imdbHits,
		wordHits:  wordHits,
		wordPaths: &wordPaths,
		wordResults: map[string]string{
			"Example Show S02": `{"resultsCount":1,"results":[{"release":"` + release + `","imdbId":"1234567","hasNFO":"no","isForeign":"no"}]}`,
		},
		details: map[string]string{
			release: `{"archived-files":[{"name":"example.show.s02e01.1080p.web.h264-grp.mkv","size":1000000000}]}`,
		},
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	meta := preparationstate.State{
		VideoPath: "/data/" + release + ".mkv",
		Identity:  api.ExternalIdentity{IMDBID: 1234567},
		Release: api.ReleaseInfo{
			Category:   "TV",
			Title:      "Example Show",
			Season:     2,
			Resolution: "1080p",
			Source:     "Web",
			Codec:      []string{"H.264"},
			Group:      "GRP",
		},
	}
	detector := newSRRDBDetector(server.Client(), server.URL, t.TempDir(), t.TempDir())
	result, err := detector.Detect(context.Background(), meta)
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if !result.IsScene || result.SceneName != release {
		t.Fatalf("expected season-only word search match %q, got %#v", release, result)
	}
	if got := wordHits.Load(); got != 1 {
		t.Fatalf("expected one word search, got %d", got)
	}
	if got := imdbHits.Load(); got != 0 {
		t.Fatalf("expected imdb fallback skipped after season-only word match, got %d", got)
	}
	if len(wordPaths) != 1 || !strings.Contains(wordPaths[0], "/v1/search/word:Example%20Show%20S02") {
		t.Fatalf("expected season-only word search path")
	}
}

func TestSceneDetectorTVWordMissSkipsIMDBFallback(t *testing.T) {
	imdbHits := &atomic.Int32{}
	wordHits := &atomic.Int32{}
	const release = "Example.Show.S02E03.1080p.WEB.h264-GRP"
	handler := srrdbFallbackHandler{
		imdbHits: imdbHits,
		wordHits: wordHits,
		rResults: map[string]string{
			release: `{"resultsCount":1,"results":[{"release":"` + release + `","imdbId":"1234567","hasNFO":"no","isForeign":"no"}]}`,
		},
		imdbPages: map[int]string{
			1: `{"resultsCount":1,"results":[{"release":"Example.Show.S02E03.720p.WEB.h264-ALT","imdbId":"1234567"}]}`,
		},
		details: map[string]string{
			release: `{"archived-files":[{"name":"example.show.s02e03.1080p.web.h264-grp.mkv","size":1000000000}]}`,
		},
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	meta := preparationstate.State{
		VideoPath: "/data/" + release + ".mkv",
		Identity:  api.ExternalIdentity{IMDBID: 1234567},
		Release: api.ReleaseInfo{
			Category:   "TV",
			Title:      "Example Show",
			Season:     2,
			Episode:    3,
			Resolution: "1080p",
			Source:     "Web",
			Codec:      []string{"H.264"},
			Group:      "GRP",
		},
	}
	detector := newSRRDBDetector(server.Client(), server.URL, t.TempDir(), t.TempDir())
	result, err := detector.Detect(context.Background(), meta)
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if !result.IsScene || result.SceneName != release {
		t.Fatalf("expected r fallback match %q, got %#v", release, result)
	}
	if got := wordHits.Load(); got != 1 {
		t.Fatalf("expected one word search, got %d", got)
	}
	if got := imdbHits.Load(); got != 0 {
		t.Fatalf("expected imdb fallback skipped for tv category, got %d", got)
	}
}

func TestSceneDetectorIMDBFallbackFiltersForeignWhenLanguageUnknown(t *testing.T) {
	var imdbPaths []string
	handler := srrdbFallbackHandler{
		imdbPaths: &imdbPaths,
		imdbPages: map[int]string{
			1: `{"resultsCount":1,"results":[{"release":"Example.Movie.2026.1080p.BluRay.x264-GRP","imdbId":"1234567","hasNFO":"no","isForeign":"no"}]}`,
		},
		details: map[string]string{
			"Example.Movie.2026.1080p.BluRay.x264-GRP": `{"archived-files":[{"name":"example.movie.2026.1080p.bluray.x264-grp.mkv","size":8000000000}]}`,
		},
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	detector := newSRRDBDetector(server.Client(), server.URL, t.TempDir(), t.TempDir())
	result, err := detector.Detect(context.Background(), renamedSceneMeta("/data/Example Movie 2026 1080p BluRay x264 GRP.mkv"))
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if !result.IsScene {
		t.Fatalf("expected scene match")
	}
	if len(imdbPaths) != 1 {
		t.Fatalf("expected one imdb request, got %d", len(imdbPaths))
	}
	if !strings.Contains(imdbPaths[0], "/v1/search/imdb:tt1234567/foreign:no/order:date/page:1") {
		t.Fatalf("expected foreign:no imdb search path")
	}
}

func TestSceneDetectorIMDBFallbackFiltersForeignNoWhenLanguageEnglish(t *testing.T) {
	var imdbPaths []string
	handler := srrdbFallbackHandler{
		imdbPaths: &imdbPaths,
		imdbPages: map[int]string{
			1: `{"resultsCount":1,"results":[{"release":"Example.Movie.2026.English.1080p.BluRay.x264-GRP","imdbId":"1234567","hasNFO":"no","isForeign":"no"}]}`,
		},
		details: map[string]string{
			"Example.Movie.2026.English.1080p.BluRay.x264-GRP": `{"archived-files":[{"name":"example.movie.2026.english.1080p.bluray.x264-grp.mkv","size":8000000000}]}`,
		},
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	meta := renamedSceneMeta("/data/Example Movie 2026 English 1080p BluRay x264 GRP.mkv")
	meta.Release.Language = []string{"English"}
	detector := newSRRDBDetector(server.Client(), server.URL, t.TempDir(), t.TempDir())
	result, err := detector.Detect(context.Background(), meta)
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if !result.IsScene {
		t.Fatalf("expected scene match")
	}
	if len(imdbPaths) != 1 {
		t.Fatalf("expected one imdb request, got %d", len(imdbPaths))
	}
	if !strings.Contains(imdbPaths[0], "/v1/search/imdb:tt1234567/foreign:no/order:date/page:1") {
		t.Fatalf("expected foreign:no imdb search path")
	}
}

func TestSceneDetectorIMDBFallbackFiltersForeignYesWhenLanguageNonEnglish(t *testing.T) {
	var imdbPaths []string
	handler := srrdbFallbackHandler{
		imdbPaths: &imdbPaths,
		imdbPages: map[int]string{
			1: `{"resultsCount":1,"results":[{"release":"Example.Movie.2026.German.1080p.BluRay.x264-GRP","imdbId":"1234567","hasNFO":"no","isForeign":"yes"}]}`,
		},
		details: map[string]string{
			"Example.Movie.2026.German.1080p.BluRay.x264-GRP": `{"archived-files":[{"name":"example.movie.2026.german.1080p.bluray.x264-grp.mkv","size":8000000000}]}`,
		},
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	meta := renamedSceneMeta("/data/Example Movie 2026 German 1080p BluRay x264 GRP.mkv")
	meta.Release.Language = []string{"German"}
	detector := newSRRDBDetector(server.Client(), server.URL, t.TempDir(), t.TempDir())
	result, err := detector.Detect(context.Background(), meta)
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if !result.IsScene {
		t.Fatalf("expected scene match")
	}
	if len(imdbPaths) != 1 {
		t.Fatalf("expected one imdb request, got %d", len(imdbPaths))
	}
	if !strings.Contains(imdbPaths[0], "/v1/search/imdb:tt1234567/foreign:yes/order:date/page:1") {
		t.Fatalf("expected foreign:yes imdb search path")
	}
}

func TestSceneDetectorIMDBFallbackSoftFailsOnError(t *testing.T) {
	handler := srrdbFallbackHandler{imdbStat: http.StatusInternalServerError}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	detector := newSRRDBDetector(server.Client(), server.URL, t.TempDir(), t.TempDir())
	result, err := detector.Detect(context.Background(), renamedSceneMeta("/data/Example Movie 2026 1080p BluRay x264 GRP.mkv"))
	if err != nil {
		t.Fatalf("expected soft-fail (nil error), got %v", err)
	}
	if result.IsScene || result.Renamed {
		t.Fatalf("expected no scene match on srrdb error, got %#v", result)
	}
}

func TestSceneDetectorRSearchSoftFailsOnNetworkError(t *testing.T) {
	// srrdb unreachable on the r: fallback (no imdb) must not block an upload.
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})}
	detector := newSRRDBDetector(client, "https://api.srrdb.com", t.TempDir(), t.TempDir())

	meta := renamedSceneMeta("/data/Example Movie 2026 1080p BluRay x264 GRP.mkv")
	meta.Identity = api.ExternalIdentity{} // force the r: fallback path
	result, err := detector.Detect(context.Background(), meta)
	if err != nil {
		t.Fatalf("expected soft-fail (nil error) on r: network error, got %v", err)
	}
	if result.IsScene || result.Renamed {
		t.Fatalf("expected no scene match on srrdb outage, got %#v", result)
	}
}

func TestSceneDetectorRFallbackFolderFirstWhenNoIMDb(t *testing.T) {
	// No imdb id: the r: fallback tries the canonical folder candidate first.
	const release = "Example.Driver.2026.1080p.BluRay.x264-GRP"
	handler := http.NewServeMux()
	handler.HandleFunc("/v1/search/r:"+release, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resultsCount":1,"results":[{"release":"` + release + `","imdbId":"0012345"}]}`))
	})
	handler.HandleFunc("/v1/details/"+release, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"archived-files":[{"name":"example.driver.2026.1080p.bluray.x264-grp.mkv","size":4695029966}]}`))
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	meta := preparationstate.State{
		SourcePath: "/data/" + release,
		VideoPath:  "/data/" + release + "/example.driver.2026.1080p.bluray.x264-grp.mkv",
	}
	detector := newSRRDBDetector(server.Client(), server.URL, t.TempDir(), t.TempDir())
	result, err := detector.Detect(context.Background(), meta)
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if !result.IsScene || result.SceneName != release {
		t.Fatalf("expected r: folder match %q, got %#v", release, result)
	}
	if result.Renamed {
		t.Fatalf("did not expect rename (media filename matches archived), got reason=%q", result.RenamedReason)
	}
}

func TestSceneDetectorRSearchSoftFailsOnMalformedBody(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("/v1/search/r:Example.Release", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resultsCount":1,"results":[`)) // truncated JSON
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	detector := newSRRDBDetector(server.Client(), server.URL, t.TempDir(), t.TempDir())
	result, err := detector.Detect(context.Background(), preparationstate.State{VideoPath: "/data/Example.Release.mkv"})
	if err != nil {
		t.Fatalf("expected soft-fail (nil error) on malformed r: body, got %v", err)
	}
	if result.IsScene {
		t.Fatalf("expected no scene match on malformed body, got %#v", result)
	}
}

func TestSceneDetectorIMDBFallbackSkippedWithoutIMDbID(t *testing.T) {
	imdbHits := &atomic.Int32{}
	handler := srrdbFallbackHandler{imdbHits: imdbHits}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	detector := newSRRDBDetector(server.Client(), server.URL, t.TempDir(), t.TempDir())
	meta := renamedSceneMeta("/data/Example Movie 2026 1080p BluRay x264 GRP.mkv")
	meta.Identity = api.ExternalIdentity{} // no known id at detect time
	result, err := detector.Detect(context.Background(), meta)
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if result.IsScene {
		t.Fatalf("expected no fallback without an imdb id, got %#v", result)
	}
	if got := imdbHits.Load(); got != 0 {
		t.Fatalf("expected no imdb: request without an imdb id, got %d", got)
	}
}

func TestSceneDetectorFallsBackToRWhenIMDbFindsNoMatch(t *testing.T) {
	// IMDb id is known, but the imdb: search returns only a non-matching release;
	// Detect must fall through to the exact r: folder search rather than give up.
	const release = "Example.Driver.2026.1080p.BluRay.x264-GRP"
	handler := srrdbFallbackHandler{
		imdbPages: map[int]string{
			1: `{"resultsCount":1,"results":[{"release":"Example.Driver.2026.720p.BluRay.x264-ALT","imdbId":"0012345"}]}`,
		},
		rResults: map[string]string{
			release: `{"resultsCount":1,"results":[{"release":"` + release + `","imdbId":"0012345"}]}`,
		},
		details: map[string]string{
			release: `{"archived-files":[{"name":"example.driver.2026.1080p.bluray.x264-grp.mkv","size":4695029966}]}`,
		},
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	meta := preparationstate.State{
		SourcePath: "/data/" + release,
		VideoPath:  "/data/" + release + "/example.driver.2026.1080p.bluray.x264-grp.mkv",
		Identity:   api.ExternalIdentity{IMDBID: 12345},
		Release: api.ReleaseInfo{
			Resolution: "1080p",
			Year:       2026,
			Group:      "GRP",
		},
	}
	detector := newSRRDBDetector(server.Client(), server.URL, t.TempDir(), t.TempDir())
	result, err := detector.Detect(context.Background(), meta)
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if !result.IsScene || result.SceneName != release {
		t.Fatalf("expected r: fallback match %q after imdb miss, got %#v", release, result)
	}
}

func TestSceneDetectorIMDBFallbackNoConfidentCandidate(t *testing.T) {
	// Only wrong-resolution releases exist for the title: no confident match.
	handler := srrdbFallbackHandler{
		imdbPages: map[int]string{
			1: `{"resultsCount":2,"results":[{"release":"Example.Movie.2026.720p.BluRay.x264-GRP","imdbId":"1234567"},{"release":"Example.Movie.2026.480p.DVDRip.x264-GRP","imdbId":"1234567"}]}`,
		},
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	detector := newSRRDBDetector(server.Client(), server.URL, t.TempDir(), t.TempDir())
	result, err := detector.Detect(context.Background(), renamedSceneMeta("/data/Example Movie 2026 1080p BluRay x264 GRP.mkv"))
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if result.IsScene || result.Renamed {
		t.Fatalf("expected no match for a non-matching candidate set, got %#v", result)
	}
}

func TestParseNFOExternalIDsText(t *testing.T) {
	ids := parseNFOExternalIDsText(`URL          : https://www.tvmaze.com/shows/12345/example-show
IMDb         : https://www.imdb.com/title/tt1234567/
TMDB         : https://www.themoviedb.org/tv/765432-example-show
TVDB         : https://thetvdb.com/series/456789
MAL          : https://myanimelist.net/anime/54321/example-anime`)

	if ids.TVmazeID != 12345 {
		t.Fatalf("expected tvmaze id 12345, got %d", ids.TVmazeID)
	}
	if ids.IMDBID != 1234567 {
		t.Fatalf("expected imdb id 1234567, got %d", ids.IMDBID)
	}
	if ids.TMDBID != 765432 {
		t.Fatalf("expected tmdb id 765432, got %d", ids.TMDBID)
	}
	if ids.TVDBID != 456789 {
		t.Fatalf("expected tvdb id 456789, got %d", ids.TVDBID)
	}
	if ids.MALID != 54321 {
		t.Fatalf("expected mal id 54321, got %d", ids.MALID)
	}
	if ids.Service != "" {
		t.Fatalf("expected no service without source field, got %q", ids.Service)
	}
}

func TestParseNFOExternalIDsTextService(t *testing.T) {
	ids := parseNFOExternalIDsText(`Source       : ITUNES
URL          : https://www.imdb.com/title/tt7654321/`)

	if ids.Service != "iT" {
		t.Fatalf("expected iT service, got %q", ids.Service)
	}
	if ids.ServiceLongName == "" {
		t.Fatalf("expected service long name")
	}
}
