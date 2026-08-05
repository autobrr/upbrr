// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/config/importer"
	"github.com/autobrr/upbrr/internal/core"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/filesystem"
	imagehostpolicy "github.com/autobrr/upbrr/internal/imagehosting/policy"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/sourcelayout"
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

const previewTimeout = 30 * time.Minute

func newTrackerAuthService(cfg config.Config, logger api.Logger) *trackerauth.Service {
	return trackerauth.NewServiceWithRegistryAndLogger(cfg, trackerimpl.MustNewRegistry(), logger)
}

// Backend owns the embedded web API runtime.
type Backend struct {
	runtimeMu         sync.RWMutex
	cfg               config.Config
	runtimeGeneration uint64
	capabilities      CoreCapabilities
	coreOwner         LifecycleOwner
	coreInitErr       error
	logger            *logging.Logger
	repo              *db.SQLiteRepository
	hub               *eventHub

	streamMu sync.Mutex
	streams  map[string]*backendLogStream
	streamWG sync.WaitGroup

	activationInitMu sync.Mutex
	activator        *RuntimeActivator
}

type backendLogStream struct {
	id        string
	sessionID string
	logger    *logging.Logger
	subID     int
	stop      chan struct{}
	done      chan struct{}
}

// logExclusions stores muted log patterns for the WebUI.
type logExclusions struct {
	Patterns []string `json:"patterns"`
}

func normalizePatterns(patterns []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		trimmed := strings.TrimSpace(pattern)
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

// NewBackendWithContext opens the shared repository, creates the logger, and
// starts the core service when cfg validates. Invalid config keeps settings
// routes usable while core-backed routes report the initialization error.
func NewBackendWithContext(ctx context.Context, cfg config.Config, hub *eventHub) (*Backend, error) {
	if ctx == nil {
		return nil, errors.New("webserver: context is required")
	}
	logger, err := logging.New(cfg.Logging, cfg.MainSettings.DBPath)
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}

	repo, err := db.OpenWithLoggerContext(ctx, cfg.MainSettings.DBPath, logger)
	if err != nil {
		_ = logger.Close()
		return nil, fmt.Errorf("web: %w", err)
	}
	if err := repo.MigrateContext(ctx); err != nil {
		_ = repo.Close()
		_ = logger.Close()
		return nil, fmt.Errorf("web: %w", err)
	}

	var capabilities CoreCapabilities
	var coreOwner LifecycleOwner
	var coreInitErr error
	if err := cfg.Validate(); err != nil {
		coreInitErr = err
		logger.Warnf("web: config invalid, core disabled until settings are fixed: %v", err)
	} else {
		coreSvc, coreErr := core.NewWithContext(ctx, api.CoreDependencies{
			Config: cfg,
			Logger: logger,
			Services: api.ServiceSet{
				Filesystem: filesystem.NewValidator(),
			},
			Repository:      repo.RepositoryCapabilities(),
			RepositoryOwner: repo,
		})
		if coreErr != nil {
			_ = repo.Close()
			_ = logger.Close()
			return nil, fmt.Errorf("web: %w", coreErr)
		}
		capabilities, coreOwner = BindCoreCapabilities(coreSvc)
	}

	backend := &Backend{
		cfg:               cfg,
		runtimeGeneration: AllocateRuntimeGenerationID(),
		capabilities:      capabilities,
		coreOwner:         coreOwner,
		coreInitErr:       coreInitErr,
		logger:            logger,
		repo:              repo,
		hub:               hub,
		streams:           make(map[string]*backendLogStream),
	}
	if _, err := backend.runtimeActivator(); err != nil {
		if coreOwner != nil {
			_ = coreOwner.Close()
		}
		_ = repo.Close()
		_ = logger.Close()
		return nil, fmt.Errorf("web: %w", err)
	}
	return backend, nil
}

// Close stops active background work and releases runtime, repository, and log resources.
func (b *Backend) Close() error {
	b.stopAllLogStreams()
	rt := b.runtimeSnapshot()
	if rt.coreOwner != nil {
		_ = rt.coreOwner.Close()
	}
	if b.repo != nil {
		_ = b.repo.Close()
	}
	if rt.logger != nil {
		_ = rt.logger.Close()
	}
	return nil
}

// DetectDiscType classifies the selected host filesystem release path.
func (b *Backend) DetectDiscType(ctx context.Context, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()
	layout, err := sourcelayout.Resolve(ctx, path)
	if err != nil {
		return wrapWebResult("", err)
	}
	return layout.DiscType, nil
}

// RenderDescription converts tracker markup into sanitized preview HTML.
func (b *Backend) RenderDescription(raw string) (string, error) {
	rt, err := b.requireRuntime()
	if err != nil {
		return "", err
	}
	descriptionCore, err := rt.descriptionCore()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	return wrapWebResult(descriptionCore.RenderDescription(ctx, raw))
}

// DiscoverPlaylists returns Blu-ray playlists available under the selected release path.
func (b *Backend) DiscoverPlaylists(ctx context.Context, path string) ([]api.PlaylistInfo, error) {
	rt, err := b.requireRuntime()
	if err != nil {
		return nil, err
	}
	playlistCore, err := rt.playlistCore()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()
	return wrapWebResult(playlistCore.DiscoverPlaylists(ctx, path))
}

// BrowseDirectory lists a host filesystem directory for the requested frontend browse mode.
func (b *Backend) BrowseDirectory(path string, mode string) (api.BrowseDirectoryResponse, error) {
	if b == nil {
		return api.BrowseDirectoryResponse{}, errors.New("backend not initialized")
	}
	fallback := BrowseDirectoryFallback(b.currentConfig().MainSettings.DBPath)
	return wrapWebResult(BrowseDirectory(api.BrowseDirectoryRequest{Path: path, Mode: mode}, fallback))
}

// BrowseDirectoryWithinRoot lists a directory only when it remains within the authorized host root.
func (b *Backend) BrowseDirectoryWithinRoot(path string, mode string, root string) (api.BrowseDirectoryResponse, error) {
	if b == nil {
		return api.BrowseDirectoryResponse{}, errors.New("backend not initialized")
	}
	fallback := BrowseDirectoryFallback(b.currentConfig().MainSettings.DBPath)
	return wrapWebResult(BrowseDirectoryWithinRoot(api.BrowseDirectoryRequest{Path: path, Mode: mode}, fallback, root))
}

// BrowseDirectoryWithinRoots lists a directory only when it remains within an authorized host root.
func (b *Backend) BrowseDirectoryWithinRoots(path string, mode string, roots []string) (api.BrowseDirectoryResponse, error) {
	if b == nil {
		return api.BrowseDirectoryResponse{}, errors.New("backend not initialized")
	}
	fallback := BrowseDirectoryFallback(b.currentConfig().MainSettings.DBPath)
	return wrapWebResult(BrowseDirectoryWithinRoots(api.BrowseDirectoryRequest{Path: path, Mode: mode}, fallback, roots))
}

// GetConfig returns the current exportable config as JSON with encrypted
// secret fields for browser settings consumers.
func (b *Backend) GetConfig() (string, error) {
	cfg, _, err := b.exportableConfig()
	if err != nil {
		return "", err
	}
	return wrapWebResult(config.ExportToJSON(cfg))
}

// GetApplicationInfo returns build/runtime metadata plus a bounded, path-free
// DVD menu capability probe for embedded-web diagnostics.
func (b *Backend) GetApplicationInfo() (api.ApplicationInfo, error) {
	rt := b.runtimeSnapshot()
	return CurrentApplicationInfo(context.Background(), rt.capabilities.DiagnosticProbe), nil
}

// GetReleaseWorkflowCapabilities returns authenticated, non-secret integration
// metadata for composite-workflow API clients.
func (b *Backend) GetReleaseWorkflowCapabilities(
	ownerID string,
	scopes []string,
	uploadOptionSchemaHash string,
) (api.ReleaseWorkflowCapabilities, error) {
	if b == nil {
		return api.ReleaseWorkflowCapabilities{}, errors.New("backend not initialized")
	}
	registry, err := trackerimpl.NewRegistry()
	if err != nil {
		return api.ReleaseWorkflowCapabilities{}, fmt.Errorf("webserver: tracker registry: %w", err)
	}
	descriptors, err := registry.CatalogDescriptors()
	if err != nil {
		return api.ReleaseWorkflowCapabilities{}, fmt.Errorf("webserver: tracker descriptors: %w", err)
	}
	catalog, err := b.ListTrackerCatalog()
	if err != nil {
		return api.ReleaseWorkflowCapabilities{}, err
	}
	catalogByName := make(map[string]api.TrackerCatalogEntry, len(catalog.Entries))
	for _, entry := range catalog.Entries {
		catalogByName[strings.ToUpper(strings.TrimSpace(entry.Name))] = entry
	}
	trackerCapabilities := make([]api.ReleaseWorkflowCapabilityTracker, 0, len(descriptors))
	for _, descriptor := range descriptors {
		entry, ok := catalogByName[strings.ToUpper(strings.TrimSpace(string(descriptor.TrackerID)))]
		if !ok {
			continue
		}
		fields := make([]api.ReleaseWorkflowCapabilityField, len(entry.Fields))
		for index, field := range entry.Fields {
			fields[index] = api.ReleaseWorkflowCapabilityField{
				Key:        field.Key,
				YAMLKey:    field.YAMLKey,
				Activation: field.Activation,
			}
		}
		trackerCapabilities = append(trackerCapabilities, api.ReleaseWorkflowCapabilityTracker{
			ID:           descriptor.TrackerID,
			DisplayName:  descriptor.DisplayName,
			Configured:   entry.Configured,
			Default:      entry.Default,
			Capabilities: descriptor.Capabilities,
			ConfigFields: fields,
		})
	}

	cfg := b.currentConfig()
	clientNames := make([]string, 0, len(cfg.TorrentClients))
	for name := range cfg.TorrentClients {
		if name = strings.TrimSpace(name); name != "" {
			clientNames = append(clientNames, name)
		}
	}
	slices.SortFunc(clientNames, func(left, right string) int {
		return strings.Compare(strings.ToLower(left), strings.ToLower(right))
	})
	torrentClients := make([]api.ReleaseWorkflowCapabilityResource, 0, len(clientNames))
	for _, name := range clientNames {
		torrentClients = append(torrentClients, api.ReleaseWorkflowCapabilityResource{
			ID:          name,
			DisplayName: name,
			Configured:  true,
		})
	}
	imageHosts := make([]api.ReleaseWorkflowCapabilityResource, 0)
	for _, host := range imagehostpolicy.KnownUploadHosts() {
		imageHosts = append(imageHosts, api.ReleaseWorkflowCapabilityResource{
			ID:          host,
			DisplayName: host,
			Configured:  releaseWorkflowImageHostConfigured(cfg.ImageHosting, host),
		})
	}
	appInfo := api.CurrentApplicationInfo()
	return api.ReleaseWorkflowCapabilities{
		ApplicationVersion: appInfo.Version,
		APIVersion:         api.ReleaseWorkflowAPIVersion,
		OwnerID:            strings.TrimSpace(ownerID),
		Scopes:             append([]string(nil), scopes...),
		Features: api.ReleaseWorkflowCapabilityFeatures{
			CompositeUpload:                   true,
			TypedFeedback:                     true,
			StrictEligibleTrackerContinuation: true,
		},
		UploadOptionSchemaHash: strings.TrimSpace(uploadOptionSchemaHash),
		Trackers:               trackerCapabilities,
		TorrentClients:         torrentClients,
		ImageHosts:             imageHosts,
	}, nil
}

func releaseWorkflowImageHostConfigured(cfg config.ImageHostingConfig, host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if cfg.HostEnabled(host) {
		return true
	}
	for _, selected := range []string{cfg.Host1, cfg.Host2, cfg.Host3, cfg.Host4, cfg.Host5, cfg.Host6} {
		if strings.EqualFold(strings.TrimSpace(selected), host) {
			return true
		}
	}
	switch host {
	case "dalexni":
		return strings.TrimSpace(cfg.DalexniAPI) != ""
	case "imgbb":
		return strings.TrimSpace(cfg.ImgBBAPI) != ""
	case "lensdump":
		return strings.TrimSpace(cfg.LensdumpAPI) != ""
	case "lostimg":
		return strings.TrimSpace(cfg.LostimgAPI) != ""
	case "onlyimage":
		return strings.TrimSpace(cfg.OnlyImageAPI) != ""
	case "passtheimage":
		return strings.TrimSpace(cfg.PassTheImageAPI) != ""
	case "ptscreens":
		return strings.TrimSpace(cfg.PTScreensAPI) != ""
	case "reelflix":
		return strings.TrimSpace(cfg.ReelflixAPI) != ""
	case "seedpool_cdn":
		return strings.TrimSpace(cfg.SeedpoolCDNAPI) != ""
	case "sharex":
		return strings.TrimSpace(cfg.ShareXURL) != "" && strings.TrimSpace(cfg.ShareXAPIKey) != ""
	case "utppm":
		return strings.TrimSpace(cfg.UTPPMAPI) != ""
	case "zipline":
		return strings.TrimSpace(cfg.ZiplineURL) != "" && strings.TrimSpace(cfg.ZiplineAPIKey) != ""
	case "hdb", "imgbox", "pixhost", "thr":
		return true
	}
	return false
}

// ExportConfig returns the exportable config, using plaintext secrets only
// when auth material for the exported snapshot's DB path explicitly allows
// unencrypted export.
func (b *Backend) ExportConfig() (string, error) {
	cfg, authDBPath, err := b.exportableConfig()
	if err != nil {
		return "", err
	}

	allowPlaintext, err := b.allowUnencryptedExport(authDBPath)
	if err != nil {
		return "", err
	}
	if allowPlaintext {
		return wrapWebResult(config.ExportToPlaintextJSON(cfg))
	}

	return wrapWebResult(config.ExportToJSON(cfg))
}

// GetDefaultConfig returns the built-in configuration serialized for the settings editor.
func (b *Backend) GetDefaultConfig() (string, error) {
	cfg, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		return "", fmt.Errorf("web: %w", err)
	}
	return wrapWebResult(config.ExportToJSON(cfg))
}

// ListTrackerAuthCapabilities returns browser-visible tracker auth support from
// the current runtime config.
func (b *Backend) ListTrackerAuthCapabilities() ([]api.TrackerAuthCapability, error) {
	if b == nil {
		return nil, errors.New("backend not initialized")
	}
	return wrapWebResult(newTrackerAuthService(b.currentConfig(), b.currentLogger()).Capabilities(context.Background()))
}

// GetTrackerAuthStatus reports local auth state for tracker from the current
// runtime config and persisted cookie/auth state.
func (b *Backend) GetTrackerAuthStatus(tracker string) (api.TrackerAuthStatus, error) {
	if b == nil {
		return api.TrackerAuthStatus{}, errors.New("backend not initialized")
	}
	return wrapWebResult(newTrackerAuthService(b.currentConfig(), b.currentLogger()).Status(context.Background(), tracker))
}

// ImportTrackerAuthCookieContent imports browser-supplied cookie content with
// the request context and the shared raw content size limit.
func (b *Backend) ImportTrackerAuthCookieContent(ctx context.Context, tracker string, fileName string, content string) (api.TrackerAuthStatus, error) {
	if b == nil {
		return api.TrackerAuthStatus{}, errors.New("backend not initialized")
	}
	return wrapWebResult(newTrackerAuthService(b.currentConfig(), b.currentLogger()).ImportCookies(ctx, tracker, fileName, content))
}

// TestTrackerAuth validates tracker auth with ctx so canceled web requests stop
// remote validation and persistence work.
func (b *Backend) TestTrackerAuth(ctx context.Context, tracker string) (api.TrackerAuthStatus, error) {
	if b == nil {
		return api.TrackerAuthStatus{}, errors.New("backend not initialized")
	}
	return wrapWebResult(newTrackerAuthService(b.currentConfig(), b.currentLogger()).Validate(ctx, tracker))
}

// LoginTrackerAuth attempts credential-based tracker auth with ctx and returns
// status for missing credentials, unsupported login, or 2FA.
func (b *Backend) LoginTrackerAuth(ctx context.Context, tracker string, req api.TrackerAuthLoginRequest) (api.TrackerAuthStatus, error) {
	if b == nil {
		return api.TrackerAuthStatus{}, errors.New("backend not initialized")
	}
	return wrapWebResult(newTrackerAuthService(b.currentConfig(), b.currentLogger()).Login(ctx, tracker, req))
}

// SubmitTrackerAuth2FA completes an active manual 2FA challenge with ctx and
// returns the refreshed tracker auth status.
func (b *Backend) SubmitTrackerAuth2FA(ctx context.Context, challengeID string, code string) (api.TrackerAuthStatus, error) {
	if b == nil {
		return api.TrackerAuthStatus{}, errors.New("backend not initialized")
	}
	return wrapWebResult(newTrackerAuthService(b.currentConfig(), b.currentLogger()).Submit2FA(ctx, challengeID, code))
}

// DeleteTrackerAuth removes stored tracker cookies and tracker-specific auth
// state with ctx, then returns the refreshed local status.
func (b *Backend) DeleteTrackerAuth(ctx context.Context, tracker string) (api.TrackerAuthStatus, error) {
	if b == nil {
		return api.TrackerAuthStatus{}, errors.New("backend not initialized")
	}
	return wrapWebResult(newTrackerAuthService(b.currentConfig(), b.currentLogger()).Delete(ctx, tracker))
}

// exportableConfig returns the normalized config snapshot and the DB path that
// must authorize plaintext export for that exact snapshot. Fresh installs with
// no persisted config export the current runtime config without saving it.
func (b *Backend) exportableConfig() (*config.Config, string, error) {
	if b.repo == nil {
		return nil, "", errors.New("config repository not initialized")
	}
	rt := b.runtimeSnapshot()
	cfg, err := config.LoadFromDatabase(context.Background(), b.repo)
	if err != nil {
		if errors.Is(err, internalerrors.ErrNotFound) {
			// Fresh web installs can run from embedded defaults before any config
			// rows exist, so export the runtime config until the user saves setup.
			cfg, normalizeErr := normalizeExportableConfig(&rt.cfg, rt.cfg.MainSettings.DBPath)
			if normalizeErr != nil {
				return nil, "", normalizeErr
			}
			return cfg, cfg.MainSettings.DBPath, nil
		}
		return nil, "", fmt.Errorf("web: %w", err)
	}
	cfg, err = normalizeExportableConfig(cfg, rt.cfg.MainSettings.DBPath)
	if err != nil {
		return nil, "", err
	}
	return cfg, cfg.MainSettings.DBPath, nil
}

// normalizeExportableConfig returns a cloned config with tracker defaults,
// legacy nils, and missing DB path values filled so browser consumers receive
// stable JSON shapes without mutating the loaded runtime or database config.
func normalizeExportableConfig(cfg *config.Config, dbPath string) (*config.Config, error) {
	normalized, err := cloneConfigForExport(cfg)
	if err != nil {
		return nil, err
	}
	if _, err := config.MergeMissingTrackerDefaults(normalized); err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}
	if strings.TrimSpace(normalized.MainSettings.DBPath) == "" {
		normalized.MainSettings.DBPath = dbPath
	}
	if normalized.Trackers.Trackers == nil {
		normalized.Trackers.Trackers = map[string]config.TrackerConfig{}
	}
	if normalized.Trackers.DefaultTrackers == nil {
		normalized.Trackers.DefaultTrackers = config.CSVList{}
	}
	return normalized, nil
}

// cloneConfigForExport deep-copies config through JSON so export
// normalization cannot mutate the source snapshot.
func cloneConfigForExport(cfg *config.Config) (*config.Config, error) {
	payload, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("web: clone config for export: marshal: %w", err)
	}
	var cloned config.Config
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return nil, fmt.Errorf("web: clone config for export: unmarshal: %w", err)
	}
	return &cloned, nil
}

// allowUnencryptedExport reports whether the auth material for dbPath permits
// plaintext config export. Missing auth material denies plaintext export;
// malformed material is returned as an error.
func (b *Backend) allowUnencryptedExport(dbPath string) (bool, error) {
	material, err := authmaterial.LoadFromDBPath(dbPath)
	if err == nil {
		return material.AllowUnencryptedExport, nil
	}
	if errors.Is(err, authmaterial.ErrUnavailable) {
		return false, nil
	}
	return false, fmt.Errorf("web: %w", err)
}

// SaveConfig decodes encrypted browser settings and delegates the complete
// config/runtime transition to the shared runtime activator.
func (b *Backend) SaveConfig(payload string) error {
	if b.repo == nil {
		return errors.New("config repository not initialized")
	}
	cfg, err := config.ImportFromJSONEncrypted(payload)
	if err != nil {
		return fmt.Errorf("web: %w", err)
	}
	activator, err := b.runtimeActivator()
	if err != nil {
		return fmt.Errorf("web: %w", err)
	}
	if err := activator.Activate(context.Background(), *cfg); err != nil {
		return fmt.Errorf("web: %w", err)
	}
	return nil
}

const configImportMaxBytes = importer.MaxFileBytes

// ImportConfig imports browser-uploaded config content, validates the saved and
// env-applied runtime forms, builds the replacement runtime, migrates shared
// cookies, then persists the non-env config before installing the runtime.
// Runtime build, migration, or save failures leave the persisted config and
// active runtime unchanged.
func (b *Backend) ImportConfig(fileName, fileContent string) (string, []string, error) {
	if b.repo == nil {
		return "", nil, errors.New("config repository not initialized")
	}
	if strings.TrimSpace(fileName) == "" {
		return "", nil, errors.New("file name is required")
	}
	if strings.TrimSpace(fileContent) == "" {
		return "", nil, errors.New("file content is required")
	}

	cfg, warnings, err := importer.ImportFromContent(fileName, []byte(fileContent))
	if err != nil {
		return "", nil, fmt.Errorf("web: %w", err)
	}

	activator, err := b.runtimeActivator()
	if err != nil {
		return "", nil, fmt.Errorf("web: %w", err)
	}
	if err := activator.Activate(context.Background(), *cfg); err != nil {
		return "", nil, fmt.Errorf("web: %w", err)
	}

	result := "imported config"
	if len(warnings) > 0 {
		result += fmt.Sprintf(" (%d warnings)", len(warnings))
	}
	return result, warnings, nil
}

// ListTrackerCatalog returns ordered tracker identity, config schemas, and local
// configured state without exposing current credential values.
func (b *Backend) ListTrackerCatalog() (api.TrackerCatalog, error) {
	registry, err := trackerimpl.NewRegistry()
	if err != nil {
		return api.TrackerCatalog{}, fmt.Errorf("webserver: tracker registry: %w", err)
	}
	schemas, err := config.OrderedTrackerSchemas()
	if err != nil {
		return api.TrackerCatalog{}, fmt.Errorf("webserver: tracker config catalog: %w", err)
	}

	cfg := b.currentConfig()
	defaultTrackers := make(map[string]struct{}, len(cfg.Trackers.DefaultTrackers))
	for _, name := range cfg.Trackers.DefaultTrackers {
		normalized := strings.ToUpper(strings.TrimSpace(name))
		if normalized != "" {
			defaultTrackers[normalized] = struct{}{}
		}
	}
	entries := make([]api.TrackerCatalogEntry, 0, len(schemas))
	seen := make(map[string]struct{}, len(schemas))
	for _, schema := range schemas {
		descriptor, ok := registry.LookupDescriptor(schema.Name)
		if !ok {
			return api.TrackerCatalog{}, fmt.Errorf("webserver: tracker config catalog entry %s has no implementation", schema.Name)
		}
		fields := make([]api.TrackerCatalogField, len(schema.Fields))
		for index, field := range schema.Fields {
			fields[index] = api.TrackerCatalogField{
				Key:        field.JSONKey,
				YAMLKey:    field.YAMLKey,
				Default:    field.Default,
				Activation: field.Activation,
			}
		}
		trackerCfg, _ := trackerConfigByName(cfg.Trackers.Trackers, schema.Name)
		_, isDefault := defaultTrackers[schema.Name]
		entries = append(entries, api.TrackerCatalogEntry{
			Name:              schema.Name,
			Family:            string(descriptor.Family),
			BaseURL:           descriptor.BaseURL,
			UploadContentMode: string(descriptor.UploadContentMode),
			Fields:            fields,
			Configured:        config.TrackerConfigured(trackerCfg, schema),
			Default:           isDefault,
		})
		seen[schema.Name] = struct{}{}
	}
	for _, name := range registry.Names() {
		if _, ok := seen[name]; !ok {
			return api.TrackerCatalog{}, fmt.Errorf("webserver: tracker implementation %s has no config catalog entry", name)
		}
	}

	unsupported := make([]string, 0)
	for name := range cfg.Trackers.Trackers {
		normalized := strings.ToUpper(strings.TrimSpace(name))
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; !ok {
			unsupported = append(unsupported, name)
		}
	}
	slices.SortFunc(unsupported, func(left, right string) int {
		return strings.Compare(strings.ToUpper(left), strings.ToUpper(right))
	})
	return api.TrackerCatalog{Entries: entries, Unsupported: unsupported}, nil
}

func trackerConfigByName(entries map[string]config.TrackerConfig, name string) (config.TrackerConfig, bool) {
	if cfg, ok := entries[name]; ok {
		return cfg, true
	}
	for entryName, cfg := range entries {
		if strings.EqualFold(strings.TrimSpace(entryName), strings.TrimSpace(name)) {
			return cfg, true
		}
	}
	return config.TrackerConfig{}, false
}

// GetImageHostPolicyMetadata returns image-host policy metadata consumed by settings and upload UI.
func (b *Backend) GetImageHostPolicyMetadata() (imagehostpolicy.Metadata, error) {
	registry, err := trackerimpl.NewRegistry()
	if err != nil {
		return imagehostpolicy.Metadata{}, fmt.Errorf("webserver: tracker registry: %w", err)
	}
	metadata := imagehostpolicy.Metadata{
		UploadHosts:        imagehostpolicy.KnownUploadHosts(),
		TrackerUploadHosts: make(map[string][]string),
		OwnedHosts:         make(map[string]string),
	}
	for _, tracker := range registry.Names() {
		policy, ok := registry.LookupImageHostPolicy(tracker)
		if !ok {
			continue
		}
		hosts := append([]string(nil), policy.AllowedHosts...)
		if host := strings.ToLower(strings.TrimSpace(policy.ConditionalHost)); host != "" {
			hosts = append(hosts, host)
		}
		uploadHosts := make([]string, 0, len(hosts))
		for _, host := range hosts {
			normalized := strings.ToLower(strings.TrimSpace(host))
			if imagehostpolicy.IsUploadHost(normalized) && !slices.Contains(uploadHosts, normalized) {
				uploadHosts = append(uploadHosts, normalized)
			}
		}
		if len(uploadHosts) > 0 {
			metadata.TrackerUploadHosts[tracker] = uploadHosts
		}
		for _, host := range policy.OwnedHosts {
			metadata.OwnedHosts[strings.ToLower(strings.TrimSpace(host))] = tracker
		}
	}
	return metadata, nil
}

// ListHistory returns persisted release history in repository-defined order.
func (b *Backend) ListHistory() ([]api.HistoryEntry, error) {
	rt := b.runtimeSnapshot()
	history, err := rt.historyCore()
	if err != nil {
		return nil, err
	}
	return wrapWebResult(history.ListHistory(context.Background()))
}

// GetHistoryOverview returns persisted upload and asset detail for one source path.
func (b *Backend) GetHistoryOverview(sourcePath string) (api.HistoryOverview, error) {
	rt := b.runtimeSnapshot()
	history, err := rt.historyCore()
	if err != nil {
		return api.HistoryOverview{}, err
	}
	return wrapWebResult(history.GetHistoryOverview(context.Background(), sourcePath))
}

// DeleteHistoryRelease purges persisted history and managed artifacts for one source path.
func (b *Backend) DeleteHistoryRelease(sourcePath string) error {
	rt, err := b.requireRuntime()
	if err != nil {
		return err
	}
	historyCore, err := rt.historyCore()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	return wrapWebError(historyCore.DeleteHistoryRelease(ctx, strings.TrimSpace(sourcePath)))
}

// GetLogPath returns the host filesystem path of the active application log.
func (b *Backend) GetLogPath() (string, error) {
	return wrapWebResult(logging.LogPath(b.currentConfig().MainSettings.DBPath))
}

// GetRecentLogs returns up to limit sanitized recent log entries.
func (b *Backend) GetRecentLogs(limit int) ([]logging.Entry, error) {
	logger := b.currentLogger()
	if logger == nil {
		return nil, errors.New("logger not initialized")
	}
	return logger.Recent(limit), nil
}

// GetLogExclusions returns persisted frontend log-filter patterns.
func (b *Backend) GetLogExclusions() ([]string, error) {
	if b.repo == nil {
		return nil, errors.New("config repository not initialized")
	}
	var exclusions logExclusions
	err := config.LoadSectionFromDatabase(context.Background(), "log_exclusions", &exclusions, b.repo)
	if err != nil {
		if errorsIsNotFound(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("web: %w", err)
	}
	return normalizePatterns(exclusions.Patterns), nil
}

// UpdateLogExclusions validates and persists frontend log-filter patterns.
func (b *Backend) UpdateLogExclusions(patterns []string) error {
	if b.repo == nil {
		return errors.New("config repository not initialized")
	}
	return wrapWebError(config.SaveSectionToDatabase(context.Background(), "log_exclusions", logExclusions{
		Patterns: normalizePatterns(patterns),
	}, b.repo))
}

// StartLogStream subscribes the browser session to live log events. Active
// streams are rebound when settings replace the runtime logger. If no logger or
// event hub is installed, it returns an error without registering a stream.
func (b *Backend) StartLogStream(sessionID string) (string, error) {
	streamID, err := randomString(12)
	if err != nil {
		return "", err
	}
	if b == nil {
		return "", errors.New("logger not initialized")
	}
	if b.hub == nil {
		return "", errors.New("event hub not initialized")
	}

	b.runtimeMu.RLock()
	logger := b.logger
	if logger == nil {
		b.runtimeMu.RUnlock()
		return "", errors.New("logger not initialized")
	}
	session := &backendLogStream{
		id:        streamID,
		sessionID: sessionID,
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	b.streamMu.Lock()
	b.streams[streamID] = session
	b.startLogStreamWorker(session, logger)
	b.streamMu.Unlock()
	b.runtimeMu.RUnlock()

	return streamID, nil
}

// startLogStreamWorker subscribes session to logger and forwards entries to
// the browser event hub until the stream is stopped.
func (b *Backend) startLogStreamWorker(session *backendLogStream, logger *logging.Logger) {
	subID, ch := logger.Subscribe(0)
	stop := session.stop
	done := session.done
	session.logger = logger
	session.subID = subID

	b.streamWG.Go(func() {
		defer close(done)
		for {
			select {
			case entry, ok := <-ch:
				if !ok {
					return
				}
				b.hub.Emit(session.sessionID, "log:stream:"+session.id, entry)
			case <-stop:
				logger.Unsubscribe(subID)
				return
			}
		}
	})
}

// rebindLogStreams moves streams attached to oldLogger onto newLogger without
// changing their browser-visible stream IDs.
func (b *Backend) rebindLogStreams(oldLogger *logging.Logger, newLogger *logging.Logger) {
	if b == nil || oldLogger == nil || newLogger == nil || oldLogger == newLogger {
		return
	}

	type stoppedStream struct {
		session *backendLogStream
		done    <-chan struct{}
	}

	b.streamMu.Lock()
	stopped := make([]stoppedStream, 0, len(b.streams))
	for _, session := range b.streams {
		if session == nil || session.logger != oldLogger {
			continue
		}
		stopped = append(stopped, stoppedStream{
			session: session,
			done:    session.done,
		})
		select {
		case <-session.stop:
		default:
			close(session.stop)
		}
	}
	b.streamMu.Unlock()

	for _, stream := range stopped {
		if stream.done != nil {
			<-stream.done
		}
	}

	b.streamMu.Lock()
	for _, stream := range stopped {
		session := stream.session
		if session == nil || b.streams[session.id] != session {
			continue
		}
		session.stop = make(chan struct{})
		session.done = make(chan struct{})
		b.startLogStreamWorker(session, newLogger)
	}
	b.streamMu.Unlock()
}

// StopLogStream stops streamID only when it belongs to sessionID.
// Unknown streams and streams owned by other sessions are treated as no-ops.
func (b *Backend) StopLogStream(sessionID string, streamID string) error {
	trimmedSessionID := strings.TrimSpace(sessionID)
	b.streamMu.Lock()
	session := b.streams[streamID]
	if session != nil && strings.TrimSpace(session.sessionID) != trimmedSessionID {
		session = nil
	}
	if session != nil {
		delete(b.streams, streamID)
		select {
		case <-session.stop:
		default:
			close(session.stop)
		}
	}
	b.streamMu.Unlock()
	if session != nil {
		<-session.done
	}
	return nil
}

// StopSessionLogStreams closes all active log streams owned by sessionID.
func (b *Backend) StopSessionLogStreams(sessionID string) {
	trimmedSessionID := strings.TrimSpace(sessionID)
	if trimmedSessionID == "" {
		return
	}

	b.streamMu.Lock()
	streamIDs := make([]string, 0)
	for id, stream := range b.streams {
		if stream != nil && stream.sessionID == trimmedSessionID {
			streamIDs = append(streamIDs, id)
		}
	}
	b.streamMu.Unlock()

	for _, streamID := range streamIDs {
		_ = b.StopLogStream(trimmedSessionID, streamID)
	}
}

func (b *Backend) stopAllLogStreams() {
	b.streamMu.Lock()
	streams := make([]*backendLogStream, 0, len(b.streams))
	for id, stream := range b.streams {
		delete(b.streams, id)
		streams = append(streams, stream)
		select {
		case <-stream.stop:
		default:
			close(stream.stop)
		}
	}
	b.streamMu.Unlock()
	for _, stream := range streams {
		if stream != nil {
			<-stream.done
		}
	}
	b.streamWG.Wait()
}

func errorsIsNotFound(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "not found")
}
