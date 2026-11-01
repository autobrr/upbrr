// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bluraycom

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/autobrr/upbrr/internal/connectiontest"
	"github.com/autobrr/upbrr/pkg/api"
)

// TestConnection checks one public search using Lookup's endpoint, headers and
// movie-link parser. The fixed public IMDb reference avoids sending release or
// account data and does not fetch individual releases or images.
func TestConnection(ctx context.Context, client *http.Client) api.ConnectionTestResult {
	endpoint := baseURL + "/search/?" + url.Values{
		"quicksearch":         {"1"},
		"quicksearch_country": {"all"},
		"quicksearch_keyword": {formatIMDbID(133093)},
		"section":             {"theatrical"},
	}.Encode()
	headers := make(http.Header)
	setPageHeaders(headers, baseURL+"/")
	body, failure := connectiontest.Request(ctx, client, http.MethodGet, endpoint, nil, headers)
	if failure.Code != "" {
		return failure
	}
	if strings.Contains(string(body), "No index") {
		return connectiontest.UnexpectedResponse()
	}
	movies, err := parseMovieLinks(string(body))
	if err != nil {
		return connectiontest.UnexpectedResponse()
	}
	for _, movie := range movies {
		link, err := url.Parse(movie.ReleasesURL)
		if err == nil && (link.Hostname() == "www.blu-ray.com" || link.Hostname() == "blu-ray.com") && movie.ProductID != "" &&
			strings.TrimSpace(movie.Title) != "" {
			return api.ConnectionTestResult{
				Code:    "success",
				Message: "Blu-ray.com returned a recognized public catalog search response. No credentials are required and no release or image data was saved.",
			}
		}
	}
	return connectiontest.UnexpectedResponse()
}

// setPageHeaders keeps public connection checks aligned with normal lookups.
// Referrers preserve the page query but omit navigation fragments.
func setPageHeaders(headers http.Header, referer string) {
	headers.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	headers.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	headers.Set("Accept-Language", "en-US,en;q=0.9")
	if referer != "" {
		referer, _, _ = strings.Cut(referer, "#")
		headers.Set("Referer", referer)
	}
}
