// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bluraycom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/metadata/discparse"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	baseURL                    = "https://www.blu-ray.com"
	maxResponseBytes           = 16 << 20
	maxFallbackReleaseSections = 3
)

var (
	releaseIDPattern       = regexp.MustCompile(`(?i)blu-ray\.com/(?:movies|dvd)/.*?/(\d+)/`)
	productIDPattern       = regexp.MustCompile(`(?i)blu-ray\.com/.+?/(\d+)/?`)
	videoCodecPattern      = regexp.MustCompile(`(?i)Codec:\s*(.+?)(?:\s+Resolution:|$)`)
	videoResolutionPattern = regexp.MustCompile(`(?i)Resolution:\s*(.+)$`)
	discTypePattern        = regexp.MustCompile(`(?i)(Blu-ray Disc|DVD|Ultra HD Blu-ray|4K Ultra HD)`)
	singleDiscPattern      = regexp.MustCompile(`(?i)Single disc\s*\(1\s+([^)]+)\)`)
	multiDiscPattern       = regexp.MustCompile(`(?i)(One|Two|Three|Four|Five|\d+)[ -]disc set(?:\s*\(([^)]+)\))?`)
	bdFormatPattern        = regexp.MustCompile(`(?i)(\d+\s*BD-\d+|\d+\s*BD|BD-\d+)`)
	playbackRegionPattern  = regexp.MustCompile(`(?i)(?:2K Blu-ray|4K Blu-ray|DVD):\s*Region\s*([A-C])(?:\s*\(([^)]+)\))?`)
	appendImagePattern     = regexp.MustCompile(`(?is)append\(\s*['"](<img\b.*?>)['"]\s*\)`)
)

type Client struct {
	httpClient *http.Client
	logger     api.Logger
}

type LookupInput struct {
	SourcePath        string
	IMDBID            int
	DiscType          string
	Resolution        string
	Is3D              string
	BDInfo            *discparse.BDInfo
	SelectedReleaseID string
	ScoreThreshold    float64
	SingleThreshold   float64
}

type movieLink struct {
	Title       string
	Year        string
	ReleasesURL string
	ProductID   string
}

func NewClient(client *http.Client, loggers ...api.Logger) *Client {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	var logger api.Logger = api.NopLogger{}
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	return &Client{httpClient: client, logger: logger}
}

func (c *Client) Lookup(ctx context.Context, input LookupInput) (*api.BlurayMetadata, error) {
	if c == nil {
		c = NewClient(nil)
	}
	if input.IMDBID <= 0 {
		return nil, nil
	}

	searchURL := fmt.Sprintf("%s/search/?quicksearch=1&quicksearch_country=all&quicksearch_keyword=%s&section=theatrical", baseURL, formatIMDbID(input.IMDBID))
	searchHTML, err := c.fetch(ctx, searchURL, baseURL+"/")
	if err != nil {
		return nil, err
	}
	movies, err := parseMovieLinks(searchHTML)
	if err != nil {
		return nil, err
	}

	candidates := make([]api.BlurayReleaseCandidate, 0)
	for _, movie := range movies {
		if strings.TrimSpace(movie.ProductID) == "" {
			continue
		}
		ajaxURL := fmt.Sprintf("%s/products/menu_ajax.php?p=%s&c=20&action=showreleasesall", baseURL, url.QueryEscape(movie.ProductID))
		releasesHTML, fetchErr := c.fetch(ctx, ajaxURL, movie.ReleasesURL)
		if fetchErr != nil {
			continue
		}
		releases, parseErr := parseReleaseInfo(releasesHTML, input, c.logger)
		if parseErr != nil {
			continue
		}
		for idx := range releases {
			releases[idx].ProductID = movie.ProductID
			releases[idx].MovieTitle = movie.Title
			releases[idx].MovieYear = movie.Year
			detailsHTML, detailsErr := c.fetch(ctx, releases[idx].URL, ajaxURL)
			if detailsErr != nil {
				releases[idx].Warnings = append(releases[idx].Warnings, "release details unavailable")
			} else if parsed, detailsParseErr := parseReleaseDetails(detailsHTML, releases[idx]); detailsParseErr == nil {
				releases[idx] = parsed
			} else {
				releases[idx].Warnings = append(releases[idx].Warnings, "release details parse failed")
			}
			scoreCandidate(&releases[idx], input.BDInfo)
			candidates = append(candidates, releases[idx])
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score == candidates[j].Score {
			return candidates[i].Title < candidates[j].Title
		}
		return candidates[i].Score > candidates[j].Score
	})

	result := &api.BlurayMetadata{
		SourcePath:   strings.TrimSpace(input.SourcePath),
		IMDBID:       input.IMDBID,
		SearchURL:    searchURL,
		Candidates:   candidates,
		UpdatedAt:    time.Now().UTC(),
		Threshold:    thresholdForCandidateCount(len(candidates), input),
		BestScore:    bestCandidateScore(candidates),
		AutoSelected: false,
	}
	selectBestCandidate(result, strings.TrimSpace(input.SelectedReleaseID))
	return result, nil
}

func (c *Client) fetch(ctx context.Context, targetURL string, referer string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return "", fmt.Errorf("bluray.com: create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	if strings.Contains(targetURL, "menu_ajax.php") {
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("bluray.com: fetch %s: %w", targetURL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", fmt.Errorf("bluray.com: read %s: %w", targetURL, err)
	}
	var extra [1]byte
	n, err := resp.Body.Read(extra[:])
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("bluray.com: read truncation check %s: %w", targetURL, err)
	}
	if n > 0 {
		return "", fmt.Errorf("bluray.com: response for %s exceeds %d bytes", targetURL, maxResponseBytes)
	}
	text := string(body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("bluray.com: fetch %s status %d", targetURL, resp.StatusCode)
	}
	if strings.Contains(text, "No index") {
		return "", fmt.Errorf("bluray.com: anti-scraping response for %s", targetURL)
	}
	return text, nil
}

func parseMovieLinks(htmlText string) ([]movieLink, error) {
	root, err := html.Parse(strings.NewReader(htmlText))
	if err != nil {
		return nil, fmt.Errorf("bluray.com: parse search html: %w", err)
	}
	figures := findAll(root, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "div" && hasClass(n, "figure")
	})
	results := make([]movieLink, 0, len(figures))
	for _, figure := range figures {
		link := findFirst(figure, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "a" && hasClass(n, "alphaborder") && attr(n, "href") != ""
		})
		if link == nil {
			continue
		}
		movieURL := absolutize(attr(link, "href"))
		titleNode := findFirst(figure, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "div" && strings.Contains(strings.ToLower(attr(n, "style")), "font-weight: bold")
		})
		yearNode := findFirst(figure, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "div" && strings.Contains(strings.ToLower(attr(n, "style")), "margin-top")
		})
		productID := ""
		if match := productIDPattern.FindStringSubmatch(movieURL); len(match) == 2 {
			productID = match[1]
		}
		results = append(results, movieLink{
			Title:       cleanText(textContent(titleNode)),
			Year:        cleanText(textContent(yearNode)),
			ReleasesURL: movieURL + "#Releases",
			ProductID:   productID,
		})
	}
	return results, nil
}

func parseReleaseInfo(htmlText string, input LookupInput, loggers ...api.Logger) ([]api.BlurayReleaseCandidate, error) {
	root, err := html.Parse(strings.NewReader(htmlText))
	if err != nil {
		return nil, fmt.Errorf("bluray.com: parse releases html: %w", err)
	}
	allH3 := findAll(root, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "h3"
	})
	sections := make([]*html.Node, 0, len(allH3))
	for _, h3 := range allH3 {
		if releaseSectionMatches(textContent(h3), input) {
			sections = append(sections, h3)
		}
	}
	if len(sections) == 0 {
		var logger api.Logger
		if len(loggers) > 0 {
			logger = loggers[0]
		}
		matchedHeaders := 0
		for _, h3 := range allH3 {
			title := textContent(h3)
			if strings.Contains(title, "Blu-ray Editions") || strings.Contains(title, "DVD Editions") {
				matchedHeaders++
				if len(sections) < maxFallbackReleaseSections {
					sections = append(sections, h3)
				}
			}
		}
		if matchedHeaders > 0 && logger != nil {
			logger.Warnf("bluray.com: release section fallback used max_sections=%d matched_headers=%d", maxFallbackReleaseSections, matchedHeaders)
		}
	}

	results := make([]api.BlurayReleaseCandidate, 0)
	for _, section := range sections {
		for n := nextNode(section); n != nil; n = nextNode(n) {
			if n.Type == html.ElementNode && n.Data == "h3" {
				break
			}
			if n.Type != html.ElementNode || n.Data != "a" {
				continue
			}
			href := absolutize(attr(n, "href"))
			if !strings.Contains(href, "blu-ray.com/movies/") && !strings.Contains(href, "blu-ray.com/dvd/") {
				continue
			}
			match := releaseIDPattern.FindStringSubmatch(href)
			if len(match) != 2 {
				continue
			}
			country := previousFlagTitle(root, n)
			publisher := cleanText(textContent(nextSmallWithStyle(n, "color: #999999")))
			price := cleanText(textContent(nextSmallWithStyle(n, "color: green")))
			title := cleanText(attr(n, "title"))
			if title == "" {
				title = cleanText(textContent(n))
			}
			results = append(results, api.BlurayReleaseCandidate{
				ReleaseID: match[1],
				Title:     title,
				URL:       href,
				Price:     price,
				Publisher: publisher,
				Country:   country,
				Region:    countryToRegion(country),
				Score:     0,
			})
		}
	}
	return dedupeReleases(results), nil
}

func parseReleaseDetails(htmlText string, release api.BlurayReleaseCandidate) (api.BlurayReleaseCandidate, error) {
	root, err := html.Parse(strings.NewReader(htmlText))
	if err != nil {
		return release, fmt.Errorf("bluray.com: parse release details html: %w", err)
	}
	specsNode := findFirst(root, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "td" && attr(n, "width") == "228px" &&
			strings.Contains(strings.ToLower(attr(n, "style")), "font-size: 12px")
	})
	if specsNode == nil {
		release.SpecsMissing = true
		return release, nil
	}

	specs := api.BluraySpecs{}
	videoSection := extractSection(specsNode, "Video")
	if match := videoCodecPattern.FindStringSubmatch(videoSection); len(match) == 2 {
		specs.Video.Codec = cleanText(match[1])
	}
	if match := videoResolutionPattern.FindStringSubmatch(videoSection); len(match) == 2 {
		specs.Video.Resolution = cleanText(match[1])
	}

	audioNode := findFirst(specsNode, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "div" && (attr(n, "id") == "longaudio" || attr(n, "id") == "shortaudio")
	})
	specs.Audio = parseAudioLines(nodeTextWithBreaks(audioNode))

	subsNode := findFirst(specsNode, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "div" && (attr(n, "id") == "longsubs" || attr(n, "id") == "shortsubs")
	})
	specs.Subtitles = parseSubtitles(nodeTextWithBreaks(subsNode))

	discSection := extractSection(specsNode, "Discs")
	if match := discTypePattern.FindStringSubmatch(discSection); len(match) == 2 {
		specs.Discs.Type = cleanText(match[1])
	}
	if match := singleDiscPattern.FindStringSubmatch(discSection); len(match) == 2 {
		specs.Discs.Count = 1
		specs.Discs.Format = cleanText(match[1])
	} else if match := multiDiscPattern.FindStringSubmatch(discSection); len(match) >= 2 {
		specs.Discs.Count = wordNumber(match[1])
		if len(match) > 2 {
			specs.Discs.Format = extractBDFormat(match[2])
		}
		if specs.Discs.Format == "" {
			specs.Discs.Format = "multiple discs"
		}
	}

	playbackSection := extractSection(specsNode, "Playback")
	if match := playbackRegionPattern.FindStringSubmatch(playbackSection); len(match) >= 2 {
		specs.Playback.Region = cleanText(match[1])
		if len(match) > 2 {
			specs.Playback.RegionNotes = cleanText(match[2])
		}
	}

	release.Specs = specs
	release.CoverImages = extractCoverImages(htmlText, root)
	release.SpecsMissing = specs.Video.Codec == "" && len(specs.Audio) == 0 && len(specs.Subtitles) == 0 && specs.Discs.Format == ""
	return release, nil
}

func selectBestCandidate(metadata *api.BlurayMetadata, requestedReleaseID string) {
	if metadata == nil || len(metadata.Candidates) == 0 {
		return
	}
	if requestedReleaseID != "" && metadata.SelectCandidate(requestedReleaseID, false, "manual") {
		return
	}
	best := metadata.Candidates[0]
	if best.Score >= metadata.Threshold {
		metadata.SelectCandidate(best.ReleaseID, true, "score")
		return
	}
	metadata.SelectionReason = fmt.Sprintf("best score %.1f below threshold %.1f", best.Score, metadata.Threshold)
}

func thresholdForCandidateCount(count int, input LookupInput) float64 {
	if count <= 1 {
		if input.SingleThreshold > 0 {
			return input.SingleThreshold
		}
		return 100
	}
	if input.ScoreThreshold > 0 {
		return input.ScoreThreshold
	}
	return 100
}

func bestCandidateScore(candidates []api.BlurayReleaseCandidate) float64 {
	if len(candidates) == 0 {
		return 0
	}
	return candidates[0].Score
}

func formatIMDbID(id int) string {
	return fmt.Sprintf("tt%07d", id)
}
