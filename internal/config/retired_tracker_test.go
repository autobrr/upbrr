// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRemovedTrackerConfigImport(t *testing.T) {
	t.Parallel()
	for name, decode := range map[string]func(*Config) error{
		"json": func(cfg *Config) error {
			return json.Unmarshal([]byte(`{"Trackers":{"DefaultTrackers":["AITHER"," thr "],"PreferredTracker":"ThR","Trackers":{"tHr":{"Password":"upbrr-enc:v1:invalid","PronfoAPIKey":"synthetic-retired-secret"},"RETIRED":{"PronfoRAPIID":"synthetic-retired-secret","keep_me":"retained","ImgAPI":"synthetic-shared-secret"}}}}`), cfg)
		},
		"yaml": func(cfg *Config) error {
			return yaml.Unmarshal([]byte("trackers:\n  default_trackers: [AITHER, ' thr ']\n  preferred_tracker: ThR\n  tHr:\n    password: upbrr-enc:v1:invalid\n    pronfo_api_key: synthetic-retired-secret\n  RETIRED:\n    pronfo_rapi_id: synthetic-retired-secret\n    keep_me: retained\n    img_api: synthetic-shared-secret\n"), cfg)
		},
	} {
		t.Run(name, func(t *testing.T) {
			var cfg Config
			if err := decode(&cfg); err != nil {
				t.Fatal(err)
			}
			if _, ok := cfg.Trackers.Trackers["tHr"]; ok {
				t.Fatal("removed tracker retained")
			}
			if !slices.Equal(cfg.Trackers.DefaultTrackers, CSVList{"AITHER"}) || cfg.Trackers.PreferredTracker != "" {
				t.Fatal("removed tracker selections retained")
			}
			other := cfg.Trackers.Trackers["RETIRED"]
			if other.Unknown["keep_me"] != "retained" || other.ImgAPI != "synthetic-shared-secret" {
				t.Fatal("unrelated configuration lost")
			}
			for _, marshal := range []func(any) ([]byte, error){json.Marshal, yaml.Marshal} {
				out, err := marshal(&cfg)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(strings.ToLower(string(out)), "pronfo") || strings.Contains(string(out), "synthetic-retired-secret") {
					t.Fatal("retired fields leaked into export")
				}
			}
		})
	}
}

func TestRemovedTrackerEncryptedImportAndBackup(t *testing.T) {
	t.Parallel()
	envelope, err := encryptSecretString("synthetic-retired-secret", "synthetic-old-helper")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{"Trackers": map[string]any{"Trackers": map[string]any{"THR": map[string]any{"Password": envelope, "PronfoAPIKey": envelope}}}})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ImportFromJSONEncrypted(string(payload))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Trackers.Trackers) != 0 {
		t.Fatal("encrypted removed tracker imported")
	}
	path, err := BackupToYAML(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	restored, err := ImportFromYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Trackers.Trackers) != 0 {
		t.Fatal("removed tracker restored from backup")
	}
}

func TestRemovedTrackerExportDoesNotMutateSource(t *testing.T) {
	t.Parallel()
	cfg := Config{Trackers: TrackersConfig{
		DefaultTrackers:  CSVList{"THR", "AITHER"},
		PreferredTracker: "THR",
		Trackers:         map[string]TrackerConfig{"THR": {Password: "synthetic-retired-secret"}, "RETIRED": {Unknown: map[string]any{"PronfoAPIKey": "synthetic-retired-secret", "keep_me": "retained"}}},
	}}
	for _, marshal := range []func(any) ([]byte, error){json.Marshal, yaml.Marshal} {
		payload, err := marshal(&cfg)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(payload), "THR") || strings.Contains(string(payload), "synthetic-retired-secret") {
			t.Fatal("retired configuration exported")
		}
	}
	if len(cfg.Trackers.Trackers) != 2 || len(cfg.Trackers.DefaultTrackers) != 2 || cfg.Trackers.PreferredTracker != "THR" || cfg.Trackers.Trackers["RETIRED"].Unknown["PronfoAPIKey"] != "synthetic-retired-secret" {
		t.Fatal("export mutated caller's configuration")
	}
}
