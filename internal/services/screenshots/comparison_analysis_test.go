// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"bytes"
	"context"
	"errors"
	"image"
	"io"
	"math"
	"net/http"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/comparisons"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestComparisonMainCanvasPadsWithoutChangingNativeInterior(t *testing.T) {
	executable, err := resolveFFmpeg()
	if err != nil {
		t.Skip("FFmpeg unavailable")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "Example.Cropped.mkv")
	command := exec.CommandContext(t.Context(), executable, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=1920x1040:rate=1:duration=1", "-c:v", "ffv1", path)
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("author canvas fixture: %v %s", err, data)
	}
	service := NewService(config.Config{}, api.NopLogger{}, dir, nil)
	source, err := service.InspectComparisonSource(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	captured, err := service.ComparisonCapture(t.Context(), ComparisonCaptureRequest{
		Source:       source,
		OutputWidth:  1920,
		OutputHeight: 1080,
	})
	if err != nil {
		t.Fatal(err)
	}
	original, err := exec.CommandContext(t.Context(), executable, "-hide_banner", "-loglevel", "error", "-threads", "4", "-i", path, "-frames:v", "1", "-f", "image2pipe", "-vcodec", "png", "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	want, _, err := image.Decode(bytes.NewReader(original))
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := image.Decode(bytes.NewReader(captured.ImageBytes))
	if err != nil {
		t.Fatal(err)
	}
	if got.Bounds() != image.Rect(0, 0, 1920, 1080) {
		t.Fatalf("canvas=%v", got.Bounds())
	}
	for y := range 1080 {
		for x := range 1920 {
			r, g, b, a := got.At(x, y).RGBA()
			if y < 20 || y >= 1060 {
				if r != 0 || g != 0 || b != 0 || a != 65535 {
					t.Fatalf("padding is not pure black at %d,%d", x, y)
				}
			} else {
				wr, wg, wb, wa := want.At(x, y-20).RGBA()
				if r != wr || g != wg || b != wb || a != wa {
					t.Fatalf("native interior changed at %d,%d", x, y)
				}
			}
		}
	}
}

func TestComparisonAnalysisMetricRetentionAndBinding(t *testing.T) {
	stream := &comparisonRasterStream{height: 288, motionHeight: 144}
	stream.reset(t.Context())
	diagnostic := &comparisonDiagnosticStream{stream: stream}
	write := func(value string) {
		t.Helper()
		if _, err := diagnostic.Write([]byte(value + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	write("[metadata@comparison_cut @ bounded] frame:0 pts:0 pts_time:0")
	write("[metadata@comparison_cut @ bounded] lavfi.scd.score=0")
	for range 100 {
		write(strings.Repeat("diagnostic ", 700))
	}
	write("[metadata@comparison_cut @ bounded] frame:1 pts:40 pts_time:0.04")
	write("[metadata@comparison_cut @ bounded] lavfi.scd.score=12.5")
	if _, err := diagnostic.result(); err != nil || len(stream.cuts) != 2 || stream.cuts[1].score != 12.5 {
		t.Fatal("late cut metrics were lost beyond the diagnostic tail")
	}
	if err := stream.metric("[metadata@comparison_cut @ bounded] lavfi.scd.score=10"); err == nil {
		t.Fatal("duplicate metric accepted")
	}
	stream.reset(context.Background())
	branch := ComparisonFrameObservation{
		PTS:                 40,
		TimeBaseNumerator:   1,
		TimeBaseDenominator: 1000,
		TimestampSeconds:    .04,
		transportChecksum:   "1234",
		Width:               272,
		Height:              144,
		PixelFormat:         "gray",
	}
	if err := stream.observe("showinfo@comparison_motion", branch); err != nil {
		t.Fatal(err)
	}
	wrong := branch
	wrong.TimeBaseDenominator = 0
	if err := stream.observe("showinfo@comparison_motion", wrong); err == nil {
		t.Fatal("an invalid output clock retained native authority")
	}
	if record := <-stream.motionRecords; record.PTS != 40 || record.TimestampSeconds != .04 {
		t.Fatal("transport clock escaped into native provenance")
	}
}

func TestComparisonSharedRasterMeasurements(t *testing.T) {
	const width, height = 512, 320
	original := bytes.Repeat([]byte{128}, width*height*3/2)
	levels := []byte{7, 8, 63, 64, 200}
	for y := range height {
		for x := range width {
			original[y*width+x] = levels[(y*width+x)%5]
		}
	}
	measurement, err := measureSceneRasters(t.Context(), original, original, height)
	if err != nil || measurement.DarkFraction != .6 || measurement.NearBlackFraction != .2 || measurement.OriginalContrast != 193 || measurement.BlurredContrast != 193 {
		t.Fatalf("histogram boundary mismatch: %+v %v", measurement, err)
	}
	// A smooth luma tile with fine chroma detail must fail the unblurred guard.
	for i := range width * height {
		original[i] = 32
	}
	for i := width * height; i < width*height*5/4; i++ {
		if i%2 == 0 {
			original[i] = 64
		} else {
			original[i] = 192
		}
	}
	blurred := bytes.Repeat([]byte{128}, len(original))
	for i := range width * height {
		blurred[i] = 32
	}
	measurement, err = measureSceneRasters(t.Context(), original, blurred, height)
	if err != nil {
		t.Fatal(err)
	}
	for _, tile := range measurement.Tiles {
		if tile.Qualified {
			t.Fatal("blur erased chroma texture and incorrectly qualified a flat tile")
		}
	}
	// Partial edge tiles retain their actual area in a non-16:9 analysis raster.
	const partialHeight = 302
	original = bytes.Repeat([]byte{128}, width*partialHeight*3/2)
	measurement, err = measureSceneRasters(t.Context(), original, original, partialHeight)
	if err != nil {
		t.Fatal(err)
	}
	area := 0
	for _, tile := range measurement.Tiles {
		area += tile.Region.Area
	}
	if area != width*partialHeight || measurement.Tiles[len(measurement.Tiles)-1].Region.Height != 14 {
		t.Fatalf("partial tile area=%d", area)
	}
}

func sceneGlyphGrid() ([]byte, api.ComparisonSceneRegion) {
	const width, height = 512, 2048
	pixels := bytes.Repeat([]byte{128}, width*height)
	for y := 0; y+5 <= height; y += 8 {
		for x := 0; x+2 <= width; x += 4 {
			for dy := range 5 {
				pixels[(y+dy)*width+x], pixels[(y+dy)*width+x+1] = 255, 255
			}
		}
	}
	return pixels, api.ComparisonSceneRegion{
		Width:  width,
		Height: height,
		Area:   width * height,
	}
}

func TestComparisonTextGroupingBoundsDenseComponentsAndCancellation(t *testing.T) {
	pixels, active := sceneGlyphGrid()
	rows, err := sceneTextRows(t.Context(), pixels, active.Width, active)
	if err != nil || len(rows) == 0 {
		t.Fatalf("dense glyph lines were lost: rows=%d err=%v", len(rows), err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := sceneTextRows(ctx, pixels, active.Width, active); !errors.Is(err, context.Canceled) {
		t.Fatalf("text grouping ignored cancellation: %v", err)
	}
}

func BenchmarkComparisonTextDenseComponents(b *testing.B) {
	pixels, active := sceneGlyphGrid()
	for b.Loop() {
		if _, err := sceneTextRows(b.Context(), pixels, active.Width, active); err != nil {
			b.Fatal(err)
		}
	}
}

func TestComparisonAnalysisNativeStream(t *testing.T) {
	executable, err := resolveFFmpeg()
	if err != nil {
		t.Skip("FFmpeg unavailable")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "Example.mkv")
	cmd := exec.CommandContext(t.Context(), executable, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=512x288:rate=25:duration=4", "-c:v", "ffv1", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("author synthetic media: %v %s", err, output)
	}
	s := NewService(config.Config{}, api.NopLogger{}, dir, nil)
	source, err := s.InspectComparisonSource(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	source.InputFingerprint = "synthetic-source"
	t.Run("sparse output bypasses the ambient proxy", func(t *testing.T) {
		t.Setenv("http_proxy", "http://127.0.0.1:0")
		t.Setenv("no_proxy", "")
		frames, err := s.AnalyzeComparisonSource(t.Context(), ComparisonSampleRequest{Source: source, DurationSeconds: 1})
		if err != nil || !slices.ContainsFunc(frames, func(f ComparisonSample) bool { return f.AnalysisAnchor }) {
			t.Fatalf("missing direct sparse output: %v", err)
		}
	})
	transport := &comparisonRasterStream{
		height:       288,
		motionHeight: 144,
		maxSparse:    comparisonSparseFrameLimit(4),
	}
	frames, err := s.SampleComparisonSource(t.Context(), ComparisonSampleRequest{
		Source:          source,
		DurationSeconds: 4,
		StrideSeconds:   .002,
		NativeFrames:    true,
		NormalizeSDR:    true,
		analysis:        transport,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) < 12 {
		t.Fatalf("missing sparse observations: %d", len(frames))
	}
	rawBytes := transport.motion.bytes + transport.sparse.bytes
	legacyBytes := int64(transport.motion.count+transport.sparse.count) * 1024 * 288 * 3 / 2
	if rawBytes*10 > legacyBytes*3 {
		t.Fatalf("compact analysis missed 70 percent transport reduction: compact=%d legacy=%d", rawBytes, legacyBytes)
	}
	t.Logf("raw transport compact=%d legacy=%d reduction=%.2f%%", rawBytes, legacyBytes, 100*(1-float64(rawBytes)/float64(legacyBytes)))
	t.Run("cancellation joins both native outputs", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		ctx = api.WithWorkflowProgressReporter(ctx, func(update api.WorkflowProgressUpdate) {
			if update.Completed >= 600 {
				cancel()
			}
		})
		stream := &comparisonRasterStream{
			height:       288,
			motionHeight: 144,
			maxSparse:    comparisonSparseFrameLimit(4),
		}
		_, err := s.SampleComparisonSource(ctx, ComparisonSampleRequest{
			Source:          source,
			DurationSeconds: 4,
			StrideSeconds:   .002,
			NativeFrames:    true,
			NormalizeSDR:    true,
			analysis:        stream,
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("analysis cancellation lost its origin: %v", err)
		}
		select {
		case <-stream.done:
		default:
			t.Fatal("analysis returned before its output coordinator exited")
		}
	})
	t.Run("standalone seeks share an encoded-byte allowance", func(t *testing.T) {
		_, input, err := s.openComparisonFileInput(t.Context(), source.Path, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(input.close)
		initial := input.budget.RemainingEncodedBytes
		for _, span := range []string{"bytes=0-31", "bytes=64-95"} {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, input.url, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Range", span)
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			n, readErr := io.Copy(io.Discard, response.Body)
			closeErr := response.Body.Close()
			if response.StatusCode != http.StatusPartialContent || n != 32 || readErr != nil || closeErr != nil {
				t.Fatalf("bounded seek returned %d bytes, HTTP %d: %v %v", n, response.StatusCode, readErr, closeErr)
			}
		}
		input.mu.Lock()
		remaining := input.budget.RemainingEncodedBytes
		input.mu.Unlock()
		if initial <= 0 || initial > 8<<30 || initial-remaining < 64 {
			t.Fatal("standalone source reads escaped the shared default byte limit")
		}
	})
	budget := &ComparisonDecodeBudget{RemainingEncodedBytes: 8 << 30}
	bounded, err := s.AnalyzeComparisonSource(t.Context(), ComparisonSampleRequest{
		Source:           source,
		DurationSeconds:  4,
		Budget:           budget,
		MatchAspectRatio: 2,
		MatchCrop: &ComparisonCrop{
			X:      0,
			Y:      16,
			Width:  512,
			Height: 256,
		},
	})
	if err != nil || len(bounded) != len(frames) || budget.RemainingEncodedBytes >= 8<<30 {
		t.Fatalf("bounded file input failed: frames=%d error=%v admitted=%d", len(bounded), err, (8<<30)-budget.RemainingEncodedBytes)
	}
	matching, err := s.SampleComparisonSource(t.Context(), ComparisonSampleRequest{
		Source:           source,
		DurationSeconds:  4,
		StrideSeconds:    .002,
		NativeFrames:     true,
		NormalizeSDR:     true,
		MatchAspectRatio: 2,
		MatchCrop: &ComparisonCrop{
			X:      0,
			Y:      16,
			Width:  512,
			Height: 256,
		},
	})
	if err != nil || len(matching) != len(frames) {
		t.Fatalf("independent matching reduction: %v", err)
	}
	for i := range frames {
		if bounded[i].PTS != frames[i].PTS || !bytes.Equal(bounded[i].Pixels, matching[i].Pixels) || bounded[i].Evidence.Motion != frames[i].Evidence.Motion || bounded[i].Evidence.Dark != frames[i].Evidence.Dark || bounded[i].Evidence.Flat != frames[i].Evidence.Flat {
			t.Fatal("file admission or the shared matching branch changed native identity or scene evidence")
		}
	}
	limited := &ComparisonDecodeBudget{RemainingEncodedBytes: 64}
	if _, err := s.AnalyzeComparisonSource(t.Context(), ComparisonSampleRequest{
		Source:          source,
		DurationSeconds: 4,
		Retain:          ComparisonRetentionLimit(4096),
	}); !errors.Is(err, ErrComparisonFastInconclusive) {
		t.Fatalf("analysis escaped the shared parent retention limit: %v", err)
	}
	if _, err := s.AnalyzeComparisonSource(t.Context(), ComparisonSampleRequest{
		Source:          source,
		DurationSeconds: 4,
		Budget:          limited,
	}); !errors.Is(err, ErrComparisonFastInconclusive) {
		t.Fatalf("file reads escaped admission: %v", err)
	}
	previous := -1.0
	for i, f := range frames {
		if !f.AnalysisAnchor {
			continue
		}
		if f.Identity == nil || f.Replay == nil || f.Evidence == nil || f.Evidence.Width != 512 || f.Evidence.Height != 288 || len(f.Pixels) != 256 {
			t.Fatalf("unbound analysis sample %d: identity=%v replay=%v evidence=%+v pixels=%d", i, f.Identity, f.Replay != nil, f.Evidence, len(f.Pixels))
		}
		if previous >= 0 && f.RelativeTimestampSeconds-previous < .25 {
			t.Fatal("sparse cadence below 0.25 seconds")
		}
		previous = f.RelativeTimestampSeconds
	}
	// Independently produce the frozen unblurred motion raster. The packed
	// transport must not rescale its luma or route analysis blur into V9.
	baseline, err := exec.CommandContext(t.Context(), executable, "-hide_banner", "-loglevel", "error", "-threads", "4", "-filter_threads", "1", "-i", path, "-an", "-sn", "-dn", "-vf", "scale=256:144:flags=area:out_range=full,format=gray", "-fps_mode", "passthrough", "-f", "rawvideo", "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	if len(baseline) != len(frames)*256*144 {
		t.Fatal("motion baseline lost native cadence")
	}
	for i := 1; i < len(frames); i++ {
		if frames[i].Evidence.Reason == "title_or_transition" {
			continue
		}
		a, b := baseline[(i-1)*256*144:i*256*144], baseline[i*256*144:(i+1)*256*144]
		speed, _, _, _ := comparisons.MeasureMotion(a, b, 256, 144, frames[i].RelativeTimestampSeconds-frames[i-1].RelativeTimestampSeconds)
		if math.Abs(speed-frames[i].Evidence.Motion.Score) > 1e-12 {
			t.Fatalf("transport changed V9 luma at frame %d: %.9f versus %.9f", i, frames[i].Evidence.Motion.Score, speed)
		}
	}
}
