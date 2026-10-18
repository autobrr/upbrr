// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestHDBGeneratedReleaseVersions(t *testing.T) {
	t.Parallel()
	for _, shape := range []struct {
		name     string
		category api.CanonicalCategory
		season   string
		episode  string
		date     string
		pack     bool
		identity string
	}{
		{
			name:     "movie",
			category: api.CanonicalCategoryMovie,
			identity: "2026",
		},
		{
			name:     "episode",
			category: api.CanonicalCategoryTV,
			season:   "S01",
			episode:  "E02",
			identity: "S01E02",
		},
		{
			name:     "daily episode",
			category: api.CanonicalCategoryTV,
			date:     "2026-10-01",
			identity: "2026-10-01",
		},
		{
			name:     "season pack",
			category: api.CanonicalCategoryTV,
			season:   "S01",
			pack:     true,
			identity: "S01",
		},
	} {
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()
			for _, version := range []string{"", "REPACK", "REPACK2", "REPACK3", "PROPER", "PROPER2", "PROPER3", "RERIP"} {
				t.Run("version="+version, func(t *testing.T) {
					t.Parallel()
					meta := hdbGeneratedVersionSubject()
					meta.Identity.Category = shape.category
					meta.SeasonStr, meta.EpisodeStr = shape.season, shape.episode
					meta.DailyEpisodeDate, meta.TVPack = shape.date, shape.pack
					meta.Repack = version
					input, failure := trackers.PrepareInputWithReleaseNamePolicy(
						trackers.PreparationInput{Tracker: "HDB", Meta: meta}, Profile().ReleaseNamePolicy,
					)
					if failure != nil {
						t.Fatal(failure)
					}
					want := "Example Original " + shape.identity + " "
					if version != "" {
						want += version + " "
					}
					want += "1080p WEB-DL DDP 5.1 H.264-GRP"
					got, err := input.ReviewedUploadName()
					if err != nil || got != want || input.Projection.DuplicateCriteria.Name != want {
						t.Fatalf("upload=%q search=%q err=%v, want %q", got, input.Projection.DuplicateCriteria.Name, err, want)
					}
					if meta.Repack != version || meta.ReleaseName != meta.GeneratedReleaseNames.IncludeEpisodeTitle.Name {
						t.Fatal("HDB naming mutated the prepared subject")
					}
					validation := api.TrackerValidationSubject{ProvenanceFacts: api.ProvenanceFacts{Status: api.MetadataEvidenceStatusComplete}}
					if failures := hdbTitleFailures(validation, meta); len(failures) != 0 {
						t.Fatalf("supported version rejected: %+v", failures)
					}
				})
			}
		})
	}
}

func TestHDBRequestedNamePreservesAutomaticSearch(t *testing.T) {
	t.Parallel()
	const requested = "Manual.Complete.Name.PROPER3-GRP"
	meta := hdbGeneratedVersionSubject()
	input, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker:             "HDB",
		Meta:                meta,
		RequestedUploadName: new(requested),
	}, Profile().ReleaseNamePolicy)
	if failure != nil {
		t.Fatal(failure)
	}
	got, err := input.ReviewedUploadName()
	if err != nil || got != requested {
		t.Fatalf("requested HDB name = %q, err=%v", got, err)
	}
	const automatic = "Example Original 2026 REPACK 1080p WEB-DL DDP 5.1 H.264-GRP"
	if input.Projection.DuplicateCriteria.Name != automatic {
		t.Fatalf("manual upload changed automatic search: %q", input.Projection.DuplicateCriteria.Name)
	}
	if meta.Repack != "REPACK" || meta.ReleaseName == requested {
		t.Fatal("manual name changed canonical facts")
	}
	input, failure = trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker:             "HDB",
		Meta:                meta,
		RequestedUploadName: new(" "),
	}, Profile().ReleaseNamePolicy)
	if failure == nil {
		t.Fatalf("empty requested name accepted: %+v", input.Projection)
	}
}

func TestHDBExactNameDoesNotAcquireResolvedVersion(t *testing.T) {
	t.Parallel()
	const exact = "Example.Original.2026.PROPER2.1080p.WEB-DL-GRP"
	meta := hdbGeneratedVersionSubject()
	meta.ReleaseName = exact
	resolved, err := Profile().ReleaseNamePolicy.Resolver(trackers.ReleaseNameInput{Subject: meta})
	if err != nil || resolved.Upload != exact {
		t.Fatalf("exact HDB name = %q, err=%v", resolved.Upload, err)
	}
}

func TestHDBSeasonPackVersionReachesReviewedPayload(t *testing.T) {
	t.Parallel()
	meta := hdbGeneratedVersionSubject()
	meta.Identity.Category = api.CanonicalCategoryTV
	meta.Identity.TVDBID = 1234567
	meta.SeasonStr, meta.TVPack = "S01", true
	meta.TorrentPath = filepath.Join(t.TempDir(), "prepared.torrent")
	if err := os.WriteFile(meta.TorrentPath, []byte("prepared torrent"), 0o600); err != nil {
		t.Fatal(err)
	}
	input, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker:       "HDB",
		Intent:        trackers.PreparationIntentDryRun,
		Meta:          meta,
		TrackerConfig: config.TrackerConfig{Username: "example", Passkey: "example"},
		Assets:        &trackers.DescriptionAssets{Description: "prepared description", Final: true},
	}, Profile().ReleaseNamePolicy)
	if failure != nil {
		t.Fatal(failure)
	}
	plan, failure := New().Prepare(t.Context(), input)
	if failure != nil {
		t.Fatal(failure)
	}
	preview := plan.DryRun()
	const want = "Example Original S01 REPACK 1080p WEB-DL DDP 5.1 H.264-GRP"
	if preview.ReleaseName != want || preview.Payload["name"] != want || input.Projection.UploadReleaseName != want {
		t.Fatalf("review=%q preview=%q payload=%q", input.Projection.UploadReleaseName, preview.ReleaseName, preview.Payload["name"])
	}
	input.Meta.Repack = "PROPER"
	if _, failure := trackers.PrepareInputWithReleaseNamePolicy(input, Profile().ReleaseNamePolicy); failure == nil {
		t.Fatal("stale reviewed release version accepted")
	}
}

func TestHDBReleaseVersionInvalidatesRetainedCatalog(t *testing.T) {
	t.Parallel()
	oldProfile := Profile()
	oldProfile.ReleaseNamePolicy.ID = "standalone/hdb/v6"
	oldRegistry := trackers.NewRegistry()
	if err := oldRegistry.Register(standalone.MustNew(oldProfile)); err != nil {
		t.Fatal(err)
	}
	descriptors, err := oldRegistry.CatalogDescriptors()
	if err != nil {
		t.Fatal(err)
	}
	retained, err := (api.TrackerCatalogSnapshot{CatalogVersion: "tracker-projection-v1", Trackers: descriptors}).WithFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	registry := trackers.NewRegistry()
	if err := registry.Register(standalone.MustNew(Profile())); err != nil {
		t.Fatal(err)
	}
	projector, err := trackers.NewWorkflowProjector(registry, config.Config{}, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	if current, err := projector.CatalogCurrent(retained); err != nil || current {
		t.Fatalf("old HDB naming catalog reused: current=%t err=%v", current, err)
	}
	descriptors, err = registry.CatalogDescriptors()
	if err != nil {
		t.Fatal(err)
	}
	current, err := (api.TrackerCatalogSnapshot{CatalogVersion: "tracker-projection-v1", Trackers: descriptors}).WithFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if valid, err := projector.CatalogCurrent(current); err != nil || !valid {
		t.Fatalf("current HDB naming catalog invalidated: valid=%t err=%v", valid, err)
	}
}

func hdbGeneratedVersionSubject() api.UploadSubject {
	// Source and rendered tokens intentionally disagree with the finalized version.
	const generated = "Example.Release.2026.PROPER3.1080p.WEB-DL.H.264-GRP"
	return api.UploadSubject{
		SourcePath:  generated,
		ReleaseName: generated,
		GeneratedReleaseNames: api.GeneratedReleaseNameVariants{
			IncludeEpisodeTitle: api.ReleaseNameVariant{Name: generated},
			OmitEpisodeTitle:    api.ReleaseNameVariant{Name: generated},
		},
		Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
		EffectiveMetadata: api.EffectiveMetadata{
			OriginalTitle:           "Example Original",
			OriginalTitleProvenance: api.FactProvenanceManual,
		},
		Release:    api.ReleaseInfo{Year: 2026, Resolution: "1080p"},
		Type:       "WEBDL",
		Source:     "WEB-DL",
		Repack:     "REPACK",
		Edition:    "Director's Cut",
		VideoCodec: "H.264",
		Audio:      "DDP",
		Channels:   "5.1",
		Tag:        "-GRP",
	}
}
