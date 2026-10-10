// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Audionut/go-hdr10-plus/extract"
	bridge "github.com/Audionut/go-hdr10-plus/integration/bdinfo"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/services/bdinfo"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

func capturePreparedHDR(ctx context.Context, resource *preparationstate.DiscResource, artifactDir, sourceFingerprint string) bdinfo.HDRCapture {
	return func(outcome *bridge.Outcome) error {
		preparationstate.ReleaseHDRCaptures(resource.HDRCaptures)
		captures := make(map[string]preparationstate.HDRCaptureResource)
		resource.HDRCaptures = captures
		var failures []error
		for _, selected := range resource.SelectedPlaylists {
			name, err := api.NormalizeHDRPlaylist(selected.File)
			if err != nil {
				return fmt.Errorf("hdr_capture: %w", err)
			}
			capture, err := captureHDRPlaylist(ctx, resource.Root, name, artifactDir, sourceFingerprint, outcome)
			if err != nil {
				failure, _ := api.AsHDRAnalysisFailure(hdranalysis.Classify(err))
				capture = preparationstate.HDRCaptureResource{SourceFingerprint: sourceFingerprint, Failure: &failure}
				failures = append(failures, err)
			}
			captures[name] = capture
		}
		return errors.Join(failures...)
	}
}

func captureHDRPlaylist(
	ctx context.Context,
	discRoot, name, artifactDir, sourceFingerprint string,
	outcome *bridge.Outcome,
) (capture preparationstate.HDRCaptureResource, resultErr error) {
	sidecar, captureErr := hdranalysis.DiscExtraction(outcome, name)
	if captureErr != nil && !errors.Is(captureErr, extract.ErrNoMetadata) {
		return capture, fmt.Errorf("collect HDR capture: %w", captureErr)
	}
	identity := hdranalysis.ExtractionIdentity{
		SourceFingerprint: sourceFingerprint,
		TargetID:          api.HDRTargetID(filepath.Clean(discRoot), name),
		SelectionPolicy:   "primary_hevc_angle_zero",
	}
	if sidecar.Extraction != nil {
		identity.ResolvedTrackID = sidecar.Extraction.Frames[0].Stream.TrackID
	}
	parent := filepath.Join(artifactDir, "hdr-provisional")
	artifactRoot, err := os.OpenRoot(artifactDir)
	if err != nil {
		return capture, fmt.Errorf("open HDR capture parent: %w", err)
	}
	defer artifactRoot.Close()
	if err := artifactRoot.MkdirAll("hdr-provisional", 0o700); err != nil {
		return capture, fmt.Errorf("create HDR capture parent: %w", err)
	}
	directory, err := os.MkdirTemp(parent, "capture-")
	if err != nil {
		return capture, fmt.Errorf("create HDR capture: %w", err)
	}
	capture = preparationstate.HDRCaptureResource{
		Directory:         directory,
		Path:              filepath.Join(directory, "metadata.json"),
		SourceFingerprint: sourceFingerprint,
		TrackID:           identity.ResolvedTrackID,
		Absent:            errors.Is(captureErr, extract.ErrNoMetadata),
	}
	defer func() {
		if resultErr != nil {
			preparationstate.ReleaseHDRCaptures(map[string]preparationstate.HDRCaptureResource{name: capture})
		}
	}()
	capture.Lease, err = preparationstate.NewHDRCaptureLease(directory)
	if err != nil {
		return capture, fmt.Errorf("own HDR capture: %w", err)
	}
	sidecar.Identity, sidecar.Absent = identity, capture.Absent
	if err := hdranalysis.PublishFile(ctx, capture.Path, func(out io.Writer) error { return hdranalysis.WriteSidecar(ctx, out, sidecar) }); err != nil {
		return capture, fmt.Errorf("publish HDR capture: %w", err)
	}
	file, err := os.Open(capture.Path)
	if err != nil {
		return capture, fmt.Errorf("open HDR capture: %w", err)
	}
	hash := sha256.New()
	size, hashErr := io.Copy(hash, file)
	closeErr := file.Close()
	if err := errors.Join(hashErr, closeErr); err != nil {
		return capture, fmt.Errorf("hash HDR capture: %w", err)
	}
	capture.Size, capture.SHA256 = size, hex.EncodeToString(hash.Sum(nil))
	return capture, nil
}

// CleanupProvisionalHDR runs before metadata work and preserves OS-leased live captures.
func CleanupProvisionalHDR(ctx context.Context, dbPath string) (int, error) {
	root, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return 0, fmt.Errorf("resolve provisional HDR root: %w", err)
	}
	count, err := preparationstate.CleanupHDRCaptures(ctx, root)
	if err != nil {
		return count, fmt.Errorf("recover provisional HDR captures: %w", err)
	}
	return count, nil
}
