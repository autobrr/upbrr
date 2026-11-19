// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestASCSearchDoesNotUseSourcePathAsTitle(t *testing.T) {
	t.Parallel()
	meta := api.DuplicateSubject{Anime: true, SourcePath: `C:\private\Example.Release.2026.mkv`}
	if got := resolveASCTitle(meta); got != "" {
		t.Fatalf("source path became a search title: %q", got)
	}
	result := (dupeSearcher{http: &http.Client{}}).Search(t.Context(), meta)
	if result.Disposition() != dupe.DispositionNotRun || result.Code() != dupe.NotRunMissingMetadata {
		t.Fatalf("missing title outcome = %v/%s", result.Disposition(), result.Code())
	}
}

func TestResolveASCTitleHonorsManualTitle(t *testing.T) {
	t.Parallel()

	projection := &api.TrackerReleaseProjection{DuplicateCriteria: api.TrackerDuplicateCriteria{Name: "Projected Release"}}
	manual := api.DuplicateSubject{
		Release:    api.ReleaseInfo{Title: "Automatic Release"},
		Projection: projection,
		EffectiveMetadata: api.EffectiveMetadata{
			Title:           "Manual Release",
			TitleProvenance: api.FactProvenanceManual,
		},
	}
	if got := resolveASCTitle(manual); got != "Manual Release" {
		t.Fatalf("manual ASC search title = %q", got)
	}
	manual.EffectiveMetadata = api.EffectiveMetadata{TitleProvenance: api.FactProvenanceManualEmpty}
	if got := resolveASCTitle(manual); got != "" {
		t.Fatalf("manual-empty ASC search title = %q", got)
	}
	if got := resolveASCTitle(api.DuplicateSubject{Projection: projection}); got != "Projected Release" {
		t.Fatalf("automatic ASC search title = %q", got)
	}
}

type ascTestTransport func(*http.Request) (*http.Response, error)

func (f ascTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestASCSearchUsesNewSiteAndEnumeratesPages(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "cookies"), 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := "# Netscape HTTP Cookie File\n.amigos-share.club\tTRUE\t/\tTRUE\t0\tamigos-share-club-session\tsynthetic-session\n"
	if err := os.WriteFile(filepath.Join(dir, "cookies", testCookieFileName), []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}

	calls := 0
	client := &http.Client{Transport: ascTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodGet || r.URL.Host != "amigos-share.club" || r.URL.Path != "/torrents" || r.URL.Query().Get("q") != "tt1234567" {
			t.Errorf("unexpected search request: %s %s", r.Method, r.URL.Path)
		}
		if _, err := r.Cookie("amigos-share-club-session"); err != nil {
			t.Error("missing session cookie")
		}
		w := httptest.NewRecorder()
		switch calls {
		case 1:
			_, _ = w.WriteString(`<script data-page="app" type="application/json">{"props":{"torrents":{"current_page":1,"last_page":2,"data":[{"id":1,"name":"Synthetic Movie","size":123}]}}}</script>`)
		case 2:
			if r.URL.Query().Get("page") != "2" {
				t.Error("second page not requested")
			}
			_, _ = w.WriteString(`<script data-page="app" type="application/json">{"props":{"torrents":{"current_page":2,"last_page":2,"data":[]}}}</script>`)
		default:
			t.Error("unexpected extra HTTP request")
			w.WriteHeader(http.StatusBadRequest)
		}
		return w.Result(), nil
	})}
	result := (dupeSearcher{
		cfg:  config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(dir, "upbrr.db")}},
		http: client,
	}).Search(t.Context(), api.DuplicateSubject{Identity: api.ExternalIdentity{IMDBID: 1234567}})
	entries := result.Entries()
	if calls != 2 || !result.SearchEvidence().EffectiveComplete() || len(entries) != 1 || entries[0].Link != "https://amigos-share.club/torrents/1" {
		t.Fatalf("search calls=%d, outcome=%s, evidence=%+v, entries=%+v", calls, result.Code(), result.SearchEvidence(), entries)
	}
}

func TestASCConnectionChecksImportedSession(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		cookie string
		status int
		body   string
		want   string
	}{
		{name: "missing", want: "missing_configuration"},
		{
			name:   "active",
			cookie: "synthetic",
			status: http.StatusOK,
			body:   `<script data-page="app" type="application/json">{"props":{"auth":{"user":{"id":1}}}}</script>`,
			want:   "success",
		},
		{
			name:   "expired",
			cookie: "synthetic",
			status: http.StatusUnauthorized,
			want:   "authentication_required",
		},
		{
			name:   "login page",
			cookie: "synthetic",
			status: http.StatusOK,
			body:   `<form action="/login"></form>`,
			want:   "unexpected_response",
		},
		{
			name:   "signed out",
			cookie: "synthetic",
			status: http.StatusOK,
			body:   `<script data-page="app" type="application/json">{"props":{"auth":{"user":null}}}</script>`,
			want:   "unexpected_response",
		},
		{
			name:   "redirect",
			cookie: "synthetic",
			status: http.StatusFound,
			want:   "unexpected_response",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := &http.Client{Transport: ascTestTransport(func(r *http.Request) (*http.Response, error) {
				if tc.cookie == "" || r.Method != http.MethodGet || r.URL.Host != siteHost || r.URL.Path != "/dashboard" {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
				}
				w := httptest.NewRecorder()
				if tc.status == http.StatusFound {
					w.Header().Set("Location", "https://example.invalid/login")
				}
				w.WriteHeader(tc.status)
				_, _ = w.WriteString(tc.body)
				return w.Result(), nil
			})}
			input := trackers.ConnectionProbeInput{}
			if tc.cookie != "" {
				input.Cookies = map[string]string{"amigos-share-club-session": tc.cookie}
			}
			if got := testConnection(t.Context(), input, client); got.Code != tc.want {
				t.Fatalf("connection check = %q (%s), want %q", got.Code, got.Message, tc.want)
			}
		})
	}
}

func TestASCRejectsLegacyUploadPreparation(t *testing.T) {
	t.Parallel()
	_, _, err := prepareUploadState(t.Context(), trackers.PreparationInput{}, false)
	if err == nil || !strings.Contains(err.Error(), uploadUnavailable) {
		t.Fatalf("upload preparation = %v, want migration block", err)
	}
}
