// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

// TrackerAssetProvenanceKey identifies a description whose imported images
// were collected with comparison filtering. It remains stable as images are
// removed from the stored list.
func TrackerAssetProvenanceKey(record api.TrackerMetadata) string {
	tracker := strings.ToUpper(strings.TrimSpace(record.Tracker))
	digest := sha256.Sum256([]byte(record.SourcePath + "\x00" + tracker + "\x00" +
		record.TrackerID + "\x00" + record.Description))
	return fmt.Sprintf("%s:asset_format_v2:%x", tracker, digest[:12])
}

// TrackerImageDeletionKey records a user's decision to remove imported images
// from this version of a tracker description.
func TrackerImageDeletionKey(record api.TrackerMetadata) string {
	return TrackerAssetProvenanceKey(record) + ":user_deleted"
}

// LegacyImageAssetsNeedProvenance reports whether older imported image lists
// can contain comparison images no longer identifiable from their description.
func LegacyImageAssetsNeedProvenance(registry *Registry, tracker string) bool {
	if registry == nil {
		return false
	}
	family, ok := registry.LookupFamily(tracker)
	if ok && family == FamilyUnit3D {
		return true
	}
	policy, ok := registry.LookupDataPolicy(tracker)
	return ok && policy.LegacyImageAssetsNeedProvenance
}

type trackerTimestampReader interface {
	GetTrackerTimestamp(context.Context, string) (time.Time, error)
}

// FilterUnverifiedTrackerImages withholds legacy Unit3D and configured
// standalone image lists until their description has verified provenance.
func FilterUnverifiedTrackerImages(
	ctx context.Context,
	repo trackerTimestampReader,
	registry *Registry,
	records []api.TrackerMetadata,
	logger api.Logger,
) []api.TrackerMetadata {
	if len(records) == 0 || registry == nil {
		return records
	}
	filtered := append([]api.TrackerMetadata(nil), records...)
	for index := range filtered {
		record := &filtered[index]
		if len(record.ImageURLs) == 0 || !LegacyImageAssetsNeedProvenance(registry, record.Tracker) {
			continue
		}
		if repo == nil {
			record.ImageURLs = nil
			continue
		}
		if _, err := repo.GetTrackerTimestamp(ctx, TrackerAssetProvenanceKey(*record)); err != nil {
			if logger != nil {
				if errors.Is(err, internalerrors.ErrNotFound) {
					logger.Tracef("trackers: withholding unverified legacy images tracker=%s count=%d", record.Tracker, len(record.ImageURLs))
				} else {
					logger.Warnf("trackers: failed to check image provenance tracker=%s", record.Tracker)
				}
			}
			record.ImageURLs = nil
		}
	}
	return filtered
}

// ComparisonSafeTrackerImageURLs uses validated imported URLs for trackers
// whose legacy lists have already been withheld by FilterUnverifiedTrackerImages.
// Other trackers still require description-based comparison filtering.
func ComparisonSafeTrackerImageURLs(record api.TrackerMetadata, registry *Registry) []string {
	if LegacyImageAssetsNeedProvenance(registry, record.Tracker) {
		return record.ImageURLs
	}
	return FilterImportedComparisonImageURLs(record.Description, record.ImageURLs)
}
