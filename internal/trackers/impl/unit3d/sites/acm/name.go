// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package acm

import (
	"fmt"
	"slices"
	"strings"

	"golang.org/x/text/language"

	"github.com/autobrr/upbrr/internal/languageutil"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func namePolicy() trackers.ReleaseNamePolicyBinding {
	return trackers.StructuredReleaseNamePolicy("unit3d/acm/v5", trackers.StructuredNamePolicy{Defaults: applyACMNameDefaults})
}

func applyACMNameDefaults(editor *trackers.NameEditor, meta api.UploadSubject, _ config.TrackerConfig) error {
	if err := applyACMAlternateTitle(editor, meta); err != nil {
		return err
	}
	if err := normalizeACMTechnicalComponents(editor, meta); err != nil {
		return err
	}
	if unit3d.IsDiscType(meta.DiscType) {
		return nil
	}
	if subtitle := acmSubtitleTag(acmSubtitleCodesFor(meta)); subtitle != "" {
		anchor := firstACMPresentRole(editor.PresentRoles(), api.NameRoleGroup, api.NameRoleVideoEncode, api.NameRoleVideoCodec,
			api.NameRoleAudio, api.NameRoleVideoFormat, api.NameRoleSource)
		if anchor.Valid() {
			if err := editor.InsertAfter(api.NameRoleSubtitleMarker, subtitle, anchor); err != nil {
				return fmt.Errorf("insert ACM subtitle marker: %w", err)
			}
		}
	}
	return nil
}

func applyACMAlternateTitle(editor *trackers.NameEditor, meta api.UploadSubject) error {
	if meta.NamePresentation.Version == api.ReleaseNamePresentationVersionV1 && meta.NamePresentation.OmitAlternateTitle {
		return nil
	}
	title, ok := editor.Component(api.NameRoleTitle)
	if !ok || strings.TrimSpace(title.Value) == "" {
		return nil
	}
	original := acmOriginalTitle(meta)
	if original == "" || strings.EqualFold(strings.TrimSpace(title.Value), original) {
		return nil
	}
	if err := editor.Set(api.NameRoleAlternateTitle, "/ "+original+" \u202A"); err != nil {
		return fmt.Errorf("set ACM alternate title: %w", err)
	}
	if err := editor.Include(api.NameRoleAlternateTitle); err != nil {
		return fmt.Errorf("include ACM alternate title: %w", err)
	}
	if err := editor.MoveBefore(api.NameRoleAlternateTitle, api.NameRoleYear); err != nil {
		return fmt.Errorf("move ACM alternate title: %w", err)
	}
	return nil
}

func acmOriginalTitle(meta api.UploadSubject) string {
	if meta.EffectiveMetadata.OriginalTitleProvenance.IsManual() {
		return strings.TrimPrefix(strings.TrimSpace(trackers.PreferredOriginalTitle(meta, "")), "AKA ")
	}
	if meta.ProviderMetadata.IsCurrentFor(meta.SourcePath, meta.Identity) {
		if tmdb := meta.ProviderMetadata.TMDB; tmdb != nil {
			for _, value := range []string{tmdb.OriginalTitle, tmdb.RetrievedAKA} {
				if value = strings.TrimSpace(value); value != "" {
					return strings.TrimPrefix(value, "AKA ")
				}
			}
		}
		if imdb := meta.ProviderMetadata.IMDB; imdb != nil {
			if value := strings.TrimSpace(imdb.AKA); value != "" {
				return strings.TrimPrefix(value, "AKA ")
			}
		}
	}
	return strings.TrimPrefix(strings.TrimSpace(meta.AlternateTitle), "AKA ")
}

func normalizeACMTechnicalComponents(editor *trackers.NameEditor, meta api.UploadSubject) error {
	if audio, ok := editor.Component(api.NameRoleAudio); ok {
		normalized := strings.Join(strings.Fields(audio.Value), " ")
		normalized = strings.ReplaceAll(normalized, "AAC ", "AAC")
		normalized = strings.ReplaceAll(normalized, "DD+ ", "DD+")
		normalized = strings.ReplaceAll(normalized, " Atmos", "")
		if err := editor.Set(api.NameRoleAudio, normalized); err != nil {
			return fmt.Errorf("set ACM audio: %w", err)
		}
	}
	for _, role := range []api.ReleaseNameRole{api.NameRoleVideoCodec, api.NameRoleVideoEncode} {
		if component, ok := editor.Component(role); ok {
			values := strings.Fields(component.Value)
			changed := false
			for index, value := range values {
				if strings.EqualFold(value, "H.265") {
					values[index] = "HEVC"
					changed = true
				}
			}
			if !changed {
				continue
			}
			if err := editor.Set(role, strings.Join(values, " ")); err != nil {
				return fmt.Errorf("set ACM %s: %w", role, err)
			}
		}
	}
	if strings.EqualFold(strings.TrimSpace(meta.Type), "REMUX") && strings.EqualFold(strings.TrimSpace(meta.Source), "BluRay") {
		if err := editor.Omit(api.NameRoleUHD); err != nil {
			return fmt.Errorf("omit ACM remux UHD marker: %w", err)
		}
		if err := editor.Omit(api.NameRoleSource); err != nil {
			return fmt.Errorf("omit ACM remux source: %w", err)
		}
		if err := editor.Set(api.NameRoleVideoFormat, "Remux"); err != nil {
			return fmt.Errorf("set ACM remux format: %w", err)
		}
	}
	if !strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		return nil
	}
	if strings.TrimSpace(unit3d.Resolution(meta)) != "" {
		if err := editor.Include(api.NameRoleResolution); err != nil {
			return fmt.Errorf("include ACM DVD resolution: %w", err)
		}
		if err := editor.Set(api.NameRoleDVDSize, "DVD"); err != nil {
			return fmt.Errorf("set ACM DVD size: %w", err)
		}
		if err := editor.Set(api.NameRoleSource, strings.TrimSpace(meta.Source)); err != nil {
			return fmt.Errorf("set ACM DVD source: %w", err)
		}
		if err := editor.Include(api.NameRoleSource); err != nil {
			return fmt.Errorf("include ACM DVD source: %w", err)
		}
		if err := editor.Omit(api.NameRoleDVDSystem); err != nil {
			return fmt.Errorf("omit ACM DVD system: %w", err)
		}
		if err := editor.MoveBefore(api.NameRoleResolution, api.NameRoleDVDSize); err != nil {
			return fmt.Errorf("move ACM DVD resolution: %w", err)
		}
		if err := editor.MoveAfter(api.NameRoleSource, api.NameRoleDVDSize); err != nil {
			return fmt.Errorf("move ACM DVD source: %w", err)
		}
	}
	if audio, ok := editor.Component(api.NameRoleAudio); ok &&
		strings.TrimSpace(audio.Value) == strings.TrimSpace(meta.Channels) && strings.TrimSpace(audio.Value) != "" {
		if err := editor.Set(api.NameRoleAudio, "MPEG "+strings.TrimSpace(audio.Value)); err != nil {
			return fmt.Errorf("set ACM DVD MPEG audio: %w", err)
		}
	}
	return nil
}

func firstACMPresentRole(present []api.ReleaseNameRole, candidates ...api.ReleaseNameRole) api.ReleaseNameRole {
	for _, candidate := range candidates {
		if slices.Contains(present, candidate) {
			return candidate
		}
	}
	if len(present) > 0 {
		return present[len(present)-1]
	}
	return ""
}

func acmSubtitleCodesFor(meta api.UploadSubject) []string {
	out := make([]string, 0, len(meta.SubtitleLanguages))
	seen := make(map[string]struct{}, len(meta.SubtitleLanguages))
	for _, value := range meta.SubtitleLanguages {
		key := strings.ToLower(strings.TrimSpace(value))
		if tag, ok := languageutil.LanguageTag(value); ok {
			tag, _ = language.All.Canonicalize(tag)
			base, _, region := tag.Raw()
			key = base.String()
			if key == "pt" && region.String() == "BR" {
				key = "pt-br"
			} else if key == "nb" || key == "nn" {
				key = "no"
			}
		}
		code, ok := acmSubtitleCodes[key]
		if !ok {
			continue
		}
		if _, exists := seen[code]; exists {
			continue
		}
		seen[code] = struct{}{}
		out = append(out, code)
	}
	return out
}

func acmSubtitleTag(subtitles []string) string {
	if len(subtitles) == 0 {
		return "[No subs]"
	}
	if slices.Contains(subtitles, "Eng") {
		return ""
	}
	if len(subtitles) > 1 {
		return "[No Eng subs]"
	}
	return "[" + subtitles[0] + " subs only]"
}

var acmSubtitleCodes = map[string]string{
	"ar":                    "Ara",
	"brazilian":             "Por-BR",
	"portuguese-br":         "Por-BR",
	"pt-br":                 "Por-BR",
	"bg":                    "Bul",
	"zh":                    "Chi",
	"hr":                    "Cro",
	"scr":                   "Cro",
	"cz":                    "Cze",
	"cs":                    "Cze",
	"da":                    "Dan",
	"nl":                    "Dut",
	"en":                    "Eng",
	"english (cc)":          "Eng",
	"english - sdh":         "Eng",
	"english intertitles":   "Eng",
	"english (intertitles)": "Eng",
	"english - intertitles": "Eng",
	"en (intertitles)":      "Eng",
	"et":                    "Est",
	"fi":                    "Fin",
	"fr":                    "Fre",
	"de":                    "Ger",
	"el":                    "Gre",
	"he":                    "Heb",
	"hi":                    "Hin",
	"hu":                    "Hun",
	"is":                    "Ice",
	"id":                    "Ind",
	"it":                    "Ita",
	"ja":                    "Jpn",
	"ko":                    "Kor",
	"lv":                    "Lav",
	"lt":                    "Lit",
	"no":                    "Nor",
	"fa":                    "Per",
	"far":                   "Per",
	"pl":                    "Pol",
	"pt":                    "Por",
	"ro":                    "Rom",
	"ru":                    "Rus",
	"sr":                    "Ser",
	"scc":                   "Ser",
	"sk":                    "Slo",
	"sl":                    "Slv",
	"es":                    "Spa",
	"sv":                    "Swe",
	"th":                    "Tha",
	"tr":                    "Tur",
	"uk":                    "Ukr",
	"vi":                    "Vie",
}
