// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestPrintCLIVersion(t *testing.T) {
	info := api.ApplicationInfo{
		Version:         "v1.2.3",
		BuildIdentifier: "abcdef123456",
		BuildTime:       "2026-09-20T01:02:03Z",
		Dependencies: []api.ApplicationDependency{
			{Path: "github.com/autobrr/go-bdinfo", Version: "v0.4.2"},
			{Path: "github.com/autobrr/rls", Version: "v0.9.0 (replacement: v0.9.1)"},
			{Path: "github.com/autobrr/mkbrr", Version: "v1.25.1 (replacement: local build)"},
		},
		GoVersion:                "go1.27.1",
		GOOS:                     "windows",
		GOARCH:                   "amd64",
		DVDMenuCapabilityStatus:  "available",
		DVDMenuCapabilityMessage: "Compatible FFmpeg dvdvideo menu support detected.",
		DVDMenuEngine: api.DVDMenuEngineInfo{
			EngineVersion:     "engine-test",
			SchemaVersion:     1,
			SupportedFeatures: []string{"ifo_inventory", "navigation"},
			FFmpegVersion:     "ffmpeg version example",
			FFmpegDVDVideo:    true,
		},
	}
	var output bytes.Buffer
	printCLIVersion(&output, info)
	want := "upbrr v1.2.3\nBuild: abcdef123456 (2026-09-20 01:02:03 UTC)\nGo: go1.27.1\nPlatform: windows/amd64\n" +
		"\nAutobrr dependencies:\n" +
		"  github.com/autobrr/go-bdinfo v0.4.2\n" +
		"  github.com/autobrr/rls v0.9.0 (replacement: v0.9.1)\n" +
		"  github.com/autobrr/mkbrr v1.25.1 (replacement: local build)\n" +
		"\nFFmpeg: ffmpeg version example\nDVD menu engine: engine-test\nDVD metadata schema: 1\n" +
		"DVD engine features: ifo_inventory, navigation\nFFmpeg dvdvideo menu support: available\n" +
		"  Compatible FFmpeg dvdvideo menu support detected.\n"
	if output.String() != want {
		t.Fatalf("version output = %q, want %q", output.String(), want)
	}
}

func TestPrintCLIVersionWithoutBuildOrFFmpeg(t *testing.T) {
	var output bytes.Buffer
	printCLIVersion(&output, api.ApplicationInfo{
		Version:                  "dev",
		DVDMenuCapabilityStatus:  "unavailable",
		DVDMenuCapabilityMessage: "FFmpeg was not found or its dvdvideo menu capability could not be inspected.",
	})
	for _, want := range []string{
		"Build: unknown\n",
		"unavailable (module build information not embedded)",
		"FFmpeg: unavailable\n",
		"FFmpeg dvdvideo menu support: unavailable\n",
		"FFmpeg was not found",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("version output = %q, want %q", output.String(), want)
		}
	}
}

func TestCLIApplicationVersion(t *testing.T) {
	for _, test := range []struct {
		info api.ApplicationInfo
		want string
	}{
		{api.ApplicationInfo{Version: "v0.3.4", BuildIdentifier: "abcdef123456"}, "v0.3.4"},
		{api.ApplicationInfo{
			Version:         "dev",
			BuildIdentifier: "abcdef123456-dirty",
			BuildTime:       "2026-09-20T01:02:03Z",
		}, "abcdef123456-dirty (2026-09-20 01:02:03 UTC)"},
		{api.ApplicationInfo{Version: "dev", BuildIdentifier: "abcdef123456"}, "abcdef123456"},
		{api.ApplicationInfo{Version: "dev"}, "dev"},
	} {
		if got := cliApplicationVersion(test.info); got != test.want {
			t.Errorf("application version = %q, want %q", got, test.want)
		}
	}
}
