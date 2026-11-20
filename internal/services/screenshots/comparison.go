// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	gomediainfo "github.com/autobrr/go-mediainfo"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

// ComparisonCaptionVersion identifies the generated caption layout for retained pixels.
const ComparisonCaptionVersion = 5

// ComparisonDiscCaptionVersion uses authored title-relative display positions.
const ComparisonDiscCaptionVersion = 6

// Normalizers can rescale the output clock to microseconds. Frame identities
// and replay witnesses always retain the source PTS and timebase.
const comparisonNormalizedClockTolerance = 1e-6

// ComparisonSource describes one local video. Missing timing stays zero; callers
// own source fingerprint verification and alignment with other inputs.
type ComparisonSource struct {
	Codec, CodecProfile, NativeCodec         string
	NativePixelFormat                        string
	NativeColour                             string
	NativeChromaLocation                     string
	SARNumerator, SARDenominator             int64
	Interlace                                string
	Path                                     string
	Input                                    *filesystem.ResolvedComparisonInput
	InputFingerprint                         string
	ScratchDir                               string
	FrameRateNumerator, FrameRateDenominator int64
	// StartTimeSeconds is the demuxer's input start, used by input-relative -ss.
	StartTimeSeconds float64
	DurationSeconds  float64
	FrameRate        float64
	// FrameCount is reported video metadata, not an enumerated decoder index.
	FrameCount int64
	// VideoDurationSeconds is video-only timing for display-count estimation.
	// General duration may include a nonzero source-clock origin.
	VideoDurationSeconds float64
	Width                int
	Height               int
	WidthScale           float64
	HeightScale          float64
	ScreenshotWidth      int  `json:",omitempty"`
	ScreenshotHeight     int  `json:",omitempty"`
	ScreenshotTurn       bool `json:",omitempty"`
	ScreenshotMatrix     bool `json:",omitempty"`
	HDRFacts             api.HDRFacts
	// FileIdentity binds regular-file decoding to the file sampled by the caller.
	FileIdentity os.FileInfo `json:"-"`
}

// DisplaySize returns the square-pixel dimensions used by comparison captures.
func (s ComparisonSource) DisplaySize() (int, int) {
	width, height := s.Width, s.Height
	widthScale, heightScale := s.WidthScale, s.HeightScale
	if widthScale == 0 {
		widthScale = 1
	}
	if heightScale == 0 {
		heightScale = 1
	}
	if widthScale != 1 || heightScale != 1 {
		width = roundToEven(float64(width) * widthScale)
		height = roundToEven(float64(height) * heightScale)
	}
	return width, height
}

// ScreenshotDisplaySize includes the file's display transform for sizing modes.
func (s ComparisonSource) ScreenshotDisplaySize() (int, int) {
	if s.ScreenshotWidth > 0 && s.ScreenshotHeight > 0 {
		return s.ScreenshotWidth, s.ScreenshotHeight
	}
	if s.Input != nil && s.SARNumerator > 0 && s.SARDenominator > 0 {
		return screenshotRasterSize(s.Width, s.Height, s.SARNumerator, s.SARDenominator)
	}
	return s.DisplaySize()
}

// MatchingDisplaySize returns the display dimensions after the transform used
// by visual analysis. Matching crops are measured in the oriented raster.
func (s ComparisonSource) MatchingDisplaySize() (int, int) {
	return s.matchingGeometry().DisplaySize()
}

func (s ComparisonSource) matchingGeometry() ComparisonSource {
	if s.Input == nil && s.ScreenshotTurn {
		s.Width, s.Height = s.Height, s.Width
		s.WidthScale, s.HeightScale = s.HeightScale, s.WidthScale
	}
	return s
}

// ComparisonFrameObservation identifies the decoded picture in the source clock.
// TimestampSeconds is PTS * TimeBaseNumerator / TimeBaseDenominator, not the
// requested seek or a frame ordinal. Empty PictureType means unknown.
type ComparisonFrameObservation struct {
	FrameID             string
	Identity            *api.ComparisonFrameIdentity
	Replay              *ComparisonFrameReplay
	PTS                 int64
	DurationTicks       int64
	TimeBaseNumerator   int64
	TimeBaseDenominator int64
	TimestampSeconds    float64
	// RelativeTimestampSeconds is the observed PTS time minus the input start.
	RelativeTimestampSeconds float64
	PictureType              string
	// transportChecksum binds a named analysis output to its raw buffer.
	transportChecksum                        string
	FrameRateNumerator, FrameRateDenominator int64
	Width, Height                            int
	SARNumerator, SARDenominator             int64
	PixelFormat, Interlace                   string
	Colour                                   string
	ChromaLocation                           string
	packetPosition                           *int64
}

// ComparisonCaptureRequest selects a seek relative to the file's start time.
// OverlayLabel optionally adds metadata-based frame counters, observed picture
// type, resolution when requested, applied tone mapping, then the label.
// Missing counters stay unknown.
// Labels accept at most 80 ASCII letters, digits, spaces, dots, dashes and
// underscores. Overlay enablement is comparison-local; text size follows the
// ordinary screenshot setting and scales with the source height.
type ComparisonCaptureRequest struct {
	Replay           *ComparisonFrameReplay
	Source           ComparisonSource
	TimestampSeconds float64
	OverlayLabel     string
	// ShowResolution includes inspected display and actual output dimensions.
	ShowResolution bool
	// ToneMap overrides the ordinary screenshot setting; nil inherits it.
	ToneMap *bool
	// OutputWidth/OutputHeight bound the canvas or the proportional fit.
	// Both zero retain the source size.
	OutputWidth  int
	OutputHeight int
	// ResizeOnly uses the caller's aspect-preserving dimensions without padding.
	ResizeOnly bool
	// ScreenshotSizing selects geometry based on the oriented decoder raster.
	ScreenshotSizing string
}

// ValidOutputSize checks the decoded result against the requested geometry.
func (r ComparisonCaptureRequest) ValidOutputSize(frame ComparisonFrame) bool {
	width, height := r.OutputWidth, r.OutputHeight
	switch r.ScreenshotSizing {
	case api.ComparisonSizingResize:
		sourceWidth, sourceHeight := frame.displaySize()
		if sourceWidth <= 0 || sourceHeight <= 0 {
			return false
		}
		// Match FFmpeg's force_original_aspect_ratio=decrease rounding.
		width, height = min(width, int(math.Round(float64(height)*float64(sourceWidth)/float64(sourceHeight)))),
			min(height, int(math.Round(float64(width)*float64(sourceHeight)/float64(sourceWidth))))
	case api.ComparisonSizingSource:
		width, height = frame.displaySize()
	default:
		if width == 0 && height == 0 {
			width, height = r.Source.DisplaySize()
		}
	}
	return width > 0 && height > 0 && frame.Width == width && frame.Height == height
}

func (frame ComparisonFrameObservation) displaySize() (int, int) {
	return screenshotRasterSize(frame.Width, frame.Height, frame.SARNumerator, frame.SARDenominator)
}

func screenshotRasterSize(width, height int, sarNumerator, sarDenominator int64) (int, int) {
	if sarNumerator > 0 && sarDenominator > 0 {
		sar := float64(sarNumerator) / float64(sarDenominator)
		switch {
		case sar > 1:
			width = max(1, int(math.Round(float64(width)*sar)))
		case sar < 1:
			height = max(1, int(math.Round(float64(height)/sar)))
		}
	}
	return width, height
}

// ComparisonFrame retains a PNG and its observed identity without persisting or
// uploading it. RequestedTimestampSeconds remains relative to the input start.
type ComparisonFrame struct {
	ComparisonFrameObservation
	RequestedTimestampSeconds float64
	ImageBytes                []byte
	Processing                api.ScreenshotProcessing
	Width                     int
	Height                    int
}

// ComparisonSample contains one 16-by-16, row-major grayscale picture and its
// observed identity. Values are normalized to an 8-bit full-range gray raster.
type ComparisonSample struct {
	NativeSequence bool `json:",omitempty"`
	AnalysisAnchor bool `json:",omitempty"`
	Evidence       *api.ComparisonSceneEvidence
	ComparisonFrameObservation
	Pixels []byte
}

// ComparisonSampleRequest bounds a source-clock sampling window. NormalizeSDR
// requests matching-only colour conversion, independently of capture preferences.
// DVD matching always deinterlaces and corrects display geometry in either path.
type ComparisonSampleRequest struct {
	// Retain admits the observed memory demand of this physical read.
	// A nil function uses the standalone decoder limits.
	Retain              func(int64) bool
	NativeFrames        bool
	Renderer            *ComparisonAnalysisRenderer
	analysis            *comparisonRasterStream
	encodedAttemptBytes int64
	seek                float64
	MatchCrop           *ComparisonCrop
	Budget              *ComparisonDecodeBudget
	Source              ComparisonSource
	StartSeconds        float64
	DurationSeconds     float64
	StrideSeconds       float64
	NormalizeSDR        bool
	// MatchAspectRatio selects the shared central picture area for normalized
	// matching only. Captures retain the source's own framing.
	MatchAspectRatio float64
}

// ComparisonRetentionLimit bounds a standalone read by its actual native observations.
func ComparisonRetentionLimit(limit int64) func(int64) bool {
	return func(needed int64) bool { return needed <= limit }
}

// ComparisonAnalysisRenderer pins a successful colour conversion for one
// source/attempt. A later failure is explicit rather than mixing signatures.
type ComparisonAnalysisRenderer struct{ Mode string }

// ErrComparisonNormalizationUnavailable reports unavailable inspected colour
// facts or compatible conversion without authorizing another scan mode.
// Cancellation, corruption and invalid frame identities never use this fallback.
var ErrComparisonNormalizationUnavailable = errors.New("screenshots: comparison SDR normalization unavailable")

// ErrComparisonWorkerFailed identifies a decoder process failure. Source and
// decoded-identity validation failures do not use this partial-result boundary.
var ErrComparisonWorkerFailed = errors.New("screenshots: comparison sampling failed")

// ErrComparisonFastInconclusive preserves an explicit bounded/manual outcome.
var ErrComparisonFastInconclusive = errors.New("comparison fast attempt exhausted its bounded work budget")

// ComparisonDecodeBudget is mutable, attempt-local physical decode admission.
// Repeated spans, probes and retries consume allowance unless decoded results are
// reused. AttemptRemaining optionally shares encoded-byte allowance across sources.
type ComparisonDecodeBudget struct {
	RemainingEncodedBytes int64
	AttemptRemaining      *int64
	// MovieSecondsLimit caps cumulative decoded source seconds when positive.
	MovieSecondsLimit float64
	// ConsumedMovieSeconds includes every admitted physical decode window.
	ConsumedMovieSeconds float64
	// ProcessStarts counts processes started under this budget, including probes.
	ProcessStarts int
}

func (b *ComparisonDecodeBudget) admitMovieSeconds(seconds float64) error {
	if b == nil {
		return nil
	}
	if !comparisonFinite(seconds) || seconds <= 0 || b.MovieSecondsLimit > 0 && seconds > b.MovieSecondsLimit-b.ConsumedMovieSeconds+1e-9 {
		return ErrComparisonFastInconclusive
	}
	b.ConsumedMovieSeconds += seconds
	return nil
}

func (b *ComparisonDecodeBudget) admit(count int64) error {
	if count < 0 || count > b.RemainingEncodedBytes || b.AttemptRemaining != nil && count > *b.AttemptRemaining {
		return ErrComparisonFastInconclusive
	}
	b.RemainingEncodedBytes -= count
	if b.AttemptRemaining != nil {
		*b.AttemptRemaining -= count
	}
	return nil
}

// InspectComparisonSource probes one regular local file using the existing
// native MediaInfo reader. It rejects ambiguous video selection and does not
// substitute guessed frame rates or infer HDR from a filename.
func (s *Service) InspectComparisonSource(ctx context.Context, path string, expected ...os.FileInfo) (ComparisonSource, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	path, err := comparisonLocalPath(path)
	if err != nil {
		return ComparisonSource{}, err
	}
	// The MediaInfo API opens its own path. Check it around the metadata read
	// and use the pinned file for the decoded probe.
	var identity os.FileInfo
	if len(expected) != 0 {
		identity = expected[0]
	}
	if !comparisonInspectionPathCurrent(path, identity) {
		return ComparisonSource{}, comparisonFileInputChanged()
	}
	report, err := gomediainfo.AnalyzeFileContext(ctx, path)
	if !comparisonInspectionPathCurrent(path, identity) {
		return ComparisonSource{}, comparisonFileInputChanged()
	}
	if err != nil {
		return ComparisonSource{}, fmt.Errorf("screenshots: inspect comparison source: %w", err)
	}
	payload, err := gomediainfo.Render([]gomediainfo.Report{report}, gomediainfo.OutputJSON)
	if err != nil {
		return ComparisonSource{}, fmt.Errorf("screenshots: render comparison source facts: %w", err)
	}
	var doc mediaInfoDoc
	if err := json.Unmarshal([]byte(payload), &doc); err != nil {
		return ComparisonSource{}, fmt.Errorf("screenshots: decode comparison source facts: %w", err)
	}
	videos := 0
	var frameCount int64
	var videoDuration float64
	var codec, codecProfile string
	for _, track := range doc.Media.Track {
		if strings.EqualFold(trackString(track, "@type"), "video") {
			codec = trackString(track, "Format", "CodecID")
			codecProfile = trackString(track, "Format_Profile")
			videos++
			frameCount, _ = strconv.ParseInt(trackString(track, "FrameCount"), 10, 64)
			videoDuration = parseDurationValue(trackString(track, "Duration", "Duration/String3", "Duration/String2", "Duration/String"))
		}
	}
	if videos != 1 {
		return ComparisonSource{}, errors.New("screenshots: comparison requires exactly one inspected video stream")
	}
	width, height, widthScale, heightScale := mediaInfoVideoGeometry(doc)
	return s.InspectComparisonDisplay(ctx, ComparisonSource{
		Codec:                codec,
		CodecProfile:         codecProfile,
		Path:                 path,
		DurationSeconds:      mediaInfoDurationSeconds(doc),
		FrameRate:            mediaInfoFrameRate(doc),
		FrameCount:           max(0, frameCount),
		VideoDurationSeconds: videoDuration,
		Width:                width,
		Height:               height,
		WidthScale:           widthScale,
		HeightScale:          heightScale,
		HDRFacts:             mediafacts.HDRFromMediaInfoDocument(mediafacts.MediaInfoDocument(doc)),
		FileIdentity:         identity,
	})
}

func comparisonInspectionPathCurrent(path string, expected os.FileInfo) bool {
	if expected == nil {
		return true
	}
	current, err := pathing.RegularFileInfo(path)
	return err == nil && pathing.SameFileVersion(expected, current)
}

// InspectComparisonDisplay refreshes decoded geometry and clock facts while
// reusing the retained container metadata.
func (s *Service) InspectComparisonDisplay(ctx context.Context, source ComparisonSource) (ComparisonSource, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	executable, err := resolveFFmpeg()
	if err != nil {
		return ComparisonSource{}, err
	}
	if err := requireComparisonFileCapability(ctx, s.runner, executable); err != nil {
		return ComparisonSource{}, err
	}
	args := []string{"-hide_banner", "-loglevel", "info", "-noautorotate"}
	path := source.Path
	var input *comparisonFileInput
	if source.FileIdentity != nil {
		ctx, input, err = s.openComparisonFileInput(ctx, path, nil, source.FileIdentity)
		if err != nil {
			return ComparisonSource{}, err
		}
		defer input.close()
		path = input.url
		if input.server != nil {
			args = append(args, "-http_proxy", "")
		}
	}
	args = append(args, "-i", path, "-map", "0:V:0", "-an", "-sn", "-dn",
		// Limit the filter as well as the output when FFmpeg buffers frames.
		"-frames:v", "1", "-vf", "trim=end_frame=1,showinfo=checksum=0", "-f", "null", "-",
	)
	probe, err := (finiteComparisonRunner{
		base:        s.runner,
		outputLimit: 64 << 10,
		retain:      ComparisonRetentionLimit(16 << 10),
	}).Run(ctx, executable, args, "")
	if input != nil {
		if inputErr := input.failure(); inputErr != nil {
			return ComparisonSource{}, inputErr
		}
	}
	if ctx.Err() != nil {
		return ComparisonSource{}, fmt.Errorf("screenshots: comparison inspection canceled: %w", ctx.Err())
	}
	if err != nil {
		return ComparisonSource{}, fmt.Errorf("screenshots: inspect comparison timestamp: %w", err)
	}
	if corruption := ffmpegFrameCorruptionError(probe.Stderr); corruption != nil {
		return ComparisonSource{}, corruption
	}
	start := comparisonInputStart.FindSubmatch(probe.Stderr)
	if probe.ExitCode != 0 || len(start) != 2 {
		return ComparisonSource{}, errors.New("screenshots: comparison input start time unavailable")
	}
	startSeconds, err := strconv.ParseFloat(string(start[1]), 64)
	if err != nil || !comparisonFinite(startSeconds) {
		return ComparisonSource{}, errors.New("screenshots: comparison input start time invalid")
	}
	source.StartTimeSeconds = startSeconds
	source.ScreenshotTurn = comparisonQuarterTurn(probe.Stderr)
	source.ScreenshotMatrix = comparisonDisplayMatrix.Match(probe.Stderr)
	if observed, observationErr := comparisonObservations(probe.Stderr); observationErr == nil && len(observed) == 1 {
		frame := observed[0]
		source.NativePixelFormat, source.Interlace = frame.PixelFormat, frame.Interlace
		source.NativeColour, source.NativeChromaLocation = frame.Colour, frame.ChromaLocation
		source.SARNumerator, source.SARDenominator = frame.SARNumerator, frame.SARDenominator
		source.ScreenshotWidth, source.ScreenshotHeight = frame.displaySize()
		if source.ScreenshotTurn {
			source.ScreenshotWidth, source.ScreenshotHeight = source.ScreenshotHeight, source.ScreenshotWidth
		}
	}
	if rate := comparisonFrameRate.FindSubmatch(probe.Stderr); len(rate) == 3 {
		source.FrameRateNumerator, _ = strconv.ParseInt(string(rate[1]), 10, 64)
		source.FrameRateDenominator, _ = strconv.ParseInt(string(rate[2]), 10, 64)
		if source.FrameRate <= 0 && source.FrameRateDenominator > 0 {
			source.FrameRate = float64(source.FrameRateNumerator) / float64(source.FrameRateDenominator)
		}
	}
	if match := comparisonMappedCodec.FindSubmatch(probe.Stderr); len(match) == 2 {
		source.NativeCodec = string(match[1])
	}
	return source, nil
}

// ComparisonPreview uses the strict comparison capture path. It never applies
// black-frame timestamp offsets or disc-segment recovery.
func (s *Service) ComparisonPreview(ctx context.Context, request ComparisonCaptureRequest) (ComparisonFrame, error) {
	if request.Source.InputFingerprint != "" && request.Replay == nil {
		selected, err := InspectComparisonMoment(ctx, s, request.Source, request.TimestampSeconds)
		if err != nil {
			return ComparisonFrame{}, err
		}
		request.Replay, request.TimestampSeconds = selected.Replay, selected.RelativeTimestampSeconds
	}
	return s.ComparisonCapture(ctx, request)
}

// InspectComparisonMoment binds a newly requested manual time to an observed
// native picture before it can be applied, previewed or captured.
func InspectComparisonMoment(ctx context.Context, sampler interface {
	SampleComparisonSource(context.Context, ComparisonSampleRequest) ([]ComparisonSample, error)
}, source ComparisonSource, seconds float64) (ComparisonSample, error) {
	start := max(0, seconds-.5)
	duration := source.VideoDurationSeconds
	if duration <= 0 {
		duration = source.DurationSeconds
	}
	frames, err := sampler.SampleComparisonSource(ctx, ComparisonSampleRequest{
		Source:          source,
		StartSeconds:    start,
		DurationSeconds: min(1.5, duration-start),
		StrideSeconds:   .002,
		NativeFrames:    true,
	})
	if err != nil {
		return ComparisonSample{}, fmt.Errorf("inspect comparison moment: %w", err)
	}
	for _, frame := range frames {
		if frame.RelativeTimestampSeconds >= seconds-1e-9 {
			if frame.Replay == nil || frame.Replay.Version != 2 || frame.Replay.MissingBoundary() {
				break
			}
			return frame, nil
		}
	}
	return ComparisonSample{}, comparisonDiscFailure("inspect_frame_required")
}

// ComparisonCapture returns one usable PNG with observed source PTS. Renderer
// fallback keeps the same seek, and differing observed pictures fail closed.
// Black output, missing PTS, cancellation and corruption never move the seek.
func (s *Service) ComparisonCapture(ctx context.Context, request ComparisonCaptureRequest) (captured ComparisonFrame, captureErr error) {
	if request.Source.Input != nil {
		return s.captureDiscComparison(ctx, request)
	}
	ctx, processStarts := comparisonProcessCounter(ctx, nil)
	defer func() {
		logger := logging.FromContext(ctx, s.logger)
		state := "complete"
		if captureErr != nil {
			state = "failed"
			if errors.Is(captureErr, context.Canceled) {
				state = "cancelled"
			} else {
				logger.Warnf("comparisons: phase=capture state=failed decision=blocked")
			}
		}
		logger.Debugf("comparisons: phase=capture_process state=%s process_starts=%d", state, *processStarts)
	}()
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if !comparisonFinite(request.TimestampSeconds) || !comparisonFinite(request.Source.StartTimeSeconds) ||
		request.TimestampSeconds < 0 || !comparisonLabel.MatchString(request.OverlayLabel) {
		return ComparisonFrame{}, errors.New("screenshots: invalid comparison seek or overlay label")
	}
	if (request.OutputWidth != 0 || request.OutputHeight != 0) && (request.OutputWidth <= 0 || request.OutputHeight <= 0) {
		return ComparisonFrame{}, errors.New("screenshots: comparison output dimensions must both be positive")
	}
	path, err := comparisonLocalPath(request.Source.Path)
	if err != nil {
		return ComparisonFrame{}, err
	}
	executable, err := resolveFFmpeg()
	if err != nil {
		return ComparisonFrame{}, err
	}
	if request.Replay != nil &&
		(!request.Replay.validFile(request.Source) || math.Abs(request.TimestampSeconds-(float64(request.Replay.Identity.PTS)*float64(request.Replay.Identity.TimeBaseNumerator)/float64(request.Replay.Identity.TimeBaseDenominator)-request.Source.StartTimeSeconds)) > 1e-6) {
		return ComparisonFrame{}, comparisonDiscFailure("inspect_frame_required")
	}
	var input *comparisonFileInput
	if request.Replay != nil || request.Source.FileIdentity != nil {
		if err := requireComparisonFileCapability(ctx, s.runner, executable); err != nil {
			return ComparisonFrame{}, err
		}
		ctx, input, err = s.openComparisonFileInput(ctx, path, nil, request.Source.FileIdentity)
		if err != nil {
			return ComparisonFrame{}, err
		}
		defer input.close()
		path = input.url
	}
	runner := &comparisonRunner{
		service:        s,
		expected:       request.Replay,
		runner:         s.runner,
		source:         request.Source,
		seek:           request.TimestampSeconds,
		label:          request.OverlayLabel,
		showResolution: request.ShowResolution,
		sizing:         request.ScreenshotSizing,
		width:          request.OutputWidth,
		height:         request.OutputHeight,
		overlaySize:    s.cfg.ScreenshotHandling.OverlayTextSize,
		overlayHeight:  request.Source.Height,
	}
	if request.OutputHeight > 0 {
		runner.overlayHeight = request.OutputHeight
	}
	if _, native := s.runner.(commandRunner); native {
		runner.runner = finiteComparisonRunner{base: s.runner, outputLimit: 64 << 20}
	}
	toneMap := s.cfg.ScreenshotHandling.ToneMap
	if request.ToneMap != nil {
		toneMap = *request.ToneMap
	}
	frame, err := renderFrame(ctx, runner, executable, captureRequest{
		InputPath:        path,
		Timestamp:        request.TimestampSeconds,
		InspectFrame:     true,
		HDRFacts:         request.Source.HDRFacts,
		SourceWidth:      request.Source.Width,
		SourceHeight:     request.Source.Height,
		WidthScale:       request.Source.WidthScale,
		HeightScale:      request.Source.HeightScale,
		ToneMap:          toneMap && request.Source.HDRFacts.SupportsToneMapping(),
		UseLibplacebo:    s.cfg.ScreenshotHandling.UseLibplacebo,
		Algorithm:        s.cfg.ScreenshotHandling.TonemapAlgorithm,
		Desat:            s.cfg.ScreenshotHandling.Desat,
		Compression:      s.cfg.ScreenshotHandling.FFmpegCompression,
		OutputWidth:      request.OutputWidth,
		OutputHeight:     request.OutputHeight,
		ResizeOnly:       request.ResizeOnly,
		ScreenshotSizing: request.ScreenshotSizing,
	}, logging.FromContext(ctx, s.logger))
	if input != nil {
		if inputErr := input.failure(); inputErr != nil {
			return ComparisonFrame{}, inputErr
		}
	}
	if err != nil {
		return ComparisonFrame{}, err
	}
	if runner.observed == nil {
		return ComparisonFrame{}, errors.New("screenshots: comparison decoded frame identity unavailable")
	}
	runner.observed.RelativeTimestampSeconds = runner.observed.TimestampSeconds - request.Source.StartTimeSeconds
	expectedSource := request.Source
	if runner.displayTurn {
		expectedSource.Width, expectedSource.Height = expectedSource.Height, expectedSource.Width
	}
	if err := bindFileObservation(expectedSource, runner.observed, false); err != nil {
		return ComparisonFrame{}, err
	}
	runner.observed.Replay = request.Replay
	geometry, _, err := image.DecodeConfig(bytes.NewReader(frame.Payload))
	if err != nil {
		return ComparisonFrame{}, fmt.Errorf("screenshots: decode comparison PNG: %w", err)
	}
	captured = ComparisonFrame{
		ComparisonFrameObservation: *runner.observed,
		RequestedTimestampSeconds:  request.TimestampSeconds,
		ImageBytes:                 frame.Payload,
		Processing:                 frame.Processing,
		Width:                      geometry.Width,
		Height:                     geometry.Height,
	}
	if (request.OutputWidth > 0 || request.ScreenshotSizing == api.ComparisonSizingSource) && !request.ValidOutputSize(captured) {
		return ComparisonFrame{}, errors.New("screenshots: comparison renderer did not produce the selected frame size")
	}
	return captured, nil
}

// SampleComparisonSource decodes a bounded window without image recovery or
// rendering overlays. Start and duration are input-relative seconds; spacing is
// measured on actual frame timestamps. At most 50,000 samples may be requested.
// Returned PTS remains in the original source clock, including a nonzero start.
// A successfully decoded window containing no frames returns an empty result.
func (s *Service) SampleComparisonSource(ctx context.Context, request ComparisonSampleRequest) ([]ComparisonSample, error) {
	if request.NormalizeSDR && request.Renderer == nil {
		request.Renderer = &ComparisonAnalysisRenderer{}
	}
	if request.Source.Input != nil {
		return s.sampleDiscComparison(ctx, request)
	}
	source := request.Source
	start, duration, strideSeconds := request.StartSeconds, request.DurationSeconds, request.StrideSeconds
	logger := logging.FromContext(ctx, s.logger)
	logger.Debugf("comparisons: phase=sampling decision=decode window_seconds=%.3f stride_seconds=%.4f", duration, strideSeconds)
	windowStart := source.StartTimeSeconds + start
	windowEnd := windowStart + duration
	if !comparisonFinite(start) || !comparisonFinite(duration) || !comparisonFinite(strideSeconds) || !comparisonFinite(source.StartTimeSeconds) ||
		!comparisonFinite(windowEnd) || !comparisonFinite(request.MatchAspectRatio) || request.MatchAspectRatio < 0 ||
		start < 0 || duration <= 0 || duration > 64 || strideSeconds <= 0 || !request.NativeFrames && math.Ceil(duration/strideSeconds)+1 > 50000 {
		logger.Warnf("comparisons: phase=sampling state=blocked reason=invalid_window")
		return nil, errors.New("screenshots: comparison sampling window exceeds its bounds")
	}
	path, err := comparisonLocalPath(source.Path)
	if err != nil {
		return nil, err
	}
	executable, err := resolveFFmpeg()
	if err != nil {
		return nil, err
	}
	if err := requireComparisonFileCapability(ctx, s.runner, executable); err != nil {
		return nil, err
	}
	ctx, input, err := s.openComparisonFileInput(ctx, path, request.Budget, source.FileIdentity)
	if err != nil {
		return nil, err
	}
	defer input.close()
	path = input.url
	filter := fmt.Sprintf("trim=start=%.9f:end=%.9f,showinfo=checksum=0", windowStart, windowEnd)
	seek := start
	if input.server != nil {
		seek = max(0, start-.125)
		filter = "showinfo@comparison_bounds=checksum=0," + filter
	}
	started := time.Now()
	succeeded := false
	defer func() {
		logger.Debugf("comparisons: phase=sampling completed=%t elapsed_seconds=%.3f window_seconds=%.3f stride_seconds=%.4f",
			succeeded, time.Since(started).Seconds(), duration, strideSeconds)
	}()
	ctx = context.WithValue(ctx, comparisonSampleProgressKey{}, &comparisonSampleProgress{
		ctx:      ctx,
		start:    windowStart,
		duration: duration,
	})
	args := []string{
		"-hide_banner", "-loglevel", "info", "-threads", "4", "-filter_threads", "4", "-copyts", "-ss", strconv.FormatFloat(seek, 'f', 9, 64),
	}
	if !request.NormalizeSDR {
		// Identity-only seeks use the coded raster. Visual matching uses the
		// same display transform as its crop detector.
		args = append(args, "-noautorotate")
	}
	args = append(args, "-i", path, "-map", "0:V:0", "-an", "-sn", "-dn")
	if input.server != nil {
		args = comparisonFileCommand(args)
	}
	service := *s
	service.runner = finiteComparisonRunner{
		base:        s.runner,
		outputLimit: 50000 * 256,
		retain:      request.Retain,
	}
	physical := request
	physical.DurationSeconds += start - seek
	result, err := service.runComparisonSamples(ctx, executable, args, filter, physical)
	if inputErr := input.failure(); inputErr != nil {
		return nil, inputErr
	}
	if err != nil {
		return nil, err
	}
	request.seek = seek
	if request.analysis != nil {
		samples, err := analysisSamples(result, request, nil)
		succeeded = err == nil
		return samples, err
	}
	observations, err := comparisonObservations(result.Stderr)
	if err != nil {
		return nil, err
	}
	if len(observations) > 50000 || len(result.Stdout) != len(observations)*256 {
		return nil, errors.New("screenshots: comparison samples lack matching decoded frame identities")
	}
	if err := bindComparisonSequence(request, observations, nil, result.bounds); err != nil {
		return nil, err
	}
	if request.NormalizeSDR {
		output, outputErr := comparisonObservationsFor(result.Stderr, comparisonOutputShowInfoLine)
		if outputErr != nil || len(output) != len(observations) {
			return nil, errors.New("screenshots: normalized samples lack matching output frame identities")
		}
		for i, frame := range output {
			if math.Abs(frame.TimestampSeconds-observations[i].TimestampSeconds) > comparisonNormalizedClockTolerance {
				return nil, errors.New("screenshots: normalization changed comparison frame identity")
			}
		}
	}
	samples := make([]ComparisonSample, 0, len(observations))
	for i, observation := range observations {
		if !request.NativeFrames && len(samples) > 0 &&
			observation.RelativeTimestampSeconds-samples[len(samples)-1].RelativeTimestampSeconds < request.StrideSeconds-1e-9 {
			continue
		}
		samples = append(samples, ComparisonSample{
			NativeSequence:             request.NativeFrames,
			ComparisonFrameObservation: observation,
			Pixels:                     bytes.Clone(result.Stdout[i*256 : (i+1)*256]),
		})
	}
	succeeded = true
	api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
		Phase:     "comparisons_sampling",
		Status:    api.StageStatusCompleted,
		Completed: 1000,
		Total:     1000,
		ItemOnly:  true,
	})
	return samples, nil
}

func (s *Service) runComparisonSamples(ctx context.Context, executable string, args []string, filter string,
	request ComparisonSampleRequest,
) (CommandResult, error) {
	ctx, processStarts := comparisonProcessCounter(ctx, request.Budget)
	beforeStarts := *processStarts
	if request.analysis != nil {
		request.analysis.totalMotionBytes, request.analysis.totalSparseBytes = 0, 0
	}
	defer func() {
		logger := logging.FromContext(ctx, s.logger)
		logger.Debugf("comparisons: phase=decode_process process_starts=%d", *processStarts-beforeStarts)
		if request.analysis != nil {
			logger.Debugf(
				"comparisons: phase=analysis_transport motion_bytes=%d sparse_bytes=%d",
				request.analysis.totalMotionBytes,
				request.analysis.totalSparseBytes,
			)
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return CommandResult{}, fmt.Errorf("screenshots: comparison sampling canceled: %w", err)
	}
	mode := "none"
	if request.NormalizeSDR {
		policy, err := ComparisonAnalysisPolicy(request.Source)
		if err != nil {
			return CommandResult{}, err
		}
		mode = policy.Mode
	}
	if request.Renderer != nil && request.Renderer.Mode != "" {
		if request.Renderer.Mode != mode {
			return CommandResult{}, fmt.Errorf("%w: analysis policy changed within attempt", ErrComparisonNormalizationUnavailable)
		}
	}
	requestCtx := ctx
	if err := ctx.Err(); err != nil {
		return CommandResult{}, fmt.Errorf("screenshots: comparison sampling canceled: %w", err)
	}
	normalization, err := s.comparisonNormalizationFilter(request, mode)
	if err != nil {
		return CommandResult{}, err
	}
	decoders, err := comparisonDecoders(ctx, s.runner, executable, request.Source,
		mode == "libplacebo" || request.NormalizeSDR && request.Source.ScreenshotMatrix)
	if err != nil {
		return CommandResult{}, err
	}
	normalized := filter + normalization
	if comparisonNormalizesPicture(request) && request.analysis == nil {
		normalized += ",showinfo@comparison_output=checksum=0"
	}
	command := slices.Clone(args)
	if mode == "libplacebo" {
		command = append([]string{"-init_hw_device", "vulkan=upbrr", "-filter_hw_device", "upbrr"}, command...)
	}
	if stream := request.analysis; stream != nil {
		stream.reset(ctx)
		requestCtx = context.WithValue(ctx, comparisonRasterStreamKey{}, stream)
		input := "0:V:0"
		for i, arg := range command {
			if arg == "-map" && i+1 < len(command) {
				input = command[i+1]
				command[i+1] = "[analysis]"
			}
		}
		matchingRequest := request
		matchingRequest.analysis = nil
		matchingRequest.MatchCrop = stream.matchCrop
		matchingRequest.MatchAspectRatio = stream.matchAspect
		matching, err := s.comparisonNormalizationFilter(matchingRequest, mode)
		if err != nil {
			return CommandResult{}, err
		}
		graph := "[" + input + "]" + filter + ",split=2[scenein][matchin];[scenein]null" + normalization + ",split=2[motionin][statsin];" +
			fmt.Sprintf(
				"[motionin]scale=256:%d:flags=area:out_range=full,format=gray,scdet=threshold=10,metadata@comparison_cut=mode=print[motion];",
				stream.motionHeight,
			) +
			fmt.Sprintf("[matchin]null%s,pad=16:%d:0:0:color=black[matching];", matching, stream.motionHeight) +
			"[motion][matching]hstack=inputs=2,setsar=1,showinfo@comparison_motion[analysis];" +
			fmt.Sprintf(
				"[statsin]select='isnan(prev_selected_pts)+gte(pts-prev_selected_pts,ceil(0.25/TB))',scale=512:%d:flags=area:out_range=full,format=yuv420p,split=2[original][blurin];",
				stream.height,
			) +
			"[blurin]gblur=sigma=1.2:steps=2[blur];[original][blur]hstack=inputs=2,setsar=1,showinfo@comparison_stats[stats]"
		command = append(
			command,
			"-filter_complex_threads",
			"1",
			"-filter_complex",
			graph,
			"-fps_mode",
			"passthrough",
			"-frames:v",
			"50000",
			"-pix_fmt",
			"gray",
			"-c:v",
			"rawvideo",
			"-f",
			"rawvideo",
			"-",
			"-map",
			"[stats]",
			"-an",
			"-sn",
			"-dn",
			"-fps_mode",
			"passthrough",
			"-frames:v",
			"50000",
			"-pix_fmt",
			"yuv420p",
			"-c:v",
			"rawvideo",
			"-f",
			"rawvideo",
			"-method",
			"POST",
			"-chunked_post",
			"1",
			"-content_type",
			"application/octet-stream",
			// Override the inherited proxy for this loopback output only.
			"-http_proxy",
			"",
			comparisonSparseOutput,
		)
	} else {
		command = append(command, "-vf", normalized, "-fps_mode", "passthrough", "-frames:v", "50000", "-pix_fmt", "gray", "-f", "rawvideo", "-")
	}
	result, err := s.runComparisonDecode(requestCtx, s.runner, executable, command, "", decoders, &request, request.Source.Input == nil && request.NormalizeSDR)
	if errors.Is(err, internalerrors.ErrFrameCorruption) || errors.Is(err, errComparisonDecoderLimit) || errors.Is(err, ErrComparisonFastInconclusive) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return CommandResult{}, fmt.Errorf("comparison sampling: %w", err)
	}
	if corruption := ffmpegFrameCorruptionError(result.Stderr); corruption != nil {
		return CommandResult{}, corruption
	}
	if err != nil && !errors.Is(err, ErrComparisonWorkerFailed) {
		return CommandResult{}, fmt.Errorf("comparison decoder evidence: %w", err)
	}
	if ctx.Err() != nil {
		return CommandResult{}, fmt.Errorf("screenshots: comparison sampling canceled: %w", ctx.Err())
	}
	if err == nil && result.ExitCode == 0 {
		if request.Renderer != nil {
			request.Renderer.Mode = mode
		}
		profile, _, _ := strings.Cut(request.Source.HDRFacts.DolbyVisionProfile, ".")
		if mode == "libplacebo" && profile == "5" && !showInfoDOVIMetadata.Match(result.Stderr) {
			return CommandResult{}, fmt.Errorf("%w: decoded Dolby Vision metadata missing", ErrComparisonNormalizationUnavailable)
		}
		logging.FromContext(
			ctx,
			s.logger,
		).Debugf(
			"comparisons: phase=sampling decoder=%s normalization=%s normalized_sdr=%t",
			result.comparisonBackend,
			mode,
			request.NormalizeSDR,
		)
		return result, nil
	}
	if mode == "none" {
		return CommandResult{}, fmt.Errorf("comparison decoder process: %w", err)
	}
	logging.FromContext(ctx, s.logger).Debugf("comparisons: phase=sampling renderer=%s state=failed reason=%s", mode, renderFailureStage(result, err))
	return CommandResult{}, fmt.Errorf("%w: compatible tone-mapping renderers failed", ErrComparisonNormalizationUnavailable)
}

func (s *Service) comparisonNormalizationFilter(request ComparisonSampleRequest, mode string) (string, error) {
	normalized := ""
	if comparisonDeinterlaceForMatching(request) {
		normalized += ",bwdif=mode=send_frame:parity=auto:deint=interlaced"
	}
	if mode == "software" {
		filters, filterErr := s.softwareComparisonAnalysisFilters(request)
		if filterErr != nil {
			return "", filterErr
		}
		normalized += "," + strings.Join(filters, ",")
	} else if mode != "none" {
		normalized += "," + strings.Join(toneMappingFilters(captureRequest{
			ToneMap:   true,
			Algorithm: comparisonAnalysisAlgorithm,
			Desat:     comparisonAnalysisDesat,
		}, mode == "libplacebo"), ",")
	}
	switch {
	case comparisonNormalizesPicture(request) && mode != "software":
		geometry := request.Source.matchingGeometry()
		if crop := request.MatchCrop; crop != nil {
			if !crop.ValidFor(geometry.Width, geometry.Height) {
				return "", comparisonDiscFailure("matching_crop")
			}
			normalized += fmt.Sprintf(",crop=%d:%d:%d:%d:exact=1", crop.Width, crop.Height, crop.X, crop.Y)
			geometry.Width, geometry.Height = crop.Width, crop.Height
		}
		if scale := buildPARScaleFilter(captureRequest{
			SourceWidth:  geometry.Width,
			SourceHeight: geometry.Height,
			WidthScale:   geometry.WidthScale,
			HeightScale:  geometry.HeightScale,
		}); scale != "" {
			normalized += "," + scale
		}
		width, height := geometry.DisplaySize()
		if request.MatchAspectRatio > 0 && width > 0 && height > 0 && math.Abs(float64(width)/float64(height)-request.MatchAspectRatio) > 1e-6 {
			normalized += fmt.Sprintf(
				",crop=w='min(iw,round(ih*%.9f/2)*2)':h='min(ih,round(iw/%.9f/2)*2)':exact=1",
				request.MatchAspectRatio,
				request.MatchAspectRatio,
			)
		}
		// Area averaging keeps small matching rasters stable across resolutions.
		if request.analysis == nil {
			normalized += ",scale=16:16:flags=area:out_range=full,format=gray"
		}
	case mode == "software" && request.analysis == nil:
		normalized += ",scale=16:16:flags=area:out_range=full,format=gray"
	case request.analysis == nil:
		normalized += ",scale=16:16:out_range=full,format=gray"
	}
	return normalized, nil
}

func comparisonDeinterlaceForMatching(request ComparisonSampleRequest) bool {
	return request.Source.Input != nil && request.Source.Input.Kind == "dvd"
}

func comparisonNormalizesPicture(request ComparisonSampleRequest) bool {
	return request.NormalizeSDR || comparisonDeinterlaceForMatching(request)
}

type comparisonSampleProgressKey struct{}

// comparisonSampleProgress observes existing showinfo records without changing
// decoding or buffering raw diagnostics in progress events. Units are permille of
// the requested source-clock window; completion requires validated output.
type comparisonSampleProgress struct {
	ctx             context.Context
	start, duration float64
	header          []byte
	line            []byte
	discard         bool
	completed       int
}

func (p *comparisonSampleProgress) Write(data []byte) (int, error) {
	for _, value := range data {
		switch {
		case value == '\n' || value == '\r':
			if !p.discard {
				p.report()
			}
			p.line, p.discard = p.line[:0], false
		case len(p.line) < 8192:
			p.line = append(p.line, value)
		default:
			p.discard = true
		}
	}
	return len(data), nil
}

func (p *comparisonSampleProgress) report() {
	line := comparisonShowInfoLine.FindSubmatch(p.line)
	if len(line) != 2 {
		return
	}
	if comparisonTimeBase.Match(line[1]) {
		p.header = append(bytes.Clone(p.line), '\n')
		return
	}
	if len(p.header) == 0 || !comparisonPTS.Match(line[1]) {
		return
	}
	observed, err := comparisonObservations(append(bytes.Clone(p.header), p.line...))
	if err != nil || len(observed) != 1 {
		return
	}
	completed := int(math.Floor(1000 * (observed[0].TimestampSeconds - p.start) / p.duration))
	completed = max(0, min(999, completed))
	if completed < p.completed+10 {
		return
	}
	p.completed = completed
	api.EmitWorkflowProgress(p.ctx, api.WorkflowProgressUpdate{
		Phase:     "comparisons_sampling",
		Status:    api.StageStatusRunning,
		Completed: completed,
		Total:     1000,
		ItemOnly:  true,
	})
}

var (
	comparisonAnyShowInfoLine    = regexp.MustCompile(`(?m)^\[(?:Parsed_showinfo_\d+|showinfo@comparison_\w+) @ [^\]\r\n]+\]\s+([^\r\n]+)`)
	comparisonLabel              = regexp.MustCompile(`^[A-Za-z0-9 ._-]{0,80}$`)
	comparisonShowInfoLine       = regexp.MustCompile(`(?m)^\[Parsed_showinfo_\d+ @ [^\]\r\n]+\]\s+([^\r\n]+)`)
	comparisonOutputShowInfoLine = regexp.MustCompile(`(?m)^\[showinfo@comparison_output @ [^\]\r\n]+\]\s+([^\r\n]+)`)
	comparisonTimeBase           = regexp.MustCompile(`^config in time_base:\s*(\d+)/(\d+),`)
	comparisonPTS                = regexp.MustCompile(`^n:\s*\d+\s+pts:\s*(-?\d+)\s`)
	comparisonMappedCodec        = regexp.MustCompile(`(?m)^[ \t]*Stream #\d+:\d+[^\r\n]*? -> #\d+:\d+ \(([a-zA-Z0-9_]+) \(`)
	comparisonInputStart         = regexp.MustCompile(`(?m)^  Duration: [^\r\n]*, start: ([+-]?[0-9.]+), bitrate:`)
	comparisonFrameRate          = regexp.MustCompile(`(?m)^\[Parsed_showinfo_\d+ @ [^\]\r\n]+\]\s+config in time_base: [^\r\n]*frame_rate:\s*(\d+)/(\d+)`)
	comparisonPictureFacts       = regexp.MustCompile(`fmt:(\S+).*?sar:(\d+)[/:](\d+).*?s:(\d+)x(\d+).*?i:(\S+)`)
)

var comparisonChecksum = regexp.MustCompile(`\bchecksum:([0-9A-Fa-f]+)`)

var comparisonColourFacts = regexp.MustCompile(`^color_range:(\w+)\s+color_space:(\w+)\s+color_primaries:(\w+)\s+color_trc:(\w+)`)
var comparisonDuration = regexp.MustCompile(`\bduration:\s*(-?\d+)\b`)
var comparisonChromaLocation = regexp.MustCompile(`\bcl:(\w+)`)

func comparisonColour(body string) string {
	// FFmpeg can append an asynchronous "New ... stream" notice before showinfo writes its newline.
	body, _, _ = strings.Cut(body, "New ")
	if fields := comparisonColourFacts.FindStringSubmatch(body); len(fields) == 5 {
		return strings.Join(fields[1:], "/")
	}
	return ""
}

func comparisonObservations(stderr []byte) ([]ComparisonFrameObservation, error) {
	return comparisonObservationsFor(stderr, comparisonShowInfoLine)
}

func comparisonObservationsFor(stderr []byte, pattern *regexp.Regexp) ([]ComparisonFrameObservation, error) {
	// FFmpeg progress reports can end with CR immediately before a showinfo record.
	stderr = bytes.ReplaceAll(stderr, []byte{'\r'}, []byte{'\n'})
	var numerator, denominator int64
	var rateNumerator, rateDenominator int64
	var observations []ComparisonFrameObservation
	for _, line := range pattern.FindAllSubmatch(stderr, -1) {
		body := string(line[1])
		if match := comparisonTimeBase.FindStringSubmatch(body); len(match) == 3 {
			numerator, _ = strconv.ParseInt(match[1], 10, 64)
			denominator, _ = strconv.ParseInt(match[2], 10, 64)
			if _, rate, ok := strings.Cut(body, "frame_rate:"); ok {
				n, d, ok := strings.Cut(strings.TrimSpace(rate), "/")
				if ok {
					rateNumerator, _ = strconv.ParseInt(n, 10, 64)
					rateDenominator, _ = strconv.ParseInt(d, 10, 64)
				}
			}
			continue
		}
		if !strings.HasPrefix(body, "n:") {
			if value, ok := strings.CutPrefix(body, "comparison_packet_pos:"); ok && len(observations) > 0 {
				pos, err := strconv.ParseInt(value, 10, 64)
				if err != nil {
					return nil, comparisonDiscFailure("packet_address")
				}
				observations[len(observations)-1].packetPosition = new(pos)
			}
			if colour := comparisonColour(body); colour != "" && len(observations) > 0 {
				observations[len(observations)-1].Colour = colour
			}
			continue
		}
		match := comparisonPTS.FindStringSubmatch(body)
		if len(match) != 2 || numerator <= 0 || denominator <= 0 {
			return nil, errors.New("screenshots: comparison frame has unknown PTS or timebase")
		}
		pts, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			return nil, errors.New("screenshots: comparison frame PTS is invalid")
		}
		pictureType := parseShowInfo(body).FrameType
		if pictureType != "I" && pictureType != "P" && pictureType != "B" {
			pictureType = ""
		}
		observation := ComparisonFrameObservation{
			FrameRateNumerator:   rateNumerator,
			FrameRateDenominator: rateDenominator,
			PTS:                  pts,
			TimeBaseNumerator:    numerator,
			TimeBaseDenominator:  denominator,
			TimestampSeconds:     float64(pts) * float64(numerator) / float64(denominator),
			PictureType:          pictureType,
		}
		if duration := comparisonDuration.FindStringSubmatch(body); len(duration) == 2 {
			observation.DurationTicks, _ = strconv.ParseInt(duration[1], 10, 64)
		}
		if chroma := comparisonChromaLocation.FindStringSubmatch(body); len(chroma) == 2 {
			observation.ChromaLocation = chroma[1]
		}
		if facts := comparisonPictureFacts.FindStringSubmatch(body); len(facts) == 7 {
			observation.PixelFormat = facts[1]
			observation.SARNumerator, _ = strconv.ParseInt(facts[2], 10, 64)
			observation.SARDenominator, _ = strconv.ParseInt(facts[3], 10, 64)
			observation.Width, _ = strconv.Atoi(facts[4])
			observation.Height, _ = strconv.Atoi(facts[5])
			observation.Interlace = facts[6]
		}
		observations = append(observations, observation)
	}
	return observations, nil
}

type comparisonRunner struct {
	service        *Service
	expected       *ComparisonFrameReplay
	runner         Runner
	source         ComparisonSource
	seek           float64
	label          string
	showResolution bool
	sizing         string
	overlaySize    int
	overlayHeight  int
	width, height  int
	observed       *ComparisonFrameObservation
	changed        bool
	displayTurn    bool
}

func (r *comparisonRunner) Run(ctx context.Context, executable string, args []string, dir string) (CommandResult, error) {
	if r.changed {
		return CommandResult{}, errors.New("screenshots: comparison renderer changed decoded frame identity")
	}
	args = append([]string{"-copyts"}, args...)
	for i, arg := range args {
		switch arg {
		case "-map":
			args[i+1] = "0:V:0"
		case "-ss":
			start := r.seek
			if r.expected != nil {
				start = r.expected.File.Seek
			}
			args[i+1] = strconv.FormatFloat(start, 'f', 9, 64)
		case "-vf":
			filter := strings.Replace(args[i+1], "showinfo", "showinfo=checksum=0", 1)
			if r.expected != nil {
				window := r.expected.File
				start := window.InputStart + window.Start
				selection := fmt.Sprintf("eq(n,%d)*eq(pts,%d)", window.Slot, r.expected.Identity.PTS)
				if r.sizing != "" && window.PacketPosition != nil {
					// Autorotation can reset the filter index without changing the
					// native clock. Replay still checks the original packet address.
					selection = fmt.Sprintf("eq(pts,%d)", r.expected.Identity.PTS)
				}
				filter = fmt.Sprintf(
					"trim=start=%.9f:end=%.9f,select='%s',",
					start,
					start+window.Duration,
					selection,
				) + filter
			}
			args[i+1] = filter
		}
	}
	if _, ok := ctx.Value(comparisonInputURLKey{}).(string); ok {
		args = comparisonFileCommand(args)
	}
	var labelFrame *ComparisonFrameObservation
	if r.expected != nil && r.expected.PictureType != "" {
		labelFrame = &ComparisonFrameObservation{
			PictureType: r.expected.PictureType,
			TimestampSeconds: float64(
				r.expected.Identity.PTS,
			) * float64(
				r.expected.Identity.TimeBaseNumerator,
			) / float64(
				r.expected.Identity.TimeBaseDenominator,
			),
		}
	} else if disc, ok := r.runner.(exactDiscCaptureRunner); ok && disc.replay.PictureType != "" {
		labelFrame = &ComparisonFrameObservation{PictureType: disc.replay.PictureType, TimestampSeconds: float64(disc.replay.DecoderPTS) / 90000}
	}
	filterIndex := slices.Index(args, "-vf") + 1
	// Decoder-sized outputs need their actual dimensions before captioning.
	boundLabel := r.label != "" && labelFrame != nil && (r.sizing == "" || r.sizing == api.ComparisonSizingFit && r.width > 0 && !r.showResolution)
	if boundLabel {
		width, height := r.width, r.height
		if width == 0 {
			width, height = r.source.DisplaySize()
		}
		caption := r.captionFilters(*labelFrame, width, height, args[filterIndex])
		// Test the exact RGB threshold on the underlying picture before drawtext.
		mask := fmt.Sprintf("if(gt(val,%d),255,0)", blackPixelThreshold)
		args[filterIndex] += fmt.Sprintf(
			",split=2[caption][probe];[probe]lutrgb=r='%s':g='%s':b='%s',scale=out_color_matrix=bt709:out_range=full,format=yuv444p,signalstats,metadata@comparison_black=print:key=lavfi.signalstats.YMAX,nullsink;[caption]%s",
			mask,
			mask,
			mask,
			caption,
		)
	}
	result, err := r.run(ctx, executable, args, dir)
	if comparisonTerminalFailure(err) {
		return result, err
	}
	if corruption := ffmpegFrameCorruptionError(result.Stderr); corruption != nil {
		return result, corruption
	}
	if ctx.Err() != nil {
		return result, fmt.Errorf("screenshots: comparison rendering canceled: %w", ctx.Err())
	}
	if err != nil || result.ExitCode != 0 || r.label == "" {
		return result, err
	}
	if labelFrame != nil && (r.observed == nil || r.observed.PictureType != labelFrame.PictureType) {
		return result, comparisonDiscFailure("changed_picture_type")
	}
	if boundLabel {
		match := comparisonBlackReport.FindSubmatch(result.Stderr)
		if len(match) != 2 {
			return result, errors.New("comparison caption lacks underlying picture verification")
		}
		if string(match[1]) == "0" {
			return result, errFFmpegBlackImage
		}
		return result, nil
	}
	if err := validateImagePayload(result.Stdout); err != nil {
		return result, err
	}
	geometry, _, err := image.DecodeConfig(bytes.NewReader(result.Stdout))
	if err != nil || r.observed == nil {
		return result, errors.New("comparison caption lacks picture facts")
	}
	caption := r.captionFilters(*r.observed, geometry.Width, geometry.Height, args[filterIndex])
	base := r.runner
	if disc, ok := base.(exactDiscCaptureRunner); ok {
		base = disc.base
	}
	if finite, ok := base.(finiteComparisonRunner); ok {
		base = finite.base
	}
	imageRunner := finiteComparisonRunner{
		base:        base,
		outputLimit: 64 << 20,
		input:       result.Stdout,
	}
	compression := args[slices.Index(args, "-compression_level")+1]
	labeled, err := imageRunner.Run(
		ctx,
		executable,
		[]string{
			"-hide_banner",
			"-loglevel",
			"error",
			"-f",
			"image2pipe",
			"-noautorotate",
			"-i",
			"pipe:0",
			"-frames:v",
			"1",
			"-vf",
			caption,
			"-c:v",
			"png",
			"-compression_level",
			compression,
			"-pred",
			"mixed",
			"-f",
			"image2pipe",
			"-",
		},
		dir,
	)
	if err != nil || labeled.ExitCode != 0 {
		return labeled, err
	}
	result.Stdout = labeled.Stdout
	return result, nil
}

var comparisonBlackReport = regexp.MustCompile(`(?m)^\[metadata@comparison_black @ [^\]]+\] lavfi\.signalstats\.YMAX=(\d+)\s*$`)

func (r *comparisonRunner) captionFilters(observed ComparisonFrameObservation, width, height int, filter string) string {
	pictureType := observed.PictureType
	if pictureType == "" {
		pictureType = "Unknown"
	}
	number, total := comparisonDisplayCounters(r.source, observed)
	lines := []string{
		"Frame " + number + " of " + total,
		"Picture type\\: " + pictureType,
	}
	if r.showResolution {
		sourceWidth, sourceHeight := r.source.DisplaySize()
		if r.sizing != "" {
			sourceWidth, sourceHeight = observed.displaySize()
		}
		resolution := fmt.Sprintf("Resolution\\: %d x %d", sourceWidth, sourceHeight)
		if sourceWidth != width || sourceHeight != height {
			resolution += fmt.Sprintf(" -> %d x %d", width, height)
		}
		lines = append(lines, resolution)
	}
	if strings.Contains(filter, "tonemap=") || strings.Contains(filter, "libplacebo=") {
		lines = append(lines, "Source\\: "+hdrOverlaySourceLabel(r.source.HDRFacts)+" - Tonemapped")
	}
	lines = append(lines, r.label)
	overlayHeight := r.overlayHeight
	if r.sizing != "" {
		overlayHeight = height
	}
	return strings.Join(overlayTextFilters(lines, r.overlaySize, overlayHeight), ",")
}

// comparisonDisplayCounters uses the ordinary screenshot's zero-based nominal
// frame convention at the actual captured position. These display estimates do
// not replace PTS/timebase identity or enumerate variable-rate video frames.
func comparisonDisplayCounters(source ComparisonSource, observed ComparisonFrameObservation) (string, string) {
	number, total := "unknown", "unknown"
	if source.FrameCount > 0 {
		total = strconv.FormatInt(source.FrameCount, 10)
	}
	if source.FrameRate <= 0 || !comparisonFinite(source.FrameRate) {
		return number, total
	}
	// Absorb floating-point residue at an integer frame boundary, without
	// rounding an arbitrary fractional position to the following nominal frame.
	counter := func(value float64) string {
		if !comparisonFinite(value) || value < 0 || value >= math.Exp2(63) {
			return "unknown"
		}
		return strconv.FormatInt(int64(math.Floor(value+1e-7)), 10)
	}
	position := observed.TimestampSeconds - source.StartTimeSeconds
	number = counter(position * source.FrameRate)
	if total == "unknown" {
		duration := source.VideoDurationSeconds
		if duration <= 0 && source.StartTimeSeconds == 0 {
			duration = source.DurationSeconds
		}
		if duration > 0 {
			total = counter(duration * source.FrameRate)
			if total == "0" {
				total = "unknown"
			}
		}
	}
	if n, err := strconv.ParseInt(number, 10, 64); err == nil {
		if count, err := strconv.ParseInt(total, 10, 64); err == nil && n >= count {
			number = "unknown"
		}
	}
	return number, total
}

func (r *comparisonRunner) run(ctx context.Context, executable string, args []string, dir string) (CommandResult, error) {
	var result CommandResult
	var runErr error
	if _, authored := r.runner.(exactDiscCaptureRunner); !authored && r.service != nil {
		result, runErr = r.service.runComparisonCaptureDecoder(ctx, r.runner, executable, args, dir, r.source, "capture-file", r.sizing != "")
	} else {
		result, runErr = r.runner.Run(ctx, executable, args, dir)
	}
	if runErr != nil {
		runErr = fmt.Errorf("screenshots: comparison decoder failed: %w", runErr)
	}
	observed, err := comparisonObservations(result.Stderr)
	if err != nil || len(observed) != 1 {
		if runErr == nil && result.ExitCode == 0 {
			return result, errors.New("screenshots: comparison decoded frame identity unavailable")
		}
		return result, runErr
	}
	if r.observed != nil && (r.observed.PTS != observed[0].PTS || r.observed.TimeBaseNumerator != observed[0].TimeBaseNumerator ||
		r.observed.TimeBaseDenominator != observed[0].TimeBaseDenominator) {
		r.changed = true
		return result, errors.New("screenshots: comparison renderer changed decoded frame identity")
	}
	r.observed = &observed[0]
	r.displayTurn = r.sizing != "" && (r.source.ScreenshotTurn || comparisonQuarterTurn(result.Stderr))
	if r.expected != nil &&
		(r.observed.PTS != r.expected.Identity.PTS || r.observed.TimeBaseNumerator != r.expected.Identity.TimeBaseNumerator || r.observed.TimeBaseDenominator != r.expected.Identity.TimeBaseDenominator) {
		return result, comparisonDiscFailure("replay_picture_changed")
	}
	if r.expected != nil && r.expected.File.PacketPosition != nil &&
		(r.observed.packetPosition == nil || *r.observed.packetPosition != *r.expected.File.PacketPosition) {
		return result, comparisonDiscFailure("replay_packet_changed")
	}
	return result, runErr
}

func validateComparisonNativeFacts(source ComparisonSource, observation ComparisonFrameObservation, allowDisplayTurn bool) error {
	if allowDisplayTurn && observation.Width == source.Height && observation.Height == source.Width &&
		(observation.Width != source.Width || observation.SARNumerator != source.SARNumerator || observation.SARDenominator != source.SARDenominator) {
		observation.Width, observation.Height = observation.Height, observation.Width
		observation.SARNumerator, observation.SARDenominator = observation.SARDenominator, observation.SARNumerator
	}
	if source.NativePixelFormat != "" &&
		(observation.PixelFormat != source.NativePixelFormat || observation.Width != source.Width || observation.Height != source.Height) {
		return comparisonDiscFailure("changing_format")
	}
	if source.SARNumerator > 0 && (observation.SARNumerator != source.SARNumerator || observation.SARDenominator != source.SARDenominator) ||
		source.NativeColour != "" && observation.Colour != source.NativeColour {
		return comparisonDiscFailure("changing_display_format")
	}
	if source.NativeChromaLocation != "" && observation.ChromaLocation != source.NativeChromaLocation {
		return comparisonDiscFailure("changing_chroma_location")
	}
	return nil
}

var comparisonDisplayMatrix = regexp.MustCompile(
	`(?im)^[ \t]*(?:\[[^\r\n]+\][ \t]*)?(?:side data - 3x3 )?display ?matrix: rotation of (-?[0-9]+(?:\.[0-9]+)?) degrees`,
)

func comparisonQuarterTurn(diagnostics []byte) bool {
	matrix := comparisonDisplayMatrix.FindSubmatch(diagnostics)
	if matrix == nil {
		return false
	}
	degrees, err := strconv.ParseFloat(string(matrix[1]), 64)
	return err == nil && math.Abs(math.Mod(math.Abs(degrees), 180)-90) < 1
}

func bindFileObservation(source ComparisonSource, observation *ComparisonFrameObservation, displayTurn bool) error {
	swapped := displayTurn && observation.Width == source.Height && observation.Height == source.Width
	if source.NativePixelFormat != "" && !swapped && (observation.Width != source.Width || observation.Height != source.Height) {
		return comparisonDiscFailure("changing_geometry")
	}
	if source.InputFingerprint == "" {
		return nil
	}
	identity := api.ComparisonFrameIdentity{
		Version:             1,
		InputFingerprint:    source.InputFingerprint,
		Occurrence:          "file-0",
		StreamID:            "video-0",
		PTS:                 observation.PTS,
		TimeBaseNumerator:   observation.TimeBaseNumerator,
		TimeBaseDenominator: observation.TimeBaseDenominator,
	}
	key, err := api.CanonicalWorkflowFingerprint(identity)
	if err != nil {
		return fmt.Errorf("bind comparison file identity: %w", err)
	}
	observation.Identity = &identity
	observation.FrameID = string(key)
	return nil
}

func comparisonFileCommand(args []string) []string {
	args[slices.Index(args, "-loglevel")+1] = "repeat+info"
	return slices.Insert(args, slices.Index(args, "-i"), "-http_proxy", "", "-bsf:V:0", comparisonPacketFilter)
}

func (r *ComparisonFrameReplay) validFile(source ComparisonSource) bool {
	w, id := r.File, r.Identity
	return r.Version == 2 && w != nil && !w.Unenclosed && id.Version == 1 && id.InputFingerprint == source.InputFingerprint &&
		id.Occurrence == "file-0" && id.StreamID == "video-0" && id.TimeBaseNumerator > 0 && id.TimeBaseDenominator > 0 &&
		comparisonFinite(
			w.Start,
		) && w.Start >= 0 && w.Seek >= 0 && w.Seek <= w.Start && w.Start-w.Seek <= .125+1e-9 && w.Duration > 0 && w.Duration <= 64 && w.InputStart == source.StartTimeSeconds &&
		w.Slot >= 0 && w.Slot < 50000 && w.FirstPTS <= id.PTS && id.PTS <= w.LastPTS
}

func validateComparisonSequence(frames []ComparisonFrameObservation, window *comparisonDiscWindow) error {
	var last int64
	for i, frame := range frames {
		tick := frame.PTS
		if window != nil {
			tick = comparisonDiscClockNear(tick, window.Start90)
		}
		if i > 0 && (tick <= last || frame.TimeBaseNumerator != frames[0].TimeBaseNumerator || frame.TimeBaseDenominator != frames[0].TimeBaseDenominator) {
			return comparisonDiscFailure("ambiguous_native_pts")
		}
		last = tick
	}
	return nil
}

func bindComparisonSequence(
	request ComparisonSampleRequest,
	frames []ComparisonFrameObservation,
	window *comparisonDiscWindow,
	bounds *comparisonSequenceBounds,
) error {
	if err := validateComparisonSequence(frames, window); err != nil {
		return err
	}
	duration := request.Source.VideoDurationSeconds
	if duration <= 0 {
		duration = request.Source.DurationSeconds
	}
	if len(frames) > 0 && bounds != nil && (frames[0].TimeBaseNumerator != bounds.numerator || frames[0].TimeBaseDenominator != bounds.denominator) {
		return comparisonDiscFailure("original_clock_witness")
	}
	for i := range frames {
		frame := &frames[i]
		if window != nil {
			if err := bindDiscObservation(request.Source, *window, frame); err != nil {
				return err
			}
			continue
		}
		frame.RelativeTimestampSeconds = frame.TimestampSeconds - request.Source.StartTimeSeconds
		displayTurn := request.NormalizeSDR && request.Source.ScreenshotTurn
		if err := validateComparisonNativeFacts(request.Source, *frame, displayTurn); err != nil {
			return err
		}
		if err := bindFileObservation(request.Source, frame, displayTurn); err != nil {
			return err
		}
		if frame.Identity != nil {
			frame.Replay = &ComparisonFrameReplay{
				Version:     2,
				Identity:    *frame.Identity,
				PictureType: frame.PictureType,
				File: &ComparisonFileInterval{
					Unenclosed: bounds != nil &&
						(bounds.first >= frame.PTS && request.seek > 0 || bounds.last <= frame.PTS && request.StartSeconds+request.DurationSeconds < duration-1e-6),
					PacketPosition: frame.packetPosition,
					Seek:           request.seek,
					Start:          request.StartSeconds,
					Duration:       request.DurationSeconds,
					InputStart:     request.Source.StartTimeSeconds,
					FirstPTS:       frames[0].PTS,
					LastPTS:        frames[len(frames)-1].PTS,
					Slot:           i,
				},
			}
			if i > 0 && frames[i-1].packetPosition != nil {
				frame.Replay.File.Previous = &ComparisonFileNeighbor{PTS: frames[i-1].PTS, Position: *frames[i-1].packetPosition}
			} else if i == 0 && bounds != nil {
				frame.Replay.File.Previous = bounds.before
			}
			if i+1 < len(frames) && frames[i+1].packetPosition != nil {
				frame.Replay.File.Next = &ComparisonFileNeighbor{PTS: frames[i+1].PTS, Position: *frames[i+1].packetPosition}
			} else if i == len(frames)-1 && bounds != nil {
				frame.Replay.File.Next = bounds.after
			}
		}
	}
	return nil
}

func comparisonLocalPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("screenshots: comparison requires a local file")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("screenshots: resolve comparison source: %w", err)
	}
	info, err := os.Stat(abs) // #nosec G703 -- Explicit local media selection permits files outside application storage.
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("screenshots: comparison requires an accessible regular local file")
	}
	return abs, nil
}

func comparisonFinite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
