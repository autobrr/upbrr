// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

// Family identifies a tracker protocol family.
type Family string

const (
	// FamilyUnknown identifies an unclassified tracker.
	FamilyUnknown Family = ""
	// FamilyUnit3D identifies trackers built on Unit3D.
	FamilyUnit3D Family = "unit3d"
	// FamilyAZFamily identifies trackers using the AvistaZ-family protocol.
	FamilyAZFamily Family = "azfamily"
	// FamilyStandalone identifies trackers with standalone implementations.
	FamilyStandalone Family = "standalone"
)
