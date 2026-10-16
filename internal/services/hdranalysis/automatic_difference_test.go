// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strconv"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSampledDifferenceDisclosesCoverageAndEmptySamples(t *testing.T) {
	for _, test := range []struct {
		kind       api.HDRArtifactKind
		definition string
	}{
		{api.HDRArtifactDVDifference, "RPU L1 minus measured picture; different statistics"},
		{api.HDRArtifactHDR10PlusDifference, "HDR10+ metadata minus measured picture; different statistics"},
		{api.HDRArtifactAutomaticGrade, "Primary picture minus HDR10 reference"},
	} {
		t.Run(string(test.kind), func(t *testing.T) {
			report := sampledReportFixture()
			for i := range report.Automatic.Frames {
				frame := &report.Automatic.Frames[i]
				frame.MaxRGBNits, frame.FALLNits, frame.DVKnown, frame.HDR10PlusKnown = 0, 0, true, true
			}
			if test.kind == api.HDRArtifactAutomaticGrade {
				report.Options.Comparison = &api.HDRComparisonOptions{
					SourceID:    "reference",
					FrameOffset: new(0),
					Crop:        api.HDRCrop{Width: 192, Height: 112},
					Grade:       true,
				}
				report.Sources[1] = "synthetic-reference"
				report.Automatic.Reference = append([]AutomaticFrame(nil), report.Automatic.Frames...)
				report.Automatic.Sampling.Frames[1] = 26
				report.Automatic.Sampling.Origins[1] = report.Automatic.Sampling.Origins[0]
				cadence := measurementTimestamp(pictureTime{
					Value:     1,
					Timescale: 25,
					Valid:     true,
				})
				report.Automatic.Sampling.Cadence = &cadence
			}
			var output bytes.Buffer
			if err := (&Session{}).RenderMeasurementArtifact(t.Context(), report, test.kind, "Synthetic empty sampled differences", &output); err != nil {
				t.Fatal(err)
			}
			actual, err := png.Decode(&output)
			if err != nil {
				t.Fatal(err)
			}
			expected := image.NewRGBA(actual.Bounds())
			draw.Draw(expected, expected.Bounds(), image.White, image.Point{}, draw.Src)
			plot, err := newAutomaticPlot(expected)
			if err != nil {
				t.Fatal(err)
			}
			defer plot.face.Close()
			plot.label(85, 68, "FALL / average: "+test.definition+"; no eligible measured samples | n=0", color.Black)
			plot.label(85, 785, "Sampled every 25 source frames; brief mismatches can be missed. Black frames excluded (FALL <= 0.05 nits); lines connect measured samples.", color.Gray{Y: 90})
			// Compare the exported PNG's actual visible glyphs, rather than a text
			// formatter. This catches both clipped captions and invented zeros.
			for _, check := range []struct {
				name string
				rect image.Rectangle
			}{
				{"empty_statistics", image.Rect(85, 48, 1750, 72)},
				{"sampling_warning", image.Rect(85, 766, 1750, 798)},
			} {
				t.Run(check.name, func(t *testing.T) {
					rect := check.rect
					for y := rect.Min.Y; y < rect.Max.Y; y++ {
						for x := rect.Min.X; x < rect.Max.X; x++ {
							if color.RGBAModel.Convert(actual.At(x, y)) != expected.RGBAAt(x, y) {
								t.Fatalf("missing or incorrect visible difference disclosure at %d,%d", x, y)
							}
						}
					}
				})
			}
		})
	}
}

func TestReferenceOnlyChromaticityRendersMappedCloud(t *testing.T) {
	for _, offset := range []int{-1, 0, 1} {
		t.Run(strconv.Itoa(offset), func(t *testing.T) {
			report := automaticReportFixture()
			report.Automatic.Frames = nil
			report.Automatic.Reference = nil
			for i := range 12 {
				frame := AutomaticFrame{Index: i, PTS: measurementTimestamp(pictureTime{
					Value:     int64(i),
					Timescale: 24,
					Valid:     true,
				})}
				report.Automatic.Frames = append(report.Automatic.Frames, frame)
				report.Automatic.Reference = append(report.Automatic.Reference, frame)
				report.Automatic.Chromaticity[1] = append(report.Automatic.Chromaticity[1], ChromaticityPoint{
					Index: i,
					X:     .3,
					Y:     .3,
				})
			}
			report.Automatic.Chromaticity[0] = nil
			report.Options.Comparison = &api.HDRComparisonOptions{FrameOffset: new(offset)}
			canvas := image.NewRGBA(image.Rect(0, 0, 2400, 900))
			plot, err := newAutomaticPlot(canvas)
			if err != nil {
				t.Fatal(err)
			}
			defer plot.face.Close()
			plot.chromaticity(report)
			// Each of the five panels must contain the retained reference pixel cloud.
			for panel := range 5 {
				x := 55 + panel*455 + int(.3/.75*390)
				y := 572 - int(.3/.85*442)
				if canvas.RGBAAt(x, y) != automaticGreen {
					t.Fatalf("reference cloud absent at panel %d (%d,%d)", panel, x, y)
				}
			}
		})
	}
}
