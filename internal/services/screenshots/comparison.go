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

	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/pkg/api"
)

// ComparisonCaptionVersion identifies the generated caption layout for retained pixels.
const ComparisonCaptionVersion = 2

// ComparisonSource describes one local video. Missing timing stays zero; callers
// own source fingerprint verification and alignment with other inputs.
type ComparisonSource struct {
	Path string
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
	HDRFacts             api.HDRFacts
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

// ComparisonFrameObservation identifies the decoded picture in the source clock.
// TimestampSeconds is PTS * TimeBaseNumerator / TimeBaseDenominator, not the
// requested seek or a frame ordinal. Empty PictureType means unknown.
type ComparisonFrameObservation struct {
	PTS                 int64
	TimeBaseNumerator   int64
	TimeBaseDenominator int64
	TimestampSeconds    float64
	// RelativeTimestampSeconds is the observed PTS time minus the input start.
	RelativeTimestampSeconds float64
	PictureType              string
}

// ComparisonCaptureRequest selects a seek relative to the file's start time.
// OverlayLabel optionally adds the three-line comparison caption: metadata-based
// frame counters, observed picture type, then label. Missing counters stay unknown.
// Labels accept at most 80 ASCII letters, digits, spaces, dots, dashes and
// underscores. Overlay enablement is comparison-local; text size follows the
// ordinary screenshot setting and scales with the source height.
type ComparisonCaptureRequest struct {
	Source           ComparisonSource
	TimestampSeconds float64
	OverlayLabel     string
	// ToneMap overrides the ordinary screenshot setting; nil inherits it.
	ToneMap *bool
	// OutputWidth/OutputHeight select a square-pixel fit-and-pad canvas. Both
	// zero retain the source size; both must be positive when scaling is enabled.
	OutputWidth  int
	OutputHeight int
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
	ComparisonFrameObservation
	Pixels []byte
}

// InspectComparisonSource probes one regular local file using the existing
// native MediaInfo reader. It rejects ambiguous video selection and does not
// substitute guessed frame rates or infer HDR from a filename.
func (s *Service) InspectComparisonSource(ctx context.Context, path string) (ComparisonSource, error) {
	path, err := comparisonLocalPath(path)
	if err != nil {
		return ComparisonSource{}, err
	}
	report, err := gomediainfo.AnalyzeFileContext(ctx, path)
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
	for _, track := range doc.Media.Track {
		if strings.EqualFold(trackString(track, "@type"), "video") {
			videos++
			frameCount, _ = strconv.ParseInt(trackString(track, "FrameCount"), 10, 64)
			videoDuration = parseDurationValue(trackString(track, "Duration", "Duration/String3", "Duration/String2", "Duration/String"))
		}
	}
	if videos != 1 {
		return ComparisonSource{}, errors.New("screenshots: comparison requires exactly one inspected video stream")
	}
	width, height, widthScale, heightScale := mediaInfoVideoGeometry(doc)
	executable, err := resolveFFmpeg()
	if err != nil {
		return ComparisonSource{}, err
	}
	probe, err := s.runner.Run(ctx, executable, []string{
		"-hide_banner", "-loglevel", "info", "-i", path, "-map", "0:V:0", "-an", "-sn", "-dn",
		"-frames:v", "1", "-vf", "showinfo", "-f", "null", "-",
	}, "")
	if ctx.Err() != nil {
		return ComparisonSource{}, fmt.Errorf("screenshots: comparison inspection canceled: %w", ctx.Err())
	}
	if corruption := ffmpegFrameCorruptionError(probe.Stderr); corruption != nil {
		return ComparisonSource{}, corruption
	}
	start := comparisonInputStart.FindSubmatch(probe.Stderr)
	if err != nil || probe.ExitCode != 0 || len(start) != 2 {
		return ComparisonSource{}, errors.New("screenshots: comparison input start time unavailable")
	}
	startSeconds, err := strconv.ParseFloat(string(start[1]), 64)
	if err != nil || !comparisonFinite(startSeconds) {
		return ComparisonSource{}, errors.New("screenshots: comparison input start time invalid")
	}
	frameRate := mediaInfoFrameRate(doc)
	if rate := comparisonFrameRate.FindSubmatch(probe.Stderr); frameRate <= 0 && len(rate) == 3 {
		numerator, _ := strconv.ParseFloat(string(rate[1]), 64)
		denominator, _ := strconv.ParseFloat(string(rate[2]), 64)
		if denominator > 0 {
			frameRate = numerator / denominator
		}
	}
	return ComparisonSource{
		Path:                 path,
		StartTimeSeconds:     startSeconds,
		DurationSeconds:      mediaInfoDurationSeconds(doc),
		FrameRate:            frameRate,
		FrameCount:           max(0, frameCount),
		VideoDurationSeconds: videoDuration,
		Width:                width,
		Height:               height,
		WidthScale:           widthScale,
		HeightScale:          heightScale,
		HDRFacts:             mediafacts.HDRFromMediaInfoDocument(mediafacts.MediaInfoDocument(doc)),
	}, nil
}

// ComparisonPreview uses the strict comparison capture path. It never applies
// black-frame timestamp offsets or disc-segment recovery.
func (s *Service) ComparisonPreview(ctx context.Context, request ComparisonCaptureRequest) (ComparisonFrame, error) {
	return s.ComparisonCapture(ctx, request)
}

// ComparisonCapture returns one usable PNG with observed source PTS. Renderer
// fallback keeps the same seek, and differing observed pictures fail closed.
// Black output, missing PTS, cancellation and corruption never move the seek.
func (s *Service) ComparisonCapture(ctx context.Context, request ComparisonCaptureRequest) (ComparisonFrame, error) {
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
	runner := &comparisonRunner{
		runner:        s.runner,
		source:        request.Source,
		seek:          request.TimestampSeconds,
		label:         request.OverlayLabel,
		overlaySize:   s.cfg.ScreenshotHandling.OverlayTextSize,
		overlayHeight: request.Source.Height,
	}
	if request.OutputHeight > 0 {
		runner.overlayHeight = request.OutputHeight
	}
	toneMap := s.cfg.ScreenshotHandling.ToneMap
	if request.ToneMap != nil {
		toneMap = *request.ToneMap
	}
	frame, err := renderFrame(ctx, runner, executable, captureRequest{
		InputPath:    path,
		Timestamp:    request.TimestampSeconds,
		InspectFrame: true,
		HDRFacts:     request.Source.HDRFacts,
		SourceWidth:  request.Source.Width,
		SourceHeight: request.Source.Height,
		WidthScale:   request.Source.WidthScale,
		HeightScale:  request.Source.HeightScale,
		ToneMap: toneMap && slices.ContainsFunc(request.Source.HDRFacts.Formats, func(format api.HDRFormat) bool {
			return format != api.HDRFormatSDR && format != api.HDRFormatWCG
		}),
		UseLibplacebo: s.cfg.ScreenshotHandling.UseLibplacebo,
		Algorithm:     s.cfg.ScreenshotHandling.TonemapAlgorithm,
		Desat:         s.cfg.ScreenshotHandling.Desat,
		Compression:   s.cfg.ScreenshotHandling.FFmpegCompression,
		OutputWidth:   request.OutputWidth,
		OutputHeight:  request.OutputHeight,
	}, logging.FromContext(ctx, s.logger))
	if err != nil {
		return ComparisonFrame{}, err
	}
	if runner.observed == nil {
		return ComparisonFrame{}, errors.New("screenshots: comparison decoded frame identity unavailable")
	}
	runner.observed.RelativeTimestampSeconds = runner.observed.TimestampSeconds - request.Source.StartTimeSeconds
	geometry, _, err := image.DecodeConfig(bytes.NewReader(frame.Payload))
	if err != nil {
		return ComparisonFrame{}, fmt.Errorf("screenshots: decode comparison PNG: %w", err)
	}
	if request.OutputWidth > 0 && (geometry.Width != request.OutputWidth || geometry.Height != request.OutputHeight) {
		return ComparisonFrame{}, errors.New("screenshots: comparison renderer did not produce the selected frame size")
	}
	return ComparisonFrame{
		ComparisonFrameObservation: *runner.observed,
		RequestedTimestampSeconds:  request.TimestampSeconds,
		ImageBytes:                 frame.Payload,
		Processing:                 frame.Processing,
		Width:                      geometry.Width,
		Height:                     geometry.Height,
	}, nil
}

// SampleComparisonSource decodes a bounded window without image recovery or
// rendering overlays. Start and duration are input-relative seconds; spacing is
// measured on actual frame timestamps. At most 50,000 samples may be requested.
// Returned PTS remains in the original source clock, including a nonzero start.
func (s *Service) SampleComparisonSource(ctx context.Context, source ComparisonSource, start, duration, strideSeconds float64) ([]ComparisonSample, error) {
	logger := logging.FromContext(ctx, s.logger)
	logger.Debugf("comparisons: phase=sampling decision=decode window_seconds=%.3f stride_seconds=%.4f", duration, strideSeconds)
	windowStart := source.StartTimeSeconds + start
	windowEnd := windowStart + duration
	if !comparisonFinite(start) || !comparisonFinite(duration) || !comparisonFinite(strideSeconds) || !comparisonFinite(source.StartTimeSeconds) ||
		!comparisonFinite(windowEnd) || start < 0 || duration <= 0 || strideSeconds <= 0 || math.Ceil(duration/strideSeconds)+1 > 50000 {
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
	filter := fmt.Sprintf(
		"trim=start=%.9f:end=%.9f,select='isnan(prev_selected_t)+gte(t-prev_selected_t,%.9f)',showinfo,scale=16:16:out_range=full,format=gray",
		windowStart,
		windowEnd,
		strideSeconds,
	)
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
	result, err := s.runner.Run(ctx, executable, []string{
		"-hide_banner", "-loglevel", "info", "-copyts", "-ss", strconv.FormatFloat(start, 'f', 9, 64), "-i", path,
		"-map", "0:V:0", "-an", "-sn", "-dn", "-vf", filter, "-fps_mode", "passthrough",
		"-frames:v", "50000", "-pix_fmt", "gray", "-f", "rawvideo", "-",
	}, "")
	if ctx.Err() != nil {
		return nil, fmt.Errorf("screenshots: comparison sampling canceled: %w", ctx.Err())
	}
	if corruption := ffmpegFrameCorruptionError(result.Stderr); corruption != nil {
		return nil, corruption
	}
	if err != nil || result.ExitCode != 0 {
		return nil, errors.New("screenshots: comparison sampling failed")
	}
	observations, err := comparisonObservations(result.Stderr)
	if err != nil || len(observations) == 0 || len(observations) > 50000 || len(result.Stdout) != len(observations)*256 {
		return nil, errors.New("screenshots: comparison samples lack matching decoded frame identities")
	}
	samples := make([]ComparisonSample, 0, len(observations))
	for i, observation := range observations {
		observation.RelativeTimestampSeconds = observation.TimestampSeconds - source.StartTimeSeconds
		samples = append(samples, ComparisonSample{
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
	comparisonLabel        = regexp.MustCompile(`^[A-Za-z0-9 ._-]{0,80}$`)
	comparisonShowInfoLine = regexp.MustCompile(`(?m)^\[Parsed_showinfo_\d+ @ [^\]\r\n]+\]\s+([^\r\n]+)`)
	comparisonTimeBase     = regexp.MustCompile(`^config in time_base:\s*(\d+)/(\d+),`)
	comparisonPTS          = regexp.MustCompile(`^n:\s*\d+\s+pts:\s*(-?\d+)\s`)
	comparisonInputStart   = regexp.MustCompile(`(?m)^  Duration: [^\r\n]*, start: ([+-]?[0-9.]+), bitrate:`)
	comparisonFrameRate    = regexp.MustCompile(`(?m)^\[Parsed_showinfo_\d+ @ [^\]\r\n]+\]\s+config in time_base: [^\r\n]*frame_rate:\s*(\d+)/(\d+)`)
)

func comparisonObservations(stderr []byte) ([]ComparisonFrameObservation, error) {
	// FFmpeg progress reports can end with CR immediately before a showinfo record.
	stderr = bytes.ReplaceAll(stderr, []byte{'\r'}, []byte{'\n'})
	var numerator, denominator int64
	var observations []ComparisonFrameObservation
	for _, line := range comparisonShowInfoLine.FindAllSubmatch(stderr, -1) {
		body := string(line[1])
		if match := comparisonTimeBase.FindStringSubmatch(body); len(match) == 3 {
			numerator, _ = strconv.ParseInt(match[1], 10, 64)
			denominator, _ = strconv.ParseInt(match[2], 10, 64)
			continue
		}
		if !strings.HasPrefix(body, "n:") {
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
		observations = append(observations, ComparisonFrameObservation{
			PTS:                 pts,
			TimeBaseNumerator:   numerator,
			TimeBaseDenominator: denominator,
			TimestampSeconds:    float64(pts) * float64(numerator) / float64(denominator),
			PictureType:         pictureType,
		})
	}
	return observations, nil
}

type comparisonRunner struct {
	runner        Runner
	source        ComparisonSource
	seek          float64
	label         string
	overlaySize   int
	overlayHeight int
	observed      *ComparisonFrameObservation
	changed       bool
}

func (r *comparisonRunner) Run(ctx context.Context, executable string, args []string, dir string) (CommandResult, error) {
	if r.changed {
		return CommandResult{}, errors.New("screenshots: comparison renderer changed decoded frame identity")
	}
	args = append([]string{"-copyts"}, args...)
	for i := range args {
		if args[i] == "-ss" && i+1 < len(args) {
			args[i+1] = strconv.FormatFloat(r.seek, 'f', 9, 64)
		}
	}
	result, err := r.run(ctx, executable, args, dir)
	if ctx.Err() != nil {
		return result, fmt.Errorf("screenshots: comparison rendering canceled: %w", ctx.Err())
	}
	if corruption := ffmpegFrameCorruptionError(result.Stderr); corruption != nil {
		return result, corruption
	}
	if err != nil || result.ExitCode != 0 || r.label == "" || validateImagePayload(result.Stdout) != nil {
		return result, err
	}
	if r.observed == nil {
		return result, errors.New("screenshots: comparison overlay lacks decoded frame identity")
	}
	pictureType := r.observed.PictureType
	if pictureType == "" {
		pictureType = "Unknown"
	}
	number, total := comparisonDisplayCounters(r.source, *r.observed)
	lines := []string{
		"Frame " + number + " of " + total,
		"Picture type\\: " + pictureType,
		r.label,
	}
	for i := range args {
		if args[i] == "-vf" && i+1 < len(args) {
			args[i+1] += "," + strings.Join(overlayTextFilters(lines, r.overlaySize, r.overlayHeight), ",")
		}
	}

	return r.run(ctx, executable, args, dir)
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
	result, runErr := r.runner.Run(ctx, executable, args, dir)
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
	return result, runErr
}

func comparisonLocalPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("screenshots: comparison requires a local file")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("screenshots: resolve comparison source: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("screenshots: comparison requires an accessible regular local file")
	}
	return abs, nil
}

func comparisonFinite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
