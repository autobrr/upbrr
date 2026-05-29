// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tmdb

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestAniListSearchRetriesTimeouts(t *testing.T) {
	attempts := 0
	client := &Client{
		http: &http.Client{
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				attempts++
				if attempts < 3 {
					return nil, timeoutError{err: errors.New("timeout")}
				}
				body := `{"data":{"Page":{"media":[{"id":1,"idMal":20,"title":{"romaji":"Test","english":"Test"},"seasonYear":"2024","episodes":12,"tags":[{"name":"Shounen"}]}]}}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(body)),
					Header:     make(http.Header),
				}, nil
			}),
		},
		logger: api.NopLogger{},
	}

	items, err := client.anilistSearch(context.Background(), "Test", 0)
	if err != nil {
		t.Fatalf("expected success after retry, got error: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
	if len(items) != 1 || items[0].IDMal != 20 {
		t.Fatalf("expected one AniList result with MAL id 20, got %+v", items)
	}
}

func TestAniListSearchDoesNotRetryNonTimeoutErrors(t *testing.T) {
	attempts := 0
	client := &Client{
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
