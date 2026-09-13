// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBTNSceneReleasesBypassActiveClaims(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name      string
		scene     bool
		sceneName string
		wantClaim bool
	}{
		{name: "confirmed scene", scene: true},
		{name: "resolved scene name", sceneName: "Example.Show.S01E01.2160p-GRP"},
		{name: "unknown origin remains claimed", wantClaim: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.Config{}
			cfg.MainSettings.DBPath = filepath.Join(t.TempDir(), "test.sqlite")
			cachePath, err := btnClaimsPath(cfg.MainSettings.DBPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := writeBTNClaimedCache(cachePath, btnTitleVariants("Example Show")); err != nil {
				t.Fatal(err)
			}
			checker := New().NewClaimChecker(cfg, nil)
			claimed, err := checker.HasClaim(t.Context(), api.UploadSubject{
				SeasonInt:   1,
				EpisodeInt:  1,
				Identity:    api.ExternalIdentity{Category: "TV"},
				ReleaseName: "Example.Show.S01E01.2160p-GRP",
				Scene:       tt.scene,
				SceneName:   tt.sceneName,
			})
			if err != nil || claimed != tt.wantClaim {
				t.Fatalf("claimed=%t want=%t err=%v", claimed, tt.wantClaim, err)
			}
		})
	}
}
