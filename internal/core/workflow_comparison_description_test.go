// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/comparisons"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func testComparisonDescriptionMedia(t *testing.T, reuse bool) (api.MediaArtifactSet, workflowMediaPrivateArtifacts) {
	t.Helper()
	root := t.TempDir()
	media := api.MediaArtifactSet{}
	retained := workflowMediaPrivateArtifacts{
		ArtifactImages: make(map[api.PublicResourceID]api.ScreenshotImage),
		HostedImages:   make(map[api.PublicResourceID]api.UploadedImageLink),
		HostedSources:  make(map[api.PublicResourceID]api.PublicResourceID),
	}
	var inputs []comparisons.ImportedImage
	for row := 1; row <= 2; row++ {
		for column, label := range []string{"Source", "Encode", "Filtered"} {
			id := api.PublicResourceID(fmt.Sprintf("comparison-%d-%d", row, column+1))
			pixels := image.NewRGBA(image.Rect(0, 0, 2, 2))
			pixels.SetRGBA(0, 0, color.RGBA{
				R: uint8(row * 60),
				G: uint8(column * 70),
				B: 100,
				A: 255,
			})
			var content bytes.Buffer
			if err := png.Encode(&content, pixels); err != nil {
				t.Fatal(err)
			}
			data := content.Bytes()
			path := filepath.Join(root, string(id)+".png")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			inputs = append(inputs, comparisons.ImportedImage{
				ID:    string(id),
				Name:  fmt.Sprintf("%02d-%02d-%s.png", row, column+1, label),
				Bytes: data,
			})
			sum := sha256.Sum256(data)
			digest := hex.EncodeToString(sum[:])
			retained.ArtifactImages[id] = api.ScreenshotImage{
				Path:          path,
				Purpose:       api.ScreenshotPurposeComparison,
				ContentSHA256: digest,
			}
			media.Artifacts = append(media.Artifacts, api.MediaArtifact{
				ID:       id,
				Kind:     api.MediaArtifactComparison,
				Purpose:  api.ScreenshotPurposeComparison,
				Selected: true,
				Order:    (row-1)*3 + column,
			})
			hostedID := api.PublicResourceID("hosted-" + string(id))
			retained.HostedImages[hostedID] = api.UploadedImageLink{
				ImagePath:     path,
				Purpose:       api.ScreenshotPurposeComparison,
				ContentSHA256: digest,
				Host:          "imgbb",
				UsageScope:    "global",
				RawURL:        "https://images.example.invalid/" + string(id) + ".png",
			}
			retained.HostedSources[hostedID] = id
			media.Artifacts = append(media.Artifacts, api.MediaArtifact{
				ID:       hostedID,
				Kind:     api.MediaArtifactHostedImage,
				Purpose:  api.ScreenshotPurposeComparison,
				Selected: true,
				Source:   string(id),
			})
		}
	}
	plan, problems := comparisons.Import(inputs, "02")
	if len(problems) != 0 {
		t.Fatalf("fixture comparison import: %+v", problems)
	}
	plan.ReuseMainScreenshots = reuse
	fingerprint, err := comparisons.ReviewFingerprint(plan)
	if err != nil {
		t.Fatal(err)
	}
	plan.Fingerprint, plan.ApprovedFingerprint = fingerprint, fingerprint
	media.Comparisons = &plan
	return media, retained
}

func TestComparisonDescriptionProjectionRetainsCellIdentity(t *testing.T) {
	t.Parallel()
	for _, reuse := range []bool{false, true} {
		t.Run(fmt.Sprintf("reuse=%t", reuse), func(t *testing.T) {
			media, retained := testComparisonDescriptionMedia(t, reuse)
			for index := range media.Artifacts {
				if media.Artifacts[index].ID == "comparison-1-2" {
					media.Artifacts[index].SourceUnavailable = true
				}
			}
			exact, err := resolveWorkflowExactMedia(t.Context(), retained, media, config.ScreenshotHandlingConfig{})
			if err != nil {
				t.Fatal(err)
			}
			if len(exact.ComparisonImages) != 6 || len(exact.ComparisonUploads) != 6 || exact.Comparisons.MainSourceID != "2" {
				t.Fatalf("comparison row/column identity was flattened: %+v", exact)
			}
			wantScreenshots := 0
			if reuse {
				wantScreenshots = 2
			}
			if len(exact.Screenshots) != wantScreenshots || len(exact.ScreenshotUploads) != wantScreenshots {
				t.Fatalf("ordinary screenshots = %d/%d, want %d", len(exact.Screenshots), len(exact.ScreenshotUploads), wantScreenshots)
			}
			for index, screenshot := range exact.Screenshots {
				id := api.PublicResourceID(fmt.Sprintf("comparison-%d-2", index+1))
				original := retained.ArtifactImages[id]
				link := retained.HostedImages[api.PublicResourceID("hosted-"+string(id))]
				if screenshot.Path != original.Path || screenshot.ContentSHA256 != original.ContentSHA256 ||
					exact.ScreenshotUploads[index].RawURL != link.RawURL || screenshot.Purpose != api.ScreenshotPurposeFinal ||
					screenshot.SourceUnavailable != (index == 0) {
					t.Fatalf("main column acquired a different image/upload: %+v", screenshot)
				}
			}
			exact.Comparisons.Sources[0].Label = "Changed"
			if media.Comparisons.Sources[0].Label == "Changed" {
				t.Fatal("description projection changed retained comparison settings")
			}
			media.Comparisons.ApprovedFingerprint = ""
			if _, err := resolveWorkflowExactMedia(t.Context(), retained, media, config.ScreenshotHandlingConfig{}); err == nil {
				t.Fatal("unreviewed comparison reached description projection")
			}
		})
	}
}

func TestComparisonDescriptionReuseTracksRowsPixelsAndLinks(t *testing.T) {
	media, retained := testComparisonDescriptionMedia(t, false)
	exact, err := resolveWorkflowExactMedia(t.Context(), retained, media, config.ScreenshotHandlingConfig{})
	if err != nil {
		t.Fatal(err)
	}
	normalized := func() []byte {
		t.Helper()
		value, available, err := normalizeReusableDescriptionMedia(t.Context(), exact)
		if err != nil || !available {
			t.Fatalf("comparison reuse unavailable: %v", err)
		}
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return payload
	}
	before := normalized()
	exact.Comparisons.Sources[0].Label = "Changed source label"
	after := normalized()
	if bytes.Equal(before, after) {
		t.Fatal("comparison label changes reused stale descriptions")
	}
	before = after
	id := exact.Comparisons.Rows[0].Cells[0].ArtifactID
	exact.ComparisonUploads[id][0].RawURL = "https://images.example.invalid/replaced.png"
	if bytes.Equal(before, normalized()) {
		t.Fatal("comparison link changes reused stale descriptions")
	}
	if err := os.WriteFile(exact.ComparisonImages[id].Path, []byte("changed pixels"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, available, _ := normalizeReusableDescriptionMedia(t.Context(), exact); available {
		t.Fatal("changed comparison bytes remained reusable")
	}
}

func TestComparisonDescriptionReuseRejectsChangedHostedLinkAssignment(t *testing.T) {
	fixture := newWorkflowDescriptionReuseFixture(t)
	media, retained := testComparisonDescriptionMedia(t, false)
	retained.screenshotSubject.MediaBinding = fixture.resolver.subject.MediaBinding
	left, right := &media.Comparisons.Rows[0].Cells[0], &media.Comparisons.Rows[0].Cells[1]
	pixels, err := os.ReadFile(retained.ArtifactImages[left.ArtifactID].Path)
	if err != nil {
		t.Fatal(err)
	}
	rightImage := retained.ArtifactImages[right.ArtifactID]
	if err := os.WriteFile(rightImage.Path, pixels, 0o600); err != nil {
		t.Fatal(err)
	}
	right.ContentSHA256, rightImage.ContentSHA256 = left.ContentSHA256, left.ContentSHA256
	retained.ArtifactImages[right.ArtifactID] = rightImage
	fingerprint, err := comparisons.ReviewFingerprint(*media.Comparisons)
	if err != nil {
		t.Fatal(err)
	}
	media.Comparisons.Fingerprint, media.Comparisons.ApprovedFingerprint = fingerprint, fingerprint
	for i := range media.Artifacts {
		if media.Artifacts[i].Kind == api.MediaArtifactHostedImage {
			media.Artifacts[i].Order = 1
		}
	}
	var variants [2]api.PublicResourceID
	for i, cell := range []*api.ComparisonCell{left, right} {
		originalID := api.PublicResourceID("hosted-" + string(cell.ArtifactID))
		link := retained.HostedImages[originalID]
		link.ContentSHA256 = cell.ContentSHA256
		retained.HostedImages[originalID] = link
		// A host may return the same URL for uploads of the same PNG.
		link.RawURL = "https://images.example.invalid/shared.png"
		id := api.PublicResourceID("variant-" + string(cell.ArtifactID))
		variants[i] = id
		retained.HostedImages[id], retained.HostedSources[id] = link, cell.ArtifactID
		media.Artifacts = append(media.Artifacts, api.MediaArtifact{
			ID:       id,
			Kind:     api.MediaArtifactHostedImage,
			Purpose:  api.ScreenshotPurposeComparison,
			Selected: true,
			Order:    2 - 2*i,
			Source:   string(cell.ArtifactID),
		})
	}
	// Removing either variant leaves the same ordered URLs when flattened,
	// but changes which URL the renderer selects for the right-hand cell.
	remove := func(id api.PublicResourceID) (api.MediaArtifactSet, any) {
		t.Helper()
		selected, private, err := (workflowMediaBuilder{}).RemoveHostedImages(
			t.Context(), fixture.oldRelease, media, retained, []api.PublicResourceID{id}, time.Now(),
		)
		if err != nil {
			t.Fatal(err)
		}
		return selected, private
	}
	before, beforePrivate := remove(variants[1])
	if err := fixture.recordReusableDescriptions(t, fixture.oldRelease, before, beforePrivate, fixture.snapshot); err != nil {
		t.Fatal(err)
	}
	instructions := fixture.instructions
	instructions.Overrides = nil
	restore := func(candidate api.MediaArtifactSet, private any) api.DescriptionSet {
		t.Helper()
		snapshot, _, err := fixture.builder.RestoreCompatibleDescriptions(t.Context(), fixture.oldRelease, fixture.projections, candidate, private, instructions)
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	if got := restore(before, beforePrivate); got.Status != api.StageStatusCompleted {
		t.Fatal("unchanged link assignments did not reuse the saved descriptions")
	}
	after, afterPrivate := remove(variants[0])
	if got := restore(after, afterPrivate); len(got.Descriptions) != 0 {
		t.Fatal("moving a hosted link between equal-content cells reused the saved descriptions")
	}
}

func TestComparisonHostingRetryRequiresCompleteCoverage(t *testing.T) {
	media, _ := testComparisonDescriptionMedia(t, true)
	projections := []api.TrackerReleaseProjection{{TrackerID: "ALPHA", Artifacts: api.TrackerArtifactRequirements{ScreenshotCount: 2}}}
	attempt := api.HostedImageAttempt{
		Host:       "imgbb",
		TrackerIDs: []api.TrackerID{"ALPHA"},
		Status:     api.StageStatusCompleted,
	}
	for _, artifact := range media.Artifacts {
		if artifact.Kind == api.MediaArtifactHostedImage {
			attempt.Results = append(attempt.Results, artifact)
		}
	}
	failure := hostedImageFailure("ALPHA", "imgbb", "previous upload failed")
	media.Failures = []api.WorkflowFailure{failure}
	reconcileRetriedImageHostFailures(&media, projections, []api.HostedImageAttempt{attempt}, nil)
	if len(media.Failures) != 0 {
		t.Fatal("successful complete comparison retry retained the old hosting failure")
	}
	media.Failures = []api.WorkflowFailure{failure}
	attempt.Results = attempt.Results[:len(attempt.Results)-1]
	reconcileRetriedImageHostFailures(&media, projections, []api.HostedImageAttempt{attempt}, nil)
	if len(media.Failures) != 1 {
		t.Fatal("partial comparison retry cleared the old hosting failure")
	}
}
