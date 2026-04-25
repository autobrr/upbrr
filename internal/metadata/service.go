// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/metadata/discparse"
	"github.com/autobrr/upbrr/internal/metadata/mediainfo"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/metadata/seasonep"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/services/bdinfo"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackerdata"
	"github.com/autobrr/upbrr/pkg/api"
)

type Service struct {
	repo     db.MetadataRepository
	tagsPath string
	scene    SceneDetector
	mi       mediainfo.Exporter
	bdinfo   *bdinfo.Service
	logger   api.Logger
	cacheDir string
	nfoDir   string
	cfg      config.Config
	tmdb     TMDBClient
	imdb     IMDBClient
	tvdb     TVDBClient
	tvmaze   TVmazeClient
	sonarr   ArrLookupClient
	radarr   ArrLookupClient
	tracker  TrackerDataLookup
}

type cachedBDMVSummary struct {
	Playlist    string
	Summary     string
	ExtSummary  string
	SummaryPath string
	ExtPath     string
}

type bdmvSummaryCache struct {
	Entries map[string]cachedBDMVSummary
}

var bdmvSummaryPlaylistPattern = regexp.MustCompile(`(?mi)^Playlist:\s*(.+?)\s*$`)

var (
	discoverBDMVPlaylists = filesystem.DiscoverPlaylists
	parseBDMVPlaylist     = filesystem.ParseMPLS
	executePlaylistBDInfo = func(svc *bdinfo.Service, ctx context.Context, bdmvPath string, playlistFile string, outputPath string) (string, error) {
		return svc.ExecuteForPlaylist(ctx, bdmvPath, playlistFile, outputPath)
	}
	executeFullBDInfoScan = func(svc *bdinfo.Service, ctx context.Context, bdmvPath string, outputDir string) (bdinfo.ScanResult, error) {
		return svc.ExecuteFullScan(ctx, bdmvPath, outputDir)
	}
	parseBDInfoOutput = func(svc *bdinfo.Service, filePath string) (map[string]interface{}, error) {
		return svc.ParseOutput(filePath)
	}
)

type TrackerDataLookup interface {
	Lookup(
		ctx context.Context,
		tracker string,
		trackerID string,
		meta api.PreparedMetadata,
		searchFileName string,
		onlyID bool,
		keepImages bool,
	) (trackerdata.Result, error)
}

type ArrLookupClient interface {
	Lookup(ctx context.Context, meta api.PreparedMetadata) (ArrLookupResult, error)
}

type Option func(*Service)

func WithTagsPathFromDB(dbPath string) Option {
	return func(s *Service) {
		s.tagsPath = resolveTagsPath(dbPath)
	}
}

func WithSceneDetector(detector SceneDetector) Option {
	return func(s *Service) {
		s.scene = detector
	}
}

func WithLogger(logger api.Logger) Option {
	return func(s *Service) {
		s.logger = logger
	}
}

func WithMediaInfoExporter(exporter mediainfo.Exporter) Option {
	return func(s *Service) {
		s.mi = exporter
	}
}

func WithConfig(cfg config.Config) Option {
	return func(s *Service) {
		s.cfg = cfg
	}
}

func WithTMDBClient(client TMDBClient) Option {
	return func(s *Service) {
		s.tmdb = client
	}
}

func WithIMDBClient(client IMDBClient) Option {
	return func(s *Service) {
		s.imdb = client
	}
}

func WithTVDBClient(client TVDBClient) Option {
	return func(s *Service) {
		s.tvdb = client
	}
}

func WithTVmazeClient(client TVmazeClient) Option {
	return func(s *Service) {
		s.tvmaze = client
	}
}

func WithSonarrClient(client ArrLookupClient) Option {
	return func(s *Service) {
		s.sonarr = client
	}
}

func WithRadarrClient(client ArrLookupClient) Option {
	return func(s *Service) {
		s.radarr = client
	}
}

func WithBDInfoService(bi *bdinfo.Service) Option {
	return func(s *Service) {
		s.bdinfo = bi
	}
}

func WithTrackerDataLookup(lookup TrackerDataLookup) Option {
	return func(s *Service) {
		s.tracker = lookup
	}
}

func WithSRRDBPaths(dbPath string) Option {
	return func(s *Service) {
		cacheDir, nfoDir := resolveSRRDBPaths(dbPath)
		s.cacheDir = cacheDir
		s.nfoDir = nfoDir
	}
}

func NewService(repo db.MetadataRepository, opts ...Option) *Service {
	service := &Service{repo: repo, logger: api.NopLogger{}}
	for _, opt := range opts {
		if opt != nil {
			opt(service)
		}
	}
	if service.logger == nil {
		service.logger = api.NopLogger{}
	}
	if service.mi == nil {
		service.mi = mediainfo.NewService(service.logger, nil)
	}
	if service.tagsPath == "" {
		service.tagsPath = resolveTagsPath("")
	}
	if service.cacheDir == "" || service.nfoDir == "" {
		cacheDir, nfoDir := resolveSRRDBPaths("")
		service.cacheDir = cacheDir
		service.nfoDir = nfoDir
	}
	if service.scene == nil {
		service.scene = newSRRDBDetector(nil, "", service.cacheDir, service.nfoDir)
	}
	if service.tracker == nil {
		service.tracker = trackerdata.NewClient(service.cfg, service.logger, nil)
	}
	return service
}

func resolveTagsPath(dbPath string) string {
	root, err := db.RootDir(dbPath)
	if err != nil {
		return ""
	}
	return filepath.Join(root, "data", "tags.json")
}

func resolveSRRDBPaths(dbPath string) (string, string) {
	cacheRoot, err := db.Subdir(dbPath, "cache")
	if err != nil {
		return "", ""
	}
	nfoRoot, err := db.Subdir(dbPath, "nfo")
	if err != nil {
		return cacheRoot, ""
	}
	cacheDir := filepath.Join(cacheRoot, "srrdb")
	_ = os.MkdirAll(cacheDir, 0o700)
	return cacheDir, nfoRoot
}

func cloneTrackerIDs(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		normalizedKey := strings.ToLower(strings.TrimSpace(key))
		normalizedValue := strings.TrimSpace(value)
		if normalizedKey == "" || normalizedValue == "" {
			continue
		}
		cloned[normalizedKey] = normalizedValue
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}

func applyTorrentOverridesToPreparedMeta(meta *api.PreparedMetadata) {
	if meta == nil {
		return
	}

	if meta.TorrentOverrides.InfoHash != nil {
		meta.InfoHash = strings.ToLower(strings.TrimSpace(*meta.TorrentOverrides.InfoHash))
	}
}

func (s *Service) Prepare(ctx context.Context, req api.Request) (api.PreparedMetadata, error) {
	select {
	case <-ctx.Done():
		return api.PreparedMetadata{}, ctx.Err()
	default:
	}

	s.logger.Debugf("metadata: preparing metadata for %d paths", len(req.Paths))

	if len(req.Paths) == 0 {
		return api.PreparedMetadata{}, internalerrors.ErrInvalidInput
	}
	if s.repo == nil {
		return api.PreparedMetadata{}, errors.New("metadata: repository not configured")
	}

	primary := strings.TrimSpace(req.Paths[0])
	if primary == "" {
		return api.PreparedMetadata{}, fmt.Errorf("metadata: empty primary path: %w", internalerrors.ErrInvalidInput)
	}

	absPath, err := filepath.Abs(primary)
	if err != nil {
		return api.PreparedMetadata{}, fmt.Errorf("metadata: resolve path: %w", err)
	}
	primary = absPath
	s.logger.Debugf("metadata: primary path resolved to %s", primary)

	storedOverrides := api.ReleaseNameOverrides{}
	if stored, err := s.repo.GetReleaseNameOverrides(ctx, primary); err == nil {
		storedOverrides = stored
	} else if !errors.Is(err, internalerrors.ErrNotFound) {
		return api.PreparedMetadata{}, fmt.Errorf("metadata: release overrides lookup: %w", err)
	}
	mergedOverrides := mergeReleaseNameOverrides(storedOverrides, req.ReleaseNameOverrides)
	if hasReleaseNameOverrides(req.ReleaseNameOverrides) {
		if err := s.repo.SaveReleaseNameOverrides(ctx, primary, mergedOverrides); err != nil {
			return api.PreparedMetadata{}, fmt.Errorf("metadata: release overrides persist: %w", err)
		}
	}

	normalizedPaths := make([]string, 0, len(req.Paths))
	seenPaths := make(map[string]struct{}, len(req.Paths))
	for _, value := range req.Paths {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return api.PreparedMetadata{}, fmt.Errorf("metadata: empty path: %w", internalerrors.ErrInvalidInput)
		}
		abs, err := filepath.Abs(trimmed)
		if err != nil {
			return api.PreparedMetadata{}, fmt.Errorf("metadata: resolve path: %w", err)
		}
		if _, exists := seenPaths[abs]; exists {
			continue
		}
		seenPaths[abs] = struct{}{}
		normalizedPaths = append(normalizedPaths, abs)
		s.logger.Tracef("metadata: normalized path %s", abs)
	}

	meta := api.PreparedMetadata{
		SourcePath:             primary,
		SourceLookupURL:        strings.TrimSpace(req.SourceLookupURL),
		Paths:                  normalizedPaths,
		Mode:                   req.Mode,
		Trackers:               append([]string{}, req.Trackers...),
		Options:                req.Options,
		TrackersRemove:         append([]string{}, req.TrackersRemove...),
		TrackerIDs:             cloneTrackerIDs(req.TrackerIDOverrides),
		DescriptionOverride:    strings.TrimSpace(req.DescriptionOverrideRaw),
		MetadataOverrides:      req.MetadataOverrides,
		TrackerConfigOverrides: req.TrackerConfigOverrides,
		TrackerSiteOverrides:   req.TrackerSiteOverrides,
		ClientOverrides:        req.ClientOverrides,
		ImageHostOverrides:     req.ImageHostOverrides,
		ScreenshotOverrides:    normalizeScreenshotOverrides(req.ScreenshotOverrides),
		TorrentOverrides:       req.TorrentOverrides,
		ExternalIDOverrides:    req.ExternalIDOverrides,
		ReleaseNameOverrides:   mergedOverrides,
	}
	applyTorrentOverridesToPreparedMeta(&meta)
	applySourceLookupOverride(&meta)
	release := ParseReleaseInfo(primary)
	meta.Release = release

	discType, err := filesystem.DetectDiscType(ctx, primary)
	if err != nil {
		return api.PreparedMetadata{}, fmt.Errorf("metadata: detect disc: %w", err)
	}
	meta.DiscType = discType
	if discType != "" {
		s.logger.Debugf("metadata: detected disc type %s", discType)
	}

	// For BDMV discs, check if a playlist was selected
	if discType == "BDMV" && s.repo != nil {
		var sel db.PlaylistSelection
		var playlistPath string
		var found bool

		s.logger.Debugf("metadata: checking playlist selection, bdinfo=%v", s.bdinfo != nil)

		// Normalize primary path for DB lookup (must match normalized save path)
		primaryNorm := filepath.ToSlash(filepath.Clean(primary))
		bdmvNorm := filepath.ToSlash(filepath.Clean(filepath.Join(primary, "BDMV")))

		s.logger.Debugf("metadata: querying playlist selection for normalized path: %s", primaryNorm)

		// Try normalized primary path first
		if result, err := s.repo.GetPlaylistSelection(ctx, primaryNorm); err == nil && len(result.SelectedPlaylists) > 0 {
			sel = result
			playlistPath = primary // Use original path for file operations
			found = true
			s.logger.Debugf("metadata: found playlist selection at normalized primary path: %s", primaryNorm)
		} else {
			// Try normalized BDMV path
			s.logger.Debugf("metadata: querying playlist selection for normalized BDMV path: %s", bdmvNorm)
			if result, err := s.repo.GetPlaylistSelection(ctx, bdmvNorm); err == nil && len(result.SelectedPlaylists) > 0 {
				sel = result
				playlistPath = filepath.Join(primary, "BDMV")
				found = true
				s.logger.Debugf("metadata: found playlist selection at normalized BDMV path: %s", bdmvNorm)
			} else {
				s.logger.Debugf("metadata: no playlist selection found, err=%v", err)
			}
		}

		if found && len(sel.SelectedPlaylists) > 0 {
			s.logger.Debugf("metadata: found playlist selection with %d playlists at %s", len(sel.SelectedPlaylists), playlistPath)
			selectedPlaylists, derr := loadSelectedBDMVPlaylists(ctx, playlistPath, sel.SelectedPlaylists)
			if derr != nil {
				return api.PreparedMetadata{}, fmt.Errorf("metadata: discover selected playlists: %w", derr)
			}
			meta.SelectedBDMVPlaylists = selectedPlaylists
			selectedPlaylistNames := playlistNames(meta.SelectedBDMVPlaylists)

			// Execute BDInfo on selected playlists
			if s.bdinfo != nil {
				tmpRoot, rerr := db.Subdir(s.cfg.MainSettings.DBPath, "tmp")
				if rerr != nil {
					return api.PreparedMetadata{}, fmt.Errorf("metadata: resolve tmp root: %w", rerr)
				}
				tmpDir, _, rerr := paths.ReleaseTempDir(tmpRoot, meta, primary)
				if rerr != nil {
					return api.PreparedMetadata{}, fmt.Errorf("metadata: resolve bdinfo temp dir: %w", rerr)
				}
				s.logger.Debugf("metadata: bdinfo temp dir: %s", tmpDir)
				if err := os.MkdirAll(tmpDir, 0755); err != nil {
					return api.PreparedMetadata{}, fmt.Errorf("metadata: create bdinfo temp dir: %w", err)
				}

				outputPath, needScan, berr := s.resolveOrCreateBDMVSummaries(ctx, req, tmpDir, playlistPath, selectedPlaylistNames)
				if berr != nil {
					return api.PreparedMetadata{}, berr
				}
				if strings.TrimSpace(outputPath) != "" {
					s.logger.Debugf("metadata: parsing canonical bdinfo output from %s", outputPath)
					bdinfoParsed, perr := parseBDInfoOutput(s.bdinfo, outputPath)
					if perr != nil {
						return api.PreparedMetadata{}, fmt.Errorf("metadata: bdinfo parse failed: %w", perr)
					}
					meta.BDInfo = bdinfoParsed
					s.logger.Debugf("metadata: bdinfo data collected with %d fields", len(bdinfoParsed))
				}
				if needScan {
					s.logger.Debugf("metadata: bdinfo scan completed for %d selected playlists", len(selectedPlaylistNames))
				}
			} else {
				s.logger.Debugf("metadata: bdinfo service is nil, skipping disc analysis")
			}

			// Extract m2ts files from selected playlist(s)
			m2tsFiles, mainFile, err := s.extractM2TSFromPlaylist(ctx, playlistPath, selectedPlaylistNames)
			if err != nil {
				s.logger.Debugf("metadata: failed to extract m2ts from playlist: %v", err)
				// Fall back to regular disc handling
			} else if mainFile != "" && len(m2tsFiles) > 0 {
				meta.VideoPath = mainFile
				meta.FileList = m2tsFiles
				s.logger.Debugf("metadata: extracted %d m2ts files from playlist, using %s as main", len(m2tsFiles), filepath.Base(mainFile))
			}
		}
	}

	if discType == "" {
		video, filelist, err := filesystem.CollectVideoFiles(ctx, primary, false)
		if err != nil {
			return api.PreparedMetadata{}, fmt.Errorf("metadata: collect video files: %w", err)
		}
		meta.VideoPath = video
		meta.FileList = filelist
		s.logger.Debugf("metadata: collected %d video files", len(filelist))
	}

	applySeasonEpisodeMetadata(&meta, seasonep.Extract(primary, meta), s.logger)

	size, err := filesystem.SourceSize(ctx, primary, meta.DiscType, meta.FileList, meta.VideoPath)
	if err != nil {
		return api.PreparedMetadata{}, fmt.Errorf("metadata: source size: %w", err)
	}
	meta.SourceSize = size
	s.logger.Debugf("metadata: source size %d bytes", size)

	storedInfoHash := ""
	if existing, err := s.repo.GetByPath(ctx, primary); err == nil {
		meta.StoredUpdatedAt = existing.UpdatedAt
		if metadataFingerprintMatches(primary, meta, existing) {
			meta.StoredDataFresh = true
			meta.StoredInfoHash = strings.TrimSpace(existing.InfoHash)
			storedInfoHash = meta.StoredInfoHash
			if s.logger != nil {
				s.logger.Debugf("metadata: reusing stored metadata snapshot for %s", primary)
			}
		} else if s.logger != nil {
			s.logger.Debugf("metadata: stored metadata stale for %s; recomputing", primary)
		}
	} else if !errors.Is(err, internalerrors.ErrNotFound) {
		return api.PreparedMetadata{}, fmt.Errorf("metadata: lookup: %w", err)
	}

	if meta.StoredDataFresh {
		if storedIDs, err := s.repo.GetExternalIDs(ctx, primary); err == nil {
			meta.ExternalIDs = storedIDs
		} else if !errors.Is(err, internalerrors.ErrNotFound) {
			return api.PreparedMetadata{}, fmt.Errorf("metadata: external ids lookup: %w", err)
		}
		if storedMeta, err := s.repo.GetExternalMetadata(ctx, primary); err == nil {
			meta.ExternalMetadata = storedMeta
		} else if !errors.Is(err, internalerrors.ErrNotFound) {
			return api.PreparedMetadata{}, fmt.Errorf("metadata: external metadata lookup: %w", err)
		}
	}

	if s.scene != nil {
		result, err := s.scene.Detect(ctx, meta)
		if err != nil {
			return api.PreparedMetadata{}, fmt.Errorf("metadata: scene detect: %w", err)
		}
		meta.Scene = result.IsScene
		meta.SceneName = result.SceneName
		meta.SceneIMDB = result.IMDBID
		meta.SceneNFOPath = result.NFOPath
		meta.SceneNFONew = result.NFONew
		if meta.Scene {
			s.logger.Debugf("metadata: scene release detected")
		}
		if meta.SceneIMDB > 0 {
			s.logger.Debugf("metadata: scene imdb detected %d", meta.SceneIMDB)
		}
		if meta.SceneNFOPath != "" {
			if meta.SceneNFONew {
				s.logger.Debugf("metadata: scene nfo downloaded %s", meta.SceneNFOPath)
			} else {
				s.logger.Debugf("metadata: scene nfo found %s", meta.SceneNFOPath)
			}
		}
	}
	if release.Title != "" || release.Alt != "" || release.Subtitle != "" || release.Artist != "" || release.Year != 0 || release.Month != 0 || release.Day != 0 || release.Source != "" || release.Resolution != "" || release.Ext != "" || release.Site != "" || release.Genre != "" || release.Channels != "" || release.Collection != "" || release.Region != "" || release.Size != "" || release.Group != "" || release.Disc != "" || release.Type != "" || release.Category != "" || len(release.Codec) > 0 || len(release.Audio) > 0 || len(release.HDR) > 0 || len(release.Language) > 0 {
		s.logger.Debugf(
			"metadata: release parsed category=%q type=%q artist=%q title=%q subtitle=%q alt=%q year=%d month=%d day=%d source=%q resolution=%q codec=%v audio=%v hdr=%v ext=%q language=%v site=%q genre=%q channels=%q collection=%q region=%q size=%q group=%q disc=%q",
			release.Category,
			release.Type,
			release.Artist,
			release.Title,
			release.Subtitle,
			release.Alt,
			release.Year,
			release.Month,
			release.Day,
			release.Source,
			release.Resolution,
			release.Codec,
			release.Audio,
			release.HDR,
			release.Ext,
			release.Language,
			release.Site,
			release.Genre,
			release.Channels,
			release.Collection,
			release.Region,
			release.Size,
			release.Group,
			release.Disc,
		)
	}
	if len(release.Edition) > 0 || len(release.Other) > 0 {
		s.logger.Tracef("metadata: release editions=%v other=%v", release.Edition, release.Other)
	}
	if release.Group != "" {
		meta.Tag = "-" + release.Group
	}

	select {
	case <-ctx.Done():
		return api.PreparedMetadata{}, ctx.Err()
	default:
	}

	if s.mi != nil {
		tmpRoot, err := db.Subdir(s.cfg.MainSettings.DBPath, "tmp")
		if err != nil {
			return api.PreparedMetadata{}, fmt.Errorf("metadata: tmp dir: %w", err)
		}
		miResult, err := s.mi.Export(ctx, mediainfo.Request{
			SourcePath: meta.SourcePath,
			DiscType:   meta.DiscType,
			VideoPath:  meta.VideoPath,
			TempRoot:   tmpRoot,
			Release:    meta.Release,
		})
		if err != nil {
			return api.PreparedMetadata{}, fmt.Errorf("metadata: mediainfo: %w", err)
		}
		meta.MediaInfoJSONPath = miResult.JSONPath
		meta.MediaInfoTextPath = miResult.TextPath
		meta.DVDIFOPath = miResult.IFOPath
		meta.DVDVOBPath = miResult.VOBPath
		meta.DVDVOBSet = miResult.VOBSet
		meta.DVDVOBMediaInfoJSON = miResult.VOBJSON
		meta.DVDVOBMediaInfoText = miResult.VOBText
		if strings.EqualFold(meta.DiscType, "DVD") {
			dvdDetails := extractDVDMediaInfo(meta)
			dvdDetails.SourcePath = meta.SourcePath
			dvdDetails.IFOPath = miResult.IFOPath
			dvdDetails.VOBPath = miResult.VOBPath
			dvdDetails.VOBSet = miResult.VOBSet
			dvdDetails.MediaInfoJSON = meta.MediaInfoJSONPath
			dvdDetails.MediaInfoText = meta.MediaInfoTextPath
			dvdDetails.VOBMediaInfoRaw = metautil.FirstNonEmptyTrimmed(strings.TrimSpace(miResult.VOBText), strings.TrimSpace(miResult.VOBJSON))
			dvdDetails.UpdatedAt = time.Now().UTC()
			if err := s.repo.SaveDVDMediaInfo(ctx, dvdDetails); err != nil {
				return api.PreparedMetadata{}, fmt.Errorf("metadata: persist dvd mediainfo: %w", err)
			}
		}
	}
	if s.tagsPath != "" {
		if tag, override, err := ApplyTagOverrides(primary, meta.Tag, s.tagsPath); err == nil {
			meta.Tag = tag
			meta.TagOverride = override
			if override != nil {
				s.logger.Debugf("metadata: tag override applied")
				if strings.TrimSpace(override.Source) != "" {
					meta.Release.Source = override.Source
				}
				if strings.TrimSpace(override.Type) != "" {
					meta.Release.Type = override.Type
				}
				if strings.TrimSpace(override.Template) != "" {
					meta.DescriptionTemplate = override.Template
				}
				if override.PersonalRelease {
					meta.PersonalRelease = true
				}
			}
		}
	}

	select {
	case <-ctx.Done():
		return api.PreparedMetadata{}, ctx.Err()
	default:
	}

	select {
	case <-ctx.Done():
		return api.PreparedMetadata{}, ctx.Err()
	default:
	}
	if err := s.repo.Save(ctx, db.FileMetadata{
		Path:       primary,
		InfoHash:   storedInfoHash,
		UpdatedAt:  time.Now().UTC(),
		DiscType:   meta.DiscType,
		VideoPath:  meta.VideoPath,
		FileList:   meta.FileList,
		SourceSize: meta.SourceSize,
		Scene:      meta.Scene,
		SceneName:  meta.SceneName,
		SceneIMDB:  meta.SceneIMDB,
		Category:   api.NormalizeCategory(meta.Release.Category),
		Type:       meta.Release.Type,
		Artist:     meta.Release.Artist,
		Title:      meta.Release.Title,
		Subtitle:   meta.Release.Subtitle,
		Alt:        meta.Release.Alt,
		Year:       meta.Release.Year,
		Month:      meta.Release.Month,
		Day:        meta.Release.Day,
		Source:     meta.Release.Source,
		Resolution: meta.Release.Resolution,
		Codec:      meta.Release.Codec,
		Audio:      meta.Release.Audio,
		HDR:        meta.Release.HDR,
		Ext:        meta.Release.Ext,
		Language:   meta.Release.Language,
		Site:       meta.Release.Site,
		Genre:      meta.Release.Genre,
		Channels:   meta.Release.Channels,
		Collection: meta.Release.Collection,
		Region:     meta.Release.Region,
		Size:       meta.Release.Size,
		Group:      meta.Release.Group,
		Disc:       meta.Release.Disc,
		Edition:    meta.Release.Edition,
		Other:      meta.Release.Other,
	}); err != nil {
		return api.PreparedMetadata{}, fmt.Errorf("metadata: persist: %w", err)
	}
	s.logger.Debugf("metadata: persisted metadata for %s", primary)

	return meta, nil
}

// extractM2TSFromPlaylist parses selected playlist files and extracts m2ts file references.
// Returns all m2ts files and the largest one to use as VideoPath.
func (s *Service) extractM2TSFromPlaylist(ctx context.Context, bdmvPath string, playlistFiles []string) ([]string, string, error) {
	playlistDir := filepath.Join(bdmvPath, "PLAYLIST")
	if _, err := os.Stat(playlistDir); err != nil {
		return nil, "", fmt.Errorf("playlist directory not found: %w", err)
	}

	// Collect all m2ts files from selected playlists
	allM2TS := make(map[string]struct{})
	var largestFile string
	var largestSize int64

	for _, playlistFile := range playlistFiles {
		playlistPath := filepath.Join(playlistDir, playlistFile)
		if !strings.HasSuffix(playlistPath, ".MPLS") && !strings.HasSuffix(playlistPath, ".mpls") {
			playlistPath += ".MPLS"
		}

		// Parse the playlist file
		duration, items, err := parseBDMVPlaylist(playlistPath)
		if err != nil {
			s.logger.Debugf("metadata: failed to parse playlist %s: %v", playlistFile, err)
			continue
		}
		s.logger.Debugf("metadata: parsed playlist %s (duration=%.1fs, items=%d)", playlistFile, duration, len(items))

		// Collect m2ts files from this playlist
		for _, item := range items {
			if item.File != "" {
				allM2TS[item.File] = struct{}{}
				// Track the largest file
				if item.Size > largestSize {
					largestSize = item.Size
					largestFile = filepath.Join(bdmvPath, "STREAM", item.File)
				}
			}
		}
	}

	if len(allM2TS) == 0 {
		return nil, "", errors.New("no m2ts files found in selected playlists")
	}

	// Build full paths for all m2ts files
	m2tsFiles := make([]string, 0, len(allM2TS))
	streamDir := filepath.Join(bdmvPath, "STREAM")
	for file := range allM2TS {
		fullPath := filepath.Join(streamDir, file)
		m2tsFiles = append(m2tsFiles, fullPath)
	}

	s.logger.Debugf("metadata: extracted %d m2ts files from playlists, largest is %s (%d bytes)", len(m2tsFiles), filepath.Base(largestFile), largestSize)
	return m2tsFiles, largestFile, nil
}

func loadSelectedBDMVPlaylists(ctx context.Context, bdmvPath string, selected []string) ([]api.PlaylistInfo, error) {
	discovered, err := discoverBDMVPlaylists(ctx, bdmvPath)
	if err != nil {
		return nil, err
	}

	byName := make(map[string]filesystem.PlaylistInfo, len(discovered))
	for _, playlist := range discovered {
		byName[discparse.NormalizePlaylistName(playlist.File)] = playlist
	}

	result := make([]api.PlaylistInfo, 0, len(selected))
	for _, name := range selected {
		normalized := discparse.NormalizePlaylistName(name)
		playlist, ok := byName[normalized]
		if !ok {
			return nil, fmt.Errorf("selected playlist %s was not found during discovery", normalized)
		}
		items := make([]api.PlaylistItem, 0, len(playlist.Items))
		for _, item := range playlist.Items {
			items = append(items, api.PlaylistItem{
				File: item.File,
				Size: item.Size,
			})
		}
		result = append(result, api.PlaylistInfo{
			File:     playlist.File,
			Duration: playlist.Duration,
			Items:    items,
			Score:    playlist.Score,
			Edition:  playlist.Edition,
		})
	}

	return result, nil
}

func playlistNames(playlists []api.PlaylistInfo) []string {
	names := make([]string, 0, len(playlists))
	for _, playlist := range playlists {
		normalized := discparse.NormalizePlaylistName(playlist.File)
		if normalized == "" {
			continue
		}
		names = append(names, normalized)
	}
	return names
}

func writeSelectedPlaylistSummaries(tmpDir string, fullReport string, selected []string) (string, error) {
	reports, err := discparse.ExtractPlaylistReports(fullReport, selected)
	if err != nil {
		return "", err
	}
	if len(reports) == 0 {
		return "", errors.New("no selected playlist reports extracted")
	}

	rawPath := filepath.Join(tmpDir, "BD_FULL.txt")
	if err := os.WriteFile(rawPath, []byte(fullReport), 0o600); err != nil {
		return "", fmt.Errorf("write full report: %w", err)
	}

	for _, report := range reports {
		summaryPath := paths.BDMVSummaryPath(tmpDir, report.Playlist)
		if err := os.WriteFile(summaryPath, []byte(strings.TrimSpace(report.Summary)+"\n"), 0o600); err != nil {
			return "", fmt.Errorf("write summary %s: %w", report.Playlist, err)
		}

		extSidecarPath := paths.BDMVExtSummaryPath(tmpDir, report.Playlist)
		if err := os.WriteFile(extSidecarPath, []byte(strings.TrimSpace(report.ExtSummary)+"\n"), 0o600); err != nil {
			return "", fmt.Errorf("write extended summary %s: %w", report.Playlist, err)
		}
	}

	return paths.BDMVSummaryPath(tmpDir, reports[0].Playlist), nil
}

func writeCachedSelectedPlaylistSummaries(tmpDir string, cache bdmvSummaryCache, selected []string) (string, error) {
	if len(selected) == 0 {
		return "", errors.New("no selected playlists")
	}
	entry, ok := cache.Entries[discparse.NormalizePlaylistName(selected[0])]
	if !ok {
		return "", fmt.Errorf("cached summary for %s not found", selected[0])
	}
	return entry.SummaryPath, nil
}

func discoverBDMVSummaryCache(tmpDir string) (bdmvSummaryCache, error) {
	cache := bdmvSummaryCache{Entries: map[string]cachedBDMVSummary{}}
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		if os.IsNotExist(err) {
			return cache, nil
		}
		return cache, fmt.Errorf("read tmp dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "BD_SUMMARY_") || strings.HasPrefix(name, "BD_SUMMARY_EXT_") || !strings.HasSuffix(name, ".txt") {
			continue
		}
		playlistFromName := paths.BDMVPlaylistKey(strings.TrimSuffix(strings.TrimPrefix(name, "BD_SUMMARY_"), ".txt"))
		if playlistFromName == "" {
			continue
		}
		summaryPath := filepath.Join(tmpDir, name)
		summaryPayload, err := os.ReadFile(summaryPath)
		if err != nil {
			return bdmvSummaryCache{}, fmt.Errorf("read cached summary %s: %w", name, err)
		}
		playlist := parsePlaylistFromSummaryText(string(summaryPayload))
		if playlist == "" {
			continue
		}
		if playlist != playlistFromName {
			return bdmvSummaryCache{}, fmt.Errorf("cached summary filename %s does not match playlist %s", name, playlist)
		}
		if _, exists := cache.Entries[playlist]; exists {
			return bdmvSummaryCache{}, fmt.Errorf("duplicate cached playlist summary for %s", playlist)
		}
		extPath := paths.BDMVExtSummaryPath(tmpDir, playlist)
		extPayload := ""
		if extPath != "" {
			cleanTmpDir := filepath.Clean(tmpDir)
			cleanExtPath := filepath.Clean(extPath)
			if relPath, err := filepath.Rel(cleanTmpDir, cleanExtPath); err == nil && relPath != ".." && !strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
				if rawExt, err := os.ReadFile(cleanExtPath); err == nil {
					extPayload = string(rawExt)
				}
			}
		}
		cache.Entries[playlist] = cachedBDMVSummary{
			Playlist:    playlist,
			Summary:     string(summaryPayload),
			ExtSummary:  extPayload,
			SummaryPath: summaryPath,
			ExtPath:     extPath,
		}
	}

	return cache, nil
}

func parsePlaylistFromSummaryText(summary string) string {
	matches := bdmvSummaryPlaylistPattern.FindStringSubmatch(summary)
	if len(matches) != 2 {
		return ""
	}
	return discparse.NormalizePlaylistName(matches[1])
}

func missingCachedPlaylists(cache bdmvSummaryCache, selected []string) []string {
	var missing []string
	for _, playlist := range selected {
		normalized := discparse.NormalizePlaylistName(playlist)
		if normalized == "" {
			continue
		}
		if _, ok := cache.Entries[normalized]; !ok {
			missing = append(missing, normalized)
		}
	}
	return missing
}

func cachedPlaylistNames(cache bdmvSummaryCache) []string {
	names := make([]string, 0, len(cache.Entries))
	for name := range cache.Entries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (s *Service) resolveOrCreateBDMVSummaries(ctx context.Context, req api.Request, tmpDir string, playlistPath string, selected []string) (string, bool, error) {
	cache, err := discoverBDMVSummaryCache(tmpDir)
	if err != nil {
		return "", false, fmt.Errorf("metadata: discover bdmv tmp cache: %w", err)
	}

	missing := missingCachedPlaylists(cache, selected)
	switch {
	case len(selected) > 0 && len(missing) == 0:
		outputPath, err := writeCachedSelectedPlaylistSummaries(tmpDir, cache, selected)
		if err != nil {
			return "", false, fmt.Errorf("metadata: refresh cached bdmv summaries: %w", err)
		}
		return outputPath, false, nil
	case len(missing) > 0 && len(missing) < len(selected):
		if req.Options.InteractionMode == api.InteractionModeUnattended || req.ConfirmBDMVRescan {
			// proceed to rescan
		} else {
			return "", false, &api.BDMVRescanRequiredError{
				SourcePath:        req.Paths[0],
				SelectedPlaylists: append([]string(nil), selected...),
				CachedPlaylists:   cachedPlaylistNames(cache),
				MissingPlaylists:  missing,
			}
		}
	case len(selected) > 0 && len(missing) == len(selected):
		// No selected playlists are cached, so we need a fresh scan.
	}

	if len(selected) > 1 {
		s.logger.Debugf("metadata: executing full-disc bdinfo for %d selected playlists", len(selected))
		scanResult, berr := executeFullBDInfoScan(s.bdinfo, ctx, playlistPath, tmpDir)
		if berr != nil {
			return "", false, fmt.Errorf("metadata: bdinfo full scan failed: %w", berr)
		}
		outputPath, werr := writeSelectedPlaylistSummaries(tmpDir, scanResult.ReportText, selected)
		if werr != nil {
			return "", false, fmt.Errorf("metadata: derive playlist summaries: %w", werr)
		}
		return outputPath, true, nil
	}

	playlistName := selected[0]
	s.logger.Debugf("metadata: executing bdinfo for playlist %s in path %s", playlistName, playlistPath)
	outputPath, berr := executePlaylistBDInfo(s.bdinfo, ctx, playlistPath, playlistName, paths.BDMVSummaryPath(tmpDir, playlistName))
	if berr != nil {
		return "", false, fmt.Errorf("metadata: bdinfo execution failed: %w", berr)
	}
	return outputPath, true, nil
}

func metadataFingerprintMatches(primary string, current api.PreparedMetadata, stored db.FileMetadata) bool {
	if !pathEqualForFingerprint(primary, stored.Path) {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(current.DiscType), strings.TrimSpace(stored.DiscType)) {
		return false
	}
	if current.SourceSize != 0 && stored.SourceSize != 0 && current.SourceSize != stored.SourceSize {
		return false
	}
	if strings.TrimSpace(current.VideoPath) != "" && strings.TrimSpace(stored.VideoPath) != "" && !pathEqualForFingerprint(current.VideoPath, stored.VideoPath) {
		return false
	}
	if len(current.FileList) > 0 && len(stored.FileList) > 0 {
		if len(current.FileList) != len(stored.FileList) {
			return false
		}
		currentFiles := normalizePathListForFingerprint(current.FileList)
		storedFiles := normalizePathListForFingerprint(stored.FileList)
		for index := range currentFiles {
			if currentFiles[index] != storedFiles[index] {
				return false
			}
		}
	}
	return true
}

func normalizePathListForFingerprint(paths []string) []string {
	normalized := make([]string, 0, len(paths))
	for _, value := range paths {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		normalized = append(normalized, normalizePathForFingerprint(trimmed))
	}
	sort.Strings(normalized)
	return normalized
}

func pathEqualForFingerprint(left string, right string) bool {
	return normalizePathForFingerprint(left) == normalizePathForFingerprint(right)
}

func normalizePathForFingerprint(path string) string {
	normalized := filepath.ToSlash(filepath.Clean(path))
	if runtime.GOOS == "windows" {
		return strings.ToLower(normalized)
	}
	return normalized
}

func normalizeScreenshotOverrides(overrides api.ScreenshotOverrides) api.ScreenshotOverrides {
	if len(overrides.ManualFrames) > 0 {
		frames := make([]int, 0, len(overrides.ManualFrames))
		for _, frame := range overrides.ManualFrames {
			if frame <= 0 {
				continue
			}
			frames = append(frames, frame)
		}
		overrides.ManualFrames = frames
	}
	if len(overrides.ComparisonPaths) > 0 {
		paths := make([]string, 0, len(overrides.ComparisonPaths))
		seen := make(map[string]struct{}, len(overrides.ComparisonPaths))
		for _, value := range overrides.ComparisonPaths {
			trimmed := strings.TrimSpace(value)
			if trimmed == "" {
				continue
			}
			absPath, err := filepath.Abs(trimmed)
			if err != nil {
				continue
			}
			normalized := normalizePathForFingerprint(absPath)
			if _, ok := seen[normalized]; ok {
				continue
			}
			seen[normalized] = struct{}{}
			paths = append(paths, absPath)
		}
		overrides.ComparisonPaths = paths
	}
	if overrides.ComparisonPrimaryIndex != nil && *overrides.ComparisonPrimaryIndex <= 0 {
		overrides.ComparisonPrimaryIndex = nil
	}
	return overrides
}

func applySeasonEpisodeMetadata(meta *api.PreparedMetadata, result seasonep.Result, logger api.Logger) {
	if meta == nil {
		return
	}

	if result.Season > 0 {
		meta.SeasonInt = result.Season
		meta.SeasonStr = seasonep.FormatSeason(result.Season)
		if meta.Release.Season == 0 {
			meta.Release.Season = result.Season
		}
	}
	if result.Episode > 0 {
		meta.EpisodeInt = result.Episode
		meta.EpisodeStr = seasonep.FormatEpisode(result.Episode)
		if meta.Release.Episode == 0 {
			meta.Release.Episode = result.Episode
		}
	}
	if result.DailyDate != "" {
		meta.DailyEpisodeDate = result.DailyDate
	}
	meta.TVPack = result.TVPack

	if logger != nil && (meta.SeasonStr != "" || meta.EpisodeStr != "" || meta.DailyEpisodeDate != "" || meta.TVPack) {
		logger.Debugf("metadata: parsed season/episode season=%q episode=%q daily_date=%q tv_pack=%t", meta.SeasonStr, meta.EpisodeStr, meta.DailyEpisodeDate, meta.TVPack)
	}
}
