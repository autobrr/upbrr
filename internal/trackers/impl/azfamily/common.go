// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	xhtml "golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

var (
	azTokenPattern     = regexp.MustCompile(`name="_token"\s+content="([^"]+)"`)
	azTaskIDPattern    = regexp.MustCompile(`/(\d+)$`)
	azTorrentIDPattern = regexp.MustCompile(`/torrent/(\d+)`)
)

type mediaLookupResult struct {
	MediaCode string
	Missing   bool
}

type taskInfo struct {
	TaskID      string
	InfoHash    string
	RedirectURL string
}

type languageBundle struct {
	Audio     []string
	Subtitles []string
}

func category(meta api.PreparedMetadata) string {
	if value := strings.TrimSpace(meta.ExternalIDs.Category); value != "" {
		return value
	}
	if value := strings.TrimSpace(meta.MediaInfoCategory); value != "" {
		return value
	}
	if meta.ExternalMetadata.TMDB != nil && strings.TrimSpace(meta.ExternalMetadata.TMDB.Category) != "" {
		return meta.ExternalMetadata.TMDB.Category
	}
	return "MOVIE"
}

func categoryID(meta api.PreparedMetadata) string {
	switch strings.ToUpper(strings.TrimSpace(category(meta))) {
	case "MOVIE":
		return "1"
	case "TV":
		return "2"
	default:
		return ""
	}
}

func categorySlug(meta api.PreparedMetadata) string {
	if strings.EqualFold(category(meta), "TV") {
		return "tv"
	}
	return "movie"
}

func isTV(meta api.PreparedMetadata) bool {
	return strings.EqualFold(category(meta), "TV")
}

func imdbForLookup(meta api.PreparedMetadata) string {
	if meta.ExternalIDs.IMDBID != 0 {
		return fmt.Sprintf("tt%07d", meta.ExternalIDs.IMDBID)
	}
	if meta.MediaInfoIMDBID != 0 {
		return fmt.Sprintf("tt%07d", meta.MediaInfoIMDBID)
	}
	return ""
}

func tmdbForLookup(meta api.PreparedMetadata) string {
	if meta.ExternalIDs.TMDBID != 0 {
		return strconv.Itoa(meta.ExternalIDs.TMDBID)
	}
	if meta.MediaInfoTMDBID != 0 {
		return strconv.Itoa(meta.MediaInfoTMDBID)
	}
	return ""
}

func lookupTitle(meta api.PreparedMetadata) string {
	if title := strings.TrimSpace(meta.Release.Title); title != "" {
		return title
	}
	if meta.ExternalMetadata.TMDB != nil {
		if title := strings.TrimSpace(meta.ExternalMetadata.TMDB.Title); title != "" {
			return title
		}
	}
	return strings.TrimSpace(meta.Filename)
}

func tvCode(meta api.PreparedMetadata) string {
	if meta.SeasonInt <= 0 || meta.EpisodeInt <= 0 {
		return ""
	}
	return fmt.Sprintf("S%02dE%02d", meta.SeasonInt, meta.EpisodeInt)
}

func detectResolution(value string) string {
	lower := strings.ToLower(strings.TrimSpace(value))
	for _, candidate := range []string{"4320p", "2160p", "1080p", "1080i", "720p", "576p", "576i", "480p", "480i"} {
		if strings.Contains(lower, candidate) {
			return candidate
		}
	}
	return ""
}

func resolutionValue(meta api.PreparedMetadata) string {
	if meta.VideoWidth > 0 && meta.VideoHeight > 0 {
		return fmt.Sprintf("%dx%d", meta.VideoWidth, meta.VideoHeight)
	}
	return strings.TrimSpace(meta.Release.Resolution)
}

func videoQualityID(site siteDefinition, meta api.PreparedMetadata) string {
	resolution := strings.ToLower(strings.TrimSpace(meta.Release.Resolution))
	if resolution == "" {
		resolution = strings.ToLower(detectResolution(meta.ReleaseName))
	}
	if site.Name != "PHD" {
		resolutionInt, _ := strconv.Atoi(strings.NewReplacer("p", "", "i", "").Replace(resolution))
		if resolutionInt > 0 && resolutionInt < 720 {
			return "1"
		}
	}
	switch resolution {
	case "720p":
		return "2"
	case "1080p":
		return "3"
	case "2160p":
		return "6"
	case "1080i":
		return "7"
	case "4320p":
		return "8"
	default:
		return "0"
	}
}

func ripTypeName(meta api.PreparedMetadata) string {
	discType := strings.ToLower(strings.TrimSpace(meta.DiscType))
	if discType == "dvd" {
		return "DVD"
	}
	if discType == "bdmv" {
		return "BluRay Raw"
	}

	releaseOther := meta.Release.Other
	releaseSource := strings.ToLower(strings.TrimSpace(meta.Release.Source))
	for _, other := range releaseOther {
		if strings.ToLower(other) == "remux" {
			if releaseSource == "bluray" {
				return "BluRay REMUX"
			}
			if releaseSource == "dvd" {
				return "DVD Remux"
			}
		}
	}

	switch strings.ToLower(strings.TrimSpace(meta.Release.Source)) {
	case "bdrip":
		return "BDRip"
	case "bluray":
		return "BluRay"
	case "brrip":
		return "BRRip"
	case "dvd":
		return "DVD"
	case "dvdrip":
		return "DVDRip"
	case "hdrip":
		return "HDRip"
	case "hdtv":
		return "HDTV"
	case "sdtv":
		return "SDTV"
	case "vcd":
		return "VCD"
	case "vcdr":
		return "VCDRip"
	case "vhsrc":
		return "VHSRip"
	case "vodrip":
		return "VODRip"
	case "web-dl":
		return "WEB-DL"
	case "webrip":
		return "WEBRip"
	}

	return ""
}

// Avistaz and CinemaZ: BDRip, BluRay, BluRay Raw, BluRay REMUX, BRRip, DVD, DVD Remux, DVDRip, HDRip, HDTV, SDTV, VCD, VCDRip, VHSRip, VODRip, WEB-DL, WEBRip
// PrivateHD: BDRip, BluRay, BluRay Raw, HDRip, HDTV, REMUX, WEB-DL, WEBRip
func ripTypeID(site siteDefinition, meta api.PreparedMetadata) string {
	name := ripTypeName(meta)

	if site.Name == "AZ" || site.Name == "CZ" {
		switch name {
		case "BDRip":
			return "1"
		case "BluRay":
			return "2"
		case "BRRip":
			return "3"
		case "DVD":
			return "4"
		case "DVDRip":
			return "5"
		case "HDRip":
			return "6"
		case "HDTV":
			return "7"
		case "VCD":
			return "8"
		case "VCDRip":
			return "9"
		case "VHSRip":
			return "10"
		case "VODRip":
			return "11"
		case "WEB-DL":
			return "12"
		case "WEBRip":
			return "13"
		case "BluRay REMUX":
			return "14"
		case "BluRay Raw":
			return "15"
		case "SDTV":
			return "16"
		case "DVD Remux":
			return "17"
		}
	}

	if site.Name == "PHD" {
		switch name {
		case "BDRip":
			return "1"
		case "BluRay":
			return "2"
		case "BluRay Raw":
			return "15"
		case "HDRip":
			return "6"
		case "HDTV":
			return "7"
		case "REMUX":
			return "14"
		case "WEB-DL":
			return "12"
		case "WEBRip":
			return "13"
		}
	}

	return "0"
}

func anonEnabled(req trackers.UploadRequest) bool {
	return req.TrackerConfig.Anon
}

func absoluteURL(baseURL, location string) string {
	trimmed := strings.TrimSpace(location)
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		return trimmed
	}
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(trimmed, "/")
}

func extractPatternGroup(pattern *regexp.Regexp, value string) string {
	if pattern == nil {
		return ""
	}
	matches := pattern.FindStringSubmatch(value)
	if len(matches) < 2 {
		return ""
	}
	return strings.TrimSpace(matches[1])
}

func mustParseURL(raw string) *url.URL {
	parsed, _ := url.Parse(raw)
	return parsed
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func sortedKeys(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func attrValue(node *xhtml.Node, key string) string {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) {
			return strings.TrimSpace(attr.Val)
		}
	}
	return ""
}

func nodeText(node *xhtml.Node) string {
	if node == nil {
		return ""
	}
	if node.Type == xhtml.TextNode {
		return node.Data
	}
	var builder strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		builder.WriteString(nodeText(child))
	}
	return builder.String()
}

func valuesToMap(values url.Values) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, entries := range values {
		out[key] = strings.Join(entries, ",")
	}
	return out
}
