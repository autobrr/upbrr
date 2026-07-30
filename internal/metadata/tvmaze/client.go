// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvmaze

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/pkg/api"
)

const defaultBaseURL = "https://api.tvmaze.com"

var errNotFound = errors.New("tvmaze: not found")

// Client resolves TV series and episode metadata through TVmaze.
type Client struct {
	baseURL string
	http    *http.Client
	logger  api.Logger
}

// NewClient substitutes a 10-second HTTP client and no-op logger for nil
// dependencies.
func NewClient(httpClient *http.Client, logger api.Logger) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	if logger == nil {
		logger = api.NopLogger{}
	}
	return &Client{
		baseURL: defaultBaseURL,
		http:    httpClient,
		logger:  logger,
	}
}

// Search prefers a manual TVmaze ID, then TVDB and IMDb lookups, followed by
// optional full-name and two-word searches. Lookup failures are treated as
// misses. ManualID remains selected even when its detail fetch fails; ManualDate
// suppresses automatic selection from ordinary candidates.
func (c *Client) Search(ctx context.Context, input SearchInput) (SearchResult, error) {
	input = applyReleaseHints(input)
	imdbID := metautil.ParseIMDbNumeric(input.ImdbID)
	tvdbID := normalizeTVDBID(input.TVDBID)

	if input.ManualID != 0 {
		selected := input.ManualID
		candidates := make([]Candidate, 0, 1)
		if cand, err := c.getShow(ctx, selected); err == nil {
			candidates = append(candidates, cand)
			if parsed := metautil.ParseIMDbNumeric(cand.Externals.IMDB); imdbID == 0 && parsed != 0 {
				imdbID = parsed
			}
			if tvdbID == 0 && cand.Externals.TVDB != 0 {
				tvdbID = cand.Externals.TVDB
			}
		}
		if c.logger != nil {
			c.logger.Infof("tvmaze: manual selected id=%d imdb=%d tvdb=%d", selected, imdbID, tvdbID)
		}
		return SearchResult{
			SelectedID: selected,
			IMDBID:     imdbID,
			TVDBID:     tvdbID,
			Candidates: candidates,
		}, nil
	}

	results := make([]Candidate, 0)
	if tvdbID != 0 {
		cand, err := c.lookupShow(ctx, "thetvdb", strconv.Itoa(tvdbID))
		if err == nil {
			results = append(results, cand)
		}
	}
	if len(results) == 0 && imdbID != 0 {
		cand, err := c.lookupShow(ctx, "imdb", fmt.Sprintf("tt%07d", imdbID))
		if err == nil {
			results = append(results, cand)
		}
	}
	allowNameFallback := !input.StrictIDOnly
	if input.AllowNameFallback {
		allowNameFallback = true
	}
	if allowNameFallback && len(results) == 0 {
		queryResults, err := c.searchShows(ctx, input.Filename)
		if err == nil {
			results = append(results, queryResults...)
		}
	}
	if allowNameFallback && len(results) == 0 {
		firstTwo := firstTwoWords(input.Filename)
		if firstTwo != "" && firstTwo != input.Filename {
			queryResults, err := c.searchShows(ctx, firstTwo)
			if err == nil {
				results = append(results, queryResults...)
			}
		}
	}

	candidates := dedupeCandidates(results)
	selectedID, selectedIMDB, selectedTVDB := selectCandidate(candidates, imdbID, tvdbID, input.ManualDate)
	if c.logger != nil && selectedID != 0 {
		c.logger.Tracef("tvmaze: search selected id=%d imdb=%d tvdb=%d candidates=%d", selectedID, selectedIMDB, selectedTVDB, len(candidates))
	}

	return SearchResult{
		SelectedID:   selectedID,
		IMDBID:       selectedIMDB,
		TVDBID:       selectedTVDB,
		Candidates:   candidates,
		AutoSelected: input.ManualDate == "" && selectedID != 0,
	}, nil
}

func applyReleaseHints(input SearchInput) SearchInput {
	base := strings.TrimSpace(input.Filename)
	if base == "" {
		return input
	}
	release := metautil.ParseRelease(base)

	mainTitle := release.Title
	if mainTitle == "" {
		mainTitle = release.Subtitle
	}
	if mainTitle != "" {
		input.Filename = mainTitle
	}
	if strings.TrimSpace(input.Year) == "" && release.Year != 0 {
		input.Year = strconv.Itoa(release.Year)
	}
	return input
}

// GetEpisodeByNumber falls back on a date lookup only when TVmaze returns 404.
// The fallback date prefers ManualDate, then the matching TVDB episode air date.
func (c *Client) GetEpisodeByNumber(ctx context.Context, tvmazeID, season, episode int, lookup EpisodeLookupContext) (*EpisodeData, error) {
	if tvmazeID == 0 || season == 0 || episode == 0 {
		return nil, errNotFound
	}

	endpoint := fmt.Sprintf("%s/shows/%d/episodebynumber", c.baseURL, tvmazeID)
	params := url.Values{}
	params.Set("season", strconv.Itoa(season))
	params.Set("number", strconv.Itoa(episode))

	var episodeResp episodeResponse
	if err := c.getJSON(ctx, endpoint, params, &episodeResp); err != nil {
		if errors.Is(err, errNotFound) {
			return c.fallbackEpisodeByDate(ctx, tvmazeID, lookup)
		}
		return nil, err
	}
	data := c.buildEpisodeData(ctx, episodeResp)
	if c.logger != nil && data != nil {
		c.logger.Tracef("tvmaze: episode lookup id=%d season=%d episode=%d series=%q", tvmazeID, data.SeasonNumber, data.EpisodeNumber, data.SeriesName)
	}
	return data, nil
}

// GetEpisodeByDate returns the first episode supplied for airdate. Invalid input,
// a 404 response, or an empty episode list returns the package not-found error.
func (c *Client) GetEpisodeByDate(ctx context.Context, tvmazeID int, airdate string) (*EpisodeData, error) {
	if tvmazeID == 0 || strings.TrimSpace(airdate) == "" {
		return nil, errNotFound
	}
	endpoint := fmt.Sprintf("%s/shows/%d/episodesbydate", c.baseURL, tvmazeID)
	params := url.Values{}
	params.Set("date", airdate)

	var episodes []episodeResponse
	if err := c.getJSON(ctx, endpoint, params, &episodes); err != nil {
		return nil, err
	}
	if len(episodes) == 0 {
		return nil, errNotFound
	}
	data := c.buildEpisodeData(ctx, episodes[0])
	if c.logger != nil && data != nil {
		c.logger.Tracef("tvmaze: episode lookup id=%d airdate=%s series=%q", tvmazeID, data.AirDate, data.SeriesName)
	}
	return data, nil
}

func (c *Client) fallbackEpisodeByDate(ctx context.Context, tvmazeID int, lookup EpisodeLookupContext) (*EpisodeData, error) {
	airdate := strings.TrimSpace(lookup.ManualDate)
	if airdate == "" && lookup.TVDBEpisodeID != 0 {
		for _, ep := range lookup.TVDBEpisodeData {
			if ep.ID == lookup.TVDBEpisodeID {
				airdate = strings.TrimSpace(ep.Aired)
				break
			}
		}
	}
	if airdate == "" {
		return nil, errNotFound
	}
	return c.GetEpisodeByDate(ctx, tvmazeID, airdate)
}

func (c *Client) buildEpisodeData(ctx context.Context, episode episodeResponse) *EpisodeData {
	show := showResponse{}
	showName := ""
	if episode.Links.Show.Href != "" {
		if err := c.getJSON(ctx, episode.Links.Show.Href, nil, &show); err == nil {
			showName = show.Name
		}
	}

	seriesName := metautil.FirstNonEmpty(show.Name, episode.Links.Show.Name)
	if seriesName == "" {
		seriesName = showName
	}

	return &EpisodeData{
		EpisodeName:       episode.Name,
		Overview:          cleanSummary(episode.Summary),
		SeasonNumber:      episode.Season,
		EpisodeNumber:     episode.Number,
		AirDate:           episode.Airdate,
		Runtime:           episode.Runtime,
		SeriesName:        seriesName,
		SeriesOverview:    cleanSummary(show.Summary),
		Image:             episode.Image.Original,
		ImageMedium:       episode.Image.Medium,
		SeriesImage:       show.Image.Original,
		SeriesImageMedium: show.Image.Medium,
	}
}

func (c *Client) lookupShow(ctx context.Context, key, value string) (Candidate, error) {
	endpoint := c.baseURL + "/lookup/shows"
	params := url.Values{}
	params.Set(key, value)

	var show showResponse
	if err := c.getJSON(ctx, endpoint, params, &show); err != nil {
		return Candidate{}, err
	}
	return candidateFromShow(show), nil
}

func (c *Client) searchShows(ctx context.Context, query string) ([]Candidate, error) {
	endpoint := c.baseURL + "/search/shows"
	params := url.Values{}
	params.Set("q", query)

	var items []searchResponse
	if err := c.getJSON(ctx, endpoint, params, &items); err != nil {
		return nil, err
	}

	candidates := make([]Candidate, 0, len(items))
	for _, item := range items {
		candidates = append(candidates, candidateFromShow(item.Show))
	}
	return candidates, nil
}

func (c *Client) getShow(ctx context.Context, id int) (Candidate, error) {
	if id == 0 {
		return Candidate{}, errNotFound
	}
	endpoint := fmt.Sprintf("%s/shows/%d", c.baseURL, id)

	var show showResponse
	if err := c.getJSON(ctx, endpoint, nil, &show); err != nil {
		return Candidate{}, err
	}
	return candidateFromShow(show), nil
}

func (c *Client) getJSON(ctx context.Context, endpoint string, params url.Values, target any) error {
	reqURL := endpoint
	if params != nil {
		parsed, err := url.Parse(endpoint)
		if err != nil {
			return fmt.Errorf("tvmaze: parse request endpoint: %w", err)
		}
		parsed.RawQuery = params.Encode()
		reqURL = parsed.String()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return fmt.Errorf("tvmaze: build request for %s: %w", endpoint, err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("tvmaze: execute request for %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return errNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("tvmaze: http %d", resp.StatusCode)
	}

	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("tvmaze: decode response: %w", err)
	}
	return nil
}

func dedupeCandidates(candidates []Candidate) []Candidate {
	seen := make(map[int]struct{}, len(candidates))
	unique := make([]Candidate, 0, len(candidates))
	for _, cand := range candidates {
		if cand.ID == 0 {
			continue
		}
		if _, ok := seen[cand.ID]; ok {
			continue
		}
		seen[cand.ID] = struct{}{}
		unique = append(unique, cand)
	}
	return unique
}

func selectCandidate(candidates []Candidate, imdbID, tvdbID int, manualDate string) (int, int, int) {
	if len(candidates) == 0 {
		return 0, imdbID, tvdbID
	}

	if manualDate != "" {
		return 0, imdbID, tvdbID
	}

	selected := candidates[0]
	updatedIMDB := imdbID
	if updatedIMDB == 0 {
		updatedIMDB = metautil.ParseIMDbNumeric(selected.Externals.IMDB)
	}
	updatedTVDB := tvdbID
	if updatedTVDB == 0 && selected.Externals.TVDB != 0 {
		updatedTVDB = selected.Externals.TVDB
	}
	return selected.ID, updatedIMDB, updatedTVDB
}

func candidateFromShow(show showResponse) Candidate {
	network := TVNetwork{
		Name:      strings.TrimSpace(show.Network.Name),
		Country:   strings.TrimSpace(show.Network.Country.Name),
		Logo:      strings.TrimSpace(show.Network.Image.Original),
		LogoSmall: strings.TrimSpace(show.Network.Image.Medium),
	}
	webChannel := TVNetwork{
		Name:      strings.TrimSpace(show.WebChannel.Name),
		Country:   strings.TrimSpace(show.WebChannel.Country.Name),
		Logo:      strings.TrimSpace(show.WebChannel.Image.Original),
		LogoSmall: strings.TrimSpace(show.WebChannel.Image.Medium),
	}
	country := metautil.FirstNonEmpty(network.Country, webChannel.Country)
	return Candidate{
		ID:             show.ID,
		Name:           strings.TrimSpace(show.Name),
		Premiered:      strings.TrimSpace(show.Premiered),
		Ended:          strings.TrimSpace(show.Ended),
		Summary:        cleanSummary(show.Summary),
		Status:         strings.TrimSpace(show.Status),
		Type:           strings.TrimSpace(show.Type),
		Language:       strings.TrimSpace(show.Language),
		Genres:         append([]string{}, show.Genres...),
		Runtime:        show.Runtime,
		AverageRuntime: show.AverageRuntime,
		Rating:         show.Rating.Average,
		Weight:         show.Weight,
		OfficialSite:   strings.TrimSpace(show.OfficialSite),
		Country:        country,
		Network:        network,
		WebChannel:     webChannel,
		Image: Image{
			Original: strings.TrimSpace(show.Image.Original),
			Medium:   strings.TrimSpace(show.Image.Medium),
		},
		Externals: Externals{
			IMDB: show.Externals.IMDB,
			TVDB: show.Externals.TVDB,
			Other: map[string]any{
				"thetvdb": show.Externals.TVDB,
				"imdb":    show.Externals.IMDB,
			},
		},
	}
}

func normalizeTVDBID(value string) int {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed == "0" {
		return 0
	}
	id, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0
	}
	return id
}

func firstTwoWords(value string) string {
	fields := strings.Fields(value)
	if len(fields) < 2 {
		return strings.TrimSpace(value)
	}
	return strings.Join(fields[:2], " ")
}

func cleanSummary(value string) string {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.ReplaceAll(trimmed, "<p>", "")
	trimmed = strings.ReplaceAll(trimmed, "</p>", "")
	return strings.TrimSpace(trimmed)
}

type searchResponse struct {
	Show showResponse `json:"show"`
}

type showResponse struct {
	ID             int      `json:"id"`
	Name           string   `json:"name"`
	Premiered      string   `json:"premiered"`
	Ended          string   `json:"ended"`
	Summary        string   `json:"summary"`
	Status         string   `json:"status"`
	Type           string   `json:"type"`
	Language       string   `json:"language"`
	Genres         []string `json:"genres"`
	Runtime        int      `json:"runtime"`
	AverageRuntime int      `json:"averageRuntime"`
	Weight         int      `json:"weight"`
	OfficialSite   string   `json:"officialSite"`
	Rating         struct {
		Average float64 `json:"average"`
	} `json:"rating"`
	Externals struct {
		TVDB int    `json:"thetvdb"`
		IMDB string `json:"imdb"`
	} `json:"externals"`
	Image struct {
		Original string `json:"original"`
		Medium   string `json:"medium"`
	} `json:"image"`
	Network    channelResponse `json:"network"`
	WebChannel channelResponse `json:"webChannel"`
}

type channelResponse struct {
	Name    string `json:"name"`
	Country struct {
		Name string `json:"name"`
	} `json:"country"`
	Image struct {
		Original string `json:"original"`
		Medium   string `json:"medium"`
	} `json:"image"`
}

type episodeResponse struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
	Season  int    `json:"season"`
	Number  int    `json:"number"`
	Airdate string `json:"airdate"`
	Runtime int    `json:"runtime"`
	Image   struct {
		Original string `json:"original"`
		Medium   string `json:"medium"`
	} `json:"image"`
	Links struct {
		Show struct {
			Href string `json:"href"`
			Name string `json:"name"`
		} `json:"show"`
	} `json:"_links"`
}
