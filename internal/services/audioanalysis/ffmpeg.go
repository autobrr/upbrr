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
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	diagnosticLimit    = 64 << 10
	inspectTitleLimit  = 512
	probeInputPrefix   = "Input #0,"
	probeCompletedLine = "At least one output file must be specified"
	inspectLineLimit   = diagnosticLimit
)

var ffmpegAudioStreamPattern = regexp.MustCompile(`(?m)^\s*Stream #0:\d+(?:\[[^\]\r\n]+\])?(?:\([^\r\n)]*\))?: Audio: ([^,\r\n]+),\s*(\d+) Hz,\s*([^,\r\n]+)`)
var ffmpegStreamHeaderPattern = regexp.MustCompile(`(?m)^\s*Stream #0:\d+`)
var ffmpegStreamTitlePattern = regexp.MustCompile(`(?mi)^\s*title\s*:\s*([^\r\n]+)`)
var ffmpegDurationPattern = regexp.MustCompile(`^\s*Duration:\s*(\d+):(\d+):(\d+(?:\.\d+)?)`)
var ffmpegStreamDurationPattern = regexp.MustCompile(`(?i)^\s*DURATION(?:-[a-z]+)?\s*:\s*(\d+):(\d+):(\d+(?:\.\d+)?)`)

var errNoInspectedAudioStreams = errors.New("audio analysis: no audio streams found")

type inspectedStream struct {
	codec           string
	title           string
	secondaryTitle  bool
	sampleRate      int
	layout          string
	channels        int
	durationSeconds float64
}

func isSecondaryAudioTitle(title string) bool {
	title = strings.ToLower(title)
	return strings.Contains(title, "commentary") || strings.Contains(title, "compatibility")
}

type decoder interface {
	Inspect(context.Context, string) ([]inspectedStream, error)
	Decode(context.Context, decodeRequest, []func(io.Reader) error) error
}

type decodeStreamRequest struct {
	audioOrdinal int
	codec        string
}

type decodeRequest struct {
	path           string
	streams        []decodeStreamRequest
	resourceLimits api.AudioAnalysisResourceLimits
}

type ffmpegDecoder struct {
	executable string
	command    func(context.Context, ...string) *exec.Cmd
	logger     api.Logger
}

func newFFmpegDecoder(logger api.Logger) (*ffmpegDecoder, error) {
	executable, err := screenshots.ResolveFFmpegExecutable()
	if err != nil {
		return nil, fmt.Errorf("resolve audio-analysis FFmpeg: %w", api.NewAudioAnalysisError(api.AudioAnalysisFailure{
			Code:    api.AudioAnalysisFailureFFmpegUnavailable,
			Message: "FFmpeg is unavailable for audio analysis",
		}, fmt.Errorf("audio analysis: ffmpeg unavailable: %w", err)))
	}
	return &ffmpegDecoder{executable: executable, logger: logger}, nil
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
	if strings.ContainsAny(path, "\r\n") {
		return nil, errors.New("audio analysis: source path contains an unsupported line break")
	}
	args := []string{"-hide_banner", "-nostdin", "-i", path}
	var stderr inspectDiagnosticBuffer
	cmd := d.commandContext(ctx, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	started := time.Now()
	if d.logger != nil {
		d.logger.Debugf("audioanalysis: state=started phase=ffmpeg_inspect")
	}
	err := cmd.Run()
	stderr.finishProbeLine()
	if d.logger != nil {
		state := "completed"
		if err != nil && !stderr.completedInputProbe() {
			state = "failed"
		}
		d.logger.Debugf(
			"audioanalysis: state=%s phase=ffmpeg_inspect elapsed_ms=%d diagnostic_bytes=%d diagnostic_truncated=%t",
			state, time.Since(started).Milliseconds(), stderr.diagnostics.Len(), stderr.diagnostics.Truncated(),
		)
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("audio analysis: inspect canceled: %w", ctx.Err())
	}
	streams, parseErr := parseInspectedStreams(stderr.streamText())
	if errors.Is(parseErr, errNoInspectedAudioStreams) &&
		stderr.completedInputProbe() {
		parseErr = audioAnalysisError(
			api.AudioAnalysisFailureNoAudio,
			"the source contains no decodable audio streams",
			errNoInspectedAudioStreams,
		)
	}
	if parseErr != nil {
		if err != nil {
			failure, typed := api.AsAudioAnalysisFailure(parseErr)
			if !typed || failure.Code != api.AudioAnalysisFailureNoAudio {
				return nil, fmt.Errorf("audio analysis: inspect streams: %w", err)
			}
		}
		return nil, parseErr
	}
	streamDurations := stderr.streamDurationValues()
	for index := range streams {
		if index < len(stderr.streamTitles) {
			streams[index].title = stderr.streamTitles[index]
			streams[index].secondaryTitle = stderr.streamSecondary[index]
		}
		if index < len(streamDurations) && streamDurations[index] > 0 {
			streams[index].durationSeconds = streamDurations[index]
			continue
		}
		streams[index].durationSeconds = stderr.duration()
	}
	return streams, nil
}

func parseInspectedStreams(text string) ([]inspectedStream, error) {
	if before, _, ok := strings.Cut(text, "Stream mapping:"); ok {
		text = before
	}
	matches := ffmpegAudioStreamPattern.FindAllStringSubmatchIndex(text, -1)
	streams := make([]inspectedStream, 0, len(matches))
	for _, match := range matches {
		rate, rateErr := strconv.Atoi(text[match[4]:match[5]])
		if rateErr != nil || rate <= 0 {
			return nil, errors.New("audio analysis: ffmpeg returned malformed stream facts")
		}
		layout := strings.TrimSpace(text[match[6]:match[7]])
		block := text[match[1]:]
		if next := ffmpegStreamHeaderPattern.FindStringIndex(block); next != nil {
			block = block[:next[0]]
		}
		title := ""
		if titleMatch := ffmpegStreamTitlePattern.FindStringSubmatch(block); len(titleMatch) > 1 {
			title = strings.TrimSpace(titleMatch[1])
		}
		streams = append(streams, inspectedStream{
			codec:          strings.TrimSpace(text[match[2]:match[3]]),
			title:          title,
			secondaryTitle: isSecondaryAudioTitle(title),
			sampleRate:     rate,
			layout:         layout,
			channels:       channelCountFromFFmpegLayout(layout),
		})
	}
	if len(streams) == 0 {
		return nil, errNoInspectedAudioStreams
	}
	return streams, nil
}

func (d *ffmpegDecoder) Decode(ctx context.Context, request decodeRequest, consumers []func(io.Reader) error) error {
	if d == nil || strings.TrimSpace(d.executable) == "" {
		return audioAnalysisError(
			api.AudioAnalysisFailureFFmpegUnavailable,
			"FFmpeg is unavailable for audio analysis",
			errors.New("audio analysis: ffmpeg unavailable"),
		)
	}
	if len(request.streams) == 0 || len(consumers) != len(request.streams) {
		return errors.New("audio analysis: one PCM consumer is required per stream")
	}
	for _, consume := range consumers {
		if consume == nil {
			return errors.New("audio analysis: PCM consumer is required")
		}
	}
	if len(consumers) == 1 {
		return d.decodeStdout(ctx, request, consumers[0])
	}
	return d.decodeMultiple(ctx, request, consumers)
}

func (d *ffmpegDecoder) decodeStdout(ctx context.Context, request decodeRequest, consume func(io.Reader) error) error {
	args := ffmpegDecodeArgs(request, []string{"pipe:1"})
	cmd := d.commandContext(ctx, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("audio analysis: open PCM stream: %w", err)
	}
	var diagnostics limitedBuffer
	cmd.Stderr = &diagnostics
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("audio analysis: start decoder: %w", err)
	}
	started := time.Now()
	d.logDecodeStart(request, "stdout")

	consumeErr := consume(stdout)
	if (consumeErr != nil && !errors.Is(consumeErr, errSpectrogramGeometryChanged) && !errors.Is(consumeErr, errDecodedAudioEmpty)) || ctx.Err() != nil {
		_ = stdout.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
	waitErr := cmd.Wait()
	d.logDecodeFinish(request, "stdout", started, diagnostics, consumeErr != nil || waitErr != nil || ctx.Err() != nil)
	decodeErr := classifyDecodeResult(ctx, consumeErr, waitErr, diagnostics.String())
	return withDecodeCompletion(decodeErr, []bool{decodeErr == nil}, waitErr != nil && consumeErr == nil && ctx.Err() == nil)
}

func (d *ffmpegDecoder) decodeMultiple(
	ctx context.Context,
	request decodeRequest,
	consumers []func(io.Reader) error,
) error {
	outputs, err := newPCMOutputs(len(consumers))
	if err != nil {
		return fmt.Errorf("audio analysis: open private PCM streams: %w", err)
	}
	defer closePCMOutputs(outputs)
	destinations := make([]string, len(consumers))
	for index := range outputs {
		destinations[index] = outputs[index].destination()
	}

	cmd := d.commandContext(ctx, ffmpegDecodeArgs(request, destinations)...)
	attachPCMOutputs(cmd, outputs)
	cmd.Stdout = io.Discard
	var diagnostics limitedBuffer
	cmd.Stderr = &diagnostics
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("audio analysis: start decoder: %w", err)
	}
	for index := range outputs {
		outputs[index].started()
	}
	started := time.Now()
	d.logDecodeStart(request, "private_pipe")

	type consumerResult struct {
		index     int
		err       error
		completed bool
		closed    bool
		fatal     bool
	}
	consumerResults := make(chan consumerResult, len(consumers))
	acceptCtx, cancelAccept := context.WithCancel(ctx)
	defer cancelAccept()
	for index, consume := range consumers {
		output := &outputs[index]
		go func() {
			connection, acceptErr := output.reader(acceptCtx, cmd.Process.Pid)
			if acceptErr != nil {
				consumerResults <- consumerResult{
					index:  index,
					err:    fmt.Errorf("audio analysis: accept private PCM stream: %w", acceptErr),
					closed: acceptCtx.Err() != nil,
					fatal:  acceptCtx.Err() == nil,
				}
				return
			}
			stopClose := context.AfterFunc(ctx, func() { _ = connection.Close() })
			consumeErr := consume(connection)
			var drainErr error
			if consumeErr != nil {
				_, drainErr = io.Copy(io.Discard, connection)
			}
			closeErr := connection.Close()
			stopClose()
			if consumeErr != nil {
				consumerResults <- consumerResult{
					index: index,
					err:   consumeErr,
					fatal: drainErr != nil,
				}
				return
			}
			if closeErr != nil {
				consumerResults <- consumerResult{index: index, err: fmt.Errorf("audio analysis: close local PCM stream: %w", closeErr)}
				return
			}
			consumerResults <- consumerResult{index: index, completed: ctx.Err() == nil}
		}()
	}
	waitResult := make(chan error, 1)
	go func() {
		waitResult <- cmd.Wait()
	}()

	remainingConsumers := len(consumers)
	var consumeErr error
	var waitErr error
	completed := make([]bool, len(consumers))
	waited := false
	for remainingConsumers > 0 || !waited {
		select {
		case result := <-consumerResults:
			remainingConsumers--
			completed[result.index] = result.completed
			if result.err != nil && !result.closed && consumeErr == nil {
				consumeErr = result.err
			}
			if result.fatal {
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
				cancelAccept()
			}
		case waitErr = <-waitResult:
			waited = true
			cancelAccept()
		}
	}
	d.logDecodeFinish(request, "private_pipe", started, diagnostics, consumeErr != nil || waitErr != nil || ctx.Err() != nil)
	decodeErr := classifyDecodeResult(ctx, consumeErr, waitErr, diagnostics.String())
	if decodeErr == nil && slices.Contains(completed, false) {
		decodeErr = errors.New("audio analysis: decoder did not open every local PCM stream")
	}
	return withDecodeCompletion(decodeErr, completed, waitErr != nil)
}

type decodeBatchError struct {
	cause          error
	completed      []bool
	processFailure bool
}

func (e *decodeBatchError) Error() string { return e.cause.Error() }

func (e *decodeBatchError) Unwrap() error { return e.cause }

func withDecodeCompletion(cause error, completed []bool, processFailure bool) error {
	if cause == nil {
		return nil
	}
	return &decodeBatchError{
		cause:          cause,
		completed:      append([]bool(nil), completed...),
		processFailure: processFailure,
	}
}

func completedDecodeStreams(err error, count int) ([]bool, bool) {
	completed := make([]bool, count)
	var batch *decodeBatchError
	if !errors.As(err, &batch) || len(batch.completed) != count {
		return completed, false
	}
	copy(completed, batch.completed)
	return completed, batch.processFailure
}

func classifyDecodeResult(ctx context.Context, consumeErr error, waitErr error, diagnostics string) error {
	if ctx.Err() != nil {
		return fmt.Errorf("audio analysis: decode canceled: %w", ctx.Err())
	}
	if waitErr != nil && errors.Is(consumeErr, errDecodedAudioEmpty) {
		consumeErr = nil // FFmpeg's failed exit and diagnostic explain why it produced no PCM.
	}
	if consumeErr != nil {
		return consumeErr
	}
	if waitErr != nil {
		if unsupportedDecoderDiagnostic(diagnostics) {
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

func closePCMOutputs(outputs []pcmOutput) {
	for index := range outputs {
		outputs[index].close()
	}
}

func (d *ffmpegDecoder) logDecodeStart(request decodeRequest, transport string) {
	if d.logger == nil {
		return
	}
	d.logger.Debugf(
		"audioanalysis: state=started phase=ffmpeg_decode stream_count=%d decoder_threads=%d transport=%s",
		len(request.streams), request.resourceLimits.DecoderThreads, transport,
	)
}

func (d *ffmpegDecoder) logDecodeFinish(
	request decodeRequest,
	transport string,
	started time.Time,
	diagnostics limitedBuffer,
	failed bool,
) {
	if d.logger == nil {
		return
	}
	state := "completed"
	if failed {
		state = "failed"
	}
	d.logger.Debugf(
		"audioanalysis: state=%s phase=ffmpeg_decode stream_count=%d transport=%s elapsed_ms=%d diagnostic_bytes=%d diagnostic_truncated=%t",
		state, len(request.streams), transport, time.Since(started).Milliseconds(), diagnostics.Len(), diagnostics.Truncated(),
	)
}

func unsupportedDecoderDiagnostic(diagnostic string) bool {
	normalized := strings.ToLower(diagnostic)
	return strings.Contains(normalized, "unknown decoder") ||
		strings.Contains(normalized, "no decoder found for") ||
		(strings.Contains(normalized, "decoder (codec ") && strings.Contains(normalized, "not found"))
}

func ffmpegDecodeArgs(request decodeRequest, destinations []string) []string {
	if len(destinations) != len(request.streams) {
		return nil
	}
	args := []string{
		"-hide_banner", "-nostdin", "-y",
		"-threads", strconv.Itoa(request.resourceLimits.DecoderThreads),
	}
	for _, stream := range request.streams {
		if !codecUsesDecoderDRC(stream.codec) {
			continue
		}
		args = append(args, "-drc_scale", "0")
		break
	}
	args = append(args, "-i", request.path)
	for index, stream := range request.streams {
		destination := destinations[index] // #nosec G602 -- destination and stream counts are equal above.
		args = append(args,
			"-map", fmt.Sprintf("0:a:%d", stream.audioOrdinal),
			"-vn", "-sn", "-dn",
			"-c:a", "pcm_f32le",
			"-f", "f32le",
			"-flush_packets", "0",
			destination,
		)
	}
	return args
}

type limitedBuffer struct {
	buffer    bytes.Buffer
	truncated bool
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := diagnosticLimit - b.buffer.Len()
	if len(value) > remaining {
		b.truncated = true
	}
	if remaining > 0 {
		value = value[:min(len(value), remaining)]
		_, _ = b.buffer.Write(value)
	}
	return original, nil
}

func (b *limitedBuffer) String() string { return b.buffer.String() }

func (b *limitedBuffer) Len() int { return b.buffer.Len() }

func (b *limitedBuffer) Truncated() bool { return b.truncated }

type inspectDiagnosticBuffer struct {
	diagnostics     limitedBuffer
	streamFacts     limitedBuffer
	line            []byte
	lineOverflow    bool
	inputOpened     bool
	probeCompleted  bool
	streamMapping   bool
	capturingAudio  bool
	durationSeconds float64
	streamDurations []float64
	streamTitles    []string
	streamSecondary []bool
	keywordWindow   [13]byte
	keywordLength   int
	lineSecondary   bool
}

func (b *inspectDiagnosticBuffer) Write(value []byte) (int, error) {
	_, _ = b.diagnostics.Write(value)
	for _, character := range value {
		if character == '\n' {
			b.finishProbeLine()
			continue
		}
		if !b.lineSecondary {
			lower := character
			if lower >= 'A' && lower <= 'Z' {
				lower += 'a' - 'A'
			}
			if b.keywordLength < len(b.keywordWindow) {
				b.keywordWindow[b.keywordLength] = lower
				b.keywordLength++
			} else {
				copy(b.keywordWindow[:], b.keywordWindow[1:])
				b.keywordWindow[len(b.keywordWindow)-1] = lower
			}
			window := b.keywordWindow[:b.keywordLength]
			b.lineSecondary = bytes.HasSuffix(window, []byte("commentary")) || bytes.HasSuffix(window, []byte("compatibility"))
		}
		if len(b.line) < inspectLineLimit {
			b.line = append(b.line, character)
		} else {
			b.lineOverflow = true
		}
	}
	return len(value), nil
}

func (b *inspectDiagnosticBuffer) finishProbeLine() {
	line := strings.TrimSuffix(string(b.line), "\r")
	if b.durationSeconds == 0 {
		b.durationSeconds = parseFFmpegDuration(line)
	}
	b.inputOpened = b.inputOpened || strings.HasPrefix(line, probeInputPrefix)
	b.probeCompleted = b.probeCompleted || (!b.lineOverflow && line == probeCompletedLine)
	if !b.streamMapping {
		mappingStarted := strings.TrimSpace(line) == "Stream mapping:"
		if !mappingStarted {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "Stream #") {
				b.capturingAudio = ffmpegAudioStreamPattern.MatchString(line)
				if b.capturingAudio {
					_, _ = b.streamFacts.Write([]byte(line))
					_, _ = b.streamFacts.Write([]byte{'\n'})
					b.streamDurations = append(b.streamDurations, 0)
					b.streamTitles = append(b.streamTitles, "")
					b.streamSecondary = append(b.streamSecondary, false)
				}
			} else if b.capturingAudio {
				if match := ffmpegStreamTitlePattern.FindStringSubmatch(line); len(match) > 1 {
					title := strings.TrimSpace(match[1])
					index := len(b.streamTitles) - 1
					b.streamTitles[index] = title[:min(len(title), inspectTitleLimit)]
					b.streamSecondary[index] = b.lineSecondary
				}
			}
			if b.capturingAudio && len(b.streamDurations) > 0 {
				if duration := parseFFmpegStreamDuration(line); duration > 0 {
					b.streamDurations[len(b.streamDurations)-1] = duration
				}
			}
		}
		b.streamMapping = mappingStarted
	}
	b.line = b.line[:0]
	b.lineOverflow = false
	b.keywordLength = 0
	b.lineSecondary = false
}

func (b *inspectDiagnosticBuffer) streamText() string { return b.streamFacts.String() }

func (b *inspectDiagnosticBuffer) duration() float64 { return b.durationSeconds }

func (b *inspectDiagnosticBuffer) streamDurationValues() []float64 {
	return append([]float64(nil), b.streamDurations...)
}

func (b *inspectDiagnosticBuffer) completedInputProbe() bool {
	return b.inputOpened && b.probeCompleted
}

func parseFFmpegDuration(line string) float64 {
	return parseMatchedDuration(ffmpegDurationPattern.FindStringSubmatch(line))
}

func parseFFmpegStreamDuration(line string) float64 {
	return parseMatchedDuration(ffmpegStreamDurationPattern.FindStringSubmatch(line))
}

func parseMatchedDuration(match []string) float64 {
	if len(match) != 4 {
		return 0
	}
	hours, hourErr := strconv.ParseFloat(match[1], 64)
	minutes, minuteErr := strconv.ParseFloat(match[2], 64)
	seconds, secondErr := strconv.ParseFloat(match[3], 64)
	if hourErr != nil || minuteErr != nil || secondErr != nil || minutes >= 60 || seconds >= 60 {
		return 0
	}
	return hours*3600 + minutes*60 + seconds
}

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
