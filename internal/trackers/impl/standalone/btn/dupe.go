// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

const btnDupePageLimit = 100

var btnGroupEpisodePattern = regexp.MustCompile(`(?i)\bS(\d{1,3})E(\d{1,4})\b`)

type dupeSearcher struct {
	cfg      config.Config
	http     *http.Client
	logger   api.Logger
	endpoint string
	maxPages int
}

type btnDupePage struct {
	torrents   map[string]btnTorrent
	itemCount  int
	total      int
	totalKnown bool
	malformed  bool
}

type btnTorrentsResponse struct {
	Error  json.RawMessage `json:"error"`
	Result *struct {
		Results  json.RawMessage `json:"results"`
		Torrents json.RawMessage `json:"torrents"`
	} `json:"result"`
}

type btnTorrent struct {
	id            string
	groupID       string
	releaseName   string
	category      string
	source        string
	codec         string
	container     string
	resolution    string
	provider      string
	origin        string
	group         string
	providerIDs   []api.TrackerProviderID
	size          int64
	flags         []string
	flagsPresent  bool
	flagsComplete bool
	uploadedAt    time.Time
	timePresent   bool
	timeValid     bool
}

// NewDuplicateAdapter returns a duplicate-search adapter bound to one immutable dependency set.
func newDuplicateAdapter(deps dupe.Dependencies) dupe.Adapter {
	cfg := deps.BoundConfig()
	httpClient := deps.HTTPClient()
	logger := deps.Logger()
	return &dupeSearcher{
		cfg:      cfg,
		http:     httpClient,
		logger:   logger,
		endpoint: "https://api.broadcasthe.net/",
		maxPages: deps.MaxPages(100),
	}
}

func (s *dupeSearcher) Search(ctx context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	if s.http == nil {
		return dupe.Failed(dupe.FailureInternal, "BTN handler misconfigured: no HTTP client", nil)
	}
	token := strings.TrimSpace(config.ResolveBTNAPIToken(s.cfg))
	if token == "" {
		return dupe.NotRun(dupe.NotRunMissingCredentials, "missing api_key for tracker", nil)
	}
	if !isTV(meta) {
		return dupe.NotRun(dupe.NotRunUnsupportedContent, "BTN only supports TV dupe search", nil)
	}
	filter, workScope, daily := btnDupeFilter(meta)
	if len(filter) == 0 {
		return dupe.NotRun(dupe.NotRunMissingMetadata, "missing btn/tvdb id and title for BTN dupe search", nil)
	}
	mode := "ordinary"
	if daily {
		mode = "daily"
	}
	s.logger.Debugf(
		"dupechecking: BTN search started tracker=BTN mode=%s work_scope=%s page_limit=%d max_pages=%d",
		mode,
		workScope,
		btnDupePageLimit,
		s.maxPages,
	)

	entries := make([]api.DupeEntry, 0)
	seenIDs := make(map[string]struct{})
	offset := 0
	pages := 0
	reportedTotal := -1
	complete := false
	warning := ""
	for pages < s.maxPages {
		page, failureCode, fetchErr := s.fetchPage(ctx, token, filter, offset)
		if failureCode != "" {
			s.logger.Warnf(
				"dupechecking: BTN search page failed tracker=BTN offset=%d pages=%d complete=false code=%s",
				offset,
				pages,
				failureCode,
			)
			if pages == 0 {
				return dupe.Failed(failureCode, "BTN search failed", fetchErr)
			}
			warning = "BTN search stopped after a partial request failure"
			break
		}
		pages++
		switch {
		case !page.totalKnown:
			warning = "BTN search response omitted a valid results count"
		case reportedTotal < 0:
			reportedTotal = page.total
		case reportedTotal != page.total:
			warning = "BTN search returned inconsistent results counts"
		}

		for id, torrent := range page.torrents {
			id = strings.TrimSpace(id)
			if id == "" {
				page.malformed = true
				continue
			}
			if _, seen := seenIDs[id]; seen {
				continue
			}
			seenIDs[id] = struct{}{}
			entries = append(entries, torrent.dupeEntry())
		}
		if warning != "" {
			break
		}
		switch {
		case page.malformed:
			warning = "BTN search returned malformed torrent evidence"
		case reportedTotal == len(seenIDs):
			complete = true
		case reportedTotal >= 0 && len(seenIDs) > reportedTotal:
			warning = "BTN search returned more torrents than its results count"
		case daily:
			warning = "BTN daily search returned fewer torrents than its results count; pagination intentionally skipped"
		case page.itemCount == 0:
			warning = "BTN search stopped before receiving all reported results"
		default:
			offset += page.itemCount
			continue
		}
		break
	}
	if !complete && warning == "" {
		warning = "BTN search reached its page bound before receiving all reported results"
	}
	warnings := []string(nil)
	if warning != "" {
		warnings = []string{warning}
		s.logger.Warnf(
			"dupechecking: BTN search finished tracker=BTN mode=%s work_scope=%s pages=%d reported_total=%d unique_rows=%d complete=false decision=incomplete reason=%s",
			mode,
			workScope,
			pages,
			reportedTotal,
			len(entries),
			warning,
		)
	} else {
		s.logger.Debugf(
			"dupechecking: BTN search finished tracker=BTN mode=%s work_scope=%s pages=%d reported_total=%d unique_rows=%d complete=true decision=complete",
			mode,
			workScope,
			pages,
			reportedTotal,
			len(entries),
		)
	}
	scope := "work_identity"
	if daily {
		scope = "daily_episode"
	}
	return dupe.ResolvedWithSearch(entries, warnings, dupe.SearchEvidence{
		Complete:  complete,
		WorkScope: workScope,
		Pages:     pages,
		Scope:     scope,
		Warnings:  warnings,
	})
}

func (s *dupeSearcher) fetchPage(ctx context.Context, token string, filter map[string]any, offset int) (btnDupePage, string, error) {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      "upbrr-btn-search",
		"method":  "getTorrents",
		"params":  []any{token, filter, btnDupePageLimit, offset},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return btnDupePage{}, dupe.FailureInternal, fmt.Errorf("marshal BTN search page at offset %d: %w", offset, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(raw))
	if err != nil {
		return btnDupePage{}, dupe.FailureRequest, fmt.Errorf("build BTN search page at offset %d: %w", offset, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return btnDupePage{}, dupe.FailureRequest, fmt.Errorf("request BTN search page at offset %d: %w", offset, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return btnDupePage{}, dupe.FailureResponseStatus, fmt.Errorf("BTN search page at offset %d returned HTTP status %d", offset, resp.StatusCode)
	}
	var response btnTorrentsResponse
	if err := decodeBTNAPIJSON(resp.Body, &response); err != nil {
		return btnDupePage{}, dupe.FailureResponseParse, fmt.Errorf("decode BTN search page at offset %d: %w", offset, err)
	}
	if btnAPIErrorPresent(response.Error) {
		return btnDupePage{}, dupe.FailureResponseStatus, fmt.Errorf("BTN API rejected search page at offset %d", offset)
	}
	if response.Result == nil {
		return btnDupePage{}, dupe.FailureResponseParse, fmt.Errorf("BTN search page at offset %d omitted its result", offset)
	}
	torrents, itemCount, malformed, err := decodeBTNDupeTorrents(response.Result.Torrents)
	if err != nil {
		return btnDupePage{}, dupe.FailureResponseParse, fmt.Errorf("decode BTN torrents at offset %d: %w", offset, err)
	}
	total, totalKnown := btnResultCount(response.Result.Results)
	return btnDupePage{
		torrents:   torrents,
		itemCount:  itemCount,
		total:      total,
		totalKnown: totalKnown,
		malformed:  malformed,
	}, "", nil
}

func btnDupeFilter(meta api.DuplicateSubject) (map[string]any, dupe.WorkScope, bool) {
	title := searchTitle(meta)
	date, daily := btnDailyDate(meta.DailyEpisodeDate)
	groupID := trackerID(meta)
	filter := make(map[string]any)
	workScope := dupe.WorkScopeUnknown
	switch {
	case groupID != "":
		workScope = dupe.WorkScopeTrackerGroup
		filter["id"] = groupID
	case meta.Identity.TVDBID != 0:
		workScope = dupe.WorkScopeProviderID
		filter["tvdb"] = strconv.Itoa(meta.Identity.TVDBID)
	case title != "":
		workScope = dupe.WorkScopeTitle
	default:
		return nil, workScope, daily
	}
	if title != "" && (workScope == dupe.WorkScopeTitle || daily) {
		filter["search"] = strings.Join(strings.Fields(title), "%")
	}
	if daily {
		filter["category"] = "Episode"
		filter["name"] = date + "%"
	}
	return filter, workScope, daily
}

func btnDailyDate(value string) (string, bool) {
	date, err := time.Parse("2006-01-02", strings.TrimSpace(value))
	if err != nil {
		return "", false
	}
	return date.Format("2006.01.02"), true
}

func decodeBTNDupeTorrents(raw json.RawMessage) (map[string]btnTorrent, int, bool, error) {
	trimmed := strings.TrimSpace(string(raw))
	switch trimmed {
	case "", "null", "false", "[]", "{}", `""`, `"0"`:
		return nil, 0, false, nil
	}
	var encoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, 0, false, errors.New("invalid torrent result map")
	}
	torrents := make(map[string]btnTorrent, len(encoded))
	malformed := false
	for id, value := range encoded {
		var torrent map[string]any
		if err := json.Unmarshal(value, &torrent); err != nil || torrent == nil {
			malformed = true
			continue
		}
		torrents[id] = decodeBTNTorrent(id, torrent)
	}
	return torrents, len(encoded), malformed, nil
}

func btnResultCount(raw json.RawMessage) (int, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return 0, false
	}
	var text string
	switch typed := value.(type) {
	case json.Number:
		text = typed.String()
	case string:
		text = strings.TrimSpace(typed)
	default:
		return 0, false
	}
	total, err := strconv.Atoi(text)
	return total, err == nil && total >= 0
}

func btnAPIErrorPresent(raw json.RawMessage) bool {
	switch strings.TrimSpace(string(raw)) {
	case "", "null", "false", "{}", "[]":
		return false
	default:
		return true
	}
}

func isTV(meta api.DuplicateSubject) bool {
	category, err := meta.Identity.RequireCategory()
	return err == nil && category == api.CanonicalCategoryTV
}

func trackerID(meta api.DuplicateSubject) string {
	for key, value := range meta.TrackerIDs {
		if strings.EqualFold(strings.TrimSpace(key), "BTN") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func searchTitle(meta api.DuplicateSubject) string {
	candidates := []string{strings.TrimSpace(meta.Release.Title)}
	if meta.Projection != nil {
		candidates = append(candidates, dupe.ProjectedSearchName(meta))
	}
	if meta.ProviderMetadata.TVDB != nil {
		candidates = append(candidates, strings.TrimSpace(meta.ProviderMetadata.TVDB.Name), strings.TrimSpace(meta.ProviderMetadata.TVDB.NameEnglish))
	}
	if meta.ProviderMetadata.TVmaze != nil {
		candidates = append(candidates, strings.TrimSpace(meta.ProviderMetadata.TVmaze.Name))
	}
	candidates = append(candidates, strings.TrimSpace(meta.Filename), strings.TrimSpace(meta.ReleaseName))
	for _, candidate := range candidates {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

func decodeBTNTorrent(id string, values map[string]any) btnTorrent {
	hdr, hdrPresent := firstPresent(values, "HDR", "hdr")
	dolbyVision, dolbyVisionPresent := firstPresent(values, "DolbyVision", "dolbyVision", "DV", "dv")
	rawTime, timePresent := firstPresent(values, "Time", "time")
	uploadedAt, timeValid := parseBTNTimestamp(rawTime)
	return btnTorrent{
		id:            strings.TrimSpace(id),
		groupID:       btnString(first(values, "GroupID", "groupId")),
		releaseName:   firstBTNString(values, "ReleaseName", "releaseName", "SceneName", "Name", "name", "Series", "series"),
		category:      btnString(first(values, "Category", "category")),
		source:        btnString(first(values, "Source", "source")),
		codec:         btnString(first(values, "Codec", "codec")),
		container:     btnString(first(values, "Container", "container")),
		resolution:    btnString(first(values, "Resolution", "resolution")),
		provider:      btnString(first(values, "Provider", "provider", "Service", "service")),
		origin:        btnString(first(values, "Origin", "origin")),
		group:         btnString(first(values, "GroupName", "groupName", "ReleaseGroup", "releaseGroup")),
		providerIDs:   btnProviderIDs(values),
		size:          btnInt(first(values, "Size", "size")),
		flags:         btnFlags(hdr, dolbyVision),
		flagsPresent:  hdrPresent || dolbyVisionPresent,
		flagsComplete: hdrPresent && dolbyVisionPresent,
		uploadedAt:    uploadedAt,
		timePresent:   timePresent,
		timeValid:     timeValid,
	}
}

func (torrent btnTorrent) dupeEntry() api.DupeEntry {
	season, episode := btnGroupEpisode(torrent.group)
	entry := api.DupeEntry{
		Name:          torrent.name(),
		ID:            torrent.id,
		Link:          torrent.link(),
		Res:           torrent.resolution,
		Category:      torrent.category,
		Season:        season,
		Episode:       episode,
		Pack:          strings.EqualFold(strings.TrimSpace(torrent.category), "season"),
		Source:        torrent.source,
		Codec:         torrent.codec,
		Container:     torrent.container,
		Provider:      torrent.provider,
		ProviderIDs:   append([]api.TrackerProviderID(nil), torrent.providerIDs...),
		Group:         torrent.group,
		ReleaseOrigin: torrent.origin,
		Internal:      isBTNInternalGroupName(torrent.group),
		Flags:         append([]string(nil), torrent.flags...),
		FlagsPresent:  torrent.flagsPresent,
		FlagsComplete: torrent.flagsComplete,
	}
	if torrent.size > 0 {
		entry.SizeKnown, entry.SizeBytes = true, torrent.size
	}
	return entry
}

func btnGroupEpisode(groupName string) (int, int) {
	match := btnGroupEpisodePattern.FindStringSubmatch(groupName)
	if len(match) != 3 {
		return 0, 0
	}
	season, _ := strconv.Atoi(match[1])
	episode, _ := strconv.Atoi(match[2])
	return season, episode
}

func (torrent btnTorrent) name() string {
	for _, candidate := range []string{torrent.releaseName, torrent.id} {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

func (torrent btnTorrent) link() string {
	if torrent.groupID == "" || torrent.id == "" {
		return ""
	}
	return "https://broadcasthe.net/torrents.php?id=" + torrent.groupID + "&torrentid=" + torrent.id
}

func btnFlags(values ...any) []string {
	out := make([]string, 0, 2)
	for index, raw := range values {
		value := btnString(raw)
		upper := strings.ToUpper(strings.TrimSpace(value))
		switch upper {
		case "", "0", "FALSE", "NO":
			continue
		case "1", "TRUE", "YES":
			if index == 0 {
				upper = "HDR"
			} else {
				upper = "DV"
			}
		}
		out = append(out, upper)
	}
	return out
}

func btnProviderIDs(values map[string]any) []api.TrackerProviderID {
	fields := []struct {
		provider string
		keys     []string
	}{
		{provider: "btn", keys: []string{"GroupID", "groupId"}},
		{provider: "tvdb", keys: []string{"TVDBID", "TvdbID", "tvdbId", "tvdb"}},
		{provider: "imdb", keys: []string{"IMDBID", "ImdbID", "imdbId", "imdb"}},
		{provider: "tvrage", keys: []string{"TVRageID", "TvrageID", "tvrageId", "tvrage"}},
	}
	ids := make([]api.TrackerProviderID, 0, len(fields))
	for _, field := range fields {
		if value := btnString(first(values, field.keys...)); value != "" && value != "0" {
			ids = append(ids, api.TrackerProviderID{Provider: field.provider, Value: value})
		}
	}
	return ids
}

func firstBTNString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := btnString(values[key]); value != "" {
			return value
		}
	}
	return ""
}

func firstPresent(values map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if value, ok := values[key]; ok && value != nil {
			return value, true
		}
	}
	return nil, false
}

func first(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value
		}
	}
	return nil
}

func btnString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
