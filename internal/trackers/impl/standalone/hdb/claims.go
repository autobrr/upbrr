// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	htmlnode "golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	hdbClaimsPath         = "/forums/viewtopic?topicid=76939"
	hdbClaimsCacheVersion = 2
	hdbClaimsCacheTTL     = 48 * time.Hour
	hdbClaimsMaxBytes     = 4 << 20
	hdbClaimWindowHours   = 48
	hdbClaimDefaultGrace  = 24
)

var (
	hdbAKAExtractPattern = regexp.MustCompile(`(?i)\(\s*aka\s*:\s*((?:[^()]|\([^()]*\))*)\)`)
	hdbNonAlnumPattern   = regexp.MustCompile(`[^a-z0-9]+`)
	hdbSpacePattern      = regexp.MustCompile(`\s+`)
	hdbTime24Pattern     = regexp.MustCompile(`^(\d{1,2}):(\d{2})(?::(\d{2}))?$`)
	hdbTime12Pattern     = regexp.MustCompile(`^(\d{1,2}):(\d{2})\s*([AaPp][Mm])$`)
)

type hdbClaimedShowsCache struct {
	Version   int              `json:"version"`
	FetchedAt int64            `json:"fetched_at"`
	SourceURL string           `json:"source_url"`
	Complete  bool             `json:"complete"`
	Claims    []hdbClaimRecord `json:"claims"`
}

type hdbClaimRecord struct {
	Title          string   `json:"title"`
	Aliases        []string `json:"aliases,omitempty"`
	Sites          []string `json:"sites"`
	Group          string   `json:"group"`
	RelayedFromBTN bool     `json:"relayed_from_btn,omitempty"`
}

type hdbClaimData struct {
	Records         []hdbClaimRecord
	FetchedAt       int64
	Complete        bool
	FreshStructured bool
}

type claimChecker struct {
	cfg           config.Config
	endpoint      string
	httpClient    *http.Client
	logger        api.Logger
	fetchOverride func(context.Context) (hdbClaimData, error)
}

// NewClaimChecker returns a checker using HDB's stored session and separate
// claim cache. It never logs in or uses BTN credentials, cookies, or cached data.
// A nil logger is replaced with a no-op logger.
func (d *Definition) NewClaimChecker(cfg config.Config, logger api.Logger) trackers.ClaimChecker {
	if logger == nil {
		logger = api.NopLogger{}
	}
	client := d.httpClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &claimChecker{
		cfg:        cfg,
		endpoint:   d.baseURL + hdbClaimsPath,
		httpClient: client,
		logger:     logger,
	}
}

// HasClaim reports an active HDB TV WEB claim. HDB claim-list access failures
// are warnings and fail open, while cancellation remains observable by callers.
// Only fresh, unambiguous direct ownership permits a configured internal group
// to bypass its own claim; stale, partial, or relayed claims may still block uploads.
func (s *claimChecker) HasClaim(ctx context.Context, meta api.UploadSubject) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("metadata: HDB claim check canceled: %w", err)
	}
	if isHDBSceneRelease(meta) {
		s.logger.Debugf("metadata: HDB claims skipped origin=scene decision=allowed")
		return false, nil
	}
	applies, known := hdbClaimPolicyApplies(meta)
	if !known {
		s.logger.Warnf("metadata: HDB claim check unavailable: WEB TV applicability is unknown")
		return false, nil
	}
	if !applies {
		return false, nil
	}

	cachePath, err := hdbClaimsCachePath(s.cfg.MainSettings.DBPath)
	if err != nil {
		s.logger.Warnf("metadata: HDB claim list unavailable: %v", err)
		return false, nil
	}
	claims, err := s.loadHDBClaims(ctx, cachePath, hdbClaimsCacheTTL)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false, err
		}
		s.logger.Warnf("metadata: HDB claim list unavailable: %v", err)
		return false, nil
	}
	matchedClaims, matchedTitle := matchHDBClaimRecords(meta, claims)
	if len(matchedClaims) == 0 {
		s.logger.Debugf("metadata: HDB claims found no canonical title match")
		return false, nil
	}

	graceHours := s.claimWindowGraceHours()
	expired, thresholdHours, hoursSinceAir := hdbClaimWindowExpired(meta, graceHours)
	if expired {
		s.logger.Debugf("metadata: HDB claim window expired for %q hours_since_air=%.2f threshold=%d", matchedTitle, hoursSinceAir, thresholdHours)
		return false, nil
	}
	trackerCfg, _ := hdbTrackerConfig(s.cfg)
	groupPolicy := trackers.ResolveGroupPolicy(trackerCfg, meta)
	ownedByGroup := hdbClaimsOwnedByGroup(matchedClaims, groupPolicy.Group)
	if groupPolicy.Internal && claims.FreshStructured && ownedByGroup {
		s.logger.Infof("metadata: HDB claim match bypassed group=%s decision=own_claim", groupPolicy.Group)
		return false, nil
	}

	s.logger.Warnf(
		"metadata: HDB claim match found title=%q threshold_hours=%d cache_ttl=%s release_group=%q internal_group=%t fresh_structured=%t own_claim=%t matched_claims=%d",
		matchedTitle,
		thresholdHours,
		hdbClaimsCacheTTL,
		groupPolicy.Group,
		groupPolicy.Internal,
		claims.FreshStructured,
		ownedByGroup,
		len(matchedClaims),
	)
	return true, nil
}

// FailureReason describes an active HDB claim and links to its authoritative source.
func (s *claimChecker) FailureReason(meta api.UploadSubject) string {
	expired, thresholdHours, hoursSinceAir := hdbClaimWindowExpired(meta, s.claimWindowGraceHours())
	if expired {
		return "HDB claim window has expired"
	}
	if hoursSinceAir <= 0 {
		return "HDB has an active TV WEB claim; check the signup thread at " + s.endpoint
	}
	hoursRemaining := max(int(float64(thresholdHours)-hoursSinceAir+0.999999999), 1)
	return fmt.Sprintf("HDB has an active TV WEB claim; approximately %d hours remain. Check the signup thread at %s", hoursRemaining, s.endpoint)
}

func (s *claimChecker) loadHDBClaims(ctx context.Context, cachePath string, cacheTTL time.Duration) (hdbClaimData, error) {
	if err := ctx.Err(); err != nil {
		return hdbClaimData{}, fmt.Errorf("metadata: HDB claim cache load canceled: %w", err)
	}
	cached, cacheErr := readHDBClaimCache(cachePath, s.endpoint)
	if cacheErr != nil && !errors.Is(cacheErr, os.ErrNotExist) {
		s.logger.Warnf("metadata: HDB claims cache read failed path=%s err=%s", cachePath, redaction.RedactValue(cacheErr.Error(), nil))
	}
	cacheAge := time.Since(time.Unix(cached.FetchedAt, 0))
	cacheFresh := cached.Complete && cacheAge >= 0 && cacheAge < cacheTTL
	if cacheFresh {
		cached.FreshStructured = true
		s.logger.Debugf(
			"metadata: HDB claims cache hit path=%s age=%s ttl=%s records=%d",
			cachePath,
			cacheAge.Round(time.Second),
			cacheTTL,
			len(cached.Records),
		)
		return cached, nil
	}

	s.logger.Infof("metadata: HDB claims refreshing")
	fresh, fetchErr := s.fetchClaims(ctx)
	if fetchErr == nil && fresh.Complete {
		fresh.FreshStructured = true
		if err := writeHDBClaimCache(cachePath, s.endpoint, fresh.Records); err != nil {
			s.logger.Warnf("metadata: HDB claims cache write failed: %v", err)
		}
		s.logger.Infof("metadata: HDB claims refreshed count=%d", len(fresh.Records))
		return fresh, nil
	}
	if errors.Is(fetchErr, context.Canceled) || errors.Is(fetchErr, context.DeadlineExceeded) {
		return hdbClaimData{}, fetchErr
	}
	if fetchErr == nil && len(fresh.Records) > 0 {
		fresh.FreshStructured = false
		if cached.Complete {
			fresh.Records = slices.Concat(fresh.Records, cached.Records)
		}
		s.logger.Warnf("metadata: HDB claims list is incomplete; recognized claims can block uploads but cannot authorize an ownership bypass")
		return fresh, nil
	}
	if cached.Complete {
		cached.FreshStructured = false
		s.logger.Warnf("metadata: HDB claims fetch failed; using stale cache: %v", fetchErr)
		return cached, nil
	}
	return hdbClaimData{}, fetchErr
}

func (s *claimChecker) fetchClaims(ctx context.Context) (hdbClaimData, error) {
	if s.fetchOverride != nil {
		return s.fetchOverride(ctx)
	}
	cookies, err := resolveHDBCookies(ctx, s.cfg.MainSettings.DBPath)
	if err != nil {
		return hdbClaimData{}, err
	}
	if len(cookies) == 0 {
		return hdbClaimData{}, errors.New("metadata: HDB claims require stored HDB cookies")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint, nil)
	if err != nil {
		return hdbClaimData{}, fmt.Errorf("metadata: build HDB claims request: %w", err)
	}
	req.Header.Set("User-Agent", "upbrr")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	client := *s.httpClient
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return hdbClaimData{}, fmt.Errorf("metadata: fetch HDB claims: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return hdbClaimData{}, fmt.Errorf("metadata: HDB claims response status=%d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, hdbClaimsMaxBytes+1))
	if err != nil {
		return hdbClaimData{}, fmt.Errorf("metadata: read HDB claims: %w", err)
	}
	if len(body) > hdbClaimsMaxBytes {
		return hdbClaimData{}, errors.New("metadata: HDB claims response exceeds limit")
	}
	records, complete := extractHDBClaimRecords(string(body))
	if !complete && len(records) == 0 {
		return hdbClaimData{}, errors.New("metadata: HDB claims list missing; check the stored session")
	}
	return hdbClaimData{
		Records:         records,
		FetchedAt:       time.Now().Unix(),
		Complete:        complete,
		FreshStructured: complete,
	}, nil
}

func extractHDBClaimRecords(rawHTML string) ([]hdbClaimRecord, bool) {
	text := hdbClaimText(rawHTML)
	if text == "" {
		return nil, false
	}
	relay := false
	inList := false
	records := make([]hdbClaimRecord, 0)
	sawRow := false
	complete := true
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			continue
		}
		if !inList {
			if strings.Contains(line, "ALL titles on the list are also claimed for HDB.") {
				relay = true
			}
			if isHDBClaimHeader(line) {
				inList = true
			}
			continue
		}
		parts := strings.SplitN(line, "--", 4)
		if len(parts) < 3 {
			complete = false
			continue
		}
		title := strings.TrimSpace(parts[0])
		sites := parseHDBClaimSites(parts[1])
		if title == "" || len(sites) == 0 {
			complete = false
			continue
		}
		sawRow = true
		relayed := relay && slices.Contains(sites, "BTN") && !slices.Contains(sites, "HDB")
		if !slices.Contains(sites, "HDB") && !relayed {
			continue
		}
		_, aliases := hdbExtractAKAAliases(line)
		records = append(records, hdbClaimRecord{
			Title:          title,
			Aliases:        aliases,
			Sites:          sites,
			Group:          normalizeHDBClaimGroup(parts[2]),
			RelayedFromBTN: relayed,
		})
	}
	return records, sawRow && complete
}

func isHDBClaimHeader(line string) bool {
	parts := strings.Split(line, "--")
	return len(parts) >= 3 && strings.EqualFold(strings.TrimSpace(parts[0]), "Show") &&
		strings.EqualFold(strings.TrimSpace(parts[1]), "Site(s) Uploaded To") && strings.EqualFold(strings.TrimSpace(parts[2]), "Group")
}

func hdbClaimText(rawHTML string) string {
	doc, err := htmlnode.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return ""
	}
	// Search children first so a validated post/list is selected before any
	// enclosing forum container. A saved fragment uses the synthetic body.
	var text string
	var findHeader func(*htmlnode.Node)
	findHeader = func(node *htmlnode.Node) {
		if text != "" {
			return
		}
		if node.Type == htmlnode.ElementNode && (node.Data == "script" || node.Data == "style") {
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			findHeader(child)
		}
		if text != "" || node.Type != htmlnode.ElementNode || (node.Data != "div" && node.Data != "td" && node.Data != "body") {
			return
		}
		candidate := hdbNodeText(node)
		inList := false
		for line := range strings.SplitSeq(candidate, "\n") {
			line = strings.Join(strings.Fields(line), " ")
			if isHDBClaimHeader(line) {
				inList = true
				continue
			}
			parts := strings.SplitN(line, "--", 4)
			if inList && len(parts) >= 3 && strings.TrimSpace(parts[0]) != "" && len(parseHDBClaimSites(parts[1])) > 0 {
				text = candidate
				return
			}
		}
	}
	findHeader(doc)
	return text
}

func hdbNodeText(scope *htmlnode.Node) string {
	var text strings.Builder
	var visit func(*htmlnode.Node)
	visit = func(node *htmlnode.Node) {
		if node.Type == htmlnode.ElementNode && (node.Data == "script" || node.Data == "style") {
			return
		}
		if node.Type == htmlnode.TextNode {
			text.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
		if node.Type == htmlnode.ElementNode && (node.Data == "br" || node.Data == "tr" || node.Data == "p" || node.Data == "div" || node.Data == "li") {
			text.WriteByte('\n')
		}
	}
	visit(scope)
	return html.UnescapeString(text.String())
}

func parseHDBClaimSites(value string) []string {
	seen := make(map[string]struct{})
	sites := make([]string, 0)
	for _, part := range strings.FieldsFunc(strings.ToUpper(value), func(r rune) bool { return r == '|' || unicode.IsSpace(r) || r == ',' }) {
		site := strings.TrimSpace(part)
		if site == "BOTH" {
			for _, both := range []string{"HDB", "BTN"} {
				if _, ok := seen[both]; !ok {
					seen[both] = struct{}{}
					sites = append(sites, both)
				}
			}
			continue
		}
		if site != "HDB" && site != "BTN" {
			continue
		}
		if _, ok := seen[site]; !ok {
			seen[site] = struct{}{}
			sites = append(sites, site)
		}
	}
	return sites
}

func matchHDBClaimRecords(meta api.UploadSubject, claims hdbClaimData) ([]hdbClaimRecord, string) {
	candidates := hdbCandidateTitles(meta)
	matches := make([]hdbClaimRecord, 0)
	matchedTitle := ""
	for _, claim := range claims.Records {
		matched := false
		for variant := range hdbTitleVariants(claim.Title) {
			if _, ok := candidates[variant]; !ok {
				continue
			}
			matches = append(matches, claim)
			if matchedTitle == "" {
				matchedTitle = claim.Title
			}
			matched = true
			break
		}
		if matched {
			continue
		}
		for _, alias := range claim.Aliases {
			for variant := range hdbTitleVariants(alias) {
				if _, ok := candidates[variant]; !ok {
					continue
				}
				matches = append(matches, claim)
				if matchedTitle == "" {
					matchedTitle = claim.Title
				}
				matched = true
				break
			}
			if matched {
				break
			}
		}
	}
	return matches, matchedTitle
}

func hdbCandidateTitles(meta api.UploadSubject) map[string]struct{} {
	result := make(map[string]struct{})
	add := func(title string, year int) {
		for variant := range hdbTitleVariants(title) {
			result[variant] = struct{}{}
			if year > 0 {
				result[variant+" "+strconv.Itoa(year)] = struct{}{}
			}
		}
	}
	addAlternate := func(title string, year int) {
		title = strings.TrimSpace(title)
		if len(title) > len("AKA ") && strings.EqualFold(title[:len("AKA ")], "AKA ") {
			title = strings.TrimSpace(title[len("AKA "):])
		}
		add(title, year)
	}
	add(meta.Release.Title, meta.Release.Year)
	addAlternate(meta.AlternateTitle, meta.Release.Year)
	if imdb := meta.ProviderMetadata.IMDB; imdb != nil {
		add(imdb.Title, imdb.Year)
		addAlternate(imdb.AKA, imdb.Year)
	}
	if tvdb := meta.ProviderMetadata.TVDB; tvdb != nil {
		add(tvdb.Name, tvdb.Year)
		add(tvdb.NameEnglish, tvdb.Year)
	}
	if tmdb := meta.ProviderMetadata.TMDB; tmdb != nil {
		add(tmdb.Title, tmdb.Year)
		add(tmdb.OriginalTitle, tmdb.Year)
		addAlternate(tmdb.RetrievedAKA, tmdb.Year)
	}
	if tvmaze := meta.ProviderMetadata.TVmaze; tvmaze != nil {
		year := 0
		if premiered, err := time.Parse("2006-01-02", tvmaze.Premiered); err == nil {
			year = premiered.Year()
		}
		add(tvmaze.Name, year)
	}
	return result
}

func hdbTitleVariants(value string) map[string]struct{} {
	canonical, aliases := hdbExtractAKAAliases(value)
	variants := make(map[string]struct{})
	for _, title := range append([]string{canonical}, aliases...) {
		if normalized := normalizeHDBClaimTitle(title); normalized != "" {
			variants[normalized] = struct{}{}
		}
	}
	return variants
}

func hdbExtractAKAAliases(value string) (string, []string) {
	value = strings.TrimSpace(value)
	matches := hdbAKAExtractPattern.FindAllStringSubmatch(value, -1)
	if len(matches) == 0 {
		return value, nil
	}
	aliases := make([]string, 0)
	for _, match := range matches {
		for alias := range strings.FieldsFuncSeq(match[1], func(r rune) bool { return r == ',' || r == '/' || r == ';' }) {
			if alias = strings.TrimSpace(alias); alias != "" {
				aliases = append(aliases, alias)
			}
		}
	}
	return strings.TrimSpace(hdbAKAExtractPattern.ReplaceAllString(value, "")), aliases
}

func normalizeHDBClaimTitle(value string) string {
	value = html.UnescapeString(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "&", " and ")
	value = strings.ToLower(value)
	value = hdbNonAlnumPattern.ReplaceAllString(value, " ")
	return strings.TrimSpace(hdbSpacePattern.ReplaceAllString(value, " "))
}

func hdbClaimsOwnedByGroup(claims []hdbClaimRecord, group string) bool {
	group = trackers.NormalizeTrackerReleaseGroup(group)
	if group == "" || len(claims) == 0 {
		return false
	}
	for _, claim := range claims {
		if claim.RelayedFromBTN || !slices.Contains(claim.Sites, "HDB") || !strings.EqualFold(normalizeHDBClaimGroup(claim.Group), group) {
			return false
		}
	}
	return true
}

func normalizeHDBClaimGroup(value string) string {
	return trackers.NormalizeTrackerReleaseGroup(strings.TrimSpace(value))
}

func readHDBClaimCache(path, endpoint string) (hdbClaimData, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return hdbClaimData{}, fmt.Errorf("metadata: read HDB claims cache: %w", err)
	}
	var cache hdbClaimedShowsCache
	if err := json.Unmarshal(payload, &cache); err != nil {
		return hdbClaimData{}, fmt.Errorf("metadata: unmarshal HDB claims cache: %w", err)
	}
	if cache.Version != hdbClaimsCacheVersion || cache.SourceURL != endpoint || cache.FetchedAt <= 0 || cache.FetchedAt > time.Now().Unix() {
		return hdbClaimData{}, errors.New("metadata: HDB claims cache identity is invalid")
	}
	records := make([]hdbClaimRecord, 0, len(cache.Claims))
	for _, claim := range cache.Claims {
		claim.Title = strings.TrimSpace(claim.Title)
		claim.Aliases = normalizeHDBClaimAliases(claim.Aliases)
		claim.Sites = parseHDBClaimSites(strings.Join(claim.Sites, " "))
		claim.Group = normalizeHDBClaimGroup(claim.Group)
		if claim.Title == "" || len(claim.Sites) == 0 || (!slices.Contains(claim.Sites, "HDB") && !claim.RelayedFromBTN) {
			return hdbClaimData{}, errors.New("metadata: HDB claims cache contains an invalid record")
		}
		records = append(records, claim)
	}
	return hdbClaimData{
		Records:   records,
		FetchedAt: cache.FetchedAt,
		Complete:  cache.Complete,
	}, nil
}

func writeHDBClaimCache(path, endpoint string, records []hdbClaimRecord) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("metadata: create HDB claims cache directory: %w", err)
	}
	records = append([]hdbClaimRecord(nil), records...)
	for i := range records {
		records[i].Title = strings.TrimSpace(records[i].Title)
		records[i].Aliases = normalizeHDBClaimAliases(records[i].Aliases)
		records[i].Group = normalizeHDBClaimGroup(records[i].Group)
		sort.Strings(records[i].Sites)
	}
	sort.SliceStable(records, func(left, right int) bool {
		if titleOrder := strings.Compare(normalizeHDBClaimTitle(records[left].Title), normalizeHDBClaimTitle(records[right].Title)); titleOrder != 0 {
			return titleOrder < 0
		}
		if groupOrder := strings.Compare(strings.ToLower(records[left].Group), strings.ToLower(records[right].Group)); groupOrder != 0 {
			return groupOrder < 0
		}
		return strings.Join(records[left].Sites, "|") < strings.Join(records[right].Sites, "|")
	})
	payload, err := json.MarshalIndent(hdbClaimedShowsCache{
		Version:   hdbClaimsCacheVersion,
		FetchedAt: time.Now().Unix(),
		SourceURL: endpoint,
		Complete:  true,
		Claims:    records,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("metadata: marshal HDB claims cache: %w", err)
	}
	return writeHDBClaimCacheFile(path, payload)
}

func normalizeHDBClaimAliases(aliases []string) []string {
	result := make([]string, 0, len(aliases))
	seen := make(map[string]struct{})
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if normalizeHDBClaimTitle(alias) == "" {
			continue
		}
		if _, ok := seen[alias]; ok {
			continue
		}
		seen[alias] = struct{}{}
		result = append(result, alias)
	}
	sort.Strings(result)
	return result
}

func writeHDBClaimCacheFile(path string, payload []byte) error {
	tmpFile, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("metadata: create temporary HDB claims cache: %w", err)
	}
	tmpPath := tmpFile.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmpFile.Chmod(0o600); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("metadata: chmod temporary HDB claims cache: %w", err)
	}
	if _, err := tmpFile.Write(payload); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("metadata: write temporary HDB claims cache: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("metadata: sync temporary HDB claims cache: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("metadata: close temporary HDB claims cache: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("metadata: replace HDB claims cache: %w", err)
	}
	removeTemp = false
	return nil
}

func hdbClaimsCachePath(dbPath string) (string, error) {
	path, err := db.FileInSubdir(dbPath, "cache", filepath.Join("banned", "HDB_claimed_releases.json"))
	if err != nil {
		return "", fmt.Errorf("metadata: resolve HDB claims path: %w", err)
	}
	return path, nil
}

func hdbTrackerConfig(cfg config.Config) (config.TrackerConfig, bool) {
	for name, entry := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), "HDB") {
			return entry, true
		}
	}
	return config.TrackerConfig{}, false
}

func (s *claimChecker) claimWindowGraceHours() int {
	grace := hdbClaimDefaultGrace
	entry, ok := hdbTrackerConfig(s.cfg)
	if !ok || entry.Unknown == nil {
		return grace
	}
	value, ok := entry.Unknown["claim_window_grace_hours"]
	if !ok {
		return grace
	}
	parsed := hdbOptionalInt(value)
	if parsed < 0 {
		return 0
	}
	if parsed == 0 {
		return grace
	}
	return parsed
}

func hdbOptionalInt(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	}
	return 0
}

func hdbClaimPolicyApplies(meta api.UploadSubject) (bool, bool) {
	isTV := hdbIsTVCategory(meta) || strings.EqualFold(strings.TrimSpace(string(meta.Identity.Category)), "TV")
	if !isTV {
		return false, strings.TrimSpace(string(meta.Identity.Category)) != ""
	}
	hasKnownSource := false
	for _, value := range []string{meta.Type, meta.Source, meta.Release.Type, meta.Release.Source} {
		normalized := strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(value)))
		if normalized == "" {
			continue
		}
		if normalized == "web" || strings.Contains(normalized, "webdl") || strings.Contains(normalized, "webrip") {
			return true, true
		}
		switch normalized {
		case "bluray", "bdrip", "remux", "hdtv", "pdtv", "sdtv", "dvd", "dvdrip", "encode":
			hasKnownSource = true
		}
	}
	return false, hasKnownSource
}

func hdbIsTVCategory(meta api.UploadSubject) bool {
	return meta.SeasonInt > 0 || meta.EpisodeInt > 0 || meta.Release.Season > 0 || meta.Release.Episode > 0 || meta.TVPack ||
		strings.TrimSpace(meta.DailyEpisodeDate) != ""
}

func isHDBSceneRelease(meta api.UploadSubject) bool {
	return meta.Scene || strings.TrimSpace(meta.SceneName) != ""
}

func hdbClaimWindowExpired(meta api.UploadSubject, graceHours int) (bool, int, float64) {
	thresholdHours := hdbClaimWindowHours + graceHours
	airedDate, err := time.Parse("2006-01-02", strings.TrimSpace(meta.TVDBAiredDate))
	if err != nil {
		return false, thresholdHours, 0
	}
	airsTime, hasTime := hdbParseTime(strings.TrimSpace(meta.TVDBAirsTime))
	location := time.UTC
	if hasTime {
		thresholdHours = hdbClaimWindowHours
		if timezone := strings.TrimSpace(meta.TVDBAirsTimezone); timezone != "" {
			if loaded, err := time.LoadLocation(timezone); err == nil {
				location = loaded
			} else {
				location = time.Now().Location()
			}
		} else {
			location = time.Now().Location()
		}
	}
	airedAt := time.Date(airedDate.Year(), airedDate.Month(), airedDate.Day(), 0, 0, 0, 0, time.UTC)
	if hasTime {
		airedAt = time.Date(airedDate.Year(), airedDate.Month(), airedDate.Day(), airsTime.Hour(), airsTime.Minute(), airsTime.Second(), 0, location)
	}
	hoursSinceAir := time.Since(airedAt.UTC()).Hours()
	return hoursSinceAir > float64(thresholdHours), thresholdHours, hoursSinceAir
}

func hdbParseTime(value string) (time.Time, bool) {
	if match := hdbTime24Pattern.FindStringSubmatch(value); len(match) == 4 {
		hour, _ := strconv.Atoi(match[1])
		minute, _ := strconv.Atoi(match[2])
		second := 0
		if match[3] != "" {
			second, _ = strconv.Atoi(match[3])
		}
		if hour <= 23 && minute <= 59 && second <= 59 {
			return time.Date(2000, 1, 1, hour, minute, second, 0, time.UTC), true
		}
	}
	if match := hdbTime12Pattern.FindStringSubmatch(value); len(match) == 4 {
		hour, _ := strconv.Atoi(match[1])
		minute, _ := strconv.Atoi(match[2])
		if hour >= 1 && hour <= 12 && minute <= 59 {
			hour %= 12
			if strings.EqualFold(match[3], "pm") {
				hour += 12
			}
			return time.Date(2000, 1, 1, hour, minute, 0, 0, time.UTC), true
		}
	}
	return time.Time{}, false
}
