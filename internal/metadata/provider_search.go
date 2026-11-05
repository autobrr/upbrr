// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/autobrr/upbrr/pkg/api"
)

var providerQueryYear = regexp.MustCompile(`^(.+?)\s+\(?(18\d{2}|19\d{2}|20\d{2}|21\d{2})\)?$`)

type candidateSearcher interface {
	SearchCandidates(context.Context, string, int, api.CanonicalCategory) ([]api.ExternalIdentityCandidate, error)
}

type providerSearchCacheEntry struct {
	result  api.ProviderSearchResult
	expires time.Time
}

// SearchProviderCandidates inspects providers without reading or writing release facts or corrections.
// Requests use one provider search page and at most one lightweight detail request per returned candidate.
// Searches are serialized per service, cancelable while queued, and successful results are reused for five minutes.
func (s *Service) SearchProviderCandidates(ctx context.Context, request api.ProviderSearchRequest) (api.ProviderSearchResult, error) {
	request.Category, _ = api.NormalizeCanonicalCategory(string(request.Category))
	request.Query = strings.Join(strings.Fields(request.Query), " ")
	if request.Query == "" || len(request.Query) > 512 || request.Category != api.CanonicalCategoryMovie && request.Category != api.CanonicalCategoryTV {
		return api.ProviderSearchResult{}, errors.New("metadata: provide a title of at most 512 bytes and a movie or TV category")
	}
	s.providerSearchInit.Do(func() {
		s.providerSearchGate = make(chan struct{}, 1)
		s.providerSearchCache = make(map[api.ProviderSearchRequest]providerSearchCacheEntry)
	})
	select {
	case s.providerSearchGate <- struct{}{}:
		defer func() { <-s.providerSearchGate }()
	case <-ctx.Done():
		return api.ProviderSearchResult{}, fmt.Errorf("provider search canceled: %w", ctx.Err())
	}
	if err := ctx.Err(); err != nil {
		return api.ProviderSearchResult{}, fmt.Errorf("provider search: %w", err)
	}
	if cached, ok := s.providerSearchCache[request]; ok && time.Now().Before(cached.expires) {
		return cached.result, nil
	}
	query, year := request.Query, 0
	if match := providerQueryYear.FindStringSubmatch(query); len(match) > 0 {
		query = strings.TrimSpace(match[1])
		year, _ = strconv.Atoi(match[2])
	}
	tmdbClient, _, imdbClient, tvdbClient, _ := s.ensureExternalClients(ctx)
	providers := []struct {
		name   api.IdentityProvider
		client any
	}{{api.IdentityProviderTMDB, tmdbClient}, {api.IdentityProviderIMDB, imdbClient}}
	if request.Category == api.CanonicalCategoryTV {
		providers = append(providers, struct {
			name   api.IdentityProvider
			client any
		}{api.IdentityProviderTVDB, tvdbClient})
	}
	result := api.ProviderSearchResult{
		Query:     request.Query,
		Category:  request.Category,
		Providers: make([]api.ProviderCandidateResult, len(providers)),
	}
	var group errgroup.Group
	for i, provider := range providers {
		group.Go(func() error {
			out := api.ProviderCandidateResult{
				Provider:   provider.name,
				Query:      query,
				Year:       year,
				Category:   request.Category,
				Status:     api.MetadataEvidenceStatusUnavailable,
				Candidates: []api.ExternalIdentityCandidate{},
			}
			switch provider.name {
			case api.IdentityProviderTMDB:
				out.Filters = map[string]string{
					"page":          "1",
					"language":      "en-US",
					"include_adult": "true",
				}
			case api.IdentityProviderIMDB:
				out.Filters = map[string]string{"first": "20", "category": "filtered from returned titles"}
			case api.IdentityProviderTVmaze, api.IdentityProviderMAL:
				return errors.New("metadata: unsupported candidate provider")
			case api.IdentityProviderTVDB:
				out.Filters = map[string]string{"type": "series", "limit": "20"}
			}
			if client, ok := provider.client.(candidateSearcher); ok {
				items, err := client.SearchCandidates(ctx, query, year, request.Category)
				out.SearchFailed = err != nil
				if err == nil {
					out.Status = api.MetadataEvidenceStatusComplete
					out.Candidates = items
					if out.Candidates == nil {
						out.Candidates = []api.ExternalIdentityCandidate{}
					}
					for _, item := range items {
						if item.DetailsStatus == api.MetadataEvidenceStatusPartial {
							out.Status = api.MetadataEvidenceStatusPartial
						}
					}
				}
			}
			result.Providers[i] = out
			return ctx.Err()
		})
	}
	if err := group.Wait(); err != nil {
		return api.ProviderSearchResult{}, fmt.Errorf("provider search: %w", err)
	}
	cacheable := true
	for _, provider := range result.Providers {
		if provider.Status != api.MetadataEvidenceStatusComplete {
			cacheable = false
		}
	}
	if cacheable {
		if len(s.providerSearchCache) >= 32 {
			var oldest api.ProviderSearchRequest
			var expiry time.Time
			for key, entry := range s.providerSearchCache {
				if expiry.IsZero() || entry.expires.Before(expiry) {
					oldest, expiry = key, entry.expires
				}
			}
			delete(s.providerSearchCache, oldest)
		}
		s.providerSearchCache[request] = providerSearchCacheEntry{result: result, expires: time.Now().Add(5 * time.Minute)}
	}
	return result, nil
}
