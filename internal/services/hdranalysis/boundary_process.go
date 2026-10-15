// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func boundaryMilliseconds(stamp pictureTime) (int64, error) {
	if !stamp.Valid || stamp.Timescale == 0 || stamp.Value < 0 {
		return 0, errDecodedMapping
	}
	value := stamp.rational().Mul(stamp.rational(), big.NewRat(1000, 1))
	if !value.IsInt() || !value.Num().IsInt64() {
		return 0, errDecodedMapping
	}
	return value.Num().Int64(), nil
}

func boundarySecondsArgument(milliseconds int64) string {
	return strconv.FormatInt(milliseconds/1000, 10) + "." + fmt.Sprintf("%03d", milliseconds%1000)
}

// prepareBoundaryClip never asks FFmpeg to probe or decode source pictures.
// A byte-limited receiver and the producer deadline precede complete syntax validation.
func (d pictureDecoder) prepareBoundaryClip(
	ctx context.Context,
	input DecodedInput,
	plan *BoundaryPlan,
	window BoundaryWindow,
	directory string,
) (string, boundaryClipProof, error) {
	var proof boundaryClipProof
	start, err := boundaryMilliseconds(plan.times[window.First])
	if err != nil {
		return "", proof, err
	}
	stopIndex := min(len(plan.times)-1, window.Last+17)
	stop, err := boundaryMilliseconds(plan.times[stopIndex])
	if err != nil {
		return "", proof, err
	}
	if stopIndex == window.Last {
		if stop > int64(^uint64(0)>>1)-1000 {
			return "", proof, errDecodedMapping
		}
		// A final-picture duration is not a timestamp authority. Allow EOF to
		// finish the finite remux; packet/byte caps still bound the producer.
		stop += 1000
	}
	file, err := os.CreateTemp(directory, ".hdr-boundary-*.mkv")
	if err != nil {
		return "", proof, fmt.Errorf("create finite HDR clip: %w", err)
	}
	path := file.Name()
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	runCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	args := []string{"-hide_banner", "-nostdin", "-nostats", "-loglevel", "error", "-copyts", "-seek_timestamp", "1", "-ss", boundarySecondsArgument(start),
		"-noaccurate_seek", "-nofind_stream_info", "-fflags", "+nofillin", "-i", input.Path, "-map", fmt.Sprintf("0:V:%d", input.Source.VideoStreamOrdinal),
		"-an", "-sn", "-dn", "-c:v", "copy", "-frames:v", "512", "-to", boundarySecondsArgument(stop), "-bsf:v", "setts=pts=PTS:dts=N-513",
		"-avoid_negative_ts", "disabled", "-f", "matroska", "-live", "1", "pipe:1"}
	command := d.command
	if command == nil {
		command = exec.CommandContext
	}
	cmd := command(runCtx, d.executable, args...)
	cmd.WaitDelay = 5 * time.Second
	digest := sha256.New()
	encoded := &boundedWriter{output: io.MultiWriter(file, digest), remaining: 64 << 20}
	cmd.Stdout = encoded
	cmd.Stderr = &boundedWriter{output: io.Discard, remaining: 64 << 10}
	if err := cmd.Run(); err != nil {
		return "", proof, fmt.Errorf("prepare finite HDR clip: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", proof, fmt.Errorf("close finite HDR clip: %w", err)
	}
	proof, err = validateBoundaryClip(runCtx, path)
	if err != nil {
		return "", proof, fmt.Errorf("validate finite HDR clip: %w", err)
	}
	proof.sha256, proof.bytes = hex.EncodeToString(digest.Sum(nil)), (64<<20)-encoded.remaining
	if int(proof.geometry.Width) != input.Source.Width || int(proof.geometry.Height) != input.Source.Height ||
		int(proof.geometry.BitDepth) != input.Source.BitDepth {
		return "", proof, errDecodedMapping
	}
	for index := window.First; index <= window.Last; index++ {
		wanted := plan.times[index].rational()
		matches := 0
		for _, stamp := range proof.timestamps {
			if _, err := boundaryMilliseconds(pictureTime{
				Value:     stamp.Value,
				Timescale: stamp.Timescale,
				Valid:     stamp.Valid,
			}); err != nil {
				return "", proof, errDecodedMapping
			}
			if (pictureTime{
				Value:     stamp.Value,
				Timescale: stamp.Timescale,
				Valid:     true,
			}).rational().Cmp(wanted) == 0 {
				matches++
			}
		}
		if matches != 1 {
			return "", proof, errDecodedMapping
		}
	}
	ok = true
	return path, proof, nil
}

type boundaryDiagnostic struct {
	tail      []byte
	buffer    []byte
	source    api.HDRDecodedSourceEvidence
	base      uint64
	numerator uint64
	stamps    []pictureTime
	pending   bool
	err       error
}

func (d *boundaryDiagnostic) Write(data []byte) (int, error) {
	count := len(data)
	for len(data) > 0 {
		end := bytes.IndexByte(data, '\n')
		if end < 0 {
			end = len(data)
		}
		if len(d.buffer)+end > 8192 {
			d.err = errDecodedOutput
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
			return 0, d.err
		}
	}
	return count, nil
}

func (d *boundaryDiagnostic) line(line string) {
	if len(d.tail)+len(line)+1 > 32<<10 {
		d.tail = d.tail[:0]
	}
	d.tail = append(d.tail, line...)
	d.tail = append(d.tail, '\n')
	if strings.Contains(line, "unknown side data") || strings.Contains(line, "side data - type ") ||
		strings.Contains(line, "side data - Film grain parameters") {
		d.err = fmt.Errorf("boundary side data: %w", errDecodedMapping)
		return
	}
	if !strings.HasPrefix(line, "[showinfo@boundary @ ") {
		return
	}
	if match := showTimeBase.FindStringSubmatch(line); match != nil {
		n, e1 := strconv.ParseUint(match[1], 10, 32)
		base, e2 := strconv.ParseUint(match[2], 10, 32)
		if e1 != nil || e2 != nil || n != 1 || base != 1000 || d.base != 0 {
			d.err = fmt.Errorf("boundary time base: %w", errDecodedMapping)
			return
		}
		d.numerator, d.base = n, base
		return
	}
	if match := showPicture.FindStringSubmatch(line); match != nil {
		index, e1 := strconv.Atoi(match[1])
		pts, e2 := strconv.ParseInt(match[2], 10, 64)
		width, e3 := strconv.Atoi(match[6])
		height, e4 := strconv.Atoi(match[7])
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil || index != len(d.stamps) || index >= 512 || d.pending || d.base != 1000 ||
			width != d.source.Width || height != d.source.Height || match[3] != "yuv420p10le" || match[4] != "1" || match[5] != "1" {
			d.err = fmt.Errorf("boundary frame geometry/order: %w", errDecodedMapping)
			return
		}
		d.stamps = append(d.stamps, pictureTime{
			Value:     pts,
			Timescale: d.base,
			Valid:     true,
		})
		d.pending = true
		return
	}
	if match := showColour.FindStringSubmatch(line); match != nil {
		want := "tv"
		if d.source.Range == "full" {
			want = "pc"
		}
		if !d.pending || match[1] != want || match[2] != "bt2020nc" || match[3] != "bt2020" || match[4] != "smpte2084" {
			d.err = fmt.Errorf("boundary colour proof: %w", errDecodedMapping)
		}
		d.pending = false
		return
	}
	if strings.Contains(line, " n:") || strings.Contains(line, "color_range:") || strings.Contains(line, "config in time_base:") {
		d.err = fmt.Errorf("boundary diagnostic syntax: %w", errDecodedMapping)
	}
}

func (d *boundaryDiagnostic) finish(expected []pictureTime, vcl int) error {
	if len(d.buffer) > 0 {
		d.line(string(d.buffer))
	}
	if d.err != nil || d.pending || len(d.stamps) == 0 || len(d.stamps) > vcl {
		return errors.Join(errDecodedMapping, d.err)
	}
	for _, wanted := range expected {
		matches := 0
		for _, stamp := range d.stamps {
			if stamp.rational().Cmp(wanted.rational()) == 0 {
				matches++
			}
		}
		if matches != 1 {
			return errDecodedMapping
		}
	}
	for i := 1; i < len(d.stamps); i++ {
		if d.stamps[i].rational().Cmp(d.stamps[i-1].rational()) <= 0 {
			return errDecodedMapping
		}
	}
	return nil
}
