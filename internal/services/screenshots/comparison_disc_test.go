package screenshots

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestComparisonAuthoredSizingUsesDecodedPixelAspect(t *testing.T) {
	retainedDVD := ComparisonSource{
		Input:          &filesystem.ResolvedComparisonInput{Kind: "dvd"},
		Width:          720,
		Height:         480,
		WidthScale:     8.0 / 9.0,
		SARNumerator:   8,
		SARDenominator: 9,
	}
	if width, height := retainedDVD.ScreenshotDisplaySize(); width != 720 || height != 540 {
		t.Fatalf("retained DVD screenshot size = %dx%d, want 720x540", width, height)
	}
	if width, height := retainedDVD.DisplaySize(); width != 640 || height != 480 {
		t.Fatalf("legacy fit-and-pad size = %dx%d, want 640x480", width, height)
	}
}

func TestComparisonHistoricalDiscReinspectionAdmitsBothReadsBeforeStaging(t *testing.T) {
	root := t.TempDir()
	source := ComparisonSource{
		InputFingerprint: "disc",
		ScratchDir:       filepath.Join(root, "staging"),
		Input:            &filesystem.ResolvedComparisonInput{Root: root},
	}
	replay := &ComparisonFrameReplay{Identity: api.ComparisonFrameIdentity{
		Version:          1,
		InputFingerprint: "disc",
		Occurrence:       "one",
		StreamID:         "video",
	}, Window: comparisonDiscWindow{
		Occurrence:     "one",
		StreamID:       "video",
		End90:          90000,
		PrerollSeconds: 2,
		EncodedSHA256:  strings.Repeat("0", 64),
		Spans:          []filesystem.ComparisonSpan{{Path: filepath.Join(root, "source.mpg"), End: 100}},
	}}
	for _, tc := range []struct {
		name                    string
		sourceBytes, totalBytes int64
		seconds                 float64
	}{{"staging and decode", 199, 200, 3}, {"shared allowance", 200, 199, 3}, {"preroll", 200, 200, 2}} {
		t.Run(tc.name, func(t *testing.T) {
			budget := ComparisonDecodeBudget{
				RemainingEncodedBytes: tc.sourceBytes,
				AttemptRemaining:      new(tc.totalBytes),
				MovieSecondsLimit:     tc.seconds,
			}
			_, err := new(Service).ReinspectComparisonReplay(t.Context(), ComparisonSampleRequest{Source: source, Budget: &budget}, replay)
			if !errors.Is(err, ErrComparisonFastInconclusive) || budget.ProcessStarts != 0 || budget.ConsumedMovieSeconds != 0 || budget.RemainingEncodedBytes != tc.sourceBytes || *budget.AttemptRemaining != tc.totalBytes {
				t.Fatalf("unadmitted historical read: budget=%+v err=%v", budget, err)
			}
			if _, err := os.Stat(source.ScratchDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("staging began before admission: %v", err)
			}
		})
	}
}

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
		PixelFormat:         "yuv420p",
		Width:               720,
		Height:              576,
		SARNumerator:        16,
		SARDenominator:      15,
		Colour:              "bt470bg",
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
	established := ComparisonSource{
		InputFingerprint:  source.InputFingerprint,
		NativePixelFormat: "yuv420p",
		Width:             720,
		Height:            576,
		SARNumerator:      16,
		SARDenominator:    15,
		NativeColour:      "bt470bg",
	}
	for _, test := range []struct {
		name   string
		change func(*ComparisonFrameObservation)
	}{
		{name: "unchanged"},
		{name: "pixel format", change: func(o *ComparisonFrameObservation) { o.PixelFormat = "yuv420p10le" }},
		{name: "geometry", change: func(o *ComparisonFrameObservation) { o.Width = 704 }},
		{name: "sample aspect ratio", change: func(o *ComparisonFrameObservation) { o.SARNumerator = 64 }},
		{name: "colour", change: func(o *ComparisonFrameObservation) { o.Colour = "bt709" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			native := observation
			native.PTS, native.Identity, native.Replay = -45000, nil, nil
			if test.change != nil {
				test.change(&native)
			}
			err := bindDiscObservation(established, window, &native)
			if test.change != nil {
				if err == nil || native.Identity != nil || native.Replay != nil {
					t.Fatalf("changed native format acquired replay authority: %+v err=%v", native, err)
				}
			} else if err != nil || native.Replay == nil || native.Replay.DecoderPTS != -45000 {
				t.Fatalf("unchanged native format lost original replay: %+v err=%v", native, err)
			}
		})
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
	if source.DurationSeconds < 5 {
		t.Skip("programme is shorter than the five-second overlap needed for the replay check")
	}
	starts := []float64{20, 600, 2500, 5000}
	if source.DurationSeconds < starts[0]+4.5 {
		starts = []float64{.5}
	}
	for _, second := range starts {
		if second+4.5 > source.DurationSeconds {
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
		t.Logf("title_seconds=%.6f pts=%d occurrence=%s frames=%d", first.RelativeTimestampSeconds, first.PTS, first.Identity.Occurrence, len(frames))
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
				if other.Replay.TitleTime90 != first.Replay.TitleTime90 {
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
		if captured.FrameID != first.FrameID || !captured.Replay.SamePicture(first.Replay) {
			t.Fatal("capture changed selected native picture")
		}
	}
}
