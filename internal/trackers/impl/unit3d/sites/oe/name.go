// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package oe

import (
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func namePolicy() trackers.ReleaseNamePolicyBinding {
	return trackers.StructuredReleaseNamePolicy("unit3d/oe/v2", trackers.StructuredNamePolicy{
		Defaults: applyOENameDefaults,
	})
}

func applyOENameDefaults(editor *trackers.NameEditor, meta api.UploadSubject, _ config.TrackerConfig) error {
	tag := strings.TrimSpace(strings.TrimPrefix(meta.Tag, "-"))
	if tag != "" && !unit3d.IsNoGroupTag(tag) {
		return nil
	}
	if err := editor.Set(api.NameRoleGroup, "-NOGRP"); err != nil {
		return fmt.Errorf("set OE no-group marker: %w", err)
	}
	if err := editor.Include(api.NameRoleGroup); err != nil {
		return fmt.Errorf("include OE no-group marker: %w", err)
	}
	return nil
}
