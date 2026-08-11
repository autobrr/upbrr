// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hds

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	xhtml "golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

type hdsRoundTripFunc func(*http.Request) (*http.Response, error)

func (f hdsRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestHDSSearchRequiresExpectedResultStructure(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	cookieDir := filepath.Join(tmp, "cookies")
	if err := os.MkdirAll(cookieDir, 0o755); err != nil {
		t.Fatalf("mkdir cookies: %v", err)
	}
	cookieText := "# Netscape HTTP Cookie File\n.hd-space.org\tTRUE\t/\tTRUE\t0\tsession\tcookievalue\n"
	if err := os.WriteFile(filepath.Join(cookieDir, "HDS.txt"), []byte(cookieText), 0o600); err != nil {
		t.Fatalf("write cookies: %v", err)
	}

	for _, test := range []struct {
		name            string
		body            string
		wantDisposition dupe.Disposition
		wantComplete    bool
		wantPages       int
		wantWarning     string
	}{
		{
			name:            "markerless login response fails",
			body:            `<form action="/login.php"><input name="username"></form>`,
			wantDisposition: dupe.DispositionFailed,
		},
		{
			name:            "marker-present empty results complete",
			body:            `Show/Hide Categories<table></table>`,
			wantDisposition: dupe.DispositionResolved,
			wantComplete:    true,
			wantPages:       1,
		},
		{
			name:            "forward page without entries reports no progress",
			body:            `Show/Hide Categories<table></table><a href="index.php?page=torrents&amp;pages=1">2</a>`,
			wantDisposition: dupe.DispositionResolved,
			wantPages:       1,
			wantWarning:     "HDS search made no pagination progress",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: hdsRoundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(test.body)),
					Header:     make(http.Header),
				}, nil
			})}
			result := (&dupeSearcher{
				cfg:  config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmp, "upbrr.db")}},
				http: client,
			}).Search(context.Background(), api.DuplicateSubject{Identity: api.ExternalIdentity{IMDBID: 1234567}})
			if result.Disposition() != test.wantDisposition {
				t.Fatalf("disposition = %q, want %q", result.Disposition(), test.wantDisposition)
			}
			if test.wantDisposition == dupe.DispositionFailed && result.Code() != dupe.FailureResponseParse {
				t.Fatalf("failure code = %q, want %q", result.Code(), dupe.FailureResponseParse)
			}
			search := result.SearchEvidence()
			if search.Complete != test.wantComplete || search.Pages != test.wantPages || len(result.Entries()) != 0 {
				t.Fatalf("search evidence = %#v, entries = %#v", search, result.Entries())
			}
			if test.wantWarning != "" && (len(search.Warnings) != 1 || search.Warnings[0] != test.wantWarning) {
				t.Fatalf("warnings = %#v, want %q", search.Warnings, test.wantWarning)
			}
		})
	}
}

func TestHDSHasNextPageIgnoresEarlierGenericLinks(t *testing.T) {
	t.Parallel()

	root, err := xhtml.Parse(strings.NewReader(`<a href="index.php?pages=0">1</a>`))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if hdsHasNextPage(root, 1) {
		t.Fatal("earlier page link reported as next page")
	}
}
