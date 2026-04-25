// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/unit3dmeta"
	"github.com/autobrr/upbrr/pkg/api"
)

var (
	imdbURLIDPattern   = regexp.MustCompile(`(?i)tt(\d+)`)
	tmdbURLIDPattern   = regexp.MustCompile(`(?i)/(movie|tv)/(\d+)`)
	tvmazeURLIDPattern = regexp.MustCompile(`(?i)/shows/(\d+)`)
	tvdbURLIDPattern   = regexp.MustCompile(`(?i)/(series|movies|movie)/(\d+)`)
	malURLIDPattern    = regexp.MustCompile(`(?i)/(anime|manga)/(\d+)`)
	unit3dIDPattern    = regexp.MustCompile(`/(\d+)`)
)

type sourceLookupResolution struct {
	Tracker   string
	TrackerID string
	TMDBID    int
	IMDBID    int
	TVDBID    int
	TVmazeID  int
	MALID     int
	Mode      string
}

func applySourceLookupOverride(meta *api.PreparedMetadata) {
	if meta == nil {
		return
	}

	raw := strings.TrimSpace(meta.SourceLookupURL)
	if raw == "" {
		return
	}

	resolution, err := resolveSourceLookupURL(raw)
	if err != nil {
		meta.LookupWarnings = append(meta.LookupWarnings, "Source URL lookup failed; using default metadata lookup flow.")
		return
	}

	if resolution.Mode == "tracker" {
		trackerKey := strings.ToLower(strings.TrimSpace(resolution.Tracker))
		if trackerKey == "" || strings.TrimSpace(resolution.TrackerID) == "" {
			meta.LookupWarnings = append(meta.LookupWarnings, "Source URL did not contain a usable tracker ID; using default metadata lookup flow.")
			return
		}
		if meta.TrackerIDs == nil {
			meta.TrackerIDs = make(map[string]string)
		}
		meta.TrackerIDs[trackerKey] = strings.TrimSpace(resolution.TrackerID)
		meta.Trackers = []string{strings.ToUpper(trackerKey)}
		meta.MatchedTrackers = []string{strings.ToUpper(trackerKey)}
		meta.SourceLookupActive = true
		meta.SourceLookupMode = "tracker"
		meta.SourceLookupTracker = strings.ToUpper(trackerKey)
		meta.SourceLookupTrackerID = strings.TrimSpace(resolution.TrackerID)
		return
	}

	if resolution.Mode == "media" {
		if resolution.TMDBID > 0 && meta.ExternalIDOverrides.TMDBID == nil {
			tmdbID := resolution.TMDBID
			meta.ExternalIDOverrides.TMDBID = &tmdbID
		}
		if resolution.IMDBID > 0 && meta.ExternalIDOverrides.IMDBID == nil {
			imdbID := resolution.IMDBID
			meta.ExternalIDOverrides.IMDBID = &imdbID
		}
		if resolution.TVDBID > 0 && meta.ExternalIDOverrides.TVDBID == nil {
			tvdbID := resolution.TVDBID
			meta.ExternalIDOverrides.TVDBID = &tvdbID
		}
		if resolution.TVmazeID > 0 && meta.ExternalIDOverrides.TVmazeID == nil {
			tvmazeID := resolution.TVmazeID
			meta.ExternalIDOverrides.TVmazeID = &tvmazeID
		}
		if resolution.MALID > 0 && meta.ExternalIDOverrides.MALID == nil {
			malID := resolution.MALID
			meta.ExternalIDOverrides.MALID = &malID
		}
		meta.SourceLookupActive = true
		meta.SourceLookupMode = "media"
		meta.Trackers = nil
		meta.MatchedTrackers = nil
		meta.TrackerIDs = nil
		return
	}

	meta.LookupWarnings = append(meta.LookupWarnings, "Source URL lookup did not resolve supported IDs; using default metadata lookup flow.")
}

func resolveSourceLookupURL(raw string) (sourceLookupResolution, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return sourceLookupResolution{}, err
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if scheme != "http" && scheme != "https" {
		return sourceLookupResolution{}, url.InvalidHostError("unsupported scheme")
	}

	host := normalizedHost(parsed.Hostname())
	if host == "" {
		return sourceLookupResolution{}, url.InvalidHostError("missing host")
	}

	if tracker, trackerID, ok := extractUnit3DTrackerID(host, parsed.Path); ok {
		return sourceLookupResolution{Tracker: tracker, TrackerID: trackerID, Mode: "tracker"}, nil
	}

	path := parsed.EscapedPath()
	query := parsed.Query()
	if imdbMatch := imdbURLIDPattern.FindStringSubmatch(path); len(imdbMatch) == 2 {
		if value, convErr := strconv.Atoi(imdbMatch[1]); convErr == nil && value > 0 {
			return sourceLookupResolution{IMDBID: value, Mode: "media"}, nil
		}
	}
	if tmdbMatch := tmdbURLIDPattern.FindStringSubmatch(path); len(tmdbMatch) == 3 {
		if value, convErr := strconv.Atoi(tmdbMatch[2]); convErr == nil && value > 0 {
			return sourceLookupResolution{TMDBID: value, Mode: "media"}, nil
		}
	}
	if tvmazeMatch := tvmazeURLIDPattern.FindStringSubmatch(path); len(tvmazeMatch) == 2 {
		if value, convErr := strconv.Atoi(tvmazeMatch[1]); convErr == nil && value > 0 {
			return sourceLookupResolution{TVmazeID: value, Mode: "media"}, nil
		}
	}
	if tvdbMatch := tvdbURLIDPattern.FindStringSubmatch(path); len(tvdbMatch) == 3 {
		if value, convErr := strconv.Atoi(tvdbMatch[2]); convErr == nil && value > 0 {
			return sourceLookupResolution{TVDBID: value, Mode: "media"}, nil
		}
	}
	if tvdbID, ok := extractTVDBIDFromQuery(host, query); ok {
		return sourceLookupResolution{TVDBID: tvdbID, Mode: "media"}, nil
	}
	if malMatch := malURLIDPattern.FindStringSubmatch(path); len(malMatch) == 3 && host == "myanimelist.net" {
		if value, convErr := strconv.Atoi(malMatch[2]); convErr == nil && value > 0 {
			return sourceLookupResolution{MALID: value, Mode: "media"}, nil
		}
	}

	return sourceLookupResolution{}, url.InvalidHostError("no supported id in source url")
}

func extractTVDBIDFromQuery(host string, query url.Values) (int, bool) {
	if host != "thetvdb.com" {
		return 0, false
	}

	tab := strings.ToLower(strings.TrimSpace(query.Get("tab")))
	if tab != "" && tab != "series" && tab != "movie" && tab != "movies" {
		return 0, false
	}

	rawID := strings.TrimSpace(query.Get("id"))
	if rawID == "" {
		return 0, false
	}

	id, err := strconv.Atoi(rawID)
	if err != nil || id <= 0 {
		return 0, false
	}

	return id, true
}

func extractUnit3DTrackerID(host string, path string) (string, string, bool) {
	for _, tracker := range trackers.Unit3DTrackers() {
		baseURL, ok := unit3dmeta.BaseURL(tracker)
		if !ok {
			continue
		}
		parsedBase, err := url.Parse(baseURL)
		if err != nil {
			continue
		}
		if normalizedHost(parsedBase.Hostname()) != host {
			continue
		}
		match := unit3dIDPattern.FindStringSubmatch(path)
		if len(match) != 2 {
			return tracker, "", false
		}
		return tracker, match[1], true
	}
	return "", "", false
}

func normalizedHost(host string) string {
	trimmed := strings.ToLower(strings.TrimSpace(host))
	trimmed = strings.TrimPrefix(trimmed, "www.")
	return trimmed
}
