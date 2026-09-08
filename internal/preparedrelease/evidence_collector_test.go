// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

type privateResourcePipelineFake struct {
	state preparationstate.State
}

type resolvedNamingPipelineFake struct {
	state preparationstate.State
}

func (f resolvedNamingPipelineFake) CollectPreparationEvidence(
	context.Context,
	preparationstate.Request,
) (preparationstate.State, error) {
	state := f.state
	metadata.RebuildReleaseName(&state, api.NopLogger{})
	return state, nil
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

func TestMapCollectedFactsProjectsEffectiveInstructionValues(t *testing.T) {
	t.Parallel()
	meta := preparationstate.State{
		SourcePath:       "Example.Release.2026.1080p-GRP.mkv",
		Type:             "REMUX",
		Source:           "BluRay",
		Service:          "AMZN",
		ServiceLongName:  "Amazon Prime",
		Edition:          "Extended",
		Region:           "B",
		Audio:            "DTS-HD MA 5.1",
		Tag:              "-OTHER",
		EpisodeTitle:     "Corrected Title",
		SeasonInt:        3,
		EpisodeInt:       7,
		SeasonStr:        "S03",
		EpisodeStr:       "E07",
		DailyEpisodeDate: "2026-02-03",
		Release: api.ReleaseInfo{
			Type:       "REMUX",
			Source:     "BluRay",
			Genre:      "Parsed Genre",
			Resolution: "2160p",
			Region:     "B",
			Year:       2027,
			Group:      "OTHER",
		},
		ResolvedNaming: preparationstate.ResolvedNaming{
			Type:         "REMUX",
			Year:         2027,
			Source:       "BluRay",
			Resolution:   "2160p",
			Genre:        "Resolved Genre",
			EpisodeTitle: "Resolved Episode",
		},
	}
	facts := mapCollectedFacts(meta)
	if facts.Naming.Type != "REMUX" || facts.Media.Type != "REMUX" {
		t.Fatalf("type facts = %q/%q", facts.Naming.Type, facts.Media.Type)
	}
	if facts.Naming.Source != "BluRay" || facts.Media.Source != "BluRay" {
		t.Fatalf("source facts = %q/%q", facts.Naming.Source, facts.Media.Source)
	}
	if facts.Naming.Resolution != "2160p" || facts.Naming.Year != 2027 {
		t.Fatalf("naming facts = %q/%d", facts.Naming.Resolution, facts.Naming.Year)
	}
	if facts.Naming.Genre != "Resolved Genre" {
		t.Fatalf("genre fact = %q", facts.Naming.Genre)
	}
	if facts.Naming.Tag != "-OTHER" || facts.Naming.Group != "OTHER" {
		t.Fatalf("tag facts = %q/%q", facts.Naming.Tag, facts.Naming.Group)
	}
	if facts.Media.Service != "AMZN" || facts.Media.ServiceLongName != "Amazon Prime" || facts.Media.Edition != "Extended" || facts.Media.Region != "B" ||
		facts.Media.Audio != "DTS-HD MA 5.1" {
		t.Fatalf("media facts = %#v", facts.Media)
	}
	if facts.Episode.Season != 3 || facts.Episode.Episode != 7 || facts.Episode.SeasonLabel != "S03" || facts.Episode.EpisodeLabel != "E07" ||
		facts.Episode.Title != "Resolved Episode" ||
		facts.Episode.DailyDate != "2026-02-03" {
		t.Fatalf("episode facts = %#v", facts.Episode)
	}
	if facts.Identity.Season != 3 || facts.Identity.Episode != 7 {
		t.Fatalf("identity intent = %#v", facts.Identity)
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

func TestMapCollectedFactsPublishesDVDCapacityToReleaseInfo(t *testing.T) {
	t.Parallel()

	facts := mapCollectedFacts(preparationstate.State{Release: api.ReleaseInfo{Size: "DVD9"}})
	if facts.Naming.Size != "DVD9" {
		t.Fatalf("naming size = %q, want DVD9", facts.Naming.Size)
	}
	if got := releaseInfo(api.PreparedRelease{Naming: facts.Naming}).Size; got != "DVD9" {
		t.Fatalf("release info size = %q, want DVD9", got)
	}
}

func TestEvidenceCollectorPublishesResolvedNamingFromMetadataProducer(t *testing.T) {
	t.Parallel()
	const sourcePath = "Example.Show.S01.2026.BDRip.1080p.x265-GRP.mkv"
	collector, err := NewEvidenceCollector(resolvedNamingPipelineFake{state: preparationstate.State{
		SourcePath: sourcePath,
		Identity: api.ExternalIdentity{
			Category: api.CanonicalCategoryTV,
			TVDBID:   123456,
		},
		ProviderMetadata: api.SourceScopedMetadata{TVDB: &api.TVDBMetadata{
			TVDBID:             123456,
			Name:               "Resolved Original",
			NameEnglish:        "Resolved Series",
			OriginalLanguage:   "ja",
			Year:               2026,
			YearFromAlias:      true,
			Genres:             "Animation, Drama",
			EpisodeSeason:      1,
			EpisodeNumber:      2,
			EpisodeNameEnglish: "Resolved Episode",
		}},
		Type:         "ENCODE",
		Source:       "BluRay",
		Region:       "B",
		Channels:     "5.1",
		Edition:      "Extended",
		SeasonInt:    1,
		EpisodeInt:   2,
		SeasonStr:    "S01",
		EpisodeStr:   "E02",
		EpisodeTitle: "Parsed Episode",
		Tag:          "-GRP",
		Release: api.ReleaseInfo{
			Title:      "Parsed Series",
			Alt:        "Resolved Series",
			Year:       1999,
			Source:     "Web",
			Type:       "WEBDL",
			Resolution: "1080p",
			Codec:      []string{"x265"},
			Audio:      []string{"DDP5.1"},
			HDR:        []string{"HDR10"},
			Language:   []string{"English"},
		},
		ReleaseNameOverrides: api.ReleaseNameOverrides{NoAKA: new(true), NoYear: new(true)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	facts, err := collector.Collect(context.Background(), preparationstate.Request{Manifest: api.SourceManifest{SourcePath: sourcePath}})
	if err != nil {
		t.Fatal(err)
	}
	if facts.Naming.Title != "Resolved Series" || facts.Naming.AlternateTitle != "AKA Resolved Original" || facts.Naming.Year != 2026 ||
		facts.Naming.Source != "BluRay" || facts.Naming.Type != "ENCODE" || facts.Naming.Resolution != "1080p" {
		t.Fatalf("naming facts = %#v", facts.Naming)
	}
	if facts.Media.Source != "BluRay" || facts.Media.Type != "ENCODE" {
		t.Fatalf("media facts = %#v", facts.Media)
	}
	if facts.Naming.Group != "GRP" || facts.Naming.Region != "B" || facts.Naming.Channels != "5.1" ||
		!slices.Equal(facts.Naming.Editions, []string{"Extended"}) {
		t.Fatalf("final naming duplicates = %#v", facts.Naming)
	}
	if facts.Naming.Genre != "Animation, Drama" {
		t.Fatalf("genre fact = %q", facts.Naming.Genre)
	}
	if !slices.Equal(facts.Naming.Codecs, []string{"x265"}) || !slices.Equal(facts.Naming.Audio, []string{"DDP5.1"}) ||
		!slices.Equal(facts.Naming.HDR, []string{"HDR10"}) || !slices.Equal(facts.Naming.Languages, []string{"English"}) {
		t.Fatalf("parser naming tokens changed = %#v", facts.Naming)
	}
	if facts.Identity.Title != "Resolved Series" || facts.Identity.Year != 2026 {
		t.Fatalf("identity intent = %#v", facts.Identity)
	}
	if facts.Episode.Title != "Resolved Episode" {
		t.Fatalf("episode facts = %#v", facts.Episode)
	}
}

func TestMapCollectedFactsPublishesResolvedAlternateTitle(t *testing.T) {
	t.Parallel()
	for _, alternate := range []string{"AKA Rei no Sakuhin", ""} {
		facts := mapCollectedFacts(preparationstate.State{
			Release:        api.ReleaseInfo{Alt: "Parsed Original"},
			ResolvedNaming: preparationstate.ResolvedNaming{AlternateTitle: alternate},
		})
		if facts.Naming.AlternateTitle != alternate {
			t.Fatalf("alternate title = %q, want %q", facts.Naming.AlternateTitle, alternate)
		}
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
