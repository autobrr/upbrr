// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

func decodedProcessFixture() pictureDecodeRequest {
	input := DecodedInput{Path: "private-source", Source: api.HDRDecodedSourceEvidence{
		VideoStreams:           1,
		Codec:                  "hevc",
		Width:                  2,
		Height:                 2,
		BitDepth:               10,
		PixelAspectNumerator:   1,
		PixelAspectDenominator: 1,
		Range:                  "limited",
		Matrix:                 "bt2020nc",
		Primaries:              "bt2020",
		Transfer:               "smpte2084",
	}}
	return pictureDecodeRequest{
		inputs: []DecodedInput{input},
		times: [][]pictureTime{{
			{
				Value:     -1000,
				Timescale: 1000,
				Valid:     true,
			}, {
				Value:     0,
				Timescale: 1000,
				Valid:     true,
			}, {
				Value:     1000,
				Timescale: 1000,
				Valid:     true,
			},
		}},
		selected: [][]int{{0, 2}},
	}
}

func TestHDRDecoderChild(_ *testing.T) {
	mode := os.Getenv("UPBRR_HDR_DECODER_CHILD")
	if mode == "" {
		return
	}
	if mode == "blocked" {
		_, _ = os.Stdout.Write(make([]byte, 1<<20))
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}
	if mode == "flood" {
		for range 10000 {
			fmt.Fprintln(os.Stderr, "unrelated bounded diagnostic")
		}
	}
	if mode == "oversized" {
		_, _ = os.Stderr.Write(make([]byte, 9000))
		fmt.Fprintln(os.Stderr)
		os.Exit(0)
	}
	streams := 1
	if mode == "paired-ahead" {
		streams = 2
	}
	for stream := range streams {
		fmt.Fprintf(os.Stderr, "[showinfo@hdr%d @ child] config in time_base: 1/1000, frame_rate: 1/1\n", stream)
		for i := range 3 {
			index, pts := i, (i-1)*1000
			if mode == "duplicate" && i == 1 {
				index = 0
			}
			if mode == "wrong-pts" && i == 1 {
				pts++
			}
			fmt.Fprintf(os.Stderr, "[showinfo@hdr%d @ child] n: %d pts: %d pts_time:unused fmt:yuv420p10le sar:1/1 s:2x2\n", stream, index, pts)
			fmt.Fprintf(os.Stderr, "[showinfo@hdr%d @ child] color_range:tv color_space:bt2020nc color_primaries:bt2020 color_trc:smpte2084\n", stream)
			if streams == 1 && i != 1 {
				if mode == "truncated" && i == 2 {
					_, _ = os.Stdout.Write([]byte{0})
				} else {
					_, _ = os.Stdout.Write(make([]byte, 8))
				}
			}
		}
	}
	if streams == 2 {
		_, _ = os.Stdout.Write(make([]byte, 2*4*2*12))
	}
	if mode == "extra" {
		_, _ = os.Stdout.Write([]byte{1})
	}
	if mode == "exit-failure" {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestDecodedProcessFramingAndCancellation(t *testing.T) {
	for _, mode := range []string{"normal", "flood", "paired-ahead", "duplicate", "wrong-pts", "truncated", "oversized", "extra", "exit-failure", "blocked"} {
		t.Run(mode, func(t *testing.T) {
			request := decodedProcessFixture()
			if mode == "paired-ahead" {
				request.inputs = append(request.inputs, request.inputs[0])
				request.times = append(request.times, request.times[0])
				request.selected = append(request.selected, request.selected[0])
				request.crop = &api.HDRCrop{Width: 2, Height: 2}
			}
			decoder := pictureDecoder{executable: os.Args[0], command: func(ctx context.Context, name string, _ ...string) *exec.Cmd {
				cmd := exec.CommandContext(ctx, name, "-test.run=^TestHDRDecoderChild$")
				cmd.Env = append(os.Environ(), "UPBRR_HDR_DECODER_CHILD="+mode)
				return cmd
			}}
			ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
			defer cancel()
			if mode == "blocked" {
				time.AfterFunc(200*time.Millisecond, cancel)
			}
			count := 0
			err := decoder.decode(ctx, request, func(_ int, _ []byte) error { count++; return nil })
			if mode == "normal" || mode == "flood" || mode == "paired-ahead" {
				if err != nil || count != 2 {
					t.Fatalf("valid process frames=%d err=%v", count, err)
				}
			} else if err == nil {
				t.Fatal("invalid or canceled process completed")
			}
		})
	}
}

func TestDecodedTimingUsesExactLargeRationals(t *testing.T) {
	request := decodedProcessFixture()
	request.selected = [][]int{{0}}
	request.times = [][]pictureTime{{{
		Value:     -9007199254740994,
		Timescale: 3000,
		Valid:     true,
	}}}
	proof := newDecodedPipeProof(request)
	for _, line := range []string{
		"[showinfo@hdr0 @ child] config in time_base: 2/3000, frame_rate: 1/1",
		"[showinfo@hdr0 @ child] n: 0 pts: -4503599627370497 pts_time:wrong fmt:yuv420p10le sar:1/1 s:2x2",
		"[showinfo@hdr0 @ child] color_range:tv color_space:bt2020nc color_primaries:bt2020 color_trc:smpte2084",
	} {
		if err := proof.line(line); err != nil {
			t.Fatal(err)
		}
	}
	close(proof.done)
	if err := proof.await(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
}

func TestDecodedAuthoredPictureMapping(t *testing.T) {
	if os.Getenv("UPBRR_HDR_DECODE_CALIBRATION") != "1" {
		t.Skip("set UPBRR_HDR_DECODE_CALIBRATION=1 for actual-decoder calibration")
	}
	executable, err := screenshots.ResolveFFmpegExecutable()
	if err != nil {
		t.Fatal(err)
	}
	for _, rgb := range []bool{false, true} {
		identity, err := (pictureDecoder{executable: executable}).capabilities(t.Context(), rgb)
		if err != nil || identity.Fingerprint == "" {
			t.Fatalf("actual decoder conversion rgb=%v: %v", rgb, err)
		}
	}
	err = New(NewAdmission(), nil).WithSession(t.Context(), func(session *Session) error {
		path := filepath.Join("testdata", "decoded-pq-cuts.mkv")
		native, err := session.ExtractNativeFile(t.Context(), path, api.HDRAnalysisFormatsBoth)
		if err != nil {
			return fmt.Errorf("extract calibration source: %w", err)
		}
		defer native.Release()
		evidence, err := native.Evidence(api.HDRAnalysisFormatsBoth, api.HDRSourceEvidence{})
		if err != nil {
			return fmt.Errorf("convert calibration evidence: %w", err)
		}
		defer evidence.Release()
		input := DecodedInput{
			Path:   path,
			Native: evidence,
			Source: api.HDRDecodedSourceEvidence{
				VideoStreams:           1,
				Codec:                  "hevc",
				Width:                  192,
				Height:                 112,
				BitDepth:               10,
				PixelAspectNumerator:   1,
				PixelAspectDenominator: 1,
				Range:                  "limited",
				Matrix:                 "bt2020nc",
				Primaries:              "bt2020",
				Transfer:               "smpte2084",
			},
		}
		times, err := decodedTimeline(input)
		if err != nil {
			return fmt.Errorf("calibration timeline (formats=%+v): %w", evidence.Results, err)
		}
		if len(times) != 120 {
			t.Fatalf("native calibration picture count %d", len(times))
		}
		request := pictureDecodeRequest{
			inputs:   []DecodedInput{input},
			times:    [][]pictureTime{times},
			selected: [][]int{{0, 12, 36, 60, 84, 108}},
		}
		decoder := pictureDecoder{executable: executable}
		levels := []float64{0.12, 0.64, 0.22, 0.78, 0.32, 0.88}
		decodeErr := decoder.decode(t.Context(), request, func(i int, frame []byte) error {
			if len(frame) != 192*112*2 {
				t.Fatal("unexpected calibration frame size")
			}
			black := binary.LittleEndian.Uint16(frame[:2])
			center := float64(binary.LittleEndian.Uint16(frame[2*(56*192+96):])) / 65535
			if black > 1 || math.Abs(center-levels[i]) > 0.002 {
				t.Fatalf("selected frame %d black=%d center=%f expected=%f", request.selected[0][i], black, center, levels[i])
			}
			return nil
		})
		if decodeErr != nil {
			return fmt.Errorf("calibration decode: %w", decodeErr)
		}
		// Distinct A/B indices and large offsets prove framesync pairs the
		// selected pixels rather than their original container timestamps.
		for _, indices := range [][2][]int{
			{{0, 12, 36}, {60, 72, 96}},
			{{60, 72, 96}, {0, 12, 36}},
		} {
			paired := pictureDecodeRequest{
				inputs:   []DecodedInput{input, input},
				times:    [][]pictureTime{times, times},
				selected: [][]int{indices[0], indices[1]},
				crop: &api.HDRCrop{
					Left:   0,
					Top:    12,
					Width:  192,
					Height: 88,
				},
			}
			if err := decoder.decode(t.Context(), paired, func(i int, frame []byte) error {
				for side := range 2 {
					index := paired.selected[side][i]
					level := 0.12
					for j, start := range []int{0, 12, 36, 60, 84, 108} {
						if index >= start {
							level = levels[j]
						}
					}
					// Independent ST 2084 EOTF on the authored 10-bit code.
					pq := math.Round(876*level) / 876
					power := math.Pow(pq, 32.0/2523)
					want := 10000 * math.Pow(max(power-3424.0/4096, 0)/(2413.0/128-2392.0/128*power), 16384.0/2610)
					rgb, valid, _ := gradePixel(frame, 384, 88, side*192+96, 44)
					for _, value := range rgb {
						if !valid || math.Abs(value-want) > max(0.01, want*0.005) {
							t.Fatalf("paired pixel side=%d index=%d value=%f want=%f", side, index, value, want)
						}
					}
				}
				return nil
			}); err != nil {
				return fmt.Errorf("calibrate paired output: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
