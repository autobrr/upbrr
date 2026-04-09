// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/clients"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/dupechecking"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/services/bdinfo"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/services/description"
	"github.com/autobrr/upbrr/internal/services/imagehosting"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/internal/torrent"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

type Core struct {
	cfg       config.Config
	logger    api.Logger
	services  api.ServiceSet
	repo      db.MetadataRepository
	dupeMu    sync.RWMutex
	dupeCache map[string]dupeCacheEntry
}

type dupeCacheEntry struct {
	meta      api.PreparedMetadata
	signature string
	updatedAt time.Time
}

func New(deps api.CoreDependencies) (*Core, error) {
	logger := deps.Logger
	if logger == nil {
		logger = api.NopLogger{}
	}
	logger.Infof("core: initializing")

	if err := deps.Config.Validate(); err != nil {
		return nil, err
	}

	repo := deps.Repository
	if repo == nil {
		logger.Debugf("core: opening repository")
		sqliteRepo, err := db.OpenWithLogger(deps.Config.MainSettings.DBPath, logger)
		if err != nil {
			return nil, err
		}
		if err := sqliteRepo.Migrate(); err != nil {
			_ = sqliteRepo.Close()
			return nil, err
		}
		repo = sqliteRepo
	}

	services := deps.Services
	if services.Metadata == nil {
		bdinfoService := bdinfo.New(logger)

		services.Metadata = metadata.NewService(
			repo,
			metadata.WithTagsPathFromDB(deps.Config.MainSettings.DBPath),
			metadata.WithLogger(logger),
			metadata.WithSRRDBPaths(deps.Config.MainSettings.DBPath),
			metadata.WithConfig(deps.Config),
			metadata.WithBDInfoService(bdinfoService),
		)
	}
	if services.Torrents == nil {
		tmpDir, err := db.Subdir(deps.Config.MainSettings.DBPath, "tmp")
		if err != nil {
			return nil, fmt.Errorf("core: tmp dir: %w", err)
		}
		services.Torrents = torrent.NewService(logger, tmpDir)
	}
	if services.Screenshots == nil {
		tmpDir, err := db.Subdir(deps.Config.MainSettings.DBPath, "tmp")
		if err != nil {
			return nil, fmt.Errorf("core: tmp dir: %w", err)
		}
		services.Screenshots = screenshots.NewServiceWithRepo(deps.Config, logger, tmpDir, nil, repo)
	}
	if services.Images == nil {
		services.Images = imagehosting.NewService(deps.Config, logger, repo)
	}
	if services.Trackers == nil {
		registry, err := trackerimpl.NewRegistry()
		if err != nil {
			return nil, fmt.Errorf("core: tracker registry: %w", err)
		}
		services.Trackers = trackers.NewServiceWithRegistryAndImages(deps.Config, logger, repo, registry, services.Images)
	}
	if services.Clients == nil {
		services.Clients = clients.NewService(deps.Config, logger)
	}
	if services.Filesystem == nil {
		services.Filesystem = filesystem.NewValidatorWithLogger(logger)
	}
	if services.Dupes == nil {
		services.Dupes = dupechecking.NewService(deps.Config, logger)
	}
	logger.Infof("core: initialized services")

	return &Core{
		cfg:       deps.Config,
		logger:    logger,
		services:  services,
		repo:      repo,
		dupeCache: make(map[string]dupeCacheEntry),
	}, nil
}

func (c *Core) RunUpload(ctx context.Context, req api.Request) (api.Result, error) {
	req = normalizeExecutionRequest(req)
	c.logger.Debugf("core: upload request routed to upload-only handler")
	return c.RunUploadPrepared(ctx, req)
}

func (c *Core) RunUploadPrepared(ctx context.Context, req api.Request) (api.Result, error) {
	req = normalizeExecutionRequest(req)
	resolvedReq, err := c.resolveDescriptionOverrideRequest(ctx, req)
	if err != nil {
		return api.Result{}, err
	}
	req = resolvedReq

	if len(req.Paths) == 0 {
		return api.Result{}, internalerrors.ErrInvalidInput
	}
	c.logger.Debugf("core: upload-only request with %d paths", len(req.Paths))

	normalizedPaths, err := c.services.Filesystem.ValidatePaths(ctx, req.Paths)
	if err != nil {
		return api.Result{}, err
	}

	uniquePaths := make([]string, 0, len(normalizedPaths))
	seenPaths := make(map[string]struct{}, len(normalizedPaths))
	for _, path := range normalizedPaths {
		if _, exists := seenPaths[path]; exists {
			continue
		}
		seenPaths[path] = struct{}{}
		uniquePaths = append(uniquePaths, path)
	}
	c.logger.Debugf("core: upload-only processing %d unique paths", len(uniquePaths))

	options, err := c.applyDefaultOptions(req.Options)
	if err != nil {
		return api.Result{}, err
	}

	var totalUploaded int
	for _, path := range uniquePaths {
		select {
		case <-ctx.Done():
			return api.Result{}, ctx.Err()
		default:
		}

		singleReq := req
		singleReq.Paths = []string{path}
		singleReq.Options = options
		singleReq.ExternalIDOverrides = mergeExternalIDOverrides(req.ExternalIDOverrides, resolveExternalIDSelection(req.ExternalIDSelections, path))

		signature := overrideSignature(singleReq.ExternalIDOverrides, singleReq.ReleaseNameOverrides, singleReq.MetadataOverrides, singleReq.TrackerConfigOverrides, singleReq.TrackerSiteOverrides, singleReq.ClientOverrides, singleReq.TorrentOverrides, singleReq.ImageHostOverrides, singleReq.ScreenshotOverrides)
		meta, ok := c.getDupeCache(path, signature)
		if !ok {
			return api.Result{}, fmt.Errorf("core: upload-only requires prepared metadata for %s", path)
		}

		uploaded, err := c.executePreparedUpload(ctx, singleReq, meta)
		if err != nil {
			return api.Result{}, err
		}
		totalUploaded += uploaded
	}

	c.logger.Debugf("core: upload-only complete with %d uploaded", totalUploaded)
	return api.Result{UploadedCount: totalUploaded}, nil
}

func (c *Core) executePreparedUpload(ctx context.Context, req api.Request, meta api.PreparedMetadata) (int, error) {
	meta = applyRequestToPreparedMeta(meta, req)
	descriptionGroups, err := c.resolveCanonicalDescriptionGroups(ctx, meta, req)
	if err != nil {
		return 0, err
	}
	meta.DescriptionGroups = descriptionGroups

	torrent, err := c.services.Torrents.Create(ctx, meta)
	if err != nil {
		return 0, err
	}
	c.logger.Debugf("core: torrent ready for %s", meta.SourcePath)
	meta.TorrentPath = torrent.Path

	if c.repo != nil && torrent.InfoHash != "" {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
		}
		if err := c.repo.Save(ctx, db.FileMetadata{Path: meta.SourcePath, InfoHash: torrent.InfoHash, UpdatedAt: time.Now().UTC()}); err != nil {
			return 0, fmt.Errorf("metadata: persist info hash: %w", err)
		}
	}

	if req.Options.DryRun || req.Options.Debug {
		c.logger.Debugf("core: dry-run or debug enabled, skipping injection/upload")
		return 0, nil
	}

	c.logger.Debugf("core: uploading to trackers for %s", meta.SourcePath)
	summary, err := c.services.Trackers.Upload(ctx, meta)
	if err != nil {
		return 0, err
	}

	if !meta.Options.NoSeed {
		if len(summary.UploadedTorrents) == 0 {
			c.logger.Warnf("core: no tracker torrent artifacts available for injection for %s", meta.SourcePath)
		} else {
			for _, uploaded := range summary.UploadedTorrents {
				torrentPath := strings.TrimSpace(uploaded.TorrentPath)
				torrentURL := strings.TrimSpace(uploaded.DownloadURL)
				if torrentPath == "" && torrentURL == "" {
					continue
				}
				c.logger.Debugf("core: injecting tracker torrent for %s from %s", meta.SourcePath, uploaded.Tracker)
				if err := c.services.Clients.Inject(ctx, meta, api.TorrentResult{
					Path:    torrentPath,
					URL:     torrentURL,
					Tracker: uploaded.Tracker,
				}); err != nil {
					return 0, err
				}
			}
		}
	}

	if summary.Uploaded < 0 {
		return 0, fmt.Errorf("upload summary invalid: %d", summary.Uploaded)
	}

	return summary.Uploaded, nil
}

func (c *Core) CheckDupes(ctx context.Context, req api.Request) (api.DupeCheckSummary, error) {
	req = normalizeExecutionRequest(req)
	if len(req.Paths) == 0 {
		return api.DupeCheckSummary{}, internalerrors.ErrInvalidInput
	}
	if c.services.Dupes == nil {
		return api.DupeCheckSummary{}, errors.New("core: dupe service not configured")
	}

	normalizedPaths, err := c.services.Filesystem.ValidatePaths(ctx, req.Paths)
	if err != nil {
		return api.DupeCheckSummary{}, err
	}

	uniquePaths := make([]string, 0, len(normalizedPaths))
	seenPaths := make(map[string]struct{}, len(normalizedPaths))
	for _, path := range normalizedPaths {
		if _, exists := seenPaths[path]; exists {
			continue
		}
		seenPaths[path] = struct{}{}
		uniquePaths = append(uniquePaths, path)
	}
	if len(uniquePaths) != 1 {
		return api.DupeCheckSummary{}, internalerrors.ErrInvalidInput
	}

	cacheSignature := overrideSignature(req.ExternalIDOverrides, req.ReleaseNameOverrides, req.MetadataOverrides, req.TrackerConfigOverrides, req.TrackerSiteOverrides, req.ClientOverrides, req.TorrentOverrides, req.ImageHostOverrides, req.ScreenshotOverrides)
	if req.Mode == api.ModeGUI {
		if cached, ok := c.getGUICachedMeta(uniquePaths[0], cacheSignature, req.ExternalIDOverrides); ok {
			matchedTrackers := mergeTrackerRemovals(nil, cached.MatchedTrackers)
			removeTrackers := mergeTrackerRemovals(req.TrackersRemove, matchedTrackers)
			resolvedTrackers := trackers.ResolveTrackersWithDefaults(c.cfg, req.Trackers, removeTrackers, c.logger)
			summary, err := c.services.Dupes.Check(ctx, cached, resolvedTrackers)
			if err != nil {
				return api.DupeCheckSummary{}, err
			}
			summary = appendPathedDupeResults(summary, matchedTrackers)
			applyDupeSummaryToPreparedMeta(&cached, summary)
			c.storeDupeCache(cached.SourcePath, overrideSignature(cached.ExternalIDOverrides, cached.ReleaseNameOverrides, cached.MetadataOverrides, cached.TrackerConfigOverrides, cached.TrackerSiteOverrides, cached.ClientOverrides, cached.TorrentOverrides, cached.ImageHostOverrides, cached.ScreenshotOverrides), cached)
			return summary, nil
		}
		return api.DupeCheckSummary{}, errors.New("core: dupe check requires metadata preview")
	}

	options, err := c.applyDefaultOptions(req.Options)
	if err != nil {
		return api.DupeCheckSummary{}, err
	}

	singleReq := req
	singleReq.Paths = []string{uniquePaths[0]}
	singleReq.Options = options
	singleReq.ExternalIDOverrides = mergeExternalIDOverrides(req.ExternalIDOverrides, resolveExternalIDSelection(req.ExternalIDSelections, uniquePaths[0]))

	if err := c.resetContentForExternalOverrides(ctx, uniquePaths[0], singleReq.ExternalIDOverrides); err != nil {
		return api.DupeCheckSummary{}, err
	}

	meta, err := c.services.Metadata.Prepare(ctx, singleReq)
	if err != nil {
		return api.DupeCheckSummary{}, err
	}

	storedApplied, err := c.applyStoredTrackerData(ctx, &meta)
	if err != nil {
		return api.DupeCheckSummary{}, err
	}
	runPathedSearch := func(applyTrackerData bool) error {
		if meta.Options.SkipAutoTorrent {
			c.logger.Debugf("core: skip_auto_torrent enabled, skipping pathed search for %s", meta.SourcePath)
			return nil
		}

		searchResult, searchErr := c.services.Clients.SearchPathedTorrents(ctx, meta)
		if searchErr != nil {
			return searchErr
		}

		meta.FoundTrackerMatch = meta.FoundTrackerMatch || searchResult.FoundTrackerMatch
		meta.MatchedTrackers = mergeTrackerRemovals(meta.MatchedTrackers, searchResult.MatchedTrackers)
		meta.TrackersRemove = mergeTrackerRemovals(meta.TrackersRemove, searchResult.MatchedTrackers)

		if !applyTrackerData {
			return nil
		}

		if searchResult.InfoHash == "" && len(searchResult.TorrentComments) == 0 {
			return nil
		}
		meta.InfoHash = searchResult.InfoHash
		meta.ClientTorrentPath = searchResult.TorrentPath
		meta.TrackerIDs = searchResult.TrackerIDs
		meta.TorrentComments = searchResult.TorrentComments
		meta.PieceSizeConstraint = searchResult.PieceSizeConstraint
		meta.FoundPreferredPiece = searchResult.FoundPreferredPiece
		return nil
	}

	switch {
	case meta.SourceLookupActive:
		c.logger.Debugf("core: running pathed search for tracker presence with source url override active for %s", meta.SourcePath)
		if err := runPathedSearch(false); err != nil {
			return api.DupeCheckSummary{}, err
		}
	case storedApplied:
		c.logger.Debugf("core: running pathed search for tracker presence with stored tracker data present for %s", meta.SourcePath)
		if err := runPathedSearch(false); err != nil {
			return api.DupeCheckSummary{}, err
		}
	case meta.StoredDataFresh:
		if meta.InfoHash == "" && meta.StoredInfoHash != "" {
			meta.InfoHash = meta.StoredInfoHash
			c.logger.Debugf("core: using stored infohash before pathed tracker-presence search for %s", meta.SourcePath)
		} else {
			c.logger.Debugf("core: running pathed search for tracker presence with fresh stored metadata snapshot for %s", meta.SourcePath)
		}
		if err := runPathedSearch(false); err != nil {
			return api.DupeCheckSummary{}, err
		}
	default:
		if meta.InfoHash == "" && len(meta.TrackerIDs) == 0 {
			if err := runPathedSearch(true); err != nil {
				return api.DupeCheckSummary{}, err
			}
		} else {
			c.logger.Debugf("core: running pathed search for tracker presence with existing infohash/tracker IDs for %s", meta.SourcePath)
			if err := runPathedSearch(false); err != nil {
				return api.DupeCheckSummary{}, err
			}
		}
	}

	meta, err = c.services.Metadata.EnrichTrackerData(ctx, meta)
	if err != nil {
		return api.DupeCheckSummary{}, err
	}
	meta, err = c.services.Metadata.ApplyMediaInfoIDs(ctx, meta)
	if err != nil {
		return api.DupeCheckSummary{}, err
	}
	meta, err = c.services.Metadata.ApplyArrData(ctx, meta)
	if err != nil {
		return api.DupeCheckSummary{}, err
	}
	meta, err = c.services.Metadata.ResolveExternalIDs(ctx, meta)
	if err != nil {
		return api.DupeCheckSummary{}, err
	}
	meta, err = c.services.Metadata.ApplyMediaDetails(ctx, meta)
	if err != nil {
		return api.DupeCheckSummary{}, err
	}

	c.storeDupeCache(meta.SourcePath, overrideSignature(meta.ExternalIDOverrides, meta.ReleaseNameOverrides, meta.MetadataOverrides, meta.TrackerConfigOverrides, meta.TrackerSiteOverrides, meta.ClientOverrides, meta.TorrentOverrides, meta.ImageHostOverrides, meta.ScreenshotOverrides), meta)

	matchedTrackers := mergeTrackerRemovals(nil, meta.MatchedTrackers)
	removeTrackers := mergeTrackerRemovals(req.TrackersRemove, matchedTrackers)
	resolvedTrackers := trackers.ResolveTrackers(c.cfg, req.Trackers, removeTrackers, c.logger)
	summary, err := c.services.Dupes.Check(ctx, meta, resolvedTrackers)
	if err != nil {
		return api.DupeCheckSummary{}, err
	}
	summary = appendPathedDupeResults(summary, matchedTrackers)
	applyDupeSummaryToPreparedMeta(&meta, summary)
	c.storeDupeCache(meta.SourcePath, overrideSignature(meta.ExternalIDOverrides, meta.ReleaseNameOverrides, meta.MetadataOverrides, meta.TrackerConfigOverrides, meta.TrackerSiteOverrides, meta.ClientOverrides, meta.TorrentOverrides, meta.ImageHostOverrides, meta.ScreenshotOverrides), meta)
	return summary, nil
}

func (c *Core) FetchScreenshotPlan(ctx context.Context, req api.Request) (api.ScreenshotPlan, error) {
	if len(req.Paths) == 0 {
		return api.ScreenshotPlan{}, internalerrors.ErrInvalidInput
	}
	if c.services.Screenshots == nil {
		return api.ScreenshotPlan{}, errors.New("core: screenshots service not configured")
	}

	normalizedPaths, err := c.services.Filesystem.ValidatePaths(ctx, req.Paths)
	if err != nil {
		return api.ScreenshotPlan{}, err
	}
	uniquePaths := make([]string, 0, len(normalizedPaths))
	seenPaths := make(map[string]struct{}, len(normalizedPaths))
	for _, path := range normalizedPaths {
		if _, exists := seenPaths[path]; exists {
			continue
		}
		seenPaths[path] = struct{}{}
		uniquePaths = append(uniquePaths, path)
	}
	if len(uniquePaths) != 1 {
		return api.ScreenshotPlan{}, internalerrors.ErrInvalidInput
	}

	if req.Mode == api.ModeGUI {
		signature := overrideSignature(req.ExternalIDOverrides, req.ReleaseNameOverrides, req.MetadataOverrides, req.TrackerConfigOverrides, req.TrackerSiteOverrides, req.ClientOverrides, req.TorrentOverrides, req.ImageHostOverrides, req.ScreenshotOverrides)
		if cached, ok := c.getGUICachedMeta(uniquePaths[0], signature, req.ExternalIDOverrides); ok {
			return c.services.Screenshots.Plan(ctx, cached, cached.Options.Screens)
		}
		return api.ScreenshotPlan{}, errors.New("core: screenshot plan requires metadata preview")
	}

	options, err := c.applyDefaultOptions(req.Options)
	if err != nil {
		return api.ScreenshotPlan{}, err
	}

	singleReq := req
	singleReq.Paths = []string{uniquePaths[0]}
	singleReq.Options = options

	meta, err := c.services.Metadata.Prepare(ctx, singleReq)
	if err != nil {
		return api.ScreenshotPlan{}, err
	}

	return c.services.Screenshots.Plan(ctx, meta, options.Screens)
}

func (c *Core) GenerateScreenshots(ctx context.Context, req api.Request, selections []api.ScreenshotSelection, purpose api.ScreenshotPurpose) (api.ScreenshotResult, error) {
	if len(req.Paths) == 0 {
		return api.ScreenshotResult{}, internalerrors.ErrInvalidInput
	}
	if c.services.Screenshots == nil {
		return api.ScreenshotResult{}, errors.New("core: screenshots service not configured")
	}
	if len(selections) == 0 {
		return api.ScreenshotResult{}, internalerrors.ErrInvalidInput
	}

	normalizedPaths, err := c.services.Filesystem.ValidatePaths(ctx, req.Paths)
	if err != nil {
		return api.ScreenshotResult{}, err
	}
	uniquePaths := make([]string, 0, len(normalizedPaths))
	seenPaths := make(map[string]struct{}, len(normalizedPaths))
	for _, path := range normalizedPaths {
		if _, exists := seenPaths[path]; exists {
			continue
		}
		seenPaths[path] = struct{}{}
		uniquePaths = append(uniquePaths, path)
	}
	if len(uniquePaths) != 1 {
		return api.ScreenshotResult{}, internalerrors.ErrInvalidInput
	}

	if req.Mode == api.ModeGUI {
		signature := overrideSignature(req.ExternalIDOverrides, req.ReleaseNameOverrides, req.MetadataOverrides, req.TrackerConfigOverrides, req.TrackerSiteOverrides, req.ClientOverrides, req.TorrentOverrides, req.ImageHostOverrides, req.ScreenshotOverrides)
		if cached, ok := c.getGUICachedMeta(uniquePaths[0], signature, req.ExternalIDOverrides); ok {
			return c.services.Screenshots.Capture(ctx, cached, selections, purpose)
		}
		return api.ScreenshotResult{}, errors.New("core: screenshot capture requires metadata preview")
	}

	options, err := c.applyDefaultOptions(req.Options)
	if err != nil {
		return api.ScreenshotResult{}, err
	}
	singleReq := req
	singleReq.Paths = []string{uniquePaths[0]}
	singleReq.Options = options

	meta, err := c.services.Metadata.Prepare(ctx, singleReq)
	if err != nil {
		return api.ScreenshotResult{}, err
	}

	return c.services.Screenshots.Capture(ctx, meta, selections, purpose)
}

func (c *Core) PreviewScreenshotFrame(ctx context.Context, req api.Request, timestampSeconds float64) (api.ScreenshotPreview, error) {
	if len(req.Paths) == 0 {
		return api.ScreenshotPreview{}, internalerrors.ErrInvalidInput
	}
	if c.services.Screenshots == nil {
		return api.ScreenshotPreview{}, errors.New("core: screenshots service not configured")
	}

	normalizedPaths, err := c.services.Filesystem.ValidatePaths(ctx, req.Paths)
	if err != nil {
		return api.ScreenshotPreview{}, err
	}
	uniquePaths := make([]string, 0, len(normalizedPaths))
	seenPaths := make(map[string]struct{}, len(normalizedPaths))
	for _, path := range normalizedPaths {
		if _, exists := seenPaths[path]; exists {
			continue
		}
		seenPaths[path] = struct{}{}
		uniquePaths = append(uniquePaths, path)
	}
	if len(uniquePaths) != 1 {
		return api.ScreenshotPreview{}, internalerrors.ErrInvalidInput
	}

	if req.Mode == api.ModeGUI {
		signature := overrideSignature(req.ExternalIDOverrides, req.ReleaseNameOverrides, req.MetadataOverrides, req.TrackerConfigOverrides, req.TrackerSiteOverrides, req.ClientOverrides, req.TorrentOverrides, req.ImageHostOverrides, req.ScreenshotOverrides)
		if cached, ok := c.getGUICachedMeta(uniquePaths[0], signature, req.ExternalIDOverrides); ok {
			return c.services.Screenshots.PreviewFrame(ctx, cached, timestampSeconds)
		}
		return api.ScreenshotPreview{}, errors.New("core: screenshot preview requires metadata preview")
	}

	options, err := c.applyDefaultOptions(req.Options)
	if err != nil {
		return api.ScreenshotPreview{}, err
	}
	singleReq := req
	singleReq.Paths = []string{uniquePaths[0]}
	singleReq.Options = options

	meta, err := c.services.Metadata.Prepare(ctx, singleReq)
	if err != nil {
		return api.ScreenshotPreview{}, err
	}

	return c.services.Screenshots.PreviewFrame(ctx, meta, timestampSeconds)
}

func (c *Core) DeleteScreenshot(ctx context.Context, req api.Request, imagePath string) error {
	if len(req.Paths) == 0 {
		return internalerrors.ErrInvalidInput
	}
	if c.services.Screenshots == nil {
		return errors.New("core: screenshots service not configured")
	}
	if strings.TrimSpace(imagePath) == "" {
		return internalerrors.ErrInvalidInput
	}

	normalizedPaths, err := c.services.Filesystem.ValidatePaths(ctx, req.Paths)
	if err != nil {
		return err
	}
	uniquePaths := make([]string, 0, len(normalizedPaths))
	seenPaths := make(map[string]struct{}, len(normalizedPaths))
	for _, path := range normalizedPaths {
		if _, exists := seenPaths[path]; exists {
			continue
		}
		seenPaths[path] = struct{}{}
		uniquePaths = append(uniquePaths, path)
	}
	if len(uniquePaths) != 1 {
		return internalerrors.ErrInvalidInput
	}

	if req.Mode == api.ModeGUI {
		signature := overrideSignature(req.ExternalIDOverrides, req.ReleaseNameOverrides, req.MetadataOverrides, req.TrackerConfigOverrides, req.TrackerSiteOverrides, req.ClientOverrides, req.TorrentOverrides, req.ImageHostOverrides, req.ScreenshotOverrides)
		if cached, ok := c.getGUICachedMeta(uniquePaths[0], signature, req.ExternalIDOverrides); ok {
			return c.services.Screenshots.Delete(ctx, cached, imagePath)
		}
		return errors.New("core: screenshot delete requires metadata preview")
	}

	options, err := c.applyDefaultOptions(req.Options)
	if err != nil {
		return err
	}
	singleReq := req
	singleReq.Paths = []string{uniquePaths[0]}
	singleReq.Options = options

	meta, err := c.services.Metadata.Prepare(ctx, singleReq)
	if err != nil {
		return err
	}

	return c.services.Screenshots.Delete(ctx, meta, imagePath)
}

func (c *Core) DeleteTrackerImageURL(ctx context.Context, req api.Request, url string) error {
	if len(req.Paths) == 0 {
		return internalerrors.ErrInvalidInput
	}
	if c.repo == nil {
		return errors.New("core: repository not configured")
	}
	trimmedURL := strings.TrimSpace(url)
	if trimmedURL == "" {
		return internalerrors.ErrInvalidInput
	}

	normalizedPaths, err := c.services.Filesystem.ValidatePaths(ctx, req.Paths)
	if err != nil {
		return err
	}
	uniquePaths := make([]string, 0, len(normalizedPaths))
	seenPaths := make(map[string]struct{}, len(normalizedPaths))
	for _, path := range normalizedPaths {
		if _, exists := seenPaths[path]; exists {
			continue
		}
		seenPaths[path] = struct{}{}
		uniquePaths = append(uniquePaths, path)
	}
	if len(uniquePaths) != 1 {
		return internalerrors.ErrInvalidInput
	}

	records, err := c.repo.ListTrackerMetadataByPath(ctx, uniquePaths[0])
	if err != nil {
		return fmt.Errorf("core: tracker metadata lookup: %w", err)
	}

	for _, record := range records {
		if len(record.ImageURLs) == 0 {
			continue
		}
		filtered := make([]string, 0, len(record.ImageURLs))
		removed := false
		for _, value := range record.ImageURLs {
			if strings.TrimSpace(value) == trimmedURL {
				removed = true
				continue
			}
			filtered = append(filtered, value)
		}
		if !removed {
			continue
		}
		record.ImageURLs = filtered
		if strings.TrimSpace(record.SourcePath) == "" {
			record.SourcePath = uniquePaths[0]
		}
		if err := c.repo.SaveTrackerMetadata(ctx, record); err != nil {
			return fmt.Errorf("core: save tracker metadata: %w", err)
		}
	}

	return nil
}

func (c *Core) SaveFinalScreenshotSelections(ctx context.Context, req api.Request, images []api.ScreenshotImage) error {
	if len(req.Paths) == 0 {
		return internalerrors.ErrInvalidInput
	}
	if c.services.Screenshots == nil {
		return errors.New("core: screenshots service not configured")
	}

	normalizedPaths, err := c.services.Filesystem.ValidatePaths(ctx, req.Paths)
	if err != nil {
		return err
	}
	uniquePaths := make([]string, 0, len(normalizedPaths))
	seenPaths := make(map[string]struct{}, len(normalizedPaths))
	for _, path := range normalizedPaths {
		if _, exists := seenPaths[path]; exists {
			continue
		}
		seenPaths[path] = struct{}{}
		uniquePaths = append(uniquePaths, path)
	}
	if len(uniquePaths) != 1 {
		return internalerrors.ErrInvalidInput
	}

	if req.Mode == api.ModeGUI {
		signature := overrideSignature(req.ExternalIDOverrides, req.ReleaseNameOverrides, req.MetadataOverrides, req.TrackerConfigOverrides, req.TrackerSiteOverrides, req.ClientOverrides, req.TorrentOverrides, req.ImageHostOverrides, req.ScreenshotOverrides)
		if cached, ok := c.getGUICachedMeta(uniquePaths[0], signature, req.ExternalIDOverrides); ok {
			return c.services.Screenshots.SaveFinalSelections(ctx, cached, images)
		}
		return errors.New("core: screenshot selection save requires metadata preview")
	}

	options, err := c.applyDefaultOptions(req.Options)
	if err != nil {
		return err
	}
	singleReq := req
	singleReq.Paths = []string{uniquePaths[0]}
	singleReq.Options = options

	meta, err := c.services.Metadata.Prepare(ctx, singleReq)
	if err != nil {
		return err
	}

	return c.services.Screenshots.SaveFinalSelections(ctx, meta, images)
}

func (c *Core) ListUploadCandidates(ctx context.Context, req api.Request) ([]api.ScreenshotImage, error) {
	if len(req.Paths) == 0 {
		return nil, internalerrors.ErrInvalidInput
	}
	if c.services.Images == nil {
		return nil, errors.New("core: image hosting service not configured")
	}

	normalizedPaths, err := c.services.Filesystem.ValidatePaths(ctx, req.Paths)
	if err != nil {
		return nil, err
	}
	uniquePaths := make([]string, 0, len(normalizedPaths))
	seenPaths := make(map[string]struct{}, len(normalizedPaths))
	for _, path := range normalizedPaths {
		if _, exists := seenPaths[path]; exists {
			continue
		}
		seenPaths[path] = struct{}{}
		uniquePaths = append(uniquePaths, path)
	}
	if len(uniquePaths) != 1 {
		return nil, internalerrors.ErrInvalidInput
	}

	var meta api.PreparedMetadata
	if req.Mode == api.ModeGUI {
		signature := overrideSignature(req.ExternalIDOverrides, req.ReleaseNameOverrides, req.MetadataOverrides, req.TrackerConfigOverrides, req.TrackerSiteOverrides, req.ClientOverrides, req.TorrentOverrides, req.ImageHostOverrides, req.ScreenshotOverrides)
		if cached, ok := c.getGUICachedMeta(uniquePaths[0], signature, req.ExternalIDOverrides); ok {
			meta = cached
		} else {
			meta = api.PreparedMetadata{SourcePath: uniquePaths[0]}
		}
	} else {
		options, err := c.applyDefaultOptions(req.Options)
		if err != nil {
			return nil, err
		}
		singleReq := req
		singleReq.Paths = []string{uniquePaths[0]}
		singleReq.Options = options

		preparedMeta, err := c.services.Metadata.Prepare(ctx, singleReq)
		if err != nil {
			return nil, err
		}
		meta = preparedMeta
	}

	return c.services.Images.ListCandidates(ctx, meta)
}

func (c *Core) ListUploadedImages(ctx context.Context, req api.Request) ([]api.UploadedImageLink, error) {
	if len(req.Paths) == 0 {
		return nil, internalerrors.ErrInvalidInput
	}
	if c.repo == nil {
		return nil, errors.New("core: repository not configured")
	}

	normalizedPaths, err := c.services.Filesystem.ValidatePaths(ctx, req.Paths)
	if err != nil {
		return nil, err
	}
	uniquePaths := make([]string, 0, len(normalizedPaths))
	seenPaths := make(map[string]struct{}, len(normalizedPaths))
	for _, path := range normalizedPaths {
		if _, exists := seenPaths[path]; exists {
			continue
		}
		seenPaths[path] = struct{}{}
		uniquePaths = append(uniquePaths, path)
	}
	if len(uniquePaths) != 1 {
		return nil, internalerrors.ErrInvalidInput
	}

	return c.repo.ListUploadedImagesByPath(ctx, uniquePaths[0])
}

func (c *Core) UploadImages(ctx context.Context, req api.Request, host string, images []api.ScreenshotImage) ([]api.UploadedImageLink, error) {
	if len(req.Paths) == 0 {
		return nil, internalerrors.ErrInvalidInput
	}
	if c.services.Images == nil {
		return nil, errors.New("core: image hosting service not configured")
	}
	if len(images) == 0 {
		return nil, internalerrors.ErrInvalidInput
	}

	normalizedPaths, err := c.services.Filesystem.ValidatePaths(ctx, req.Paths)
	if err != nil {
		return nil, err
	}
	uniquePaths := make([]string, 0, len(normalizedPaths))
	seenPaths := make(map[string]struct{}, len(normalizedPaths))
	for _, path := range normalizedPaths {
		if _, exists := seenPaths[path]; exists {
			continue
		}
		seenPaths[path] = struct{}{}
		uniquePaths = append(uniquePaths, path)
	}
	if len(uniquePaths) != 1 {
		return nil, internalerrors.ErrInvalidInput
	}

	var meta api.PreparedMetadata
	if req.Mode == api.ModeGUI {
		signature := overrideSignature(req.ExternalIDOverrides, req.ReleaseNameOverrides, req.MetadataOverrides, req.TrackerConfigOverrides, req.TrackerSiteOverrides, req.ClientOverrides, req.TorrentOverrides, req.ImageHostOverrides)
		if cached, ok := c.getGUICachedMeta(uniquePaths[0], signature, req.ExternalIDOverrides); ok {
			meta = cached
		} else {
			meta = api.PreparedMetadata{SourcePath: uniquePaths[0]}
		}
	} else {
		options, err := c.applyDefaultOptions(req.Options)
		if err != nil {
			return nil, err
		}
		singleReq := req
		singleReq.Paths = []string{uniquePaths[0]}
		singleReq.Options = options

		preparedMeta, err := c.services.Metadata.Prepare(ctx, singleReq)
		if err != nil {
			return nil, err
		}
		meta = preparedMeta
	}

	return c.services.Images.Upload(ctx, meta, host, "global", images)
}

func (c *Core) DeleteUploadedImage(ctx context.Context, req api.Request, imagePath string, host string) error {
	if len(req.Paths) == 0 {
		return internalerrors.ErrInvalidInput
	}
	if c.repo == nil {
		return errors.New("core: repository not configured")
	}
	if strings.TrimSpace(imagePath) == "" {
		return internalerrors.ErrInvalidInput
	}
	if strings.TrimSpace(host) == "" {
		return internalerrors.ErrInvalidInput
	}

	normalizedPaths, err := c.services.Filesystem.ValidatePaths(ctx, req.Paths)
	if err != nil {
		return err
	}
	uniquePaths := make([]string, 0, len(normalizedPaths))
	seenPaths := make(map[string]struct{}, len(normalizedPaths))
	for _, path := range normalizedPaths {
		if _, exists := seenPaths[path]; exists {
			continue
		}
		seenPaths[path] = struct{}{}
		uniquePaths = append(uniquePaths, path)
	}
	if len(uniquePaths) != 1 {
		return internalerrors.ErrInvalidInput
	}

	c.logger.Debugf("core: deleting uploaded image %s (%s)", imagePath, host)
	return c.repo.DeleteUploadedImage(ctx, uniquePaths[0], imagePath, host)
}

func (c *Core) FetchMetadataPreview(ctx context.Context, req api.Request) (api.MetadataPreview, error) {
	req = normalizeExecutionRequest(req)
	if len(req.Paths) == 0 {
		return api.MetadataPreview{}, internalerrors.ErrInvalidInput
	}
	c.logger.Debugf("core: metadata preview request with %d paths", len(req.Paths))

	normalizedPaths, err := c.services.Filesystem.ValidatePaths(ctx, req.Paths)
	if err != nil {
		return api.MetadataPreview{}, err
	}

	uniquePaths := make([]string, 0, len(normalizedPaths))
	seenPaths := make(map[string]struct{}, len(normalizedPaths))
	for _, path := range normalizedPaths {
		if _, exists := seenPaths[path]; exists {
			continue
		}
		seenPaths[path] = struct{}{}
		uniquePaths = append(uniquePaths, path)
	}
	if len(uniquePaths) != 1 {
		return api.MetadataPreview{}, internalerrors.ErrInvalidInput
	}

	options, err := c.applyDefaultOptions(req.Options)
	if err != nil {
		return api.MetadataPreview{}, err
	}

	singleReq := req
	singleReq.Paths = []string{uniquePaths[0]}
	singleReq.Options = options
	singleReq.ExternalIDOverrides = mergeExternalIDOverrides(req.ExternalIDOverrides, resolveExternalIDSelection(req.ExternalIDSelections, uniquePaths[0]))

	if err := c.resetContentForExternalOverrides(ctx, uniquePaths[0], singleReq.ExternalIDOverrides); err != nil {
		return api.MetadataPreview{}, err
	}
	progressPath := uniquePaths[0]

	emitProgress := func(phase string, status string, message string) {
		api.EmitMetadataProgress(ctx, api.MetadataProgressUpdate{
			Path:    progressPath,
			Phase:   phase,
			Status:  status,
			Message: message,
			Level:   "info",
		})
	}

	emitPhase := func(phase string, message string, run func() error) error {
		emitProgress(phase, "running", message)
		if err := run(); err != nil {
			emitProgress(phase, "failed", err.Error())
			return err
		}
		emitProgress(phase, "completed", message)
		return nil
	}

	var meta api.PreparedMetadata
	if err := emitPhase("prepare", "Preparing metadata", func() error {
		var prepareErr error
		meta, prepareErr = c.services.Metadata.Prepare(ctx, singleReq)
		if prepareErr == nil {
			progressPath = meta.SourcePath
		}
		return prepareErr
	}); err != nil {
		return api.MetadataPreview{}, err
	}
	c.logger.Debugf("core: metadata prepared for %s", meta.SourcePath)

	storedApplied, err := c.applyStoredTrackerData(ctx, &meta)
	if err != nil {
		return api.MetadataPreview{}, err
	}
	runPathedSearch := func(applyTrackerData bool) error {
		if meta.Options.SkipAutoTorrent {
			c.logger.Debugf("core: skip_auto_torrent enabled, skipping pathed search for %s", meta.SourcePath)
			return nil
		}

		c.logger.Debugf("core: running pathed search for %s", meta.SourcePath)
		searchResult, searchErr := c.services.Clients.SearchPathedTorrents(ctx, meta)
		if searchErr != nil {
			return searchErr
		}
		meta.FoundTrackerMatch = meta.FoundTrackerMatch || searchResult.FoundTrackerMatch
		meta.MatchedTrackers = mergeTrackerRemovals(meta.MatchedTrackers, searchResult.MatchedTrackers)
		meta.TrackersRemove = mergeTrackerRemovals(meta.TrackersRemove, searchResult.MatchedTrackers)
		if !applyTrackerData {
			c.logger.Debugf("core: pathed search merged tracker presence only for %s", meta.SourcePath)
			return nil
		}
		if searchResult.InfoHash != "" || len(searchResult.TorrentComments) > 0 {
			meta.InfoHash = searchResult.InfoHash
			meta.ClientTorrentPath = searchResult.TorrentPath
			meta.TrackerIDs = searchResult.TrackerIDs
			meta.TorrentComments = searchResult.TorrentComments
			meta.PieceSizeConstraint = searchResult.PieceSizeConstraint
			meta.FoundPreferredPiece = searchResult.FoundPreferredPiece
			c.logger.Debugf("core: pathed torrents resolved for %s", meta.SourcePath)
		} else {
			c.logger.Debugf("core: pathed search returned no matches for %s", meta.SourcePath)
		}
		return nil
	}

	switch {
	case meta.SourceLookupActive:
		c.logger.Debugf("core: running pathed search for tracker presence with source url override active for %s", meta.SourcePath)
		if err := runPathedSearch(false); err != nil {
			return api.MetadataPreview{}, err
		}
	case storedApplied:
		c.logger.Debugf("core: running pathed search for tracker presence with stored tracker data present for %s", meta.SourcePath)
		if err := runPathedSearch(false); err != nil {
			return api.MetadataPreview{}, err
		}
	case meta.StoredDataFresh:
		if meta.InfoHash == "" && meta.StoredInfoHash != "" {
			meta.InfoHash = meta.StoredInfoHash
			c.logger.Debugf("core: using stored infohash before pathed tracker-presence search for %s", meta.SourcePath)
		} else {
			c.logger.Debugf("core: running pathed search for tracker presence with fresh stored metadata snapshot for %s", meta.SourcePath)
		}
		if err := runPathedSearch(false); err != nil {
			return api.MetadataPreview{}, err
		}
	case meta.InfoHash != "":
		c.logger.Debugf("core: running pathed search for tracker presence with existing infohash for %s", meta.SourcePath)
		if err := runPathedSearch(false); err != nil {
			return api.MetadataPreview{}, err
		}
	case len(meta.TrackerIDs) > 0:
		c.logger.Debugf("core: running pathed search for tracker presence with existing tracker IDs for %s", meta.SourcePath)
		if err := runPathedSearch(false); err != nil {
			return api.MetadataPreview{}, err
		}
	default:
		if err := runPathedSearch(true); err != nil {
			return api.MetadataPreview{}, err
		}
	}

	if err := emitPhase("tracker-data", "Enriching tracker data", func() error {
		var enrichErr error
		meta, enrichErr = c.services.Metadata.EnrichTrackerData(ctx, meta)
		return enrichErr
	}); err != nil {
		return api.MetadataPreview{}, err
	}
	if meta.SourceLookupActive && strings.EqualFold(meta.SourceLookupMode, "tracker") && !sourceLookupTrackerHasUsableData(meta) {
		meta.LookupWarnings = append(meta.LookupWarnings, "Source URL lookup returned no tracker data; fell back to default metadata lookup flow.")
		meta.SourceLookupActive = false
		meta.SourceLookupMode = ""
		meta.SourceLookupTracker = ""
		meta.SourceLookupTrackerID = ""
		meta.Trackers = append([]string{}, singleReq.Trackers...)
		meta.TrackerIDs = nil
		meta.MatchedTrackers = nil
		meta.TrackerData = nil
		if err := runPathedSearch(true); err != nil {
			return api.MetadataPreview{}, err
		}
		if err := emitPhase("tracker-data-fallback", "Retrying tracker data lookup", func() error {
			var enrichErr error
			meta, enrichErr = c.services.Metadata.EnrichTrackerData(ctx, meta)
			return enrichErr
		}); err != nil {
			return api.MetadataPreview{}, err
		}
	}
	if err := emitPhase("mediainfo-ids", "Applying MediaInfo IDs", func() error {
		var applyErr error
		meta, applyErr = c.services.Metadata.ApplyMediaInfoIDs(ctx, meta)
		return applyErr
	}); err != nil {
		return api.MetadataPreview{}, err
	}
	if err := emitPhase("arr", "Applying Sonarr/Radarr data", func() error {
		var applyErr error
		meta, applyErr = c.services.Metadata.ApplyArrData(ctx, meta)
		return applyErr
	}); err != nil {
		return api.MetadataPreview{}, err
	}
	if err := emitPhase("external-ids", "Resolving external IDs", func() error {
		var resolveErr error
		meta, resolveErr = c.services.Metadata.ResolveExternalIDs(ctx, meta)
		return resolveErr
	}); err != nil {
		return api.MetadataPreview{}, err
	}
	if err := emitPhase("media-details", "Applying media details", func() error {
		var applyErr error
		meta, applyErr = c.services.Metadata.ApplyMediaDetails(ctx, meta)
		return applyErr
	}); err != nil {
		return api.MetadataPreview{}, err
	}

	c.storeDupeCache(meta.SourcePath, overrideSignature(meta.ExternalIDOverrides, meta.ReleaseNameOverrides, meta.MetadataOverrides, meta.TrackerConfigOverrides, meta.TrackerSiteOverrides, meta.ClientOverrides, meta.TorrentOverrides, meta.ImageHostOverrides, meta.ScreenshotOverrides), meta)

	emitProgress("complete", "completed", "Metadata preview ready")

	return buildMetadataPreview(meta, c.cfg), nil
}

func (c *Core) FetchPreparationPreview(ctx context.Context, req api.Request) (api.PreparationPreview, error) {
	resolvedReq, err := c.resolveDescriptionOverrideRequest(ctx, req)
	if err != nil {
		return api.PreparationPreview{}, err
	}
	req = resolvedReq

	if len(req.Paths) == 0 {
		return api.PreparationPreview{}, internalerrors.ErrInvalidInput
	}
	if c.services.Filesystem == nil {
		return api.PreparationPreview{}, errors.New("core: filesystem service not configured")
	}
	if c.services.Trackers == nil {
		return api.PreparationPreview{}, errors.New("core: tracker service not configured")
	}
	c.logger.Debugf("core: preparation preview request with %d paths", len(req.Paths))

	normalizedPaths, err := c.services.Filesystem.ValidatePaths(ctx, req.Paths)
	if err != nil {
		return api.PreparationPreview{}, err
	}

	uniquePaths := make([]string, 0, len(normalizedPaths))
	seenPaths := make(map[string]struct{}, len(normalizedPaths))
	for _, path := range normalizedPaths {
		if _, exists := seenPaths[path]; exists {
			continue
		}
		seenPaths[path] = struct{}{}
		uniquePaths = append(uniquePaths, path)
	}
	if len(uniquePaths) != 1 {
		return api.PreparationPreview{}, internalerrors.ErrInvalidInput
	}

	if req.Mode == api.ModeGUI {
		signature := overrideSignature(req.ExternalIDOverrides, req.ReleaseNameOverrides, req.MetadataOverrides, req.TrackerConfigOverrides, req.TrackerSiteOverrides, req.ClientOverrides, req.TorrentOverrides, req.ImageHostOverrides, req.ScreenshotOverrides)
		if cached, ok := c.getGUICachedMeta(uniquePaths[0], signature, req.ExternalIDOverrides); ok {
			cached = applyRequestToPreparedMeta(cached, req)
			resolvedTrackers := trackers.ResolveTrackersWithDefaults(c.cfg, req.Trackers, req.TrackersRemove, c.logger)
			c.logger.Debugf("core: preparation resolved trackers %v", resolvedTrackers)
			return c.services.Trackers.BuildPreparation(ctx, cached, resolvedTrackers)
		}
		// No cache available; fall through to Prepare (e.g., after playlist selection)
	}

	options, err := c.applyDefaultOptions(req.Options)
	if err != nil {
		return api.PreparationPreview{}, err
	}

	singleReq := req
	singleReq.Paths = []string{uniquePaths[0]}
	singleReq.Options = options

	meta, err := c.services.Metadata.Prepare(ctx, singleReq)
	if err != nil {
		return api.PreparationPreview{}, err
	}
	meta = applyRequestToPreparedMeta(meta, singleReq)

	resolvedTrackers := trackers.ResolveTrackersWithDefaults(c.cfg, req.Trackers, req.TrackersRemove, c.logger)
	c.logger.Debugf("core: preparation resolved trackers %v", resolvedTrackers)
	return c.services.Trackers.BuildPreparation(ctx, meta, resolvedTrackers)
}

func (c *Core) FetchTrackerDryRunPreview(ctx context.Context, req api.Request) (api.TrackerDryRunPreview, error) {
	req = normalizeExecutionRequest(req)
	resolvedReq, err := c.resolveDescriptionOverrideRequest(ctx, req)
	if err != nil {
		return api.TrackerDryRunPreview{}, err
	}
	req = resolvedReq

	if len(req.Paths) == 0 {
		return api.TrackerDryRunPreview{}, internalerrors.ErrInvalidInput
	}
	if c.services.Filesystem == nil {
		return api.TrackerDryRunPreview{}, errors.New("core: filesystem service not configured")
	}
	if c.services.Metadata == nil {
		return api.TrackerDryRunPreview{}, errors.New("core: metadata service not configured")
	}
	if c.services.Torrents == nil {
		return api.TrackerDryRunPreview{}, errors.New("core: torrent service not configured")
	}
	if c.services.Trackers == nil {
		return api.TrackerDryRunPreview{}, errors.New("core: tracker service not configured")
	}

	c.logger.Debugf("core: tracker dry-run preview request with %d paths", len(req.Paths))

	normalizedPaths, err := c.services.Filesystem.ValidatePaths(ctx, req.Paths)
	if err != nil {
		return api.TrackerDryRunPreview{}, err
	}

	uniquePaths := make([]string, 0, len(normalizedPaths))
	seenPaths := make(map[string]struct{}, len(normalizedPaths))
	for _, path := range normalizedPaths {
		if _, exists := seenPaths[path]; exists {
			continue
		}
		seenPaths[path] = struct{}{}
		uniquePaths = append(uniquePaths, path)
	}
	if len(uniquePaths) != 1 {
		return api.TrackerDryRunPreview{}, internalerrors.ErrInvalidInput
	}

	options, err := c.applyDefaultOptions(req.Options)
	if err != nil {
		return api.TrackerDryRunPreview{}, err
	}
	options.DryRun = true

	singleReq := req
	singleReq.Paths = []string{uniquePaths[0]}
	singleReq.Options = options
	singleReq.ExternalIDOverrides = mergeExternalIDOverrides(req.ExternalIDOverrides, resolveExternalIDSelection(req.ExternalIDSelections, uniquePaths[0]))

	signature := overrideSignature(singleReq.ExternalIDOverrides, singleReq.ReleaseNameOverrides, singleReq.MetadataOverrides, singleReq.TrackerConfigOverrides, singleReq.TrackerSiteOverrides, singleReq.ClientOverrides, singleReq.TorrentOverrides, singleReq.ImageHostOverrides, singleReq.ScreenshotOverrides)
	meta, ok := c.getDupeCache(uniquePaths[0], signature)
	if !ok {
		return api.TrackerDryRunPreview{}, fmt.Errorf("core: tracker dry-run requires prepared metadata for %s", uniquePaths[0])
	}
	c.logger.Debugf("core: tracker dry-run using cached prepared metadata for %s", uniquePaths[0])

	meta.Mode = singleReq.Mode
	meta = applyRequestToPreparedMeta(meta, singleReq)
	descriptionGroups, err := c.resolveCanonicalDescriptionGroups(ctx, meta, singleReq)
	if err != nil {
		return api.TrackerDryRunPreview{}, err
	}
	meta.DescriptionGroups = descriptionGroups

	torrent, err := c.services.Torrents.Create(ctx, meta)
	if err != nil {
		return api.TrackerDryRunPreview{}, err
	}
	meta.TorrentPath = torrent.Path

	resolvedTrackers := trackers.ResolveTrackersWithDefaults(c.cfg, req.Trackers, req.TrackersRemove, c.logger)
	entries, err := c.services.Trackers.BuildUploadDryRun(ctx, meta, resolvedTrackers)
	if err != nil {
		return api.TrackerDryRunPreview{}, err
	}

	c.storeDupeCache(meta.SourcePath, overrideSignature(meta.ExternalIDOverrides, meta.ReleaseNameOverrides, meta.MetadataOverrides, meta.TrackerConfigOverrides, meta.TrackerSiteOverrides, meta.ClientOverrides, meta.TorrentOverrides, meta.ImageHostOverrides, meta.ScreenshotOverrides), meta)

	return api.TrackerDryRunPreview{SourcePath: meta.SourcePath, Trackers: entries}, nil
}

func (c *Core) FetchDescriptionBuilderPreview(ctx context.Context, req api.Request) (api.DescriptionBuilderPreview, error) {
	resolvedReq, err := c.resolveDescriptionOverrideRequest(ctx, req)
	if err != nil {
		return api.DescriptionBuilderPreview{}, err
	}
	req = resolvedReq

	if len(req.Paths) == 0 {
		return api.DescriptionBuilderPreview{}, internalerrors.ErrInvalidInput
	}
	if c.services.Filesystem == nil {
		return api.DescriptionBuilderPreview{}, errors.New("core: filesystem service not configured")
	}
	if c.services.Trackers == nil {
		return api.DescriptionBuilderPreview{}, errors.New("core: tracker service not configured")
	}
	if c.repo == nil {
		return api.DescriptionBuilderPreview{}, errors.New("core: repository not configured")
	}
	if req.Mode != api.ModeGUI && c.services.Metadata == nil {
		return api.DescriptionBuilderPreview{}, errors.New("core: metadata service not configured")
	}
	c.logger.Debugf("core: description builder request with %d paths", len(req.Paths))

	normalizedPaths, err := c.services.Filesystem.ValidatePaths(ctx, req.Paths)
	if err != nil {
		return api.DescriptionBuilderPreview{}, err
	}

	uniquePaths := make([]string, 0, len(normalizedPaths))
	seenPaths := make(map[string]struct{}, len(normalizedPaths))
	for _, path := range normalizedPaths {
		if _, exists := seenPaths[path]; exists {
			continue
		}
		seenPaths[path] = struct{}{}
		uniquePaths = append(uniquePaths, path)
	}
	if len(uniquePaths) != 1 {
		return api.DescriptionBuilderPreview{}, internalerrors.ErrInvalidInput
	}

	storedOverrides, err := c.repo.ListDescriptionOverridesByPath(ctx, uniquePaths[0])
	if err != nil && !errors.Is(err, internalerrors.ErrNotFound) {
		return api.DescriptionBuilderPreview{}, fmt.Errorf("core: description override: %w", err)
	}
	overrideByGroup := make(map[string]api.DescriptionOverride, len(storedOverrides))
	for _, override := range storedOverrides {
		overrideByGroup[strings.TrimSpace(override.GroupKey)] = override
	}

	var meta api.PreparedMetadata
	signature := overrideSignature(req.ExternalIDOverrides, req.ReleaseNameOverrides, req.MetadataOverrides, req.TrackerConfigOverrides, req.TrackerSiteOverrides, req.ClientOverrides, req.TorrentOverrides, req.ImageHostOverrides, req.ScreenshotOverrides)
	if req.Mode == api.ModeGUI {
		if cached, ok := c.getGUICachedMeta(uniquePaths[0], signature, req.ExternalIDOverrides); ok {
			c.logger.Debugf("core: description builder cache hit source=%s signature=%q", uniquePaths[0], signature)
			meta = applyRequestToPreparedMeta(cached, req)
		}
	}
	if strings.TrimSpace(meta.SourcePath) == "" {
		if c.services.Metadata == nil {
			c.logger.Warnf("core: description builder missing metadata service for source=%s", uniquePaths[0])
			if len(overrideByGroup) > 0 {
				groupKeys := make([]string, 0, len(overrideByGroup))
				for groupKey := range overrideByGroup {
					groupKeys = append(groupKeys, groupKey)
				}
				sort.Strings(groupKeys)
				preview := api.DescriptionBuilderPreview{
					SourcePath: uniquePaths[0],
					Groups:     make([]api.DescriptionBuilderGroup, 0, len(groupKeys)),
				}
				for _, groupKey := range groupKeys {
					override := overrideByGroup[groupKey]
					preview.Groups = append(preview.Groups, api.DescriptionBuilderGroup{
						GroupKey:           groupKey,
						Trackers:           nil,
						RawDescription:     override.Description,
						RawDescriptionHTML: description.Render(override.Description),
						HasOverride:        true,
					})
				}
				return preview, nil
			}
			return api.DescriptionBuilderPreview{}, errors.New("core: metadata service not configured")
		}
		c.logger.Debugf("core: description builder preparing metadata source=%s", uniquePaths[0])
		options, err := c.applyDefaultOptions(req.Options)
		if err != nil {
			return api.DescriptionBuilderPreview{}, err
		}
		singleReq := req
		singleReq.Paths = []string{uniquePaths[0]}
		singleReq.Options = options
		meta, err = c.services.Metadata.Prepare(ctx, singleReq)
		if err != nil {
			return api.DescriptionBuilderPreview{}, err
		}
		meta = applyRequestToPreparedMeta(meta, singleReq)
		if req.Mode == api.ModeGUI {
			c.storeDupeCache(meta.SourcePath, signature, meta)
		}
	}

	resolvedTrackers := trackers.ResolveTrackersWithDefaults(c.cfg, req.Trackers, req.TrackersRemove, c.logger)
	prep, err := c.services.Trackers.BuildPreparation(ctx, meta, resolvedTrackers)
	if err != nil {
		c.logger.Errorf("core: description builder preparation failed source=%s: %v", meta.SourcePath, err)
		return api.DescriptionBuilderPreview{}, err
	}

	preview := api.DescriptionBuilderPreview{SourcePath: meta.SourcePath}
	if len(prep.Descriptions) > 0 {
		preview.Groups = make([]api.DescriptionBuilderGroup, 0, len(prep.Descriptions))
		for _, entry := range prep.Descriptions {
			preview.Groups = append(preview.Groups, buildDescriptionBuilderGroup(entry, overrideByGroup))
		}
	}
	c.logger.Infof("core: description builder prepared source=%s groups=%d", preview.SourcePath, len(preview.Groups))
	return preview, nil
}

func buildDescriptionBuilderGroup(entry api.PreparationDescription, overrideByGroup map[string]api.DescriptionOverride) api.DescriptionBuilderGroup {
	groupKey := normalizeDescriptionBuilderGroupKey(entry.GroupKey, entry.Trackers)
	rawDescription := entry.RawDescription
	if strings.TrimSpace(rawDescription) == "" {
		rawDescription = entry.Description
	}
	hasOverride := entry.HasOverride
	if override, ok := overrideByGroup[groupKey]; ok && strings.TrimSpace(override.Description) != "" {
		rawDescription = override.Description
		hasOverride = true
	}
	return api.DescriptionBuilderGroup{
		GroupKey:           groupKey,
		Trackers:           append([]string{}, entry.Trackers...),
		RawDescription:     rawDescription,
		RawDescriptionHTML: description.Render(rawDescription),
		HasOverride:        hasOverride,
		ImageHost:          entry.ImageHost,
	}
}

func normalizeDescriptionBuilderGroupKey(groupKey string, trackersList []string) string {
	normalized := strings.TrimSpace(groupKey)
	if normalized == "" && len(trackersList) > 0 {
		normalized = strings.ToLower(strings.TrimSpace(trackersList[0]))
	}
	return normalized
}

func (c *Core) FetchDescriptionBuilderGroupPreview(ctx context.Context, req api.Request) (api.DescriptionBuilderGroup, error) {
	targetGroup := strings.TrimSpace(req.DescriptionOverrideGroup)
	if targetGroup == "" && len(req.Trackers) > 0 {
		targetGroup = trackers.DescriptionOverrideGroupForTracker(req.Trackers[0])
	}
	targetGroup = normalizeDescriptionBuilderGroupKey(targetGroup, req.Trackers)
	if targetGroup == "" {
		return api.DescriptionBuilderGroup{}, internalerrors.ErrInvalidInput
	}

	resolvedReq, err := c.resolveDescriptionOverrideRequest(ctx, req)
	if err != nil {
		return api.DescriptionBuilderGroup{}, err
	}
	req = resolvedReq

	if len(req.Paths) == 0 {
		return api.DescriptionBuilderGroup{}, internalerrors.ErrInvalidInput
	}
	if c.services.Filesystem == nil {
		return api.DescriptionBuilderGroup{}, errors.New("core: filesystem service not configured")
	}
	if c.services.Trackers == nil {
		return api.DescriptionBuilderGroup{}, errors.New("core: tracker service not configured")
	}
	if c.repo == nil {
		return api.DescriptionBuilderGroup{}, errors.New("core: repository not configured")
	}
	if req.Mode != api.ModeGUI && c.services.Metadata == nil {
		return api.DescriptionBuilderGroup{}, errors.New("core: metadata service not configured")
	}

	normalizedPaths, err := c.services.Filesystem.ValidatePaths(ctx, req.Paths)
	if err != nil {
		return api.DescriptionBuilderGroup{}, err
	}
	uniquePaths := make([]string, 0, len(normalizedPaths))
	seenPaths := make(map[string]struct{}, len(normalizedPaths))
	for _, candidate := range normalizedPaths {
		if _, exists := seenPaths[candidate]; exists {
			continue
		}
		seenPaths[candidate] = struct{}{}
		uniquePaths = append(uniquePaths, candidate)
	}
	if len(uniquePaths) != 1 {
		return api.DescriptionBuilderGroup{}, internalerrors.ErrInvalidInput
	}

	storedOverrides, err := c.repo.ListDescriptionOverridesByPath(ctx, uniquePaths[0])
	if err != nil && !errors.Is(err, internalerrors.ErrNotFound) {
		return api.DescriptionBuilderGroup{}, fmt.Errorf("core: description override: %w", err)
	}
	overrideByGroup := make(map[string]api.DescriptionOverride, len(storedOverrides))
	for _, override := range storedOverrides {
		overrideByGroup[strings.TrimSpace(override.GroupKey)] = override
	}

	var meta api.PreparedMetadata
	signature := overrideSignature(req.ExternalIDOverrides, req.ReleaseNameOverrides, req.MetadataOverrides, req.TrackerConfigOverrides, req.TrackerSiteOverrides, req.ClientOverrides, req.TorrentOverrides, req.ImageHostOverrides, req.ScreenshotOverrides)
	if req.Mode == api.ModeGUI {
		if cached, ok := c.getGUICachedMeta(uniquePaths[0], signature, req.ExternalIDOverrides); ok {
			meta = applyRequestToPreparedMeta(cached, req)
		}
	}
	if strings.TrimSpace(meta.SourcePath) == "" {
		if c.services.Metadata == nil {
			if override, ok := overrideByGroup[targetGroup]; ok && strings.TrimSpace(override.Description) != "" {
				return api.DescriptionBuilderGroup{
					GroupKey:           targetGroup,
					Trackers:           append([]string{}, req.Trackers...),
					RawDescription:     override.Description,
					RawDescriptionHTML: description.Render(override.Description),
					HasOverride:        true,
				}, nil
			}
			return api.DescriptionBuilderGroup{}, errors.New("core: metadata service not configured")
		}
		options, err := c.applyDefaultOptions(req.Options)
		if err != nil {
			return api.DescriptionBuilderGroup{}, err
		}
		singleReq := req
		singleReq.Paths = []string{uniquePaths[0]}
		singleReq.Options = options
		meta, err = c.services.Metadata.Prepare(ctx, singleReq)
		if err != nil {
			return api.DescriptionBuilderGroup{}, err
		}
		meta = applyRequestToPreparedMeta(meta, singleReq)
		if req.Mode == api.ModeGUI {
			c.storeDupeCache(meta.SourcePath, signature, meta)
		}
	}

	resolvedTrackers := req.Trackers
	if len(resolvedTrackers) == 0 {
		resolvedTrackers = trackers.ResolveTrackersWithDefaults(c.cfg, req.Trackers, req.TrackersRemove, c.logger)
	}
	prep, err := c.services.Trackers.BuildPreparation(ctx, meta, resolvedTrackers)
	if err != nil {
		return api.DescriptionBuilderGroup{}, err
	}
	for _, entry := range prep.Descriptions {
		normalizedGroupKey := normalizeDescriptionBuilderGroupKey(entry.GroupKey, entry.Trackers)
		if strings.EqualFold(normalizedGroupKey, targetGroup) {
			group := buildDescriptionBuilderGroup(entry, overrideByGroup)
			if len(group.Trackers) == 0 && len(req.Trackers) > 0 {
				group.Trackers = append([]string{}, req.Trackers...)
			}
			return group, nil
		}
	}
	return api.DescriptionBuilderGroup{}, internalerrors.ErrNotFound
}

func (c *Core) storeDupeCache(path string, signature string, meta api.PreparedMetadata) {
	if strings.TrimSpace(path) == "" {
		return
	}
	c.dupeMu.Lock()
	defer c.dupeMu.Unlock()
	c.dupeCache[path] = dupeCacheEntry{meta: meta, signature: signature, updatedAt: time.Now().UTC()}
}

func (c *Core) clearDupeCache(path string) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return
	}
	c.dupeMu.Lock()
	defer c.dupeMu.Unlock()
	delete(c.dupeCache, trimmedPath)
}

func (c *Core) resetContentForExternalOverrides(ctx context.Context, path string, overrides api.ExternalIDOverrides) error {
	if !hasExternalIDOverrides(overrides) || c.repo == nil {
		return nil
	}
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return nil
	}
	if err := c.repo.PurgeContentData(ctx, trimmedPath); err != nil {
		return fmt.Errorf("core: purge content data for external override %s: %w", trimmedPath, err)
	}
	c.clearDupeCache(trimmedPath)
	c.logger.Debugf("core: purged content data for external override path=%s", trimmedPath)
	return nil
}

func (c *Core) applyStoredTrackerData(ctx context.Context, meta *api.PreparedMetadata) (bool, error) {
	if c.repo == nil || meta == nil {
		return false, nil
	}
	path := strings.TrimSpace(meta.SourcePath)
	if path == "" {
		return false, nil
	}
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	default:
	}
	records, err := c.repo.ListTrackerMetadataByPath(ctx, path)
	if err != nil {
		return false, fmt.Errorf("core: tracker metadata lookup: %w", err)
	}
	if len(meta.Trackers) > 0 && len(records) > 0 {
		allowed := make(map[string]struct{}, len(meta.Trackers))
		for _, tracker := range meta.Trackers {
			trimmed := strings.TrimSpace(tracker)
			if trimmed == "" {
				continue
			}
			allowed[strings.ToUpper(trimmed)] = struct{}{}
		}
		if len(allowed) > 0 {
			filtered := records[:0]
			for _, record := range records {
				key := strings.ToUpper(strings.TrimSpace(record.Tracker))
				if key == "" {
					continue
				}
				if _, ok := allowed[key]; !ok {
					continue
				}
				filtered = append(filtered, record)
			}
			records = filtered
		}
	}
	if len(records) == 0 {
		return false, nil
	}

	if len(meta.TrackerData) == 0 {
		meta.TrackerData = append([]api.TrackerMetadata{}, records...)
	} else {
		seen := make(map[string]struct{}, len(meta.TrackerData))
		for _, record := range meta.TrackerData {
			key := strings.ToLower(strings.TrimSpace(record.Tracker))
			if key == "" {
				continue
			}
			seen[key] = struct{}{}
		}
		for _, record := range records {
			key := strings.ToLower(strings.TrimSpace(record.Tracker))
			if key == "" {
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			meta.TrackerData = append(meta.TrackerData, record)
			seen[key] = struct{}{}
		}
	}

	if meta.TrackerIDs == nil {
		meta.TrackerIDs = make(map[string]string)
	}
	matched := make([]string, 0, len(records))
	for _, record := range records {
		key := strings.ToLower(strings.TrimSpace(record.Tracker))
		if key != "" && strings.TrimSpace(record.TrackerID) != "" {
			if _, exists := meta.TrackerIDs[key]; !exists {
				meta.TrackerIDs[key] = strings.TrimSpace(record.TrackerID)
			}
		}
		if meta.InfoHash == "" && strings.TrimSpace(record.InfoHash) != "" {
			meta.InfoHash = strings.TrimSpace(record.InfoHash)
		}
		if record.Matched {
			matched = append(matched, record.Tracker)
		}
	}
	if len(matched) > 0 {
		meta.MatchedTrackers = mergeTrackerRemovals(meta.MatchedTrackers, matched)
		meta.FoundTrackerMatch = true
		meta.TrackersRemove = mergeTrackerRemovals(meta.TrackersRemove, meta.MatchedTrackers)
	}

	return true, nil
}

func (c *Core) getDupeCache(path string, signature string) (api.PreparedMetadata, bool) {
	if strings.TrimSpace(path) == "" {
		return api.PreparedMetadata{}, false
	}
	c.dupeMu.RLock()
	defer c.dupeMu.RUnlock()
	entry, ok := c.dupeCache[path]
	if !ok {
		return api.PreparedMetadata{}, false
	}
	if signature != "" && entry.signature != signature {
		return api.PreparedMetadata{}, false
	}
	return entry.meta, true
}

func (c *Core) getGUICachedMeta(path string, signature string, overrides api.ExternalIDOverrides) (api.PreparedMetadata, bool) {
	if cached, ok := c.getDupeCache(path, signature); ok {
		return cached, true
	}
	if hasExternalIDOverrides(overrides) {
		return api.PreparedMetadata{}, false
	}
	return c.getDupeCache(path, "")
}

// ExportGUICachedPreparedMeta exposes the resolved GUI prepared metadata cache entry
// so callers can hand off metadata to isolated per-run cores.
func (c *Core) ExportGUICachedPreparedMeta(ctx context.Context, req api.Request) (api.PreparedMetadata, bool, error) {
	if err := ctx.Err(); err != nil {
		return api.PreparedMetadata{}, false, err
	}
	path, err := c.resolveSinglePreparedMetaPath(ctx, req.Paths)
	if err != nil {
		return api.PreparedMetadata{}, false, err
	}
	overrides := mergeExternalIDOverrides(req.ExternalIDOverrides, resolveExternalIDSelection(req.ExternalIDSelections, path))
	signature := overrideSignature(overrides, req.ReleaseNameOverrides, req.MetadataOverrides, req.TrackerConfigOverrides, req.TrackerSiteOverrides, req.ClientOverrides, req.TorrentOverrides, req.ImageHostOverrides, req.ScreenshotOverrides)
	meta, ok := c.getGUICachedMeta(path, signature, overrides)
	if !ok {
		return api.PreparedMetadata{}, false, nil
	}
	return deepCopyPreparedMetadata(meta), true, nil
}

// ImportPreparedMetadataForGUI stores prepared metadata on a per-run core so GUI dry-run
// and upload-only flows can reuse metadata prepared on the long-lived GUI core.
func (c *Core) ImportPreparedMetadataForGUI(ctx context.Context, req api.Request, meta api.PreparedMetadata) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := c.resolveSinglePreparedMetaPath(ctx, req.Paths)
	if err != nil {
		return err
	}
	overrides := mergeExternalIDOverrides(req.ExternalIDOverrides, resolveExternalIDSelection(req.ExternalIDSelections, path))
	signature := overrideSignature(overrides, req.ReleaseNameOverrides, req.MetadataOverrides, req.TrackerConfigOverrides, req.TrackerSiteOverrides, req.ClientOverrides, req.TorrentOverrides, req.ImageHostOverrides, req.ScreenshotOverrides)
	c.storeDupeCache(path, signature, deepCopyPreparedMetadata(meta))
	return nil
}

func (c *Core) resolveSinglePreparedMetaPath(ctx context.Context, paths []string) (string, error) {
	if len(paths) == 0 {
		return "", internalerrors.ErrInvalidInput
	}
	if c.services.Filesystem == nil {
		return "", errors.New("core: filesystem service not configured")
	}

	normalizedPaths, err := c.services.Filesystem.ValidatePaths(ctx, paths)
	if err != nil {
		return "", err
	}
	if len(normalizedPaths) == 0 {
		return "", internalerrors.ErrInvalidInput
	}

	first := normalizedPaths[0]
	for _, path := range normalizedPaths[1:] {
		if path != first {
			return "", internalerrors.ErrInvalidInput
		}
	}
	return first, nil
}

func deepCopyPreparedMetadata(meta api.PreparedMetadata) api.PreparedMetadata {
	copyMeta := meta
	copyMeta.LookupWarnings = append([]string(nil), meta.LookupWarnings...)
	copyMeta.Paths = append([]string(nil), meta.Paths...)
	copyMeta.FileList = append([]string(nil), meta.FileList...)
	copyMeta.DescriptionGroups = cloneDescriptionBuilderGroups(meta.DescriptionGroups)
	copyMeta.Trackers = append([]string(nil), meta.Trackers...)
	copyMeta.TrackersRemove = append([]string(nil), meta.TrackersRemove...)
	copyMeta.MatchedTrackers = append([]string(nil), meta.MatchedTrackers...)
	copyMeta.Release = deepCopyReleaseInfo(meta.Release)
	copyMeta.TagOverride = deepCopyTagOverride(meta.TagOverride)
	copyMeta.MetadataOverrides = deepCopyMetadataOverrides(meta.MetadataOverrides)
	copyMeta.TrackerConfigOverrides = deepCopyTrackerConfigOverrides(meta.TrackerConfigOverrides)
	copyMeta.TrackerSiteOverrides = deepCopyTrackerSiteOverrides(meta.TrackerSiteOverrides)
	copyMeta.ClientOverrides = deepCopyClientOverrides(meta.ClientOverrides)
	copyMeta.ImageHostOverrides = deepCopyImageHostOverrides(meta.ImageHostOverrides)
	copyMeta.ScreenshotOverrides = deepCopyScreenshotOverrides(meta.ScreenshotOverrides)
	copyMeta.TorrentOverrides = deepCopyTorrentOverrides(meta.TorrentOverrides)
	copyMeta.TrackerIDs = cloneStringMap(meta.TrackerIDs)
	copyMeta.TorrentComments = deepCopyTorrentMatches(meta.TorrentComments)
	copyMeta.TrackerData = deepCopyTrackerMetadata(meta.TrackerData)
	copyMeta.ArrGenres = append([]string(nil), meta.ArrGenres...)
	copyMeta.ExternalIDOverrides = deepCopyExternalIDOverrides(meta.ExternalIDOverrides)
	copyMeta.ReleaseNameOverrides = deepCopyReleaseNameOverrides(meta.ReleaseNameOverrides)
	copyMeta.TrackerQuestionnaireAnswers = deepCopyQuestionnaireAnswers(meta.TrackerQuestionnaireAnswers)
	copyMeta.TVDBAirsDays = append([]string(nil), meta.TVDBAirsDays...)
	copyMeta.SelectedBDMVPlaylists = deepCopyPlaylistInfos(meta.SelectedBDMVPlaylists)
	copyMeta.ExternalIDCandidates = deepCopyExternalIDCandidates(meta.ExternalIDCandidates)
	copyMeta.ExternalMetadata = deepCopyExternalMetadata(meta.ExternalMetadata)
	copyMeta.AudioLanguages = append([]string(nil), meta.AudioLanguages...)
	copyMeta.SubtitleLanguages = append([]string(nil), meta.SubtitleLanguages...)
	copyMeta.ReleaseNameMissing = append([]string(nil), meta.ReleaseNameMissing...)
	copyMeta.BlockedTrackers = deepCopyBlockedTrackers(meta.BlockedTrackers)
	copyMeta.TrackerRuleFailures = deepCopyTrackerRuleFailures(meta.TrackerRuleFailures)
	copyMeta.BDInfo = deepCopyStringInterfaceMap(meta.BDInfo)
	return copyMeta
}

func deepCopyReleaseInfo(info api.ReleaseInfo) api.ReleaseInfo {
	copyInfo := info
	copyInfo.Codec = append([]string(nil), info.Codec...)
	copyInfo.Audio = append([]string(nil), info.Audio...)
	copyInfo.HDR = append([]string(nil), info.HDR...)
	copyInfo.Language = append([]string(nil), info.Language...)
	copyInfo.Edition = append([]string(nil), info.Edition...)
	copyInfo.Other = append([]string(nil), info.Other...)
	return copyInfo
}

func deepCopyTagOverride(tag *api.TagOverride) *api.TagOverride {
	if tag == nil {
		return nil
	}
	copyTag := *tag
	return &copyTag
}

func deepCopyMetadataOverrides(overrides api.MetadataOverrides) api.MetadataOverrides {
	return api.MetadataOverrides{
		Distributor:      clonePtr(overrides.Distributor),
		OriginalLanguage: clonePtr(overrides.OriginalLanguage),
		PersonalRelease:  clonePtr(overrides.PersonalRelease),
		Commentary:       clonePtr(overrides.Commentary),
		WebDV:            clonePtr(overrides.WebDV),
		StreamOptimized:  clonePtr(overrides.StreamOptimized),
		Anime:            clonePtr(overrides.Anime),
	}
}

func deepCopyTrackerConfigOverrides(overrides api.TrackerConfigOverrides) api.TrackerConfigOverrides {
	return api.TrackerConfigOverrides{
		Anon:    clonePtr(overrides.Anon),
		Draft:   clonePtr(overrides.Draft),
		ModQ:    clonePtr(overrides.ModQ),
		Channel: clonePtr(overrides.Channel),
	}
}

func deepCopyTrackerSiteOverrides(overrides api.TrackerSiteOverrides) api.TrackerSiteOverrides {
	return api.TrackerSiteOverrides{
		TIK: api.TIKOverrides{
			Foreign:  clonePtr(overrides.TIK.Foreign),
			Opera:    clonePtr(overrides.TIK.Opera),
			Asian:    clonePtr(overrides.TIK.Asian),
			DiscType: clonePtr(overrides.TIK.DiscType),
		},
	}
}

func deepCopyClientOverrides(overrides api.ClientOverrides) api.ClientOverrides {
	return api.ClientOverrides{
		Client:       clonePtr(overrides.Client),
		QbitCategory: clonePtr(overrides.QbitCategory),
		QbitTag:      clonePtr(overrides.QbitTag),
		ForceRecheck: clonePtr(overrides.ForceRecheck),
	}
}

func deepCopyImageHostOverrides(overrides api.ImageHostOverrides) api.ImageHostOverrides {
	return api.ImageHostOverrides{
		PreferredHost: clonePtr(overrides.PreferredHost),
		SkipUpload:    clonePtr(overrides.SkipUpload),
	}
}

func deepCopyScreenshotOverrides(overrides api.ScreenshotOverrides) api.ScreenshotOverrides {
	return api.ScreenshotOverrides{
		ManualFrames:           append([]int(nil), overrides.ManualFrames...),
		ComparisonPaths:        append([]string(nil), overrides.ComparisonPaths...),
		ComparisonPrimaryIndex: clonePtr(overrides.ComparisonPrimaryIndex),
	}
}

func deepCopyTorrentOverrides(overrides api.TorrentOverrides) api.TorrentOverrides {
	return api.TorrentOverrides{
		InfoHash:        clonePtr(overrides.InfoHash),
		MaxPieceSizeMiB: clonePtr(overrides.MaxPieceSizeMiB),
		NoHash:          clonePtr(overrides.NoHash),
		Rehash:          clonePtr(overrides.Rehash),
	}
}

func deepCopyExternalIDOverrides(overrides api.ExternalIDOverrides) api.ExternalIDOverrides {
	return api.ExternalIDOverrides{
		TMDBID:   clonePtr(overrides.TMDBID),
		IMDBID:   clonePtr(overrides.IMDBID),
		TVDBID:   clonePtr(overrides.TVDBID),
		TVmazeID: clonePtr(overrides.TVmazeID),
		MALID:    clonePtr(overrides.MALID),
	}
}

func deepCopyReleaseNameOverrides(overrides api.ReleaseNameOverrides) api.ReleaseNameOverrides {
	return api.ReleaseNameOverrides{
		Category:         clonePtr(overrides.Category),
		Type:             clonePtr(overrides.Type),
		Source:           clonePtr(overrides.Source),
		Resolution:       clonePtr(overrides.Resolution),
		Tag:              clonePtr(overrides.Tag),
		Service:          clonePtr(overrides.Service),
		Edition:          clonePtr(overrides.Edition),
		Season:           clonePtr(overrides.Season),
		Episode:          clonePtr(overrides.Episode),
		EpisodeTitle:     clonePtr(overrides.EpisodeTitle),
		ManualYear:       clonePtr(overrides.ManualYear),
		ManualDate:       clonePtr(overrides.ManualDate),
		UseSeasonEpisode: clonePtr(overrides.UseSeasonEpisode),
		NoSeason:         clonePtr(overrides.NoSeason),
		NoYear:           clonePtr(overrides.NoYear),
		NoAKA:            clonePtr(overrides.NoAKA),
		NoTag:            clonePtr(overrides.NoTag),
		NoEdition:        clonePtr(overrides.NoEdition),
		NoDub:            clonePtr(overrides.NoDub),
		NoDual:           clonePtr(overrides.NoDual),
		DualAudio:        clonePtr(overrides.DualAudio),
		Region:           clonePtr(overrides.Region),
	}
}

func deepCopyQuestionnaireAnswers(input map[string]map[string]string) map[string]map[string]string {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]map[string]string, len(input))
	for tracker, values := range input {
		inner := make(map[string]string, len(values))
		for key, value := range values {
			inner[key] = value
		}
		cloned[tracker] = inner
	}
	return cloned
}

func deepCopyPlaylistInfos(playlists []api.PlaylistInfo) []api.PlaylistInfo {
	if len(playlists) == 0 {
		return nil
	}
	cloned := make([]api.PlaylistInfo, len(playlists))
	for idx, playlist := range playlists {
		cloned[idx] = playlist
		cloned[idx].Items = append([]api.PlaylistItem(nil), playlist.Items...)
	}
	return cloned
}

func deepCopyExternalIDCandidates(candidates api.ExternalIDCandidates) api.ExternalIDCandidates {
	return api.ExternalIDCandidates{
		TMDB:             append([]api.ExternalIDCandidate(nil), candidates.TMDB...),
		IMDB:             append([]api.ExternalIDCandidate(nil), candidates.IMDB...),
		TMDBAutoSelected: candidates.TMDBAutoSelected,
		IMDBAutoSelected: candidates.IMDBAutoSelected,
	}
}

func deepCopyExternalMetadata(metadata api.ExternalMetadata) api.ExternalMetadata {
	return api.ExternalMetadata{
		SourcePath: metadata.SourcePath,
		TMDB:       deepCopyTMDBMetadata(metadata.TMDB),
		IMDB:       deepCopyIMDBMetadata(metadata.IMDB),
		TVDB:       deepCopyTVDBMetadata(metadata.TVDB),
		TVmaze:     deepCopyTVmazeMetadata(metadata.TVmaze),
		UpdatedAt:  metadata.UpdatedAt,
	}
}

func deepCopyTMDBMetadata(metadata *api.TMDBMetadata) *api.TMDBMetadata {
	if metadata == nil {
		return nil
	}
	cloned := *metadata
	cloned.OriginCountry = append([]string(nil), metadata.OriginCountry...)
	cloned.Creators = append([]string(nil), metadata.Creators...)
	cloned.Directors = append([]string(nil), metadata.Directors...)
	cloned.Cast = append([]string(nil), metadata.Cast...)
	cloned.ProductionCompanies = append([]api.TMDBCompany(nil), metadata.ProductionCompanies...)
	cloned.ProductionCountries = append([]api.TMDBCountry(nil), metadata.ProductionCountries...)
	cloned.Networks = append([]api.TMDBNetwork(nil), metadata.Networks...)
	return &cloned
}

func deepCopyIMDBMetadata(metadata *api.IMDBMetadata) *api.IMDBMetadata {
	if metadata == nil {
		return nil
	}
	cloned := *metadata
	cloned.Directors = append([]api.IMDBPerson(nil), metadata.Directors...)
	cloned.Creators = append([]api.IMDBPerson(nil), metadata.Creators...)
	cloned.Writers = append([]api.IMDBPerson(nil), metadata.Writers...)
	cloned.Stars = append([]api.IMDBPerson(nil), metadata.Stars...)
	cloned.Editions = append([]string(nil), metadata.Editions...)
	cloned.EditionDetails = deepCopyIMDBEditionDetails(metadata.EditionDetails)
	cloned.Akas = deepCopyIMDBAKAs(metadata.Akas)
	cloned.Episodes = append([]api.IMDBEpisode(nil), metadata.Episodes...)
	cloned.SeasonsSummary = append([]api.IMDBSeasonSummary(nil), metadata.SeasonsSummary...)
	cloned.SoundMixes = append([]string(nil), metadata.SoundMixes...)
	return &cloned
}

func deepCopyTVDBMetadata(metadata *api.TVDBMetadata) *api.TVDBMetadata {
	if metadata == nil {
		return nil
	}
	cloned := *metadata
	cloned.Aliases = append([]string(nil), metadata.Aliases...)
	return &cloned
}

func deepCopyTVmazeMetadata(metadata *api.TVmazeMetadata) *api.TVmazeMetadata {
	if metadata == nil {
		return nil
	}
	cloned := *metadata
	return &cloned
}

func deepCopyTorrentMatches(matches []api.TorrentMatch) []api.TorrentMatch {
	if len(matches) == 0 {
		return nil
	}
	cloned := make([]api.TorrentMatch, len(matches))
	for idx, match := range matches {
		cloned[idx] = match
		cloned[idx].TrackerURLsRaw = append([]string(nil), match.TrackerURLsRaw...)
		cloned[idx].TrackerURLs = append([]api.TrackerMatch(nil), match.TrackerURLs...)
	}
	return cloned
}

func deepCopyTrackerMetadata(metadata []api.TrackerMetadata) []api.TrackerMetadata {
	if len(metadata) == 0 {
		return nil
	}
	cloned := make([]api.TrackerMetadata, len(metadata))
	for idx, item := range metadata {
		cloned[idx] = item
		cloned[idx].ImageURLs = append([]string(nil), item.ImageURLs...)
	}
	return cloned
}

func deepCopyBlockedTrackers(blocked map[string][]api.TrackerBlockReason) map[string][]api.TrackerBlockReason {
	if len(blocked) == 0 {
		return nil
	}
	cloned := make(map[string][]api.TrackerBlockReason, len(blocked))
	for tracker, reasons := range blocked {
		cloned[tracker] = append([]api.TrackerBlockReason(nil), reasons...)
	}
	return cloned
}

func deepCopyTrackerRuleFailures(failures map[string][]api.RuleFailure) map[string][]api.RuleFailure {
	if len(failures) == 0 {
		return nil
	}
	cloned := make(map[string][]api.RuleFailure, len(failures))
	for tracker, items := range failures {
		cloned[tracker] = append([]api.RuleFailure(nil), items...)
	}
	return cloned
}

func deepCopyIMDBEditionDetails(details map[string]api.IMDBEditionDetail) map[string]api.IMDBEditionDetail {
	if len(details) == 0 {
		return nil
	}
	cloned := make(map[string]api.IMDBEditionDetail, len(details))
	for key, detail := range details {
		copied := detail
		copied.Attributes = append([]string(nil), detail.Attributes...)
		cloned[key] = copied
	}
	return cloned
}

func deepCopyIMDBAKAs(items []api.IMDBAKA) []api.IMDBAKA {
	if len(items) == 0 {
		return nil
	}
	cloned := make([]api.IMDBAKA, len(items))
	for idx, item := range items {
		cloned[idx] = item
		cloned[idx].Attributes = append([]string(nil), item.Attributes...)
	}
	return cloned
}

func deepCopyStringInterfaceMap(input map[string]interface{}) map[string]interface{} {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]interface{}, len(input))
	for key, value := range input {
		cloned[key] = deepCopyInterfaceValue(value)
	}
	return cloned
}

func deepCopyInterfaceValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		return deepCopyStringInterfaceMap(typed)
	case []interface{}:
		cloned := make([]interface{}, len(typed))
		for idx, item := range typed {
			cloned[idx] = deepCopyInterfaceValue(item)
		}
		return cloned
	case []string:
		return append([]string(nil), typed...)
	case []int:
		return append([]int(nil), typed...)
	case []int64:
		return append([]int64(nil), typed...)
	case []float64:
		return append([]float64(nil), typed...)
	case []bool:
		return append([]bool(nil), typed...)
	default:
		return typed
	}
}

func clonePtr[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func (c *Core) DiscoverPlaylists(ctx context.Context, sourcePath string) ([]api.PlaylistInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(sourcePath) == "" {
		return nil, internalerrors.ErrInvalidInput
	}

	c.logger.Debugf("core: discovering playlists in %q", sourcePath)

	playlists, err := filesystem.DiscoverPlaylists(ctx, sourcePath)
	if err != nil {
		c.logger.Warnf("core: discover playlists failed: %v", err)
		return nil, err
	}

	// Convert filesystem types to API types.
	var result []api.PlaylistInfo
	for _, p := range playlists {
		var items []api.PlaylistItem
		for _, item := range p.Items {
			items = append(items, api.PlaylistItem{
				File: item.File,
				Size: item.Size,
			})
		}
		result = append(result, api.PlaylistInfo{
			File:     p.File,
			Duration: p.Duration,
			Items:    items,
			Score:    p.Score,
			Edition:  p.Edition,
		})
	}

	c.logger.Infof("core: discovered %d playlists", len(result))
	return result, nil
}

func (c *Core) SavePlaylistSelection(ctx context.Context, sourcePath string, playlists []string, useAll bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(sourcePath) == "" {
		return internalerrors.ErrInvalidInput
	}
	if c.repo == nil {
		return errors.New("core: repository not initialized")
	}

	// Normalize path to ensure consistent storage and retrieval
	normalizedPath := filepath.ToSlash(filepath.Clean(sourcePath))
	c.logger.Debugf("core: saving playlist selection for %q (normalized: %q): %d playlists, useAll=%v", sourcePath, normalizedPath, len(playlists), useAll)

	if err := c.repo.SavePlaylistSelection(ctx, normalizedPath, playlists, useAll); err != nil {
		c.logger.Warnf("core: save playlist selection failed: %v", err)
		return err
	}

	c.logger.Infof("core: playlist selection saved for %q", normalizedPath)
	return nil
}

func (c *Core) LoadPlaylistSelection(ctx context.Context, sourcePath string) (api.PlaylistSelection, error) {
	if err := ctx.Err(); err != nil {
		return api.PlaylistSelection{}, err
	}
	if strings.TrimSpace(sourcePath) == "" {
		return api.PlaylistSelection{}, internalerrors.ErrInvalidInput
	}
	if c.repo == nil {
		return api.PlaylistSelection{}, errors.New("core: repository not initialized")
	}

	c.logger.Debugf("core: loading playlist selection for %q", sourcePath)

	selection, err := c.repo.GetPlaylistSelection(ctx, sourcePath)
	if err != nil {
		if errors.Is(err, internalerrors.ErrNotFound) {
			c.logger.Debugf("core: no playlist selection found for %q", sourcePath)
			return api.PlaylistSelection{}, internalerrors.ErrNotFound
		}
		c.logger.Warnf("core: load playlist selection failed: %v", err)
		return api.PlaylistSelection{}, err
	}

	c.logger.Debugf("core: loaded playlist selection for %q: %d playlists, useAll=%v", sourcePath, len(selection.SelectedPlaylists), selection.UseAll)
	return selection, nil
}

func (c *Core) ListHistory(ctx context.Context) ([]api.HistoryEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.repo == nil {
		return nil, errors.New("core: repository not initialized")
	}

	entries, err := c.repo.ListHistoryEntries(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]api.HistoryEntry, 0, len(entries))
	for _, entry := range entries {
		entryCopy := entry
		entryCopy.LatestUploadStatus = historyStatusLabel(entry.LatestUploadStatus, entry.RuleFailureCount)
		result = append(result, entryCopy)
	}

	return result, nil
}

func (c *Core) GetHistoryOverview(ctx context.Context, sourcePath string) (api.HistoryOverview, error) {
	if err := ctx.Err(); err != nil {
		return api.HistoryOverview{}, err
	}
	trimmed := strings.TrimSpace(sourcePath)
	if trimmed == "" {
		return api.HistoryOverview{}, internalerrors.ErrInvalidInput
	}
	if c.repo == nil {
		return api.HistoryOverview{}, errors.New("core: repository not initialized")
	}

	metadata, err := c.repo.GetByPath(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, err
	}

	overview := api.HistoryOverview{
		SourcePath:        metadata.Path,
		ReleaseTitle:      metadata.Title,
		ReleaseSource:     metadata.Source,
		ReleaseResolution: metadata.Resolution,
		MetadataUpdatedAt: metadata.UpdatedAt,
		Metadata:          metadata,
	}

	externalIDs, err := c.repo.GetExternalIDs(ctx, trimmed)
	if err == nil {
		overview.ExternalIDs = externalIDs
	} else if !errors.Is(err, internalerrors.ErrNotFound) {
		return api.HistoryOverview{}, err
	}

	externalMetadata, err := c.repo.GetExternalMetadata(ctx, trimmed)
	if err == nil {
		overview.ExternalMetadata = externalMetadata
	} else if !errors.Is(err, internalerrors.ErrNotFound) {
		return api.HistoryOverview{}, err
	}

	releaseOverrides, err := c.repo.GetReleaseNameOverrides(ctx, trimmed)
	if err == nil {
		overview.ReleaseNameOverrides = releaseOverrides
	} else if !errors.Is(err, internalerrors.ErrNotFound) {
		return api.HistoryOverview{}, err
	}

	descriptionOverride, err := c.repo.GetDescriptionOverride(ctx, trimmed, "")
	if err == nil {
		overview.DescriptionOverride = descriptionOverride
	} else if !errors.Is(err, internalerrors.ErrNotFound) {
		return api.HistoryOverview{}, err
	}

	playlistSelection, err := c.repo.GetPlaylistSelection(ctx, trimmed)
	if err == nil {
		overview.PlaylistSelection = playlistSelection
	} else if !errors.Is(err, internalerrors.ErrNotFound) {
		return api.HistoryOverview{}, err
	}

	trackerMetadata, err := c.repo.ListTrackerMetadataByPath(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, err
	}
	overview.TrackerMetadata = trackerMetadata

	ruleFailures, err := c.repo.ListTrackerRuleFailuresByPath(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, err
	}
	overview.TrackerRuleFailures = ruleFailures

	screenshots, err := c.repo.ListScreenshotsByPath(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, err
	}
	overview.Screenshots = screenshots

	finalSelections, err := c.repo.ListFinalSelections(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, err
	}
	overview.FinalSelections = finalSelections

	uploadedImages, err := c.repo.ListUploadedImagesByPath(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, err
	}
	overview.UploadedImages = uploadedImages

	uploadHistory, err := c.repo.ListUploadHistoryByPath(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, err
	}
	overview.UploadHistory = uploadHistory
	if len(uploadHistory) > 0 {
		overview.LatestUploadStatus = uploadHistory[0].Status
		overview.LatestUploadAt = uploadHistory[0].CreatedAt
	}
	overview.StatusLabel = historyStatusLabel(overview.LatestUploadStatus, len(ruleFailures))

	return overview, nil
}

func (c *Core) DeleteHistoryRelease(ctx context.Context, sourcePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	trimmed := strings.TrimSpace(sourcePath)
	if trimmed == "" {
		return internalerrors.ErrInvalidInput
	}
	if c.repo == nil {
		return errors.New("core: repository not initialized")
	}

	return c.deleteStoredRelease(ctx, trimmed)
}

func (c *Core) Close() error {
	if c == nil || c.repo == nil {
		return nil
	}
	closer, ok := c.repo.(interface{ Close() error })
	if !ok {
		return nil
	}
	return closer.Close()
}

func (c *Core) RenderDescription(ctx context.Context, raw string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return description.Render(raw), nil
}

func (c *Core) SaveDescriptionOverride(ctx context.Context, req api.Request, raw string) (api.DescriptionBuilderGroup, error) {
	if len(req.Paths) == 0 {
		return api.DescriptionBuilderGroup{}, internalerrors.ErrInvalidInput
	}
	if c.services.Filesystem == nil {
		return api.DescriptionBuilderGroup{}, errors.New("core: filesystem service not configured")
	}
	if c.repo == nil {
		return api.DescriptionBuilderGroup{}, errors.New("core: repository not configured")
	}

	normalizedPaths, err := c.services.Filesystem.ValidatePaths(ctx, req.Paths)
	if err != nil {
		return api.DescriptionBuilderGroup{}, err
	}

	uniquePaths := make([]string, 0, len(normalizedPaths))
	seenPaths := make(map[string]struct{}, len(normalizedPaths))
	for _, path := range normalizedPaths {
		if _, exists := seenPaths[path]; exists {
			continue
		}
		seenPaths[path] = struct{}{}
		uniquePaths = append(uniquePaths, path)
	}
	if len(uniquePaths) != 1 {
		return api.DescriptionBuilderGroup{}, internalerrors.ErrInvalidInput
	}

	trimmed := strings.TrimSpace(raw)
	groupKey := strings.TrimSpace(req.DescriptionOverrideGroup)
	if trimmed == "" {
		if err := c.repo.DeleteDescriptionOverride(ctx, uniquePaths[0], groupKey); err != nil {
			return api.DescriptionBuilderGroup{}, err
		}
		req.Paths = []string{uniquePaths[0]}
		return c.FetchDescriptionBuilderGroupPreview(ctx, req)
	}

	if err := c.repo.SaveDescriptionOverride(ctx, api.DescriptionOverride{
		SourcePath:  uniquePaths[0],
		GroupKey:    groupKey,
		Description: trimmed,
		UpdatedAt:   time.Now().UTC(),
	}); err != nil {
		return api.DescriptionBuilderGroup{}, err
	}

	return api.DescriptionBuilderGroup{
		GroupKey:           normalizeDescriptionBuilderGroupKey(groupKey, req.Trackers),
		Trackers:           append([]string{}, req.Trackers...),
		RawDescription:     trimmed,
		RawDescriptionHTML: description.Render(trimmed),
		HasOverride:        true,
	}, nil
}

func historyStatusLabel(rawStatus string, ruleFailureCount int) string {
	status := strings.TrimSpace(strings.ToLower(rawStatus))
	switch status {
	case "pending":
		return "Pending"
	case "pending-internal":
		return "Pending Internal"
	case "uploaded", "success", "completed":
		return "Uploaded"
	case "failed", "error":
		return "Failed"
	}
	if status != "" {
		normalized := strings.ReplaceAll(status, "-", " ")
		words := strings.Fields(normalized)
		for idx, word := range words {
			if word == "" {
				continue
			}
			words[idx] = strings.ToUpper(word[:1]) + word[1:]
		}
		return strings.Join(words, " ")
	}
	if ruleFailureCount > 0 {
		return "Rule Issues"
	}
	return "Stored"
}

func (c *Core) applyDefaultOptions(options api.UploadOptions) (api.UploadOptions, error) {
	if options.InteractionMode == "" {
		options.InteractionMode = api.InteractionModeInteractive
	}
	if options.Screens <= 0 {
		options.Screens = c.cfg.ScreenshotHandling.Screens
	}
	if !options.OnlyID {
		options.OnlyID = c.cfg.Metadata.OnlyID
	}
	if !options.SkipAutoTorrent {
		options.SkipAutoTorrent = c.cfg.Metadata.SkipAutoTorrent
	}
	if !options.KeepImages {
		options.KeepImages = c.cfg.Metadata.KeepImages
	}
	if options.Screens <= 0 {
		return api.UploadOptions{}, internalerrors.ErrInvalidInput
	}
	return options, nil
}

func (c *Core) resolveDescriptionOverrideRequest(ctx context.Context, req api.Request) (api.Request, error) {
	if strings.TrimSpace(req.DescriptionOverrideRaw) != "" || strings.TrimSpace(req.DescriptionOverrideURL) == "" {
		return req, nil
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(req.DescriptionOverrideURL), nil)
	if err != nil {
		return api.Request{}, fmt.Errorf("core: description override url: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return api.Request{}, fmt.Errorf("core: description override fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return api.Request{}, fmt.Errorf("core: description override fetch: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return api.Request{}, fmt.Errorf("core: description override read: %w", err)
	}
	if strings.TrimSpace(string(body)) == "" {
		return api.Request{}, errors.New("core: description override fetch returned empty body")
	}

	req.DescriptionOverrideRaw = string(body)
	return req, nil
}

func buildMetadataPreview(meta api.PreparedMetadata, cfg config.Config) api.MetadataPreview {
	return api.MetadataPreview{
		SourcePath:           meta.SourcePath,
		TrackerName:          trackerNameForExternalIDs(meta),
		ReleaseName:          meta.ReleaseName,
		Warnings:             append([]string{}, meta.LookupWarnings...),
		ReleaseNameOverrides: meta.ReleaseNameOverrides,
		ExternalIDs:          meta.ExternalIDs,
		ExternalIDCandidates: meta.ExternalIDCandidates,
		ExternalIDInfo:       buildExternalIDInfo(meta.ExternalIDs),
		ExternalPreview:      buildExternalPreviews(meta.ExternalIDs, meta.ExternalMetadata),
		TrackerData:          buildTrackerPreview(meta.TrackerData, cfg),
	}
}

func sourceLookupTrackerHasUsableData(meta api.PreparedMetadata) bool {
	tracker := strings.TrimSpace(meta.SourceLookupTracker)
	if tracker == "" {
		return false
	}
	for _, record := range meta.TrackerData {
		if !strings.EqualFold(record.Tracker, tracker) {
			continue
		}
		if record.TMDBID != 0 || record.IMDBID != 0 || record.TVDBID != 0 || record.MALID != 0 {
			return true
		}
		if strings.TrimSpace(record.Description) != "" || len(record.ImageURLs) > 0 {
			return true
		}
	}
	return false
}

func buildTrackerPreview(records []api.TrackerMetadata, cfg config.Config) []api.TrackerPreview {
	if len(records) == 0 {
		return nil
	}
	chooseBest := func(existing api.TrackerMetadata, candidate api.TrackerMetadata) api.TrackerMetadata {
		existingTime := existing.UpdatedAt
		candidateTime := candidate.UpdatedAt
		if !candidateTime.IsZero() && (existingTime.IsZero() || candidateTime.After(existingTime)) {
			return candidate
		}
		if existingTime.IsZero() && !candidateTime.IsZero() {
			return candidate
		}
		if len(candidate.ImageURLs) > len(existing.ImageURLs) {
			return candidate
		}
		if len(candidate.Description) > len(existing.Description) {
			return candidate
		}
		if candidate.Matched && !existing.Matched {
			return candidate
		}
		return existing
	}
	byTracker := make(map[string]api.TrackerMetadata, len(records))
	orderedKeys := make([]string, 0, len(records))
	for _, record := range records {
		key := strings.ToUpper(strings.TrimSpace(record.Tracker))
		if key == "" {
			key = fmt.Sprintf("unknown-%d", len(orderedKeys))
		}
		if existing, ok := byTracker[key]; ok {
			byTracker[key] = chooseBest(existing, record)
			continue
		}
		byTracker[key] = record
		orderedKeys = append(orderedKeys, key)
	}

	result := make([]api.TrackerPreview, 0, len(byTracker))
	for _, key := range orderedKeys {
		record := byTracker[key]
		preview := api.TrackerPreview{
			Tracker:         record.Tracker,
			TrackerID:       record.TrackerID,
			TorrentURL:      trackerTorrentURL(cfg, record.Tracker, record.TrackerID),
			InfoHash:        record.InfoHash,
			TMDBID:          record.TMDBID,
			IMDBID:          record.IMDBID,
			TVDBID:          record.TVDBID,
			MALID:           record.MALID,
			Category:        record.Category,
			Description:     record.Description,
			DescriptionHTML: description.Render(record.Description),
			ImageURLs:       append([]string{}, record.ImageURLs...),
			Filename:        record.Filename,
			Matched:         record.Matched,
		}
		if !record.UpdatedAt.IsZero() {
			preview.UpdatedAt = record.UpdatedAt.UTC().Format(time.RFC3339)
		}
		result = append(result, preview)
	}
	return result
}

func overrideSignature(
	idOverrides api.ExternalIDOverrides,
	nameOverrides api.ReleaseNameOverrides,
	metadataOverrides api.MetadataOverrides,
	trackerOverrides api.TrackerConfigOverrides,
	trackerSiteOverrides api.TrackerSiteOverrides,
	clientOverrides api.ClientOverrides,
	torrentOverrides api.TorrentOverrides,
	imageHostOverrides api.ImageHostOverrides,
	screenshotOverrides ...api.ScreenshotOverrides,
) string {
	parts := make([]string, 0, 64)
	if idOverrides.TMDBID != nil {
		parts = append(parts, fmt.Sprintf("tmdb=%d", *idOverrides.TMDBID))
	}
	if idOverrides.IMDBID != nil {
		parts = append(parts, fmt.Sprintf("imdb=%d", *idOverrides.IMDBID))
	}
	if idOverrides.TVDBID != nil {
		parts = append(parts, fmt.Sprintf("tvdb=%d", *idOverrides.TVDBID))
	}
	if idOverrides.TVmazeID != nil {
		parts = append(parts, fmt.Sprintf("tvmaze=%d", *idOverrides.TVmazeID))
	}
	if idOverrides.MALID != nil {
		parts = append(parts, fmt.Sprintf("mal=%d", *idOverrides.MALID))
	}
	if nameOverrides.Category != nil {
		parts = append(parts, "category="+strings.TrimSpace(*nameOverrides.Category))
	}
	if nameOverrides.Type != nil {
		parts = append(parts, "type="+strings.TrimSpace(*nameOverrides.Type))
	}
	if nameOverrides.Source != nil {
		parts = append(parts, "source="+strings.TrimSpace(*nameOverrides.Source))
	}
	if nameOverrides.Resolution != nil {
		parts = append(parts, "res="+strings.TrimSpace(*nameOverrides.Resolution))
	}
	if nameOverrides.Tag != nil {
		parts = append(parts, "tag="+strings.TrimSpace(*nameOverrides.Tag))
	}
	if nameOverrides.Service != nil {
		parts = append(parts, "service="+strings.TrimSpace(*nameOverrides.Service))
	}
	if nameOverrides.Edition != nil {
		parts = append(parts, "edition="+strings.TrimSpace(*nameOverrides.Edition))
	}
	if nameOverrides.Season != nil {
		parts = append(parts, "season="+strings.TrimSpace(*nameOverrides.Season))
	}
	if nameOverrides.Episode != nil {
		parts = append(parts, "episode="+strings.TrimSpace(*nameOverrides.Episode))
	}
	if nameOverrides.EpisodeTitle != nil {
		parts = append(parts, "episodeTitle="+strings.TrimSpace(*nameOverrides.EpisodeTitle))
	}
	if nameOverrides.ManualYear != nil {
		parts = append(parts, fmt.Sprintf("manualYear=%d", *nameOverrides.ManualYear))
	}
	if nameOverrides.ManualDate != nil {
		parts = append(parts, "manualDate="+strings.TrimSpace(*nameOverrides.ManualDate))
	}
	if nameOverrides.NoSeason != nil {
		parts = append(parts, fmt.Sprintf("noSeason=%t", *nameOverrides.NoSeason))
	}
	if nameOverrides.NoYear != nil {
		parts = append(parts, fmt.Sprintf("noYear=%t", *nameOverrides.NoYear))
	}
	if nameOverrides.NoAKA != nil {
		parts = append(parts, fmt.Sprintf("noAKA=%t", *nameOverrides.NoAKA))
	}
	if nameOverrides.NoTag != nil {
		parts = append(parts, fmt.Sprintf("noTag=%t", *nameOverrides.NoTag))
	}
	if nameOverrides.NoEdition != nil {
		parts = append(parts, fmt.Sprintf("noEdition=%t", *nameOverrides.NoEdition))
	}
	if nameOverrides.NoDub != nil {
		parts = append(parts, fmt.Sprintf("noDub=%t", *nameOverrides.NoDub))
	}
	if nameOverrides.NoDual != nil {
		parts = append(parts, fmt.Sprintf("noDual=%t", *nameOverrides.NoDual))
	}
	if nameOverrides.DualAudio != nil {
		parts = append(parts, fmt.Sprintf("dualAudio=%t", *nameOverrides.DualAudio))
	}
	if nameOverrides.Region != nil {
		parts = append(parts, "region="+strings.TrimSpace(*nameOverrides.Region))
	}
	if metadataOverrides.Distributor != nil {
		parts = append(parts, "distributor="+strings.TrimSpace(*metadataOverrides.Distributor))
	}
	if metadataOverrides.OriginalLanguage != nil {
		parts = append(parts, "originalLanguage="+strings.TrimSpace(*metadataOverrides.OriginalLanguage))
	}
	if metadataOverrides.PersonalRelease != nil {
		parts = append(parts, fmt.Sprintf("personalRelease=%t", *metadataOverrides.PersonalRelease))
	}
	if metadataOverrides.Commentary != nil {
		parts = append(parts, fmt.Sprintf("commentary=%t", *metadataOverrides.Commentary))
	}
	if metadataOverrides.WebDV != nil {
		parts = append(parts, fmt.Sprintf("webdv=%t", *metadataOverrides.WebDV))
	}
	if metadataOverrides.StreamOptimized != nil {
		parts = append(parts, fmt.Sprintf("stream=%t", *metadataOverrides.StreamOptimized))
	}
	if metadataOverrides.Anime != nil {
		parts = append(parts, fmt.Sprintf("anime=%t", *metadataOverrides.Anime))
	}
	if trackerOverrides.Anon != nil {
		parts = append(parts, fmt.Sprintf("anon=%t", *trackerOverrides.Anon))
	}
	if trackerOverrides.Draft != nil {
		parts = append(parts, fmt.Sprintf("draft=%t", *trackerOverrides.Draft))
	}
	if trackerOverrides.ModQ != nil {
		parts = append(parts, fmt.Sprintf("modq=%t", *trackerOverrides.ModQ))
	}
	if trackerOverrides.Channel != nil {
		parts = append(parts, "channel="+strings.TrimSpace(*trackerOverrides.Channel))
	}
	if trackerSiteOverrides.TIK.Foreign != nil {
		parts = append(parts, fmt.Sprintf("tikForeign=%t", *trackerSiteOverrides.TIK.Foreign))
	}
	if trackerSiteOverrides.TIK.Opera != nil {
		parts = append(parts, fmt.Sprintf("tikOpera=%t", *trackerSiteOverrides.TIK.Opera))
	}
	if trackerSiteOverrides.TIK.Asian != nil {
		parts = append(parts, fmt.Sprintf("tikAsian=%t", *trackerSiteOverrides.TIK.Asian))
	}
	if trackerSiteOverrides.TIK.DiscType != nil {
		parts = append(parts, "tikDiscType="+strings.TrimSpace(*trackerSiteOverrides.TIK.DiscType))
	}
	if clientOverrides.Client != nil {
		parts = append(parts, "client="+strings.TrimSpace(*clientOverrides.Client))
	}
	if clientOverrides.QbitCategory != nil {
		parts = append(parts, "qbitCategory="+strings.TrimSpace(*clientOverrides.QbitCategory))
	}
	if clientOverrides.QbitTag != nil {
		parts = append(parts, "qbitTag="+strings.TrimSpace(*clientOverrides.QbitTag))
	}
	if clientOverrides.ForceRecheck != nil {
		parts = append(parts, fmt.Sprintf("forceRecheck=%t", *clientOverrides.ForceRecheck))
	}
	if torrentOverrides.InfoHash != nil {
		parts = append(parts, "infohash="+strings.ToLower(strings.TrimSpace(*torrentOverrides.InfoHash)))
	}
	if torrentOverrides.MaxPieceSizeMiB != nil {
		parts = append(parts, fmt.Sprintf("maxPieceSizeMiB=%d", *torrentOverrides.MaxPieceSizeMiB))
	}
	if torrentOverrides.NoHash != nil {
		parts = append(parts, fmt.Sprintf("nohash=%t", *torrentOverrides.NoHash))
	}
	if torrentOverrides.Rehash != nil {
		parts = append(parts, fmt.Sprintf("rehash=%t", *torrentOverrides.Rehash))
	}
	if imageHostOverrides.PreferredHost != nil {
		parts = append(parts, "imageHost="+strings.ToLower(strings.TrimSpace(*imageHostOverrides.PreferredHost)))
	}
	if imageHostOverrides.SkipUpload != nil {
		parts = append(parts, fmt.Sprintf("skipImageHostUpload=%t", *imageHostOverrides.SkipUpload))
	}
	if len(screenshotOverrides) > 0 {
		screenshot := screenshotOverrides[0]
		if len(screenshot.ManualFrames) > 0 {
			parts = append(parts, fmt.Sprintf("manualFrames=%v", screenshot.ManualFrames))
		}
		if len(screenshot.ComparisonPaths) > 0 {
			parts = append(parts, "comparisonPaths="+strings.Join(screenshot.ComparisonPaths, ","))
		}
		if screenshot.ComparisonPrimaryIndex != nil {
			parts = append(parts, fmt.Sprintf("comparisonPrimaryIndex=%d", *screenshot.ComparisonPrimaryIndex))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "|")
}

func hasExternalIDOverrides(overrides api.ExternalIDOverrides) bool {
	return overrides.TMDBID != nil ||
		overrides.IMDBID != nil ||
		overrides.TVDBID != nil ||
		overrides.TVmazeID != nil ||
		overrides.MALID != nil
}

func resolveExternalIDSelection(selections map[string]api.ExternalIDSelection, sourcePath string) api.ExternalIDSelection {
	if len(selections) == 0 {
		return api.ExternalIDSelection{}
	}
	if selected, ok := selections[sourcePath]; ok {
		return selected
	}
	cleaned := filepath.Clean(sourcePath)
	if selected, ok := selections[cleaned]; ok {
		return selected
	}
	for key, selected := range selections {
		if filepath.Clean(key) == cleaned {
			return selected
		}
	}
	return api.ExternalIDSelection{}
}

func mergeExternalIDOverrides(base api.ExternalIDOverrides, selection api.ExternalIDSelection) api.ExternalIDOverrides {
	merged := base
	if selection.TMDBID != nil {
		merged.TMDBID = selection.TMDBID
	}
	if selection.IMDBID != nil {
		merged.IMDBID = selection.IMDBID
	}
	if selection.TVDBID != nil {
		merged.TVDBID = selection.TVDBID
	}
	if selection.TVmazeID != nil {
		merged.TVmazeID = selection.TVmazeID
	}
	return merged
}

func trackerTorrentURL(cfg config.Config, tracker string, trackerID string) string {
	if strings.TrimSpace(tracker) == "" || strings.TrimSpace(trackerID) == "" {
		return ""
	}
	base := trackerBaseURL(cfg, tracker)
	if base == "" {
		return ""
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return ""
	}
	parsed.Path = path.Join("/", "torrents", trackerID)
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func trackerBaseURL(cfg config.Config, tracker string) string {
	if strings.TrimSpace(tracker) == "" {
		return ""
	}
	for name, entry := range cfg.Trackers.Trackers {
		if strings.EqualFold(name, tracker) {
			return baseFromAnnounce(entry.AnnounceURL)
		}
	}
	return ""
}

func baseFromAnnounce(announce string) string {
	trimmed := strings.TrimSpace(announce)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return ""
	}
	parsed.Path = "/"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func buildExternalIDInfo(ids api.ExternalIDs) []api.ExternalIDInfo {
	result := make([]api.ExternalIDInfo, 0, 4)
	if ids.IMDBID != 0 {
		result = append(result, api.ExternalIDInfo{Provider: "imdb", ID: ids.IMDBID, Source: ids.SourceIMDB})
	}
	if ids.TMDBID != 0 {
		result = append(result, api.ExternalIDInfo{Provider: "tmdb", ID: ids.TMDBID, Source: ids.SourceTMDB})
	}
	if ids.TVDBID != 0 {
		result = append(result, api.ExternalIDInfo{Provider: "tvdb", ID: ids.TVDBID, Source: ids.SourceTVDB})
	}
	if ids.TVmazeID != 0 {
		result = append(result, api.ExternalIDInfo{Provider: "tvmaze", ID: ids.TVmazeID, Source: ids.SourceTVmaze})
	}
	return result
}

func buildExternalPreviews(ids api.ExternalIDs, metadata api.ExternalMetadata) []api.ExternalPreview {
	result := make([]api.ExternalPreview, 0, 4)
	if ids.IMDBID != 0 {
		preview := api.ExternalPreview{Provider: "imdb", ID: ids.IMDBID, Source: ids.SourceIMDB}
		if metadata.IMDB != nil {
			preview.Title = metadata.IMDB.Title
			preview.Year = metadata.IMDB.Year
			preview.Overview = metadata.IMDB.Plot
			preview.PosterURL = metadata.IMDB.Cover
			preview.IMDBType = metadata.IMDB.Type
			preview.Rating = metadata.IMDB.Rating
			preview.RatingCount = metadata.IMDB.RatingCount
			preview.RuntimeMinutes = metadata.IMDB.RuntimeMinutes
			preview.Genres = metadata.IMDB.Genres
			preview.Country = metadata.IMDB.Country
			preview.IMDB = metadata.IMDB
		}
		result = append(result, preview)
	}
	if ids.TMDBID != 0 {
		preview := api.ExternalPreview{Provider: "tmdb", ID: ids.TMDBID, Source: ids.SourceTMDB}
		if metadata.TMDB != nil {
			preview.Title = metadata.TMDB.Title
			preview.Year = metadata.TMDB.Year
			preview.Overview = metadata.TMDB.Overview
			preview.PosterURL = metadata.TMDB.Poster
			preview.BackdropURL = metadata.TMDB.Backdrop
			preview.Category = metadata.TMDB.Category
			preview.OriginalTitle = metadata.TMDB.OriginalTitle
			preview.ReleaseDate = metadata.TMDB.ReleaseDate
			preview.FirstAirDate = metadata.TMDB.FirstAirDate
			preview.LastAirDate = metadata.TMDB.LastAirDate
			preview.OriginalLanguage = metadata.TMDB.OriginalLanguage
			preview.TMDBType = metadata.TMDB.TMDBType
			preview.Runtime = metadata.TMDB.Runtime
			preview.Genres = metadata.TMDB.Genres
			preview.Keywords = metadata.TMDB.Keywords
			preview.YouTube = metadata.TMDB.YouTube
			preview.IMDBID = metadata.TMDB.IMDBID
			preview.TVDBID = metadata.TMDB.TVDBID
			preview.TMDB = metadata.TMDB
		}
		result = append(result, preview)
	}
	if ids.TVDBID != 0 {
		preview := api.ExternalPreview{Provider: "tvdb", ID: ids.TVDBID, Source: ids.SourceTVDB}
		if metadata.TVDB != nil {
			preview.Title = metadata.TVDB.Name
			preview.Year = metadata.TVDB.Year
			preview.Overview = metadata.TVDB.Overview
			preview.PosterURL = metadata.TVDB.Poster
			preview.FirstAirDate = metadata.TVDB.FirstAired
			preview.TMDBType = metadata.TVDB.Type
			preview.Country = metadata.TVDB.OriginalCountry
			preview.OriginalLanguage = metadata.TVDB.OriginalLanguage
			preview.Genres = metadata.TVDB.Genres
			preview.TVDBID = metadata.TVDB.TVDBID
			preview.TVDB = metadata.TVDB
		}
		result = append(result, preview)
	}
	if ids.TVmazeID != 0 {
		preview := api.ExternalPreview{Provider: "tvmaze", ID: ids.TVmazeID, Source: ids.SourceTVmaze}
		if metadata.TVmaze != nil {
			preview.Title = metadata.TVmaze.Name
			preview.Year = yearFromDate(metadata.TVmaze.Premiered)
			preview.Overview = metadata.TVmaze.Summary
			preview.PosterURL = metadata.TVmaze.Poster
			preview.BackdropURL = metadata.TVmaze.Backdrop
			preview.TMDBType = metadata.TVmaze.Type
			preview.OriginalLanguage = metadata.TVmaze.Language
			preview.Runtime = metadata.TVmaze.Runtime
			preview.Genres = metadata.TVmaze.Genres
			preview.Rating = metadata.TVmaze.Rating
			preview.RatingCount = metadata.TVmaze.Weight
			preview.Country = metadata.TVmaze.Country
			preview.Premiered = metadata.TVmaze.Premiered
			preview.IMDBID = metadata.TVmaze.IMDBID
			preview.TVDBID = metadata.TVmaze.TVDBID
			preview.TVmaze = metadata.TVmaze
		}
		result = append(result, preview)
	}
	return result
}

func trackerNameForExternalIDs(meta api.PreparedMetadata) string {
	if len(meta.TrackerData) == 0 {
		return ""
	}
	ids := meta.ExternalIDs
	if ids.SourceTMDB != "tracker" && ids.SourceIMDB != "tracker" && ids.SourceTVDB != "tracker" {
		return ""
	}
	for _, record := range meta.TrackerData {
		if record.Tracker == "" {
			continue
		}
		if ids.SourceTMDB == "tracker" && record.TMDBID != 0 && record.TMDBID == ids.TMDBID {
			return record.Tracker
		}
		if ids.SourceIMDB == "tracker" && record.IMDBID != 0 && record.IMDBID == ids.IMDBID {
			return record.Tracker
		}
		if ids.SourceTVDB == "tracker" && record.TVDBID != 0 && record.TVDBID == ids.TVDBID {
			return record.Tracker
		}
	}
	for _, record := range meta.TrackerData {
		if record.Tracker == "" {
			continue
		}
		if record.TMDBID != 0 || record.IMDBID != 0 || record.TVDBID != 0 {
			return record.Tracker
		}
	}
	return ""
}

func yearFromDate(value string) int {
	if len(value) < 4 {
		return 0
	}
	year, err := strconv.Atoi(value[:4])
	if err != nil {
		return 0
	}
	return year
}

func mergeTrackerRemovals(existing []string, additions []string) []string {
	if len(existing) == 0 && len(additions) == 0 {
		return nil
	}
	merged := make([]string, 0, len(existing)+len(additions))
	seen := make(map[string]struct{}, len(existing)+len(additions))
	for _, value := range append(existing, additions...) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		upper := strings.ToUpper(trimmed)
		if _, ok := seen[upper]; ok {
			continue
		}
		seen[upper] = struct{}{}
		merged = append(merged, upper)
	}
	return merged
}

func normalizeExecutionRequest(req api.Request) api.Request {
	if req.Execution.SiteCheck {
		req.Options.DryRun = true
	}
	if strings.TrimSpace(req.Execution.SiteUploadTracker) != "" {
		req.Trackers = []string{strings.ToUpper(strings.TrimSpace(req.Execution.SiteUploadTracker))}
	}
	return req
}

func (c *Core) resolveCanonicalDescriptionGroups(ctx context.Context, meta api.PreparedMetadata, req api.Request) ([]api.DescriptionBuilderGroup, error) {
	if len(req.DescriptionGroups) > 0 {
		return cloneDescriptionBuilderGroups(req.DescriptionGroups), nil
	}
	if len(meta.DescriptionGroups) > 0 {
		return cloneDescriptionBuilderGroups(meta.DescriptionGroups), nil
	}
	if c.services.Trackers == nil {
		return nil, errors.New("core: tracker service not configured")
	}

	resolvedTrackers := trackers.ResolveTrackersWithDefaults(c.cfg, req.Trackers, req.TrackersRemove, c.logger)
	prep, err := c.services.Trackers.BuildPreparation(ctx, meta, resolvedTrackers)
	if err != nil {
		return nil, err
	}
	if len(prep.Descriptions) == 0 {
		return nil, nil
	}

	overrideByGroup := make(map[string]api.DescriptionOverride)
	if c.repo != nil && strings.TrimSpace(meta.SourcePath) != "" {
		overrides, err := c.repo.ListDescriptionOverridesByPath(ctx, meta.SourcePath)
		if err != nil && !errors.Is(err, internalerrors.ErrNotFound) {
			return nil, fmt.Errorf("core: description override: %w", err)
		}
		for _, override := range overrides {
			overrideByGroup[strings.TrimSpace(override.GroupKey)] = override
		}
	}

	groups := make([]api.DescriptionBuilderGroup, 0, len(prep.Descriptions))
	for _, entry := range prep.Descriptions {
		groups = append(groups, buildDescriptionBuilderGroup(entry, overrideByGroup))
	}
	return groups, nil
}

func appendPathedDupeResults(summary api.DupeCheckSummary, matchedTrackers []string) api.DupeCheckSummary {
	const pathedNote = "pathed torrent match found; skipping dupe search"

	if len(matchedTrackers) == 0 && len(summary.Results) == 0 {
		return summary
	}

	combinedTrackers := make(map[string]struct{}, len(matchedTrackers))
	filteredResults := make([]api.DupeCheckResult, 0, len(summary.Results))
	for _, result := range summary.Results {
		if hasPathedNote(result.Notes, pathedNote) {
			for _, tracker := range splitTrackerLabel(result.Tracker) {
				combinedTrackers[tracker] = struct{}{}
			}
			continue
		}
		filteredResults = append(filteredResults, result)
	}

	for _, tracker := range mergeTrackerRemovals(nil, matchedTrackers) {
		combinedTrackers[tracker] = struct{}{}
	}

	if len(combinedTrackers) == 0 {
		summary.Results = filteredResults
		return summary
	}

	trackers := make([]string, 0, len(combinedTrackers))
	for tracker := range combinedTrackers {
		trackers = append(trackers, tracker)
	}
	sort.Strings(trackers)

	filteredResults = append(filteredResults, api.DupeCheckResult{
		Tracker:   strings.Join(trackers, ", "),
		HasDupes:  true,
		Status:    "completed",
		Notes:     []string{pathedNote},
		CheckedAt: time.Now().UTC(),
	})
	summary.Results = filteredResults
	return summary
}

func hasPathedNote(notes []string, pathedNote string) bool {
	for _, note := range notes {
		if strings.EqualFold(strings.TrimSpace(note), pathedNote) {
			return true
		}
	}
	return false
}

func splitTrackerLabel(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return mergeTrackerRemovals(nil, strings.Split(value, ","))
}
