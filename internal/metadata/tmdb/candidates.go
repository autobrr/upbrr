// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tmdb

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/pkg/api"
)

// SearchCandidates performs one exact title/year search page without identity selection or fallback queries.
func (c *Client) SearchCandidates(ctx context.Context, query string, year int, category api.CanonicalCategory) ([]api.ExternalIdentityCandidate, error) {
	items, err := c.searchTitle(ctx, query, year, string(category))
	if err != nil {
		return nil, err
	}
	if items == nil {
		return nil, errors.New("tmdb: missing search response")
	}
	candidates := make([]api.ExternalIdentityCandidate, 0, len(items))
	for _, item := range items {
		if item.ID <= 0 {
			continue
		}
		poster := ""
		if item.PosterPath != "" {
			poster = imageBaseURL + item.PosterPath
		}
		candidates = append(candidates, api.ExternalIdentityCandidate{
			Provider:      api.IdentityProviderTMDB,
			ID:            item.ID,
			Title:         resultTitle(item),
			OriginalTitle: resultOriginalTitle(item),
			Year:          resultYear(item),
			Category:      category,
			Overview:      item.Overview,
			PosterURL:     poster,
		})
	}
	err = metautil.EnrichCandidates(ctx, candidates, func(ctx context.Context, candidate *api.ExternalIdentityCandidate) error {
		kind := "movie"
		if category == api.CanonicalCategoryTV {
			kind = "tv"
		}
		type candidateCastCredit struct {
			Name  string `json:"name"`
			Order *int   `json:"order"`
		}
		var details struct {
			ID             int   `json:"id"`
			Runtime        int   `json:"runtime"`
			EpisodeRunTime []int `json:"episode_run_time"`
			Credits        struct {
				Cast []candidateCastCredit `json:"cast"`
				Crew []struct {
					Name string `json:"name"`
					Job  string `json:"job"`
				} `json:"crew"`
			} `json:"credits"`
		}
		if err := c.getJSON(ctx, fmt.Sprintf("/%s/%d", kind, candidate.ID), map[string]string{
			"api_key":            c.apiKey,
			"append_to_response": "credits",
			"language":           "en-US",
		}, &details); err != nil {
			return err
		}
		if details.ID != candidate.ID {
			return errors.New("tmdb: candidate detail identity mismatch")
		}
		candidate.RuntimeMinutes = details.Runtime
		candidate.RuntimeKind = "movie"
		if category == api.CanonicalCategoryTV {
			candidate.RuntimeMinutes = 0
			candidate.RuntimeKind = "episode"
			if len(details.EpisodeRunTime) > 0 {
				candidate.RuntimeMinutes = details.EpisodeRunTime[0]
			}
		}
		// TMDB explicitly supplies billing order; an absent order is not zero.
		cast := slices.DeleteFunc(details.Credits.Cast, func(person candidateCastCredit) bool {
			return person.Order == nil || *person.Order < 0 || strings.TrimSpace(person.Name) == ""
		})
		slices.SortStableFunc(cast, func(a, b candidateCastCredit) int {
			return *a.Order - *b.Order
		})
		for _, person := range cast {
			if !slices.Contains(candidate.Cast, person.Name) {
				candidate.Cast = append(candidate.Cast, person.Name)
			}
			if len(candidate.Cast) == 3 {
				break
			}
		}
		if len(candidate.Cast) > 0 {
			candidate.CastSource = "billing"
		}
		for _, person := range details.Credits.Crew {
			if person.Job == "Director" && strings.TrimSpace(person.Name) != "" && !slices.Contains(candidate.Directors, person.Name) {
				candidate.Directors = append(candidate.Directors, person.Name)
			}
		}
		return nil
	})
	if err != nil {
		return candidates, fmt.Errorf("tmdb candidate enrichment: %w", err)
	}
	return candidates, nil
}
