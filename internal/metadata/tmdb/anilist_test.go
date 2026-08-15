// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tmdb

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

const testAnilistURL = "https://anilist.invalid/graphql"

func TestAniListSearchRetriesTimeouts(t *testing.T) {
	attempts := 0
	logger := &captureTMDBLogger{}
	client := &Client{
		anilistURL: testAnilistURL,
		http: &http.Client{
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				attempts++
				if attempts < 3 {
					return nil, timeoutError{err: errors.New("timeout")}
				}
				body := `{"data":{"Page":{"media":[{"id":1,"idMal":20,"title":{"romaji":"Test","english":"Test"},"seasonYear":2024,"episodes":12,"tags":[{"name":"Shounen"}]}]}}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(body)),
					Header:     make(http.Header),
				}, nil
			}),
		},
		logger: logger,
	}

	items, err := client.anilistSearch(context.Background(), "Example.Anime.2026.1080p.WEB-DL-GRP", 0)
	if err != nil {
		t.Fatalf("expected success after retry, got error: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
	if len(items) != 1 || items[0].IDMal != 20 {
		t.Fatalf("expected one AniList result with MAL id 20, got %+v", items)
	}
	warnings := logger.warnings()
	if len(warnings) != 2 {
		t.Fatalf("expected two timeout warnings, got %#v", warnings)
	}
	for _, warning := range warnings {
		if !strings.HasPrefix(warning, "tmdb: anilist request retrying mal=0 retry=") {
			t.Fatalf("expected stable timeout warning fields, got %q", warning)
		}
		if strings.Contains(warning, "Example") || strings.Contains(warning, "GRP") {
			t.Fatalf("expected timeout warning to omit the search term, got %q", warning)
		}
	}
}

func TestAniListSearchRetriesRateLimit(t *testing.T) {
	attempts := 0
	client := &Client{
		anilistURL: testAnilistURL,
		http: &http.Client{
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				attempts++
				if attempts == 1 {
					return &http.Response{
						StatusCode: http.StatusTooManyRequests,
						Body:       io.NopCloser(strings.NewReader(`{"errors":[{"message":"Too Many Requests"}]}`)),
						Header:     http.Header{"Retry-After": []string{"0"}},
					}, nil
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"data":{"Page":{"media":[]}}}`)),
					Header:     make(http.Header),
				}, nil
			}),
		},
		logger: api.NopLogger{},
	}

	if _, err := client.anilistSearch(t.Context(), "Example Anime", 0); err != nil {
		t.Fatalf("expected success after rate-limit retry, got %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
}

func TestAniListSearchStopsWhenRetryDelayExceedsBudget(t *testing.T) {
	attempts := 0
	client := &Client{
		anilistURL: testAnilistURL,
		http: &http.Client{
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				attempts++
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Body:       io.NopCloser(strings.NewReader(`{"errors":[{"message":"Too Many Requests"}]}`)),
					Header:     http.Header{"Retry-After": []string{"120"}},
				}, nil
			}),
		},
		logger: api.NopLogger{},
	}

	_, err := client.anilistSearch(t.Context(), "Example Anime", 0)
	if err == nil || !strings.Contains(err.Error(), "http 429") {
		t.Fatalf("expected rate-limit error, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected 1 attempt when retry delay exceeds budget, got %d", attempts)
	}
}

func TestAniListSearchDoesNotRetryNonTimeoutErrors(t *testing.T) {
	attempts := 0
	client := &Client{
		anilistURL: testAnilistURL,
		http: &http.Client{
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				attempts++
				return nil, errors.New("boom")
			}),
		},
		logger: api.NopLogger{},
	}

	_, err := client.anilistSearch(context.Background(), "Test", 0)
	if err == nil {
		t.Fatalf("expected non-timeout error")
	}
	if attempts != 1 {
		t.Fatalf("expected 1 attempt for non-timeout error, got %d", attempts)
	}
}

func TestFetchAniListMetadataReturnsGraphQLErrors(t *testing.T) {
	attempts := 0
	client := &Client{
		anilistURL: testAnilistURL,
		http: &http.Client{
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				attempts++
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"errors":[{"message":"anime unavailable"}]}`)),
					Header:     make(http.Header),
				}, nil
			}),
		},
		logger: api.NopLogger{},
	}

	_, err := client.FetchAniListMetadata(context.Background(), 20)
	if err == nil {
		t.Fatal("expected GraphQL error")
	}
	if !strings.Contains(err.Error(), "graphql metadata error") || !strings.Contains(err.Error(), "anime unavailable") {
		t.Fatalf("expected GraphQL error message, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected one request, got %d", attempts)
	}
}

func TestFetchAniListMetadataRetriesRateLimit(t *testing.T) {
	attempts := 0
	client := &Client{
		anilistURL: testAnilistURL,
		http: &http.Client{
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				attempts++
				if attempts == 1 {
					return &http.Response{
						StatusCode: http.StatusTooManyRequests,
						Body:       io.NopCloser(strings.NewReader(`{"errors":[{"message":"Too Many Requests"}]}`)),
						Header:     http.Header{"Retry-After": []string{"0"}},
					}, nil
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"data":{"Media":{"id":1,"idMal":20}}}`)),
					Header:     make(http.Header),
				}, nil
			}),
		},
		logger: api.NopLogger{},
	}

	result, err := client.FetchAniListMetadata(t.Context(), 20)
	if err != nil {
		t.Fatalf("expected success after rate-limit retry, got %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
	if result.AniListID != 1 || result.MALID != 20 {
		t.Fatalf("unexpected mapped result: %#v", result)
	}
}

func TestFetchAniListMetadataStopsWhenRetryDelayExceedsBudget(t *testing.T) {
	attempts := 0
	client := &Client{
		anilistURL: testAnilistURL,
		http: &http.Client{
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				attempts++
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Body:       io.NopCloser(strings.NewReader(`{"errors":[{"message":"Too Many Requests"}]}`)),
					Header:     http.Header{"Retry-After": []string{"120"}},
				}, nil
			}),
		},
		logger: api.NopLogger{},
	}

	_, err := client.FetchAniListMetadata(t.Context(), 20)
	if err == nil || !strings.Contains(err.Error(), "http 429") {
		t.Fatalf("expected rate-limit error, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected 1 attempt when retry delay exceeds budget, got %d", attempts)
	}
}

func TestFetchAniListMetadataReturnsEmptyOnNotFound(t *testing.T) {
	attempts := 0
	client := &Client{
		anilistURL: testAnilistURL,
		http: &http.Client{
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				attempts++
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader(`{"errors":[{"message":"Not Found"}]}`)),
					Header:     make(http.Header),
				}, nil
			}),
		},
		logger: api.NopLogger{},
	}

	result, err := client.FetchAniListMetadata(t.Context(), 999999)
	if err != nil {
		t.Fatalf("expected missing media to return no error, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected one request, got %d", attempts)
	}
	if result.AniListID != 0 || result.MALID != 0 || result.TitleRomaji != "" {
		t.Fatalf("expected empty metadata, got %#v", result)
	}
}

func TestFetchAniListMetadataRejectsOversizedResponse(t *testing.T) {
	attempts := 0
	client := &Client{
		anilistURL: testAnilistURL,
		http: &http.Client{
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				attempts++
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(strings.Repeat(" ", int(maxAniListMetadataResponseBytes)+1))),
					Header:     make(http.Header),
				}, nil
			}),
		},
		logger: api.NopLogger{},
	}

	_, err := client.FetchAniListMetadata(context.Background(), 20)
	if err == nil {
		t.Fatal("expected oversized response error")
	}
	if !strings.Contains(err.Error(), "metadata response too large") {
		t.Fatalf("expected oversized response error, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected one request, got %d", attempts)
	}
}

func TestFetchAniListMetadataRetriesTimeoutsToMappedResult(t *testing.T) {
	attempts := 0
	client := &Client{
		anilistURL: testAnilistURL,
		http: &http.Client{
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				attempts++
				if attempts < 3 {
					return nil, timeoutError{err: errors.New("timeout")}
				}
				body := `{"data":{"Media":{"id":1,"idMal":20,"siteUrl":"https://anilist.co/anime/1","title":{"romaji":"Example Anime","english":"Example Anime"},"description":"Anime overview","format":"TV","status":"FINISHED","startDate":{"year":2026,"month":4,"day":1},"seasonYear":2026,"episodes":12,"duration":24,"coverImage":{"large":"https://img.example/cover.jpg"},"genres":["Action"],"averageScore":82,"popularity":100}}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(body)),
					Header:     make(http.Header),
				}, nil
			}),
		},
		logger: api.NopLogger{},
	}

	result, err := client.FetchAniListMetadata(context.Background(), 20)
	if err != nil {
		t.Fatalf("expected success after retry")
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
	if result.MALID != 20 || result.AniListID != 1 || result.TitleRomaji != "Example Anime" || result.Status != "FINISHED" {
		t.Fatalf("expected mapped AniList metadata after retry, got %#v", result)
	}
}

func TestAniListRequestsUseInjectedEndpoint(t *testing.T) {
	var requested []string
	client := &Client{
		anilistURL: testAnilistURL,
		http: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requested = append(requested, req.URL.String())
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"data":{}}`)),
					Header:     make(http.Header),
				}, nil
			}),
		},
		logger: api.NopLogger{},
	}

	if _, err := client.anilistSearch(context.Background(), "Example Anime", 0); err != nil {
		t.Fatalf("anilist search: %v", err)
	}
	if _, err := client.FetchAniListMetadata(context.Background(), 20); err != nil {
		t.Fatalf("anilist metadata: %v", err)
	}
	if len(requested) != 2 {
		t.Fatalf("expected one search and one metadata request, got %d", len(requested))
	}
	for _, endpoint := range requested {
		if endpoint != testAnilistURL {
			t.Fatalf("expected injected endpoint %q, got %q", testAnilistURL, endpoint)
		}
	}
}

func TestResolveAnimeWarnsSanitizedOnSearchFailure(t *testing.T) {
	logger := &captureTMDBLogger{}
	client := &Client{
		anilistURL: testAnilistURL,
		http: &http.Client{
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(strings.NewReader("upstream failure")),
					Header:     make(http.Header),
				}, nil
			}),
		},
		logger: logger,
	}

	result, err := client.ResolveAnime(context.Background(), "Example Anime", MetadataInput{
		Filename: "Example.Anime.2026.S01.1080p.WEB-DL-GRP",
	})
	if err != nil {
		t.Fatalf("expected best-effort resolve to succeed, got %v", err)
	}
	if result.MALID != 0 {
		t.Fatalf("expected no MAL id without candidates, got %d", result.MALID)
	}

	warnings := logger.warnings()
	if len(warnings) != 2 {
		t.Fatalf("expected one warning per failed search term, got %#v", warnings)
	}
	for _, warning := range warnings {
		if !strings.HasPrefix(warning, "tmdb: anilist search failed mal=0 err=") {
			t.Fatalf("expected key/value warning, got %q", warning)
		}
		if strings.Contains(warning, "Example") || strings.Contains(warning, "GRP") {
			t.Fatalf("expected warning to omit the search term, got %q", warning)
		}
	}
}

func TestResolveAnimeSearchesKnownMALIDOnce(t *testing.T) {
	attempts := 0
	client := &Client{
		anilistURL: testAnilistURL,
		http: &http.Client{
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				attempts++
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"data":{"Page":{"media":[]}}}`)),
					Header:     make(http.Header),
				}, nil
			}),
		},
		logger: api.NopLogger{},
	}

	result, err := client.ResolveAnime(t.Context(), "Example Anime", MetadataInput{
		Filename:  "Example.Anime.2026.S01.1080p.WEB-DL-GRP",
		MALManual: 20,
	})
	if err != nil {
		t.Fatalf("expected best-effort resolve to succeed, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected one MAL ID search, got %d", attempts)
	}
	if result.MALID != 20 {
		t.Fatalf("expected explicit MAL ID to be retained, got %d", result.MALID)
	}
}

func TestParseAniListRetryDelay(t *testing.T) {
	now := time.Date(2026, time.August, 15, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		retryAfter string
		reset      string
		want       time.Duration
	}{
		{
			name:       "delta seconds",
			retryAfter: "51",
			want:       51 * time.Second,
		},
		{
			name:       "http date",
			retryAfter: now.Add(20 * time.Second).Format(http.TimeFormat),
			want:       20 * time.Second,
		},
		{
			name:  "reset fallback",
			reset: strconv.FormatInt(now.Add(30*time.Second).Unix(), 10),
			want:  30 * time.Second,
		},
		{
			name:       "retry after precedence",
			retryAfter: "10",
			reset:      strconv.FormatInt(now.Add(30*time.Second).Unix(), 10),
			want:       10 * time.Second,
		},
		{
			name:       "invalid and past",
			retryAfter: "invalid",
			reset:      strconv.FormatInt(now.Add(-time.Second).Unix(), 10),
			want:       0,
		},
		{
			name:       "over budget",
			retryAfter: "120",
			want:       anilistRetryDelayOverBudget,
		},
		{
			name:  "reset over budget",
			reset: strconv.FormatInt(now.Add(120*time.Second).Unix(), 10),
			want:  anilistRetryDelayOverBudget,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := make(http.Header)
			if tt.retryAfter != "" {
				header.Set("Retry-After", tt.retryAfter)
			}
			if tt.reset != "" {
				header.Set("X-Ratelimit-Reset", tt.reset)
			}
			if got := parseAniListRetryDelay(header, now); got != tt.want {
				t.Fatalf("parseAniListRetryDelay() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestWaitForAniListRetryReturnsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := waitForAniListRetry(ctx, anilistMaxRetryWait)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForAniListRetry() error = %v, want context canceled", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type timeoutError struct {
	err error
}

func (e timeoutError) Error() string {
	return e.err.Error()
}

func (e timeoutError) Timeout() bool {
	return true
}

func (e timeoutError) Temporary() bool {
	return true
}
