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

	"github.com/autobrr/upbrr/internal/config"
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
					InputPath:     input,
					Timestamp:     0.5,
					FrameRate:     24,
					ToneMap:       hdr,
					UseLibplacebo: lib,
					FrameOverlay:  true,
					OverlaySize:   18,
					Resolution:    "1080p",
					HDRFacts:      api.HDRFacts{Formats: []api.HDRFormat{api.HDRFormatHDR10}},
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

func TestNativeScreenshotVideoAndCoverSelection(t *testing.T) {
	if os.Getenv("UPBRR_SCREENSHOT_NATIVE_TEST") != "1" {
		t.Skip("set UPBRR_SCREENSHOT_NATIVE_TEST=1 for real FFmpeg checks")
	}
	executable, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal("native check requires FFmpeg")
	}
	root := t.TempDir()
	cover := filepath.Join(root, "cover.png")
	if _, err := (commandRunner{}).Run(t.Context(), executable, []string{
		"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=red:size=32x32", "-frames:v", "1", cover,
	}, ""); err != nil {
		t.Fatal(err)
	}
	for _, coverOnly := range []bool{false, true} {
		input := filepath.Join(root, "movie.mp4")
		args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=blue:size=64x48:duration=1", "-i", cover,
			"-map", "0:v", "-map", "1:v", "-c:v:0", "libx264", "-c:v:1", "png", "-disposition:v:1", "attached_pic"}
		if coverOnly {
			input = filepath.Join(root, "cover-only.m4a")
			args = []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=duration=1", "-i", cover,
				"-map", "0:a", "-map", "1:v", "-c:a", "aac", "-c:v", "png", "-disposition:v", "attached_pic"}
		}
		if _, err := (commandRunner{}).Run(t.Context(), executable, append(args, input), ""); err != nil {
			t.Fatal(err)
		}
		payload, _, err := captureFrameBytes(t.Context(), commandRunner{}, executable, captureRequest{InputPath: input}, api.NopLogger{})
		if err != nil {
			t.Fatalf("cover_only=%t: %v", coverOnly, err)
		}
		decoded, _, err := image.Decode(bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		want := image.Rect(0, 0, 64, 48)
		if coverOnly {
			want = image.Rect(0, 0, 32, 32)
		}
		if decoded.Bounds() != want {
			t.Fatalf("cover_only=%t captured geometry=%v want=%v", coverOnly, decoded.Bounds(), want)
		}
	}
}

func TestNativeDVDScreenshotWithZeroPrefixedVOB(t *testing.T) {
	if os.Getenv("UPBRR_SCREENSHOT_NATIVE_TEST") != "1" {
		t.Skip("set UPBRR_SCREENSHOT_NATIVE_TEST=1 for real FFmpeg checks")
	}
	executable, err := resolveFFmpeg()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	videoTS := filepath.Join(root, "VIDEO_TS")
	if err := os.Mkdir(videoTS, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct{ name, color string }{
		{"VTS_01_1.VOB", "red"},
		{"VTS_01_2.VOB", "blue"},
	} {
		path := filepath.Join(videoTS, fixture.name)
		if fixture.color == "red" {
			path = filepath.Join(root, "prefix-source.vob")
		}
		result, err := (commandRunner{}).Run(t.Context(), executable, []string{
			"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i",
			"color=" + fixture.color + ":size=720x576:rate=25:duration=3",
			"-c:v", "mpeg2video", "-f", "dvd", path,
		}, "")
		if err != nil {
			t.Fatalf("generate VOB: %s", ffmpegResultPreview(result, err))
		}
		if fixture.color == "red" {
			payload, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			dst, err := os.OpenFile(filepath.Join(videoTS, fixture.name), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			// The zero-filled prefix exceeds FFmpeg's default format probe budget.
			_, err = dst.WriteAt(payload, 34<<20)
			closeErr := dst.Close()
			if err != nil {
				t.Fatal(err)
			}
			if closeErr != nil {
				t.Fatal(closeErr)
			}
		}
	}
	mediaInfo := filepath.Join(root, "mediainfo.json")
	if err := os.WriteFile(mediaInfo, []byte(`{"media":{"track":[{"@type":"General","Duration":"6"},{"@type":"Video","FrameRate":"25.000"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewService(config.Config{}, api.NopLogger{}, t.TempDir(), commandRunner{})
	subject := api.ScreenshotSubject{
		SourcePath:        root,
		DiscType:          "DVD",
		MediaInfoJSONPath: mediaInfo,
	}
	for _, tc := range []struct {
		name    string
		second  float64
		wantRed bool
	}{
		{
			name:    "zero-prefixed segment",
			second:  1,
			wantRed: true,
		},
		{
			name:   "ordinary segment",
			second: 4,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertFrame := func(payload []byte) {
				t.Helper()
				frame, _, err := image.Decode(bytes.NewReader(payload))
				if err != nil {
					t.Fatal(err)
				}
				red, _, blue, _ := frame.At(frame.Bounds().Dx()/2, frame.Bounds().Dy()/2).RGBA()
				if (tc.wantRed && red < 0x8000) || (!tc.wantRed && blue < 0x8000) {
					t.Fatalf("wrong segment or unusable frame: red=%d blue=%d", red, blue)
				}
			}
			preview, err := service.PreviewFrame(t.Context(), subject, "", tc.second)
			if err != nil {
				t.Fatal(err)
			}
			assertFrame(preview.ImageBytes)
			capture, err := service.Capture(t.Context(), subject, []api.ScreenshotSelection{{TimestampSeconds: tc.second}}, api.ScreenshotPurposeFinal)
			if err != nil || len(capture.Images) != 1 {
				t.Fatalf("capture: got %d images, err=%v", len(capture.Images), err)
			}
			payload, err := os.ReadFile(capture.Images[0].Path)
			if err != nil {
				t.Fatal(err)
			}
			assertFrame(payload)
		})
	}
}
