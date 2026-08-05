// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

type applicationInfoCapabilityProvider struct {
	info api.DVDMenuEngineInfo
	err  error
}

type nilApplicationInfoCapabilityProvider struct{}

func (*nilApplicationInfoCapabilityProvider) DVDMenuCapability(context.Context) (api.DVDMenuEngineInfo, error) {
	panic("typed-nil provider must not be called")
}

func (p applicationInfoCapabilityProvider) DVDMenuCapability(context.Context) (api.DVDMenuEngineInfo, error) {
	return p.info, p.err
}

func TestCurrentApplicationInfoIncludesDVDMenuCapability(t *testing.T) {
	info := CurrentApplicationInfo(context.Background(), applicationInfoCapabilityProvider{info: api.DVDMenuEngineInfo{
		EngineVersion:     "phase0a-1",
		FFmpegVersion:     "ffmpeg version example",
		FFmpegDVDVideo:    true,
		SupportedFeatures: []string{"ifo_inventory"},
	}})
	if info.DVDMenuCapabilityStatus != DVDMenuCapabilityAvailable || !info.DVDMenuEngine.FFmpegDVDVideo {
		t.Fatalf("DVD menu diagnostics = %#v", info)
	}
	if info.DVDMenuEngine.EngineVersion != "phase0a-1" || info.DVDMenuEngine.FFmpegVersion != "ffmpeg version example" {
		t.Fatalf("DVD menu engine info = %#v", info.DVDMenuEngine)
	}
}

func TestCurrentApplicationInfoRejectsTypedNilCapability(t *testing.T) {
	t.Parallel()

	var provider *nilApplicationInfoCapabilityProvider
	info := CurrentApplicationInfo(context.Background(), provider)
	if info.DVDMenuCapabilityStatus != DVDMenuCapabilityUnavailable {
		t.Fatalf("expected unavailable status, got %q", info.DVDMenuCapabilityStatus)
	}
}

func TestCurrentApplicationInfoReportsPathFreeIncompatibleCapability(t *testing.T) {
	info := CurrentApplicationInfo(context.Background(), applicationInfoCapabilityProvider{
		info: api.DVDMenuEngineInfo{
			EngineVersion:        "phase0a-1",
			MissingFFmpegOptions: []string{"-menu_vts", "-pgc"},
		},
		err: errors.New(`inspect C:\path\to\ffmpeg.exe`),
	})
	if info.DVDMenuCapabilityStatus != DVDMenuCapabilityIncompatible {
		t.Fatalf("capability status = %q", info.DVDMenuCapabilityStatus)
	}
	if strings.Contains(info.DVDMenuCapabilityMessage, `C:\path\to`) {
		t.Fatal("capability diagnostic leaked a local path")
	}
	if !strings.Contains(info.DVDMenuCapabilityMessage, "-menu_vts, -pgc") {
		t.Fatalf("capability message = %q", info.DVDMenuCapabilityMessage)
	}
}
