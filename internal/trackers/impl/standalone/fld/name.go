// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fld

import (
	"fmt"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func namePolicy() trackers.ReleaseNamePolicyBinding {
	return trackers.StructuredReleaseNamePolicy("standalone/fld/v1", trackers.StructuredNamePolicy{
		Defaults: applyFLDNameDefaults,
	})
}

func applyFLDNameDefaults(editor *trackers.NameEditor, meta api.UploadSubject, _ config.TrackerConfig) error {
	if component, exists := editor.Component(api.NameRoleAudio); exists && component.Present {
		value := strings.ReplaceAll(component.Value, "DD+", "DDP")
		if err := editor.Set(api.NameRoleAudio, value); err != nil {
			return fmt.Errorf("normalize FLD audio: %w", err)
		}
	}
	if isFullDVDDisc(meta) {
		codec := strings.TrimSpace(meta.VideoCodec)
		if codec == "" && len(meta.Release.Codec) > 0 {
			codec = meta.Release.Codec[0]
		}
		if codec != "" {
			if component, exists := editor.Component(api.NameRoleVideoCodec); exists && component.Present {
				if err := editor.MoveBefore(api.NameRoleVideoCodec, api.NameRoleAudio); err != nil {
					return fmt.Errorf("order DVD video codec: %w", err)
				}
			} else if err := editor.InsertBefore(api.NameRoleVideoCodec, codec, api.NameRoleAudio); err != nil {
				return fmt.Errorf("insert DVD video codec: %w", err)
			}
		}
	}
	return nil
}

func isFullDVDDisc(meta api.UploadSubject) bool {
	if strings.EqualFold(strings.TrimSpace(meta.Type), "DVDRIP") {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		return true
	}
	if isDVDSource(meta.Source) || isDVDSource(meta.Release.Source) {
		return true
	}
	return false
}

func isDVDSource(source string) bool {
	upper := strings.ToUpper(strings.TrimSpace(source))
	return slices.Contains([]string{"PAL DVD", "NTSC DVD", "DVD", "NTSC", "PAL"}, upper)
}
