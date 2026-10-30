// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"

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

// ApplyDefaultAudioName uses agreeing inspected default audio labels when available.
// Missing or ambiguous defaults retain the finalized audio name; tracker-specific
// default-flag requirements belong to validation, independently of language eligibility.
func ApplyDefaultAudioName(editor *NameEditor, meta api.UploadSubject) error {
	if IsFullDiscUpload(meta.DiscType, meta.Type) {
		return nil
	}
	if audio, ok := editor.Component(api.NameRoleAudio); ok && audio.Manual {
		return nil
	}
	if meta.LanguageFacts.AudioAbsent {
		if err := editor.Omit(api.NameRoleAudio); err != nil {
			return fmt.Errorf("omit absent audio: %w", err)
		}
		return nil
	}
	selectedLabel := ""
	for i := range meta.LanguageFacts.Tracks {
		track := &meta.LanguageFacts.Tracks[i]
		if track.Kind != api.MediaTrackAudio {
			continue
		}
		if !track.DefaultKnown {
			return nil
		}
		if !track.Default {
			continue
		}
		if strings.TrimSpace(track.Codec) == "" || strings.EqualFold(track.Codec, "Unknown") || track.AudioLabel == "" {
			return nil
		}
		if selectedLabel != "" && selectedLabel != track.AudioLabel {
			return nil
		}
		selectedLabel = track.AudioLabel
	}
	if selectedLabel == "" {
		return nil
	}
	if err := editor.Set(api.NameRoleAudio, selectedLabel); err != nil {
		return fmt.Errorf("set default audio label: %w", err)
	}
	return nil
}

// KnownProgrammeLanguageCount counts established distinct programme languages,
// without turning unknown language labels into additional audio options.
func KnownProgrammeLanguageCount(facts api.LanguageFacts) int {
	if facts.ProgrammeStatus == api.MetadataEvidenceStatusContradictory {
		return 0
	}
	return languageutil.LanguageIdentityCount(facts.ProgrammeLanguages)
}
