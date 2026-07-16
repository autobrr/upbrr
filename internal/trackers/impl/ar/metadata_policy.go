// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ar

import "github.com/autobrr/upbrr/internal/trackers"

func (d *Definition) MetadataPolicy() *trackers.TrackerMetadataPolicy {
	return &trackers.TrackerMetadataPolicy{
		RequireKnownCategory: true,
		Requirements: []trackers.MetadataRequirement{
			{Scope: trackers.MetadataScopeMovie, AnyOf: []trackers.MetadataField{trackers.MetadataFieldTMDB, trackers.MetadataFieldIMDB}},
			{
				Scope: trackers.MetadataScopeTV,
				AnyOf: []trackers.MetadataField{trackers.MetadataFieldTMDB, trackers.MetadataFieldIMDB, trackers.MetadataFieldTVDB},
			},
			{Scope: trackers.MetadataScopeAny, AnyOf: []trackers.MetadataField{trackers.MetadataFieldPoster}},
		},
	}
}
