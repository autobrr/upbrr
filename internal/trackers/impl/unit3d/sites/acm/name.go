// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package acm

import (
	"fmt"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func namePolicy() trackers.ReleaseNamePolicyBinding {
	return trackers.StructuredReleaseNamePolicy("unit3d/acm/v4", trackers.StructuredNamePolicy{Defaults: applyACMNameDefaults})
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
	for _, language := range meta.SubtitleLanguages {
		code, ok := acmSubtitleCodes[strings.ToLower(strings.TrimSpace(language))]
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
	"arabic":                "Ara",
	"ara":                   "Ara",
	"ar":                    "Ara",
	"brazilian portuguese":  "Por-BR",
	"brazilian":             "Por-BR",
	"portuguese-br":         "Por-BR",
	"pt-br":                 "Por-BR",
	"bulgarian":             "Bul",
	"bul":                   "Bul",
	"bg":                    "Bul",
	"chinese":               "Chi",
	"chi":                   "Chi",
	"zh":                    "Chi",
	"chinese (simplified)":  "Chi",
	"chinese (traditional)": "Chi",
	"croatian":              "Cro",
	"hrv":                   "Cro",
	"hr":                    "Cro",
	"scr":                   "Cro",
	"czech":                 "Cze",
	"cze":                   "Cze",
	"cz":                    "Cze",
	"cs":                    "Cze",
	"danish":                "Dan",
	"dan":                   "Dan",
	"da":                    "Dan",
	"dutch":                 "Dut",
	"dut":                   "Dut",
	"nl":                    "Dut",
	"english":               "Eng",
	"eng":                   "Eng",
	"en":                    "Eng",
	"english (cc)":          "Eng",
	"english - sdh":         "Eng",
	"english - forced":      "Eng",
	"english (forced)":      "Eng",
	"en (forced)":           "Eng",
	"english intertitles":   "Eng",
	"english (intertitles)": "Eng",
	"english - intertitles": "Eng",
	"en (intertitles)":      "Eng",
	"estonian":              "Est",
	"est":                   "Est",
	"et":                    "Est",
	"finnish":               "Fin",
	"fin":                   "Fin",
	"fi":                    "Fin",
	"french":                "Fre",
	"fre":                   "Fre",
	"fr":                    "Fre",
	"german":                "Ger",
	"ger":                   "Ger",
	"de":                    "Ger",
	"greek":                 "Gre",
	"gre":                   "Gre",
	"el":                    "Gre",
	"hebrew":                "Heb",
	"heb":                   "Heb",
	"he":                    "Heb",
	"hindi":                 "Hin",
	"hin":                   "Hin",
	"hi":                    "Hin",
	"hungarian":             "Hun",
	"hun":                   "Hun",
	"hu":                    "Hun",
	"icelandic":             "Ice",
	"ice":                   "Ice",
	"is":                    "Ice",
	"indonesian":            "Ind",
	"ind":                   "Ind",
	"id":                    "Ind",
	"italian":               "Ita",
	"ita":                   "Ita",
	"it":                    "Ita",
	"japanese":              "Jpn",
	"jpn":                   "Jpn",
	"ja":                    "Jpn",
	"korean":                "Kor",
	"kor":                   "Kor",
	"ko":                    "Kor",
	"latvian":               "Lav",
	"lav":                   "Lav",
	"lv":                    "Lav",
	"lithuanian":            "Lit",
	"lit":                   "Lit",
	"lt":                    "Lit",
	"norwegian":             "Nor",
	"nor":                   "Nor",
	"no":                    "Nor",
	"persian":               "Per",
	"fa":                    "Per",
	"far":                   "Per",
	"polish":                "Pol",
	"pol":                   "Pol",
	"pl":                    "Pol",
	"portuguese":            "Por",
	"por":                   "Por",
	"pt":                    "Por",
	"romanian":              "Rom",
	"rum":                   "Rom",
	"ro":                    "Rom",
	"russian":               "Rus",
	"rus":                   "Rus",
	"ru":                    "Rus",
	"serbian":               "Ser",
	"srp":                   "Ser",
	"sr":                    "Ser",
	"scc":                   "Ser",
	"slovak":                "Slo",
	"slo":                   "Slo",
	"sk":                    "Slo",
	"slovenian":             "Slv",
	"slv":                   "Slv",
	"sl":                    "Slv",
	"spanish":               "Spa",
	"spa":                   "Spa",
	"es":                    "Spa",
	"swedish":               "Swe",
	"swe":                   "Swe",
	"sv":                    "Swe",
	"thai":                  "Tha",
	"tha":                   "Tha",
	"th":                    "Tha",
	"turkish":               "Tur",
	"tur":                   "Tur",
	"tr":                    "Tur",
	"ukrainian":             "Ukr",
	"ukr":                   "Ukr",
	"uk":                    "Ukr",
	"vietnamese":            "Vie",
	"vie":                   "Vie",
	"vi":                    "Vie",
}
