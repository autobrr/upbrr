// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrentclient

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
	"unicode"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/torrent"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"

	"github.com/anacrolix/torrent/metainfo"

	qbittorrent "github.com/autobrr/go-qbittorrent"
)

const (
	mtvPieceSizeLimit  = 8 * 1024 * 1024
	max16PieceSize     = 16 * 1024 * 1024
	proxySearchTimeout = 15 * time.Second
)

// trackerPattern associates a tracker endpoint with the expression used to
// extract its torrent identifier.
type trackerPattern struct {
	trackerURLs      []string
	commentURLs      []string
	pattern          *regexp.Regexp
	workingTrackerID string
	inferMatchFromID bool
}

func buildTrackerIDPatterns(registry *trackers.Registry) map[string]trackerPattern {
	patterns := make(map[string]trackerPattern)
	for _, tracker := range registry.Names() {
		policy, ok := registry.LookupTorrentIdentityPolicy(tracker)
		if !ok {
			continue
		}
		var pattern *regexp.Regexp
		if policy.DetailIDPattern != "" {
			pattern = regexp.MustCompile(policy.DetailIDPattern)
		}
		patterns[strings.ToLower(tracker)] = trackerPattern{
			trackerURLs:      append([]string(nil), policy.TrackerURLPatterns...),
			commentURLs:      append([]string(nil), policy.CommentURLPatterns...),
			pattern:          pattern,
			workingTrackerID: policy.WorkingTrackerID,
			inferMatchFromID: policy.InferMatchFromResolvedID,
		}
	}
	return patterns
}

func trackerSearchPreferenceNames(registry *trackers.Registry, preference trackers.TorrentSearchPreference) []string {
	names := make([]string, 0)
	for _, tracker := range registry.Names() {
		policy, ok := registry.LookupTorrentIdentityPolicy(tracker)
		if ok && policy.SearchPreference == preference {
			names = append(names, tracker)
		}
	}
	return names
}

// proxySearchResponse models the torrent list returned by the Qui search proxy.
type proxySearchResponse struct {
	Torrents []qbittorrent.Torrent `json:"torrents"`
}

// pieceConstraints describes tracker-specific preferences used when selecting
// a reusable torrent from validated client matches.
type pieceConstraints struct {
	label       string
	preferSmall bool
	preferMax16 bool
}

// validatedTorrentSelection records validated client matches and the single
// torrent file, if any, that is safe to reuse for the searched source.
type validatedTorrentSelection struct {
	infoHash       string
	torrentPath    string
	foundPreferred string
	matches        []api.TorrentMatch
}

// torrentDataValidation classifies exported torrent metadata separately for
// tracker provenance and for saving as this source's reusable torrent file.
type torrentDataValidation struct {
	// reusable means the exported torrent file can be saved and used for this source.
	reusable bool
	// clientMatch means the client torrent is valid for tracker provenance. Some
	// path mismatches intentionally set this without allowing torrent-file reuse.
	clientMatch bool
	pieceSize   int64
	infoHash    string
	reason      string
}

// SearchPathedTorrents searches selected qBittorrent or Qui clients in
// configured order, validates exported metainfo against meta, and stops once
// the active piece-size preference is satisfied. Match lists are deduplicated
// across clients; per-client scalar results are replaced as later matching
// clients are evaluated.
//
// A reusable export is stripped of announce, comment, and web-seed metadata and
// written with mode 0600 below the configured DB temporary directory. A requested
// force recheck also mutates direct qBittorrent state and waits for checking to
// finish. Client-level failures abort the search; individual property or tracker
// lookup failures degrade that candidate, while export failures reject only the
// affected candidate.
func (s *Service) SearchPathedTorrents(ctx context.Context, meta api.ClientSubject) (result api.ClientSearchResult, err error) {
	defer func() {
		if err != nil {
			s.logger.Warnf("clients: pathed search blocked err=%s", redaction.RedactValue(err.Error(), nil))
		}
	}()

	if strings.TrimSpace(meta.SourcePath) == "" {
		return api.ClientSearchResult{}, internalerrors.ErrInvalidInput
	}

	constraints := resolvePieceConstraints(s.cfg, s.smallPieceTrackers)
	result = api.ClientSearchResult{PieceSizeConstraint: constraints.label}
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
	s.logger.Debugf("clients: pathed search for %s (clients=%d constraints=%q)", meta.SourcePath, len(clients), constraints.label)

	allMatches := make([]api.TorrentMatch, 0)
	seenHashes := make(map[string]struct{})

	for _, name := range clients {
		select {
		case <-ctx.Done():
			return api.ClientSearchResult{}, fmt.Errorf("context canceled: %w", ctx.Err())
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
		s.logger.Tracef(
			"clients: pathed search client %s results matches=%d trackerMatch=%t preferred=%q",
			name,
			len(matches),
			clientResult.FoundTrackerMatch,
			clientResult.FoundPreferredPiece,
		)
		if len(matches) == 0 {
			s.logger.Debugf("clients: no torrent matches found in %s", name)
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
		s.logger.Debugf("clients: pathed search yielded no matches")
		return result, nil
	}

	result.TorrentComments = allMatches
	result.MatchedTrackers = dedupeStrings(result.MatchedTrackers)
	s.logger.Debugf("clients: pathed search found %d matches", len(allMatches))
	logPathedSearchMatches(s.logger, allMatches)

	return result, nil
}

func logPathedSearchMatches(logger api.Logger, matches []api.TorrentMatch) {
	if logger == nil {
		return
	}
	for idx, match := range matches {
		logger.Debugf(
			"clients: pathed search match #%d name=%q hash=%s size=%d seeders=%d category=%q working_tracker=%t tracker_ids=%q save_path=%q content_path=%q",
			idx+1,
			match.Name,
			normalizeQbitHash(match.Hash),
			match.Size,
			match.Seeders,
			match.Category,
			match.HasWorkingTracker,
			formatTrackerMatches(match.TrackerURLs),
			match.SavePath,
			match.ContentPath,
		)
	}
}

func formatTrackerMatches(matches []api.TrackerMatch) string {
	if len(matches) == 0 {
		return ""
	}
	formatted := make([]string, 0, len(matches))
	for _, match := range matches {
		id := strings.ToUpper(strings.TrimSpace(match.ID))
		if id == "" {
			continue
		}
		trackerID := strings.TrimSpace(match.TrackerID)
		if trackerID != "" {
			formatted = append(formatted, id+":"+trackerID)
			continue
		}
		formatted = append(formatted, id)
	}
	sort.Strings(formatted)
	return strings.Join(formatted, ",")
}

func resolvePieceConstraints(cfg config.Config, smallPieceTrackers []string) pieceConstraints {
	for _, tracker := range smallPieceTrackers {
		trackerCfg, ok := cfg.Trackers.Trackers[tracker]
		if !ok {
			for name, candidate := range cfg.Trackers.Trackers {
				if strings.EqualFold(strings.TrimSpace(name), tracker) {
					trackerCfg, ok = candidate, true
					break
				}
			}
		}
		if ok && trackerCfg.PreferMTV {
			return pieceConstraints{label: strings.ToUpper(tracker), preferSmall: true}
		}
	}
	if cfg.TorrentCreation.PreferMax16 {
		return pieceConstraints{label: "16MiB", preferMax16: true}
	}
	return pieceConstraints{}
}

// resolveSearchClients returns configured qbit/qui client names for pathed
// search. Explicit overrides, searching_client_list, and default_torrent_client
// are authoritative after normalization. Blank search list entries do not block
// default_torrent_client, while "none" disables implicit fallback. The boolean
// reports only whether the implicit all qbit/qui fallback was used.
func resolveSearchClients(cfg config.Config, overrides api.ClientOverrides) ([]string, bool) {
	if overrides.Client != nil {
		requested := strings.TrimSpace(*overrides.Client)
		if requested == "" || strings.EqualFold(requested, "none") {
			return nil, false
		}
		if name, _, ok := lookupTorrentClientConfig(cfg.TorrentClients, requested); ok {
			return []string{name}, false
		}
		return nil, false
	}

	values := make([]string, 0)
	if hasSearchDisableSelector(cfg.ClientSetup.SearchClients) {
		return selectSearchClientNames(cfg.TorrentClients, cfg.ClientSetup.SearchClients), false
	}
	if hasNonBlankSearchSelector(cfg.ClientSetup.SearchClients) {
		values = append(values, cfg.ClientSetup.SearchClients...)
	} else if strings.TrimSpace(cfg.ClientSetup.DefaultClient) != "" {
		values = append(values, cfg.ClientSetup.DefaultClient)
	}

	if hasSearchDisableSelector(values) {
		return selectSearchClientNames(cfg.TorrentClients, values), false
	}
	if hasNonBlankSearchSelector(values) {
		return selectSearchClientNames(cfg.TorrentClients, values), false
	}

	clientNames := make([]string, 0, len(cfg.TorrentClients))
	for name := range cfg.TorrentClients {
		clientNames = append(clientNames, name)
	}
	sort.Strings(clientNames)
	result := make([]string, 0, len(clientNames))
	for _, name := range clientNames {
		clientType := strings.ToLower(strings.TrimSpace(cfg.TorrentClients[name].ClientType()))
		if clientType != "qbit" && clientType != "qbittorrent" && clientType != "qui" {
			continue
		}
		result = append(result, name)
	}

	return result, len(result) > 0
}

// hasNonBlankSearchSelector reports whether configured search selectors should
// suppress lower-priority selectors after blank and "none" entries are ignored.
func hasNonBlankSearchSelector(selected []string) bool {
	for _, value := range selected {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" && !strings.EqualFold(trimmed, "none") {
			return true
		}
	}
	return false
}

// hasSearchDisableSelector reports whether a selector list explicitly disables
// search. "none" is authoritative when no usable client selector accompanies it.
func hasSearchDisableSelector(selected []string) bool {
	hasNone := false
	for _, value := range selected {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if !strings.EqualFold(trimmed, "none") {
			return false
		}
		hasNone = true
	}
	return hasNone
}

// selectSearchClientNames resolves configured search selectors to canonical map
// keys. Unknown and ambiguous selectors are ignored rather than returned as raw
// names for callers to skip later.
func selectSearchClientNames(clients map[string]config.TorrentClientConfig, selected []string) []string {
	if len(clients) == 0 || len(selected) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(selected))
	result := make([]string, 0, len(selected))
	for _, value := range selected {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || strings.EqualFold(trimmed, "none") {
			continue
		}
		name, _, ok := lookupTorrentClientConfig(clients, trimmed)
		if !ok {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result
}

func (s *Service) searchQbitClient(
	ctx context.Context,
	name string,
	clientCfg config.TorrentClientConfig,
	meta api.ClientSubject,
	constraints pieceConstraints,
) (api.ClientSearchResult, []api.TorrentMatch, error) {
	searchTerms := buildSearchTerms(meta)
	if len(searchTerms) == 0 {
		s.logger.Debugf("clients: %s search term empty for source=%s", name, meta.SourcePath)
		return api.ClientSearchResult{}, nil, internalerrors.ErrInvalidInput
	}

	s.logger.Tracef("clients: searching qBittorrent client %s for %s", name, strings.Join(searchTerms, ", "))

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
		items, err := searchProxyTorrentTerms(ctx, httpClient, proxyBaseURL, searchTerms)
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

	priorityOrder := effectiveTrackerPriority(s.cfg, s.trackerPriority)
	matches := make([]api.TorrentMatch, 0)
	nameMatched := 0

	for _, torrent := range torrents {
		select {
		case <-ctx.Done():
			return api.ClientSearchResult{}, nil, fmt.Errorf("context canceled: %w", ctx.Err())
		default:
		}

		if !torrentMatchesMeta(torrent, meta) {
			continue
		}
		nameMatched++

		comment := strings.TrimSpace(torrent.Comment)
		props, err := fetchTorrentProperties(ctx, qbitClient, httpClient, proxyBaseURL, torrent.Hash, useProxy)
		if err != nil {
			s.logger.Debugf("clients: %s properties lookup failed for %s: %v", name, torrent.Name, err)
		} else if comment == "" {
			comment = strings.TrimSpace(props.Comment)
		}

		trackers, err := fetchTorrentTrackers(ctx, qbitClient, httpClient, proxyBaseURL, torrent.Hash, useProxy, torrent.Trackers)
		if err != nil {
			s.logger.Debugf("clients: %s trackers lookup failed for %s: %v", name, torrent.Name, err)
		}

		trackerURLs := collectTrackerURLs(torrent.Tracker, trackers)

		hasWorkingTracker := useProxy
		if !useProxy {
			for _, tracker := range trackers {
				if tracker.Status == qbittorrent.TrackerStatusOK {
					hasWorkingTracker = true
					break
				}
			}
		}

		trackerMatches, trackerFound := extractTrackerMatchesWithPatterns(comment, trackerURLs, hasWorkingTracker, priorityOrder, s.trackerPatterns)

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

	selection, err := s.selectValidTorrent(ctx, meta, matches, constraints, qbitClient, httpClient, proxyBaseURL, useProxy)
	if err != nil {
		return api.ClientSearchResult{}, nil, err
	}
	s.logger.Tracef("clients: %s validated %d of %d matched torrents", name, len(selection.matches), len(matches))

	trackerIDs := collectTrackerIDs(selection.matches, priorityOrder)
	matchedTrackers := collectMatchedTrackers(selection.matches, s.trackerPatterns)
	matchedTrackers = ensureMatchedTrackersForKnownIDs(matchedTrackers, trackerIDs, s.trackerPatterns)

	result := api.ClientSearchResult{
		InfoHash:            selection.infoHash,
		TrackerIDs:          trackerIDs,
		FoundTrackerMatch:   hasTrackerIDMatch(selection.matches),
		TorrentComments:     selection.matches,
		PieceSizeConstraint: constraints.label,
		FoundPreferredPiece: selection.foundPreferred,
		MatchedTrackers:     matchedTrackers,
		TorrentPath:         selection.torrentPath,
	}

	return result, selection.matches, nil
}

// buildSearchTerms returns distinct sanitized qBittorrent search terms for the
// source path, including extensionless single-file variants when safe.
func buildSearchTerms(meta api.ClientSubject) []string {
	terms := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)
	add := func(base string, allowExtensionless bool) {
		addSearchTerm(&terms, seen, base)
		if !allowExtensionless {
			return
		}
		extensionless := extensionlessSearchBase(base)
		if extensionless == "" {
			return
		}
		addSearchTerm(&terms, seen, extensionless)
	}

	sourceBase := pathutil.Base(meta.SourcePath)
	fileBase := ""
	singleFile := meta.DiscType == "" && len(meta.FileList) == 1
	if singleFile {
		fileBase = pathutil.Base(meta.FileList[0])
	}

	allowSourceExtensionless := meta.DiscType == "" && (!singleFile || strings.EqualFold(sourceBase, fileBase))
	add(sourceBase, allowSourceExtensionless)
	if singleFile && !strings.EqualFold(sourceBase, fileBase) {
		add(fileBase, true)
	}

	return terms
}

func addSearchTerm(terms *[]string, seen map[string]struct{}, base string) {
	term := normalizeSearchBase(base)
	if term == "" {
		return
	}
	key := strings.ToLower(term)
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	*terms = append(*terms, term)
}

func normalizeSearchBase(base string) string {
	if base == "." || base == "/" {
		return ""
	}
	search := strings.ReplaceAll(base, "[", ".")
	search = strings.ReplaceAll(search, "]", ".")
	return sanitizeSearchTerm(search)
}

// extensionlessSearchBase strips a file-like extension from base. It leaves
// release names with non-extension suffixes unchanged.
func extensionlessSearchBase(base string) string {
	trimmed := strings.TrimSpace(base)
	ext := filepath.Ext(trimmed)
	if !looksLikeFileExtension(ext) {
		return ""
	}
	extensionless := strings.TrimSpace(strings.TrimSuffix(trimmed, ext))
	if extensionless == "" || extensionless == trimmed {
		return ""
	}
	return extensionless
}

func looksLikeFileExtension(ext string) bool {
	trimmed := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ext)), ".")
	if len(trimmed) < 2 || len(trimmed) > 8 {
		return false
	}
	hasLetter := false
	for idx, r := range trimmed {
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
		case unicode.IsDigit(r) && idx > 0:
		default:
			return false
		}
	}
	return hasLetter
}

func sanitizeSearchTerm(value string) string {
	var builder strings.Builder
	for _, r := range value {
		if unicode.IsSymbol(r) {
			continue
		}
		builder.WriteRune(r)
	}
	return strings.TrimSpace(builder.String())
}

func torrentMatchesMeta(torrent qbittorrent.Torrent, meta api.ClientSubject) bool {
	if torrentNameMatches(torrent.Name, meta) {
		return true
	}
	contentBase := pathutil.Base(torrent.ContentPath)
	return contentBase != "" && torrentNameMatches(contentBase, meta)
}

func torrentNameMatches(name string, meta api.ClientSubject) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	base := pathutil.Base(meta.SourcePath)
	if meta.DiscType == "" && len(meta.FileList) == 1 {
		fileBase := pathutil.Base(meta.FileList[0])
		return torrentNameEquals(name, fileBase) ||
			torrentNameEquals(name, base) ||
			torrentNameEqualsExtensionless(name, fileBase) ||
			torrentNameEqualsExtensionless(name, base)
	}
	if meta.DiscType == "" && len(meta.FileList) == 0 {
		return torrentNameEquals(name, base) || torrentNameEqualsExtensionless(name, base)
	}
	return torrentNameEquals(name, base)
}

func torrentNameEquals(left string, right string) bool {
	if strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right)) {
		return true
	}
	leftCanonical := canonicalTorrentName(left)
	rightCanonical := canonicalTorrentName(right)
	return leftCanonical != "" && rightCanonical != "" && leftCanonical == rightCanonical
}

func torrentNameEqualsExtensionless(name string, sourceBase string) bool {
	sourceExtensionless := extensionlessSearchBase(sourceBase)
	return sourceExtensionless != "" && torrentNameEquals(name, sourceExtensionless)
}

func canonicalTorrentName(value string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

// searchProxyTorrentTerms searches each proxy term in order and deduplicates
// non-empty torrent hashes while preserving the first returned item.
func searchProxyTorrentTerms(ctx context.Context, client *http.Client, proxyBase string, searchTerms []string) ([]qbittorrent.Torrent, error) {
	torrents := make([]qbittorrent.Torrent, 0)
	seen := make(map[string]struct{}, len(searchTerms))
	for _, searchTerm := range searchTerms {
		items, err := searchProxyTorrents(ctx, client, proxyBase, searchTerm)
		if err != nil {
			return nil, err
		}
		for _, torrent := range items {
			hash := normalizeQbitHash(torrent.Hash)
			if hash != "" {
				if _, ok := seen[hash]; ok {
					continue
				}
				seen[hash] = struct{}{}
			}
			torrents = append(torrents, torrent)
		}
	}
	return torrents, nil
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

func fetchTorrentProperties(
	ctx context.Context,
	client *qbittorrent.Client,
	httpClient *http.Client,
	proxyBase, hash string,
	useProxy bool,
) (qbittorrent.TorrentProperties, error) {
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
		return qbittorrent.TorrentProperties{}, fmt.Errorf("clients: decode proxy torrent properties: %w", err)
	}
	return props, nil
}

func fetchTorrentTrackers(
	ctx context.Context,
	client *qbittorrent.Client,
	httpClient *http.Client,
	proxyBase, hash string,
	useProxy bool,
	fallback []qbittorrent.TorrentTracker,
) ([]qbittorrent.TorrentTracker, error) {
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
			return nil, fmt.Errorf("clients: decode proxy torrent trackers: %w", err)
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

func extractTrackerMatchesWithPatterns(
	comment string,
	trackerURLs []string,
	hasWorkingTracker bool,
	priority []string,
	patterns map[string]trackerPattern,
) ([]api.TrackerMatch, bool) {
	matches := make([]api.TrackerMatch, 0)
	trackerFound := false
	lowerComment := strings.ToLower(comment)

	for _, trackerID := range trackerIDExtractionOrder(priority, patterns) {
		pattern, ok := patterns[trackerID]
		if !ok {
			continue
		}
		if !hasWorkingTracker || pattern.pattern == nil || !trackerPatternMatchesComment(lowerComment, pattern) {
			continue
		}
		match := pattern.pattern.FindStringSubmatch(comment)
		if len(match) < 2 {
			continue
		}
		matches = append(matches, api.TrackerMatch{ID: trackerID, TrackerID: match[1]})
		trackerFound = true
	}

	if hasWorkingTracker {
		for _, trackerID := range trackerIDExtractionOrder(priority, patterns) {
			pattern := patterns[trackerID]
			if pattern.workingTrackerID == "" || !trackerURLsMatch(trackerURLs, pattern.trackerURLs) {
				continue
			}
			matches = append(matches, api.TrackerMatch{ID: trackerID, TrackerID: pattern.workingTrackerID})
			trackerFound = true
		}
	}

	return matches, trackerFound
}

func trackerIDExtractionOrder(priority []string, patterns map[string]trackerPattern) []string {
	seen := make(map[string]struct{}, len(priority)+len(patterns))
	ordered := make([]string, 0, len(priority)+len(patterns))
	for _, trackerID := range priority {
		key := strings.ToLower(strings.TrimSpace(trackerID))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		ordered = append(ordered, key)
	}

	keys := make([]string, 0, len(patterns))
	for key := range patterns {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		ordered = append(ordered, key)
	}
	return ordered
}

func trackerPatternMatchesComment(lowerComment string, pattern trackerPattern) bool {
	for _, commentURL := range pattern.commentURLs {
		if strings.Contains(lowerComment, strings.ToLower(strings.TrimSpace(commentURL))) {
			return true
		}
	}
	return false
}

func matchTrackerURLs(trackerURLs []string, patterns map[string]trackerPattern) []string {
	found := make(map[string]struct{})
	for _, trackerURL := range trackerURLs {
		lowerURL := strings.ToLower(strings.TrimSpace(trackerURL))
		if lowerURL == "" {
			continue
		}
		for id, identity := range patterns {
			for _, pattern := range identity.trackerURLs {
				if strings.Contains(lowerURL, pattern) {
					found[strings.ToUpper(id)] = struct{}{}
				}
			}
		}
	}
	return mapKeys(found)
}

func trackerURLsMatch(trackerURLs []string, patterns []string) bool {
	for _, trackerURL := range trackerURLs {
		lowerURL := strings.ToLower(strings.TrimSpace(trackerURL))
		for _, pattern := range patterns {
			if strings.Contains(lowerURL, strings.ToLower(strings.TrimSpace(pattern))) {
				return true
			}
		}
	}
	return false
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

// collectMatchedTrackers derives matched tracker names from validated torrent
// announce URLs only.
func collectMatchedTrackers(matches []api.TorrentMatch, patterns map[string]trackerPattern) []string {
	matched := make([]string, 0, len(matches))
	for _, match := range matches {
		matched = append(matched, matchTrackerURLs(match.TrackerURLsRaw, patterns)...)
	}
	return dedupeStrings(matched)
}

// hasTrackerIDMatch reports whether any validated match produced a tracker ID.
func hasTrackerIDMatch(matches []api.TorrentMatch) bool {
	for _, match := range matches {
		if match.HasTracker {
			return true
		}
	}
	return false
}

func ensureMatchedTrackersForKnownIDs(
	matchedTrackers []string,
	trackerIDs map[string]string,
	patterns map[string]trackerPattern,
) []string {
	if len(trackerIDs) == 0 {
		return matchedTrackers
	}
	resolved := append([]string{}, matchedTrackers...)
	for tracker, id := range trackerIDs {
		if strings.TrimSpace(id) == "" {
			continue
		}
		normalized := strings.ToLower(strings.TrimSpace(tracker))
		pattern, ok := patterns[normalized]
		if ok && pattern.inferMatchFromID && !hasMatchedTracker(resolved, tracker) {
			resolved = append(resolved, strings.ToUpper(normalized))
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

func effectiveTrackerPriority(cfg config.Config, priority []string) []string {
	return applyPreferredTrackerPriority(priority, cfg.Trackers.PreferredTracker)
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

func shouldStopSearch(constraints string, foundPreferred string) bool {
	if constraints == "" {
		return foundPreferred == "no_constraints"
	}
	if foundPreferred == "no_constraints" {
		return true
	}
	if constraints != "" && foundPreferred == constraints {
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

// selectValidTorrent validates qBittorrent name/comment matches against
// exported metadata, returning only matches trusted for tracker provenance and
// choosing one reusable torrent file when strict path and piece checks pass.
func (s *Service) selectValidTorrent(
	ctx context.Context,
	meta api.ClientSubject,
	matches []api.TorrentMatch,
	constraints pieceConstraints,
	qbitClient *qbittorrent.Client,
	httpClient *http.Client,
	proxyBase string,
	useProxy bool,
) (validatedTorrentSelection, error) {
	selection := validatedTorrentSelection{}
	if len(matches) == 0 {
		return selection, nil
	}
	tmpRoot, err := db.Subdir(s.cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		return selection, fmt.Errorf("clients: tmp dir: %w", err)
	}

	bestHash := ""
	bestPath := ""
	var bestData []byte
	var bestPiece int64
	rechecked := make(map[string]struct{})

	considerClientMatch := func(match api.TorrentMatch) {
		selection.matches = append(selection.matches, match)
	}

	considerReusable := func(hash string, path string, data []byte, pieceSize int64) {
		if bestHash == "" {
			bestHash = hash
			bestPath = path
			bestData = data
			bestPiece = pieceSize
			return
		}
		if constraints.preferSmall || constraints.preferMax16 {
			if shouldReplaceBest(pieceSize, bestPiece, constraints) {
				bestHash = hash
				bestPath = path
				bestData = data
				bestPiece = pieceSize
			}
		}
	}

	forceRecheckValidated := func(hash string) error {
		if !shouldForceRecheck(meta.ClientOverrides) {
			return nil
		}
		if useProxy || qbitClient == nil {
			s.logger.Debugf("clients: force-recheck requested for %s but direct qBittorrent access is unavailable", hash)
			return nil
		}
		if _, ok := rechecked[hash]; ok {
			return nil
		}
		if err := forceRecheckTorrent(ctx, qbitClient, hash, s.logger); err != nil {
			return err
		}
		rechecked[hash] = struct{}{}
		return nil
	}

	for _, match := range matches {
		select {
		case <-ctx.Done():
			return validatedTorrentSelection{}, fmt.Errorf("context canceled: %w", ctx.Err())
		default:
		}

		normalizedHash := normalizeQbitHash(match.Hash)
		if normalizedHash == "" {
			continue
		}

		outputPath, err := torrent.TempTorrentPath(tmpRoot, meta.SourcePath)
		if err != nil {
			return validatedTorrentSelection{}, fmt.Errorf("clients: %w", err)
		}

		if info, err := os.Stat(outputPath); err == nil && !info.IsDir() {
			data, err := os.ReadFile(outputPath)
			if err == nil {
				validation := validateTorrentData(meta, normalizedHash, data, constraints)
				if validation.clientMatch && strings.EqualFold(validation.infoHash, normalizedHash) {
					if err := forceRecheckValidated(normalizedHash); err != nil {
						return validatedTorrentSelection{}, err
					}
					considerClientMatch(match)
				}
				if validation.reusable && strings.EqualFold(validation.infoHash, normalizedHash) {
					s.logger.Debugf("clients: validated existing torrent for %s (piece=%d)", normalizedHash, validation.pieceSize)
					considerReusable(normalizedHash, outputPath, nil, validation.pieceSize)
					continue
				}
				if validation.reason != "" {
					s.logger.Debugf("clients: existing torrent failed validation for %s: %s", normalizedHash, validation.reason)
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

		validation := validateTorrentData(meta, normalizedHash, data, constraints)
		if !validation.clientMatch {
			if validation.reason != "" {
				s.logger.Debugf("clients: exported torrent failed validation for %s: %s", normalizedHash, validation.reason)
			}
			continue
		}
		if !strings.EqualFold(validation.infoHash, normalizedHash) {
			s.logger.Debugf("clients: exported torrent infohash mismatch for %s", normalizedHash)
			continue
		}
		if err := forceRecheckValidated(normalizedHash); err != nil {
			return validatedTorrentSelection{}, err
		}
		considerClientMatch(match)

		if !validation.reusable {
			s.logger.Tracef("clients: validated exported torrent as client match only for %s (reason=%s)", normalizedHash, validation.reason)
			continue
		}
		s.logger.Tracef("clients: validated exported torrent for %s (piece=%d)", normalizedHash, validation.pieceSize)

		considerReusable(normalizedHash, outputPath, data, validation.pieceSize)
	}

	if bestHash == "" {
		return selection, nil
	}
	if bestData != nil {
		path, err := writeTorrentFile(bestPath, bestData)
		if err != nil {
			return validatedTorrentSelection{}, err
		}
		bestPath = path
	}
	selection.infoHash = bestHash
	selection.torrentPath = bestPath
	selection.foundPreferred = preferredPieceLabel(bestPiece, constraints)
	return selection, nil
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

// validateTorrentData parses torrent data and reports whether its hash, paths,
// and piece metadata are usable as a client match and/or a reusable torrent file.
func validateTorrentData(meta api.ClientSubject, hash string, data []byte, constraints pieceConstraints) torrentDataValidation {
	metaInfo, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		return torrentDataValidation{reason: "load_failed"}
	}
	info, err := metaInfo.UnmarshalInfo()
	if err != nil {
		return torrentDataValidation{reason: "info_unmarshal_failed"}
	}
	infoHash := metaInfo.HashInfoBytes().String()
	if strings.TrimSpace(hash) != "" && !strings.EqualFold(infoHash, hash) {
		return torrentDataValidation{infoHash: infoHash, reason: "hash_mismatch"}
	}

	valid, wrongFile := validateTorrentPaths(meta, info)
	if !valid && !wrongFile {
		return torrentDataValidation{infoHash: infoHash, reason: "path_mismatch"}
	}

	pieceSize := info.PieceLength
	pieces := info.NumPieces()
	if pieceSize <= 0 || pieces <= 0 {
		return torrentDataValidation{
			pieceSize: pieceSize,
			infoHash:  infoHash,
			reason:    "piece_metadata_invalid",
		}
	}

	if wrongFile {
		// Same-basename path mismatches are valid for tracker data, but not strict
		// enough to use the exported torrent file as this source's torrent file.
		return torrentDataValidation{
			clientMatch: true,
			pieceSize:   pieceSize,
			infoHash:    infoHash,
			reason:      "path_mismatch",
		}
	}

	if invalidPieceConstraints(constraints, pieceSize, pieces, int64(len(data)), wrongFile) {
		return torrentDataValidation{
			clientMatch: true,
			pieceSize:   pieceSize,
			infoHash:    infoHash,
			reason:      "piece_constraints",
		}
	}

	return torrentDataValidation{
		reusable:    true,
		clientMatch: true,
		pieceSize:   pieceSize,
		infoHash:    infoHash,
	}
}

// validateTorrentPaths returns whether torrent paths strictly match the source
// and whether a same-basename layout mismatch can still identify provenance.
func validateTorrentPaths(meta api.ClientSubject, info metainfo.Info) (bool, bool) {
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
		if torrentPathContainsSourceBase(common, base) {
			return true, false
		}
		return false, false
	}

	if len(torrentFiles) == 1 && len(fileList) == 1 {
		if strings.EqualFold(pathutil.Base(torrentFiles[0]), pathutil.Base(fileList[0])) {
			if pathutil.Base(torrentFiles[0]) == torrentFiles[0] {
				return true, false
			}
			// Folder-wrapped single-file torrents can still identify tracker
			// provenance, but their layout is wrong for torrent-file reuse.
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

func torrentPathContainsSourceBase(path string, base string) bool {
	trimmedBase := strings.TrimSpace(base)
	if trimmedBase == "" {
		return false
	}
	if strings.Contains(strings.ToLower(path), strings.ToLower(trimmedBase)) {
		return true
	}
	canonicalPath := canonicalTorrentName(path)
	canonicalBase := canonicalTorrentName(trimmedBase)
	return canonicalPath != "" && canonicalBase != "" && strings.Contains(canonicalPath, canonicalBase)
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

// invalidPieceConstraints reports metadata limits that prevent saving an
// exported torrent as the reusable torrent file for this source.
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
		limit := min(len(candidate), len(parts))
		idx := 0
		for idx < limit {
			if parts[idx] != candidate[idx] {
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

// preferredPieceLabel returns the constraint label satisfied by a selected
// reusable torrent, or "no_constraints" when no constraint was configured.
func preferredPieceLabel(pieceSize int64, constraints pieceConstraints) string {
	if pieceSize <= 0 {
		return ""
	}
	if !constraints.preferSmall && !constraints.preferMax16 {
		return "no_constraints"
	}
	if constraints.preferSmall && pieceSize <= mtvPieceSizeLimit {
		return constraints.label
	}
	if constraints.preferMax16 && pieceSize <= max16PieceSize {
		return "16MiB"
	}
	return ""
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
