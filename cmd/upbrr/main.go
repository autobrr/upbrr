// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/config/importer"
	"github.com/autobrr/upbrr/internal/configstore"
	"github.com/autobrr/upbrr/internal/core"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/webserver"
	"github.com/autobrr/upbrr/pkg/api"
)

var version = "dev"

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
	api.SetApplicationBuild(version, "")

	if len(os.Args) > 1 && os.Args[1] == "serve" {
		if err := runServe(os.Args[2:]); err != nil {
			var helpErr *cliHelpError
			if errors.As(err, &helpErr) {
				fmt.Fprint(os.Stdout, helpErr.Usage())
				return nil
			}
			return exitError(1, err)
		}
		return nil
	}

	opts, visitedFlags, paths, err := parseCLIOptions(os.Args[1:])
	if err != nil {
		var helpErr *cliHelpError
		if errors.As(err, &helpErr) {
			fmt.Fprint(os.Stdout, helpErr.Usage())
			return nil
		}
		return exitError(2, err)
	}

	configFlagProvided := visitedFlags["config"]

	if opts.ShowVersion {
		fmt.Printf("upbrr %s\n", version)
		return nil
	}

	if strings.TrimSpace(opts.ExportConfigPath) != "" && strings.TrimSpace(opts.ImportConfigPath) != "" {
		return exitError(2, errors.New("--export-config and --import-config cannot be used together"))
	}
	if opts.CreateAuth && strings.TrimSpace(opts.ExportConfigPath) != "" {
		return exitError(2, errors.New("--create-auth and --export-config cannot be used together"))
	}
	if opts.CreateAuth && strings.TrimSpace(opts.ImportConfigPath) != "" {
		return exitError(2, errors.New("--create-auth and --import-config cannot be used together"))
	}

	if opts.CreateAuth {
		dbPath, err := resolveExportDBPath(opts.ConfigPath, configFlagProvided)
		if err != nil {
			return exitError(1, err)
		}
		if err := createCLIAuthFile(os.Stdin, os.Stdout, dbPath); err != nil {
			return exitError(1, err)
		}
		fmt.Printf("created %s\n", webserver.AuthFilePath(dbPath))
		return nil
	}

	ctx := context.Background()
	if strings.TrimSpace(opts.ExportConfigPath) != "" {
		if err := exportConfigToYAML(ctx, opts.ConfigPath, configFlagProvided, opts.ExportConfigPath, opts.ExportConfigPlaintext); err != nil {
			return exitError(1, err)
		}
		fmt.Printf("exported config to %s\n", opts.ExportConfigPath)
		return nil
	}

	if strings.TrimSpace(opts.ImportConfigPath) != "" {
		if err := importConfig(ctx, opts.ImportConfigPath, opts.ConfigPath, configFlagProvided); err != nil {
			return exitError(1, err)
		}
		return nil
	}

	resolvedConfigPath, err := configstore.ResolveYAMLPath(opts.ConfigPath, configFlagProvided)
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	ctx = withCLIUploadProgressLogger(ctx, logger)
	coreSvc, err := core.NewWithContext(ctx, api.CoreDependencies{
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

	if opts.Cleanup {
		deleted, err := coreSvc.DeleteAllHistoryReleases(ctx)
		if err != nil {
			return exitError(1, err)
		}
		fmt.Printf("deleted stored database content for %d release(s)\n", deleted)
		return nil
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

	if opts.DeleteTmp {
		paths, err = normalizeCLIPaths(ctx, paths)
		if err != nil {
			return exitError(1, err)
		}
		if err := deleteCLIStoredReleases(ctx, coreSvc, paths); err != nil {
			return exitError(1, err)
		}
	}

	// Handle BDMV playlist selection before upload
	if err := handleBDMVPlaylistSelection(ctx, paths, coreSvc, cfg, logger, opts); err != nil {
		return exitError(1, err)
	}

	if opts.UploadOnly {
		uploadReq, err := buildCLIRequest(opts, visitedFlags, paths, screens)
		if err != nil {
			return exitError(1, err)
		}
		uploadReq, err = prepareCLIUploadMetadata(ctx, coreSvc, uploadReq)
		if err != nil {
			return exitError(1, err)
		}
		if opts.Debug {
			reviews, err := buildCLIUploadDebugReviews(ctx, coreSvc, paths, uploadReq)
			if err != nil {
				return exitError(1, err)
			}
			for _, review := range reviews {
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
		if err := runInteractiveCLIPath(ctx, coreSvc, os.Args[1:], opts, visitedFlags, sourcePath, screens, cfg); err != nil {
			return exitError(1, err)
		}
	}
	return nil
}

func createCLIAuthFile(stdin io.Reader, stdout io.Writer, dbPath string) error {
	if stdin == nil {
		return errors.New("create auth: nil stdin")
	}
	if stdout == nil {
		return errors.New("create auth: nil stdout")
	}

	reader := bufio.NewReader(stdin)

	username, err := promptAuthValue(reader, stdout, "Username: ")
	if err != nil {
		return err
	}
	password, err := promptAuthPassword(stdin, reader, stdout, "Password: ")
	if err != nil {
		return err
	}
	confirm, err := promptAuthPassword(stdin, reader, stdout, "Confirm password: ")
	if err != nil {
		return err
	}
	if password != confirm {
		return errors.New("create auth: passwords do not match")
	}
	if len(password) < webserver.AuthPasswordMinLength {
		return fmt.Errorf("create auth: password too short (minimum %d characters)", webserver.AuthPasswordMinLength)
	}
	if err := webserver.BootstrapAuthFile(dbPath, username, password); err != nil {
		return fmt.Errorf("upbrr: %w", err)
	}
	return nil
}

func promptAuthValue(reader *bufio.Reader, stdout io.Writer, label string) (string, error) {
	if _, err := fmt.Fprint(stdout, label); err != nil {
		return "", fmt.Errorf("create auth: write prompt: %w", err)
	}
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("create auth: read prompt: %w", err)
	}
	value := strings.TrimSpace(line)
	if value == "" {
		return "", errors.New("create auth: value cannot be empty")
	}
	return value, nil
}

func promptAuthPassword(stdin io.Reader, reader *bufio.Reader, stdout io.Writer, label string) (string, error) {
	if _, err := fmt.Fprint(stdout, label); err != nil {
		return "", fmt.Errorf("create auth: write password prompt: %w", err)
	}
	if file, ok := stdin.(*os.File); ok {
		fd, ok := terminalFileDescriptor(file)
		if ok && term.IsTerminal(fd) {
			raw, err := term.ReadPassword(fd)
			if err != nil {
				return "", fmt.Errorf("create auth: read password: %w", err)
			}
			if _, err := fmt.Fprintln(stdout); err != nil {
				return "", fmt.Errorf("create auth: finish password prompt: %w", err)
			}
			value := strings.TrimSpace(string(raw))
			if value == "" {
				return "", errors.New("create auth: password cannot be empty")
			}
			return value, nil
		}
	}

	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("create auth: read password: %w", err)
	}
	value := strings.TrimSpace(line)
	if value == "" {
		return "", errors.New("create auth: password cannot be empty")
	}
	return value, nil
}

func terminalFileDescriptor(file *os.File) (int, bool) {
	fd, err := strconv.Atoi(fmt.Sprint(file.Fd()))
	if err != nil {
		return 0, false
	}
	return fd, true
}

func runServe(args []string) error {
	opts, visitedFlags, err := parseServeOptions(args)
	if err != nil {
		return err
	}

	configFlagProvided := visitedFlags["config"]
	resolvedConfigPath, err := configstore.ResolveYAMLPath(opts.ConfigPath, configFlagProvided)
	if err != nil {
		return fmt.Errorf("upbrr: %w", err)
	}

	cfg, dbPath, err := loadServeConfig(resolvedConfigPath, configFlagProvided)
	if err != nil {
		return err
	}

	webCfg, err := webserver.LoadCLIConfig(dbPath)
	if err != nil {
		return fmt.Errorf("upbrr: %w", err)
	}
	if err := webserver.SaveCLIConfig(dbPath, webCfg); err != nil {
		return fmt.Errorf("upbrr: %w", err)
	}
	if opts.DevNoAuth {
		webCfg.OpenBrowser = false
	}

	server, err := webserver.New(webserver.Options{
		Config:            cfg,
		CLIConfig:         webCfg,
		DevelopmentNoAuth: opts.DevNoAuth,
	})
	if err != nil {
		return fmt.Errorf("upbrr: %w", err)
	}
	defer server.Close()

	return wrapUpbrrError(server.Run(context.Background()))
}

func loadCLIConfig(configPath string, configProvided bool) (config.Config, string, error) {
	cfg, dbPath, err := configstore.Bootstrap(context.Background(), configPath, configProvided, true)
	if err != nil {
		return config.Config{}, "", fmt.Errorf("upbrr: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return config.Config{}, "", fmt.Errorf("upbrr: %w", err)
	}
	return cfg, dbPath, nil
}

// loadServeConfig loads config for the web server without requiring a fully
// valid config (e.g. tmdb_api). The web UI handles initial setup, so the
// server must be able to start even on a fresh install with no config yet.
func loadServeConfig(configPath string, configProvided bool) (config.Config, string, error) {
	return wrapUpbrrResult2(configstore.Bootstrap(context.Background(), configPath, configProvided, false))
}

func exportConfigToYAML(ctx context.Context, configPath string, configProvided bool, outputPath string, plaintext bool) error {
	dbPath, err := resolveExportDBPath(configPath, configProvided)
	if err != nil {
		return err
	}

	repo, err := db.OpenContext(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("open config database: %w", err)
	}
	defer repo.Close()

	if err := repo.MigrateContext(ctx); err != nil {
		return fmt.Errorf("migrate config database: %w", err)
	}

	if plaintext {
		if err := config.ExportFromDatabaseToPlaintextYAML(ctx, outputPath, repo); err != nil {
			return fmt.Errorf("upbrr: %w", err)
		}
		return nil
	}

	if err := config.ExportFromDatabaseToYAML(ctx, outputPath, repo); err != nil {
		return fmt.Errorf("upbrr: %w", err)
	}

	return nil
}

func resolveExportDBPath(configPath string, configProvided bool) (string, error) {
	if configProvided {
		resolvedConfigPath, err := configstore.ResolveYAMLPath(configPath, configProvided)
		if err != nil {
			return "", fmt.Errorf("upbrr: %w", err)
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
	ptrValue := value
	return &ptrValue
}

func intPtr(value int) *int {
	ptrValue := value
	return &ptrValue
}

func boolPtr(value bool) *bool {
	ptrValue := value
	return &ptrValue
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
		return nil, fmt.Errorf("upbrr: %w", err)
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

func prepareCLIUploadMetadata(ctx context.Context, coreSvc api.Core, req api.Request) (api.Request, error) {
	resolvedReq := req
	resolvedPaths := make([]string, 0, len(req.Paths))
	resolvedSelections := req.ExternalIDSelections
	for _, sourcePath := range req.Paths {
		singleReq := req
		singleReq.Paths = []string{sourcePath}
		singleReq.ExternalIDSelections = resolvedSelections
		preview, err := coreSvc.FetchMetadataPreview(ctx, singleReq)
		if err != nil {
			return api.Request{}, fmt.Errorf("upbrr: %w", err)
		}
		resolvedPath := resolvedCLIMetadataSourcePath(sourcePath, preview)
		resolvedSelections = cloneCLIExternalIDSelectionsForResolvedPath(resolvedSelections, sourcePath, resolvedPath)
		if shouldRefreshCLIResolvedMetadataPreview(singleReq, sourcePath, resolvedPath) {
			resolvedReq := singleReq
			resolvedReq.Paths = []string{resolvedPath}
			resolvedReq.ExternalIDSelections = resolvedSelections
			preview, err = coreSvc.FetchMetadataPreview(ctx, resolvedReq)
			if err != nil {
				return api.Request{}, fmt.Errorf("upbrr: %w", err)
			}
			resolvedPath = resolvedCLIMetadataSourcePath(resolvedPath, preview)
			resolvedSelections = cloneCLIExternalIDSelectionsForResolvedPath(resolvedSelections, sourcePath, resolvedPath)
		}
		resolvedPaths = append(resolvedPaths, resolvedPath)
	}
	resolvedReq.Paths = resolvedPaths
	resolvedReq.ExternalIDSelections = resolvedSelections
	return resolvedReq, nil
}

func shouldRefreshCLIResolvedMetadataPreview(req api.Request, sourcePath string, resolvedPath string) bool {
	trimmedSourcePath := strings.TrimSpace(sourcePath)
	trimmedResolvedPath := strings.TrimSpace(resolvedPath)
	if trimmedSourcePath == "" || trimmedResolvedPath == "" {
		return false
	}
	if filepath.Clean(trimmedSourcePath) == filepath.Clean(trimmedResolvedPath) {
		return false
	}
	if cliHasExternalIDOverrides(req.ExternalIDOverrides) {
		return true
	}
	_, ok := resolveCLIExternalIDSelection(req.ExternalIDSelections, sourcePath)
	return ok
}

func cliHasExternalIDOverrides(overrides api.ExternalIDOverrides) bool {
	return overrides.TMDBID != nil ||
		overrides.IMDBID != nil ||
		overrides.TVDBID != nil ||
		overrides.TVmazeID != nil ||
		overrides.MALID != nil
}

func buildCLIUploadDebugReviews(ctx context.Context, coreSvc api.Core, sourcePaths []string, uploadReq api.Request) ([]api.UploadReview, error) {
	reviews := make([]api.UploadReview, 0, len(sourcePaths))
	for idx, sourcePath := range sourcePaths {
		resolvedPath := sourcePath
		if idx < len(uploadReq.Paths) && strings.TrimSpace(uploadReq.Paths[idx]) != "" {
			resolvedPath = uploadReq.Paths[idx]
		}
		debugReq := uploadReq
		debugReq.Paths = []string{resolvedPath}
		debugReq.ExternalIDSelections = cloneCLIExternalIDSelectionsForResolvedPath(uploadReq.ExternalIDSelections, sourcePath, resolvedPath)
		review, err := coreSvc.BuildUploadReview(ctx, debugReq)
		if err != nil {
			return nil, fmt.Errorf("build upload review for %q: %w", resolvedPath, err)
		}
		if strings.TrimSpace(sourcePath) != "" {
			review.SourcePath = sourcePath
		}
		reviews = append(reviews, review)
	}
	return reviews, nil
}

func cloneCLIExternalIDSelectionsForResolvedPath(selections map[string]api.ExternalIDSelection, sourcePath string, resolvedPath string) map[string]api.ExternalIDSelection {
	if len(selections) == 0 {
		return selections
	}
	trimmedSourcePath := strings.TrimSpace(sourcePath)
	trimmedResolvedPath := strings.TrimSpace(resolvedPath)
	if trimmedResolvedPath == "" || trimmedSourcePath == "" {
		return selections
	}
	if filepath.Clean(trimmedSourcePath) == filepath.Clean(trimmedResolvedPath) {
		return selections
	}
	selected, ok := resolveCLIExternalIDSelection(selections, sourcePath)
	if !ok {
		return selections
	}
	if _, ok := resolveCLIExternalIDSelection(selections, trimmedResolvedPath); ok {
		return selections
	}
	cloned := make(map[string]api.ExternalIDSelection, len(selections)+1)
	maps.Copy(cloned, selections)
	cloned[trimmedResolvedPath] = selected
	return cloned
}

func resolveCLIExternalIDSelection(selections map[string]api.ExternalIDSelection, sourcePath string) (api.ExternalIDSelection, bool) {
	if len(selections) == 0 {
		return api.ExternalIDSelection{}, false
	}
	if selected, ok := selections[sourcePath]; ok {
		return selected, true
	}
	cleanedSourcePath := filepath.Clean(sourcePath)
	if selected, ok := selections[cleanedSourcePath]; ok {
		return selected, true
	}
	for key, selected := range selections {
		if filepath.Clean(key) == cleanedSourcePath {
			return selected, true
		}
	}
	return api.ExternalIDSelection{}, false
}

func handleBDMVPlaylistSelection(ctx context.Context, paths []string, coreSvc api.Core, cfg config.Config, logger api.Logger, opts cliOptions) error {
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
				if opts.Unattended && !opts.UnattendedConfirm {
					return fmt.Errorf("upbrr: unattended BDMV playlist discovery failed for %s: %w", absPath, err)
				}
				logger.Warnf("cli: discover playlists: %v", err)
				continue
			}

			if len(playlists) == 0 {
				if opts.Unattended && !opts.UnattendedConfirm {
					return fmt.Errorf("upbrr: unattended BDMV upload found no playlists for %s", absPath)
				}
				continue
			}
			// Save the best (highest-scoring) playlist
			selected := []string{playlists[0].File}
			if err := coreSvc.SavePlaylistSelection(ctx, absPath, selected, false); err != nil {
				if opts.Unattended && !opts.UnattendedConfirm {
					return fmt.Errorf("upbrr: unattended BDMV playlist selection save failed for %s: %w", absPath, err)
				}
				logger.Warnf("cli: save playlist selection: %v", err)
			} else {
				logger.Infof("cli: auto-selected playlist %s (score: %.2f)", playlists[0].File, playlists[0].Score)
			}
			continue
		}

		// Interactive selection required
		logger.Infof("cli: discovering playlists for %s", absPath)
		playlists, err := coreSvc.DiscoverPlaylists(ctx, absPath)
		if err != nil {
			if opts.Unattended && !opts.UnattendedConfirm {
				return fmt.Errorf("upbrr: unattended BDMV playlist discovery failed for %s: %w", absPath, err)
			}
			logger.Warnf("cli: discover playlists: %v", err)
			continue
		}

		if len(playlists) == 0 {
			if opts.Unattended && !opts.UnattendedConfirm {
				return fmt.Errorf("upbrr: unattended BDMV upload found no playlists for %s", absPath)
			}
			logger.Warnf("cli: no playlists found for %s", absPath)
			continue
		}

		logger.Infof("cli: found %d playlists", len(playlists))
		if opts.Unattended && !opts.UnattendedConfirm && len(playlists) > 1 {
			return fmt.Errorf("upbrr: unattended BDMV upload requires a saved playlist selection or use_largest_playlist for %s", absPath)
		}

		// Display top playlists and prompt user
		if len(playlists) == 1 {
			fmt.Printf("[*] Only one playlist found: %s (%.0fs, score: %.2f)\n", playlists[0].File, playlists[0].Duration, playlists[0].Score)
			fmt.Printf("[*] Auto-selecting...\n")
			if err := coreSvc.SavePlaylistSelection(ctx, absPath, []string{playlists[0].File}, false); err != nil {
				if opts.Unattended && !opts.UnattendedConfirm {
					return fmt.Errorf("upbrr: unattended BDMV playlist selection save failed for %s: %w", absPath, err)
				}
				logger.Warnf("cli: save playlist selection: %v", err)
			}
		} else {
			// Display top 5 playlists
			topCount := min(len(playlists), 5)

			fmt.Printf("\nAvailable playlists for %s:\n", absPath)
			for i := range topCount {
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
					for i := range topCount {
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
		return fmt.Errorf("upbrr: %w", err)
	}

	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}

	dbPath, err := resolveExportDBPath(configPath, configProvided)
	if err != nil {
		return fmt.Errorf("resolve database path: %w", err)
	}

	cfg.MainSettings.DBPath = dbPath

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validate imported config: %w", err)
	}

	if err := configstore.SaveToDBPath(ctx, cfg, dbPath); err != nil {
		return fmt.Errorf("save imported config: %w", err)
	}

	if len(warnings) > 0 {
		fmt.Printf("imported config from %s (%d warnings)\n", importPath, len(warnings))
	} else {
		fmt.Printf("imported config from %s\n", importPath)
	}
	return nil
}
