// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package spd

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func namePolicy() trackers.ReleaseNamePolicyBinding {
	return trackers.StructuredReleaseNamePolicy("standalone/spd/v2", trackers.StructuredNamePolicy{
		Defaults: normalizeSPDComponents,
		Search: func(meta api.UploadSubject, _ config.TrackerConfig) string {
			if meta.Identity.IMDBID == 0 {
				return strings.TrimSpace(meta.Release.Title)
			}
			return normalizeSPDComponent(metautil.FirstNonEmptyTrimmed(meta.ReleaseName, meta.Release.Title, meta.Filename))
		},
	})
}

func normalizeSPDComponents(editor *trackers.NameEditor, _ api.UploadSubject, _ config.TrackerConfig) error {
	for _, role := range editor.PresentRoles() {
		component, _ := editor.Component(role)
		value := normalizeSPDComponent(component.Value)
		if value == "" {
			if err := editor.Omit(role); err != nil {
				return fmt.Errorf("omit empty SPD %s: %w", role, err)
			}
			continue
		}
		if err := editor.Set(role, value); err != nil {
			return fmt.Errorf("normalize SPD %s: %w", role, err)
		}
	}
	return nil
}

func normalizeSPDComponent(input string) string {
	mapper := func(r rune) rune {
		if r > unicode.MaxASCII || strings.ContainsRune(`\/*?"<>|`, r) {
			return -1
		}
		return r
	}
	return strings.Join(strings.Fields(strings.Map(mapper, strings.ReplaceAll(input, ":", " -"))), " ")
}
