// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvdb

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/pkg/api"
)

// SearchCandidates searches series only, then inspects each returned series once.
// TVDB character sort values are provider ordering, not a claim of billing order.
func (c *Client) SearchCandidates(ctx context.Context, query string, year int, category api.CanonicalCategory) ([]api.ExternalIdentityCandidate, error) {
	if category != api.CanonicalCategoryTV {
		return []api.ExternalIdentityCandidate{}, nil
	}
	params := map[string]string{
		"query": query,
		"type":  "series",
		"limit": "20",
	}
	if year > 0 {
		params["year"] = strconv.Itoa(year)
	}
	var response searchSeriesResponse
	err := c.getJSON(ctx, "/search", params, &response)
	if err != nil {
		return nil, err
	}
	if response.Data == nil {
		return nil, errors.New("tvdb: missing search response")
	}
	items := mapSeriesSearchResults(response.Data)
	candidates := make([]api.ExternalIdentityCandidate, 0, len(items))
	for _, item := range items {
		if item.TVDBID <= 0 {
			continue
		}
		itemYear, _ := strconv.Atoi(item.Year)
		candidates = append(candidates, api.ExternalIdentityCandidate{
			Provider: api.IdentityProviderTVDB,
			ID:       item.TVDBID,
			Title:    metautil.FirstNonEmptyTrimmed(item.NameEnglish, item.Name),
			Year:     itemYear,
			Category: api.CanonicalCategoryTV,
		})
	}
	err = metautil.EnrichCandidates(ctx, candidates, func(ctx context.Context, candidate *api.ExternalIdentityCandidate) error {
		var response seriesExtendedResponse
		if err := c.getJSON(ctx, fmt.Sprintf("/series/%d/extended", candidate.ID), nil, &response); err != nil {
			return err
		}
		data := response.Data
		if data.ID != candidate.ID {
			return errors.New("tvdb: candidate detail identity mismatch")
		}
		candidate.Overview = data.Overview
		candidate.PosterURL = extractPosterURL(data)
		candidate.RuntimeMinutes = data.AverageRuntime
		candidate.RuntimeKind = "average_episode"
		for _, person := range data.Characters {
			if strings.EqualFold(person.PeopleType, "Director") && person.SeriesID == candidate.ID && person.EpisodeID == 0 && person.MovieID == 0 &&
				strings.TrimSpace(person.PersonName) != "" &&
				!slices.Contains(candidate.Directors, person.PersonName) {
				candidate.Directors = append(candidate.Directors, person.PersonName)
			}
		}
		characters := slices.DeleteFunc(data.Characters, func(person characterResponse) bool {
			return person.Sort == nil || *person.Sort < 0 || person.SeriesID != candidate.ID || person.EpisodeID != 0 || person.MovieID != 0 ||
				!strings.EqualFold(person.PeopleType, "Actor") ||
				strings.TrimSpace(person.PersonName) == ""
		})
		slices.SortStableFunc(characters, func(a, b characterResponse) int { return *a.Sort - *b.Sort })
		for _, person := range characters {
			if !slices.Contains(candidate.Cast, person.PersonName) {
				candidate.Cast = append(candidate.Cast, person.PersonName)
			}
			if len(candidate.Cast) == 3 {
				break
			}
		}
		if len(candidate.Cast) > 0 {
			candidate.CastSource = "provider_sort"
		}

		return nil
	})
	if err != nil {
		return candidates, fmt.Errorf("tvdb candidate enrichment: %w", err)
	}
	return candidates, nil
}

type characterResponse struct {
	PersonName string `json:"personName"`
	PeopleType string `json:"peopleType"`
	Sort       *int   `json:"sort"`
	SeriesID   int    `json:"seriesId"`
	EpisodeID  int    `json:"episodeId"`
	MovieID    int    `json:"movieId"`
}
