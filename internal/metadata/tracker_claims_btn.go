// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	btnClaimedShowsURL         = "https://broadcasthe.net/forums.php?action=viewthread&threadid=30793"
	btnClaimedShowsPostID      = "post1405482"
	btnClaimedShowsCacheTTL    = 24 * time.Hour
	btnClaimWindowBaseHours    = 48
	btnClaimWindowDefaultGrace = 24
)

var (
	btnLineBreakPattern       = regexp.MustCompile(`(?i)<br\s*/?>`)
	btnTagPattern             = regexp.MustCompile(`(?s)<[^>]*>`)
	btnAKAExtractPattern      = regexp.MustCompile(`(?i)\(\s*aka\s*:\s*([^\)]*?)\)`)
	btnSxxExxCutPattern       = regexp.MustCompile(`(?i)\bS\d{1,2}E\d{1,3}\b`)
	btnSeasonCutPattern       = regexp.MustCompile(`(?i)\bS\d{1,2}\b`)
	btnDateCutPattern         = regexp.MustCompile(`\b\d{4}[\-\.]\d{2}[\-\.]\d{2}\b`)
	btnNonAlnumPattern        = regexp.MustCompile(`[^a-z0-9]+`)
	btnSpacePattern           = regexp.MustCompile(`\s+`)
	btnTime24Pattern          = regexp.MustCompile(`^(\d{1,2}):(\d{2})(?::(\d{2}))?$`)
	btnTime12Pattern          = regexp.MustCompile(`^(\d{1,2}):(\d{2})\s*([AaPp][Mm])$`)
	btnClaimedPostTableRegex  = regexp.MustCompile(`(?is)<table[^>]*id=["']` + btnClaimedShowsPostID + `["'][^>]*>(.*?)</table>`)
	btnClaimedContentDivRegex = regexp.MustCompile(`(?is)<div[^>]*(?:id=["']content1405482["'][^>]*|class=["'][^"']*\bpostcontent\b[^"']*["'][^>]*)>(.*?)</div>`)
)

type btnClaimedShowsCache struct {
	FetchedAt int64    `json:"fetched_at"`
	SourceURL string   `json:"source_url"`
	PostID    string   `json:"post_id"`
	Titles    []string `json:"titles"`
}

func (s *Service) hasBTNClaim(ctx context.Context, meta api.PreparedMetadata) (bool, error) {
	if !isTVCategory(meta) {
		return false, nil
	}

	claimedTitles, err := s.loadBTNClaimedTitles(ctx)
	if err != nil {
		if s.logger != nil {
			s.logger.Warnf("metadata: BTN claim list unavailable: %v", err)
		}
		return false, nil
	}
	if len(claimedTitles) == 0 {
		return false, nil
	}

	matched, matchedTitle := matchBTNClaimedTitle(meta, claimedTitles)
	if !matched {
		return false, nil
	}

	graceHours := s.btnClaimWindowGraceHours()
	expired, thresholdHours, hoursSinceAir := btnClaimWindowExpired(meta, graceHours)
	if expired {
		if s.logger != nil {
			s.logger.Infof("metadata: BTN claim window expired for %q (hours_since_air=%.2f threshold=%d)", matchedTitle, hoursSinceAir, thresholdHours)
		}
		return false, nil
	}

	if s.logger != nil {
		s.logger.Warnf("metadata: BTN claimed show blocked title=%q threshold_hours=%d", matchedTitle, thresholdHours)
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

func (s *Service) loadBTNClaimedTitles(ctx context.Context) (map[string]struct{}, error) {
	cachePath, err := btnClaimedShowsCachePath(s.cfg.MainSettings.DBPath)
	if err != nil {
		return nil, fmt.Errorf("metadata: BTN claims cache path: %w", err)
	}

	cached, fetchedAt, _ := readBTNClaimedCache(cachePath)
	if len(cached) > 0 && time.Since(time.Unix(fetchedAt, 0)) < btnClaimedShowsCacheTTL {
		return cached, nil
	}

	fresh, fetchErr := s.fetchBTNClaimedTitles(ctx)
	if fetchErr == nil && len(fresh) > 0 {
		if err := writeBTNClaimedCache(cachePath, fresh); err != nil && s.logger != nil {
			s.logger.Warnf("metadata: BTN claims cache write failed: %v", err)
		}
		return fresh, nil
	}
	if fetchErr != nil && s.logger != nil {
		s.logger.Warnf("metadata: BTN claims fetch failed; falling back to cache: %v", fetchErr)
	}
	if len(cached) > 0 {
		return cached, nil
	}
	return nil, fetchErr
}

func (s *Service) fetchBTNClaimedTitles(ctx context.Context) (map[string]struct{}, error) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Timeout: 30 * time.Second, Jar: jar}

	_ = s.loginBTNForClaims(ctx, client)
	mirrorBTNCookiesForClaimedThread(client)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, btnClaimedShowsURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	payload, err := ioReadAllLimit(resp, 1<<20)
	if err != nil {
		return nil, err
	}
	return extractBTNClaimedShows(string(payload)), nil
}

func (s *Service) loginBTNForClaims(ctx context.Context, client *http.Client) error {
	entry, ok := trackerConfigFor(s.cfg, "BTN")
	if !ok {
		return nil
	}
	baseURL := strings.TrimRight(strings.TrimSpace(entry.URL), "/")
	if baseURL == "" {
		baseURL = "https://backup.landof.tv"
	}
	username := strings.TrimSpace(entry.Username)
	password := strings.TrimSpace(entry.Password)
	if username == "" || password == "" {
		return nil
	}

	values := map[string]string{
		"username":   username,
		"password":   password,
		"keeplogged": "1",
	}
	encoded := encodeForm(values)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/login.php", strings.NewReader(encoded))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "upbrr")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("login status %d", resp.StatusCode)
	}
	return nil
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
	if tableMatch := btnClaimedPostTableRegex.FindStringSubmatch(rawHTML); len(tableMatch) > 1 {
		scoped := tableMatch[1]
		if contentMatch := btnClaimedContentDivRegex.FindStringSubmatch(scoped); len(contentMatch) > 1 {
			return contentMatch[1]
		}
		return scoped
	}
	return rawHTML
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

func btnClaimedShowsCachePath(dbPath string) (string, error) {
	return db.FileInSubdir(dbPath, "cache", filepath.Join("banned", "BTN_claimed_shows.json"))
}

func readBTNClaimedCache(path string) (map[string]struct{}, int64, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	var cache btnClaimedShowsCache
	if err := json.Unmarshal(payload, &cache); err != nil {
		return nil, 0, err
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
		return err
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
		return err
	}
	return os.WriteFile(path, encoded, 0o600)
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
		copied := *cookie
		copied.Domain = "broadcasthe.net"
		if copied.Path == "" {
			copied.Path = "/"
		}
		mirrored = append(mirrored, &copied)

		dotted := copied
		dotted.Domain = ".broadcasthe.net"
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
	return io.ReadAll(io.LimitReader(resp.Body, maxBytes))
}
