// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestHDRLargeReportsReplayAndKeepTypedArtifactBounds(t *testing.T) {
	builder, resolver, _ := hdrFixture(t)
	release := resolver.subject.Release
	const count = 120000
	report := &hdranalysis.MeasurementReport{
		Schema:     "hdr-measurement-v1",
		Dependency: hdranalysis.NativeDependencyFingerprint,
		Sources:    [2]string{"synthetic-full-picture", ""},
		Raster:     [2]int{192, 112},
		Options:    api.HDRDecodedOptions{Automatic: true},
		Result: api.HDRDecodedResult{
			Mode:     api.HDRDecodedAutomatic,
			Profile:  hdranalysis.AutomaticMeasurementProfile,
			State:    api.HDRDecodedComplete,
			Selected: count,
			Measured: count,
		},
		Automatic: &hdranalysis.AutomaticMeasurement{Frames: make([]hdranalysis.AutomaticFrame, count)},
	}
	for index := range report.Automatic.Frames {
		report.Automatic.Frames[index] = hdranalysis.AutomaticFrame{
			PTS: hdranalysis.MeasurementTimestamp{
				Value:     strconv.Itoa(index),
				Timescale: "25",
				Known:     true,
			},
			MaxRGBNits: 100,
			FALLNits:   50,
		}
	}
	record, checkpoint, err := builder.retainMeasurement(t.Context(), releaseworkflow.HDRExtractionRecord{
		Release:           release,
		ID:                "synthetic-native",
		TargetID:          resolver.subject.Targets[0].Target.ID,
		SourceFingerprint: report.Sources[0],
	}, "synthetic-measurement", "large-measurement", report)
	if err != nil {
		t.Fatal(err)
	}
	if record.Size <= 32<<20 {
		t.Fatalf("fixture did not exceed the previous opener limit: bytes=%d", record.Size)
	}
	reader, err := checkpoint.OpenMeasurement(t.Context(), record)
	if err != nil {
		t.Fatalf("reopen valid large checkpoint: %v", err)
	}
	restored, readErr := hdranalysis.ReadMeasurement(t.Context(), reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || restored == nil || len(restored.Automatic.Frames) != count {
		t.Fatalf("source-free large checkpoint replay: read=%v close=%v", readErr, closeErr)
	}
	tooLarge := record
	tooLarge.Size = hdranalysis.MaxAutomaticMeasurementBytes + 1
	if _, err := checkpoint.OpenMeasurement(t.Context(), tooLarge); !errors.Is(err, releaseworkflow.ErrPrivateResourceIntegrity) {
		t.Fatalf("accepted oversized checkpoint: %v", err)
	}

	directory, err := hdrAttemptRoot(builder.root, release, "hdr-analysis", "large-analysis")
	if err != nil {
		t.Fatal(err)
	}
	if err := createHDRDirectory(directory); err != nil {
		t.Fatal(err)
	}
	artifact := api.HDRTypedArtifact{
		ID:      "large-report",
		Kind:    api.HDRArtifactStructuralReport,
		MIME:    "application/json",
		Profile: api.HDRAnalysisProfileV2,
	}
	target := api.HDRAnalysisTargetResult{Artifacts: []api.HDRTypedArtifact{artifact}}
	path := filepath.Join(directory, artifact.Filename())
	if err := hdranalysis.PublishFile(t.Context(), path, func(out io.Writer) error {
		return (&hdranalysis.NativeEvidence{}).WriteReport(t.Context(), out, api.HDRPeakHistogram, "", target, []*hdranalysis.MeasurementReport{report})
	}); err != nil {
		t.Fatal(err)
	}
	file, err := hdrFileIntegrity(path)
	if err != nil {
		t.Fatal(err)
	}
	if file.Size <= 32<<20 {
		t.Fatalf("structural producer did not exceed previous opener limit: bytes=%d", file.Size)
	}
	resource := hdrManagedResource{
		Root:      builder.root,
		Directory: directory,
		Kind:      "analysis",
		Files:     map[api.PublicResourceID]hdrManagedFile{artifact.ID: file},
	}
	snapshot := api.HDRAnalysisResult{
		Release:        release,
		AttemptID:      "large-analysis",
		ProfileVersion: api.HDRAnalysisProfileV2,
		Targets:        []api.HDRAnalysisTargetResult{target},
	}
	content, err := resource.OpenArtifact(t.Context(), snapshot, artifact.ID)
	if err != nil {
		t.Fatalf("open valid large structural download: %v", err)
	}
	bytesRead, copyErr := io.Copy(io.Discard, content.Body)
	closeErr = content.Body.Close()
	if copyErr != nil || closeErr != nil || bytesRead != file.Size || content.ContentType != "application/json" {
		t.Fatalf("large structural download: copy=%v close=%v bytes=%d", copyErr, closeErr, bytesRead)
	}
	oversized := file
	oversized.Size = hdranalysis.MaxReportBytes + 1
	resource.Files[artifact.ID] = oversized
	if _, err := resource.OpenArtifact(t.Context(), snapshot, artifact.ID); !errors.Is(err, releaseworkflow.ErrPrivateResourceIntegrity) {
		t.Fatalf("accepted oversized structural download: %v", err)
	}

	// A valid PNG header followed by padding must retain its own image bound.
	imagePath := filepath.Join(directory, "large-picture.png")
	imageFile, err := os.OpenFile(imagePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	width, height := api.HDRArtifactPicturePlot.ImageDimensions()
	encodeErr := png.Encode(imageFile, image.NewRGBA(image.Rect(0, 0, width, height)))
	truncateErr := imageFile.Truncate(32<<20 + 1)
	closeErr = imageFile.Close()
	if err := errors.Join(encodeErr, truncateErr, closeErr); err != nil {
		t.Fatal(err)
	}
	imageIntegrity, err := hdrFileIntegrity(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	imageArtifact := api.HDRTypedArtifact{
		ID:      "large-picture",
		Kind:    api.HDRArtifactPicturePlot,
		MIME:    "image/png",
		Profile: api.HDRAnalysisProfileV2,
		Width:   width,
		Height:  height,
	}
	snapshot.Targets[0].Artifacts = []api.HDRTypedArtifact{imageArtifact}
	resource.Files[imageArtifact.ID] = imageIntegrity
	if _, err := resource.OpenArtifact(t.Context(), snapshot, imageArtifact.ID); !errors.Is(err, releaseworkflow.ErrPrivateResourceIntegrity) {
		t.Fatalf("raised the individual PNG bound: %v", err)
	}
}

func TestHDRPrivateResourceIndexSupportsAllTrackArtifacts(t *testing.T) {
	builder, resolver, _ := hdrFixture(t)
	directory, err := hdrAttemptRoot(builder.root, resolver.subject.Release, "hdr-analysis", "all-track-index")
	if err != nil {
		t.Fatal(err)
	}
	if err := createHDRDirectory(directory); err != nil {
		t.Fatal(err)
	}
	resource := hdrManagedResource{
		Root:      builder.root,
		Directory: directory,
		Kind:      "analysis",
		Files:     make(map[api.PublicResourceID]hdrManagedFile),
	}
	for i := range api.HDRAnalysisMaxTargets * 216 {
		resource.Files[api.PublicResourceID("artifact-"+strconv.Itoa(i))] = hdrManagedFile{
			Path:   filepath.Join(directory, "track-"+strconv.Itoa(i)+"-audio_waveform.png"),
			Size:   123,
			SHA256: strings.Repeat("a", 64),
		}
	}
	_, encoded, err := resource.MarshalPrivateResource()
	if err != nil || len(encoded) <= 64<<10 {
		t.Fatalf("large resource fixture: %d %v", len(encoded), err)
	}
	if _, err := hdrResourceCodec(builder.root, "analysis").Decode(encoded); err != nil {
		t.Fatalf("maximum artifact index rejected: %v", err)
	}
	resource.Files["extra"] = hdrManagedFile{}
	_, encoded, err = resource.MarshalPrivateResource()
	if err == nil {
		if _, err := hdrResourceCodec(builder.root, "analysis").Decode(encoded); err == nil {
			t.Fatal("unbounded artifact map accepted")
		}
	}
}
