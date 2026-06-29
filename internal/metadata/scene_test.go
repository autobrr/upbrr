// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
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

	meta := api.PreparedMetadata{VideoPath: "/data/Example.Release.mkv"}
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

	meta := api.PreparedMetadata{VideoPath: "/data/Example.Release.mkv"}
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

	result, err := detector.Detect(context.Background(), api.PreparedMetadata{VideoPath: "/data/Example.Release.mkv"})
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

	result, err := detector.Detect(context.Background(), api.PreparedMetadata{VideoPath: "/data/Example.Release.mkv"})
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

	result, err := detector.Detect(context.Background(), api.PreparedMetadata{VideoPath: "/data/Example.Release.mkv"})
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

	result, err := detector.Detect(context.Background(), api.PreparedMetadata{VideoPath: "/data/Example.Release.mkv"})
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

	result, err := detector.Detect(context.Background(), api.PreparedMetadata{VideoPath: "/data/Example.Release.mkv"})
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

	result, err := detector.Detect(context.Background(), api.PreparedMetadata{VideoPath: "/data/Example.Release.mkv"})
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

	result, err := detector.Detect(ctx, api.PreparedMetadata{VideoPath: "/data/Example.Release.mkv"})
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
	imdbPages map[int]string // page -> JSON body for /v1/search/imdb:<id>/...
	details   map[string]string
	rEmpty    bool // r: search returns an empty result set (forces the fallback)
	imdbStat  int  // non-zero overrides the imdb: search status code
}

func (h srrdbFallbackHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Path
	switch {
	case strings.Contains(path, "/v1/search/r:"):
		if h.rEmpty {
			_, _ = w.Write([]byte(`{"resultsCount":0,"results":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"resultsCount":0,"results":[]}`))
	case strings.Contains(path, "/v1/search/imdb:"):
		if h.imdbStat != 0 {
			w.WriteHeader(h.imdbStat)
			return
		}
		page := 1
		if idx := strings.Index(path, "page:"); idx >= 0 {
			if p, err := strconv.Atoi(path[idx+len("page:"):]); err == nil {
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

func renamedSceneMeta(videoPath string) api.PreparedMetadata {
	return api.PreparedMetadata{
		VideoPath:   videoPath,
		ExternalIDs: api.ExternalIDs{IMDBID: 111161},
		Release: api.ReleaseInfo{
			Resolution: "1080p",
			Year:       2014,
			Group:      "GRP",
			Source:     "BluRay",
			Codec:      []string{"x264"},
		},
	}
}

func TestSceneDetectorIMDBFallbackDetectsRename(t *testing.T) {
	handler := srrdbFallbackHandler{
		rEmpty: true,
		imdbPages: map[int]string{
			1: `{"resultsCount":1,"results":[{"release":"Fury.2014.1080p.BluRay.x264-GRP","imdbId":"111161","hasNFO":"no","isForeign":"no"}]}`,
		},
		details: map[string]string{
			"Fury.2014.1080p.BluRay.x264-GRP": `{"files":[],"archived-files":[{"name":"fury.2014.1080p.bluray.x264-grp.mkv","crc":"AABBCCDD","size":8000000000}]}`,
		},
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	detector := newSRRDBDetector(server.Client(), server.URL, t.TempDir(), t.TempDir())
	result, err := detector.Detect(context.Background(), renamedSceneMeta("/data/Fury 2014 1080p BluRay x264 GRP.mkv"))
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if !result.IsScene {
		t.Fatalf("expected scene via imdb fallback")
	}
	if !result.Renamed {
		t.Fatalf("expected renamed verdict")
	}
	if result.SceneName != "Fury.2014.1080p.BluRay.x264-GRP" {
		t.Fatalf("unexpected scene name: %q", result.SceneName)
	}
	if result.IMDBID != 111161 {
		t.Fatalf("unexpected imdb id: %d", result.IMDBID)
	}
	if strings.TrimSpace(result.RenamedReason) == "" {
		t.Fatalf("expected a rename reason")
	}
}

func TestSceneDetectorIMDBFallbackUnmodifiedNotRenamed(t *testing.T) {
	handler := srrdbFallbackHandler{
		rEmpty: true,
		imdbPages: map[int]string{
			1: `{"resultsCount":1,"results":[{"release":"Fury.2014.1080p.BluRay.x264-GRP","imdbId":"111161","hasNFO":"no"}]}`,
		},
		details: map[string]string{
			"Fury.2014.1080p.BluRay.x264-GRP": `{"archived-files":[{"name":"Fury.2014.1080p.BluRay.x264-GRP.mkv","size":8000000000}]}`,
		},
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	detector := newSRRDBDetector(server.Client(), server.URL, t.TempDir(), t.TempDir())
	// On-disk name equals the canonical media basename (case aside), so although
	// the r: search missed, this must not be flagged as renamed.
	result, err := detector.Detect(context.Background(), renamedSceneMeta("/data/fury.2014.1080p.bluray.x264-grp.mkv"))
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

func TestSceneDetectorIMDBFallbackPaginates(t *testing.T) {
	// Page 1 is full (40 wrong-resolution entries); the match is only on page 2.
	var page1 strings.Builder
	page1.WriteString(`{"resultsCount":41,"results":[`)
	for i := 0; i < 40; i++ {
		if i > 0 {
			page1.WriteString(",")
		}
		page1.WriteString(`{"release":"Fury.2014.720p.BluRay.x264-GRP","imdbId":"111161"}`)
	}
	page1.WriteString(`]}`)

	handler := srrdbFallbackHandler{
		rEmpty: true,
		imdbPages: map[int]string{
			1: page1.String(),
			2: `{"resultsCount":41,"results":[{"release":"Fury.2014.1080p.BluRay.x264-GRP","imdbId":"111161"}]}`,
		},
		details: map[string]string{
			"Fury.2014.1080p.BluRay.x264-GRP": `{"archived-files":[{"name":"fury.2014.1080p.bluray.x264-grp.mkv","size":8000000000}]}`,
		},
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	detector := newSRRDBDetector(server.Client(), server.URL, t.TempDir(), t.TempDir())
	result, err := detector.Detect(context.Background(), renamedSceneMeta("/data/Fury 2014 1080p BluRay x264 GRP.mkv"))
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if !result.IsScene || result.SceneName != "Fury.2014.1080p.BluRay.x264-GRP" {
		t.Fatalf("expected paginated match, got %#v", result)
	}
}

func TestSceneDetectorIMDBFallbackSoftFailsOnError(t *testing.T) {
	handler := srrdbFallbackHandler{rEmpty: true, imdbStat: http.StatusInternalServerError}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	detector := newSRRDBDetector(server.Client(), server.URL, t.TempDir(), t.TempDir())
	result, err := detector.Detect(context.Background(), renamedSceneMeta("/data/Fury 2014 1080p BluRay x264 GRP.mkv"))
	if err != nil {
		t.Fatalf("expected soft-fail (nil error), got %v", err)
	}
	if result.IsScene || result.Renamed {
		t.Fatalf("expected no scene match on srrdb error, got %#v", result)
	}
}

func TestSceneDetectorRSearchSoftFailsOnNetworkError(t *testing.T) {
	// srrdb unreachable on the initial r: search must not block an upload.
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})}
	detector := newSRRDBDetector(client, "https://api.srrdb.com", t.TempDir(), t.TempDir())

	result, err := detector.Detect(context.Background(), renamedSceneMeta("/data/Fury 2014 1080p BluRay x264 GRP.mkv"))
	if err != nil {
		t.Fatalf("expected soft-fail (nil error) on r: network error, got %v", err)
	}
	if result.IsScene || result.Renamed {
		t.Fatalf("expected no scene match on srrdb outage, got %#v", result)
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
	result, err := detector.Detect(context.Background(), api.PreparedMetadata{VideoPath: "/data/Example.Release.mkv"})
	if err != nil {
		t.Fatalf("expected soft-fail (nil error) on malformed r: body, got %v", err)
	}
	if result.IsScene {
		t.Fatalf("expected no scene match on malformed body, got %#v", result)
	}
}

func TestSceneDetectorIMDBFallbackSkippedWithoutIMDbID(t *testing.T) {
	handler := srrdbFallbackHandler{rEmpty: true}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	detector := newSRRDBDetector(server.Client(), server.URL, t.TempDir(), t.TempDir())
	meta := renamedSceneMeta("/data/Fury 2014 1080p BluRay x264 GRP.mkv")
	meta.ExternalIDs = api.ExternalIDs{} // no known id at detect time
	result, err := detector.Detect(context.Background(), meta)
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if result.IsScene {
		t.Fatalf("expected no fallback without an imdb id, got %#v", result)
	}
}

func TestSceneDetectorIMDBFallbackNoConfidentCandidate(t *testing.T) {
	// Only wrong-resolution releases exist for the title: no confident match.
	handler := srrdbFallbackHandler{
		rEmpty: true,
		imdbPages: map[int]string{
			1: `{"resultsCount":2,"results":[{"release":"Fury.2014.720p.BluRay.x264-GRP","imdbId":"111161"},{"release":"Fury.2014.480p.DVDRip.x264-GRP","imdbId":"111161"}]}`,
		},
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	detector := newSRRDBDetector(server.Client(), server.URL, t.TempDir(), t.TempDir())
	result, err := detector.Detect(context.Background(), renamedSceneMeta("/data/Fury 2014 1080p BluRay x264 GRP.mkv"))
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if result.IsScene || result.Renamed {
		t.Fatalf("expected no match for a non-matching candidate set, got %#v", result)
	}
}

func TestParseNFOExternalIDsText(t *testing.T) {
	ids := parseNFOExternalIDsText(`URL          : https://www.tvmaze.com/shows/54723/from
IMDb         : https://www.imdb.com/title/tt9813792/
TMDB         : https://www.themoviedb.org/tv/124364-from
TVDB         : https://thetvdb.com/series/401003
MAL          : https://myanimelist.net/anime/5114/fullmetal-alchemist-brotherhood`)

	if ids.TVmazeID != 54723 {
		t.Fatalf("expected tvmaze id 54723, got %d", ids.TVmazeID)
	}
	if ids.IMDBID != 9813792 {
		t.Fatalf("expected imdb id 9813792, got %d", ids.IMDBID)
	}
	if ids.TMDBID != 124364 {
		t.Fatalf("expected tmdb id 124364, got %d", ids.TMDBID)
	}
	if ids.TVDBID != 401003 {
		t.Fatalf("expected tvdb id 401003, got %d", ids.TVDBID)
	}
	if ids.MALID != 5114 {
		t.Fatalf("expected mal id 5114, got %d", ids.MALID)
	}
	if ids.Service != "" {
		t.Fatalf("expected no service without source field, got %q", ids.Service)
	}
}

func TestParseNFOExternalIDsTextService(t *testing.T) {
	ids := parseNFOExternalIDsText(`Source       : ITUNES
URL          : https://www.imdb.com/title/tt14850054/`)

	if ids.Service != "iT" {
		t.Fatalf("expected iT service, got %q", ids.Service)
	}
	if ids.ServiceLongName == "" {
		t.Fatalf("expected service long name")
	}
}
