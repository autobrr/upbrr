// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackerdata"
	"github.com/autobrr/upbrr/internal/trackers/unit3dmeta"
	"github.com/autobrr/upbrr/pkg/api"
)

const trackerClaimsCacheTTL = 24 * time.Hour
const trackerClaimRuleActive = "claim_active"

type trackerClaimProvider interface {
	cachePath(dbPath string, tracker string) (string, error)
	cacheTTL() time.Duration
	hasClaim(ctx context.Context, s *Service, tracker string, meta api.PreparedMetadata) (bool, error)
}

type apiTrackerClaimProvider struct{}

type trackerClaimsCache struct {
	LastUpdated string              `json:"last_updated"`
	Claims      []trackerClaimEntry `json:"extracted_data"`
}

type trackerClaimEntry struct {
	Title       string                  `json:"title"`
	Season      int                     `json:"season"`
	TMDBID      int                     `json:"tmdb_id"`
	Categories  trackerClaimCategories  `json:"categories,omitempty"`
	Resolutions trackerClaimResolutions `json:"resolutions"`
	Types       trackerClaimTypes       `json:"types"`
}

type trackerClaimsResponse struct {
	Data []trackerClaimsItem `json:"data"`
	Meta trackerClaimsMeta   `json:"meta"`
}

type trackerClaimsItem struct {
	Attributes trackerClaimsAttributes `json:"attributes"`
}

type trackerClaimsMeta struct {
	NextCursor string `json:"next_cursor"`
}

type trackerClaimsAttributes struct {
	Title       string                  `json:"title"`
	Season      json.RawMessage         `json:"season"`
	TMDBID      json.RawMessage         `json:"tmdb_id"`
	Categories  trackerClaimCategories  `json:"categories"`
	Resolutions trackerClaimResolutions `json:"resolutions"`
	Types       trackerClaimTypes       `json:"types"`
}

func (s *Service) applyTrackerClaims(ctx context.Context, meta api.PreparedMetadata) (api.PreparedMetadata, error) {
	resolved := uniqueUpperTrackers(trackersWithClaimChecks(meta))
	if len(resolved) == 0 {
		if s.logger != nil {
			s.logger.Debugf("metadata: tracker claims skipped (no eligible trackers)")
		}
		return meta, nil
	}
	if s.logger != nil {
		s.logger.Debugf("metadata: tracker claims evaluating trackers=%v", resolved)
	}

	for _, tracker := range resolved {
		select {
		case <-ctx.Done():
			return api.PreparedMetadata{}, fmt.Errorf("context canceled: %w", ctx.Err())
		default:
		}

		match, err := s.hasTrackerClaim(ctx, tracker, meta)
		if err != nil {
			return api.PreparedMetadata{}, err
		}
		if !match {
			continue
		}

		meta.BlockedTrackers = addMetadataTrackerBlockReason(meta.BlockedTrackers, tracker, api.TrackerBlockReasonClaim)
		meta.TrackerRuleFailures = addMetadataTrackerRuleFailure(meta.TrackerRuleFailures, tracker, api.RuleFailure{
			Rule:   trackerClaimRuleActive,
			Reason: trackerClaimFailureReason(tracker, meta, s),
		})
		if s.logger != nil {
			s.logger.Warnf("metadata: tracker claim match found for %s", tracker)
		}
	}

	return meta, nil
}

func (s *Service) hasTrackerClaim(ctx context.Context, tracker string, meta api.PreparedMetadata) (bool, error) {
	provider, ok := resolveTrackerClaimProvider(tracker)
	if !ok {
		return false, nil
	}
	return provider.hasClaim(ctx, s, tracker, meta)
}

func resolveTrackerClaimProvider(tracker string) (trackerClaimProvider, bool) {
	switch strings.ToUpper(strings.TrimSpace(tracker)) {
	case "BTN":
		return btnTrackerClaimProvider{}, true
	case "AITHER":
		return apiTrackerClaimProvider{}, true
	default:
		return nil, false
	}
}

func (apiTrackerClaimProvider) cachePath(dbPath string, tracker string) (string, error) {
	return trackerClaimsPath(dbPath, tracker)
}

func (apiTrackerClaimProvider) cacheTTL() time.Duration {
	return trackerClaimsCacheTTL
}

func (p apiTrackerClaimProvider) hasClaim(ctx context.Context, s *Service, tracker string, meta api.PreparedMetadata) (bool, error) {
	cachePath, err := p.cachePath(s.cfg.MainSettings.DBPath, tracker)
	if err != nil {
		return false, fmt.Errorf("metadata: tracker claims path %s: %w", tracker, err)
	}

	claims, err := s.loadTrackerClaims(ctx, tracker, cachePath, p.cacheTTL())
	if err != nil {
		return false, err
	}

	target, ok := trackerClaimTarget(meta)
	if !ok {
		return false, nil
	}

	for _, claim := range claims {
		if claim.TMDBID != target.tmdbID {
			continue
		}
		if target.isTV && claim.Season != target.season {
			continue
		}
		if len(claim.Categories) > 0 && !containsTrackerClaimValue(claim.Categories, target.category) {
			continue
		}
		if !containsTrackerClaimValue(claim.Resolutions, target.resolution) {
			continue
		}
		if !containsTrackerClaimValue(claim.Types, target.typeName) {
			continue
		}
		return true, nil
	}

	return false, nil
}

func (s *Service) loadTrackerClaims(ctx context.Context, tracker string, cachePath string, cacheTTL time.Duration) ([]trackerClaimEntry, error) {
	if payload, ok := loadFreshTrackerClaimsCache(cachePath, cacheTTL); ok {
		return payload.Claims, nil
	}

	claims, err := s.fetchTrackerClaims(ctx, tracker)
	if err != nil {
		return nil, err
	}
	if err := writeTrackerClaimsCache(cachePath, claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func loadFreshTrackerClaimsCache(path string, cacheTTL time.Duration) (trackerClaimsCache, bool) {
	info, err := os.Stat(path)
	if err != nil || time.Since(info.ModTime()) >= cacheTTL {
		return trackerClaimsCache{}, false
	}

	payload, err := os.ReadFile(path)
	if err != nil {
		return trackerClaimsCache{}, false
	}

	var cache trackerClaimsCache
	if err := json.Unmarshal(payload, &cache); err != nil {
		return trackerClaimsCache{}, false
	}
	return cache, true
}

func writeTrackerClaimsCache(path string, claims []trackerClaimEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("metadata: tracker claims cache dir: %w", err)
	}

	payload, err := json.MarshalIndent(trackerClaimsCache{
		LastUpdated: time.Now().UTC().Format("2006-01-02"),
		Claims:      claims,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("metadata: tracker claims encode: %w", err)
	}

	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return fmt.Errorf("metadata: tracker claims write: %w", err)
	}
	return nil
}

func (s *Service) fetchTrackerClaims(ctx context.Context, tracker string) ([]trackerClaimEntry, error) {
	baseURL, ok := trackerClaimsBaseURL(s.cfg, tracker)
	if !ok {
		return nil, nil
	}

	apiKey := strings.TrimSpace(trackerdata.TrackerAPIKey(s.cfg, tracker))
	if apiKey == "" {
		return nil, nil
	}

	client := &http.Client{Timeout: 20 * time.Second}
	endpoint := strings.TrimRight(baseURL, "/") + "/api/internals/claim"
	cursor := ""
	claims := make([]trackerClaimEntry, 0)

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("metadata: tracker claims request %s: %w", tracker, err)
		}

		query := url.Values{}
		query.Set("per_page", "100")
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		req.URL.RawQuery = query.Encode()
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Accept", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("metadata: tracker claims fetch %s: %w", tracker, err)
		}

		var payload trackerClaimsResponse
		decodeErr := json.NewDecoder(resp.Body).Decode(&payload)
		_ = resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("metadata: tracker claims fetch %s: status %d", tracker, resp.StatusCode)
		}
		if decodeErr != nil {
			return nil, fmt.Errorf("metadata: tracker claims decode %s: %w", tracker, decodeErr)
		}

		for _, item := range payload.Data {
			entry, ok := normalizeTrackerClaimEntry(item.Attributes)
			if !ok {
				continue
			}
			claims = append(claims, entry)
		}

		cursor = strings.TrimSpace(payload.Meta.NextCursor)
		if cursor == "" {
			break
		}
	}

	return claims, nil
}

func normalizeTrackerClaimEntry(attrs trackerClaimsAttributes) (trackerClaimEntry, bool) {
	tmdbID, ok := parseRawInt(attrs.TMDBID)
	if !ok || tmdbID == 0 {
		return trackerClaimEntry{}, false
	}

	season, _ := parseRawInt(attrs.Season)
	return trackerClaimEntry{
		Title:       strings.TrimSpace(attrs.Title),
		Season:      season,
		TMDBID:      tmdbID,
		Categories:  append(trackerClaimCategories{}, attrs.Categories...),
		Resolutions: append(trackerClaimResolutions{}, attrs.Resolutions...),
		Types:       append(trackerClaimTypes{}, attrs.Types...),
	}, true
}

func parseRawInt(raw json.RawMessage) (int, bool) {
	if len(raw) == 0 {
		return 0, false
	}

	var intValue int
	if err := json.Unmarshal(raw, &intValue); err == nil {
		return intValue, true
	}

	var stringValue string
	if err := json.Unmarshal(raw, &stringValue); err == nil {
		parsed, err := strconv.Atoi(strings.TrimSpace(stringValue))
		if err == nil {
			return parsed, true
		}
	}

	return 0, false
}

type trackerClaimMatchTarget struct {
	tmdbID     int
	season     int
	category   string
	typeName   string
	resolution string
	isTV       bool
}

func trackerClaimTarget(meta api.PreparedMetadata) (trackerClaimMatchTarget, bool) {
	season, _ := meta.SeasonEpisodeWithParsedFallback()
	target := trackerClaimMatchTarget{
		tmdbID:     meta.ExternalIDs.TMDBID,
		season:     season,
		category:   trackerdata.CanonicalUnit3DCategory(resolveTrackerClaimCategory(meta)),
		typeName:   trackerdata.CanonicalUnit3DType(resolveTrackerClaimType(meta)),
		resolution: trackerdata.CanonicalUnit3DResolution(resolveTrackerClaimResolution(meta)),
		isTV:       isTVCategory(meta),
	}
	if target.tmdbID == 0 || target.typeName == "" || target.resolution == "" {
		return trackerClaimMatchTarget{}, false
	}
	if target.isTV && target.season == 0 {
		return trackerClaimMatchTarget{}, false
	}
	return target, true
}

func resolveTrackerClaimCategory(meta api.PreparedMetadata) string {
	for _, candidate := range []string{meta.ExternalIDs.Category, meta.MediaInfoCategory, meta.Release.Category} {
		if canonical := trackerdata.CanonicalUnit3DCategory(candidate); canonical != "" {
			return canonical
		}
	}
	if isTVCategory(meta) {
		return "TV"
	}
	return "MOVIE"
}

func resolveTrackerClaimType(meta api.PreparedMetadata) string {
	for _, candidate := range []string{meta.Type, meta.Release.Type, meta.Source, meta.Release.Source} {
		if canonical := trackerdata.CanonicalUnit3DType(candidate); canonical != "" {
			return canonical
		}
	}
	return ""
}

func resolveTrackerClaimResolution(meta api.PreparedMetadata) string {
	for _, candidate := range []string{meta.Release.Resolution} {
		if canonical := trackerdata.CanonicalUnit3DResolution(candidate); canonical != "" {
			return canonical
		}
	}
	return ""
}

func trackerClaimsPath(dbPath string, tracker string) (string, error) {
	path, err := db.FileInSubdir(dbPath, "cache", filepath.Join("banned", strings.ToUpper(strings.TrimSpace(tracker))+"_claimed_releases.json"))
	if err != nil {
		return "", fmt.Errorf("metadata: resolve tracker claims path: %w", err)
	}
	return path, nil
}

func trackerClaimsBaseURL(cfg config.Config, tracker string) (string, bool) {
	if entry, ok := trackerConfigFor(cfg, tracker); ok {
		if announceBase := announceBaseURL(entry.AnnounceURL); announceBase != "" {
			return announceBase, true
		}
	}
	return unit3dmeta.BaseURL(tracker)
}

func announceBaseURL(value string) string {
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

func trackersWithClaimChecks(meta api.PreparedMetadata) []string {
	resolved := resolveClaimTrackerCandidates(meta)
	if len(resolved) == 0 {
		return nil
	}

	selected := make([]string, 0, len(resolved))
	for _, tracker := range resolved {
		normalized := strings.ToUpper(strings.TrimSpace(tracker))
		switch normalized {
		case "AITHER", "BTN":
			selected = append(selected, normalized)
		}
	}
	return selected
}

func resolveClaimTrackerCandidates(meta api.PreparedMetadata) []string {
	values := make([]string, 0, len(meta.Trackers)+len(meta.MatchedTrackers)+len(meta.TrackerIDs))
	values = append(values, meta.Trackers...)
	values = append(values, meta.MatchedTrackers...)
	for key := range meta.TrackerIDs {
		values = append(values, key)
	}
	return uniqueUpperTrackers(values)
}

func uniqueUpperTrackers(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		upper := strings.ToUpper(strings.TrimSpace(value))
		if upper == "" {
			continue
		}
		if _, ok := seen[upper]; ok {
			continue
		}
		seen[upper] = struct{}{}
		out = append(out, upper)
	}
	return out
}

func addMetadataTrackerBlockReason(blocked map[string][]api.TrackerBlockReason, tracker string, reason api.TrackerBlockReason) map[string][]api.TrackerBlockReason {
	name := strings.ToUpper(strings.TrimSpace(tracker))
	if name == "" || strings.TrimSpace(string(reason)) == "" {
		return blocked
	}
	if blocked == nil {
		blocked = make(map[string][]api.TrackerBlockReason)
	}
	if slices.Contains(blocked[name], reason) {
		return blocked
	}
	blocked[name] = append(blocked[name], reason)
	return blocked
}

func addMetadataTrackerRuleFailure(failures map[string][]api.RuleFailure, tracker string, failure api.RuleFailure) map[string][]api.RuleFailure {
	name := strings.ToUpper(strings.TrimSpace(tracker))
	rule := strings.TrimSpace(failure.Rule)
	reason := strings.TrimSpace(failure.Reason)
	if name == "" || (rule == "" && reason == "") {
		return failures
	}
	if failures == nil {
		failures = make(map[string][]api.RuleFailure)
	}
	for _, existing := range failures[name] {
		if strings.EqualFold(strings.TrimSpace(existing.Rule), rule) && strings.EqualFold(strings.TrimSpace(existing.Reason), reason) {
			return failures
		}
	}
	failures[name] = append(failures[name], api.RuleFailure{Rule: rule, Reason: reason})
	return failures
}

func trackerClaimFailureReason(tracker string, meta api.PreparedMetadata, s *Service) string {
	name := strings.ToUpper(strings.TrimSpace(tracker))
	switch name {
	case "BTN":
		return btnClaimFailureReason(meta, s.btnClaimWindowGraceHours())
	default:
		return name + " has an active claim for this release"
	}
}

func containsTrackerClaimValue(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}

type trackerClaimCategories []string

func (v *trackerClaimCategories) UnmarshalJSON(data []byte) error {
	values, err := decodeTrackerClaimValues(data, trackerdata.CategoryNames, trackerdata.CanonicalUnit3DCategory)
	if err != nil {
		return err
	}
	*v = trackerClaimCategories(values)
	return nil
}

type trackerClaimTypes []string

func (v *trackerClaimTypes) UnmarshalJSON(data []byte) error {
	values, err := decodeTrackerClaimValues(data, trackerdata.TypeNames, trackerdata.CanonicalUnit3DType)
	if err != nil {
		return err
	}
	*v = trackerClaimTypes(values)
	return nil
}

type trackerClaimResolutions []string

func (v *trackerClaimResolutions) UnmarshalJSON(data []byte) error {
	values, err := decodeTrackerClaimValues(data, trackerdata.ResolutionNames, trackerdata.CanonicalUnit3DResolution)
	if err != nil {
		return err
	}
	*v = trackerClaimResolutions(values)
	return nil
}

func decodeTrackerClaimValues(data []byte, namesByID func(string) []string, canonicalize func(string) string) ([]string, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}

	var items []json.RawMessage
	if trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return nil, fmt.Errorf("metadata: unmarshal tracker claim values: %w", err)
		}
	} else {
		items = []json.RawMessage{trimmed}
	}

	values := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		resolved, err := decodeTrackerClaimValue(item, namesByID, canonicalize)
		if err != nil {
			return nil, err
		}
		for _, value := range resolved {
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			values = append(values, value)
		}
	}

	return values, nil
}

func decodeTrackerClaimValue(raw json.RawMessage, namesByID func(string) []string, canonicalize func(string) string) ([]string, error) {
	if id, ok := parseRawInt(raw); ok {
		return namesByID(strconv.Itoa(id)), nil
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		text = strings.TrimSpace(text)
		if text == "" {
			return nil, nil
		}
		if values := namesByID(text); len(values) > 0 {
			return values, nil
		}
		if canonical := canonicalize(text); canonical != "" {
			return []string{canonical}, nil
		}
		return nil, nil
	}

	return nil, fmt.Errorf("metadata: invalid tracker claim value %s", string(raw))
}

func isTVCategory(meta api.PreparedMetadata) bool {
	return meta.HasTVSeasonEpisodeSignal() ||
		meta.TVPack ||
		strings.TrimSpace(meta.DailyEpisodeDate) != ""
}
