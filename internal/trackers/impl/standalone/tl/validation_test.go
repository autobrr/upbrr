// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestAPIAnimeRequiresCurrentMappedAniListIdentity(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		change func(*api.UploadSubject)
		valid  bool
	}{
		{name: "distinct provider IDs", valid: true},
		{name: "missing snapshot", change: func(meta *api.UploadSubject) { meta.ProviderMetadata.AniList = nil }},
		{name: "nonpositive AniList", change: func(meta *api.UploadSubject) { meta.ProviderMetadata.AniList.AniListID = 0 }},
		{name: "manual MAL replacement", change: func(meta *api.UploadSubject) { meta.Identity.MALID = 333 }},
		{name: "manual MAL clear", change: func(meta *api.UploadSubject) { meta.Identity.MALID = 0 }},
		{name: "stale generation", change: func(meta *api.UploadSubject) { meta.ProviderMetadata.Generation = 2 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := api.UploadSubject{
				Anime: true,
				Identity: api.ExternalIdentity{
					Category:   api.CanonicalCategoryTV,
					MALID:      111,
					IMDBID:     456,
					Generation: 1,
				},
				ProviderMetadata: api.SourceScopedMetadata{Generation: 1, AniList: &api.AniListMetadata{MALID: 111, AniListID: 222}},
			}
			if test.change != nil {
				test.change(&meta)
			}
			for _, apiUpload := range []bool{false, true} {
				subject := api.NewTrackerValidationSubject(meta, "TL")
				subject.APIUpload = apiUpload
				failures, err := validationPolicy().Check(t.Context(), subject, api.NopLogger{})
				wantValid := test.valid || !apiUpload && meta.Identity.MALID > 0
				if err != nil || (len(failures) == 0) != wantValid {
					t.Fatalf("api=%t failures=%+v err=%v", apiUpload, failures, err)
				}
			}
			// Valid direct preparation must succeed; invalid identity fails before assets are inspected.
			if test.valid {
				meta.TorrentPath = filepath.Join(t.TempDir(), "prepared.torrent")
				if err := os.WriteFile(meta.TorrentPath, []byte("synthetic torrent"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := prepareUpload(t.Context(), trackers.PreparationInput{
				Tracker:       "TL",
				Meta:          meta,
				TrackerConfig: config.TrackerConfig{APIUpload: true},
				Assets:        &trackers.DescriptionAssets{Final: true, Description: "Reviewed description"},
				Projection: &api.TrackerReleaseProjection{
					UploadReleaseName: "Example.Show.S01E01.1080p.WEB-DL-GRP",
					Readiness:         api.ReadinessStatusReady,
					UploadReady:       true,
				},
			})
			guidance := "refresh metadata"
			if meta.Identity.MALID <= 0 {
				guidance = "require a MAL ID"
			}
			if test.valid && err != nil || !test.valid && (err == nil || !strings.Contains(err.Error(), guidance)) {
				t.Fatalf("direct API preparation: %v", err)
			}
		})
	}
}

func TestRestoredOldTLProjectionCannotPrepareSubmission(t *testing.T) {
	t.Parallel()
	legacy := Profile()
	legacy.ReleaseNamePolicy.ID = "standalone/tl/v1"
	legacy.ValidationPolicy = trackers.NoExtraValidationPolicy("standalone-tl-constructibility-v1")
	legacy.DupePolicy = nil
	oldRegistry, currentRegistry := trackers.NewRegistry(), trackers.NewRegistry()
	if err := oldRegistry.Register(standalone.MustNew(legacy)); err != nil {
		t.Fatal(err)
	}
	if err := currentRegistry.Register(New()); err != nil {
		t.Fatal(err)
	}
	meta := api.UploadSubject{
		SourcePath:       filepath.Join(t.TempDir(), "Example.Movie.mkv"),
		ReleaseName:      "Example.Movie.2026.1080p.WEB-DL-GRP",
		Identity:         api.ExternalIdentity{Category: api.CanonicalCategoryMovie, IMDBID: 456},
		ProviderMetadata: api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{IMDBID: 456, Title: "Example Movie"}},
	}
	old, failure := oldRegistry.ProjectRelease(t.Context(), trackers.PreparationInput{Tracker: "TL", Meta: meta}, "input", "catalog", "config")
	if failure != nil || !old.UploadReady {
		t.Fatalf("old projection was not eligible: %+v %v", old, failure)
	}
	payload, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	var restored api.TrackerReleaseProjection
	if err := json.Unmarshal(payload, &restored); err != nil {
		t.Fatal(err)
	}
	service := trackers.NewServiceWithRegistry(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "upbrr.db")}, Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"TL": {Passkey: "synthetic", APIUpload: true}}}}, api.NopLogger{}, nil, currentRegistry)
	retained, err := service.PrepareRetainedUploadPlan(t.Context(), meta, []api.TrackerReleaseProjection{restored})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := retained.Release(); err != nil {
			t.Error(err)
		}
	})
	preparations := retained.Preparations()
	if len(preparations) != 1 || preparations[0].Failure == nil || preparations[0].Failure.Code != "name_policy_stale" {
		t.Fatalf("restored preparation = %+v", preparations)
	}
}

func TestWorkflowProjectionBindsAPIConstructibility(t *testing.T) {
	t.Parallel()
	registry := trackers.NewRegistry()
	if err := registry.Register(New()); err != nil {
		t.Fatal(err)
	}
	meta := api.UploadSubject{
		Anime:       true,
		ReleaseName: "Example.Show.S01E01.1080p.WEB-DL-GRP",
		Identity: api.ExternalIdentity{
			Category: api.CanonicalCategoryTV,
			MALID:    111,
			IMDBID:   456,
		},
		Release:          api.ReleaseInfo{Title: "Example Show", Resolution: "1080p"},
		ProviderMetadata: api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{IMDBID: 456, Title: "Example Show"}},
	}
	for _, apiUpload := range []bool{false, true} {
		cfg := config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"TL": {APIUpload: apiUpload, Passkey: "synthetic"}}}}
		projector, err := trackers.NewWorkflowProjector(registry, cfg, api.NopLogger{})
		if err != nil {
			t.Fatal(err)
		}
		_, _, _, projections, err := projector.Build(t.Context(), api.ReleaseSnapshot{}, meta, []api.TrackerID{"TL"}, nil, nil, api.WorkflowExecutionModeNormal)
		if err != nil {
			t.Fatal(err)
		}
		if len(projections.Projections) != 1 {
			t.Fatalf("projections = %+v", projections)
		}
		projection := projections.Projections[0]
		blocked := false
		for _, decision := range projection.PolicyDecisions {
			if strings.Contains(decision.Message, "refresh metadata") {
				blocked = true
			}
		}
		if blocked != apiUpload || projection.DupeReady == apiUpload {
			t.Fatalf("api=%t projection=%+v", apiUpload, projection)
		}
	}
}

func TestReconstructedAssessmentRejectsChangedTLValidationPolicy(t *testing.T) {
	t.Parallel()
	// Change only validation policy; its fingerprint must invalidate retained duplicate authority.
	legacyProfile := Profile()
	legacyProfile.ValidationPolicy = trackers.NoExtraValidationPolicy("standalone-tl-constructibility-v1")
	oldRegistry, currentRegistry := trackers.NewRegistry(), trackers.NewRegistry()
	if err := oldRegistry.Register(standalone.MustNew(legacyProfile)); err != nil {
		t.Fatal(err)
	}
	if err := currentRegistry.Register(New()); err != nil {
		t.Fatal(err)
	}
	input := trackers.PreparationInput{Tracker: "TL", Meta: api.UploadSubject{ReleaseName: "Example.Movie.2026.1080p.WEB-DL-GRP", Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie, IMDBID: 456}}}
	oldProjection, oldFailure := oldRegistry.ProjectRelease(t.Context(), input, "input", "catalog", "config")
	currentProjection, currentFailure := currentRegistry.ProjectRelease(t.Context(), input, "input", "catalog", "config")
	if oldFailure != nil || currentFailure != nil {
		t.Fatalf("projection failed: %v %v", oldFailure, currentFailure)
	}
	meta := api.DuplicateSubject{Identity: input.Meta.Identity, Projection: &oldProjection}
	assessment := dupe.NewAssessment(meta, config.Config{}, []dupe.AssessmentEvidence{{Tracker: "TL", Disposition: dupe.DispositionResolved}})
	payload, err := assessment.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	assessment, err = dupe.UnmarshalAssessment(payload)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.RetainValid(meta, config.Config{}).IsEmpty() {
		t.Fatal("unchanged restored evidence was lost")
	}
	meta.Projection = &currentProjection
	if !assessment.RetainValid(meta, config.Config{}).IsEmpty() {
		t.Fatal("old policy authorized after reconstruction")
	}
}
