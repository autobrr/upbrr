// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mtv

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func adapterEvidence(result dupe.AdapterResult) ([]api.DupeEntry, []string, error) {
	return result.Entries(), result.Notes(), result.Cause()
}

func TestMTVHandlerUsesIMDBPriorityAndParsesXML(t *testing.T) {
	t.Parallel()

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodGet {
				t.Fatalf("expected GET request, got %s", req.Method)
			}
			if got := req.URL.String(); !strings.HasPrefix(got, mtvTorznabEndpoint+"?") {
				t.Fatalf("unexpected endpoint %q", got)
			}

			query := req.URL.Query()
			assertQueryParam(t, query, "t", "search")
			assertQueryParam(t, query, "apikey", "token")
			assertQueryParam(t, query, "limit", "100")
			assertQueryParam(t, query, "imdbid", "tt0123456")
			if got := query.Get("tmdbid"); got != "" {
				t.Fatalf("tmdbid should be empty when imdbid is present, got %q", got)
			}
			if got := query.Get("tvdbid"); got != "" {
				t.Fatalf("tvdbid should be empty when imdbid is present, got %q", got)
			}
			if got := query.Get("q"); got != "" {
				t.Fatalf("q should be empty when imdbid is present, got %q", got)
			}

			body := `<?xml version="1.0" encoding="UTF-8"?>
<rss xmlns:torznab="http://torznab.com/schemas/2015/feed">
  <channel>
    <response offset="0" total="2" />
    <item>
      <title>Example.Release.1080p.WEB-DL.DDP5.1.H.264-GRP</title>
      <files>3</files>
      <size>123456789</size>
      <guid>https://www.morethantv.me/torrents.php?id=100</guid>
      <link>https://www.morethantv.me/download.php/100?torrent_pass=abc&amp;https=1</link>
    </item>
    <item>
      <title>Example.Release.2160p.WEB-DL.DDP5.1.HEVC-GRP</title>
      <guid>https://www.morethantv.me/torrents.php?id=101</guid>
      <link>https://www.morethantv.me/download.php/101?torrent_pass=abc&amp;https=1</link>
      <torznab:attr name="files" value="7" />
      <torznab:attr name="size" value="222333444" />
    </item>
  </channel>
</rss>`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	handler := dupe.NewAdapter(New(), "MTV",
		config.Config{
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"MTV": {APIKey: "token"},
				},
			},
		}, client, api.NopLogger{})

	meta := api.DuplicateSubject{
		Identity: api.ExternalIdentity{
			IMDBID: 123456,
			TMDBID: 999,
			TVDBID: 888,
		},
		Release: api.ReleaseInfo{Title: "Ignored Title"},
	}

	entries, notes, err := adapterEvidence(handler.Search(context.Background(), meta))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected no notes, got %#v", notes)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	first := entries[0]
	if first.Name != "Example.Release.1080p.WEB-DL.DDP5.1.H.264-GRP" {
		t.Fatalf("unexpected first entry name %q", first.Name)
	}
	if first.FileCount != 3 {
		t.Fatalf("expected file_count=3, got %d", first.FileCount)
	}
	if !first.SizeKnown || first.SizeBytes != 123456789 {
		t.Fatalf("expected parsed size for first entry, got known=%t size=%d", first.SizeKnown, first.SizeBytes)
	}
	if first.ID != "https://www.morethantv.me/torrents.php?id=100" {
		t.Fatalf("unexpected first entry ID %q", first.ID)
	}
	if first.Link != "https://www.morethantv.me/torrents.php?id=100" {
		t.Fatalf("unexpected first entry link %q", first.Link)
	}
	if first.Download != "https://www.morethantv.me/download.php/100?torrent_pass=abc&https=1" {
		t.Fatalf("unexpected first entry download %q", first.Download)
	}
	if len(first.Files) != 0 {
		t.Fatalf("expected Torznab title to remain separate from unproven file evidence, got %#v", first.Files)
	}

	second := entries[1]
	if second.FileCount != 7 {
		t.Fatalf("expected attr-derived file_count=7, got %d", second.FileCount)
	}
	if !second.SizeKnown || second.SizeBytes != 222333444 {
		t.Fatalf("expected attr-derived size, got known=%t size=%d", second.SizeKnown, second.SizeBytes)
	}
}

func TestMTVHandlerUsesExactProjectedTitleQuery(t *testing.T) {
	t.Parallel()

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			query := req.URL.Query()
			assertQueryParam(t, query, "t", "search")
			assertQueryParam(t, query, "apikey", "token")
			assertQueryParam(t, query, "limit", "100")
			assertQueryParam(t, query, "q", "Exact Projected Query")
			if got := query.Get("imdbid"); got != "" {
				t.Fatalf("imdbid should be empty, got %q", got)
			}
			body := `<rss><channel><response offset="0" total="0" /></channel></rss>`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	handler := dupe.NewAdapter(New(), "MTV",
		config.Config{
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"MTV": {APIKey: "token"},
				},
			},
		}, client, api.NopLogger{})
	meta := api.DuplicateSubject{
		Release: api.ReleaseInfo{Title: "Ignored Title"},
		Projection: &api.TrackerReleaseProjection{
			DuplicateCriteria: api.TrackerDuplicateCriteria{Name: "Exact Projected Query"},
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

func TestMTVHandlerPaginatesUsingAdvertisedOffsets(t *testing.T) {
	t.Parallel()

	calls := 0
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			offset := req.URL.Query().Get("offset")
			if calls == 1 && offset != "" {
				t.Fatalf("first offset = %q", offset)
			}
			if calls == 2 && offset != "100" {
				t.Fatalf("second offset = %q", offset)
			}
			count := 100
			if calls == 2 {
				count = 1
			}
			var body strings.Builder
			body.WriteString("<rss><channel>")
			fmt.Fprintf(&body, `<response offset="%d" total="101" />`, (calls-1)*100)
			for index := range count {
				fmt.Fprintf(&body, "<item><title>Example.Release.%03d.1080p-GRP</title></item>", index+(calls-1)*100)
			}
			body.WriteString("</channel></rss>")
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body.String())),
				Header:     make(http.Header),
			}, nil
		}),
	}
	handler := dupe.NewAdapter(New(), "MTV", config.Config{
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"MTV": {APIKey: "token"},
		}},
	}, client, api.NopLogger{})

	result := handler.Search(context.Background(), api.DuplicateSubject{
		Identity: api.ExternalIdentity{IMDBID: 123456},
	})
	if err := result.Cause(); err != nil {
		t.Fatalf("search: %v", err)
	}
	if calls != 2 || len(result.Entries()) != 101 {
		t.Fatalf("calls=%d entries=%d", calls, len(result.Entries()))
	}
	evidence := result.SearchEvidence()
	if !evidence.Complete || evidence.Pages != 2 {
		t.Fatalf("search evidence = %#v", evidence)
	}
}

func TestMTVHandlerTreatsMetadataFreeShortPageAsComplete(t *testing.T) {
	t.Parallel()

	for _, itemCount := range []int{0, 5} {
		t.Run(fmt.Sprintf("items_%d", itemCount), func(t *testing.T) {
			t.Parallel()

			calls := 0
			client := &http.Client{
				Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					calls++
					var body strings.Builder
					body.WriteString("<rss><channel>")
					for index := range itemCount {
						fmt.Fprintf(&body, "<item><title>Example.Release.%03d.1080p-GRP</title></item>", index)
					}
					body.WriteString("</channel></rss>")
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(body.String())),
						Header:     make(http.Header),
					}, nil
				}),
			}
			handler := dupe.NewAdapter(New(), "MTV", config.Config{
				Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
					"MTV": {APIKey: "token"},
				}},
			}, client, api.NopLogger{})

			result := handler.Search(context.Background(), api.DuplicateSubject{
				Identity: api.ExternalIdentity{IMDBID: 123456},
			})
			if err := result.Cause(); err != nil {
				t.Fatalf("search: %v", err)
			}
			evidence := result.SearchEvidence()
			if calls != 1 || !evidence.Complete || evidence.Pages != 1 || len(evidence.Warnings) != 0 {
				t.Fatalf("calls=%d search evidence=%#v", calls, evidence)
			}
			if len(result.Entries()) != itemCount {
				t.Fatalf("entries=%d, want %d", len(result.Entries()), itemCount)
			}
		})
	}
}

func TestMTVHandlerReportsIncompletePaginationMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		response string
		warning  string
	}{
		{
			name:    "omitted response",
			warning: "MTV search reached the result limit without pagination support; results may be truncated",
		},
		{
			name:     "omitted attributes",
			response: "<response />",
			warning:  "MTV search returned incomplete pagination metadata",
		},
		{
			name:     "inconsistent offset",
			response: `<response offset="1" total="100" />`,
			warning:  "MTV search returned inconsistent pagination metadata",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			client := &http.Client{
				Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					calls++
					var body strings.Builder
					body.WriteString("<rss><channel>")
					body.WriteString(test.response)
					for index := range 100 {
						fmt.Fprintf(&body, "<item><title>Example.Release.%03d.1080p-GRP</title></item>", index)
					}
					body.WriteString("</channel></rss>")
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(body.String())),
						Header:     make(http.Header),
					}, nil
				}),
			}
			handler := dupe.NewAdapter(New(), "MTV", config.Config{
				Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
					"MTV": {APIKey: "token"},
				}},
			}, client, api.NopLogger{})

			result := handler.Search(context.Background(), api.DuplicateSubject{
				Identity: api.ExternalIdentity{IMDBID: 123456},
			})
			if err := result.Cause(); err != nil {
				t.Fatalf("search: %v", err)
			}
			evidence := result.SearchEvidence()
			if calls != 1 || evidence.Complete || evidence.Pages != 1 || len(evidence.Warnings) != 1 {
				t.Fatalf("calls=%d search evidence=%#v", calls, evidence)
			}
			if evidence.Warnings[0] != test.warning {
				t.Fatalf("warning=%q, want %q", evidence.Warnings[0], test.warning)
			}
		})
	}
}

func TestMTVHandlerRejectsTorznabErrorResponse(t *testing.T) {
	t.Parallel()

	client := &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`<error code="201" description="Incorrect parameter." />`)),
				Header:     make(http.Header),
			}, nil
		}),
	}
	handler := dupe.NewAdapter(New(), "MTV", config.Config{
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"MTV": {APIKey: "token"},
		}},
	}, client, api.NopLogger{})

	result := handler.Search(context.Background(), api.DuplicateSubject{
		Identity: api.ExternalIdentity{IMDBID: 123456},
	})
	if result.Disposition() != dupe.DispositionFailed {
		t.Fatalf("disposition=%v, want failed", result.Disposition())
	}
	if result.Code() != dupe.FailureResponseStatus {
		t.Fatalf("code=%q, want %q", result.Code(), dupe.FailureResponseStatus)
	}
	if result.SafeMessage() != "MTV API rejected search" {
		t.Fatalf("safe message=%q", result.SafeMessage())
	}
}

func TestCleanMTVSearchTitleDirectFallback(t *testing.T) {
	t.Parallel()

	if got := cleanMTVSearchTitle(api.DuplicateSubject{Release: api.ReleaseInfo{Title: "John's Story: The Return"}}); got != "Johns Story The Return" {
		t.Fatalf("fallback query = %q", got)
	}
}

func TestMTVHandlerSkipsTVDBForMovie(t *testing.T) {
	t.Parallel()

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			query := req.URL.Query()
			assertQueryParam(t, query, "q", "Movie Title")
			if got := query.Get("tvdbid"); got != "" {
				t.Fatalf("tvdbid should be empty for movie category, got %q", got)
			}
			body := `<rss><channel><response offset="0" total="0" /></channel></rss>`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	handler := dupe.NewAdapter(New(), "MTV",
		config.Config{
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"MTV": {APIKey: "token"},
				},
			},
		}, client, api.NopLogger{})
	meta := api.DuplicateSubject{
		Identity: api.ExternalIdentity{Category: "MOVIE", TVDBID: 888},
		Release:  api.ReleaseInfo{Title: "Movie Title"},
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

func TestMTVHandlerUsesTVDBForTV(t *testing.T) {
	t.Parallel()

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			query := req.URL.Query()
			assertQueryParam(t, query, "tvdbid", "888")
			if got := query.Get("q"); got != "" {
				t.Fatalf("q should be empty when tvdbid is present, got %q", got)
			}
			body := `<rss><channel><response offset="0" total="0" /></channel></rss>`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	handler := dupe.NewAdapter(New(), "MTV",
		config.Config{
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"MTV": {APIKey: "token"},
				},
			},
		}, client, api.NopLogger{})
	meta := api.DuplicateSubject{
		Identity: api.ExternalIdentity{Category: "TV", TVDBID: 888},
		Release:  api.ReleaseInfo{Title: "Show Title"},
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func assertQueryParam(t *testing.T, query url.Values, key string, expected string) {
	t.Helper()
	if got := query.Get(key); got != expected {
		t.Fatalf("unexpected %s query value: got %q want %q", key, got, expected)
	}
}
