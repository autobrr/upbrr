// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package clients

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
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/torrent"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/unit3dmeta"
	"github.com/autobrr/upbrr/pkg/api"

	"github.com/anacrolix/torrent/metainfo"

	qbittorrent "github.com/autobrr/go-qbittorrent"
)

const (
	mtvPieceSizeLimit  = 8 * 1024 * 1024
	max16PieceSize     = 16 * 1024 * 1024
	proxySearchTimeout = 15 * time.Second
)

type trackerPattern struct {
	url     string
	pattern *regexp.Regexp
}

var (
	unit3dTrackerIDPattern = regexp.MustCompile(`/(\d+)`)
	trackerIDPatterns      = buildTrackerIDPatterns()
)

var trackerURLPatterns = map[string][]string{
	"acm":    {"https://eiga.moi"},
	"aither": {"https://aither.cc"},
	"ant":    {"tracker.anthelion.me"},
	"ar":     {"tracker.alpharatio"},
	"asc":    {"amigos-share.club"},
	"az":     {"tracker.avistaz.to"},
	"bhd":    {"https://beyond-hd.me", "tracker.beyond-hd.me"},
	"bjs":    {"tracker.bj-share.info"},
	"blu":    {"https://blutopia.cc"},
	"bt":     {"t.brasiltracker.org"},
	"btn":    {"https://broadcasthe.net", "https://backup.landof.tv", "https://landof.tv", "landof.tv/"},
	"cbr":    {"capybarabr.com"},
	"cz":     {"tracker.cinemaz.to"},
	"dc":     {"tracker.digitalcore.club", "trackerprxy.digitalcore.club"},
	"dp":     {"https://darkpeers.org"},
	"ff":     {"tracker.funfile.org"},
	"fl":     {"reactor.filelist", "reactor.thefl.org"},
	"fnp":    {"https://fearnopeer.com"},
	"gpw":    {"https://tracker.greatposterwall.com"},
	"hdb":    {"https://tracker.hdbits.org"},
	"hds":    {"hd-space.pw"},
	"hdt":    {"https://hdts-announce.ru"},
	"hhd":    {"https://homiehelpdesk.net"},
	"ihd":    {"https://infinityhd.net"},
	"is":     {"https://immortalseed.me"},
	"itt":    {"https://itatorrents.xyz"},
	"lcd":    {"locadora.cc"},
	"ldu":    {"theldu.to"},
	"lst":    {"https://lst.gg"},
	"lt":     {"https://lat-team.com"},
	"lume":   {"https://luminarr.me"},
	"mtv":    {"tracker.morethantv"},
	"nbl":    {"tracker.nebulance"},
	"oe":     {"https://onlyencodes.cc"},
	"otw":    {"https://oldtoons.world"},
	"phd":    {"tracker.privatehd"},
	"pt":     {"https://portugas.org"},
	"ptp":    {"passthepopcorn.me"},
	"pts":    {"https://tracker.ptskit.com"},
	"ras":    {"https://rastastugan.org"},
	"rf":     {"https://reelflix.xyz", "https://reelflix.cc"},
	"rtf":    {"peer.retroflix"},
	"sam":    {"https://samaritano.cc"},
	"sp":     {"https://seedpool.org"},
	"spd":    {"ramjet.speedapp.io", "ramjet.speedapp.to", "ramjet.speedappio.org"},
	"stc":    {"https://skipthecommercials.xyz"},
	"thr":    {"torrenthr"},
	"tl":     {"tracker.tleechreload", "tracker.torrentleech"},
	"tlz":    {"https://tlzdigital.com/"},
	"tos":    {"https://theoldschool.cc"},
	"ttr":    {"https://torrenteros.org"},
	"tvc":    {"https://tvchaosuk.com"},
	"ulcx":   {"https://upload.cx"},
	"yus":    {"https://yu-scene.net"},
}

var lowerTrackerURLPatterns = buildLowerTrackerURLPatterns(trackerURLPatterns)

func buildLowerTrackerURLPatterns(source map[string][]string) map[string][]string {
	if len(source) == 0 {
		return nil
	}
	lowered := make(map[string][]string, len(source))
	for id, patterns := range source {
		normalized := make([]string, 0, len(patterns))
		for _, pattern := range patterns {
			trimmed := strings.ToLower(strings.TrimSpace(pattern))
			if trimmed == "" {
				continue
			}
			normalized = append(normalized, trimmed)
		}
		lowered[id] = normalized
	}
	return lowered
}

func buildTrackerIDPatterns() map[string]trackerPattern {
	patterns := map[string]trackerPattern{
		"hdb": {url: "https://hdbits.org", pattern: regexp.MustCompile(`id=(\d+)`)},
		"btn": {url: "https://broadcasthe.net", pattern: regexp.MustCompile(`id=(\d+)`)},
		"bhd": {url: "https://beyond-hd.me", pattern: regexp.MustCompile(`details/(\d+)`)},
		"ptp": {url: "passthepopcorn.me", pattern: regexp.MustCompile(`torrentid=(\d+)`)},
	}

	for _, tracker := range trackers.Unit3DTrackers() {
		baseURL, ok := unit3dmeta.BaseURL(tracker)
		if !ok || strings.TrimSpace(baseURL) == "" {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(tracker))
		if key == "" {
			continue
		}
		patterns[key] = trackerPattern{url: strings.ToLower(baseURL), pattern: unit3dTrackerIDPattern}
	}

	return patterns
}

type proxySearchResponse struct {
	Torrents []qbittorrent.Torrent `json:"torrents"`
}

type pieceConstraints struct {
	label       string
	preferSmall bool
	preferMax16 bool
}

func (s *Service) SearchPathedTorrents(ctx context.Context, meta api.PreparedMetadata) (api.ClientSearchResult, error) {
	if strings.TrimSpace(meta.SourcePath) == "" {
		return api.ClientSearchResult{}, internalerrors.ErrInvalidInput
	}

	constraints := resolvePieceConstraints(s.cfg)
	result := api.ClientSearchResult{PieceSizeConstraint: constraints.label}
	s.logger.Tracef("clients: pathed search start source=%s constraints=%q", meta.SourcePath, constraints.label)

	clients, usedFallback := resolveSearchClients(s.cfg, meta.ClientOverrides)
	if len(clients) == 0 {
		s.logger.Debugf("clients: no search clients configured (default_torrent_client/searching_client_list), skipping pathed search")
		return result, nil
	}
	if usedFallback {
		s.logger.Debugf("clients: no default search client set; searching all qBittorrent clients (%d)", len(clients))
	}
	s.logger.Tracef("clients: pathed search clients=%s", strings.Join(clients, ","))
	if meta.Options.Debug {
		s.logger.Debugf("clients: pathed search for %s (clients=%d constraints=%q)", meta.SourcePath, len(clients), constraints.label)
	}

	allMatches := make([]api.TorrentMatch, 0)
	seenHashes := make(map[string]struct{})

	for _, name := range clients {
		select {
		case <-ctx.Done():
			return api.ClientSearchResult{}, ctx.Err()
		default:
		}

		s.logger.Tracef("clients: pathed search running for client %s", name)

		clientCfg, ok := s.cfg.TorrentClients[name]
		if !ok {
			s.logger.Debugf("clients: search client %s not found in config", name)
			continue
		}

		clientType := strings.ToLower(strings.TrimSpace(clientCfg.ClientType()))
		if clientType != "qbit" && clientType != "qbittorrent" && clientType != "qui" {
			s.logger.Debugf("clients: search client %s is not qBittorrent (type=%s)", name, clientType)
			continue
		}

		clientResult, matches, err := s.searchQbitClient(ctx, name, clientCfg, meta, constraints)
		if err != nil {
			return api.ClientSearchResult{}, err
		}
		s.logger.Tracef("clients: pathed search client %s results matches=%d trackerMatch=%t preferred=%q", name, len(matches), clientResult.FoundTrackerMatch, clientResult.FoundPreferredPiece)
		if len(matches) == 0 {
			if meta.Options.Debug {
				s.logger.Debugf("clients: no torrent matches found in %s", name)
			}
			continue
		}

		result.FoundTrackerMatch = result.FoundTrackerMatch || clientResult.FoundTrackerMatch
		result.MatchedTrackers = append(result.MatchedTrackers, clientResult.MatchedTrackers...)
		result.TorrentComments = append(result.TorrentComments, matches...)
		result.TrackerIDs = clientResult.TrackerIDs
		if clientResult.InfoHash != "" {
			result.InfoHash = clientResult.InfoHash
		}
		if clientResult.TorrentPath != "" {
			result.TorrentPath = clientResult.TorrentPath
		}
		result.FoundPreferredPiece = clientResult.FoundPreferredPiece

		for _, match := range matches {
			if _, exists := seenHashes[match.Hash]; exists {
				continue
			}
			seenHashes[match.Hash] = struct{}{}
			allMatches = append(allMatches, match)
		}

		if shouldStopSearch(constraints.label, clientResult.FoundPreferredPiece) {
			s.logger.Tracef("clients: stopping pathed search after %s (preferred=%q)", name, clientResult.FoundPreferredPiece)
			break
		}
	}

	if len(allMatches) == 0 {
		if meta.Options.Debug {
			s.logger.Debugf("clients: pathed search yielded no matches")
		}
		return result, nil
	}

	result.TorrentComments = allMatches
	result.MatchedTrackers = dedupeStrings(result.MatchedTrackers)
	if meta.Options.Debug {
		s.logger.Debugf("clients: pathed search found %d matches", len(allMatches))
	}

	return result, nil
}

func resolvePieceConstraints(cfg config.Config) pieceConstraints {
	mtv := false
	if trackerCfg, ok := cfg.Trackers.Trackers["MTV"]; ok {
		mtv = trackerCfg.PreferMTV
	}
	if mtv {
		return pieceConstraints{label: "MTV", preferSmall: true}
	}
	if cfg.TorrentCreation.PreferMax16 {
		return pieceConstraints{label: "16MiB", preferMax16: true}
	}
	return pieceConstraints{}
}

func resolveSearchClients(cfg config.Config, overrides api.ClientOverrides) ([]string, bool) {
	if overrides.Client != nil {
		requested := strings.TrimSpace(*overrides.Client)
		if requested == "" || strings.EqualFold(requested, "none") {
			return nil, false
		}
		for name := range cfg.TorrentClients {
			if strings.EqualFold(name, requested) {
				return []string{name}, false
			}
		}
		return nil, false
	}

	values := make([]string, 0)
	if len(cfg.ClientSetup.SearchClients) > 0 {
		values = append(values, cfg.ClientSetup.SearchClients...)
	} else if strings.TrimSpace(cfg.ClientSetup.DefaultClient) != "" {
		values = append(values, cfg.ClientSetup.DefaultClient)
	}

	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || strings.EqualFold(trimmed, "none") {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	if len(result) > 0 {
		return result, false
	}

	clientNames := make([]string, 0, len(cfg.TorrentClients))
	for name := range cfg.TorrentClients {
		clientNames = append(clientNames, name)
	}
	sort.Strings(clientNames)
	for _, name := range clientNames {
		clientType := strings.ToLower(strings.TrimSpace(cfg.TorrentClients[name].ClientType()))
		if clientType != "qbit" && clientType != "qbittorrent" && clientType != "qui" {
			continue
		}
		result = append(result, name)
	}

	return result, len(result) > 0
}

func (s *Service) searchQbitClient(ctx context.Context, name string, clientCfg config.TorrentClientConfig, meta api.PreparedMetadata, constraints pieceConstraints) (api.ClientSearchResult, []api.TorrentMatch, error) {
	searchTerm := buildSearchTerm(meta)
	if searchTerm == "" {
		s.logger.Debugf("clients: %s search term empty for source=%s", name, meta.SourcePath)
		return api.ClientSearchResult{}, nil, internalerrors.ErrInvalidInput
	}

	s.logger.Tracef("clients: searching qBittorrent client %s for %s", name, searchTerm)

	useProxy := clientCfg.UsesQuiProxy()
	var (
		qbitClient   *qbittorrent.Client
		httpClient   *http.Client
		proxyBaseURL string
	)

	if useProxy {
		proxyBaseURL = strings.TrimRight(strings.TrimSpace(clientCfg.QuiProxyURL), "/")
		if proxyBaseURL == "" {
			return api.ClientSearchResult{}, nil, fmt.Errorf("clients: %s proxy url is required", name)
		}
		s.logger.Tracef("clients: %s searching via qBittorrent proxy", name)
		httpClient = qbittorrent.NewClient(qbittorrent.Config{
			Host:          proxyBaseURL,
			Timeout:       int(proxySearchTimeout.Seconds()),
			TLSSkipVerify: clientCfg.QbitTLSSkipVerify(),
		}).GetHTTPClient()
	} else {
		host := strings.TrimSpace(clientCfg.QbitHost())
		if host == "" {
			return api.ClientSearchResult{}, nil, fmt.Errorf("clients: %s qbit host is required", name)
		}
		s.logger.Tracef("clients: %s searching via qBittorrent WebAPI host=%s", name, host)
		qbitClient = qbittorrent.NewClient(qbittorrent.Config{
			Host:          host,
			Username:      strings.TrimSpace(clientCfg.QbitUsername()),
			Password:      strings.TrimSpace(clientCfg.QbitPassword()),
			TLSSkipVerify: clientCfg.QbitTLSSkipVerify(),
		})
		if err := qbitClient.LoginCtx(ctx); err != nil {
			return api.ClientSearchResult{}, nil, fmt.Errorf("clients: %s qbit login: %w", name, err)
		}
	}

	var torrents []qbittorrent.Torrent
	if useProxy {
		items, err := searchProxyTorrents(ctx, httpClient, proxyBaseURL, searchTerm)
		if err != nil {
			return api.ClientSearchResult{}, nil, err
		}
		torrents = items
	} else {
		items, err := qbitClient.GetTorrentsCtx(ctx, qbittorrent.TorrentFilterOptions{
			Sort:    "added_on",
			Reverse: true,
			Limit:   100,
		})
		if err != nil {
			return api.ClientSearchResult{}, nil, fmt.Errorf("clients: %s qbit list: %w", name, err)
		}
		torrents = items
	}
	s.logger.Tracef("clients: %s fetched %d torrents", name, len(torrents))

	if len(torrents) == 0 {
		return api.ClientSearchResult{}, nil, nil
	}

	propertiesCache := make(map[string]qbittorrent.TorrentProperties)
	priorityOrder := effectiveTrackerPriority(s.cfg)
	matches := make([]api.TorrentMatch, 0)
	matchedTrackers := make([]string, 0)
	foundTrackerMatch := false
	nameMatched := 0

	for _, torrent := range torrents {
		select {
		case <-ctx.Done():
			return api.ClientSearchResult{}, nil, ctx.Err()
		default:
		}

		if !torrentNameMatches(torrent.Name, meta) {
			continue
		}
		nameMatched++

		comment := strings.TrimSpace(torrent.Comment)
		props, err := fetchTorrentProperties(ctx, qbitClient, httpClient, proxyBaseURL, torrent.Hash, useProxy)
		if err != nil {
			s.logger.Debugf("clients: %s properties lookup failed for %s: %v", name, torrent.Name, err)
		} else {
			propertiesCache[torrent.Hash] = props
			if comment == "" {
				comment = strings.TrimSpace(props.Comment)
			}
		}

		trackers, err := fetchTorrentTrackers(ctx, qbitClient, httpClient, proxyBaseURL, torrent.Hash, useProxy, torrent.Trackers)
		if err != nil {
			s.logger.Debugf("clients: %s trackers lookup failed for %s: %v", name, torrent.Name, err)
		}

		trackerURLs := collectTrackerURLs(torrent.Tracker, trackers)
		matchedTrackers = append(matchedTrackers, matchTrackerURLs(trackerURLs)...)

		hasWorkingTracker := useProxy
		if !useProxy {
			for _, tracker := range trackers {
				if tracker.Status == qbittorrent.TrackerStatusOK {
					hasWorkingTracker = true
					break
				}
			}
		}

		trackerMatches, trackerFound := extractTrackerMatches(comment, trackerURLs, hasWorkingTracker, priorityOrder)
		if trackerFound {
			foundTrackerMatch = true
		}

		match := api.TorrentMatch{
			Hash:              torrent.Hash,
			Name:              torrent.Name,
			SavePath:          torrent.SavePath,
			ContentPath:       torrent.ContentPath,
			Size:              torrent.Size,
			Category:          torrent.Category,
			Seeders:           torrent.NumComplete,
			Tracker:           torrent.Tracker,
			HasWorkingTracker: hasWorkingTracker,
			Comment:           comment,
			TrackerURLsRaw:    trackerURLs,
			TrackerURLs:       trackerMatches,
			HasTracker:        trackerFound,
		}
		matches = append(matches, match)
	}

	s.logger.Tracef("clients: %s name-matched %d of %d torrents", name, nameMatched, len(torrents))

	if len(matches) == 0 {
		s.logger.Debugf("clients: %s no matching torrent names (checked %d)", name, len(torrents))
		return api.ClientSearchResult{}, nil, nil
	}
	s.logger.Tracef("clients: %s matched %d torrents", name, len(matches))

	sortMatchingTorrents(matches, priorityOrder)

	bestMatch := matches[0]
	var foundPreferred string
	if constraints.preferSmall || constraints.preferMax16 {
		bestMatch, foundPreferred = selectPreferredMatch(ctx, matches, propertiesCache, qbitClient, httpClient, proxyBaseURL, useProxy, constraints)
	} else {
		foundPreferred = "no_constraints"
	}
	s.logger.Tracef("clients: %s selected hash %s (preferred=%q)", name, bestMatch.Hash, foundPreferred)

	trackerIDs := collectTrackerIDs(matches, priorityOrder)
	matchedTrackers = ensureMatchedTrackersForKnownIDs(matchedTrackers, trackerIDs)

	result := api.ClientSearchResult{
		InfoHash:            "",
		TrackerIDs:          trackerIDs,
		FoundTrackerMatch:   foundTrackerMatch,
		TorrentComments:     matches,
		PieceSizeConstraint: constraints.label,
		FoundPreferredPiece: foundPreferred,
		MatchedTrackers:     matchedTrackers,
	}

	validatedHash, torrentPath, err := s.selectValidTorrent(ctx, meta, matches, constraints, qbitClient, httpClient, proxyBaseURL, useProxy)
	if err != nil {
		return api.ClientSearchResult{}, nil, err
	}
	result.InfoHash = validatedHash
	result.TorrentPath = torrentPath

	return result, matches, nil
}

func buildSearchTerm(meta api.PreparedMetadata) string {
	base := pathutil.Base(meta.SourcePath)
	if base == "." || base == "/" {
		return ""
	}
	search := strings.ReplaceAll(base, "[", ".")
	search = strings.ReplaceAll(search, "]", ".")
	return search
}

func torrentNameMatches(name string, meta api.PreparedMetadata) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	base := pathutil.Base(meta.SourcePath)
	if meta.DiscType == "" && len(meta.FileList) == 1 {
		fileBase := pathutil.Base(meta.FileList[0])
		return strings.EqualFold(name, fileBase) || strings.EqualFold(name, base)
	}
	return strings.EqualFold(name, base)
}

func searchProxyTorrents(ctx context.Context, client *http.Client, proxyBase, searchTerm string) ([]qbittorrent.Torrent, error) {
	proxyURL, err := buildProxySearchURL(proxyBase, searchTerm)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, proxyURL, nil)
	if err != nil {
		return nil, fmt.Errorf("clients: proxy request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("clients: proxy search: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("clients: proxy search status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("clients: proxy search read: %w", err)
	}

	var torrents []qbittorrent.Torrent
	if err := json.Unmarshal(body, &torrents); err == nil {
		return torrents, nil
	}

	var wrapper proxySearchResponse
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, fmt.Errorf("clients: proxy search decode: %w", err)
	}

	return wrapper.Torrents, nil
}

func buildProxySearchURL(proxyBase, searchTerm string) (string, error) {
	parsed, err := url.Parse(proxyBase)
	if err != nil {
		return "", fmt.Errorf("clients: proxy url parse: %w", err)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/api/v2/torrents/search"
	query := parsed.Query()
	query.Set("search", searchTerm)
	query.Set("sort", "added_on")
	query.Set("reverse", "true")
	query.Set("limit", "100")
	query.Set("filter", "unregistered,tracker_down")
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func fetchTorrentProperties(ctx context.Context, client *qbittorrent.Client, httpClient *http.Client, proxyBase, hash string, useProxy bool) (qbittorrent.TorrentProperties, error) {
	if !useProxy {
		props, err := client.GetTorrentPropertiesCtx(ctx, hash)
		if err != nil {
			return qbittorrent.TorrentProperties{}, fmt.Errorf("clients: get qbit torrent properties: %w", err)
		}
		return props, nil
	}

	propertiesURL := strings.TrimRight(proxyBase, "/") + "/api/v2/torrents/properties"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, propertiesURL, nil)
	if err != nil {
		return qbittorrent.TorrentProperties{}, fmt.Errorf("clients: proxy properties request: %w", err)
	}
	q := req.URL.Query()
	q.Set("hash", hash)
	req.URL.RawQuery = q.Encode()

	resp, err := httpClient.Do(req)
	if err != nil {
		return qbittorrent.TorrentProperties{}, fmt.Errorf("clients: proxy properties: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return qbittorrent.TorrentProperties{}, qbittorrent.ErrTorrentNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return qbittorrent.TorrentProperties{}, fmt.Errorf("clients: proxy properties status %d", resp.StatusCode)
	}

	var props qbittorrent.TorrentProperties
	if err := json.NewDecoder(resp.Body).Decode(&props); err != nil {
		return qbittorrent.TorrentProperties{}, err
	}
	return props, nil
}

func fetchTorrentTrackers(ctx context.Context, client *qbittorrent.Client, httpClient *http.Client, proxyBase, hash string, useProxy bool, fallback []qbittorrent.TorrentTracker) ([]qbittorrent.TorrentTracker, error) {
	if useProxy {
		if len(fallback) > 0 {
			return fallback, nil
		}
		trackersURL := strings.TrimRight(proxyBase, "/") + "/api/v2/torrents/trackers"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, trackersURL, nil)
		if err != nil {
			return nil, fmt.Errorf("clients: proxy trackers request: %w", err)
		}
		q := req.URL.Query()
		q.Set("hash", hash)
		req.URL.RawQuery = q.Encode()
		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("clients: proxy trackers: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden {
			return nil, nil
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("clients: proxy trackers status %d", resp.StatusCode)
		}
		var trackers []qbittorrent.TorrentTracker
		if err := json.NewDecoder(resp.Body).Decode(&trackers); err != nil {
			return nil, err
		}
		return trackers, nil
	}

	trackers, err := client.GetTorrentTrackersCtx(ctx, hash)
	if err != nil {
		return nil, fmt.Errorf("clients: get qbit torrent trackers: %w", err)
	}
	return trackers, nil
}

func collectTrackerURLs(primary string, trackers []qbittorrent.TorrentTracker) []string {
	urls := make([]string, 0, len(trackers)+1)
	if strings.TrimSpace(primary) != "" {
		urls = append(urls, primary)
	}
	for _, tracker := range trackers {
		trimmed := strings.TrimSpace(tracker.Url)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "** [DHT]") || strings.HasPrefix(trimmed, "** [PeX]") || strings.HasPrefix(trimmed, "** [LSD]") {
			continue
		}
		urls = append(urls, trimmed)
	}
	return urls
}

func extractTrackerMatches(comment string, trackerURLs []string, hasWorkingTracker bool, priority []string) ([]api.TrackerMatch, bool) {
	matches := make([]api.TrackerMatch, 0)
	trackerFound := false
	lowerComment := strings.ToLower(comment)

	for _, trackerID := range priority {
		pattern, ok := trackerIDPatterns[trackerID]
		if !ok {
			continue
		}
		if !hasWorkingTracker || !strings.Contains(lowerComment, strings.ToLower(pattern.url)) {
			continue
		}
		match := pattern.pattern.FindStringSubmatch(comment)
		if len(match) < 2 {
			continue
		}
		matches = append(matches, api.TrackerMatch{ID: trackerID, TrackerID: match[1]})
		trackerFound = true
	}

	for _, url := range trackerURLs {
		lowerURL := strings.ToLower(url)
		if strings.Contains(lowerURL, "tracker.anthelion.me") {
			if hasWorkingTracker {
				matches = append(matches, api.TrackerMatch{ID: "ant", TrackerID: "1"})
				trackerFound = true
			}
		}
	}

	return matches, trackerFound
}

func matchTrackerURLs(trackerURLs []string) []string {
	found := make(map[string]struct{})
	for _, trackerURL := range trackerURLs {
		lowerURL := strings.ToLower(strings.TrimSpace(trackerURL))
		if lowerURL == "" {
			continue
		}
		for id, patterns := range lowerTrackerURLPatterns {
			for _, pattern := range patterns {
				if strings.Contains(lowerURL, pattern) {
					found[strings.ToUpper(id)] = struct{}{}
				}
			}
		}
	}
	return mapKeys(found)
}

func sortMatchingTorrents(matches []api.TorrentMatch, priority []string) {
	priorityIndex := make(map[string]int, len(priority))
	for idx, id := range priority {
		priorityIndex[id] = idx
	}

	sort.Slice(matches, func(i, j int) bool {
		left := matches[i]
		right := matches[j]

		leftPriority := trackerPriorityScore(left, priorityIndex)
		rightPriority := trackerPriorityScore(right, priorityIndex)

		if left.HasWorkingTracker != right.HasWorkingTracker {
			return left.HasWorkingTracker
		}
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		if left.HasTracker != right.HasTracker {
			return left.HasTracker
		}
		return left.Seeders > right.Seeders
	})
}

func trackerPriorityScore(match api.TorrentMatch, priorityIndex map[string]int) int {
	score := 100
	for _, tracker := range match.TrackerURLs {
		if idx, ok := priorityIndex[tracker.ID]; ok {
			if idx < score {
				score = idx
			}
		}
	}
	return score
}

func collectTrackerIDs(matches []api.TorrentMatch, priority []string) map[string]string {
	trackerIDs := make(map[string]string)
	if len(matches) == 0 {
		return trackerIDs
	}

	for _, preferred := range priority {
		if _, ok := trackerIDs[preferred]; ok {
			continue
		}
		for _, match := range matches {
			for _, tracker := range match.TrackerURLs {
				if tracker.ID == "" || tracker.TrackerID == "" {
					continue
				}
				if !strings.EqualFold(tracker.ID, preferred) {
					continue
				}
				trackerIDs[strings.ToLower(tracker.ID)] = tracker.TrackerID
				break
			}
			if _, ok := trackerIDs[preferred]; ok {
				break
			}
		}
	}

	for _, match := range matches {
		for _, tracker := range match.TrackerURLs {
			if tracker.ID == "" || tracker.TrackerID == "" {
				continue
			}
			key := strings.ToLower(tracker.ID)
			if _, ok := trackerIDs[key]; ok {
				continue
			}
			trackerIDs[key] = tracker.TrackerID
		}
	}

	return trackerIDs
}

func ensureMatchedTrackersForKnownIDs(matchedTrackers []string, trackerIDs map[string]string) []string {
	if len(trackerIDs) == 0 {
		return matchedTrackers
	}
	resolved := append([]string{}, matchedTrackers...)
	for tracker, id := range trackerIDs {
		if strings.TrimSpace(id) == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(tracker), "btn") && !hasMatchedTracker(resolved, "BTN") {
			resolved = append(resolved, "BTN")
		}
	}
	return resolved
}

func hasMatchedTracker(trackers []string, target string) bool {
	for _, tracker := range trackers {
		if strings.EqualFold(strings.TrimSpace(tracker), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}

func effectiveTrackerPriority(cfg config.Config) []string {
	return applyPreferredTrackerPriority(trackers.TrackerPriority(), cfg.Trackers.PreferredTracker)
}

func applyPreferredTrackerPriority(priority []string, preferred string) []string {
	if len(priority) == 0 {
		return nil
	}

	ordered := make([]string, len(priority))
	copy(ordered, priority)

	preferred = strings.ToLower(strings.TrimSpace(preferred))
	if preferred == "" {
		return ordered
	}

	preferredIndex := -1
	for idx, tracker := range ordered {
		if strings.EqualFold(strings.TrimSpace(tracker), preferred) {
			preferredIndex = idx
			break
		}
	}
	if preferredIndex <= 0 {
		return ordered
	}

	selected := ordered[preferredIndex]
	copy(ordered[1:preferredIndex+1], ordered[0:preferredIndex])
	ordered[0] = selected
	return ordered
}

func selectPreferredMatch(
	ctx context.Context,
	matches []api.TorrentMatch,
	propertiesCache map[string]qbittorrent.TorrentProperties,
	qbitClient *qbittorrent.Client,
	httpClient *http.Client,
	proxyBase string,
	useProxy bool,
	constraints pieceConstraints,
) (api.TorrentMatch, string) {
	best := matches[0]
	bestPiece := 0

	getPieceSize := func(hash string) (int, error) {
		if props, ok := propertiesCache[hash]; ok {
			return props.PieceSize, nil
		}
		props, err := fetchTorrentProperties(ctx, qbitClient, httpClient, proxyBase, hash, useProxy)
		if err != nil {
			return 0, err
		}
		propertiesCache[hash] = props
		return props.PieceSize, nil
	}

	for _, match := range matches {
		pieceSize, err := getPieceSize(match.Hash)
		if err != nil || pieceSize <= 0 {
			continue
		}

		if bestPiece == 0 {
			best = match
			bestPiece = pieceSize
			continue
		}

		if constraints.preferSmall || constraints.preferMax16 {
			if shouldReplaceBest(int64(pieceSize), int64(bestPiece), constraints) {
				best = match
				bestPiece = pieceSize
			}
			continue
		}
	}

	if bestPiece == 0 {
		return matches[0], ""
	}

	if constraints.preferSmall {
		if bestPiece <= mtvPieceSizeLimit {
			return best, "MTV"
		}
		return best, ""
	}
	if constraints.preferMax16 {
		if bestPiece <= max16PieceSize {
			return best, "16MiB"
		}
		return best, ""
	}
	return best, ""
}

func shouldStopSearch(constraints string, foundPreferred string) bool {
	if constraints == "" {
		return foundPreferred == "no_constraints"
	}
	if foundPreferred == "no_constraints" {
		return true
	}
	if foundPreferred == "MTV" {
		return true
	}
	if foundPreferred == "16MiB" && constraints == "16MiB" {
		return true
	}
	return false
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func (s *Service) selectValidTorrent(
	ctx context.Context,
	meta api.PreparedMetadata,
	matches []api.TorrentMatch,
	constraints pieceConstraints,
	qbitClient *qbittorrent.Client,
	httpClient *http.Client,
	proxyBase string,
	useProxy bool,
) (string, string, error) {
	if len(matches) == 0 {
		return "", "", nil
	}
	tmpRoot, err := db.Subdir(s.cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		return "", "", fmt.Errorf("clients: tmp dir: %w", err)
	}

	bestHash := ""
	bestPath := ""
	var bestData []byte
	var bestPiece int64
	rechecked := make(map[string]struct{})

	for _, match := range matches {
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		default:
		}

		normalizedHash := normalizeQbitHash(match.Hash)
		if normalizedHash == "" {
			continue
		}
		if shouldForceRecheck(meta.ClientOverrides) {
			if useProxy || qbitClient == nil {
				s.logger.Debugf("clients: force-recheck requested for %s but direct qBittorrent access is unavailable", normalizedHash)
			} else if _, ok := rechecked[normalizedHash]; !ok {
				if err := forceRecheckTorrent(ctx, qbitClient, normalizedHash, s.logger); err != nil {
					return "", "", err
				}
				rechecked[normalizedHash] = struct{}{}
			}
		}

		outputPath, err := torrent.TempTorrentPath(tmpRoot, meta, meta.SourcePath)
		if err != nil {
			return "", "", err
		}

		if info, err := os.Stat(outputPath); err == nil && !info.IsDir() {
			data, err := os.ReadFile(outputPath)
			if err == nil {
				valid, pieceSize, infoHash, reason := validateTorrentData(meta, normalizedHash, data, constraints)
				if valid && strings.EqualFold(infoHash, normalizedHash) {
					s.logger.Debugf("clients: validated existing torrent for %s (piece=%d)", normalizedHash, pieceSize)
					if shouldSelectPreferred(pieceSize, constraints) {
						return normalizedHash, outputPath, nil
					}
					if shouldReplaceBest(pieceSize, bestPiece, constraints) {
						bestHash = normalizedHash
						bestPath = outputPath
						bestPiece = pieceSize
						bestData = nil
					}
					continue
				}
				if reason != "" {
					s.logger.Debugf("clients: existing torrent failed validation for %s: %s", normalizedHash, reason)
				}
			}
		}

		data, err := exportTorrent(ctx, qbitClient, httpClient, proxyBase, normalizedHash, useProxy)
		if err != nil {
			s.logger.Debugf("clients: export torrent failed for %s: %v", normalizedHash, err)
			continue
		}
		if len(data) == 0 {
			s.logger.Debugf("clients: empty torrent export for %s", normalizedHash)
			continue
		}

		valid, pieceSize, infoHash, reason := validateTorrentData(meta, normalizedHash, data, constraints)
		if !valid {
			if reason != "" {
				s.logger.Debugf("clients: exported torrent failed validation for %s: %s", normalizedHash, reason)
			}
			continue
		}
		if !strings.EqualFold(infoHash, normalizedHash) {
			s.logger.Debugf("clients: exported torrent infohash mismatch for %s", normalizedHash)
			continue
		}
		s.logger.Tracef("clients: validated exported torrent for %s (piece=%d)", normalizedHash, pieceSize)

		if shouldSelectPreferred(pieceSize, constraints) {
			path, err := writeTorrentFile(outputPath, data)
			if err != nil {
				return "", "", err
			}
			return normalizedHash, path, nil
		}
		if shouldReplaceBest(pieceSize, bestPiece, constraints) {
			bestHash = normalizedHash
			bestPiece = pieceSize
			bestData = data
			bestPath = outputPath
		}
	}

	if bestHash == "" {
		return "", "", nil
	}
	if bestData != nil {
		path, err := writeTorrentFile(bestPath, bestData)
		if err != nil {
			return "", "", err
		}
		return bestHash, path, nil
	}
	return bestHash, bestPath, nil
}

func shouldForceRecheck(overrides api.ClientOverrides) bool {
	return overrides.ForceRecheck != nil && *overrides.ForceRecheck
}

func forceRecheckTorrent(ctx context.Context, client *qbittorrent.Client, hash string, logger api.Logger) error {
	if client == nil || strings.TrimSpace(hash) == "" {
		return nil
	}
	if logger != nil {
		logger.Debugf("clients: forcing qBittorrent recheck for %s", hash)
	}
	if err := client.RecheckCtx(ctx, []string{hash}); err != nil {
		return fmt.Errorf("clients: qbit recheck %s: %w", hash, err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		torrents, err := client.GetTorrentsCtx(waitCtx, qbittorrent.TorrentFilterOptions{
			Hashes: []string{hash},
			Limit:  1,
		})
		if err != nil {
			return fmt.Errorf("clients: qbit recheck status %s: %w", hash, err)
		}
		if len(torrents) == 0 || !qbitTorrentChecking(torrents[0].State) {
			if logger != nil {
				logger.Debugf("clients: qBittorrent recheck completed for %s", hash)
			}
			return nil
		}

		select {
		case <-waitCtx.Done():
			return fmt.Errorf("clients: qbit recheck %s timed out: %w", hash, waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func qbitTorrentChecking(state qbittorrent.TorrentState) bool {
	//nolint:exhaustive // This helper only classifies the checking states.
	switch state {
	case qbittorrent.TorrentStateCheckingUp,
		qbittorrent.TorrentStateCheckingDl,
		qbittorrent.TorrentStateCheckingResumeData:
		return true
	default:
		return false
	}
}

func normalizeQbitHash(hash string) string {
	return strings.ToLower(strings.TrimSpace(hash))
}

func exportTorrent(ctx context.Context, qbitClient *qbittorrent.Client, httpClient *http.Client, proxyBase, hash string, useProxy bool) ([]byte, error) {
	if !useProxy {
		if qbitClient == nil {
			return nil, errors.New("clients: qbit client is required")
		}
		data, err := qbitClient.ExportTorrentCtx(ctx, hash)
		if err != nil {
			return nil, fmt.Errorf("clients: export qbit torrent: %w", err)
		}
		return data, nil
	}

	proxyURL := strings.TrimRight(proxyBase, "/") + "/api/v2/torrents/export"
	data := url.Values{}
	data.Set("hash", hash)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, proxyURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("clients: proxy export request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("clients: proxy export: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("clients: proxy export status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("clients: read proxy export body: %w", err)
	}
	return body, nil
}

func writeTorrentFile(path string, data []byte) (string, error) {
	sanitized, err := sanitizeTorrentData(data)
	if err != nil {
		return "", fmt.Errorf("clients: sanitize torrent: %w", err)
	}
	if err := os.WriteFile(path, sanitized, 0o600); err != nil {
		return "", fmt.Errorf("clients: write torrent: %w", err)
	}
	return path, nil
}

func sanitizeTorrentData(data []byte) ([]byte, error) {
	metaInfo, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("clients: parse torrent metadata: %w", err)
	}
	metaInfo.Comment = ""
	metaInfo.Announce = ""
	metaInfo.AnnounceList = nil
	metaInfo.UrlList = nil

	var buf bytes.Buffer
	if err := metaInfo.Write(&buf); err != nil {
		return nil, fmt.Errorf("clients: write sanitized torrent metadata: %w", err)
	}
	return buf.Bytes(), nil
}

func validateTorrentData(meta api.PreparedMetadata, hash string, data []byte, constraints pieceConstraints) (bool, int64, string, string) {
	metaInfo, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		return false, 0, "", "load_failed"
	}
	info, err := metaInfo.UnmarshalInfo()
	if err != nil {
		return false, 0, "", "info_unmarshal_failed"
	}
	infoHash := metaInfo.HashInfoBytes().String()
	if strings.TrimSpace(hash) != "" && !strings.EqualFold(infoHash, hash) {
		return false, 0, infoHash, "hash_mismatch"
	}

	valid, wrongFile := validateTorrentPaths(meta, info)
	if !valid || wrongFile {
		return false, 0, infoHash, "path_mismatch"
	}

	pieceSize := info.PieceLength
	pieces := info.NumPieces()
	if pieceSize <= 0 || pieces <= 0 {
		return false, pieceSize, infoHash, "piece_metadata_invalid"
	}

	if invalidPieceConstraints(constraints, pieceSize, pieces, int64(len(data)), wrongFile) {
		return false, pieceSize, infoHash, "piece_constraints"
	}

	return true, pieceSize, infoHash, ""
}

func validateTorrentPaths(meta api.PreparedMetadata, info metainfo.Info) (bool, bool) {
	fileList := meta.FileList
	metaPath := strings.TrimSpace(meta.SourcePath)
	wrongFile := false

	torrentFiles := buildTorrentFileList(info)
	if len(torrentFiles) == 0 {
		return false, false
	}

	if meta.DiscType != "" {
		common := commonPath(torrentFiles)
		base := pathutil.Base(metaPath)
		if base != "" && strings.Contains(strings.ToLower(common), strings.ToLower(base)) {
			return true, false
		}
		return false, false
	}

	if len(torrentFiles) == 1 && len(fileList) == 1 {
		if strings.EqualFold(pathutil.Base(torrentFiles[0]), pathutil.Base(fileList[0])) {
			if pathutil.Base(torrentFiles[0]) == torrentFiles[0] {
				return true, false
			}
			wrongFile = true
		}
		return false, wrongFile
	}

	if len(torrentFiles) == len(fileList) && len(fileList) > 0 {
		torrentCommon := commonPath(torrentFiles)
		actualCommon := commonPath(fileList)
		if strings.Contains(strings.ToLower(actualCommon), strings.ToLower(torrentCommon)) {
			return true, false
		}
	}

	return false, false
}

func buildTorrentFileList(info metainfo.Info) []string {
	files := info.UpvertedFiles()
	if len(files) == 0 {
		return nil
	}
	root := info.BestName()
	result := make([]string, 0, len(files))
	for _, file := range files {
		parts := file.BestPath()
		if len(parts) == 0 {
			result = append(result, root)
			continue
		}
		full := append([]string{root}, parts...)
		result = append(result, filepath.Join(full...))
	}
	return result
}

func invalidPieceConstraints(constraints pieceConstraints, pieceSize int64, pieces int, torrentSize int64, wrongFile bool) bool {
	if pieces >= 5000 && pieceSize < 4294304 {
		return true
	}
	if pieces >= 8000 && pieceSize < 8488608 && !constraints.preferSmall {
		return true
	}
	if pieces >= 12000 {
		return true
	}
	if pieceSize < 32768 {
		return true
	}
	if torrentSize > 250*1024 {
		return true
	}
	if wrongFile {
		return true
	}
	return false
}

func commonPath(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	parts := splitPath(paths[0])
	for _, value := range paths[1:] {
		candidate := splitPath(value)
		limit := len(parts)
		if len(candidate) < limit {
			limit = len(candidate)
		}
		idx := 0
		for idx < limit {
			if !strings.EqualFold(parts[idx], candidate[idx]) {
				break
			}
			idx++
		}
		parts = parts[:idx]
		if len(parts) == 0 {
			return ""
		}
	}
	return strings.Join(parts, "/")
}

func splitPath(value string) []string {
	cleaned := filepath.ToSlash(filepath.Clean(value))
	trimmed := strings.Trim(cleaned, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

func shouldSelectPreferred(pieceSize int64, constraints pieceConstraints) bool {
	if !constraints.preferSmall && !constraints.preferMax16 {
		return true
	}
	if constraints.preferSmall {
		return pieceSize <= mtvPieceSizeLimit
	}
	if constraints.preferMax16 {
		return pieceSize <= max16PieceSize
	}
	return false
}

func shouldReplaceBest(pieceSize int64, bestPiece int64, constraints pieceConstraints) bool {
	if bestPiece == 0 {
		return true
	}
	if constraints.preferSmall {
		return pieceSize < bestPiece
	}
	if constraints.preferMax16 {
		bestFits := bestPiece <= max16PieceSize
		candidateFits := pieceSize <= max16PieceSize
		if candidateFits && (!bestFits || pieceSize < bestPiece) {
			return true
		}
		if !bestFits && !candidateFits && pieceSize < bestPiece {
			return true
		}
		return false
	}
	return pieceSize < bestPiece
}

func mapKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}
