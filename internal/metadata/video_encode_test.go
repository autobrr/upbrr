// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDeriveMediaFactsUsesEffectiveTypeForVideoEncode(t *testing.T) {
	for _, test := range []struct {
		name, filename, format, profile, library, correction, wantType, wantEncode, wantNameVideo string
		wantRole                                                                                  api.ReleaseNameRole
	}{
		{
			name:          "unknown anime type",
			filename:      "Example Anime S01 (BD 1080p HEVC) [GRP]",
			format:        "HEVC",
			library:       "x265",
			wantType:      "ENCODE",
			wantEncode:    "x265",
			wantNameVideo: "x265",
			wantRole:      api.NameRoleVideoEncode,
		},
		{
			name:          "unknown type without writing library",
			filename:      "Example Anime S01 (BD 1080p HEVC) [GRP]",
			format:        "HEVC",
			wantType:      "ENCODE",
			wantEncode:    "x265",
			wantNameVideo: "x265",
			wantRole:      api.NameRoleVideoEncode,
		},
		{
			name:          "manual encode",
			filename:      "Example Anime S01 (BD 1080p HEVC) [GRP]",
			format:        "HEVC",
			correction:    "ENCODE",
			wantType:      "ENCODE",
			wantEncode:    "x265",
			wantNameVideo: "x265",
			wantRole:      api.NameRoleVideoEncode,
		},
		{
			name:          "remux corrected to encode",
			filename:      "Example.Show.S01.1080p.BluRay.REMUX.HEVC-GRP",
			format:        "HEVC",
			correction:    "ENCODE",
			wantType:      "ENCODE",
			wantEncode:    "x265",
			wantNameVideo: "x265",
			wantRole:      api.NameRoleVideoEncode,
		},
		{
			name:          "encode corrected to remux",
			filename:      "Example.Show.S01.1080p.BluRay.x265-GRP",
			format:        "HEVC",
			correction:    "REMUX",
			wantType:      "REMUX",
			wantNameVideo: "HEVC",
			wantRole:      api.NameRoleVideoCodec,
		},
		{
			name:          "encode corrected to web download",
			filename:      "Example.Show.S01.1080p.BluRay.x265-GRP",
			format:        "HEVC",
			correction:    "WEBDL",
			wantType:      "WEBDL",
			wantEncode:    "H.265",
			wantNameVideo: "H.265",
			wantRole:      api.NameRoleVideoEncode,
		},
		{
			name:          "web download preserved",
			filename:      "Example.Show.S01.1080p.WEB-DL.H265-GRP",
			format:        "HEVC",
			wantType:      "WEBDL",
			wantEncode:    "H.265",
			wantNameVideo: "H.265",
			wantRole:      api.NameRoleVideoEncode,
		},
		{
			name:          "remux preserved",
			filename:      "Example.Show.S01.1080p.BluRay.REMUX.HEVC-GRP",
			format:        "HEVC",
			wantType:      "REMUX",
			wantNameVideo: "HEVC",
			wantRole:      api.NameRoleVideoCodec,
		},
		{
			name:          "AVC profile preserved",
			filename:      "Example Anime S01 (BD 1080p AVC) [GRP]",
			format:        "AVC",
			profile:       "High 10",
			wantType:      "ENCODE",
			wantEncode:    "Hi10P x264",
			wantNameVideo: "Hi10P x264",
			wantRole:      api.NameRoleVideoEncode,
		},
		{
			name:          "unrecognized encoder preserved",
			filename:      "Example.Show.S01.1080p.BluRay.x265-GRP",
			format:        "MPEG-4 Visual",
			library:       "ffmpeg",
			wantType:      "ENCODE",
			wantNameVideo: "MPEG-4 Visual",
			wantRole:      api.NameRoleVideoCodec,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			miPath := filepath.Join(t.TempDir(), "mediainfo.json")
			report := fmt.Sprintf(`{"media":{"track":[{"@type":"General"},{"@type":"Video","Format":%q,"Format_Profile":%q,"BitDepth":"10","Encoded_Library_Name":%q}]}}`, test.format, test.profile, test.library)
			if err := os.WriteFile(miPath, []byte(report), 0o600); err != nil {
				t.Fatal(err)
			}
			input := preparationstate.State{
				SourcePath:        test.filename,
				MediaInfoJSONPath: miPath,
				Release:           ParseReleaseInfo(test.filename),
				SeasonStr:         "S01",
				TVPack:            true,
			}
			if test.correction != "" {
				input.ReleaseNameOverrides.Type = &test.correction
			}
			meta, err := NewService(&fakeRepo{}, WithConfig(config.Config{})).deriveMediaFacts(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			if meta.ResolvedNaming.Type != test.wantType || meta.VideoEncode != test.wantEncode || meta.VideoCodec != test.format || meta.BitDepth != "10" {
				t.Fatalf("type/encode/codec/depth = %q/%q/%q/%q, want %q/%q/%q/10", meta.ResolvedNaming.Type, meta.VideoEncode, meta.VideoCodec, meta.BitDepth, test.wantType, test.wantEncode, test.format)
			}
			if !strings.Contains(meta.ReleaseName, test.wantNameVideo) {
				t.Fatalf("name %q missing %q", meta.ReleaseName, test.wantNameVideo)
			}
			for _, document := range []*api.ReleaseNameDocument{meta.GeneratedName, meta.AvailableGeneratedName} {
				component, ok := document.Component(test.wantRole)
				if !ok || !component.Present || component.Value != test.wantNameVideo || component.AvailableValue != test.wantNameVideo {
					t.Fatalf("video component = %#v, present=%v; want %q", component, ok, test.wantNameVideo)
				}
			}
		})
	}
}
