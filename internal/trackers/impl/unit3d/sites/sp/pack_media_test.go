// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sp

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestSPMetadataDemandRetainsOnlyRequiredTMDB(t *testing.T) {
	registry := trackers.NewRegistry()
	definition := unit3d.NewWithProfile(Profile())
	if err := registry.Register(definition); err != nil {
		t.Fatal(err)
	}
	selected, err := trackers.CollectMetadataRequirements(registry, []api.TrackerID{"SP"})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Requirements) != 1 || selected.Requirements[0].Scope != api.MetadataRequirementScopeAny ||
		!slices.Equal(selected.Requirements[0].AnyOf, []api.MetadataRequirementField{api.MetadataRequirementField(trackers.MetadataFieldTMDBIDOnly)}) ||
		selected.Requirements[0].Disposition != api.RuleDispositionStrict {
		t.Fatalf("SP metadata demand must retain strict TMDB without requesting extra file probes: %+v", selected)
	}
}
