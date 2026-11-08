// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"reflect"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

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

func TestConfigImpactsIgnoreCompatibilitySettingsAndRetainActiveTrackerScope(t *testing.T) {
	base := config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"BJS": {}, "FF": {}}}, TorrentClients: map[string]config.TorrentClientConfig{"qbit": {}}}
	next := config.Config{
		MainSettings: config.MainSettingsConfig{
			UpdateNotification:  true,
			VerboseNotification: true,
			TrackerPassChecks:   2,
		},
		Metadata: config.MetadataConfig{
			UserOverrides: true,
			PingUnit3D:    true,
			CheckPredb:    true,
		},
		ScreenshotHandling: config.ScreenshotHandlingConfig{CutoffScreens: 3},
		Description:        config.DescriptionSettingsConfig{MultiScreens: 2, CharLimit: 100},
		ArrIntegration:     config.ArrIntegrationConfig{EmbyDir: "legacy"},
		TorrentCreation:    config.TorrentCreationConfig{MkbrrThreads: 2},
		PostUpload:         config.PostUploadConfig{CrossSeeding: true, ShowUploadDuration: true},
		Trackers:           config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"BJS": {}, "FF": {CheckRequests: true}}},
		TorrentClients:     map[string]config.TorrentClientConfig{"qbit": {StorageDir: "legacy"}},
	}
	if got := ConfigImpactDetails(base, next); len(got) != 0 {
		t.Fatalf("compatibility-only impacts = %v", got)
	}
	for _, edit := range []func(*config.TrackerConfig){
		func(c *config.TrackerConfig) { c.Anon = true },
		func(c *config.TrackerConfig) { c.ShowGroupIfAnon = true },
	} {
		tracker := config.TrackerConfig{}
		edit(&tracker)
		next.Trackers.Trackers["BJS"] = tracker
		got := ConfigImpactDetails(base, next)
		if len(got) != 1 || got[0].Kind != api.ConfigImpactTrackers || !slices.Equal(got[0].TrackerIDs, []api.TrackerID{"BJS"}) {
			t.Fatalf("mixed active/compatibility impacts = %v", got)
		}
	}
	if next.TorrentClients["qbit"].StorageDir != "legacy" || !next.Trackers.Trackers["FF"].CheckRequests {
		t.Fatal("impact comparison changed saved compatibility values")
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
		"UpdateNotification":  "",
		"VerboseNotification": "",
		"TMDBAPI":             api.ConfigImpactProvider,
		"TrackerPassChecks":   "",
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
					t.Fatalf("non-runtime field impacts = %v", got)
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
