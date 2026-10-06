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

func TestSPPackMediaDemandIsSelectedAndTVScoped(t *testing.T) {
	registry := trackers.NewRegistry()
	profile := Profile()
	definition := unit3d.NewWithProfile(profile)
	if err := registry.RegisterDescriptor(trackers.Descriptor{
		Name:       "SP",
		Definition: definition,
		Metadata:   definition.MetadataPolicy(),
	}); err != nil {
		t.Fatal(err)
	}
	selected, err := trackers.CollectMetadataRequirements(registry, []api.TrackerID{"SP"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(selected.Requirements, func(requirement api.MetadataRequirement) bool {
		return requirement.Scope == api.MetadataRequirementScopeTV && slices.Contains(requirement.AnyOf, api.MetadataRequirementNonDiscTVPackMedia)
	}) {
		t.Fatalf("selected demand=%+v", selected)
	}
	unselected, err := trackers.CollectMetadataRequirements(registry, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(unselected.Requirements) != 0 {
		t.Fatalf("unselected demand=%+v", unselected)
	}
	for _, test := range []struct {
		name    string
		subject api.RuleSubject
		want    bool
	}{
		{name: "single episode", want: true},
		{
			name:    "disc pack",
			subject: api.RuleSubject{TVPack: true, DiscType: "BDMV"},
			want:    true,
		},
		{name: "missing probes", subject: api.RuleSubject{TVPack: true}},
		{name: "failed file", subject: api.RuleSubject{TVPack: true, MediaFileFacts: api.MediaFileFacts{ExpectedFileCount: 2, Files: []api.MediaFileFact{{VideoTrackCount: 1}, {}}}}},
		{
			name: "collected source unresolved",
			subject: api.RuleSubject{TVPack: true, MediaFileFacts: api.MediaFileFacts{
				Status:            api.MetadataEvidenceStatusPartial,
				ExpectedFileCount: 2,
				Files:             []api.MediaFileFact{{VideoTrackCount: 1}, {VideoTrackCount: 1}},
			}},
			want: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := trackers.MetadataFieldPresent(trackers.MetadataFieldNonDiscTVPackMedia, test.subject); got != test.want {
				t.Fatalf("collected=%t, want %t", got, test.want)
			}
		})
	}
}
