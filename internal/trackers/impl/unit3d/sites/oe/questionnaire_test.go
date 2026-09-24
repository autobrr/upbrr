// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package oe

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestQuestionnaireUsesResolvedEncodeAndGroupFacts(t *testing.T) {
	for _, tc := range []struct {
		name string
		meta api.UploadSubject
		want []string
	}{
		{
name: "AV1 encode",
 meta: api.UploadSubject{Type: "ENCODE", VideoCodec: "AV1"},
 want: []string{oeEncodingSettingsKey},
},
		{
name: "AV1 WEBRip",
 meta: api.UploadSubject{Type: "WEBRIP", VideoCodec: "AV1"},
 want: []string{oeEncodingSettingsKey},
},
		{
name: "AV1 DVD rip",
 meta: api.UploadSubject{Type: "DVDRIP", VideoCodec: "AV1"},
 want: []string{oeEncodingSettingsKey},
},
		{name: "MediaInfo settings", meta: api.UploadSubject{
Type: "ENCODE",
 VideoCodec: "AV1",
 HasEncodeSettings: true,
}},
		{name: "AV1 WEB-DL", meta: api.UploadSubject{Type: "WEBDL", VideoCodec: "AV1"}},
		{name: "other codec", meta: api.UploadSubject{Type: "ENCODE", VideoCodec: "HEVC"}},
		{
name: "normalized group",
 meta: api.UploadSubject{Tag: " -sM737 "},
 want: []string{oeSourceNotesKey},
},
		{name: "different group", meta: api.UploadSubject{Tag: "SM737-other"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var keys []string
			if schema := inputSchema(tc.meta); schema != nil {
				for _, field := range schema.Fields {
					keys = append(keys, field.Key)
					if !field.Required || field.Kind != "textarea" {
						t.Fatalf("unexpected evidence control: %+v", field)
					}
				}
			}
			if !slices.Equal(keys, tc.want) {
				t.Fatalf("fields = %v, want %v", keys, tc.want)
			}
		})
	}
	if slices.Contains(BannedGroups(), "SM737") {
		t.Fatal("SM737 must be conditional on source notes, not unconditionally banned")
	}
}
