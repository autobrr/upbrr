// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dc

import (
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func namePolicy() trackers.ReleaseNamePolicyBinding {
	return trackers.StructuredReleaseNamePolicy("standalone/dc/v2", trackers.StructuredNamePolicy{
		Defaults: normalizeDCComponents,
		ExactName: func(meta api.UploadSubject, _ config.TrackerConfig) string {
			if meta.Scene && strings.TrimSpace(meta.SceneName) != "" {
				return strings.TrimSpace(meta.SceneName) + " [UNRAR]"
			}
			return ""
		},
	})
}

func normalizeDCComponents(editor *trackers.NameEditor, _ api.UploadSubject, _ config.TrackerConfig) error {
	for _, role := range editor.PresentRoles() {
		component, _ := editor.Component(role)
		value := api.CleanReleaseNameFilename(component.Value)
		if role == api.NameRoleAudio {
			value = strings.NewReplacer("DD+", "DDP", "DTS:", "DTS-").Replace(value)
		}
		if role == api.NameRoleHDR {
			value = strings.ReplaceAll(value, "HDR10+", "HDR10P")
		}
		value = normalizeDCComponent(value)
		if value == "" {
			if err := editor.Omit(role); err != nil {
				return fmt.Errorf("omit empty DC %s: %w", role, err)
			}
			continue
		}
		if err := editor.Set(role, value); err != nil {
			return fmt.Errorf("normalize DC %s: %w", role, err)
		}
	}
	return nil
}

func normalizeDCComponent(value string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == ' ' || r == '.' || r == '-' {
			return r
		}
		return -1
	}, value))
}
