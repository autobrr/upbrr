// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package clients

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/pkg/api"

	qbittorrent "github.com/autobrr/go-qbittorrent"
)

type Service struct {
	cfg    config.Config
	logger api.Logger
}

func NewService(cfg config.Config, logger api.Logger) *Service {
	if logger == nil {
		logger = api.NopLogger{}
	}
	return &Service{cfg: cfg, logger: logger}
}

func (s *Service) Inject(ctx context.Context, meta api.PreparedMetadata, torrent api.TorrentResult) error {
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
	s.logger.Tracef("clients: injection input source=%s tracker=%s has_file=%t has_url=%t configured_clients=%d", meta.SourcePath, strings.TrimSpace(torrent.Tracker), torrentPath != "", torrentURL != "", len(s.cfg.TorrentClients))

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

	for _, name := range clientNames {
		client := applyClientOverrides(clients[name], clientOverrides)
		clientType := strings.ToLower(strings.TrimSpace(client.ClientType()))
		s.logger.Debugf("clients: processing client %s (%s)", name, clientType)
		// Watch folders can still consume a local torrent file when URL metadata
		// is also present. Skip only URL-only input before any injection delay so
		// a skipped client cannot fail a successful URL add.
		if clientType == "watch" && torrentPath == "" && torrentURL != "" {
			s.logger.Debugf("clients: skipping watch folder client %s for URL injection", name)
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
		case "qbit", "qbittorrent", "qui":
			if err := s.injectQbit(ctx, name, client, meta, torrent); err != nil {
				return err
			}
		case "":
			return fmt.Errorf("clients: %s type is required", name)
		default:
			return fmt.Errorf("clients: type %q not yet supported: %w", client.ClientType(), internalerrors.ErrNotImplemented)
		}
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

	s.logger.Infof("clients: copied torrent to watch folder %s", destPath)
	return nil
}

func (s *Service) injectQbit(ctx context.Context, name string, client config.TorrentClientConfig, meta api.PreparedMetadata, torrent api.TorrentResult) error {
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

	qbit := qbittorrent.NewClient(qbittorrent.Config{
		Host:          host,
		Username:      username,
		Password:      password,
		TLSSkipVerify: client.QbitTLSSkipVerify(),
	})
	s.logger.Debugf("clients: connecting to qbit %s", redaction.RedactValue(host, nil))
	if !client.UsesQuiProxy() {
		if err := qbit.LoginCtx(ctx); err != nil {
			return fmt.Errorf("clients: %s qbit login: %w", name, err)
		}
	}

	options := qbittorrent.TorrentAddOptions{}
	options.SkipHashCheck = true
	staging, err := s.prepareLinkStaging(ctx, name, client, meta, torrent.Tracker)
	if err != nil {
		return err
	}
	if staging.Linked {
		options.SavePath = staging.SavePath
		s.logger.Debugf("clients: qbit link staging ready client=%s tracker=%s save_path=%s", name, strings.TrimSpace(torrent.Tracker), staging.SavePath)
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

	if torrentPath := strings.TrimSpace(torrent.Path); torrentPath != "" {
		s.logger.Debugf("clients: adding torrent file to qbit client %s for %s", name, meta.SourcePath)
		if _, err := qbit.AddTorrentFromFileCtx(ctx, torrentPath, options.Prepare()); err != nil {
			s.cleanupFailedLinkStaging(name, torrent.Tracker, staging)
			return fmt.Errorf("clients: %s qbit add torrent file: %w", name, err)
		}

		s.logger.Infof("clients: added torrent file to qbit client %s for %s", name, meta.SourcePath)
		return nil
	}

	if torrentURL := strings.TrimSpace(torrent.URL); torrentURL != "" {
		s.logger.Debugf("clients: adding tracker torrent URL to qbit client %s for %s", name, meta.SourcePath)
		if _, err := qbit.AddTorrentFromUrlCtx(ctx, torrentURL, options.Prepare()); err != nil {
			s.cleanupFailedLinkStaging(name, torrent.Tracker, staging)
			return fmt.Errorf("clients: %s qbit add torrent URL: %w", name, err)
		}
		s.logger.Infof("clients: added tracker torrent URL to qbit client %s for %s", name, meta.SourcePath)
		return nil
	}

	return internalerrors.ErrInvalidInput
}

func (s *Service) cleanupFailedLinkStaging(clientName string, tracker string, staging linkStagingResult) {
	if staging.Cleanup == nil {
		return
	}
	if err := staging.Cleanup.Run(); err != nil {
		s.logger.Warnf("clients: %s cleanup failed after qbit add error tracker=%s: %v", clientName, strings.TrimSpace(tracker), err)
		return
	}
	s.logger.Debugf("clients: %s cleaned staged links after qbit add error tracker=%s", clientName, strings.TrimSpace(tracker))
}

// resolveInjectClients selects the configured torrent clients that should receive
// an injected torrent. Explicit client overrides take precedence, then a
// non-empty injecting_client_list, then default_torrent_client. Unknown config
// selectors fall through to lower-priority defaults, while unknown explicit
// overrides stay authoritative and select no clients.
func resolveInjectClients(cfg config.Config, overrides api.ClientOverrides) map[string]config.TorrentClientConfig {
	clients := cfg.TorrentClients
	if len(clients) == 0 {
		return nil
	}

	if overrides.Client != nil && strings.TrimSpace(*overrides.Client) != "" {
		return selectTorrentClients(clients, []string{*overrides.Client})
	}

	if hasNonBlankSelector(cfg.ClientSetup.InjectClients) {
		if selected := selectTorrentClients(clients, cfg.ClientSetup.InjectClients); len(selected) > 0 {
			return selected
		}
	}

	if strings.TrimSpace(cfg.ClientSetup.DefaultClient) != "" {
		if selected := selectTorrentClients(clients, []string{cfg.ClientSetup.DefaultClient}); len(selected) > 0 {
			return selected
		}
	}

	if len(clients) == 1 {
		for name, client := range clients {
			return map[string]config.TorrentClientConfig{name: client}
		}
	}

	return nil
}

// hasNonBlankSelector reports whether a selector list should participate in
// client resolution after whitespace-only entries are ignored.
func hasNonBlankSelector(selected []string) bool {
	for _, value := range selected {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

// selectTorrentClients returns configured clients selected by name. Blank,
// unknown, normalized duplicate, and ambiguous selectors are ignored rather than
// allowing map iteration order to choose a target. Failed lookups do not consume
// the normalized selector, so a later exact selector remains eligible.
func selectTorrentClients(clients map[string]config.TorrentClientConfig, selected []string) map[string]config.TorrentClientConfig {
	if len(clients) == 0 || len(selected) == 0 {
		return nil
	}

	matches := make(map[string]config.TorrentClientConfig)
	seenSelectors := make(map[string]struct{}, len(selected))
	for _, value := range selected {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		normalized := strings.ToLower(trimmed)
		if _, ok := seenSelectors[normalized]; ok {
			continue
		}
		name, client, ok := lookupTorrentClientConfig(clients, trimmed)
		if ok {
			seenSelectors[normalized] = struct{}{}
			matches[name] = client
		}
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

// withURLCapableInjectFallback adds configured qbit/qui clients for URL-only
// injection when a global/default selection exists but cannot consume URLs.
// Selected none/disabled clients are authoritative and suppress fallback fanout.
func withURLCapableInjectFallback(selected, configured map[string]config.TorrentClientConfig) map[string]config.TorrentClientConfig {
	if len(selected) == 0 {
		return selected
	}
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

	merged := make(map[string]config.TorrentClientConfig, len(selected)+len(fallback))
	maps.Copy(merged, selected)
	maps.Copy(merged, fallback)
	return merged
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
