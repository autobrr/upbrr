// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestAutomaticHDRWorkflowRetainedReplayAndMissingEvidence(t *testing.T) {
	for _, sampled := range []bool{false, true} {
		name := "full"
		if sampled {
			name = "sampled"
		}
		t.Run(name, func(t *testing.T) { testAutomaticHDRWorkflowRetainedReplay(t, sampled, false) })
	}
	t.Run("metadata_audio", func(t *testing.T) { testAutomaticHDRWorkflowRetainedReplay(t, false, true) })
}

func testAutomaticHDRWorkflowRetainedReplay(t *testing.T, sampled, audioOnly bool) {
	t.Helper()
	if os.Getenv("UPBRR_HDR_DECODE_CALIBRATION") != "1" {
		t.Skip("requires actual shared decoder")
	}
	builder, resolver, _ := hdrFixture(t)
	data, err := os.ReadFile(filepath.Join("..", "services", "hdranalysis", "testdata", "decoded-pq-cuts-25.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "Synthetic.HDR.mkv")
	if err := os.WriteFile(source, data, 0o600); err != nil {
		t.Fatal(err)
	}
	executable, err := screenshots.ResolveFFmpegExecutable()
	if err != nil {
		t.Fatal(err)
	}
	withAudio := filepath.Join(t.TempDir(), "Synthetic.HDR.Audio.mkv")
	command := exec.CommandContext(t.Context(), executable, "-hide_banner", "-nostdin", "-loglevel", "error", "-i", source, "-f", "lavfi", "-i",
		"sine=frequency=1000:sample_rate=48000:duration=4.8", "-map", "0:v:0", "-map", "1:a:0", "-c:v", "copy", "-c:a", "pcm_s24le", withAudio)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("author primary audio fixture: %v %s", err, output)
	}
	source = withAudio
	resolver.subject.Targets[0].Path, resolver.subject.Targets[0].DiscRoot = source, ""
	resolver.subject.Targets[0].Target.Evidence = api.HDRSourceEvidence{UniqueHEVC: true, Decoded: &api.HDRDecodedSourceEvidence{
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
	}}
	release := resolver.subject.Release
	request := api.HDRAnalysisInstructions{
		Release:        release,
		TargetIDs:      []string{resolver.subject.Targets[0].Target.ID},
		ProfileVersion: api.HDRAnalysisProfileV2,
		Decoded: &api.HDRDecodedOptions{
			Automatic: !audioOnly,
			Audio:     audioOnly,
			Sampled:   sampled,
		},
	}
	referencePath := filepath.Join(t.TempDir(), "Synthetic.Reference.mkv")
	if err := os.WriteFile(referencePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	verified, err := releaseworkflow.VerifyHDRComparisonSource(t.Context(), referencePath, verifyWorkflowInput)
	if err != nil {
		t.Fatal(err)
	}
	const comparisonID = "synthetic-reference"
	if !audioOnly {
		request.Decoded.Comparison = &api.HDRComparisonOptions{
			SourceID:    comparisonID,
			FrameOffset: new(0),
			Crop: api.HDRCrop{
				Top:    12,
				Width:  192,
				Height: 88,
			},
			Grade:  true,
			Colour: true,
		}
	}
	entries := make(map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord)
	resources := make(map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource)
	publish := func(record releaseworkflow.HDRExtractionRecord, resource releaseworkflow.RetainedHDRExtractionResource) error {
		entries[record.ID], resources[record.ID] = record, resource
		return nil
	}
	store := releaseworkflow.HDRMeasurementStore{
		Records:   make(map[string]releaseworkflow.HDRMeasurementRecord),
		Resources: make(map[string]releaseworkflow.RetainedHDRMeasurementResource),
		Comparison: &releaseworkflow.HDRComparisonSource{
			Ref: api.HDRComparisonSourceRef{
				ID:          comparisonID,
				Release:     release,
				Fingerprint: verified.SourceVersion,
			},
			Path:     verified.CanonicalPath,
			Evidence: resolver.subject.Targets[0].Target.Evidence,
		},
	}
	store.Publish = func(record releaseworkflow.HDRMeasurementRecord, resource releaseworkflow.RetainedHDRMeasurementResource) error {
		if len(entries) != 1 {
			t.Fatal("automatic measurement preceded native checkpoint")
		}
		if _, err := restoreHDRMeasurement(t.Context(), record, resource); err != nil {
			return err
		}
		store.Records[record.ID], store.Resources[record.ID] = record, resource
		return nil
	}
	first, firstResource, err := builder.Build(t.Context(), releaseworkflow.HDRPreparation{Release: release}, request,
		"automatic-first", time.Now().UTC(), nil, nil, entries, resources, publish, &store)
	expectedRecords := 2
	if audioOnly {
		expectedRecords = 1
	}
	if err != nil || len(store.Records) != expectedRecords || len(first.Targets) != 1 || len(first.Targets[0].Decoded) != expectedRecords {
		t.Fatalf("automatic result=%+v records=%d err=%v", first, len(store.Records), err)
	}
	if first.Targets[0].Decoded[0].State != api.HDRDecodedComplete || len(first.Targets[0].Decoded[0].Artifacts) < 2 {
		t.Fatalf("automatic plots missing: %+v", first.Targets[0])
	}
	if !first.CanIncludeImages() || !first.CanIncludeDefaultImages() {
		t.Fatal("successful primary audio must be included without native dynamic metadata")
	}
	defaults := first.Targets[0].DescriptionImages()
	if len(defaults) != 2 || defaults[0].Kind != api.HDRArtifactAudioWaveform || defaults[1].Kind != api.HDRArtifactAudioSpectrogram {
		t.Fatalf("default audio images or picture opt-in: %+v", defaults)
	}
	first.ID, first.WorkflowID, first.Revision = "automatic", "workflow", 1
	if err := first.Validate(); err != nil {
		t.Fatal(err)
	}
	statsFound := false
	for _, artifact := range first.Targets[0].Artifacts {
		if artifact.Kind != api.HDRArtifactAudioStats {
			continue
		}
		statsFound = true
		for _, selectable := range first.Targets[0].SelectableImages() {
			if selectable.ID == artifact.ID {
				t.Fatal("statistics were offered as an uploadable image")
			}
		}
		content, err := firstResource.OpenArtifact(t.Context(), first, artifact.ID)
		if err != nil {
			t.Fatal(err)
		}
		text, readErr := io.ReadAll(content.Body)
		closeErr := content.Body.Close()
		if readErr != nil || closeErr != nil || content.ContentType != "text/plain" || content.Filename != "audio-stats.txt" ||
			!strings.Contains(string(text), "Num samples") {
			t.Fatalf("retained audio stats failed: read=%v close=%v bytes=%d", readErr, closeErr, len(text))
		}
	}
	if !statsFound {
		t.Fatal("shared primary audio statistics missing")
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	// Lose the optional handle and media, while preserving the exact original
	// options/measurement identity. Recovery must keep A and audio without IO.
	store.Comparison = nil
	if err := os.Remove(referencePath); err != nil {
		t.Fatal(err)
	}
	// Reopening copies every published artifact, including reference evidence,
	// without granting access to the old comparison handle or numeric reports.
	reopenedRequest := request
	reopenedRequest.Release.Generation++
	reopenedRequest.Decoded = nil
	t.Run("changed_rendering_title", func(t *testing.T) {
		originalTitle := resolver.subject.Title
		resolver.subject.Title = "Revised synthetic title"
		defer func() { resolver.subject.Title = originalTitle }()
		copied, resource, err := builder.Build(t.Context(), releaseworkflow.HDRPreparation{Release: reopenedRequest.Release}, reopenedRequest,
			"title-reopened", time.Now().UTC(), &first, firstResource, nil, nil, nil,
			&releaseworkflow.HDRMeasurementStore{RetainedArtifactsOnly: true})
		if err != nil || resource == nil || copied.RenderFingerprint != first.RenderFingerprint ||
			len(copied.Targets[0].Artifacts) != len(first.Targets[0].Artifacts) {
			t.Fatalf("changed title discarded frozen outputs or their provenance: %+v %v", copied, err)
		}
	})
	t.Run("artifactless_terminal_sibling", func(t *testing.T) {
		originalTargets := resolver.subject.Targets
		defer func() { resolver.subject.Targets = originalTargets }()
		failed := first.Targets[0]
		failed.TargetID = api.HDRTargetID(first.Release.SourcePath, "00002.MPLS")
		failed.Artifacts = nil
		failed.Decoded = slices.Clone(failed.Decoded)
		for i := range failed.Decoded {
			failed.Decoded[i].State = api.HDRDecodedCanceled
			failed.Decoded[i].Reason = "render_canceled"
			failed.Decoded[i].Selected, failed.Decoded[i].Measured = 0, 0
			failed.Decoded[i].Artifacts = nil
		}
		failed.Status = failed.AnalysisStatus()
		prior := first
		prior.TargetIDs = append(slices.Clone(first.TargetIDs), failed.TargetID)
		prior.Targets = append(slices.Clone(first.Targets), failed)
		prior.Status = api.NativeHDRAggregate(prior.Targets)
		if err := prior.Validate(); err != nil {
			t.Fatal(err)
		}
		terminal := originalTargets[0]
		terminal.Target.ID = failed.TargetID
		resolver.subject.Targets = append(slices.Clone(originalTargets), terminal)
		request := reopenedRequest
		request.TargetIDs = slices.Clone(prior.TargetIDs)
		copied, resource, err := builder.Build(t.Context(), releaseworkflow.HDRPreparation{Release: request.Release}, request,
			"partial-reopened", time.Now().UTC(), &prior, firstResource, nil, nil, nil,
			&releaseworkflow.HDRMeasurementStore{RetainedArtifactsOnly: true})
		if err != nil || resource == nil || len(copied.Targets) != 2 || len(copied.Targets[0].Artifacts) != len(first.Targets[0].Artifacts) ||
			len(copied.Targets[1].Artifacts) != 0 || copied.Targets[1].Status != failed.Status {
			t.Fatalf("terminal target discarded its saved sibling: %+v %v", copied, err)
		}
		copied.ID, copied.WorkflowID, copied.Revision = "partial-copy", "new-workflow", 1
		if err := copied.Validate(); err != nil {
			t.Fatal(err)
		}
	})
	reopened, reopenedResource, err := builder.Build(t.Context(), releaseworkflow.HDRPreparation{Release: reopenedRequest.Release}, reopenedRequest,
		"automatic-reopened", time.Now().UTC(), &first, firstResource, nil, nil, nil,
		&releaseworkflow.HDRMeasurementStore{RetainedArtifactsOnly: true})
	if err != nil || reopenedResource == nil || len(reopened.Targets[0].Artifacts) != len(first.Targets[0].Artifacts) {
		t.Fatalf("retained artifact reopening: %+v %v", reopened, err)
	}
	reopened.ID, reopened.WorkflowID, reopened.Revision = "reopened", "new-workflow", 1
	if err := reopened.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := first.Validate(); err != nil {
		t.Fatalf("copy mutated the prior decoded artifact references: %v", err)
	}
	for i, artifact := range reopened.Targets[0].Artifacts {
		before, err := firstResource.LocalArtifactPath(first, first.Targets[0].Artifacts[i].ID)
		if err != nil {
			t.Fatal(err)
		}
		after, err := reopenedResource.LocalArtifactPath(reopened, artifact.ID)
		if err != nil {
			t.Fatal(err)
		}
		beforeFile, beforeErr := hdrFileIntegrity(before)
		afterFile, afterErr := hdrFileIntegrity(after)
		if beforeErr != nil || afterErr != nil || beforeFile.SHA256 != afterFile.SHA256 || artifact.Kind != first.Targets[0].Artifacts[i].Kind ||
			before == after {
			t.Fatalf("copied artifact changed: %s before=%v after=%v", artifact.Kind, beforeErr, afterErr)
		}
	}
	// Drop published images and prior result: rebuilding must use the numeric
	// checkpoints and never implicitly reopen or decode unavailable media.
	second, _, err := builder.Build(t.Context(), releaseworkflow.HDRPreparation{Release: release}, request,
		"automatic-replay", time.Now().UTC(), nil, nil, entries, resources, publish, &store)
	if err != nil || len(entries) != 1 || len(store.Records) != expectedRecords || len(second.Targets[0].Decoded[0].Artifacts) < 2 {
		t.Fatalf("source-free replay=%+v records=%d err=%v", second, len(store.Records), err)
	}
	for _, artifact := range second.Targets[0].Artifacts {
		if artifact.Kind == api.HDRArtifactReferencePicturePlot || artifact.Kind == api.HDRArtifactAutomaticGrade ||
			artifact.Kind == api.HDRArtifactAutomaticColour {
			t.Fatal("recovery kept unverifiable reference plots")
		}
	}
	clear(store.Resources)
	missing, _, err := builder.Build(t.Context(), releaseworkflow.HDRPreparation{Release: release}, request,
		"automatic-missing", time.Now().UTC(), nil, nil, entries, resources, publish, &store)
	if err != nil || len(entries) != 1 || len(store.Records) != expectedRecords {
		t.Fatalf("missing checkpoint silently rescanned: %+v %v", missing, err)
	}
	for _, result := range missing.Targets[0].Decoded {
		if result.State != api.HDRDecodedFailed || result.Reason != "resource_unavailable" {
			t.Fatalf("missing outcome: %+v", result)
		}
	}
	image := first.Targets[0].SelectableImages()[0]
	path, err := firstResource.LocalArtifactPath(first, image.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("damaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = builder.Build(t.Context(), releaseworkflow.HDRPreparation{Release: reopenedRequest.Release}, reopenedRequest,
		"automatic-damaged-reopen", time.Now().UTC(), &first, firstResource, nil, nil, nil,
		&releaseworkflow.HDRMeasurementStore{RetainedArtifactsOnly: true})
	if failure, ok := api.AsHDRAnalysisFailure(err); !ok || failure.Code != api.HDRAnalysisFailureResourceUnavailable {
		t.Fatalf("damaged retained image silently reanalyzed media: %v", err)
	}
}

func TestAutomaticHDRRetryKeepsSuccessfulTargetSourceFree(t *testing.T) {
	if os.Getenv("UPBRR_HDR_DECODE_CALIBRATION") != "1" {
		t.Skip("requires actual shared decoder")
	}
	builder, resolver, _ := hdrFixture(t)
	data, err := os.ReadFile(filepath.Join("..", "services", "hdranalysis", "testdata", "decoded-pq-cuts-25.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	original := resolver.subject.Targets[0]
	resolver.subject.Targets = nil
	request := api.HDRAnalysisInstructions{
		Release:        resolver.subject.Release,
		ProfileVersion: api.HDRAnalysisProfileV2,
		Decoded:        &api.HDRDecodedOptions{Automatic: true},
	}
	for _, name := range []string{"First.mkv", "Second.mkv"} {
		target := original
		target.Path = filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(target.Path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		target.Target.ID = api.HDRTargetID(target.Path, "")
		target.Target.Evidence, err = hdranalysis.InspectFileSource(t.Context(), target.Path)
		if err != nil {
			t.Fatal(err)
		}
		resolver.subject.Targets = append(resolver.subject.Targets, target)
		request.TargetIDs = append(request.TargetIDs, target.Target.ID)
	}
	entries := make(map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord)
	resources := make(map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource)
	publish := func(record releaseworkflow.HDRExtractionRecord, resource releaseworkflow.RetainedHDRExtractionResource) error {
		entries[record.ID], resources[record.ID] = record, resource
		return nil
	}
	store := releaseworkflow.HDRMeasurementStore{
		Records:   make(map[string]releaseworkflow.HDRMeasurementRecord),
		Resources: make(map[string]releaseworkflow.RetainedHDRMeasurementResource),
	}
	store.Publish = func(record releaseworkflow.HDRMeasurementRecord, resource releaseworkflow.RetainedHDRMeasurementResource) error {
		store.Records[record.ID], store.Resources[record.ID] = record, resource
		return nil
	}
	first, owned, err := builder.Build(
		t.Context(),
		releaseworkflow.HDRPreparation{Release: request.Release},
		request,
		"mixed-first",
		time.Now(),
		nil,
		nil,
		entries,
		resources,
		publish,
		&store,
	)
	if err != nil || len(first.Targets) != 2 || len(entries) != 2 {
		t.Fatalf("initial targets: %+v %v", first, err)
	}
	for _, target := range first.Targets {
		if target.Decoded[0].State != api.HDRDecodedComplete {
			t.Fatalf("initial decoded result: %+v", target.Decoded)
		}
	}
	for id, record := range store.Records {
		if record.TargetID == request.TargetIDs[1] && record.Result.Mode == api.HDRDecodedAutomatic {
			delete(store.Resources, id)
		}
	}
	partial, partialOwned, err := builder.Build(
		t.Context(),
		releaseworkflow.HDRPreparation{Release: request.Release},
		request,
		"mixed-partial",
		time.Now(),
		&first,
		owned,
		entries,
		resources,
		publish,
		&store,
	)
	if err != nil || partial.Targets[0].Decoded[0].State != api.HDRDecodedComplete || partial.Targets[1].Decoded[0].State != api.HDRDecodedFailed {
		t.Fatalf("mixed result: %+v %v", partial, err)
	}
	if err := os.Remove(resolver.subject.Targets[0].Path); err != nil {
		t.Fatal(err)
	}
	request.DecodedRetry = &api.HDRDecodedRetry{AnalysisAttemptID: partial.AttemptID, Modes: []api.HDRDecodedMode{api.HDRDecodedAutomatic}}
	retry, _, err := builder.Build(
		t.Context(),
		releaseworkflow.HDRPreparation{Release: request.Release},
		request,
		"mixed-retry",
		time.Now(),
		&partial,
		partialOwned,
		entries,
		resources,
		publish,
		&store,
	)
	if err != nil || len(retry.Targets) != 2 {
		t.Fatalf("retry: %+v %v", retry, err)
	}
	for _, target := range retry.Targets {
		if target.Decoded[0].State != api.HDRDecodedComplete {
			t.Fatalf("retry lost successful target: %+v", target.Decoded)
		}
	}
	if len(entries) != 2 || len(store.Records) != 5 {
		t.Fatalf("retry reread successful sibling: native=%d measurements=%d", len(entries), len(store.Records))
	}
}
