// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ar

import (
	"context"
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
			if got := query.Get("searchstr"); got != "Movie Title 2023" {
				t.Fatalf("expected searchstr to include title+year, got %q", got)
			}
			if got := req.Header.Get("User-Agent"); got == "" {
				t.Fatalf("expected User-Agent header")
			}
			if raw := req.Header.Get("Cookie"); !strings.Contains(raw, "session=abc123") {
				t.Fatal("expected cookie header to include session token")
			}

			body := `{"status":"success","response":{"results":[{"groupName":"Movie.Title.2023.1080p.BluRay-GRP","size":123456789,"fileCount":1,"groupId":44,"torrentId":55}]}}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	handler := dupe.NewAdapter(New(), "AR",
		config.Config{
			MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmpDir, "ua.db")},
		}, client, api.NopLogger{})

	meta := api.DuplicateSubject{Release: api.ReleaseInfo{Title: "Movie Title", Year: 2023}}
	entries, notes, err := adapterEvidence(handler.Search(context.Background(), meta))
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
