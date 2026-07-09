// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp/syntax"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers/unit3dmeta"
	"github.com/autobrr/upbrr/pkg/api"
)

const bannedGroupsCacheTTL = 24 * time.Hour
const maxBannedGroupsResponseBytes int64 = 4 * 1024 * 1024
const maxDynamicBannedGroupPages = 1000
const maxTRaSHReleaseGroupExpansions = 512

var errBannedGroupsRefreshSkipped = errors.New("banned groups refresh skipped")

// trashGuideBannedGroupsURL is the TRaSH Guides low-quality custom format used
// as LUME's dynamic banned-group source.
var trashGuideBannedGroupsURL = "https://raw.githubusercontent.com/TRaSH-Guides/Guides/refs/heads/master/docs/json/radarr/cf/lq.json"

// BannedGroupChecker checks tracker release groups against built-in and cached
// banned-group lists.
type BannedGroupChecker struct {
	basePath string
	mu       sync.Mutex
	cache    map[string]map[string]struct{}
}

type bannedGroupsFile struct {
	LastUpdated  string            `json:"last_updated,omitempty"`
	BannedGroups string            `json:"banned_groups"`
	RawData      []json.RawMessage `json:"raw_data,omitempty"`
}

// bannedGroupsPage matches the paginated Unit3D-style blacklist response used
// by AITHER/LST and keeps raw records so the cache can preserve source data.
type bannedGroupsPage struct {
	Data []json.RawMessage `json:"data"`
	Meta struct {
		NextCursor string `json:"next_cursor"`
	} `json:"meta"`
}

// trashGuideCustomFormat contains the subset of TRaSH custom-format JSON needed
// to extract ReleaseGroupSpecification entries.
type trashGuideCustomFormat struct {
	Specifications []trashGuideSpecification `json:"specifications"`
}

// trashGuideSpecification contains one TRaSH custom-format specification. Only
// ReleaseGroupSpecification values contribute banned-group names.
type trashGuideSpecification struct {
	Implementation string `json:"implementation"`
	Fields         struct {
		Value string `json:"value"`
	} `json:"fields"`
}

// NewBannedGroupChecker creates a checker rooted at the banned-group cache
// directory derived from dbPath.
func NewBannedGroupChecker(dbPath string) *BannedGroupChecker {
	basePath, err := db.Subdir(dbPath, "cache")
	if err != nil {
		return nil
	}
	basePath = filepath.Join(basePath, "banned")
	return &BannedGroupChecker{basePath: basePath, cache: make(map[string]map[string]struct{})}
}

// IsBanned reports whether group is banned for tracker after normalizing both
// values; built-in matches still return true when the cache file cannot be read.
func (c *BannedGroupChecker) IsBanned(tracker, group string) (bool, error) {
	if c == nil {
		return false, nil
	}
	tracker = strings.ToUpper(strings.TrimSpace(tracker))
	group = strings.ToLower(strings.TrimSpace(group))
	if tracker == "" || group == "" {
		return false, nil
	}

	groups, err := c.load(tracker)
	if err != nil {
		if _, found := groups[group]; found {
			return true, nil
		}
		return false, err
	}
	_, found := groups[group]
	return found, nil
}

// RefreshDynamic refreshes API-backed banned-group caches for trackers that
// publish those lists. Fresh cache files are treated as authoritative for the
// TTL and invalidate any older in-memory lookup. Fetch failures keep any
// existing cache in use; write failures are returned because they prevent the
// refreshed list from applying. Cancellation is checked before fetches and again
// before durable writes so canceled refreshes do not replace cache files.
func (c *BannedGroupChecker) RefreshDynamic(ctx context.Context, cfg config.Config, trackerNames []string, logger api.Logger) error {
	return c.refreshDynamic(ctx, cfg, trackerNames, logger, fetchDynamicBannedGroups)
}

// dynamicBannedGroupsFetcher lets tests place cancellation exactly between a
// successful fetch and durable cache mutation.
type dynamicBannedGroupsFetcher func(context.Context, config.Config, string) ([]string, []json.RawMessage, error)

func (c *BannedGroupChecker) refreshDynamic(
	ctx context.Context,
	cfg config.Config,
	trackerNames []string,
	logger api.Logger,
	fetch dynamicBannedGroupsFetcher,
) error {
	if c == nil || len(trackerNames) == 0 {
		return nil
	}
	for _, tracker := range uniqueBannedGroupTrackers(trackerNames) {
		if !usesDynamicBannedGroups(tracker) {
			continue
		}
		if ctx.Err() != nil {
			return fmt.Errorf("trackers: refresh banned groups canceled: %w", ctx.Err())
		}
		cachePath := c.cachePath(tracker)
		if bannedGroupsCacheFresh(cachePath, bannedGroupsCacheTTL) {
			c.invalidate(tracker)
			if logger != nil {
				logger.Debugf("trackers: banned groups refresh skipped tracker=%s decision=cache_fresh", tracker)
			}
			continue
		}

		groups, raw, err := fetch(ctx, cfg, tracker)
		if errors.Is(err, errBannedGroupsRefreshSkipped) {
			if logger != nil {
				logger.Debugf("trackers: banned groups refresh skipped tracker=%s decision=missing_config", tracker)
			}
			continue
		}
		if err != nil {
			if logger != nil {
				logger.Warnf("trackers: banned groups refresh failed tracker=%s decision=cache_fallback err=%s", tracker, redaction.RedactValue(err.Error(), nil))
			}
			continue
		}
		if ctx.Err() != nil {
			return fmt.Errorf("trackers: refresh banned groups canceled: %w", ctx.Err())
		}
		if err := writeBannedGroupsCache(cachePath, groups, raw); err != nil {
			return fmt.Errorf("trackers: write banned groups cache %s: %w", tracker, err)
		}
		c.invalidate(tracker)
		if logger != nil {
			logger.Debugf("trackers: banned groups cache refreshed tracker=%s groups=%d", tracker, len(groups))
		}
	}
	return nil
}

func (c *BannedGroupChecker) load(tracker string) (map[string]struct{}, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if cached, ok := c.cache[tracker]; ok {
		return cached, nil
	}

	groups := map[string]struct{}{}
	if builtin := builtinBannedGroups[tracker]; len(builtin) > 0 {
		for _, value := range builtin {
			cleaned := strings.ToLower(strings.TrimSpace(value))
			if cleaned != "" {
				groups[cleaned] = struct{}{}
			}
		}
	}

	filePath := c.cachePath(tracker)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			c.cache[tracker] = groups
			return groups, nil
		}
		return groups, fmt.Errorf("trackers: read banned groups: %w", err)
	}

	var payload bannedGroupsFile
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("trackers: unmarshal banned groups: %w", err)
	}

	for value := range strings.SplitSeq(payload.BannedGroups, ",") {
		cleaned := strings.ToLower(strings.TrimSpace(value))
		if cleaned == "" {
			continue
		}
		groups[cleaned] = struct{}{}
	}

	c.cache[tracker] = groups
	return groups, nil
}

func (c *BannedGroupChecker) cachePath(tracker string) string {
	return filepath.Join(c.basePath, strings.ToUpper(strings.TrimSpace(tracker))+"_banned_groups.json")
}

// invalidate removes a tracker from the in-memory lookup cache after a refresh
// observes a fresher cache file or writes a new banned-group file.
func (c *BannedGroupChecker) invalidate(tracker string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cache, strings.ToUpper(strings.TrimSpace(tracker)))
}

// uniqueBannedGroupTrackers returns stable uppercase tracker names for one
// refresh pass.
func uniqueBannedGroupTrackers(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		tracker := strings.ToUpper(strings.TrimSpace(value))
		if tracker == "" {
			continue
		}
		if _, ok := seen[tracker]; ok {
			continue
		}
		seen[tracker] = struct{}{}
		out = append(out, tracker)
	}
	return out
}

// usesDynamicBannedGroups reports whether upbrr knows how to fetch a tracker
// banned-group API instead of relying only on built-ins or a manual cache file.
func usesDynamicBannedGroups(tracker string) bool {
	switch strings.ToUpper(strings.TrimSpace(tracker)) {
	case "AITHER", "LST", "LUME", "SPD":
		return true
	default:
		return false
	}
}

// bannedGroupsCacheFresh reports whether a cache file can satisfy a refresh
// request without hitting the tracker API.
func bannedGroupsCacheFresh(path string, cacheTTL time.Duration) bool {
	info, err := os.Stat(path)
	return err == nil && time.Since(info.ModTime()) < cacheTTL
}

// writeBannedGroupsCache persists the compact CSV consumed by IsBanned while
// retaining raw records for diagnostics and future parser changes. The cache is
// written to a synced temp file and then replaced as a single filesystem update
// where the platform supports it.
func writeBannedGroupsCache(path string, groups []string, raw []json.RawMessage) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create banned groups cache dir: %w", err)
	}
	groups = normalizedBannedGroupList(groups)
	payload, err := json.MarshalIndent(bannedGroupsFile{
		LastUpdated:  time.Now().UTC().Format("2006-01-02"),
		BannedGroups: strings.Join(groups, ", "),
		RawData:      raw,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal banned groups cache: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp banned groups cache: %w", err)
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
		return fmt.Errorf("chmod temp banned groups cache: %w", err)
	}
	if _, err := tmpFile.Write(payload); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write temp banned groups cache: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("sync temp banned groups cache: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp banned groups cache: %w", err)
	}
	if err := replaceBannedGroupsCacheFile(tmpPath, path); err != nil {
		return err
	}
	removeTemp = false
	return nil
}

// replaceBannedGroupsCacheFile moves a prepared temp file into the cache path.
// If direct rename cannot replace an existing file, the old cache is backed up
// and restored on replacement failure so a failed write does not destroy it.
func replaceBannedGroupsCacheFile(tmpPath, path string) error {
	return replaceBannedGroupsCacheFileWithOps(tmpPath, path, os.Rename, os.Remove)
}

// replaceBannedGroupsCacheFileWithOps runs the cache replacement algorithm with
// caller-provided filesystem operations. Once tmpPath has been renamed into
// path, backup removal is best-effort because the replacement is committed.
func replaceBannedGroupsCacheFileWithOps(tmpPath, path string, rename func(string, string) error, remove func(string) error) error {
	renameErr := rename(tmpPath, path)
	if renameErr == nil {
		return nil
	}

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("rename temp banned groups cache into place: %w", renameErr)
		}
		return fmt.Errorf("stat banned groups cache: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", path)
	}

	backupPath, err := reserveBannedGroupsCacheBackupPath(filepath.Dir(path), filepath.Base(path)+".backup-*")
	if err != nil {
		return err
	}
	if err := rename(path, backupPath); err != nil {
		_ = remove(backupPath)
		return fmt.Errorf("backup banned groups cache: %w", err)
	}
	if err := rename(tmpPath, path); err != nil {
		restoreErr := rename(backupPath, path)
		if restoreErr != nil {
			return errors.Join(err, fmt.Errorf("restore banned groups cache: %w", restoreErr))
		}
		return fmt.Errorf("replace banned groups cache: %w", err)
	}
	_ = remove(backupPath)
	return nil
}

// reserveBannedGroupsCacheBackupPath reserves a unique backup name without
// leaving a placeholder file that would block a later rename.
func reserveBannedGroupsCacheBackupPath(dir, pattern string) (string, error) {
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", fmt.Errorf("reserve banned groups cache backup: %w", err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close banned groups cache backup reservation: %w", err)
	}
	if err := os.Remove(path); err != nil {
		return "", fmt.Errorf("release banned groups cache backup reservation: %w", err)
	}
	return path, nil
}

// normalizedBannedGroupList trims, de-duplicates case-insensitively, and sorts
// tracker group names before writing a stable cache file.
func normalizedBannedGroupList(groups []string) []string {
	seen := make(map[string]struct{}, len(groups))
	out := make([]string, 0, len(groups))
	for _, group := range groups {
		cleaned := strings.TrimSpace(group)
		if cleaned == "" {
			continue
		}
		key := strings.ToLower(cleaned)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, cleaned)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}

// fetchDynamicBannedGroups downloads every page for API-backed trackers, or the
// TRaSH guide source for LUME. Missing endpoint config or API key returns
// errBannedGroupsRefreshSkipped so callers do not overwrite an existing cache
// with an empty file. Pagination is bounded and repeated cursors fail instead
// of looping indefinitely.
func fetchDynamicBannedGroups(ctx context.Context, cfg config.Config, tracker string) ([]string, []json.RawMessage, error) {
	if strings.EqualFold(strings.TrimSpace(tracker), "LUME") {
		return fetchTRaSHGuideBannedGroups(ctx, trashGuideBannedGroupsURL)
	}

	endpoint, ok := bannedGroupsEndpoint(cfg, tracker)
	if !ok {
		return nil, nil, errBannedGroupsRefreshSkipped
	}
	apiKey := strings.TrimSpace(bannedGroupAPIKey(cfg, tracker))
	if apiKey == "" {
		return nil, nil, errBannedGroupsRefreshSkipped
	}

	client := &http.Client{Timeout: 20 * time.Second}
	cursor := ""
	groups := make([]string, 0)
	rawItems := make([]json.RawMessage, 0)
	seenCursors := make(map[string]struct{})

	for page := 1; ; page++ {
		pageGroups, pageRaw, nextCursor, err := fetchDynamicBannedGroupsPage(ctx, client, endpoint, tracker, apiKey, cursor)
		if err != nil {
			return nil, nil, err
		}
		groups = append(groups, pageGroups...)
		rawItems = append(rawItems, pageRaw...)
		cursor = strings.TrimSpace(nextCursor)
		if cursor == "" {
			break
		}
		if page >= maxDynamicBannedGroupPages {
			return nil, nil, fmt.Errorf("fetch banned groups %s: exceeded %d pages", tracker, maxDynamicBannedGroupPages)
		}
		if _, ok := seenCursors[cursor]; ok {
			return nil, nil, fmt.Errorf("fetch banned groups %s: repeated cursor %q", tracker, cursor)
		}
		seenCursors[cursor] = struct{}{}
	}
	return groups, rawItems, nil
}

// fetchTRaSHGuideBannedGroups downloads the TRaSH low-quality custom format and
// extracts release-group specifications into the same cache shape as tracker
// API-backed banned groups.
func fetchTRaSHGuideBannedGroups(ctx context.Context, endpoint string) ([]string, []json.RawMessage, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("build TRaSH banned groups request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch TRaSH banned groups: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, fmt.Errorf("fetch TRaSH banned groups: status %d", resp.StatusCode)
	}

	var payload trashGuideCustomFormat
	if err := decodeBannedGroupsJSON(resp.Body, &payload); err != nil {
		return nil, nil, fmt.Errorf("decode TRaSH banned groups: %w", err)
	}
	return extractTRaSHGuideBannedGroups(payload)
}

// extractTRaSHGuideBannedGroups keeps only ReleaseGroupSpecification values and
// records raw cache items as {"name": "..."} objects.
func extractTRaSHGuideBannedGroups(payload trashGuideCustomFormat) ([]string, []json.RawMessage, error) {
	groups := make([]string, 0)
	for _, spec := range payload.Specifications {
		if spec.Implementation != "ReleaseGroupSpecification" {
			continue
		}
		groups = append(groups, trashGuideReleaseGroupNames(spec.Fields.Value)...)
	}
	groups = normalizedBannedGroupList(groups)
	if len(groups) == 0 {
		return nil, nil, errors.New("TRaSH banned groups contained no release groups")
	}

	raw := make([]json.RawMessage, 0, len(groups))
	for _, group := range groups {
		item, err := json.Marshal(map[string]string{"name": group})
		if err != nil {
			return nil, nil, fmt.Errorf("marshal TRaSH banned group item: %w", err)
		}
		raw = append(raw, item)
	}
	return groups, raw, nil
}

// trashGuideReleaseGroupNames extracts the group token from the regex-like
// ReleaseGroupSpecification field used by TRaSH Guides.
func trashGuideReleaseGroupNames(value string) []string {
	name := strings.TrimSpace(value)
	if name == "" {
		return nil
	}
	if groups, ok := expandTRaSHReleaseGroupPattern(name); ok {
		return normalizedBannedGroupList(groups)
	}

	cleaned := stripTRaSHReleaseGroupPattern(name)
	if cleaned == "" {
		return nil
	}
	return []string{cleaned}
}

func expandTRaSHReleaseGroupPattern(value string) ([]string, bool) {
	re, err := syntax.Parse(value, syntax.Perl)
	if err != nil {
		return nil, false
	}
	groups, ok := expandTRaSHReleaseGroupRegexp(re.Simplify())
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(groups))
	for _, group := range groups {
		cleaned := strings.TrimSpace(group)
		if cleaned != "" {
			out = append(out, cleaned)
		}
	}
	return out, len(out) > 0
}

func expandTRaSHReleaseGroupRegexp(re *syntax.Regexp) ([]string, bool) {
	switch re.Op {
	case syntax.OpNoMatch:
		return nil, false
	case syntax.OpEmptyMatch, syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText, syntax.OpEndText, syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		return []string{""}, true
	case syntax.OpLiteral:
		return []string{string(re.Rune)}, true
	case syntax.OpCharClass:
		return expandTRaSHReleaseGroupCharClass(re.Rune)
	case syntax.OpAnyCharNotNL, syntax.OpAnyChar:
		return []string{"."}, true
	case syntax.OpCapture:
		return expandTRaSHReleaseGroupRegexp(re.Sub[0])
	case syntax.OpAlternate:
		out := make([]string, 0, len(re.Sub))
		for _, sub := range re.Sub {
			groups, ok := expandTRaSHReleaseGroupRegexp(sub)
			if !ok {
				return nil, false
			}
			out = append(out, groups...)
			if len(out) > maxTRaSHReleaseGroupExpansions {
				return nil, false
			}
		}
		return out, true
	case syntax.OpConcat:
		out := []string{""}
		for _, sub := range re.Sub {
			groups, ok := expandTRaSHReleaseGroupRegexp(sub)
			if !ok {
				return nil, false
			}
			out, ok = concatTRaSHReleaseGroupExpansions(out, groups)
			if !ok {
				return nil, false
			}
		}
		return out, true
	case syntax.OpQuest:
		groups, ok := expandTRaSHReleaseGroupRegexp(re.Sub[0])
		if !ok {
			return nil, false
		}
		return append([]string{""}, groups...), true
	case syntax.OpStar, syntax.OpPlus:
		return nil, false
	case syntax.OpRepeat:
		return repeatTRaSHReleaseGroupRegexp(re)
	default:
		return nil, false
	}
}

func expandTRaSHReleaseGroupCharClass(ranges []rune) ([]string, bool) {
	out := make([]string, 0, len(ranges)/2)
	for idx := 0; idx < len(ranges); idx += 2 {
		lo, hi := ranges[idx], ranges[idx+1]
		if hi-lo > 16 {
			return nil, false
		}
		for r := lo; r <= hi; r++ {
			out = append(out, string(r))
			if len(out) > maxTRaSHReleaseGroupExpansions {
				return nil, false
			}
		}
	}
	return out, true
}

func repeatTRaSHReleaseGroupRegexp(re *syntax.Regexp) ([]string, bool) {
	if re.Max < 0 || re.Max > 4 {
		return nil, false
	}
	unit, ok := expandTRaSHReleaseGroupRegexp(re.Sub[0])
	if !ok {
		return nil, false
	}
	out := make([]string, 0)
	for count := re.Min; count <= re.Max; count++ {
		repeated := []string{""}
		for range count {
			var ok bool
			repeated, ok = concatTRaSHReleaseGroupExpansions(repeated, unit)
			if !ok {
				return nil, false
			}
		}
		out = append(out, repeated...)
	}
	return out, len(out) <= maxTRaSHReleaseGroupExpansions
}

func concatTRaSHReleaseGroupExpansions(left, right []string) ([]string, bool) {
	if len(left)*len(right) > maxTRaSHReleaseGroupExpansions {
		return nil, false
	}
	out := make([]string, 0, len(left)*len(right))
	for _, prefix := range left {
		for _, suffix := range right {
			out = append(out, prefix+suffix)
		}
	}
	return out, true
}

// stripTRaSHReleaseGroupPattern removes the regex wrappers that TRaSH uses
// around literal release-group names.
func stripTRaSHReleaseGroupPattern(value string) string {
	cleaned := strings.TrimSpace(value)
	cleaned = strings.ReplaceAll(cleaned, `\b`, "")
	replacer := strings.NewReplacer(`\`, "", "^", "", "$", "", "(", "", ")", "", "[", "", "]", "", "|", "")
	cleaned = replacer.Replace(cleaned)
	return strings.Join(strings.Fields(cleaned), " ")
}

// fetchDynamicBannedGroupsPage fetches and parses one blacklist page, including
// SPD's alternate Authorization header form when bearer auth is rejected.
func fetchDynamicBannedGroupsPage(ctx context.Context, client *http.Client, endpoint string, tracker string, apiKey string, cursor string) ([]string, []json.RawMessage, string, error) {
	var raw json.RawMessage
	var statusCode int
	var err error
	authValues := bannedGroupsAuthValues(tracker, apiKey)
	for idx, authValue := range authValues {
		raw, statusCode, err = doBannedGroupsRequest(ctx, client, endpoint, tracker, authValue, cursor)
		if err != nil {
			return nil, nil, "", err
		}
		if statusCode >= 200 && statusCode < 300 {
			break
		}
		if idx+1 < len(authValues) && (statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden) {
			continue
		}
		return nil, nil, "", fmt.Errorf("fetch banned groups %s: status %d", tracker, statusCode)
	}

	items, nextCursor, err := bannedGroupsPageItems(raw)
	if err != nil {
		return nil, nil, "", err
	}
	groups := make([]string, 0, len(items))
	for _, item := range items {
		if group := bannedGroupName(item); group != "" {
			groups = append(groups, group)
		}
	}
	return groups, items, nextCursor, nil
}

// doBannedGroupsRequest performs one HTTP request and returns only decoded JSON
// for successful responses so error paths never include remote response bodies.
func doBannedGroupsRequest(ctx context.Context, client *http.Client, endpoint string, tracker string, authValue string, cursor string) (json.RawMessage, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build banned groups request %s: %w", tracker, err)
	}
	query := url.Values{}
	query.Set("per_page", "100")
	if strings.TrimSpace(cursor) != "" {
		query.Set("cursor", strings.TrimSpace(cursor))
	}
	req.URL.RawQuery = query.Encode()
	req.Header.Set("Authorization", authValue)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("fetch banned groups %s: %w", tracker, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, nil
	}

	var raw json.RawMessage
	if err := decodeBannedGroupsJSON(resp.Body, &raw); err != nil {
		return nil, 0, fmt.Errorf("decode banned groups %s: %w", tracker, err)
	}
	return raw, resp.StatusCode, nil
}

// decodeBannedGroupsJSON reads a successful banned-group response with a fixed
// upper bound and rejects trailing JSON values before callers persist decoded data.
func decodeBannedGroupsJSON(body io.Reader, dest any) error {
	payload, err := io.ReadAll(io.LimitReader(body, maxBannedGroupsResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read banned groups response: %w", err)
	}
	if int64(len(payload)) > maxBannedGroupsResponseBytes {
		return fmt.Errorf("response body exceeds %d bytes", maxBannedGroupsResponseBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(dest); err != nil {
		return fmt.Errorf("decode banned groups response: %w", err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("decode banned groups response: %w", err)
	}
	return errors.New("decode banned groups response: trailing JSON value")
}

// bannedGroupsAuthValues returns the Authorization header values to try for a
// tracker; SPD's API uses raw API-key auth in existing upbrr code paths.
func bannedGroupsAuthValues(tracker string, apiKey string) []string {
	values := []string{"Bearer " + apiKey}
	if strings.EqualFold(strings.TrimSpace(tracker), "SPD") {
		values = append(values, apiKey)
	}
	return values
}

// bannedGroupsPageItems normalizes accepted API response shapes into raw item
// records plus the next pagination cursor.
func bannedGroupsPageItems(raw json.RawMessage) ([]json.RawMessage, string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, "", nil
	}
	if trimmed[0] == '[' {
		var items []json.RawMessage
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return nil, "", fmt.Errorf("unmarshal banned groups list: %w", err)
		}
		return items, "", nil
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil {
		return nil, "", fmt.Errorf("unmarshal banned groups page: %s", redaction.RedactValue(err.Error(), nil))
	}
	if _, hasData := object["data"]; !hasData {
		if bannedGroupName(trimmed) != "" {
			return []json.RawMessage{trimmed}, "", nil
		}
		return nil, "", errors.New("unmarshal banned groups page: missing data")
	}

	var page bannedGroupsPage
	if err := json.Unmarshal(trimmed, &page); err != nil {
		return nil, "", fmt.Errorf("unmarshal banned groups page: %w", err)
	}
	return page.Data, strings.TrimSpace(page.Meta.NextCursor), nil
}

// bannedGroupName extracts a group name from supported raw record shapes,
// including JSON:API-style attributes wrappers.
func bannedGroupName(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var item map[string]json.RawMessage
	if err := json.Unmarshal(raw, &item); err != nil {
		return ""
	}
	for _, key := range []string{"name", "group", "release_group", "releaseGroup"} {
		if value, ok := item[key]; ok {
			var name string
			if err := json.Unmarshal(value, &name); err == nil {
				return strings.TrimSpace(name)
			}
		}
	}
	if value, ok := item["attributes"]; ok {
		return bannedGroupName(value)
	}
	return ""
}

// bannedGroupsEndpoint resolves the tracker-specific blacklist URL. AITHER and
// LST use Unit3D-style base URLs; SPD uses its SpeedApp API endpoint.
func bannedGroupsEndpoint(cfg config.Config, tracker string) (string, bool) {
	name := strings.ToUpper(strings.TrimSpace(tracker))
	switch name {
	case "AITHER":
		return bannedGroupsBaseEndpoint(cfg, name, "/api/blacklists/releasegroups")
	case "LST":
		return bannedGroupsBaseEndpoint(cfg, name, "/api/bannedReleaseGroups")
	case "SPD":
		if base, ok := configuredTrackerBaseURL(cfg, name); ok {
			return strings.TrimRight(base, "/") + "/api/torrent/release-group/blacklist", true
		}
		return "https://speedapp.io/api/torrent/release-group/blacklist", true
	default:
		return "", false
	}
}

// bannedGroupsBaseEndpoint resolves a Unit3D tracker base URL from config first
// and embedded defaults second, then appends the tracker-specific API path.
func bannedGroupsBaseEndpoint(cfg config.Config, tracker string, endpointPath string) (string, bool) {
	baseURL, ok := configuredTrackerBaseURL(cfg, tracker)
	if !ok {
		baseURL, ok = unit3dmeta.BaseURL(tracker)
	}
	if !ok {
		return "", false
	}
	return strings.TrimRight(baseURL, "/") + endpointPath, true
}

// configuredTrackerBaseURL derives a web base URL from configured tracker URL
// fields without retaining announce paths or query strings.
func configuredTrackerBaseURL(cfg config.Config, tracker string) (string, bool) {
	entry := trackerConfigFor(cfg, tracker)
	for _, value := range []string{entry.URL, entry.AnnounceURL, entry.MyAnnounceURL} {
		if base := webBaseURL(value); base != "" {
			return base, true
		}
	}
	return "", false
}

// bannedGroupAPIKey returns the configured tracker API key used for dynamic
// banned-group refreshes.
func bannedGroupAPIKey(cfg config.Config, tracker string) string {
	return strings.TrimSpace(trackerConfigFor(cfg, tracker).APIKey)
}

// webBaseURL strips a configured URL to scheme and host for API endpoint
// construction.
func webBaseURL(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	parsed.Path = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/")
}

var builtinBannedGroups = map[string][]string{
	"A4K": {
		"BiTOR",
		"DepraveD",
		"Flights",
		"SasukeducK",
		"SPDVD",
		"TEKNO3D",
	},
	"ANT": {
		"3LTON",
		"4yEo",
		"ADE",
		"AFG",
		"AniHLS",
		"AnimeRG",
		"AniURL",
		"AROMA",
		"aXXo",
		"Brrip",
		"CHD",
		"CM8",
		"CrEwSaDe",
		"d3g",
		"DDR",
		"DNL",
		"DeadFish",
		"ELiTE",
		"eSc",
		"EVO",
		"FaNGDiNG0",
		"FGT",
		"FRDS",
		"FUM",
		"HAiKU",
		"HD2DVD",
		"HDS",
		"HDTime",
		"Hi10",
		"ION10",
		"iPlanet",
		"JIVE",
		"KiNGDOM",
		"Leffe",
		"LiGaS",
		"LOAD",
		"MeGusta",
		"MkvCage",
		"mHD",
		"mSD",
		"NhaNc3",
		"nHD",
		"NOIVTC",
		"nSD",
		"Oj",
		"Ozlem",
		"PiRaTeS",
		"PRoDJi",
		"RAPiDCOWS",
		"RARBG",
		"RetroPeeps",
		"RDN",
		"REsuRRecTioN",
		"RMTeam",
		"SANTi",
		"SicFoI",
		"SPASM",
		"SM737",
		"SPDVD",
		"STUTTERSHIT",
		"TBS",
		"Telly",
		"TM",
		"UPiNSMOKE",
		"URANiME",
		"WAF",
		"xRed",
		"XS",
		"YIFY",
		"YTS",
		"Zeus",
		"ZKBL",
		"ZmN",
		"ZMNT",
	},
	"BHD": {
		"Sicario",
		"TOMMY",
		"x0r",
		"nikt0",
		"FGT",
		"d3g",
		"MeGusta",
		"YIFY",
		"tigole",
		"TEKNO3D",
		"C4K",
		"RARBG",
		"4K4U",
		"EASports",
		"ReaLHD",
		"Telly",
		"AOC",
		"WKS",
		"SasukeducK",
		"CRUCiBLE",
		"iFT",
		"ProRes",
		"MezRips",
		"Flights",
		"BiTOR",
		"iVy",
		"QxR",
		"SyncUP",
		"OFT",
		"TGS",
	},
	"BLU": {
		"[Oj]",
		"3LTON",
		"4yEo",
		"ADE",
		"AFG",
		"AniHLS",
		"AnimeRG",
		"AniURL",
		"AROMA",
		"aXXo",
		"B3LLUM",
		"BHDStudio",
		"Brrip",
		"CHD",
		"CM8",
		"CrEwSaDe",
		"d3g",
		"DeadFish",
		"DNL",
		"DTLegacy",
		"ELiTE",
		"eSc",
		"EZTV",
		"EZTV.RE",
		"F13",
		"FaNGDiNG0",
		"FGT",
		"Flights",
		"flower",
		"FRDS",
		"FUM",
		"HAiKU",
		"hallowed",
		"HD2DVD",
		"HDS",
		"HDTime",
		"Hi10",
		"ION10",
		"iPlanet",
		"JIVE",
		"KiNGDOM",
		"LAMA",
		"Leffe",
		"LEGi0N",
		"LOAD",
		"MeGusta",
		"mHD",
		"mSD",
		"NhaNc3",
		"nHD",
		"nikt0",
		"NOIVTC",
		"nSD",
		"OFT",
		"PiRaTeS",
		"playBD",
		"PlaySD",
		"playXD",
		"PRODJi",
		"RAPiDCOWS",
		"RARBG",
		"RetroPeeps",
		"RDN",
		"REsuRRecTioN",
		"RMTeam",
		"SANTi",
		"SasukeducK",
		"SicFoI",
		"SPASM",
		"SPDVD",
		"STUTTERSHIT",
		"Telly",
		"TheFarm",
		"TM",
		"TRiToN",
		"UPiNSMOKE",
		"URANiME",
		"VN_Foxcore",
		"WAF",
		"WKS",
		"x0r",
		"xRed",
		"XS",
		"YIFY",
		"ZKBL",
		"ZmN",
		"ZMNT",
	},
	"CBR": {
		"4K4U",
		"afm72",
		"Alcaide_Kira",
		"AROMA",
		"ASM",
		"Bandi",
		"BiTOR",
		"BLUDV",
		"Bluespots",
		"BOLS",
		"CaNNIBal",
		"Comando", //nolint:misspell // Specific group spelling.
		"d3g",
		"DepraveD",
		"EMBER",
		"FGT",
		"FreetheFish",
		"Garshasp",
		"Ghost",
		"Grym",
		"HDS",
		"Hi10",
		"HiQVE",
		"Hiro360",
		"ImE",
		"ION10",
		"iVy",
		"Judas",
		"LAMA",
		"Langbard",
		"Lapumia",
		"LION",
		"MeGusta",
		"MONOLITH",
		"MRCS",
		"NaNi",
		"Natty",
		"nikt0",
		"OEPlus",
		"OFT",
		"OsC",
		"Panda",
		"PANDEMONiUM",
		"PHOCiS",
		"PiRaTeS",
		"PYC",
		"QxR",
		"r00t",
		"Ralphy",
		"RARBG",
		"RetroPeeps",
		"RZeroX",
		"S74Ll10n",
		"SAMPA",
		"Sicario",
		"SiCFoI",
		"Silence",
		"SkipTT",
		"SM737",
		"SPDVD",
		"STUTTERSHIT",
		"SWTYBLZ",
		"t3nzin",
		"TAoE",
		"TEKNO3D",
		"Telly",
		"TGx",
		"Tigole",
		"TSP",
		"TSPxL",
		"TWA",
		"UnKn0wn",
		"VXT",
		"Vyndros",
		"W32",
		"Will1869",
		"x0r",
		"YIFY",
		"YTS.MX",
		"YTS",
	},
	"DP": {
		"ARCADE",
		"aXXo",
		"BANDOLEROS",
		"BONE",
		"BRrip",
		"CM8",
		"CrEwSaDe",
		"CTFOH",
		"dAV1nci",
		"DNL",
		"eranger2",
		"FaNGDiNG0",
		"FGT",
		"FiSTER",
		"flower",
		"GalaxyTV",
		"HD2DVD",
		"HDTime",
		"HorribleSubs",
		"iHYTECH",
		"ION10",
		"iPlanet",
		"KiNGDOM",
		"LAMA",
		"MeGusta",
		"mHD",
		"mSD",
		"NaNi",
		"NhaNc3",
		"nHD",
		"nikt0",
		"nSD",
		"OFT",
		"PiTBULL",
		"PRODJi",
		"PSA",
		"RARBG",
		"Rifftrax",
		"ROCKETRACCOON",
		"SANTi",
		"SasukeducK",
		"SEEDSTER",
		"ShAaNiG",
		"Sicario",
		"STUTTERSHIT",
		"Subsplease",
		"SyncUp",
		"TAoE",
		"TGALAXY",
		"TGx",
		"TORRENTGALAXY",
		"ToVaR",
		"Trix",
		"TSP",
		"TSPxL",
		"ViSION",
		"VXT",
		"WAF",
		"WKS",
		"X0r",
		"YIFY",
		"YTS",
	},
	"GPW": {
		"ALT",
		"aXXo",
		"BATWEB",
		"BlackTV",
		"BitsTV",
		"BMDRu",
		"BRrip",
		"CM8",
		"CrEwSaDe",
		"CTFOH",
		"CTRLHD",
		"DDHDTV",
		"DNL",
		"DreamHD",
		"ENTHD",
		"FaNGDiNG0",
		"FGT",
		"HD2DVD",
		"HDTime",
		"HDT",
		"Huawei",
		"GPTHD",
		"ION10",
		"iPlanet",
		"KiNGDOM",
		"Leffe",
		"Mp4Ba",
		"mHD",
		"MiniHD",
		"mSD",
		"MOMOWEB",
		"nHD",
		"nikt0",
		"NSBC",
		"nSD",
		"NhaNc3",
		"NukeHD",
		"OFT",
		"PRODJi",
		"RARBG",
		"RDN",
		"SANTi",
		"SeeHD",
		"SeeWEB",
		"SM737",
		"SonyHD",
		"STUTTERSHIT",
		"TAGWEB",
		"ViSION",
		"VXT",
		"WAF",
		"x0r",
		"Xiaomi",
		"YIFY",
	},
	"HHD": {
		"aXXo",
		"BONE",
		"BRrip",
		"CM8",
		"CrEwSaDe",
		"CTFOH",
		"dAV1nci",
		"d3g",
		"DNL",
		"FaNGDiNG0",
		"GalaxyTV",
		"HD2DVD",
		"HDTime",
		"iHYTECH",
		"ION10",
		"iPlanet",
		"KiNGDOM",
		"LAMA",
		"MeGusta",
		"mHD",
		"mSD",
		"NaNi",
		"NhaNc3",
		"nHD",
		"nikt0",
		"nSD",
		"OFT",
		"PRODJi",
		"RARBG",
		"Rifftrax",
		"SANTi",
		"SasukeducK",
		"ShAaNiG",
		"Sicario",
		"STUTTERSHIT",
		"TGALAXY",
		"TORRENTGALAXY",
		"TSP",
		"TSPxL",
		"ViSION",
		"VXT",
		"WAF",
		"WKS",
		"x0r",
		"YAWNiX",
		"YIFY",
		"YTS",
		"PSA",
		"EVO",
	},
	"LT": {
		"EVO",
	},
	"MTV": {
		"3LTON",
		"[Oj]",
		"aXXo",
		"BDP",
		"BRrip",
		"CM8",
		"CrEwSaDe",
		"CMCT",
		"DeadFish",
		"DNL",
		"ELiTE",
		"AFG",
		"ZMNT",
		"FaNGDiNG0",
		"FRDS",
		"FUM",
		"h65",
		"HD2DVD",
		"HDTime",
		"ION10",
		"iPlanet",
		"JIVE",
		"KiNGDOM",
		"LAMA",
		"Leffe",
		"LOAD",
		"mHD",
		"mRS",
		"mSD",
		"NhaNc3",
		"nHD",
		"nikt0",
		"nSD",
		"PandaRG",
		"PRODJi",
		"QxR",
		"RARBG",
		"RDN",
		"SANTi",
		"STUTTERSHIT",
		"TERMiNAL",
		"TM",
		"ViSiON",
		"WAF",
		"x0r",
		"XS",
		"YIFY",
		"ZKBL",
		"ZmN",
	},
	"NBL": {
		"0neshot",
		"3LTON",
		"4yEo",
		"[Oj]",
		"AFG",
		"AkihitoSubs",
		"AniHLS",
		"Anime Time",
		"AnimeRG",
		"AniURL",
		"ASW",
		"BakedFish",
		"bonkai77",
		"Cleo",
		"DeadFish",
		"DeeJayAhmed",
		"ELiTE",
		"EMBER",
		"eSc",
		"EVO",
		"FGT",
		"FUM",
		"GERMini",
		"HAiKU",
		"Hi10",
		"ION10",
		"JacobSwaggedUp",
		"JIVE",
		"Judas",
		"LOAD",
		"MeGusta",
		"Mr.Deadpool",
		"mSD",
		"NemDiggers",
		"neoHEVC",
		"NhaNc3",
		"NOIVTC",
		"PlaySD",
		"playXD",
		"project-gxs",
		"PSA",
		"QaS",
		"Ranger",
		"RAPiDCOWS",
		"Raze",
		"Reaktor",
		"REsuRRecTioN",
		"RMTeam",
		"ROBOTS",
		"SpaceFish",
		"SPASM",
		"SSA",
		"Telly",
		"Tenrai-Sensei",
		"TM",
		"Trix",
		"URANiME",
		"VipapkStudios",
		"ViSiON",
		"Wardevil",
		"xRed",
		"XS",
		"YakuboEncodes",
		"YuiSubs",
		"ZKBL",
		"ZmN",
		"ZMNT",
	},
	"OE": {
		"0neshot",
		"3LT0N",
		"4K4U",
		"4yEo",
		"$andra",
		"[Oj]",
		"AFG",
		"AkihitoSubs",
		"Alcaide_Kira",
		"AniHLS",
		"Anime Time",
		"AnimeRG",
		"AniURL",
		"AOC",
		"AR",
		"AROMA",
		"ASW",
		"aXXo",
		"BakedFish",
		"BiTOR",
		"BRrip",
		"bonkai",
		"Cleo",
		"CM8",
		"C4K",
		"CrEwSaDe",
		"core",
		"d3g",
		"DDR",
		"DE3PM",
		"DeadFish",
		"DeeJayAhmed",
		"DNL",
		"ELiTE",
		"EMBER",
		"eSc",
		"EVO",
		"EZTV",
		"FaNGDiNG0",
		"FGT",
		"fenix",
		"FUM",
		"FRDS",
		"FROZEN",
		"GalaxyTV",
		"GalaxyRG",
		"GalaxyRG265",
		"GERMini",
		"Grym",
		"GrymLegacy",
		"HAiKU",
		"HD2DVD",
		"HDTime",
		"Hi10",
		"HiQVE",
		"ION10",
		"iPlanet",
		"iVy",
		"JacobSwaggedUp",
		"JIVE",
		"Judas",
		"KiNGDOM",
		"LAMA",
		"Leffe",
		"LiGaS",
		"LOAD",
		"LycanHD",
		"MeGusta",
		"MezRips",
		"mHD",
		"Mr.Deadpool",
		"mSD",
		"NemDiggers",
		"neoHEVC",
		"NeXus",
		"nHD",
		"nikt0",
		"nSD",
		"NhaNc3",
		"NOIVTC",
		"pahe.in",
		"PlaySD",
		"playXD",
		"PRODJi",
		"ProRes",
		"project-gxs",
		"PSA",
		"QaS",
		"Ranger",
		"RAPiDCOWS",
		"RARBG",
		"Raze",
		"RCDiVX",
		"RDN",
		"Reaktor",
		"REsuRRecTioN",
		"RMTeam",
		"ROBOTS",
		"rubix",
		"SANTi",
		"SHUTTERSHIT",
		"SM737",
		"SpaceFish",
		"SPASM",
		"SSA",
		"TBS",
		"Telly",
		"Tenrai-Sensei",
		"TERMiNAL",
		"TGx",
		"TM",
		"topaz",
		"ToVaR",
		"TSP",
		"TSPxL",
		"UnKn0wn",
		"URANiME",
		"UTR",
		"VipapkSudios",
		"ViSION",
		"WAF",
		"Wardevil",
		"x0r",
		"xRed",
		"XS",
		"YakuboEncodes",
		"YAWNTiC",
		"YAWNiX",
		"YIFY",
		"YTS",
		"YuiSubs",
		"ZKBL",
		"ZmN",
		"ZMNT",
	},
	"OTW": {
		"[Oj]",
		"3LTON",
		"4yEo",
		"ADE",
		"AFG",
		"AniHLS",
		"AnimeRG",
		"AniURL",
		"AROMA",
		"aXXo",
		"CM8",
		"CrEwSaDe",
		"DeadFish",
		"DNL",
		"ELiTE",
		"eSc",
		"FaNGDiNG0",
		"FGT",
		"Flights",
		"FRDS",
		"FUM",
		"GalaxyRG",
		"HAiKU",
		"HD2DVD",
		"HDS",
		"HDTime",
		"Hi10",
		"INFINITY",
		"ION10",
		"iPlanet",
		"JIVE",
		"KiNGDOM",
		"LAMA",
		"Leffe",
		"LOAD",
		"mHD",
		"NhaNc3",
		"nHD",
		"NOIVTC",
		"nSD",
		"PiRaTeS",
		"PRODJi",
		"RAPiDCOWS",
		"RARBG",
		"RDN",
		"REsuRRecTioN",
		"RMTeam",
		"SANTi",
		"SicFoI",
		"SPASM",
		"STUTTERSHIT",
		"Telly",
		"TM",
		"UPiNSMOKE",
		"WAF",
		"xRed",
		"XS",
		"YELLO",
		"YIFY",
		"YTS",
		"ZKBL",
		"ZmN",
		"4f8c4100292",
		"Azkars",
		"Sync0rdi",
	},
	"PHD": {
		"RARBG",
		"STUTTERSHIT",
		"LiGaS",
		"DDR",
		"Zeus",
		"TBS",
		"SWTYBLZ",
		"EASports",
		"C4K",
		"d3g",
		"MeGusta",
		"YTS",
		"YIFY",
		"Tigole",
		"x0r",
		"nikt0",
		"NhaNc3",
		"PRoDJi",
		"RDN",
		"SANTi",
		"FaNGDiNG0",
		"FRDS",
		"HD2DVD",
		"HDTime",
		"iPlanet",
		"KiNGDOM",
		"Leffe",
		"4K4U",
		"Xiaomi",
		"VisionXpert",
		"WKS",
	},
	"PTP": {
		"aXXo",
		"BMDru",
		"BRrip",
		"CM8",
		"CrEwSaDe",
		"CTFOH",
		"d3g",
		"DNL",
		"FaNGDiNG0",
		"HD2DVD",
		"HDT",
		"HDTime",
		"ION10",
		"iPlanet",
		"KiNGDOM",
		"mHD",
		"mSD",
		"nHD",
		"nikt0",
		"nSD",
		"NhaNc3",
		"OFT",
		"PRODJi",
		"SANTi",
		"SPiRiT",
		"STUTTERSHIT",
		"ViSION",
		"VXT",
		"WAF",
		"x0r",
		"YIFY",
		"LAMA",
		"WORLD",
	},
	"PTT": {
		"ViP",
		"BiRD",
		"M@RTiNU$",
		"inTGrity",
		"CiNEMAET",
		"MusicET",
		"TeamET",
		"R2D2",
	},
	"RAS": {
		"YTS",
		"YiFY",
		"LAMA",
		"MeGUSTA",
		"NAHOM",
		"GalaxyRG",
		"RARBG",
		"INFINITY",
	},
	"RHD": {
		"1XBET",
		"MEGA",
		"MTZ",
		"Whistler",
		"WOTT",
		"Taylor.D",
		"HELD",
		"FSX",
		"FuN",
		"MagicX",
		"w00t",
		"PaTroL",
		"BB",
		"266ers",
		"GTF",
		"JellyfinPlex",
		"2BA",
		"FritzBox",
		"FUNXDTV",
	},
	"TOS": {
		"FL3ER",
		"SUNS3T",
		"WoLFHD",
		"EXTREME",
		"Slay3R",
		"3T3AM",
		"BARBiE",
	},
	"BTN": {
		"3LTON",
		"4yEo",
		"7VFr33104D",
		"AFG",
		"AniHLS",
		"AnimeRG",
		"AniURL",
		"DeadFish",
		"ELiTE",
		"eSc",
		"EVO",
		"FGT",
		"FUM",
		"GalaxyTV",
		"GRANiTEN",
		"HAiKU",
		"Hi10",
		"ION10",
		"JFF",
		"JIVE",
		"LOAD",
		"MeGusta",
		"mSD",
		"NhaNc3",
		"NOIVTC",
		"PHOENiX",
		"PlaySD",
		"playXD",
		"Pr1M371M3",
		"RAPiDCOWS",
		"REsuRRecTioN",
		"RMTeam",
		"ROBOTS",
		"RUBiK",
		"SPASM",
		"Telly",
		"TM",
		"URANiME",
		"ViSiON",
		"W45Ps",
		"xRed",
		"XS",
		"ZKBL",
		"ZmN",
		"ZMNT",
		"[Oj]",
	},
	"ULCX": {
		"4K4U",
		"AROMA",
		"d3g",
		"EDGE2020",
		"EMBER",
		"FGT",
		"FnP",
		"FRDS",
		"Grym",
		"Hi10",
		"iAHD",
		"INFINITY",
		"ION10",
		"iVy",
		"Judas",
		"LAMA",
		"MeGusta",
		"NAHOM",
		"Niblets",
		"nikt0",
		"NuBz",
		"OFT",
		"QxR",
		"Ralphy",
		"RARBG",
		"Sicario",
		"SM737",
		"SPDVD",
		"SWTYBLZ",
		"TAoE",
		"TGx",
		"Tigole",
		"TSP",
		"TSPxL",
		"VXT",
		"Vyndros",
		"Will1869",
		"x0r",
		"YIFY",
		"Alcaide_Kira",
		"PHOCiS",
		"HDT",
		"SPx",
		"seedpool",
	},
	"YUS": {
		"ADDICTION",
		"B3LLUM",
		"BANDOLEROS",
		"BigEasy",
		"CINEMAXIS",
		"D3US",
		"d3g",
		"DUMMESCHWEDEN",
		"FGT",
		"GRANiTEN",
		"KiNGDOM",
		"Lama",
		"MeGusta",
		"MezRips",
		"mHD",
		"mRS",
		"msd",
		"NeXus",
		"NhaNc3",
		"nHD",
		"NorTekst",
		"NORViNE",
		"PANDEMONiUM",
		"PiTBULL",
		"RAPiDCOWS",
		"RARBG",
		"Radarr",
		"RCDiVX",
		"RDN",
		"ROCKETRACCOON",
		"SANTi",
		"SHOWTiME",
		"SOOSi",
		"SUXWIC",
		"TOXVIO",
		"TWA",
		"VXT",
		"Will1869",
		"x0r",
		"XS",
		"YIFY",
		"YOLAND",
		"YTS",
		"ZKBL",
		"ZmN",
		"ZMNT",
	},
}
