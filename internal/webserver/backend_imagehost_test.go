// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
)

func TestReleaseWorkflowImageHostConfiguredSamaritanoRequiresAPIKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  config.ImageHostingConfig
		want bool
	}{
		{name: "disabled with key", cfg: config.ImageHostingConfig{SamaritanoAPI: "secret"}},
		{name: "enabled without key", cfg: config.ImageHostingConfig{SamaritanoEnabled: true}},
		{
			name: "enabled with key",
			cfg:  config.ImageHostingConfig{SamaritanoEnabled: true, SamaritanoAPI: "secret"},
			want: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := releaseWorkflowImageHostConfigured(test.cfg, "samaritano"); got != test.want {
				t.Fatalf("configured = %v, want %v", got, test.want)
			}
		})
	}
}
