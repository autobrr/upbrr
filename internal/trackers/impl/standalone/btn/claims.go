// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
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
	"github.com/autobrr/upbrr/internal/cookies"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	btnSiteBaseURL              = "https://broadcasthe.net"
	btnBackupBaseURL            = "https://backup.landof.tv"
	btnUserPath                 = "/user.php"
	btnClaimsLoginPath          = "/login.php"
	btnClaimedShowsURL          = "https://broadcasthe.net/forums.php?action=viewthread&threadid=30793"
	btnClaimedShowsPostID       = "post1405482"
	btnClaimedShowsCacheVersion = 2
	btnClaimedShowsCacheTTL     = 48 * time.Hour
	btnClaimWindowBaseHours     = 48
	btnClaimWindowDefaultGrace  = 24
)

var (
	btnLineBreakPattern  = regexp.MustCompile(`(?i)<br\s*/?>`)
	btnTagPattern        = regexp.MustCompile(`(?s)<[^>]*>`)
	btnAKAExtractPattern = regexp.MustCompile(`(?i)\(\s*aka\s*:\s*([^\)]*?)\)`)
	btnSxxExxCutPattern  = regexp.MustCompile(`(?i)\bS\d{1,2}E\d{1,3}\b`)
	btnSeasonCutPattern  = regexp.MustCompile(`(?i)\bS\d{1,2}\b`)
	btnDateCutPattern    = regexp.MustCompile(`\b\d{4}[\-\.]\d{2}[\-\.]\d{2}\b`)
	btnNonAlnumPattern   = regexp.MustCompile(`[^a-z0-9]+`)
	btnSpacePattern      = regexp.MustCompile(`\s+`)
	btnTime24Pattern     = regexp.MustCompile(`^(\d{1,2}):(\d{2})(?::(\d{2}))?$`)
	btnTime12Pattern     = regexp.MustCompile(`^(\d{1,2}):(\d{2})\s*([AaPp][Mm])$`)
)

type btnClaimedShowsCache struct {
	Version   int              `json:"version,omitempty"`
	FetchedAt int64            `json:"fetched_at"`
	SourceURL string           `json:"source_url"`
	PostID    string           `json:"post_id"`
	Claims    []btnClaimRecord `json:"claims,omitempty"`
	Titles    []string         `json:"titles,omitempty"`
}

type btnClaimRecord struct {
	Title string   `json:"title"`
	Sites []string `json:"sites"`
	Group string   `json:"group"`
}

type btnClaimData struct {
	Records         []btnClaimRecord
	LegacyTitles    map[string]struct{}
	FetchedAt       int64
	FreshStructured bool
}

type claimChecker struct {
	cfg           config.Config
	logger        api.Logger
	fetchOverride func(context.Context) (btnClaimData, error)
}

// NewClaimChecker returns a BTN-only claim checker using BTN's stored session
// and claim cache. A nil logger is replaced with a no-op logger.
func (d *Definition) NewClaimChecker(cfg config.Config, logger api.Logger) trackers.ClaimChecker {
	if logger == nil {
		logger = api.NopLogger{}
	}
	return &claimChecker{
		cfg:    cfg,
		logger: logger,
	}
}

// HasClaim reports whether a TV title appears in BTN's claimed-show list and
// remains inside its claim window. Confirmed Scene releases and configured
// internal releases with fresh, unambiguous same-group ownership evidence
// bypass claims. Legacy or stale cache data may preserve a claim block but
// cannot authorize that bypass. Non-TV content and unavailable claim data fail
// open as unclaimed; malformed or missing air dates keep a matched claim active
// because expiry cannot be established.
func (s *claimChecker) HasClaim(ctx context.Context, meta api.UploadSubject) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("metadata: BTN claim check canceled: %w", err)
	}
	if isBTNSceneRelease(meta) {
		if s.logger != nil {
			s.logger.Debugf("metadata: BTN claims skipped origin=scene decision=allowed")
		}
		return false, nil
	}
	if !btnIsTVCategory(meta) {
		if s.logger != nil {
			s.logger.Debugf("metadata: BTN claims skipped for non-TV content")
		}
		return false, nil
	}

	cachePath, err := btnClaimsPath(s.cfg.MainSettings.DBPath)
	if err != nil {
		return false, fmt.Errorf("metadata: BTN claims cache path: %w", err)
	}

	claims, err := s.loadBTNClaims(ctx, cachePath, btnClaimedShowsCacheTTL)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false, err
		}
		if s.logger != nil {
			s.logger.Warnf("metadata: BTN claim list unavailable: %v", err)
		}
		return false, nil
	}
	matchedClaims, matchedTitle := matchBTNClaimRecords(meta, claims)
	if len(matchedClaims) == 0 {
		if s.logger != nil {
			s.logger.Debugf("metadata: BTN claims found no title match for release=%q", meta.ReleaseName)
		}
		return false, nil
	}

	graceHours := s.btnClaimWindowGraceHours()
	expired, thresholdHours, hoursSinceAir := btnClaimWindowExpired(meta, graceHours)
	if expired {
		if s.logger != nil {
			s.logger.Debugf(
				"metadata: BTN claim window expired for %q (hours_since_air=%.2f threshold=%d)",
				matchedTitle,
				hoursSinceAir,
				thresholdHours,
			)
		}
		return false, nil
	}
	trackerCfg, _ := trackerConfigFor(s.cfg, "BTN")
	groupPolicy := trackers.ResolveGroupPolicy(trackerCfg, meta)
	ownedByGroup := claimsOwnedByGroup(matchedClaims, groupPolicy.Group)
	if groupPolicy.Internal && claims.FreshStructured && ownedByGroup {
		if s.logger != nil {
			s.logger.Infof("metadata: BTN claim match bypassed group=%s decision=own_claim", groupPolicy.Group)
		}
		return false, nil
	}

	if s.logger != nil {
		s.logger.Warnf(
			"metadata: BTN claim match found title=%q threshold_hours=%d cache_ttl=%s release_group=%q internal_group=%t fresh_structured=%t own_claim=%t matched_claims=%d",
			matchedTitle,
			thresholdHours,
			btnClaimedShowsCacheTTL,
			groupPolicy.Group,
			groupPolicy.Internal,
			claims.FreshStructured,
			ownedByGroup,
			len(matchedClaims),
		)
	}
	return true, nil
}

func claimsOwnedByGroup(claims []btnClaimRecord, group string) bool {
	if group == "" || len(claims) == 0 {
		return false
	}
	for _, claim := range claims {
		claimGroup := trackers.NormalizeTrackerReleaseGroup(claim.Group)
		if claimGroup == "" || !strings.EqualFold(claimGroup, group) {
			return false
		}
	}
	return true
}

func (s *claimChecker) btnClaimWindowGraceHours() int {
	grace := btnClaimWindowDefaultGrace
	entry, ok := trackerConfigFor(s.cfg, "BTN")
	if !ok || entry.Unknown == nil {
		return grace
	}
	value, ok := entry.Unknown["claim_window_grace_hours"]
	if !ok {
		return grace
	}
	parsed := parseOptionalInt(value)
	if parsed < 0 {
		return 0
	}
	if parsed == 0 {
		return grace
	}
	return parsed
}

func (s *claimChecker) loadBTNClaimedTitles(ctx context.Context, cachePath string, cacheTTL time.Duration) (map[string]struct{}, error) {
	claims, err := s.loadBTNClaims(ctx, cachePath, cacheTTL)
	if err != nil {
		return nil, err
	}
	return claimDataTitles(claims), nil
}

func (s *claimChecker) loadBTNClaims(ctx context.Context, cachePath string, cacheTTL time.Duration) (btnClaimData, error) {
	if err := ctx.Err(); err != nil {
		return btnClaimData{}, fmt.Errorf("metadata: BTN claim cache load canceled: %w", err)
	}
	cached, cacheErr := readBTNClaimCache(cachePath)
	if cacheErr != nil {
		if s.logger != nil && !errors.Is(cacheErr, os.ErrNotExist) {
			s.logger.Warnf("metadata: BTN claims cache read failed path=%s err=%s", cachePath, redaction.RedactValue(cacheErr.Error(), nil))
		}
	} else if s.logger != nil {
		s.logger.Debugf(
			"metadata: BTN claims cache loaded path=%s target=BTN records=%d legacy_titles=%d fetched_at=%d",
			cachePath,
			len(cached.Records),
			len(cached.LegacyTitles),
			cached.FetchedAt,
		)
	}
	cacheAge := time.Since(time.Unix(cached.FetchedAt, 0))
	cacheFresh := cached.hasClaims() && cacheAge >= 0 && cacheAge < cacheTTL
	if cacheFresh && len(cached.Records) > 0 {
		cached.FreshStructured = true
		if s.logger != nil {
			s.logger.Debugf(
				"metadata: BTN claims cache hit path=%s target=BTN age=%s ttl=%s records=%d",
				cachePath,
				cacheAge.Round(time.Second),
				cacheTTL,
				len(cached.Records),
			)
		}
		return cached, nil
	}
	if s.logger != nil {
		switch {
		case !cached.hasClaims():
			s.logger.Debugf("metadata: BTN claims cache miss path=%s target=BTN", cachePath)
		case cacheFresh:
			s.logger.Debugf(
				"metadata: BTN claims cache refresh required path=%s target=BTN reason=legacy_format age=%s ttl=%s legacy_titles=%d",
				cachePath,
				cacheAge.Round(time.Second),
				cacheTTL,
				len(cached.LegacyTitles),
			)
		default:
			s.logger.Debugf(
				"metadata: BTN claims cache refresh required path=%s target=BTN reason=stale age=%s ttl=%s records=%d legacy_titles=%d",
				cachePath,
				cacheAge.Round(time.Second),
				cacheTTL,
				len(cached.Records),
				len(cached.LegacyTitles),
			)
		}
	}

	fresh, fetchErr := s.fetchClaims(ctx)
	if fetchErr == nil && len(fresh.Records) > 0 {
		fresh.FreshStructured = true
		if err := writeBTNClaimCache(cachePath, fresh.Records); err != nil && s.logger != nil {
			s.logger.Warnf("metadata: BTN claims cache write failed: %v", err)
		} else if s.logger != nil {
			s.logger.Debugf("metadata: BTN claims cache saved path=%s records=%d", cachePath, len(fresh.Records))
		}
		return fresh, nil
	}
	if fetchErr == nil && len(fresh.Records) == 0 {
		fetchErr = errors.New("metadata: BTN claims fetch returned no structured records")
	}
	if fetchErr != nil && s.logger != nil {
		s.logger.Warnf("metadata: BTN claims fetch failed; falling back to cache: %v", fetchErr)
	}
	if errors.Is(fetchErr, context.Canceled) || errors.Is(fetchErr, context.DeadlineExceeded) {
		return btnClaimData{}, fetchErr
	}
	if len(cached.Records) > 0 || len(cached.LegacyTitles) > 0 {
		cached.FreshStructured = false
		if s.logger != nil {
			s.logger.Debugf(
				"metadata: BTN claims cache fallback path=%s target=BTN decision=non_authoritative records=%d legacy_titles=%d",
				cachePath,
				len(cached.Records),
				len(cached.LegacyTitles),
			)
		}
		return cached, nil
	}
	return btnClaimData{}, fetchErr
}

func (s *claimChecker) fetchClaims(ctx context.Context) (btnClaimData, error) {
	if s.fetchOverride != nil {
		return s.fetchOverride(ctx)
	}
	return s.fetchBTNClaims(ctx)
}

func (s *claimChecker) fetchBTNClaimedTitles(ctx context.Context) (map[string]struct{}, error) {
	claims, err := s.fetchBTNClaims(ctx)
	if err != nil {
		return nil, err
	}
	return claimDataTitles(claims), nil
}

func (s *claimChecker) fetchBTNClaims(ctx context.Context) (btnClaimData, error) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Timeout: 30 * time.Second, Jar: jar}

	if s.logger != nil {
		s.logger.Debugf("metadata: BTN claims fetch starting url=%s", btnClaimedShowsURL)
	}
	if err := s.loadBTNCookiesForClaims(ctx, client); err != nil {
		if s.logger != nil {
			s.logger.Warnf("metadata: BTN claims cookie load failed: %v", err)
		}
	} else if s.logger != nil {
		s.logger.Debugf("metadata: BTN claims cookie load completed")
	}
	isLoggedIn, err := s.btnClaimsSessionValid(ctx, client)
	if err != nil {
		if s.logger != nil {
			s.logger.Warnf("metadata: BTN claims session validation failed: %v", err)
		}
		return btnClaimData{}, err
	} else if s.logger != nil {
		s.logger.Debugf("metadata: BTN claims session valid=%t", isLoggedIn)
	}
	if !isLoggedIn {
		return btnClaimData{}, errors.New("metadata: BTN claims require an existing valid session")
	}
	mirrorBTNCookiesForClaimedThread(client)
	if s.logger != nil {
		s.logger.Debugf("metadata: BTN claims mirrored cookies for broadcasthe thread")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, btnClaimedShowsURL, nil)
	if err != nil {
		if s.logger != nil {
			s.logger.Warnf("metadata: BTN claims request build failed: %v", err)
		}
		return btnClaimData{}, fmt.Errorf("metadata: build BTN claims request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		if s.logger != nil {
			s.logger.Warnf("metadata: BTN claims fetch request failed: %v", err)
		}
		return btnClaimData{}, fmt.Errorf("metadata: execute BTN claims request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if s.logger != nil {
			s.logger.Warnf("metadata: BTN claims fetch returned status=%d", resp.StatusCode)
		}
		return btnClaimData{}, fmt.Errorf("status %d", resp.StatusCode)
	}
	if s.logger != nil {
		s.logger.Debugf("metadata: BTN claims fetch returned status=%d", resp.StatusCode)
	}

	payload, err := ioReadAllLimit(resp, 1<<20)
	if err != nil {
		if s.logger != nil {
			s.logger.Warnf("metadata: BTN claims response read failed: %v", err)
		}
		return btnClaimData{}, err
	}
	if err := ctx.Err(); err != nil {
		return btnClaimData{}, fmt.Errorf("metadata: BTN claims response canceled: %w", err)
	}
	records := extractBTNClaimRecords(string(payload))
	if s.logger != nil {
		s.logger.Debugf("metadata: BTN claims parsed %d structured records from claimed thread", len(records))
	}
	return btnClaimData{
		Records:         records,
		FetchedAt:       time.Now().Unix(),
		FreshStructured: true,
	}, nil
}

func (s *claimChecker) loadBTNCookiesForClaims(ctx context.Context, client *http.Client) error {
	if client == nil || client.Jar == nil {
		return nil
	}

	trackerCookies, err := cookies.LoadTrackerHTTPCookies(ctx, s.cfg.MainSettings.DBPath, "BTN", "")
	if err != nil {
		if s.logger != nil {
			s.logger.Debugf("metadata: BTN claims cookie load skipped: %v", err)
		}
		return nil
	}

	if err := setBTNJarCookiesFromNetscape(client, btnSiteBaseURL, trackerCookies); err != nil {
		return err
	}
	if err := setBTNJarCookiesFromNetscape(client, btnBackupBaseURL, trackerCookies); err != nil {
		return err
	}

	if s.logger != nil {
		s.logger.Debugf("metadata: BTN claims loaded %d cookies from shared store", len(trackerCookies))
	}
	return nil
}

func setBTNJarCookiesFromNetscape(client *http.Client, rawURL string, values []*http.Cookie) error {
	if client == nil || client.Jar == nil || len(values) == 0 {
		return nil
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("metadata: parse BTN cookie URL: %w", err)
	}

	jarCookies := make([]*http.Cookie, 0, len(values))
	for _, cookie := range values {
		if cookie == nil || strings.TrimSpace(cookie.Name) == "" || strings.TrimSpace(cookie.Value) == "" {
			continue
		}
		copied := *cookie // #nosec G124 -- Outbound tracker jar cookie mirrors imported BTN cookie attributes.
		copied.Domain = parsed.Hostname()
		if strings.TrimSpace(copied.Path) == "" {
			copied.Path = "/"
		}
		// #nosec G124 -- Outbound tracker jar cookie mirrors imported BTN cookie attributes.
		jarCookies = append(jarCookies, &copied)
	}
	if len(jarCookies) == 0 {
		return nil
	}

	client.Jar.SetCookies(parsed, jarCookies)
	return nil
}

func (s *claimChecker) btnClaimsSessionValid(ctx context.Context, client *http.Client) (bool, error) {
	if client == nil {
		return false, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, btnSiteBaseURL+btnUserPath, nil)
	if err != nil {
		return false, fmt.Errorf("metadata: build BTN claims session validation request: %w", err)
	}
	req.Header.Set("User-Agent", "upbrr")

	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("metadata: execute BTN claims session validation request: %w", err)
	}
	defer resp.Body.Close()

	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	if strings.Contains(strings.ToLower(finalURL), strings.ToLower(btnClaimsLoginPath)) {
		return false, nil
	}
	return resp.StatusCode >= 200 && resp.StatusCode < 400, nil
}

func extractBTNClaimedShows(rawHTML string) map[string]struct{} {
	records := extractBTNClaimRecords(rawHTML)
	claims := btnClaimData{Records: records}
	return claimDataTitles(claims)
}

func extractBTNClaimRecords(rawHTML string) []btnClaimRecord {
	scopedHTML, ok := extractBTNClaimedPostHTML(rawHTML)
	if !ok {
		return nil
	}
	normalized := btnLineBreakPattern.ReplaceAllString(scopedHTML, "\n")
	normalized = btnTagPattern.ReplaceAllString(normalized, "")
	normalized = html.UnescapeString(normalized)

	lines := strings.Split(normalized, "\n")
	inCurrentShows := false
	records := make([]btnClaimRecord, 0)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "current shows:") {
			inCurrentShows = true
			continue
		}
		if !inCurrentShows {
			continue
		}
		if strings.Contains(lower, "upcoming shows:") || strings.Contains(lower, "available shows:") {
			break
		}
		parts := strings.SplitN(trimmed, "--", 4)
		if len(parts) != 4 {
			continue
		}
		title := strings.TrimSpace(parts[0])
		if strings.EqualFold(title, "show") || title == "" {
			continue
		}
		sites := parseBTNClaimSites(parts[1])
		group := normalizeBTNClaimGroup(parts[2])
		if len(sites) == 0 {
			continue
		}
		records = append(records, btnClaimRecord{
			Title: title,
			Sites: sites,
			Group: group,
		})
	}
	sort.SliceStable(records, func(left, right int) bool {
		if titleOrder := strings.Compare(normalizeBTNTitle(records[left].Title), normalizeBTNTitle(records[right].Title)); titleOrder != 0 {
			return titleOrder < 0
		}
		if groupOrder := strings.Compare(strings.ToLower(records[left].Group), strings.ToLower(records[right].Group)); groupOrder != 0 {
			return groupOrder < 0
		}
		return strings.Join(records[left].Sites, "|") < strings.Join(records[right].Sites, "|")
	})
	return slices.CompactFunc(records, func(left, right btnClaimRecord) bool {
		return normalizeBTNTitle(left.Title) == normalizeBTNTitle(right.Title) &&
			strings.EqualFold(left.Group, right.Group) && slices.Equal(left.Sites, right.Sites)
	})
}

func normalizeBTNClaimGroup(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "-")
	return strings.TrimSpace(value)
}

func parseBTNClaimSites(value string) []string {
	seen := make(map[string]struct{}, 2)
	sites := make([]string, 0, 2)
	for token := range strings.FieldsFuncSeq(value, func(r rune) bool {
		return r == '|' || unicode.IsSpace(r)
	}) {
		site := strings.ToUpper(strings.TrimSpace(token))
		if site != "BTN" && site != "HDB" {
			continue
		}
		if _, ok := seen[site]; ok {
			continue
		}
		seen[site] = struct{}{}
		sites = append(sites, site)
	}
	return sites
}

func extractBTNClaimedPostHTML(rawHTML string) (string, bool) {
	if strings.TrimSpace(rawHTML) == "" {
		return "", false
	}

	doc, err := htmlnode.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return "", false
	}

	content := findBTNHTMLNode(doc, func(node *htmlnode.Node) bool {
		return node.Type == htmlnode.ElementNode && node.Data == "div" && btnHTMLNodeID(node) == "content1405482"
	})
	if content != nil {
		if rendered := renderBTNHTMLChildren(content); rendered != "" {
			return rendered, true
		}
	}

	scope := findBTNHTMLNode(doc, func(node *htmlnode.Node) bool {
		return node.Type == htmlnode.ElementNode && node.Data == "table" && btnHTMLNodeID(node) == btnClaimedShowsPostID
	})
	if scope == nil {
		return "", false
	}

	content = findBTNHTMLNode(scope, func(node *htmlnode.Node) bool {
		if node.Type != htmlnode.ElementNode || node.Data != "div" {
			return false
		}
		return btnHTMLNodeHasClass(node, "postcontent")
	})
	if content != nil {
		if rendered := renderBTNHTMLChildren(content); rendered != "" {
			return rendered, true
		}
	}

	if rendered := renderBTNHTMLChildren(scope); rendered != "" {
		return rendered, true
	}

	return "", false
}

func findBTNHTMLNode(root *htmlnode.Node, match func(*htmlnode.Node) bool) *htmlnode.Node {
	if root == nil {
		return nil
	}
	if match(root) {
		return root
	}
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if found := findBTNHTMLNode(child, match); found != nil {
			return found
		}
	}
	return nil
}

func btnHTMLNodeID(node *htmlnode.Node) string {
	return strings.TrimSpace(btnHTMLAttr(node, "id"))
}

func btnHTMLNodeHasClass(node *htmlnode.Node, className string) bool {
	classes := strings.FieldsSeq(strings.TrimSpace(btnHTMLAttr(node, "class")))
	for candidate := range classes {
		if strings.EqualFold(candidate, className) {
			return true
		}
	}
	return false
}

func btnHTMLAttr(node *htmlnode.Node, key string) string {
	if node == nil {
		return ""
	}
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) {
			return attr.Val
		}
	}
	return ""
}

func renderBTNHTMLChildren(node *htmlnode.Node) string {
	if node == nil {
		return ""
	}
	var buf bytes.Buffer
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if err := htmlnode.Render(&buf, child); err != nil {
			return ""
		}
	}
	return buf.String()
}

func matchBTNClaimedTitle(meta api.UploadSubject, claimed map[string]struct{}) (bool, string) {
	for candidate := range btnCandidateTitles(meta) {
		if _, ok := claimed[candidate]; ok {
			return true, candidate
		}
	}
	return false, ""
}

func matchBTNClaimRecords(meta api.UploadSubject, claims btnClaimData) ([]btnClaimRecord, string) {
	candidates := btnCandidateTitles(meta)
	matches := make([]btnClaimRecord, 0)
	matchedTitle := ""
	for _, claim := range claims.Records {
		if !slices.Contains(claim.Sites, "BTN") {
			continue
		}
		for variant := range btnTitleVariants(claim.Title) {
			if _, ok := candidates[variant]; !ok {
				continue
			}
			matches = append(matches, claim)
			if matchedTitle == "" {
				matchedTitle = claim.Title
			}
			break
		}
	}
	if len(matches) == 0 {
		if matched, legacyTitle := matchBTNClaimedTitle(meta, claims.LegacyTitles); matched {
			matches = append(matches, btnClaimRecord{Title: legacyTitle, Sites: []string{"BTN"}})
			matchedTitle = legacyTitle
		}
	}
	return matches, matchedTitle
}

func claimDataTitles(claims btnClaimData) map[string]struct{} {
	titles := make(map[string]struct{}, len(claims.LegacyTitles)+len(claims.Records))
	for title := range claims.LegacyTitles {
		titles[title] = struct{}{}
	}
	for _, claim := range claims.Records {
		if !slices.Contains(claim.Sites, "BTN") {
			continue
		}
		for title := range btnTitleVariants(claim.Title) {
			titles[title] = struct{}{}
		}
	}
	return titles
}

func (claims btnClaimData) hasClaims() bool {
	return len(claims.Records) > 0 || len(claims.LegacyTitles) > 0
}

func btnCandidateTitles(meta api.UploadSubject) map[string]struct{} {
	candidates := []string{
		meta.ReleaseName,
		meta.ReleaseNameNoTag,
		meta.Filename,
		pathutil.Base(meta.SourcePath),
	}
	if meta.ProviderMetadata.TVDB != nil {
		candidates = append(candidates, meta.ProviderMetadata.TVDB.Name, meta.ProviderMetadata.TVDB.NameEnglish)
	}
	if meta.ProviderMetadata.TMDB != nil {
		candidates = append(candidates, meta.ProviderMetadata.TMDB.Title, meta.ProviderMetadata.TMDB.OriginalTitle)
	}
	if meta.ProviderMetadata.TVmaze != nil {
		candidates = append(candidates, meta.ProviderMetadata.TVmaze.Name)
	}

	out := make(map[string]struct{})
	for _, title := range candidates {
		trimmed := strings.TrimSpace(title)
		if trimmed == "" {
			continue
		}
		for variant := range btnTitleVariants(trimmed) {
			out[variant] = struct{}{}
		}
		derived := btnDeriveShowTitleFromRelease(trimmed)
		for variant := range btnTitleVariants(derived) {
			out[variant] = struct{}{}
		}
	}
	return out
}

func btnDeriveShowTitleFromRelease(value string) string {
	candidate := strings.ReplaceAll(value, ".", " ")
	candidate = strings.ReplaceAll(candidate, "_", " ")
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return ""
	}
	cutAt := len(candidate)
	for _, pattern := range []*regexp.Regexp{btnSxxExxCutPattern, btnSeasonCutPattern, btnDateCutPattern} {
		if match := pattern.FindStringIndex(candidate); match != nil && match[0] < cutAt {
			cutAt = match[0]
		}
	}
	return strings.TrimSpace(candidate[:cutAt])
}

func btnTitleVariants(value string) map[string]struct{} {
	canonical, aliases := extractAKAAliases(value)
	out := make(map[string]struct{})
	for _, candidate := range append([]string{canonical}, aliases...) {
		if normalized := normalizeBTNTitle(candidate); normalized != "" {
			out[normalized] = struct{}{}
		}
	}
	return out
}

func extractAKAAliases(value string) (string, []string) {
	cleaned := strings.TrimSpace(value)
	if cleaned == "" {
		return "", nil
	}
	match := btnAKAExtractPattern.FindStringSubmatch(cleaned)
	if len(match) < 2 {
		return cleaned, nil
	}
	aliases := make([]string, 0)
	for _, part := range strings.FieldsFunc(match[1], func(r rune) bool {
		switch r {
		case ',', '/', ';':
			return true
		default:
			return false
		}
	}) {
		if alias := strings.TrimSpace(part); alias != "" {
			aliases = append(aliases, alias)
		}
	}
	cleaned = strings.TrimSpace(btnAKAExtractPattern.ReplaceAllString(cleaned, ""))
	return cleaned, aliases
}

func normalizeBTNTitle(value string) string {
	cleaned := strings.TrimSpace(value)
	if cleaned == "" {
		return ""
	}
	cleaned = html.UnescapeString(cleaned)
	cleaned = strings.ReplaceAll(cleaned, "\u2018", "'")
	cleaned = strings.ReplaceAll(cleaned, "\u2019", "'")
	cleaned = strings.ReplaceAll(cleaned, "\u0060", "'")
	cleaned = strings.ReplaceAll(cleaned, "&", " and ")
	cleaned = strings.ToLower(cleaned)
	cleaned = btnNonAlnumPattern.ReplaceAllString(cleaned, " ")
	cleaned = btnSpacePattern.ReplaceAllString(cleaned, " ")
	return strings.TrimSpace(cleaned)
}

func btnClaimWindowExpired(meta api.UploadSubject, graceHours int) (bool, int, float64) {
	airedDate := strings.TrimSpace(meta.TVDBAiredDate)
	thresholdHours := btnClaimWindowBaseHours + graceHours
	if airedDate == "" {
		return false, thresholdHours, 0
	}
	airedDateValue, err := time.Parse("2006-01-02", airedDate)
	if err != nil {
		return false, thresholdHours, 0
	}

	airsTime, hasTime := parseBTNTime(strings.TrimSpace(meta.TVDBAirsTime))
	graceHoursUsed := graceHours
	location := time.UTC
	if hasTime {
		graceHoursUsed = 0
		if tz := strings.TrimSpace(meta.TVDBAirsTimezone); tz != "" {
			if loaded, err := time.LoadLocation(tz); err == nil {
				location = loaded
			} else {
				location = time.Now().Location()
			}
		} else {
			location = time.Now().Location()
		}
	}
	thresholdHours = btnClaimWindowBaseHours + graceHoursUsed

	var airedAt time.Time
	if hasTime {
		airedAt = time.Date(
			airedDateValue.Year(),
			airedDateValue.Month(),
			airedDateValue.Day(),
			airsTime.Hour(),
			airsTime.Minute(),
			airsTime.Second(),
			0,
			location,
		)
	} else {
		airedAt = time.Date(airedDateValue.Year(), airedDateValue.Month(), airedDateValue.Day(), 0, 0, 0, 0, time.UTC)
	}

	hoursSinceAir := time.Since(airedAt.UTC()).Hours()
	return hoursSinceAir > float64(thresholdHours), thresholdHours, hoursSinceAir
}

func btnClaimFailureReason(meta api.UploadSubject, graceHours int) string {
	expired, thresholdHours, hoursSinceAir := btnClaimWindowExpired(meta, graceHours)
	if expired {
		return "BTN claim window has expired"
	}
	if thresholdHours <= 0 {
		return "BTN has an active claim for this release"
	}
	if hoursSinceAir <= 0 {
		return fmt.Sprintf("BTN has an active claim for this release; up to %d hours remain in the claim window", thresholdHours)
	}
	hoursRemaining := max(int(float64(thresholdHours)-hoursSinceAir+0.999999999), 1)
	return fmt.Sprintf(
		"BTN has an active claim for this release; approximately %d hours remain in the %d-hour claim window",
		hoursRemaining,
		thresholdHours,
	)
}

func parseBTNTime(value string) (time.Time, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, false
	}
	if match := btnTime24Pattern.FindStringSubmatch(trimmed); len(match) == 4 {
		hour, _ := strconv.Atoi(match[1])
		minute, _ := strconv.Atoi(match[2])
		second := 0
		if match[3] != "" {
			second, _ = strconv.Atoi(match[3])
		}
		if hour >= 0 && hour <= 23 && minute >= 0 && minute <= 59 && second >= 0 && second <= 59 {
			return time.Date(2000, 1, 1, hour, minute, second, 0, time.UTC), true
		}
	}
	if match := btnTime12Pattern.FindStringSubmatch(trimmed); len(match) == 4 {
		hour, _ := strconv.Atoi(match[1])
		minute, _ := strconv.Atoi(match[2])
		if hour >= 1 && hour <= 12 && minute >= 0 && minute <= 59 {
			hour %= 12
			if strings.EqualFold(match[3], "pm") {
				hour += 12
			}
			return time.Date(2000, 1, 1, hour, minute, 0, 0, time.UTC), true
		}
	}
	return time.Time{}, false
}

func readBTNClaimCache(path string) (btnClaimData, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return btnClaimData{}, fmt.Errorf("metadata: read BTN claimed cache: %w", err)
	}
	var cache btnClaimedShowsCache
	if err := json.Unmarshal(payload, &cache); err != nil {
		return btnClaimData{}, fmt.Errorf("metadata: unmarshal BTN claimed cache: %w", err)
	}
	if cache.Version != 0 && cache.Version != btnClaimedShowsCacheVersion {
		return btnClaimData{}, fmt.Errorf("metadata: unsupported BTN claimed cache version %d", cache.Version)
	}
	if cache.Version == btnClaimedShowsCacheVersion &&
		(cache.SourceURL != btnClaimedShowsURL || cache.PostID != btnClaimedShowsPostID ||
			cache.FetchedAt <= 0 || cache.FetchedAt > time.Now().Unix()) {
		return btnClaimData{}, errors.New("metadata: BTN claimed cache identity is invalid")
	}
	titles := make(map[string]struct{}, len(cache.Titles))
	for _, title := range cache.Titles {
		if normalized := normalizeBTNTitle(title); normalized != "" {
			titles[normalized] = struct{}{}
		}
	}
	records := make([]btnClaimRecord, 0, len(cache.Claims))
	if cache.Version == btnClaimedShowsCacheVersion {
		for _, claim := range cache.Claims {
			claim.Title = strings.TrimSpace(claim.Title)
			claim.Group = normalizeBTNClaimGroup(claim.Group)
			claim.Sites = parseBTNClaimSites(strings.Join(claim.Sites, " "))
			if claim.Title == "" || len(claim.Sites) == 0 {
				continue
			}
			records = append(records, claim)
		}
	}
	return btnClaimData{
		Records:      records,
		LegacyTitles: titles,
		FetchedAt:    cache.FetchedAt,
	}, nil
}

func writeBTNClaimedCache(path string, titles map[string]struct{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("metadata: create BTN claimed cache dir: %w", err)
	}
	serializedTitles := make([]string, 0, len(titles))
	for title := range titles {
		serializedTitles = append(serializedTitles, title)
	}
	sort.Strings(serializedTitles)
	cache := btnClaimedShowsCache{
		FetchedAt: time.Now().Unix(),
		SourceURL: btnClaimedShowsURL,
		PostID:    btnClaimedShowsPostID,
		Titles:    serializedTitles,
	}
	encoded, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("metadata: marshal BTN claimed cache: %w", err)
	}
	return writeBTNClaimCacheFile(path, encoded)
}

func writeBTNClaimCache(path string, records []btnClaimRecord) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("metadata: create BTN claimed cache dir: %w", err)
	}
	records = append([]btnClaimRecord(nil), records...)
	for index := range records {
		records[index].Title = strings.TrimSpace(records[index].Title)
		records[index].Group = normalizeBTNClaimGroup(records[index].Group)
		records[index].Sites = append([]string(nil), records[index].Sites...)
		sort.Strings(records[index].Sites)
	}
	sort.SliceStable(records, func(left, right int) bool {
		if titleOrder := strings.Compare(normalizeBTNTitle(records[left].Title), normalizeBTNTitle(records[right].Title)); titleOrder != 0 {
			return titleOrder < 0
		}
		if groupOrder := strings.Compare(strings.ToLower(records[left].Group), strings.ToLower(records[right].Group)); groupOrder != 0 {
			return groupOrder < 0
		}
		return strings.Join(records[left].Sites, "|") < strings.Join(records[right].Sites, "|")
	})
	cache := btnClaimedShowsCache{
		Version:   btnClaimedShowsCacheVersion,
		FetchedAt: time.Now().Unix(),
		SourceURL: btnClaimedShowsURL,
		PostID:    btnClaimedShowsPostID,
		Claims:    records,
		Titles:    sortedClaimTitles(btnClaimData{Records: records}),
	}
	encoded, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("metadata: marshal BTN claimed cache: %w", err)
	}
	return writeBTNClaimCacheFile(path, encoded)
}

func writeBTNClaimCacheFile(path string, encoded []byte) error {
	dir := filepath.Dir(path)
	tmpFile, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("metadata: create temporary BTN claimed cache: %w", err)
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
		return fmt.Errorf("metadata: chmod temporary BTN claimed cache: %w", err)
	}
	if _, err := tmpFile.Write(encoded); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("metadata: write temporary BTN claimed cache: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("metadata: sync temporary BTN claimed cache: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("metadata: close temporary BTN claimed cache: %w", err)
	}
	if err := replaceBTNClaimCacheFile(tmpPath, path); err != nil {
		return err
	}
	removeTemp = false
	return nil
}

func replaceBTNClaimCacheFile(tmpPath, path string) error {
	if err := os.Rename(tmpPath, path); err == nil {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("metadata: stat BTN claimed cache: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("metadata: BTN claimed cache path is a directory: %s", path)
	}
	backup, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".backup-*")
	if err != nil {
		return fmt.Errorf("metadata: reserve BTN claimed cache backup: %w", err)
	}
	backupPath := backup.Name()
	if err := backup.Close(); err != nil {
		_ = os.Remove(backupPath)
		return fmt.Errorf("metadata: close BTN claimed cache backup: %w", err)
	}
	if err := os.Remove(backupPath); err != nil {
		return fmt.Errorf("metadata: release BTN claimed cache backup: %w", err)
	}
	if err := os.Rename(path, backupPath); err != nil {
		return fmt.Errorf("metadata: back up BTN claimed cache: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		if restoreErr := os.Rename(backupPath, path); restoreErr != nil {
			return errors.Join(err, fmt.Errorf("metadata: restore BTN claimed cache: %w", restoreErr))
		}
		return fmt.Errorf("metadata: replace BTN claimed cache: %w", err)
	}
	_ = os.Remove(backupPath)
	return nil
}

func sortedClaimTitles(claims btnClaimData) []string {
	titles := claimDataTitles(claims)
	result := make([]string, 0, len(titles))
	for title := range titles {
		result = append(result, title)
	}
	sort.Strings(result)
	return result
}

func parseOptionalInt(value any) int {
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

func mirrorBTNCookiesForClaimedThread(client *http.Client) {
	if client == nil || client.Jar == nil {
		return
	}

	backupURL, backupErr := url.Parse("https://backup.landof.tv/")
	broadcastURL, broadcastErr := url.Parse("https://broadcasthe.net/")
	if backupErr != nil || broadcastErr != nil {
		return
	}

	backupCookies := client.Jar.Cookies(backupURL)
	if len(backupCookies) == 0 {
		return
	}

	mirrored := make([]*http.Cookie, 0, len(backupCookies)*2)
	for _, cookie := range backupCookies {
		if cookie == nil || strings.TrimSpace(cookie.Name) == "" {
			continue
		}
		copied := *cookie // #nosec G124 -- Outbound tracker jar cookie mirrors backup BTN session attributes.
		copied.Domain = "broadcasthe.net"
		if copied.Path == "" {
			copied.Path = "/"
		}
		// #nosec G124 -- Outbound tracker jar cookie mirrors backup BTN session attributes.
		mirrored = append(mirrored, &copied)

		dotted := copied // #nosec G124 -- Outbound tracker jar cookie mirrors backup BTN session attributes.
		dotted.Domain = ".broadcasthe.net"
		// #nosec G124 -- Outbound tracker jar cookie mirrors backup BTN session attributes.
		mirrored = append(mirrored, &dotted)
	}
	if len(mirrored) == 0 {
		return
	}
	client.Jar.SetCookies(broadcastURL, mirrored)
}

// FailureReason describes an active BTN claim using the configured grace
// period and the remaining or expired claim-window state.
func (s *claimChecker) FailureReason(meta api.UploadSubject) string {
	return btnClaimFailureReason(meta, s.btnClaimWindowGraceHours())
}

func btnClaimsPath(dbPath string) (string, error) {
	path, err := db.FileInSubdir(dbPath, "cache", filepath.Join("banned", "BTN_claimed_releases.json"))
	if err != nil {
		return "", fmt.Errorf("metadata: resolve tracker claims path: %w", err)
	}
	return path, nil
}

func trackerConfigFor(cfg config.Config, tracker string) (config.TrackerConfig, bool) {
	for name, entry := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(tracker)) {
			return entry, true
		}
	}
	return config.TrackerConfig{}, false
}

func btnIsTVCategory(meta api.UploadSubject) bool {
	return meta.SeasonInt > 0 || meta.EpisodeInt > 0 || meta.Release.Season > 0 || meta.Release.Episode > 0 ||
		meta.TVPack || strings.TrimSpace(meta.DailyEpisodeDate) != ""
}

// NormalizeClaimTitle exposes BTN's claim-title normalization for contract tests.
func NormalizeClaimTitle(value string) string { return normalizeBTNTitle(value) }

// ExtractClaimedShows exposes BTN's claimed-thread parser for contract tests.
func ExtractClaimedShows(value string) map[string]struct{} { return extractBTNClaimedShows(value) }

// MirrorCookiesForClaimedThread exposes BTN's backup-domain mirroring behavior for contract tests.
func MirrorCookiesForClaimedThread(client *http.Client) { mirrorBTNCookiesForClaimedThread(client) }

// ClaimWindowExpired exposes BTN's claim-window calculation for contract tests.
func ClaimWindowExpired(meta api.UploadSubject, graceHours int) (bool, int, float64) {
	return btnClaimWindowExpired(meta, graceHours)
}

// LoadClaimedTitles exposes BTN cache/fetch behavior for contract tests.
func LoadClaimedTitles(ctx context.Context, cfg config.Config, logger api.Logger, cachePath string, cacheTTL time.Duration) (map[string]struct{}, error) {
	return (&claimChecker{
		cfg:    cfg,
		logger: logger,
	}).loadBTNClaimedTitles(ctx, cachePath, cacheTTL)
}

// FetchClaimedTitles exposes BTN remote claim retrieval for contract tests.
func FetchClaimedTitles(ctx context.Context, cfg config.Config, logger api.Logger) (map[string]struct{}, error) {
	return (&claimChecker{
		cfg:    cfg,
		logger: logger,
	}).fetchBTNClaimedTitles(ctx)
}

func ioReadAllLimit(resp *http.Response, maxBytes int64) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, errors.New("nil response body")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return nil, fmt.Errorf("read limited response body: %w", err)
	}
	return body, nil
}
