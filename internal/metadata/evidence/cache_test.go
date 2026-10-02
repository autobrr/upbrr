// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package evidence

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

type memoryEvidenceStore struct {
	mu      sync.Mutex
	records map[[4]string]api.MetadataEvidence
	loadErr error
	saveErr error
}

func (s *memoryEvidenceStore) LoadMetadataEvidence(_ context.Context, source, fingerprint, domain, key string) (api.MetadataEvidence, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return api.MetadataEvidence{}, s.loadErr
	}
	record, ok := s.records[[4]string{source, fingerprint, domain, key}]
	if !ok {
		return api.MetadataEvidence{}, internalerrors.ErrNotFound
	}
	return record, nil
}

func (s *memoryEvidenceStore) SaveMetadataEvidence(_ context.Context, record api.MetadataEvidence) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return s.saveErr
	}
	if s.records == nil {
		s.records = make(map[[4]string]api.MetadataEvidence)
	}
	s.records[[4]string{record.SourcePath, record.SourceFingerprint, record.Domain, record.Key}] = record
	return nil
}

func TestLookupSuccessReplaysDetachedEvidenceAcrossScopes(t *testing.T) {
	t.Parallel()
	store := &memoryEvidenceStore{}
	source := filepath.Join(t.TempDir(), "Example.Release.mkv")
	calls := 0
	fetch := func() (map[string][]string, error) {
		calls++
		return map[string][]string{"titles": {fmt.Sprintf("Provider title %d", calls)}}, nil
	}
	ctx, _ := WithScope(t.Context(), store, source, "fingerprint", api.ExternalFreshnessLoad, nil)
	first, err := Lookup(ctx, "provider.metadata", 11, fetch, nil)
	if err != nil {
		t.Fatal(err)
	}
	first["titles"][0] = "User correction"
	for _, freshness := range []api.ExternalFreshness{api.ExternalFreshnessReuse, api.ExternalFreshnessLoad} {
		ctx, scope := WithScope(t.Context(), store, source, "fingerprint", freshness, nil)
		for range 2 {
			got, err := Lookup(ctx, "provider.metadata", 11, fetch, nil)
			if err != nil || got["titles"][0] != "Provider title 1" || calls != 1 {
				t.Fatalf("replay = %v, %v; calls = %d", got, err, calls)
			}
			got["titles"][0] = "Another correction"
		}
		if err := scope.Err(); err != nil {
			t.Fatal(err)
		}
	}
	ctx, _ = WithScope(t.Context(), store, source, "fingerprint", api.ExternalFreshnessRefresh, nil)
	for range 2 {
		got, err := Lookup(ctx, "provider.metadata", 11, fetch, nil)
		if err != nil || got["titles"][0] != "Provider title 2" || calls != 2 {
			t.Fatalf("refresh = %v, %v; calls = %d", got, err, calls)
		}
	}
}

func TestLookupEmptyRetriesOncePerNewLoad(t *testing.T) {
	t.Parallel()
	store := &memoryEvidenceStore{}
	source := filepath.Join(t.TempDir(), "Example.Release.mkv")
	calls := 0
	fetch := func() ([]string, error) { calls++; return []string{}, nil }
	empty := func(value []string) bool { return len(value) == 0 }
	passes := []struct {
		freshness api.ExternalFreshness
		wantCalls int
	}{
		{freshness: api.ExternalFreshnessLoad, wantCalls: 1},
		{freshness: api.ExternalFreshnessReuse, wantCalls: 1},
		{freshness: api.ExternalFreshnessLoad, wantCalls: 2},
		{freshness: api.ExternalFreshnessReuse, wantCalls: 2},
		{freshness: api.ExternalFreshnessRefresh, wantCalls: 3},
	}
	for _, pass := range passes {
		ctx, _ := WithScope(t.Context(), store, source, "fingerprint", pass.freshness, nil)
		for range 3 {
			got, err := Lookup(ctx, "provider.search", "Example Release", fetch, empty)
			if err != nil || got == nil || len(got) != 0 || calls != pass.wantCalls {
				t.Fatalf("freshness %q: result = %#v, %v; calls = %d, want %d", pass.freshness, got, err, calls, pass.wantCalls)
			}
		}
	}
}

func TestLookupFailureRetainedUntilHistoryPurge(t *testing.T) {
	t.Parallel()
	store := &memoryEvidenceStore{}
	source := filepath.Join(t.TempDir(), "Example.Release.mkv")
	providerErr := errors.New("provider rejected synthetic-secret-token")
	calls := 0
	fetch := func() (string, error) { calls++; return "", providerErr }
	ctx, _ := WithScope(t.Context(), store, source, "fingerprint", api.ExternalFreshnessLoad, nil)
	if _, err := Lookup(ctx, "provider.metadata", "synthetic-secret-token", fetch, nil); !errors.Is(err, providerErr) {
		t.Fatalf("initial failure = %v", err)
	}
	for _, freshness := range []api.ExternalFreshness{api.ExternalFreshnessReuse, api.ExternalFreshnessLoad, api.ExternalFreshnessRefresh} {
		ctx, _ := WithScope(t.Context(), store, source, "fingerprint", freshness, nil)
		if _, err := Lookup(ctx, "provider.metadata", "synthetic-secret-token", fetch, nil); !errors.Is(err, ErrSuppressed) {
			t.Fatalf("freshness %q failure = %v, want suppressed", freshness, err)
		}
	}
	if calls != 1 {
		t.Fatalf("failed lookup retried %d times", calls)
	}
	for _, record := range store.records {
		if record.Outcome != api.MetadataEvidenceFailed || len(record.Payload) != 0 {
			t.Fatalf("failed record contains provider result: %#v", record)
		}
		if decoded, err := hex.DecodeString(record.Key); err != nil || len(decoded) != 32 {
			t.Fatalf("query key = %q, want SHA-256 digest", record.Key)
		}
		encoded, err := json.Marshal(record)
		if err != nil || strings.Contains(string(encoded), "synthetic-secret-token") {
			t.Fatalf("record leaked query/error: %s, %v", encoded, err)
		}
	}
	clear(store.records)
	ctx, _ = WithScope(t.Context(), store, source, "fingerprint", api.ExternalFreshnessLoad, nil)
	if _, err := Lookup(ctx, "provider.metadata", "synthetic-secret-token", fetch, nil); !errors.Is(err, providerErr) || calls != 2 {
		t.Fatalf("lookup after purge = %v; calls = %d", err, calls)
	}
}

func TestLookupDependenciesAreIndependent(t *testing.T) {
	t.Parallel()
	store := &memoryEvidenceStore{}
	source := filepath.Join(t.TempDir(), "Example.Release.mkv")
	other := filepath.Join(t.TempDir(), "Other.Release.mkv")
	calls := 0
	fetch := func() (int, error) { calls++; return calls, nil }
	queries := []struct {
		source      string
		fingerprint string
		domain      string
		query       int
	}{
		{
			source:      source,
			fingerprint: "original",
			domain:      "provider.metadata",
			query:       11,
		},
		{
			source:      source,
			fingerprint: "original",
			domain:      "provider.metadata",
			query:       12,
		},
		{
			source:      source,
			fingerprint: "changed",
			domain:      "provider.metadata",
			query:       11,
		},
		{
			source:      other,
			fingerprint: "original",
			domain:      "provider.metadata",
			query:       11,
		},
		{
			source:      source,
			fingerprint: "original",
			domain:      "provider.episode",
			query:       11,
		},
	}
	for range 2 {
		for i, query := range queries {
			ctx, _ := WithScope(t.Context(), store, query.source, query.fingerprint, api.ExternalFreshnessReuse, nil)
			got, err := Lookup(ctx, query.domain, query.query, fetch, nil)
			if err != nil || got != i+1 {
				t.Fatalf("dependency case %d = %d, %v", i, got, err)
			}
		}
	}
	if calls != len(queries) {
		t.Fatalf("calls = %d, want %d independent queries", calls, len(queries))
	}
}

func TestLookupCancellationIsNeverRetained(t *testing.T) {
	t.Parallel()
	for _, canceled := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(canceled.Error(), func(t *testing.T) {
			store := &memoryEvidenceStore{}
			ctx, scope := WithScope(t.Context(), store, filepath.Join(t.TempDir(), "Example.Release.mkv"), "fingerprint", api.ExternalFreshnessReuse, nil)
			calls := 0
			fetch := func() (string, error) {
				calls++
				if calls == 1 {
					return "", fmt.Errorf("provider interrupted: %w", canceled)
				}
				return "Provider title", nil
			}
			if _, err := Lookup(ctx, "provider.metadata", 11, fetch, nil); !errors.Is(err, canceled) {
				t.Fatalf("interrupted lookup = %v", err)
			}
			if len(store.records) != 0 || scope.Err() != nil {
				t.Fatalf("interruption persisted: records=%d, scope error=%v", len(store.records), scope.Err())
			}
			got, err := Lookup(ctx, "provider.metadata", 11, fetch, nil)
			if err != nil || got != "Provider title" || calls != 2 {
				t.Fatalf("retry after interruption = %q, %v; calls = %d", got, err, calls)
			}
		})
	}
}

func TestLookupCanceledRefreshPreservesPriorEvidence(t *testing.T) {
	t.Parallel()
	store := &memoryEvidenceStore{}
	source := filepath.Join(t.TempDir(), "Example.Release.mkv")
	ctx, _ := WithScope(t.Context(), store, source, "fingerprint", api.ExternalFreshnessLoad, nil)
	if _, err := Lookup(ctx, "provider.metadata", 11, func() (string, error) { return "Provider title", nil }, nil); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx, _ = WithScope(canceled, store, source, "fingerprint", api.ExternalFreshnessRefresh, nil)
	_, _ = Lookup(ctx, "provider.metadata", 11, func() (string, error) {
		cancel()
		return "Incomplete title", nil
	}, nil)
	ctx, _ = WithScope(t.Context(), store, source, "fingerprint", api.ExternalFreshnessReuse, nil)
	got, err := Lookup(ctx, "provider.metadata", 11, func() (string, error) {
		t.Fatal("canceled refresh discarded successful evidence")
		return "", nil
	}, nil)
	if err != nil || got != "Provider title" {
		t.Fatalf("retained result = %q, %v", got, err)
	}
}

func TestLookupPersistenceFailuresReachScope(t *testing.T) {
	t.Parallel()
	storageErr := errors.New("storage unavailable")
	for _, operation := range []string{"load", "save", "encode"} {
		t.Run(operation, func(t *testing.T) {
			store := &memoryEvidenceStore{}
			source := filepath.Join(t.TempDir(), "Example.Release.mkv")
			ctx, scope := WithScope(t.Context(), store, source, "fingerprint", api.ExternalFreshnessReuse, nil)
			fetch := func() (any, error) { return "Provider title", nil }
			switch operation {
			case "load":
				store.loadErr = storageErr
			case "save":
				store.saveErr = storageErr
			case "encode":
				fetch = func() (any, error) { return make(chan int), nil }
			}
			_, err := Lookup(ctx, "provider.metadata", 11, fetch, nil)
			if err == nil || scope.Err() == nil || !errors.Is(err, scope.Err()) {
				t.Fatalf("%s failure = %v, scope error = %v", operation, err, scope.Err())
			}
			if (operation == "load" || operation == "save") && !errors.Is(scope.Err(), storageErr) {
				t.Fatalf("storage error lost: %v", scope.Err())
			}
		})
	}
}

func TestAttemptRetriesSuccessfulArtifactButRetainsFailure(t *testing.T) {
	t.Parallel()
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprintf("failed=%t", failed), func(t *testing.T) {
			store := &memoryEvidenceStore{}
			source := filepath.Join(t.TempDir(), "Example.Release.mkv")
			ctx, _ := WithScope(t.Context(), store, source, "fingerprint", api.ExternalFreshnessLoad, nil)
			calls := 0
			providerErr := errors.New("artifact unavailable")
			fetch := func() error {
				calls++
				if failed {
					return providerErr
				}
				return nil
			}
			first := Attempt(ctx, "tracker.artifact", 11, fetch)
			if failed && !errors.Is(first, providerErr) || !failed && first != nil {
				t.Fatalf("first artifact attempt = %v", first)
			}
			ctx, _ = WithScope(t.Context(), store, source, "fingerprint", api.ExternalFreshnessRefresh, nil)
			second := Attempt(ctx, "tracker.artifact", 11, fetch)
			if failed {
				if !errors.Is(second, ErrSuppressed) || calls != 1 {
					t.Fatalf("failed artifact attempt = %v; calls = %d", second, calls)
				}
			} else if second != nil || calls != 2 {
				t.Fatalf("missing successful artifact attempt = %v; calls = %d", second, calls)
			}
		})
	}
}

func TestLookupWithoutRepositoryPreservesDirectClient(t *testing.T) {
	t.Parallel()
	ctx, scope := WithScope(t.Context(), nil, "", "", api.ExternalFreshnessReuse, nil)
	if Enabled(ctx) || Enabled(t.Context()) {
		t.Fatal("evidence enabled without a repository")
	}
	calls := 0
	for _, ctx := range []context.Context{ctx, t.Context()} {
		got, err := Lookup(ctx, "provider.metadata", 11, func() ([]string, error) { calls++; return []string{"Provider title"}, nil }, nil)
		if err != nil || !reflect.DeepEqual(got, []string{"Provider title"}) {
			t.Fatalf("direct result = %v, %v", got, err)
		}
	}
	if calls != 2 || scope.Err() != nil {
		t.Fatalf("direct calls = %d, scope error = %v", calls, scope.Err())
	}
}

func TestLookupCoalescesConcurrentQueries(t *testing.T) {
	t.Parallel()
	store := &memoryEvidenceStore{}
	ctx, _ := WithScope(t.Context(), store, filepath.Join(t.TempDir(), "Example.Release.mkv"), "fingerprint", api.ExternalFreshnessLoad, nil)
	var calls atomic.Int32
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			<-start
			got, err := Lookup(ctx, "provider.metadata", 11, func() (string, error) {
				calls.Add(1)
				time.Sleep(20 * time.Millisecond)
				return "Provider title", nil
			}, nil)
			if err != nil || got != "Provider title" {
				t.Errorf("concurrent result = %q, %v", got, err)
			}
		})
	}
	close(start)
	workers.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("concurrent identical requests = %d, want 1", got)
	}
}

func TestLookupUnrelatedQueriesRemainConcurrent(t *testing.T) {
	t.Parallel()
	store := &memoryEvidenceStore{}
	ctx, _ := WithScope(t.Context(), store, filepath.Join(t.TempDir(), "Example.Release.mkv"), "fingerprint", api.ExternalFreshnessLoad, nil)
	started := make(chan struct{})
	release := make(chan struct{})
	var worker sync.WaitGroup
	worker.Go(func() {
		_, err := Lookup(ctx, "provider.metadata", 11, func() (string, error) {
			close(started)
			<-release
			return "First title", nil
		}, nil)
		if err != nil {
			t.Errorf("first lookup: %v", err)
		}
	})
	<-started
	finished := make(chan error, 1)
	worker.Go(func() {
		_, err := Lookup(ctx, "provider.metadata", 12, func() (string, error) { return "Second title", nil }, nil)
		finished <- err
	})
	select {
	case err := <-finished:
		if err != nil {
			t.Errorf("independent lookup: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("independent query blocked behind a different key")
	}
	close(release)
	worker.Wait()
}

func TestJSONDistinguishesEmptyEnvelopesFromPartialFacts(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		body  string
		empty bool
	}{
		{
			name:  "null",
			body:  `null`,
			empty: true,
		},
		{
			name:  "empty search",
			body:  `{"page":1,"results":[],"total_results":0,"total_pages":1}`,
			empty: true,
		},
		{
			name:  "empty artwork",
			body:  `{"id":11,"logos":[],"backdrops":[],"posters":[]}`,
			empty: true,
		},
		{
			name:  "empty TVDB data",
			body:  `{"status":"success","data":[]}`,
			empty: true,
		},
		{
			name:  "empty IMDb title",
			body:  `{"data":{"title":null}}`,
			empty: true,
		},
		{
			name:  "empty IMDb edges",
			body:  `{"data":{"search":{"edges":[],"pageInfo":{"hasNextPage":false}}}}`,
			empty: true,
		},
		{name: "one candidate", body: `{"page":1,"results":[{"id":11,"title":"Example Movie"}]}`},
		{name: "season without episodes", body: `{"id":11,"name":"Season One","episodes":[]}`},
		{name: "person without credits", body: `{"name":"Example Person","cast":[]}`},
		{name: "episode without crew", body: `{"name":"Example Episode","crew":[]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &memoryEvidenceStore{}
			source := filepath.Join(t.TempDir(), "Example.Release.mkv")
			calls := 0
			for range 2 {
				ctx, _ := WithScope(t.Context(), store, source, "fingerprint", api.ExternalFreshnessLoad, nil)
				var target map[string]any
				if err := JSON(ctx, "provider.response", 11, &target, nil, nil, func() error {
					calls++
					return json.Unmarshal([]byte(test.body), &target)
				}); err != nil {
					t.Fatal(err)
				}
			}
			wantCalls := 1
			if test.empty {
				wantCalls = 2
			}
			if calls != wantCalls {
				t.Fatalf("response %s: requests=%d, want %d", test.body, calls, wantCalls)
			}
		})
	}
}

func TestJSONSeparatesWireProjectionsOfSameEndpoint(t *testing.T) {
	t.Parallel()
	store := &memoryEvidenceStore{}
	ctx, _ := WithScope(t.Context(), store, filepath.Join(t.TempDir(), "Example.Release.mkv"), "fingerprint", api.ExternalFreshnessReuse, nil)
	type titleResponse struct{ Title string }
	type seasonsResponse struct{ Seasons []int }
	calls := 0
	for range 2 {
		var title titleResponse
		if err := JSON(ctx, "provider.response", 11, &title, nil, nil, func() error {
			calls++
			title.Title = "Example Series"
			return nil
		}); err != nil || title.Title != "Example Series" {
			t.Fatalf("title projection = %#v, %v", title, err)
		}
		var seasons seasonsResponse
		if err := JSON(ctx, "provider.response", 11, &seasons, nil, nil, func() error {
			calls++
			seasons.Seasons = []int{1, 2}
			return nil
		}); err != nil || !reflect.DeepEqual(seasons.Seasons, []int{1, 2}) {
			t.Fatalf("season projection = %#v, %v", seasons, err)
		}
	}
	if calls != 2 {
		t.Fatalf("projection requests = %d, want 2", calls)
	}
}

func TestLookupCanceledWaiterDoesNotFetch(t *testing.T) {
	t.Parallel()
	store := &memoryEvidenceStore{}
	ctx, _ := WithScope(t.Context(), store, filepath.Join(t.TempDir(), "Example.Release.mkv"), "fingerprint", api.ExternalFreshnessReuse, nil)
	started := make(chan struct{})
	release := make(chan struct{})
	var worker sync.WaitGroup
	worker.Go(func() {
		_, err := Lookup(ctx, "provider.metadata", 11, func() (string, error) {
			close(started)
			<-release
			return "Example Movie", nil
		}, nil)
		if err != nil {
			t.Errorf("first query: %v", err)
		}
	})
	<-started
	waiting, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	_, err := Lookup(waiting, "provider.metadata", 11, func() (string, error) {
		t.Error("canceled waiter made another provider request")
		return "", nil
	}, nil)
	close(release)
	worker.Wait()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting query = %v, want deadline exceeded", err)
	}
}

func TestProviderRefreshRetainsIndependentSourceEvidence(t *testing.T) {
	for _, domain := range []string{"provider.metadata", "scene.word", "bluray.page", "tracker.lookup"} {
		t.Run(domain, func(t *testing.T) {
			store := &memoryEvidenceStore{}
			calls := 0
			fetch := func() (string, error) { calls++; return "retained fact", nil }
			source := filepath.Join(t.TempDir(), "Example.Movie.mkv")
			for _, mode := range []api.ExternalFreshness{api.ExternalFreshnessLoad, api.ExternalFreshnessRefresh} {
				ctx, _ := WithScope(t.Context(), store, source, "fingerprint", mode, nil)
				if _, err := Lookup(ctx, domain, "query", fetch, nil); err != nil {
					t.Fatal(err)
				}
			}
			want := 1
			if domain == "provider.metadata" {
				want = 2
			}
			if calls != want {
				t.Fatalf("domain=%s calls=%d want=%d", domain, calls, want)
			}
		})
	}
}

func TestLookupUnreadableEvidenceDoesNotPoisonScope(t *testing.T) {
	t.Parallel()
	for _, outcome := range []api.MetadataEvidenceOutcome{api.MetadataEvidenceSuccess, api.MetadataEvidenceEmpty, api.MetadataEvidenceFailed} {
		t.Run(string(outcome), func(t *testing.T) {
			for _, payload := range []string{"{", `{"unexpected":"shape"}`} {
				t.Run(payload, func(t *testing.T) {
					store := &memoryEvidenceStore{}
					source := filepath.Join(t.TempDir(), "Example.Release.mkv")
					key, err := queryKey(11)
					if err != nil {
						t.Fatal(err)
					}
					if err := store.SaveMetadataEvidence(t.Context(), api.MetadataEvidence{
						SourcePath:        source,
						SourceFingerprint: "fingerprint",
						Domain:            "provider.metadata",
						Key:               key,
						Outcome:           outcome,
						Payload:           json.RawMessage(payload),
					}); err != nil {
						t.Fatal(err)
					}
					calls := 0
					fetch := func() (string, error) { calls++; return "Provider title", nil }
					for _, freshness := range []api.ExternalFreshness{api.ExternalFreshnessReuse, api.ExternalFreshnessLoad, api.ExternalFreshnessRefresh} {
						ctx, scope := WithScope(t.Context(), store, source, "fingerprint", freshness, nil)
						for range 2 {
							got, err := Lookup(ctx, "provider.metadata", 11, fetch, nil)
							if outcome == api.MetadataEvidenceFailed {
								if got != "" || !errors.Is(err, ErrSuppressed) || calls != 0 {
									t.Fatalf("failed evidence = %q, %v; calls = %d", got, err, calls)
								}
							} else {
								wantCalls := 1
								if freshness == api.ExternalFreshnessRefresh {
									wantCalls = 2
								}
								if got != "Provider title" || err != nil || calls != wantCalls {
									t.Fatalf("recovered evidence = %q, %v; calls = %d, want %d", got, err, calls, wantCalls)
								}
							}
							if err := scope.Err(); err != nil {
								t.Fatalf("unreadable evidence poisoned collection: %v", err)
							}
						}
					}
				})
			}
		})
	}
}

func TestProviderRefreshRetainsIndependentEmptyEvidence(t *testing.T) {
	t.Parallel()
	for _, domain := range []string{"scene.word", "bluray.page", "tracker.lookup"} {
		t.Run(domain, func(t *testing.T) {
			store := &memoryEvidenceStore{}
			source := filepath.Join(t.TempDir(), "Example.Movie.mkv")
			calls := 0
			fetch := func() ([]string, error) { calls++; return []string{}, nil }
			for index, freshness := range []api.ExternalFreshness{api.ExternalFreshnessLoad, api.ExternalFreshnessReuse, api.ExternalFreshnessRefresh, api.ExternalFreshnessLoad} {
				ctx, scope := WithScope(t.Context(), store, source, "fingerprint", freshness, nil)
				for range 2 {
					got, err := Lookup(ctx, domain, "query", fetch, func(value []string) bool { return len(value) == 0 })
					wantCalls := 1
					if index == 3 {
						wantCalls = 2
					}
					if len(got) != 0 || err != nil || calls != wantCalls || scope.Err() != nil {
						t.Fatalf("freshness=%s result=%v error=%v calls=%d want=%d scope=%v", freshness, got, err, calls, wantCalls, scope.Err())
					}
				}
			}
		})
	}
}
