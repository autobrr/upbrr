// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

type antHandler struct {
	cfg    config.Config
	http   *http.Client
	logger api.Logger
}

func (h antHandler) Search(ctx context.Context, meta api.PreparedMetadata, _ string) ([]api.DupeEntry, []string, error) {
	_, apiKey, ok := trackerCfgWithAPIKey(h.cfg, "ANT")
	if !ok {
		return nil, []string{noteSkip("missing api_key for tracker")}, nil
	}
	params := url.Values{}
	params.Set("t", "search")
	params.Set("o", "json")
	switch {
	case meta.ExternalIDs.TMDBID != 0:
		params.Set("tmdb", strconv.Itoa(meta.ExternalIDs.TMDBID))
	case meta.ExternalIDs.IMDBID != 0:
		params.Set("imdb", strconv.Itoa(meta.ExternalIDs.IMDBID))
	default:
		return nil, []string{noteSkip("missing tmdb/imdb id for ANT dupe search")}, nil
	}
	headers := map[string]string{
		"User-Agent": "upbrr",
		"X-API-Key":  apiKey,
	}

	status, payload, err := doJSONGet(ctx, h.http, "https://anthelion.me/api.php", params, headers)
	if err != nil {
		return nil, []string{noteSkip("ANT request failed")}, nil
	}
	if status < 200 || status >= 300 || len(payload) == 0 {
		return nil, []string{noteSkip("ANT search failed")}, nil
	}

	items, _ := payload["item"].([]any)
	entries := make([]api.DupeEntry, 0, len(items))
	targetResolution := strings.ToLower(strings.TrimSpace(meta.Release.Resolution))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if targetResolution != "" && strings.ToLower(stringFromAny(item["resolution"])) != targetResolution {
			continue
		}
		files := make([]string, 0)
		fileCount := int(intFromAny(item["fileCount"]))
		if rawFiles, ok := item["files"].([]any); ok {
			for _, rf := range rawFiles {
				if fileMap, ok := rf.(map[string]any); ok {
					name := stringFromAny(fileMap["name"])
					if name != "" {
						files = append(files, name)
					}
				}
			}
			if fileCount == 0 {
				fileCount = len(files)
			}
		}
		entry := api.DupeEntry{
			Name:      stringFromAny(item["fileName"]),
			Files:     files,
			FileCount: fileCount,
			Link:      stringFromAny(item["guid"]),
			Download:  strings.ReplaceAll(stringFromAny(item["link"]), "&amp;", "&"),
		}
		if entry.Name == "" && len(files) > 0 {
			entry.Name = files[0]
		}
		size := intFromAny(item["size"])
		if size > 0 {
			entry.SizeKnown = true
			entry.SizeBytes = size
		}
		if rawFlags, ok := item["flags"].([]any); ok {
			for _, flag := range rawFlags {
				f := stringFromAny(flag)
				if f != "" {
					entry.Flags = append(entry.Flags, f)
				}
			}
		}
		entries = append(entries, entry)
	}
	return entries, nil, nil
}
