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

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/autobrr/upbrr/internal/clientdiscovery"
	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/metadata/bluraycom"
	"github.com/autobrr/upbrr/internal/metadata/discparse"
	"github.com/autobrr/upbrr/internal/metadata/mediainfo"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/metadata/seasonep"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/services/bdinfo"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerdata "github.com/autobrr/upbrr/internal/trackers/data"
	"github.com/autobrr/upbrr/pkg/api"
)

// Service collects source-scoped metadata evidence for canonical preparation.
// Optional providers and client discovery are installed through [Option] values.
type Service struct {
	repo     repository
	tagsPath string
	scene    SceneDetector
	mi       mediainfo.Exporter
	bdinfo   *bdinfo.Service
	logger   api.Logger
	cacheDir string
	nfoDir   string
	cfg      config.Config
	tmdb     TMDBClient
	anilist  AniListClient
	imdb     IMDBClient
	tvdb     TVDBClient
	tvmaze   TVmazeClient
	sonarr   ArrLookupClient
	radarr   ArrLookupClient
	tracker  TrackerDataLookup
	bluray   *bluraycom.Client
	registry *trackers.Registry
	clients  *clientdiscovery.Module
}

type repository interface {
	GetByPath(context.Context, string) (api.FileMetadata, error)
	Save(context.Context, api.FileMetadata) error
	GetExternalIdentity(context.Context, string) (api.ExternalIdentity, error)
	GetExternalMetadata(context.Context, string) (api.SourceScopedMetadata, error)
	SaveDVDMediaInfo(context.Context, api.DVDMediaInfo) error
	GetReleaseNameOverrides(context.Context, string) (api.ReleaseNameOverrides, error)
	SaveReleaseNameOverrides(context.Context, string, api.ReleaseNameOverrides) error
	GetPlaylistSelection(context.Context, string) (api.PlaylistSelection, error)
	GetTrackerTimestamp(context.Context, string) (time.Time, error)
	SaveTrackerTimestamp(context.Context, api.TrackerTimestamp) error
	SaveTrackerMetadata(context.Context, api.TrackerMetadata) error
	SaveTrackerRuleFailures(context.Context, string, string, []api.TrackerRuleFailure) error
}

type cachedBDMVSummary struct {
	Playlist    string
	Summary     string
	ExtSummary  string
	FullSummary string
	SummaryPath string
	ExtPath     string
	FullPath    string
}

type bdmvSummaryCache struct {
	Entries map[string]cachedBDMVSummary
}

var bdmvSummaryPlaylistPattern = regexp.MustCompile(`(?mi)^Playlist:\s*(.+?)\s*$`)

var (
	discoverBDMVPlaylists = filesystem.DiscoverPlaylists
	parseBDMVPlaylist     = filesystem.ParseMPLS
	executePlaylistBDInfo = func(svc *bdinfo.Service, ctx context.Context, bdmvPath string, playlistFile string, outputPath string, summaryOnly bool) (string, error) {
		return svc.ExecuteForPlaylist(ctx, bdmvPath, playlistFile, outputPath, summaryOnly)
	}
	executeFullBDInfoScan = func(svc *bdinfo.Service, ctx context.Context, bdmvPath string, outputDir string) (bdinfo.ScanResult, error) {
		return svc.ExecuteFullScan(ctx, bdmvPath, outputDir)
	}
	parseBDInfoOutput = func(svc *bdinfo.Service, filePath string) (map[string]any, error) {
		return svc.ParseOutput(filePath)
	}
)

// TrackerDataLookup provides optional remote enrichment for one tracker
// candidate. Implementations receive the operation's ID-only and image-retention
// policy; persistence and cross-tracker selection remain owned by [Service].
type TrackerDataLookup interface {
	Lookup(
		ctx context.Context,
		tracker string,
		trackerID string,
		subject api.UploadSubject,
		searchFileName string,
		onlyID bool,
		keepImages bool,
	) (trackerdata.Result, error)
}

// ArrLookupClient returns non-authoritative source-path evidence from an Arr
// service. An empty result means the source was not matched.
type ArrLookupClient interface {
	Lookup(ctx context.Context, meta preparationstate.State) (ArrLookupResult, error)
}

// Option configures a Service during construction. Options run in caller order;
// later options may replace earlier dependencies.
type Option func(*Service)

// WithTagsPathFromDB derives the optional tag-override file from dbPath.
func WithTagsPathFromDB(dbPath string) Option {
	return func(s *Service) {
		s.tagsPath = resolveTagsPath(dbPath)
	}
}

// WithSceneDetector installs an explicit detector. A non-nil detector is used
// even when configured scene detection is disabled; nil permits the
// config-gated default.
func WithSceneDetector(detector SceneDetector) Option {
	return func(s *Service) {
		s.scene = detector
	}
}

// WithLogger installs the service logger; nil is normalized to [api.NopLogger].
func WithLogger(logger api.Logger) Option {
	return func(s *Service) {
		s.logger = logger
	}
}

// WithMediaInfoExporter installs the MediaInfo exporter; nil permits the
// construction-time default.
func WithMediaInfoExporter(exporter mediainfo.Exporter) Option {
	return func(s *Service) {
		s.mi = exporter
	}
}

// WithConfig copies cfg for provider, path, and policy decisions.
func WithConfig(cfg config.Config) Option {
	return func(s *Service) {
		s.cfg = cfg
	}
}

// WithTrackerRegistry supplies tracker policies and factories and enables the
// default tracker-data client when no explicit lookup is installed.
func WithTrackerRegistry(registry *trackers.Registry) Option {
	return func(s *Service) { s.registry = registry }
}

// WithClientDiscovery installs canonical preparation-time client discovery.
func WithClientDiscovery(discovery *clientdiscovery.Module) Option {
	return func(s *Service) { s.clients = discovery }
}

// WithTMDBClient also installs client as the AniList provider when it implements
// [AniListClient] and no earlier option supplied one.
func WithTMDBClient(client TMDBClient) Option {
	return func(s *Service) {
		s.tmdb = client
		if anilistClient, ok := client.(AniListClient); ok && s.anilist == nil {
			s.anilist = anilistClient
		}
	}
}

// WithAniListClient explicitly replaces the current AniList provider, including
// one inferred by an earlier [WithTMDBClient] option.
func WithAniListClient(client AniListClient) Option {
	return func(s *Service) {
		s.anilist = client
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

func WithBlurayClient(client *bluraycom.Client) Option {
	return func(s *Service) {
		s.bluray = client
	}
}

// WithSRRDBPaths derives the scene cache and NFO directories from dbPath and may
// create the private cache directory during construction.
func WithSRRDBPaths(dbPath string) Option {
	return func(s *Service) {
		cacheDir, nfoDir := resolveSRRDBPaths(dbPath)
		s.cacheDir = cacheDir
		s.nfoDir = nfoDir
	}
}

// NewService applies non-nil options in order, then installs default logging,
// MediaInfo, configured scene detection, tracker lookup, and Blu-ray providers.
// Resolving default scene paths may create private cache directories.
func NewService(repo repository, opts ...Option) *Service {
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
	// Scene detection adds srrdb network fan-out to every prepared item, so it is
	// gated by config (default on). Disabling it makes zero srrdb requests; an
	// explicitly injected detector (WithSceneDetector) is always honored.
	if service.scene == nil && service.cfg.MainSettings.SceneDetection {
		detector := newSRRDBDetector(nil, "", service.cacheDir, service.nfoDir)
		detector.logger = service.logger
		service.scene = detector
	}
	if service.tracker == nil && service.registry != nil {
		service.tracker = trackerdata.NewClientWithRegistry(service.cfg, service.logger, nil, service.registry)
	}
	if service.bluray == nil {
		service.bluray = bluraycom.NewClient(nil, service.logger)
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

// collectSourceEvidence validates source resources, resolves Blu-ray selection,
// and gathers filesystem evidence before provider and client enrichment.
func (s *Service) collectSourceEvidence(ctx context.Context, request preparationstate.Request) (meta preparationstate.State, err error) {
	bdinfoActive := false
	bdinfoTerminal := false
	defer func() {
		if err != nil {
			if bdinfoActive && !bdinfoTerminal {
				api.EmitPreparationProgress(
					ctx,
					api.NewPreparationProgressUpdate(api.PreparationPhaseBDInfo, api.PreparationProgressFailed, "Blu-ray analysis failed."),
				)
			}
			s.logger.Warnf("metadata: preparation blocked err=%s", redaction.RedactValue(err.Error(), nil))
		}
	}()

	select {
	case <-ctx.Done():
		return preparationstate.State{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	s.logger.Debugf("metadata: preparing source")
	if s.repo == nil {
		return preparationstate.State{}, errors.New("metadata: repository not configured")
	}

	input := request.Input
	primary := strings.TrimSpace(request.Layout.SourcePath)
	if primary == "" {
		return preparationstate.State{}, fmt.Errorf("metadata: empty primary path: %w", internalerrors.ErrInvalidInput)
	}

	absPath, err := filepath.Abs(primary)
	if err != nil {
		return preparationstate.State{}, fmt.Errorf("metadata: resolve path: %w", err)
	}
	primary = absPath
	s.logger.Debugf("metadata: primary path resolved to %s", primary)

	normalizedPaths := []string{primary}
	s.logger.Tracef("metadata: normalized path %s", primary)

	meta = preparationstate.State{
		SourcePath:      primary,
		SourceLookupURL: strings.TrimSpace(input.Instructions.SourceLookup),
		Paths:           normalizedPaths,
		Policy: preparationstate.CollectionPolicy{
			OnlyID:          input.Policy.OnlyID,
			KeepFolder:      input.Policy.KeepFolder,
			KeepImages:      input.Policy.KeepImages,
			InteractionMode: input.Controls.Interaction,
		},
		TrackerIDs:           cloneTrackerIDs(input.Instructions.TrackerIDs),
		MetadataOverrides:    input.Instructions.Metadata,
		ExternalIDOverrides:  input.Instructions.Identity,
		ReleaseNameOverrides: input.Instructions.ReleaseName,
	}
	applySourceLookupOverrideWithRegistry(&meta, s.registry)
	meta.Release = ParseReleaseInfo(primary)

	discType := request.Layout.DiscType
	meta.DiscType = discType
	if discType != "" {
		s.logger.Debugf("metadata: detected disc type %s", discType)
	}

	// For BDMV discs, check if a playlist was selected
	if discType == "BDMV" {
		s.logger.Debugf("metadata: checking playlist selection, bdinfo=%v", s.bdinfo != nil)
		selectedPlaylists, derr := s.resolveBDMVPlaylistSelection(ctx, request)
		if derr != nil {
			return preparationstate.State{}, derr
		}
		meta.SelectedBDMVPlaylists = selectedPlaylists
		if len(selectedPlaylists) > 0 {
			playlistPath := request.Layout.BDMVRoot
			s.logger.Debugf("metadata: resolved playlist selection playlists=%d", len(selectedPlaylists))
			selectedPlaylistNames := playlistNames(meta.SelectedBDMVPlaylists)

			// Execute BDInfo on selected playlists
			if s.bdinfo != nil {
				bdinfoActive = true
				api.EmitPreparationProgress(
					ctx,
					api.NewPreparationProgressUpdate(api.PreparationPhaseBDInfo, api.PreparationProgressRunning, "Preparing Blu-ray analysis."),
				)
				tmpRoot, rerr := db.Subdir(s.cfg.MainSettings.DBPath, "tmp")
				if rerr != nil {
					bdinfoTerminal = true
					api.EmitPreparationProgress(
						ctx,
						api.NewPreparationProgressUpdate(api.PreparationPhaseBDInfo, api.PreparationProgressFailed, "Blu-ray analysis failed."),
					)
					return preparationstate.State{}, fmt.Errorf("metadata: resolve tmp root: %w", rerr)
				}
				tmpDir, _, rerr := paths.ReleaseTempDir(tmpRoot, meta, primary)
				if rerr != nil {
					return preparationstate.State{}, fmt.Errorf("metadata: resolve bdinfo temp dir: %w", rerr)
				}
				s.logger.Debugf("metadata: bdinfo temp dir: %s", tmpDir)
				if err := os.MkdirAll(tmpDir, 0755); err != nil {
					return preparationstate.State{}, fmt.Errorf("metadata: create bdinfo temp dir: %w", err)
				}

				outputPath, needScan, berr := s.resolveOrCreateBDMVSummaries(ctx, input, tmpDir, playlistPath, selectedPlaylistNames)
				if berr != nil {
					bdinfoTerminal = true
					api.EmitPreparationProgress(
						ctx,
						api.NewPreparationProgressUpdate(api.PreparationPhaseBDInfo, api.PreparationProgressFailed, "Blu-ray analysis failed."),
					)
					return preparationstate.State{}, berr
				}
				if strings.TrimSpace(outputPath) != "" {
					s.logger.Debugf("metadata: parsing canonical bdinfo output from %s", outputPath)
					bdinfoParsed, perr := parseBDInfoOutput(s.bdinfo, outputPath)
					if perr != nil {
						return preparationstate.State{}, fmt.Errorf("metadata: bdinfo parse failed: %w", perr)
					}
					meta.BDInfo = bdinfoParsed
					s.logger.Debugf("metadata: bdinfo data collected with %d fields", len(bdinfoParsed))
				}
				if needScan {
					s.logger.Debugf("metadata: bdinfo scan completed for %d selected playlists", len(selectedPlaylistNames))
					api.EmitPreparationProgress(
						ctx,
						api.NewPreparationProgressUpdate(api.PreparationPhaseBDInfo, api.PreparationProgressCompleted, "Blu-ray analysis complete."),
					)
					bdinfoTerminal = true
				} else {
					api.SkipPreparationProgress(ctx, api.PreparationPhaseBDInfo, "Reused cached Blu-ray analysis.")
					bdinfoTerminal = true
				}
			} else {
				s.logger.Debugf("metadata: bdinfo service is nil, skipping disc analysis")
				api.SkipPreparationProgress(ctx, api.PreparationPhaseBDInfo, "Blu-ray analysis is unavailable.")
			}

			// Extract m2ts files from selected playlist(s)
			m2tsFiles, mainFile, err := s.extractM2TSFromPlaylist(playlistPath, selectedPlaylistNames)
			if err != nil {
				s.logger.Debugf("metadata: failed to extract m2ts from playlist: %v", err)
				// Fall back to regular disc handling
			} else if mainFile != "" && len(m2tsFiles) > 0 {
				meta.VideoPath = mainFile
				meta.FileList = m2tsFiles
				s.logger.Debugf("metadata: extracted m2ts files count=%d main=%s", len(m2tsFiles), filepath.Base(mainFile))
			}
		}
	}

	if discType == "" {
		video, filelist, err := filesystem.CollectVideoFiles(ctx, primary, false)
		if err != nil {
			return preparationstate.State{}, fmt.Errorf("metadata: collect video files: %w", err)
		}
		meta.VideoPath = video
		meta.FileList = filelist
		s.logger.Debugf("metadata: collected %d video files", len(filelist))
	}

	applySeasonEpisodeMetadata(&meta, seasonep.Extract(primary, meta), s.logger)
	release := ParseReleaseInfo(primary)
	meta.Release = release

	storedOverrides := api.ReleaseNameOverrides{}
	if stored, err := s.repo.GetReleaseNameOverrides(ctx, primary); err == nil {
		storedOverrides = stored
	} else if !errors.Is(err, internalerrors.ErrNotFound) {
		return preparationstate.State{}, fmt.Errorf("metadata: release overrides lookup: %w", err)
	}
	mergedOverrides := mergeReleaseNameOverrides(storedOverrides, input.Instructions.ReleaseName)
	meta.ReleaseNameOverrides = mergedOverrides
	if hasReleaseNameOverrides(input.Instructions.ReleaseName) {
		if err := s.repo.SaveReleaseNameOverrides(ctx, primary, mergedOverrides); err != nil {
			return preparationstate.State{}, fmt.Errorf("metadata: release overrides persist: %w", err)
		}
	}

	size, err := filesystem.SourceSize(ctx, primary, meta.DiscType, meta.FileList, meta.VideoPath)
	if err != nil {
		return preparationstate.State{}, fmt.Errorf("metadata: source size: %w", err)
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
		return preparationstate.State{}, fmt.Errorf("metadata: lookup: %w", err)
	}

	if meta.StoredDataFresh {
		if storedIDs, err := s.repo.GetExternalIdentity(ctx, primary); err == nil {
			meta.Identity = storedIDs
		} else if !errors.Is(err, internalerrors.ErrNotFound) {
			return preparationstate.State{}, fmt.Errorf("metadata: external ids lookup: %w", err)
		}
		if storedMeta, err := s.repo.GetExternalMetadata(ctx, primary); err == nil {
			meta.ProviderMetadata = storedMeta
		} else if !errors.Is(err, internalerrors.ErrNotFound) {
			return preparationstate.State{}, fmt.Errorf("metadata: external metadata lookup: %w", err)
		}
	}

	// Scene detection runs during fact derivation after provider evidence and
	// rebuilt naming are available. Running it here would miss renamed releases.
	if release.Title != "" || release.Alt != "" || release.Subtitle != "" || release.Artist != "" || release.Year != 0 || release.Month != 0 ||
		release.Day != 0 ||
		release.Source != "" ||
		release.Resolution != "" ||
		release.Ext != "" ||
		release.Site != "" ||
		release.Genre != "" ||
		release.Channels != "" ||
		release.Collection != "" ||
		release.Region != "" ||
		release.Size != "" ||
		release.Group != "" ||
		release.Disc != "" ||
		release.Type != "" ||
		release.Category != "" ||
		len(release.Codec) > 0 ||
		len(release.Audio) > 0 ||
		len(release.HDR) > 0 ||
		len(release.Language) > 0 {
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
		return preparationstate.State{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	if s.mi != nil {
		tmpRoot, err := db.Subdir(s.cfg.MainSettings.DBPath, "tmp")
		if err != nil {
			return preparationstate.State{}, fmt.Errorf("metadata: tmp dir: %w", err)
		}
		miResult, err := s.mi.Export(ctx, mediainfo.Request{
			SourcePath: meta.SourcePath,
			DiscType:   meta.DiscType,
			VideoPath:  meta.VideoPath,
			TempRoot:   tmpRoot,
			Release:    meta.Release,
		})
		if err != nil {
			return preparationstate.State{}, fmt.Errorf("metadata: mediainfo: %w", err)
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
				return preparationstate.State{}, fmt.Errorf("metadata: persist dvd mediainfo: %w", err)
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
		return preparationstate.State{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	select {
	case <-ctx.Done():
		return preparationstate.State{}, fmt.Errorf("context canceled: %w", ctx.Err())
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
		return preparationstate.State{}, fmt.Errorf("metadata: persist: %w", err)
	}
	s.logger.Debugf("metadata: persisted metadata for %s", primary)

	return meta, nil
}

// sceneResultHasData reports whether a scene detector returned metadata worth
// applying even when a recoverable side-effect error was also returned.
func sceneResultHasData(result SceneResult) bool {
	return result.IsScene ||
		strings.TrimSpace(result.SceneName) != "" ||
		result.TMDBID > 0 ||
		result.IMDBID > 0 ||
		result.TVDBID > 0 ||
		result.TVmazeID > 0 ||
		result.MALID > 0 ||
		strings.TrimSpace(result.Service) != "" ||
		strings.TrimSpace(result.ServiceLongName) != "" ||
		strings.TrimSpace(result.NFOPath) != ""
}

// applySceneResult copies detector scene metadata into prepared metadata,
// preserving existing service labels unless the detector supplied them first.
func applySceneResult(meta *preparationstate.State, result SceneResult) {
	meta.Scene = result.IsScene
	meta.SceneName = result.SceneName
	meta.SceneTMDBID = result.TMDBID
	meta.SceneIMDB = result.IMDBID
	meta.SceneTVDBID = result.TVDBID
	meta.SceneTVmazeID = result.TVmazeID
	meta.SceneMALID = result.MALID
	if meta.Service == "" {
		meta.Service = strings.TrimSpace(result.Service)
	}
	if meta.ServiceLongName == "" {
		meta.ServiceLongName = strings.TrimSpace(result.ServiceLongName)
	}
	meta.SceneNFOPath = result.NFOPath
	meta.SceneNFONew = result.NFONew
	meta.SceneRenamed = result.Renamed
	meta.SceneRenamedReason = result.RenamedReason
}

// extractM2TSFromPlaylist parses selected playlist files and extracts m2ts file references.
// Returns all m2ts files and the largest one to use as VideoPath.
func (s *Service) extractM2TSFromPlaylist(bdmvPath string, playlistFiles []string) ([]string, string, error) {
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
		return "", fmt.Errorf("metadata: %w", err)
	}
	if len(reports) == 0 {
		return "", errors.New("no selected playlist reports extracted")
	}

	rawPath := filepath.Join(tmpDir, "BD_FULL.txt")
	if err := safeWriteFile(tmpDir, rawPath, []byte(fullReport)); err != nil {
		return "", fmt.Errorf("write full report: %w", err)
	}

	for _, report := range reports {
		summaryPath := paths.BDMVSummaryPath(tmpDir, report.Playlist)
		if err := safeWriteFile(tmpDir, summaryPath, []byte(strings.TrimSpace(report.Summary)+"\n")); err != nil {
			return "", fmt.Errorf("write summary %s: %w", report.Playlist, err)
		}

		extSidecarPath := paths.BDMVExtSummaryPath(tmpDir, report.Playlist)
		if err := safeWriteFile(tmpDir, extSidecarPath, []byte(strings.TrimSpace(report.ExtSummary)+"\n")); err != nil {
			return "", fmt.Errorf("write extended summary %s: %w", report.Playlist, err)
		}

		fullPath := paths.BDMVFullSummaryPath(tmpDir, report.Playlist)
		if err := safeWriteFile(tmpDir, fullPath, []byte(strings.TrimSpace(report.Raw)+"\n")); err != nil {
			return "", fmt.Errorf("write full summary %s: %w", report.Playlist, err)
		}
	}

	return paths.BDMVSummaryPath(tmpDir, reports[0].Playlist), nil
}

func writePlaylistSummaries(tmpDir string, fullReport string, playlistName string) (string, error) {
	normalized := discparse.NormalizePlaylistName(playlistName)
	if normalized == "" {
		return "", errors.New("invalid playlist name")
	}

	summary, _, extSummary := discparse.SplitBDInfoReport(fullReport)
	if strings.TrimSpace(summary) == "" {
		return "", fmt.Errorf("playlist %s did not contain a quick summary", normalized)
	}

	summaryPath := paths.BDMVSummaryPath(tmpDir, normalized)
	if err := safeWriteFile(tmpDir, summaryPath, []byte(strings.TrimSpace(summary)+"\n")); err != nil {
		return "", fmt.Errorf("write summary %s: %w", normalized, err)
	}

	extPath := paths.BDMVExtSummaryPath(tmpDir, normalized)
	if err := safeWriteFile(tmpDir, extPath, []byte(strings.TrimSpace(extSummary)+"\n")); err != nil {
		return "", fmt.Errorf("write extended summary %s: %w", normalized, err)
	}

	fullPath := paths.BDMVFullSummaryPath(tmpDir, normalized)
	if err := safeWriteFile(tmpDir, fullPath, []byte(strings.TrimSpace(fullReport)+"\n")); err != nil {
		return "", fmt.Errorf("write full summary %s: %w", normalized, err)
	}

	return summaryPath, nil
}

func writeCachedSelectedPlaylistSummaries(cache bdmvSummaryCache, selected []string) (string, error) {
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
		if !strings.HasPrefix(name, "BD_SUMMARY_") || strings.HasPrefix(name, "BD_SUMMARY_EXT_") || strings.HasPrefix(name, "BD_SUMMARY_FULL_") ||
			!strings.HasSuffix(name, ".txt") {
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
			if relPath, err := filepath.Rel(
				cleanTmpDir,
				cleanExtPath,
			); err == nil && relPath != ".." &&
				!strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
				if rawExt, err := os.ReadFile(cleanExtPath); err == nil {
					extPayload = string(rawExt)
				}
			}
		}
		fullPath := paths.BDMVFullSummaryPath(tmpDir, playlist)
		fullPayload := ""
		if fullPath != "" {
			cleanTmpDir := filepath.Clean(tmpDir)
			cleanFullPath := filepath.Clean(fullPath)
			if relPath, err := filepath.Rel(
				cleanTmpDir,
				cleanFullPath,
			); err == nil && relPath != ".." &&
				!strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
				if rawFull, err := os.ReadFile(cleanFullPath); err == nil {
					fullPayload = string(rawFull)
				}
			}
		}
		cache.Entries[playlist] = cachedBDMVSummary{
			Playlist:    playlist,
			Summary:     string(summaryPayload),
			ExtSummary:  extPayload,
			FullSummary: fullPayload,
			SummaryPath: summaryPath,
			ExtPath:     extPath,
			FullPath:    fullPath,
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

// resolveOrCreateBDMVSummaries reuses complete cached playlist reports or runs
// the minimum permitted BDInfo scan, requiring confirmation for partial caches.
func (s *Service) resolveOrCreateBDMVSummaries(
	ctx context.Context,
	input api.PrepareInput,
	tmpDir string,
	playlistPath string,
	selected []string,
) (string, bool, error) {
	cache, err := discoverBDMVSummaryCache(tmpDir)
	if err != nil {
		return "", false, fmt.Errorf("metadata: discover bdmv tmp cache: %w", err)
	}

	missing := missingCachedPlaylists(cache, selected)
	switch {
	case len(selected) > 0 && len(missing) == 0:
		outputPath, err := writeCachedSelectedPlaylistSummaries(cache, selected)
		if err != nil {
			return "", false, fmt.Errorf("metadata: refresh cached bdmv summaries: %w", err)
		}
		return outputPath, false, nil
	case len(missing) > 0 && len(missing) < len(selected):
		if input.Controls.Interaction != api.InteractionModeUnattended && !input.Controls.ConfirmBDMVRescan {
			return "", false, &api.BDMVRescanRequiredError{
				SourcePath:        input.SourcePath,
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
	fullPath := paths.BDMVFullSummaryPath(tmpDir, playlistName)
	_, berr := executePlaylistBDInfo(s.bdinfo, ctx, playlistPath, playlistName, fullPath, false)
	if berr != nil {
		return "", false, fmt.Errorf("metadata: bdinfo execution failed: %w", berr)
	}
	fullReportBytes, rerr := os.ReadFile(fullPath)
	if rerr != nil {
		return "", false, fmt.Errorf("metadata: read full bdinfo report: %w", rerr)
	}
	outputPath, werr := writePlaylistSummaries(tmpDir, string(fullReportBytes), playlistName)
	if werr != nil {
		return "", false, fmt.Errorf("metadata: write playlist summaries: %w", werr)
	}
	return outputPath, true, nil
}

func metadataFingerprintMatches(primary string, current preparationstate.State, stored db.FileMetadata) bool {
	if !pathEqualForFingerprint(primary, stored.Path) {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(current.DiscType), strings.TrimSpace(stored.DiscType)) {
		return false
	}
	if current.SourceSize != 0 && stored.SourceSize != 0 && current.SourceSize != stored.SourceSize {
		return false
	}
	if strings.TrimSpace(current.VideoPath) != "" && strings.TrimSpace(stored.VideoPath) != "" &&
		!pathEqualForFingerprint(current.VideoPath, stored.VideoPath) {
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

func applySeasonEpisodeMetadata(meta *preparationstate.State, result seasonep.Result, logger api.Logger) {
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
		logger.Debugf(
			"metadata: parsed season/episode season=%q episode=%q daily_date=%q tv_pack=%t",
			meta.SeasonStr,
			meta.EpisodeStr,
			meta.DailyEpisodeDate,
			meta.TVPack,
		)
	}
}

func safeWriteFile(dir string, path string, data []byte) error {
	cleanDir := filepath.Clean(dir)
	cleanPath := filepath.Clean(path)
	rel, err := filepath.Rel(cleanDir, cleanPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("path traversal detected: %s is not within %s", path, dir)
	}
	//nolint:gosec // Path is validated against path traversal using filepath.Rel.
	if err := os.WriteFile(cleanPath, data, 0o600); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	return nil
}
