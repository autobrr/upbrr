// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"path/filepath"
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestShouldLookupBlurayWhenDescriptionNeedsBlurayData(t *testing.T) {
	tests := []struct {
		name        string
		cfg         config.Config
		wantLookup  bool
		wantEnabled bool
	}{
		{
			name: "metadata lookup enabled",
			cfg: config.Config{
				Metadata: config.MetadataConfig{GetBlurayInfo: true},
			},
			wantLookup:  true,
			wantEnabled: true,
		},
		{
			name: "description link enabled",
			cfg: config.Config{
				Description: config.DescriptionSettingsConfig{AddBlurayLink: true},
			},
			wantLookup:  true,
			wantEnabled: true,
		},
		{
			name: "description images enabled",
			cfg: config.Config{
				Description: config.DescriptionSettingsConfig{UseBlurayImages: true},
			},
			wantLookup:  true,
			wantEnabled: true,
		},
		{
			name: "all bluray options disabled",
			cfg:  config.Config{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, err := db.Open(filepath.Join(t.TempDir(), "metadata.sqlite"))
			if err != nil {
				t.Fatalf("open repo: %v", err)
			}
			t.Cleanup(func() {
				if err := repo.Close(); err != nil {
					t.Errorf("close repo: %v", err)
				}
			})

			service := NewService(repo, WithConfig(tt.cfg))
			gotEnabled := service.blurayLookupEnabled()
			if gotEnabled != tt.wantEnabled {
				t.Fatalf("blurayLookupEnabled() = %t, want %t", gotEnabled, tt.wantEnabled)
			}

			meta := preparationstate.State{
				DiscType: "BDMV",
				Identity: api.ExternalIdentity{IMDBID: 75784},
			}
			if gotLookup := service.shouldLookupBluray(meta); gotLookup != tt.wantLookup {
				t.Fatalf("shouldLookupBluray() = %t, want %t", gotLookup, tt.wantLookup)
			}
		})
	}
}
