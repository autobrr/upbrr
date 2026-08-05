// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func adapterEvidence(result dupe.AdapterResult) ([]api.DupeEntry, []string, error) {
	return result.Entries(), result.Notes(), result.Cause()
}

type hdbRoundTripFunc func(*http.Request) (*http.Response, error)

func (f hdbRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type hdbCaptureLogger struct {
	debug []string
}

func (l *hdbCaptureLogger) Tracef(string, ...any) {}
func (l *hdbCaptureLogger) Infof(string, ...any)  {}
func (l *hdbCaptureLogger) Warnf(string, ...any)  {}
func (l *hdbCaptureLogger) Errorf(string, ...any) {}
func (l *hdbCaptureLogger) Debugf(format string, args ...any) {
	l.debug = append(l.debug, fmt.Sprintf(format, args...))
}

func hdbTestInt(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}

func hdbTestString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func hdbTestPageBody(t *testing.T, startID int, count int) io.ReadCloser {
	t.Helper()
	items := make([]map[string]any, 0, count)
	for offset := range count {
		id := startID + offset
		name := fmt.Sprintf("Example.Release.%03d.1080p-GRP", id)
		items = append(items, map[string]any{
			"id":       id,
			"name":     name,
			"filename": name + ".torrent",
			"size":     1000 + id,
			"numfiles": 1,
		})
	}
	body, err := json.Marshal(map[string]any{"status": 0, "data": items})
	if err != nil {
		t.Fatalf("marshal HDB response fixture: %v", err)
	}
	return io.NopCloser(strings.NewReader(string(body)))
}

func TestHDBHandlerSearchBuildsPayloadAndParsesResults(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	logger := &hdbCaptureLogger{}

	client := &http.Client{
		Transport: hdbRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodPost {
				t.Fatalf("expected POST request, got %s", req.Method)
			}
			if req.URL.String() != "https://hdbits.org/api/torrents" {
				t.Fatalf("unexpected endpoint %q", req.URL.String())
			}

			var payload map[string]any
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request payload: %v", err)
			}
			if got := hdbTestString(payload["username"]); got != "user" {
				t.Fatalf("unexpected username %q", got)
			}
			if got := hdbTestString(payload["passkey"]); got != "pk" {
				t.Fatalf("unexpected passkey %q", got)
			}
			category, ok := payload["category"].([]any)
			if !ok || len(category) != 1 || hdbTestInt(category[0]) != 1 {
				t.Fatal("unexpected category filter")
			}
			if _, hasCodec := payload["codec"]; hasCodec {
				t.Fatal("did not expect codec filter")
			}
			if _, hasMedium := payload["medium"]; hasMedium {
				t.Fatal("did not expect medium filter")
			}
			imdb, ok := payload["imdb"].(map[string]any)
			if !ok || hdbTestString(imdb["id"]) != "1234567" {
				t.Fatalf("unexpected imdb payload %#v", payload["imdb"])
			}
			if _, hasTVDB := payload["tvdb"]; hasTVDB {
				t.Fatalf("did not expect tvdb payload when imdb is present")
			}
			if got := hdbTestInt(payload["limit"]); got != hdbDupePageLimit {
				t.Fatalf("unexpected limit %d", got)
			}
			if got := hdbTestInt(payload["page"]); got != 0 {
				t.Fatalf("unexpected page %d", got)
			}

			body := `{"status":0,"data":[{"id":42,"name":"Movie.Title.2024.1080p.WEB-DL.DDP5.1.H.265-GRP","filename":"Movie Title (2024).torrent","size":1234567890,"numfiles":3}]}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	handler := dupe.NewAdapter(New(), "HDB",
		config.Config{
			MainSettings: config.MainSettingsConfig{
				DBPath: filepath.Join(tmpDir, "ua.db"),
			},
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"HDB": {
						Username: "user",
						Passkey:  "pk",
					},
				},
			},
		}, client, logger)

	meta := api.DuplicateSubject{
		SourcePath: "C:/media/movie",
		Identity: api.ExternalIdentity{
			IMDBID:   1234567,
			TVDBID:   765432,
			Category: "MOVIE",
		},
		VideoCodec: "HEVC",
		Type:       "WEBDL",
	}

	result := handler.Search(context.Background(), meta)
	entries, notes, err := adapterEvidence(result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected no notes, got %#v", notes)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %d", len(entries))
	}

	entry := entries[0]
	if entry.Name != "Movie.Title.2024.1080p.WEB-DL.DDP5.1.H.265-GRP" {
		t.Fatalf("expected entry name from 'name', got %q", entry.Name)
	}
	if entry.ID != "42" {
		t.Fatalf("unexpected id %q", entry.ID)
	}
	if entry.Link != "https://hdbits.org/details.php?id=42" {
		t.Fatalf("unexpected link %q", entry.Link)
	}
	if entry.Download != "https://hdbits.org/download.php/Movie+Title+%282024%29.torrent?id=42&passkey=pk" {
		t.Fatalf("unexpected download %q", entry.Download)
	}
	if !entry.SizeKnown || entry.SizeBytes != 1234567890 {
		t.Fatalf("unexpected size known=%t size=%d", entry.SizeKnown, entry.SizeBytes)
	}
	if entry.FileCount != 3 {
		t.Fatalf("unexpected file count %d", entry.FileCount)
	}
	logs := strings.Join(logger.debug, "\n")
	if strings.Contains(logs, `"username":"user"`) || strings.Contains(logs, `"passkey":"pk"`) {
		t.Fatal("HDB request log exposed credentials")
	}
	if !strings.Contains(logs, `"username":"[REDACTED]"`) || !strings.Contains(logs, `"passkey":"[REDACTED]"`) {
		t.Fatal("HDB request log did not preserve redaction markers")
	}
	search := result.SearchEvidence()
	if !search.Complete || search.Pages != 1 || search.Scope != "work_identity" {
		t.Fatalf("unexpected search evidence %#v", search)
	}
}

func TestHDBHandlerSearchPaginatesFullPages(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	requestedPages := make([]int64, 0, 2)

	client := &http.Client{
		Transport: hdbRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			var payload map[string]any
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request payload: %v", err)
			}
			if got := hdbTestInt(payload["limit"]); got != hdbDupePageLimit {
				t.Fatalf("unexpected limit %d", got)
			}
			page := hdbTestInt(payload["page"])
			requestedPages = append(requestedPages, page)

			var body io.ReadCloser
			switch page {
			case 0:
				body = hdbTestPageBody(t, 1, hdbDupePageLimit)
			case 1:
				body = hdbTestPageBody(t, hdbDupePageLimit+1, 1)
			default:
				t.Fatalf("unexpected page %d", page)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       body,
				Header:     make(http.Header),
			}, nil
		}),
	}

	handler := dupe.NewAdapter(New(), "HDB",
		config.Config{
			MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmpDir, "ua.db")},
			Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
				"HDB": {Username: "user", Passkey: "pk"},
			}},
		}, client, api.NopLogger{})

	result := handler.Search(context.Background(), api.DuplicateSubject{
		SourcePath: "C:/media/movie",
		Identity: api.ExternalIdentity{
			IMDBID:   1234567,
			Category: "MOVIE",
		},
	})
	entries, notes, err := adapterEvidence(result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected no notes, got %#v", notes)
	}
	if len(entries) != hdbDupePageLimit+1 || entries[0].ID != "1" || entries[hdbDupePageLimit].ID != "101" {
		t.Fatal("unexpected paginated entries")
	}
	if len(requestedPages) != 2 || requestedPages[0] != 0 || requestedPages[1] != 1 {
		t.Fatalf("unexpected requested pages %v", requestedPages)
	}
	search := result.SearchEvidence()
	if !search.Complete || search.Pages != 2 || search.Scope != "work_identity" {
		t.Fatalf("unexpected search evidence %#v", search)
	}
}

func TestHDBHandlerSearchStopsAtPageBound(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	requests := 0
	client := &http.Client{
		Transport: hdbRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
			requests++
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       hdbTestPageBody(t, 1, hdbDupePageLimit),
				Header:     make(http.Header),
			}, nil
		}),
	}
	handler := &dupeSearcher{
		cfg: config.Config{
			MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmpDir, "ua.db")},
			Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
				"HDB": {Username: "user", Passkey: "pk"},
			}},
		},
		http:     client,
		logger:   api.NopLogger{},
		endpoint: "https://hdbits.org/api/torrents",
		maxPages: 1,
	}

	result := handler.Search(context.Background(), api.DuplicateSubject{
		SourcePath:  "C:/media/movie",
		ReleaseName: "Example.Release.2026.1080p-GRP",
	})
	entries, notes, err := adapterEvidence(result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if requests != 1 || len(entries) != hdbDupePageLimit {
		t.Fatal("unexpected bounded search result")
	}
	search := result.SearchEvidence()
	if search.Complete || search.Pages != 1 || len(search.Warnings) != 1 || len(notes) != 1 {
		t.Fatalf("unexpected incomplete search evidence %#v notes=%#v", search, notes)
	}
}

func TestHDBHandlerSearchFallsBackToTextSearchWhenIDsMissing(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	client := &http.Client{
		Transport: hdbRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			var payload map[string]any
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request payload: %v", err)
			}
			if _, hasIMDB := payload["imdb"]; hasIMDB {
				t.Fatalf("did not expect imdb in payload")
			}
			if _, hasTVDB := payload["tvdb"]; hasTVDB {
				t.Fatalf("did not expect tvdb in payload")
			}
			if got := hdbTestString(payload["search"]); got != "Some.Release.Name.2024.1080p.WEB-DL" {
				t.Fatalf("unexpected fallback search %q", got)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"status":0,"data":[]}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	handler := dupe.NewAdapter(New(), "HDB",
		config.Config{
			MainSettings: config.MainSettingsConfig{
				DBPath: filepath.Join(tmpDir, "ua.db"),
			},
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"HDB": {
						Username: "user",
						Passkey:  "pk",
					},
				},
			},
		}, client, api.NopLogger{})

	meta := api.DuplicateSubject{
		SourcePath:  "C:/media/no-ids",
		ReleaseName: "Some.Release.Name.2024.1080p.WEB-DL",
	}

	entries, notes, err := adapterEvidence(handler.Search(context.Background(), meta))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected no notes, got %#v", notes)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no entries, got %d", len(entries))
	}
}

func TestHDBHandlerSearchUsesTVDBWhenIMDbMissing(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	client := &http.Client{
		Transport: hdbRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			var payload map[string]any
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request payload: %v", err)
			}

			if _, hasIMDB := payload["imdb"]; hasIMDB {
				t.Fatalf("did not expect imdb in payload")
			}
			tvdb, ok := payload["tvdb"].(map[string]any)
			if !ok || hdbTestInt(tvdb["id"]) != 765432 {
				t.Fatalf("unexpected tvdb payload %#v", payload["tvdb"])
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"status":0,"data":[]}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	handler := dupe.NewAdapter(New(), "HDB",
		config.Config{
			MainSettings: config.MainSettingsConfig{
				DBPath: filepath.Join(tmpDir, "ua.db"),
			},
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"HDB": {
						Username: "user",
						Passkey:  "pk",
					},
				},
			},
		}, client, api.NopLogger{})

	meta := api.DuplicateSubject{
		SourcePath: "C:/media/show",
		Identity: api.ExternalIdentity{
			TVDBID:   765432,
			Category: "TV",
		},
	}

	entries, notes, err := adapterEvidence(handler.Search(context.Background(), meta))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected no notes, got %#v", notes)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no entries, got %d", len(entries))
	}
}

func TestHDBHandlerSearchSkipsTVDBForMovie(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	client := &http.Client{
		Transport: hdbRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			var payload map[string]any
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request payload: %v", err)
			}
			if _, hasTVDB := payload["tvdb"]; hasTVDB {
				t.Fatalf("did not expect tvdb in movie payload")
			}
			if got := hdbTestString(payload["search"]); got != "Movie.Release.2024.1080p.WEB-DL" {
				t.Fatalf("unexpected fallback search %q", got)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"status":0,"data":[]}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	handler := dupe.NewAdapter(New(), "HDB",
		config.Config{
			MainSettings: config.MainSettingsConfig{
				DBPath: filepath.Join(tmpDir, "ua.db"),
			},
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"HDB": {
						Username: "user",
						Passkey:  "pk",
					},
				},
			},
		}, client, api.NopLogger{})

	meta := api.DuplicateSubject{
		SourcePath:  "C:/media/movie",
		ReleaseName: "Movie.Release.2024.1080p.WEB-DL",
		Identity: api.ExternalIdentity{
			TVDBID:   765432,
			Category: "MOVIE",
		},
	}

	entries, notes, err := adapterEvidence(handler.Search(context.Background(), meta))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected no notes, got %#v", notes)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no entries, got %d", len(entries))
	}
}

func TestHDBHandlerSearchOmitsUnknownCategory(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	client := &http.Client{
		Transport: hdbRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			var payload map[string]any
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request payload: %v", err)
			}

			if _, hasCategory := payload["category"]; hasCategory {
				t.Fatal("did not expect unknown category filter")
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"status":0,"data":[]}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	handler := dupe.NewAdapter(New(), "HDB",
		config.Config{
			MainSettings: config.MainSettingsConfig{
				DBPath: filepath.Join(tmpDir, "ua.db"),
			},
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"HDB": {
						Username: "user",
						Passkey:  "pk",
					},
				},
			},
		}, client, api.NopLogger{})

	meta := api.DuplicateSubject{
		SourcePath:  "C:/media/zero-filters",
		ReleaseName: "Unknown.Release.2024",
	}

	entries, notes, err := adapterEvidence(handler.Search(context.Background(), meta))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected no notes, got %#v", notes)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no entries, got %d", len(entries))
	}
}
