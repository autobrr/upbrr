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
	// DetailsIdentityVersion identifies verified details or a failed inspection that accepted no page facts.
	DetailsIdentityVersion = "final-release-url-v1"
)

var (
	// ErrDetailReleaseNotRetained reports a request for a release absent from the retained listing.
	ErrDetailReleaseNotRetained = errors.New("bluray.com: requested detail release is not in the retained listing")

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
	// ExactRelease accepts a URL without catalogue discovery. Bare IDs use
	// DiscType to choose the provider movie or DVD namespace.
	ExactRelease string
	// Retained contains compatible listing/detail evidence; scores are recomputed locally.
	Retained        *api.BlurayMetadata
	Details         *api.BlurayDetailRequest
	FetchSelected   bool
	SkipSelection   bool
	ScoreThreshold  float64
	SingleThreshold float64
}

type movieLink struct {
	Title       string
	Year        string
	ReleasesURL string
	ProductID   string
}

// verifiedReleasePage distinguishes detail evidence collected after redirect validation.
type verifiedReleasePage struct {
	Body string
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

// Lookup discovers listings without release-page requests. Details are fetched
// only for an explicit request, an exact release absent from retained listings,
// or a selected release needed for cover handling. Successful pages are reused.
// Explicit inspections report catalogue failures; individual failed detail
// requests retain the listing and other successful candidates.
func (c *Client) Lookup(ctx context.Context, input LookupInput) (*api.BlurayMetadata, error) {
	if c == nil {
		c = NewClient(nil)
	}
	var result *api.BlurayMetadata
	legacyDetails := HasUnverifiedDetails(input.Retained)
	if input.Retained != nil && !legacyDetails {
		retained := *input.Retained
		retained.Candidates = slices.Clone(retained.Candidates)
		result = &retained
	}
	requested := strings.TrimSpace(input.SelectedReleaseID)
	legacyManualURL := ""
	if legacyDetails && requested != "" {
		if candidate := input.Retained.CandidateByID(requested); candidate != nil {
			raw := candidate.URL
			if raw == "" {
				raw = requested
			}
			id, target, err := providerid.ParseBlurayRelease(raw)
			if err == nil && id == requested {
				if raw == requested && strings.EqualFold(strings.TrimSpace(input.DiscType), "DVD") {
					target = baseURL + "/dvd/movies.php?id=" + id
				}
				legacyManualURL = target
			}
		}
	}
	if legacyDetails && input.IMDBID <= 0 && input.ExactRelease == "" {
		switch {
		case legacyManualURL != "":
			input.ExactRelease = legacyManualURL
		case input.Details != nil || requested != "":
			return nil, ErrDetailReleaseNotRetained
		default:
			return nil, nil
		}
	}
	var exact bool
	if input.ExactRelease != "" {
		raw := strings.TrimSpace(input.ExactRelease)
		id, target, err := providerid.ParseBlurayRelease(raw)
		if err != nil {
			return nil, fmt.Errorf("bluray.com: parse exact release: %w", err)
		}
		if raw == id && strings.EqualFold(strings.TrimSpace(input.DiscType), "DVD") {
			target = baseURL + "/dvd/movies.php?id=" + id
		}
		requested = id
		if old := result.CandidateByID(id); old != nil && !sameReleaseNamespace(old.URL, target) {
			result = nil
		}
		if result == nil {
			result = &api.BlurayMetadata{IMDBID: input.IMDBID, SourcePath: input.SourcePath}
		}
		if result.CandidateByID(id) == nil {
			result.Candidates = append(result.Candidates, api.BlurayReleaseCandidate{ReleaseID: id, URL: target})
			exact = true
		}
	}
	input.SelectedReleaseID = requested
	needsCatalogue := result == nil || result.SearchURL == "" && (input.ExactRelease == "" && requested == "" || input.Details != nil && input.Details.All)
	if needsCatalogue {
		catalogue, err := c.discover(ctx, input)
		if err != nil {
			return result, err
		}
		if catalogue == nil {
			if result == nil && legacyManualURL == "" {
				if input.Details != nil && !input.Details.All {
					return nil, ErrDetailReleaseNotRetained
				}
				return nil, nil
			}
		} else {
			if result != nil {
				oldSelected, listedSelected := result.CandidateByID(requested), catalogue.CandidateByID(requested)
				if oldSelected != nil && listedSelected != nil && !sameReleaseNamespace(oldSelected.URL, listedSelected.URL) {
					return result, errors.New("bluray.com: the selected release and source catalogue use different disc formats")
				}
				if old := result.CandidateByID(requested); old != nil && catalogue.CandidateByID(requested) == nil {
					catalogue.Candidates = append(catalogue.Candidates, *old)
				}
			}
			priorDetails := result
			if legacyDetails {
				priorDetails = input.Retained
			}
			for i := range catalogue.Candidates {
				listing := &catalogue.Candidates[i]
				if old := priorDetails.CandidateByID(
					listing.ReleaseID,
				); old != nil && old.DetailsIdentityVersion == DetailsIdentityVersion &&
					sameReleaseNamespace(old.URL, listing.URL) {
					listing.DetailsStatus, listing.Specs, listing.ReleaseDate = old.DetailsStatus, old.Specs, old.ReleaseDate
					listing.DetailsIdentityVersion = old.DetailsIdentityVersion
					if old.DetailsStatus == "complete" {
						if old.Country != "" && !strings.EqualFold(old.Country, "Unknown") {
							listing.Country, listing.Region = old.Country, old.Region
						}
						if old.Publisher != "" {
							listing.Publisher = old.Publisher
						}
					}
					listing.CoverImages, listing.Warnings, listing.MatchNotes = old.CoverImages, old.Warnings, old.MatchNotes
					listing.GenericDisc, listing.SpecsMissing = old.GenericDisc, old.SpecsMissing
				}
			}
			result = catalogue
		}
	}
	if legacyManualURL != "" && result.CandidateByID(requested) == nil {
		if input.Details != nil && (input.Details.All || strings.TrimSpace(input.Details.ReleaseID) != requested) {
			return result, ErrDetailReleaseNotRetained
		}
		if result == nil {
			result = &api.BlurayMetadata{IMDBID: input.IMDBID, SourcePath: input.SourcePath}
		}
		result.Candidates = append(result.Candidates, api.BlurayReleaseCandidate{ReleaseID: requested, URL: legacyManualURL})
		exact = true
	}
	if legacyManualURL != "" {
		if listed := result.CandidateByID(requested); listed != nil && !sameReleaseNamespace(legacyManualURL, listed.URL) {
			return &api.BlurayMetadata{
				SelectedReleaseID: requested,
				SelectedURL:       legacyManualURL,
				Candidates:        []api.BlurayReleaseCandidate{{ReleaseID: requested, URL: legacyManualURL}},
			}, errors.New("bluray.com: the selected release and source catalogue use different disc formats")
		}
	}
	if details := input.Details; details != nil {
		if details.All == (strings.TrimSpace(details.ReleaseID) != "") {
			return result, errors.New("bluray.com: request one retained release or all releases")
		}
		if !details.All && result.CandidateByID(details.ReleaseID) == nil {
			return result, ErrDetailReleaseNotRetained
		}
		if exact && !details.All && strings.TrimSpace(details.ReleaseID) != requested {
			return result, ErrDetailReleaseNotRetained
		}
	}
	// Filter before fetching so explicit all respects the effective release country.
	// An exact release obtains its country from its own page first.
	if !exact {
		SelectRelease(result, input)
	} else if country := input.ReleaseCountry(); country != "" {
		result.Candidates = slices.DeleteFunc(result.Candidates, func(candidate api.BlurayReleaseCandidate) bool {
			return candidate.ReleaseID != requested && !strings.EqualFold(strings.TrimSpace(candidate.Country), country)
		})
	}
	if input.Details != nil && !input.Details.All && result.CandidateByID(input.Details.ReleaseID) == nil {
		return result, ErrDetailReleaseNotRetained
	}
	selected := input.SelectedReleaseID
	if selected == "" {
		selected = result.SelectedReleaseID
	}

	wanted := make([]int, 0)
	for i := range result.Candidates {
		candidate := &result.Candidates[i]
		want := exact && candidate.ReleaseID == requested ||
			input.Details == nil && input.FetchSelected && candidate.ReleaseID == selected && candidate.DetailsStatus == ""
		if input.Details != nil {
			want = want || input.Details.All || strings.EqualFold(strings.TrimSpace(candidate.ReleaseID), strings.TrimSpace(input.Details.ReleaseID))
		}
		if want && candidate.DetailsStatus != "complete" {
			wanted = append(wanted, i)
		}
	}
	if len(wanted) > 0 {
		step := progress.Begin(ctx, "Read Blu-ray.com release details")
		failed := 0
		step.Update(0, len(wanted), "Inspecting requested editions.")
		for count, i := range wanted {
			candidate := &result.Candidates[i]
			referer := baseURL + "/"
			if candidate.ProductID != "" {
				referer = fmt.Sprintf("%s/products/menu_ajax.php?p=%s&c=20&action=showreleasesall", baseURL, url.QueryEscape(candidate.ProductID))
			}
			body, err := c.fetch(ctx, candidate.URL, referer)
			if ctx.Err() != nil {
				step.Finish("Canceled.", false)
				return result, fmt.Errorf("bluray.com detail request canceled: %w", ctx.Err())
			}
			if err == nil {
				var parsed api.BlurayReleaseCandidate
				parsed, err = parseReleaseDetails(body, *candidate)
				if err == nil && exact && candidate.ReleaseID == requested && parsed.Title == "" && parsed.SpecsMissing {
					err = errors.New("release page does not identify an available edition")
				}
				if err == nil {
					parsed.DetailsIdentityVersion = DetailsIdentityVersion
					*candidate = parsed
				}
			}
			if err != nil {
				failed++
				candidate.DetailsStatus = "failed"
				candidate.DetailsIdentityVersion = DetailsIdentityVersion
				candidate.Warnings = []string{"Release details unavailable."}
				if exact {
					step.Finish("Requested release unavailable.", true)
					return result, fmt.Errorf("bluray.com: release %s unavailable: %w", candidate.ReleaseID, err)
				}
			} else {
				candidate.DetailsStatus = "complete"
				candidate.Warnings = nil
			}
			if candidate.DetailsStatus == "complete" {
				scoreCandidate(candidate, input.BDInfo)
			}
			snapshot := *candidate
			update := api.NewPreparationProgressUpdate(
				api.PreparationPhaseMediaFacts,
				api.PreparationProgressRunning,
				"Release inspection "+candidate.DetailsStatus+".",
			)
			update.BlurayCandidate = &snapshot
			api.EmitPreparationProgress(ctx, update)
			step.Update(count+1, len(wanted), fmt.Sprintf("%d editions unavailable.", failed))
		}
		step.Finish(fmt.Sprintf("Release details complete; %d unavailable.", failed), failed > 0)
	}

	result.UpdatedAt = time.Now().UTC()
	SelectRelease(result, input)
	if input.ExactRelease != "" && result.SelectedReleaseID != requested {
		return result, fmt.Errorf("bluray.com: requested release %s is not eligible for the release country", requested)
	}
	return result, nil
}

// HasUnverifiedDetails identifies legacy parsed facts that must be rebuilt from
// catalogue evidence before they can affect selection, scoring or naming.
func HasUnverifiedDetails(metadata *api.BlurayMetadata) bool {
	if metadata == nil {
		return false
	}
	for _, candidate := range metadata.Candidates {
		if candidate.DetailsIdentityVersion == DetailsIdentityVersion {
			continue
		}
		if candidate.DetailsStatus != "" || candidate.ReleaseDate != "" || len(candidate.MatchNotes) > 0 || len(candidate.CoverImages) > 0 ||
			candidate.Specs.Video != (api.BlurayVideoSpec{}) || candidate.Specs.Discs != (api.BlurayDiscSpec{}) || candidate.Specs.Playback != (api.BlurayPlaybackSpec{}) ||
			len(candidate.Specs.Audio) > 0 || len(candidate.Specs.Subtitles) > 0 {
			return true
		}
	}
	return false
}

func (c *Client) discover(ctx context.Context, input LookupInput) (*api.BlurayMetadata, error) {
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
	discovered := 0
	for movieIndex, movie := range movies {
		if strings.TrimSpace(movie.ProductID) == "" {
			continue
		}
		ajaxURL := fmt.Sprintf("%s/products/menu_ajax.php?p=%s&c=20&action=showreleasesall", baseURL, url.QueryEscape(movie.ProductID))
		discovery := progress.Begin(ctx, "Discover Blu-ray.com editions")
		discovery.Update(movieIndex, len(movies), fmt.Sprintf("%d editions discovered.", discovered))
		releasesHTML, fetchErr := c.fetch(ctx, ajaxURL, movie.ReleasesURL)
		if fetchErr != nil {
			discovery.Finish("Edition catalogue unavailable.", true)
			if ctx.Err() != nil {
				return nil, fmt.Errorf("bluray.com lookup canceled: %w", ctx.Err())
			}
			if input.Details != nil {
				return nil, fmt.Errorf("bluray.com edition catalogue: %w", fetchErr)
			}
			continue
		}
		releases, parseErr := parseReleaseInfo(releasesHTML, input, logging.FromContext(ctx, c.logger))
		if parseErr != nil {
			discovery.Finish("Edition catalogue could not be read.", true)
			if input.Details != nil {
				return nil, fmt.Errorf("bluray.com edition catalogue: %w", parseErr)
			}
			continue
		}
		discovered += len(releases)
		discovery.Update(movieIndex+1, len(movies), fmt.Sprintf("%d editions discovered.", discovered))
		discovery.Finish("Catalogue discovery complete.", false)
		for idx := range releases {
			releases[idx].ProductID = movie.ProductID
			releases[idx].MovieTitle = movie.Title
			releases[idx].MovieYear = movie.Year
			candidates = append(candidates, releases[idx])
		}
	}

	result := &api.BlurayMetadata{
		SourcePath: strings.TrimSpace(input.SourcePath),
		IMDBID:     input.IMDBID,
		SearchURL:  searchURL,
		Candidates: candidates,
		UpdatedAt:  time.Now().UTC(),
	}
	return result, nil
}

// Numeric IDs are independent in the provider's movie and DVD namespaces.
func sameReleaseNamespace(first, second string) bool {
	_, a, err := providerid.ParseBlurayRelease(first)
	if err != nil {
		return false
	}
	_, b, err := providerid.ParseBlurayRelease(second)
	return err == nil && strings.HasPrefix(a, baseURL+"/dvd/") == strings.HasPrefix(b, baseURL+"/dvd/")
}

func (c *Client) fetch(ctx context.Context, targetURL string, referer string) (string, error) {
	if _, _, err := providerid.ParseBlurayRelease(targetURL); err == nil {
		page, fetchErr := evidence.Lookup(ctx, "bluray.page", []string{targetURL, referer}, func() (verifiedReleasePage, error) {
			body, pageErr := c.fetchPage(ctx, targetURL, referer)
			return verifiedReleasePage{Body: body}, pageErr
		}, func(value verifiedReleasePage) bool { return strings.TrimSpace(value.Body) == "" })
		if fetchErr != nil {
			return page.Body, fmt.Errorf("metadata evidence lookup: %w", fetchErr)
		}
		return page.Body, nil
	}
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
	if requestedID, _, parseErr := providerid.ParseBlurayRelease(targetURL); parseErr == nil {
		if resp.Request == nil || resp.Request.URL == nil {
			return "", errors.New("bluray.com: release response has no final URL")
		}
		finalURL := resp.Request.URL.String()
		finalID, _, finalErr := providerid.ParseBlurayRelease(finalURL)
		if finalErr != nil || finalID != requestedID || !sameReleaseNamespace(targetURL, finalURL) {
			return "", errors.New("bluray.com: release response redirected to a different edition")
		}
	}
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
			id, target, parseErr := providerid.ParseBlurayRelease(href)
			if parseErr != nil {
				continue
			}
			href = target
			country := previousFlagTitle(section, n)
			publisher := cleanText(textContent(nextSmallWithStyle(n, "color: #999999")))
			price := cleanText(textContent(nextSmallWithStyle(n, "color: green")))
			title := cleanText(attr(n, "title"))
			if title == "" {
				title = cleanText(textContent(n))
			}
			results = append(results, api.BlurayReleaseCandidate{
				ReleaseID: id,
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
	release.CoverImages = extractCoverImages(htmlText, root)
	canonical := findFirst(root, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "link" && attr(n, "rel") == "canonical"
	})
	if canonical != nil {
		id, target, parseErr := providerid.ParseBlurayRelease(attr(canonical, "href"))
		if parseErr != nil || id != release.ReleaseID || !sameReleaseNamespace(release.URL, target) {
			return release, errors.New("release page does not identify the requested edition")
		}
		release.URL = target
	}
	if release.Title == "" {
		if canonical == nil {
			return release, errors.New("release page does not identify the requested edition")
		}
		title := findFirst(root, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "h1" })
		release.Title = cleanText(textContent(title))
	}
	if release.Country == "" {
		heading := findFirst(root, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "h1" })
		if heading != nil && heading.Parent != nil {
			for flag := heading.Parent.NextSibling; flag != nil; flag = flag.NextSibling {
				if flag.Type == html.ElementNode && flag.Data == "br" {
					break
				}
				if flag.Type == html.ElementNode && flag.Data == "img" && strings.Contains(attr(flag, "src"), "/flags/") {
					release.Country = cleanText(attr(flag, "title"))
					break
				}
			}
		}
		release.Region = countryToRegion(release.Country)
	}
	if release.Publisher == "" {
		publisher := findFirst(root, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "a" && strings.Contains(attr(n, "href"), "studioid=")
		})
		release.Publisher = cleanText(textContent(publisher))
	}
	specsNode := findFirst(root, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "td" && attr(n, "width") == "228px" &&
			strings.Contains(strings.ToLower(attr(n, "style")), "font-size: 12px")
	})
	if specsNode == nil {
		if release.ReleaseDate == "" && len(release.CoverImages) == 0 && canonical == nil {
			return release, errors.New("release detail page unavailable")
		}
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
	release.SpecsMissing = specs.Video.Codec == "" && len(specs.Audio) == 0 && len(specs.Subtitles) == 0 && specs.Discs.Format == ""
	return release, nil
}

// SelectRelease filters, deduplicates and sorts candidates for fresh or retained
// results, restoring legacy inspection state and rescoring completed details.
// Catalogue-wide unique-country selection ignores scores; other automatic matches
// require complete comparisons. Manual choices must be in the filtered set.
func SelectRelease(metadata *api.BlurayMetadata, input LookupInput) {
	if metadata == nil {
		return
	}
	metadata.SelectedReleaseID, metadata.SelectedURL, metadata.SelectionReason = "", "", ""
	metadata.AutoSelected = false
	candidates := dedupeReleases(metadata.Candidates)
	for i := range candidates {
		candidate := &candidates[i]
		if candidate.DetailsStatus == "" && len(candidate.MatchNotes) > 0 {
			// Older persisted results predate explicit inspection state. Only
			// actual detail fields establish a completed inspection.
			if len(candidate.Warnings) > 0 {
				candidate.DetailsStatus = "failed"
			} else if candidate.Specs.Video.Codec != "" || len(candidate.Specs.Audio) > 0 || len(candidate.Specs.Subtitles) > 0 || candidate.Specs.Discs.Format != "" || candidate.ReleaseDate != "" || len(candidate.CoverImages) > 0 {
				candidate.DetailsStatus = "complete"
			}
			if candidate.DetailsStatus != "complete" {
				candidate.Score, candidate.MatchNotes = 0, nil
			}
		}
		if candidates[i].DetailsStatus == "complete" {
			scoreCandidate(&candidates[i], input.BDInfo)
		}
	}
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
	uniqueCountry := metadata.SearchURL != "" && country != "" && len(candidates) == 1
	if uniqueCountry {
		metadata.Threshold = 0
	}
	if input.SkipSelection {
		metadata.SelectionReason = "Blu-ray.com selection skipped."
		return
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
	if metadata.SearchURL == "" {
		metadata.SelectionReason = "Catalogue discovery is needed for automatic selection."
		return
	}
	best := candidates[0]
	complete := !slices.ContainsFunc(candidates, func(c api.BlurayReleaseCandidate) bool { return c.DetailsStatus != "complete" })
	if uniqueCountry || complete && best.Score >= metadata.Threshold {
		reason := "score"
		if uniqueCountry {
			reason = "single_country"
		}
		metadata.SelectCandidate(best.ReleaseID, true, reason)
		return
	}
	if !complete {
		metadata.SelectionReason = "Select a release or fetch details to compare editions."
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
