// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mtv

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	mtvTorznabEndpoint = "https://www.morethantv.me/api/torznab"
	mtvSearchLimit     = 100
)

type dupeSearcher struct {
	cfg      config.Config
	http     *http.Client
	maxPages int
}

// newDuplicateAdapter returns a duplicate-search adapter bound to one immutable dependency set.
func newDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	_ = logger
	return &dupeSearcher{
		cfg:      cfg,
		http:     httpClient,
		maxPages: deps.MaxPages(100),
	}
}

func (h dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	apiKey := mtvAPIKey(h.cfg)
	if apiKey == "" {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing api_key for tracker", nil)
	}

	params := url.Values{}
	params.Set("t", "search")
	params.Set("apikey", apiKey)
	params.Set("limit", strconv.Itoa(mtvSearchLimit))

	switch {
	case meta.Identity.IMDBID != 0:
		params.Set("imdbid", providerid.IMDb(meta.Identity.IMDBID).Prefixed())
	case meta.Identity.TMDBID != 0:
		params.Set("tmdbid", strconv.Itoa(meta.Identity.TMDBID))
	case isMTVTVCategory(meta) && meta.Identity.TVDBID != 0:
		params.Set("tvdbid", strconv.Itoa(meta.Identity.TVDBID))
	default:
		query := cleanMTVSearchTitle(meta)
		if query == "" {
			return dupe.NotRun(dupe.NotRunMissingMetadata, "missing imdb/tmdb/tvdb id or title for MTV dupe search", nil)
		}
		params.Set("q", query)
	}

	maxPages := h.maxPages
	if maxPages <= 0 {
		maxPages = 100
	}
	entries := make([]api.DupeEntry, 0)
	offset := 0
	complete := false
	pages := 0
	incompleteWarning := ""
	for pages < maxPages {
		pageParams := cloneMTVValues(params)
		if offset > 0 {
			pageParams.Set("offset", strconv.Itoa(offset))
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, mtvTorznabEndpoint, nil)
		if err != nil {
			return dupe.Failed(dupe.FailureRequest, "MTV search failed", fmt.Errorf("build MTV request: %w", err))
		}
		req.URL.RawQuery = pageParams.Encode()

		resp, err := h.http.Do(req)
		if err != nil {
			return dupe.Failed(dupe.FailureRequest, "MTV search failed", err)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			return dupe.Failed(dupe.FailureResponseStatus, "MTV search failed", nil)
		}

		var payload mtvRSS
		decodeErr := xml.NewDecoder(resp.Body).Decode(&payload)
		resp.Body.Close()
		if decodeErr != nil {
			return dupe.Failed(dupe.FailureResponseParse, "MTV response parse failed", decodeErr)
		}
		if strings.EqualFold(payload.XMLName.Local, "error") {
			return dupe.Failed(dupe.FailureResponseStatus, "MTV API rejected search", nil)
		}
		if !strings.EqualFold(payload.XMLName.Local, "rss") {
			return dupe.Failed(dupe.FailureResponseParse, "MTV response parse failed", nil)
		}
		pages++
		for _, item := range payload.Channel.Items {
			if entry, ok := mtvDupeEntry(item); ok {
				entries = append(entries, entry)
			}
		}
		response := payload.Channel.Response
		if response == nil {
			if len(payload.Channel.Items) < mtvSearchLimit {
				complete = true
			} else {
				incompleteWarning = "MTV search reached the result limit without pagination support; results may be truncated"
			}
			break
		}
		if response.Offset == nil || response.Total == nil {
			incompleteWarning = "MTV search returned incomplete pagination metadata"
			break
		}
		total := *response.Total
		responseOffset := *response.Offset
		if responseOffset < 0 || total < 0 || responseOffset != offset {
			incompleteWarning = "MTV search returned inconsistent pagination metadata"
			break
		}
		nextOffset := responseOffset + len(payload.Channel.Items)
		switch {
		case nextOffset == total:
			complete = true
		case len(payload.Channel.Items) > 0 && nextOffset < total:
			offset = nextOffset
			continue
		case nextOffset > total:
			incompleteWarning = "MTV search returned inconsistent pagination metadata"
		default:
			incompleteWarning = "MTV search pagination stopped before the advertised result total"
		}
		break
	}
	warnings := []string(nil)
	if !complete {
		if incompleteWarning == "" {
			incompleteWarning = "MTV search reached the pagination page limit before proving completeness"
		}
		warnings = []string{incompleteWarning}
	}
	return dupe.ResolvedWithSearch(entries, warnings, dupe.SearchEvidence{
		Complete: complete,
		Pages:    pages,
		Scope:    "work_identity",
		Warnings: warnings,
	})
}

func mtvDupeEntry(item mtvItem) (api.DupeEntry, bool) {
	title := strings.TrimSpace(item.Title)
	if title == "" {
		return api.DupeEntry{}, false
	}

	fileCount := parsePositiveInt(item.Files)
	sizeBytes := parsePositiveInt64(item.Size)
	attributes := make(map[string]string)
	var hdrFlags []string
	hdrFieldPresent := false
	for _, attr := range item.allAttrs() {
		name := strings.ToLower(strings.TrimSpace(attr.Name))
		value := strings.TrimSpace(attr.Value)
		if name == "" {
			continue
		}
		isHDRField := name == "hdr" || name == "dolbyvision" || name == "dv"
		hdrFieldPresent = hdrFieldPresent || isHDRField
		if value == "" {
			continue
		}
		attributes[name] = value
		switch {
		case fileCount == 0 && (name == "files" || name == "file_count" || name == "filecount"):
			fileCount = parsePositiveInt(value)
		case sizeBytes == 0 && name == "size":
			sizeBytes = parsePositiveInt64(value)
		case isHDRField:
			hdrFlags = appendUniqueMTVFlags(hdrFlags, mtvHDRFlags(value)...)
		}
	}

	guid := strings.TrimSpace(item.GUID)
	download := strings.TrimSpace(item.Link)
	if download == "" {
		download = strings.TrimSpace(item.Enclosure.URL)
	}
	entry := api.DupeEntry{
		Name:         title,
		FileCount:    fileCount,
		ID:           guid,
		Link:         guid,
		Download:     strings.ReplaceAll(download, "&amp;", "&"),
		Res:          attributes["resolution"],
		Type:         attributes["type"],
		Source:       attributes["source"],
		Category:     attributes["category"],
		Codec:        attributes["codec"],
		Attributes:   attributes,
		Flags:        hdrFlags,
		FlagsPresent: hdrFieldPresent,
	}
	if len(hdrFlags) > 0 {
		entry.HDR = dupe.NormalizeTrackerHDRFlags(hdrFlags, true, false)
	}
	if sizeBytes > 0 {
		entry.SizeKnown = true
		entry.SizeBytes = sizeBytes
	}
	return entry, true
}

func mtvHDRFlags(value string) []string {
	upper := strings.ToUpper(strings.TrimSpace(value))
	var flags []string
	if strings.Contains(upper, "DOLBY VISION") || strings.Contains(upper, "DOVI") || mtvTokenPresent(upper, "DV") {
		flags = append(flags, "DV")
	}
	if strings.Contains(upper, "HDR10+") || strings.Contains(upper, "HDR10PLUS") {
		flags = append(flags, "HDR10+")
	} else if strings.Contains(upper, "HDR10") || mtvTokenPresent(upper, "HDR") {
		flags = append(flags, "HDR10")
	}
	if mtvTokenPresent(upper, "HLG") {
		flags = append(flags, "HLG")
	}
	return flags
}

func mtvTokenPresent(value string, token string) bool {
	return slices.Contains(strings.FieldsFunc(value, func(r rune) bool {
		return r == '.' || r == ' ' || r == '_' || r == '-' || r == ',' || r == '|' || r == '/' || r == '+'
	}), token)
}

func appendUniqueMTVFlags(flags []string, values ...string) []string {
	for _, value := range values {
		if !slices.Contains(flags, value) {
			flags = append(flags, value)
		}
	}
	return flags
}

func cloneMTVValues(values url.Values) url.Values {
	cloned := make(url.Values, len(values))
	for key, entries := range values {
		cloned[key] = append([]string(nil), entries...)
	}
	return cloned
}

// isMTVTVCategory reports whether MTV torznab searches may use a TVDB ID query.
// Canonical movie identity suppresses TVDB queries.
func isMTVTVCategory(meta api.DuplicateSubject) bool {
	category, err := meta.Identity.RequireCategory()
	return err == nil && category == api.CanonicalCategoryTV
}

func cleanMTVSearchTitle(meta api.DuplicateSubject) string {
	if meta.Projection != nil {
		return dupe.ProjectedSearchName(meta)
	}
	query := strings.TrimSpace(meta.Release.Title)
	if query == "" {
		query = strings.TrimSpace(meta.ReleaseName)
	}
	if query == "" {
		return ""
	}
	query = strings.ReplaceAll(query, ": ", " ")
	query = strings.ReplaceAll(query, "’", "")
	query = strings.ReplaceAll(query, "'", "")
	return strings.Join(strings.Fields(query), " ")
}

func parsePositiveInt(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

func parsePositiveInt64(value string) int64 {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

type mtvRSS struct {
	XMLName xml.Name
	Channel mtvChannel `xml:"channel"`
}

type mtvChannel struct {
	Items    []mtvItem          `xml:"item"`
	Response *mtvSearchResponse `xml:"response"`
}

type mtvSearchResponse struct {
	Offset *int `xml:"offset,attr"`
	Total  *int `xml:"total,attr"`
}

type mtvItem struct {
	Title        string       `xml:"title"`
	Files        string       `xml:"files"`
	Size         string       `xml:"size"`
	GUID         string       `xml:"guid"`
	Link         string       `xml:"link"`
	Enclosure    mtvEnclosure `xml:"enclosure"`
	Attrs        []mtvAttr    `xml:"attr"`
	TorznabAttrs []mtvAttr    `xml:"http://torznab.com/schemas/2015/feed attr"`
}

func (i mtvItem) allAttrs() []mtvAttr {
	if len(i.Attrs) == 0 {
		return i.TorznabAttrs
	}
	if len(i.TorznabAttrs) == 0 {
		return i.Attrs
	}
	combined := make([]mtvAttr, 0, len(i.Attrs)+len(i.TorznabAttrs))
	combined = append(combined, i.Attrs...)
	combined = append(combined, i.TorznabAttrs...)
	return combined
}

type mtvAttr struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type mtvEnclosure struct {
	URL string `xml:"url,attr"`
}

func mtvAPIKey(cfg config.Config) string {
	for name, entry := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), "MTV") {
			return strings.TrimSpace(entry.APIKey)
		}
	}
	return ""
}
