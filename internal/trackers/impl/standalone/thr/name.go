// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later
package thr

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

var thrReleaseNamePattern = regexp.MustCompile(`[^0-9a-zA-Z. '\-\[\]]+`)

func namePolicy() trackers.ReleaseNamePolicyBinding {
	return trackers.StructuredReleaseNamePolicy("standalone/thr/v2", trackers.StructuredNamePolicy{
		Defaults: applyNameDefaults,
		ExactName: func(meta api.UploadSubject, _ config.TrackerConfig) string {
			return standalone.QuestionnaireAnswers(meta, "THR")["name_override"]
		},
	})
}
func applyNameDefaults(editor *trackers.NameEditor, _ api.UploadSubject, _ config.TrackerConfig) error {
	for _, role := range editor.PresentRoles() {
		c, _ := editor.Component(role)
		value := c.Value
		if role == api.NameRoleAudio {
			value = strings.ReplaceAll(value, "DD+", "DDP")
		}
		value = strings.TrimSpace(thrReleaseNamePattern.ReplaceAllString(value, " "))
		if value == "" {
			if err := editor.Omit(role); err != nil {
				return fmt.Errorf("omit empty THR %s: %w", role, err)
			}
		} else if err := editor.Set(role, value); err != nil {
			return fmt.Errorf("normalize THR %s: %w", role, err)
		}
	}
	return nil
}
