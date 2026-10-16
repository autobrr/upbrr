// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"strings"
	"time"

	dovi "github.com/Audionut/go-dovi-extract"

	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

// DecoderIdentity binds measurements to the actual probed executable build and
// calibrated conversion profile. It contains no local executable or media path.
type DecoderIdentity struct {
	Version          string `json:"version"`
	Fingerprint      string `json:"fingerprint"`
	RequestedThreads int    `json:"requestedThreads,omitempty"`
	EffectiveThreads []int  `json:"effectiveThreads,omitempty"`
}

// CurrentDecoderFingerprint checks build identity without opening source media.
func CurrentDecoderFingerprint(ctx context.Context) (string, error) {
	executable, err := screenshots.ResolveFFmpegExecutable()
	if err != nil {
		return "", fmt.Errorf("resolve HDR decoder identity: %w", err)
	}
	version, err := (pictureDecoder{executable: executable}).probe(ctx, nil, "-hide_banner", "-version")
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(version)
	return hex.EncodeToString(digest[:]), nil
}

func (d pictureDecoder) probe(ctx context.Context, input []byte, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := d.command
	if command == nil {
		command = exec.CommandContext
	}
	cmd := command(ctx, d.executable, args...)
	cmd.WaitDelay = 5 * time.Second
	cmd.Stdin = bytes.NewReader(input)
	var output bytes.Buffer
	cmd.Stdout = &boundedWriter{output: &output, remaining: 64 << 10}
	cmd.Stderr = &boundedWriter{output: io.Discard, remaining: 32 << 10}
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("probe bounded HDR decoder: %w", err)
	}
	return output.Bytes(), nil
}

func (d pictureDecoder) capabilities(ctx context.Context, rgb bool) (DecoderIdentity, error) {
	var identity DecoderIdentity
	version, err := d.probe(ctx, nil, "-hide_banner", "-version")
	if err != nil {
		return identity, err
	}
	filters, err := d.probe(ctx, nil, "-hide_banner", "-filters")
	if err != nil {
		return identity, err
	}
	formats, err := d.probe(ctx, nil, "-hide_banner", "-pix_fmts")
	if err != nil {
		return identity, err
	}
	if !bytes.Contains(filters, []byte(" zscale ")) || !bytes.Contains(filters, []byte(" showinfo ")) ||
		!bytes.Contains(formats, []byte("gray16le")) || rgb && (!bytes.Contains(filters, []byte(" hstack ")) || !bytes.Contains(formats, []byte("gbrpf32le"))) {
		return identity, errDecodedMapping
	}
	if err := d.calibrateConversion(ctx, rgb); err != nil {
		return identity, err
	}
	first, _, _ := strings.Cut(string(version), "\n")
	identity.Version = first[:min(len(first), 200)]
	digest := sha256.Sum256(version)
	identity.Fingerprint = hex.EncodeToString(digest[:])
	return identity, nil
}

func (d pictureDecoder) calibrateConversion(ctx context.Context, rgb bool) error {
	// The neutral-chroma ramp uses exact 16-bit limited-range code values. The
	// independently tested ST 2084 helper supplies the absolute luminance oracle.
	codes := []uint16{4096, 5000, 12000, 25000, 32500, 46000, 55000, 60160}
	const width, height = 8, 2
	input := make([]byte, width*height*3*2)
	for i := range width * height {
		binary.LittleEndian.PutUint16(input[i*2:], codes[i%width])
		binary.LittleEndian.PutUint16(input[(width*height+i)*2:], 32768)
		binary.LittleEndian.PutUint16(input[(2*width*height+i)*2:], 32768)
	}
	format := "gray16le"
	graph := "[0:v]" + decodedColourFilter(api.HDRDecodedSourceEvidence{Range: "limited"}, false) + "[out]"
	if rgb {
		format = "gbrpf32le"
		filter := decodedColourFilter(api.HDRDecodedSourceEvidence{Range: "limited"}, true)
		graph = "[0:v]split[a][b];[a]" + filter + "[ra];[b]hflip," + filter + "[rb];[ra][rb]hstack=inputs=2,format=gbrpf32le[out]"
	}
	output, err := d.probe(ctx, input, "-hide_banner", "-nostdin", "-loglevel", "error", "-filter_complex_threads", "1",
		"-f", "rawvideo", "-pixel_format", "yuv444p16le", "-video_size", "8x2", "-framerate", "1", "-i", "pipe:0",
		"-filter_complex", graph, "-map", "[out]", "-frames:v", "1", "-c:v", "rawvideo", "-threads:v", "1", "-pix_fmt", format, "-f", "rawvideo", "pipe:1")
	if err != nil {
		return fmt.Errorf("calibrate HDR conversion: %w", err)
	}
	if !rgb {
		if len(output) != width*height*2 {
			return errDecodedOutput
		}
		for i := range width * height {
			want := float64(codes[i%width]-4096) / 56064
			got := float64(binary.LittleEndian.Uint16(output[i*2:])) / 65535
			if math.Abs(got-want) > 2.0/65535 {
				return errors.New("HDR luma normalization failed calibration")
			}
		}
		return nil
	}
	if len(output) != 2*width*height*3*4 {
		return errDecodedOutput
	}
	for plane := range 3 {
		for row := range height {
			for col := range 2 * width {
				codeIndex := col
				if col >= width {
					codeIndex = 2*width - 1 - col
				}
				want, err := dovi.PQToNits(float64(codes[codeIndex]-4096) / 56064)
				if err != nil {
					return fmt.Errorf("calculate calibration oracle: %w", err)
				}
				offset := (plane*2*width*height + row*2*width + col) * 4
				got := float64(math.Float32frombits(binary.LittleEndian.Uint32(output[offset:])))
				if math.IsNaN(got) || math.IsInf(got, 0) || math.Abs(got-want) > max(0.01, want*0.005) {
					return errors.New("HDR absolute-light conversion failed calibration")
				}
			}
		}
	}
	return nil
}
