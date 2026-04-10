// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/pkg/api"
)

type mediaInfoIDs struct {
	Category string
	TMDBID   int
	IMDBID   int
	TVDBID   int
}

func (s *Service) ApplyMediaInfoIDs(ctx context.Context, meta api.PreparedMetadata) (api.PreparedMetadata, error) {
	select {
	case <-ctx.Done():
		return api.PreparedMetadata{}, ctx.Err()
	default:
	}

	if strings.TrimSpace(meta.DiscType) != "" {
		return meta, nil
	}

	ids, err := loadMediaInfoIDs(meta.MediaInfoJSONPath, meta.MediaInfoTextPath)
	if err != nil {
		if s.logger != nil {
			s.logger.Warnf("metadata: mediainfo id lookup failed: %v", err)
		}
		return meta, nil
	}
	if ids == nil || (ids.TMDBID == 0 && ids.IMDBID == 0 && ids.TVDBID == 0 && ids.Category == "") {
		return meta, nil
	}

	meta.MediaInfoCategory = ids.Category
	meta.MediaInfoTMDBID = ids.TMDBID
	meta.MediaInfoIMDBID = ids.IMDBID
	meta.MediaInfoTVDBID = ids.TVDBID

	trackerTMDB, trackerIMDB, trackerTVDB := resolveTrackerIDs(meta.TrackerData)
	if trackerTMDB != 0 && ids.TMDBID != 0 && trackerTMDB != ids.TMDBID {
		meta.MismatchedMediaInfoTMDBID = ids.TMDBID
	}
	if trackerIMDB != 0 && ids.IMDBID != 0 && trackerIMDB != ids.IMDBID {
		meta.MismatchedMediaInfoIMDBID = ids.IMDBID
	}
	if trackerTVDB != 0 && ids.TVDBID != 0 && trackerTVDB != ids.TVDBID {
		meta.MismatchedMediaInfoTVDBID = ids.TVDBID
	}

	return meta, nil
}

func loadMediaInfoIDs(jsonPath, textPath string) (*mediaInfoIDs, error) {
	if strings.TrimSpace(jsonPath) != "" {
		ids, err := parseMediaInfoJSON(jsonPath)
		if err == nil && ids != nil {
			return ids, nil
		}
		if err != nil {
			if strings.TrimSpace(textPath) == "" {
				return nil, err
			}
			ids, textErr := parseMediaInfoText(textPath)
			if textErr != nil {
				return nil, err
			}
			return ids, nil
		}
	}
	if strings.TrimSpace(textPath) != "" {
		ids, err := parseMediaInfoText(textPath)
		if err != nil {
			return nil, err
		}
		return ids, nil
	}
	return nil, nil
}

type mediaInfoJSON struct {
	Media struct {
		Tracks []mediaInfoTrack `json:"track"`
	} `json:"media"`
}

type mediaInfoTrack struct {
	Type  string                 `json:"@type"`
	Extra map[string]interface{} `json:"extra"`
}

func parseMediaInfoJSON(path string) (*mediaInfoIDs, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("metadata: read mediainfo json: %w", err)
	}
	var doc mediaInfoJSON
	if err := json.Unmarshal(payload, &doc); err != nil {
		return nil, fmt.Errorf("metadata: parse mediainfo json: %w", err)
	}

	extra := extractMediaInfoExtra(doc.Media.Tracks)
	if len(extra) == 0 {
		return nil, nil
	}
	ids := parseMediaInfoExtra(extra)
	return &ids, nil
}

func extractMediaInfoExtra(tracks []mediaInfoTrack) map[string]interface{} {
	if len(tracks) == 0 {
		return nil
	}
	for _, track := range tracks {
		if strings.EqualFold(track.Type, "General") && len(track.Extra) > 0 {
			return track.Extra
		}
	}
	for _, track := range tracks {
		if len(track.Extra) > 0 {
			return track.Extra
		}
	}
	return nil
}

func parseMediaInfoText(path string) (*mediaInfoIDs, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("metadata: read mediainfo text: %w", err)
	}
	lines := strings.Split(string(payload), "\n")
	ids := mediaInfoIDs{}
	var tvdbFallback int
	for _, line := range lines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key == "" || value == "" {
			continue
		}

		lower := strings.ToLower(key)
		switch {
		case strings.HasPrefix(lower, "tmdb"):
			if ids.TMDBID == 0 {
				category, tmdbID := parseTMDBValue(value)
				if tmdbID != 0 {
					ids.TMDBID = tmdbID
					ids.Category = metautil.FirstNonEmptyTrimmed(ids.Category, category)
				}
			}
		case strings.HasPrefix(lower, "imdb"):
			if ids.IMDBID == 0 {
				if imdbID := parseIMDBValue(value); imdbID != 0 {
					ids.IMDBID = imdbID
				}
			}
		case strings.HasPrefix(lower, "tvdb2"):
			if tvdbFallback == 0 {
				tvdbFallback = parseNumericID(value)
			}
		case strings.HasPrefix(lower, "tvdb"):
			if ids.TVDBID == 0 {
				ids.TVDBID = parseNumericID(value)
			}
		}
	}
	if ids.TVDBID == 0 && tvdbFallback != 0 {
		ids.TVDBID = tvdbFallback
	}
	if ids.TMDBID == 0 && ids.IMDBID == 0 && ids.TVDBID == 0 && ids.Category == "" {
		return nil, nil
	}
	return &ids, nil
}

func parseMediaInfoExtra(extra map[string]interface{}) mediaInfoIDs {
	ids := mediaInfoIDs{}
	var tvdbFallback int
	for key, raw := range extra {
		value := strings.TrimSpace(fmt.Sprint(raw))
		if value == "" {
			continue
		}
		lower := strings.ToLower(strings.TrimSpace(key))
		switch {
		case strings.HasPrefix(lower, "tmdb"):
			if ids.TMDBID == 0 {
				category, tmdbID := parseTMDBValue(value)
				if tmdbID != 0 {
					ids.TMDBID = tmdbID
					ids.Category = metautil.FirstNonEmptyTrimmed(ids.Category, category)
				}
			}
		case strings.HasPrefix(lower, "imdb"):
			if ids.IMDBID == 0 {
				if imdbID := parseIMDBValue(value); imdbID != 0 {
					ids.IMDBID = imdbID
				}
			}
		case strings.HasPrefix(lower, "tvdb2"):
			if tvdbFallback == 0 {
				tvdbFallback = parseNumericID(value)
			}
		case strings.HasPrefix(lower, "tvdb"):
			if ids.TVDBID == 0 {
				ids.TVDBID = parseNumericID(value)
			}
		}
	}
	if ids.TVDBID == 0 && tvdbFallback != 0 {
		ids.TVDBID = tvdbFallback
	}
	return ids
}

func parseTMDBValue(value string) (string, int) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", 0
	}

	category := ""
	lower := strings.ToLower(trimmed)
	parts := strings.Split(strings.Trim(lower, "/"), "/")
	if len(parts) >= 2 {
		candidate := parts[len(parts)-2]
		if candidate == "movie" || candidate == "tv" {
			category = candidate
		}
	}

	id := parseNumericID(trimmed)
	return category, id
}

func parseIMDBValue(value string) int {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}

	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "/title/") {
		idx := strings.Index(lower, "/title/")
		segment := trimmed[idx+len("/title/"):]
		segment = strings.TrimPrefix(segment, "tt")
		return parseNumericID(segment)
	}
	if strings.HasPrefix(lower, "tt") {
		return parseNumericID(trimmed[2:])
	}
	return parseNumericID(trimmed)
}

func parseNumericID(value string) int {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}

	end := len(trimmed) - 1
	for end >= 0 {
		ch := trimmed[end]
		if ch >= '0' && ch <= '9' {
			break
		}
		end--
	}
	if end < 0 {
		return 0
	}
	start := end
	for start >= 0 {
		ch := trimmed[start]
		if ch < '0' || ch > '9' {
			break
		}
		start--
	}

	id, err := strconv.Atoi(trimmed[start+1 : end+1])
	if err != nil {
		return 0
	}
	return id
}

func resolveTrackerIDs(records []api.TrackerMetadata) (int, int, int) {
	var tmdbID int
	var imdbID int
	var tvdbID int
	for _, record := range records {
		if tmdbID == 0 && record.TMDBID != 0 {
			tmdbID = record.TMDBID
		}
		if imdbID == 0 && record.IMDBID != 0 {
			imdbID = record.IMDBID
		}
		if tvdbID == 0 && record.TVDBID != 0 {
			tvdbID = record.TVDBID
		}
		if tmdbID != 0 && imdbID != 0 && tvdbID != 0 {
			break
		}
	}
	return tmdbID, imdbID, tvdbID
}
