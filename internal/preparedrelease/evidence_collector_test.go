// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"errors"
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestMapLegacyFactsUsesTypedConcreteAssessments(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		meta       preparationstate.State
		uniqueID   api.UniqueIDStatus
		settings   api.EncodeSettingsStatus
		naming     api.NamingStatus
		missingLen int
	}{
		{
			name: "non-applicable remux",
			meta: preparationstate.State{
				SourcePath:                     "Example.Release.2026.1080p-GRP.mp4",
				Type:                           "REMUX",
				MediaInfoUniqueIDPresent:       false,
				MediaInfoEncodeSettingsPresent: false,
			},
			uniqueID: api.UniqueIDStatusNotApplicable,
			settings: api.EncodeSettingsStatusNotApplicable,
			naming:   api.NamingStatusComplete,
		},
		{
			name: "missing mkv encode facts",
			meta: preparationstate.State{
				SourcePath:                     "Example.Release.2026.1080p-GRP.mkv",
				FileList:                       []string{"Example.Release.2026.1080p-GRP.mkv"},
				Type:                           "ENCODE",
				VideoCodec:                     "H.264",
				MediaInfoUniqueIDPresent:       false,
				MediaInfoEncodeSettingsPresent: false,
				ReleaseNameMissing:             []string{"resolution"},
			},
			uniqueID:   api.UniqueIDStatusMissing,
			settings:   api.EncodeSettingsStatusMissing,
			naming:     api.NamingStatusIncomplete,
			missingLen: 1,
		},
		{
			name: "present mkv encode facts",
			meta: preparationstate.State{
				SourcePath:                     "Example.Release.2026.1080p-GRP.mkv",
				Type:                           "ENCODE",
				VideoCodec:                     "HEVC",
				MediaInfoUniqueIDPresent:       true,
				MediaInfoEncodeSettingsPresent: true,
			},
			uniqueID: api.UniqueIDStatusPresent,
			settings: api.EncodeSettingsStatusPresent,
			naming:   api.NamingStatusComplete,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			facts := mapCollectedFacts(tt.meta)
			if facts.Assessments.MediaInfoUniqueID != tt.uniqueID ||
				facts.Assessments.MediaInfoEncodeSettings != tt.settings ||
				facts.Assessments.Naming.Status != tt.naming ||
				len(facts.Assessments.Naming.Missing) != tt.missingLen {
				t.Fatalf("assessments = %#v", facts.Assessments)
			}
		})
	}
}

func TestApplyBlurayFactInstructionSelectsCandidateBeforePublication(t *testing.T) {
	t.Parallel()
	meta := preparationstate.State{
		ProviderMetadata: api.SourceScopedMetadata{
			Bluray: &api.BlurayMetadata{
				Candidates: []api.BlurayReleaseCandidate{{
					ReleaseID: "candidate-2",
					Region:    "B",
					Publisher: "Example Publisher",
				}},
			},
		},
	}
	if err := applyBlurayFactInstruction(&meta, "candidate-2"); err != nil {
		t.Fatalf("apply Blu-ray instruction: %v", err)
	}
	if meta.ProviderMetadata.Bluray.SelectedReleaseID != "candidate-2" || meta.Release.Region != "B" || meta.Distributor != "EXAMPLE PUBLISHER" {
		t.Fatalf("selected facts = %#v", meta)
	}
}

func TestApplyBlurayFactInstructionRejectsUnknownCandidate(t *testing.T) {
	t.Parallel()
	meta := preparationstate.State{ProviderMetadata: api.SourceScopedMetadata{Bluray: &api.BlurayMetadata{}}}
	if err := applyBlurayFactInstruction(&meta, "missing"); !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("error = %v, want not found", err)
	}
}
