// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path" //nolint:depguard // Builds URL paths, not local filesystem paths.
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/clients"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/dupechecking"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/paths"
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
	ownsRepo  bool
	dupeMu    sync.RWMutex
	dupeCache map[string]dupeCacheEntry
}

type dupeCacheEntry struct {
	meta             api.PreparedMetadata
	signature        string
	updatedAt        time.Time
	requestRefreshed bool
}

func New(deps api.CoreDependencies) (*Core, error) {
	return newCore(context.Background(), deps)
}

func NewWithContext(ctx context.Context, deps api.CoreDependencies) (*Core, error) {
	if ctx == nil {
		return nil, errors.New("core: context is required")
	}
	return newCore(ctx, deps)
}

func newCore(ctx context.Context, deps api.CoreDependencies) (*Core, error) {
	if ctx == nil {
		return nil, errors.New("core: context is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = api.NopLogger{}
	}
	logger.Infof("core: initializing")

	var cfg config.Config
	switch typed := deps.Config.(type) {
	case nil:
		return nil, errors.New("core: config is required")
	case config.Config:
		cfg = typed
	case *config.Config:
		if typed == nil {
			return nil, errors.New("core: config is required")
		}
		cfg = *typed
	default:
		return nil, fmt.Errorf("core: unsupported config type %T", deps.Config)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("core: %w", err)
	}

	repo := deps.Repository
	ownsRepo := false
	if repo == nil {
		logger.Debugf("core: opening repository")
		sqliteRepo, err := db.OpenWithLoggerContext(ctx, cfg.MainSettings.DBPath, logger)
		if err != nil {
			return nil, fmt.Errorf("core: %w", err)
		}
		if err := sqliteRepo.MigrateContext(ctx); err != nil {
			_ = sqliteRepo.Close()
			return nil, fmt.Errorf("core: %w", err)
		}

		repo = sqliteRepo
		ownsRepo = true
	}
	if sqliteRepo, ok := repo.(*db.SQLiteRepository); ok {
		if err := migrateLegacyCookies(ctx, sqliteRepo.RawDB(), cfg.MainSettings.DBPath, logger); err != nil {
			logger.Warnf("core: cookie migration failed: %v (continuing)", err)
		}
	}

	services := deps.Services
	if err := maybeApplyE2EServices(ctx, &services, cfg, repo, logger); err != nil {
		if ownsRepo {
			if closer, ok := repo.(interface{ Close() error }); ok {
				_ = closer.Close()
			}
		}
		return nil, err
	}
	if services.Metadata == nil {
		bdinfoService := bdinfo.New(logger)

		services.Metadata = metadata.NewService(
			repo,
			metadata.WithTagsPathFromDB(cfg.MainSettings.DBPath),
			metadata.WithLogger(logger),
			metadata.WithSRRDBPaths(cfg.MainSettings.DBPath),
			metadata.WithConfig(cfg),
			metadata.WithBDInfoService(bdinfoService),
		)
	}
	if services.Torrents == nil {
		tmpDir, err := db.Subdir(cfg.MainSettings.DBPath, "tmp")
		if err != nil {
			return nil, fmt.Errorf("core: tmp dir: %w", err)
		}
		services.Torrents = torrent.NewService(logger, tmpDir)
	}
	if services.Screenshots == nil {
		tmpDir, err := db.Subdir(cfg.MainSettings.DBPath, "tmp")
		if err != nil {
			return nil, fmt.Errorf("core: tmp dir: %w", err)
		}
		services.Screenshots = screenshots.NewServiceWithRepo(cfg, logger, tmpDir, nil, repo)
	}
	if services.Images == nil {
		services.Images = imagehosting.NewService(cfg, logger, repo)
	}
	if services.Trackers == nil {
		registry, err := trackerimpl.NewRegistry()
		if err != nil {
			return nil, fmt.Errorf("core: tracker registry: %w", err)
		}
		services.Trackers = trackers.NewServiceWithRegistryAndImages(cfg, logger, repo, registry, services.Images)
	}
	if services.Clients == nil {
		services.Clients = clients.NewService(cfg, logger)
	}
	if services.Filesystem == nil {
		services.Filesystem = filesystem.NewValidatorWithLogger(logger)
	}
	if services.Dupes == nil {
		services.Dupes = dupechecking.NewService(cfg, logger)
	}
	logger.Infof("core: initialized services")

	return &Core{
		cfg:       cfg,
		logger:    logger,
		services:  services,
		repo:      repo,
		ownsRepo:  ownsRepo,
		dupeCache: make(map[string]dupeCacheEntry),
	}, nil
}

func (c *Core) RunUpload(ctx context.Context, req api.Request) (api.Result, error) {
	req = normalizeExecutionRequest(req)
	c.logger.Debugf("core: upload request routed to upload-only handler")
	return c.RunUploadPrepared(ctx, req)
}

// RunUploadPrepared uploads from cached prepared metadata for each requested path.
// Explicit tracker selections that resolve empty return without tracker upload
// side effects, while omitted tracker selections retain configured default behavior.
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
		return api.Result{}, fmt.Errorf("core: %w", err)
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
			return api.Result{}, fmt.Errorf("context canceled: %w", ctx.Err())
		default:
		}

		singleReq := req
		singleReq.Paths = []string{path}
		singleReq.Options = options
		singleReq.ExternalIDOverrides = mergeExternalIDOverrides(req.ExternalIDOverrides, resolveExternalIDSelection(req.ExternalIDSelections, path))

		signature := overrideSignature(singleReq.ExternalIDOverrides, singleReq.ReleaseNameOverrides, singleReq.MetadataOverrides, singleReq.TrackerConfigOverrides, singleReq.TrackerSiteOverrides, singleReq.ClientOverrides, singleReq.TorrentOverrides, singleReq.ImageHostOverrides, singleReq.ScreenshotOverrides)
		meta, ok := c.getDupeCache(path, signature)
		if req.Mode == api.ModeGUI {
			meta, ok = c.getGUICachedMeta(path, signature, singleReq.ExternalIDOverrides)
		}
		if !ok {
			return api.Result{}, fmt.Errorf("core: upload-only requires prepared metadata for %s", path)
		}

		if len(singleReq.ScreenshotOverrides.MenuPaths) > 0 {
			if err := c.ImportMenuImages(ctx, singleReq, singleReq.ScreenshotOverrides.MenuPaths); err != nil {
				return api.Result{}, fmt.Errorf("core: import menu images failed: %w", err)
			}
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
	var err error
	meta, err = c.applyRequestToCachedPreparedMeta(ctx, meta, req)
	if err != nil {
		return 0, err
	}
	if _, explicitEmpty := resolveTrackersPreservingExplicitEmpty(c.cfg, req.Trackers, meta.TrackersRemove, c.logger, false, false); explicitEmpty {
		c.logger.Debugf("core: upload prepared explicit trackers resolved empty source=%s", meta.SourcePath)
		return 0, nil
	}

	descriptionGroups, err := c.resolveCanonicalDescriptionGroups(ctx, meta, req)
	if err != nil {
		return 0, err
	}
	meta.DescriptionGroups = descriptionGroups

	emitPreparedUploadProgress(ctx, req, meta.SourcePath, "", "torrent", "running", "Preparing torrent")
	torrent, err := c.services.Torrents.Create(ctx, meta)
	if err != nil {
		return 0, fmt.Errorf("core: %w", err)
	}
	c.logger.Debugf("core: torrent ready for %s", meta.SourcePath)
	meta.TorrentPath = torrent.Path

	if c.repo != nil && torrent.InfoHash != "" {
		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("context canceled: %w", ctx.Err())
		default:
		}
		if err := c.persistPreparedInfoHash(ctx, meta.SourcePath, torrent.InfoHash); err != nil {
			return 0, fmt.Errorf("metadata: persist info hash: %w", err)
		}
	}

	if req.Options.DryRun || req.Options.Debug {
		if !meta.Options.NoSeed {
			if err := c.injectPreparedTorrent(ctx, req, meta, torrent); err != nil {
				return 0, err
			}
		}
		c.logger.Debugf("core: dry-run or debug enabled, skipping tracker upload")
		emitPreparedUploadProgress(ctx, req, meta.SourcePath, "", "upload", "completed", "Dry run complete")
		return 0, nil
	}

	c.logger.Debugf("core: uploading to trackers for %s", meta.SourcePath)
	emitPreparedUploadProgress(ctx, req, meta.SourcePath, "", "tracker_upload", "running", "Uploading to tracker")
	summary, err := c.services.Trackers.Upload(ctx, meta)
	if err != nil {
		emitPreparedUploadProgress(ctx, req, meta.SourcePath, "", "tracker_upload", "failed", "Tracker upload failed")
		return 0, fmt.Errorf("core: %w", err)
	}
	emitPreparedUploadProgress(ctx, req, meta.SourcePath, "", "tracker_upload", "completed", "Tracker upload complete")

	if !meta.Options.NoSeed {
		// Cross-seed torrents come from dupe matches and should be injected even when
		// the tracker upload summary later reports no successful uploads.
		if err := c.injectCrossSeedTorrents(ctx, req, meta); err != nil {
			return 0, err
		}
	}

	if summary.Uploaded < 0 {
		return 0, fmt.Errorf("upload summary invalid: %d", summary.Uploaded)
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
				emitPreparedUploadProgress(ctx, req, meta.SourcePath, uploaded.Tracker, "client_injection", "running", "Injecting torrent into client")
				if err := c.services.Clients.Inject(ctx, meta, api.TorrentResult{
					Path:    torrentPath,
					URL:     torrentURL,
					Tracker: uploaded.Tracker,
				}); err != nil {
					emitPreparedUploadProgress(ctx, req, meta.SourcePath, uploaded.Tracker, "client_injection", "failed", "Client injection failed")
					return 0, fmt.Errorf("core: %w", err)
				}
				emitPreparedUploadProgress(ctx, req, meta.SourcePath, uploaded.Tracker, "client_injection", "completed", "Client injection complete")
			}
		}
	}

	return summary.Uploaded, nil
}

func (c *Core) persistPreparedInfoHash(ctx context.Context, sourcePath string, infoHash string) error {
	if c == nil || c.repo == nil {
		return nil
	}
	trimmedPath := strings.TrimSpace(sourcePath)
	trimmedHash := strings.TrimSpace(infoHash)
	if trimmedPath == "" || trimmedHash == "" {
		return nil
	}

	metadata, err := c.repo.GetByPath(ctx, trimmedPath)
	if err != nil {
		if !errors.Is(err, internalerrors.ErrNotFound) {
			return fmt.Errorf("lookup existing metadata: %w", err)
		}
		metadata = db.FileMetadata{Path: trimmedPath}
	} else if strings.TrimSpace(metadata.Path) == "" {
		metadata.Path = trimmedPath
	}
	metadata.InfoHash = trimmedHash
	metadata.UpdatedAt = time.Now().UTC()
	if err := c.repo.Save(ctx, metadata); err != nil {
		return fmt.Errorf("save metadata: %w", err)
	}
	return nil
}

func (c *Core) injectCrossSeedTorrents(ctx context.Context, req api.Request, meta api.PreparedMetadata) error {
	if !c.cfg.PostUpload.CrossSeeding || len(meta.CrossSeedTorrents) == 0 {
		return nil
	}
	if c.services.Clients == nil {
		return errors.New("core: client service not configured")
	}
	for _, crossSeed := range meta.CrossSeedTorrents {
		torrentPath := strings.TrimSpace(crossSeed.TorrentPath)
		torrentURL := strings.TrimSpace(crossSeed.DownloadURL)
		if torrentPath == "" && torrentURL == "" {
			continue
		}
		tracker := strings.ToUpper(strings.TrimSpace(crossSeed.Tracker))
		c.logger.Debugf("core: injecting cross-seed torrent for %s from %s", meta.SourcePath, tracker)
		emitPreparedUploadProgress(ctx, req, meta.SourcePath, tracker, "client_injection", "running", "Injecting cross-seed torrent into client")
		if err := c.services.Clients.Inject(ctx, meta, api.TorrentResult{
			Path:      torrentPath,
			URL:       torrentURL,
			Tracker:   tracker,
			CrossSeed: true,
		}); err != nil {
			emitPreparedUploadProgress(ctx, req, meta.SourcePath, tracker, "client_injection", "failed", "Cross-seed client injection failed")
			return fmt.Errorf("core: %w", err)
		}
		emitPreparedUploadProgress(ctx, req, meta.SourcePath, tracker, "client_injection", "completed", "Cross-seed client injection complete")
	}
	return nil
}

func (c *Core) injectPreparedTorrent(ctx context.Context, req api.Request, meta api.PreparedMetadata, torrent api.TorrentResult) error {
	if c.services.Clients == nil {
		return errors.New("core: client service not configured")
	}
	c.logger.Debugf("core: dry-run or debug enabled, injecting prepared torrent for %s", meta.SourcePath)
	emitPreparedUploadProgress(ctx, req, meta.SourcePath, torrent.Tracker, "client_injection", "running", "Injecting torrent into client")
	if err := c.services.Clients.Inject(ctx, meta, torrent); err != nil {
		emitPreparedUploadProgress(ctx, req, meta.SourcePath, torrent.Tracker, "client_injection", "failed", "Client injection failed")
		return fmt.Errorf("core: %w", err)
	}
	emitPreparedUploadProgress(ctx, req, meta.SourcePath, torrent.Tracker, "client_injection", "completed", "Client injection complete")
	return nil
}

func emitPreparedUploadProgress(ctx context.Context, req api.Request, sourcePath string, tracker string, task string, status string, message string) {
	normalizedTracker := strings.TrimSpace(tracker)
	if normalizedTracker == "" && len(req.Trackers) == 1 {
		normalizedTracker = firstRequestedTracker(req.Trackers)
	}
	api.EmitUploadProgress(ctx, api.UploadProgressUpdate{
		SourcePath: sourcePath,
		Tracker:    normalizedTracker,
		Task:       task,
		Status:     status,
		Message:    message,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	})
}

func firstRequestedTracker(trackers []string) string {
	for _, tracker := range trackers {
		name := strings.TrimSpace(tracker)
		if name != "" {
			return name
		}
	}
	return ""
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
		return api.DupeCheckSummary{}, fmt.Errorf("core: %w", err)
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

	options, err := c.applyDefaultOptions(req.Options)
	if err != nil {
		return api.DupeCheckSummary{}, err
	}
	cacheReq := req
	cacheReq.Options = options

	if req.Mode == api.ModeGUI || req.Mode == api.ModeCLI {
		if cached, ok, err := c.resolveGUICachedPreparedMeta(ctx, cacheReq, uniquePaths[0]); err != nil {
			return api.DupeCheckSummary{}, err
		} else if ok {
			matchedTrackers := mergeTrackerRemovals(nil, cached.MatchedTrackers)
			removeTrackers := mergeTrackerRemovals(req.TrackersRemove, matchedTrackers)
			resolvedTrackers := trackers.ResolveTrackers(c.cfg, req.Trackers, removeTrackers, c.logger)
			summary, err := c.services.Dupes.Check(ctx, cached, resolvedTrackers)
			if err != nil {
				return api.DupeCheckSummary{}, fmt.Errorf("core: %w", err)
			}
			summary = appendPathedDupeResults(summary, matchedTrackers)
			applyDupeSummaryToPreparedMeta(&cached, summary)
			c.storeRefreshedDupeCache(cached.SourcePath, overrideSignature(cached.ExternalIDOverrides, cached.ReleaseNameOverrides, cached.MetadataOverrides, cached.TrackerConfigOverrides, cached.TrackerSiteOverrides, cached.ClientOverrides, cached.TorrentOverrides, cached.ImageHostOverrides, cached.ScreenshotOverrides), cached)
			return summary, nil
		}
		if req.Mode == api.ModeGUI {
			return api.DupeCheckSummary{}, errors.New("core: dupe check requires metadata preview")
		}
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
		return api.DupeCheckSummary{}, fmt.Errorf("core: %w", err)
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
			return fmt.Errorf("core: %w", searchErr)
		}

		if applyTrackerData {
			resetPreparedClientData(&meta, singleReq)
		}
		meta.FoundTrackerMatch = meta.FoundTrackerMatch || searchResult.FoundTrackerMatch
		meta.MatchedTrackers = mergeTrackerRemovals(meta.MatchedTrackers, searchResult.MatchedTrackers)
		meta.TrackersRemove = mergeTrackerRemovals(meta.TrackersRemove, searchResult.MatchedTrackers)
		markTrackerDataMatches(&meta, searchResult.MatchedTrackers)

		if !applyTrackerData {
			return nil
		}

		if !applyPathedTorrentData(&meta, searchResult) {
			return nil
		}
		return nil
	}

	switch {
	case meta.SourceLookupActive:
		c.logger.Debugf("core: running pathed search for tracker presence with source url override active for %s", meta.SourcePath)
		if err := runPathedSearch(false); err != nil {
			return api.DupeCheckSummary{}, err
		}
	case storedApplied:
		c.logger.Debugf("core: running pathed search with stored tracker data present for %s", meta.SourcePath)
		if err := runPathedSearch(true); err != nil {
			return api.DupeCheckSummary{}, err
		}
	case meta.StoredDataFresh:
		if meta.InfoHash == "" && meta.StoredInfoHash != "" {
			meta.InfoHash = meta.StoredInfoHash
			c.logger.Debugf("core: using stored infohash before pathed search for %s", meta.SourcePath)
		} else {
			c.logger.Debugf("core: running pathed search with fresh stored metadata snapshot for %s", meta.SourcePath)
		}
		if err := runPathedSearch(true); err != nil {
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
		return api.DupeCheckSummary{}, fmt.Errorf("core: %w", err)
	}
	meta, err = c.services.Metadata.ApplyMediaInfoIDs(ctx, meta)
	if err != nil {
		return api.DupeCheckSummary{}, fmt.Errorf("core: %w", err)
	}
	meta, err = c.services.Metadata.ApplyArrData(ctx, meta)
	if err != nil {
		return api.DupeCheckSummary{}, fmt.Errorf("core: %w", err)
	}
	meta, err = c.services.Metadata.ResolveExternalIDs(ctx, meta)
	if err != nil {
		return api.DupeCheckSummary{}, fmt.Errorf("core: %w", err)
	}
	meta, err = c.services.Metadata.ApplyMediaDetails(ctx, meta)
	if err != nil {
		return api.DupeCheckSummary{}, fmt.Errorf("core: %w", err)
	}

	c.storeRefreshedDupeCache(meta.SourcePath, overrideSignature(meta.ExternalIDOverrides, meta.ReleaseNameOverrides, meta.MetadataOverrides, meta.TrackerConfigOverrides, meta.TrackerSiteOverrides, meta.ClientOverrides, meta.TorrentOverrides, meta.ImageHostOverrides, meta.ScreenshotOverrides), meta)

	matchedTrackers := mergeTrackerRemovals(nil, meta.MatchedTrackers)
	removeTrackers := mergeTrackerRemovals(req.TrackersRemove, matchedTrackers)
	resolvedTrackers := trackers.ResolveTrackers(c.cfg, req.Trackers, removeTrackers, c.logger)
	summary, err := c.services.Dupes.Check(ctx, meta, resolvedTrackers)
	if err != nil {
		return api.DupeCheckSummary{}, fmt.Errorf("core: %w", err)
	}
	summary = appendPathedDupeResults(summary, matchedTrackers)
	applyDupeSummaryToPreparedMeta(&meta, summary)
	c.storeRefreshedDupeCache(meta.SourcePath, overrideSignature(meta.ExternalIDOverrides, meta.ReleaseNameOverrides, meta.MetadataOverrides, meta.TrackerConfigOverrides, meta.TrackerSiteOverrides, meta.ClientOverrides, meta.TorrentOverrides, meta.ImageHostOverrides, meta.ScreenshotOverrides), meta)
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
		return api.ScreenshotPlan{}, fmt.Errorf("core: %w", err)
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
		if cached, ok, err := c.resolveGUICachedPreparedMeta(ctx, req, uniquePaths[0]); err != nil {
			return api.ScreenshotPlan{}, err
		} else if ok {
			return wrapCoreResult(c.services.Screenshots.Plan(ctx, cached, cached.Options.Screens))
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
		return api.ScreenshotPlan{}, fmt.Errorf("core: %w", err)
	}

	return wrapCoreResult(c.services.Screenshots.Plan(ctx, meta, options.Screens))
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
		return api.ScreenshotResult{}, fmt.Errorf("core: %w", err)
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
		if cached, ok, err := c.resolveGUICachedPreparedMeta(ctx, req, uniquePaths[0]); err != nil {
			return api.ScreenshotResult{}, err
		} else if ok {
			return wrapCoreResult(c.services.Screenshots.Capture(ctx, cached, selections, purpose))
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
		return api.ScreenshotResult{}, fmt.Errorf("core: %w", err)
	}

	return wrapCoreResult(c.services.Screenshots.Capture(ctx, meta, selections, purpose))
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
		return api.ScreenshotPreview{}, fmt.Errorf("core: %w", err)
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
		if cached, ok, err := c.resolveGUICachedPreparedMeta(ctx, req, uniquePaths[0]); err != nil {
			return api.ScreenshotPreview{}, err
		} else if ok {
			return wrapCoreResult(c.services.Screenshots.PreviewFrame(ctx, cached, timestampSeconds))
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
		return api.ScreenshotPreview{}, fmt.Errorf("core: %w", err)
	}

	return wrapCoreResult(c.services.Screenshots.PreviewFrame(ctx, meta, timestampSeconds))
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
		return fmt.Errorf("core: %w", err)
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
		if cached, ok, err := c.resolveGUICachedPreparedMeta(ctx, req, uniquePaths[0]); err != nil {
			return err
		} else if ok {
			return wrapCoreError(c.services.Screenshots.Delete(ctx, cached, imagePath))
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
		return fmt.Errorf("core: %w", err)
	}

	return wrapCoreError(c.services.Screenshots.Delete(ctx, meta, imagePath))
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
		return fmt.Errorf("core: %w", err)
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
		return fmt.Errorf("core: %w", err)
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
		if cached, ok, err := c.resolveGUICachedPreparedMeta(ctx, req, uniquePaths[0]); err != nil {
			return err
		} else if ok {
			return wrapCoreError(c.services.Screenshots.SaveFinalSelections(ctx, cached, images))
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
		return fmt.Errorf("core: %w", err)
	}

	return wrapCoreError(c.services.Screenshots.SaveFinalSelections(ctx, meta, images))
}

func (c *Core) ImportMenuImages(ctx context.Context, req api.Request, importPaths []string) error {
	if len(req.Paths) == 0 {
		return internalerrors.ErrInvalidInput
	}
	if len(importPaths) == 0 {
		return nil
	}
	normalizedPaths, err := c.services.Filesystem.ValidatePaths(ctx, req.Paths)
	if err != nil {
		return fmt.Errorf("core: validate menu paths: %w", err)
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
	sourcePath := uniquePaths[0]

	existing, err := c.repo.ListFinalSelections(ctx, sourcePath)
	if err != nil && !errors.Is(err, internalerrors.ErrNotFound) {
		return fmt.Errorf("core: list menu selections: %w", err)
	}
	maxOrder := -1
	for _, sel := range existing {
		if sel.Order > maxOrder {
			maxOrder = sel.Order
		}
	}

	var expandedPaths []string
	for _, p := range importPaths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		info, err := os.Stat(p)
		if err != nil {
			return fmt.Errorf("stat menu path %s: %w", p, err)
		}
		if info.IsDir() {
			entries, err := os.ReadDir(p)
			if err != nil {
				return fmt.Errorf("read menu dir %s: %w", p, err)
			}
			for _, entry := range entries {
				if !entry.IsDir() {
					ext := strings.ToLower(filepath.Ext(entry.Name()))
					if ext == ".png" || ext == ".jpg" || ext == ".jpeg" {
						expandedPaths = append(expandedPaths, filepath.Join(p, entry.Name()))
					}
				}
			}
		} else {
			ext := strings.ToLower(filepath.Ext(p))
			if ext == ".png" || ext == ".jpg" || ext == ".jpeg" {
				expandedPaths = append(expandedPaths, p)
			}
		}
	}

	tmpRoot, err := db.Subdir(c.cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		return fmt.Errorf("core: resolve tmp root: %w", err)
	}

	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, api.PreparedMetadata{}, sourcePath)
	if err != nil {
		return fmt.Errorf("core: create release tmp dir: %w", err)
	}

	for idx, p := range expandedPaths {
		destPath := filepath.Join(tmpDir, filepath.Base(p))

		// Copy file to release tmp dir
		if err := filesystem.CopyFile(p, destPath); err != nil {
			return fmt.Errorf("core: copy menu image %s: %w", p, err)
		}

		existing = append(existing, api.ScreenshotFinalSelection{
			SourcePath: sourcePath,
			ImagePath:  destPath,
			Order:      maxOrder + 1 + idx,
			Source:     string(api.ScreenshotPurposeMenu),
			SelectedAt: time.Now().UTC(),
		})
	}
	if err := c.repo.SaveFinalSelections(ctx, sourcePath, existing); err != nil {
		return fmt.Errorf("core: save menu selections: %w", err)
	}
	return nil
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
		return nil, fmt.Errorf("core: %w", err)
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
		if cached, ok, err := c.resolveGUICachedPreparedMeta(ctx, req, uniquePaths[0]); err != nil {
			return nil, err
		} else if ok {
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
			return nil, fmt.Errorf("core: %w", err)
		}
		meta = preparedMeta
	}

	return wrapCoreResult(c.services.Images.ListCandidates(ctx, meta))
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
		return nil, fmt.Errorf("core: %w", err)
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

	return wrapCoreResult(c.repo.ListUploadedImagesByPath(ctx, uniquePaths[0]))
}

func (c *Core) UploadImages(ctx context.Context, req api.Request, host string, images []api.ScreenshotImage) (api.UploadImagesResult, error) {
	if len(req.Paths) == 0 {
		return api.UploadImagesResult{}, internalerrors.ErrInvalidInput
	}
	if c.services.Images == nil {
		return api.UploadImagesResult{}, errors.New("core: image hosting service not configured")
	}
	if len(images) == 0 {
		return api.UploadImagesResult{}, internalerrors.ErrInvalidInput
	}

	normalizedPaths, err := c.services.Filesystem.ValidatePaths(ctx, req.Paths)
	if err != nil {
		return api.UploadImagesResult{}, fmt.Errorf("core: %w", err)
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
		return api.UploadImagesResult{}, internalerrors.ErrInvalidInput
	}

	var meta api.PreparedMetadata
	if req.Mode == api.ModeGUI {
		if cached, ok, err := c.resolveGUICachedPreparedMeta(ctx, req, uniquePaths[0]); err != nil {
			return api.UploadImagesResult{}, err
		} else if ok {
			meta = cached
		} else {
			meta = api.PreparedMetadata{SourcePath: uniquePaths[0]}
		}
	} else {
		options, err := c.applyDefaultOptions(req.Options)
		if err != nil {
			return api.UploadImagesResult{}, err
		}
		singleReq := req
		singleReq.Paths = []string{uniquePaths[0]}
		singleReq.Options = options

		preparedMeta, err := c.services.Metadata.Prepare(ctx, singleReq)
		if err != nil {
			return api.UploadImagesResult{}, fmt.Errorf("core: %w", err)
		}
		meta = preparedMeta
	}

	targets, err := c.resolveImageUploadTargets(req, meta, host)
	if err != nil {
		return api.UploadImagesResult{}, err
	}
	return c.uploadImagesToTargetsWithFallback(ctx, meta, host, targets, images)
}

func (c *Core) resolveImageUploadTargets(req api.Request, meta api.PreparedMetadata, host string) ([]trackers.ImageUploadTarget, error) {
	normalizedHost := strings.ToLower(strings.TrimSpace(host))
	if normalizedHost == "" {
		return nil, internalerrors.ErrInvalidInput
	}

	trackerCfg := c.cfg
	trackerCfg.Trackers.DefaultTrackers = nil
	resolvedTrackers := trackers.ResolveTrackers(trackerCfg, req.Trackers, req.TrackersRemove, c.logger)
	resolvedTrackers = c.filterImageUploadTrackers(resolvedTrackers, meta)
	targets, err := trackers.NeededImageUploadTargetsForMetadata(c.cfg, resolvedTrackers, normalizedHost, meta)
	if err != nil {
		return nil, fmt.Errorf("core: %w", err)
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("core: image host %q is tracker-scoped but no active tracker can use it", normalizedHost)
	}
	normalized := make([]trackers.ImageUploadTarget, 0, len(targets))
	for _, target := range targets {
		target = normalizeImageUploadTarget(target)
		if target.Host == "" {
			continue
		}
		normalized = append(normalized, target)
	}
	if len(normalized) == 0 {
		return nil, fmt.Errorf("core: image host %q resolved image upload targets were filtered out after tracker eligibility and normalization", normalizedHost)
	}
	return normalized, nil
}

// filterImageUploadTrackers returns canonical tracker names that can receive
// tracker-scoped image uploads, excluding blocked, rule-failed, or already matched trackers.
func (c *Core) filterImageUploadTrackers(trackerNames []string, meta api.PreparedMetadata) []string {
	filtered := make([]string, 0, len(trackerNames))
	for _, tracker := range trackerNames {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name == "" {
			continue
		}
		blockedReasons := blockedReasonsForTracker(meta.BlockedTrackers, name)
		ruleFailures := ruleFailuresForTracker(meta.TrackerRuleFailures, name)
		existingMatch := matchedTrackerForUpload(meta.MatchedTrackers, name)
		if len(blockedReasons) > 0 || (!meta.IgnoreTrackerRuleFailures && len(ruleFailures) > 0) || existingMatch {
			if c.logger != nil {
				c.logger.Debugf("core: excluding blocked image upload tracker tracker=%s blocked_reasons=%v rule_failures=%d existing_match=%t", name, blockedReasons, len(ruleFailures), existingMatch)
			}
			continue
		}
		filtered = append(filtered, name)
	}
	return filtered
}

func matchedTrackerForUpload(matchedTrackers []string, tracker string) bool {
	if len(matchedTrackers) == 0 {
		return false
	}
	name := strings.ToUpper(strings.TrimSpace(tracker))
	if name == "" {
		return false
	}
	for _, matched := range matchedTrackers {
		for _, entry := range splitTrackerLabel(matched) {
			if strings.EqualFold(strings.TrimSpace(entry), name) {
				return true
			}
		}
	}
	return false
}

func blockedReasonsForTracker(blocked map[string][]api.TrackerBlockReason, tracker string) []api.TrackerBlockReason {
	if len(blocked) == 0 {
		return nil
	}
	name := strings.ToUpper(strings.TrimSpace(tracker))
	if reasons, ok := blocked[name]; ok {
		return reasons
	}
	for key, reasons := range blocked {
		if strings.EqualFold(strings.TrimSpace(key), name) {
			return reasons
		}
	}
	return nil
}

func ruleFailuresForTracker(failures map[string][]api.RuleFailure, tracker string) []api.RuleFailure {
	if len(failures) == 0 {
		return nil
	}
	name := strings.ToUpper(strings.TrimSpace(tracker))
	if trackerFailures, ok := failures[name]; ok {
		return trackerFailures
	}
	for key, trackerFailures := range failures {
		if strings.EqualFold(strings.TrimSpace(key), name) {
			return trackerFailures
		}
	}
	return nil
}

func (c *Core) resolveFallbackImageUploadTargets(host string, trackerNames []string, excludedHosts []string, meta api.PreparedMetadata) ([]trackers.ImageUploadTarget, error) {
	normalizedHost := strings.ToLower(strings.TrimSpace(host))
	if normalizedHost == "" || len(trackerNames) == 0 {
		return nil, nil
	}
	targets, err := trackers.NeededImageUploadTargetsForMetadataExcluding(c.cfg, trackerNames, normalizedHost, excludedHosts, meta)
	if err != nil {
		return nil, fmt.Errorf("core: %w", err)
	}
	normalized := make([]trackers.ImageUploadTarget, 0, len(targets))
	for _, target := range targets {
		target = normalizeImageUploadTarget(target)
		if target.Host == "" {
			continue
		}
		normalized = append(normalized, target)
	}
	return normalized, nil
}

func (c *Core) uploadImagesToTargetsWithFallback(ctx context.Context, meta api.PreparedMetadata, host string, targets []trackers.ImageUploadTarget, images []api.ScreenshotImage) (api.UploadImagesResult, error) {
	allLinks := make([]api.UploadedImageLink, 0, len(images)*len(targets))
	failedHosts := make(map[string]struct{}, len(targets))
	currentTargets := targets
	var failures []api.UploadImageHostFailure

	for len(currentTargets) > 0 {
		result := c.uploadImagesToTargets(ctx, meta, currentTargets, images)
		allLinks = append(allLinks, result.Links...)
		if len(result.Failures) == 0 {
			return api.UploadImagesResult{Links: allLinks}, nil
		}

		failures = result.Failures
		for _, failure := range result.Failures {
			host := strings.ToLower(strings.TrimSpace(failure.Host))
			if host != "" {
				failedHosts[host] = struct{}{}
			}
		}

		blockedTrackers := uploadFailureTrackers(failures)
		fallbackTargets, err := c.resolveFallbackImageUploadTargets(host, blockedTrackers, sortedMapKeys(failedHosts), meta)
		if err != nil {
			return api.UploadImagesResult{}, err
		}

		var recoveredTrackers []string
		nextTargets := make([]trackers.ImageUploadTarget, 0, len(fallbackTargets))
		for _, target := range fallbackTargets {
			if uploadedLinksCoverTarget(allLinks, target, len(images)) {
				recoveredTrackers = append(recoveredTrackers, target.Trackers...)
				continue
			}
			nextTargets = append(nextTargets, target)
		}
		failures = filterUploadFailuresForRecoveredTrackers(failures, recoveredTrackers)
		if len(nextTargets) == 0 {
			if len(failures) == 0 {
				return api.UploadImagesResult{Links: allLinks}, nil
			}
			return api.UploadImagesResult{Links: allLinks, Failures: failures}, nil
		}

		c.logger.Warnf("core: retrying image uploads after host failures failed_hosts=%s fallback_hosts=%s trackers=%v", strings.Join(sortedMapKeys(failedHosts), ","), strings.Join(uploadTargetHosts(nextTargets), ","), uploadTargetTrackers(nextTargets))
		currentTargets = nextTargets
	}

	return api.UploadImagesResult{Links: allLinks, Failures: failures}, nil
}

func uploadFailureTrackers(failures []api.UploadImageHostFailure) []string {
	trackersList := make([]string, 0)
	for _, failure := range failures {
		for _, tracker := range failure.Trackers {
			trackersList = appendUniqueNormalizedTracker(trackersList, tracker)
		}
	}
	return trackersList
}

func filterUploadFailuresForRecoveredTrackers(failures []api.UploadImageHostFailure, recoveredTrackers []string) []api.UploadImageHostFailure {
	if len(failures) == 0 || len(recoveredTrackers) == 0 {
		return failures
	}
	recovered := make(map[string]struct{}, len(recoveredTrackers))
	for _, tracker := range recoveredTrackers {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name != "" {
			recovered[name] = struct{}{}
		}
	}

	filtered := make([]api.UploadImageHostFailure, 0, len(failures))
	for _, failure := range failures {
		remainingTrackers := make([]string, 0, len(failure.Trackers))
		for _, tracker := range failure.Trackers {
			name := strings.ToUpper(strings.TrimSpace(tracker))
			if name == "" {
				continue
			}
			if _, ok := recovered[name]; ok {
				continue
			}
			remainingTrackers = appendUniqueNormalizedTracker(remainingTrackers, name)
		}
		if len(failure.Trackers) > 0 && len(remainingTrackers) == 0 {
			continue
		}
		failure.Trackers = remainingTrackers
		filtered = append(filtered, failure)
	}
	return filtered
}

func uploadedLinksCoverTarget(links []api.UploadedImageLink, target trackers.ImageUploadTarget, expectedImages int) bool {
	if expectedImages == 0 {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(target.Host))
	scope := normalizeImageUploadUsageScope(target.UsageScope)
	seenPaths := make(map[string]struct{}, expectedImages)
	for _, link := range links {
		if !strings.EqualFold(strings.TrimSpace(link.Host), host) {
			continue
		}
		if normalizeImageUploadUsageScope(link.UsageScope) != scope {
			continue
		}
		path := normalizedUploadImagePath(link.ImagePath)
		if path == "" {
			continue
		}
		seenPaths[path] = struct{}{}
	}
	return len(seenPaths) >= expectedImages
}

func uploadTargetHosts(targets []trackers.ImageUploadTarget) []string {
	hosts := make([]string, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		host := strings.ToLower(strings.TrimSpace(target.Host))
		if host == "" {
			continue
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		hosts = append(hosts, host)
	}
	slices.Sort(hosts)
	return hosts
}

func uploadTargetTrackers(targets []trackers.ImageUploadTarget) []string {
	trackersList := make([]string, 0)
	for _, target := range targets {
		for _, tracker := range target.Trackers {
			trackersList = appendUniqueNormalizedTracker(trackersList, tracker)
		}
	}
	slices.Sort(trackersList)
	return trackersList
}

func sortedMapKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if strings.TrimSpace(key) == "" {
			continue
		}
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func (c *Core) uploadImagesToTargets(ctx context.Context, meta api.PreparedMetadata, targets []trackers.ImageUploadTarget, images []api.ScreenshotImage) api.UploadImagesResult {
	type uploadResult struct {
		index  int
		target trackers.ImageUploadTarget
		links  []api.UploadedImageLink
		err    error
	}

	resultCh := make(chan uploadResult, len(targets))
	var wg sync.WaitGroup
	for idx, target := range targets {
		wg.Add(1)
		go func(idx int, target trackers.ImageUploadTarget) {
			defer wg.Done()
			uploaded, err := c.uploadImagesToTarget(ctx, meta, target, images)
			resultCh <- uploadResult{
				index:  idx,
				target: normalizeImageUploadTarget(target),
				links:  uploaded,
				err:    err,
			}
		}(idx, target)
	}
	wg.Wait()
	close(resultCh)

	ordered := make([]uploadResult, len(targets))
	for result := range resultCh {
		ordered[result.index] = result
	}

	results := make([]api.UploadedImageLink, 0, len(images)*len(targets))
	failures := make([]api.UploadImageHostFailure, 0)
	failureMessages := make([]string, 0)
	for _, result := range ordered {
		results = append(results, result.links...)
		if result.err != nil {
			failure := api.UploadImageHostFailure{
				Host:       result.target.Host,
				UsageScope: result.target.UsageScope,
				Trackers:   slices.Clone(result.target.Trackers),
				Message:    uploadFailureMessage(result.err),
			}
			failures = append(failures, failure)
			failureMessages = append(failureMessages, fmt.Sprintf("%s: %v", result.target.Host, result.err))
		}
	}
	if len(failures) == 0 {
		return api.UploadImagesResult{Links: results}
	}
	result := api.UploadImagesResult{Links: results, Failures: failures}
	if len(results) > 0 {
		c.logger.Warnf("core: image uploads completed with %d host failures and %d successful links: %s", len(failures), len(results), strings.Join(failureMessages, "; "))
		return result
	}
	c.logger.Warnf("core: image uploads failed for all hosts: %s", strings.Join(failureMessages, "; "))
	return result
}

func normalizeImageUploadTarget(target trackers.ImageUploadTarget) trackers.ImageUploadTarget {
	target.Host = strings.ToLower(strings.TrimSpace(target.Host))
	target.UsageScope = normalizeImageUploadUsageScope(target.UsageScope)
	trackersList := make([]string, 0, len(target.Trackers))
	for _, tracker := range target.Trackers {
		trackersList = appendUniqueNormalizedTracker(trackersList, tracker)
	}
	target.Trackers = trackersList
	return target
}

func appendUniqueNormalizedTracker(trackersList []string, tracker string) []string {
	name := strings.ToUpper(strings.TrimSpace(tracker))
	if name == "" {
		return trackersList
	}
	if slices.Contains(trackersList, name) {
		return trackersList
	}
	return append(trackersList, name)
}

func (c *Core) uploadImagesToTarget(ctx context.Context, meta api.PreparedMetadata, target trackers.ImageUploadTarget, images []api.ScreenshotImage) ([]api.UploadedImageLink, error) {
	target.Host = strings.ToLower(strings.TrimSpace(target.Host))
	target.UsageScope = normalizeImageUploadUsageScope(target.UsageScope)
	if c.repo == nil {
		c.logger.Debugf("core: uploading images host=%s scope=%s trackers=%v count=%d", target.Host, target.UsageScope, target.Trackers, len(images))
		return wrapCoreResult(c.services.Images.Upload(ctx, meta, target.Host, target.UsageScope, images))
	}

	existing, err := c.repo.ListUploadedImagesByPath(ctx, meta.SourcePath)
	if err != nil {
		return nil, fmt.Errorf("core: %w", err)
	}
	existingByPath := uploadedImagesByPathForTarget(existing, target)
	results := make([]api.UploadedImageLink, 0, len(images))
	missing := make([]api.ScreenshotImage, 0, len(images))
	for _, image := range images {
		key := normalizedUploadImagePath(image.Path)
		if key == "" {
			missing = append(missing, image)
			continue
		}
		if link, ok := existingByPath[key]; ok {
			results = append(results, link)
			continue
		}
		missing = append(missing, image)
	}
	if len(missing) == 0 {
		c.logger.Debugf("core: reusing uploaded images host=%s scope=%s trackers=%v count=%d", target.Host, target.UsageScope, target.Trackers, len(results))
		return results, nil
	}

	c.logger.Debugf("core: uploading missing images host=%s scope=%s trackers=%v missing=%d reused=%d", target.Host, target.UsageScope, target.Trackers, len(missing), len(results))
	uploaded, err := c.services.Images.Upload(ctx, meta, target.Host, target.UsageScope, missing)
	results = append(results, uploaded...)
	if err != nil {
		return results, fmt.Errorf("core: %w", err)
	}
	return results, nil
}

func uploadFailureMessage(err error) string {
	if err == nil {
		return ""
	}
	if unwrapped := errors.Unwrap(err); unwrapped != nil {
		return unwrapped.Error()
	}
	return err.Error()
}

func uploadedImagesByPathForTarget(images []api.UploadedImageLink, target trackers.ImageUploadTarget) map[string]api.UploadedImageLink {
	matches := make(map[string]api.UploadedImageLink, len(images))
	for _, image := range images {
		if !strings.EqualFold(strings.TrimSpace(image.Host), target.Host) {
			continue
		}
		if !strings.EqualFold(normalizeImageUploadUsageScope(image.UsageScope), normalizeImageUploadUsageScope(target.UsageScope)) {
			continue
		}
		key := normalizedUploadImagePath(image.ImagePath)
		if key == "" {
			continue
		}
		matches[key] = image
	}
	return matches
}

func normalizedUploadImagePath(pathValue string) string {
	trimmed := strings.TrimSpace(pathValue)
	if trimmed == "" {
		return ""
	}
	absPath, err := filepath.Abs(trimmed)
	if err != nil {
		return filepath.Clean(trimmed)
	}
	return filepath.Clean(absPath)
}

func normalizeImageUploadUsageScope(scope string) string {
	trimmed := strings.TrimSpace(scope)
	if trimmed == "" || strings.EqualFold(trimmed, "global") {
		return "global"
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "tracker:") {
		tracker := strings.ToUpper(strings.TrimSpace(trimmed[len("tracker:"):]))
		if tracker == "" {
			return "global"
		}
		return "tracker:" + tracker
	}
	return trimmed
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
		return fmt.Errorf("core: %w", err)
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
	return wrapCoreError(c.repo.DeleteUploadedImage(ctx, uniquePaths[0], imagePath, host))
}

// FetchMetadataPreview prepares and enriches metadata for one validated source path,
// emits metadata progress, and stores the refreshed prepared metadata cache entry.
// An empty tracker request is not an explicit "no trackers" request; downstream
// tracker resolution can still use configured defaults.
func (c *Core) FetchMetadataPreview(ctx context.Context, req api.Request) (api.MetadataPreview, error) {
	req = normalizeExecutionRequest(req)
	if len(req.Paths) == 0 {
		return api.MetadataPreview{}, internalerrors.ErrInvalidInput
	}
	c.logger.Debugf("core: metadata preview request with %d paths", len(req.Paths))

	normalizedPaths, err := c.services.Filesystem.ValidatePaths(ctx, req.Paths)
	if err != nil {
		return api.MetadataPreview{}, fmt.Errorf("core: %w", err)
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
		return wrapCoreError(prepareErr)
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
			return fmt.Errorf("core: %w", searchErr)
		}
		if applyTrackerData {
			resetPreparedClientData(&meta, singleReq)
		}
		meta.FoundTrackerMatch = meta.FoundTrackerMatch || searchResult.FoundTrackerMatch
		meta.MatchedTrackers = mergeTrackerRemovals(meta.MatchedTrackers, searchResult.MatchedTrackers)
		meta.TrackersRemove = mergeTrackerRemovals(meta.TrackersRemove, searchResult.MatchedTrackers)
		markTrackerDataMatches(&meta, searchResult.MatchedTrackers)
		if !applyTrackerData {
			c.logger.Debugf("core: pathed search merged tracker presence only for %s", meta.SourcePath)
			return nil
		}
		if applyPathedTorrentData(&meta, searchResult) {
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
		c.logger.Debugf("core: running pathed search with stored tracker data present for %s", meta.SourcePath)
		if err := runPathedSearch(true); err != nil {
			return api.MetadataPreview{}, err
		}
	case meta.StoredDataFresh:
		if meta.InfoHash == "" && meta.StoredInfoHash != "" {
			meta.InfoHash = meta.StoredInfoHash
			c.logger.Debugf("core: using stored infohash before pathed search for %s", meta.SourcePath)
		} else {
			c.logger.Debugf("core: running pathed search with fresh stored metadata snapshot for %s", meta.SourcePath)
		}
		if err := runPathedSearch(true); err != nil {
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
		return wrapCoreError(enrichErr)
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
			return wrapCoreError(enrichErr)
		}); err != nil {
			return api.MetadataPreview{}, err
		}
	}
	if err := emitPhase("mediainfo-ids", "Applying MediaInfo IDs", func() error {
		var applyErr error
		meta, applyErr = c.services.Metadata.ApplyMediaInfoIDs(ctx, meta)
		return wrapCoreError(applyErr)
	}); err != nil {
		return api.MetadataPreview{}, err
	}
	if err := emitPhase("arr", "Applying Sonarr/Radarr data", func() error {
		var applyErr error
		meta, applyErr = c.services.Metadata.ApplyArrData(ctx, meta)
		return wrapCoreError(applyErr)
	}); err != nil {
		return api.MetadataPreview{}, err
	}
	if err := emitPhase("external-ids", "Resolving external IDs", func() error {
		var resolveErr error
		meta, resolveErr = c.services.Metadata.ResolveExternalIDs(ctx, meta)
		return wrapCoreError(resolveErr)
	}); err != nil {
		return api.MetadataPreview{}, err
	}
	if err := emitPhase("media-details", "Applying media details", func() error {
		var applyErr error
		meta, applyErr = c.services.Metadata.ApplyMediaDetails(ctx, meta)
		return wrapCoreError(applyErr)
	}); err != nil {
		return api.MetadataPreview{}, err
	}

	c.storeRefreshedDupeCache(meta.SourcePath, overrideSignature(meta.ExternalIDOverrides, meta.ReleaseNameOverrides, meta.MetadataOverrides, meta.TrackerConfigOverrides, meta.TrackerSiteOverrides, meta.ClientOverrides, meta.TorrentOverrides, meta.ImageHostOverrides, meta.ScreenshotOverrides), meta)

	emitProgress("complete", "completed", "Metadata preview ready")

	return buildMetadataPreview(meta, c.cfg), nil
}

// FetchPreparationPreview builds the tracker preparation preview for one validated
// source path. Explicit tracker selections limit preparation to those trackers;
// selections that resolve empty return an empty preview.
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
		return api.PreparationPreview{}, fmt.Errorf("core: %w", err)
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
		if cached, ok, err := c.resolveGUICachedPreparedMeta(ctx, req, uniquePaths[0]); err != nil {
			return api.PreparationPreview{}, err
		} else if ok {
			resolvedTrackers, explicitEmpty := resolveTrackersPreservingExplicitEmpty(c.cfg, req.Trackers, requestPreparedMetaTrackersRemove(cached, req), c.logger, false, false)
			if explicitEmpty {
				c.logger.Debugf("core: preparation explicit trackers resolved empty source=%s", cached.SourcePath)
				return api.PreparationPreview{SourcePath: cached.SourcePath}, nil
			}
			c.logger.Debugf("core: preparation resolved trackers %v", resolvedTrackers)
			return wrapCoreResult(c.services.Trackers.BuildPreparation(ctx, cached, resolvedTrackers))
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
	singleReq.ExternalIDOverrides = mergeExternalIDOverrides(req.ExternalIDOverrides, resolveExternalIDSelection(req.ExternalIDSelections, uniquePaths[0]))

	meta, err := c.services.Metadata.Prepare(ctx, singleReq)
	if err != nil {
		return api.PreparationPreview{}, fmt.Errorf("core: %w", err)
	}
	meta = applyRequestToPreparedMeta(meta, singleReq, c.cfg, c.logger)

	resolvedTrackers, explicitEmpty := resolveTrackersPreservingExplicitEmpty(c.cfg, req.Trackers, meta.TrackersRemove, c.logger, false, false)
	if explicitEmpty {
		c.logger.Debugf("core: preparation explicit trackers resolved empty after prepare source=%s", meta.SourcePath)
		return api.PreparationPreview{SourcePath: meta.SourcePath}, nil
	}
	if req.Mode == api.ModeGUI {
		c.storeDupeCache(meta.SourcePath, overrideSignature(meta.ExternalIDOverrides, meta.ReleaseNameOverrides, meta.MetadataOverrides, meta.TrackerConfigOverrides, meta.TrackerSiteOverrides, meta.ClientOverrides, meta.TorrentOverrides, meta.ImageHostOverrides, meta.ScreenshotOverrides), meta)
	}
	c.logger.Debugf("core: preparation resolved trackers %v", resolvedTrackers)
	return wrapCoreResult(c.services.Trackers.BuildPreparation(ctx, meta, resolvedTrackers))
}

// FetchTrackerDryRunPreview builds per-tracker dry-run upload entries from cached
// prepared metadata, creating torrents and cache state only after selected trackers resolve.
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
		return api.TrackerDryRunPreview{}, fmt.Errorf("core: %w", err)
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
	c.logger.Debugf("core: tracker dry-run options resolved path=%s debug=%t dry_run=%t no_seed=%t run_log_level=%s", uniquePaths[0], options.Debug, options.DryRun, options.NoSeed, options.RunLogLevel)

	singleReq := req
	singleReq.Paths = []string{uniquePaths[0]}
	singleReq.Options = options
	singleReq.ExternalIDOverrides = mergeExternalIDOverrides(req.ExternalIDOverrides, resolveExternalIDSelection(req.ExternalIDSelections, uniquePaths[0]))

	signature := overrideSignature(singleReq.ExternalIDOverrides, singleReq.ReleaseNameOverrides, singleReq.MetadataOverrides, singleReq.TrackerConfigOverrides, singleReq.TrackerSiteOverrides, singleReq.ClientOverrides, singleReq.TorrentOverrides, singleReq.ImageHostOverrides, singleReq.ScreenshotOverrides)
	meta, ok := c.getDupeCache(uniquePaths[0], signature)
	if req.Mode == api.ModeGUI {
		entry, _, found := c.lookupGUICachedMetaEntry(singleReq, uniquePaths[0])
		if found {
			ok = true
			if _, explicitEmpty := resolveTrackersPreservingExplicitEmpty(c.cfg, req.Trackers, requestPreparedMetaTrackersRemove(entry.meta, singleReq), c.logger, false, false); explicitEmpty {
				c.logger.Debugf("core: tracker dry-run explicit trackers resolved empty source=%s", entry.meta.SourcePath)
				return api.TrackerDryRunPreview{SourcePath: entry.meta.SourcePath, Trackers: []api.TrackerDryRunEntry{}}, nil
			}
			if entry.requestRefreshed && cachedPreparedMetaMatchesRequest(entry.meta, singleReq, uniquePaths[0]) {
				meta = deepCopyPreparedMetadata(entry.meta)
			} else {
				meta, err = c.applyRequestToCachedPreparedMeta(ctx, entry.meta, singleReq)
				if err != nil {
					return api.TrackerDryRunPreview{}, err
				}
			}
		}
	} else {
		if !ok {
			return api.TrackerDryRunPreview{}, fmt.Errorf("core: tracker dry-run requires prepared metadata for %s", uniquePaths[0])
		}
		if _, explicitEmpty := resolveTrackersPreservingExplicitEmpty(c.cfg, req.Trackers, requestPreparedMetaTrackersRemove(meta, singleReq), c.logger, false, false); explicitEmpty {
			c.logger.Debugf("core: tracker dry-run explicit trackers resolved empty source=%s", meta.SourcePath)
			return api.TrackerDryRunPreview{SourcePath: meta.SourcePath, Trackers: []api.TrackerDryRunEntry{}}, nil
		}
		meta, err = c.applyRequestToCachedPreparedMeta(ctx, meta, singleReq)
		if err != nil {
			return api.TrackerDryRunPreview{}, err
		}
	}
	if !ok {
		return api.TrackerDryRunPreview{}, fmt.Errorf("core: tracker dry-run requires prepared metadata for %s", uniquePaths[0])
	}
	c.logger.Debugf("core: tracker dry-run using cached prepared metadata for %s meta_no_seed=%t req_no_seed=%t", uniquePaths[0], meta.Options.NoSeed, singleReq.Options.NoSeed)
	resolvedTrackers, explicitEmpty := resolveTrackersPreservingExplicitEmpty(c.cfg, req.Trackers, meta.TrackersRemove, c.logger, false, false)
	if explicitEmpty {
		c.logger.Debugf("core: tracker dry-run explicit trackers resolved empty source=%s", meta.SourcePath)
		return api.TrackerDryRunPreview{SourcePath: meta.SourcePath, Trackers: []api.TrackerDryRunEntry{}}, nil
	}
	descriptionGroups, err := c.resolveCanonicalDescriptionGroups(ctx, meta, singleReq)
	if err != nil {
		return api.TrackerDryRunPreview{}, err
	}
	meta.DescriptionGroups = descriptionGroups

	torrent, err := c.services.Torrents.Create(ctx, meta)
	if err != nil {
		return api.TrackerDryRunPreview{}, fmt.Errorf("core: %w", err)
	}
	meta.TorrentPath = torrent.Path

	entries, err := c.services.Trackers.BuildUploadDryRun(ctx, meta, resolvedTrackers)
	if err != nil {
		return api.TrackerDryRunPreview{}, fmt.Errorf("core: %w", err)
	}
	annotateDryRunReleaseNames(meta, entries)

	c.logger.Debugf("core: tracker dry-run torrent ready for %s path=%s no_seed=%t", meta.SourcePath, torrent.Path, meta.Options.NoSeed)
	if meta.Options.NoSeed {
		c.logger.Debugf("core: tracker dry-run skipping client injection for %s: no-seed enabled", meta.SourcePath)
	} else if err := c.injectTrackerDryRunTorrents(ctx, singleReq, meta, entries, torrent); err != nil {
		return api.TrackerDryRunPreview{}, err
	}

	c.storeDupeCache(meta.SourcePath, overrideSignature(meta.ExternalIDOverrides, meta.ReleaseNameOverrides, meta.MetadataOverrides, meta.TrackerConfigOverrides, meta.TrackerSiteOverrides, meta.ClientOverrides, meta.TorrentOverrides, meta.ImageHostOverrides, meta.ScreenshotOverrides), meta)

	return api.TrackerDryRunPreview{SourcePath: meta.SourcePath, Trackers: entries}, nil
}

func (c *Core) injectTrackerDryRunTorrents(ctx context.Context, req api.Request, meta api.PreparedMetadata, entries []api.TrackerDryRunEntry, fallback api.TorrentResult) error {
	ready := make([]api.TrackerDryRunEntry, 0, len(entries))
	for _, entry := range entries {
		if strings.EqualFold(strings.TrimSpace(entry.Status), "ready") {
			ready = append(ready, entry)
		} else {
			c.logger.Debugf("core: tracker dry-run skipping client injection for tracker=%s status=%s", strings.TrimSpace(entry.Tracker), strings.TrimSpace(entry.Status))
		}
	}
	if len(ready) == 0 {
		c.logger.Warnf("core: tracker dry-run found no ready tracker payloads for client injection for %s", meta.SourcePath)
		return nil
	}
	c.logger.Debugf("core: tracker dry-run injecting %d ready tracker torrent(s) for %s", len(ready), meta.SourcePath)

	for _, entry := range ready {
		trackerName := strings.ToUpper(strings.TrimSpace(entry.Tracker))
		torrentPath := trackerDryRunTorrentPath(entry)
		injectMeta, err := c.prepareDryRunInjectionMeta(meta, trackerName, torrentPath)
		if err != nil {
			return err
		}
		injectTorrent := api.TorrentResult{Path: strings.TrimSpace(injectMeta.TorrentPath), InfoHash: fallback.InfoHash, Tracker: trackerName}
		if injectTorrent.Path == "" {
			injectTorrent.Path = strings.TrimSpace(fallback.Path)
		}
		if injectTorrent.Path == "" {
			c.logger.Debugf("core: tracker dry-run skipping client injection for tracker=%s: no torrent file", trackerName)
			continue
		}
		if injectTorrent.Path == strings.TrimSpace(fallback.Path) {
			c.logger.Debugf("core: tracker dry-run injecting fallback torrent for tracker=%s path=%s", trackerName, injectTorrent.Path)
		} else {
			c.logger.Debugf("core: tracker dry-run injecting tracker torrent for tracker=%s path=%s", trackerName, injectTorrent.Path)
		}
		if err := c.injectPreparedTorrent(ctx, req, injectMeta, injectTorrent); err != nil {
			return err
		}
	}
	return nil
}

func (c *Core) prepareDryRunInjectionMeta(meta api.PreparedMetadata, trackerName string, torrentPath string) (api.PreparedMetadata, error) {
	injectMeta := meta
	if trimmed := strings.TrimSpace(torrentPath); trimmed != "" {
		injectMeta.TorrentPath = trimmed
	}
	trackerCfg := config.TrackerConfig{}
	for name, cfg := range c.cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), trackerName) {
			trackerCfg = cfg
			break
		}
	}
	prepared, err := trackers.PrepareDryRunInjectionTorrent(injectMeta, c.cfg.MainSettings.DBPath, trackerName, trackerCfg)
	if err != nil {
		return api.PreparedMetadata{}, fmt.Errorf("core: tracker dry-run injection torrent artifact tracker=%s: %w", trackerName, err)
	}
	return prepared, nil
}

func trackerDryRunTorrentPath(entry api.TrackerDryRunEntry) string {
	for _, file := range entry.Files {
		if strings.EqualFold(strings.TrimSpace(file.Field), "torrent") && file.Present {
			return strings.TrimSpace(file.Path)
		}
	}
	return ""
}

func annotateDryRunReleaseNames(meta api.PreparedMetadata, entries []api.TrackerDryRunEntry) {
	original := strings.TrimSpace(meta.ReleaseName)
	if original == "" {
		original = strings.TrimSpace(meta.ReleaseNameNoTag)
	}
	if original == "" {
		original = strings.TrimSpace(meta.Filename)
	}
	for idx := range entries {
		uploadName := strings.TrimSpace(entries[idx].ReleaseName)
		if uploadName == "" {
			uploadName = original
		}
		entries[idx].OriginalReleaseName = original
		entries[idx].UploadReleaseName = uploadName
		entries[idx].ReleaseNameChanged = original != "" && uploadName != "" && uploadName != original
		if entries[idx].ReleaseNameChanged && strings.TrimSpace(entries[idx].ReleaseNameChangeReason) == "" {
			entries[idx].ReleaseNameChangeReason = "tracker naming rules"
		}
	}
}

// FetchDescriptionBuilderPreview builds editable description groups from cached
// or freshly prepared metadata. When request trackers are provided, only that
// selected set contributes groups; selections that resolve empty return an empty
// preview instead of falling back to configured defaults.
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
		return api.DescriptionBuilderPreview{}, fmt.Errorf("core: %w", err)
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
		overrideByGroup[normalizeDescriptionBuilderGroupKey(override.GroupKey, nil)] = override
	}

	var meta api.PreparedMetadata
	storePreparedCache := false
	if req.Mode == api.ModeGUI {
		if cached, ok, err := c.resolveGUICachedPreparedMeta(ctx, req, uniquePaths[0]); err != nil {
			return api.DescriptionBuilderPreview{}, err
		} else if ok {
			c.logger.Debugf("core: description builder cache hit source=%s", uniquePaths[0])
			meta = applyRequestToPreparedMetaWithDerivedFields(cached, req, c.cfg, c.logger, false)
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
		singleReq.ExternalIDOverrides = mergeExternalIDOverrides(req.ExternalIDOverrides, resolveExternalIDSelection(req.ExternalIDSelections, uniquePaths[0]))
		meta, err = c.services.Metadata.Prepare(ctx, singleReq)
		if err != nil {
			return api.DescriptionBuilderPreview{}, fmt.Errorf("core: %w", err)
		}
		meta = applyRequestToPreparedMeta(meta, singleReq, c.cfg, c.logger)
		storePreparedCache = req.Mode == api.ModeGUI
	}
	resolvedTrackers, explicitEmpty := resolveTrackersPreservingExplicitEmpty(c.cfg, req.Trackers, meta.TrackersRemove, c.logger, false, false)
	if explicitEmpty {
		c.logger.Debugf("core: description builder explicit trackers resolved empty source=%s", meta.SourcePath)
		return api.DescriptionBuilderPreview{SourcePath: meta.SourcePath}, nil
	}
	if storePreparedCache {
		c.storeDupeCache(meta.SourcePath, overrideSignature(meta.ExternalIDOverrides, meta.ReleaseNameOverrides, meta.MetadataOverrides, meta.TrackerConfigOverrides, meta.TrackerSiteOverrides, meta.ClientOverrides, meta.TorrentOverrides, meta.ImageHostOverrides, meta.ScreenshotOverrides), meta)
	}
	meta, err = c.ensureDescriptionBuilderMetadata(ctx, req, uniquePaths[0], meta)
	if err != nil {
		return api.DescriptionBuilderPreview{}, err
	}

	prep, err := c.services.Trackers.BuildPreparation(ctx, meta, resolvedTrackers)
	if err != nil {
		c.logger.Errorf("core: description builder preparation failed source=%s: %v", meta.SourcePath, err)
		return api.DescriptionBuilderPreview{}, fmt.Errorf("core: %w", err)
	}

	preview := api.DescriptionBuilderPreview{SourcePath: meta.SourcePath}
	if len(prep.Descriptions) > 0 {
		preview.Groups = make([]api.DescriptionBuilderGroup, 0, len(prep.Descriptions))
		for _, entry := range prep.Descriptions {
			preview.Groups = append(preview.Groups, buildDescriptionBuilderGroup(entry, overrideByGroup, meta, c.logger))
		}
	}
	c.logger.Infof("core: description builder prepared source=%s groups=%d", preview.SourcePath, len(preview.Groups))
	return preview, nil
}

func buildDescriptionBuilderGroup(entry api.PreparationDescription, overrideByGroup map[string]api.DescriptionOverride, _ api.PreparedMetadata, _ api.Logger) api.DescriptionBuilderGroup {
	groupKey := normalizeDescriptionBuilderGroupKey(entry.GroupKey, entry.Trackers)
	descriptionText := strings.TrimSpace(entry.Description)
	if descriptionText == "" {
		descriptionText = strings.TrimSpace(entry.RawDescription)
	}
	descriptionHTML := entry.DescriptionHTML
	if strings.TrimSpace(descriptionHTML) == "" {
		descriptionHTML = description.Render(descriptionText)
	}
	rawDescription := descriptionText
	rawDescriptionHTML := descriptionHTML
	if strings.TrimSpace(rawDescription) == "" {
		rawDescription = strings.TrimSpace(entry.RawDescription)
		if strings.TrimSpace(rawDescription) != "" {
			rawDescriptionHTML = entry.RawDescriptionHTML
		} else {
			rawDescriptionHTML = ""
		}
	}
	hasOverride := entry.HasOverride
	if strings.TrimSpace(rawDescription) == "" {
		if override, ok := overrideByGroup[groupKey]; ok && strings.TrimSpace(override.Description) != "" {
			rawDescription = strings.TrimSpace(override.Description)
			rawDescriptionHTML = description.Render(rawDescription)
		}
	}
	if _, ok := overrideByGroup[groupKey]; ok && strings.TrimSpace(rawDescription) != "" {
		hasOverride = true
	}
	if strings.TrimSpace(rawDescriptionHTML) == "" {
		rawDescriptionHTML = description.Render(rawDescription)
	}
	return api.DescriptionBuilderGroup{
		GroupKey:           groupKey,
		Trackers:           append([]string{}, entry.Trackers...),
		Description:        rawDescription,
		DescriptionHTML:    rawDescriptionHTML,
		RawDescription:     rawDescription,
		RawDescriptionHTML: rawDescriptionHTML,
		HasOverride:        hasOverride,
		ImageHost:          entry.ImageHost,
	}
}

func (c *Core) ensureDescriptionBuilderMetadata(ctx context.Context, req api.Request, path string, meta api.PreparedMetadata) (api.PreparedMetadata, error) {
	if c.services.Metadata == nil || !descriptionBuilderNeedsExternalMetadata(c.cfg, meta) {
		return meta, nil
	}
	resolved, err := c.services.Metadata.ResolveExternalIDs(ctx, meta)
	if err != nil {
		return api.PreparedMetadata{}, fmt.Errorf("core: %w", err)
	}
	if req.Mode == api.ModeGUI && cacheableGUIPreparedMetaRequest(req) {
		overrides := mergeExternalIDOverrides(req.ExternalIDOverrides, resolveExternalIDSelection(req.ExternalIDSelections, path))
		signature := overrideSignature(overrides, req.ReleaseNameOverrides, req.MetadataOverrides, req.TrackerConfigOverrides, req.TrackerSiteOverrides, req.ClientOverrides, req.TorrentOverrides, req.ImageHostOverrides, req.ScreenshotOverrides)
		c.storeRefreshedDupeCache(path, signature, resolved)
	}
	return resolved, nil
}

func descriptionBuilderNeedsExternalMetadata(cfg config.Config, meta api.PreparedMetadata) bool {
	if strings.TrimSpace(meta.SourcePath) == "" {
		return false
	}
	if cfg.Description.AddLogo {
		if meta.ExternalMetadata.TMDB == nil || strings.TrimSpace(meta.ExternalMetadata.TMDB.Logo) == "" {
			return true
		}
	}
	return cfg.Description.EpisodeOverview && strings.TrimSpace(meta.EpisodeOverview) == "" && descriptionBuilderEpisodeLike(meta)
}

func descriptionBuilderEpisodeLike(meta api.PreparedMetadata) bool {
	if meta.SeasonInt > 0 || meta.EpisodeInt > 0 {
		return true
	}
	if strings.TrimSpace(meta.SeasonStr) != "" || strings.TrimSpace(meta.EpisodeStr) != "" || strings.TrimSpace(meta.DailyEpisodeDate) != "" {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(meta.Release.Category), "TV")
}

func normalizeDescriptionBuilderGroupKey(groupKey string, trackersList []string) string {
	normalized := strings.ToLower(strings.TrimSpace(groupKey))
	if normalized == "" && len(trackersList) > 0 {
		normalized = strings.ToLower(strings.TrimSpace(trackersList[0]))
	}
	return normalized
}

// FetchDescriptionBuilderGroupPreview rebuilds one description group from cached
// or freshly prepared metadata. Request trackers limit the rebuild to the
// selected set while tracker removals still suppress removed selections.
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
		return api.DescriptionBuilderGroup{}, fmt.Errorf("core: %w", err)
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
		overrideByGroup[normalizeDescriptionBuilderGroupKey(override.GroupKey, nil)] = override
	}

	var meta api.PreparedMetadata
	storePreparedCache := false
	if req.Mode == api.ModeGUI {
		if cached, ok, err := c.resolveGUICachedPreparedMeta(ctx, req, uniquePaths[0]); err != nil {
			return api.DescriptionBuilderGroup{}, err
		} else if ok {
			meta = applyRequestToPreparedMetaWithDerivedFields(cached, req, c.cfg, c.logger, false)
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
		singleReq.ExternalIDOverrides = mergeExternalIDOverrides(req.ExternalIDOverrides, resolveExternalIDSelection(req.ExternalIDSelections, uniquePaths[0]))
		meta, err = c.services.Metadata.Prepare(ctx, singleReq)
		if err != nil {
			return api.DescriptionBuilderGroup{}, fmt.Errorf("core: %w", err)
		}
		meta = applyRequestToPreparedMeta(meta, singleReq, c.cfg, c.logger)
		storePreparedCache = req.Mode == api.ModeGUI
	}
	resolvedTrackers, explicitEmpty := resolveTrackersPreservingExplicitEmpty(c.cfg, req.Trackers, meta.TrackersRemove, c.logger, false, false)
	if explicitEmpty {
		return api.DescriptionBuilderGroup{}, nil
	}
	if storePreparedCache {
		c.storeDupeCache(meta.SourcePath, overrideSignature(meta.ExternalIDOverrides, meta.ReleaseNameOverrides, meta.MetadataOverrides, meta.TrackerConfigOverrides, meta.TrackerSiteOverrides, meta.ClientOverrides, meta.TorrentOverrides, meta.ImageHostOverrides, meta.ScreenshotOverrides), meta)
	}
	meta, err = c.ensureDescriptionBuilderMetadata(ctx, req, uniquePaths[0], meta)
	if err != nil {
		return api.DescriptionBuilderGroup{}, err
	}

	prep, err := c.services.Trackers.BuildPreparation(ctx, meta, resolvedTrackers)
	if err != nil {
		return api.DescriptionBuilderGroup{}, fmt.Errorf("core: %w", err)
	}
	for _, entry := range prep.Descriptions {
		normalizedGroupKey := normalizeDescriptionBuilderGroupKey(entry.GroupKey, entry.Trackers)
		if strings.EqualFold(normalizedGroupKey, targetGroup) {
			group := buildDescriptionBuilderGroup(entry, overrideByGroup, meta, c.logger)
			if len(group.Trackers) == 0 && len(resolvedTrackers) > 0 {
				group.Trackers = append([]string{}, resolvedTrackers...)
			}
			return group, nil
		}
	}
	return api.DescriptionBuilderGroup{}, internalerrors.ErrNotFound
}

func (c *Core) SelectBlurayCandidate(ctx context.Context, sourcePath string, releaseID string) (api.MetadataPreview, error) {
	if err := ctx.Err(); err != nil {
		return api.MetadataPreview{}, fmt.Errorf("core: select blu-ray candidate canceled: %w", err)
	}
	if c.repo == nil {
		return api.MetadataPreview{}, errors.New("core: repository not configured")
	}
	trimmedPath := strings.TrimSpace(sourcePath)
	trimmedReleaseID := strings.TrimSpace(releaseID)
	if trimmedPath == "" || trimmedReleaseID == "" {
		return api.MetadataPreview{}, internalerrors.ErrInvalidInput
	}
	external, err := c.repo.GetExternalMetadata(ctx, trimmedPath)
	if err != nil {
		return api.MetadataPreview{}, fmt.Errorf("core: load blu-ray metadata: %w", err)
	}
	if external.Bluray == nil || !external.Bluray.SelectCandidate(trimmedReleaseID, false, "manual") {
		return api.MetadataPreview{}, internalerrors.ErrNotFound
	}
	external.UpdatedAt = time.Now().UTC()
	external.Bluray.UpdatedAt = external.UpdatedAt

	c.dupeMu.Lock()
	cachePath := trimmedPath
	entry, ok := c.dupeCache[cachePath]
	if !ok {
		cleanedPath := filepath.Clean(trimmedPath)
		for key, candidate := range c.dupeCache {
			if filepath.Clean(key) == cleanedPath {
				cachePath = key
				entry = candidate
				ok = true
				break
			}
		}
	}
	if ok {
		entry.meta.ExternalMetadata.Bluray = external.Bluray
		entry.meta.ExternalMetadata.UpdatedAt = external.UpdatedAt
		applyBlurayCandidateToPreparedMeta(&entry.meta)
	}
	c.dupeMu.Unlock()
	if !ok {
		return api.MetadataPreview{}, internalerrors.ErrNotFound
	}

	if c.services.Metadata != nil {
		if refreshed, refreshErr := c.services.Metadata.RefreshPreparedMetadata(ctx, entry.meta); refreshErr == nil {
			entry.meta = refreshed
		} else if c.logger != nil {
			c.logger.Warnf("core: refresh metadata after blu-ray selection failed: %v", refreshErr)
		}
	}

	if err := c.repo.SaveExternalMetadata(ctx, external); err != nil {
		return api.MetadataPreview{}, fmt.Errorf("core: save blu-ray selection: %w", err)
	}

	c.dupeMu.Lock()
	entry.updatedAt = time.Now().UTC()
	entry.requestRefreshed = true
	c.dupeCache[cachePath] = entry
	c.dupeMu.Unlock()

	return buildMetadataPreview(entry.meta, c.cfg), nil
}

func applyBlurayCandidateToPreparedMeta(meta *api.PreparedMetadata) {
	if meta == nil || meta.ExternalMetadata.Bluray == nil {
		return
	}
	candidate := meta.ExternalMetadata.Bluray.SelectedCandidate()
	if candidate == nil {
		return
	}
	if region := strings.TrimSpace(candidate.Region); region != "" {
		meta.Region = region
		meta.Release.Region = region
	}
	if publisher := strings.TrimSpace(candidate.Publisher); publisher != "" {
		meta.Distributor = strings.ToUpper(publisher)
	}
}

func (c *Core) storeDupeCache(path string, signature string, meta api.PreparedMetadata) {
	c.storeDupeCacheEntry(path, signature, meta, false)
}

func (c *Core) storeRefreshedDupeCache(path string, signature string, meta api.PreparedMetadata) {
	c.storeDupeCacheEntry(path, signature, meta, true)
}

func (c *Core) storeDupeCacheEntry(path string, signature string, meta api.PreparedMetadata, requestRefreshed bool) {
	if strings.TrimSpace(path) == "" {
		return
	}
	c.dupeMu.Lock()
	defer c.dupeMu.Unlock()
	c.dupeCache[path] = dupeCacheEntry{meta: meta, signature: signature, updatedAt: time.Now().UTC(), requestRefreshed: requestRefreshed}
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
	if meta.Options.SkipAutoTorrent {
		c.logger.Debugf("core: skip_auto_torrent enabled, ignoring stored tracker metadata for %s", meta.SourcePath)
		return false, nil
	}
	path := strings.TrimSpace(meta.SourcePath)
	if path == "" {
		return false, nil
	}
	select {
	case <-ctx.Done():
		return false, fmt.Errorf("context canceled: %w", ctx.Err())
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

func resetPreparedClientData(meta *api.PreparedMetadata, req api.Request) {
	if meta == nil {
		return
	}
	meta.FoundTrackerMatch = false
	meta.MatchedTrackers = nil
	meta.TrackersRemove = mergeTrackerRemovals(nil, req.TrackersRemove)
	meta.TorrentComments = nil
	meta.ClientTorrentPath = ""
	meta.PieceSizeConstraint = ""
	meta.FoundPreferredPiece = ""
	meta.InfoHash = cachedInfoHash(*meta)
	meta.TrackerIDs = cloneStringMap(req.TrackerIDOverrides)
	applyTorrentOverridesToPreparedMeta(meta)
	for idx := range meta.TrackerData {
		meta.TrackerData[idx].Matched = false
	}
}

func cachedInfoHash(meta api.PreparedMetadata) string {
	if value := strings.TrimSpace(meta.StoredInfoHash); value != "" {
		return value
	}
	for _, record := range meta.TrackerData {
		if value := strings.TrimSpace(record.InfoHash); value != "" {
			return value
		}
	}
	return ""
}

func markTrackerDataMatches(meta *api.PreparedMetadata, matchedTrackers []string) {
	if meta == nil || len(matchedTrackers) == 0 || len(meta.TrackerData) == 0 {
		return
	}
	for idx := range meta.TrackerData {
		if matchedTrackerForUpload(matchedTrackers, meta.TrackerData[idx].Tracker) {
			meta.TrackerData[idx].Matched = true
		}
	}
}

func applyPathedTorrentData(meta *api.PreparedMetadata, searchResult api.ClientSearchResult) bool {
	if meta == nil || !hasPathedTorrentData(searchResult) {
		return false
	}
	if infoHash := strings.TrimSpace(searchResult.InfoHash); infoHash != "" {
		meta.InfoHash = infoHash
	}
	if torrentPath := strings.TrimSpace(searchResult.TorrentPath); torrentPath != "" {
		meta.ClientTorrentPath = torrentPath
	}
	if len(searchResult.TrackerIDs) > 0 {
		meta.TrackerIDs = mergeTrackerIDOverrides(searchResult.TrackerIDs, meta.TrackerIDs)
	}
	if len(searchResult.TorrentComments) > 0 {
		meta.TorrentComments = append([]api.TorrentMatch{}, searchResult.TorrentComments...)
	}
	meta.PieceSizeConstraint = searchResult.PieceSizeConstraint
	meta.FoundPreferredPiece = searchResult.FoundPreferredPiece
	meta.StoredDataFresh = false
	return true
}

func hasPathedTorrentData(searchResult api.ClientSearchResult) bool {
	return strings.TrimSpace(searchResult.InfoHash) != "" ||
		strings.TrimSpace(searchResult.TorrentPath) != "" ||
		len(searchResult.TrackerIDs) > 0 ||
		len(searchResult.TorrentComments) > 0 ||
		strings.TrimSpace(searchResult.PieceSizeConstraint) != "" ||
		strings.TrimSpace(searchResult.FoundPreferredPiece) != ""
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

func (c *Core) getGUICachedMetaEntry(path string, signature string, overrides api.ExternalIDOverrides) (dupeCacheEntry, bool) {
	if strings.TrimSpace(signature) != "" {
		c.dupeMu.RLock()
		entry, ok := c.dupeCache[path]
		c.dupeMu.RUnlock()
		if ok && entry.signature == signature {
			return entry, true
		}
	}
	if hasExternalIDOverrides(overrides) {
		return dupeCacheEntry{}, false
	}
	c.dupeMu.RLock()
	defer c.dupeMu.RUnlock()
	entry, ok := c.dupeCache[path]
	if !ok || hasExternalIDOverrides(entry.meta.ExternalIDOverrides) {
		return dupeCacheEntry{}, false
	}
	return entry, true
}

func (c *Core) getGUICachedMeta(path string, signature string, overrides api.ExternalIDOverrides) (api.PreparedMetadata, bool) {
	entry, ok := c.getGUICachedMetaEntry(path, signature, overrides)
	if !ok {
		return api.PreparedMetadata{}, false
	}
	return entry.meta, true
}

func (c *Core) lookupGUICachedMetaEntry(req api.Request, path string) (dupeCacheEntry, string, bool) {
	mergedOverrides := mergeExternalIDOverrides(req.ExternalIDOverrides, resolveExternalIDSelection(req.ExternalIDSelections, path))
	signature := overrideSignature(mergedOverrides, req.ReleaseNameOverrides, req.MetadataOverrides, req.TrackerConfigOverrides, req.TrackerSiteOverrides, req.ClientOverrides, req.TorrentOverrides, req.ImageHostOverrides, req.ScreenshotOverrides)
	entry, ok := c.getGUICachedMetaEntry(path, signature, mergedOverrides)
	return entry, signature, ok
}

func (c *Core) lookupGUICachedMeta(req api.Request, path string) (api.PreparedMetadata, bool) {
	entry, _, ok := c.lookupGUICachedMetaEntry(req, path)
	if !ok {
		return api.PreparedMetadata{}, false
	}
	return entry.meta, true
}

func (c *Core) resolveGUICachedPreparedMeta(ctx context.Context, req api.Request, path string) (api.PreparedMetadata, bool, error) {
	entry, signature, ok := c.lookupGUICachedMetaEntry(req, path)
	if !ok {
		return api.PreparedMetadata{}, false, nil
	}
	if entry.requestRefreshed && cachedPreparedMetaMatchesRequest(entry.meta, req, path) {
		return deepCopyPreparedMetadata(entry.meta), true, nil
	}
	resolved, err := c.applyRequestToCachedPreparedMeta(ctx, entry.meta, req)
	if err != nil {
		return api.PreparedMetadata{}, false, err
	}
	if cacheableGUIPreparedMetaRequest(req) {
		c.storeRefreshedDupeCache(path, signature, resolved)
	}
	return resolved, true, nil
}

func cachedPreparedMetaMatchesRequest(meta api.PreparedMetadata, req api.Request, path string) bool {
	if strings.TrimSpace(meta.SourcePath) != strings.TrimSpace(path) {
		return false
	}
	mergedOverrides := mergeExternalIDOverrides(req.ExternalIDOverrides, resolveExternalIDSelection(req.ExternalIDSelections, path))
	return meta.Mode == req.Mode &&
		reflect.DeepEqual(meta.Options, req.Options) &&
		requestedTrackersApplied(meta.Trackers, req.Trackers) &&
		requestedTrackersRemoveApplied(meta.TrackersRemove, req.TrackersRemove) &&
		requestedTrackerIDsApplied(meta.TrackerIDs, req.TrackerIDOverrides) &&
		strings.TrimSpace(meta.DescriptionOverride) == strings.TrimSpace(req.DescriptionOverrideRaw) &&
		reflect.DeepEqual(meta.MetadataOverrides, req.MetadataOverrides) &&
		reflect.DeepEqual(meta.TrackerConfigOverrides, req.TrackerConfigOverrides) &&
		reflect.DeepEqual(meta.TrackerSiteOverrides, req.TrackerSiteOverrides) &&
		reflect.DeepEqual(meta.ClientOverrides, req.ClientOverrides) &&
		reflect.DeepEqual(meta.ImageHostOverrides, req.ImageHostOverrides) &&
		reflect.DeepEqual(meta.ScreenshotOverrides, req.ScreenshotOverrides) &&
		reflect.DeepEqual(meta.TorrentOverrides, req.TorrentOverrides) &&
		meta.IgnoreTrackerRuleFailures == req.IgnoreTrackerRuleFailures &&
		len(req.IgnoreDupesFor) == 0 &&
		reflect.DeepEqual(meta.ExternalIDOverrides, mergedOverrides) &&
		reflect.DeepEqual(meta.ReleaseNameOverrides, req.ReleaseNameOverrides) &&
		sameQuestionnaireAnswers(meta.TrackerQuestionnaireAnswers, req.TrackerQuestionnaireAnswers) &&
		sameDescriptionGroups(meta.DescriptionGroups, req.DescriptionGroups)
}

func requestedTrackersApplied(metaTrackers []string, requested []string) bool {
	if len(requested) == 0 {
		return true
	}
	return sameTrackerSet(metaTrackers, requested)
}

func sameTrackerSet(left []string, right []string) bool {
	leftCounts, leftTotal := trackerNameCounts(left)
	rightCounts, rightTotal := trackerNameCounts(right)
	if leftTotal != rightTotal {
		return false
	}
	if len(leftCounts) != len(rightCounts) {
		return false
	}
	for name, count := range leftCounts {
		if rightCounts[name] != count {
			return false
		}
	}
	return true
}

func trackerNameCounts(trackers []string) (map[string]int, int) {
	counts := make(map[string]int, len(trackers))
	total := 0
	for _, tracker := range trackers {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name == "" {
			continue
		}
		counts[name]++
		total++
	}
	return counts, total
}

// explicitTrackerSelectionResolvedEmpty reports whether a non-empty requested
// tracker set was fully removed by tracker resolution.
func explicitTrackerSelectionResolvedEmpty(requested []string, resolved []string) bool {
	return len(requested) > 0 && len(resolved) == 0
}

// resolveTrackersPreservingExplicitEmpty resolves requested trackers while
// preserving the difference between an omitted selection and an explicit
// selection that was fully removed.
//
// includeDefaults adds configured defaults to non-empty explicit selections.
// fallbackDefaultsWhenExplicitEmpty controls whether a removed explicit
// selection may fall back to defaults instead of returning explicitEmpty.
func resolveTrackersPreservingExplicitEmpty(
	cfg config.Config,
	requested []string,
	remove []string,
	logger api.Logger,
	includeDefaults bool,
	fallbackDefaultsWhenExplicitEmpty bool,
) ([]string, bool) {
	resolved := trackers.ResolveTrackers(cfg, requested, remove, logger)
	if explicitTrackerSelectionResolvedEmpty(requested, resolved) {
		if includeDefaults && fallbackDefaultsWhenExplicitEmpty {
			defaults := trackers.ResolveTrackers(cfg, nil, remove, logger)
			if len(defaults) > 0 {
				return defaults, false
			}
		}
		return nil, true
	}
	if includeDefaults && len(requested) > 0 {
		return trackers.ResolveTrackersWithDefaults(cfg, requested, remove, logger), false
	}
	return resolved, false
}

func requestedTrackersRemoveApplied(metaTrackersRemove []string, requested []string) bool {
	if len(requested) == 0 {
		return true
	}
	seen := make(map[string]struct{}, len(metaTrackersRemove))
	for _, tracker := range metaTrackersRemove {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name != "" {
			seen[name] = struct{}{}
		}
	}
	for _, tracker := range requested {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name == "" {
			continue
		}
		if _, ok := seen[name]; !ok {
			return false
		}
	}
	return true
}

func requestedTrackerIDsApplied(metaTrackerIDs map[string]string, requested map[string]string) bool {
	if len(requested) == 0 {
		return true
	}
	for tracker, id := range requested {
		name := strings.ToLower(strings.TrimSpace(tracker))
		if name == "" {
			continue
		}
		got := ""
		for metaTracker, metaID := range metaTrackerIDs {
			if strings.EqualFold(strings.TrimSpace(metaTracker), name) {
				got = metaID
				break
			}
		}
		if strings.TrimSpace(got) != strings.TrimSpace(id) {
			return false
		}
	}
	return true
}

func sameQuestionnaireAnswers(left map[string]map[string]string, right map[string]map[string]string) bool {
	if len(left) == 0 && len(right) == 0 {
		return true
	}
	return reflect.DeepEqual(left, right)
}

func sameDescriptionGroups(left []api.DescriptionBuilderGroup, right []api.DescriptionBuilderGroup) bool {
	if len(left) == 0 && len(right) == 0 {
		return true
	}
	return reflect.DeepEqual(left, right)
}

func cacheableGUIPreparedMetaRequest(req api.Request) bool {
	return !req.SkipDupeCheck &&
		len(req.IgnoreDupesFor) == 0 &&
		len(req.IgnoreTrackerRuleFailuresFor) == 0 &&
		strings.TrimSpace(req.DescriptionOverrideRaw) == "" &&
		len(req.DescriptionGroups) == 0
}

// ExportGUICachedPreparedMeta exposes the resolved GUI prepared metadata cache entry
// so callers can hand off metadata to isolated per-run cores.
func (c *Core) ExportGUICachedPreparedMeta(ctx context.Context, req api.Request) (api.PreparedMetadata, bool, error) {
	if err := ctx.Err(); err != nil {
		return api.PreparedMetadata{}, false, fmt.Errorf("core: export cached prepared metadata canceled: %w", err)
	}
	path, err := c.resolveSinglePreparedMetaPath(ctx, req.Paths)
	if err != nil {
		return api.PreparedMetadata{}, false, err
	}
	meta, ok := c.lookupGUICachedMeta(req, path)
	if !ok {
		c.logger.Debugf("core: gui prepared metadata export miss path=%s", path)
		return api.PreparedMetadata{}, false, nil
	}
	c.logger.Debugf("core: gui prepared metadata export hit path=%s meta_no_seed=%t", path, meta.Options.NoSeed)
	return deepCopyPreparedMetadata(meta), true, nil
}

// ImportPreparedMetadataForGUI stores prepared metadata on a per-run core so GUI dry-run
// and upload-only flows can reuse metadata prepared on the long-lived GUI core.
func (c *Core) ImportPreparedMetadataForGUI(ctx context.Context, req api.Request, meta api.PreparedMetadata) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("core: import prepared metadata canceled: %w", err)
	}
	path, err := c.resolveSinglePreparedMetaPath(ctx, req.Paths)
	if err != nil {
		return err
	}
	overrides := mergeExternalIDOverrides(req.ExternalIDOverrides, resolveExternalIDSelection(req.ExternalIDSelections, path))
	signature := overrideSignature(overrides, req.ReleaseNameOverrides, req.MetadataOverrides, req.TrackerConfigOverrides, req.TrackerSiteOverrides, req.ClientOverrides, req.TorrentOverrides, req.ImageHostOverrides, req.ScreenshotOverrides)
	c.storeDupeCache(path, signature, deepCopyPreparedMetadata(meta))
	c.logger.Debugf("core: gui prepared metadata import stored path=%s request_no_seed=%t meta_no_seed=%t", path, req.Options.NoSeed, meta.Options.NoSeed)
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
		return "", fmt.Errorf("core: %w", err)
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
	copyMeta.DescriptionGroups = api.CloneDescriptionBuilderGroups(meta.DescriptionGroups)
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
	copyMeta.CrossSeedTorrents = append([]api.UploadedTorrent(nil), meta.CrossSeedTorrents...)
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
		maps.Copy(inner, values)
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
		Bluray:     deepCopyBlurayMetadata(metadata.Bluray),
		UpdatedAt:  metadata.UpdatedAt,
	}
}

func deepCopyBlurayMetadata(metadata *api.BlurayMetadata) *api.BlurayMetadata {
	if metadata == nil {
		return nil
	}
	cloned := *metadata
	cloned.Candidates = make([]api.BlurayReleaseCandidate, len(metadata.Candidates))
	for idx, candidate := range metadata.Candidates {
		cloned.Candidates[idx] = candidate
		cloned.Candidates[idx].Warnings = append([]string(nil), candidate.Warnings...)
		cloned.Candidates[idx].MatchNotes = append([]string(nil), candidate.MatchNotes...)
		cloned.Candidates[idx].Specs.Audio = append([]string(nil), candidate.Specs.Audio...)
		cloned.Candidates[idx].Specs.Subtitles = append([]string(nil), candidate.Specs.Subtitles...)
		cloned.Candidates[idx].CoverImages = append([]api.BlurayImage(nil), candidate.CoverImages...)
	}
	return &cloned
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

func deepCopyStringInterfaceMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = deepCopyInterfaceValue(value)
	}
	return cloned
}

func deepCopyInterfaceValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return deepCopyStringInterfaceMap(typed)
	case []any:
		cloned := make([]any, len(typed))
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
		return nil, fmt.Errorf("core: discover playlists canceled: %w", err)
	}
	if strings.TrimSpace(sourcePath) == "" {
		return nil, internalerrors.ErrInvalidInput
	}

	c.logger.Debugf("core: discovering playlists in %q", sourcePath)

	playlists, err := filesystem.DiscoverPlaylists(ctx, sourcePath)
	if err != nil {
		c.logger.Warnf("core: discover playlists failed: %v", err)
		return nil, fmt.Errorf("core: %w", err)
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
		return fmt.Errorf("core: save playlist selection canceled: %w", err)
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
		return fmt.Errorf("core: %w", err)
	}

	c.logger.Infof("core: playlist selection saved for %q", normalizedPath)
	return nil
}

func (c *Core) LoadPlaylistSelection(ctx context.Context, sourcePath string) (api.PlaylistSelection, error) {
	if err := ctx.Err(); err != nil {
		return api.PlaylistSelection{}, fmt.Errorf("core: load playlist selection canceled: %w", err)
	}
	if strings.TrimSpace(sourcePath) == "" {
		return api.PlaylistSelection{}, internalerrors.ErrInvalidInput
	}
	if c.repo == nil {
		return api.PlaylistSelection{}, errors.New("core: repository not initialized")
	}

	normalizedPath := filepath.ToSlash(filepath.Clean(sourcePath))
	c.logger.Debugf("core: loading playlist selection for %q (normalized: %q)", sourcePath, normalizedPath)

	selection, err := c.repo.GetPlaylistSelection(ctx, normalizedPath)
	if err != nil {
		if errors.Is(err, internalerrors.ErrNotFound) {
			c.logger.Debugf("core: no playlist selection found for %q", sourcePath)
			return api.PlaylistSelection{}, internalerrors.ErrNotFound
		}
		c.logger.Warnf("core: load playlist selection failed: %v", err)
		return api.PlaylistSelection{}, fmt.Errorf("core: %w", err)
	}

	c.logger.Debugf("core: loaded playlist selection for %q: %d playlists, useAll=%v", sourcePath, len(selection.SelectedPlaylists), selection.UseAll)
	return selection, nil
}

func (c *Core) ListHistory(ctx context.Context) ([]api.HistoryEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("core: list history canceled: %w", err)
	}
	if c.repo == nil {
		return nil, errors.New("core: repository not initialized")
	}

	entries, err := c.repo.ListHistoryEntries(ctx)
	if err != nil {
		return nil, fmt.Errorf("core: %w", err)
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
		return api.HistoryOverview{}, fmt.Errorf("core: get history overview canceled: %w", err)
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
		return api.HistoryOverview{}, fmt.Errorf("core: %w", err)
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
		return api.HistoryOverview{}, fmt.Errorf("core: %w", err)
	}

	externalMetadata, err := c.repo.GetExternalMetadata(ctx, trimmed)
	if err == nil {
		overview.ExternalMetadata = externalMetadata
	} else if !errors.Is(err, internalerrors.ErrNotFound) {
		return api.HistoryOverview{}, fmt.Errorf("core: %w", err)
	}

	releaseOverrides, err := c.repo.GetReleaseNameOverrides(ctx, trimmed)
	if err == nil {
		overview.ReleaseNameOverrides = releaseOverrides
	} else if !errors.Is(err, internalerrors.ErrNotFound) {
		return api.HistoryOverview{}, fmt.Errorf("core: %w", err)
	}

	descriptionOverrides, err := c.repo.ListDescriptionOverridesByPath(ctx, trimmed)
	if err == nil {
		overview.DescriptionOverrides = append([]api.DescriptionOverride(nil), descriptionOverrides...)
		overview.DescriptionOverride = preferredHistoryDescriptionOverride(descriptionOverrides)
	} else if !errors.Is(err, internalerrors.ErrNotFound) {
		return api.HistoryOverview{}, fmt.Errorf("core: %w", err)
	}

	playlistSelection, err := c.repo.GetPlaylistSelection(ctx, trimmed)
	if err == nil {
		overview.PlaylistSelection = playlistSelection
	} else if !errors.Is(err, internalerrors.ErrNotFound) {
		return api.HistoryOverview{}, fmt.Errorf("core: %w", err)
	}

	trackerMetadata, err := c.repo.ListTrackerMetadataByPath(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, fmt.Errorf("core: %w", err)
	}
	overview.TrackerMetadata = trackerMetadata

	ruleFailures, err := c.repo.ListTrackerRuleFailuresByPath(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, fmt.Errorf("core: %w", err)
	}
	overview.TrackerRuleFailures = ruleFailures

	screenshots, err := c.repo.ListScreenshotsByPath(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, fmt.Errorf("core: %w", err)
	}
	overview.Screenshots = screenshots

	finalSelections, err := c.repo.ListFinalSelections(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, fmt.Errorf("core: %w", err)
	}
	overview.FinalSelections = finalSelections

	uploadedImages, err := c.repo.ListUploadedImagesByPath(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, fmt.Errorf("core: %w", err)
	}
	overview.UploadedImages = uploadedImages

	uploadHistory, err := c.repo.ListUploadHistoryByPath(ctx, trimmed)
	if err != nil {
		return api.HistoryOverview{}, fmt.Errorf("core: %w", err)
	}
	overview.UploadHistory = uploadHistory
	if len(uploadHistory) > 0 {
		overview.LatestUploadStatus = uploadHistory[0].Status
		overview.LatestUploadAt = uploadHistory[0].CreatedAt
	}
	overview.StatusLabel = historyStatusLabel(overview.LatestUploadStatus, len(ruleFailures))

	return overview, nil
}

func preferredHistoryDescriptionOverride(overrides []api.DescriptionOverride) api.DescriptionOverride {
	if len(overrides) == 0 {
		return api.DescriptionOverride{}
	}
	for _, override := range overrides {
		if strings.TrimSpace(override.GroupKey) == "" {
			return override
		}
	}
	for _, override := range overrides {
		if strings.TrimSpace(override.Description) != "" {
			return override
		}
	}
	return overrides[0]
}

func (c *Core) DeleteHistoryRelease(ctx context.Context, sourcePath string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("core: delete history release canceled: %w", err)
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
	if c == nil || c.repo == nil || !c.ownsRepo {
		return nil
	}
	closer, ok := c.repo.(interface{ Close() error })
	if !ok {
		return nil
	}
	return wrapCoreError(closer.Close())
}

func (c *Core) RenderDescription(ctx context.Context, raw string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("core: render description canceled: %w", err)
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
		return api.DescriptionBuilderGroup{}, fmt.Errorf("core: %w", err)
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
			return api.DescriptionBuilderGroup{}, fmt.Errorf("core: %w", err)
		}
		req.Paths = []string{uniquePaths[0]}
		group, err := c.FetchDescriptionBuilderGroupPreview(ctx, req)
		if err == nil {
			return group, nil
		}
		if errors.Is(err, internalerrors.ErrNotFound) {
			return api.DescriptionBuilderGroup{
				GroupKey:    normalizeDescriptionBuilderGroupKey(groupKey, req.Trackers),
				Trackers:    append([]string{}, req.Trackers...),
				HasOverride: false,
			}, nil
		}
		return api.DescriptionBuilderGroup{}, err
	}

	if err := c.repo.SaveDescriptionOverride(ctx, api.DescriptionOverride{
		SourcePath:  uniquePaths[0],
		GroupKey:    groupKey,
		Description: trimmed,
		UpdatedAt:   time.Now().UTC(),
	}); err != nil {
		return api.DescriptionBuilderGroup{}, fmt.Errorf("core: %w", err)
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
		Bluray:               deepCopyBlurayMetadata(meta.ExternalMetadata.Bluray),
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
			Category:        string(record.Category),
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

// requestPreparedMetaTrackersRemove returns the removal set that should be used
// for pre-application explicit-empty checks. Request dupe bypasses are applied
// first so ignored or skipped matched trackers can still resolve when selected.
func requestPreparedMetaTrackersRemove(meta api.PreparedMetadata, req api.Request) []string {
	metaRemove, matched := duplicateTrackerStateForRequest(meta, req)
	remove := mergeTrackerRemovals(req.TrackersRemove, metaRemove)
	return mergeTrackerRemovals(remove, matched)
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

// resolveCanonicalDescriptionGroups returns request or cached description groups
// before rebuilding groups from tracker preparation for the selected tracker set.
// Explicit tracker selections constrain the rebuild and do not fall back to
// configured defaults when they resolve empty.
func (c *Core) resolveCanonicalDescriptionGroups(ctx context.Context, meta api.PreparedMetadata, req api.Request) ([]api.DescriptionBuilderGroup, error) {
	if len(req.DescriptionGroups) > 0 {
		return api.CloneDescriptionBuilderGroups(req.DescriptionGroups), nil
	}
	if len(meta.DescriptionGroups) > 0 {
		return api.CloneDescriptionBuilderGroups(meta.DescriptionGroups), nil
	}
	if c.services.Trackers == nil {
		return nil, errors.New("core: tracker service not configured")
	}

	resolvedTrackers, explicitEmpty := resolveTrackersPreservingExplicitEmpty(c.cfg, req.Trackers, meta.TrackersRemove, c.logger, false, false)
	if explicitEmpty {
		return nil, nil
	}
	prep, err := c.services.Trackers.BuildPreparation(ctx, meta, resolvedTrackers)
	if err != nil {
		return nil, fmt.Errorf("core: %w", err)
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
			overrideByGroup[normalizeDescriptionBuilderGroupKey(override.GroupKey, nil)] = override
		}
	}

	groups := make([]api.DescriptionBuilderGroup, 0, len(prep.Descriptions))
	for _, entry := range prep.Descriptions {
		groups = append(groups, buildDescriptionBuilderGroup(entry, overrideByGroup, meta, c.logger))
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

// migrateLegacyCookies performs automatic migration of cookies from file-based storage
// to the encrypted database. This is called during core initialization if needed.
func migrateLegacyCookies(ctx context.Context, sqliteDB *sql.DB, dbPath string, logger api.Logger) error {
	if sqliteDB == nil {
		return errors.New("database connection is required for cookie migration")
	}

	if err := cookies.SyncCookieEncryptionWithAuth(ctx, sqliteDB, dbPath); err != nil {
		if errors.Is(err, cookies.ErrAuthHelperUnavailable) {
			logger.Debugf("core: cookie encryption sync skipped: web auth helper unavailable")
		} else {
			return fmt.Errorf("cookies encryption sync: %w", err)
		}
	}

	cookiesDir, err := db.CookiePath(dbPath, "")
	if err != nil {
		logger.Debugf("core: failed to resolve cookies directory: %v", err)
		return nil // Non-fatal: directory path resolution failed
	}

	if err := cookies.EnsureCookieMigration(ctx, sqliteDB, dbPath, cookiesDir, logger); err != nil {
		if errors.Is(err, cookies.ErrAuthHelperUnavailable) {
			logger.Debugf("core: cookie migration skipped: web auth helper unavailable")
			return nil
		}
		return fmt.Errorf("cookies migration: %w", err)
	}

	return nil
}
