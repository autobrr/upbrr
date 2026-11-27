// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"
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
	if err := editor.Set(api.NameRoleTitle, title); err != nil {
		return err
	}
	if subject.NamePresentation.OmitAlternateTitle || subject.EffectiveMetadata.AlternateTitleProvenance.IsManual() {
		return nil
	}
	alternate, _ := editor.Component(api.NameRoleAlternateTitle)
	year := providerNameTVYear(editor, subject)
	if alternate.Manual || (alternate.Present && distinctAutomaticTitle(title, metautil.TrimAKAPrefix(alternate.Value), year)) {
		return nil
	}
	// Recover a former primary only when a current provider corroborates it as an original.
	for _, original := range currentProviderAlternateTitles(subject, "") {
		if !sameAutomaticTitle(component.Value, original, year) || !distinctAutomaticTitle(title, original, year) {
			continue
		}
		value := metautil.FormatOriginalAKA(metautil.TrimTrailingParentheticalYear(original, year))
		if alternate.Present {
			return editor.Set(api.NameRoleAlternateTitle, value)
		}
		return editor.InsertAfter(api.NameRoleAlternateTitle, value, api.NameRoleTitle)
	}
	return nil
}

// DistinctProviderAlternateTitle returns a bound original or anime romanization that differs from the primary.
func DistinctProviderAlternateTitle(subject api.UploadSubject, primary string, year int) string {
	romaji := metautil.AnimeRomanizedAlternateTitle(subject.Identity, subject.ProviderMetadata, primary)
	for _, original := range currentProviderAlternateTitles(subject, romaji) {
		if distinctAutomaticTitle(primary, original, year) {
			return metautil.TrimTrailingParentheticalYear(original, year)
		}
	}
	return ""
}

func currentProviderAlternateTitles(subject api.UploadSubject, romaji string) []string {
	if !currentNameProviderMetadata(subject) {
		return nil
	}
	titles := make([]string, 0, 4)
	metadata := subject.ProviderMetadata
	if imdb := metadata.IMDB; imdb != nil && subject.Identity.IMDBID > 0 && imdb.IMDBID == subject.Identity.IMDBID {
		titles = append(titles, imdb.AKA)
	}
	if romaji != "" {
		titles = append(titles, romaji)
	}
	if tmdb := metadata.TMDB; tmdb != nil && subject.Identity.TMDBID > 0 && tmdb.TMDBID == subject.Identity.TMDBID {
		if category, err := api.NormalizeCanonicalCategory(tmdb.Category); err == nil && category == subject.Identity.Category {
			titles = append(titles, tmdb.OriginalTitle)
		}
	}
	if subject.Identity.Category == api.CanonicalCategoryTV && metadata.TVDBNamingAvailable(subject.SourcePath, subject.Identity) {
		tvdb := metadata.TVDB
		if tvdb.OriginalLanguage != "" && !languageutil.SameLanguage(tvdb.OriginalLanguage, "en") {
			titles = append(titles, tvdb.Name)
		}
	}
	return titles
}

func distinctAutomaticTitle(primary, alternate string, year int) bool {
	return metautil.DistinctTitle(metautil.TrimTrailingParentheticalYear(primary, year), metautil.TrimTrailingParentheticalYear(alternate, year))
}

func sameAutomaticTitle(primary, alternate string, year int) bool {
	primaryKey := metautil.TitleIdentityKey(metautil.TrimTrailingParentheticalYear(primary, year))
	return primaryKey != "" && primaryKey == metautil.TitleIdentityKey(metautil.TrimTrailingParentheticalYear(alternate, year))
}

func providerNameTVYear(editor *NameEditor, subject api.UploadSubject) int {
	if subject.Identity.Category != api.CanonicalCategoryTV {
		return 0
	}
	component, _ := editor.Component(api.NameRoleYear)
	year, _ := strconv.Atoi(firstProjectionValue(component.Value, component.AvailableValue))
	return year
}

// normalizeProviderName keeps canonical title formatting after provider and
// qualifier selection. It changes neither manual titles nor manual alternates.
func normalizeProviderName(editor *NameEditor, subject api.UploadSubject, provider api.IdentityProvider) error {
	title, ok := editor.Component(api.NameRoleTitle)
	if provider == "" || !ok || !title.Present || title.Manual || subject.EffectiveMetadata.TitleProvenance.IsManual() {
		return nil
	}
	year := providerNameTVYear(editor, subject)
	if subject.Identity.Category == api.CanonicalCategoryTV {
		title.Value = metautil.TrimTrailingParentheticalYear(title.Value, year)
		if err := editor.Set(api.NameRoleTitle, title.Value); err != nil {
			return err
		}
	}
	alternate, ok := editor.Component(api.NameRoleAlternateTitle)
	if !ok || !alternate.Present || alternate.Manual || subject.EffectiveMetadata.AlternateTitleProvenance.IsManual() {
		return nil
	}
	if !distinctAutomaticTitle(title.Value, metautil.TrimAKAPrefix(alternate.Value), year) {
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
	restoreGeneratedEpisodeTitle(subject.GeneratedName, editor.document, subject)
	editor.OmitRepeatedEpisodeTitle()
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
