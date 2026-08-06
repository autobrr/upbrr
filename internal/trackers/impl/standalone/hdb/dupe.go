// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

type dupeSearcher struct {
	cfg      config.Config
	http     *http.Client
	logger   api.Logger
	endpoint string
	maxPages int
}

const hdbDupePageLimit = 100

type hdbDupeRequest struct {
	Username string            `json:"username"`
	Passkey  string            `json:"passkey"`
	Category []int             `json:"category,omitempty"`
	IMDB     map[string]string `json:"imdb,omitempty"`
	TVDB     map[string]int    `json:"tvdb,omitempty"`
	Search   string            `json:"search,omitempty"`
	Limit    int               `json:"limit"`
	Page     int               `json:"page"`
}

// newDuplicateAdapterAt returns a duplicate-search adapter bound to one
// immutable dependency set and tracker base URL.
func newDuplicateAdapterAt(deps dupe.Dependencies, baseURL string) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	_ = logger
	if logger == nil {
		logger = api.NopLogger{}
	}
	return &dupeSearcher{
		cfg:      cfg,
		http:     httpClient,
		logger:   logger,
		endpoint: strings.TrimRight(baseURL, "/") + "/api/torrents",
		maxPages: deps.MaxPages(100),
	}
}

func (s *dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	if s.http == nil {
		return dupe.Failed(dupe.FailureInternal, "HDB handler misconfigured: no HTTP client", nil)
	}
	username, passkey := hdbCredentials(s.cfg)
	if username == "" || passkey == "" {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing username/passkey for tracker", nil)
	}
	request := hdbDupeRequest{
		Username: username,
		Passkey:  passkey,
		Limit:    hdbDupePageLimit,
	}
	if category := hdbDupeCategoryID(meta); category > 0 {
		request.Category = []int{category}
	}
	searchMethod := "id"
	if meta.Identity.IMDBID != 0 {
		request.IMDB = map[string]string{"id": providerid.IMDb(meta.Identity.IMDBID).Digits()}
	} else if isHDBDupeTVCategory(meta) && meta.Identity.TVDBID != 0 {
		request.TVDB = map[string]int{"id": meta.Identity.TVDBID}
	}
	if request.IMDB == nil && request.TVDB == nil {
		query := dupe.ProjectedSearchName(meta)
		if meta.Projection == nil {
			query = firstHDBText(meta.ReleaseName, meta.Filename, meta.Release.Title)
		}
		if query == "" {
			s.logger.Warnf("dupechecking: HDB missing imdb/tvdb IDs and search text for %s", meta.SourcePath)
			return dupe.NotRun(dupe.NotRunMissingMetadata, "missing imdb/tvdb id for HDB dupe search", nil)
		}
		request.Search, searchMethod = query, "text_fallback"
		s.logger.Debugf("dupechecking: HDB falling back to text search for %s", meta.SourcePath)
	}
	if logPayload, err := json.Marshal(request); err != nil {
		s.logger.Debugf("dupechecking: HDB search payload_marshal_failed=%v source=%s", err, meta.SourcePath)
	} else {
		s.logger.Debugf("dupechecking: HDB search payload=%s source=%s", redaction.RedactValue(string(logPayload), nil), meta.SourcePath)
	}

	maxPages := s.maxPages
	if maxPages <= 0 {
		maxPages = 100
	}
	entries := make([]api.DupeEntry, 0)
	seenIDs := make(map[string]struct{})
	pages := 0
	complete := false
	warning := ""
	for page := 0; pages < maxPages; page++ {
		request.Page = page
		items, failureCode, fetchErr := s.fetchPage(ctx, request)
		if failureCode != "" {
			errorText := "none"
			if fetchErr != nil {
				errorText = redaction.RedactValue(fetchErr.Error(), nil)
			}
			s.logger.Warnf(
				"dupechecking: HDB search failed source=%s page=%d code=%s error=%s",
				meta.SourcePath,
				page,
				failureCode,
				errorText,
			)
			if pages == 0 {
				return dupe.Failed(failureCode, "HDB search failed", fetchErr)
			}
			warning = "HDB search stopped after a partial request failure"
			break
		}
		pages++
		accepted := 0
		malformed := false
		for _, rawItem := range items {
			item, ok := rawItem.(map[string]any)
			if !ok {
				malformed = true
				continue
			}
			id, filename := hdbString(item["id"]), hdbString(item["filename"])
			if id == "" {
				malformed = true
				continue
			}
			if _, seen := seenIDs[id]; seen {
				continue
			}
			seenIDs[id] = struct{}{}
			flags, flagsPresent := hdbTags(item)
			candidateType := hdbCandidateType(item["medium"])
			entry := api.DupeEntry{
				Name:          hdbString(item["name"]),
				ID:            id,
				Link:          "https://hdbits.org/details.php?id=" + id,
				Download:      "https://hdbits.org/download.php/" + url.QueryEscape(filename) + "?id=" + id + "&passkey=" + passkey,
				FileCount:     hdbInt(item["numfiles"]),
				Category:      hdbCandidateCategory(item["category"]),
				Type:          candidateType,
				CanonicalType: candidateType,
				Res:           hdbString(item["resolution"]),
				Codec:         hdbCandidateCodec(item["codec"]),
				Container:     hdbString(item["container"]),
				Group:         firstHDBText(hdbString(item["releaseGroup"]), hdbString(item["group"])),
				Flags:         flags,
				FlagsPresent:  flagsPresent,
				HDR:           dupe.NormalizeTrackerHDRFlags(flags, flagsPresent, false),
				Internal:      hdbInt(item["origin"]) == 1,
				Description:   hdbString(item["descr"]),
			}
			if size := hdbInt(item["size"]); size > 0 {
				entry.SizeKnown, entry.SizeBytes = true, int64(size)
			}
			entries = append(entries, entry)
			accepted++
		}
		switch {
		case malformed:
			warning = "HDB search returned malformed result evidence"
		case len(items) < hdbDupePageLimit:
			complete = true
		case accepted == 0:
			warning = "HDB search repeated a full result page"
		default:
			continue
		}
		break
	}
	if !complete && warning == "" {
		warning = "HDB search reached page bound before consuming a partial page"
	}
	warnings := []string(nil)
	if warning != "" {
		warnings = []string{warning}
	}
	s.logger.Debugf(
		"dupechecking: HDB returned %d entries for %s method=%s pages=%d complete=%t",
		len(entries),
		meta.SourcePath,
		searchMethod,
		pages,
		complete,
	)
	return dupe.ResolvedWithSearch(entries, warnings, dupe.SearchEvidence{
		Complete: complete,
		Pages:    pages,
		Scope:    "work_identity",
		Warnings: warnings,
	})
}

func (s *dupeSearcher) fetchPage(ctx context.Context, request hdbDupeRequest) ([]any, string, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, dupe.FailureInternal, fmt.Errorf("marshal HDB search page %d: %w", request.Page, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, dupe.FailureRequest, fmt.Errorf("build HDB search page %d request: %w", request.Page, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, dupe.FailureRequest, fmt.Errorf("request HDB search page %d: %w", request.Page, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, dupe.FailureResponseStatus, fmt.Errorf("HDB search page %d returned HTTP status %d", request.Page, resp.StatusCode)
	}
	var body map[string]any
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&body); err != nil {
		return nil, dupe.FailureResponseParse, fmt.Errorf("decode HDB search page %d: %w", request.Page, err)
	}
	if len(body) == 0 {
		return nil, dupe.FailureResponseParse, errors.New("HDB search returned an empty response")
	}
	if hdbInt(body["status"]) != 0 {
		return nil, dupe.FailureResponseStatus, errors.New("HDB API rejected search")
	}
	items, ok := body["data"].([]any)
	if !ok {
		return nil, dupe.FailureResponseParse, errors.New("HDB search response data is missing or invalid")
	}
	return items, "", nil
}

func firstHDBText(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// hdbCandidateCategory maps HDB category IDs to the shared movie and TV vocabulary.
func hdbCandidateCategory(value any) string {
	switch hdbInt(value) {
	case 1, 3, 4:
		return string(api.CanonicalCategoryMovie)
	case 2:
		return string(api.CanonicalCategoryTV)
	default:
		return hdbString(value)
	}
}

// hdbCandidateCodec maps HDB codec IDs to names understood by duplicate normalization.
func hdbCandidateCodec(value any) string {
	switch hdbInt(value) {
	case 1:
		return "H.264"
	case 2:
		return "MPEG-2"
	case 3:
		return "VC-1"
	case 4:
		return "XviD"
	case 5:
		return "H.265"
	case 6:
		return "VP9"
	default:
		return hdbString(value)
	}
}

// hdbCandidateType maps HDB medium IDs and labels to the shared duplicate type vocabulary.
func hdbCandidateType(value any) string {
	switch hdbInt(value) {
	case 1:
		return "DISC"
	case 3:
		return "ENCODE"
	case 4:
		return "HDTV"
	case 5:
		return "REMUX"
	case 6:
		return "WEBDL"
	}
	normalized := normalizeHDBType(hdbString(value))
	switch normalized {
	case "BLURAY", "HDDVD", "DISC":
		return "DISC"
	case "ENCODE", "HDTV", "REMUX", "WEBDL", "WEBRIP":
		return normalized
	default:
		return ""
	}
}

// hdbTags normalizes array or comma-delimited tags and reports whether the response supplied tag evidence.
func hdbTags(item map[string]any) ([]string, bool) {
	value, present := item["tags"]
	if !present {
		return nil, false
	}
	var tags []string
	switch typed := value.(type) {
	case []any:
		for _, raw := range typed {
			if tag := hdbString(raw); tag != "" {
				tags = append(tags, tag)
			}
		}
	case []string:
		for _, raw := range typed {
			if tag := strings.TrimSpace(raw); tag != "" {
				tags = append(tags, tag)
			}
		}
	case string:
		for raw := range strings.SplitSeq(typed, ",") {
			if tag := strings.TrimSpace(raw); tag != "" {
				tags = append(tags, tag)
			}
		}
	}
	return tags, true
}

func isHDBDupeTVCategory(meta api.DuplicateSubject) bool {
	category, err := meta.Identity.RequireCategory()
	return err == nil && category == api.CanonicalCategoryTV
}

func hdbDupeCategoryID(meta api.DuplicateSubject) int {
	category, _ := meta.Identity.RequireCategory()
	switch category {
	case api.CanonicalCategoryMovie:
		return 1
	case api.CanonicalCategoryTV:
		return 2
	case api.CanonicalCategoryUnknown:
	}
	genres, keywords := "", ""
	if meta.ProviderMetadata.TMDB != nil {
		genres = strings.ToLower(strings.TrimSpace(meta.ProviderMetadata.TMDB.Genres))
		keywords = strings.ToLower(strings.TrimSpace(meta.ProviderMetadata.TMDB.Keywords))
	}
	if strings.Contains(genres, "documentary") || strings.Contains(keywords, "documentary") {
		return 3
	}
	if meta.ProviderMetadata.IMDB != nil {
		imdbType := strings.ToLower(strings.TrimSpace(meta.ProviderMetadata.IMDB.Type))
		imdbGenres := strings.ToLower(strings.TrimSpace(meta.ProviderMetadata.IMDB.Genres))
		if strings.Contains(imdbType, "concert") || (strings.Contains(imdbType, "video") && strings.Contains(imdbGenres, "music")) {
			return 4
		}
	}
	return 0
}
