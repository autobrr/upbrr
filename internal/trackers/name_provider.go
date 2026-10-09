// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/pkg/api"
)

func applyTitleProvider(editor *NameEditor, subject api.UploadSubject, provider api.IdentityProvider) error {
	if provider == "" {
		return nil
	}
	component, ok := editor.Component(api.NameRoleTitle)
	if ok && component.Manual {
		return nil
	}
	if subject.EffectiveMetadata.TitleProvenance.IsManual() {
		return editor.Set(api.NameRoleTitle, subject.EffectiveMetadata.Title)
	}
	title := ""
	if currentNameProviderMetadata(subject) {
		switch provider {
		case api.IdentityProviderTMDB:
			if data := subject.ProviderMetadata.TMDB; data != nil && subject.Identity.TMDBID > 0 && data.TMDBID == subject.Identity.TMDBID {
				if category, err := api.NormalizeCanonicalCategory(data.Category); err == nil && category == subject.Identity.Category {
					title = strings.TrimSpace(data.Title)
				}
			}
		case api.IdentityProviderIMDB:
			if data := subject.ProviderMetadata.IMDB; data != nil && subject.Identity.IMDBID > 0 && data.IMDBID == subject.Identity.IMDBID {
				title = strings.TrimSpace(data.Title)
			}
		case "", api.IdentityProviderTVDB, api.IdentityProviderTVmaze, api.IdentityProviderMAL:
		}
	}
	if title == "" || !ok || !component.Present {
		return &NameRuleError{
			Rule:   editor.rule,
			Role:   api.NameRoleTitle,
			Reason: fmt.Sprintf("current matching %s title and generated title component are required; refresh metadata and reprepare the release", provider),
		}
	}
	return editor.Set(api.NameRoleTitle, title)
}

// normalizeProviderName keeps canonical title formatting after provider and
// qualifier selection. It changes neither manual titles nor manual alternates.
func normalizeProviderName(editor *NameEditor, subject api.UploadSubject, provider api.IdentityProvider) error {
	title, ok := editor.Component(api.NameRoleTitle)
	if provider == "" || !ok || !title.Present || title.Manual || subject.EffectiveMetadata.TitleProvenance.IsManual() {
		return nil
	}
	year := 0
	if subject.Identity.Category == api.CanonicalCategoryTV {
		component, _ := editor.Component(api.NameRoleYear)
		year, _ = strconv.Atoi(firstProjectionValue(component.Value, component.AvailableValue))
		title.Value = metautil.TrimTrailingParentheticalYear(title.Value, year)
		if err := editor.Set(api.NameRoleTitle, title.Value); err != nil {
			return err
		}
	}
	alternate, ok := editor.Component(api.NameRoleAlternateTitle)
	if !ok || !alternate.Present || alternate.Manual || subject.EffectiveMetadata.AlternateTitleProvenance.IsManual() {
		return nil
	}
	words := strings.Fields(alternate.Value)
	if len(words) > 0 && strings.EqualFold(words[0], "AKA") {
		words = words[1:]
	}
	alternateTitle := metautil.TrimTrailingParentheticalYear(strings.Join(words, " "), year)
	if strings.EqualFold(alternateTitle, strings.Join(strings.Fields(title.Value), " ")) {
		return editor.Omit(api.NameRoleAlternateTitle)
	}
	return nil
}

// applyLegacyTitleProvider projects generated components and finalized title
// fields onto a subject copy before a legacy resolver runs. Opaque names remain
// untouched; no string matching or parser reconstruction supplies title identity.
func applyLegacyTitleProvider(subject api.UploadSubject, requested *string, binding ReleaseNamePolicyBinding) (api.UploadSubject, error) {
	if binding.TitleProvider == "" || requested != nil || subject.GeneratedName == nil ||
		strings.TrimSpace(subject.ReleaseName) != subject.GeneratedName.Render().Name {
		return subject, nil
	}
	if err := subject.GeneratedName.Validate(); err != nil {
		return api.UploadSubject{}, fmt.Errorf("generated name: %w", err)
	}
	editor := &NameEditor{document: subject.GeneratedName.Clone(), rule: binding.ID}
	if err := applyTitleProvider(editor, subject, binding.TitleProvider); err != nil {
		return api.UploadSubject{}, err
	}
	if err := normalizeProviderName(editor, subject, binding.TitleProvider); err != nil {
		return api.UploadSubject{}, err
	}
	subject.GeneratedName = editor.document
	title, _ := editor.Component(api.NameRoleTitle)
	subject.Release.Title = title.Value
	subject.EffectiveMetadata.Title = title.Value
	rendered := editor.document.Render()
	subject.ReleaseName = rendered.Name
	subject.ReleaseNameNoTag = rendered.NameNoTag
	subject.ReleaseNameClean = rendered.CleanName
	// Rebuild structural variants from the same copy so an episode-title
	// omission cannot select the old provider's rendered title.
	subject.GeneratedReleaseNames.IncludeEpisodeTitle = rendered
	omitted := &NameEditor{document: editor.document.Clone(), rule: binding.ID}
	if err := omitted.Omit(api.NameRoleEpisodeTitle); err != nil {
		return api.UploadSubject{}, err
	}
	subject.GeneratedReleaseNames.OmitEpisodeTitle = omitted.document.Render()
	return subject, nil
}
