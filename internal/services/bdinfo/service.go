// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bdinfo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path" //nolint:depguard // BDInfo timeline source names are slash-delimited disc data, not host filesystem paths.
	"path/filepath"
	"strings"
	"sync"

	bdrunner "github.com/autobrr/go-bdinfo/pkg/bdinfo"

	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/metadata/discparse"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

type runRequest struct {
	BDMVPath        string
	PlaylistName    string
	ReportPath      string
	OnProgress      func(bdrunner.ProgressEvent)
	SummaryOnly     bool
	IncludeTimeline bool
}

var runBDInfo = func(ctx context.Context, req runRequest) (bdrunner.Result, error) {
	settings := bdrunner.DefaultSettings(filepath.Dir(req.ReportPath))
	settings.GenerateStreamDiagnostics = false
	settings.ExtendedStreamDiagnostics = true
	settings.SummaryOnly = true
	settings.GenerateTextSummary = true
	settings.PlaylistOnly = req.PlaylistName
	settings.SummaryOnly = req.SummaryOnly

	return bdrunner.Run(ctx, bdrunner.Options{
		Path:            req.BDMVPath,
		ReportPath:      req.ReportPath,
		Settings:        settings,
		OnProgress:      req.OnProgress,
		IncludeTimeline: req.IncludeTimeline,
	})
}

// scanProgress logs stage-local thresholds. Only execute logs 100%, after the
// scanner and report persistence succeed; scanner completion events are earlier.
type scanProgress struct {
	logger       api.Logger
	bdmvPath     string
	playlistName string
	mu           sync.Mutex
	stage        bdrunner.Stage
	lastPercent  int
}

func (p *scanProgress) emit(event bdrunner.ProgressEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if event.Stage != p.stage {
		p.stage = event.Stage
		p.lastPercent = 0
	}

	//nolint:exhaustive // Only measured scan stages have meaningful percentages.
	switch event.Stage {
	case bdrunner.StageClipInfo, bdrunner.StagePlaylist, bdrunner.StageStream, bdrunner.StageInitialize:
	default:
		return
	}

	var percent float64
	switch {
	case event.TotalBytes > 0:
		percent = float64(event.ProcessedBytes) / float64(event.TotalBytes) * 100
	case event.Total > 0:
		percent = float64(event.Completed) / float64(event.Total) * 100
	default:
		return
	}

	for next := p.lastPercent + 5; next <= 95 && float64(next) <= percent; next += 5 {
		p.log(event.Stage, next)
		p.lastPercent = next
	}
}

func (p *scanProgress) log(stage bdrunner.Stage, percent int) {
	p.logger.Debugf("bdinfo: progress stage=%s percent=%d%% bdmvPath=%s playlist=%s", stage, percent, p.bdmvPath, p.playlistName)
}

func emitProgressEvent(reporter ProgressReporter, event bdrunner.ProgressEvent) {
	if reporter == nil {
		return
	}
	//nolint:exhaustive // We intentionally emit progress only for user-facing stages.
	switch event.Stage {
	case bdrunner.StageStarting, bdrunner.StageScanning:
		if strings.TrimSpace(event.Path) != "" {
			reporter("Scanning: " + event.Path)
		}
	case bdrunner.StageClipInfo, bdrunner.StagePlaylist, bdrunner.StageStream:
		emitDetailedProgressEvent(reporter, event)
	case bdrunner.StageDiscovered:
		reporter(fmt.Sprintf("Found %d playlists, %d clip infos, %d streams", event.Playlists, event.ClipInfos, event.Streams))
	case bdrunner.StageScanComplete:
		reporter("Scan phase complete")
	case bdrunner.StageRenderingReport:
		reporter("Rendering report")
	case bdrunner.StageDone:
		if event.Elapsed > 0 {
			reporter(fmt.Sprintf("Scan complete in %s", event.Elapsed.Round(1e6)))
		} else {
			reporter("Scan complete")
		}
	}
}

func emitDetailedProgressEvent(reporter ProgressReporter, event bdrunner.ProgressEvent) {
	if reporter == nil {
		return
	}

	stage := strings.ToUpper(string(event.Stage))

	if event.Total > 0 {
		if event.TotalBytes > 0 {
			percentage := float64(event.ProcessedBytes) / float64(event.TotalBytes) * 100
			reporter(fmt.Sprintf("%s: %d/%d (%.1f%%)", stage, event.Completed, event.Total, percentage))
			return
		}
		reporter(fmt.Sprintf("%s: %d/%d", stage, event.Completed, event.Total))
		return
	}

	reporter(stage)
}

// Service runs the in-process BDInfo scanner and parses its persisted text
// reports.
type Service struct {
	logger api.Logger
	hdr    *hdranalysis.Service
}

// ScanResult contains the rendered report payload and its persisted location.
type ScanResult struct {
	ReportPath string
	ReportText string
}

type hdrCaptureKey struct{}
type HDRCapture func(*hdranalysis.NativeDiscOutcome) error

type hdrCaptureRequest struct {
	capture   HDRCapture
	playlists []string
}

// WithHDRCapture collects HDR metadata during the next report scan.
func WithHDRCapture(ctx context.Context, capture HDRCapture, playlists ...string) context.Context {
	return context.WithValue(ctx, hdrCaptureKey{}, hdrCaptureRequest{capture: capture, playlists: append([]string(nil), playlists...)})
}

// SetHDRService installs the same admission owner used by workflow and standalone analysis.
func (s *Service) SetHDRService(service *hdranalysis.Service) { s.hdr = service }

type progressReporterKey struct{}

// ProgressReporter receives user-facing progress messages derived from BDInfo
// scanner stages.
type ProgressReporter func(line string)

// WithProgressReporter attaches a progress reporter to ctx. A nil reporter
// leaves ctx unchanged.
func WithProgressReporter(ctx context.Context, reporter ProgressReporter) context.Context {
	if reporter == nil {
		return ctx
	}
	return context.WithValue(ctx, progressReporterKey{}, reporter)
}

func progressReporterFromContext(ctx context.Context) ProgressReporter {
	if ctx == nil {
		return nil
	}
	reporter, _ := ctx.Value(progressReporterKey{}).(ProgressReporter)
	return reporter
}

func normalizePlaylistSelector(playlistFile string) string {
	playlistName := strings.TrimSpace(playlistFile)
	playlistName = strings.ReplaceAll(playlistName, "\\", "/")
	if idx := strings.LastIndex(playlistName, "/"); idx >= 0 {
		playlistName = playlistName[idx+1:]
	}
	playlistName = strings.TrimSpace(playlistName)
	if !strings.HasSuffix(strings.ToUpper(playlistName), ".MPLS") {
		playlistName += ".MPLS"
	}
	return strings.ToUpper(playlistName)
}

// New returns a BDInfo service, replacing a nil logger with [api.NopLogger].
func New(logger api.Logger) *Service {
	if logger == nil {
		logger = api.NopLogger{}
	}
	return &Service{logger: logger}
}

// ExecuteForPlaylist scans one playlist and writes its report to outputPath;
// newly created report files use mode 0600. playlistFile is reduced to an
// uppercased basename and gains an .MPLS suffix when absent; the returned path
// may be replaced by the scanner's reported output path.
func (s *Service) ExecuteForPlaylist(ctx context.Context, bdmvPath string, playlistFile string, outputPath string, summaryOnly bool) (string, error) {
	result, err := s.execute(ctx, bdmvPath, normalizePlaylistSelector(playlistFile), outputPath, summaryOnly)
	if err != nil {
		return "", err
	}
	return result.ReportPath, nil
}

// ExecuteFullScan scans the full disc, writes BD_FULL.txt beneath outputDir,
// and returns both the persisted path and report text. A newly created report
// file uses mode 0600. selectedPlaylists limits ISO read-error validation to the
// content the caller will consume; absent names validate all reported playlists.
func (s *Service) ExecuteFullScan(ctx context.Context, bdmvPath string, outputDir string, selectedPlaylists ...string) (ScanResult, error) {
	return s.execute(ctx, bdmvPath, "", filepath.Join(outputDir, "BD_FULL.txt"), false, selectedPlaylists...)
}

func (s *Service) execute(
	ctx context.Context,
	bdmvPath string,
	playlistName string,
	outputPath string,
	summaryOnly bool,
	selectedPlaylists ...string,
) (ScanResult, error) {
	logger := logging.FromContext(ctx, s.logger)

	if err := ctx.Err(); err != nil {
		return ScanResult{}, fmt.Errorf("bdinfo: scan canceled: %w", err)
	}
	reporter := progressReporterFromContext(ctx)
	progress := scanProgress{
		logger:       logger,
		bdmvPath:     bdmvPath,
		playlistName: playlistName,
	}
	outputDir := filepath.Dir(outputPath)

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return ScanResult{}, fmt.Errorf("bdinfo: create output dir: %w", err)
	}

	logger.Debugf("bdinfo: bdmvPath=%s, playlistFile=%s, outputDir=%s", bdmvPath, playlistName, filepath.Dir(outputPath))
	if playlistName != "" {
		logger.Debugf("bdinfo: normalized playlist name: %s", playlistName)
		logger.Debugf("bdinfo: running in-process for playlist %s", playlistName)
	} else {
		logger.Debugf("bdinfo: running in-process full-disc scan")
	}
	iso := false
	if strings.EqualFold(filepath.Ext(bdmvPath), ".iso") {
		info, err := os.Stat(bdmvPath)
		if err != nil {
			return ScanResult{}, fmt.Errorf("bdinfo: inspect ISO source: %w", err)
		}
		iso = info.Mode().IsRegular()
	}
	req := runRequest{
		BDMVPath:     bdmvPath,
		PlaylistName: playlistName,
		ReportPath:   outputPath,
		OnProgress: func(event bdrunner.ProgressEvent) {
			progress.emit(event)
			emitProgressEvent(reporter, event)
		},
		SummaryOnly:     summaryOnly,
		IncludeTimeline: iso,
	}
	var result bdrunner.Result
	var err error
	if capture, ok := ctx.Value(hdrCaptureKey{}).(hdrCaptureRequest); ok && capture.capture != nil && s.hdr != nil {
		err = s.hdr.WithSession(ctx, func(session *hdranalysis.Session) error {
			settings := bdrunner.DefaultSettings(filepath.Dir(outputPath))
			settings.GenerateStreamDiagnostics = false
			settings.ExtendedStreamDiagnostics = true
			settings.SummaryOnly = summaryOnly
			settings.GenerateTextSummary = true
			settings.PlaylistOnly = playlistName
			options := bdrunner.Options{
				Path:            bdmvPath,
				ReportPath:      outputPath,
				Settings:        settings,
				OnProgress:      req.OnProgress,
				IncludeTimeline: true,
			}
			outcome, scanErr := session.ScanNativeDisc(
				ctx,
				options,
				api.HDRAnalysisFormatsBoth,
				0,
				capture.playlists[:min(len(capture.playlists), api.HDRAnalysisMaxTargets)]...)
			defer outcome.Release()
			if scanErr != nil {
				return fmt.Errorf("combined HDR report scan: %w", scanErr)
			}
			if outcome == nil || outcome.Report == nil {
				return errors.New("bdinfo: combined scan returned no report")
			}
			result = *outcome.Report
			if captureErr := capture.capture(outcome); captureErr != nil {
				logger.Warnf("bdinfo: HDR capture state=unavailable")
			}
			return nil
		})
	} else {
		result, err = runBDInfo(ctx, req)
	}
	if err != nil {
		logger.Debugf("bdinfo: in-process execution failed: %v", err)
		return ScanResult{}, fmt.Errorf("bdinfo: execution failed: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return ScanResult{}, fmt.Errorf("bdinfo: scan canceled: %w", err)
	}
	if iso {
		if playlistName != "" {
			selectedPlaylists = []string{playlistName}
		}
		if err := validateISOScan(result, selectedPlaylists); err != nil {
			return ScanResult{}, err
		}
	}
	if strings.TrimSpace(result.ReportPath) != "" {
		outputPath = result.ReportPath
	}

	reportText := result.Report
	if strings.TrimSpace(reportText) == "" {
		return ScanResult{}, errors.New("bdinfo: empty report content")
	}

	if err := os.WriteFile(outputPath, []byte(reportText), 0o600); err != nil {
		return ScanResult{}, fmt.Errorf("bdinfo: write output: %w", err)
	}

	if _, err := os.Stat(outputPath); err != nil {
		return ScanResult{}, fmt.Errorf("bdinfo: output not found: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return ScanResult{}, fmt.Errorf("bdinfo: scan canceled: %w", err)
	}

	if result.Scan.ScanError == "" && len(result.Scan.FileErrors) == 0 {
		progress.log(bdrunner.StageDone, 100)
	}
	if playlistName != "" {
		logger.Debugf("bdinfo: successfully completed for playlist %s", playlistName)
	} else {
		logger.Debugf("bdinfo: successfully completed full-disc scan")
	}
	logger.Debugf("bdinfo: output file found at %s", outputPath)
	return ScanResult{
		ReportPath: outputPath,
		ReportText: reportText,
	}, nil
}

// validateISOScan rejects selected-content read failures without treating an
// unrelated file warning or unsupported SSIF clock mapping as a failed report.
func validateISOScan(result bdrunner.Result, playlistNames []string) error {
	if result.Scan.ScanError != "" {
		return errors.New("bdinfo: incomplete ISO scan; use a readable decrypted 3D Blu-ray image")
	}
	if len(result.Scan.FileErrors) == 0 {
		return nil
	}
	selected := make(map[string]struct{})
	remaining := make(map[string]bool, len(playlistNames))
	for _, name := range playlistNames {
		remaining[normalizePlaylistSelector(name)] = true
	}
	if len(result.Timelines) == 0 {
		return errors.New("bdinfo: cannot associate ISO scan errors with selected content")
	}
	for _, timeline := range result.Timelines {
		name := normalizePlaylistSelector(timeline.Name)
		if len(playlistNames) > 0 {
			if !remaining[name] {
				continue
			}
			delete(remaining, name)
		}
		selected[strings.ToUpper(timeline.Name)] = struct{}{}
		if len(timeline.Items) == 0 {
			return errors.New("bdinfo: cannot associate ISO scan errors with selected content")
		}
		for _, item := range timeline.Items {
			if len(item.Angles) == 0 || item.Angles[0].Source.LogicalClip == "" {
				return errors.New("bdinfo: cannot associate ISO scan errors with selected content")
			}
			source := item.Angles[0].Source
			clip := strings.ToUpper(source.LogicalClip)
			selected[clip] = struct{}{}
			selected[strings.TrimSuffix(clip, path.Ext(clip))+".CLPI"] = struct{}{}
			selected[strings.ToUpper(path.Base(source.Path))] = struct{}{}
		}
	}
	if len(remaining) > 0 {
		return errors.New("bdinfo: cannot associate ISO scan errors with selected content")
	}
	for name := range result.Scan.FileErrors {
		if _, failed := selected[strings.ToUpper(name)]; failed {
			return errors.New("bdinfo: selected ISO content could not be read; use a readable decrypted 3D Blu-ray image")
		}
	}
	return nil
}

// ParseOutput extracts title, label, size, length, and quick-summary fields from
// either a full BDInfo report or a persisted standalone quick summary. Missing
// fields are omitted without an error.
func (s *Service) ParseOutput(ctx context.Context, filePath string) (map[string]any, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("bdinfo: read output: %w", err)
	}

	text := string(content)
	result := make(map[string]any)

	// Extract basic info
	if idx := strings.Index(text, "Disc Title:"); idx >= 0 {
		end := strings.Index(text[idx:], "\n")
		if end > 0 {
			result["title"] = strings.TrimSpace(text[idx+11 : idx+end])
		}
	}

	if idx := strings.Index(text, "Disc Label:"); idx >= 0 {
		end := strings.Index(text[idx:], "\n")
		if end > 0 {
			result["label"] = strings.TrimSpace(text[idx+11 : idx+end])
		}
	}

	if idx := strings.Index(text, "Disc Size:"); idx >= 0 {
		end := strings.Index(text[idx:], "\n")
		if end > 0 {
			result["size"] = strings.TrimSpace(text[idx+10 : idx+end])
		}
	}

	if idx := strings.Index(text, "Length:"); idx >= 0 {
		end := strings.Index(text[idx:], "\n")
		if end > 0 {
			result["length"] = strings.TrimSpace(text[idx+7 : idx+end])
		}
	}

	if summary, _, _ := discparse.SplitBDInfoReport(text); strings.TrimSpace(summary) != "" {
		result["summary"] = summary
	}

	logging.FromContext(ctx, s.logger).Debugf("bdinfo: parsed output with %d fields", len(result))
	return result, nil
}
