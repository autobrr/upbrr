// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/evidence"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	trackerdata "github.com/autobrr/upbrr/internal/trackers/data"
	"github.com/autobrr/upbrr/pkg/api"
)

// lookupTrackerDataEvidence reuses tracker metadata without promoting replayed
// image acceptance into a new collection. Descriptor provenance alone is not a receipt.
func lookupTrackerDataEvidence(ctx context.Context, query any, onlyID, keepImages bool, fetch func() (trackerdata.Result, error)) (trackerdata.Result, error) {
	collected := false
	result, err := evidence.Lookup(ctx, "tracker.lookup", query, func() (trackerdata.Result, error) {
		collected = true
		return fetch()
	}, func(value trackerdata.Result) bool {
		return !value.HasData() || !onlyID && strings.TrimSpace(value.Description) == "" || keepImages && len(value.Images) == 0
	})
	if !collected {
		result.UninspectedImages = nil
	}
	if err != nil {
		return result, fmt.Errorf("tracker data lookup: %w", err)
	}
	return result, nil
}

// importedScreenshotEvidenceFromResult retains only the current lookup's typed
// acceptance receipt, including typed current persistence outcomes. Neither
// cached image URLs nor generic download errors qualify.
func importedScreenshotEvidenceFromResult(meta preparationstate.State, record api.TrackerMetadata, result trackerdata.Result) *api.ImportedScreenshotEvidence {
	if !meta.Policy.KeepImages || meta.Policy.OnlyID || meta.SourceFingerprint == "" ||
		result.ImportedImagePolicyVersion != api.ImportedScreenshotEvidencePolicyVersion ||
		result.ImportedImageDescriptionFingerprint == "" || len(result.UninspectedImages) == 0 ||
		strings.TrimSpace(record.TrackerID) == "" && strings.TrimSpace(record.InfoHash) == "" {
		return nil
	}
	return &api.ImportedScreenshotEvidence{
		Tracker:                record.Tracker,
		TrackerID:              record.TrackerID,
		InfoHash:               record.InfoHash,
		SourceFingerprint:      meta.SourceFingerprint,
		PolicyVersion:          result.ImportedImagePolicyVersion,
		DescriptionFingerprint: result.ImportedImageDescriptionFingerprint,
		References:             slices.Clone(result.UninspectedImages),
	}
}
