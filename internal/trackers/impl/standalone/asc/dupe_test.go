// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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

	for _, tc := range []struct {
		name     string
		lastPage int
		cancel   bool
	}{
		{name: "complete", lastPage: 2},
		{name: "truncated", lastPage: maxSearchPages + 1},
		{name: "canceled", cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			client := &http.Client{Transport: ascTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodGet || r.URL.Host != siteHost || r.URL.Path != "/torrents" || r.URL.Query().Get("q") != "tt1234567" {
					t.Errorf("unexpected search request: %s %s", r.Method, r.URL.Path)
				}
				if _, err := r.Cookie("amigos-share-club-session"); err != nil {
					t.Error("missing session cookie")
				}
				if tc.cancel {
					cancel()
					return nil, r.Context().Err()
				}
				if calls > 1 && r.URL.Query().Get("page") != strconv.Itoa(calls) {
					t.Errorf("unexpected page parameter for request %d", calls)
				}
				w := httptest.NewRecorder()
				_, _ = fmt.Fprintf(w, `<script data-page="app" type="application/json">
					{"props":{"torrents":{"current_page":%d,"last_page":%d,"data":[
						{"id":%d,"name":"Synthetic Series - S02E03",
						 "badges":[{"kind":"source","label":"720p"},{"kind":"resolution","label":"4K"}]}
					]}}}</script>`, calls, tc.lastPage, calls)
				return w.Result(), nil
			})}
			result := (dupeSearcher{
				cfg:  config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(dir, "upbrr.db")}},
				http: client,
			}).Search(ctx, api.DuplicateSubject{Identity: api.ExternalIdentity{IMDBID: 1234567}})
			if tc.cancel {
				if calls != 1 || !errors.Is(result.Cause(), context.Canceled) {
					t.Fatalf("search calls=%d, expected cancellation cause, got %v", calls, result.Cause())
				}
				return
			}
			pages := min(tc.lastPage, maxSearchPages)
			complete := tc.lastPage <= maxSearchPages
			entries, evidence := result.Entries(), result.SearchEvidence()
			if calls != pages || evidence.Pages != pages || len(entries) != pages ||
				evidence.EffectiveComplete() != complete || (len(evidence.Warnings) == 0) != complete {
				t.Fatalf("search calls=%d, outcome=%s, evidence=%+v, entries=%d", calls, result.Code(), evidence, len(entries))
			}
			for i, entry := range entries {
				if entry.Link != fmt.Sprintf("%s/torrents/%d", siteURL, i+1) || entry.Res != "2160p" ||
					entry.Season != 0 || entry.Episode != 0 || entry.Pack {
					t.Errorf("page %d produced unexpected duplicate evidence", i+1)
				}
				if candidate := dupe.NormalizeCandidate(entry, "ASC"); candidate.Season != 2 || candidate.Episode != 3 {
					t.Errorf("page %d lost title-derived episode coordinates", i+1)
				}
			}
		})
	}
}

func TestASCDisplayTitleIsNotExactReleaseIdentity(t *testing.T) {
	t.Parallel()

	const title = "Synthetic Series - S02E03"
	candidate := dupe.NormalizeCandidate(searchRow{
		ID: 1,
 Name: title,
		Badges: []searchBadge{{Kind: "resolution", Label: "4K"}},
	}.entry(), "ASC")
	target := api.TrackerDuplicateTarget{
		Names: []string{title},
 Resolution: "1080p",
 Season: 2,
 Episode: 3,
	}
	evidence := dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID}
	evaluation := dupe.Evaluate(target, []dupe.TrackerCandidate{candidate}, trackers.DupePolicy{}, evidence)
	if got := evaluation.Candidates[0]; got.Relation != api.DupeRelationCoexists || got.Candidate.Name != title || evaluation.Blocks {
		t.Fatalf("different resolution with display title %q: relation=%s, blocks=%t, candidate=%q", title, got.Relation, evaluation.Blocks, got.Candidate.Name)
	}

	target.Resolution = "2160p"
	evaluation = dupe.Evaluate(target, []dupe.TrackerCandidate{candidate}, trackers.DupePolicy{}, evidence)
	if evaluation.Blocks || !evaluation.RequiresAction {
		t.Fatalf("same resolution without exact release evidence: blocks=%t, requires action=%t", evaluation.Blocks, evaluation.RequiresAction)
	}

	candidate.Files = []string{"Synthetic.Series.S02E03.2160p-GRP.mkv"}
	target.FileNames = candidate.Files
	evaluation = dupe.Evaluate(target, []dupe.TrackerCandidate{candidate}, trackers.DupePolicy{}, evidence)
	if evaluation.Candidates[0].Relation != api.DupeRelationExactDuplicate || !evaluation.Blocks {
		t.Fatalf("matching primary file: relation=%s, blocks=%t", evaluation.Candidates[0].Relation, evaluation.Blocks)
	}
}

func TestASCConnectionChecksImportedSession(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		cookie   string
		status   int
		location string
		body     string
		want     string
	}{
		{name: "missing", want: "missing_configuration"},
		{
			name:   "active",
			cookie: "synthetic",
			status: http.StatusOK,
			body:   `<script data-page="app" type="application/json">{"props": {"auth": {"user": {"name": "Synthetic", "id": 1}}}}</script>`,
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
			want:   "authentication_required",
		},
		{
			name:   "null ID",
			cookie: "synthetic",
			status: http.StatusOK,
			body:   `<script data-page="app" type="application/json">{"props":{"auth":{"user":{"id":null}}}}</script>`,
			want:   "unexpected_response",
		},
		{
			name:   "user outside auth",
			cookie: "synthetic",
			status: http.StatusOK,
			body:   `<script data-page="app" type="application/json">{"props":{"user":{"id":1}}}</script>`,
			want:   "unexpected_response",
		},
		{
			name:     "redirect to ASC login",
			cookie:   "synthetic",
			status:   http.StatusFound,
			location: "/login?expired=1",
			want:     "authentication_required",
		},
		{
			name:     "redirect to ASC login with explicit HTTPS port",
			cookie:   "synthetic",
			status:   http.StatusSeeOther,
			location: "https://" + siteHost + ":443/login",
			want:     "authentication_required",
		},
		{
			name:     "redirect to foreign login",
			cookie:   "synthetic",
			status:   http.StatusFound,
			location: "https://example.invalid/login",
			want:     "unexpected_response",
		},
		{
			name:     "redirect to different ASC port",
			cookie:   "synthetic",
			status:   http.StatusFound,
			location: "https://" + siteHost + ":444/login",
			want:     "unexpected_response",
		},
		{
			name:     "redirect to another ASC page",
			cookie:   "synthetic",
			status:   http.StatusFound,
			location: "/maintenance",
			want:     "unexpected_response",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := &http.Client{Transport: ascTestTransport(func(r *http.Request) (*http.Response, error) {
				if tc.cookie == "" || r.Method != http.MethodGet || r.URL.Host != siteHost || r.URL.Path != "/dashboard" {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
				}
				w := httptest.NewRecorder()
				if tc.location != "" {
					w.Header().Set("Location", tc.location)
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
