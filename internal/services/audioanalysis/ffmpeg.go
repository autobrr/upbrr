// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package audioanalysis

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

const diagnosticLimit = 64 << 10

var ffmpegAudioStreamPattern = regexp.MustCompile(`(?m)^\s*Stream #0:\d+(?:\[[^\]\r\n]+\])?(?:\([^\r\n)]*\))?: Audio: ([^,\r\n]+),\s*(\d+) Hz,\s*([^,\r\n]+)`)

type inspectedStream struct {
	codec      string
	sampleRate int
	layout     string
	channels   int
}

type decoder interface {
	Inspect(context.Context, string) ([]inspectedStream, error)
	Decode(context.Context, decodeRequest, func(io.Reader) error) error
}

type decodeRequest struct {
	path         string
	audioOrdinal int
	codec        string
}

type ffmpegDecoder struct {
	executable string
	command    func(context.Context, ...string) *exec.Cmd
}

func newFFmpegDecoder() (*ffmpegDecoder, error) {
	executable, err := screenshots.ResolveFFmpegExecutable()
	if err != nil {
		return nil, fmt.Errorf("resolve audio-analysis FFmpeg: %w", api.NewAudioAnalysisError(api.AudioAnalysisFailure{
			Code:    api.AudioAnalysisFailureFFmpegUnavailable,
			Message: "FFmpeg is unavailable for audio analysis",
		}, fmt.Errorf("audio analysis: ffmpeg unavailable: %w", err)))
	}
	return &ffmpegDecoder{executable: executable}, nil
}

func (d *ffmpegDecoder) commandContext(ctx context.Context, args ...string) *exec.Cmd {
	if d.command != nil {
		return d.command(ctx, args...)
	}
	//nolint:gosec // The trusted FFmpeg resolver supplies the executable; argv is passed without a shell.
	return exec.CommandContext(ctx, d.executable, args...)
}

func (d *ffmpegDecoder) Inspect(ctx context.Context, path string) ([]inspectedStream, error) {
	if d == nil || strings.TrimSpace(d.executable) == "" {
		return nil, audioAnalysisError(
			api.AudioAnalysisFailureFFmpegUnavailable,
			"FFmpeg is unavailable for audio analysis",
			errors.New("audio analysis: ffmpeg unavailable"),
		)
	}
	args := []string{"-hide_banner", "-nostdin", "-i", path}
	var stderr limitedBuffer
	cmd := d.commandContext(ctx, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("audio analysis: inspect canceled: %w", ctx.Err())
	}
	streams, parseErr := parseInspectedStreams(stderr.String())
	if parseErr != nil {
		if err != nil {
			return nil, fmt.Errorf("audio analysis: inspect streams: %w", err)
		}
		return nil, parseErr
	}
	return streams, nil
}

func parseInspectedStreams(text string) ([]inspectedStream, error) {
	if before, _, ok := strings.Cut(text, "Stream mapping:"); ok {
		text = before
	}
	matches := ffmpegAudioStreamPattern.FindAllStringSubmatch(text, -1)
	streams := make([]inspectedStream, 0, len(matches))
	for _, match := range matches {
		rate, rateErr := strconv.Atoi(match[2])
		if rateErr != nil || rate <= 0 {
			return nil, errors.New("audio analysis: ffmpeg returned malformed stream facts")
		}
		layout := strings.TrimSpace(match[3])
		streams = append(streams, inspectedStream{
			codec:      strings.TrimSpace(match[1]),
			sampleRate: rate,
			layout:     layout,
			channels:   channelCountFromFFmpegLayout(layout),
		})
	}
	if len(streams) == 0 {
		return nil, audioAnalysisError(
			api.AudioAnalysisFailureNoAudio,
			"the source contains no decodable audio streams",
			errors.New("audio analysis: no audio streams found"),
		)
	}
	return streams, nil
}

func (d *ffmpegDecoder) Decode(ctx context.Context, request decodeRequest, consume func(io.Reader) error) error {
	if d == nil || strings.TrimSpace(d.executable) == "" {
		return audioAnalysisError(
			api.AudioAnalysisFailureFFmpegUnavailable,
			"FFmpeg is unavailable for audio analysis",
			errors.New("audio analysis: ffmpeg unavailable"),
		)
	}
	if consume == nil {
		return errors.New("audio analysis: PCM consumer is required")
	}
	args := ffmpegDecodeArgs(request)
	cmd := d.commandContext(ctx, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("audio analysis: open PCM stream: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("audio analysis: open decoder diagnostics: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("audio analysis: start decoder: %w", err)
	}

	var diagnostics limitedBuffer
	var stderrErr error
	var stderrDone sync.WaitGroup
	stderrDone.Go(func() {
		_, stderrErr = io.Copy(&diagnostics, stderr)
	})

	consumeErr := consume(stdout)
	if consumeErr != nil || ctx.Err() != nil {
		_ = stdout.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
	stderrDone.Wait()
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return fmt.Errorf("audio analysis: decode canceled: %w", ctx.Err())
	}
	if consumeErr != nil {
		return consumeErr
	}
	if stderrErr != nil {
		return fmt.Errorf("audio analysis: read decoder diagnostics: %w", stderrErr)
	}
	if waitErr != nil {
		if unsupportedDecoderDiagnostic(diagnostics.String()) {
			return audioAnalysisError(
				api.AudioAnalysisFailureUnsupportedCodec,
				"the selected audio codec is not supported by this FFmpeg build",
				fmt.Errorf("audio analysis: decoder unavailable: %w", waitErr),
			)
		}
		return fmt.Errorf("audio analysis: decoder failed: %w", waitErr)
	}
	return nil
}

func unsupportedDecoderDiagnostic(diagnostic string) bool {
	normalized := strings.ToLower(diagnostic)
	return strings.Contains(normalized, "unknown decoder") ||
		strings.Contains(normalized, "no decoder found for") ||
		(strings.Contains(normalized, "decoder (codec ") && strings.Contains(normalized, "not found"))
}

func ffmpegDecodeArgs(request decodeRequest) []string {
	args := []string{"-hide_banner", "-nostdin"}
	if codecUsesDecoderDRC(request.codec) {
		args = append(args, "-drc_scale", "0")
	}
	args = append(args,
		"-i", request.path,
		"-map", fmt.Sprintf("0:a:%d", request.audioOrdinal),
		"-vn", "-sn", "-dn",
		"-c:a", "pcm_f32le",
		"-f", "f32le",
		"pipe:1",
	)
	return args
}

type limitedBuffer struct {
	buffer bytes.Buffer
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := diagnosticLimit - b.buffer.Len()
	if remaining > 0 {
		value = value[:min(len(value), remaining)]
		_, _ = b.buffer.Write(value)
	}
	return original, nil
}

func (b *limitedBuffer) String() string { return b.buffer.String() }

func codecUsesDecoderDRC(codec string) bool {
	family := audioCodecFamily(codec)
	return family == "ac3" || family == "eac3"
}

func channelCountFromFFmpegLayout(layout string) int {
	normalized := strings.ToLower(strings.TrimSpace(layout))
	switch {
	case strings.HasPrefix(normalized, "mono"):
		return 1
	case strings.HasPrefix(normalized, "stereo"):
		return 2
	case strings.HasPrefix(normalized, "2.1"):
		return 3
	case strings.HasPrefix(normalized, "3.0"):
		return 3
	case strings.HasPrefix(normalized, "4.0"), strings.HasPrefix(normalized, "quad"):
		return 4
	case strings.HasPrefix(normalized, "5.0"):
		return 5
	case strings.HasPrefix(normalized, "5.1"):
		return 6
	case strings.HasPrefix(normalized, "6.1"):
		return 7
	case strings.HasPrefix(normalized, "7.1"):
		return 8
	}
	fields := strings.Fields(normalized)
	if len(fields) > 1 && fields[1] == "channels" {
		count, _ := strconv.Atoi(fields[0])
		return count
	}
	return 0
}
