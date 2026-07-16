// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/datatypes"
	"github.com/autobrr/upbrr/pkg/api"
)

type dataLookup struct {
	cfg      config.Config
	http     *http.Client
	endpoint string
}

type ptpLookup struct {
	trackerID string
	imdbID    int
	infoHash  string
}

func (d *Definition) NewDataLookup(cfg config.Config, httpClient *http.Client, _ api.Logger) trackers.DataLookup {
	return &dataLookup{
		cfg:      cfg,
		http:     httpClient,
		endpoint: strings.TrimRight(d.baseURL, "/") + ptpTorrentPath,
	}
}

func (l *dataLookup) Lookup(ctx context.Context, req trackers.DataLookupRequest) (datatypes.Result, error) {
	apiUser, apiKey := ptpAPIKeys(l.cfg)
	if apiUser == "" || apiKey == "" {
		return datatypes.Result{}, nil
	}
	headers := map[string]string{"ApiUser": apiUser, "ApiKey": apiKey}
	foundID := strings.TrimSpace(req.TrackerID)
	var found ptpLookup
	var err error
	if foundID != "" {
		found, err = l.fetchByID(ctx, headers, foundID)
	} else {
		found, err = l.search(ctx, headers, req.SearchName)
		foundID = found.trackerID
	}
	if err != nil {
		return datatypes.Result{}, err
	}
	if found.imdbID == 0 && foundID == "" {
		return datatypes.Result{}, nil
	}
	result := datatypes.Result{
		TrackerID: foundID,
		IMDBID:    found.imdbID,
		InfoHash:  found.infoHash,
	}
	if foundID == "" || req.OnlyID && !req.KeepImages {
		return result, nil
	}
	description, err := l.description(ctx, headers, foundID)
	if err != nil {
		return result, err
	}
	report := CleanDescription(description, req.Meta.DiscType)
	if !req.OnlyID {
		result.Description = strings.TrimSpace(report.Description)
	}
	if req.KeepImages {
		result.Images = report.Images
	}
	return result, nil
}

func (l *dataLookup) fetchByID(ctx context.Context, headers map[string]string, id string) (ptpLookup, error) {
	body, err := l.getJSON(ctx, url.Values{"torrentid": {id}}, headers)
	if err != nil || len(body) == 0 {
		return ptpLookup{}, err
	}
	return parsePTPResponse(body, id, ""), nil
}

func (l *dataLookup) search(ctx context.Context, headers map[string]string, search string) (ptpLookup, error) {
	if strings.TrimSpace(search) == "" {
		return ptpLookup{}, nil
	}
	body, err := l.getJSON(ctx, url.Values{"searchstr": {search}}, headers)
	if err != nil || len(body) == 0 {
		return ptpLookup{}, err
	}
	for _, raw := range ptpSlice(body["Movies"]) {
		item := ptpMap(raw)
		lookup := parsePTPResponse(item, "", search)
		if id := ptpInt(item["ImdbId"]); id != 0 {
			lookup.imdbID = id
		}
		if lookup.imdbID != 0 || lookup.trackerID != "" {
			return lookup, nil
		}
	}
	return ptpLookup{}, nil
}

func (l *dataLookup) description(ctx context.Context, headers map[string]string, id string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("trackerdata: ptp description request: %w", err)
	}
	req.URL.RawQuery = url.Values{"id": {id}, "action": {"get_description"}}.Encode()
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := l.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("trackerdata: ptp description request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", nil
	}
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("trackerdata: ptp description read: %w", err)
	}
	return string(payload), nil
}

func (l *dataLookup) getJSON(ctx context.Context, params url.Values, headers map[string]string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("trackerdata: request: %w", err)
	}
	req.URL.RawQuery = params.Encode()
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := l.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trackerdata: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, nil
	}
	var result map[string]any
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("trackerdata: decode: %w", err)
	}
	return result, nil
}

func parsePTPResponse(body map[string]any, trackerID, searchTerm string) ptpLookup {
	selectedID, infoHash := strings.TrimSpace(trackerID), ""
	needle := strings.ToLower(strings.TrimSpace(searchTerm))
	for _, raw := range ptpSlice(body["Torrents"]) {
		item := ptpMap(raw)
		id := ptpString(item["Id"])
		releaseName := strings.ToLower(ptpString(item["ReleaseName"]))
		if selectedID == "" && needle != "" && strings.Contains(releaseName, needle) {
			selectedID, infoHash = id, ptpString(item["InfoHash"])
			break
		}
		if selectedID != "" && selectedID == id {
			infoHash = ptpString(item["InfoHash"])
			break
		}
		if selectedID == "" {
			selectedID, infoHash = id, ptpString(item["InfoHash"])
		}
	}
	return ptpLookup{
		trackerID: selectedID,
		imdbID:    ptpInt(body["ImdbId"]),
		infoHash:  infoHash,
	}
}

func ptpMap(value any) map[string]any { result, _ := value.(map[string]any); return result }
func ptpSlice(value any) []any        { result, _ := value.([]any); return result }
func ptpInt(value any) int {
	trimmed := strings.TrimPrefix(ptpString(value), "tt")
	parsed, _ := strconv.Atoi(trimmed)
	return parsed
}
