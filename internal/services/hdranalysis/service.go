// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package hdranalysis extracts and renders bounded native HDR10+ metadata.
package hdranalysis

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"image/png"
	"io"
	"os"
	"path/filepath"

	hdr "github.com/Audionut/go-hdr10-plus"
	"github.com/Audionut/go-hdr10-plus/extract"
	bridge "github.com/Audionut/go-hdr10-plus/integration/bdinfo"
	bd "github.com/autobrr/go-bdinfo/pkg/bdinfo"
	gomediainfo "github.com/autobrr/go-mediainfo"

	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	MaxSidecarBytes       = 256 << 20
	MaxFrames             = 500000
	MaxPayloads           = 500000
	MaxRestoreBytes       = 2 << 30
	MKVRetainedBytes      = 2 << 30
	DiscRetainedBytes     = 2 << 30
	DiscScanRetainedBytes = 3 << 30
	// DependencyFingerprint identifies the pinned native readers for retained metadata reuse.
	DependencyFingerprint = "hdr:7cddd9fc0675;bdinfo:6250d2b849ca"
)

// Admission is shared by combined disc preparation, standalone analysis and workflow rendering.
type Admission struct{ slot chan struct{} }

// NewAdmission creates a single-slot limiter for native extraction and rendering.
func NewAdmission() *Admission { return &Admission{slot: make(chan struct{}, 1)} }

var processAdmission = NewAdmission()

// ProcessAdmission is shared across production cores, including runtime replacement.
func ProcessAdmission() *Admission { return processAdmission }

// Acquire waits for the analysis slot or context cancellation.
// The caller must invoke the returned release callback exactly once.
func (a *Admission) Acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("HDR admission: %w", err)
	}
	select {
	case a.slot <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-a.slot
			return nil, fmt.Errorf("HDR admission: %w", err)
		}
		return func() { <-a.slot }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("HDR admission: %w", ctx.Err())
	}
}

// Service shares admission across HDR extraction, retention and plot generation.
type Service struct {
	admission *Admission
	logger    api.Logger
}

// Session holds the single admission slot while extraction, retention and rendering run.
type Session struct{ service *Service }

// WithSession holds admission until run returns and propagates its error.
// Use Session methods inside run to avoid acquiring the same slot again.
func (s *Service) WithSession(ctx context.Context, run func(*Session) error) error {
	s.progress(ctx, "inspecting")
	release, err := s.admission.Acquire(ctx)
	if err != nil {
		return Classify(err)
	}
	defer release()
	return run(&Session{service: s})
}

// ReportOutcome reports the settled target aggregate, independently of admission callbacks.
func (s *Service) ReportOutcome(ctx context.Context, status api.StageStatus) {
	phase := string(status)
	if status == api.StageStatusCompleted {
		phase = "complete"
	}
	s.progress(ctx, phase)
}

// ReportFailure keeps sanitized dependency diagnostics out of public target failures.
func (s *Service) ReportFailure(ctx context.Context, stage string, err error) {
	if typed, ok := errors.AsType[*api.HDRAnalysisError](err); ok && typed.Unwrap() != nil {
		err = typed.Unwrap()
	}
	logging.FromContext(ctx, s.logger).Warnf("HDR analysis: stage=%s state=failed cause=%s", stage, logging.SanitizeMessage(err.Error()))
}

// ExtractFile requires MediaInfo-confirmed HDR10+ on a unique HEVC Matroska track.
// Native extraction resolves the actual track number using the session's held admission.
func (s *Session) ExtractFile(ctx context.Context, pathValue string) (*extract.Extraction, error) {
	return s.service.extractFile(ctx, pathValue)
}

// ScanDisc performs a combined native BDInfo/HDR scan using held admission.
// The outcome retains playlist timelines and separate HDR target failures.
func (s *Session) ScanDisc(ctx context.Context, options bd.Options) (*bridge.Outcome, error) {
	return s.service.scanDisc(ctx, options)
}

// Render validates metadata and publishes a PNG using held admission.
// Cancellation during synchronous plotting is observed when plotting returns.
func (s *Session) Render(ctx context.Context, extraction *extract.Extraction, peak api.HDRPeakSource, title, target string) error {
	return s.service.render(ctx, extraction, peak, title, target)
}

// New constructs a service with initialized shared admission; a nil logger disables logging.
func New(admission *Admission, logger api.Logger) *Service {
	if logger == nil {
		logger = api.NopLogger{}
	}
	return &Service{admission: admission, logger: logger}
}

// Progress reports an analysis phase and optional workflow completion estimate.
// Completed and Total use percentage units when Total is 100; zero Total omits the estimate.
type Progress struct {
	Phase     string
	Completed int
	Total     int
	Message   string
}
type progressKey struct{}

// DiscoverDisc reads playlist metadata without scanning transport streams.
func DiscoverDisc(ctx context.Context, options bd.Options) (bd.Result, error) {
	result, err := bd.DiscoverPlaylists(ctx, options)
	if err != nil {
		return result, fmt.Errorf("discover HDR disc targets: %w", err)
	}
	return result, nil
}

// WithProgress attaches a synchronous callback for phase and completion updates.
func WithProgress(ctx context.Context, report func(Progress)) context.Context {
	return context.WithValue(ctx, progressKey{}, report)
}

func (s *Service) progress(ctx context.Context, phase string) {
	logging.FromContext(ctx, s.logger).Infof("HDR analysis: phase=%s", phase)
	progress := Progress{Phase: phase}
	switch phase {
	case "inspecting":
		progress.Message = "Waiting for the HDR analysis slot"
	case "inspecting_track":
		progress.Message = "Checking the MediaInfo HDR10+ video track"
	case "reading_metadata":
		progress.Message = "Reading HDR10+ metadata"
	case "assembling_playlist":
		progress.Message = "Assembling the selected playlist timeline"
	case "rendering":
		progress.Message, progress.Completed, progress.Total = "Rendering the HDR10+ plot", 95, 100
	case "saving":
		progress.Message, progress.Completed, progress.Total = "Saving the HDR10+ plot", 98, 100
	case "validating_metadata":
		progress.Message, progress.Completed, progress.Total = "Validating HDR10+ metadata", 90, 100
	case "complete":
		progress.Message = "HDR10+ analysis complete"
	case "failed":
		progress.Message = "HDR10+ analysis failed; inspect the target failures"
	case "partial":
		progress.Message = "HDR10+ analysis finished with failed targets"
	case "canceled":
		progress.Message = "HDR10+ analysis canceled"
	case "interrupted":
		progress.Message = "HDR10+ analysis interrupted"
	default:
		progress.Message = "HDR10+ analysis: " + phase
	}
	s.reportProgress(ctx, progress)
}

func (s *Service) reportProgress(ctx context.Context, progress Progress) {
	status := api.StageStatusRunning
	switch progress.Phase {
	case "complete":
		status = api.StageStatusCompleted
	case "failed":
		status = api.StageStatusFailed
	case "partial":
		status = api.StageStatusPartial
	case "canceled":
		status = api.StageStatusCanceled
	case "interrupted":
		status = api.StageStatusInterrupted
	}
	api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
		Phase:     "hdr_analysis_" + progress.Phase,
		Kind:      "hdr_analysis",
		Label:     "HDR10+ analysis",
		Status:    status,
		Message:   progress.Message,
		Completed: progress.Completed,
		Total:     progress.Total,
	})
	if report, ok := ctx.Value(progressKey{}).(func(Progress)); ok && report != nil {
		report(progress)
	}
}

func (s *Service) scanDisc(ctx context.Context, options bd.Options) (*bridge.Outcome, error) {
	s.progress(ctx, "reading_metadata")
	options.IncludeTimeline = true
	outcome, err := bridge.Scan(ctx, bridge.Options{
		BDInfo:               options,
		Limits:               extract.Limits{MaxRetainedBytes: DiscRetainedBytes},
		MaxScanRetainedBytes: DiscScanRetainedBytes,
	})
	if err != nil {
		return outcome, fmt.Errorf("HDR disc scan: %w", err)
	}
	s.progress(ctx, "assembling_playlist")
	return outcome, nil
}

func (s *Service) extractFile(ctx context.Context, pathValue string) (*extract.Extraction, error) {
	s.progress(ctx, "inspecting_track")
	report, err := gomediainfo.AnalyzeFileContext(ctx, pathValue)
	if err != nil {
		return nil, Classify(err)
	}
	payload, err := gomediainfo.Render([]gomediainfo.Report{report}, gomediainfo.OutputJSON)
	if err != nil {
		return nil, Classify(err)
	}
	var doc mediafacts.MediaInfoDocument
	if err := json.Unmarshal([]byte(payload), &doc); err != nil {
		return nil, Classify(err)
	}
	if !mediafacts.HDR10PlusTrackFromMediaInfo(doc) {
		return nil, fmt.Errorf("check HDR track: %w", api.NewHDRAnalysisError(api.HDRAnalysisFailure{
			Code:    api.HDRAnalysisFailureUnsupportedInput,
			Message: "MediaInfo must confirm HDR10+ on a unique HEVC video track.",
		}, nil))
	}
	s.progress(ctx, "reading_metadata")
	file, err := os.Open(pathValue)
	if err != nil {
		return nil, Classify(err)
	}
	stat, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, Classify(err)
	}
	reader := newFileReader(ctx, file, stat.Size(), logging.FromContext(ctx, s.logger), func(progress Progress) { s.reportProgress(ctx, progress) })
	result, extractErr := extract.Extract(ctx, reader, extract.Options{
		TrackID: 0,
		Limits:  extract.Limits{MaxRetainedBytes: MKVRetainedBytes, MaxFrames: MaxFrames},
	})
	closeErr := file.Close()
	if extractErr != nil {
		return nil, Classify(extractErr)
	}
	if closeErr != nil {
		return nil, Classify(closeErr)
	}
	s.progress(ctx, "validating_metadata")
	if err := ValidateExtraction(result, true); err != nil {
		return nil, Classify(err)
	}
	return result, nil
}

// ValidateExtraction checks storage bounds and actual stream binding before retaining a complete result.
func ValidateExtraction(result *extract.Extraction, uniqueTrack bool) error {
	if result == nil || len(result.Frames) == 0 || len(result.Payloads) == 0 {
		return extract.ErrIncomplete
	}
	if len(result.Frames) > MaxFrames || len(result.Payloads) > MaxPayloads || len(result.SceneStarts) > MaxFrames {
		return extract.ErrResourceLimit
	}
	track := result.Frames[0].Stream.TrackID
	if track == 0 {
		return extract.ErrAmbiguousTrack
	}
	for _, frame := range result.Frames {
		if len(frame.Stream.SourceID)+len(frame.Stream.ClipID) > 1024 {
			return extract.ErrResourceLimit
		}
		if frame.Stream.TrackID == 0 || uniqueTrack && frame.Stream != result.Frames[0].Stream {
			return extract.ErrAmbiguousTrack
		}
	}
	_, err := result.PlotMetadata()
	if err != nil {
		return fmt.Errorf("validate HDR plot metadata: %w", err)
	}
	return nil
}

// Render holds admission through synchronous rendering and checked atomic publication.
// Cancellation during Render is observed when the synchronous library call returns.
func (s *Service) Render(ctx context.Context, extraction *extract.Extraction, peak api.HDRPeakSource, title, target string) error {
	release, err := s.admission.Acquire(ctx)
	if err != nil {
		return Classify(err)
	}
	defer release()
	return s.render(ctx, extraction, peak, title, target)
}

func (s *Service) render(ctx context.Context, extraction *extract.Extraction, peak api.HDRPeakSource, title, target string) error {
	if err := ValidateExtraction(extraction, false); err != nil {
		return Classify(err)
	}
	peak, err := peak.Normalize()
	if err != nil {
		return Classify(err)
	}
	if err := ctx.Err(); err != nil {
		return Classify(err)
	}
	model, err := extraction.PlotMetadata()
	if err != nil {
		return Classify(err)
	}
	if err := ctx.Err(); err != nil {
		return Classify(err)
	}
	s.progress(ctx, "rendering")
	var source hdr.PeakSource
	switch peak {
	case api.HDRPeakHistogram:
		source = hdr.PeakHistogram
	case api.HDRPeakHistogram99:
		source = hdr.PeakHistogram99
	case api.HDRPeakMaxSCL:
		source = hdr.PeakMaxSCL
	case api.HDRPeakMaxSCLLuminance:
		source = hdr.PeakMaxSCLLuminance
	}
	image, err := hdr.Render(model, hdr.Options{Title: title, PeakSource: source})
	if err != nil {
		return Classify(err)
	}
	if err := ctx.Err(); err != nil {
		return Classify(err)
	}
	s.progress(ctx, "saving")
	if err := PublishFile(ctx, target, func(out io.Writer) error { return png.Encode(out, image) }); err != nil {
		return fmt.Errorf(
			"publish HDR plot: %w",
			api.NewHDRAnalysisError(api.HDRAnalysisFailure{Code: api.HDRAnalysisFailureOutput, Message: "the HDR plot could not be saved"}, err),
		)
	}
	return nil
}

// PublishFile uses a fresh private sibling and never replaces an existing target.
// The caller owns an exclusive attempt directory; other attempts cannot race this target.
func PublishFile(ctx context.Context, target string, encode func(io.Writer) error) error {
	file, err := os.CreateTemp(filepath.Dir(target), ".hdr-staging-*")
	if err != nil {
		return fmt.Errorf("create HDR staging file: %w", err)
	}
	staging := file.Name()
	defer func() { _ = os.Remove(staging) }()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("protect HDR staging file: %w", err)
	}
	writeErr := encode(file)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return fmt.Errorf("write HDR artifact: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("publish HDR artifact: %w", err)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			err = os.ErrExist
		}
		return fmt.Errorf("HDR artifact destination is unavailable: %w", err)
	}
	if err := os.Rename(staging, target); err != nil {
		return fmt.Errorf("publish HDR artifact: %w", err)
	}
	return nil
}

// Classify preserves dependency causes while exposing only fixed, path-free messages.
func Classify(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := api.AsHDRAnalysisFailure(err); ok {
		return err
	}
	code, message := api.HDRAnalysisFailureRead, "HDR metadata could not be read"
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		code, message = api.HDRAnalysisFailureInterrupted, "HDR analysis timed out"
	case errors.Is(err, context.Canceled):
		code, message = api.HDRAnalysisFailureCanceled, "HDR analysis was canceled"
	case errors.Is(err, extract.ErrNoMetadata):
		code, message = api.HDRAnalysisFailureAbsent, "the complete source contains no HDR10+ metadata"
	case errors.Is(err, extract.ErrAmbiguousTrack):
		code, message = api.HDRAnalysisFailureAmbiguousBinding, "the source contains more than one HEVC video track"
	case errors.Is(err, extract.ErrUnsupportedInput):
		code, message = api.HDRAnalysisFailureUnsupportedInput, "this source format or video mapping is unsupported"
	case errors.Is(err, extract.ErrUnsupportedMetadata):
		code, message = api.HDRAnalysisFailureUnsupportedMetadata, "this HDR10+ metadata dialect is unsupported"
	case errors.Is(err, extract.ErrInvalidBitstream), errors.Is(err, hdr.ErrInvalidMetadata):
		code, message = api.HDRAnalysisFailureInvalidBitstream, "the source contains invalid HDR10+ or video metadata"
	case errors.Is(err, extract.ErrIncomplete), errors.Is(err, io.ErrUnexpectedEOF):
		code, message = api.HDRAnalysisFailureIncomplete, "HDR metadata extraction did not complete"
	case errors.Is(err, extract.ErrResourceLimit):
		code, message = api.HDRAnalysisFailureResourceLimit, "HDR analysis exceeded its fixed resource limit"
	}
	return fmt.Errorf("HDR analysis: %w", api.NewHDRAnalysisError(api.HDRAnalysisFailure{Code: code, Message: message}, err))
}
