// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

var errComparisonDecoderLimit = errors.New("comparison decoder limit exceeded")
var errComparisonHardwareUnavailable = errors.New("comparison hardware decoder unavailable")

var comparisonHardwareDiagnostic = regexp.MustCompile(
	`(?i)^\[(?:AVHW(?:Device|Frames)Context|CUDA|(?:h264|hevc|av1|vp[89]|mpeg[124]video|mpeg4|vc1)(?:_[a-z0-9]+)?|(?:Parsed_)?hwdownload(?:_\d+)?) @ [^\]]+\] `,
)

var comparisonHardwareFailureCode = regexp.MustCompile(
	`(?i)\b(?:CUDA_ERROR_(?:NO_DEVICE|DEVICE_UNAVAILABLE|OUT_OF_MEMORY|CONTEXT_IS_DESTROYED|INVALID_CONTEXT)|DXGI_ERROR_DEVICE_(?:REMOVED|RESET|HUNG)|(?:0x)?887a000[567]|VA_STATUS_ERROR_(?:INVALID_DISPLAY|UNSUPPORTED_PROFILE)|kVT(?:VideoDecoder(?:MalfunctionErr|NotAvailableNowErr)|CouldNotFindVideoDecoderErr))\b`,
)
var comparisonVideoToolboxFailure = regexp.MustCompile(`Failed to decode frame \((?:decoder malfunction|invalid session), -?\d+\)`)

var comparisonVAAPITransferFailure = regexp.MustCompile(
	`Failed to (?:sync surface|read image from surface|map image from surface) (?:0x)?[0-9a-fA-F]+: (\d+) \(`,
)

func comparisonHardwareStageFailure(line string) error {
	if !comparisonHardwareDiagnostic.MatchString(line) {
		return nil
	}
	if comparisonHardwareFailureCode.MatchString(line) || comparisonVideoToolboxFailure.MatchString(line) {
		return errComparisonHardwareUnavailable
	}
	if match := comparisonVAAPITransferFailure.FindStringSubmatch(line); len(match) == 2 {
		switch match[1] {
		case "1", "3", "6":
			return errComparisonHardwareUnavailable
		case "23":
			return internalerrors.ErrFrameCorruption
		}
	}
	return nil
}

type comparisonFirstNativeKey struct{}
type comparisonProcessCounterKey struct{}

func comparisonProcessCounter(ctx context.Context, budget *ComparisonDecodeBudget) (context.Context, *int) {
	if count, ok := ctx.Value(comparisonProcessCounterKey{}).(*int); ok {
		return ctx, count
	}
	count := new(0)
	if budget != nil {
		count = &budget.ProcessStarts
	}
	return context.WithValue(ctx, comparisonProcessCounterKey{}, count), count
}

func recordComparisonProcessStart(ctx context.Context) {
	if count, ok := ctx.Value(comparisonProcessCounterKey{}).(*int); ok {
		(*count)++
	}
}

func comparisonDecoderLimit(reason string) error {
	return api.NewOperationError(api.OperationFailure{
		Code:             api.OperationFailureInvalidSource,
		Operation:        api.OperationKindMedia,
		Recovery:         api.OperationRecoveryEditInput,
		ComparisonReason: reason,
		Message:          "The comparison decoder exceeded a bounded resource limit.",
	}, errComparisonDecoderLimit)
}

// finiteComparisonRunner retains parsed provenance independently of its bounded
// diagnostic tail. A noisy decoder cannot evict observations or grow buffers.
type finiteComparisonRunner struct {
	base           Runner
	outputLimit    int
	retentionLimit int64
}

type comparisonOutputBuffer struct {
	buffer bytes.Buffer
	limit  int
	err    error
	cancel context.CancelFunc
}

func (b *comparisonOutputBuffer) Write(data []byte) (int, error) {
	if len(data) > b.limit-b.buffer.Len() {
		b.err = comparisonDecoderLimit("decoder_output_limit")
		if b.cancel != nil {
			b.cancel()
		}
		return 0, b.err
	}
	_, _ = b.buffer.Write(data)
	return len(data), nil
}

type comparisonDiagnosticStream struct {
	retentionLimit int64
	stream         *comparisonRasterStream
	inputURL       string
	outputURL      string
	buffer, tail   []byte
	config         map[string]string
	records        map[string][]ComparisonFrameObservation
	counts         map[string]int
	sideData       map[string]hash.Hash
	firstNative    func()
	hardwareDecode bool
	count          int
	err            error
	cancel         context.CancelFunc
}

func (d *comparisonDiagnosticStream) Write(data []byte) (int, error) {
	count := len(data)
	for len(data) > 0 {
		end := bytes.IndexAny(data, "\r\n")
		if end < 0 {
			end = len(data)
		}
		if len(d.buffer)+end > 8192 {
			d.err = comparisonDecoderLimit("decoder_diagnostic_limit")
			if d.cancel != nil {
				d.cancel()
			}
			return 0, d.err
		}
		d.buffer = append(d.buffer, data[:end]...)
		data = data[end:]
		if len(data) == 0 {
			break
		}
		d.line(string(d.buffer))
		d.buffer = d.buffer[:0]
		data = data[1:]
		if d.err != nil {
			if d.cancel != nil {
				d.cancel()
			}
			return 0, d.err
		}
	}
	return count, nil
}

func (d *comparisonDiagnosticStream) line(line string) {
	if d.inputURL != "" {
		line = strings.ReplaceAll(line, d.inputURL, "comparison-input")
	}
	if d.outputURL != "" {
		line = strings.ReplaceAll(line, d.outputURL, "comparison-output")
	}
	if line == "" {
		return
	}
	if err := ffmpegFrameCorruptionError([]byte(line)); err != nil {
		d.err = err
		return
	}
	if d.hardwareDecode {
		if err := comparisonHardwareStageFailure(line); err != nil {
			d.err = err
			return
		}
	}
	if comparisonAnyShowInfoLine.MatchString(line) {
		prefix, body, ok := strings.Cut(line, "]")
		if ok {
			body = strings.TrimSpace(body)
			name := strings.Fields(strings.TrimPrefix(prefix, "["))[0]
			if d.config == nil {
				d.config = map[string]string{}
				d.records = map[string][]ComparisonFrameObservation{}
				d.counts = map[string]int{}
				d.sideData = map[string]hash.Hash{}
			}
			if strings.HasPrefix(body, "config in time_base:") {
				d.config[name] = body
				return
			}
			if strings.HasPrefix(body, "n:") {
				if d.retentionLimit > 0 && !strings.HasPrefix(name, "showinfo@") && int64(d.counts[name]+1)*4096 > d.retentionLimit {
					d.err = ErrComparisonFastInconclusive
					return
				}
				if d.counts[name] >= 50000 {
					d.err = comparisonDecoderLimit("decoder_observation_limit")
					return
				}
				text := "[Parsed_showinfo_1 @ bounded] " + d.config[name] + "\n[Parsed_showinfo_1 @ bounded] " + body + "\n"
				observed, err := comparisonObservations([]byte(text))
				if err != nil || len(observed) != 1 {
					d.err = errors.New("comparison decoder provenance is incomplete")
					return
				}
				if d.stream != nil {
					if err := d.stream.observe(name, observed[0]); err != nil {
						d.err = err
						return
					}
				}
				// Streamed branch/output records have already bound their raster.
				// Retain only native provenance for publication, avoiding a second
				// movie-sized collection and diagnostic reconstruction of branches.
				if name != "showinfo@comparison_decoder_init" && (d.stream == nil || !strings.HasPrefix(name, "showinfo@")) {
					d.records[name] = append(d.records[name], observed[0])
					d.sideData[name] = sha256.New()
				}
				if !strings.HasPrefix(name, "showinfo@") || name == "showinfo@comparison_decoder_init" {
					if d.firstNative != nil {
						d.firstNative()
						d.firstNative = nil
					}
				}
				d.counts[name]++
				d.count++
				if d.count > 200000 {
					d.err = comparisonDecoderLimit("decoder_observation_limit")
				}
				return
			}
			if colour := comparisonColour(body); colour != "" && len(d.records[name]) > 0 {
				index := len(d.records[name]) - 1
				d.records[name][index].Colour = colour
				return
			}
			if !strings.HasPrefix(name, "showinfo@") && len(d.records[name]) > 0 && !strings.HasPrefix(body, "config ") {
				if digest := d.sideData[name]; digest != nil {
					_, _ = digest.Write([]byte(body + "\n"))
					d.records[name][len(d.records[name])-1].SideDataChecksum = hex.EncodeToString(digest.Sum(nil))
				}
			}
			// Side-data records remain in the bounded diagnostic tail.
		}
	}
	if d.stream != nil && strings.Contains(line, "[metadata@comparison_cut @") {
		if err := d.stream.metric(line); err != nil {
			d.err = err
		}
		return
	}
	if len(d.tail)+len(line)+1 > 64<<10 {
		keep := (64 << 10) - len(line) - 1
		copy(d.tail, d.tail[len(d.tail)-keep:])
		d.tail = d.tail[:keep]
	}
	d.tail = append(d.tail, line...)
	d.tail = append(d.tail, '\n')
}

func (d *comparisonDiagnosticStream) result() ([]byte, error) {
	if len(d.buffer) > 0 {
		d.line(string(d.buffer))
		d.buffer = nil
	}
	if d.err != nil {
		return nil, d.err
	}
	var out bytes.Buffer
	_, _ = out.Write(d.tail)
	for name, records := range d.records {
		prefix := "[Parsed_showinfo_1 @ bounded] "
		if strings.HasPrefix(name, "showinfo@") {
			prefix = "[" + name + " @ bounded] "
		}
		_, _ = fmt.Fprintln(&out, prefix+d.config[name])
		for i, record := range records {
			_, _ = fmt.Fprintf(
				&out,
				"%sn: %d pts: %d pts_time:%.9f duration:%d fmt:%s cl:%s sar:%d/%d s:%dx%d i:%s type:%s checksum:%s plane_checksum:[%s]\n",
				prefix,
				i,
				record.PTS,
				record.TimestampSeconds,
				record.DurationTicks,
				record.PixelFormat,
				record.ChromaLocation,
				record.SARNumerator,
				record.SARDenominator,
				record.Width,
				record.Height,
				record.Interlace,
				record.PictureType,
				record.PictureChecksum,
				record.PlaneChecksums,
			)
			if record.SideDataChecksum != "" {
				_, _ = fmt.Fprintln(&out, prefix+"comparison_side_data_sha256:"+record.SideDataChecksum)
			}
			if colour := strings.Split(record.Colour, "/"); len(colour) == 4 {
				_, _ = fmt.Fprintf(
					&out,
					"%scolor_range:%s color_space:%s color_primaries:%s color_trc:%s\n",
					prefix,
					colour[0],
					colour[1],
					colour[2],
					colour[3],
				)
			}
		}
	}
	return out.Bytes(), nil
}

func (r finiteComparisonRunner) Run(ctx context.Context, name string, args []string, dir string) (CommandResult, error) {
	runCtx, deadlineCancel := context.WithTimeout(ctx, 60*time.Second)
	defer deadlineCancel()
	runCtx, cancelCause := context.WithCancelCause(runCtx)
	defer cancelCause(nil)
	stream, _ := ctx.Value(comparisonRasterStreamKey{}).(*comparisonRasterStream)
	// Preserve existing injected runners for deterministic decoder-failure tests.
	if _, native := r.base.(commandRunner); !native {
		result, err := r.base.Run(runCtx, name, args, dir)
		err = comparisonProcessFailure(err, result.ExitCode)
		if stream == nil && len(result.Stdout) > r.outputLimit {
			return CommandResult{}, comparisonDecoderLimit("decoder_output_limit")
		}
		diagnostic := &comparisonDiagnosticStream{
			stream:         stream,
			retentionLimit: r.retentionLimit,
			hardwareDecode: slices.Contains(args, "-hwaccel"),
		}
		if stream != nil {
			if streamErr := consumeInjectedAnalysis(runCtx, stream, result, diagnostic); streamErr != nil {
				if comparisonTerminalFailure(err) {
					return CommandResult{}, err
				}
				return CommandResult{}, streamErr
			}
			result.Stdout = nil
			result.SparseOutput = nil
			result.Stderr, err = diagnosticResult(diagnostic, err)
			return result, err
		}
		if _, parseErr := diagnostic.Write(result.Stderr); parseErr != nil {
			return CommandResult{}, parseErr
		}
		result.Stderr, err = diagnosticResult(diagnostic, err)
		return result, err
	}
	if stream != nil {
		return r.runAnalysis(runCtx, name, args, dir, stream)
	}
	// #nosec G204 G702 -- Resolved decoder and structured arguments; no shell.
	cmd := exec.CommandContext(runCtx, name, append([]string{"-nostdin", "-nostats"}, args...)...)
	cmd.Dir = dir
	cmd.WaitDelay = 5 * time.Second
	output := &comparisonOutputBuffer{limit: r.outputLimit}
	output.cancel = func() { cancelCause(output.err) }
	inputURL, _ := ctx.Value(comparisonInputURLKey{}).(string)
	diagnostic := &comparisonDiagnosticStream{
		retentionLimit: r.retentionLimit,
		stream:         stream,
		inputURL:       inputURL,
		hardwareDecode: slices.Contains(args, "-hwaccel"),
	}
	diagnostic.cancel = func() { cancelCause(diagnostic.err) }
	diagnostic.firstNative, _ = ctx.Value(comparisonFirstNativeKey{}).(func())
	cmd.Stdout = output
	cmd.Stderr = diagnostic
	if progress, ok := ctx.Value(comparisonSampleProgressKey{}).(*comparisonSampleProgress); ok {
		cmd.Stderr = io.MultiWriter(diagnostic, progress)
	}
	err := cmd.Start()
	if err == nil {
		recordComparisonProcessStart(ctx)
		err = cmd.Wait()
	}
	cause := context.Cause(runCtx)
	if comparisonTerminalFailure(cause) {
		return CommandResult{}, fmt.Errorf("comparison process failed: %w", cause)
	}
	if comparisonTerminalFailure(diagnostic.err) {
		return CommandResult{}, diagnostic.err
	}
	if comparisonTerminalFailure(output.err) {
		return CommandResult{}, output.err
	}
	if cause != nil {
		return CommandResult{}, fmt.Errorf("comparison process interrupted: %w", cause)
	}
	stderr, parseErr := diagnostic.result()
	if parseErr != nil {
		return CommandResult{}, parseErr
	}
	if runCtx.Err() != nil {
		return CommandResult{}, fmt.Errorf("comparison decoder deadline or cancellation: %w", context.Cause(runCtx))
	}
	result := CommandResult{Stdout: output.buffer.Bytes(), Stderr: stderr}
	if err != nil {
		result.ExitCode = 1
		return result, fmt.Errorf("run comparison decoder: %w", comparisonProcessFailure(err, result.ExitCode))
	}
	return result, nil
}

// Observed evidence, source and admission failures fail closed even when the
// caller also cancels. A killed worker or unavailable device can be teardown.
func comparisonTerminalFailure(err error) bool {
	return err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) &&
		!errors.Is(err, ErrComparisonWorkerFailed) && !errors.Is(err, errComparisonHardwareUnavailable) &&
		!errors.Is(err, errComparisonHardwareInitialization)
}

// Only process errors receive the worker classification. Diagnostic and raster
// validation above return their original errors, even when they stop the child.
func comparisonProcessFailure(err error, exitCode int) error {
	if errors.Is(err, errComparisonHardwareUnavailable) {
		return err
	}
	if err == nil {
		if exitCode != 0 {
			return ErrComparisonWorkerFailed
		}
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, internalerrors.ErrFrameCorruption) ||
		errors.Is(err, errComparisonDecoderLimit) || errors.Is(err, ErrComparisonFastInconclusive) ||
		errors.Is(err, ErrComparisonNormalizationUnavailable) || errors.Is(err, ErrComparisonWorkerFailed) {
		return err
	}
	if _, invalid := api.AsOperationFailure(err); invalid {
		return err
	}
	return fmt.Errorf("%w: %w", ErrComparisonWorkerFailed, err)
}

func diagnosticResult(diagnostic *comparisonDiagnosticStream, runErr error) ([]byte, error) {
	data, err := diagnostic.result()
	if err != nil {
		return nil, err
	}
	return data, runErr
}
