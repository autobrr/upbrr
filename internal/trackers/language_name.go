// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"fmt"

	"github.com/autobrr/upbrr/pkg/api"
)

// ApplyEnglishAudioNameDefaults applies the exclusive original-plus-English
// naming convention only for trackers that explicitly opt into it. Manual
// presentation choices remain owned by NameEditor, independently of factual tags.
func ApplyEnglishAudioNameDefaults(editor *NameEditor, meta api.UploadSubject) error {
	if IsFullDiscUpload(meta.DiscType, meta.Type) {
		return nil
	}
	for _, role := range []api.ReleaseNameRole{api.NameRoleDualAudio, api.NameRoleDubbed} {
		if err := editor.Omit(role); err != nil {
			return fmt.Errorf("omit automatic language marker: %w", err)
		}
	}
	if meta.LanguageFacts.ProgrammeStatus == api.MetadataEvidenceStatusContradictory || !meta.LanguageFacts.HasEnglishDub() {
		return nil
	}
	role, label := api.NameRoleDubbed, "Dubbed"
	if meta.LanguageFacts.HasOriginalAudio() {
		role, label = api.NameRoleDualAudio, "Dual-Audio"
	}

	if _, exists := editor.Component(role); exists {
		if err := editor.Set(role, label); err != nil {
			return fmt.Errorf("set audio language marker: %w", err)
		}
		if err := editor.Include(role); err != nil {
			return fmt.Errorf("include audio language marker: %w", err)
		}
		return nil
	}
	anchor := api.NameRoleAudio
	if component, ok := editor.Component(anchor); !ok || !component.Present {
		anchor = api.NameRoleGroup
	}
	if err := editor.InsertBefore(role, label, anchor); err != nil {
		return fmt.Errorf("insert audio language marker: %w", err)
	}
	return nil
}

// ApplyDefaultAudioName uses an inspected default programme track's technical
// label for sites whose naming contract explicitly follows that track.
func ApplyDefaultAudioName(editor *NameEditor, meta api.UploadSubject) error {
	if IsFullDiscUpload(meta.DiscType, meta.Type) {
		return nil
	}
	var selected *api.MediaTrackFacts
	for i := range meta.LanguageFacts.Tracks {
		track := &meta.LanguageFacts.Tracks[i]
		if track.Kind != api.MediaTrackAudio || !track.Default || (track.Role != api.AudioRoleProgramme && track.Role != api.AudioRoleAlternateMix) {
			continue
		}
		if selected != nil {
			return nil
		}
		selected = track
	}
	if selected == nil || selected.AudioLabel == "" {
		return nil
	}
	if err := editor.Set(api.NameRoleAudio, selected.AudioLabel); err != nil {
		return fmt.Errorf("set default programme audio label: %w", err)
	}
	return nil
}
