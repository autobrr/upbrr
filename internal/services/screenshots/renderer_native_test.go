// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"bytes"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

// This opt-in check exercises real FFmpeg output. Tagged synthetic HDR tests
// pipeline support and records pixel parity, not mastered colour accuracy.
func TestNativeScreenshotRenderer(t *testing.T) {
	if os.Getenv("UPBRR_SCREENSHOT_NATIVE_TEST") != "1" {
		t.Skip("set UPBRR_SCREENSHOT_NATIVE_TEST=1 for real FFmpeg checks")
	}
	executable, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal("native check requires FFmpeg")
	}
	for _, hdr := range []bool{false, true} {
		name := "SDR"
		if hdr {
			name = "HDR10 tagged synthetic"
		}
		t.Run(name, func(t *testing.T) {
			input := filepath.Join(t.TempDir(), "source.mkv")
			args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=128x128:rate=24:duration=1"}
			if hdr {
				args = append(args, "-vf", "format=yuv420p10le,setparams=range=tv:color_primaries=bt2020:color_trc=smpte2084:colorspace=bt2020nc", "-c:v", "libx265", "-x265-params", "pools=1:frame-threads=1:log-level=error:colorprim=9:transfer=16:colormatrix=9")
			} else {
				args = append(args, "-c:v", "ffv1")
			}
			args = append(args, input)
			if _, err := (commandRunner{}).Run(t.Context(), executable, args, ""); err != nil {
				t.Fatal("cannot generate native synthetic input")
			}
			for _, lib := range []bool{false, true} {
				if lib && !hdr {
					continue
				}
				req := captureRequest{
InputPath: input,
 Timestamp: 0.5,
 FrameRate: 24,
 ToneMap: hdr,
 UseLibplacebo: lib,
 FrameOverlay: true,
 OverlaySize: 18,
 Resolution: "1080p",
 HDRFacts: api.HDRFacts{Formats: []api.HDRFormat{api.HDRFormatHDR10}},
}
				preview, processing, err := captureFrameBytes(t.Context(), commandRunner{}, executable, req, api.NopLogger{})
				if err != nil {
					t.Fatal(err)
				}
				if processing.Tonemapped != hdr || (lib && processing.Renderer != "libplacebo") {
					t.Fatalf("native renderer did not meet requested mode: %+v", processing)
				}
				req.OutputPath = filepath.Join(t.TempDir(), "capture.png")
				frame, err := captureFrame(t.Context(), commandRunner{}, executable, req, api.NopLogger{})
				if err != nil {
					t.Fatal(err)
				}
				if frame.Processing.Renderer != processing.Renderer || frame.Processing.Tonemapped != processing.Tonemapped {
					t.Fatal("native entrypoints selected different processing outcomes")
				}
				captured, err := os.ReadFile(req.OutputPath)
				if err != nil {
					t.Fatal(err)
				}
				left, _, err := image.Decode(bytes.NewReader(preview))
				if err != nil {
					t.Fatal(err)
				}
				right, _, err := image.Decode(bytes.NewReader(captured))
				if err != nil {
					t.Fatal(err)
				}
				if left.Bounds() != right.Bounds() {
					t.Fatal("native frame geometry differs")
				}
				bounds := left.Bounds()
				var maxDifference uint32
				differingPixels := 0
				for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
					for x := bounds.Min.X; x < bounds.Max.X; x++ {
						lr, lg, lb, la := left.At(x, y).RGBA()
						rr, rg, rb, ra := right.At(x, y).RGBA()
						if lr != rr || lg != rg || lb != rb || la != ra {
							differingPixels++
						}
						for _, pair := range [][2]uint32{{lr, rr}, {lg, rg}, {lb, rb}, {la, ra}} {
							difference := max(pair[0], pair[1]) - min(pair[0], pair[1])
							maxDifference = max(maxDifference, difference)
						}
					}
				}
				t.Logf("renderer=%s differing_pixels=%d max_channel_delta_8bit=%d", processing.Renderer, differingPixels, maxDifference/257)
				// Exact pixels across independent GPU renders are diagnostic;
				// renderer selection, decoded images and geometry are the contract.
				if !lib && maxDifference > 0 {
					t.Fatal("native software pixels differ")
				}
			}
		})
	}
}
