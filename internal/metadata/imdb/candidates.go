// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imdb

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/pkg/api"
)

// SearchCandidates returns one exact search page with lightweight title fields.
// Curated principal Stars are labelled separately from verified billing order; absent runtime stays unknown.
func (c *Client) SearchCandidates(ctx context.Context, query string, year int, category api.CanonicalCategory) ([]api.ExternalIdentityCandidate, error) {
	constraints := map[string]any{"titleTextConstraint": map[string]any{"searchTerm": query}}
	if year > 0 {
		constraints["releaseDateConstraint"] = map[string]any{"releaseDateRange": map[string]any{
			"start": fmt.Sprintf("%d-01-01", year), "end": fmt.Sprintf("%d-12-31", year),
		}}
	}
	const document = `query ProviderCandidates($constraints: AdvancedTitleSearchConstraints!) { advancedTitleSearch(first: 20, constraints: $constraints) { edges { node { title { id titleText { text } originalTitleText { text } titleType { text } releaseYear { year } plot { plotText { plainText } } primaryImage { url } runtime { seconds } principalCredits { category { text } credits { name { id nameText { text } } } } } } } } }`
	var response map[string]any
	if err := c.postGraphQL(ctx, "ProviderCandidates", document, map[string]any{"constraints": constraints}, &response); err != nil {
		return nil, err
	}
	if getMap(response, "data", "advancedTitleSearch") == nil {
		return nil, errors.New("imdb: missing search response")
	}
	candidates := []api.ExternalIdentityCandidate{}
	for _, edge := range getList(response, "data", "advancedTitleSearch", "edges") {
		title := getMapFromMap(edge, "node", "title")
		kind := getStringFromMap(title, "titleType", "text")
		id := metautil.ParseIMDbNumeric(getStringFromMap(title, "id"))
		if id <= 0 || kind == "" || strings.Contains(strings.ToLower(kind), "episode") || !typeMatches(string(category), kind) {
			continue
		}
		candidate := api.ExternalIdentityCandidate{
			Provider:       api.IdentityProviderIMDB,
			ID:             id,
			Title:          getStringFromMap(title, "titleText", "text"),
			OriginalTitle:  getStringFromMap(title, "originalTitleText", "text"),
			Year:           getIntFromMap(title, "releaseYear", "year"),
			Category:       category,
			MediaType:      kind,
			Overview:       getStringFromMap(title, "plot", "plotText", "plainText"),
			PosterURL:      getStringFromMap(title, "primaryImage", "url"),
			RuntimeMinutes: getIntFromMap(title, "runtime", "seconds") / 60,
			RuntimeKind:    "movie",
			DetailsStatus:  api.MetadataEvidenceStatusComplete,
		}
		if category == api.CanonicalCategoryTV {
			candidate.RuntimeKind = "episode"
		}
		for _, person := range collectCredits(title, "Director") {
			candidate.Directors = append(candidate.Directors, person.Name)
		}
		for _, person := range collectCredits(title, "Star") {
			if strings.TrimSpace(person.Name) != "" {
				candidate.Cast = append(candidate.Cast, person.Name)
			}
			if len(candidate.Cast) == 3 {
				break
			}
		}
		if len(candidate.Cast) > 0 {
			candidate.CastSource = "principal"
		}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}
