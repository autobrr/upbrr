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

// ApplyDefaultAudioName uses the unique inspected default audio track.
// Automatic naming remains unresolved without that evidence; manual components
// and full-disc names retain their existing presentation authority.
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
	var selected *api.MediaTrackFacts
	for i := range meta.LanguageFacts.Tracks {
		track := &meta.LanguageFacts.Tracks[i]
		if track.Kind != api.MediaTrackAudio || !track.Default {
			continue
		}
		if selected != nil {
			return &NameRuleError{
				Rule:   "default_audio",
				Role:   api.NameRoleAudio,
				Reason: "Unresolved default audio naming: multiple inspected audio tracks are marked default",
			}
		}
		selected = track
	}
	if selected == nil || strings.TrimSpace(selected.Codec) == "" || strings.EqualFold(selected.Codec, "Unknown") || selected.AudioLabel == "" {
		return &NameRuleError{
			Rule:   "default_audio",
			Role:   api.NameRoleAudio,
			Reason: "Unresolved default audio naming: an inspected default audio track and technical label are required",
		}
	}
	if err := editor.Set(api.NameRoleAudio, selected.AudioLabel); err != nil {
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
	languages := make(map[string]struct{}, len(facts.ProgrammeLanguages))
	for _, language := range facts.ProgrammeLanguages {
		code := languageutil.NormalizeLanguageCode(language)
		if code != "" && code != "und" && code != "mul" {
			languages[code] = struct{}{}
		}
	}
	return len(languages)
}
