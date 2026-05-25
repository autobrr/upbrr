// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackerdata

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/autobrr/upbrr/internal/services/bbcode"
	"github.com/autobrr/upbrr/pkg/api"
)

type ptpLookup struct {
	trackerID string
	imdbID    int
	infoHash  string
}

func (c *Client) lookupPTP(
	ctx context.Context,
	trackerID string,
	meta api.PreparedMetadata,
	searchFileName string,
	onlyID bool,
	keepImages bool,
) (Result, error) {
	cfg, ok := c.trackerCfg("PTP")
	if !ok {
		return Result{}, nil
	}
	apiUser := strings.TrimSpace(cfg.PTPAPIUser)
	apiKey := strings.TrimSpace(cfg.PTPAPIKey)
	if apiUser == "" || apiKey == "" {
		return Result{}, nil
	}
	headers := map[string]string{"ApiUser": apiUser, "ApiKey": apiKey}

	foundID := strings.TrimSpace(trackerID)
	var lookup ptpLookup

	if foundID != "" {
		var err error
		lookup, err = c.ptpFetchByID(ctx, headers, foundID)
		if err != nil {
			return Result{}, err
		}
	} else {
		var err error
		lookup, err = c.ptpSearch(ctx, headers, searchFileName)
		if err != nil {
			return Result{}, err
		}
		foundID = lookup.trackerID
	}
	imdbID := lookup.imdbID
	infoHash := lookup.infoHash
	if imdbID == 0 && strings.TrimSpace(foundID) == "" {
		return Result{}, nil
	}

	result := Result{TrackerID: foundID, IMDBID: imdbID, InfoHash: infoHash}
	if strings.TrimSpace(foundID) == "" || (onlyID && !keepImages) {
		return result, nil
	}

	description, err := c.ptpDescription(ctx, headers, foundID)
	if err != nil {
		return result, err
	}
	report := bbcode.CleanPTPDescription(description, meta.DiscType)
	if !onlyID {
		result.Description = strings.TrimSpace(report.Description)
	}
	if keepImages {
		result.Images = report.Images
	}
	return result, nil
}

func (c *Client) ptpFetchByID(ctx context.Context, headers map[string]string, trackerID string) (ptpLookup, error) {
	params := url.Values{}
	params.Set("torrentid", trackerID)
	body, err := c.getJSON(ctx, c.ptpURL, params, headers)
	if err != nil || len(body) == 0 {
		return ptpLookup{}, err
	}
	return parsePTPResponse(body, trackerID, "")
}

func (c *Client) ptpSearch(ctx context.Context, headers map[string]string, search string) (ptpLookup, error) {
	if strings.TrimSpace(search) == "" {
		return ptpLookup{}, nil
	}
	params := url.Values{}
	params.Set("searchstr", search)
	body, err := c.getJSON(ctx, c.ptpURL, params, headers)
	if err != nil || len(body) == 0 {
		return ptpLookup{}, err
	}
	return parsePTPSearchResponse(body, search)
}

func (c *Client) ptpDescription(ctx context.Context, headers map[string]string, trackerID string) (string, error) {
	params := url.Values{}
	params.Set("id", trackerID)
	params.Set("action", "get_description")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.ptpURL, nil)
	if err != nil {
		return "", fmt.Errorf("trackerdata: ptp description request: %w", err)
	}
	req.URL.RawQuery = params.Encode()
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("trackerdata: ptp description request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil
	}
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("trackerdata: ptp description read: %w", err)
	}
	return string(payload), nil
}

func parsePTPResponse(body map[string]any, trackerID string, searchTerm string) (ptpLookup, error) {
	imdbID := intValue(body["ImdbId"])
	torrents, _ := body["Torrents"].([]any)
	selectedID := strings.TrimSpace(trackerID)
	infoHash := ""
	needle := strings.ToLower(strings.TrimSpace(searchTerm))
	for _, torrent := range torrents {
		item := mapValue(torrent)
		id := stringValue(item["Id"])
		releaseName := strings.ToLower(stringValue(item["ReleaseName"]))
		if selectedID == "" && needle != "" && strings.Contains(releaseName, needle) {
			selectedID = id
			infoHash = stringValue(item["InfoHash"])
			break
		}
		if selectedID != "" && selectedID == id {
			infoHash = stringValue(item["InfoHash"])
			break
		}
		if selectedID == "" {
			selectedID = id
			infoHash = stringValue(item["InfoHash"])
		}
	}
	return ptpLookup{trackerID: selectedID, imdbID: imdbID, infoHash: infoHash}, nil
}

func parsePTPSearchResponse(body map[string]any, searchTerm string) (ptpLookup, error) {
	movies, _ := body["Movies"].([]any)
	for _, movie := range movies {
		item := mapValue(movie)
		imdbID := intValue(item["ImdbId"])
		lookup, err := parsePTPResponse(item, "", searchTerm)
		if err != nil {
			return ptpLookup{}, err
		}
		if imdbID != 0 {
			lookup.imdbID = imdbID
		}
		if lookup.imdbID != 0 || lookup.trackerID != "" {
			return lookup, nil
		}
	}
	return ptpLookup{}, nil
}
