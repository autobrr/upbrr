// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

type candidateSearchStub struct {
	mu       sync.Mutex
	requests []api.ProviderSearchRequest
	years    []int
	search   func(context.Context) ([]api.ExternalIdentityCandidate, error)
}

func (s *candidateSearchStub) SearchCandidates(ctx context.Context, query string, year int, category api.CanonicalCategory) ([]api.ExternalIdentityCandidate, error) {
	s.mu.Lock()
	s.requests = append(s.requests, api.ProviderSearchRequest{Query: query, Category: category})
	s.years = append(s.years, year)
	s.mu.Unlock()
	if s.search != nil {
		return s.search(ctx)
	}
	return []api.ExternalIdentityCandidate{}, nil
}

type searchTMDB struct {
	*stubTMDB
	*candidateSearchStub
}
type searchIMDB struct {
	*stubIMDB
	*candidateSearchStub
}
type searchTVDB struct {
	*stubTVDB
	*candidateSearchStub
}

func TestProviderSearchProvenanceCacheAndMovieExclusion(t *testing.T) {
	tmdb, imdb, tvdb := &candidateSearchStub{}, &candidateSearchStub{}, &candidateSearchStub{}
	service := &Service{
		tmdb:   &searchTMDB{&stubTMDB{}, tmdb},
		imdb:   &searchIMDB{&stubIMDB{}, imdb},
		tvdb:   &searchTVDB{&stubTVDB{}, tvdb},
		tvmaze: &stubTVmaze{},
	}
	request := api.ProviderSearchRequest{Query: "  Example   Series 2025 ", Category: "TV"}
	for range 2 {
		result, err := service.SearchProviderCandidates(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		if result.Query != "Example Series 2025" || len(result.Providers) != 3 {
			t.Fatalf("submitted provenance=%+v", result)
		}
		for _, provider := range result.Providers {
			if provider.Query != "Example Series" || provider.Year != 2025 || provider.Status != api.MetadataEvidenceStatusComplete || provider.Candidates == nil {
				t.Fatalf("effective provenance=%+v", provider)
			}
		}
	}
	for _, provider := range []*candidateSearchStub{tmdb, imdb, tvdb} {
		if len(provider.requests) != 1 || provider.requests[0].Query != "Example Series" || provider.years[0] != 2025 {
			t.Fatalf("cache/query=%+v", provider.requests)
		}
	}
	request.Category = api.CanonicalCategoryMovie
	result, err := service.SearchProviderCandidates(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Providers) != 2 || len(tvdb.requests) != 1 || len(tmdb.requests) != 2 {
		t.Fatal("media category reused the wrong provider cache")
	}
	request.Query = "Example Series 2024"
	if _, err := service.SearchProviderCandidates(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if len(tmdb.requests) != 3 || tmdb.years[2] != 2024 {
		t.Fatal("year filter reused stale results")
	}
}

func TestProviderSearchPartialAndFailedProvidersRemainDistinctAndRetryable(t *testing.T) {
	tmdb := &candidateSearchStub{search: func(context.Context) ([]api.ExternalIdentityCandidate, error) {
		return []api.ExternalIdentityCandidate{{ID: 1, DetailsStatus: api.MetadataEvidenceStatusPartial}}, nil
	}}
	imdb := &candidateSearchStub{search: func(context.Context) ([]api.ExternalIdentityCandidate, error) {
		return nil, errors.New("synthetic failure")
	}}
	service := &Service{
		tmdb:   &searchTMDB{&stubTMDB{}, tmdb},
		imdb:   &searchIMDB{&stubIMDB{}, imdb},
		tvdb:   &stubTVDB{},
		tvmaze: &stubTVmaze{},
	}
	for range 2 {
		result, err := service.SearchProviderCandidates(t.Context(), api.ProviderSearchRequest{Query: "Example", Category: api.CanonicalCategoryMovie})
		if err != nil {
			t.Fatal(err)
		}
		if result.Providers[0].Status != api.MetadataEvidenceStatusPartial || len(result.Providers[0].Candidates) != 1 || result.Providers[1].Status != api.MetadataEvidenceStatusUnavailable {
			t.Fatalf("outcomes=%+v", result)
		}
	}
	if len(tmdb.requests) != 2 || len(imdb.requests) != 2 {
		t.Fatal("partial/failing results suppressed an explicit retry")
	}
}

func TestProviderSearchQueuedCancellationDoesNotRunOrCache(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	tmdb := &candidateSearchStub{search: func(ctx context.Context) ([]api.ExternalIdentityCandidate, error) {
		close(started)
		select {
		case <-release:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	service := &Service{
		tmdb:   &searchTMDB{&stubTMDB{}, tmdb},
		imdb:   &searchIMDB{&stubIMDB{}, &candidateSearchStub{}},
		tvdb:   &stubTVDB{},
		tvmaze: &stubTVmaze{},
	}
	request := api.ProviderSearchRequest{Query: "Example", Category: api.CanonicalCategoryMovie}
	finished := make(chan error, 1)
	go func() { _, err := service.SearchProviderCandidates(t.Context(), request); finished <- err }()
	<-started
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.SearchProviderCandidates(ctx, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled queued search=%v", err)
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if len(tmdb.requests) != 1 {
		t.Fatal("canceled search reached provider")
	}
}
