// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package clients

import (
	"context"
	"fmt"
	"io"
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
	clients := selectedTorrentClients(s.cfg.TorrentClients, clientOverrides)
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
		if err := s.waitInjectDelay(ctx, torrent.Tracker); err != nil {
			return err
		}
		switch clientType {
		case "none", "disabled":
			s.logger.Debugf("clients: skipping disabled client %s", name)
			continue
		case "watch":
			if torrentURL != "" {
				s.logger.Debugf("clients: skipping watch folder client %s for URL injection", name)
				continue
			}
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

func selectedTorrentClients(clients map[string]config.TorrentClientConfig, overrides api.ClientOverrides) map[string]config.TorrentClientConfig {
	if len(clients) == 0 {
		return nil
	}

	if overrides.Client == nil || strings.TrimSpace(*overrides.Client) == "" {
		return clients
	}

	selected := strings.TrimSpace(*overrides.Client)
	for name, client := range clients {
		if strings.EqualFold(strings.TrimSpace(name), selected) {
			return map[string]config.TorrentClientConfig{name: client}
		}
	}
	return nil
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
