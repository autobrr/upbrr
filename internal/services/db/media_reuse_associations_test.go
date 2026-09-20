// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestReusableMediaRejectsExpiredCoordinatorWrites(t *testing.T) {
	t.Parallel()
	repo := openMigratedTestRepo(t)
	now := time.Now().UTC()
	empty, err := repo.LoadActiveInput(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	opening := api.ActiveInputRecord{
		State:          api.ActiveInputOpening,
		Revision:       1,
		Fence:          1,
		OwnerID:        "owner",
		CoordinatorID:  "current",
		LeaseExpiresAt: now.Add(time.Minute),
		ReservationID:  "open",
		RequestedPath:  `C:\releases\Example.Release.2026.mkv`,
		IdempotencyKey: "open",
	}
	if err := repo.CompareAndSwapActiveInput(t.Context(), empty, opening, now); err != nil {
		t.Fatal(err)
	}
	binding := api.PreparedMediaBinding{
		SourcePath:               opening.RequestedPath,
		PreparedMediaFingerprint: "prepared",
		PreparedGeneration:       1,
	}
	stale := api.WithActiveInputAuthority(t.Context(), api.ActiveInputAuthority{CoordinatorID: "previous", Fence: 1})
	if err := repo.CommitReusableMedia(stale, binding, api.MediaCompatibilityKey(mediaReuseTestSHA256("source")), nil, api.ReusableMediaCommit{
		WorkflowID: "workflow",
		MediaID:    "media",
		Revision:   1,
	}); !errors.Is(err, api.ErrActiveInputLeaseLost) {
		t.Fatalf("stale commit = %v", err)
	}
	if err := repo.DeleteReusableMediaAssets(stale, binding, []string{`C:\vault\image.png`}); !errors.Is(err, api.ErrActiveInputLeaseLost) {
		t.Fatalf("stale delete = %v", err)
	}
}

func TestReusableMediaAssetsRoundTripOnlyStrongClaims(t *testing.T) {
	t.Parallel()

	repo := openMigratedTestRepo(t)
	binding := api.PreparedMediaBinding{
		SourcePath:               "C:\\releases\\Example.Release.2026.mkv",
		PreparedMediaFingerprint: "prepared-media",
		PreparedGeneration:       3,
	}
	compatibilityKey := api.MediaCompatibilityKey(mediaReuseTestSHA256("verified source"))
	asset := api.ReusableMediaAsset{
		Binding:            binding,
		CompatibilityKey:   compatibilityKey,
		CaptureFingerprint: api.WorkflowFingerprint(mediaReuseTestSHA256("capture")),
		ContentSHA256:      mediaReuseTestSHA256("image bytes"),
		Kind:               api.MediaArtifactScreenshot,
		Image: api.ScreenshotImage{
			DiscID:           "disc-a",
			Index:            2,
			TimestampSeconds: 120,
			Path:             "C:\\vault\\screen.png",
			Purpose:          api.ScreenshotPurposeFinal,
			Width:            1920,
			Height:           1080,
			SizeBytes:        42,
		},
		Selected: true,
		Order:    4,
		HostedLinks: []api.UploadedImageLink{{
			Host:         "pixhost",
			UsageScope:   "global",
			AccountScope: mediaReuseTestSHA256("pixhost account"),
			ImgURL:       "https://images.invalid/screen.png",
			SizeBytes:    42,
			UploadedAt:   time.Date(2026, time.September, 19, 0, 0, 0, 0, time.UTC),
		}},
	}
	if err := repo.CommitReusableMedia(t.Context(), binding, compatibilityKey, []api.ReusableMediaAsset{asset}, reusableMediaCommitForTest(binding)); err != nil {
		t.Fatalf("store reusable assets: %v", err)
	}
	loaded, err := repo.LoadReusableMediaAssets(t.Context(), compatibilityKey)
	if err != nil {
		t.Fatalf("load reusable assets: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Binding.PreparedGeneration != binding.PreparedGeneration ||
		loaded[0].ContentSHA256 != asset.ContentSHA256 || loaded[0].Order != asset.Order ||
		len(loaded[0].HostedLinks) != 1 || loaded[0].HostedLinks[0].AccountScope != asset.HostedLinks[0].AccountScope {
		t.Fatalf("loaded reusable assets = %#v", loaded)
	}
	if _, err := repo.LoadReusableMediaAssets(t.Context(), api.MediaCompatibilityKey("legacy-manifest-fingerprint")); err == nil {
		t.Fatal("weak legacy compatibility key was accepted")
	}
	if err := repo.DeleteReusableMediaAssets(t.Context(), binding, []string{asset.Image.Path}); err != nil {
		t.Fatalf("tombstone reusable asset: %v", err)
	}
	deleted, err := repo.LoadReusableMediaAssets(t.Context(), compatibilityKey)
	if err != nil {
		t.Fatalf("load tombstoned assets: %v", err)
	}
	if len(deleted) != 0 {
		t.Fatalf("deleted reusable assets remained available: %#v", deleted)
	}
	if err := repo.CommitReusableMedia(t.Context(), binding, compatibilityKey, []api.ReusableMediaAsset{asset}, reusableMediaCommitForTest(binding)); err != nil {
		t.Fatalf("re-capture reusable asset: %v", err)
	}
	recaptured, err := repo.LoadReusableMediaAssets(t.Context(), compatibilityKey)
	if err != nil || len(recaptured) != 1 {
		t.Fatalf("re-captured reusable assets = %#v, err=%v", recaptured, err)
	}

	updatedBinding := binding
	updatedBinding.PreparedMediaFingerprint = "current-prepared-media"
	updatedBinding.PreparedGeneration++
	updated := asset
	updated.Binding = updatedBinding
	updated.Order = 9
	updated.HostedLinks = nil
	if err := repo.CommitReusableMedia(t.Context(), updatedBinding, compatibilityKey, []api.ReusableMediaAsset{updated}, reusableMediaCommitForTest(updatedBinding)); err != nil {
		t.Fatalf("store current reusable asset: %v", err)
	}
	if _, err := repo.db.ExecContext(t.Context(), `
		UPDATE media_reusable_assets SET captured_at = ? WHERE prepared_generation = ?
	`, "2027-01-01T00:00:00Z", updatedBinding.PreparedGeneration); err != nil {
		t.Fatalf("mark current reusable asset newer: %v", err)
	}
	current, err := repo.LoadReusableMediaAssets(t.Context(), compatibilityKey)
	if err != nil || len(current) != 1 || !current[0].Binding.Equal(updatedBinding) ||
		current[0].Order != updated.Order || len(current[0].HostedLinks) != 0 {
		t.Fatalf("current reusable asset = %#v, err=%v", current, err)
	}
}

func TestReusableMediaRecapturePreservesOtherSourceTombstone(t *testing.T) {
	t.Parallel()

	repo := openMigratedTestRepo(t)
	compatibilityKey := api.MediaCompatibilityKey(mediaReuseTestSHA256("verified source"))
	bindingA := api.PreparedMediaBinding{
		SourcePath:               "C:\\releases\\a.mkv",
		PreparedMediaFingerprint: "prepared-a",
		PreparedGeneration:       1,
	}
	bindingB := api.PreparedMediaBinding{
		SourcePath:               "C:\\releases\\b.mkv",
		PreparedMediaFingerprint: "prepared-b",
		PreparedGeneration:       1,
	}
	assetA := api.ReusableMediaAsset{
		Binding:            bindingA,
		CompatibilityKey:   compatibilityKey,
		CaptureFingerprint: api.WorkflowFingerprint(mediaReuseTestSHA256("capture")),
		ContentSHA256:      mediaReuseTestSHA256("image bytes"),
		Kind:               api.MediaArtifactScreenshot,
		Image: api.ScreenshotImage{
			Path:    "C:\\vault\\a.png",
			Purpose: api.ScreenshotPurposeFinal,
		},
		Selected: true,
	}
	assetB := assetA
	assetB.Binding = bindingB
	assetB.Image.Path = "C:\\vault\\b.png"
	if err := repo.CommitReusableMedia(t.Context(), bindingA, compatibilityKey, []api.ReusableMediaAsset{assetA}, reusableMediaCommitForTest(bindingA)); err != nil {
		t.Fatalf("store source A: %v", err)
	}
	if err := repo.CommitReusableMedia(t.Context(), bindingB, compatibilityKey, []api.ReusableMediaAsset{assetB}, reusableMediaCommitForTest(bindingB)); err != nil {
		t.Fatalf("store source B: %v", err)
	}
	if err := repo.DeleteReusableMediaAssets(t.Context(), bindingA, []string{assetA.Image.Path}); err != nil {
		t.Fatalf("delete source A: %v", err)
	}
	if err := repo.DeleteReusableMediaAssets(t.Context(), bindingB, []string{assetB.Image.Path}); err != nil {
		t.Fatalf("delete source B: %v", err)
	}
	if err := repo.CommitReusableMedia(t.Context(), bindingA, compatibilityKey, []api.ReusableMediaAsset{assetA}, reusableMediaCommitForTest(bindingA)); err != nil {
		t.Fatalf("recapture source A: %v", err)
	}
	if count := reusableTombstoneSourceCount(t, repo, bindingB.SourcePath, string(compatibilityKey), string(assetA.CaptureFingerprint), assetA.ContentSHA256); count != 1 {
		t.Fatalf("source B tombstones after source A recapture = %d", count)
	}
	assets, err := repo.LoadReusableMediaAssets(t.Context(), compatibilityKey)
	if err != nil || len(assets) != 0 {
		t.Fatalf("source B suppression after source A recapture = %#v, %v", assets, err)
	}
}

func TestReusableMediaCommitMarksExactSnapshot(t *testing.T) {
	t.Parallel()

	repo := openMigratedTestRepo(t)
	binding := api.PreparedMediaBinding{
		SourcePath:               "C:\\releases\\Example.Release.2026.mkv",
		PreparedMediaFingerprint: "prepared-media",
		PreparedGeneration:       3,
	}
	commit := api.ReusableMediaCommit{
		WorkflowID: "workflow",
		MediaID:    "media",
		Revision:   8,
	}
	if err := repo.CommitReusableMedia(
		t.Context(),
		binding,
		api.MediaCompatibilityKey(mediaReuseTestSHA256("verified source")),
		nil,
		api.ReusableMediaCommit{},
	); !errors.Is(err, internalerrors.ErrInvalidInput) {
		t.Fatalf("commit zero snapshot = %v", err)
	}
	if err := repo.CommitReusableMedia(
		t.Context(),
		binding,
		api.MediaCompatibilityKey(mediaReuseTestSHA256("verified source")),
		nil,
		commit,
	); err != nil {
		t.Fatalf("commit reusable media: %v", err)
	}
	found, err := repo.HasReusableMediaCommit(t.Context(), commit)
	if err != nil || !found {
		t.Fatalf("find reusable media commit = %v, %v", found, err)
	}
	other, err := repo.HasReusableMediaCommit(t.Context(), api.ReusableMediaCommit{
		WorkflowID: commit.WorkflowID,
		MediaID:    commit.MediaID,
		Revision:   commit.Revision + 1,
	})
	if err != nil || other {
		t.Fatalf("find different reusable media commit = %v, %v", other, err)
	}
}

func reusableMediaCommitForTest(binding api.PreparedMediaBinding) api.ReusableMediaCommit {
	return api.ReusableMediaCommit{
		WorkflowID: api.WorkflowID("workflow-" + binding.PreparedMediaFingerprint),
		MediaID:    api.MediaArtifactSetID("media-" + binding.PreparedMediaFingerprint),
		Revision:   api.WorkflowRevision(binding.PreparedGeneration),
	}
}

func mediaReuseTestSHA256(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
