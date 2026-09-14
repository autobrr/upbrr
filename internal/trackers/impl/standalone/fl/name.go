// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fl

import (
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

func namePolicy() trackers.ReleaseNamePolicyBinding {
	return trackers.StructuredReleaseNamePolicy("standalone/fl/v2", trackers.StructuredNamePolicy{
		Defaults:            applyNameDefaults,
		Separator:           ".",
		SearchGeneratedName: true,
		ExactName: func(meta api.UploadSubject, _ config.TrackerConfig) string {
			return standalone.QuestionnaireAnswers(meta, "FL")["name"]
		},
		Search: func(meta api.UploadSubject, _ config.TrackerConfig) string {
			if meta.Identity.IMDBID != 0 {
				return ""
			}
			return strings.TrimSpace(meta.Release.Title)
		},
	})
}
func applyNameDefaults(editor *trackers.NameEditor, meta api.UploadSubject, _ config.TrackerConfig) error {
	if c, ok := editor.Component(api.NameRoleHDR); ok && c.Present {
		value := strings.NewReplacer("DV", "DoVi", "PQ10", "HDR", "HDR10+", "HDR").Replace(c.Value)
		if err := editor.Set(c.Role, value); err != nil {
			return fmt.Errorf("normalize FL HDR: %w", err)
		}
	}
	if c, ok := editor.Component(api.NameRoleAudio); ok {
		if err := editor.Set(c.Role, strings.ReplaceAll(c.Value, "DD+", "DDP")); err != nil {
			return fmt.Errorf("normalize FL audio: %w", err)
		}
	}
	if strings.EqualFold(meta.Type, "REMUX") && strings.EqualFold(meta.Source, "BluRay") {
		if err := editor.Omit(api.NameRoleSource); err != nil {
			return fmt.Errorf("omit FL remux source: %w", err)
		}
		if err := editor.Set(api.NameRoleVideoFormat, "Remux"); err != nil {
			return fmt.Errorf("set FL remux format: %w", err)
		}
	}
	return nil
}
