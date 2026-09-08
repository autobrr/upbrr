// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imdb

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestPostGraphQLPostsUnseenHashThenGetsKnownHash(t *testing.T) {
	const query = `query TestOperation($id: ID!) { title(id: $id) { id } }`

	queryHash := sha256.Sum256([]byte(query))
	expectedHash := hex.EncodeToString(queryHash[:])
	var requestCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertIMDbGraphQLHeaders(t, r)

		switch requestCount.Add(1) {
		case 1:
			if r.Method != http.MethodPost {
				t.Errorf("expected initial POST, got %s", r.Method)
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
				if got := getStringFromMap(payload.Variables, "id"); got != "tt1234567" {
					t.Errorf("unexpected id variable %q", got)
				}
				if payload.Extensions.PersistedQuery.Version != 1 || payload.Extensions.PersistedQuery.SHA256Hash != expectedHash {
					t.Errorf("unexpected persisted query %#v", payload.Extensions.PersistedQuery)
				}
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"query":{"id":"tt1234567"}}}`))
		case 2:
			if r.Method != http.MethodGet {
				t.Errorf("expected known-hash GET, got %s", r.Method)
			}

			params := r.URL.Query()
			if got := params.Get("operationName"); got != "TestOperation" {
				t.Errorf("unexpected operation name %q", got)
			}
			if got := params.Get("query"); got != "" {
				t.Errorf("known-hash request included query %q", got)
			}
			var variables map[string]any
			if err := json.Unmarshal([]byte(params.Get("variables")), &variables); err != nil {
				t.Errorf("decode variables: %v", err)
			} else if got := getStringFromMap(variables, "id"); got != "tt7654321" {
				t.Errorf("unexpected id variable %q", got)
			}
			var extensions graphQLExtensions
			if err := json.Unmarshal([]byte(params.Get("extensions")), &extensions); err != nil {
				t.Errorf("decode extensions: %v", err)
			} else if extensions.PersistedQuery.Version != 1 || extensions.PersistedQuery.SHA256Hash != expectedHash {
				t.Errorf("unexpected persisted query %#v", extensions.PersistedQuery)
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"query":{"id":"tt7654321"}}}`))
		default:
			t.Errorf("unexpected extra request")
			http.Error(w, "unexpected request", http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	logger := &imdbTestLogger{}
	client := NewClient(server.Client(), logger)
	client.baseURL = server.URL

	var response map[string]any
	if err := client.postGraphQL(t.Context(), "TestOperation", query, map[string]any{"id": "tt1234567"}, &response); err != nil {
		t.Fatalf("first GraphQL request: %v", err)
	}
	if got := getString(response, "data", "query", "id"); got != "tt1234567" {
		t.Fatalf("unexpected title ID %q", got)
	}
	response = nil
	if err := client.postGraphQL(t.Context(), "TestOperation", query, map[string]any{"id": "tt7654321"}, &response); err != nil {
		t.Fatalf("second GraphQL request: %v", err)
	}
	if got := getString(response, "data", "query", "id"); got != "tt7654321" {
		t.Fatalf("unexpected title ID %q", got)
	}
	if got := requestCount.Load(); got != 2 {
		t.Fatalf("expected two requests, got %d", got)
	}
	if len(logger.debugMessages) != 0 {
		t.Fatalf("unexpected debug messages %#v", logger.debugMessages)
	}
}

func TestPostGraphQLReregistersKnownHashOnCacheMiss(t *testing.T) {
	const query = `query TestOperation($id: ID!) { title(id: $id) { id } }`
	queryHash := sha256.Sum256([]byte(query))
	expectedHash := hex.EncodeToString(queryHash[:])

	for _, statusCode := range []int{http.StatusOK, http.StatusBadRequest} {
		t.Run(fmt.Sprintf("http_%d", statusCode), func(t *testing.T) {
			var requestCount atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assertIMDbGraphQLHeaders(t, r)
				w.Header().Set("Content-Type", "application/json")

				switch requestCount.Add(1) {
				case 1:
					if r.Method != http.MethodGet {
						t.Errorf("expected cached GET, got %s", r.Method)
					}
					if statusCode != http.StatusOK {
						w.WriteHeader(statusCode)
					}
					_, _ = w.Write([]byte(`{"errors":[{"message":"PersistedQueryNotFound","extensions":{"code":"PERSISTED_QUERY_NOT_FOUND"}}]}`))
				case 2:
					if r.Method != http.MethodPost {
						t.Errorf("expected registration POST, got %s", r.Method)
					}
					var payload graphQLRequest
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Errorf("decode request payload: %v", err)
					} else {
						if payload.Query != query {
							t.Errorf("unexpected query %q", payload.Query)
						}
						if got := getStringFromMap(payload.Variables, "id"); got != "tt1234567" {
							t.Errorf("unexpected id variable %q", got)
						}
					}
					_, _ = w.Write([]byte(`{"data":{"query":{"id":"tt1234567"}}}`))
				default:
					t.Errorf("unexpected extra request")
					http.Error(w, "unexpected request", http.StatusInternalServerError)
				}
			}))
			defer server.Close()

			logger := &imdbTestLogger{}
			client := NewClient(server.Client(), logger)
			client.baseURL = server.URL
			cacheKey := persistedQueryCacheKey{endpoint: server.URL, hash: expectedHash}
			client.knownPersistedQueries.Store(cacheKey, struct{}{})

			var response map[string]any
			if err := client.postGraphQL(t.Context(), "TestOperation", query, map[string]any{"id": "tt1234567"}, &response); err != nil {
				t.Fatalf("GraphQL request: %v", err)
			}
			if got := getString(response, "data", "query", "id"); got != "tt1234567" {
				t.Fatalf("unexpected title ID %q", got)
			}
			if got := requestCount.Load(); got != 2 {
				t.Fatalf("expected two requests, got %d", got)
			}
			if _, ok := client.knownPersistedQueries.Load(cacheKey); !ok {
				t.Fatal("expected successful registration to restore cached hash")
			}
			if len(logger.debugMessages) != 1 ||
				logger.debugMessages[0] != "imdb: persisted query cache miss operation=TestOperation action=register" {
				t.Fatalf("unexpected debug messages %#v", logger.debugMessages)
			}
		})
	}
}

func TestPostGraphQLReturnsGraphQLErrors(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		if r.Method != http.MethodPost {
			t.Errorf("expected initial POST, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(
			`{"errors":[{"message":"request rejected","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`,
		))
	}))
	defer server.Close()

	client := NewClient(server.Client(), nil)
	client.baseURL = server.URL
	response := map[string]any{"sentinel": "unchanged"}

	const query = `query TestOperation { title { id } }`
	err := client.postGraphQL(t.Context(), "TestOperation", query, map[string]any{}, &response)
	if err == nil {
		t.Fatal("expected GraphQL error")
	}
	const expected = "imdb: GraphQL error operation=TestOperation code=GRAPHQL_VALIDATION_FAILED message=request rejected"
	if err.Error() != expected {
		t.Fatalf("unexpected error %q", err)
	}
	if _, ok := response["errors"]; ok {
		t.Fatal("GraphQL error response populated target")
	}
	if response["sentinel"] != "unchanged" {
		t.Fatal("GraphQL error response changed target")
	}
	if got := requestCount.Load(); got != 1 {
		t.Fatalf("expected one request, got %d", got)
	}
	queryHash := sha256.Sum256([]byte(query))
	cacheKey := persistedQueryCacheKey{endpoint: server.URL, hash: hex.EncodeToString(queryHash[:])}
	if _, ok := client.knownPersistedQueries.Load(cacheKey); ok {
		t.Fatal("GraphQL error marked query hash as known")
	}
}

func TestPostGraphQLDoesNotPostForOtherKnownQueryErrors(t *testing.T) {
	const query = `query TestOperation($id: ID!) { title(id: $id) { id } }`
	queryHash := sha256.Sum256([]byte(query))
	expectedHash := hex.EncodeToString(queryHash[:])

	for _, statusCode := range []int{http.StatusOK, http.StatusBadRequest} {
		t.Run(fmt.Sprintf("http_%d", statusCode), func(t *testing.T) {
			var requestCount atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestCount.Add(1)
				if r.Method != http.MethodGet {
					t.Errorf("expected known-hash GET, got %s", r.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				if statusCode != http.StatusOK {
					w.WriteHeader(statusCode)
				}
				_, _ = w.Write([]byte(`{"errors":[{"message":"bad query","extensions":null}]}`))
			}))
			defer server.Close()

			client := NewClient(server.Client(), nil)
			client.baseURL = server.URL
			cacheKey := persistedQueryCacheKey{endpoint: server.URL, hash: expectedHash}
			client.knownPersistedQueries.Store(cacheKey, struct{}{})

			var response map[string]any
			err := client.postGraphQL(t.Context(), "TestOperation", query, map[string]any{"id": "tt1234567"}, &response)
			if err == nil {
				t.Fatal("expected request error")
			}
			if statusCode == http.StatusOK && !strings.Contains(err.Error(), "GraphQL error operation=TestOperation message=bad query") {
				t.Fatalf("unexpected GraphQL error %q", err)
			}
			if statusCode == http.StatusBadRequest && !strings.Contains(err.Error(), "imdb: http 400:") {
				t.Fatalf("unexpected HTTP error %q", err)
			}
			if got := requestCount.Load(); got != 1 {
				t.Fatalf("expected one request, got %d", got)
			}
			if _, ok := client.knownPersistedQueries.Load(cacheKey); !ok {
				t.Fatal("unrelated error evicted known query hash")
			}
		})
	}
}

func TestRunSearchUsesStableGraphQLVariables(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertIMDbGraphQLHeaders(t, r)
		w.Header().Set("Content-Type", "application/json")

		var variables map[string]any
		switch requestCount.Add(1) {
		case 1:
			if r.Method != http.MethodPost {
				t.Errorf("expected initial POST, got %s", r.Method)
			}
			var payload graphQLRequest
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode request payload: %v", err)
			} else {
				if !strings.Contains(payload.Query, "query SearchTitles($constraints: AdvancedTitleSearchConstraints!)") {
					t.Errorf("unexpected search query %q", payload.Query)
				}
				if strings.Contains(payload.Query, "Example Release 2026") {
					t.Error("search query embedded the search term")
				}
				variables = payload.Variables
			}
		case 2:
			if r.Method != http.MethodGet {
				t.Errorf("expected known-hash GET, got %s", r.Method)
			}
			if got := r.URL.Query().Get("query"); got != "" {
				t.Errorf("known-hash request included query %q", got)
			}
			if err := json.Unmarshal([]byte(r.URL.Query().Get("variables")), &variables); err != nil {
				t.Errorf("decode variables: %v", err)
			}
		default:
			t.Errorf("unexpected extra request")
			http.Error(w, "unexpected request", http.StatusInternalServerError)
			return
		}

		if requestCount.Load() == 1 {
			if got := getStringFromMap(variables, "constraints", "titleTextConstraint", "searchTerm"); got != "Example Release 2026" {
				t.Errorf("unexpected search term %q", got)
			}
			if got := getStringFromMap(variables, "constraints", "releaseDateConstraint", "releaseDateRange", "start"); got != "2025-01-01" {
				t.Errorf("unexpected start date %q", got)
			}
			if got := getStringFromMap(variables, "constraints", "releaseDateConstraint", "releaseDateRange", "end"); got != "2027-12-31" {
				t.Errorf("unexpected end date %q", got)
			}
			if got := getIntFromMap(variables, "constraints", "runtimeConstraint", "runtimeRangeMinutes", "min"); got != 110 {
				t.Errorf("unexpected minimum runtime %d", got)
			}
			if got := getIntFromMap(variables, "constraints", "runtimeConstraint", "runtimeRangeMinutes", "max"); got != 130 {
				t.Errorf("unexpected maximum runtime %d", got)
			}
		} else {
			if got := getStringFromMap(variables, "constraints", "titleTextConstraint", "searchTerm"); got != "Example Alternate" {
				t.Errorf("unexpected search term %q", got)
			}
			constraints := getMapFromMap(variables, "constraints")
			if _, ok := constraints["releaseDateConstraint"]; ok {
				t.Error("wide search included release date constraint")
			}
			if _, ok := constraints["runtimeConstraint"]; ok {
				t.Error("wide search included runtime constraint")
			}
		}

		_, _ = w.Write([]byte(`{"data":{"advancedTitleSearch":{"edges":[]}}}`))
	}))
	defer server.Close()

	client := NewClient(server.Client(), nil)
	client.baseURL = server.URL
	client.runSearch(t.Context(), "Example Release 2026", 2026, "MOVIE", 120, false)
	client.runSearch(t.Context(), "Example Alternate", 2026, "MOVIE", 120, true)

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

type imdbTestLogger struct {
	api.NopLogger
	debugMessages []string
}

func (l *imdbTestLogger) Debugf(format string, args ...any) {
	l.debugMessages = append(l.debugMessages, fmt.Sprintf(format, args...))
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
