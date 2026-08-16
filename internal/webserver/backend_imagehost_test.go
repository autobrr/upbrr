// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
)

func TestReleaseWorkflowConditionalImageHostsRequireAPIKeys(t *testing.T) {
	t.Parallel()

	assertConfigured := func(host string, cfg config.ImageHostingConfig, want bool) {
		t.Helper()
		if got := releaseWorkflowImageHostConfigured(cfg, host); got != want {
			t.Errorf("host %q configured = %v, want %v", host, got, want)
		}
	}

	assertConfigured("lostimg", config.ImageHostingConfig{LostimgAPI: "secret"}, false)
	assertConfigured("lostimg", config.ImageHostingConfig{LostimgEnabled: true}, false)
	assertConfigured("lostimg", config.ImageHostingConfig{LostimgEnabled: true, LostimgAPI: "secret"}, true)
	assertConfigured("reelflix", config.ImageHostingConfig{ReelflixAPI: "secret"}, false)
	assertConfigured("reelflix", config.ImageHostingConfig{ReelflixEnabled: true}, false)
	assertConfigured("reelflix", config.ImageHostingConfig{ReelflixEnabled: true, ReelflixAPI: "secret"}, true)
	assertConfigured("samaritano", config.ImageHostingConfig{SamaritanoAPI: "secret"}, false)
	assertConfigured("samaritano", config.ImageHostingConfig{SamaritanoEnabled: true}, false)
	assertConfigured("samaritano", config.ImageHostingConfig{SamaritanoEnabled: true, SamaritanoAPI: "secret"}, true)
}
