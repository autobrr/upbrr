// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Audionut/go-hdr10-plus/extract"
	"github.com/autobrr/upbrr/pkg/api"
)

func testExtraction() *extract.Extraction {
	distributions := make([]extract.Distribution, 9)
	for index, percentile := range []uint8{1, 5, 10, 25, 50, 75, 90, 95, 99} {
		distributions[index] = extract.Distribution{Index: percentile, Value: uint32(index+1) * 1000}
	}
	return &extract.Extraction{
		Payloads: []extract.Payload{{
			ApplicationVersion: 1,
			NumWindows:         1,
			MaxSCL:             [3]uint32{5000, 6000, 7000},
			AverageRGB:         1000,
			Distributions:      distributions,
		}},
		Frames: []extract.Picture{{
			Stream:       extract.StreamKey{TrackID: 1},
			PTS:          extract.Timestamp{Valid: true, Timescale: 24000},
			PayloadIndex: 0,
		}},
		SceneStarts: []uint64{0},
		Profile:     "A",
	}
}

func testIdentity() ExtractionIdentity {
	return ExtractionIdentity{
		SourceFingerprint: "source",
		TargetID:          "hdr_0123456789abcdef0123456789abcdef",
		SelectionPolicy:   "unique_hevc",
		ResolvedTrackID:   1,
	}
}

func TestSidecarRoundTripAndHostileRecords(t *testing.T) {
	var encoded bytes.Buffer
	if err := WriteSidecar(t.Context(), &encoded, Sidecar{Identity: testIdentity(), Extraction: testExtraction()}); err != nil {
		t.Fatal(err)
	}
	restored, err := ReadSidecar(t.Context(), bytes.NewReader(encoded.Bytes()), testIdentity())
	if err != nil {
		t.Fatal(err)
	}
	model, err := restored.Extraction.PlotMetadata()
	if err != nil || len(model.Frames) != 1 || model.Frames[0].AverageRGB != 1000 || len(restored.Extraction.Payloads[0].Distributions) != 9 {
		t.Fatalf("restored metadata = %#v, %v", restored, err)
	}
	cases := map[string][]byte{
		"old dependencies":  bytes.Replace(encoded.Bytes(), []byte(DependencyFingerprint), []byte("hdr:7f7cad251d6b;bdinfo:f7acb336424f"), 1),
		"duplicate field":   bytes.Replace(encoded.Bytes(), []byte(`"schema":`), []byte(`"schema":"hdr-extraction-v1","schema":`), 1),
		"unknown field":     bytes.Replace(encoded.Bytes(), []byte(`"schema":`), []byte(`"unrecognized":0,"schema":`), 1),
		"schema":            bytes.Replace(encoded.Bytes(), []byte("hdr-extraction-v1"), []byte("hdr-extraction-v2"), 1),
		"frame count":       bytes.Replace(encoded.Bytes(), []byte(`"frames":1`), []byte(`"frames":500001`), 1),
		"payload reference": bytes.Replace(encoded.Bytes(), []byte(`"PayloadIndex":0`), []byte(`"PayloadIndex":1`), 1),
		"native track":      bytes.Replace(encoded.Bytes(), []byte(`"TrackID":1`), []byte(`"TrackID":2`), 1),
		"max scl shape":     bytes.Replace(encoded.Bytes(), []byte(`[5000,6000,7000]`), []byte(`[5000,6000]`), 1),
		"truncated":         encoded.Bytes()[:encoded.Len()-3],
		"trailing":          append(append([]byte{}, encoded.Bytes()...), []byte("{}")...),
		"oversized record":  []byte(strings.Repeat(" ", 8193) + "\n"),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ReadSidecar(t.Context(), bytes.NewReader(input), testIdentity()); err == nil {
				t.Fatal("hostile sidecar accepted")
			}
		})
	}
	wrong := testIdentity()
	wrong.SelectionPolicy = "unknown"
	var rejected bytes.Buffer
	if err := WriteSidecar(t.Context(), &rejected, Sidecar{Identity: wrong, Extraction: testExtraction()}); err == nil {
		t.Fatal("unknown selection policy accepted")
	}
	wrong = testIdentity()
	wrong.SourceFingerprint = "other"
	if _, err := ReadSidecar(t.Context(), bytes.NewReader(encoded.Bytes()), wrong); err == nil {
		t.Fatal("wrong source accepted")
	}
}

func TestVerifiedAbsenceSidecar(t *testing.T) {
	identity := testIdentity()
	identity.ResolvedTrackID = 0
	var encoded bytes.Buffer
	if err := WriteSidecar(t.Context(), &encoded, Sidecar{Identity: identity, Absent: true}); err != nil {
		t.Fatal(err)
	}
	result, err := ReadSidecar(t.Context(), bytes.NewReader(encoded.Bytes()), identity)
	if err != nil || !result.Absent || result.Extraction != nil {
		t.Fatalf("absence = %#v, %v", result, err)
	}
}

func TestSidecarPublicationUsesRestoreAllocationLimit(t *testing.T) {
	metadata := testExtraction()
	frame := metadata.Frames[0]
	metadata.Frames = make([]extract.Picture, 400000)
	metadata.Payloads = make([]extract.Payload, 400000)
	for index := range metadata.Frames {
		metadata.Frames[index] = frame
	}
	var output bytes.Buffer
	err := WriteSidecar(t.Context(), &output, Sidecar{Identity: testIdentity(), Extraction: metadata})
	if !errors.Is(err, extract.ErrResourceLimit) || output.Len() != 0 {
		t.Fatalf("unrestorable sidecar published: bytes=%d err=%v", output.Len(), err)
	}
	var small bytes.Buffer
	if err := WriteSidecar(t.Context(), &small, Sidecar{Identity: testIdentity(), Extraction: testExtraction()}); err != nil {
		t.Fatal(err)
	}
	oversized := bytes.Replace(small.Bytes(), []byte(`"frames":1`), []byte(`"frames":400000`), 1)
	oversized = bytes.Replace(oversized, []byte(`"payloads":1`), []byte(`"payloads":400000`), 1)
	if _, err := ReadSidecar(t.Context(), bytes.NewReader(oversized), testIdentity()); !errors.Is(err, extract.ErrResourceLimit) {
		t.Fatalf("restore allocation limit diverged: %v", err)
	}
}

func TestSidecarAllowsFeatureLengthMetadataWithinBoundedRestore(t *testing.T) {
	header := sidecarHeader{
		Identity: testIdentity(),
		Frames:   183048,
		Payloads: 183048,
		Scenes:   183048,
	}
	if err := validateSidecarHeader(header); err != nil {
		t.Fatalf("feature-length metadata rejected: %v", err)
	}
	header.Frames, header.Payloads = MaxFrames, MaxPayloads
	if err := validateSidecarHeader(header); !errors.Is(err, extract.ErrResourceLimit) {
		t.Fatalf("oversized allocation accepted: %v", err)
	}
}

func TestRenderAllEstimatorsAndCancellation(t *testing.T) {
	service := New(NewAdmission(), api.NopLogger{})
	for _, peak := range []api.HDRPeakSource{api.HDRPeakHistogram, api.HDRPeakHistogram99, api.HDRPeakMaxSCL, api.HDRPeakMaxSCLLuminance} {
		t.Run(string(peak), func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "hdr10plus.png")
			if err := service.Render(t.Context(), testExtraction(), peak, "Synthetic HDR10+", output); err != nil {
				t.Fatal(err)
			}
			file, err := os.Open(output)
			if err != nil {
				t.Fatal(err)
			}
			image, decodeErr := png.Decode(file)
			closeErr := file.Close()
			if decodeErr != nil || closeErr != nil {
				t.Fatalf("PNG decode/close = %v / %v", decodeErr, closeErr)
			}
			if image.Bounds().Dx() != 3000 || image.Bounds().Dy() != 1200 {
				t.Fatalf("dimensions = %v", image.Bounds())
			}
			for y := 0; y < 1200; y += 13 {
				for x := 0; x < 3000; x += 13 {
					_, _, _, alpha := image.At(x, y).RGBA()
					if alpha != 65535 {
						t.Fatal("plot is transparent")
					}
				}
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	output := filepath.Join(t.TempDir(), "canceled.png")
	ctx = WithProgress(ctx, func(progress Progress) {
		if progress.Phase == "rendering" {
			cancel()
		}
	})
	if err := service.Render(ctx, testExtraction(), api.HDRPeakHistogram, "Synthetic HDR10+", output); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled output exists: %v", err)
	}
	if err := service.Render(t.Context(), testExtraction(), api.HDRPeakHistogram, "Synthetic HDR10+", output); err != nil {
		t.Fatalf("admission leaked: %v", err)
	}
}

func TestPublicationPreservesExistingFilesAndFailures(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "existing.png")
	if err := os.WriteFile(target, []byte("user data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PublishFile(t.Context(), target, func(out io.Writer) error {
		if _, err := out.Write([]byte("replacement")); err != nil {
			return fmt.Errorf("write synthetic artifact: %w", err)
		}
		return nil
	}); !errors.Is(err, os.ErrExist) {
		t.Fatalf("overwrite error = %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "user data" {
		t.Fatalf("existing file changed: %q / %v", data, err)
	}
	failure := errors.New("synthetic encoder failure")
	if err := PublishFile(t.Context(), filepath.Join(root, "failed.png"), func(io.Writer) error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("encoder error = %v", err)
	}
	files, err := os.ReadDir(root)
	if err != nil || len(files) != 1 {
		t.Fatalf("unpublished files remain: %v / %v", files, err)
	}
}

func TestAdmissionCancellationAndErrorClassification(t *testing.T) {
	admission := NewAdmission()
	release, err := admission.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := admission.Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting cancellation = %v", err)
	}
	release()
	for _, cause := range []error{extract.ErrNoMetadata, extract.ErrAmbiguousTrack, extract.ErrUnsupportedInput, extract.ErrUnsupportedMetadata, extract.ErrInvalidBitstream, extract.ErrIncomplete, extract.ErrResourceLimit, io.ErrUnexpectedEOF, context.Canceled, context.DeadlineExceeded} {
		classified := Classify(cause)
		if !errors.Is(classified, cause) {
			t.Fatalf("lost cause: %v", classified)
		}
		if failure, ok := api.AsHDRAnalysisFailure(classified); !ok || failure.Validate() != nil {
			t.Fatalf("invalid classification: %#v", classified)
		}
	}
	if failure, _ := api.AsHDRAnalysisFailure(Classify(context.DeadlineExceeded)); failure.Code != api.HDRAnalysisFailureInterrupted {
		t.Fatal("deadline was classified as user cancellation")
	}
}

type diagnosticLoggerFixture struct {
	api.NopLogger
	warnings []string
}

func (l *diagnosticLoggerFixture) Warnf(format string, args ...any) {
	l.warnings = append(l.warnings, fmt.Sprintf(format, args...))
}

func TestRetainedFailureDiagnosticsPreserveQuotaAndRedactPrivateDetails(t *testing.T) {
	logger := &diagnosticLoggerFixture{}
	service := New(NewAdmission(), logger)
	cause := fmt.Errorf("parent retained storage: request=187441280 used=417571696 limit=536870912: %w", extract.ErrResourceLimit)
	classified := Classify(cause)
	service.ReportFailure(t.Context(), "reading_metadata", fmt.Errorf("file target: %w", classified))
	if len(logger.warnings) != 1 || !strings.Contains(logger.warnings[0], "stage=reading_metadata") ||
		!strings.Contains(logger.warnings[0], "request=187441280 used=417571696 limit=536870912") {
		t.Fatalf("quota diagnostics lost: %v", logger.warnings)
	}
	if failure, _ := api.AsHDRAnalysisFailure(classified); strings.Contains(failure.Message, "187441280") || !errors.Is(classified, cause) {
		t.Fatalf("public failure exposes diagnostics or loses cause: %v", classified)
	}
	service.ReportFailure(t.Context(), "saving_metadata", errors.New("request failed https://operator:private-password@example.invalid/?token=private-token"))
	if strings.Contains(logger.warnings[1], "private-password") || strings.Contains(logger.warnings[1], "private-token") {
		t.Fatalf("private diagnostics were not sanitized: %v", logger.warnings)
	}
}
