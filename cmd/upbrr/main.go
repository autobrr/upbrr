// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/config/importer"
	"github.com/autobrr/upbrr/internal/core"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/guiapp"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/webserver"
	"github.com/autobrr/upbrr/pkg/api"
)

var version = "dev"

const (
	defaultConfigName = "config.yaml"
)

func main() {
	exitCode := 0
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		var cliErr *cliExitError
		if errors.As(err, &cliErr) {
			exitCode = cliErr.code
		} else {
			exitCode = 1
		}
	}
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

type cliExitError struct {
	code int
	err  error
}

func (e *cliExitError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *cliExitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func exitError(code int, err error) error {
	if err == nil {
		return nil
	}
	return &cliExitError{code: code, err: err}
}

func run() error {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		if err := runServe(os.Args[2:]); err != nil {
			return exitError(1, err)
		}
		return nil
	}

	opts, visitedFlags, paths, err := parseCLIOptions(os.Args[1:])
	if err != nil {
		return exitError(2, err)
	}

	configFlagProvided := visitedFlags["config"]

	if opts.ShowVersion {
		fmt.Printf("upbrr %s\n", version)
		return nil
	}

	if opts.GUI && strings.TrimSpace(opts.ExportConfigPath) != "" {
		return exitError(2, errors.New("--gui and --export-config cannot be used together"))
	}

	if opts.GUI {
		resolvedConfigPath := ""
		if configFlagProvided {
			resolvedConfigPath, err = resolveConfigPath(opts.ConfigPath, configFlagProvided)
			if err != nil {
				return exitError(1, err)
			}
		}
		if err := guiapp.Run(guiapp.RunOptions{ConfigPath: resolvedConfigPath, ConfigProvided: configFlagProvided}); err != nil {
			return exitError(1, err)
		}
		return nil
	}

	if strings.TrimSpace(opts.ExportConfigPath) != "" {
		ctx := context.Background()
		if err := exportConfigToYAML(ctx, opts.ConfigPath, configFlagProvided, opts.ExportConfigPath); err != nil {
			return exitError(1, err)
		}
		fmt.Printf("exported config to %s\n", opts.ExportConfigPath)
		return nil
	}

	if strings.TrimSpace(opts.ImportConfigPath) != "" {
		ctx := context.Background()
		if err := importConfig(ctx, opts.ImportConfigPath, opts.ConfigPath, configFlagProvided); err != nil {
			return exitError(1, err)
		}
		return nil
	}

	resolvedConfigPath, err := resolveConfigPath(opts.ConfigPath, configFlagProvided)
	if err != nil {
		return exitError(1, err)
	}

	if opts.Cleanup && opts.DeleteTmp {
		return exitError(2, errors.New("--cleanup and -dtmp cannot be used together"))
	}
	if opts.Cleanup && opts.UploadOnly {
		return exitError(2, errors.New("--cleanup cannot be used with --upload-only"))
	}
	if opts.Cleanup && len(paths) > 0 {
		return exitError(2, errors.New("--cleanup does not accept input paths"))
	}
	if !opts.Cleanup && len(paths) == 0 {
		if strings.TrimSpace(opts.SiteUpload) != "" {
			return exitError(2, errors.New("--site-upload currently requires at least one input path in the Go CLI"))
		}
		return exitError(2, errors.New("at least one input path is required"))
	}

	cfg, dbPath, err := loadCLIConfig(resolvedConfigPath, configFlagProvided)
	if err != nil {
		return exitError(1, err)
	}

	effectiveLogLevel := logging.ResolveEffectiveLevel(cfg.Logging.Level, opts.LogLevel, opts.Debug)
	logger, err := logging.NewWithLevel(cfg.Logging, dbPath, effectiveLogLevel)
	if err != nil {
		return exitError(1, err)
	}
	defer func() {
		if err := logger.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
	}()
	screens := opts.Screens
	if screens < 0 {
		screens = cfg.ScreenshotHandling.Screens
	}
	coreSvc, err := core.New(api.CoreDependencies{
		Config: cfg,
		Logger: logger,
		Services: api.ServiceSet{
			Filesystem: filesystem.NewValidator(),
		},
	})
	if err != nil {
		return exitError(1, err)
	}
	defer func() {
		if err := coreSvc.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	if opts.Cleanup {
		deleted, err := coreSvc.DeleteAllHistoryReleases(ctx)
		if err != nil {
			return exitError(1, err)
		}
		fmt.Printf("deleted stored database content for %d release(s)\n", deleted)
		return nil
	}

	if opts.DeleteTmp {
		paths, err = normalizeCLIPaths(ctx, paths)
		if err != nil {
			return exitError(1, err)
		}
		if err := deleteCLIStoredReleases(ctx, coreSvc, paths); err != nil {
			return exitError(1, err)
		}
	}

	if strings.TrimSpace(opts.QueueName) != "" {
		if len(paths) != 1 {
			return exitError(2, errors.New("--queue requires exactly one queue root path"))
		}
		queuePaths, err := filesystem.GatherQueuePaths(ctx, paths[0])
		if err != nil {
			return exitError(1, err)
		}
		paths = filesystem.LimitQueuePaths(queuePaths, opts.LimitQueue)
		if len(paths) == 0 {
			return exitError(1, fmt.Errorf("queue %q resolved to no upload candidates", opts.QueueName))
		}
	}

	// Handle BDMV playlist selection before upload
	if err := handleBDMVPlaylistSelection(ctx, paths, coreSvc, cfg, logger); err != nil {
		return exitError(1, err)
	}

	if opts.UploadOnly {
		uploadReq, err := buildCLIRequest(opts, visitedFlags, paths, screens)
		if err != nil {
			return exitError(1, err)
		}
		if opts.Debug {
			for _, sourcePath := range paths {
				debugReq := uploadReq
				debugReq.Paths = []string{sourcePath}
				review, err := coreSvc.BuildUploadReview(ctx, debugReq)
				if err != nil {
					return exitError(1, err)
				}
				printDebugUploadReview(review)
			}
		}
		if _, err := coreSvc.RunUploadPrepared(ctx, uploadReq); err != nil {
			return exitError(1, err)
		}
		return nil
	}

	for _, sourcePath := range paths {
		if opts.SiteCheck {
			if err := runSiteCheckCLIPath(ctx, coreSvc, opts, visitedFlags, sourcePath, screens); err != nil {
				return exitError(1, err)
			}
			continue
		}
		if err := runInteractiveCLIPath(ctx, coreSvc, os.Args[1:], opts, visitedFlags, sourcePath, screens); err != nil {
			return exitError(1, err)
		}
	}
	return nil
}

func runServe(args []string) error {
	opts, visitedFlags, err := parseServeOptions(args)
	if err != nil {
		return err
	}

	configFlagProvided := visitedFlags["config"]
	resolvedConfigPath, err := resolveConfigPath(opts.ConfigPath, configFlagProvided)
	if err != nil {
		return err
	}

	cfg, dbPath, err := loadServeConfig(resolvedConfigPath, configFlagProvided)
	if err != nil {
		return err
	}

	webCfg, err := webserver.LoadCLIConfig(dbPath)
	if err != nil {
		return err
	}
	if err := webserver.SaveCLIConfig(dbPath, webCfg); err != nil {
		return err
	}

	server, err := webserver.New(webserver.Options{
		Config:    cfg,
		CLIConfig: webCfg,
	})
	if err != nil {
		return err
	}
	defer server.Close()

	return server.Run(context.Background())
}

func loadCLIConfig(configPath string, configProvided bool) (config.Config, string, error) {
	ctx := context.Background()
	if configProvided {
		resolved, err := resolveConfigPath(configPath, configProvided)
		if err != nil {
			return config.Config{}, "", err
		}
		loaded, err := config.ImportFromYAML(resolved)
		if err != nil {
			return config.Config{}, "", err
		}
		config.ApplyEnvOverrides(loaded)
		cfg := *loaded
		dbPath := strings.TrimSpace(cfg.MainSettings.DBPath)
		if dbPath == "" {
			dbPath, err = db.DefaultPath()
			if err != nil {
				return config.Config{}, "", fmt.Errorf("default db path: %w", err)
			}
			cfg.MainSettings.DBPath = dbPath
		}
		if err := cfg.Validate(); err != nil {
			return config.Config{}, "", err
		}
		if err := saveConfigToDatabase(ctx, &cfg, dbPath); err != nil {
			return config.Config{}, "", err
		}
		return cfg, dbPath, nil
	}

	defaultDBPath, err := db.DefaultPath()
	if err != nil {
		return config.Config{}, "", fmt.Errorf("default db path: %w", err)
	}

	cfg, err := loadConfigFromDatabase(ctx, defaultDBPath)
	if err == nil {
		if strings.TrimSpace(cfg.MainSettings.DBPath) == "" || cfg.MainSettings.DBPath != defaultDBPath {
			cfg.MainSettings.DBPath = defaultDBPath
			if err := saveConfigToDatabase(ctx, &cfg, defaultDBPath); err != nil {
				return config.Config{}, "", err
			}
		}
		if err := cfg.Validate(); err != nil {
			return config.Config{}, "", err
		}
		return cfg, defaultDBPath, nil
	}
	if !errors.Is(err, internalerrors.ErrNotFound) {
		return config.Config{}, "", err
	}

	resolved, err := resolveConfigPath(configPath, configProvided)
	if err != nil {
		return config.Config{}, "", err
	}
	loaded, err := loadConfigFromPathOrEmbedded(resolved)
	if err != nil {
		return config.Config{}, "", err
	}
	config.ApplyEnvOverrides(loaded)
	cfg = *loaded

	fallbackDBPath := strings.TrimSpace(cfg.MainSettings.DBPath)
	if fallbackDBPath == "" {
		fallbackDBPath = defaultDBPath
	}

	if fallbackDBPath != defaultDBPath {
		fallbackCfg, err := loadConfigFromDatabase(ctx, fallbackDBPath)
		if err == nil {
			if err := fallbackCfg.Validate(); err != nil {
				return config.Config{}, "", err
			}
			return fallbackCfg, fallbackDBPath, nil
		}
		if !errors.Is(err, internalerrors.ErrNotFound) {
			return config.Config{}, "", err
		}
	}

	cfg.MainSettings.DBPath = fallbackDBPath
	if err := cfg.Validate(); err != nil {
		return config.Config{}, "", err
	}
	if err := saveConfigToDatabase(ctx, &cfg, fallbackDBPath); err != nil {
		return config.Config{}, "", err
	}
	return cfg, fallbackDBPath, nil
}

// loadServeConfig loads config for the web server without requiring a fully
// valid config (e.g. tmdb_api). The web UI handles initial setup, so the
// server must be able to start even on a fresh install with no config yet.
func loadServeConfig(configPath string, configProvided bool) (config.Config, string, error) {
	ctx := context.Background()
	if configProvided {
		resolved, err := resolveConfigPath(configPath, configProvided)
		if err != nil {
			return config.Config{}, "", err
		}
		loaded, err := config.ImportFromYAML(resolved)
		if err != nil {
			return config.Config{}, "", err
		}
		config.ApplyEnvOverrides(loaded)
		cfg := *loaded
		dbPath := strings.TrimSpace(cfg.MainSettings.DBPath)
		if dbPath == "" {
			dbPath, err = db.DefaultPath()
			if err != nil {
				return config.Config{}, "", fmt.Errorf("default db path: %w", err)
			}
			cfg.MainSettings.DBPath = dbPath
		}
		// Do not persist the imported YAML to the database. Writing it back would
		// overwrite previously valid database-backed config with zero values for
		// any fields omitted from the YAML file. Use it for this process only.
		return cfg, dbPath, nil
	}

	defaultDBPath, err := db.DefaultPath()
	if err != nil {
		return config.Config{}, "", fmt.Errorf("default db path: %w", err)
	}

	cfg, err := loadConfigFromDatabase(ctx, defaultDBPath)
	if err == nil {
		if strings.TrimSpace(cfg.MainSettings.DBPath) == "" || cfg.MainSettings.DBPath != defaultDBPath {
			cfg.MainSettings.DBPath = defaultDBPath
			if err := saveConfigToDatabase(ctx, &cfg, defaultDBPath); err != nil {
				return config.Config{}, "", err
			}
		}
		return cfg, defaultDBPath, nil
	}
	if !errors.Is(err, internalerrors.ErrNotFound) {
		return config.Config{}, "", err
	}

	// No database yet — use embedded defaults. The server will start with an
	// empty/default config and wait for the user to configure via the UI.
	loaded, err := loadConfigFromPathOrEmbedded(configPath)
	if err != nil {
		return config.Config{}, "", err
	}
	config.ApplyEnvOverrides(loaded)
	cfg = *loaded

	fallbackDBPath := strings.TrimSpace(cfg.MainSettings.DBPath)
	if fallbackDBPath == "" {
		fallbackDBPath = defaultDBPath
	}

	cfg.MainSettings.DBPath = fallbackDBPath
	return cfg, fallbackDBPath, nil
}

func loadConfigFromDatabase(ctx context.Context, dbPath string) (config.Config, error) {
	repo, err := db.Open(dbPath)
	if err != nil {
		return config.Config{}, err
	}
	defer repo.Close()

	if err := repo.Migrate(); err != nil {
		return config.Config{}, err
	}

	loaded, err := config.LoadFromDatabase(ctx, repo)
	if err != nil {
		return config.Config{}, err
	}
	config.ApplyEnvOverrides(loaded)
	return *loaded, nil
}

func saveConfigToDatabase(ctx context.Context, cfg *config.Config, dbPath string) error {
	repo, err := db.Open(dbPath)
	if err != nil {
		return err
	}
	defer repo.Close()

	if err := repo.Migrate(); err != nil {
		return err
	}

	if err := config.SaveToDatabase(ctx, cfg, repo); err != nil {
		return err
	}

	return nil
}

func exportConfigToYAML(ctx context.Context, configPath string, configProvided bool, outputPath string) error {
	dbPath, err := resolveExportDBPath(configPath, configProvided)
	if err != nil {
		return err
	}

	repo, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open config database: %w", err)
	}
	defer repo.Close()

	if err := repo.Migrate(); err != nil {
		return fmt.Errorf("migrate config database: %w", err)
	}

	if err := config.ExportFromDatabaseToYAML(ctx, outputPath, repo); err != nil {
		return err
	}

	return nil
}

func resolveExportDBPath(configPath string, configProvided bool) (string, error) {
	if configProvided {
		resolvedConfigPath, err := resolveConfigPath(configPath, configProvided)
		if err != nil {
			return "", err
		}

		loaded, err := config.ImportFromYAML(resolvedConfigPath)
		if err != nil {
			return "", fmt.Errorf("load config for database path: %w", err)
		}
		config.ApplyEnvOverrides(loaded)

		if configuredDBPath := strings.TrimSpace(loaded.MainSettings.DBPath); configuredDBPath != "" {
			return configuredDBPath, nil
		}
	}

	defaultDBPath, err := db.DefaultPath()
	if err != nil {
		return "", fmt.Errorf("default db path: %w", err)
	}

	return defaultDBPath, nil
}

type releaseOverrideInput struct {
	Category     string
	Type         string
	Source       string
	Resolution   string
	Tag          string
	Service      string
	Edition      string
	Season       string
	Episode      string
	EpisodeTitle string
	ManualYear   int
	ManualDate   string
	NoSeason     bool
	NoYear       bool
	NoAKA        bool
	NoTag        bool
	NoEdition    bool
	NoDub        bool
	NoDual       bool
	DualAudio    bool
	Region       string
}

func buildReleaseNameOverrides(visited map[string]bool, input releaseOverrideInput) api.ReleaseNameOverrides {
	overrides := api.ReleaseNameOverrides{}
	if visited["category"] {
		overrides.Category = stringPtr(input.Category)
	}
	if visited["type"] {
		overrides.Type = stringPtr(input.Type)
	}
	if visited["source"] {
		overrides.Source = stringPtr(input.Source)
	}
	if visited["resolution"] {
		overrides.Resolution = stringPtr(input.Resolution)
	}
	if visited["tag"] {
		overrides.Tag = stringPtr(input.Tag)
	}
	if visited["service"] {
		overrides.Service = stringPtr(input.Service)
	}
	if visited["edition"] {
		overrides.Edition = stringPtr(input.Edition)
	}
	if visited["season"] {
		overrides.Season = stringPtr(input.Season)
	}
	if visited["episode"] {
		overrides.Episode = stringPtr(input.Episode)
	}
	if visited["episode-title"] {
		overrides.EpisodeTitle = stringPtr(input.EpisodeTitle)
	}
	if visited["manual-year"] {
		overrides.ManualYear = intPtr(input.ManualYear)
	}
	if visited["daily"] {
		overrides.ManualDate = stringPtr(input.ManualDate)
	}
	if visited["no-season"] {
		overrides.NoSeason = boolPtr(input.NoSeason)
	}
	if visited["no-year"] {
		overrides.NoYear = boolPtr(input.NoYear)
	}
	if visited["no-aka"] {
		overrides.NoAKA = boolPtr(input.NoAKA)
	}
	if visited["no-tag"] {
		overrides.NoTag = boolPtr(input.NoTag)
	}
	if visited["no-edition"] {
		overrides.NoEdition = boolPtr(input.NoEdition)
	}
	if visited["no-dub"] {
		overrides.NoDub = boolPtr(input.NoDub)
	}
	if visited["no-dual"] {
		overrides.NoDual = boolPtr(input.NoDual)
	}
	if visited["dual-audio"] {
		overrides.DualAudio = boolPtr(input.DualAudio)
	}
	if visited["region"] {
		overrides.Region = stringPtr(input.Region)
	}
	return overrides
}

func stringPtr(value string) *string {
	copy := value
	return &copy
}

func intPtr(value int) *int {
	copy := value
	return &copy
}

func boolPtr(value bool) *bool {
	copy := value
	return &copy
}

func resolveConfigPath(configPath string, configFlagProvided bool) (string, error) {
	if configFlagProvided {
		if strings.TrimSpace(configPath) == "" {
			return "", errors.New("config path is required when --config is provided")
		}
		return configPath, nil
	}

	defaultPath, err := defaultConfigPath()
	if err != nil {
		return "", err
	}
	return defaultPath, nil
}

func loadConfigFromPathOrEmbedded(path string) (*config.Config, error) {
	if strings.TrimSpace(path) != "" {
		if _, err := os.Stat(path); err == nil {
			return config.ImportFromYAML(path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("check config: %w", err)
		}
	}

	loaded, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		return nil, fmt.Errorf("load embedded config: %w", err)
	}
	return loaded, nil
}

func defaultConfigPath() (string, error) {
	defaultDBPath, err := db.DefaultPath()
	if err != nil {
		return "", fmt.Errorf("default db path: %w", err)
	}
	return filepath.Join(filepath.Dir(defaultDBPath), defaultConfigName), nil
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func normalizeCLIPaths(ctx context.Context, paths []string) ([]string, error) {
	validator := filesystem.NewValidator()
	normalized, err := validator.ValidatePaths(ctx, paths)
	if err != nil {
		return nil, err
	}
	return normalized, nil
}

func deleteCLIStoredReleases(ctx context.Context, coreSvc api.Core, paths []string) error {
	for _, sourcePath := range paths {
		if err := coreSvc.DeleteHistoryRelease(ctx, sourcePath); err != nil {
			return fmt.Errorf("delete stored data for %q: %w", sourcePath, err)
		}
		fmt.Printf("deleted stored database content for %s\n", sourcePath)
	}
	return nil
}
func handleBDMVPlaylistSelection(ctx context.Context, paths []string, coreSvc api.Core, cfg config.Config, logger api.Logger) error {
	if len(paths) == 0 {
		return nil
	}

	for _, path := range paths {
		// Check if this path is a BDMV folder
		discType, err := filesystem.DetectDiscType(ctx, path)
		if err != nil {
			logger.Debugf("cli: disc type detection failed for %s: %v", path, err)
			continue
		}

		if discType != "BDMV" {
			continue
		}

		logger.Infof("cli: BDMV disc detected at %s", path)

		// Normalize to absolute path for consistency
		absPath, err := filepath.Abs(path)
		if err != nil {
			logger.Warnf("cli: resolve path %s: %v", path, err)
			continue
		}

		// Check if playlist selection is already persisted
		_, err = coreSvc.LoadPlaylistSelection(ctx, absPath)
		if err == nil {
			logger.Infof("cli: using previously saved playlist selection for %s", absPath)
			continue
		}
		if !errors.Is(err, internalerrors.ErrNotFound) {
			logger.Warnf("cli: load playlist selection: %v", err)
			continue
		}

		// No selection exists; check if we should auto-select
		if cfg.Metadata.UseLargestPlaylist {
			logger.Infof("cli: auto-selecting largest playlist (use_largest_playlist enabled)")

			playlists, err := coreSvc.DiscoverPlaylists(ctx, absPath)
			if err != nil {
				logger.Warnf("cli: discover playlists: %v", err)
				continue
			}

			if len(playlists) > 0 {
				// Save the best (highest-scoring) playlist
				selected := []string{playlists[0].File}
				if err := coreSvc.SavePlaylistSelection(ctx, absPath, selected, false); err != nil {
					logger.Warnf("cli: save playlist selection: %v", err)
				} else {
					logger.Infof("cli: auto-selected playlist %s (score: %.2f)", playlists[0].File, playlists[0].Score)
				}
			}
			continue
		}

		// Interactive selection required
		logger.Infof("cli: discovering playlists for %s", absPath)
		playlists, err := coreSvc.DiscoverPlaylists(ctx, absPath)
		if err != nil {
			logger.Warnf("cli: discover playlists: %v", err)
			continue
		}

		if len(playlists) == 0 {
			logger.Warnf("cli: no playlists found for %s", absPath)
			continue
		}

		logger.Infof("cli: found %d playlists", len(playlists))

		// Display top playlists and prompt user
		if len(playlists) == 1 {
			fmt.Printf("[*] Only one playlist found: %s (%.0fs, score: %.2f)\n", playlists[0].File, playlists[0].Duration, playlists[0].Score)
			fmt.Printf("[*] Auto-selecting...\n")
			if err := coreSvc.SavePlaylistSelection(ctx, absPath, []string{playlists[0].File}, false); err != nil {
				logger.Warnf("cli: save playlist selection: %v", err)
			}
		} else {
			// Display top 5 playlists
			topCount := len(playlists)
			if topCount > 5 {
				topCount = 5
			}

			fmt.Printf("\nAvailable playlists for %s:\n", absPath)
			for i := 0; i < topCount; i++ {
				p := playlists[i]
				durationStr := formatDuration(p.Duration)
				fmt.Printf("[%d] %s (%s, score: %.2f)\n", i, p.File, durationStr, p.Score)
			}

			// Prompt user for selection
			for {
				fmt.Printf("\nEnter playlist numbers (comma-separated), 'ALL' to select all top %d, or press Enter to auto-select best: ", topCount)
				var input string
				n, err := fmt.Scanln(&input)
				if err != nil && err.Error() != "unexpected newline" {
					logger.Warnf("cli: read input: %v", err)
					break
				}
				if n == 0 || strings.TrimSpace(input) == "" {
					// Auto-select best
					if err := coreSvc.SavePlaylistSelection(ctx, absPath, []string{playlists[0].File}, false); err != nil {
						logger.Warnf("cli: save playlist selection: %v", err)
					} else {
						fmt.Printf("[*] Auto-selected best playlist: %s\n", playlists[0].File)
					}
					break
				}

				input = strings.TrimSpace(input)
				if strings.ToLower(input) == "all" {
					var selected []string
					for i := 0; i < topCount; i++ {
						selected = append(selected, playlists[i].File)
					}
					if err := coreSvc.SavePlaylistSelection(ctx, absPath, selected, true); err != nil {
						logger.Warnf("cli: save playlist selection: %v", err)
					} else {
						fmt.Printf("[*] Selected all %d playlists\n", len(selected))
					}
					break
				}

				// Parse individual selections
				indices := strings.Split(input, ",")
				var selected []string
				valid := true
				for _, idx := range indices {
					idx = strings.TrimSpace(idx)
					var num int
					_, err := fmt.Sscanf(idx, "%d", &num)
					if err != nil || num < 0 || num >= topCount {
						fmt.Printf("[!] Invalid index: %s\n", idx)
						valid = false
						break
					}
					selected = append(selected, playlists[num].File)
				}

				if valid && len(selected) > 0 {
					if err := coreSvc.SavePlaylistSelection(ctx, absPath, selected, false); err != nil {
						logger.Warnf("cli: save playlist selection: %v", err)
					} else {
						fmt.Printf("[*] Selected %d playlist(s)\n", len(selected))
					}
					break
				}

				fmt.Printf("[!] Please try again.\n")
			}
		}
	}

	return nil
}

func formatDuration(seconds float64) string {
	h := int(seconds) / 3600
	m := (int(seconds) % 3600) / 60
	s := int(seconds) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func importConfig(ctx context.Context, importPath, configPath string, configProvided bool) error {
	cfg, warnings, err := importer.ImportFromFile(importPath)
	if err != nil {
		return fmt.Errorf("import config: %w", err)
	}

	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}

	dbPath, err := resolveExportDBPath(configPath, configProvided)
	if err != nil {
		return fmt.Errorf("resolve database path: %w", err)
	}

	cfg.MainSettings.DBPath = dbPath
	config.ApplyEnvOverrides(cfg)

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validate imported config: %w", err)
	}

	if err := saveConfigToDatabase(ctx, cfg, dbPath); err != nil {
		return fmt.Errorf("save imported config: %w", err)
	}

	fmt.Printf("imported config from %s (%d warnings)\n", importPath, len(warnings))
	return nil
}
