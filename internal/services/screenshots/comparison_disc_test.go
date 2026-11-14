package screenshots

import (
	"os"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestComparisonDiscIdentitySeparatesOccurrencesAndOriginalClockWrap(t *testing.T) {
	const wrap = int64(1 << 33)
	source := ComparisonSource{InputFingerprint: "bounded-source"}
	window := comparisonDiscWindow{
		Occurrence:   "item-0-stc-0",
		StreamID:     "0x1011",
		SourceBase90: wrap - 90000,
		Start90:      wrap - 90000,
		End90:        wrap + 90000,
	}
	observation := ComparisonFrameObservation{
		PTS:                 -45000,
		TimeBaseNumerator:   1,
		TimeBaseDenominator: 90000,
		PictureChecksum:     "1234ABCD",
		PixelFormat:         "yuv420p",
	}
	if err := bindDiscObservation(source, window, &observation); err != nil {
		t.Fatal(err)
	}
	if observation.PTS != wrap-45000 || observation.RelativeTimestampSeconds != .5 || observation.Replay.DecoderPTS != -45000 {
		t.Fatalf("wrapped native identity=%+v", observation)
	}
	replayed := ComparisonFrameObservation{
		PTS:                 45000,
		TimeBaseNumerator:   1,
		TimeBaseDenominator: 90000,
		PictureChecksum:     "ABCD1234",
		PixelFormat:         "yuv420p",
	}
	if err := bindDiscObservation(source, window, &replayed); err != nil {
		t.Fatal(err)
	}
	if replayed.PTS != wrap+45000 || replayed.RelativeTimestampSeconds != 1.5 {
		t.Fatal("clock wrap changed authored title time")
	}
	repeated := observation
	repeated.PTS = -45000
	window.Occurrence = "item-1-stc-0"
	window.TitleBase90 = 180000
	if err := bindDiscObservation(source, window, &repeated); err != nil {
		t.Fatal(err)
	}
	if repeated.FrameID == observation.FrameID || repeated.PTS != observation.PTS || repeated.RelativeTimestampSeconds != 2.5 {
		t.Fatal("repeated physical picture aliased its earlier occurrence")
	}
}

func TestLocalComparisonDiscNativeClock(t *testing.T) {
	path := os.Getenv("UPBRR_COMPARISON_DISC")
	if path == "" {
		t.Skip("opt-in native disc clock acceptance")
	}
	discovery, err := filesystem.DiscoverComparisonProgrammes(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	candidate := os.Getenv("UPBRR_COMPARISON_PROGRAMME")
	if candidate == "" && len(discovery.Candidates) == 1 {
		candidate = discovery.Candidates[0].ID
	}
	if candidate == "" {
		for _, c := range discovery.Candidates {
			t.Logf("id=%s label=%s duration=%.3f", c.ID, c.Label, c.DurationSeconds)
		}
		t.Fatal("explicit programme required")
	}
	input, ok := discovery.Inputs[candidate]
	if !ok {
		t.Fatal("unknown candidate")
	}
	input, err = filesystem.ResolveComparisonProgramme(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(config.Config{}, api.NopLogger{}, t.TempDir(), nil)
	source, err := service.InspectResolvedComparisonSource(t.Context(), input, "local-acceptance", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("kind=%s duration=%.3f native_rate=%d/%d geometry=%dx%d sar_scale=%.6f interlace=%s", input.Kind, source.DurationSeconds, source.FrameRateNumerator, source.FrameRateDenominator, source.Width, source.Height, source.WidthScale, source.Interlace)
	for _, second := range []float64{20, 600, 2500, 5000} {
		if second+4 > source.DurationSeconds {
			continue
		}
		frames, err := service.sampleDiscComparison(t.Context(), ComparisonSampleRequest{
			Source:          source,
			StartSeconds:    second,
			DurationSeconds: 4,
			StrideSeconds:   .002,
		})
		if err != nil {
			t.Fatalf("window %.3f: %v", second, err)
		}
		if len(frames) == 0 {
			t.Fatal("empty native window")
		}
		first := frames[0]
		t.Logf("title_seconds=%.6f pts=%d occurrence=%s checksum=%s frames=%d", first.RelativeTimestampSeconds, first.PTS, first.Identity.Occurrence, first.PictureChecksum, len(frames))
		overlap, err := service.sampleDiscComparison(t.Context(), ComparisonSampleRequest{
			Source:          source,
			StartSeconds:    second - .5,
			DurationSeconds: 5,
			StrideSeconds:   .002,
		})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, other := range overlap {
			if other.FrameID == first.FrameID {
				found = true
				if other.PictureChecksum != first.PictureChecksum || other.Replay.TitleTime90 != first.Replay.TitleTime90 {
					t.Fatal("origin changed canonical picture")
				}
			}
		}
		if !found {
			t.Fatal("overlapping origin lost canonical picture")
		}
		captured, err := service.ComparisonCapture(t.Context(), ComparisonCaptureRequest{
			Source:           source,
			TimestampSeconds: first.RelativeTimestampSeconds,
			Replay:           first.Replay,
		})
		if err != nil {
			t.Fatal(err)
		}
		if captured.FrameID != first.FrameID || captured.PictureChecksum != first.PictureChecksum {
			t.Fatal("capture changed selected native picture")
		}
	}
}
