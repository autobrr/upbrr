// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestPostGraphQLRegistersPersistedQueryOnCacheMiss(t *testing.T) {
	const query = `query TestOperation { title(id: "tt1234567") { id } }`

	queryHash := sha256.Sum256([]byte(query))
	expectedHash := hex.EncodeToString(queryHash[:])
	var requestCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertIMDbGraphQLHeaders(t, r)

		switch requestCount.Add(1) {
		case 1:
			if r.Method != http.MethodGet {
				t.Errorf("expected initial GET, got %s", r.Method)
			}
			params := r.URL.Query()
			if got := params.Get("operationName"); got != "TestOperation" {
				t.Errorf("unexpected operation name %q", got)
			}
			if got := params.Get("variables"); got != "{}" {
				t.Errorf("unexpected variables %q", got)
			}
			if got := params.Get("query"); got != "" {
				t.Errorf("initial request included query %q", got)
			}

			var extensions graphQLExtensions
			if err := json.Unmarshal([]byte(params.Get("extensions")), &extensions); err != nil {
				t.Errorf("decode extensions: %v", err)
			} else if extensions.PersistedQuery.Version != 1 || extensions.PersistedQuery.SHA256Hash != expectedHash {
				t.Errorf("unexpected persisted query %#v", extensions.PersistedQuery)
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errors":[{"message":"PersistedQueryNotFound","extensions":{"code":"PERSISTED_QUERY_NOT_FOUND"}}]}`))
		case 2:
			if r.Method != http.MethodPost {
				t.Errorf("expected registration POST, got %s", r.Method)
			}

			var payload graphQLRequest
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode request payload: %v", err)
			} else {
				if payload.OperationName != "TestOperation" {
					t.Errorf("unexpected operation name %q", payload.OperationName)
				}
				if payload.Query != query {
					t.Errorf("unexpected query %q", payload.Query)
				}
				if len(payload.Variables) != 0 {
					t.Errorf("unexpected variables %#v", payload.Variables)
				}
				if payload.Extensions.PersistedQuery.Version != 1 || payload.Extensions.PersistedQuery.SHA256Hash != expectedHash {
					t.Errorf("unexpected persisted query %#v", payload.Extensions.PersistedQuery)
				}
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"title":{"id":"tt1234567"}}}`))
		default:
			t.Errorf("unexpected extra request")
			http.Error(w, "unexpected request", http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	client := NewClient(server.Client(), nil)
	client.baseURL = server.URL

	var response map[string]any
	if err := client.postGraphQL(context.Background(), "TestOperation", query, &response); err != nil {
		t.Fatalf("post GraphQL: %v", err)
	}
	if got := getString(response, "data", "title", "id"); got != "tt1234567" {
		t.Fatalf("unexpected title ID %q", got)
	}
	if got := requestCount.Load(); got != 2 {
		t.Fatalf("expected two requests, got %d", got)
	}
}

func assertIMDbGraphQLHeaders(t *testing.T, request *http.Request) {
	t.Helper()

	expected := map[string]string{
		"Accept":              "application/graphql+json, application/json",
		"Content-Type":        "application/json",
		"Origin":              imdbOrigin,
		"X-Imdb-Client-Name":  imdbClientName,
		"X-Imdb-User-Country": imdbUserCountry,
	}
	for name, want := range expected {
		if got := request.Header.Get(name); got != want {
			t.Errorf("header %s = %q, want %q", name, got, want)
		}
	}
}

func TestRankCandidates(t *testing.T) {
	results := []map[string]any{
		{
			"node": map[string]any{
				"title": map[string]any{
					"id":          "tt0000001",
					"titleText":   map[string]any{"text": "Example Title"},
					"releaseYear": map[string]any{"year": 2020},
					"titleType":   map[string]any{"text": "Movie"},
					"plot":        map[string]any{"plotText": map[string]any{"plainText": "Plot"}},
				},
			},
		},
		{
			"node": map[string]any{
				"title": map[string]any{
					"id":          "tt0000002",
					"titleText":   map[string]any{"text": "Other Title"},
					"releaseYear": map[string]any{"year": 2020},
					"titleType":   map[string]any{"text": "Movie"},
					"plot":        map[string]any{"plotText": map[string]any{"plainText": "Plot 2"}},
				},
			},
		},
	}
	candidates := rankCandidates(results, "Example Title", 2020)
	if len(candidates) == 0 {
		t.Fatalf("expected candidates")
	}
	if candidates[0].IMDbID != 1 {
		t.Fatalf("expected IMDbID 1, got %d", candidates[0].IMDbID)
	}
}
