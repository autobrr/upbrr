// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
)

func TestImageHostSettingsExposeSafeProbeLimitations(t *testing.T) {
	t.Parallel()
	var backend Backend
	metadata, err := backend.GetImageHostPolicyMetadata()
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"pixhost", "imgbox", "zipline", "samaritano"} {
		if metadata.UnavailableConnectionTests[host] != "" {
			t.Errorf("supported %s lost its connection action", host)
		}
	}
	for _, host := range []string{"imgbb", "lensdump", "onlyimage", "ptscreens", "passtheimage", "utppm", "sharex"} {
		if !strings.Contains(metadata.UnavailableConnectionTests[host], "upload") {
			t.Errorf("%s has no explanation of its upload-only contract", host)
		}
	}
	for _, host := range []string{"dalexni", "seedpool_cdn", "lostimg", "reelflix", "hdb"} {
		if !strings.Contains(metadata.UnavailableConnectionTests[host], "not yet been verified") {
			t.Errorf("%s lost its unverified safe-probe explanation", host)
		}
	}
}

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
