// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

const maxSearchPages = 100

type dupeSearcher struct {
	cfg    config.Config
	http   *http.Client
	logger api.Logger
}

func newDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	return &dupeSearcher{
		cfg:    deps.BoundConfig(),
		http:   deps.HTTPClient(),
		logger: deps.Logger(),
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
	if !tlConfigured(s.cfg) {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing passkey for tracker", nil)
	}
	queries := searchQueries(meta)
	if len(queries) == 0 {
		return dupe.NotRun(dupe.NotRunMissingMetadata, "missing title for TL dupe search", nil)
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
	var entries []api.DupeEntry
	seen := make(map[string]bool)
	usable := false
	failure := dupe.Failed(dupe.FailureResponseParse, "TL search returned no usable response", nil)
	for _, query := range queries {
		queryIDs := make(map[string]bool)
		total, perPage := -1, 0
		exhausted := false
		for page := 1; evidence.Pages < maxSearchPages; page++ {
			endpoint := baseURL + query
			if page > 1 {
				// One-based IMDb pagination is verified by saved first/final-page responses.
				endpoint += "/page/" + strconv.Itoa(page)
			}
			dupe.TraceSearchRequest(logger, "TL", http.MethodGet, query, map[string]any{"page": page})
			evidence.Pages++
			payload, pageFailure := s.fetchPage(ctx, endpoint)
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
				if seen[id] {
					continue
				}
				seen[id] = true
				entry := api.DupeEntry{
					Name: tlString(row["name"]),
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

func (s *dupeSearcher) fetchPage(ctx context.Context, endpoint string) (searchPage, dupe.AdapterResult) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return searchPage{}, dupe.Failed(dupe.FailureRequest, "TL search failed", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return searchPage{}, dupe.Failed(dupe.FailureRequest, "TL search failed", err)
	}
	defer resp.Body.Close()
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

func tlConfigured(cfg config.Config) bool {
	entry, _ := config.TrackerConfigByName(cfg.Trackers.Trackers, "TL")
	return strings.TrimSpace(entry.Passkey) != ""
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
