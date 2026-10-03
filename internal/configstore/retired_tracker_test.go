// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package configstore_test

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/configstore"
	"github.com/autobrr/upbrr/internal/services/db"
)

func TestRemovedTrackerStartup(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"fresh", "current", "repair-failure"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "upbrr.db")
			repo, err := db.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			if err := repo.Migrate(); err != nil {
				t.Fatal(err)
			}
			const old = `{"DefaultTrackers":["AITHER"," thr "],"PreferredTracker":"THR","Trackers":{"THR":{"Password":"upbrr-enc:v1:unreadable","PronfoAPIKey":"upbrr-enc:v1:unreadable","ImageHost":"thr"},"RETIRED":{"keep_me":"retained","pronfo_api_key":"synthetic-retired-secret"}}}`
			if mode == "fresh" {
				cfg, err := config.LoadEmbeddedDefaultConfig()
				if err != nil {
					t.Fatal(err)
				}
				cfg.MainSettings.DBPath = path
				cfg.TorrentClients = map[string]config.TorrentClientConfig{}
				if err := configstore.SaveToDBPath(t.Context(), cfg, path); err != nil {
					t.Fatal(err)
				}
			}
			if mode != "fresh" {
				if err := repo.SaveConfigSection(t.Context(), "Trackers", json.RawMessage(old)); err != nil {
					t.Fatal(err)
				}
				if _, err := repo.RawDB().ExecContext(t.Context(), `INSERT INTO upload_records (tracker,status,created_at) VALUES ('THR','uploaded','2026-01-01')`); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "repair-failure" {
				if _, err := repo.RawDB().ExecContext(t.Context(), `CREATE TRIGGER reject_config BEFORE INSERT ON config_settings WHEN NEW.section = 'Trackers' BEGIN SELECT RAISE(ABORT, 'synthetic write failure'); END`); err != nil {
					t.Fatal(err)
				}
				cfg, err := configstore.LoadFromDBPath(t.Context(), path)
				if err == nil || cfg != nil {
					t.Fatal("expected failed startup with no config")
				}
				var untouched json.RawMessage
				if err := repo.LoadConfigSection(t.Context(), "Trackers", &untouched); err != nil {
					t.Fatal(err)
				}
				if string(untouched) != old {
					t.Fatal("failed repair modified original tracker settings")
				}
				var count int
				if err := repo.RawDB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM config_settings`).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 1 {
					t.Fatal("failed repair committed partial sections")
				}
				if _, err := repo.RawDB().ExecContext(t.Context(), `DROP TRIGGER reject_config`); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				cfg, err := configstore.LoadFromDBPath(t.Context(), path)
				if err != nil {
					t.Fatal(err)
				}
				if err := cfg.Validate(); err != nil {
					t.Fatal(err)
				}
				if _, ok := cfg.Trackers.Trackers["THR"]; ok {
					t.Fatal("removed tracker retained")
				}
				if cfg.Trackers.PreferredTracker != "" {
					t.Fatal("removed preferred tracker retained")
				}
				if mode != "fresh" && !slices.Equal(cfg.Trackers.DefaultTrackers, []string{"AITHER"}) {
					t.Fatal("default selection not migrated")
				}
				var raw json.RawMessage
				if err := repo.LoadConfigSection(t.Context(), "Trackers", &raw); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(strings.ToLower(string(raw)), "pronfo") || strings.Contains(string(raw), "unreadable") || strings.Contains(string(raw), "synthetic-retired-secret") {
					t.Fatal("retired secrets retained in database config")
				}
				if mode != "fresh" {
					if cfg.Trackers.Trackers["RETIRED"].Unknown["keep_me"] != "retained" {
						t.Fatal("unrelated extension lost")
					}
					var count int
					if err := repo.RawDB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM upload_records WHERE tracker = 'THR' AND status = 'uploaded'`).Scan(&count); err != nil {
						t.Fatal(err)
					}
					if count != 1 {
						t.Fatal("historical upload lost")
					}
				}
			}
		})
	}
}
