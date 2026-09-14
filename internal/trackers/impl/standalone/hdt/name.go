// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdt

import (
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func namePolicy() trackers.ReleaseNamePolicyBinding {
	return trackers.StructuredReleaseNamePolicy("standalone/hdt/v2", trackers.StructuredNamePolicy{
		Defaults: applyNameDefaults,
		Search: func(meta api.UploadSubject, _ config.TrackerConfig) string {
			if meta.Identity.IMDBID != 0 {
				return ""
			}
			return strings.TrimSpace(meta.Release.Title)
		},
	})
}
func applyNameDefaults(editor *trackers.NameEditor, meta api.UploadSubject, _ config.TrackerConfig) error {
	switch strings.ToUpper(strings.TrimSpace(meta.Type)) {
	case "WEBDL", "WEBRIP", "ENCODE":
		if c, ok := editor.Component(api.NameRoleAudio); ok {
			if err := editor.Set(c.Role, strings.Replace(c.Value, " ", "", 1)); err != nil {
				return fmt.Errorf("normalize HDT audio: %w", err)
			}
		}
	}
	if c, ok := editor.Component(api.NameRoleHDR); ok && c.Present {
		if err := editor.Set(c.Role, strings.ReplaceAll(c.Value, "DV", "DoVi")); err != nil {
			return fmt.Errorf("normalize HDT HDR: %w", err)
		}
	}
	if strings.EqualFold(meta.Type, "REMUX") && strings.EqualFold(meta.Source, "BluRay") {
		if err := editor.Set(api.NameRoleSource, "Blu-ray"); err != nil {
			return fmt.Errorf("set HDT remux source: %w", err)
		}
		if err := editor.Set(api.NameRoleVideoFormat, "Remux"); err != nil {
			return fmt.Errorf("set HDT remux format: %w", err)
		}
	}
	for _, role := range editor.PresentRoles() {
		c, _ := editor.Component(role)
		value := strings.ReplaceAll(c.Value, ":", "")
		if strings.TrimSpace(value) == "" {
			if err := editor.Omit(role); err != nil {
				return fmt.Errorf("omit empty HDT %s: %w", role, err)
			}
		} else if err := editor.Set(role, value); err != nil {
			return fmt.Errorf("sanitize HDT %s: %w", role, err)
		}
	}
	return nil
}
