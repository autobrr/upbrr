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
	"strings"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

var errComparisonDecoderLimit = errors.New("comparison decoder limit exceeded")

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
	outputLimit int
}

type comparisonOutputBuffer struct {
	bytes.Buffer
	limit  int
	err    error
	cancel context.CancelFunc
}

func (b *comparisonOutputBuffer) Write(data []byte) (int, error) {
	if len(data) > b.limit-b.Len() {
		b.err = comparisonDecoderLimit("decoder_output_limit")
		if b.cancel != nil {
			b.cancel()
		}
		return 0, b.err
	}
	_, _ = b.Buffer.Write(data)
	return len(data), nil
}

type comparisonDiagnosticStream struct {
	buffer, tail []byte
	config       map[string]string
	records      map[string][]ComparisonFrameObservation
	count        int
	err          error
	cancel       context.CancelFunc
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
	if line == "" {
		return
	}
	if err := ffmpegFrameCorruptionError([]byte(line)); err != nil {
		d.err = err
		return
	}
	if comparisonShowInfoLine.MatchString(line) || comparisonOutputShowInfoLine.MatchString(line) {
		prefix, body, ok := strings.Cut(line, "]")
		if ok {
			body = strings.TrimSpace(body)
			name := strings.Fields(strings.TrimPrefix(prefix, "["))[0]
			if d.config == nil {
				d.config = map[string]string{}
				d.records = map[string][]ComparisonFrameObservation{}
			}
			if strings.HasPrefix(body, "config in time_base:") {
				d.config[name] = body
				return
			}
			if strings.HasPrefix(body, "n:") {
				if len(d.records[name]) >= 50000 {
					d.err = comparisonDecoderLimit("decoder_observation_limit")
					return
				}
				text := "[Parsed_showinfo_1 @ bounded] " + d.config[name] + "\n[Parsed_showinfo_1 @ bounded] " + body + "\n"
				observed, err := comparisonObservations([]byte(text))
				if err != nil || len(observed) != 1 {
					d.err = errors.New("comparison decoder provenance is incomplete")
					return
				}
				d.records[name] = append(d.records[name], observed[0])
				d.count++
				if d.count > 100000 {
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
		if name == "showinfo@comparison_output" {
			prefix = "[showinfo@comparison_output @ bounded] "
		}
		_, _ = fmt.Fprintln(&out, prefix+d.config[name])
		for i, record := range records {
			_, _ = fmt.Fprintf(
				&out,
				"%sn: %d pts: %d pts_time:%.9f fmt:%s sar:%d/%d s:%dx%d i:%s type:%s checksum:%s\n",
				prefix,
				i,
				record.PTS,
				record.TimestampSeconds,
				record.PixelFormat,
				record.SARNumerator,
				record.SARDenominator,
				record.Width,
				record.Height,
				record.Interlace,
				record.PictureType,
				record.PictureChecksum,
			)
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
	runCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	// Preserve existing injected runners for deterministic decoder-failure tests.
	if _, native := r.base.(commandRunner); !native {
		result, err := r.base.Run(runCtx, name, args, dir)
		if len(result.Stdout) > r.outputLimit {
			return CommandResult{}, comparisonDecoderLimit("decoder_output_limit")
		}
		diagnostic := &comparisonDiagnosticStream{}
		if _, parseErr := diagnostic.Write(result.Stderr); parseErr != nil {
			return CommandResult{}, parseErr
		}
		result.Stderr, err = diagnosticResult(diagnostic, err)
		return result, err
	}
	// The resolved executable and argument vector are server-owned; no shell is used.
	cmd := exec.CommandContext(runCtx, name, append([]string{"-nostdin", "-nostats"}, args...)...) // #nosec G204
	cmd.Dir = dir
	cmd.WaitDelay = 5 * time.Second
	output := &comparisonOutputBuffer{limit: r.outputLimit, cancel: cancel}
	diagnostic := &comparisonDiagnosticStream{cancel: cancel}
	cmd.Stdout = output
	cmd.Stderr = diagnostic
	if progress, ok := ctx.Value(comparisonSampleProgressKey{}).(*comparisonSampleProgress); ok {
		cmd.Stderr = io.MultiWriter(diagnostic, progress)
	}
	err := cmd.Run()
	if output.err != nil {
		return CommandResult{}, output.err
	}
	stderr, parseErr := diagnostic.result()
	if parseErr != nil {
		return CommandResult{}, parseErr
	}
	if runCtx.Err() != nil {
		return CommandResult{}, fmt.Errorf("comparison decoder deadline or cancellation: %w", runCtx.Err())
	}
	result := CommandResult{Stdout: output.Bytes(), Stderr: stderr}
	if err != nil {
		result.ExitCode = 1
		return result, fmt.Errorf("run comparison decoder: %w", err)
	}
	return result, nil
}

func diagnosticResult(diagnostic *comparisonDiagnosticStream, runErr error) ([]byte, error) {
	data, err := diagnostic.result()
	if err != nil {
		return nil, err
	}
	return data, runErr
}
