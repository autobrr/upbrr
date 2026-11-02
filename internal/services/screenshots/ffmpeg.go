// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/pkg/api"
)

// Runner executes FFmpeg-compatible commands for capture services. Run must
// honor ctx and use dir as the working directory when it is non-empty.
type Runner interface {
	Run(ctx context.Context, name string, args []string, dir string) (CommandResult, error)
}

// CommandResult preserves command output and the observed process exit code
// for fallback decisions and bounded diagnostics.
type CommandResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

type commandRunner struct{}

func (commandRunner) Run(ctx context.Context, name string, args []string, dir string) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if strings.TrimSpace(dir) != "" {
		cmd.Dir = dir
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := CommandResult{
		Stdout:   stdout.Bytes(),
		Stderr:   stderr.Bytes(),
		ExitCode: 0,
	}
	if err != nil {
		exitCode := 1
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			exitCode = exitErr.ExitCode()
		}
		result.ExitCode = exitCode
		return result, fmt.Errorf("screenshots: run ffmpeg command: %w", err)
	}
	return result, nil
}

func resolveFFmpeg() (string, error) {
	if bundled := bundledFFmpegPath(); bundled != "" {
		return bundled, nil
	}
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", errors.New("screenshots: ffmpeg not found")
	}
	return path, nil
}

// ResolveFFmpegExecutable returns the existing bundled-or-PATH FFmpeg choice
// shared by screenshot and DVD-menu capture services.
func ResolveFFmpegExecutable() (string, error) {
	return resolveFFmpeg()
}

func bundledFFmpegPath() string {
	name := "ffmpeg"
	folder := osFolder()
	if folder == "" {
		return ""
	}
	if folder == "windows" {
		name = "ffmpeg.exe"
	}

	candidates := make([]string, 0, 6)
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "bin", "ffmpeg", folder, name))
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(exeDir, "bin", "ffmpeg", folder, name))
		candidates = append(candidates, filepath.Join(exeDir, "..", "bin", "ffmpeg", folder, name))
	}

	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func osFolder() string {
	switch runtime.GOOS {
	case "linux":
		return "linux"
	case "darwin":
		return "darwin"
	case "windows":
		return "windows"
	default:
		return ""
	}
}

type captureRequest struct {
	HDRFacts       api.HDRFacts
	TimelineOffset float64
	InspectFrame   bool
	Playlist       string
	InputPath      string
	OutputPath     string
	Timestamp      float64
	FrameRate      float64
	Resolution     string
	UseLibplacebo  bool
	ToneMap        bool
	Algorithm      string
	Desat          float64
	Compression    int
	FrameOverlay   bool
	OverlaySize    int
	FrameInfo      frameInfoResult
	SourceWidth    int
	SourceHeight   int
	WidthScale     float64
	HeightScale    float64
}

type frameInfoResult struct {
	FrameType string
	PTSTime   float64
}

const (
	ffmpegLogPreviewLimit = 2048
	// blackPixelThreshold is the highest 8-bit RGB channel value still treated
	// as black when rejecting blank ffmpeg frames.
	blackPixelThreshold = 4
)

// ffmpegDurationPattern extracts the container duration reported on FFmpeg's
// stderr.
var ffmpegDurationPattern = regexp.MustCompile(`(?m)Duration:\s*(\d{2,}:\d{2}:\d{2}(?:\.\d+)?)\s*,`)

// ffmpeg image validation errors are returned after ffmpeg exits successfully
// but fails to produce a usable screenshot frame.
var (
	errFFmpegNoImage      = errors.New("ffmpeg produced no image")
	errFFmpegBlackImage   = errors.New("ffmpeg produced black image")
	errFFmpegDOVIMetadata = errors.New("decoded Dolby Vision metadata unavailable")
)

var ffmpegFrameCorruptionIndicators = []string{
	"error while decoding",
	"invalid data found when processing input",
	"non-existing pps",
	"incomplete frame",
	"corrupt decoded frame",
	"corrupt input packet",
}

// blackFrameTimestampOffsets lists relative timestamp adjustments (in seconds)
// tried when FFmpeg produces a black image at the initial frame timestamp.
var blackFrameTimestampOffsets = []float64{0, 2, 4, 8, 16, 32, 64, -2, -4, -8, -16, -32, -64}

// captureFrame writes one validated PNG and reports its actual processing.
// Renderer recovery preserves the timestamp; only black-frame recovery below
// can move it, and the returned timestamp records that movement.
// Black output images automatically retry near the requested timestamp using
// relative offsets before failing.
func captureFrame(ctx context.Context, runner Runner, cmdPath string, req captureRequest, logger api.Logger) (renderedFrame, error) {
	var lastErr error
	var lastFrame renderedFrame
	retryingBlackFrame := false

	for _, offset := range blackFrameTimestampOffsets {
		candidateTS := req.Timestamp + offset
		if candidateTS < 0 {
			continue
		}
		attemptReq := req
		attemptReq.Timestamp = candidateTS
		if offset != 0 {
			attemptReq.FrameInfo = frameInfoResult{}
		}

		frame, err := captureFrameSingle(ctx, runner, cmdPath, attemptReq, logger)
		if err == nil {
			if offset != 0 {
				l := screenshotLogger(logger)
				l.Debugf(
					"screenshots: black image recovered with timestamp offset original=%.3f offset=%.3f new=%.3f",
					req.Timestamp,
					offset,
					candidateTS,
				)
			}
			return frame, nil
		}

		lastErr = err
		lastFrame = frame

		if errors.Is(err, errFFmpegBlackImage) {
			retryingBlackFrame = true
		} else if !retryingBlackFrame || !errors.Is(err, errFFmpegNoImage) {
			break
		}
		l := screenshotLogger(logger)
		l.Debugf(
			"screenshots: ffmpeg capture produced unusable image at timestamp=%.3f, retrying timestamp offset",
			candidateTS,
		)
	}

	return lastFrame, lastErr
}

func captureFrameSingle(ctx context.Context, runner Runner, cmdPath string, req captureRequest, logger api.Logger) (renderedFrame, error) {
	if strings.TrimSpace(req.OutputPath) == "" {
		return renderedFrame{}, errors.New("screenshots: output path required")
	}
	return renderFrame(ctx, runner, cmdPath, req, logger)
}

// validateCaptureOutput verifies that ffmpeg wrote a decodable, non-black
// image. Invalid or black output files are removed so later fallback attempts
// can replace the same target path cleanly.
func validateCaptureOutput(path string) error {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() <= 0 {
		return errFFmpegNoImage
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("screenshots: read ffmpeg capture output: %w", err)
	}
	if err := validateImagePayload(payload); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

// captureFrameBytes uses the same renderer policy and validation as final capture,
// returning a non-black PNG and its processing outcome without persisting it.
func captureFrameBytes(ctx context.Context, runner Runner, cmdPath string, req captureRequest, logger api.Logger) ([]byte, api.ScreenshotProcessing, error) {
	req.OutputPath = ""
	frame, err := renderFrame(ctx, runner, cmdPath, req, logger)
	return frame.Payload, frame.Processing, err
}

// probeVideoDuration reads FFmpeg's container timing without decoding the VOB.
// The bundled distribution does not include ffprobe, so FFmpeg performs the
// equivalent header probe with a zero-length null output. It returns seconds
// and rejects missing, malformed, or non-positive duration metadata.
func probeVideoDuration(ctx context.Context, runner Runner, cmdPath, inputPath string) (float64, error) {
	if strings.TrimSpace(inputPath) == "" {
		return 0, errors.New("screenshots: duration probe input path required")
	}
	args := []string{
		"-hide_banner",
		"-loglevel", "info",
		"-i", inputPath,
		"-map", "0:v:0",
		"-t", "0",
		"-f", "null",
		"-",
	}
	result, err := runner.Run(ctx, cmdPath, args, "")
	if ctxErr := ctx.Err(); ctxErr != nil {
		return 0, fmt.Errorf("screenshots: ffmpeg duration probe canceled: %w", ctxErr)
	}
	if corruptionErr := ffmpegFrameCorruptionError(result.Stderr); corruptionErr != nil {
		return 0, corruptionErr
	}
	if err != nil || result.ExitCode != 0 {
		return 0, fmt.Errorf("screenshots: ffmpeg duration probe failed: %s", ffmpegResultPreview(result, err))
	}
	match := ffmpegDurationPattern.FindSubmatch(result.Stderr)
	if len(match) != 2 {
		return 0, errors.New("screenshots: ffmpeg duration unavailable")
	}
	duration := parseDurationSeconds(string(match[1]))
	if duration <= 0 {
		return 0, errors.New("screenshots: ffmpeg duration invalid")
	}
	return duration, nil
}

// validateImagePayload rejects empty, undecodable, and all-black image bytes
// before callers accept a screenshot or preview as usable.
func validateImagePayload(payload []byte) error {
	if len(payload) == 0 {
		return errFFmpegNoImage
	}
	black, err := imagePayloadIsBlack(payload)
	if err != nil {
		return fmt.Errorf("screenshots: decode ffmpeg image: %w", err)
	}
	if black {
		return errFFmpegBlackImage
	}
	return nil
}

// imagePayloadIsBlack reports whether every visible pixel is at or below the
// black threshold. Fully transparent pixels do not make an image usable.
func imagePayloadIsBlack(payload []byte) (bool, error) {
	img, _, err := image.Decode(bytes.NewReader(payload))
	if err != nil {
		return false, fmt.Errorf("decode image payload: %w", err)
	}
	bounds := img.Bounds()
	if bounds.Empty() {
		return false, nil
	}
	maxChannel := uint32(blackPixelThreshold * 257)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			if a == 0 {
				continue
			}
			if r > maxChannel || g > maxChannel || b > maxChannel {
				return false, nil
			}
		}
	}
	return true, nil
}

// ffmpegResultPreview returns a bounded, redacted diagnostic string for logs
// while leaving the caller-owned error text unchanged.
func ffmpegResultPreview(result CommandResult, err error) string {
	message := strings.TrimSpace(string(result.Stderr))
	if message == "" && err != nil {
		message = err.Error()
	}
	if message == "" {
		message = fmt.Sprintf("exit_code=%d", result.ExitCode)
	}
	if len(message) > ffmpegLogPreviewLimit {
		message = message[:ffmpegLogPreviewLimit] + "...[truncated]"
	}
	return redaction.RedactValue(message, nil)
}

// ffmpegFrameCorruptionError maps allow-listed decoder diagnostics to a stable
// hard failure without exposing FFmpeg's raw stderr.
func ffmpegFrameCorruptionError(stderr []byte) error {
	diagnostics := strings.ToLower(string(stderr))
	for _, indicator := range ffmpegFrameCorruptionIndicators {
		if strings.Contains(diagnostics, indicator) {
			return fmt.Errorf("screenshots: %w: ffmpeg reported %q", internalerrors.ErrFrameCorruption, indicator)
		}
	}
	return nil
}

func buildFFmpegArgs(req captureRequest, useLibplacebo bool) []string {
	args := []string{"-hide_banner", "-y", "-loglevel", "error"}
	if req.InspectFrame {
		args[3] = "info"
	}
	if useLibplacebo {
		args = append(args, "-init_hw_device", "vulkan=upbrr", "-filter_hw_device", "upbrr")
	}
	// Software decoding preserves decoded Dolby Vision metadata and avoids making
	// optional CUDA/X11 decoding a prerequisite for either entrypoint.
	args = append(args, "-ss", fmt.Sprintf("%.3f", req.Timestamp))
	args = append(args, screenshotInputArgs(req.InputPath, req.Playlist)...)
	if req.Playlist == "" {
		args = append(args, "-map", "0:v:0")
	}
	args = append(args, "-an", "-sn", "-dn", "-frames:v", "1",
		"-vf", buildFilterChain(req, useLibplacebo), "-vcodec", "png",
		"-compression_level", strconv.Itoa(req.Compression), "-pred", "mixed")
	args = append(args, "-f", "image2pipe")
	if req.OutputPath == "" {
		return append(args, "-")
	}
	return append(args, req.OutputPath)
}

func buildFilterChain(req captureRequest, useLibplacebo bool) string {
	// Limit input as well as output: buffered renderers must not select or mix
	// later decoded frames while producing a single requested screenshot.
	filters := []string{"trim=end_frame=1"}
	if req.InspectFrame {
		filters = append(filters, "showinfo")
	}

	if scale := buildPARScaleFilter(req); scale != "" {
		filters = append(filters, scale)
	}

	if req.ToneMap {
		if useLibplacebo {
			// Keep decoded software frames (and Dolby Vision side data) intact;
			// libplacebo owns GPU transfer and returns the explicit PNG/drawtext format.
			filters = append(
				filters,
				"libplacebo=apply_dolbyvision=1:tonemapping=hable:colorspace=bt709:color_primaries=bt709:color_trc=bt709:range=tv:format=rgb24",
			)
		} else {
			algo := strings.TrimSpace(req.Algorithm)
			if algo == "" {
				algo = "mobius"
			}
			filters = append(filters,
				"zscale=transfer=linear",
				"format=gbrpf32le",
				fmt.Sprintf("tonemap=tonemap=%s:desat=%.2f", algo, req.Desat),
				"zscale=primaries=bt709:transfer=bt709:matrix=bt709:range=tv",
			)
		}
	}

	filters = append(filters, "format=rgb24")

	if req.FrameOverlay {
		filters = append(filters, overlayFilters(req)...)
	}

	return strings.Join(filters, ",")
}

func buildPARScaleFilter(req captureRequest) string {
	widthScale := req.WidthScale
	heightScale := req.HeightScale
	if widthScale == 0 {
		widthScale = 1
	}
	if heightScale == 0 {
		heightScale = 1
	}
	if widthScale == 1 && heightScale == 1 {
		return ""
	}
	if req.SourceWidth <= 0 || req.SourceHeight <= 0 {
		return ""
	}
	scaledWidth := roundToEven(float64(req.SourceWidth) * widthScale)
	scaledHeight := roundToEven(float64(req.SourceHeight) * heightScale)
	if scaledWidth <= 0 || scaledHeight <= 0 {
		return ""
	}
	return fmt.Sprintf("scale=%d:%d", scaledWidth, scaledHeight)
}

func roundToEven(value float64) int {
	rounded := int(math.RoundToEven(value))
	if rounded%2 != 0 {
		rounded++
	}
	return rounded
}

func overlayFilters(req captureRequest) []string {
	textSize := req.OverlaySize
	if textSize <= 0 {
		textSize = 18
	}
	res := digitsOnly(req.Resolution)
	if res == 0 {
		res = 1080
	}
	fontSize := (textSize * res) / 1080
	xAll := (10 * res) / 1080
	lineSpacing := int(float64(fontSize) * 1.1)
	if lineSpacing <= 0 {
		lineSpacing = fontSize
	}
	yNumber := xAll
	yType := yNumber + lineSpacing
	yHDR := yType + lineSpacing

	frameNumber := int((req.Timestamp + req.TimelineOffset) * req.FrameRate)
	frameType := req.FrameInfo.FrameType
	if strings.TrimSpace(frameType) == "" {
		frameType = "Unknown"
	}

	filters := []string{
		fmt.Sprintf("drawtext=text='Frame Number\\: %d':fontcolor=white:fontsize=%d:x=%d:y=%d:box=1:boxcolor=black@0.5", frameNumber, fontSize, xAll, yNumber),
		fmt.Sprintf("drawtext=text='Frame Type\\: %s':fontcolor=white:fontsize=%d:x=%d:y=%d:box=1:boxcolor=black@0.5", frameType, fontSize, xAll, yType),
	}
	if req.ToneMap {
		filters = append(
			filters,
			fmt.Sprintf("drawtext=text='Tonemapped HDR':fontcolor=white:fontsize=%d:x=%d:y=%d:box=1:boxcolor=black@0.5", fontSize, xAll, yHDR),
		)
	}
	return filters
}

var (
	showInfoDOVIMetadata = regexp.MustCompile(`(?m)^\[Parsed_showinfo_\d+ @ [^\]\r\n]+\]\s+side data - Dolby Vision Metadata:`)
	showInfoType         = regexp.MustCompile(`\btype:([A-Z])\b`)
	showInfoPTS          = regexp.MustCompile(`pts_time:([0-9.]+)`)
)

func parseShowInfo(output string) frameInfoResult {
	result := frameInfoResult{}
	if match := showInfoType.FindStringSubmatch(output); len(match) == 2 {
		result.FrameType = match[1]
	}
	if match := showInfoPTS.FindStringSubmatch(output); len(match) == 2 {
		if value := parseFloat(match[1]); value > 0 {
			result.PTSTime = value
		}
	}
	return result
}

func digitsOnly(value string) int {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}
	buf := strings.Builder{}
	for _, r := range trimmed {
		if r >= '0' && r <= '9' {
			buf.WriteRune(r)
		}
	}
	if buf.Len() == 0 {
		return 0
	}
	return int(parseFloat(buf.String()))
}

// screenshotInputArgs keeps protocol options before -i. An empty playlist
// preserves ordinary-file and extracted-disc capture behavior.
func screenshotInputArgs(source, playlist string) []string {
	if playlist != "" {
		return []string{"-playlist", playlist, "-i", "bluray:" + source, "-map", "0:v:0"}
	}
	return []string{"-i", source}
}
