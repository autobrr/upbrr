// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

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

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

var errComparisonDecoderLimit = errors.New("comparison decoder limit exceeded")
var errComparisonHardwareUnavailable = errors.New("comparison hardware decoder unavailable")
var errComparisonHardwareUnsupported = errors.New("comparison hardware decoder unsupported")

var comparisonHardwareDiagnostic = regexp.MustCompile(
	`(?i)^\[(?:AVHW(?:Device|Frames)Context|CUDA|(?:vist|dec)#\d+:\d+/[a-z0-9_]+|(?:h264|hevc|av1|vp[89]|mpeg[124]video|mpeg4|vc1)(?:_[a-z0-9]+)?|(?:Parsed_)?hwdownload(?:_\d+)?) @ [^\]]+\] `,
)

var comparisonHardwareFailureCode = regexp.MustCompile(
	`(?i)\b(?:CUDA_ERROR_(?:NO_DEVICE|DEVICE_UNAVAILABLE|OUT_OF_MEMORY|CONTEXT_IS_DESTROYED|INVALID_CONTEXT)|DXGI_ERROR_DEVICE_(?:REMOVED|RESET|HUNG)|(?:0x)?887a000[567]|VA_STATUS_ERROR_(?:INVALID_DISPLAY|UNSUPPORTED_PROFILE)|kVT(?:VideoDecoder(?:MalfunctionErr|NotAvailableNowErr)|CouldNotFindVideoDecoderErr))\b`,
)
var comparisonHardwareUnsupportedDiagnostic = regexp.MustCompile(
	`(?i)\b(?:cannot load (?:libcuda\.so(?:\.1)?|nvcuda\.dll)|hardware is lacking required capabilities|video (?:width|height|macroblock count) .* (?:not within range|exceeds maximum)|invalid output format .* for hwframe download)\b`,
)
var comparisonHardwareSetupFailure = regexp.MustCompile(
	`(?i)\b(?:no device available for decoder|failed setup for format (?:cuda|d3d11|vaapi|videotoolbox_vld): hwaccel initialisation returned error)\b`,
)

var comparisonVideoToolboxFailure = regexp.MustCompile(`Failed to decode frame \((?:decoder malfunction|invalid session), -?\d+\)`)

var comparisonVAAPITransferFailure = regexp.MustCompile(
	`Failed to (?:sync surface|read image from surface|map image from surface) (?:0x)?[0-9a-fA-F]+: (\d+) \(`,
)

var comparisonHardwareDeviceCreationFailure = regexp.MustCompile(`^Device creation failed: -\d+\.?$`)

// A codec/profile can accept -hwaccel but produce a software surface. An
// initial negotiation failure at the graph input cannot feed hwdownload.
var comparisonHardwareInputFormatFailure = regexp.MustCompile(
	`^Impossible to convert between the formats supported by the filter 'graph -?\d+ input from stream \d+:\d+' and the filter 'auto_scale_\d+'$`,
)

func comparisonHardwareStageFailure(line string) error {
	if comparisonHardwareDeviceCreationFailure.MatchString(line) {
		return errComparisonHardwareUnavailable
	}
	if !comparisonHardwareDiagnostic.MatchString(line) {
		return nil
	}
	if comparisonHardwareUnsupportedDiagnostic.MatchString(line) {
		return errComparisonHardwareUnsupported
	}
	if comparisonHardwareFailureCode.MatchString(line) || comparisonHardwareSetupFailure.MatchString(line) || comparisonVideoToolboxFailure.MatchString(line) {
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
	base        Runner
	input       []byte
	outputLimit int
	retain      func(int64) bool
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

// FFmpeg can interleave a demux-thread message with a partially printed
// filter diagnostic. Recognize our two tagged integers even inside that line.
// Binary-exact quarter fractions distinguish offsets from signed ticks. A
// negative sign separates either value from an interrupted diagnostic field.
const comparisonPacketFilter = "noise=amount=0:drop='if(eq(pts,nopts),0,print(-pos-1.25);print(-2*abs(pts)-lt(pts,0)-0.75));0'"

var comparisonPacketMessage = regexp.MustCompile(`(?:\[Eval @ [^\]]+\] )?-(\d+)\.(250000|750000)$`)

type comparisonPacketTrace struct {
	positions map[int64]int64
	position  *int64
	fragments map[string]string
}

// consume removes only the private packet message. A diagnostic interrupted
// before its newline is rejoined when FFmpeg resumes that filter's output.
func (p *comparisonPacketTrace) consume(line string) (string, error) {
	var token []int
	if strings.HasSuffix(line, ".250000") || strings.HasSuffix(line, ".750000") {
		token = comparisonPacketMessage.FindStringSubmatchIndex(line)
	}
	if token != nil {
		v, err := strconv.ParseInt(line[token[2]:token[3]], 10, 64)
		if err != nil {
			return "", comparisonDiscFailure("packet_address")
		}
		if line[token[4]] == '2' {
			// An unset packet PTS emits no tagged integer. A fresh position
			// supersedes the incomplete address without binding it to a picture.
			p.position = new(v - 1)
		} else {
			if p.position == nil || len(p.positions) >= 50000 {
				return "", comparisonDiscFailure("packet_address")
			}
			z := v
			pts := z / 2
			if z%2 != 0 {
				pts = -pts
			}
			pos := *p.position
			if _, exists := p.positions[pts]; exists {
				pos = -1
			}
			p.positions[pts] = pos
			p.position = nil
		}
		line = line[:token[0]]
		if prefix, body, ok := strings.Cut(line, "] "); ok {
			if len(p.fragments[prefix])+len(body) > 8192 || len(p.fragments) > 64 {
				return "", comparisonDecoderLimit("decoder_diagnostic_limit")
			}
			p.fragments[prefix] += body
		}
		return "", nil
	}
	if len(p.fragments) > 0 {
		if prefix, body, ok := strings.Cut(line, "] "); ok {
			if fragment, ok := p.fragments[prefix]; ok {
				delete(p.fragments, prefix)
				line = prefix + "] " + fragment + body
			}
		}
	}
	return line, nil
}

type comparisonSequenceBounds struct {
	first, last, numerator, denominator int64
	ticks                               []int64
	before, after                       *ComparisonFileNeighbor
}

type comparisonDiagnosticStream struct {
	bounds         *comparisonSequenceBounds
	packets        *comparisonPacketTrace
	retain         func(int64) bool
	stream         *comparisonRasterStream
	inputURL       string
	outputURL      string
	buffer, tail   []byte
	config         map[string]string
	records        map[string][]ComparisonFrameObservation
	counts         map[string]int
	firstNative    func()
	hardwareDecode bool
	nativeStarted  bool
	count          int
	err            error
	cancel         context.CancelFunc
}

func packetTraceFor(args []string) *comparisonPacketTrace {
	if !slices.Contains(args, comparisonPacketFilter) {
		return nil
	}
	return &comparisonPacketTrace{positions: map[int64]int64{}, fragments: map[string]string{}}
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
	if d.packets != nil {
		var err error
		line, err = d.packets.consume(line)
		if err != nil {
			d.err = err
			return
		}
	}
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
		if !d.nativeStarted && comparisonHardwareInputFormatFailure.MatchString(line) {
			d.err = errComparisonHardwareUnsupported
			return
		}
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
			if strings.HasPrefix(body, "n:") && (name == "showinfo@comparison_decoder_init" || !strings.HasPrefix(name, "showinfo@")) {
				d.nativeStarted = true
			}
			if name == "showinfo@comparison_decoder_init" {
				if d.firstNative != nil && strings.HasPrefix(body, "n:") {
					d.firstNative()
					d.firstNative = nil
				}
				return
			}
			if d.config == nil {
				d.config = map[string]string{}
				d.records = map[string][]ComparisonFrameObservation{}
				d.counts = map[string]int{}
			}
			if strings.HasPrefix(body, "config in time_base:") {
				d.config[name] = body
				return
			}
			if strings.HasPrefix(body, "n:") {
				if d.retain != nil && !strings.HasPrefix(name, "showinfo@") && !d.retain(int64(d.counts[name]+1)*4096) {
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
				if name == "showinfo@comparison_bounds" {
					f := observed[0]
					if d.bounds == nil {
						d.bounds = &comparisonSequenceBounds{
							first:       f.PTS,
							numerator:   f.TimeBaseNumerator,
							denominator: f.TimeBaseDenominator,
						}
					} else if f.PTS <= d.bounds.last || f.TimeBaseNumerator != d.bounds.numerator || f.TimeBaseDenominator != d.bounds.denominator {
						d.err = comparisonDiscFailure("ambiguous_native_pts")
						return
					}
					d.bounds.ticks = append(d.bounds.ticks, f.PTS)
					d.bounds.last = f.PTS
					d.counts[name]++
					return
				}
				if d.stream != nil {
					if name == "showinfo@comparison_motion" || name == "showinfo@comparison_stats" {
						if fields := comparisonChecksum.FindStringSubmatch(body); len(fields) == 2 {
							observed[0].transportChecksum = fields[1]
						}
					}
					if err := d.stream.observe(name, observed[0]); err != nil {
						d.err = err
						return
					}
				}
				// Streamed branch/output records have already bound their raster.
				// Retain only native provenance for publication, avoiding a second
				// movie-sized collection and diagnostic reconstruction of branches.
				if d.stream == nil || !strings.HasPrefix(name, "showinfo@") {
					d.records[name] = append(d.records[name], observed[0])
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
		if d.bounds != nil && d.packets != nil && !strings.HasPrefix(name, "showinfo@") && len(records) > 0 {
			left, ok := slices.BinarySearch(d.bounds.ticks, records[0].PTS)
			right, endOK := slices.BinarySearch(d.bounds.ticks, records[len(records)-1].PTS)
			if !ok || !endOK {
				return nil, comparisonDiscFailure("original_clock_witness")
			}
			neighbor := func(index int) *ComparisonFileNeighbor {
				if index < 0 || index >= len(d.bounds.ticks) {
					return nil
				}
				pts := d.bounds.ticks[index]
				if pos, ok := d.packets.positions[pts]; ok && pos >= 0 {
					return &ComparisonFileNeighbor{PTS: pts, Position: pos}
				}
				return nil
			}
			d.bounds.before, d.bounds.after = neighbor(left-1), neighbor(right+1)
		}
		for i, record := range records {
			_, _ = fmt.Fprintf(
				&out,
				"%sn: %d pts: %d pts_time:%.9f duration:%d fmt:%s cl:%s sar:%d/%d s:%dx%d i:%s type:%s\n",
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
			)
			if !strings.HasPrefix(name, "showinfo@") && d.packets != nil {
				position, ok := d.packets.positions[record.PTS]
				if !ok || position < 0 {
					return nil, comparisonDiscFailure("ambiguous_packet_address")
				}
				_, _ = fmt.Fprintf(&out, "%scomparison_packet_pos:%d\n", prefix, position)
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
			retain:         r.retain,
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
	if len(r.input) > 0 {
		cmd.Stdin = bytes.NewReader(r.input)
	}
	cmd.WaitDelay = 5 * time.Second
	output := &comparisonOutputBuffer{limit: r.outputLimit}
	output.cancel = func() { cancelCause(output.err) }
	inputURL, _ := ctx.Value(comparisonInputURLKey{}).(string)
	diagnostic := &comparisonDiagnosticStream{
		retain:         r.retain,
		stream:         stream,
		inputURL:       inputURL,
		packets:        packetTraceFor(args),
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
		if len(r.input) == 0 {
			recordComparisonProcessStart(ctx)
		}
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
	result := CommandResult{
		Stdout: output.buffer.Bytes(),
		Stderr: stderr,
		bounds: diagnostic.bounds,
	}
	if err != nil {
		result.ExitCode = 1
		return result, fmt.Errorf("run comparison decoder: %w", comparisonProcessFailure(err, result.ExitCode))
	}
	return result, nil
}

// Observed evidence, source and admission failures fail closed even when the
// caller also cancels. A killed worker or unavailable device can be teardown.
func comparisonTerminalFailure(err error) bool {
	if errors.Is(err, internalerrors.ErrFrameCorruption) || errors.Is(err, errComparisonDecoderLimit) {
		return true
	}
	if _, invalid := api.AsOperationFailure(err); invalid {
		return true
	}
	return err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) &&
		!errors.Is(err, ErrComparisonWorkerFailed) && !errors.Is(err, errComparisonHardwareUnavailable) &&
		!errors.Is(err, errComparisonHardwareInitialization) && !errors.Is(err, errComparisonHardwareUnsupported)
}

// Only process errors receive the worker classification. Diagnostic and raster
// validation above return their original errors, even when they stop the child.
func comparisonProcessFailure(err error, exitCode int) error {
	if errors.Is(err, errComparisonHardwareUnavailable) || errors.Is(err, errComparisonHardwareUnsupported) {
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
