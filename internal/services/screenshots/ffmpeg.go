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

	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/pkg/api"
)

type Runner interface {
	Run(ctx context.Context, name string, args []string, dir string) (CommandResult, error)
}

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
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
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
	InputPath     string
	OutputPath    string
	Timestamp     float64
	FrameRate     float64
	Resolution    string
	UseLibplacebo bool
	ToneMap       bool
	Algorithm     string
	Desat         float64
	Compression   int
	FrameOverlay  bool
	OverlaySize   int
	FrameInfo     frameInfoResult
	SourceWidth   int
	SourceHeight  int
	WidthScale    float64
	HeightScale   float64
}

type frameInfoResult struct {
	FrameType string
	PTSTime   float64
}

type previewRequest struct {
	InputPath string
	Timestamp float64
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
	errFFmpegNoImage    = errors.New("ffmpeg produced no image")
	errFFmpegBlackImage = errors.New("ffmpeg produced black image")
)

// captureFrame writes one PNG frame and returns whether the successful attempt
// used libplacebo. Libplacebo captures retry once before falling back to the
// software filter chain so transient Vulkan setup failures remain recoverable.
func captureFrame(ctx context.Context, runner Runner, cmdPath string, req captureRequest, logger api.Logger) (bool, error) {
	logger = screenshotLogger(logger)
	if strings.TrimSpace(req.InputPath) == "" {
		return false, errors.New("screenshots: input path required")
	}
	if strings.TrimSpace(req.OutputPath) == "" {
		return false, errors.New("screenshots: output path required")
	}

	useLibplacebo := req.UseLibplacebo && req.ToneMap && !req.FrameOverlay
	args := buildFFmpegArgs(req, useLibplacebo)
	logger.Tracef(
		"screenshots: ffmpeg capture attempt mode=%s timestamp_seconds=%.3f input=%s output=%s filters=%s",
		ffmpegModeLabel(useLibplacebo),
		req.Timestamp,
		req.InputPath,
		req.OutputPath,
		ffmpegFilterFromArgs(args),
	)
	result, err := runner.Run(ctx, cmdPath, args, "")
	if err == nil && result.ExitCode == 0 {
		if err = validateCaptureOutput(req.OutputPath); err == nil {
			logger.Tracef("screenshots: ffmpeg capture ok mode=%s exit_code=%d", ffmpegModeLabel(useLibplacebo), result.ExitCode)
			return useLibplacebo, nil
		}
	}

	if useLibplacebo {
		logger.Debugf("screenshots: ffmpeg capture retry mode=%s reason=%s", ffmpegModeLabel(true), ffmpegResultPreview(result, err))
		args = buildFFmpegArgs(req, true)
		logger.Tracef(
			"screenshots: ffmpeg capture attempt mode=%s retry=%t timestamp_seconds=%.3f input=%s output=%s filters=%s",
			ffmpegModeLabel(true),
			true,
			req.Timestamp,
			req.InputPath,
			req.OutputPath,
			ffmpegFilterFromArgs(args),
		)
		result, err = runner.Run(ctx, cmdPath, args, "")
		if err == nil && result.ExitCode == 0 {
			if err = validateCaptureOutput(req.OutputPath); err == nil {
				logger.Tracef("screenshots: ffmpeg capture ok mode=%s retry=%t exit_code=%d", ffmpegModeLabel(true), true, result.ExitCode)
				return true, nil
			}
		}

		logger.Debugf(
			"screenshots: ffmpeg capture fallback from_mode=%s to_mode=%s reason=%s",
			ffmpegModeLabel(true),
			ffmpegModeLabel(false),
			ffmpegResultPreview(result, err),
		)
		args = buildFFmpegArgs(req, false)
		logger.Tracef(
			"screenshots: ffmpeg capture attempt mode=%s timestamp_seconds=%.3f input=%s output=%s filters=%s",
			ffmpegModeLabel(false),
			req.Timestamp,
			req.InputPath,
			req.OutputPath,
			ffmpegFilterFromArgs(args),
		)
		result, err = runner.Run(ctx, cmdPath, args, "")
		if err == nil && result.ExitCode == 0 {
			if err = validateCaptureOutput(req.OutputPath); err == nil {
				logger.Tracef("screenshots: ffmpeg capture ok mode=%s exit_code=%d", ffmpegModeLabel(false), result.ExitCode)
				return false, nil
			}
		}
	}

	stderr := strings.TrimSpace(string(result.Stderr))
	if stderr == "" && err != nil {
		stderr = err.Error()
	}
	logger.Debugf("screenshots: ffmpeg capture exhausted mode=%s reason=%s", ffmpegModeLabel(useLibplacebo), ffmpegResultPreview(result, err))
	return useLibplacebo, fmt.Errorf("screenshots: ffmpeg capture failed: %s", stderr)
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

// captureFrameBytes returns a single non-black preview frame encoded as PNG
// bytes from stdout. Empty or all-black ffmpeg output is treated as a failed
// preview so callers can try alternate screenshot inputs.
func captureFrameBytes(ctx context.Context, runner Runner, cmdPath string, req previewRequest, logger api.Logger) ([]byte, error) {
	logger = screenshotLogger(logger)
	if strings.TrimSpace(req.InputPath) == "" {
		return nil, errors.New("screenshots: input path required")
	}
	if req.Timestamp < 0 {
		return nil, errors.New("screenshots: timestamp required")
	}

	args := buildFFmpegPreviewArgs(req)
	logger.Tracef("screenshots: ffmpeg preview attempt timestamp_seconds=%.3f input=%s", req.Timestamp, req.InputPath)
	result, err := runner.Run(ctx, cmdPath, args, "")
	if err == nil && result.ExitCode == 0 && len(result.Stdout) > 0 {
		if err := validateImagePayload(result.Stdout); err != nil {
			logger.Debugf("screenshots: ffmpeg preview rejected reason=%s", redaction.RedactValue(err.Error(), nil))
			return nil, fmt.Errorf("screenshots: ffmpeg preview failed: %s", err.Error())
		}
		logger.Tracef("screenshots: ffmpeg preview ok bytes=%d exit_code=%d", len(result.Stdout), result.ExitCode)
		return result.Stdout, nil
	}
	if err == nil && result.ExitCode == 0 {
		err = errFFmpegNoImage
	}

	stderr := strings.TrimSpace(string(result.Stderr))
	if stderr == "" && err != nil {
		stderr = err.Error()
	}
	logger.Debugf("screenshots: ffmpeg preview exhausted reason=%s", ffmpegResultPreview(result, err))
	return nil, fmt.Errorf("screenshots: ffmpeg preview failed: %s", stderr)
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

func ffmpegModeLabel(useLibplacebo bool) string {
	if useLibplacebo {
		return "libplacebo"
	}
	return "software"
}

func ffmpegFilterFromArgs(args []string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-vf" {
			return args[i+1]
		}
	}
	return "none"
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

func buildFFmpegPreviewArgs(req previewRequest) []string {
	return []string{
		"-hide_banner",
		"-loglevel", "error",
		"-hwaccel", "auto",
		"-ss", fmt.Sprintf("%.3f", req.Timestamp),
		"-i", req.InputPath,
		"-an",
		"-sn",
		"-dn",
		"-frames:v", "1",
		"-vf", "format=rgb24",
		"-f", "image2pipe",
		"-vcodec", "png",
		"-",
	}
}

func buildFFmpegArgs(req captureRequest, useLibplacebo bool) []string {
	vf := buildFilterChain(req, useLibplacebo)
	compression := req.Compression
	if compression <= 0 {
		compression = 6
	}

	args := []string{"-hide_banner", "-y", "-loglevel", "error", "-ss", fmt.Sprintf("%.3f", req.Timestamp), "-i", req.InputPath, "-frames:v", "1"}
	if useLibplacebo {
		args = append(args, "-init_hw_device", "vulkan")
	}
	args = append(args, "-vf", vf, "-compression_level", strconv.Itoa(compression), "-pred", "mixed", req.OutputPath)
	return args
}

func buildFilterChain(req captureRequest, useLibplacebo bool) string {
	filters := make([]string, 0, 6)

	if scale := buildPARScaleFilter(req); scale != "" {
		filters = append(filters, scale)
	}

	if req.ToneMap {
		if useLibplacebo {
			filters = append(filters, "libplacebo=tonemapping=hable:colorspace=bt709:color_primaries=bt709:color_trc=bt709:range=tv")
		} else {
			algo := strings.TrimSpace(req.Algorithm)
			if algo == "" {
				algo = "mobius"
			}
			filters = append(filters,
				"zscale=transfer=linear",
				fmt.Sprintf("tonemap=tonemap=%s:desat=%.2f", algo, req.Desat),
				"zscale=transfer=bt709",
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

	frameNumber := int(req.Timestamp * req.FrameRate)
	if req.FrameInfo.PTSTime > 1.0 && absFloat(req.FrameInfo.PTSTime-req.Timestamp) < 10 {
		frameNumber = int(req.FrameInfo.PTSTime * req.FrameRate)
	}
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

func getFrameInfo(ctx context.Context, runner Runner, cmdPath string, inputPath string, timestamp float64) (frameInfoResult, error) {
	args := []string{
		"-hide_banner",
		"-loglevel", "info",
		"-ss", fmt.Sprintf("%.3f", timestamp),
		"-i", inputPath,
		"-frames:v", "1",
		"-vf", "showinfo",
		"-f", "null",
		"-",
	}
	result, err := runner.Run(ctx, cmdPath, args, "")
	if err != nil && result.ExitCode == 0 {
		err = nil
	}
	if err != nil && result.ExitCode != 0 {
		return frameInfoResult{}, err
	}

	stderr := string(result.Stderr)
	return parseShowInfo(stderr), nil
}

var (
	showInfoType = regexp.MustCompile(`pict_type:([A-Z])`)
	showInfoPTS  = regexp.MustCompile(`pts_time:([0-9.]+)`)
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

func absFloat(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
