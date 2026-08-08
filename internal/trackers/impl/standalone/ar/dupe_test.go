// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ar

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
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
			if got := query.Get("searchstr"); got != "Example Release 2026" {
				t.Fatalf("expected broad title/year searchstr, got %q", got)
			}
			if got := req.Header.Get("User-Agent"); got == "" {
				t.Fatalf("expected User-Agent header")
			}
			if raw := req.Header.Get("Cookie"); !strings.Contains(raw, "session=abc123") {
				t.Fatal("expected cookie header to include session token")
			}

			fixture := "browse_page_1.json"
			if req.URL.Query().Get("page") == "2" {
				fixture = "browse_page_2.json"
			}
			body, err := os.ReadFile(filepath.Join("testdata", fixture))
			if err != nil {
				t.Fatalf("read AR fixture: %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(string(body))),
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
		Release: api.ReleaseInfo{Title: "Example Release", Year: 2026},
		Projection: &api.TrackerReleaseProjection{
			DuplicateCriteria: api.TrackerDuplicateCriteria{Name: "Example.Release.2026.1080p-GRP"},
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
	if len(entries) != 3 {
		t.Fatalf("expected three entries, got %d", len(entries))
	}
	entry := entries[0]
	if entry.Name != "Example.Release.2026.1080p.BluRay.x264-GRP" {
		t.Fatalf("unexpected name %q", entry.Name)
	}
	if entry.FileCount != 1 {
		t.Fatalf("expected file_count=1, got %d", entry.FileCount)
	}
	if !entry.SizeKnown || entry.SizeBytes != 123456789 {
		t.Fatalf("unexpected size known=%t size=%d", entry.SizeKnown, entry.SizeBytes)
	}
	if len(entry.Files) != 0 {
		t.Fatalf("expected no fabricated file list, got %#v", entry.Files)
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
	if !search.Complete || search.WorkScope != dupe.WorkScopeTitle || search.EffectiveComplete() ||
		search.Pages != 2 || search.Scope != "title_year" || len(search.Warnings) != 0 {
		t.Fatalf("unexpected search evidence: %#v", search)
	}
	logs := strings.Join(logger.debug, "\n")
	if !strings.Contains(logs, `AR search request method=GET action=browse searchstr="Example Release 2026"`) {
		t.Fatalf("missing safe AR request diagnostics: %q", logs)
	}
	if !strings.Contains(
		logs,
		`AR search response pages=2 advertised_pages=2 accepted_results=3 complete=true decision=completed`,
	) {
		t.Fatalf("missing safe AR response diagnostics: %q", logs)
	}
}

func TestARSearchQueryPrefersProviderTitleOverProjectedRelease(t *testing.T) {
	t.Parallel()

	got := arSearchQuery(api.DuplicateSubject{
		Release: api.ReleaseInfo{Title: "EXAMPLE DISC EDITION", Year: 2026},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{Title: "Example Release", Year: 2026},
		},
		Projection: &api.TrackerReleaseProjection{
			DuplicateCriteria: api.TrackerDuplicateCriteria{Name: "Example.Release.2026.1080p-GRP"},
		},
	})
	if got != "Example Release 2026" {
		t.Fatalf("fallback query = %q", got)
	}
}

func TestARPaginationFailuresRetainSafePartialCandidates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		maxPages    int
		secondPage  string
		secondError bool
		wantPages   int
		wantWarning string
	}{
		{
			name:        "bound",
			maxPages:    1,
			wantPages:   1,
			wantWarning: "AR search reached page bound before consuming advertised pages",
		},
		{
			name:        "inconsistent page number",
			maxPages:    3,
			secondPage:  `{"status":"success","response":{"currentPage":1,"pages":2,"results":[]}}`,
			wantPages:   2,
			wantWarning: "AR search pagination evidence is inconsistent",
		},
		{
			name:        "repeated page",
			maxPages:    3,
			secondPage:  `{"status":"success","response":{"currentPage":2,"pages":2,"results":[{"groupName":"Example.Release.2026.1080p.BluRay.x264-GRP","size":1,"fileCount":1,"groupId":44,"torrentId":55}]}}`,
			wantPages:   2,
			wantWarning: "AR search repeated a result page",
		},
		{
			name:        "partial request failure",
			maxPages:    3,
			secondError: true,
			wantPages:   1,
			wantWarning: "AR search stopped after a partial request failure",
		},
		{
			name:        "malformed result",
			maxPages:    3,
			secondPage:  `{"status":"success","response":{"currentPage":2,"pages":2,"results":[{"groupName":"Example.Release.2026.1080p.BluRay.x264-GRP","size":1,"fileCount":1,"groupId":44,"torrentId":0}]}}`,
			wantPages:   2,
			wantWarning: "AR search result evidence is malformed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			client := arClientWithCookie(t, arRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				body := `{"status":"success","response":{"currentPage":1,"pages":2,"results":[{"groupName":"Example.Release.2026.1080p.BluRay.x264-GRP","size":1,"fileCount":1,"groupId":44,"torrentId":55}]}}`
				if req.URL.Query().Get("page") == "2" {
					if test.secondError {
						return nil, errors.New("synthetic request failure")
					}
					body = test.secondPage
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(body)),
					Header:     make(http.Header),
				}, nil
			}))
			searcher := dupeSearcher{
				http:     client,
				logger:   api.NopLogger{},
				maxPages: test.maxPages,
			}
			result := searcher.Search(context.Background(), api.DuplicateSubject{
				Projection: &api.TrackerReleaseProjection{
					DuplicateCriteria: api.TrackerDuplicateCriteria{Name: "Example Release 2026"},
				},
			})
			search := result.SearchEvidence()
			if result.Disposition() != dupe.DispositionResolved || search.Complete || search.Pages != test.wantPages ||
				len(search.Warnings) != 1 || search.Warnings[0] != test.wantWarning || len(result.Entries()) != 1 {
				t.Fatalf("AR partial result=%#v search=%#v entries=%#v", result, search, result.Entries())
			}
		})
	}
}

func TestAREmptyResultSetIsComplete(t *testing.T) {
	t.Parallel()

	client := arClientWithCookie(t, arRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"status":"success","response":{"currentPage":0,"pages":0,"results":[]}}`)),
			Header:     make(http.Header),
		}, nil
	}))
	result := (dupeSearcher{
		http:     client,
		logger:   api.NopLogger{},
		maxPages: 2,
	}).Search(
		context.Background(),
		api.DuplicateSubject{Projection: &api.TrackerReleaseProjection{
			DuplicateCriteria: api.TrackerDuplicateCriteria{Name: "Example Release 2026"},
		}},
	)
	search := result.SearchEvidence()
	if !search.Complete || search.Pages != 1 || search.Scope != "title_year" || len(search.Warnings) != 0 || len(result.Entries()) != 0 {
		t.Fatalf("AR empty result search=%#v entries=%#v", search, result.Entries())
	}
}

func arClientWithCookie(t *testing.T, transport http.RoundTripper) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	trackerURL, err := url.Parse("https://alpharatio.cc/")
	if err != nil {
		t.Fatalf("parse tracker URL: %v", err)
	}
	jar.SetCookies(trackerURL, []*http.Cookie{{Name: "session", Value: "synthetic"}})
	return &http.Client{Transport: transport, Jar: jar}
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
