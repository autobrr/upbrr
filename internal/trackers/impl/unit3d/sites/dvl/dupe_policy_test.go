// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dvl

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDuplicatePolicyPreservesSearchEvidence(t *testing.T) {
	t.Parallel()

	policy := Profile().DupePolicy
	if policy == nil || policy.ID != "dvl/duplicate/v1" || policy.EvidenceID != "dvl-upload-rules-coexisting-releases" || !policy.ExactMatchOnly {
		t.Fatalf("DVL duplicate policy = %#v", policy)
	}
	for _, test := range []struct {
		name       string
		search     dupe.SearchEvidence
		wantAction bool
	}{
		{
			name:   "complete provider search",
			search: dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID},
		},
		{
			name:       "incomplete enumeration",
			search:     dupe.SearchEvidence{WorkScope: dupe.WorkScopeProviderID},
			wantAction: true,
		},
		{
			name:       "unbound title search",
			search:     dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeTitle},
			wantAction: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := dupe.Evaluate(api.TrackerDuplicateTarget{
				Names:      []string{"Example.Release.2026.1080p.WEB-DL.VariantA-GRP"},
				Type:       "WEB-DL",
				Resolution: "1080p",
			}, []dupe.TrackerCandidate{{
				Name:       "Example.Release.2026.1080p.WEB-DL.VariantB-GRP",
				Type:       "WEB-DL",
				Resolution: "1080p",
			}}, *policy, test.search)
			if got.Candidates[0].Relation != api.DupeRelationCoexists || got.Blocks || got.RequiresAction != test.wantAction ||
				got.Complete == test.wantAction {
				t.Fatalf("DVL relation=%s blocks=%t action=%t complete=%t", got.Candidates[0].Relation, got.Blocks, got.RequiresAction, got.Complete)
			}
		})
	}
}
