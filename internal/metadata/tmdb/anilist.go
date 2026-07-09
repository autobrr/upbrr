// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tmdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
)

const anilistURL = "https://graphql.anilist.co"
const anilistRetryCount = 3

// maxAniListMetadataResponseBytes caps rich AniList metadata responses before
// JSON decode so malformed or unexpected GraphQL responses cannot grow
// unbounded in memory.
const maxAniListMetadataResponseBytes int64 = 1024 * 1024

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
	if best.SeasonYear != 0 {
		result.SeasonYear = strconv.Itoa(best.SeasonYear)
	}
	result.Episodes = best.Episodes
	result.Demographic = resolveDemographic(best.Tags, result.Demographic)
	if input.MALManual != 0 {
		result.MALID = input.MALManual
	}

	return result, nil
}

// FetchAniListMetadata returns AniList media details for a MAL anime ID.
//
// A non-positive ID or missing AniList media returns an empty result without
// error. GraphQL errors, oversized responses, HTTP failures, and canceled
// contexts return an error; transient timeout-style failures are retried before
// the final error is surfaced.
func (c *Client) FetchAniListMetadata(ctx context.Context, malID int) (AniListMetadataResult, error) {
	if malID <= 0 {
		return AniListMetadataResult{}, nil
	}
	payload := map[string]any{
		"query":     anilistMetadataQuery(),
		"variables": map[string]any{"idMal": malID},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return AniListMetadataResult{}, fmt.Errorf("anilist: marshal metadata payload: %w", err)
	}

	for attempt := range anilistRetryCount {
		response, err := c.doAniListMetadata(ctx, body)
		if err == nil {
			if len(response.Errors) > 0 {
				return AniListMetadataResult{}, fmt.Errorf("anilist: graphql metadata error: %s", response.Errors[0].Message)
			}
			if response.Data.Media == nil {
				return AniListMetadataResult{}, nil
			}
			return mapAniListMetadata(*response.Data.Media), nil
		}
		if ctx.Err() != nil {
			return AniListMetadataResult{}, fmt.Errorf("context canceled: %w", ctx.Err())
		}
		if !isRetryableAniListError(err) || attempt == anilistRetryCount-1 {
			return AniListMetadataResult{}, err
		}
		if c.logger != nil {
			c.logger.Warnf("tmdb: anilist metadata request timed out for mal=%d, retrying (%d/%d)", malID, attempt+2, anilistRetryCount)
		}
	}
	return AniListMetadataResult{}, nil
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

// doAniListMetadata posts the rich metadata GraphQL request and bounds the
// response body before decoding it into the local wire shape.
func (c *Client) doAniListMetadata(ctx context.Context, body []byte) (anilistMetadataResponse, error) {
	var response anilistMetadataResponse

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anilistURL, bytes.NewReader(body))
	if err != nil {
		return response, fmt.Errorf("anilist: build metadata request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return response, fmt.Errorf("anilist: execute metadata request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return response, fmt.Errorf("anilist: http %d", resp.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxAniListMetadataResponseBytes+1))
	if err != nil {
		return response, fmt.Errorf("anilist: read metadata response: %w", err)
	}
	if int64(len(payload)) > maxAniListMetadataResponseBytes {
		return response, fmt.Errorf("anilist: metadata response too large: limit %d bytes", maxAniListMetadataResponseBytes)
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		return response, fmt.Errorf("anilist: decode metadata response: %w", err)
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

// anilistMetadataQuery requests the rich fields persisted for MAL/AniList
// preview; keep it aligned with AniListMetadataResult and API mapping.
func anilistMetadataQuery() string {
	return `query ($idMal: Int) { Media(idMal: $idMal, type: ANIME) { id idMal siteUrl title { romaji english native userPreferred } description(asHtml: false) format status startDate { year month day } endDate { year month day } season seasonYear episodes duration countryOfOrigin source coverImage { extraLarge large medium color } bannerImage genres synonyms averageScore meanScore popularity favourites isAdult tags { name rank category isAdult isGeneralSpoiler isMediaSpoiler } studios(isMain: true) { nodes { id name siteUrl } } trailer { id site thumbnail } nextAiringEpisode { airingAt timeUntilAiring episode } externalLinks { site url type language } } }`
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

type anilistMetadataResponse struct {
	Data struct {
		Media *anilistMetadataMedia `json:"Media"`
	} `json:"data"`
	Errors []anilistGraphQLError `json:"errors"`
}

type anilistGraphQLError struct {
	Message string `json:"message"`
}

type anilistMedia struct {
	ID         int          `json:"id"`
	IDMal      int          `json:"idMal"`
	Title      anilistTitle `json:"title"`
	SeasonYear int          `json:"seasonYear"`
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

type anilistMetadataMedia struct {
	ID                int                     `json:"id"`
	IDMal             int                     `json:"idMal"`
	SiteURL           string                  `json:"siteUrl"`
	Title             anilistMetadataTitle    `json:"title"`
	Description       string                  `json:"description"`
	Format            string                  `json:"format"`
	Status            string                  `json:"status"`
	StartDate         anilistFuzzyDate        `json:"startDate"`
	EndDate           anilistFuzzyDate        `json:"endDate"`
	Season            string                  `json:"season"`
	SeasonYear        int                     `json:"seasonYear"`
	Episodes          int                     `json:"episodes"`
	Duration          int                     `json:"duration"`
	CountryOfOrigin   string                  `json:"countryOfOrigin"`
	Source            string                  `json:"source"`
	CoverImage        anilistCoverImage       `json:"coverImage"`
	BannerImage       string                  `json:"bannerImage"`
	Genres            []string                `json:"genres"`
	Synonyms          []string                `json:"synonyms"`
	AverageScore      int                     `json:"averageScore"`
	MeanScore         int                     `json:"meanScore"`
	Popularity        int                     `json:"popularity"`
	Favourites        int                     `json:"favourites"`
	IsAdult           bool                    `json:"isAdult"`
	Tags              []anilistMetadataTag    `json:"tags"`
	Studios           anilistStudioConnection `json:"studios"`
	Trailer           anilistTrailer          `json:"trailer"`
	NextAiringEpisode anilistAiringEpisode    `json:"nextAiringEpisode"`
	ExternalLinks     []anilistExternalLink   `json:"externalLinks"`
}

type anilistMetadataTitle struct {
	Romaji        string `json:"romaji"`
	English       string `json:"english"`
	Native        string `json:"native"`
	UserPreferred string `json:"userPreferred"`
}

type anilistFuzzyDate struct {
	Year  int `json:"year"`
	Month int `json:"month"`
	Day   int `json:"day"`
}

type anilistCoverImage struct {
	ExtraLarge string `json:"extraLarge"`
	Large      string `json:"large"`
	Medium     string `json:"medium"`
	Color      string `json:"color"`
}

type anilistMetadataTag struct {
	Name             string `json:"name"`
	Rank             int    `json:"rank"`
	Category         string `json:"category"`
	IsAdult          bool   `json:"isAdult"`
	IsGeneralSpoiler bool   `json:"isGeneralSpoiler"`
	IsMediaSpoiler   bool   `json:"isMediaSpoiler"`
}

type anilistStudioConnection struct {
	Nodes []anilistStudio `json:"nodes"`
}

type anilistStudio struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	SiteURL string `json:"siteUrl"`
}

type anilistTrailer struct {
	ID        string `json:"id"`
	Site      string `json:"site"`
	Thumbnail string `json:"thumbnail"`
}

type anilistAiringEpisode struct {
	AiringAt        int `json:"airingAt"`
	TimeUntilAiring int `json:"timeUntilAiring"`
	Episode         int `json:"episode"`
}

type anilistExternalLink struct {
	Site     string `json:"site"`
	URL      string `json:"url"`
	Type     string `json:"type"`
	Language string `json:"language"`
}

// mapAniListMetadata converts the GraphQL wire shape into the stable metadata
// result while copying slices so callers can store or mutate the result safely.
func mapAniListMetadata(media anilistMetadataMedia) AniListMetadataResult {
	return AniListMetadataResult{
		AniListID:          media.ID,
		MALID:              media.IDMal,
		SiteURL:            media.SiteURL,
		TitleRomaji:        media.Title.Romaji,
		TitleEnglish:       media.Title.English,
		TitleNative:        media.Title.Native,
		TitleUserPreferred: media.Title.UserPreferred,
		Description:        strings.TrimSpace(media.Description),
		Format:             media.Format,
		Status:             media.Status,
		StartDate:          formatAniListDate(media.StartDate),
		EndDate:            formatAniListDate(media.EndDate),
		Season:             media.Season,
		SeasonYear:         media.SeasonYear,
		Episodes:           media.Episodes,
		Duration:           media.Duration,
		CountryOfOrigin:    media.CountryOfOrigin,
		Source:             media.Source,
		CoverExtraLarge:    media.CoverImage.ExtraLarge,
		CoverLarge:         media.CoverImage.Large,
		CoverMedium:        media.CoverImage.Medium,
		CoverColor:         media.CoverImage.Color,
		BannerImage:        media.BannerImage,
		Genres:             append([]string(nil), media.Genres...),
		Synonyms:           append([]string(nil), media.Synonyms...),
		AverageScore:       media.AverageScore,
		MeanScore:          media.MeanScore,
		Popularity:         media.Popularity,
		Favourites:         media.Favourites,
		IsAdult:            media.IsAdult,
		Tags:               mapAniListTags(media.Tags),
		Studios:            mapAniListStudios(media.Studios.Nodes),
		Trailer: AniListTrailer{
			ID:        media.Trailer.ID,
			Site:      media.Trailer.Site,
			Thumbnail: media.Trailer.Thumbnail,
		},
		NextAiringEpisode: AniListAiringEpisode{
			AiringAt:        media.NextAiringEpisode.AiringAt,
			TimeUntilAiring: media.NextAiringEpisode.TimeUntilAiring,
			Episode:         media.NextAiringEpisode.Episode,
		},
		ExternalLinks: mapAniListExternalLinks(media.ExternalLinks),
	}
}

// formatAniListDate preserves AniList fuzzy-date precision as YYYY, YYYY-MM,
// or YYYY-MM-DD instead of inventing missing month/day components.
func formatAniListDate(value anilistFuzzyDate) string {
	if value.Year == 0 {
		return ""
	}
	if value.Month == 0 {
		return strconv.Itoa(value.Year)
	}
	if value.Day == 0 {
		return fmt.Sprintf("%04d-%02d", value.Year, value.Month)
	}
	return fmt.Sprintf("%04d-%02d-%02d", value.Year, value.Month, value.Day)
}

// mapAniListTags drops nameless tags but preserves adult/spoiler markers so UI
// consumers can decide what to display.
func mapAniListTags(tags []anilistMetadataTag) []AniListTag {
	result := make([]AniListTag, 0, len(tags))
	for _, tag := range tags {
		if strings.TrimSpace(tag.Name) == "" {
			continue
		}
		result = append(result, AniListTag(tag))
	}
	return result
}

// mapAniListStudios drops nameless studio nodes from AniList's connection
// response.
func mapAniListStudios(studios []anilistStudio) []AniListStudio {
	result := make([]AniListStudio, 0, len(studios))
	for _, studio := range studios {
		if strings.TrimSpace(studio.Name) == "" {
			continue
		}
		result = append(result, AniListStudio(studio))
	}
	return result
}

// mapAniListExternalLinks keeps links that have either a site label or a URL;
// empty placeholder nodes are omitted from persisted preview metadata.
func mapAniListExternalLinks(links []anilistExternalLink) []AniListExternalLink {
	result := make([]AniListExternalLink, 0, len(links))
	for _, link := range links {
		if strings.TrimSpace(link.Site) == "" && strings.TrimSpace(link.URL) == "" {
			continue
		}
		result = append(result, AniListExternalLink(link))
	}
	return result
}
