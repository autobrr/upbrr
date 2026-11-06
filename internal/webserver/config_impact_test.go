// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestTLUploadModeChangesWithdrawReviewedDescription(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		change func(*config.TrackerConfig)
	}{
		{"API upload", func(cfg *config.TrackerConfig) { cfg.APIUpload = true }},
		{"native screenshots", func(cfg *config.TrackerConfig) { cfg.ImgRehost = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			previous := config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"TL": {}}}}
			next := config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"TL": {}}}}
			tracker := next.Trackers.Trackers["TL"]
			test.change(&tracker)
			next.Trackers.Trackers["TL"] = tracker
			now := time.Now().UTC()
			state := releaseworkflow.State{
				OwnerID: "owner",
				Workflow: api.ReleaseWorkflow{
					ID:                 "workflow",
					Revision:           1,
					Status:             api.WorkflowStatusActive,
					CreatedAt:          now,
					UpdatedAt:          now,
					FactInstructions:   api.ReleaseFactInstructionSnapshotRef{ID: "facts", Revision: 1},
					Release:            &api.ReleaseSnapshotRef{ID: "release", Revision: 1},
					TrackerCatalog:     &api.TrackerCatalogSnapshotRef{ID: "catalog", Revision: 1},
					TrackerRuntime:     &api.TrackerRuntimeSnapshotRef{ID: "runtime", Revision: 1},
					Selection:          &api.TrackerSelectionRef{ID: "selection", Revision: 1},
					TrackerProjections: &api.TrackerReleaseProjectionSetRef{ID: "projections", Revision: 1},
					Dupes:              &api.DupeAssessmentRef{ID: "dupes", Revision: 1},
					Media:              &api.MediaArtifactSetRef{ID: "media", Revision: 1},
					Descriptions:       &api.DescriptionSetRef{ID: "reviewed", Revision: 1},
					DryRun:             &api.UploadDryRunResultRef{ID: "plan", Revision: 1},
				},
				Projections: map[api.TrackerReleaseProjectionSetID]api.TrackerReleaseProjectionSet{
					"projections": {
						ID:          "projections",
						Revision:    1,
						Projections: []api.TrackerReleaseProjection{{TrackerID: "TL"}},
					},
				},
			}
			payload, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			record := api.ReleaseWorkflowStateRecord{
				OwnerID:    state.OwnerID,
				WorkflowID: state.Workflow.ID,
				Revision:   state.Workflow.Revision,
				Payload:    payload,
			}
			impacts := ConfigImpactDetails(previous, next)
			if len(impacts) == 0 {
				t.Fatal("mode change produced no invalidation")
			}
			for _, impact := range impacts {
				record, err = releaseworkflow.ApplyConfigImpact(record, impact)
				if err != nil {
					t.Fatal(err)
				}
			}
			var restored releaseworkflow.State
			if err := json.Unmarshal(record.Payload, &restored); err != nil {
				t.Fatal(err)
			}
			if restored.Workflow.Descriptions != nil || restored.Workflow.DryRun != nil || restored.Workflow.TrackerProjections != nil {
				t.Fatal("changed mode retained reviewed submission dependencies")
			}
		})
	}
}

func TestConfigImpactsClassifiesEffectiveRoots(t *testing.T) {
	base := config.Config{
		MainSettings:       config.MainSettingsConfig{TMDBAPI: "metadata", InputHistoryLimit: 20},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 3, FrameOverlay: true},
		Trackers:           config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"A": {ImageHost: "imgbb"}}},
	}
	tests := []struct {
		name string
		edit func(*config.Config)
		want api.ConfigImpact
	}{
		{"provider", func(cfg *config.Config) { cfg.Metadata.KeepImages = true }, api.ConfigImpactProvider},
		{"trackers", func(cfg *config.Config) { cfg.Trackers.PreferredTracker = "A" }, api.ConfigImpactTrackers},
		{"description", func(cfg *config.Config) { cfg.Description.AddLogo = true }, api.ConfigImpactDescription},
		{"screenshot selection", func(cfg *config.Config) { cfg.ScreenshotHandling.Screens = 4 }, api.ConfigImpactScreenshotSelection},
		{"screenshot capture", func(cfg *config.Config) { cfg.ScreenshotHandling.ToneMap = true }, api.ConfigImpactScreenshotCapture},
		{"image hosting", func(cfg *config.Config) { cfg.ImageHosting.ImgBBAPI = "key" }, api.ConfigImpactImageHosting},
		{"client injection", func(cfg *config.Config) { cfg.ClientSetup.DefaultClient = "qbit" }, api.ConfigImpactClientInjection},
		{"presentation", func(cfg *config.Config) { cfg.Logging.Level = "debug" }, api.ConfigImpactPresentation},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			next := base
			test.edit(&next)
			if got := ConfigImpactDetails(base, next); !slices.ContainsFunc(got, func(detail api.ConfigImpactDetail) bool { return detail.Kind == test.want }) {
				t.Fatalf("impacts = %v, want %q", got, test.want)
			}
		})
	}
}

func TestConfigImpactsIgnoresHostDatabasePath(t *testing.T) {
	previous := config.Config{MainSettings: config.MainSettingsConfig{DBPath: "one"}}
	next := previous
	next.MainSettings.DBPath = "two"
	if got := ConfigImpactDetails(previous, next); len(got) != 0 {
		t.Fatalf("impacts = %v, want none", got)
	}
}

func TestConfigImpactsClassifiesEveryMainSettingsField(t *testing.T) {
	// MainSettings is split by hand; unlike roots compared as a whole, every
	// new field needs an explicit dependency decision. DBPath is host-owned.
	classified := map[string]api.ConfigImpact{
		"UpdateNotification":  api.ConfigImpactPresentation,
		"VerboseNotification": api.ConfigImpactPresentation,
		"TMDBAPI":             api.ConfigImpactProvider,
		"TrackerPassChecks":   api.ConfigImpactTrackers,
		"InputHistoryLimit":   api.ConfigImpactPresentation,
		"DBPath":              "",
		"UseFavicons":         api.ConfigImpactPresentation,
		"FaviconOnly":         api.ConfigImpactPresentation,
		"SceneDetection":      api.ConfigImpactProvider,
	}
	configType := reflect.TypeFor[config.MainSettingsConfig]()
	for field := range configType.Fields() {
		if !field.IsExported() {
			continue
		}
		want, ok := classified[field.Name]
		if !ok {
			t.Fatalf("MainSettings.%s has no impact classification", field.Name)
		}
		t.Run(field.Name, func(t *testing.T) {
			var next config.Config
			value := reflect.ValueOf(&next.MainSettings).Elem().FieldByIndex(field.Index)
			switch {
			case value.Kind() == reflect.Bool:
				value.SetBool(true)
			case value.Kind() == reflect.Int:
				value.SetInt(1)
			case value.Kind() == reflect.String:
				value.SetString("changed")
			default:
				t.Fatalf("add an effective mutation for MainSettings.%s", field.Name)
			}
			got := ConfigImpactDetails(config.Config{}, next)
			if want == "" {
				if len(got) != 0 {
					t.Fatalf("host-only field impacts = %v", got)
				}
			} else if len(got) != 1 || got[0].Kind != want {
				t.Fatalf("impacts = %v, want only %s", got, want)
			}
		})
	}
	for name := range classified {
		if _, ok := configType.FieldByName(name); !ok {
			t.Fatalf("classification for removed field %s", name)
		}
	}
}
