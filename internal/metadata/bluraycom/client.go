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
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/metadata/discparse"
	"github.com/autobrr/upbrr/internal/metadata/evidence"
	"github.com/autobrr/upbrr/internal/metadata/progress"
	"github.com/autobrr/upbrr/internal/providerid"
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

// Client queries and parses Blu-ray.com search, release, and product pages.
type Client struct {
	httpClient *http.Client
	logger     api.Logger
}

// LookupInput supplies the local disc evidence used to filter and score release
// candidates. Score thresholds use the candidate's 0-100 scale; non-positive
// values default to 100.
type LookupInput struct {
	SourcePath string
	// CountryOverride is the explicit release-country correction. Nil derives
	// the folder marker; empty or unrecognized values retain unrestricted scoring.
	CountryOverride   *string
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

// NewClient uses a 30-second HTTP client when client is nil. Only the first
// optional logger is used, and a nil logger becomes [api.NopLogger].
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

// Lookup returns score-sorted release candidates for input.IMDBID and selects an
// explicit release before applying the score threshold. A non-positive IMDb ID
// returns no result. Search-page failures abort the lookup; failures for an
// individual release are skipped or retained as candidate warnings. A nil
// receiver uses default client settings. Observed cancellation aborts the lookup.
func (c *Client) Lookup(ctx context.Context, input LookupInput) (*api.BlurayMetadata, error) {
	if c == nil {
		c = NewClient(nil)
	}
	if input.IMDBID <= 0 {
		return nil, nil
	}

	searchURL := fmt.Sprintf("%s/search/?quicksearch=1&quicksearch_country=all&quicksearch_keyword=%s&section=theatrical", baseURL, formatIMDbID(input.IMDBID))
	searchProgress := progress.Begin(ctx, "Read Blu-ray.com search results")
	searchHTML, err := c.fetch(ctx, searchURL, baseURL+"/")
	if err != nil {
		searchProgress.Finish("Search unavailable.", true)
		return nil, err
	}
	searchProgress.Finish("Search results ready.", false)
	movies, err := parseMovieLinks(searchHTML)
	if err != nil {
		return nil, err
	}

	candidates := make([]api.BlurayReleaseCandidate, 0)
	processed, discovered, unavailable, failedCatalogues := 0, 0, 0, 0
	for movieIndex, movie := range movies {
		if strings.TrimSpace(movie.ProductID) == "" {
			continue
		}
		ajaxURL := fmt.Sprintf("%s/products/menu_ajax.php?p=%s&c=20&action=showreleasesall", baseURL, url.QueryEscape(movie.ProductID))
		discovery := progress.Begin(ctx, "Discover Blu-ray.com editions")
		discovery.Update(movieIndex, len(movies), fmt.Sprintf("%d editions discovered.", discovered))
		releasesHTML, fetchErr := c.fetch(ctx, ajaxURL, movie.ReleasesURL)
		if fetchErr != nil {
			failedCatalogues++
			discovery.Finish("Edition catalogue unavailable.", true)
			if ctx.Err() != nil {
				return nil, fmt.Errorf("bluray.com lookup canceled: %w", ctx.Err())
			}
			continue
		}
		releases, parseErr := parseReleaseInfo(releasesHTML, input, logging.FromContext(ctx, c.logger))
		if parseErr != nil {
			failedCatalogues++
			discovery.Finish("Edition catalogue could not be read.", true)
			continue
		}
		discovered += len(releases)
		discovery.Update(movieIndex+1, len(movies), fmt.Sprintf("%d editions discovered.", discovered))
		discovery.Finish("Catalogue discovery complete.", false)
		details := progress.Begin(ctx, "Read Blu-ray.com release details")
		total := 0
		if movieIndex == len(movies)-1 && failedCatalogues == 0 {
			total = discovered
		}
		details.Update(processed, total, fmt.Sprintf("%d editions discovered; %d unavailable.", discovered, unavailable))
		for idx := range releases {
			releases[idx].ProductID = movie.ProductID
			releases[idx].MovieTitle = movie.Title
			releases[idx].MovieYear = movie.Year
			detailsHTML, detailsErr := c.fetch(ctx, releases[idx].URL, ajaxURL)
			if ctx.Err() != nil {
				details.Finish("Canceled.", false)
				return nil, fmt.Errorf("bluray.com lookup canceled: %w", ctx.Err())
			}
			if detailsErr != nil {
				unavailable++
				releases[idx].Warnings = append(releases[idx].Warnings, "release details unavailable")
			} else if parsed, detailsParseErr := parseReleaseDetails(detailsHTML, releases[idx]); detailsParseErr == nil {
				releases[idx] = parsed
			} else {
				unavailable++
				releases[idx].Warnings = append(releases[idx].Warnings, "release details parse failed")
			}
			scoreCandidate(&releases[idx], input.BDInfo)
			candidates = append(candidates, releases[idx])
			processed++
			details.Update(processed, total, fmt.Sprintf("%d editions discovered; %d unavailable.", discovered, unavailable))
		}
		outcome := "Release details complete."
		if unavailable > 0 || failedCatalogues > 0 {
			outcome = fmt.Sprintf("Partial: %d release details and %d catalogues unavailable.", unavailable, failedCatalogues)
		}
		details.Finish(outcome, unavailable > 0 || failedCatalogues > 0)
	}

	result := &api.BlurayMetadata{
		SourcePath: strings.TrimSpace(input.SourcePath),
		IMDBID:     input.IMDBID,
		SearchURL:  searchURL,
		Candidates: candidates,
		UpdatedAt:  time.Now().UTC(),
	}
	SelectRelease(result, input)
	return result, nil
}

func (c *Client) fetch(ctx context.Context, targetURL string, referer string) (string, error) {
	result, err := evidence.Lookup(
		ctx,
		"bluray.page",
		[]string{targetURL, referer},
		func() (string, error) { return c.fetchPage(ctx, targetURL, referer) },
		func(value string) bool {
			if strings.Contains(targetURL, "/search/") {
				movies, err := parseMovieLinks(value)
				return err == nil && len(movies) == 0
			}
			if strings.Contains(targetURL, "menu_ajax.php") {
				return !releaseIDPattern.MatchString(value)
			}
			return strings.TrimSpace(value) == ""
		},
	)
	if err != nil {
		return result, fmt.Errorf("metadata evidence lookup: %w", err)
	}
	return result, nil
}

func (c *Client) fetchPage(ctx context.Context, targetURL string, referer string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return "", fmt.Errorf("bluray.com: create request: %w", err)
	}
	setPageHeaders(req.Header, referer)
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
			country := previousFlagTitle(section, n)
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
	release.ReleaseDate = releaseCalendarDate(root)
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

// SelectRelease filters, deduplicates and sorts candidates for fresh or retained
// results. A unique country match is selected regardless of score; multiple
// matches use the configured threshold. Manual choices must be in the filtered set.
func SelectRelease(metadata *api.BlurayMetadata, input LookupInput) {
	if metadata == nil {
		return
	}
	metadata.SelectedReleaseID, metadata.SelectedURL, metadata.SelectionReason = "", "", ""
	metadata.AutoSelected = false
	candidates := dedupeReleases(metadata.Candidates)
	country := input.ReleaseCountry()
	if country != "" {
		candidates = slices.DeleteFunc(candidates, func(candidate api.BlurayReleaseCandidate) bool {
			return !strings.EqualFold(strings.TrimSpace(candidate.Country), country)
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score == candidates[j].Score {
			return candidates[i].Title < candidates[j].Title
		}
		return candidates[i].Score > candidates[j].Score
	})
	for idx := range candidates {
		candidates[idx].Accepted = false
	}
	metadata.Country, metadata.Candidates = country, candidates
	metadata.Threshold = thresholdForCandidateCount(len(candidates), input)
	metadata.BestScore = bestCandidateScore(candidates)
	uniqueCountry := country != "" && len(candidates) == 1
	if uniqueCountry {
		metadata.Threshold = 0
	}
	if requested := strings.TrimSpace(input.SelectedReleaseID); requested != "" {
		if metadata.SelectCandidate(requested, false, "manual") {
			return
		}
		if country != "" {
			metadata.SelectionReason = "Requested release is not available for release country " + country + "."
			return
		}
	}
	if len(candidates) == 0 {
		metadata.SelectionReason = "No release candidates found."
		if country != "" {
			metadata.SelectionReason = "No matching release found for release country " + country + "."
		}
		return
	}
	best := candidates[0]
	if best.Score >= metadata.Threshold {
		reason := "score"
		if uniqueCountry {
			reason = "single_country"
		}
		metadata.SelectCandidate(best.ReleaseID, true, reason)
		return
	}
	metadata.SelectionReason = fmt.Sprintf("best score %.1f below threshold %.1f", best.Score, metadata.Threshold)
}

// releaseCalendarDate reads the disc calendar link independently of the film
// year and optional technical specs. Parse validates leap days without a timezone conversion.
func releaseCalendarDate(root *html.Node) string {
	for _, link := range findAll(root, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "a" }) {
		u, err := url.Parse(absolutize(attr(link, "href")))
		if err != nil || !strings.HasSuffix(u.Path, "/releasedates.php") {
			continue
		}
		date, err := time.Parse("2006January2", u.Query().Get("year")+u.Fragment)
		month, monthErr := strconv.Atoi(u.Query().Get("month"))
		if err == nil && monthErr == nil && int(date.Month()) == month && date.Year() > 0 {
			return date.Format(time.DateOnly)
		}
	}
	return ""
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
	return providerid.IMDb(id).Prefixed()
}
