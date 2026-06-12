// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/services/trackericon"
)

var trackerIconTestPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41,
	0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00,
	0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
	0x42, 0x60, 0x82,
}

func TestHandleTrackerIconUsesBackendRuntimeConfig(t *testing.T) {
	oldDBPath := filepath.Join(t.TempDir(), "old.sqlite")
	newDBPath := filepath.Join(t.TempDir(), "new.sqlite")
	oldCfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: oldDBPath},
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"TEST": {URL: "https://old.example"},
		}},
	}
	newCfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: newDBPath},
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"TEST": {URL: "https://new.example"},
		}},
	}

	iconDir, err := db.Subdir(newDBPath, "tracker-icons")
	if err != nil {
		t.Fatalf("create icon dir: %v", err)
	}
	iconPath := filepath.Join(iconDir, customTrackerIconCacheName("new.example", "https://new.example"))
	if err := os.WriteFile(iconPath, trackerIconTestPNG, 0o600); err != nil {
		t.Fatalf("write cached icon: %v", err)
	}

	server := &Server{
		cfg:     oldCfg,
		backend: &Backend{cfg: newCfg},
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/app/TrackerIcon", strings.NewReader(`{"Domain":"TEST"}`))
	recorder := httptest.NewRecorder()

	server.handleTrackerIcon(recorder, req, session{})

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body %q", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("expected image/png content type, got %q", recorder.Header().Get("Content-Type"))
	}
}

func customTrackerIconCacheName(domain string, customURL string) string {
	sum := sha256.Sum256([]byte(customURL))
	return trackericon.SafeDomainFilename(domain) + "-" + hex.EncodeToString(sum[:])[:16]
}
