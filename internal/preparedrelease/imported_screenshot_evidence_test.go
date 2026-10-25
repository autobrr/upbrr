// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"context"
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

type importedScreenshotCollector struct{ recordingCollector }

func (c *importedScreenshotCollector) Collect(ctx context.Context, request preparationstate.Request) (CollectedFacts, error) {
	facts, err := c.recordingCollector.Collect(ctx, request)
	if err != nil {
		return facts, err
	}
	facts.ImportedScreenshotEvidence = &api.ImportedScreenshotEvidence{
		Tracker:                "AITHER",
		TrackerID:              "42",
		SourceFingerprint:      request.SourceFingerprint,
		PolicyVersion:          api.ImportedScreenshotEvidencePolicyVersion,
		DescriptionFingerprint: "original-description-images",
		References:             []api.ImportedScreenshotReference{{RawURL: "https://images.example/full"}},
	}
	return facts, nil
}

func TestPreparedImportedScreenshotReceiptSurvivesHydrationWithoutRecollection(t *testing.T) {
	path := writePreparedTestFile(t, "Synthetic.mkv", "synthetic media")
	store := newMemoryStore()
	input := api.PrepareInput{SourcePath: path}
	prepared, err := newTestModule(t, store, &importedScreenshotCollector{}).Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	hydrator := newClientEvidenceTestCollector(clientEvidenceTestSnapshot("different-client-result"))
	restarted := newTestModule(t, store, hydrator)
	reused, err := restarted.Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	ref := api.ReleaseRef{SourcePath: path, Generation: reused.Release.Generation}
	subject, err := restarted.ResolveUploadSubject(t.Context(), api.UploadSubjectInput{Release: ref})
	if err != nil {
		t.Fatal(err)
	}
	if subject.CurrentImportedScreenshotEvidence() == nil || reused.Release.Generation != prepared.Release.Generation || hydrator.collectCount() != 0 {
		t.Fatal("restart lost receipt or recollected mutable source metadata")
	}
	input.Force = true
	newRelease, err := restarted.Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if newRelease.Release.ProviderMetadata.ImportedScreenshotEvidence != nil {
		t.Fatal("missing current receipt resurrected predecessor authority")
	}
	old, err := restarted.ResolveUploadSubject(t.Context(), api.UploadSubjectInput{Release: ref})
	if err == nil && old.CurrentImportedScreenshotEvidence() != nil {
		t.Fatal("superseded generation retained import authority")
	}
}

func TestCollectedImportedScreenshotEvidenceIsDetached(t *testing.T) {
	state := preparationstate.State{TrackerData: []api.TrackerMetadata{{ImportedScreenshotEvidence: &api.ImportedScreenshotEvidence{
		Tracker:                "AITHER",
		TrackerID:              "42",
		SourceFingerprint:      "source",
		PolicyVersion:          api.ImportedScreenshotEvidencePolicyVersion,
		DescriptionFingerprint: "original-description-images",
		References:             []api.ImportedScreenshotReference{{RawURL: "https://images.example/full"}},
	}}}}
	facts := mapCollectedFacts(state)
	if facts.ImportedScreenshotEvidence == nil {
		t.Fatal("current receipt missing")
	}
	state.TrackerData[0].ImportedScreenshotEvidence.References[0].RawURL = "changed"
	if facts.ImportedScreenshotEvidence.References[0].RawURL == "changed" {
		t.Fatal("prepared receipt aliases collection")
	}
}
