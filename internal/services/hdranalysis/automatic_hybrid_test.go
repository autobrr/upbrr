// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/at-wat/ebml-go"
	"github.com/autobrr/upbrr/pkg/api"
)

func automaticHybridInput(t *testing.T) DecodedInput {
	t.Helper()
	// Reuse the independent synthetic RPU/SEI oracle, replacing only its
	// arbitrary VCL bytes with the existing authored lossless calibration clip.
	// EBML syntax and block encoding remain owned by the installed library.
	var document struct {
		EBML struct {
			Version            uint64 `ebml:"EBMLVersion"`
			ReadVersion        uint64 `ebml:"EBMLReadVersion"`
			MaxIDLength        uint64 `ebml:"EBMLMaxIDLength"`
			MaxSizeLength      uint64 `ebml:"EBMLMaxSizeLength"`
			DocType            string `ebml:"EBMLDocType"`
			DocTypeVersion     uint64 `ebml:"EBMLDocTypeVersion"`
			DocTypeReadVersion uint64 `ebml:"EBMLDocTypeReadVersion"`
		}
		Segment struct {
			Info struct {
				TimecodeScale         uint64
				Duration              float64
				MuxingApp, WritingApp string
			}
			Tracks struct {
				TrackEntry []struct {
					TrackNumber, TrackUID, TrackType, DefaultDuration uint64
					CodecID                                           string
					CodecPrivate                                      []byte
					Video                                             struct {
						PixelWidth, PixelHeight uint64
						Colour                  struct{ MatrixCoefficients, Range, TransferCharacteristics, Primaries uint64 }
					}
				}
			}
			Cluster []struct {
				Timecode    uint64
				SimpleBlock []ebml.Block
				BlockGroup  []struct{ Block ebml.Block }
			}
		}
	}
	data, err := os.ReadFile(filepath.Join("testdata", "decoded-pq-cuts-25.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ebml.Unmarshal(bytes.NewReader(data), &document); err != nil {
		t.Fatal(err)
	}
	oracle, err := os.ReadFile(filepath.Join("testdata", "single-hybrid.hevc"))
	if err != nil {
		t.Fatal(err)
	}
	var rpu, sei []byte
	for nal := range bytes.SplitSeq(oracle, []byte{0, 0, 1}) {
		if len(nal) < 2 {
			continue
		}
		switch (nal[0] >> 1) & 63 {
		case 62:
			rpu = nal
		case 39:
			sei = nal
		}
	}
	if len(rpu) == 0 || len(sei) == 0 || len(document.Segment.Tracks.TrackEntry) != 1 || document.Segment.Tracks.TrackEntry[0].CodecPrivate[21]&3 != 3 {
		t.Fatal("authored oracle or length-prefixed video configuration differs")
	}
	appendNAL := func(out []byte, nal []byte) []byte {
		return append(binary.BigEndian.AppendUint32(out, uint32(len(nal))), nal...)
	}
	inject := func(block *ebml.Block) {
		if len(block.Data) != 1 {
			t.Fatal("authored video block is not a single frame")
		}
		out := appendNAL(nil, sei)
		out = append(out, block.Data[0]...)
		block.Data[0] = appendNAL(out, rpu)
	}
	for i := range document.Segment.Cluster {
		cluster := &document.Segment.Cluster[i]
		for j := range cluster.SimpleBlock {
			inject(&cluster.SimpleBlock[j])
		}
		for j := range cluster.BlockGroup {
			inject(&cluster.BlockGroup[j].Block)
		}
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "Synthetic.DV.HDR10Plus.mkv")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ebml.Marshal(&document, file); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	input := DecodedInput{
		Path:        path,
		Fingerprint: "authored-dual-hdr",
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
	return input
}

func TestAutomaticDecodableDualMetadata(t *testing.T) {
	if os.Getenv("UPBRR_HDR_DECODE_CALIBRATION") != "1" {
		t.Skip("requires actual shared decoder")
	}
	input := automaticHybridInput(t)
	directory := filepath.Dir(input.Path)
	path := input.Path
	err := New(NewAdmission(), nil).WithSession(t.Context(), func(session *Session) error {
		native, reports, err := session.AnalyzeAutomatic(t.Context(), input, nil, api.HDRDecodedOptions{Automatic: true}, api.HDRAnalysisFormatsBoth, directory, 0, true)
		if err != nil {
			return err
		}
		defer native.Release()
		if native.HDR10Plus == nil || native.DolbyVision == nil || len(native.HDR10Plus.Frames) != 120 || len(native.DolbyVision.Pictures) != 120 {
			t.Fatalf("shared native capture differs: HDR10+=%v DV=%v", native.HDR10PlusError, native.DolbyVisionError)
		}
		if reports[0].Automatic == nil || reports[0].Result.Measured != 120 {
			t.Fatalf("picture measurement differs: %+v", reports[0].Result)
		}
		for _, frame := range reports[0].Automatic.Frames {
			if !frame.DVKnown || !frame.HDR10PlusKnown {
				t.Fatal("metadata was not matched to every picture")
			}
		}
		for _, kind := range []api.HDRArtifactKind{api.HDRArtifactDVOverlay, api.HDRArtifactDVDifference, api.HDRArtifactHDR10PlusOverlay, api.HDRArtifactHDR10PlusDifference} {
			if !slices.ContainsFunc(MeasurementArtifacts(reports[0]), func(a api.HDRTypedArtifact) bool { return a.Kind == kind }) {
				t.Fatalf("missing %s", kind)
			}
			var out bytes.Buffer
			if err := session.RenderMeasurementArtifact(t.Context(), reports[0], kind, "Synthetic dual HDR", &out); err != nil {
				return err
			}
		}
		full := reports[0]
		input.Source.FrameRateNumerator, input.Source.FrameRateDenominator = "25", "1"
		selectedNative, selected, err := session.AnalyzeAutomatic(t.Context(), input, nil,
			api.HDRDecodedOptions{Automatic: true, Sampled: true}, api.HDRAnalysisFormatsBoth, directory, 0, true)
		if err != nil {
			return err
		}
		defer selectedNative.Release()
		if len(selectedNative.DolbyVision.Pictures) != 120 || len(selectedNative.HDR10Plus.Frames) != 120 || selected[0].Result.Measured != 5 {
			t.Fatal("sampling discarded complete compressed metadata or selected wrong pictures")
		}
		for _, frame := range selected[0].Automatic.Frames {
			other := full.Automatic.Frames[frame.Index]
			if frame.Index%25 != 0 || frame.PTS != other.PTS || frame.DVPQ != other.DVPQ || frame.HDR10PlusPQ != other.HDR10PlusPQ ||
				math.Abs(frame.MaxRGBNits-other.MaxRGBNits) > 1e-9 || math.Abs(frame.FALLNits-other.FALLNits) > 1e-9 || frame.Borders != other.Borders {
				t.Fatalf("sample differs from its full-resolution oracle: %+v", frame)
			}
		}
		var retained bytes.Buffer
		if err := WriteMeasurement(t.Context(), &retained, selected[0]); err != nil {
			return err
		}
		restored, err := ReadMeasurement(t.Context(), bytes.NewReader(retained.Bytes()))
		if err != nil {
			return err
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove source before retained rendering: %w", err)
		}
		for _, artifact := range MeasurementArtifacts(restored) {
			var out bytes.Buffer
			if err := session.RenderMeasurementArtifact(t.Context(), restored, artifact.Kind, "Synthetic sampled HDR", &out); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
