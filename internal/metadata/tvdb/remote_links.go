// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvdb

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/pkg/api"
)

// GetSeriesRemoteLinks refreshes only series identity evidence, leaving kept naming metadata untouched.
// Its separate wire projection cannot replay legacy snapshots that discarded remoteIds.
func (c *Client) GetSeriesRemoteLinks(ctx context.Context, seriesID int) (api.TVDBRemoteLinks, error) {
	var response struct {
		Data struct {
			ID        int              `json:"id"`
			RemoteIDs *[]remoteIDEntry `json:"remoteIds"`
		} `json:"data"`
	}
	if err := c.getJSON(ctx, fmt.Sprintf("/series/%d/extended", seriesID), nil, &response); err != nil {
		return api.TVDBRemoteLinks{Status: api.MetadataEvidenceStatusUnavailable}, err
	}
	if response.Data.ID != seriesID {
		return api.TVDBRemoteLinks{Status: api.MetadataEvidenceStatusContradictory}, errors.New("tvdb: series identity mismatch")
	}
	return parseSeriesRemoteLinks(response.Data.RemoteIDs), nil
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
	for i, link := range result.Links {
		if link.State != "valid" {
			continue
		}
		for _, other := range result.Links {
			if other.Provider == link.Provider && other.ID > 0 && other.ID != link.ID {
				result.Links[i].State = "conflicting"
				result.Status = api.MetadataEvidenceStatusContradictory
			}
		}
	}
	for i := range result.Links {
		if result.Links[i].State != "valid" {
			result.Links[i].ID = 0
		}
	}
	return result
}
