// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

// Regeneration changes managed paths even when a retained measurement produces
// exactly the same image. Preserve upload provenance only for verified bytes.
func regeneratedHDRImagePaths(ctx context.Context, prior, current api.HDRAnalysisResult, source releaseworkflow.RetainedHDRAnalysisResource,
	destination hdrManagedResource,
) map[string]string {
	paths := make(map[string]string)
	if prior.Release.SourcePath != current.Release.SourcePath || prior.ManifestFingerprint != current.ManifestFingerprint ||
		prior.ProfileVersion != api.HDRAnalysisProfileV3 || current.ProfileVersion != api.HDRAnalysisProfileV3 {
		return paths
	}
	for _, after := range current.Targets {
		for _, candidate := range after.SelectableImages() {
			var matched api.PublicResourceID
			count := 0
			for _, before := range prior.Targets {
				if before.TargetID != after.TargetID {
					continue
				}
				for _, image := range before.SelectableImages() {
					if candidate.Kind == image.Kind && candidate.Format == image.Format && candidate.Profile == image.Profile &&
						candidate.AudioTrackID == image.AudioTrackID && image.MeasurementFingerprint != "" && image.RenderFingerprint != "" &&
						candidate.MeasurementFingerprint == image.MeasurementFingerprint && candidate.RenderFingerprint == image.RenderFingerprint {
						matched = image.ID
						count++
					}
				}
			}
			if count != 1 {
				continue
			}
			from, err := source.LocalArtifactPath(prior, matched)
			if err != nil {
				continue
			}
			content, err := source.OpenArtifact(ctx, prior, matched)
			if err != nil {
				continue
			}
			hash := sha256.New()
			size, copyErr := io.Copy(hash, content.Body)
			if err := errors.Join(copyErr, content.Body.Close()); err != nil {
				continue
			}
			verified, err := destination.OpenArtifact(ctx, current, candidate.ID)
			if err != nil {
				continue
			}
			if err := verified.Body.Close(); err != nil {
				continue
			}
			newFile := destination.Files[candidate.ID]
			if size == newFile.Size && hex.EncodeToString(hash.Sum(nil)) == newFile.SHA256 {
				paths[from] = newFile.Path
			}
		}
	}
	return paths
}
