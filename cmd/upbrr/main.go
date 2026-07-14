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
	"net"
	"net/netip"
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

var (
	version         = "dev"
	buildIdentifier = ""
)

func main() {
	exitCode := 0
	if err := run(); err != nil {
		printTerminalError(err)
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

// printTerminalError writes a sanitized CLI error diagnostic to stderr.
func printTerminalError(err error) {
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "error: %s\n", logging.SanitizeMessage(err.Error()))
}

// printTerminalWarning writes a sanitized CLI warning diagnostic to stderr.
func printTerminalWarning(warning string) {
	fmt.Fprintf(os.Stderr, "warning: %s\n", logging.SanitizeMessage(warning))
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

// cliItemTimeout bounds processing of a single input path (one queue item or
// one explicit path), so large queues are not capped by a shared run deadline.
const cliItemTimeout = 30 * time.Minute

// cliSetupTimeout bounds pre-upload setup work (core init, cleanup, delete-tmp)
// so the CLI fails instead of hanging on a stuck database or I/O call. It does
// not cover the per-item upload loop, which uses cliItemTimeout per path, nor
// queue gather / BDMV discovery, which have their own phase-scoped deadlines.
const cliSetupTimeout = 30 * time.Minute

// cliQueueGatherTimeout bounds queue discovery (GatherQueuePaths) so a stuck
// filesystem walk over a large queue root fails fast instead of consuming the
// shared setup deadline that init/cleanup/delete-tmp also need.
const cliQueueGatherTimeout = 10 * time.Minute

// cliDiscDiscoveryTimeout bounds per-disc BDMV discovery work (disc-type
// detection, playlist load/discover/save) for ONE disc, so one slow disc in a
// queue cannot starve the rest. It is applied per disc inside
// handleBDMVPlaylistSelection and deliberately does NOT cover the interactive
// playlist prompt, which must run on the untimed-but-cancelable parent context.
const cliDiscDiscoveryTimeout = 5 * time.Minute

// cliUploadOnlyTimeoutCap bounds how many per-item allowances the non-queue
// upload-only deadline may sum to. RunUploadPrepared processes every path in a
// single core call (its abort-on-first-error semantics are shared/immutable), so
// the deadline is budgeted by item count but capped so a huge --paths list cannot
// produce an effectively unbounded run-wide timeout.
const cliUploadOnlyTimeoutCap = 50

func run() error {
	api.SetApplicationBuild(version, buildIdentifier)

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
		fmt.Printf("created %s\n", formatPathLabel(webserver.AuthFilePath(dbPath)))
		return nil
	}

	ctx := context.Background()
	if strings.TrimSpace(opts.ExportConfigPath) != "" {
		if err := exportConfigToYAML(ctx, opts.ConfigPath, configFlagProvided, opts.ExportConfigPath, opts.ExportConfigPlaintext); err != nil {
			return exitError(1, err)
		}
		fmt.Printf("exported config to %s\n", formatPathLabel(opts.ExportConfigPath))
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
			printTerminalError(err)
		}
	}()
	screens := opts.Screens
	if screens < 0 {
		screens = cfg.ScreenshotHandling.Screens
	}
	// Each input path runs under its own cliItemTimeout (applied per item in
	// processCLIPaths) so a long queue is not killed by a single run-wide
	// deadline. Pre-upload setup is split into purpose-scoped phase contexts,
	// each derived directly from the root cancelable ctx (siblings, not chained),
	// so cancellation propagates from the root but deadlines do not compound and
	// an earlier phase cannot starve a later one:
	//   - phase 1 (setupCtx): core init + cleanup + delete-tmp (cliSetupTimeout)
	//   - phase 2 (gatherCtx): queue gather (cliQueueGatherTimeout)
	//   - phase 3 (per-disc): BDMV discovery (cliDiscDiscoveryTimeout per disc)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = withCLIUploadProgressLogger(ctx, logger)
	// Phase 1: core init + cleanup + delete-tmp run under cliSetupTimeout.
	setupCtx, setupCancel := context.WithTimeout(ctx, cliSetupTimeout)
	defer setupCancel()
	coreSvc, err := core.NewWithContext(setupCtx, api.CoreDependencies{
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
			printTerminalError(err)
		}
	}()

	if opts.Cleanup {
		deleted, err := coreSvc.DeleteAllHistoryReleases(setupCtx)
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
		gatherCtx, gatherCancel := context.WithTimeout(ctx, cliQueueGatherTimeout)
		queuePaths, err := filesystem.GatherQueuePaths(gatherCtx, paths[0])
		gatherCancel()
		if err != nil {
			return exitError(1, err)
		}
		paths = filesystem.LimitQueuePaths(queuePaths, opts.LimitQueue)
		if len(paths) == 0 {
			return exitError(1, fmt.Errorf("queue %q resolved to no upload candidates", opts.QueueName))
		}
	}

	if opts.DeleteTmp {
		paths, err = normalizeCLIPaths(setupCtx, paths)
		if err != nil {
			return exitError(1, err)
		}
		if err := deleteCLIStoredReleases(setupCtx, coreSvc, paths); err != nil {
			return exitError(1, err)
		}
	}

	// Handle BDMV playlist selection before upload. Pass the root cancelable ctx
	// (not setupCtx): handleBDMVPlaylistSelection applies its own per-disc
	// deadline internally and must keep the interactive prompt on an
	// untimed-but-cancelable context.
	if err := handleBDMVPlaylistSelection(ctx, paths, coreSvc, cfg, logger, opts); err != nil {
		return exitError(1, err)
	}

	if opts.UploadOnly {
		uploadReq, err := buildCLIRequest(opts, visitedFlags, paths, screens)
		if err != nil {
			return exitError(1, err)
		}
		queueMode := strings.TrimSpace(opts.QueueName) != ""
		if queueMode {
			return runCLIUploadOnlyQueue(ctx, coreSvc, uploadReq, paths, opts.Debug, logger)
		}
		return runCLIUploadOnlyBatch(ctx, coreSvc, uploadReq, paths, opts.Debug, logger)
	}

	queueMode := strings.TrimSpace(opts.QueueName) != ""
	return processCLIPaths(ctx, paths, queueMode, cliItemTimeout, logger, func(itemCtx context.Context, sourcePath string) error {
		if opts.SiteCheck {
			return runSiteCheckCLIPath(itemCtx, coreSvc, opts, visitedFlags, sourcePath, screens)
		}
		return runInteractiveCLIPathWithLogger(itemCtx, coreSvc, os.Args[1:], opts, visitedFlags, sourcePath, screens, cfg, logger)
	})
}

// runCLIUploadOnlyBatch runs upload-only over all paths in a single
// RunUploadPrepared call (preserving its shared abort-on-first-error
// semantics) under a deadline budgeted by item count but capped at
// cliUploadOnlyTimeoutCap items so a large path list cannot create an
// effectively unbounded run-wide timeout.
func runCLIUploadOnlyBatch(ctx context.Context, coreSvc api.Core, uploadReq api.Request, paths []string, debug bool, logger api.Logger) error {
	budgetItems := min(max(1, len(paths)), cliUploadOnlyTimeoutCap)
	uploadCtx, uploadCancel := context.WithTimeout(ctx, time.Duration(budgetItems)*cliItemTimeout)
	defer uploadCancel()
	preparedReq, err := prepareCLIUploadMetadata(uploadCtx, coreSvc, uploadReq)
	if err != nil {
		return exitError(1, err)
	}
	if err := prepareCLIImages(uploadCtx, coreSvc, preparedReq, logger, true); err != nil {
		return exitError(1, err)
	}
	if debug {
		reviews, err := buildCLIUploadDebugReviews(uploadCtx, coreSvc, paths, preparedReq)
		if err != nil {
			return exitError(1, err)
		}
		for _, review := range reviews {
			printDebugUploadReview(review)
		}
	}
	if _, err := coreSvc.RunUploadPrepared(uploadCtx, preparedReq); err != nil {
		return exitError(1, err)
	}
	return nil
}

// runCLIUploadOnlyQueue runs upload-only in queue mode: each gathered path is
// processed independently under its own cliItemTimeout via processCLIPaths, so a
// single failing item is logged and skipped (continue-on-error) instead of
// aborting the whole queue. Per item it prepares metadata, optionally prints the
// debug review, and runs RunUploadPrepared for that one path, summing uploaded
// counts across items.
func runCLIUploadOnlyQueue(ctx context.Context, coreSvc api.Core, uploadReq api.Request, paths []string, debug bool, logger api.Logger) error {
	var uploaded int
	err := processCLIPaths(ctx, paths, true, cliItemTimeout, logger, func(itemCtx context.Context, sourcePath string) error {
		itemReq := uploadReq
		itemReq.Paths = []string{sourcePath}
		preparedReq, err := prepareCLIUploadMetadata(itemCtx, coreSvc, itemReq)
		if err != nil {
			return err
		}
		if err := prepareCLIImages(itemCtx, coreSvc, preparedReq, logger, false); err != nil {
			return err
		}
		if debug {
			// Single-path review: mirror the inline debug-review build rather than
			// buildCLIUploadDebugReviews, whose Paths[idx] indexing is fragile when
			// the request is already a single path.
			resolvedPath := sourcePath
			if len(preparedReq.Paths) > 0 && strings.TrimSpace(preparedReq.Paths[0]) != "" {
				resolvedPath = preparedReq.Paths[0]
			}
			reviewReq := preparedReq
			reviewReq.Paths = []string{resolvedPath}
			reviewReq.ExternalIDSelections = cloneCLIExternalIDSelectionsForResolvedPath(preparedReq.ExternalIDSelections, sourcePath, resolvedPath)
			review, err := coreSvc.BuildUploadReview(itemCtx, reviewReq)
			if err != nil {
				return fmt.Errorf("build upload review for %q: %w", resolvedPath, err)
			}
			if strings.TrimSpace(sourcePath) != "" {
				review.SourcePath = sourcePath
			}
			printDebugUploadReview(review)
		}
		result, err := coreSvc.RunUploadPrepared(itemCtx, preparedReq)
		// RunUploadPrepared can accept some tracker uploads and then fail on a
		// later one, returning a positive count alongside the error, so fold the
		// count in before propagating the failure.
		uploaded += result.UploadedCount
		if err != nil {
			return fmt.Errorf("upbrr: %w", err)
		}
		return nil
	})
	if logger != nil {
		// UploadedCount counts accepted tracker uploads, not queue items (one item
		// may upload to several trackers), so report it as tracker uploads.
		logger.Infof("queue: upload-only completed, %d tracker upload(s) accepted", uploaded)
	}
	return err
}

// prepareCLIImages imports manual menu paths and optionally captures automatic
// DVD menus for each request path before review or upload. Non-DVD paths are
// skipped without invoking capture. Batch callers can continue after an empty
// successful capture while retaining errors from CaptureDVDMenus.
func prepareCLIImages(ctx context.Context, coreSvc api.Core, req api.Request, logger api.Logger, continueOnEmptyCapture bool) error {
	if coreSvc == nil {
		return errors.New("upbrr: core service is required")
	}
	if logger == nil {
		logger = api.NopLogger{}
	}
	if len(req.Paths) == 0 {
		return internalerrors.ErrInvalidInput
	}

	for _, sourcePath := range req.Paths {
		singleReq := req
		singleReq.Paths = []string{sourcePath}
		if len(singleReq.ScreenshotOverrides.MenuPaths) > 0 {
			if err := coreSvc.ImportMenuImages(ctx, singleReq, singleReq.ScreenshotOverrides.MenuPaths); err != nil {
				return fmt.Errorf("upbrr: import menu images: %w", err)
			}
		}
		if !singleReq.Options.CaptureDVDMenus {
			continue
		}

		discType, err := filesystem.DetectDiscType(ctx, sourcePath)
		if err != nil {
			return fmt.Errorf("upbrr: detect DVD menu source: %w", err)
		}
		if !strings.EqualFold(strings.TrimSpace(discType), "DVD") {
			label := strings.TrimSpace(discType)
			if label == "" {
				label = "none"
			}
			logger.Infof("DVD menus: capture skipped disc_type=%s decision=skip", label)
			fmt.Printf("DVD menu capture skipped: %s is not a DVD\n", formatPathLabel(sourcePath))
			continue
		}

		result, err := coreSvc.CaptureDVDMenus(ctx, singleReq)
		if err != nil {
			return fmt.Errorf("upbrr: capture DVD menus: %w", err)
		}
		if len(result.Images) == 0 {
			if !continueOnEmptyCapture {
				return errors.New("upbrr: capture DVD menus: no menu images captured")
			}
			logger.Errorf(
				"DVD menus: capture failed source=%s decision=continue reason=no_images",
				formatPathLabel(sourcePath),
			)
			continue
		}
		if result.Partial {
			logger.Warnf(
				"DVD menus: capture incomplete captured=%d warnings=%d truncated=%t",
				len(result.Images),
				len(result.Warnings),
				result.Truncated,
			)
			fmt.Printf("DVD menus ready: %d (capture incomplete)\n", len(result.Images))
			continue
		}
		if result.Truncated {
			fmt.Printf("DVD menus ready: %d (maximum reached)\n", len(result.Images))
			continue
		}
		fmt.Printf("DVD menus ready: %d\n", len(result.Images))
	}
	return nil
}

// processCLIPaths runs process for each input path under its own itemTimeout. In
// queue mode a per-item failure is logged and processing continues so the rest
// of the queue still runs, with a summary error returned at the end if any item
// failed. Outside queue mode the first failure aborts immediately, preserving
// the original single/multi-path behavior.
func processCLIPaths(ctx context.Context, paths []string, queueMode bool, itemTimeout time.Duration, logger api.Logger, process func(ctx context.Context, sourcePath string) error) error {
	failed := make([]string, 0)
	var firstErr error
	// abortOnCancel returns a terminal error when the parent context is done (a
	// run-wide cancellation or deadline), so the queue stops instead of treating
	// the in-flight item as a normal failure. Per-item timeouts live on the
	// derived itemCtx, so they leave the parent ctx.Err() nil and fall through to
	// the ordinary failure handling below.
	abortOnCancel := func(i int) error {
		ctxErr := ctx.Err()
		if ctxErr == nil {
			return nil
		}
		if logger != nil {
			logger.Warnf("queue: aborted after %d of %d item(s): %v", i, len(paths), ctxErr)
		}
		return exitError(1, fmt.Errorf("aborted after %d of %d item(s): %w", i, len(paths), ctxErr))
	}
	for i, sourcePath := range paths {
		// Honor parent-context cancellation (e.g. Ctrl-C) before starting an item,
		// so we don't derive an already-done context and log spurious failures.
		if abortErr := abortOnCancel(i); abortErr != nil {
			return abortErr
		}
		err := runCLIPathWithTimeout(ctx, itemTimeout, sourcePath, process)
		if err == nil {
			continue
		}
		// A failure caused by parent cancellation/expiry must abort immediately
		// rather than be recorded as an item failure. This also covers the final
		// item, where the next-iteration check above never runs.
		if abortErr := abortOnCancel(i); abortErr != nil {
			return abortErr
		}
		if !queueMode {
			return exitError(1, err)
		}
		if firstErr == nil {
			firstErr = err
		}
		if logger != nil {
			logger.Errorf("queue: %q failed, continuing with remaining items: %v", sourcePath, err)
		}
		failed = append(failed, sourcePath)
	}
	if len(failed) > 0 {
		quoted := make([]string, len(failed))
		for i, p := range failed {
			quoted[i] = fmt.Sprintf("%q", p)
		}
		joined := strings.Join(quoted, ", ")
		if logger != nil {
			logger.Warnf("queue: %d of %d item(s) failed: %s", len(failed), len(paths), joined)
		}
		return exitError(1, fmt.Errorf("queue completed with %d of %d item(s) failed [%s]: %w", len(failed), len(paths), joined, firstErr))
	}
	return nil
}

// runCLIPathWithTimeout processes a single input path under its own deadline so
// each queue item receives a full timeout budget rather than sharing one
// run-wide deadline. The deadline is enforced cooperatively: itemCtx is threaded
// into every core call (FetchMetadataPreview, CheckDupes, screenshot handling,
// BuildUploadReview, RunUploadPrepared, etc.) and honored only when those calls
// check ctx. A core operation that ignores ctx will run past itemTimeout; the
// timeout cannot forcibly kill in-flight work.
func runCLIPathWithTimeout(ctx context.Context, itemTimeout time.Duration, sourcePath string, process func(ctx context.Context, sourcePath string) error) error {
	itemCtx, cancel := context.WithTimeout(ctx, itemTimeout)
	defer cancel()
	return process(itemCtx, sourcePath)
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
	envOpts, envVisited := readServeEnv()
	if visitedFlags["persist-listen"] && !hasServeListenOverrides(visitedFlags) {
		return errors.New("--persist-listen requires --addr, --host, or --port")
	}
	if visitedFlags["persist-web-config"] && !hasServeWebConfigOverrides(visitedFlags) && !hasServeEnvOverrides(envVisited) {
		return errors.New("--persist-web-config requires --addr, --host, --port, --base-url, or UPBRR_WEB_* env")
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

	storedWebCfg, err := webserver.LoadCLIConfig(dbPath)
	if err != nil {
		return fmt.Errorf("upbrr: %w", err)
	}
	webCfg := storedWebCfg
	webCfg, err = applyServeEnvOverrides(webCfg, envOpts, envVisited)
	if err != nil {
		return err
	}
	webCfg, err = applyServeOptionOverrides(webCfg, opts, visitedFlags)
	if err != nil {
		return err
	}
	persistWebCfg := servePersistConfig(storedWebCfg, webCfg, visitedFlags)
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

	if visitedFlags["persist-listen"] || visitedFlags["persist-web-config"] {
		return wrapUpbrrError(server.RunAfterListen(context.Background(), func() error {
			if err := webserver.SaveCLIConfig(dbPath, persistWebCfg); err != nil {
				return fmt.Errorf("save web config: %w", err)
			}
			return nil
		}))
	}

	return wrapUpbrrError(server.Run(context.Background()))
}

// hasServeListenOverrides reports whether serve bind flags were provided.
// These flags affect the current process, and --persist-listen makes them durable.
func hasServeListenOverrides(visited map[string]bool) bool {
	return visited["addr"] || visited["host"] || visited["port"]
}

// hasServeWebConfigOverrides reports whether serve flags that may be persisted
// by --persist-web-config were provided.
func hasServeWebConfigOverrides(visited map[string]bool) bool {
	return hasServeListenOverrides(visited) || visited["base-url"]
}

// hasServeEnvOverrides reports whether any UPBRR_WEB_* setting can affect the
// runtime web config. Env-derived settings are persisted only when
// --persist-web-config is explicit.
func hasServeEnvOverrides(visited map[string]bool) bool {
	return visited["host"] || visited["port"] || visited["base-url"] || visited["open-browser"] ||
		visited["trusted-proxies"] || hasServeOIDCEnvOverrides(visited)
}

// hasServeOIDCEnvOverrides reports whether any OIDC setting came from the
// environment. Note that persisting these with --persist-web-config writes the
// client secret to web-config.json; deployments that keep secrets out of files
// should supply them by env on every start and not persist.
func hasServeOIDCEnvOverrides(visited map[string]bool) bool {
	return visited["oidc-enabled"] || visited["oidc-issuer"] || visited["oidc-client-id"] ||
		visited["oidc-client-secret"] || visited["oidc-redirect-url"] || visited["oidc-scopes"] ||
		visited["oidc-disable-built-in-login"]
}

// serveEnvOptions stores raw UPBRR_WEB_* values so each field can be validated
// with the same parser used by the matching CLI flag.
type serveEnvOptions struct {
	Host                    string
	Port                    string
	BaseURL                 string
	OpenBrowser             string
	TrustedProxies          string
	OIDCEnabled             string
	OIDCIssuer              string
	OIDCClientID            string
	OIDCClientSecret        string
	OIDCRedirectURL         string
	OIDCScopes              string
	OIDCDisableBuiltInLogin string
}

// readServeEnv returns configured UPBRR_WEB_* values plus visited markers that
// distinguish an unset variable from an intentionally empty invalid value.
func readServeEnv() (serveEnvOptions, map[string]bool) {
	env := serveEnvOptions{}
	visited := make(map[string]bool)
	if value, ok := os.LookupEnv("UPBRR_WEB_HOST"); ok {
		env.Host = value
		visited["host"] = true
	}
	if value, ok := os.LookupEnv("UPBRR_WEB_PORT"); ok {
		env.Port = value
		visited["port"] = true
	}
	if value, ok := os.LookupEnv("UPBRR_WEB_BASE_URL"); ok {
		env.BaseURL = value
		visited["base-url"] = true
	}
	if value, ok := os.LookupEnv("UPBRR_WEB_OPEN_BROWSER"); ok {
		env.OpenBrowser = value
		visited["open-browser"] = true
	}
	if value, ok := os.LookupEnv("UPBRR_WEB_TRUSTED_PROXIES"); ok {
		env.TrustedProxies = value
		visited["trusted-proxies"] = true
	}
	if value, ok := os.LookupEnv("UPBRR_WEB_OIDC_ENABLED"); ok {
		env.OIDCEnabled = value
		visited["oidc-enabled"] = true
	}
	if value, ok := os.LookupEnv("UPBRR_WEB_OIDC_ISSUER"); ok {
		env.OIDCIssuer = value
		visited["oidc-issuer"] = true
	}
	if value, ok := os.LookupEnv("UPBRR_WEB_OIDC_CLIENT_ID"); ok {
		env.OIDCClientID = value
		visited["oidc-client-id"] = true
	}
	if value, ok := os.LookupEnv("UPBRR_WEB_OIDC_CLIENT_SECRET"); ok {
		env.OIDCClientSecret = value
		visited["oidc-client-secret"] = true
	}
	if value, ok := os.LookupEnv("UPBRR_WEB_OIDC_REDIRECT_URL"); ok {
		env.OIDCRedirectURL = value
		visited["oidc-redirect-url"] = true
	}
	if value, ok := os.LookupEnv("UPBRR_WEB_OIDC_SCOPES"); ok {
		env.OIDCScopes = value
		visited["oidc-scopes"] = true
	}
	if value, ok := os.LookupEnv("UPBRR_WEB_OIDC_DISABLE_BUILT_IN_LOGIN"); ok {
		env.OIDCDisableBuiltInLogin = value
		visited["oidc-disable-built-in-login"] = true
	}
	return env, visited
}

// applyServeEnvOverrides applies environment-sourced web config after stored
// config is loaded and before CLI flags are applied.
func applyServeEnvOverrides(webCfg webserver.CLIConfig, env serveEnvOptions, visited map[string]bool) (webserver.CLIConfig, error) {
	if visited["host"] {
		host, err := parseServeHost(env.Host)
		if err != nil {
			return webserver.CLIConfig{}, fmt.Errorf("parse serve env: UPBRR_WEB_HOST: %w", err)
		}
		webCfg.Host = host
	}
	if visited["port"] {
		port, err := parseServePort(env.Port)
		if err != nil {
			return webserver.CLIConfig{}, fmt.Errorf("parse serve env: UPBRR_WEB_PORT: %w", err)
		}
		webCfg.Port = port
	}
	if visited["base-url"] {
		if strings.TrimSpace(env.BaseURL) == "" {
			return webserver.CLIConfig{}, errors.New("parse serve env: UPBRR_WEB_BASE_URL cannot be empty")
		}
		baseURL, err := webserver.NormalizeBaseURL(env.BaseURL)
		if err != nil {
			return webserver.CLIConfig{}, fmt.Errorf("parse serve env: UPBRR_WEB_BASE_URL: %w", err)
		}
		webCfg.BaseURL = baseURL
	}
	if visited["open-browser"] {
		openBrowser, err := parseServeBool(env.OpenBrowser)
		if err != nil {
			return webserver.CLIConfig{}, fmt.Errorf("parse serve env: UPBRR_WEB_OPEN_BROWSER: %w", err)
		}
		webCfg.OpenBrowser = openBrowser
	}
	if visited["trusted-proxies"] {
		webCfg.TrustedProxies = splitCSV(env.TrustedProxies)
	}
	oidcCfg, err := applyServeOIDCEnvOverrides(webCfg.OIDC, env, visited)
	if err != nil {
		return webserver.CLIConfig{}, err
	}
	webCfg.OIDC = oidcCfg
	return webCfg, nil
}

// applyServeOIDCEnvOverrides applies UPBRR_WEB_OIDC_* settings. Values are not
// validated here; normalizeCLIConfig performs the cross-field validation so the
// same rules apply to env, flags, and persisted config.
func applyServeOIDCEnvOverrides(
	oidcCfg webserver.OIDCConfig,
	env serveEnvOptions,
	visited map[string]bool,
) (webserver.OIDCConfig, error) {
	if visited["oidc-enabled"] {
		enabled, err := parseServeBool(env.OIDCEnabled)
		if err != nil {
			return webserver.OIDCConfig{}, fmt.Errorf("parse serve env: UPBRR_WEB_OIDC_ENABLED: %w", err)
		}
		oidcCfg.Enabled = enabled
	}
	if visited["oidc-issuer"] {
		oidcCfg.Issuer = strings.TrimSpace(env.OIDCIssuer)
	}
	if visited["oidc-client-id"] {
		oidcCfg.ClientID = strings.TrimSpace(env.OIDCClientID)
	}
	if visited["oidc-client-secret"] {
		oidcCfg.ClientSecret = strings.TrimSpace(env.OIDCClientSecret)
	}
	if visited["oidc-redirect-url"] {
		oidcCfg.RedirectURL = strings.TrimSpace(env.OIDCRedirectURL)
	}
	if visited["oidc-scopes"] {
		oidcCfg.Scopes = strings.TrimSpace(env.OIDCScopes)
	}
	if visited["oidc-disable-built-in-login"] {
		disabled, err := parseServeBool(env.OIDCDisableBuiltInLogin)
		if err != nil {
			return webserver.OIDCConfig{}, fmt.Errorf(
				"parse serve env: UPBRR_WEB_OIDC_DISABLE_BUILT_IN_LOGIN: %w", err)
		}
		oidcCfg.DisableBuiltInLogin = disabled
	}
	return oidcCfg, nil
}

// parseServeBool accepts common bool spellings used in container env files.
func parseServeBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "t", "true", "y", "yes", "on":
		return true, nil
	case "0", "f", "false", "n", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid bool %q", value)
	}
}

// servePersistConfig chooses the web config that should be saved after a
// successful bind. --persist-listen saves only listen fields, while
// --persist-web-config saves the full runtime web config.
func servePersistConfig(storedWebCfg webserver.CLIConfig, runtimeWebCfg webserver.CLIConfig, visited map[string]bool) webserver.CLIConfig {
	if visited["persist-web-config"] {
		return runtimeWebCfg
	}
	if !visited["persist-listen"] {
		return storedWebCfg
	}

	persisted := storedWebCfg
	persisted.BaseURL = ""
	if visited["addr"] {
		persisted.Host = runtimeWebCfg.Host
		persisted.Port = runtimeWebCfg.Port
		return persisted
	}
	if visited["host"] {
		persisted.Host = runtimeWebCfg.Host
	}
	if visited["port"] {
		persisted.Port = runtimeWebCfg.Port
	}
	return persisted
}

// applyServeOptionOverrides returns webCfg with explicitly supplied serve flags
// applied. --addr replaces both host and port and can be combined with
// --base-url; --host and --port may update either listen field independently.
func applyServeOptionOverrides(webCfg webserver.CLIConfig, opts serveOptions, visited map[string]bool) (webserver.CLIConfig, error) {
	if visited["addr"] && (visited["host"] || visited["port"]) {
		return webserver.CLIConfig{}, errors.New("--addr cannot be used with --host or --port")
	}

	if visited["addr"] {
		host, port, err := parseServeAddress(opts.Addr)
		if err != nil {
			return webserver.CLIConfig{}, err
		}
		webCfg.Host = host
		webCfg.Port = port
	} else if visited["host"] {
		host, err := parseServeHost(opts.Host)
		if err != nil {
			return webserver.CLIConfig{}, err
		}
		webCfg.Host = host
	}
	if visited["port"] {
		port, err := parseServePort(strconv.Itoa(opts.Port))
		if err != nil {
			return webserver.CLIConfig{}, err
		}
		webCfg.Port = port
	}
	if visited["base-url"] {
		if strings.TrimSpace(opts.BaseURL) == "" {
			return webserver.CLIConfig{}, errors.New("parse serve options: --base-url cannot be empty")
		}
		baseURL, err := webserver.NormalizeBaseURL(opts.BaseURL)
		if err != nil {
			return webserver.CLIConfig{}, fmt.Errorf("parse serve options: --base-url: %w", err)
		}
		webCfg.BaseURL = baseURL
	}

	return webCfg, nil
}

// parseServeAddress splits a serve listen address into normalized host and
// validated TCP port parts.
func parseServeAddress(value string) (string, int, error) {
	host, portValue, err := net.SplitHostPort(strings.TrimSpace(value))
	if err != nil {
		return "", 0, fmt.Errorf("parse serve options: --addr must be host:port: %w", err)
	}
	parsedHost, err := parseServeAddressHost(host)
	if err != nil {
		return "", 0, err
	}
	parsedPort, err := parseServePort(portValue)
	if err != nil {
		return "", 0, err
	}
	return parsedHost, parsedPort, nil
}

// parseServeAddressHost normalizes the host part of --addr. An empty host is
// the supported :port shorthand and maps to the TCP wildcard bind host.
func parseServeAddressHost(value string) (string, error) {
	host := strings.TrimSpace(value)
	if host == "" {
		return "0.0.0.0", nil
	}
	return parseServeHost(host)
}

// parseServeHost normalizes a serve bind host. Bracketed IPv6 literals are
// unwrapped, valid IPv6 literals stay valid, and host:port values are rejected
// so port ownership stays explicit.
func parseServeHost(value string) (string, error) {
	host := strings.TrimSpace(value)
	if host == "" {
		return "", errors.New("parse serve options: --host cannot be empty")
	}
	host, err := normalizeServeHostBrackets(host)
	if err != nil {
		return "", err
	}
	if _, _, err := net.SplitHostPort(host); err == nil {
		return "", errors.New("parse serve options: --host cannot include a port; use --addr or --port")
	}
	if looksLikeUnbracketedIPv6HostPort(host) {
		return "", errors.New("parse serve options: --host cannot include a port; use bracketed IPv6 with --addr")
	}
	if strings.Contains(host, ":") && !isValidServeIPv6Host(host) {
		return "", errors.New("parse serve options: --host cannot include a port; use bracketed IPv6 with --addr")
	}
	return host, nil
}

// normalizeServeHostBrackets unwraps bracketed IPv6 literals and rejects any
// other bracket syntax before the host is persisted or passed to JoinHostPort.
func normalizeServeHostBrackets(host string) (string, error) {
	if !strings.ContainsAny(host, "[]") {
		return host, nil
	}
	if !strings.HasPrefix(host, "[") || !strings.HasSuffix(host, "]") {
		return "", errors.New("parse serve options: --host has invalid bracket syntax")
	}
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(host, "["), "]"))
	if inner == "" || strings.ContainsAny(inner, "[]") {
		return "", errors.New("parse serve options: --host has invalid bracket syntax")
	}
	if !isValidServeIPv6Host(inner) {
		return "", errors.New("parse serve options: --host brackets are only valid for IPv6 literals")
	}
	return inner, nil
}

func isValidServeIPv6Host(host string) bool {
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.Is6()
}

// looksLikeUnbracketedIPv6HostPort detects ambiguous IPv6-ish host text whose
// final colon-separated segment looks like a port suffix, including scoped
// literals where netip.ParseAddr would otherwise treat the suffix as part of
// the zone. IPv4-mapped IPv6 literals stay valid because their trailing dotted
// IPv4 text is address data, not a port suffix.
func looksLikeUnbracketedIPv6HostPort(host string) bool {
	if strings.ContainsAny(host, "[]") || strings.Count(host, ":") < 2 {
		return false
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if addr.Is4In6() {
			return false
		}
		if addr.Is6() && !strings.Contains(addr.Zone(), ":") {
			return false
		}
	}
	idx := strings.LastIndex(host, ":")
	if idx <= 0 || idx == len(host)-1 {
		return false
	}
	suffix := host[idx+1:]
	if suffix == "" {
		return false
	}
	for _, r := range suffix {
		if r < '0' || r > '9' {
			return false
		}
	}
	return isValidServeIPv6Host(host[:idx])
}

// parseServePort validates a serve TCP port in the user-assignable range.
func parseServePort(value string) (int, error) {
	port, err := parseServePortValue(value)
	if err != nil {
		return 0, fmt.Errorf("parse serve options: %w", err)
	}
	return port, nil
}

// parseServePortValue accepts decimal TCP ports in the user-assignable range.
func parseServePortValue(value string) (int, error) {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid port %q", value)
	}
	return port, nil
}

// loadCLIConfig bootstraps CLI config and validates both the env-applied
// pre-persist candidate and the returned runtime config.
func loadCLIConfig(configPath string, configProvided bool) (config.Config, string, error) {
	cfg, dbPath, err := configstore.BootstrapWithValidator(context.Background(), configPath, configProvided, true, func(cfg *config.Config) error {
		return cfg.Validate()
	})
	if err != nil {
		return config.Config{}, "", fmt.Errorf("upbrr: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return config.Config{}, "", fmt.Errorf("upbrr: %w", err)
	}
	return cfg, dbPath, nil
}

// loadServeConfig loads config for the web server without requiring a fully
// valid config. The web UI handles initial setup, so the server must be able
// to start even on a fresh install with no config yet. A
// provided --config may seed or merge database config, but invalid env-applied
// input is not persisted over stored settings.
func loadServeConfig(configPath string, configProvided bool) (config.Config, string, error) {
	return wrapUpbrrResult2(configstore.Bootstrap(context.Background(), configPath, configProvided, configProvided))
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
		fmt.Printf("deleted stored database content for %s\n", formatPathLabel(sourcePath))
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

// buildCLIUploadDebugReviews builds one review per original CLI source path,
// preserving the original display path while using any prepared resolved path.
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
	// Current source-path selections must replace any stale resolved-path
	// selections carried from a previous run or source change.
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

// isCtxErr reports whether err is a context cancellation/deadline error,
// signalling the run-wide deadline or an explicit cancel has fired. BDMV
// setup must abort (not skip the path) when this happens so cancellation is
// surfaced instead of being swallowed by a per-path continue.
func isCtxErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// handleBDMVPlaylistSelection resolves playlist selections for each BDMV disc in
// paths. ctx is the untimed-but-cancelable parent context; each disc gets its
// own cliDiscDiscoveryTimeout for the timed detection/load/discover/save work so
// one slow disc cannot starve the rest of the queue. The interactive playlist
// prompt runs on the parent (cancelable, untimed) context, not the per-disc
// deadline, so a long human wait does not abort.
func handleBDMVPlaylistSelection(ctx context.Context, paths []string, coreSvc api.Core, cfg config.Config, logger api.Logger, opts cliOptions) error {
	if len(paths) == 0 {
		return nil
	}

	for _, path := range paths {
		discCtx, discCancel := context.WithTimeout(ctx, cliDiscDiscoveryTimeout)
		err := handleBDMVDiscSelection(discCtx, ctx, path, coreSvc, cfg, logger, opts)
		discCancel()
		if err != nil {
			return err
		}
	}
	return nil
}

// handleBDMVDiscSelection resolves the playlist selection for a single BDMV
// disc. discCtx carries the per-disc deadline and is used for detection, load,
// discovery and the auto-select saves. promptCtx is the untimed-but-cancelable
// parent context used for the interactive prompt loop and its saves, so a long
// human wait does not trip the per-disc deadline. A context error from any timed
// operation aborts (returns) rather than skipping the path, surfacing
// cancellation instead of swallowing it.
func handleBDMVDiscSelection(discCtx context.Context, promptCtx context.Context, path string, coreSvc api.Core, cfg config.Config, logger api.Logger, opts cliOptions) error {
	// Check if this path is a BDMV folder
	discType, err := filesystem.DetectDiscType(discCtx, path)
	if err != nil {
		if isCtxErr(err) {
			return fmt.Errorf("upbrr: BDMV disc type detection cancelled for %s: %w", path, err)
		}
		logger.Debugf("cli: disc type detection failed for %s: %v", path, err)
		return nil
	}

	if discType != "BDMV" {
		return nil
	}

	logger.Infof("cli: BDMV disc detected at %s", path)

	// Normalize to absolute path for consistency
	absPath, err := filepath.Abs(path)
	if err != nil {
		logger.Warnf("cli: resolve path %s: %v", path, err)
		return nil
	}

	// Check if playlist selection is already persisted
	_, err = coreSvc.LoadPlaylistSelection(discCtx, absPath)
	if err == nil {
		logger.Infof("cli: using previously saved playlist selection for %s", absPath)
		return nil
	}
	if err != nil {
		if isCtxErr(err) {
			return fmt.Errorf("upbrr: BDMV playlist selection load cancelled for %s: %w", absPath, err)
		}
		if !errors.Is(err, internalerrors.ErrNotFound) {
			logger.Warnf("cli: load playlist selection: %v", err)
			return nil
		}
	}

	// No selection exists; check if we should auto-select
	if cfg.Metadata.UseLargestPlaylist {
		logger.Infof("cli: auto-selecting largest playlist (use_largest_playlist enabled)")

		playlists, err := coreSvc.DiscoverPlaylists(discCtx, absPath)
		if err != nil {
			if isCtxErr(err) {
				return fmt.Errorf("upbrr: BDMV playlist discovery cancelled for %s: %w", absPath, err)
			}
			if opts.Unattended && !opts.UnattendedConfirm {
				return fmt.Errorf("upbrr: unattended BDMV playlist discovery failed for %s: %w", absPath, err)
			}
			logger.Warnf("cli: discover playlists: %v", err)
			return nil
		}

		if len(playlists) == 0 {
			if opts.Unattended && !opts.UnattendedConfirm {
				return fmt.Errorf("upbrr: unattended BDMV upload found no playlists for %s", absPath)
			}
			return nil
		}
		// Save the best (highest-scoring) playlist
		selected := []string{playlists[0].File}
		if err := coreSvc.SavePlaylistSelection(discCtx, absPath, selected, false); err != nil {
			if isCtxErr(err) {
				return fmt.Errorf("upbrr: BDMV playlist selection save cancelled for %s: %w", absPath, err)
			}
			if opts.Unattended && !opts.UnattendedConfirm {
				return fmt.Errorf("upbrr: unattended BDMV playlist selection save failed for %s: %w", absPath, err)
			}
			logger.Warnf("cli: save playlist selection: %v", err)
		} else {
			logger.Infof("cli: auto-selected playlist %s (score: %.2f)", playlists[0].File, playlists[0].Score)
		}
		return nil
	}

	// Interactive selection required
	logger.Infof("cli: discovering playlists for %s", absPath)
	playlists, err := coreSvc.DiscoverPlaylists(discCtx, absPath)
	if err != nil {
		if isCtxErr(err) {
			return fmt.Errorf("upbrr: BDMV playlist discovery cancelled for %s: %w", absPath, err)
		}
		if opts.Unattended && !opts.UnattendedConfirm {
			return fmt.Errorf("upbrr: unattended BDMV playlist discovery failed for %s: %w", absPath, err)
		}
		logger.Warnf("cli: discover playlists: %v", err)
		return nil
	}

	if len(playlists) == 0 {
		if opts.Unattended && !opts.UnattendedConfirm {
			return fmt.Errorf("upbrr: unattended BDMV upload found no playlists for %s", absPath)
		}
		logger.Warnf("cli: no playlists found for %s", absPath)
		return nil
	}

	logger.Infof("cli: found %d playlists", len(playlists))
	if opts.Unattended && !opts.UnattendedConfirm && len(playlists) > 1 {
		return fmt.Errorf("upbrr: unattended BDMV upload requires a saved playlist selection or use_largest_playlist for %s", absPath)
	}

	// Display top playlists and prompt user
	if len(playlists) == 1 {
		fmt.Printf("[*] Only one playlist found: %s (%.0fs, score: %.2f)\n", playlists[0].File, playlists[0].Duration, playlists[0].Score)
		fmt.Printf("[*] Auto-selecting...\n")
		if err := coreSvc.SavePlaylistSelection(discCtx, absPath, []string{playlists[0].File}, false); err != nil {
			if isCtxErr(err) {
				return fmt.Errorf("upbrr: BDMV playlist selection save cancelled for %s: %w", absPath, err)
			}
			if opts.Unattended && !opts.UnattendedConfirm {
				return fmt.Errorf("upbrr: unattended BDMV playlist selection save failed for %s: %w", absPath, err)
			}
			logger.Warnf("cli: save playlist selection: %v", err)
		}
		return nil
	}

	// Display top 5 playlists
	topCount := min(len(playlists), 5)

	fmt.Printf("\nAvailable playlists for %s:\n", formatPathLabel(absPath))
	for i := range topCount {
		p := playlists[i]
		durationStr := formatDuration(p.Duration)
		fmt.Printf("[%d] %s (%s, score: %.2f)\n", i, p.File, durationStr, p.Score)
	}

	// Prompt user for selection. This loop runs on the untimed-but-cancelable
	// promptCtx (NOT discCtx) so a long human wait does not trip the per-disc
	// deadline; the saves inside it are bounded by a fresh short deadline derived
	// from promptCtx so they remain cancelable but the user-wait is not timed.
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
			saveCtx, saveCancel := context.WithTimeout(promptCtx, cliDiscDiscoveryTimeout)
			err := coreSvc.SavePlaylistSelection(saveCtx, absPath, []string{playlists[0].File}, false)
			saveCancel()
			if err != nil {
				if isCtxErr(err) {
					return fmt.Errorf("upbrr: BDMV playlist selection save cancelled for %s: %w", absPath, err)
				}
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
			saveCtx, saveCancel := context.WithTimeout(promptCtx, cliDiscDiscoveryTimeout)
			err := coreSvc.SavePlaylistSelection(saveCtx, absPath, selected, true)
			saveCancel()
			if err != nil {
				if isCtxErr(err) {
					return fmt.Errorf("upbrr: BDMV playlist selection save cancelled for %s: %w", absPath, err)
				}
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
			saveCtx, saveCancel := context.WithTimeout(promptCtx, cliDiscDiscoveryTimeout)
			err := coreSvc.SavePlaylistSelection(saveCtx, absPath, selected, false)
			saveCancel()
			if err != nil {
				if isCtxErr(err) {
					return fmt.Errorf("upbrr: BDMV playlist selection save cancelled for %s: %w", absPath, err)
				}
				logger.Warnf("cli: save playlist selection: %v", err)
			} else {
				fmt.Printf("[*] Selected %d playlist(s)\n", len(selected))
			}
			break
		}

		fmt.Printf("[!] Please try again.\n")
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
		printTerminalWarning(w)
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
		fmt.Printf("imported config from %s (%d warnings)\n", formatPathLabel(importPath), len(warnings))
	} else {
		fmt.Printf("imported config from %s\n", formatPathLabel(importPath))
	}
	return nil
}
