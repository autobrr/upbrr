// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package torrentclient injects uploads into configured torrent clients and
// searches those clients for already-pathed torrents.
package torrentclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"

	qbittorrent "github.com/autobrr/go-qbittorrent"
)

// Service coordinates torrent-client injection and search operations.
type Service struct {
	cfg             config.Config
	logger          api.Logger
	trackerPatterns map[string]trackerPattern
	trackerPriority []string
}

// qbit injection HTTP uses a short, single-attempt client so a dead WebUI or
// Qui proxy fails quickly without timing out local link staging first.
const (
	qbitInjectHTTPTimeout       = 30 * time.Second
	qbitInjectHTTPRetryAttempts = 1
	qbitLoginResponseMaxBytes   = 4 << 10
)

// qbitLoginValidatingTransport verifies direct qBittorrent login responses
// before net/http can persist response cookies in the client's jar.
type qbitLoginValidatingTransport struct {
	base http.RoundTripper
}

// RoundTrip requires the exact qBittorrent success body for HTTP 200 login
// responses. It bounds the body read and restores valid bodies for LoginCtx.
func (t qbitLoginValidatingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("qbit HTTP request: %w", err)
	}
	if !strings.HasSuffix(req.URL.Path, "/api/v2/auth/login") || resp.StatusCode != http.StatusOK {
		return resp, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, qbitLoginResponseMaxBytes+1))
	if err != nil {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("read qbit login response: %w", err)
	}
	if err := resp.Body.Close(); err != nil {
		return nil, fmt.Errorf("close qbit login response: %w", err)
	}
	if len(body) > qbitLoginResponseMaxBytes {
		return nil, errors.New("qbit login response exceeds limit")
	}
	if string(body) != "Ok." {
		return nil, errors.New("qbit login response was not successful")
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}

// NewService returns a torrent-client service backed by the default tracker registry.
func NewService(cfg config.Config, logger api.Logger) *Service {
	return NewServiceWithRegistry(cfg, logger, nil)
}

// NewServiceWithRegistry returns a torrent-client service backed by registry.
func NewServiceWithRegistry(cfg config.Config, logger api.Logger, registry *trackers.Registry) *Service {
	if logger == nil {
		logger = api.NopLogger{}
	}
	priority := trackers.TrackerPriority()
	if registry != nil {
		priority = registry.Priority()
	}
	return &Service{
		cfg:             cfg,
		logger:          logger,
		trackerPatterns: buildTrackerIDPatterns(registry),
		trackerPriority: priority,
	}
}

// Inject dispatches a prepared torrent to the configured injection clients.
// URL-only torrents require a URL-capable client unless global/default fallback
// can select one; explicit tracker or caller selections that resolve only to
// watch-folder clients return [internalerrors.ErrInvalidInput] instead of a
// successful no-op.
// Inject adds torrent to the configured client and applies post-upload linking policy.
func (s *Service) Inject(ctx context.Context, meta api.ClientSubject, torrent api.TorrentResult) (err error) {
	defer func() {
		if err != nil {
			s.logger.Warnf("clients: injection blocked err=%s", redaction.RedactValue(err.Error(), nil))
		}
	}()

	select {
	case <-ctx.Done():
		return fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	s.logger.Debugf("clients: injecting torrent for %s", meta.SourcePath)

	torrentPath := strings.TrimSpace(torrent.Path)
	torrentURL := strings.TrimSpace(torrent.URL)
	if torrentPath == "" && torrentURL == "" {
		s.logger.Debugf("clients: skipping injection for %s: no torrent file or URL", meta.SourcePath)
		return internalerrors.ErrInvalidInput
	}
	s.logger.Tracef(
		"clients: injection input source=%s tracker=%s has_file=%t has_url=%t configured_clients=%d",
		meta.SourcePath,
		strings.TrimSpace(torrent.Tracker),
		torrentPath != "",
		torrentURL != "",
		len(s.cfg.TorrentClients),
	)

	if len(s.cfg.TorrentClients) == 0 {
		s.logger.Debugf("clients: no torrent clients configured, skipping injection")
		return nil
	}

	clientOverrides := s.resolveInjectClientOverrides(meta.ClientOverrides, torrent.Tracker)
	clients := resolveInjectClients(s.cfg, clientOverrides)
	// Tracker-scoped torrent_client is an effective client override; URL
	// fallback is only for global/default selections that cannot consume URLs.
	effectiveClientOverride := clientOverrides.Client != nil && strings.TrimSpace(*clientOverrides.Client) != ""
	if torrentPath == "" && torrentURL != "" && !effectiveClientOverride {
		clients = withURLCapableInjectFallback(clients, s.cfg.TorrentClients)
	}
	if len(clients) == 0 {
		s.logger.Debugf("clients: no matching torrent clients selected, skipping injection")
		return nil
	}
	s.logger.Debugf("clients: selected %d torrent client(s) for injection", len(clients))

	clientNames := make([]string, 0, len(clients))
	for name := range clients {
		clientNames = append(clientNames, name)
	}
	sort.Strings(clientNames)

	injected := false
	skippedURLOnlyClients := 0
	for _, name := range clientNames {
		client := applyClientOverrides(clients[name], clientOverrides)
		clientType := strings.ToLower(strings.TrimSpace(client.ClientType()))
		s.logger.Debugf("clients: processing client name=%s type=%s", name, clientType)
		// Watch folders can still consume a local torrent file when URL metadata
		// is also present. Skip only URL-only input before any injection delay so
		// a skipped client cannot fail a successful URL add.
		if clientType == "watch" && torrentPath == "" && torrentURL != "" {
			s.logger.Debugf("clients: skipping watch folder client %s for URL injection", name)
			skippedURLOnlyClients++
			continue
		}
		if err := s.waitInjectDelay(ctx, torrent.Tracker); err != nil {
			return err
		}
		switch clientType {
		case "none", "disabled":
			s.logger.Debugf("clients: skipping disabled client %s", name)
			continue
		case "watch":
			if err := s.injectWatchFolder(ctx, name, client.WatchFolder, torrent.Path); err != nil {
				return err
			}
			injected = true
		case "qbit", "qbittorrent", "qui":
			if err := s.injectQbit(ctx, name, client, meta, torrent); err != nil {
				return err
			}
			injected = true
		case "":
			return fmt.Errorf("clients: %s type is required", name)
		default:
			return fmt.Errorf("clients: type %q not yet supported: %w", client.ClientType(), internalerrors.ErrNotImplemented)
		}
	}

	if effectiveClientOverride && torrentPath == "" && torrentURL != "" && skippedURLOnlyClients > 0 && !injected {
		return fmt.Errorf("clients: no selected torrent client supports URL injection: %w", internalerrors.ErrInvalidInput)
	}

	s.logger.Debugf("clients: injection dispatch complete for %s", meta.SourcePath)
	return nil
}

func (s *Service) resolveInjectClientOverrides(overrides api.ClientOverrides, tracker string) api.ClientOverrides {
	if overrides.Client != nil && strings.TrimSpace(*overrides.Client) != "" {
		return overrides
	}
	trackerClient := s.trackerTorrentClient(tracker)
	if trackerClient == "" {
		return overrides
	}
	overrides.Client = &trackerClient
	return overrides
}

func (s *Service) trackerTorrentClient(tracker string) string {
	trackerCfg, ok := s.trackerConfig(tracker)
	if !ok {
		return ""
	}
	return strings.TrimSpace(trackerCfg.TorrentClient)
}

func (s *Service) trackerConfig(tracker string) (config.TrackerConfig, bool) {
	trackerName := strings.TrimSpace(tracker)
	if trackerName == "" {
		return config.TrackerConfig{}, false
	}
	for name, trackerCfg := range s.cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), trackerName) {
			return trackerCfg, true
		}
	}
	return config.TrackerConfig{}, false
}

func (s *Service) waitInjectDelay(ctx context.Context, tracker string) error {
	delay := s.cfg.PostUpload.InjectDelay
	if trackerCfg, ok := s.trackerConfig(tracker); ok && trackerCfg.InjectDelay != nil {
		delay = *trackerCfg.InjectDelay
	}
	if delay <= 0 {
		return nil
	}

	s.logger.Debugf("clients: waiting %ds before injection for tracker %s", delay, strings.TrimSpace(tracker))
	timer := time.NewTimer(time.Duration(delay) * time.Second)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return fmt.Errorf("context canceled: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}

func (s *Service) injectWatchFolder(ctx context.Context, name, folder, torrentPath string) error {
	if strings.TrimSpace(folder) == "" {
		return fmt.Errorf("clients: %s watch_folder is required", name)
	}
	s.logger.Debugf("clients: writing torrent to watch folder for %s", name)

	absTorrent, err := filepath.Abs(torrentPath)
	if err != nil {
		return fmt.Errorf("clients: %s torrent: %w", name, err)
	}

	info, err := os.Stat(folder)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("clients: %s watch_folder: %w", name, internalerrors.ErrNotFound)
		}
		return fmt.Errorf("clients: %s watch_folder: %w", name, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("clients: %s watch_folder is not a directory", name)
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	source, err := os.Open(absTorrent)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("clients: %s torrent: %w", name, internalerrors.ErrNotFound)
		}
		return fmt.Errorf("clients: %s torrent: %w", name, err)
	}
	defer source.Close()

	destPath := filepath.Join(folder, filepath.Base(absTorrent))
	dest, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("clients: %s write torrent: %w", name, err)
	}
	defer func() {
		_ = dest.Close()
	}()

	if _, err := io.Copy(dest, source); err != nil {
		return fmt.Errorf("clients: %s write torrent: %w", name, err)
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	s.logger.Infof("clients: copied torrent to watch folder path=%s", destPath)
	return nil
}

func (s *Service) injectQbit(ctx context.Context, name string, client config.TorrentClientConfig, meta api.ClientSubject, torrent api.TorrentResult) error {
	host := strings.TrimSpace(client.QbitHost())
	if host == "" {
		return fmt.Errorf("clients: %s qbit host is required", name)
	}
	username := strings.TrimSpace(client.QbitUsername())
	if username == "" && !client.UsesQuiProxy() {
		return fmt.Errorf("clients: %s qbit username is required", name)
	}
	password := strings.TrimSpace(client.QbitPassword())
	if password == "" && !client.UsesQuiProxy() {
		return fmt.Errorf("clients: %s qbit password is required", name)
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	qbit := qbittorrent.NewClient(qbitInjectClientConfig(host, username, password, client))
	qbitHTTP := qbit.GetHTTPClient()
	baseTransport := qbitHTTP.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	// The transport sees the response before http.Client stores Set-Cookie
	// values, so a transient body cannot create an authenticated session.
	qbitHTTP.Transport = qbitLoginValidatingTransport{base: baseTransport}

	options := qbittorrent.TorrentAddOptions{}
	s.logger.Debugf("clients: preparing qbit add options client=%s tracker=%s cross_seed=%t", name, strings.TrimSpace(torrent.Tracker), torrent.CrossSeed)
	optionsStart := time.Now()
	staging, err := s.prepareLinkStaging(ctx, name, client, meta, torrent)
	if err != nil {
		return err
	}
	options.SkipHashCheck = true
	if staging.Linked {
		options.SavePath = staging.SavePath
		s.logger.Debugf(
			"clients: qbit link staging selected client=%s tracker=%s files=%d layout_validated=%t save_path=%s",
			name,
			strings.TrimSpace(torrent.Tracker),
			staging.FileCount,
			staging.LayoutValidated,
			staging.SavePath,
		)
	} else {
		// Without link staging, local_path/remote_path still controls where
		// qBittorrent should save the injected torrent on the client host.
		savePath, mapped, err := mappedQbitSavePathForSource(meta, client.LocalPath, client.RemotePath)
		if err != nil {
			return fmt.Errorf("clients: %s qbit path mapping: %w", name, err)
		}
		if mapped {
			options.SavePath = savePath
			s.logger.Debugf("clients: qbit path mapping ready client=%s save_path=%s", name, savePath)
		}
	}
	if category := strings.TrimSpace(client.QbitCrossCategory); torrent.CrossSeed && category != "" {
		options.Category = category
	} else if category := strings.TrimSpace(client.QbitCategory()); category != "" {
		options.Category = category
	}
	if tags := strings.TrimSpace(client.QbitCrossTag); torrent.CrossSeed && tags != "" {
		options.Tags = tags
	} else if tags := strings.TrimSpace(client.QbitTags()); tags != "" {
		options.Tags = tags
	} else if client.UseTrackerAsTag {
		options.Tags = strings.TrimSpace(torrent.Tracker)
	}
	autoManagement := !staging.Linked && qbitAutomaticManagementEnabled(meta, client.AutomaticManagementPaths)
	addOptions := options.Prepare()
	addOptions["autoTMM"] = strconv.FormatBool(autoManagement)
	s.logger.Debugf(
		"clients: qbit add options ready client=%s auto_tmm=%t skip_hash_check=%t elapsed=%s",
		name,
		autoManagement,
		options.SkipHashCheck,
		time.Since(optionsStart).Round(time.Millisecond),
	)

	qbitCtx, cancel := context.WithTimeout(ctx, qbitInjectHTTPTimeout)
	defer cancel()

	s.logger.Debugf(
		"clients: connecting to qbit %s timeout=%s retries=%d",
		redaction.RedactValue(host, nil),
		qbitInjectHTTPTimeout,
		qbitInjectHTTPRetryAttempts,
	)
	if !client.UsesQuiProxy() {
		if err := qbit.LoginCtx(qbitCtx); err != nil {
			s.cleanupFailedLinkStaging(name, torrent.Tracker, staging)
			return fmt.Errorf("clients: %s qbit login: %w", name, err)
		}
		s.logger.Debugf("clients: connected to qbit client %s", name)
	} else {
		s.logger.Debugf("clients: using qbit proxy for client %s", name)
	}

	if torrentPath := strings.TrimSpace(torrent.Path); torrentPath != "" {
		s.logger.Debugf("clients: adding torrent file to qbit client %s for %s", name, meta.SourcePath)
		if _, err := qbit.AddTorrentFromFileCtx(qbitCtx, torrentPath, addOptions); err != nil {
			s.cleanupFailedLinkStaging(name, torrent.Tracker, staging)
			return fmt.Errorf("clients: %s qbit add torrent file: %w", name, err)
		}

		s.logger.Infof(
			"clients: added torrent file to qbit client=%s tracker=%s linked=%t qbit_hash_check=%t source=%s",
			name,
			logTracker(torrent.Tracker),
			staging.Linked,
			!options.SkipHashCheck,
			meta.SourcePath,
		)
		return nil
	}

	if torrentURL := strings.TrimSpace(torrent.URL); torrentURL != "" {
		s.logger.Debugf("clients: adding tracker torrent URL to qbit client %s for %s", name, meta.SourcePath)
		if _, err := qbit.AddTorrentFromUrlCtx(qbitCtx, torrentURL, addOptions); err != nil {
			s.cleanupFailedLinkStaging(name, torrent.Tracker, staging)
			return fmt.Errorf("clients: %s qbit add torrent URL: %w", name, err)
		}
		s.logger.Infof(
			"clients: added tracker torrent URL to qbit client=%s tracker=%s linked=%t qbit_hash_check=%t source=%s",
			name,
			logTracker(torrent.Tracker),
			staging.Linked,
			!options.SkipHashCheck,
			meta.SourcePath,
		)
		return nil
	}

	return internalerrors.ErrInvalidInput
}

// qbitAutomaticManagementEnabled reports whether the original local save path
// is a configured automatic-management root or one of its descendants after
// trimming and filepath cleaning. Containment uses host semantics: Windows
// comparisons ignore case, while case-sensitive systems preserve it. Linked
// staging is excluded by the caller.
func qbitAutomaticManagementEnabled(meta api.ClientSubject, configuredPaths config.StringList) bool {
	if len(configuredPaths) == 0 {
		return false
	}

	source, err := sourcePathForQbitSavePath(meta)
	if err != nil {
		return false
	}
	localSavePath := filepath.Clean(filepath.Dir(source))
	for _, configuredPath := range configuredPaths {
		configuredPath = strings.TrimSpace(configuredPath)
		if configuredPath == "" {
			continue
		}
		configuredPath = filepath.Clean(configuredPath)
		if pathutil.IsWithinRoot(configuredPath, localSavePath) {
			return true
		}
	}
	return false
}

func logTracker(tracker string) string {
	tracker = strings.TrimSpace(tracker)
	if tracker == "" {
		return "none"
	}
	return tracker
}

// qbitInjectClientConfig preserves configured auth and TLS behavior while
// applying the bounded HTTP policy used only for torrent injection requests.
func qbitInjectClientConfig(host, username, password string, client config.TorrentClientConfig) qbittorrent.Config {
	return qbittorrent.Config{
		Host:          host,
		Username:      username,
		Password:      password,
		TLSSkipVerify: client.QbitTLSSkipVerify(),
		Timeout:       int(qbitInjectHTTPTimeout / time.Second),
		RetryAttempts: qbitInjectHTTPRetryAttempts,
	}
}

func (s *Service) cleanupFailedLinkStaging(clientName string, tracker string, staging linkStagingResult) {
	if staging.Cleanup == nil {
		return
	}
	if err := staging.Cleanup.Run(); err != nil {
		s.logger.Warnf("clients: %s cleanup failed after qbit injection error tracker=%s: %v", clientName, strings.TrimSpace(tracker), err)
		return
	}
	s.logger.Debugf("clients: %s cleaned staged links after qbit injection error tracker=%s", clientName, strings.TrimSpace(tracker))
}

// resolveInjectClients selects the configured torrent clients that should receive
// an injected torrent. Explicit client overrides take precedence, then a
// non-empty injecting_client_list, then default_torrent_client. Configured
// selectors are authoritative: if a non-empty selector set resolves to no
// clients, lower-priority fallbacks are skipped.
func resolveInjectClients(cfg config.Config, overrides api.ClientOverrides) map[string]config.TorrentClientConfig {
	clients := cfg.TorrentClients
	if len(clients) == 0 {
		return nil
	}

	if overrides.Client != nil && strings.TrimSpace(*overrides.Client) != "" {
		if isDisableSelector(*overrides.Client) {
			if selected := selectTorrentClients(clients, []string{*overrides.Client}); len(selected) > 0 {
				return selected
			}
			return disabledTorrentClientSelection()
		}
		return selectTorrentClients(clients, []string{*overrides.Client})
	}

	if hasDisableOnlySelector(cfg.ClientSetup.InjectClients) {
		return disabledTorrentClientSelection()
	}
	if hasNonBlankSelector(cfg.ClientSetup.InjectClients) {
		return selectTorrentClients(clients, cfg.ClientSetup.InjectClients)
	}

	if strings.TrimSpace(cfg.ClientSetup.DefaultClient) != "" {
		if isDisableSelector(cfg.ClientSetup.DefaultClient) {
			return disabledTorrentClientSelection()
		}
		return selectTorrentClients(clients, []string{cfg.ClientSetup.DefaultClient})
	}

	if len(clients) == 1 {
		for name, client := range clients {
			return map[string]config.TorrentClientConfig{name: client}
		}
	}

	return nil
}

// hasNonBlankSelector reports whether a selector list contains at least one
// real client name. A lone "none" selector is handled separately as an
// explicit disable sentinel.
func hasNonBlankSelector(selected []string) bool {
	for _, value := range selected {
		if strings.TrimSpace(value) != "" && !isDisableSelector(value) {
			return true
		}
	}
	return false
}

// hasDisableOnlySelector reports whether the selector list contains only blank
// values and "none", with at least one "none" entry.
func hasDisableOnlySelector(selected []string) bool {
	hasDisable := false
	for _, value := range selected {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if !isDisableSelector(trimmed) {
			return false
		}
		hasDisable = true
	}
	return hasDisable
}

// isDisableSelector recognizes the literal selector used in config to disable
// torrent injection or search fallback. Client configs with Type "disabled" are
// detected later after a real client has been selected.
func isDisableSelector(selected string) bool {
	return strings.EqualFold(strings.TrimSpace(selected), "none")
}

// disabledTorrentClientSelection returns a synthetic selected client set that
// carries the same no-op behavior as a configured client with Type "none".
func disabledTorrentClientSelection() map[string]config.TorrentClientConfig {
	return map[string]config.TorrentClientConfig{
		"none": {Type: "none"},
	}
}

// selectTorrentClients returns configured clients selected by name. Blank and
// unknown selectors are ignored; ambiguous case-insensitive selectors are
// ignored rather than letting map iteration order choose a target. Duplicate
// selectors are collapsed only after they resolve to the same configured client,
// so exact case-variant client names can both be selected.
func selectTorrentClients(clients map[string]config.TorrentClientConfig, selected []string) map[string]config.TorrentClientConfig {
	if len(clients) == 0 || len(selected) == 0 {
		return nil
	}

	matches := make(map[string]config.TorrentClientConfig)
	seenClients := make(map[string]struct{}, len(selected))
	for _, value := range selected {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		name, client, ok := lookupTorrentClientConfig(clients, trimmed)
		if !ok {
			continue
		}
		if _, ok := seenClients[name]; ok {
			continue
		}
		seenClients[name] = struct{}{}
		matches[name] = client
	}
	if len(matches) == 0 {
		return nil
	}
	return matches
}

// lookupTorrentClientConfig returns the configured client using the original map
// key casing when selected matches exactly or has exactly one case-insensitive
// match. Ambiguous exact or folded matches are rejected.
func lookupTorrentClientConfig(clients map[string]config.TorrentClientConfig, selected string) (string, config.TorrentClientConfig, bool) {
	trimmed := strings.TrimSpace(selected)
	if trimmed == "" {
		return "", config.TorrentClientConfig{}, false
	}

	exactMatches := make([]string, 0, 1)
	foldMatches := make([]string, 0, 1)
	for name := range clients {
		nameTrimmed := strings.TrimSpace(name)
		if nameTrimmed == trimmed {
			exactMatches = append(exactMatches, name)
			continue
		}
		if strings.EqualFold(nameTrimmed, trimmed) {
			foldMatches = append(foldMatches, name)
		}
	}

	switch len(exactMatches) {
	case 1:
		name := exactMatches[0]
		return name, clients[name], true
	case 0:
	default:
		return "", config.TorrentClientConfig{}, false
	}

	if len(foldMatches) == 1 {
		name := foldMatches[0]
		return name, clients[name], true
	}
	return "", config.TorrentClientConfig{}, false
}

// withURLCapableInjectFallback replaces empty or URL-incompatible global/default
// selections with configured qbit/qui clients for URL-only injection. The
// original selection is not merged into the fallback set, so unsupported client
// types cannot fail an otherwise valid URL fallback. Selected none/disabled
// clients are authoritative and suppress fallback fanout.
func withURLCapableInjectFallback(selected, configured map[string]config.TorrentClientConfig) map[string]config.TorrentClientConfig {
	if hasDisabledTorrentClient(selected) {
		return selected
	}
	if hasURLCapableTorrentClient(selected) {
		return selected
	}

	fallback := urlCapableTorrentClients(configured)
	if len(fallback) == 0 {
		return selected
	}

	return maps.Clone(fallback)
}

// hasDisabledTorrentClient reports whether the selected set explicitly disables
// injection, which must not fan out into fallback clients.
func hasDisabledTorrentClient(clients map[string]config.TorrentClientConfig) bool {
	for _, client := range clients {
		switch strings.ToLower(strings.TrimSpace(client.ClientType())) {
		case "none", "disabled":
			return true
		}
	}
	return false
}

// hasURLCapableTorrentClient reports whether any selected client type can add a
// torrent by URL.
func hasURLCapableTorrentClient(clients map[string]config.TorrentClientConfig) bool {
	for _, client := range clients {
		if isURLCapableTorrentClient(client) {
			return true
		}
	}
	return false
}

// urlCapableTorrentClients returns every configured client that can add a
// torrent by URL and permits fallback fanout.
func urlCapableTorrentClients(clients map[string]config.TorrentClientConfig) map[string]config.TorrentClientConfig {
	matches := make(map[string]config.TorrentClientConfig)
	for name, client := range clients {
		if client.FallbackAllowed() && isURLCapableTorrentClient(client) {
			matches[name] = client
		}
	}
	if len(matches) == 0 {
		return nil
	}
	return matches
}

// isURLCapableTorrentClient reports whether a client type supports URL adds.
func isURLCapableTorrentClient(client config.TorrentClientConfig) bool {
	switch strings.ToLower(strings.TrimSpace(client.ClientType())) {
	case "qbit", "qbittorrent", "qui":
		return true
	default:
		return false
	}
}

func applyClientOverrides(client config.TorrentClientConfig, overrides api.ClientOverrides) config.TorrentClientConfig {
	if overrides.QbitCategory != nil {
		client.Category = strings.TrimSpace(*overrides.QbitCategory)
		client.QbitCategoryValue = strings.TrimSpace(*overrides.QbitCategory)
	}
	if overrides.QbitTag != nil {
		trimmed := strings.TrimSpace(*overrides.QbitTag)
		client.Tags = nil
		client.QbitTagsValue = nil
		client.QbitTag = trimmed
	}
	return client
}
