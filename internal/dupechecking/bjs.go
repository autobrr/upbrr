// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	xhtml "golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/pkg/api"
)

type bjsHandler struct {
	cfg  config.Config
	http *http.Client
}

func (h bjsHandler) Search(ctx context.Context, meta api.PreparedMetadata, _ string) ([]api.DupeEntry, []string, error) {
	imdb := imdbForLookup(meta)
	if imdb == "" {
		return nil, []string{noteSkip("missing IMDb ID for BJS dupe search")}, nil
	}
	baseURL := trackerBaseURL(h.cfg, "BJS", "https://bj-share.info")
	cookies, err := loadTrackerCookies(ctx, h.cfg, "BJS", trackerHost(baseURL, "bj-share.info"))
	if err != nil {
		return nil, []string{noteSkip("missing valid BJS cookies")}, nil
	}
	resp, root, err := doHTMLGet(ctx, h.http, baseURL+"/torrents.php", url.Values{"searchstr": {imdb}}, cookies)
	if err != nil || !resp.ok() {
		return nil, []string{noteSkip("BJS search failed")}, nil
	}
	mainColumn := firstNode(root, func(node *xhtml.Node) bool {
		return node.Type == xhtml.ElementNode && node.Data == "div" && hasClass(node, "main_column")
	})
	if mainColumn == nil {
		return nil, nil, nil
	}
	return extractBJSResults(baseURL, mainColumn, meta), nil, nil
}

func extractBJSResults(baseURL string, root *xhtml.Node, meta api.PreparedMetadata) []api.DupeEntry {
	rows := findNodes(root, func(node *xhtml.Node) bool {
		return node.Type == xhtml.ElementNode && node.Data == "tr"
	})
	entries := make([]api.DupeEntry, 0)
	currentSeason := ""
	currentResolution := ""
	currentEpisode := ""
	currentPack := false
	for _, row := range rows {
		if updateBJSContext(row, &currentSeason, &currentResolution, &currentEpisode, &currentPack) {
			continue
		}

		rowID := attrValueHTML(row, "id")
		if !strings.HasPrefix(rowID, "torrent") || strings.HasPrefix(rowID, "torrent_") {
			continue
		}
		if !shouldProcessBJSRow(currentSeason, currentResolution, currentEpisode, currentPack, meta) {
			continue
		}

		entry := bjsEntryFromRow(baseURL, row)
		if entry.ID != "" || entry.Name != "" {
			entries = append(entries, entry)
		}
	}
	return entries
}

func updateBJSContext(row *xhtml.Node, currentSeason *string, currentResolution *string, currentEpisode *string, currentPack *bool) bool {
	classes := strings.Fields(attrValueHTML(row, "class"))
	for _, className := range classes {
		switch className {
		case "resolution_header":
			if match := regexp.MustCompile(`(?i)(\d{3,4}p|\d{3,4}i)`).FindStringSubmatch(nodeTextHTML(row)); len(match) == 2 {
				*currentResolution = strings.ToLower(match[1])
			}
			return true
		case "season_header":
			if match := regexp.MustCompile(`(?i)temporada\s+(\d+)`).FindStringSubmatch(nodeTextHTML(row)); len(match) == 2 {
				*currentSeason = match[1]
			}
			return true
		}
	}

	rowspanCell := firstNode(row, func(node *xhtml.Node) bool {
		return node.Type == xhtml.ElementNode && node.Data == "td" && attrValueHTML(node, "rowspan") != ""
	})
	if rowspanCell == nil {
		return false
	}
	link := firstNode(rowspanCell, func(node *xhtml.Node) bool {
		return node.Type == xhtml.ElementNode && node.Data == "a" && strings.Contains(attrValueHTML(node, "href"), "torrents.php?id=")
	})
	if link == nil {
		return false
	}
	text := strings.TrimSpace(nodeTextHTML(link))
	if strings.Contains(strings.ToLower(text), "temporada") {
		*currentPack = true
		*currentEpisode = ""
		return false
	}
	if match := regexp.MustCompile(`(?i)S(\d+)E(\d+)`).FindStringSubmatch(text); len(match) == 3 {
		*currentPack = false
		*currentEpisode = match[2]
	}
	return false
}

func shouldProcessBJSRow(currentSeason string, currentResolution string, currentEpisode string, currentPack bool, meta api.PreparedMetadata) bool {
	category := strings.ToUpper(metautil.FirstNonEmptyTrimmed(meta.ExternalIDs.Category, meta.MediaInfoCategory, meta.Release.Category))
	switch category {
	case "TV":
		if meta.SeasonInt <= 0 || strings.TrimSpace(currentSeason) == "" {
			return false
		}
		season, err := strconv.Atoi(strings.TrimSpace(currentSeason))
		if err != nil || season != meta.SeasonInt {
			return false
		}
		if meta.TVPack {
			return currentPack
		}
		if currentPack {
			return true
		}
		episode, err := strconv.Atoi(strings.TrimSpace(currentEpisode))
		return err == nil && episode == meta.EpisodeInt
	case "MOVIE":
		wantResolution := strings.ToLower(strings.TrimSpace(meta.Release.Resolution))
		if wantResolution == "" || strings.TrimSpace(currentResolution) == "" {
			return true
		}
		return strings.EqualFold(currentResolution, wantResolution)
	default:
		return true
	}
}

func bjsEntryFromRow(baseURL string, row *xhtml.Node) api.DupeEntry {
	link := firstNode(row, func(node *xhtml.Node) bool {
		if node.Type != xhtml.ElementNode || node.Data != "a" {
			return false
		}
		href := attrValueHTML(node, "href")
		return strings.Contains(href, "torrentid=") || strings.Contains(attrValueHTML(node, "onclick"), "loadIfNeeded")
	})
	entry := api.DupeEntry{}
	if link != nil {
		entry.Name = strings.Join(strings.Fields(nodeTextHTML(link)), " ")
		entry.ID = bjsTorrentIDFromLink(link)
		if entry.ID != "" {
			entry.Link = strings.TrimRight(baseURL, "/") + "/torrents.php?torrentid=" + entry.ID
		}
	}
	if entry.ID == "" {
		entry.ID = strings.TrimPrefix(attrValueHTML(row, "id"), "torrent")
		if entry.ID != "" {
			entry.Link = strings.TrimRight(baseURL, "/") + "/torrents.php?torrentid=" + entry.ID
		}
	}
	sizeCell := firstNode(row, func(node *xhtml.Node) bool {
		return node.Type == xhtml.ElementNode && node.Data == "td" && hasClass(node, "number_column") && hasClass(node, "nobr")
	})
	if sizeCell != nil {
		addSize(&entry, nodeTextHTML(sizeCell))
	}
	return entry
}

func bjsTorrentIDFromLink(link *xhtml.Node) string {
	if link == nil {
		return ""
	}
	href := attrValueHTML(link, "href")
	if match := regexp.MustCompile(`(?i)torrentid=(\d+)`).FindStringSubmatch(href); len(match) == 2 {
		return match[1]
	}
	if match := regexp.MustCompile(`loadIfNeeded\('(\d+)',\s*'(\d+)'`).FindStringSubmatch(attrValueHTML(link, "onclick")); len(match) >= 2 {
		return match[1]
	}
	return ""
}
