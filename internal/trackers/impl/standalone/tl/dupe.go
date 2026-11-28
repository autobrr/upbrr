// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/providerid"
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

const maxSearchPages = 100

var errTLSessionRejected = errors.New("TL search requires a current session")

type dupeSearcher struct {
	cfg      config.Config
	http     *http.Client
	logger   api.Logger
	maxPages int
}

func newDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	return &dupeSearcher{
		cfg:      deps.BoundConfig(),
		http:     deps.HTTPClient(),
		logger:   deps.Logger(),
		maxPages: deps.MaxPages(maxSearchPages),
	}
}

type searchPage struct {
	Torrents *[]map[string]any `json:"torrentList"`
	Total    *int              `json:"numFound"`
	Page     *int              `json:"page"`
	PerPage  *int              `json:"perPage"`
}

func (s *dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	if err := ctx.Err(); err != nil {
		return dupe.Failed(dupe.FailureRequest, "TL search canceled", err)
	}
	cfg, _ := config.TrackerConfigByName(s.cfg.Trackers.Trackers, "TL")
	if announceKey(cfg) == "" {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing passkey for tracker", nil)
	}
	queries := searchQueries(meta)
	if len(queries) == 0 {
		return dupe.NotRun(dupe.NotRunMissingMetadata, "missing title for TL dupe search", nil)
	}
	dbPath := s.cfg.MainSettings.DBPath
	client, err := cookieClientWithTransport(ctx, dbPath, cfg, s.http.Timeout, s.http.Transport)
	mayLogin := canLogIn(cfg)
	refresh := func() (*http.Client, error) {
		mayLogin = false
		if err := ensureCookieSession(ctx, cfg, dbPath, api.TrackerAuthLoginRequest{}, s.http.Transport); err != nil {
			return nil, err
		}
		return cookieClientWithTransport(ctx, dbPath, cfg, s.http.Timeout, s.http.Transport)
	}
	if errors.Is(err, cookies.ErrTrackerCookiesNotFound) {
		if !mayLogin {
			return dupe.NotRun(dupe.NotRunMissingCredentials, "import TorrentLeech cookies or configure login before searching for duplicates", nil)
		}
		client, err = refresh()
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return dupe.Failed(dupe.FailureRequest, "TL search canceled", ctxErr)
		}
		return tlSearchAuthFailure(err)
	}
	logger := logging.FromContext(ctx, s.logger)
	providerBound := meta.Identity.IMDBID > 0
	evidence := dupe.SearchEvidence{
		Complete:  true,
		WorkScope: dupe.WorkScopeTitle,
		Scope:     "title_query",
	}
	if providerBound {
		evidence.WorkScope, evidence.Scope = dupe.WorkScopeProviderID, "imdb_work"
	} else {
		evidence.Warnings = append(evidence.Warnings, "TL title search does not prove whole-work coverage")
	}
	pageLimit := maxSearchPages
	if s.maxPages > 0 {
		pageLimit = min(pageLimit, s.maxPages)
	}
	var entries []api.DupeEntry
	seen := make(map[string]bool)
	usable := false
	failure := dupe.Failed(dupe.FailureResponseParse, "TL search returned no usable response", nil)
searches:
	for _, query := range queries {
		queryIDs := make(map[string]bool)
		total, perPage := -1, 0
		exhausted := false
		for page := 1; evidence.Pages < pageLimit; page++ {
			endpoint := baseURL + query
			if page > 1 {
				// One-based IMDb pagination is verified by saved first/final-page responses.
				endpoint += "/page/" + strconv.Itoa(page)
			}
			dupe.TraceSearchRequest(logger, "TL", http.MethodGet, query, map[string]any{"page": page})
			evidence.Pages++
			payload, pageFailure := fetchSearchPage(ctx, client, endpoint)
			if mayLogin && evidence.Pages < pageLimit && errors.Is(pageFailure.Cause(), errTLSessionRejected) {
				refreshed, refreshErr := refresh()
				if refreshErr != nil {
					if err := ctx.Err(); err != nil {
						return dupe.Failed(dupe.FailureRequest, "TL search canceled", err)
					}
					if !usable {
						return tlSearchAuthFailure(refreshErr)
					}
					evidence.Complete = false
					break searches
				}
				client = refreshed
				// Results accepted under the old session cannot certify the refreshed search.
				if page > 1 {
					evidence.Complete = false
				}
				evidence.Pages++
				payload, pageFailure = fetchSearchPage(ctx, client, endpoint)
			}
			if err := ctx.Err(); err != nil {
				return dupe.Failed(dupe.FailureRequest, "TL search canceled", err)
			}
			if pageFailure.Disposition() == dupe.DispositionFailed {
				failure = pageFailure
				break
			}
			rows := *payload.Torrents
			metadataValid := payload.Total != nil && *payload.Total >= 0 && payload.Page != nil && *payload.Page == page &&
				payload.PerPage != nil && *payload.PerPage > 0 && len(rows) <= *payload.PerPage
			if len(rows) == 0 && (!metadataValid || *payload.Total != 0) {
				break
			}
			usable = true
			before := len(queryIDs)
			for _, row := range rows {
				id := tlString(row["fid"])
				if id == "" {
					evidence.Complete = false
					continue
				}
				queryIDs[id] = true
				if providerBound {
					returnedID := metautil.ParseIMDbNumeric(strings.ToLower(tlString(row["imdbID"])))
					if returnedID <= 0 {
						evidence.WorkScope = dupe.WorkScopeUnknown
					} else if returnedID != meta.Identity.IMDBID {
						evidence.WrongWorkCount++
						evidence.WorkScope = dupe.WorkScopeUnknown
						evidence.Complete = false
						continue
					}
				}
				name := tlString(row["name"])
				if name == "" {
					// Count the ID for pagination, but a nameless candidate cannot be evaluated.
					evidence.Complete = false
					continue
				}
				if seen[id] {
					continue
				}
				seen[id] = true
				entry := api.DupeEntry{
					Name: name,
					ID:   id,
					Link: torrentURL + url.PathEscape(id),
				}
				if size := tlInt64(row["size"]); size > 0 {
					entry.SizeKnown, entry.SizeBytes = true, size
				}
				entries = append(entries, entry)
			}
			if !metadataValid {
				break
			}
			if total < 0 {
				total, perPage = *payload.Total, *payload.PerPage
			} else if total != *payload.Total || perPage != *payload.PerPage {
				break
			}
			lastPage := 1
			if total > 0 {
				lastPage = (total-1)/perPage + 1
			}
			if len(queryIDs) == total && page == lastPage {
				exhausted = true
				break
			}
			if !providerBound || len(queryIDs) <= before || len(queryIDs) > total || page >= lastPage {
				// Title-route pagination is not verified; keep the first-page candidates.
				break
			}
		}
		if !exhausted {
			evidence.Complete = false
		}
	}
	if !usable {
		return failure
	}
	if !evidence.Complete || providerBound && evidence.WorkScope != dupe.WorkScopeProviderID {
		evidence.Warnings = append(evidence.Warnings, "TL search enumeration or work binding is incomplete; review available candidates")
	}
	return dupe.ResolvedWithSearch(entries, nil, evidence)
}

func fetchSearchPage(ctx context.Context, client *http.Client, endpoint string) (searchPage, dupe.AdapterResult) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return searchPage{}, dupe.Failed(dupe.FailureRequest, "TL search failed", err)
	}
	if len(client.Jar.Cookies(req.URL)) == 0 {
		return searchPage{}, dupe.Failed(dupe.FailureAuthentication, "TorrentLeech cookies do not apply to the search endpoint", nil)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return searchPage{}, dupe.Failed(dupe.FailureRequest, "TL search failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || redirectedToTLLogin(req, resp) {
		return searchPage{}, dupe.Failed(dupe.FailureAuthentication, "TorrentLeech did not accept the stored session", errTLSessionRejected)
	}
	if resp.StatusCode == http.StatusForbidden {
		return searchPage{}, dupe.Failed(dupe.FailureAuthentication, "TorrentLeech denied duplicate search access", nil)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return searchPage{}, dupe.Failed(dupe.FailureResponseStatus, "TL search failed", nil)
	}
	var payload searchPage
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return searchPage{}, dupe.Failed(dupe.FailureResponseParse, "TL search failed", err)
	}
	if payload.Torrents == nil {
		return searchPage{}, dupe.Failed(dupe.FailureResponseParse, "TL search returned no torrent list", nil)
	}
	return payload, dupe.AdapterResult{}
}

func redirectedToTLLogin(req *http.Request, resp *http.Response) bool {
	if resp.StatusCode < http.StatusMultipleChoices || resp.StatusCode >= http.StatusBadRequest {
		return false
	}
	location, err := req.URL.Parse(resp.Header.Get("Location"))
	return err == nil && sameAuthOrigin(location, req.URL) && location.Path == loginPath
}

func tlSearchAuthFailure(err error) dupe.AdapterResult {
	if _, needs2FA := errors.AsType[*trackerauth.Needs2FAError](err); needs2FA {
		return dupe.Failed(dupe.FailureAuthentication, "complete TorrentLeech 2FA in Tracker authentication before searching for duplicates", nil)
	}
	return dupe.Failed(dupe.FailureAuthentication, "TorrentLeech search could not establish a session", err)
}

func searchQueries(meta api.DuplicateSubject) []string {
	if meta.Identity.IMDBID > 0 {
		return []string{"/torrents/browse/list/imdbID/" + providerid.IMDb(meta.Identity.IMDBID).Prefixed()}
	}
	query := strings.TrimSpace(meta.Release.Title)
	if query == "" && meta.Projection != nil {
		query = dupe.ProjectedSearchName(meta)
	}
	if query == "" {
		query = strings.TrimSpace(meta.ReleaseName)
	}
	query = meta.EffectiveMetadata.PreferredTitle(query)
	if query == "" {
		return nil
	}
	subject := api.UploadSubject{
		Identity:          meta.Identity,
		EffectiveMetadata: meta.EffectiveMetadata,
		ProviderMetadata:  meta.ProviderMetadata,
		Release:           meta.Release,
		Source:            meta.Source,
		Type:              meta.Type,
		DiscType:          meta.DiscType,
		Anime:             meta.Anime,
		TVPack:            meta.TVPack,
	}
	if isTV(subject) {
		if meta.SeasonStr != "" && meta.DailyEpisodeDate == "" {
			query += " " + meta.SeasonStr
		}
	} else if year := meta.EffectiveMetadata.PreferredYear(meta.Release.Year); year > 0 {
		query += " " + strconv.Itoa(year)
	}
	if resolution := strings.TrimSpace(meta.Release.Resolution); resolution != "" {
		query += " " + resolution
	}
	category := resolveCategory(subject)
	path := "/torrents/browse/list/"
	if category != "" {
		path += "categories/" + category + "/"
	}
	paths := []string{path + "query/" + url.PathEscape(query)}
	if isTV(subject) && !meta.TVPack {
		subject.TVPack = true
		if packCategory := resolveCategory(subject); packCategory != category {
			paths = append(paths, fmt.Sprintf("/torrents/browse/list/categories/%s/query/%s", packCategory, url.PathEscape(query)))
		}
	}
	return paths
}

func tlString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func tlInt64(value any) int64 {
	parsed, _ := strconv.ParseInt(tlString(value), 10, 64)
	return parsed
}
