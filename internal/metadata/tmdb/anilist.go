// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tmdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
)

const anilistURL = "https://graphql.anilist.co"
const anilistRetryCount = 3

var seasonPattern = regexp.MustCompile(`(?i)(?:season\s*(\d+)|\bS(\d{1,2})\b)`)

// ResolveAnime enriches anime metadata from AniList, preferring an explicit MAL
// ID and falling back to TMDB title or filename searches.
func (c *Client) ResolveAnime(ctx context.Context, tmdbName string, input MetadataInput) (AnimeResult, error) {
	result := AnimeResult{Demographic: "Mina"}
	if input.MALManual != 0 {
		result.MALID = input.MALManual
	}

	searchTerms := []string{tmdbName}
	if input.Filename != "" {
		searchTerms = append(searchTerms, input.Filename)
	}

	var media []anilistMedia
	for _, term := range searchTerms {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		items, err := c.anilistSearch(ctx, term, result.MALID)
		if err != nil {
			continue
		}
		if len(items) > 0 {
			media = items
			break
		}
	}
	if len(media) == 0 {
		return result, nil
	}

	expectedSeason := extractSeason(input.ManualSeason)
	if expectedSeason == 0 {
		expectedSeason = extractSeason(input.Season)
	}
	if expectedSeason == 0 {
		expectedSeason = extractSeason(input.Filename)
	}

	best := media[0]
	bestScore := -1.0
	bestSeasonScore := -1.0
	searchName := buildSearchName(tmdbName, input.Filename)

	for _, item := range media {
		seasonFromTitle := findSeasonInTitles(item.Title)
		score := 0.0
		for _, title := range []string{item.Title.Romaji, item.Title.English, item.Title.Native} {
			clean := normalizeAnimeTitle(title)
			if clean == "" {
				continue
			}
			score = maxFloat(score, metautil.SimilarityRatio(clean, searchName))
		}
		if expectedSeason != 0 && seasonFromTitle == expectedSeason {
			if score > bestSeasonScore {
				bestSeasonScore = score
				best = item
			}
		} else if bestSeasonScore < 0 && score > bestScore {
			bestScore = score
			best = item
		}
	}

	result.Romaji = metautil.FirstNonEmpty(best.Title.Romaji, best.Title.English)
	result.English = metautil.FirstNonEmpty(best.Title.English, best.Title.Romaji)
	result.MALID = best.IDMal
	result.SeasonYear = best.SeasonYear
	result.Episodes = best.Episodes
	result.Demographic = resolveDemographic(best.Tags, result.Demographic)
	if input.MALManual != 0 {
		result.MALID = input.MALManual
	}

	return result, nil
}

func (c *Client) anilistSearch(ctx context.Context, term string, malID int) ([]anilistMedia, error) {
	query := anilistQuery(malID != 0)
	variables := map[string]any{}
	if malID != 0 {
		variables["search"] = malID
	} else {
		variables["search"] = cleanAnilistSearch(term)
	}
	payload := map[string]any{"query": query, "variables": variables}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("anilist: marshal search payload: %w", err)
	}

	var lastErr error
	for attempt := range anilistRetryCount {
		response, err := c.doAniListSearch(ctx, body)
		if err == nil {
			return response.Data.Page.Media, nil
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("context canceled: %w", ctx.Err())
		}
		if !isRetryableAniListError(err) || attempt == anilistRetryCount-1 {
			return nil, err
		}
		lastErr = err
		if c.logger != nil {
			c.logger.Warnf("tmdb: anilist request timed out for %q, retrying (%d/%d)", strings.TrimSpace(term), attempt+2, anilistRetryCount)
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, nil
}

func (c *Client) doAniListSearch(ctx context.Context, body []byte) (anilistResponse, error) {
	var response anilistResponse

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anilistURL, bytes.NewReader(body))
	if err != nil {
		return response, fmt.Errorf("anilist: build search request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return response, fmt.Errorf("anilist: execute search request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return response, fmt.Errorf("anilist: http %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return response, fmt.Errorf("anilist: decode search response: %w", err)
	}
	return response, nil
}

func isRetryableAniListError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func anilistQuery(byID bool) string {
	if byID {
		return `query ($search: Int) { Page (page: 1) { pageInfo { total } media (idMal: $search, type: ANIME, sort: SEARCH_MATCH) { id idMal title { romaji english native } seasonYear episodes tags { name } } } }`
	}
	return `query ($search: String) { Page (page: 1) { pageInfo { total } media (search: $search, type: ANIME, sort: SEARCH_MATCH) { id idMal title { romaji english native } seasonYear episodes tags { name } } } }`
}

func cleanAnilistSearch(value string) string {
	value = strings.ReplaceAll(value, "-", " ")
	value = strings.ReplaceAll(value, "The Movie", "")
	return strings.Join(strings.Fields(value), " ")
}

func buildSearchName(tmdbName, filename string) string {
	name := tmdbName
	if strings.Contains(strings.ToLower(filename), "subsplease") {
		name = filename
	}
	return normalizeAnimeTitle(name)
}

func normalizeAnimeTitle(value string) string {
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, " ", "")
	value = regexp.MustCompile(`[^0-9a-z\[\]]+`).ReplaceAllString(value, "")
	return value
}

func extractSeason(value string) int {
	match := seasonPattern.FindStringSubmatch(value)
	if len(match) == 0 {
		return 0
	}
	for _, group := range match[1:] {
		if group == "" {
			continue
		}
		if parsed, err := strconv.Atoi(group); err == nil {
			return parsed
		}
	}
	return 0
}

func findSeasonInTitles(title anilistTitle) int {
	for _, value := range []string{title.Romaji, title.English, title.Native} {
		if value == "" {
			continue
		}
		if match := regexp.MustCompile(`(?i)season\s*(\d+)`).FindStringSubmatch(value); len(match) > 1 {
			if parsed, err := strconv.Atoi(match[1]); err == nil {
				return parsed
			}
		}
	}
	return 0
}

func resolveDemographic(tags []anilistTag, fallback string) string {
	demos := []string{"Shounen", "Seinen", "Shoujo", "Josei", "Kodomo", "Mina"}
	for _, demo := range demos {
		for _, tag := range tags {
			if tag.Name == demo {
				return demo
			}
		}
	}
	return fallback
}

type anilistResponse struct {
	Data struct {
		Page struct {
			Media []anilistMedia `json:"media"`
		} `json:"Page"`
	} `json:"data"`
}

type anilistMedia struct {
	ID         int          `json:"id"`
	IDMal      int          `json:"idMal"`
	Title      anilistTitle `json:"title"`
	SeasonYear string       `json:"seasonYear"`
	Episodes   int          `json:"episodes"`
	Tags       []anilistTag `json:"tags"`
}

type anilistTitle struct {
	Romaji  string `json:"romaji"`
	English string `json:"english"`
	Native  string `json:"native"`
}

type anilistTag struct {
	Name string `json:"name"`
}
