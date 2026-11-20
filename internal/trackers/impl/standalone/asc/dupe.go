// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

const maxSearchPages = 5

type dupeSearcher struct {
	cfg  config.Config
	http *http.Client
}

func newDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	return &dupeSearcher{cfg: deps.BoundConfig(), http: deps.HTTPClient()}
}

func (h dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	if h.http == nil {
		return dupe.Failed(dupe.FailureInternal, "ASC handler misconfigured: no HTTP client", nil)
	}

	query, scope := resolveASCIMDb(meta), dupe.WorkScopeProviderID
	if meta.Anime {
		query, scope = resolveASCTitle(meta), dupe.WorkScopeTitle
	}
	if query == "" {
		return dupe.NotRun(dupe.NotRunMissingMetadata, "missing IMDb ID or anime title for ASC dupe search", nil)
	}
	cookies, err := loadSessionCookies(ctx, h.cfg.MainSettings.DBPath)
	if cause := ctx.Err(); cause != nil {
		return dupe.Failed(dupe.FailureRequest, "ASC search canceled", cause)
	}
	if err != nil || len(cookies) == 0 {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "import a current ASC session cookie", nil)
	}

	evidence := dupe.SearchEvidence{WorkScope: scope, Scope: "broad_work_query"}
	var entries []api.DupeEntry
	seen := make(map[int]bool)
	lastPage := 0
	for page := 1; page <= maxSearchPages; page++ {
		result, failure := readSearchPage(ctx, h.http, cookies, query, page)
		if cause := ctx.Err(); cause != nil {
			return dupe.Failed(dupe.FailureRequest, "ASC search canceled", cause)
		}
		switch failure.Code {
		case "":
		case "authentication_required":
			return dupe.NotRun(dupe.NotRunMissingCredentials, failure.Message, nil)
		case "unexpected_response":
			return dupe.Failed(dupe.FailureResponseParse, failure.Message, nil)
		default:
			return dupe.Failed(dupe.FailureRequest, failure.Message, nil)
		}
		if page > 1 && result.LastPage != lastPage {
			return dupe.Failed(dupe.FailureResponseParse, "ASC pagination changed during search", nil)
		}
		lastPage = result.LastPage
		for _, row := range result.Rows {
			if seen[row.ID] {
				return dupe.Failed(dupe.FailureResponseParse, "ASC search repeated a torrent", nil)
			}
			seen[row.ID] = true
			entries = append(entries, row.entry())
		}
		evidence.Pages = page
		if page == lastPage {
			evidence.Complete = true
			break
		}
	}
	if !evidence.Complete {
		evidence.Warnings = []string{"ASC search reached its page limit"}
	}
	return dupe.ResolvedWithSearch(entries, nil, evidence)
}

func buildASCSearchURL(query string, page int) string {
	values := url.Values{"q": {query}}
	if page > 1 {
		values.Set("page", strconv.Itoa(page))
	}
	return siteURL + "/torrents?" + values.Encode()
}

func (row searchRow) entry() api.DupeEntry {
	id := strconv.Itoa(row.ID)
	return api.DupeEntry{
		ID:        id,
		Name:      strings.TrimSpace(row.Name),
		Link:      siteURL + "/torrents/" + id,
		SizeBytes: row.Size,
		SizeKnown: row.Size > 0,
		Internal:  row.Internal,
		Res:       resolutionBadge(row.Badges),
	}
}

func resolutionBadge(badges []searchBadge) string {
	resolution := ""
	for _, badge := range badges {
		if badge.Kind != "resolution" {
			continue
		}
		label := strings.ToLower(strings.TrimSpace(badge.Label))
		switch label {
		case "4k":
			label = "2160p"
		case "2160p", "1080p", "1080i", "720p", "576p", "576i", "480p", "480i":
		default:
			continue
		}
		if resolution != "" && resolution != label {
			return ""
		}
		resolution = label
	}
	return resolution
}

func resolveASCIMDb(meta api.DuplicateSubject) string {
	if meta.Identity.IMDBID > 0 {
		return providerid.IMDb(meta.Identity.IMDBID).Prefixed()
	}
	return ""
}

func resolveASCTitle(meta api.DuplicateSubject) string {
	title := strings.TrimSpace(meta.Release.Title)
	if title == "" && meta.Projection != nil {
		title = dupe.ProjectedSearchName(meta)
	}
	if title == "" {
		title = strings.TrimSpace(meta.ReleaseName)
	}
	return meta.EffectiveMetadata.PreferredTitle(title)
}
