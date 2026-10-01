// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	xhtml "golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func adapterEvidence(result dupe.AdapterResult) ([]api.DupeEntry, []string, error) {
	return result.Entries(), result.Notes(), result.Cause()
}

type azDupeTraceLogger struct {
	api.NopLogger
	trace []string
}

func (l *azDupeTraceLogger) Tracef(format string, args ...any) {
	l.trace = append(l.trace, fmt.Sprintf(format, args...))
}

func TestAZRequestTraceIncludesFallbackAndGroupPages(t *testing.T) {
	t.Parallel()
	var terms []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie("session"); err != nil || cookie.Value != "private-cookie" {
			t.Error("logging must preserve request authentication")
		}
		switch r.URL.Path {
		case "/ajax/movies/2":
			term := r.URL.Query().Get("term")
			terms = append(terms, term)
			if term == "tt0000123" {
				_, _ = io.WriteString(w, `{"data":[]}`)
			} else {
				_, _ = io.WriteString(w, `{"data":[{"id":"77","imdb":"tt0000123"}]}`)
			}
		case "/movies/torrents/77":
			if r.URL.Query().Get("page") == "" {
				_, _ = io.WriteString(w, `<a rel="next" href="/movies/torrents/77?page=2">Next</a>`)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	logger := &azDupeTraceLogger{}
	searcher := dupeSearcher{
		http:    server.Client(),
		logger:  logger,
		tracker: "AZ",
	}
	site := azDupeSiteDef{baseURL: server.URL}
	cookies := []*http.Cookie{{Name: "session", Value: "private-cookie"}}
	id, err := searcher.lookupMediaCode(t.Context(), site, cookies, api.DuplicateSubject{
		Identity: api.ExternalIdentity{Category: api.CanonicalCategoryTV, IMDBID: 123},
		Release:  api.ReleaseInfo{Title: "Example Series"},
	})
	if err != nil || id != "77" || strings.Join(terms, ",") != "tt0000123,Example Series" {
		t.Fatalf("lookup id=%q terms=%v err=%v", id, terms, err)
	}
	_, pages, complete, warning, err := searcher.fetchTorrentList(t.Context(), site, cookies, server.URL+"/movies/torrents/"+id)
	if err != nil || !complete || warning != "" || pages != 2 {
		t.Fatalf("group pages=%d complete=%t warning=%q err=%v", pages, complete, warning, err)
	}
	logs := strings.Join(logger.trace, "\n")
	for _, want := range []string{
		`"category":"2"`, `"term":"tt0000123"`, `"term":"Example Series"`, `"group_id":"77"`, `"request_page":2`, `"page":"2"`,
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("request TRACE missing %q", want)
		}
	}
	if len(logger.trace) != 4 || strings.Contains(logs, "private-cookie") || strings.Contains(logs, server.URL) {
		t.Fatal("request TRACE has the wrong request count or exposed authentication/URL")
	}
}

func TestAZDupeTitlePreservesManualTitleAuthority(t *testing.T) {
	t.Parallel()

	projection := &api.TrackerReleaseProjection{
		DuplicateCriteria: api.TrackerDuplicateCriteria{Name: "Projected title"},
	}
	for _, test := range []struct {
		name string
		meta api.DuplicateSubject
		want string
	}{
		{
			name: "manual title wins over projection",
			meta: api.DuplicateSubject{
				Release:           api.ReleaseInfo{Title: "Detected title"},
				ProviderMetadata:  api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Title: "Provider title"}},
				EffectiveMetadata: api.EffectiveMetadata{Title: "Corrected title", TitleProvenance: api.FactProvenanceManual},
				Projection:        projection,
			},
			want: "Corrected title",
		},
		{
			name: "manual clear wins over projection",
			meta: api.DuplicateSubject{
				Release:           api.ReleaseInfo{Title: "Detected title"},
				ProviderMetadata:  api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Title: "Provider title"}},
				EffectiveMetadata: api.EffectiveMetadata{TitleProvenance: api.FactProvenanceManualEmpty},
				Projection:        projection,
			},
			want: "",
		},
		{
			name: "automatic title keeps projection priority",
			meta: api.DuplicateSubject{
				Release:          api.ReleaseInfo{Title: "Detected title"},
				ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Title: "Provider title"}},
				Projection:       projection,
			},
			want: "Projected title",
		},
		{
			name: "stale provider title is ignored without a projection",
			meta: api.DuplicateSubject{
				SourcePath: "prepared/current",
				Identity:   api.ExternalIdentity{SourcePath: "prepared/current", Generation: 2},
				Filename:   "Fallback title",
				ProviderMetadata: api.SourceScopedMetadata{
					SourcePath: "prepared/stale",
					Generation: 1,
					TMDB:       &api.TMDBMetadata{Title: "Stale title"},
				},
			},
			want: "Fallback title",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := lookupAZDupeTitle(test.meta); got != test.want {
				t.Fatalf("dupe title = %q, want %q", got, test.want)
			}
		})
	}
}

func TestAZNetworkHandlerSearchParsesHTMLResults(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "ua.db")
	cookieDir := filepath.Join(tmp, "cookies")
	if err := os.MkdirAll(cookieDir, 0o755); err != nil {
		t.Fatalf("mkdir cookie dir: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ajax/movies/1":
			_, _ = io.WriteString(w, `{"data":[{"id":"77","imdb":"tt0000123"}]}`)
		case "/movies/torrents/77":
			if r.URL.Query().Has("quality") {
				t.Error("AZ discovery must not include a quality filter")
			}
			_, _ = io.WriteString(w, `<table class="table-bordered"><tbody>
<tr><td><a class="torrent-filename" href="/torrent/123">Example.Release.2026.BluRay.Raw-GRP</a><span class="badge-extra">BluRay Raw</span></td></tr>
<tr><td><a class="torrent-filename" href="/torrent/124">Example.Release.2026.BluRay.REMUX-GRP</a><span class="badge-extra">BluRay REMUX</span></td></tr>
<tr><td><a class="torrent-filename" href="/torrent/125">Example.Release.2026.1080p.WEB-DL-GRP</a><span class="badge-extra">WEB-DL</span></td></tr>
<tr><td><a class="torrent-filename" href="/torrent/126">Example.Release.2026.1080p.WEBRip-GRP</a><span class="badge-extra">WEBRip</span></td></tr>
<tr><td><a class="torrent-filename" href="/torrent/127">Example.Release.2026.1080p.HDTV-GRP</a><span class="badge-extra">HDTV</span></td></tr>
</tbody></table>`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	parsed, _ := url.Parse(server.URL)
	cookieText := "# Netscape HTTP Cookie File\n" + parsed.Hostname() + "\tTRUE\t/\tTRUE\t0\tsession\tcookievalue\n"
	if err := os.WriteFile(filepath.Join(cookieDir, "AZ.txt"), []byte(cookieText), 0o600); err != nil {
		t.Fatalf("write cookie file: %v", err)
	}

	handler := dupe.NewAdapter(testDefinitionAt(server.URL), "AZ",
		config.Config{
			MainSettings: config.MainSettingsConfig{DBPath: dbPath},
			Trackers:     config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"AZ": {}}},
		}, server.Client(), api.NopLogger{})
	entries, notes, err := adapterEvidence(handler.Search(context.Background(), api.DuplicateSubject{
		Identity: api.ExternalIdentity{Category: "MOVIE", IMDBID: 123},
		Release:  api.ReleaseInfo{Title: "Example Release", Resolution: "1080p"},
		Type:     "WEBDL",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected no notes, got %v", notes)
	}
	if len(entries) != 5 {
		t.Fatalf("expected every mixed variant, got %d", len(entries))
	}
	if entries[0].ID != "123" {
		t.Fatalf("expected torrent id 123, got %q", entries[0].ID)
	}
	wantTypes := []string{"DISC", "REMUX", "WEBDL", "WEBRIP", "HDTV"}
	for index, want := range wantTypes {
		if entries[index].CanonicalType != want {
			t.Fatalf("candidate %d type = %#v, want %q", index, entries[index], want)
		}
	}
	evidence := handler.Search(context.Background(), api.DuplicateSubject{
		Identity: api.ExternalIdentity{Category: "MOVIE", IMDBID: 123},
		Release:  api.ReleaseInfo{Title: "Example Release", Resolution: "1080p"},
		Type:     "WEBDL",
	}).SearchEvidence()
	if !evidence.EffectiveComplete() || evidence.WorkScope != dupe.WorkScopeTrackerGroup {
		t.Fatalf("AZ search evidence = %#v", evidence)
	}
}

func TestAZCanonicalCandidateType(t *testing.T) {
	t.Parallel()

	for input, want := range map[string]string{
		"BluRay Raw":   "DISC",
		"DVD":          "DISC",
		"BluRay Remux": "REMUX",
		"DVD Remux":    "REMUX",
		"WEB-DL":       "WEBDL",
		"WEBRip":       "WEBRIP",
		"HDTV":         "HDTV",
		"SDTV":         "HDTV",
		"BDRip":        "ENCODE",
		"BluRay":       "ENCODE",
		"BRRip":        "ENCODE",
		"DVDRip":       "ENCODE",
		"HDRip":        "ENCODE",
	} {
		if got := azCanonicalCandidateType(input); got != want {
			t.Errorf("azCanonicalCandidateType(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAZLookupMediaCodeRequiresExactIMDbMatch(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		meta    api.DuplicateSubject
		payload string
		want    string
	}{
		{
			name: "matching later result",
			meta: api.DuplicateSubject{Identity: api.ExternalIdentity{
				Category: "MOVIE",
				IMDBID:   123,
			}},
			payload: `{"data":[{"id":"11","imdb":"tt9999999"},{"id":"22","imdb":"tt0000123"}]}`,
			want:    "22",
		},
		{
			name: "unmatched results",
			meta: api.DuplicateSubject{Identity: api.ExternalIdentity{
				Category: "MOVIE",
				IMDBID:   123,
			}},
			payload: `{"data":[{"id":"11","imdb":"tt9999999"}]}`,
		},
		{
			name: "title only cannot bind work",
			meta: api.DuplicateSubject{
				Identity: api.ExternalIdentity{Category: "MOVIE"},
				Release:  api.ReleaseInfo{Title: "Example Release 2026"},
			},
			payload: `{"data":[{"id":"11","imdb":"tt0000123"}]}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, test.payload)
			}))
			t.Cleanup(server.Close)

			got, err := (dupeSearcher{http: server.Client()}).lookupMediaCode(
				context.Background(),
				azDupeSiteDef{baseURL: server.URL},
				nil,
				test.meta,
			)
			if err != nil {
				t.Fatalf("lookup media code: %v", err)
			}
			if got != test.want {
				t.Fatalf("media code = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNextAZPageDistinguishesAbsentAndRejectedURL(t *testing.T) {
	t.Parallel()

	for name, fixture := range map[string]struct {
		html string
		want string
	}{
		"absent":    {html: `<div>No next page</div>`},
		"relative":  {html: `<a rel="next" href="/movies/torrents/77?page=2">Next</a>`, want: "https://az.example/movies/torrents/77?page=2"},
		"external":  {html: `<a rel="next" href="https://example.invalid/steal">Next</a>`},
		"empty":     {html: `<a rel="next" href="">Next</a>`},
		"malformed": {html: `<a rel="next" href="://">Next</a>`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			root, err := xhtml.Parse(strings.NewReader(fixture.html))
			if err != nil {
				t.Fatalf("parse fixture: %v", err)
			}
			got, present := nextAZPage(root, "https://az.example")
			wantPresent := strings.Contains(fixture.html, `rel="next"`)
			if got != fixture.want || present != wantPresent {
				t.Fatalf("next page = %q, present = %t; want %q, %t", got, present, fixture.want, wantPresent)
			}
		})
	}
}

func TestAZRejectedNextPageLeavesSearchIncomplete(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<a rel="next" href="https://example.invalid/page/2">Next</a>`)
	}))
	t.Cleanup(server.Close)

	_, pages, complete, warning, err := (dupeSearcher{http: server.Client()}).fetchTorrentList(
		context.Background(),
		azDupeSiteDef{baseURL: server.URL},
		nil,
		server.URL+"/movies/torrents/77",
	)
	if err != nil {
		t.Fatalf("fetch torrent list: %v", err)
	}
	if pages != 1 || complete || warning == "" {
		t.Fatalf("search evidence: pages=%d complete=%t warning=%q", pages, complete, warning)
	}
}
