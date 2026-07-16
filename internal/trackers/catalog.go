// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"slices"
	"sort"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/unit3dmeta"
)

// Kind identifies a tracker protocol family.
type Kind string

const (
	// KindUnknown identifies an unclassified tracker.
	KindUnknown   Kind = ""
	KindUnit3D    Kind = "unit3d"
	KindNonUnit3D Kind = "non-unit3d"
)

var knownNonUnit3DTrackers = map[string]struct{}{
	"ACM":    {},
	"ANT":    {},
	"AR":     {},
	"ASC":    {},
	"AZ":     {},
	"BHD":    {},
	"BHDTV":  {},
	"BJS":    {},
	"BT":     {},
	"BTN":    {},
	"CZ":     {},
	"CZT":    {},
	"DC":     {},
	"FF":     {},
	"FL":     {},
	"GPW":    {},
	"HDB":    {},
	"HDS":    {},
	"HDT":    {},
	"IS":     {},
	"MTV":    {},
	"NBL":    {},
	"PHD":    {},
	"PTP":    {},
	"PTS":    {},
	"RTF":    {},
	"SPD":    {},
	"THR":    {},
	"TL":     {},
	"TVC":    {},
	"MANUAL": {},
}

var (
	trackerKinds    = buildTrackerKinds()
	trackerPriority = buildTrackerPriority()
)

// KnownTrackers returns the sorted list of tracker names in the registry.
func KnownTrackers() []string {
	trackers := make([]string, 0, len(trackerKinds))
	for name := range trackerKinds {
		trackers = append(trackers, name)
	}
	sort.Strings(trackers)
	return trackers
}

// Unit3DTrackers returns the sorted list of Unit3D tracker names in the registry.
func Unit3DTrackers() []string {
	return trackersByKind(KindUnit3D)
}

// NonUnit3DTrackers returns the sorted list of non-Unit3D tracker names in the registry.
func NonUnit3DTrackers() []string {
	return trackersByKind(KindNonUnit3D)
}

// TrackerPriority returns the shared lowercase tracker ordering used when
// ranking tracker IDs and metadata lookups. It prefers the curated list first,
// then appends any remaining Unit3D trackers in sorted order.
func TrackerPriority() []string {
	return append([]string(nil), trackerPriority...)
}

func buildTrackerPriority() []string {
	preferred := []string{
		"aither", "ulcx", "lst", "blu", "oe", "btn", "bhd",
		"hdb", "ant", "rf", "otw", "yus", "dp", "sp", "ptp",
	}

	seen := make(map[string]struct{}, len(preferred))
	unit3dTrackers := Unit3DTrackers()
	ordered := make([]string, 0, len(preferred)+len(unit3dTrackers))
	for _, name := range preferred {
		trimmed := strings.ToLower(strings.TrimSpace(name))
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		ordered = append(ordered, trimmed)
	}

	for _, tracker := range unit3dTrackers {
		lower := strings.ToLower(strings.TrimSpace(tracker))
		if _, ok := seen[lower]; ok {
			continue
		}
		seen[lower] = struct{}{}
		ordered = append(ordered, lower)
	}

	return ordered
}

// IsKnownTracker reports whether name is present in the compatibility catalog.
func IsKnownTracker(name string) bool {
	return TrackerKind(name) != KindUnknown
}

// IsUnit3DTracker reports whether name uses the Unit3D protocol.
func IsUnit3DTracker(name string) bool {
	return TrackerKind(name) == KindUnit3D
}

// IsNonUnit3DTracker reports whether name is a known non-Unit3D tracker.
func IsNonUnit3DTracker(name string) bool {
	return TrackerKind(name) == KindNonUnit3D
}

// skipsModifiedReleaseCheck reports whether a tracker is exempt from the
// modified/renamed-release rule (see isRenamedRelease). The rule is on for all
// trackers by default; this is the single place to exempt special pseudo-trackers
// or any tracker known to accept modified releases.
func skipsModifiedReleaseCheck(name string) bool {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "MANUAL":
		return true
	default:
		return false
	}
}

// NeedsPTBRLocalizedMetadata reports whether a trimmed tracker name is one of
// the exact trackers that consumes pt-BR TMDB data.
func NeedsPTBRLocalizedMetadata(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "bjs", "bt", "asc":
		return true
	default:
		return false
	}
}

// AnyNeedsPTBRLocalizedMetadata reports whether any tracker name resolves to an
// exact pt-BR localized metadata consumer.
func AnyNeedsPTBRLocalizedMetadata(names []string) bool {
	return slices.ContainsFunc(names, NeedsPTBRLocalizedMetadata)
}

// TrackerKind returns the compatibility-catalog kind for name.
func TrackerKind(name string) Kind {
	return trackerKinds[strings.ToUpper(strings.TrimSpace(name))]
}

func trackersByKind(kind Kind) []string {
	trackers := make([]string, 0, len(trackerKinds))
	for name, trackerKind := range trackerKinds {
		if trackerKind != kind {
			continue
		}
		trackers = append(trackers, name)
	}
	sort.Strings(trackers)
	return trackers
}

func buildTrackerKinds() map[string]Kind {
	trackers := make(map[string]Kind, len(knownNonUnit3DTrackers)+len(unit3dmeta.Trackers()))
	for name := range knownNonUnit3DTrackers {
		trackers[name] = KindNonUnit3D
	}
	for _, tracker := range unit3dmeta.Trackers() {
		trackers[tracker] = KindUnit3D
	}
	return trackers
}
