// Package render probes FFmpeg DVD menu capability and decodes exact-coordinate
// menu background frames selected by the pure-Go engine.
package render

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	// MaxDiagnosticBytes bounds retained FFmpeg diagnostic output.
	MaxDiagnosticBytes = 64 << 10
	// MaxFrameBytes bounds retained lossless frame output.
	MaxFrameBytes = 64 << 20
	// MaxFrameWidth is the largest accepted decoded frame width.
	MaxFrameWidth = 4096
	// MaxFrameHeight is the largest accepted decoded frame height.
	MaxFrameHeight = 4096
	// MaxFramePixels bounds decoded frame allocation.
	MaxFramePixels = 16 << 20
)

var (
	// ErrCapability identifies missing or unusable FFmpeg dvdvideo support.
	ErrCapability = errors.New("FFmpeg lacks required DVD menu capability")
	// ErrFrame identifies invalid requests or failed DVD menu frame decoding.
	ErrFrame       = errors.New("FFmpeg DVD menu frame decode failed")
	errOutputLimit = errors.New("command output limit exceeded")
)

var requiredOptions = [...]string{"-menu", "-menu_lu", "-menu_vts", "-pgc", "-pg"}

// Output is bounded command output.
type Output struct {
	// Stdout contains retained output up to the requested limit.
	Stdout []byte
	// Stderr contains retained diagnostics up to MaxDiagnosticBytes.
	Stderr []byte
}

// Runner executes FFmpeg without shell interpolation.
type Runner interface {
	// Run invokes executable directly with discrete args and bounded output.
	Run(ctx context.Context, executable string, args []string, stdoutLimit int) (Output, error)
}

// ExecRunner is the production os/exec implementation.
type ExecRunner struct{}

// Run resolves executable on PATH, invokes it without a shell, and retains
// bounded stdout and stderr. Context cancellation terminates the child process.
func (ExecRunner) Run(ctx context.Context, executable string, args []string, stdoutLimit int) (Output, error) {
	if stdoutLimit <= 0 {
		return Output{}, fmt.Errorf("invalid stdout limit %d", stdoutLimit)
	}
	stdout := &limitWriter{limit: stdoutLimit}
	stderr := &limitWriter{limit: MaxDiagnosticBytes}
	resolved, err := exec.LookPath(executable)
	if err != nil {
		return Output{}, fmt.Errorf("resolve FFmpeg executable: %w", err)
	}
	//nolint:gosec // executable is resolved with exec.LookPath; args are discrete validated values, never a shell command.
	command := exec.CommandContext(ctx, resolved, args...)
	command.Stdout = stdout
	command.Stderr = stderr
	err = command.Run()
	if stdout.exceeded || stderr.exceeded {
		return Output{Stdout: stdout.data, Stderr: stderr.data}, errOutputLimit
	}
	if err != nil {
		return Output{Stdout: stdout.data, Stderr: stderr.data}, fmt.Errorf("run FFmpeg: %w", err)
	}
	return Output{Stdout: stdout.data, Stderr: stderr.data}, nil
}

type limitWriter struct {
	data     []byte
	limit    int
	exceeded bool
}

// Write retains data up to the configured limit, records overflow, and reports
// the full input length so the child process can finish cleanly.
func (w *limitWriter) Write(data []byte) (int, error) {
	remaining := w.limit - len(w.data)
	if remaining <= 0 {
		w.exceeded = true
		return len(data), nil
	}
	if len(data) > remaining {
		w.data = append(w.data, data[:remaining]...)
		w.exceeded = true
		return len(data), nil
	}
	w.data = append(w.data, data...)
	return len(data), nil
}

// Capability is safe engine diagnostic metadata without local paths.
type Capability struct {
	// Available reports that the dvdvideo demuxer and all required options were found.
	Available bool
	// Version is a bounded first line from FFmpeg's version output.
	Version string
	// Options contains the required dvdvideo options observed by the probe.
	Options []string
}

// Probe verifies the dvdvideo demuxer and every exact-coordinate menu option.
func Probe(ctx context.Context, runner Runner, executable string) (Capability, error) {
	if runner == nil || strings.TrimSpace(executable) == "" {
		return Capability{}, fmt.Errorf("%w: FFmpeg runner unavailable", ErrCapability)
	}
	help, err := runner.Run(ctx, executable, []string{"-hide_banner", "-h", "demuxer=dvdvideo"}, MaxDiagnosticBytes)
	if err != nil {
		return Capability{}, fmt.Errorf("%w: demuxer probe", ErrCapability)
	}
	text := string(append(append([]byte(nil), help.Stdout...), help.Stderr...))
	fields := strings.Fields(text)
	available := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if strings.HasPrefix(field, "-") {
			available[strings.TrimRight(field, ",:")] = struct{}{}
		}
	}
	options := make([]string, 0, len(requiredOptions))
	missing := make([]string, 0, len(requiredOptions))
	for _, option := range requiredOptions {
		if _, ok := available[option]; !ok {
			missing = append(missing, option)
			continue
		}
		options = append(options, option)
	}
	if len(missing) > 0 {
		return Capability{}, fmt.Errorf("%w: missing %s", ErrCapability, strings.Join(missing, ", "))
	}
	if !strings.Contains(strings.ToLower(text), "dvdvideo") {
		return Capability{}, fmt.Errorf("%w: dvdvideo demuxer", ErrCapability)
	}
	versionOutput, versionErr := runner.Run(ctx, executable, []string{"-hide_banner", "-version"}, MaxDiagnosticBytes)
	if versionErr != nil {
		return Capability{}, fmt.Errorf("%w: version probe", ErrCapability)
	}
	version := firstLine(string(versionOutput.Stdout))
	if version == "" {
		version = firstLine(string(versionOutput.Stderr))
	}
	return Capability{
		Available: true,
		Version:   version,
		Options:   options,
	}, nil
}

func firstLine(value string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(value), "\n")
	line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
	if len(line) > 256 {
		line = line[:256]
	}
	return line
}

// FrameRequest contains inventory-validated FFmpeg dvdvideo coordinates.
type FrameRequest struct {
	// SourcePath is the host filesystem path of the extracted VIDEO_TS directory.
	SourcePath string
	// VTS selects zero for the manager domain or 1 through 99 for a title set.
	VTS int
	// LanguageUnit is the one-based FFmpeg dvdvideo language-unit index.
	LanguageUnit int
	// PGC is the one-based menu program-chain index.
	PGC int
	// Program is the one-based program index within PGC.
	Program int
	// Target is the non-negative seek offset within the selected program.
	Target time.Duration
	// Deinterlace enables FFmpeg's bwdif filter before frame extraction.
	Deinterlace bool
}

// BuildArgs returns FFmpeg arguments with every dvdvideo input option before
// -i and no shell-built command string.
func BuildArgs(request FrameRequest) ([]string, error) {
	if strings.TrimSpace(request.SourcePath) == "" {
		return nil, fmt.Errorf("%w: empty source", ErrFrame)
	}
	if request.VTS < 0 || request.VTS > 99 || request.LanguageUnit < 1 || request.LanguageUnit > 99 || request.PGC < 1 || request.PGC > 999 ||
		request.Program < 1 ||
		request.Program > 255 ||
		request.Target < 0 {
		return nil, fmt.Errorf("%w: invalid menu coordinate", ErrFrame)
	}
	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-f", "dvdvideo",
		"-menu", "1",
		"-menu_vts", strconv.Itoa(request.VTS),
		"-menu_lu", strconv.Itoa(request.LanguageUnit),
		"-pgc", strconv.Itoa(request.PGC),
		"-pg", strconv.Itoa(request.Program),
	}
	if request.Target > 0 {
		args = append(args, "-ss", formatDuration(request.Target))
	}
	args = append(args, "-i", request.SourcePath, "-map", "0:v:0", "-an", "-sn", "-dn")
	if request.Deinterlace {
		args = append(args, "-vf", "bwdif=mode=send_frame:parity=auto:deint=all")
	}
	args = append(args, "-frames:v", "1", "-c:v", "png", "-f", "image2pipe", "pipe:1")
	return args, nil
}

func formatDuration(value time.Duration) string {
	seconds := value.Seconds()
	return strconv.FormatFloat(seconds, 'f', 6, 64)
}

// DecodeFrame invokes FFmpeg and decodes one lossless background image.
func DecodeFrame(ctx context.Context, runner Runner, executable string, request FrameRequest) (image.Image, error) {
	if runner == nil || strings.TrimSpace(executable) == "" {
		return nil, fmt.Errorf("%w: FFmpeg runner unavailable", ErrFrame)
	}
	args, err := BuildArgs(request)
	if err != nil {
		return nil, err
	}
	output, err := runner.Run(ctx, executable, args, MaxFrameBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: process", ErrFrame)
	}
	if len(output.Stdout) == 0 {
		return nil, fmt.Errorf("%w: empty image", ErrFrame)
	}
	config, err := png.DecodeConfig(bytes.NewReader(output.Stdout))
	if err != nil {
		return nil, fmt.Errorf("%w: image header", ErrFrame)
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > MaxFrameWidth || config.Height > MaxFrameHeight ||
		uint64(config.Width)*uint64(config.Height) > MaxFramePixels {
		return nil, fmt.Errorf("%w: image dimensions", ErrFrame)
	}
	frame, err := png.Decode(io.LimitReader(bytes.NewReader(output.Stdout), MaxFrameBytes))
	if err != nil {
		return nil, fmt.Errorf("%w: image decode", ErrFrame)
	}
	return frame, nil
}
