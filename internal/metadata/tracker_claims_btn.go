// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // TOTP interoperability requires SHA-1.
	"encoding/base32"
	"encoding/binary"
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
	"sort"
	"strconv"
	"strings"
	"time"

	htmlnode "golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	btnSiteBaseURL             = "https://broadcasthe.net"
	btnBackupBaseURL           = "https://backup.landof.tv"
	btnUserPath                = "/user.php"
	btnLoginPath               = "/login.php"
	btnClaimedShowsURL         = "https://broadcasthe.net/forums.php?action=viewthread&threadid=30793"
	btnClaimedShowsPostID      = "post1405482"
	btnClaimedShowsCacheTTL    = 48 * time.Hour
	btnClaimWindowBaseHours    = 48
	btnClaimWindowDefaultGrace = 24
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
	FetchedAt int64    `json:"fetched_at"`
	SourceURL string   `json:"source_url"`
	PostID    string   `json:"post_id"`
	Titles    []string `json:"titles"`
}

type btnTrackerClaimProvider struct{}

func (btnTrackerClaimProvider) cachePath(dbPath string, tracker string) (string, error) {
	return trackerClaimsPath(dbPath, tracker)
}

func (btnTrackerClaimProvider) cacheTTL() time.Duration {
	return btnClaimedShowsCacheTTL
}

func (p btnTrackerClaimProvider) hasClaim(ctx context.Context, s *Service, tracker string, meta api.PreparedMetadata) (bool, error) {
	if !isTVCategory(meta) {
		if s.logger != nil {
			s.logger.Debugf("metadata: BTN claims skipped for non-TV content")
		}
		return false, nil
	}

	cachePath, err := p.cachePath(s.cfg.MainSettings.DBPath, tracker)
	if err != nil {
		return false, fmt.Errorf("metadata: BTN claims cache path: %w", err)
	}

	claimedTitles, err := s.loadBTNClaimedTitles(ctx, cachePath, p.cacheTTL())
	if err != nil {
		if s.logger != nil {
			s.logger.Warnf("metadata: BTN claim list unavailable: %v", err)
		}
		return false, nil
	}
	if len(claimedTitles) == 0 {
		if s.logger != nil {
			s.logger.Debugf("metadata: BTN claims loaded 0 titles")
		}
		return false, nil
	}
	if s.logger != nil {
		s.logger.Debugf("metadata: BTN claims loaded %d titles", len(claimedTitles))
	}

	matched, matchedTitle := matchBTNClaimedTitle(meta, claimedTitles)
	if !matched {
		if s.logger != nil {
			s.logger.Debugf("metadata: BTN claims found no title match for release=%q", meta.ReleaseName)
		}
		return false, nil
	}

	graceHours := s.btnClaimWindowGraceHours()
	expired, thresholdHours, hoursSinceAir := btnClaimWindowExpired(meta, graceHours)
	if expired {
		if s.logger != nil {
			s.logger.Debugf("metadata: BTN claim window expired for %q (hours_since_air=%.2f threshold=%d)", matchedTitle, hoursSinceAir, thresholdHours)
		}
		return false, nil
	}

	if s.logger != nil {
		s.logger.Warnf("metadata: BTN claim match found title=%q threshold_hours=%d cache_ttl=%s", matchedTitle, thresholdHours, p.cacheTTL())
	}
	return true, nil
}

func (s *Service) btnClaimWindowGraceHours() int {
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

func (s *Service) loadBTNClaimedTitles(ctx context.Context, cachePath string, cacheTTL time.Duration) (map[string]struct{}, error) {
	cached, fetchedAt, cacheErr := readBTNClaimedCache(cachePath)
	if cacheErr != nil {
		if s.logger != nil && !errors.Is(cacheErr, os.ErrNotExist) {
			s.logger.Warnf("metadata: BTN claims cache read failed path=%s err=%v", cachePath, cacheErr)
		}
	} else if s.logger != nil {
		s.logger.Debugf("metadata: BTN claims cache loaded path=%s titles=%d fetched_at=%d", cachePath, len(cached), fetchedAt)
	}
	if len(cached) > 0 && time.Since(time.Unix(fetchedAt, 0)) < cacheTTL {
		if s.logger != nil {
			s.logger.Debugf("metadata: BTN claims cache hit path=%s age=%s ttl=%s", cachePath, time.Since(time.Unix(fetchedAt, 0)).Round(time.Second), cacheTTL)
		}
		return cached, nil
	}
	if s.logger != nil {
		if len(cached) == 0 {
			s.logger.Debugf("metadata: BTN claims cache miss path=%s", cachePath)
		} else {
			s.logger.Debugf("metadata: BTN claims cache stale path=%s age=%s ttl=%s", cachePath, time.Since(time.Unix(fetchedAt, 0)).Round(time.Second), cacheTTL)
		}
	}

	fresh, fetchErr := s.fetchBTNClaimedTitles(ctx)
	if fetchErr == nil && len(fresh) > 0 {
		if err := writeBTNClaimedCache(cachePath, fresh); err != nil && s.logger != nil {
			s.logger.Warnf("metadata: BTN claims cache write failed: %v", err)
		} else if s.logger != nil {
			s.logger.Debugf("metadata: BTN claims cache saved path=%s titles=%d", cachePath, len(fresh))
		}
		return fresh, nil
	}
	if fetchErr == nil && len(fresh) == 0 && s.logger != nil {
		s.logger.Warnf("metadata: BTN claims fetch succeeded but returned no titles")
	}
	if fetchErr != nil && s.logger != nil {
		s.logger.Warnf("metadata: BTN claims fetch failed; falling back to cache: %v", fetchErr)
	}
	if len(cached) > 0 {
		if s.logger != nil {
			s.logger.Debugf("metadata: BTN claims using cached titles after fetch failure path=%s titles=%d", cachePath, len(cached))
		}
		return cached, nil
	}
	return nil, fetchErr
}

func (s *Service) fetchBTNClaimedTitles(ctx context.Context) (map[string]struct{}, error) {
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
	} else if s.logger != nil {
		s.logger.Debugf("metadata: BTN claims session valid=%t", isLoggedIn)
	}
	if !isLoggedIn {
		if err := s.loginBTNForClaims(ctx, client); err != nil {
			if s.logger != nil {
				s.logger.Warnf("metadata: BTN claims login failed: %v", err)
			}
			return nil, err
		} else if s.logger != nil {
			s.logger.Debugf("metadata: BTN claims login completed")
		}
	} else if s.logger != nil {
		s.logger.Debugf("metadata: BTN claims login skipped; existing session is valid")
	}
	if !isLoggedIn && s.logger != nil {
		s.logger.Debugf("metadata: BTN claims continuing after login attempt")
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
		return nil, fmt.Errorf("metadata: build BTN claims request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		if s.logger != nil {
			s.logger.Warnf("metadata: BTN claims fetch request failed: %v", err)
		}
		return nil, fmt.Errorf("metadata: execute BTN claims request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if s.logger != nil {
			s.logger.Warnf("metadata: BTN claims fetch returned status=%d", resp.StatusCode)
		}
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	if s.logger != nil {
		s.logger.Debugf("metadata: BTN claims fetch returned status=%d", resp.StatusCode)
	}

	payload, err := ioReadAllLimit(resp, 1<<20)
	if err != nil {
		if s.logger != nil {
			s.logger.Warnf("metadata: BTN claims response read failed: %v", err)
		}
		return nil, err
	}
	titles := extractBTNClaimedShows(string(payload))
	if s.logger != nil {
		s.logger.Debugf("metadata: BTN claims parsed %d titles from claimed thread", len(titles))
	}
	return titles, nil
}

func (s *Service) loginBTNForClaims(ctx context.Context, client *http.Client) error {
	entry, ok := trackerConfigFor(s.cfg, "BTN")
	if !ok {
		if s.logger != nil {
			s.logger.Debugf("metadata: BTN claims login skipped; tracker not configured")
		}
		return nil
	}
	baseURL := resolveBTNLoginBaseURL(entry)
	username := strings.TrimSpace(entry.Username)
	password := strings.TrimSpace(entry.Password)
	if username == "" || password == "" {
		if s.logger != nil {
			s.logger.Debugf("metadata: BTN claims login skipped; missing username or password")
		}
		return nil
	}
	if s.logger != nil {
		s.logger.Debugf("metadata: BTN claims login attempting with configured credentials")
	}

	values := map[string]string{
		"username":   username,
		"password":   password,
		"keeplogged": "1",
		"login":      "Log In!",
	}
	if code, err := resolveBTNClaims2FACode(strings.TrimSpace(entry.OTPURI)); err == nil && code != "" {
		values["code"] = code
	} else if err != nil && s.logger != nil {
		s.logger.Warnf("metadata: BTN claims TOTP generation failed: %v", err)
	}
	encoded := encodeForm(values)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+btnLoginPath, strings.NewReader(encoded))
	if err != nil {
		if s.logger != nil {
			s.logger.Warnf("metadata: BTN claims login request build failed: %v", err)
		}
		return fmt.Errorf("metadata: build BTN claims login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "upbrr")
	resp, err := client.Do(req)
	if err != nil {
		if s.logger != nil {
			s.logger.Warnf("metadata: BTN claims login request failed: %v", err)
		}
		return fmt.Errorf("metadata: execute BTN claims login request: %w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		if s.logger != nil {
			s.logger.Warnf("metadata: BTN claims login returned status=%d", resp.StatusCode)
		}
		return fmt.Errorf("login status %d", resp.StatusCode)
	}
	if s.logger != nil {
		s.logger.Debugf("metadata: BTN claims login returned status=%d", resp.StatusCode)
	}
	if valid, err := s.btnClaimsSessionValid(ctx, client); err != nil {
		if s.logger != nil {
			s.logger.Warnf("metadata: BTN claims post-login validation failed: %v", err)
		}
		return err
	} else if !valid {
		return errors.New("BTN login failed to establish a valid session")
	}
	if s.logger != nil {
		s.logger.Debugf("metadata: BTN claims login established a valid session; leaving cookie file unchanged")
	}
	return nil
}

func resolveBTNLoginBaseURL(entry config.TrackerConfig) string {
	if baseURL := sanitizeBTNWebBaseURL(entry.URL); baseURL != "" {
		return baseURL
	}
	if baseURL := sanitizeBTNWebBaseURL(entry.AnnounceURL); baseURL != "" {
		return baseURL
	}
	if baseURL := sanitizeBTNWebBaseURL(entry.MyAnnounceURL); baseURL != "" {
		return baseURL
	}

	return btnSiteBaseURL
}

func (s *Service) loadBTNCookiesForClaims(ctx context.Context, client *http.Client) error {
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

func (s *Service) btnClaimsSessionValid(ctx context.Context, client *http.Client) (bool, error) {
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
	if strings.Contains(strings.ToLower(finalURL), strings.ToLower(btnLoginPath)) {
		return false, nil
	}
	return resp.StatusCode >= 200 && resp.StatusCode < 400, nil
}

func resolveBTNClaims2FACode(otpURI string) (string, error) {
	trimmed := strings.TrimSpace(otpURI)
	if trimmed == "" {
		return "", errors.New("otp_uri not configured")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("metadata: parse BTN claims otp_uri: %w", err)
	}
	secret := strings.TrimSpace(parsed.Query().Get("secret"))
	if secret == "" {
		return "", errors.New("otp_uri missing secret")
	}
	period := 30
	if value := strings.TrimSpace(parsed.Query().Get("period")); value != "" {
		if parsedValue, parseErr := strconv.Atoi(value); parseErr == nil && parsedValue > 0 {
			period = parsedValue
		}
	}
	decoder := base32.StdEncoding.WithPadding(base32.NoPadding)
	secretBytes, err := decoder.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", fmt.Errorf("metadata: decode BTN otp secret: %w", err)
	}
	counterTime := time.Now().Unix() / int64(period)
	if counterTime < 0 {
		return "", errors.New("totp counter before unix epoch")
	}
	counter := uint64(counterTime)
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)
	mac := hmac.New(sha1.New, secretBytes)
	_, _ = mac.Write(buf)
	hash := mac.Sum(nil)
	offset := hash[len(hash)-1] & 0x0f
	code := (int(hash[offset])&0x7f)<<24 | int(hash[offset+1])<<16 | int(hash[offset+2])<<8 | int(hash[offset+3])
	return fmt.Sprintf("%06d", code%1000000), nil
}

func sanitizeBTNWebBaseURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return ""
	}
	if parsed.Scheme == "" {
		parsed.Scheme = "https"
	}

	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	switch host {
	case "", "landof.tv":
		return ""
	case "broadcasthe.net", "www.broadcasthe.net":
		host = "broadcasthe.net"
	default:
		return ""
	}

	lowerPath := strings.ToLower(strings.TrimSpace(parsed.Path))
	if lowerPath != "" && lowerPath != "/" {
		return ""
	}

	parsed.Host = host
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/")
}

func extractBTNClaimedShows(rawHTML string) map[string]struct{} {
	scopedHTML := extractBTNClaimedPostHTML(rawHTML)
	normalized := btnLineBreakPattern.ReplaceAllString(scopedHTML, "\n")
	normalized = btnTagPattern.ReplaceAllString(normalized, "")
	normalized = html.UnescapeString(normalized)

	lines := strings.Split(normalized, "\n")
	inCurrentShows := false
	out := make(map[string]struct{})
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
		if !strings.Contains(trimmed, "--") || !strings.Contains(strings.ToUpper(trimmed), "BTN") {
			continue
		}
		title := strings.TrimSpace(strings.SplitN(trimmed, "--", 2)[0])
		if strings.EqualFold(title, "show") || title == "" {
			continue
		}
		for variant := range btnTitleVariants(title) {
			out[variant] = struct{}{}
		}
	}
	return out
}

func extractBTNClaimedPostHTML(rawHTML string) string {
	if strings.TrimSpace(rawHTML) == "" {
		return rawHTML
	}

	doc, err := htmlnode.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return rawHTML
	}

	scope := findBTNHTMLNode(doc, func(node *htmlnode.Node) bool {
		return node.Type == htmlnode.ElementNode && node.Data == "table" && btnHTMLNodeID(node) == btnClaimedShowsPostID
	})
	if scope == nil {
		return rawHTML
	}

	content := findBTNHTMLNode(scope, func(node *htmlnode.Node) bool {
		if node.Type != htmlnode.ElementNode || node.Data != "div" {
			return false
		}
		if btnHTMLNodeID(node) == "content1405482" {
			return true
		}
		return btnHTMLNodeHasClass(node, "postcontent")
	})
	if content != nil {
		if rendered := renderBTNHTMLChildren(content); rendered != "" {
			return rendered
		}
	}

	if rendered := renderBTNHTMLChildren(scope); rendered != "" {
		return rendered
	}

	return rawHTML
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

func matchBTNClaimedTitle(meta api.PreparedMetadata, claimed map[string]struct{}) (bool, string) {
	for candidate := range btnCandidateTitles(meta) {
		if _, ok := claimed[candidate]; ok {
			return true, candidate
		}
	}
	return false, ""
}

func btnCandidateTitles(meta api.PreparedMetadata) map[string]struct{} {
	candidates := []string{
		meta.ReleaseName,
		meta.ReleaseNameNoTag,
		meta.Filename,
		pathutil.Base(meta.SourcePath),
	}
	if meta.ExternalMetadata.TVDB != nil {
		candidates = append(candidates, meta.ExternalMetadata.TVDB.Name, meta.ExternalMetadata.TVDB.NameEnglish)
	}
	if meta.ExternalMetadata.TMDB != nil {
		candidates = append(candidates, meta.ExternalMetadata.TMDB.Title, meta.ExternalMetadata.TMDB.OriginalTitle)
	}
	if meta.ExternalMetadata.TVmaze != nil {
		candidates = append(candidates, meta.ExternalMetadata.TVmaze.Name)
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

func btnClaimWindowExpired(meta api.PreparedMetadata, graceHours int) (bool, int, float64) {
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
		airedAt = time.Date(airedDateValue.Year(), airedDateValue.Month(), airedDateValue.Day(), airsTime.Hour(), airsTime.Minute(), airsTime.Second(), 0, location)
	} else {
		airedAt = time.Date(airedDateValue.Year(), airedDateValue.Month(), airedDateValue.Day(), 0, 0, 0, 0, time.UTC)
	}

	hoursSinceAir := time.Since(airedAt.UTC()).Hours()
	return hoursSinceAir > float64(thresholdHours), thresholdHours, hoursSinceAir
}

func btnClaimFailureReason(meta api.PreparedMetadata, graceHours int) string {
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
	return fmt.Sprintf("BTN has an active claim for this release; approximately %d hours remain in the %d-hour claim window", hoursRemaining, thresholdHours)
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

func readBTNClaimedCache(path string) (map[string]struct{}, int64, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, fmt.Errorf("metadata: read BTN claimed cache: %w", err)
	}
	var cache btnClaimedShowsCache
	if err := json.Unmarshal(payload, &cache); err != nil {
		return nil, 0, fmt.Errorf("metadata: unmarshal BTN claimed cache: %w", err)
	}
	titles := make(map[string]struct{}, len(cache.Titles))
	for _, title := range cache.Titles {
		if normalized := normalizeBTNTitle(title); normalized != "" {
			titles[normalized] = struct{}{}
		}
	}
	return titles, cache.FetchedAt, nil
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
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return fmt.Errorf("metadata: write BTN claimed cache: %w", err)
	}
	return nil
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

func encodeForm(values map[string]string) string {
	encoded := url.Values{}
	for key, value := range values {
		encoded.Set(key, value)
	}
	return encoded.Encode()
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
