// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bytes"
	"encoding/binary"
	"image/color"
	"image/png"
	"math"
	"testing"

	dovi "github.com/Audionut/go-dovi-extract"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestAutomaticColourUsesLitPixelsAndFullResolutionXY(t *testing.T) {
	pictures, err := newAutomaticPictures(api.HDRDecodedSourceEvidence{
		Width:                  4,
		Height:                 4,
		BitDepth:               10,
		Codec:                  "hevc",
		VideoStreams:           1,
		PixelAspectNumerator:   1,
		PixelAspectDenominator: 1,
	}, &api.HDRCrop{Width: 4, Height: 4})
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 4*4*6)
	pq, _ := dovi.NitsToPQ(100)
	code := uint16(math.Round(pq * 65535))
	for _, pixel := range []int{0, 1, 4, 5} {
		binary.LittleEndian.PutUint16(data[2*(2*16+pixel):], code)
	}
	if err := pictures.observe(data); err != nil {
		t.Fatal(err)
	}
	frame := pictures.frames[0]
	if frame.ColourPixels != 1 || math.Abs(frame.MeanRGBPQ[0]-float64(code)/65535) > 1e-9 || frame.MeanRGBPQ[1] != 0 || frame.MeanRGBPQ[2] != 0 {
		t.Fatalf("black pixels diluted colour means: %+v", frame)
	}
	if frame.ChromaticityPixels != 4 || len(pictures.chroma) != 4 || math.Abs(frame.ChromaticityMean[0]-.708) > .00001 ||
		math.Abs(frame.ChromaticityMean[1]-.292) > .00001 {
		t.Fatalf("full-resolution lit-pixel chromaticity: %+v", frame)
	}
	if pictures.recent[0].RGB[0] <= pictures.recent[0].RGB[1] || pictures.recent[0].RGB[0] <= pictures.recent[0].RGB[2] {
		t.Fatal("colour scene thumbnail lost red channel evidence")
	}
	clear(data)
	if err := pictures.observe(data); err != nil {
		t.Fatal(err)
	}
	if pictures.frames[1].BorderKnown || pictures.frames[1].Borders != [4]int{} || pictures.frames[1].ColourPixels != 0 {
		t.Fatal("dark-frame border or colour coverage was guessed")
	}
}

func TestAutomaticSceneSheetShowsMetadataPairWhenPictureCutIsOffset(t *testing.T) {
	frames := make([]AutomaticFrame, 12)
	frames[6].SceneScore, frames[6].Bright = 1, true
	plan := &sceneMeasurement{candidates: []sceneCandidate{{Candidate: 5, usable: true}}}
	scene := automaticSceneResult(api.HDRNativeDolbyVision, plan, frames, []cachedCut{{index: 6, frames: []MeasurementThumbnail{{Index: 4}, {Index: 5}, {Index: 6}}}})
	if len(scene.Observations) != 1 || scene.Observations[0].BestOffset != 1 || len(scene.Thumbnails) != 2 ||
		scene.Thumbnails[0].Index != 4 || scene.Thumbnails[1].Index != 5 {
		t.Fatalf("sheet concealed an offset by displaying the picture-cut pair: %+v", scene)
	}
}

func TestAutomaticPlotLayoutsMatchGuideFamilies(t *testing.T) {
	report := automaticReportFixture()
	for i := range report.Automatic.Frames {
		report.Automatic.Frames[i].DVKnown = true
		report.Automatic.Frames[i].DVPQ = [2]float64{600, 400}
	}
	report.Automatic.Areas = []AreaObservation{{
		Start:      0,
		End:        len(report.Automatic.Frames),
		Confidence: "unclear",
	}}
	for _, kind := range []api.HDRArtifactKind{api.HDRArtifactPicturePlot, api.HDRArtifactDVOverlay, api.HDRArtifactDVDifference, api.HDRArtifactAutomaticArea} {
		var output bytes.Buffer
		if err := (&Session{}).RenderMeasurementArtifact(t.Context(), report, kind, "Synthetic guide layouts", &output); err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(output.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		width, height := kind.ImageDimensions()
		if img.Bounds().Dx() != width || img.Bounds().Dy() != height || color.RGBAModel.Convert(img.At(0, 0)) != (color.RGBA{255, 255, 255, 255}) {
			t.Fatalf("%s has unexpected layout/background", kind)
		}
		if kind == api.HDRArtifactDVDifference && color.RGBAModel.Convert(img.At(85, 355)) == (color.RGBA{255, 255, 255, 255}) {
			t.Fatal("separate FALL panel axis missing")
		}
		if kind == api.HDRArtifactAutomaticArea && color.RGBAModel.Convert(img.At(85, 730)) == (color.RGBA{255, 255, 255, 255}) {
			t.Fatal("active-area pixel/frame axes missing")
		}
		if kind == api.HDRArtifactPicturePlot && color.RGBAModel.Convert(img.At(1000, 500)) != (color.RGBA{208, 170, 244, 255}) {
			t.Fatal("short clips do not fill the area between consecutive frame samples")
		}
	}
}
