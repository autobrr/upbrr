// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ar

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func adapterEvidence(result dupe.AdapterResult) ([]api.DupeEntry, []string, error) {
	return result.Entries(), result.Notes(), result.Cause()
}

type arRoundTripFunc func(*http.Request) (*http.Response, error)

func (f arRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type dupeCaptureLogger struct {
	debug []string
}

func (l *dupeCaptureLogger) Tracef(string, ...any) {}
func (l *dupeCaptureLogger) Infof(string, ...any)  {}
func (l *dupeCaptureLogger) Warnf(string, ...any)  {}
func (l *dupeCaptureLogger) Errorf(string, ...any) {}
func (l *dupeCaptureLogger) Debugf(format string, args ...any) {
	l.debug = append(l.debug, fmt.Sprintf(format, args...))
}

func TestARHandlerSearchParsesResultsWithCookieFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cookieDir := filepath.Join(tmpDir, "cookies")
	if err := os.MkdirAll(cookieDir, 0o755); err != nil {
		t.Fatalf("mkdir cookies: %v", err)
	}
	cookiePath := filepath.Join(cookieDir, "AR.txt")
	cookieText := `# Netscape HTTP Cookie File
.alpharatio.cc	TRUE	/	TRUE	2147483647	session	abc123
`
	if err := os.WriteFile(cookiePath, []byte(cookieText), 0o644); err != nil {
		t.Fatalf("write cookies: %v", err)
	}

	client := &http.Client{
		Transport: arRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodGet {
				t.Fatalf("expected GET request, got %s", req.Method)
			}
			if got := req.URL.String(); !strings.HasPrefix(got, "https://alpharatio.cc/ajax.php?") {
				t.Fatalf("unexpected request url %q", got)
			}
			query := req.URL.Query()
			if got := query.Get("action"); got != "browse" {
				t.Fatalf("expected action=browse, got %q", got)
			}
			if got := query.Get("searchstr"); got != "Exact Projected Query" {
				t.Fatalf("expected exact projected searchstr, got %q", got)
			}
			if got := req.Header.Get("User-Agent"); got == "" {
				t.Fatalf("expected User-Agent header")
			}
			if raw := req.Header.Get("Cookie"); !strings.Contains(raw, "session=abc123") {
				t.Fatal("expected cookie header to include session token")
			}

			body := `{"status":"success","response":{"currentPage":1,"pages":1,"results":[{"groupName":"Movie.Title.2023.1080p.BluRay-GRP","size":123456789,"fileCount":1,"groupId":44,"torrentId":55}]}}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
			}, nil
		}),
	}
	logger := &dupeCaptureLogger{}

	handler := dupe.NewAdapter(New(), "AR",
		config.Config{
			MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmpDir, "ua.db")},
		}, client, logger)

	meta := api.DuplicateSubject{
		Release: api.ReleaseInfo{Title: "Movie Title", Year: 2023},
		Projection: &api.TrackerReleaseProjection{
			DuplicateCriteria: api.TrackerDuplicateCriteria{Name: "Exact Projected Query"},
		},
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
	if entry.Name != "Movie.Title.2023.1080p.BluRay-GRP" {
		t.Fatalf("unexpected name %q", entry.Name)
	}
	if entry.FileCount != 1 {
		t.Fatalf("expected file_count=1, got %d", entry.FileCount)
	}
	if !entry.SizeKnown || entry.SizeBytes != 123456789 {
		t.Fatalf("unexpected size known=%t size=%d", entry.SizeKnown, entry.SizeBytes)
	}
	if len(entry.Files) != 1 || entry.Files[0] != entry.Name {
		t.Fatalf("expected files to contain group name, got %#v", entry.Files)
	}
	if entry.ID != "55" {
		t.Fatalf("expected ID=55, got %q", entry.ID)
	}
	if entry.Link != "https://alpharatio.cc/torrents.php?id=44&torrentid=55" {
		t.Fatalf("unexpected link %q", entry.Link)
	}
	if entry.Download != "https://alpharatio.cc/torrents.php?action=download&id=55" {
		t.Fatalf("unexpected download %q", entry.Download)
	}
	search := result.SearchEvidence()
	if !search.Complete || search.Pages != 1 || search.Scope != "work_identity" || len(search.Warnings) != 0 {
		t.Fatalf("unexpected search evidence: %#v", search)
	}
	logs := strings.Join(logger.debug, "\n")
	if !strings.Contains(logs, `AR search request method=GET action=browse searchstr="Exact Projected Query"`) {
		t.Fatalf("missing safe AR request diagnostics: %q", logs)
	}
	if !strings.Contains(
		logs,
		`AR search response status_code=200 content_type="application/json" api_status=success current_page=1 pages=1 raw_results=1 accepted_results=1 complete=true decision=completed`,
	) {
		t.Fatalf("missing safe AR response diagnostics: %q", logs)
	}
}

func TestARSearchEvidenceTreatsEmptyResultSetAsComplete(t *testing.T) {
	t.Parallel()

	search := arSearchEvidence(0, 0, 0)
	if !search.Complete || search.Pages != 1 || search.Scope != "work_identity" || len(search.Warnings) != 0 {
		t.Fatalf("unexpected empty search evidence: %#v", search)
	}
}

func TestARSearchEvidenceRequiresReviewForAdditionalPages(t *testing.T) {
	t.Parallel()

	search := arSearchEvidence(1, 2, 2)
	if search.Complete || search.Pages != 1 || search.Scope != "work_identity" || len(search.Warnings) != 1 {
		t.Fatalf("unexpected paginated search evidence: %#v", search)
	}
}

func TestARSearchQueryDirectFallbackPrefersProviderTitle(t *testing.T) {
	t.Parallel()

	got := arSearchQuery(api.DuplicateSubject{
		Release: api.ReleaseInfo{Title: "EXAMPLE DISC EDITION", Year: 2026},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{Title: "Example Release", Year: 2026},
		},
	})
	if got != "Example Release 2026" {
		t.Fatalf("fallback query = %q", got)
	}
}

func TestARHandlerMissingCookieFileReturnsSkipNote(t *testing.T) {
	t.Parallel()

	handler := dupe.NewAdapter(New(), "AR",
		config.Config{
			MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "ua.db")},
		}, &http.Client{}, api.NopLogger{})
	meta := api.DuplicateSubject{Release: api.ReleaseInfo{Title: "Movie Title", Year: 2023}}

	result := handler.Search(context.Background(), meta)
	if result.Disposition() != dupe.DispositionNotRun || result.Code() != dupe.NotRunMissingCredentials {
		t.Fatalf("unexpected result disposition=%v code=%q", result.Disposition(), result.Code())
	}
}
