// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

var errDecodedOutput = errors.New("decoded HDR output is incomplete or invalid")

type pictureDecoder struct {
	executable string
	command    func(context.Context, string, ...string) *exec.Cmd
}

type pictureDecodeRequest struct {
	inputs   []DecodedInput
	times    [][]pictureTime
	selected [][]int
	crop     *api.HDRCrop
	threads  int
}

func (r pictureDecodeRequest) geometry() (int, error) {
	if len(r.inputs) < 1 || len(r.inputs) > 2 || len(r.inputs) != len(r.times) || len(r.inputs) != len(r.selected) {
		return 0, errDecodedMapping
	}
	width, height, bytesPerPixel := r.inputs[0].Source.Width, r.inputs[0].Source.Height, 2
	for i, input := range r.inputs {
		if len(r.selected[i]) == 0 || len(r.selected[i]) > 600 || len(r.times[i]) == 0 || len(r.times[i]) > MaxFrames ||
			input.Source.Width != width || input.Source.Height != height || len(r.selected[i]) != len(r.selected[0]) {
			return 0, errDecodedMapping
		}
		for j, selected := range r.selected[i] {
			if selected < 0 || selected >= len(r.times[i]) || j > 0 && selected <= r.selected[i][j-1] {
				return 0, errDecodedMapping
			}
		}
	}
	if len(r.inputs) == 2 {
		c := r.crop
		if c == nil || c.Left < 0 || c.Top < 0 || c.Width < 2 || c.Height < 2 || c.Left > width-c.Width || c.Top > height-c.Height ||
			c.Width%2 != 0 || c.Height%2 != 0 || len(r.selected[0]) > 120 || r.inputs[0].Source.RawHEVC || r.inputs[1].Source.RawHEVC {
			return 0, errDecodedMapping
		}
		width, height, bytesPerPixel = 2*c.Width, c.Height, 12
	}
	if width < 2 || width > 16384 || height < 2 || height > 4320 || int64(width)*int64(height)*int64(bytesPerPixel) > 256<<20 {
		return 0, errDecodedMapping
	}
	return width * height * bytesPerPixel, nil
}

func decodedColourFilter(source api.HDRDecodedSourceEvidence, rgb bool) string {
	filter := "zscale=rangein=" + source.Range + ":matrixin=2020_ncl:transferin=smpte2084:primariesin=2020:range=full:primaries=2020"
	if rgb {
		return filter + ":transfer=linear:matrix=gbr:npl=1,format=gbrpf32le"
	}
	return filter + ":transfer=smpte2084:matrix=2020_ncl,format=yuv444p16le,extractplanes=y"
}

func pictureDecodeArgs(r pictureDecodeRequest) []string {
	args := []string{
		"-hide_banner",
		"-nostdin",
		"-nostats",
		"-loglevel",
		"info",
		"-max_alloc",
		"268435456",
		"-copyts",
		"-filter_threads",
		"1",
		"-filter_complex_threads",
		"1",
	}
	var branches []string
	for i, input := range r.inputs {
		threads := r.threads
		if threads == 0 {
			threads = 2
		}
		args = append(args, "-threads", strconv.Itoa(threads), "-hwaccel", "none", "-noautorotate", "-apply_cropping", "0", "-i", input.Path)
		branch := fmt.Sprintf(
			"[%d:v:0]showinfo@hdr%d=checksum=0,select='%s',%s",
			i,
			i,
			pictureSelection(r.selected[i]),
			decodedColourFilter(input.Source, len(r.inputs) == 2),
		)
		if len(r.inputs) == 2 {
			c := r.crop
			branch += fmt.Sprintf(",crop=%d:%d:%d:%d,setsar=1,settb=expr=1/1,setpts=N", c.Width, c.Height, c.Left, c.Top)
		}
		branches = append(branches, fmt.Sprintf("%s[v%d]", branch, i))
	}
	format := "gray16le"
	if len(r.inputs) == 2 {
		branches = append(branches, "[v0][v1]hstack=inputs=2:shortest=1,format=gbrpf32le[out]")
		format = "gbrpf32le"
	} else {
		branches = append(branches, "[v0]null[out]")
	}
	args = append(args, "-filter_complex", strings.Join(branches, ";"), "-map", "[out]", "-an", "-sn", "-dn", "-fps_mode", "passthrough")
	if !r.inputs[0].Source.RawHEVC {
		args = append(args, "-frames:v", strconv.Itoa(len(r.selected[0])))
	}
	return append(args, "-c:v", "rawvideo", "-threads:v", "1", "-pix_fmt", format, "-f", "rawvideo", "pipe:1")
}

// FFmpeg's expression parser has a finite recursion depth. A balanced sum keeps
// the 600-frame union within that bound even when every selection is isolated.
func pictureSelection(indices []int) string {
	if len(indices) == 0 {
		return "0"
	}
	if len(indices) == 1 {
		return fmt.Sprintf("eq(n\\,%d)", indices[0])
	}
	middle := len(indices) / 2
	return "(" + pictureSelection(indices[:middle]) + "+" + pictureSelection(indices[middle:]) + ")"
}

var (
	showTimeBase = regexp.MustCompile(`config in time_base:\s*(\d+)/(\d+)`)
	showPicture  = regexp.MustCompile(`\bn:\s*(\d+)\s+pts:\s*(-?\d+|NOPTS).*?fmt:(\S+).*?sar:(\d+)/(\d+).*?s:(\d+)x(\d+)`)
	showColour   = regexp.MustCompile(`color_range:(\S+)\s+color_space:(\S+)\s+color_primaries:(\S+)\s+color_trc:(\S+)`)
)

type decodedStreamProof struct {
	numerator, denominator uint64
	seen                   int
	pending                bool
}

type decodedPipeProof struct {
	request pictureDecodeRequest
	streams []decodedStreamProof
	mu      sync.Mutex
	ready   [][]bool
	updates chan struct{}
	done    chan struct{}
	err     error
}

func newDecodedPipeProof(r pictureDecodeRequest) *decodedPipeProof {
	p := &decodedPipeProof{
		request: r,
		streams: make([]decodedStreamProof, len(r.inputs)),
		ready:   make([][]bool, len(r.inputs)),
		updates: make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
	for i := range p.ready {
		p.ready[i] = make([]bool, len(r.selected[i]))
	}
	return p
}

func (p *decodedPipeProof) line(line string) error {
	for i := range p.streams {
		if !strings.HasPrefix(line, fmt.Sprintf("[showinfo@hdr%d @ ", i)) {
			continue
		}
		s := &p.streams[i]
		if match := showTimeBase.FindStringSubmatch(line); match != nil {
			n, nerr := strconv.ParseUint(match[1], 10, 32)
			d, derr := strconv.ParseUint(match[2], 10, 32)
			if nerr != nil || derr != nil || n == 0 || d == 0 || s.numerator != 0 {
				return errDecodedMapping
			}
			s.numerator, s.denominator = n, d
			return nil
		}
		if match := showPicture.FindStringSubmatch(line); match != nil {
			n, nerr := strconv.Atoi(match[1])
			pts, ptsErr := strconv.ParseInt(match[2], 10, 64)
			width, werr := strconv.Atoi(match[6])
			height, herr := strconv.Atoi(match[7])
			input := p.request.inputs[i]
			if nerr != nil || werr != nil || herr != nil || n != s.seen || n >= len(p.request.times[i]) || s.pending || s.numerator == 0 ||
				width != input.Source.Width || height != input.Source.Height || match[4] != "1" || match[5] != "1" ||
				!strings.HasSuffix(match[3], strconv.Itoa(input.Source.BitDepth)+"le") ||
				!slices.Contains(
					[]string{
						"yuv420p10le",
						"yuv422p10le",
						"yuv444p10le",
						"yuv420p12le",
						"yuv422p12le",
						"yuv444p12le",
						"yuv420p16le",
						"yuv422p16le",
						"yuv444p16le",
					},
					match[3],
				) {
				return errDecodedMapping
			}
			if !input.Source.RawHEVC {
				// Products use arbitrary-precision rationals: a valid large PTS
				// must not overflow while proving equality across time bases.
				if ptsErr != nil {
					return errDecodedMapping
				}
				got := pictureTime{
					Value:     pts,
					Timescale: s.denominator,
					Valid:     true,
				}.rational()
				got.Mul(got, new(big.Rat).SetInt(new(big.Int).SetUint64(s.numerator)))
				if got.Cmp(p.request.times[i][n].rational()) != 0 {
					return errDecodedMapping
				}
			}
			s.seen++
			s.pending = true
			return nil
		}
		if match := showColour.FindStringSubmatch(line); match != nil {
			wantRange := "tv"
			if p.request.inputs[i].Source.Range == "full" {
				wantRange = "pc"
			}
			if !s.pending || match[1] != wantRange || match[2] != "bt2020nc" || match[3] != "bt2020" || match[4] != "smpte2084" {
				return errDecodedMapping
			}
			s.pending = false
			if selected, ok := slices.BinarySearch(p.request.selected[i], s.seen-1); ok {
				p.mu.Lock()
				p.ready[i][selected] = true
				p.mu.Unlock()
				select {
				case p.updates <- struct{}{}:
				default:
				}
			}
			return nil
		}
		if strings.Contains(line, " n:") || strings.Contains(line, "color_range:") || strings.Contains(line, "config in time_base:") {
			return errDecodedMapping
		}
	}
	return nil
}

func (p *decodedPipeProof) await(ctx context.Context, index int) error {
	finished := false
	for {
		p.mu.Lock()
		ready := true
		for _, stream := range p.ready {
			ready = ready && stream[index]
		}
		p.mu.Unlock()
		if ready {
			return nil
		}
		if finished {
			return errDecodedMapping
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("await decoded timing: %w", ctx.Err())
		case <-p.done:
			finished = true
		case <-p.updates:
		}
	}
}

func (d pictureDecoder) decode(ctx context.Context, request pictureDecodeRequest, consume func(int, []byte) error) error {
	frameBytes, err := request.geometry()
	if err != nil || consume == nil || d.executable == "" {
		return errDecodedMapping
	}
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	command := d.command
	if command == nil {
		command = exec.CommandContext
	}
	cmd := command(runCtx, d.executable, pictureDecodeArgs(request)...)
	cmd.WaitDelay = 5 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open HDR decoder output: %w", err)
	}
	defer stdout.Close()
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("open HDR decoder diagnostics: %w", err)
	}
	defer stderr.Close()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start HDR decoder: %w", err)
	}
	stop := context.AfterFunc(runCtx, func() {
		_ = stdout.Close()
		_ = stderr.Close()
		_ = cmd.Process.Kill()
	})
	defer stop()
	proof := newDecodedPipeProof(request)
	var diagnostics bytes.Buffer
	diagnostics.Grow(32 << 10)
	go func() {
		defer close(proof.done)
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 8192), 8192)
		for scanner.Scan() {
			line := scanner.Text()
			if diagnostics.Len()+len(line)+1 > 32<<10 {
				diagnostics.Reset()
			}
			diagnostics.WriteString(line)
			diagnostics.WriteByte('\n')
			if err := proof.line(line); err != nil {
				proof.err = err
				cancel()
				return
			}
		}
		if err := scanner.Err(); err != nil {
			proof.err = fmt.Errorf("read bounded decoder diagnostics: %w", err)
			cancel()
		}
	}()
	frame := make([]byte, frameBytes)
	var consumeErr error
	for i := range len(request.selected[0]) {
		if _, err := io.ReadFull(stdout, frame); err != nil {
			consumeErr = fmt.Errorf("read decoded frame: %w", errors.Join(errDecodedOutput, err))
			break
		}
		if consumeErr = proof.await(runCtx, i); consumeErr != nil {
			break
		}
		if consumeErr = consume(i, frame); consumeErr != nil {
			break
		}
	}
	if consumeErr == nil {
		var extra [1]byte
		if n, err := io.ReadFull(stdout, extra[:]); n != 0 || !errors.Is(err, io.EOF) {
			consumeErr = errDecodedOutput
		}
	}
	if consumeErr != nil {
		cancel()
	}
	<-proof.done
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return fmt.Errorf("HDR decode canceled: %w", ctx.Err())
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("HDR decode deadline: %w", runCtx.Err())
	}
	if proof.err != nil {
		return proof.err
	}
	if consumeErr != nil || waitErr != nil {
		return fmt.Errorf("HDR decode did not complete (%s): %w", diagnostics.String(), errors.Join(errDecodedOutput, consumeErr, waitErr))
	}
	for i, stream := range proof.streams {
		if stream.pending || stream.seen <= request.selected[i][len(request.selected[i])-1] ||
			request.inputs[i].Source.RawHEVC && stream.seen != len(request.times[i]) {
			return errDecodedMapping
		}
	}
	return nil
}
