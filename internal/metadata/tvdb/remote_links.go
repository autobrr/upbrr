// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvdb

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/evidence"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/pkg/api"
)

// GetSeriesRemoteLinks refreshes only series identity evidence, leaving kept naming metadata untouched.
func (c *Client) GetSeriesRemoteLinks(ctx context.Context, seriesID int) (api.TVDBRemoteLinks, error) {
	response, err := c.getSeriesExtended(ctx, seriesID, nil, nil)
	if err != nil {
		return api.TVDBRemoteLinks{Status: api.MetadataEvidenceStatusUnavailable}, err
	}
	if response.Data.ID != seriesID {
		return api.TVDBRemoteLinks{Status: api.MetadataEvidenceStatusContradictory}, errors.New("tvdb: series identity mismatch")
	}
	return parseSeriesRemoteLinks(response.Data.RemoteIDs), nil
}

func (c *Client) getSeriesExtended(
	ctx context.Context,
	seriesID int,
	params map[string]string,
	legacyNaming *seriesExtendedResponse,
) (seriesExtendedResponse, error) {
	path := fmt.Sprintf("/series/%d/extended", seriesID)
	var response seriesExtendedResponse
	if len(params) > 0 || !evidence.Enabled(ctx) {
		err := c.getJSON(ctx, path, params, &response)
		return response, err
	}
	// Default-language readers share one observation. Earlier links-only and
	// full projections could each contain a different version of the links.
	query := []any{c.baseURL, c.apiKey, path, map[string]string{}}
	fetched := false
	err := evidence.JSON(ctx, "tvdb.series.extended.v2", query, &response, errNotFound, nil, func() error {
		fetched = true
		return c.uncachedJSON(ctx, path, nil, &response)
	})
	if legacyNaming != nil && (fetched || err != nil) && ctx.Err() == nil {
		retainLegacySeriesNaming(ctx, query, seriesID, legacyNaming)
	}
	if err != nil {
		return seriesExtendedResponse{}, fmt.Errorf("metadata evidence response: %w", err)
	}
	return response, nil
}

// A legacy title without remoteIds can still supply naming on the first link
// repair. Its fields never replace the shared provider response in the cache.
func retainLegacySeriesNaming(ctx context.Context, query []any, seriesID int, naming *seriesExtendedResponse) {
	const domain = "tvdb.response.v1"
	legacyKey := []any{query, fmt.Sprintf("%T", naming)}
	var cached struct{ Data seriesExtendedResponse }
	if evidence.Reusable(ctx, domain, legacyKey) && evidence.Retained(ctx, domain, legacyKey, &cached) &&
		cached.Data.Data.ID == seriesID && cached.Data.Data.RemoteIDs == nil {
		*naming = cached.Data
	}
}

// parseSeriesRemoteLinks accepts links only from the series extended resource.
// Source names identify namespaces; numeric source types alone cannot distinguish movie/series IDs.
func parseSeriesRemoteLinks(entries *[]remoteIDEntry) api.TVDBRemoteLinks {
	result := api.TVDBRemoteLinks{Status: api.MetadataEvidenceStatusPartial}
	if entries == nil {
		return result
	}
	result.Status = api.MetadataEvidenceStatusComplete
	for _, entry := range *entries {
		link := api.TVDBRemoteLink{State: "valid"}
		switch strings.ToLower(strings.TrimSpace(entry.SourceName)) {
		case "imdb":
			link.Provider = api.IdentityProviderIMDB
		case "themoviedb.com":
			if entry.Type == 12 {
				link.Provider = api.IdentityProviderTMDB
			} else {
				link.State = "unsupported"
			}
		case "tv maze", "tvmaze":
			link.Provider = api.IdentityProviderTVmaze
		case "myanimelist":
			link.Provider = api.IdentityProviderMAL
		default:
			link.State = "unsupported"
		}
		if link.Provider != "" {
			value := strings.TrimSpace(entry.ID)
			if link.Provider == api.IdentityProviderIMDB {
				if after, ok := strings.CutPrefix(value, "tt"); ok {
					value = after
				} else {
					value = ""
				}
			}
			link.ID = providerid.ParseDecimal(value)
			if link.ID == 0 {
				link.State = "malformed"
			}
		}
		result.Links = append(result.Links, link)
	}
	firstIDs := map[api.IdentityProvider]int{}
	conflicts := map[api.IdentityProvider]bool{}
	for _, link := range result.Links {
		if link.ID <= 0 {
			continue
		}
		if firstIDs[link.Provider] == 0 {
			firstIDs[link.Provider] = link.ID
		} else if firstIDs[link.Provider] != link.ID {
			conflicts[link.Provider] = true
		}
	}
	for i := range result.Links {
		if result.Links[i].State == "valid" && conflicts[result.Links[i].Provider] {
			result.Links[i].State = "conflicting"
			result.Status = api.MetadataEvidenceStatusContradictory
		}
		if result.Links[i].State != "valid" {
			result.Links[i].ID = 0
		}
	}
	return result
}
