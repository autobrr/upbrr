// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rtf

import (
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
)

func TestRTFAPISessionDoesNotCrossCredentialOrSiteBindings(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	writeRTFWebAuthFixture(t, dbPath)
	configured := config.TrackerConfig{
APIKey: "configured-key",
 Username: "user",
 Password: "pass",
}
	seedRTFConfig(t, dbPath, configured)
	baseURL := "https://retroflix.example"
	if err := persistRefreshedRTFAPIKey(ctx, dbPath, baseURL, configured, "session-token"); err != nil {
		t.Fatalf("persist API session: %v", err)
	}

	for _, tt := range []struct {
		name string
		base string
		cfg  config.TrackerConfig
	}{
		{
name: "changed api key",
 base: baseURL,
 cfg: config.TrackerConfig{
APIKey: "replacement-key",
 Username: "user",
 Password: "pass",
},
},
		{
name: "changed credentials",
 base: baseURL,
 cfg: config.TrackerConfig{
APIKey: "configured-key",
 Username: "user",
 Password: "replacement-pass",
},
},
		{
name: "changed site",
 base: "https://other-retroflix.example",
 cfg: configured,
},
	} {
		t.Run(tt.name, func(t *testing.T) {
			token, err := loadCachedRTFAPIKey(ctx, dbPath, tt.base, tt.cfg)
			if err != nil {
				t.Fatalf("load API session: %v", err)
			}
			if token != "" {
				t.Fatal("session token was reused after its configuration binding changed")
			}
		})
	}
}
