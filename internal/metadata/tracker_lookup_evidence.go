// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/evidence"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	trackerdata "github.com/autobrr/upbrr/internal/trackers/data"
	"github.com/autobrr/upbrr/pkg/api"
)

type trackerLookupEvidence struct {
	trackerdata.Result
	ExternalIDVersion int
}

// UnmarshalJSON expires pre-fix normalized IDs without changing the query key:
// successful evidence is recollected, while failed partials stay suppressed.
func (e *trackerLookupEvidence) UnmarshalJSON(data []byte) error {
	type payload trackerLookupEvidence
	var value payload
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("decode tracker lookup evidence: %w", err)
	}
	if value.ExternalIDVersion != 1 {
		return errors.New("obsolete tracker external ID evidence")
	}
	*e = trackerLookupEvidence(value)
	return nil
}

// retainedTrackerDataIDs reads current normalized evidence without fetching or
// changing failed-query suppression. Assets still require their own provenance.
func retainedTrackerDataIDs(ctx context.Context, query any) (trackerdata.Result, bool) {
	var retained trackerLookupEvidence
	if !evidence.Retained(ctx, "tracker.lookup", query, &retained) {
		return trackerdata.Result{}, false
	}
	return retained.Result, true
}

// lookupTrackerDataEvidence reuses tracker metadata without promoting replayed
// image acceptance into a new collection. Descriptor provenance alone is not a receipt.
func lookupTrackerDataEvidence(
	ctx context.Context,
	query any,
	refreshLegacyIDs, onlyID, keepImages bool,
	fetch func() (trackerdata.Result, error),
) (trackerdata.Result, error) {
	collected := false
	fetchCurrent := func() (trackerdata.Result, error) {
		collected = true
		return fetch()
	}
	empty := func(value trackerdata.Result) bool {
		return !value.HasData() || !onlyID && strings.TrimSpace(value.Description) == "" || keepImages && len(value.Images) == 0
	}
	var result trackerdata.Result
	var err error
	if refreshLegacyIDs {
		var retained trackerLookupEvidence
		retained, err = evidence.Lookup(ctx, "tracker.lookup", query, func() (trackerLookupEvidence, error) {
			result, err := fetchCurrent()
			return trackerLookupEvidence{Result: result, ExternalIDVersion: 1}, err
		}, func(value trackerLookupEvidence) bool { return empty(value.Result) })
		result = retained.Result
	} else {
		result, err = evidence.Lookup(ctx, "tracker.lookup", query, fetchCurrent, empty)
	}
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
