// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/services/screenshots"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestAutomaticWithoutDecoderRetainsNativeMetadata(t *testing.T) {
	err := New(NewAdmission(), nil).WithSession(t.Context(), func(session *Session) error {
		input := DecodedInput{Path: filepath.Join("testdata", "reordered-hybrid.mkv"), Fingerprint: "synthetic-dual"}
		native, reports, err := session.automaticMetadataOnly(t.Context(), input, api.HDRDecodedOptions{Automatic: true}, api.HDRAnalysisFormatsBoth, 0, errors.New("decoder unavailable"))
		if err != nil {
			return err
		}
		defer native.Release()
		if !native.ScanComplete || native.HDR10Plus == nil || native.DolbyVision == nil || len(reports) != 2 {
			t.Fatal("unavailable decoder discarded native metadata")
		}
		for _, report := range reports {
			if report.Result.State != api.HDRDecodedUnsupported {
				t.Fatalf("unavailable decoder: %+v", report.Result)
			}
			if err := report.Validate(); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAutomaticSelectsUniqueHEVCAfterAnotherVideo(t *testing.T) {
	if os.Getenv("UPBRR_HDR_DECODE_CALIBRATION") != "1" {
		t.Skip("requires actual shared decoder")
	}
	executable, err := screenshots.ResolveFFmpegExecutable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "Synthetic.Multiple.Video.mkv")
	command := exec.CommandContext(t.Context(), executable, "-hide_banner", "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "color=s=16x16:r=25:d=4.8",
		"-i", filepath.Join("testdata", "decoded-pq-cuts-25.mkv"), "-map", "0:v:0", "-map", "1:v:0", "-c:v:0", "ffv1", "-c:v:1", "copy", "-shortest", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("author video selection fixture: %v: %s", err, output)
	}
	facts, err := InspectFileSource(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !facts.UniqueHEVC || facts.Decoded == nil || facts.Decoded.VideoStreamOrdinal != 1 {
		t.Fatal("header producer did not identify the selected HEVC")
	}
	err = New(NewAdmission(), nil).WithSession(t.Context(), func(session *Session) error {
		native, reports, err := session.AnalyzeAutomatic(t.Context(), DecodedInput{
			Path:        path,
			Fingerprint: "synthetic-multiple-video",
			Source:      *facts.Decoded,
		},
			nil, api.HDRDecodedOptions{Automatic: true}, api.HDRAnalysisFormatsAuto, directory, 0)
		if err != nil {
			return err
		}
		defer native.Release()
		if reports[0].Automatic == nil || reports[0].Result.Measured != 120 || reports[0].Result.State != api.HDRDecodedComplete {
			t.Fatalf("wrong video selected: %+v", reports[0].Result)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAutomaticMetadataFallbackProfile5(t *testing.T) {
	for _, profile := range []string{"5", "5.06", "8.01"} {
		t.Run(profile, func(t *testing.T) {
			err := New(NewAdmission(), nil).WithSession(t.Context(), func(session *Session) error {
				input := DecodedInput{
					Path: filepath.Join("testdata", "reordered-hybrid.mkv"),
					Fingerprint: "synthetic-dual",
					Source: api.HDRDecodedSourceEvidence{DolbyVisionProfile: profile},
				}
				native, reports, err := session.automaticMetadataOnly(t.Context(), input, api.HDRDecodedOptions{Automatic: true},
					api.HDRAnalysisFormatsBoth, 0, errors.New("decoder unavailable"))
				if err != nil {
					return err
				}
				defer native.Release()
				hasNote := slices.ContainsFunc(reports[0].Result.Checks, func(check api.HDRCheckResult) bool {
					return check.Code == optionalHDR10ReferenceNote().Code
				})
				if hasNote != strings.HasPrefix(profile, "5") {
					t.Fatalf("profile %s optional reference note = %v", profile, hasNote)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAutomaticDualMetadataDerivationPreservesCapture(t *testing.T) {
	err := New(NewAdmission(), nil).WithSession(t.Context(), func(session *Session) error {
		native, err := session.ExtractNativeFile(t.Context(), filepath.Join("testdata", "reordered-hybrid.mkv"), api.HDRAnalysisFormatsBoth)
		if err != nil {
			return err
		}
		defer native.Release()
		report := newMeasurementReport(api.HDRDecodedAutomatic, DecodedInput{Fingerprint: "synthetic-dual", Source: api.HDRDecodedSourceEvidence{Width: 192, Height: 112}},
			api.HDRDecodedOptions{Automatic: true})
		report.Result.Profile, report.Result.State = AutomaticMeasurementProfile, api.HDRDecodedComplete
		report.Automatic = &AutomaticMeasurement{}
		for i := range native.DolbyVision.Pictures {
			report.Automatic.Frames = append(report.Automatic.Frames, AutomaticFrame{
				PTS: measurementTimestamp(pictureTime{
					Value:     int64(i),
					Timescale: 25,
					Valid:     true,
				}),
				MaxRGBNits:  200,
				FALLNits:    100,
				BorderKnown: true,
			})
		}
		report.Result.Selected, report.Result.Measured = len(report.Automatic.Frames), len(report.Automatic.Frames)
		if err := finishAutomaticReport(t.Context(), report, native, &automaticPictures{}); err != nil {
			return err
		}
		if err := report.Validate(); err != nil {
			return err
		}
		artifacts := MeasurementArtifacts(report)
		for _, kind := range []api.HDRArtifactKind{api.HDRArtifactDVOverlay, api.HDRArtifactDVDifference, api.HDRArtifactHDR10PlusOverlay, api.HDRArtifactHDR10PlusDifference} {
			if !slices.ContainsFunc(artifacts, func(a api.HDRTypedArtifact) bool { return a.Kind == kind }) {
				t.Fatalf("missing %s", kind)
			}
		}
		if native.HDR10Plus == nil || len(native.HDR10Plus.Payloads) == 0 {
			t.Fatal("picture derivation released native metadata")
		}
		evidence, err := native.Evidence(api.HDRAnalysisFormatsBoth, api.HDRSourceEvidence{})
		if err != nil {
			return err
		}
		defer evidence.Release()
		return evidence.ValidateSummary()
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAutomaticReferenceAlignmentAndSelfReference(t *testing.T) {
	if os.Getenv("UPBRR_HDR_DECODE_CALIBRATION") != "1" {
		t.Skip("requires actual shared decoder")
	}
	input := DecodedInput{
		Path:        filepath.Join("testdata", "decoded-pq-cuts-25.mkv"),
		Fingerprint: "synthetic-primary",
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
			Transfer:               "smpte2084",
			Primaries:              "bt2020",
		},
	}
	directory := t.TempDir()
	reference := input
	reference.Path, reference.Fingerprint = filepath.Join(directory, "Synthetic.Reference.mkv"), "synthetic-reference"
	data, err := os.ReadFile(input.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reference.Path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	options := api.HDRDecodedOptions{Automatic: true, Comparison: &api.HDRComparisonOptions{
		SourceID:    "reference",
		FrameOffset: new(0),
		Crop: api.HDRCrop{
			Top:    12,
			Width:  192,
			Height: 88,
		},
		Grade:  true,
		Colour: true,
	}}
	err = New(NewAdmission(), nil).WithSession(t.Context(), func(session *Session) error {
		native, reports, err := session.AnalyzeAutomatic(t.Context(), input, &reference, options, api.HDRAnalysisFormatsAuto, directory, 0)
		if err != nil {
			return err
		}
		native.Release()
		if len(reports[0].Automatic.Reference) != 120 {
			t.Fatal("aligned reference was not retained")
		}
		for i := range reports[0].Automatic.Frames {
			values, valid := automaticCurveValues(reports[0], api.HDRArtifactAutomaticGrade, i)
			if !valid || values[0] != 0 || values[1] != 0 {
				t.Fatalf("identical grade difference: %v", values)
			}
		}
		omittedComparison := *options.Comparison
		unsupportedReference := reference
		unsupportedReference.Source.Transfer = ""
		var referenceTraversals int
		unsupportedContext := WithProgress(t.Context(), func(progress Progress) {
			if progress.Phase == "reading_reference_media" {
				referenceTraversals++
			}
		})
		native, reports, err = session.AnalyzeAutomatic(unsupportedContext, input, &unsupportedReference, options, api.HDRAnalysisFormatsAuto, directory, 0)
		if err != nil {
			return err
		}
		native.Release()
		if referenceTraversals != 0 || reports[0].Automatic == nil || len(reports[0].Automatic.Frames) != 120 ||
			len(reports[0].Automatic.Reference) != 0 || !slices.ContainsFunc(reports[0].Result.Checks,
			func(check api.HDRCheckResult) bool { return check.Code == "reference_unavailable" }) {
			t.Fatal("unsupported reference traversed media or discarded primary measurements")
		}
		omittedComparison.Crop = api.HDRCrop{Width: 2, Height: 2}
		omittedOptions := options
		omittedOptions.Comparison = &omittedComparison
		native, reports, err = session.AnalyzeAutomatic(t.Context(), input, &input, omittedOptions, api.HDRAnalysisFormatsAuto, directory, 0)
		if err != nil {
			return err
		}
		native.Release()
		if reports[0].Automatic == nil || len(reports[0].Automatic.Reference) != 0 || !slices.ContainsFunc(reports[0].Result.Checks,
			func(check api.HDRCheckResult) bool { return check.Code == "reference_unavailable" }) {
			t.Fatal("self-reference did not preserve primary checks")
		}
		if reports[0].Options.Comparison.Crop != omittedComparison.Crop || reports[0].Automatic.Frames[0].ColourPixels <= 1 ||
			!slices.ContainsFunc(reports[0].Result.Checks, func(check api.HDRCheckResult) bool {
				return check.Code == "complete_picture_measurement" && strings.Contains(check.Message, "observed active picture")
			}) {
			t.Fatal("omitted reference changed identity or misreported the unapplied crop")
		}
		// A valid file handle/header hint is not proof that FFmpeg can open or
		// decode a secondary input. That failure must stay secondary.
		if err := os.WriteFile(reference.Path, []byte("not a media stream"), 0o600); err != nil {
			return fmt.Errorf("write invalid reference fixture: %w", err)
		}
		native, reports, err = session.AnalyzeAutomatic(t.Context(), input, &reference, options, api.HDRAnalysisFormatsAuto, directory, 0)
		if err != nil {
			return err
		}
		native.Release()
		if reports[0].Automatic == nil || len(reports[0].Automatic.Frames) != 120 || len(reports[0].Automatic.Reference) != 0 {
			t.Fatal("reference process failure discarded primary measurements")
		}
		if !slices.ContainsFunc(reports[0].Result.Checks, func(check api.HDRCheckResult) bool {
			return check.Code == "complete_picture_measurement" && strings.Contains(check.Message, "explicit common crop")
		}) {
			t.Fatal("reference process failure discarded the actual applied primary crop")
		}
		options.Comparison.Crop.Width = 194
		native, reports, err = session.AnalyzeAutomatic(t.Context(), input, &reference, options, api.HDRAnalysisFormatsAuto, directory, 0)
		if err != nil {
			return err
		}
		native.Release()
		if reports[0].Automatic == nil || len(reports[0].Automatic.Frames) != 120 {
			t.Fatal("unusable reference crop suppressed supported primary measurement")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAutomaticAuthoredCompletePass(t *testing.T) {
	if os.Getenv("UPBRR_HDR_DECODE_CALIBRATION") != "1" {
		t.Skip("set UPBRR_HDR_DECODE_CALIBRATION=1 for actual shared-decoder verification")
	}
	input := DecodedInput{
		Path:        filepath.Join("testdata", "decoded-pq-cuts.mkv"),
		Fingerprint: "authored-pq-cuts",
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
	var measuredProgress atomic.Bool
	ctx := WithProgress(t.Context(), func(progress Progress) {
		if progress.Phase == "measuring_pictures" {
			measuredProgress.Store(true)
		}
	})
	err := New(NewAdmission(), nil).WithSession(ctx, func(session *Session) error {
		native, reports, err := session.AnalyzeAutomatic(ctx, input, nil, api.HDRDecodedOptions{Automatic: true}, api.HDRAnalysisFormatsAuto, t.TempDir(), 0)
		if err != nil {
			return err
		}
		defer native.Release()
		if !native.ScanComplete || native.DolbyVision == nil || len(native.DolbyVision.Pictures) != 120 {
			t.Fatalf("native metadata traversal incomplete: %+v", native)
		}
		if len(reports) != 2 || reports[0].Result.State != api.HDRDecodedComplete || reports[0].Automatic == nil || len(reports[0].Automatic.Frames) != 120 {
			t.Fatalf("automatic picture results: %+v", reports)
		}
		for _, report := range reports {
			if err := report.Validate(); err != nil {
				t.Fatalf("validate %s: %v", report.Result.Mode, err)
			}
		}
		if reports[1].Result.State != api.HDRDecodedNotRun || reports[1].Result.Reason != "no_audio" {
			t.Fatalf("audio outcome: %+v", reports[1].Result)
		}
		for _, index := range []int{12, 36, 60, 84, 108} {
			frame := reports[0].Automatic.Frames[index]
			if !frame.BorderKnown || frame.Borders != [4]int{0, 0, 12, 12} || math.Abs(frame.FALLNits-frame.MaxRGBNits) > 0.000001 || frame.SceneScore < 0.1 {
				t.Fatalf("authored frame %d: %+v", index, frame)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !measuredProgress.Load() {
		t.Fatal("picture progress remained silent for a source shorter than 1000 frames")
	}
}

func TestAutomaticRawHEVCRetainsNativeWithoutInventingTimes(t *testing.T) {
	if os.Getenv("UPBRR_HDR_DECODE_CALIBRATION") != "1" {
		t.Skip("requires actual authored HEVC")
	}
	executable, err := screenshots.ResolveFFmpegExecutable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), executable, "-hide_banner", "-nostdin", "-loglevel", "error", "-i",
		filepath.Join("testdata", "decoded-pq-cuts-25.mkv"), "-map", "0:v:0", "-c", "copy", "-f", "hevc", "pipe:1")
	data, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, extension := range []string{".hevc", ".h265", ".265"} {
		t.Run(extension, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "Synthetic.Raw"+extension)
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			facts, err := InspectFileSource(t.Context(), path)
			if err != nil || facts.Decoded == nil || !facts.Decoded.RawHEVC {
				t.Fatalf("raw-source producer: %+v %v", facts, err)
			}
			err = New(NewAdmission(), nil).WithSession(t.Context(), func(session *Session) error {
				native, reports, err := session.AnalyzeAutomatic(t.Context(), DecodedInput{
					Path:        path,
					Fingerprint: "synthetic-raw",
					Source:      *facts.Decoded,
				}, nil,
					api.HDRDecodedOptions{Automatic: true}, api.HDRAnalysisFormatsAuto, directory, 0)
				if err != nil {
					return err
				}
				defer native.Release()
				if !native.ScanComplete || native.DolbyVision == nil || len(native.DolbyVision.Pictures) != 120 || reports[0].Result.State != api.HDRDecodedUnsupported ||
					reports[1].Result.State != api.HDRDecodedNotRun || reports[1].Result.Reason != "no_audio" {
					t.Fatalf("raw best-effort capture lost native evidence: %+v %+v", native, reports)
				}
				for _, report := range reports {
					if err := report.Validate(); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAutomaticAuthoredAudioAndRetainedRendering(t *testing.T) {
	if os.Getenv("UPBRR_HDR_DECODE_CALIBRATION") != "1" {
		t.Skip("requires actual shared-decoder verification")
	}
	executable, err := screenshots.ResolveFFmpegExecutable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "Synthetic.HDR.Audio.mkv")
	command := exec.CommandContext(t.Context(), executable, "-hide_banner", "-nostdin", "-loglevel", "error", "-i", filepath.Join("testdata", "decoded-pq-cuts-25.mkv"),
		"-f", "lavfi", "-i", "sine=frequency=1000:sample_rate=48000:duration=4.8", "-map", "0:v:0", "-map", "1:a:0", "-c:v", "copy", "-c:a", "pcm_s16le", "-shortest", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("author AV fixture: %v: %s", err, output)
	}
	input := DecodedInput{
		Path:        path,
		Fingerprint: "authored-hdr-audio",
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
	err = New(NewAdmission(), nil).WithSession(t.Context(), func(session *Session) error {
		native, reports, err := session.AnalyzeAutomatic(t.Context(), input, nil, api.HDRDecodedOptions{Automatic: true}, api.HDRAnalysisFormatsAuto, directory, 0)
		if err != nil {
			return err
		}
		defer native.Release()
		if len(reports) != 2 || reports[1].Audio == nil || reports[1].Result.State != api.HDRDecodedComplete {
			t.Fatalf("shared AV outcomes: %+v", reports)
		}
		audio := reports[1].Audio
		if audio.Track.SampleRate != 48000 || audio.Track.Channels != 1 || audio.Track.SampleFrames != 230400 || len(audio.Images) != 2 || audio.Stats == "" {
			t.Fatalf("native-rate audio processing: %+v", audio.Track)
		}
		var sampledAudio *MeasurementReport
		sampledNative, sampled, err := session.AnalyzePreparedAutomatic(t.Context(), input, nil,
			api.HDRDecodedOptions{
				Automatic:           true,
				Sampled:             true,
				VideoDecoderThreads: 1,
			}, api.HDRAnalysisFormatsAuto, directory, 0, PreparedAudioCapture{
				Subject: api.AudioAnalysisSubject{
					VideoPath:           path,
					SourceFingerprint:   input.Fingerprint,
					ResourceID:          "resource",
					ManifestFingerprint: "manifest",
					PrimaryTrackID:      "primary",
					Tracks: []api.MediaTrackFacts{{
						ID:            "primary",
						ResourceID:    "resource",
						Kind:          api.MediaTrackAudio,
						Ordinal:       1,
						Codec:         "pcm",
						Channels:      1,
						SampleRate:    48000,
						ChannelLayout: "mono",
					}},
				},
				Selected:         []string{"primary"},
				CheckpointNative: func(*NativeExtraction, []*MeasurementReport) error { return nil },
				Publish:          func(report *MeasurementReport) error { sampledAudio = report; return report.Validate() },
			})
		if err != nil {
			return err
		}
		defer sampledNative.Release()
		if len(sampled) != 1 || sampledAudio == nil {
			t.Fatal("prepared picture or audio report missing")
		}
		sampled = append(sampled, sampledAudio)
		if sampled[0].Result.Measured != 5 || sampled[1].Audio == nil || sampled[1].Audio.Track.SampleFrames != audio.Track.SampleFrames ||
			sampled[1].Audio.Stats != audio.Stats {
			t.Fatal("picture sampling changed complete primary audio processing")
		}
		for i, image := range audio.Images {
			if !bytes.Equal(image.Data, sampled[1].Audio.Images[i].Data) {
				t.Fatal("picture sampling changed existing audio images")
			}
		}
		// The reports alone must survive round-trip and render every advertised
		// picture, waveform, spectrogram and stats output without media access.
		for _, report := range append(reports, sampled...) {
			var encoded bytes.Buffer
			if err := WriteMeasurement(t.Context(), &encoded, report); err != nil {
				return err
			}
			restored, err := ReadMeasurement(t.Context(), bytes.NewReader(encoded.Bytes()))
			if err != nil {
				return err
			}
			for _, artifact := range MeasurementArtifacts(restored) {
				var output bytes.Buffer
				if err := session.RenderMeasurementArtifact(t.Context(), restored, artifact.Kind, "Synthetic HDR input", &output); err != nil {
					return err
				}
				if output.Len() == 0 {
					return fmt.Errorf("empty retained artifact: %s", artifact.Kind)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLegacyAutomaticRejectsV3BeforeSourceAccess(t *testing.T) {
	for _, options := range []api.HDRDecodedOptions{
		{Automatic: true, VideoDecoderThreads: 1},
		{Audio: true, AllAudioTracks: true},
		{Audio: true, BoundarySync: true},
	} {
		err := New(NewAdmission(), nil).WithSession(t.Context(), func(session *Session) error {
			native, reports, err := session.AnalyzeAutomatic(t.Context(), DecodedInput{}, nil, options, api.HDRAnalysisFormatsAuto, "", 0)
			if err == nil || err.Error() != "v3 automatic analysis requires prepared audio authority" || native != nil || reports != nil {
				t.Fatalf("new options reached legacy source traversal: native=%v reports=%v err=%v", native, reports, err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
