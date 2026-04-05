// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/config"
	ascimpl "github.com/autobrr/upbrr/internal/trackers/impl/asc"
	"github.com/autobrr/upbrr/pkg/api"
)

type ascHandler struct {
	cfg    config.Config
	http   *http.Client
	logger api.Logger
}

func (h ascHandler) Search(ctx context.Context, meta api.PreparedMetadata, _ string) ([]api.DupeEntry, []string, error) {
	if h.http == nil {
		return nil, []string{noteSkip("ASC handler misconfigured: no HTTP client")}, nil
	}
	if !meta.Anime && resolveASCIMDb(meta) == "" {
		return nil, []string{noteSkip("missing IMDb ID for ASC dupe search")}, nil
	}

	cookies, _, err := ascimpl.LoadCookies(h.cfg.MainSettings.DBPath)
	if err != nil || len(cookies) == 0 {
		return nil, []string{noteSkip("missing valid ASC cookies (expected Netscape cookies at cookies/ASC.txt)")}, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, buildASCSearchURL(meta), nil)
	if err != nil {
		return nil, nil, fmt.Errorf("build ASC request: %w", err)
	}
	req.Header.Set("User-Agent", "upbrr")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}

	resp, err := h.http.Do(req)
	if err != nil {
		return nil, []string{noteSkip("ASC request failed")}, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, []string{noteSkip("ASC search returned non-success status")}, nil
	}

	doc, err := html.Parse(resp.Body)
	if err != nil {
		return nil, []string{noteSkip("ASC response parse failed")}, nil
	}

	var entries []api.DupeEntry
	var tasks []ascDetailTask
	walkASCNodes(doc, &entries, &tasks, resolveASCTitle(meta))

	if len(tasks) > 0 {
		var mu sync.Mutex
		var wg sync.WaitGroup
		for _, t := range tasks {
			t := t
			wg.Add(1)
			go func() {
				defer wg.Done()
				name := "N/A"
				reqFile, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://cliente.amigos-share.club/torrents-arquivos.php?id="+t.ID, nil)
				if err == nil {
					reqFile.Header.Set("User-Agent", "upbrr")
					for _, cookie := range cookies {
						reqFile.AddCookie(cookie)
					}
					if fileResp, err := h.http.Do(reqFile); err == nil {
						defer fileResp.Body.Close()
						if fileResp.StatusCode >= 200 && fileResp.StatusCode < 300 {
							if fileDoc, err := html.Parse(fileResp.Body); err == nil {
								if parsedName := getASCFilenameFromFiles(fileDoc); parsedName != "" {
									name = parsedName
								}
							}
						}
					}
				}

				mu.Lock()
				entries = append(entries, api.DupeEntry{
					Name:     name,
					ID:       t.ID,
					Link:     t.Link,
					SizeText: t.Size,
				})
				mu.Unlock()
			}()
		}
		wg.Wait()
	}

	return entries, nil, nil
}

func buildASCSearchURL(meta api.PreparedMetadata) string {
	base := "https://cliente.amigos-share.club"
	if meta.Anime {
		return base + "/torrents-search.php?search=" + url.QueryEscape(resolveASCTitle(meta))
	}
	if strings.EqualFold(resolveASCCategory(meta), "TV") {
		return base + "/busca-series.php?search=" + url.QueryEscape(resolveASCSeasonEpisode(meta)) + "&imdb=" + url.QueryEscape(resolveASCIMDb(meta))
	}
	return base + "/busca-filmes.php?search=&imdb=" + url.QueryEscape(resolveASCIMDb(meta))
}

type ascDetailTask struct {
	ID   string
	Link string
	Size string
}

func walkASCNodes(n *html.Node, entries *[]api.DupeEntry, tasks *[]ascDetailTask, baseTitle string) {
	if n.Type == html.ElementNode && strings.EqualFold(n.Data, "li") {
		if ascHasClass(n, "list-group-item") && ascHasClass(n, "dark-gray") {
			processASCListItem(n, entries, tasks, baseTitle)
			return
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkASCNodes(c, entries, tasks, baseTitle)
	}
}

func processASCListItem(n *html.Node, entries *[]api.DupeEntry, tasks *[]ascDetailTask, baseTitle string) {
	var detailsHref string
	var sizeText string
	var badges []string

	var walk func(*html.Node)
	walk = func(child *html.Node) {
		if child.Type == html.ElementNode && strings.EqualFold(child.Data, "a") {
			href := ascGetAttr(child, "href")
			if strings.Contains(href, "torrents-details.php?id=") {
				detailsHref = href
			}
		}
		if child.Type == html.ElementNode && strings.EqualFold(child.Data, "span") {
			if ascHasClass(child, "badge-info") {
				txt := strings.ToUpper(ascGetText(child))
				if strings.Contains(txt, "GB") || strings.Contains(txt, "MB") {
					sizeText = strings.TrimSpace(ascGetText(child))
				}
			} else if ascHasClass(child, "badge") {
				badges = append(badges, strings.TrimSpace(ascGetText(child)))
			}
		}
		for c := child.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)

	if detailsHref == "" {
		return
	}

	id := parseASCTorrentID(detailsHref)
	link := absolutizeASCLink(detailsHref)

	discTypes := []string{"BD25", "BD50", "BD66", "BD100", "DVD5", "DVD9"}
	isDisc := false
	for _, b := range badges {
		bUp := strings.ToUpper(b)
		if ascContainsAny(bUp, discTypes) {
			isDisc = true
			break
		}
	}

	if isDisc {
		year := "N/A"
		resolution := "N/A"
		diskType := "N/A"
		videoCodec := "N/A"
		audioCodec := "N/A"

		codecTerms := []string{"MPEG-4", "AV1", "AVC", "H264", "H265", "HEVC", "MPEG-1", "MPEG-2", "VC-1", "VP6", "VP9"}
		audioTerms := []string{"DTS", "AC3", "DDP", "E-AC-3", "TRUEHD", "ATMOS", "LPCM", "AAC", "FLAC"}
		resTypes := []string{"4K", "2160P", "1080P", "720P", "480P"}

		for _, b := range badges {
			bUp := strings.ToUpper(b)
			if len(bUp) == 4 && ascIsDigit(bUp) {
				year = bUp
			} else if ascContainsAnyStrict(bUp, resTypes) {
				if bUp == "4K" {
					resolution = "2160p"
				} else {
					resolution = bUp
				}
			} else if ascContainsAny(bUp, codecTerms) {
				videoCodec = strings.TrimSpace(b)
			} else if ascContainsAny(bUp, audioTerms) {
				audioCodec = strings.TrimSpace(b)
			} else if ascContainsAny(bUp, discTypes) {
				diskType = strings.TrimSpace(b)
			}
		}

		name := fmt.Sprintf("%s %s %s %s %s %s", baseTitle, year, resolution, diskType, strings.ToUpper(videoCodec), strings.ToUpper(audioCodec))
		*entries = append(*entries, api.DupeEntry{
			Name:     strings.TrimSpace(name),
			ID:       id,
			Link:     link,
			SizeText: sizeText,
		})
	} else {
		*tasks = append(*tasks, ascDetailTask{
			ID:   id,
			Link: link,
			Size: sizeText,
		})
	}
}

func ascContainsAnyStrict(s string, terms []string) bool {
	for _, t := range terms {
		if s == t {
			return true
		}
	}
	return false
}

func ascContainsAny(s string, terms []string) bool {
	for _, t := range terms {
		if strings.Contains(s, t) {
			return true
		}
	}
	return false
}

func ascIsDigit(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func ascHasClass(n *html.Node, class string) bool {
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, "class") {
			classes := strings.Fields(attr.Val)
			for _, c := range classes {
				if strings.EqualFold(c, class) {
					return true
				}
			}
			return false
		}
	}
	return false
}

func ascGetAttr(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, key) {
			return strings.TrimSpace(attr.Val)
		}
	}
	return ""
}

func ascGetText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(child *html.Node) {
		if child.Type == html.TextNode {
			b.WriteString(child.Data)
		}
		for c := child.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func getASCFilenameFromFiles(n *html.Node) string {
	var walk func(*html.Node) string
	walk = func(child *html.Node) string {
		if child.Type == html.ElementNode && strings.EqualFold(child.Data, "li") && ascHasClass(child, "list-group-item") {
			for c := child.FirstChild; c != nil; c = c.NextSibling {
				switch c.Type {
				case html.TextNode:
					txt := strings.TrimSpace(c.Data)
					if txt != "" {
						return txt
					}
				case html.ElementNode:
					txt := strings.TrimSpace(ascGetText(c))
					if txt != "" {
						return txt
					}
				}
			}
		}
		for c := child.FirstChild; c != nil; c = c.NextSibling {
			if txt := walk(c); txt != "" {
				return txt
			}
		}
		return ""
	}
	return walk(n)
}

func parseASCTorrentID(href string) string {
	parsed, err := url.Parse(href)
	if err == nil {
		if id := strings.TrimSpace(parsed.Query().Get("id")); id != "" {
			return id
		}
	}
	parts := strings.Split(href, "id=")
	if len(parts) < 2 {
		return ""
	}
	return strings.TrimSpace(strings.Split(parts[len(parts)-1], "&")[0])
}

func absolutizeASCLink(href string) string {
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}
	return "https://cliente.amigos-share.club/" + strings.TrimPrefix(href, "/")
}

func resolveASCIMDb(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.IMDB != nil && strings.TrimSpace(meta.ExternalMetadata.IMDB.IMDbIDText) != "" {
		return strings.TrimSpace(meta.ExternalMetadata.IMDB.IMDbIDText)
	}
	if meta.ExternalIDs.IMDBID > 0 {
		return fmt.Sprintf("tt%07d", meta.ExternalIDs.IMDBID)
	}
	return ""
}

func resolveASCCategory(meta api.PreparedMetadata) string {
	if strings.EqualFold(meta.ExternalIDs.Category, "TV") || meta.TVPack || meta.SeasonInt > 0 || meta.EpisodeInt > 0 {
		return "TV"
	}
	return "MOVIE"
}

func resolveASCTitle(meta api.PreparedMetadata) string {
	if strings.TrimSpace(meta.Release.Title) != "" {
		return strings.TrimSpace(meta.Release.Title)
	}
	if strings.TrimSpace(meta.ReleaseName) != "" {
		return strings.TrimSpace(meta.ReleaseName)
	}
	return strings.TrimSpace(meta.SourcePath)
}

func resolveASCSeasonEpisode(meta api.PreparedMetadata) string {
	if meta.EpisodeInt > 0 {
		return "S" + padASC2(meta.SeasonInt) + "E" + padASC2(meta.EpisodeInt)
	}
	if meta.SeasonInt > 0 {
		return "S" + padASC2(meta.SeasonInt)
	}
	return ""
}

func padASC2(value int) string {
	if value < 10 {
		return "0" + strconv.Itoa(value)
	}
	return strconv.Itoa(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
