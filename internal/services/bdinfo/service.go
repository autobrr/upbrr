// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bdinfo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	bridge "github.com/Audionut/go-hdr10-plus/integration/bdinfo"
	bdrunner "github.com/autobrr/go-bdinfo/pkg/bdinfo"

	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/metadata/discparse"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

type runRequest struct {
	BDMVPath     string
	PlaylistName string
	ReportPath   string
	Reporter     ProgressReporter
	SummaryOnly  bool
}

var runBDInfo = func(ctx context.Context, req runRequest) (bdrunner.Result, error) {
	settings := bdrunner.DefaultSettings(filepath.Dir(req.ReportPath))
	settings.GenerateStreamDiagnostics = false
	settings.ExtendedStreamDiagnostics = true
	settings.SummaryOnly = true
	settings.GenerateTextSummary = true
	settings.PlaylistOnly = req.PlaylistName
	settings.SummaryOnly = req.SummaryOnly

	var reporter func(bdrunner.ProgressEvent)
	if req.Reporter != nil {
		reporter = func(event bdrunner.ProgressEvent) {
			emitProgressEvent(req.Reporter, event)
		}
	}
	return bdrunner.Run(ctx, bdrunner.Options{
		Path:       req.BDMVPath,
		ReportPath: req.ReportPath,
		Settings:   settings,
		OnProgress: reporter,
	})
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
type HDRCapture func(*bridge.Outcome) error

// WithHDRCapture collects HDR metadata during the next report scan.
func WithHDRCapture(ctx context.Context, capture HDRCapture) context.Context {
	return context.WithValue(ctx, hdrCaptureKey{}, capture)
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
// file uses mode 0600.
func (s *Service) ExecuteFullScan(ctx context.Context, bdmvPath string, outputDir string) (ScanResult, error) {
	return s.execute(ctx, bdmvPath, "", filepath.Join(outputDir, "BD_FULL.txt"), false)
}

func (s *Service) execute(ctx context.Context, bdmvPath string, playlistName string, outputPath string, summaryOnly bool) (ScanResult, error) {
	logger := logging.FromContext(ctx, s.logger)

	if err := ctx.Err(); err != nil {
		return ScanResult{}, fmt.Errorf("bdinfo: scan canceled: %w", err)
	}
	reporter := progressReporterFromContext(ctx)
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
	req := runRequest{
		BDMVPath:     bdmvPath,
		PlaylistName: playlistName,
		ReportPath:   outputPath,
		Reporter:     reporter,
		SummaryOnly:  summaryOnly,
	}
	var result bdrunner.Result
	var err error
	if capture, ok := ctx.Value(hdrCaptureKey{}).(HDRCapture); ok && capture != nil && s.hdr != nil {
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
				IncludeTimeline: true,
			}
			if reporter != nil {
				options.OnProgress = func(event bdrunner.ProgressEvent) { emitProgressEvent(reporter, event) }
			}
			outcome, scanErr := session.ScanDisc(ctx, options)
			if scanErr != nil {
				return fmt.Errorf("combined HDR report scan: %w", scanErr)
			}
			if outcome == nil || outcome.Report == nil {
				return errors.New("bdinfo: combined scan returned no report")
			}
			result = *outcome.Report
			if captureErr := capture(outcome); captureErr != nil {
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

	if playlistName != "" {
		logger.Debugf("bdinfo: successfully completed for playlist %s", playlistName)
	} else {
		logger.Debugf("bdinfo: successfully completed full-disc scan")
	}

	if _, err := os.Stat(outputPath); err != nil {
		return ScanResult{}, fmt.Errorf("bdinfo: output not found: %w", err)
	}

	logger.Debugf("bdinfo: output file found at %s", outputPath)
	return ScanResult{
		ReportPath: outputPath,
		ReportText: reportText,
	}, nil
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
