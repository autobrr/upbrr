// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"context"
	"errors"
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

type privateResourcePipelineFake struct {
	state preparationstate.State
}

func (f privateResourcePipelineFake) CollectPreparationEvidence(
	context.Context,
	preparationstate.Request,
) (preparationstate.State, error) {
	return f.state, nil
}

func (f privateResourcePipelineFake) HydratePrivateResources(
	context.Context,
	preparationstate.Request,
) (preparationstate.State, error) {
	return f.state, nil
}

func TestEvidenceCollectorHydratesPrivateResources(t *testing.T) {
	t.Parallel()

	sourcePath := "Example.Release.2026.1080p-GRP.mkv"
	collector, err := NewEvidenceCollector(privateResourcePipelineFake{state: preparationstate.State{
		SourcePath:          sourcePath,
		Paths:               []string{sourcePath},
		VideoPath:           sourcePath,
		MediaInfoJSONPath:   "MediaInfo.json",
		MediaInfoTextPath:   "mediainfo.txt",
		DescriptionTemplate: "template.txt",
		ClientEvidence: preparationstate.ClientEvidenceSnapshot{
			Disposition: preparationstate.ClientEvidenceDispositionSearched,
			Result:      api.ClientSearchResult{InfoHash: "example-info-hash"},
		},
	}})
	if err != nil {
		t.Fatalf("new evidence collector: %v", err)
	}

	resources, err := collector.HydratePrivateResources(context.Background(), preparationstate.Request{
		Manifest: api.SourceManifest{SourcePath: sourcePath},
	})
	if err != nil {
		t.Fatalf("hydrate private resources: %v", err)
	}
	if resources.SourcePath != sourcePath ||
		resources.VideoPath != sourcePath ||
		resources.MediaInfoJSONPath != "MediaInfo.json" ||
		resources.MediaInfoTextPath != "mediainfo.txt" ||
		resources.DescriptionTemplate != "template.txt" ||
		resources.ClientEvidence.Result.InfoHash != "example-info-hash" {
		t.Fatalf("hydrated resources = %#v", resources)
	}
}

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

func TestMapCollectedFactsPreservesGeneratedReleaseNameVariants(t *testing.T) {
	t.Parallel()

	variants := api.GeneratedReleaseNameVariants{
		IncludeEpisodeTitle: api.ReleaseNameVariant{
			NameNoTag: "Example.Show.S01E02.Example.Episode.1080p.WEB-DL",
			Name:      "Example.Show.S01E02.Example.Episode.1080p.WEB-DL-GRP",
			CleanName: "Example Show S01E02 Example Episode 1080p WEB-DL-GRP",
		},
		OmitEpisodeTitle: api.ReleaseNameVariant{
			NameNoTag: "Example.Show.S01E02.1080p.WEB-DL",
			Name:      "Example.Show.S01E02.1080p.WEB-DL-GRP",
			CleanName: "Example Show S01E02 1080p WEB-DL-GRP",
		},
	}
	facts := mapCollectedFacts(preparationstate.State{GeneratedReleaseNames: variants})
	if facts.Naming.GeneratedReleaseNames != variants {
		t.Fatalf("collected variants = %#v, want %#v", facts.Naming.GeneratedReleaseNames, variants)
	}
}

func TestMapCollectedFactsPublishesFinalizedNamingSourceAndType(t *testing.T) {
	t.Parallel()
	facts := mapCollectedFacts(preparationstate.State{
		SourcePath: "Example.Show.S01.2026.BDRip.1080p.x265-GRP.mkv",
		Source:     "BluRay",
		Type:       "ENCODE",
		Release: api.ReleaseInfo{
			Source: "BluRay",
			Type:   "ENCODE",
		},
	})
	if facts.Naming.Source != "BluRay" || facts.Naming.Type != "ENCODE" {
		t.Fatalf("naming facts = %#v", facts.Naming)
	}
	if facts.Media.Source != "BluRay" || facts.Media.Type != "ENCODE" {
		t.Fatalf("media facts = %#v", facts.Media)
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
