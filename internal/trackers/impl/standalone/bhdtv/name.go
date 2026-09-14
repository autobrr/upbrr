// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later
package bhdtv

import (
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func namePolicy() trackers.ReleaseNamePolicyBinding {
	return trackers.StructuredReleaseNamePolicy("standalone/bhdtv/v2", trackers.StructuredNamePolicy{Defaults: applyNameDefaults, Separator: "."})
}
func applyNameDefaults(editor *trackers.NameEditor, _ api.UploadSubject, _ config.TrackerConfig) error {
	for _, role := range editor.PresentRoles() {
		c, _ := editor.Component(role)
		value := strings.ReplaceAll(c.Value, ":", ".")
		if role == api.NameRoleAudio {
			value = strings.ReplaceAll(value, "DD+", "DDP")
		}
		value = strings.Join(strings.FieldsFunc(value, func(r rune) bool { return r == '.' || r == ' ' }), ".")
		if value == "" {
			if err := editor.Omit(role); err != nil {
				return fmt.Errorf("omit empty BHDTV %s: %w", role, err)
			}
		} else if err := editor.Set(role, value); err != nil {
			return fmt.Errorf("normalize BHDTV %s: %w", role, err)
		}
	}
	return nil
}
