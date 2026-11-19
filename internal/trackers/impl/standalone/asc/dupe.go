// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

const maxSearchPages = 5

var episodeSuffix = regexp.MustCompile(`\s-\sS(\d{1,3})(?:E(\d{1,4}))?\s*$`)

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
	if err != nil || len(cookies) == 0 {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "import a current ASC session cookie", nil)
	}

	evidence := dupe.SearchEvidence{WorkScope: scope, Scope: "broad_work_query"}
	var entries []api.DupeEntry
	seen := make(map[int]bool)
	lastPage := 0
	for page := 1; page <= maxSearchPages; page++ {
		result, failure := readSearchPage(ctx, h.http, cookies, query, page)
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
	entry := api.DupeEntry{
		ID:        id,
		Name:      strings.TrimSpace(row.Name),
		Link:      siteURL + "/torrents/" + id,
		SizeBytes: row.Size,
		SizeKnown: row.Size > 0,
		Internal:  row.Internal,
	}
	if match := episodeSuffix.FindStringSubmatch(row.Name); match != nil {
		entry.Season, _ = strconv.Atoi(match[1])
		entry.Episode, _ = strconv.Atoi(match[2])
		entry.Pack = match[2] == ""
	}
	return entry
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
