// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ff

import (
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func namePolicy() trackers.ReleaseNamePolicyBinding {
	return trackers.StructuredReleaseNamePolicy("standalone/ff/v2", trackers.StructuredNamePolicy{
		Defaults:  dotSeparateFFComponents,
		Separator: ".",
		ExactName: func(meta api.UploadSubject, _ config.TrackerConfig) string {
			if meta.Scene {
				return meta.SceneName
			}
			return ""
		},
		Search: func(meta api.UploadSubject, _ config.TrackerConfig) string {
			if meta.Anime {
				if title := strings.TrimSpace(meta.Release.Title); title != "" {
					return title
				}
				return strings.TrimSpace(meta.ReleaseName)
			}
			return ""
		},
	})
}

func dotSeparateFFComponents(editor *trackers.NameEditor, _ api.UploadSubject, _ config.TrackerConfig) error {
	for _, role := range editor.PresentRoles() {
		component, _ := editor.Component(role)
		value := api.CleanReleaseNameFilename(component.Value)
		if err := editor.Set(role, strings.ReplaceAll(strings.Join(strings.Fields(value), " "), " ", ".")); err != nil {
			return fmt.Errorf("normalize FF %s: %w", role, err)
		}
	}
	return nil
}
