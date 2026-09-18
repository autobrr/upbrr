// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

var bhdAudioChannelPattern = regexp.MustCompile(`^(.+?)(\d+(?:\.\d+){1,2})$`)

// applyBHDNameDefaults projects BHD's title, media, and group conventions onto
// a generated document. It never interprets an already-rendered release name.
func applyBHDNameDefaults(editor *trackers.NameEditor, meta api.UploadSubject, _ config.TrackerConfig) error {
	if isBHDTV(meta) {
		if err := applyBHDTVTitleDefaults(editor, meta); err != nil {
			return err
		}
	} else if err := applyBHDMovieTitleDefaults(editor, meta); err != nil {
		return err
	}
	if component, exists := editor.Component(api.NameRoleAudio); exists && component.Present {
		if audio := parseBHDAudioName(component.Value).formatted(); audio != "" {
			if err := editor.Set(api.NameRoleAudio, audio); err != nil {
				return bhdNameEditorError("format audio", err)
			}
		}
	}
	if isBHDFullDisc(meta) && IsDVDSource(meta.Source) {
		if codec := strings.TrimSpace(meta.VideoCodec); codec != "" {
			if component, exists := editor.Component(api.NameRoleVideoCodec); exists && component.Present {
				if err := editor.MoveBefore(api.NameRoleVideoCodec, api.NameRoleAudio); err != nil {
					return bhdNameEditorError("order DVD video codec", err)
				}
			} else if err := editor.InsertBefore(api.NameRoleVideoCodec, codec, api.NameRoleAudio); err != nil {
				return bhdNameEditorError("insert DVD video codec", err)
			}
		}
	}
	if isBHDSDRHEVC(meta) {
		if err := editor.Set(api.NameRoleHDR, "SDR"); err != nil {
			return bhdNameEditorError("set SDR marker", err)
		}
		if err := editor.Include(api.NameRoleHDR); err != nil {
			return bhdNameEditorError("include SDR marker", err)
		}
		videoRole := api.NameRoleVideoCodec
		if component, exists := editor.Component(api.NameRoleVideoEncode); exists && component.Present {
			videoRole = api.NameRoleVideoEncode
		}
		if err := editor.MoveBefore(api.NameRoleHDR, videoRole); err != nil {
			return bhdNameEditorError("order SDR marker", err)
		}
	}
	return applyBHDGroupDefaults(editor, meta)
}

func applyBHDMovieTitleDefaults(editor *trackers.NameEditor, meta api.UploadSubject) error {
	title, alternate, year := bhdMovieTitles(meta)
	if title != "" {
		if err := editor.Set(api.NameRoleTitle, title); err != nil {
			return bhdNameEditorError("set movie title", err)
		}
	}
	if alternate == "" {
		if err := editor.Omit(api.NameRoleAlternateTitle); err != nil {
			return bhdNameEditorError("omit movie alternate title", err)
		}
	} else if err := editor.Set(api.NameRoleAlternateTitle, "AKA "+alternate); err != nil {
		return bhdNameEditorError("set movie alternate title", err)
	}
	if year <= 0 {
		return nil
	}
	if err := editor.Set(api.NameRoleYear, strconv.Itoa(year)); err != nil {
		return bhdNameEditorError("set movie year", err)
	}
	if err := editor.Include(api.NameRoleYear); err != nil {
		return bhdNameEditorError("include movie year", err)
	}
	return nil
}

func applyBHDTVTitleDefaults(editor *trackers.NameEditor, meta api.UploadSubject) error {
	if !meta.ProviderMetadata.IsCurrentFor(meta.SourcePath, meta.Identity) || meta.ProviderMetadata.TVDB == nil {
		return nil
	}
	tvdb := meta.ProviderMetadata.TVDB
	title := trackers.PreferredTitle(meta, firstBHDTitle(tvdb.NameEnglish, tvdb.NameDisambiguation.CanonicalName))
	if title != "" {
		if err := editor.Set(api.NameRoleTitle, title); err != nil {
			return bhdNameEditorError("set TV title", err)
		}
	}
	if alternate := bhdAlternateTitle(meta); alternate == "" {
		if err := editor.Omit(api.NameRoleAlternateTitle); err != nil {
			return bhdNameEditorError("omit TV alternate title", err)
		}
	} else if err := editor.Set(api.NameRoleAlternateTitle, "AKA "+alternate); err != nil {
		return bhdNameEditorError("set TV alternate title", err)
	}
	evidence := tvdb.NameDisambiguation
	if meta.EffectiveMetadata.YearProvenance.IsManual() {
		evidence.SeriesYear = meta.EffectiveMetadata.Year
	}
	if !evidence.IncludeYear || evidence.SeriesYear <= 0 {
		return nil
	}
	if err := editor.Set(api.NameRoleYear, strconv.Itoa(evidence.SeriesYear)); err != nil {
		return bhdNameEditorError("set TV year", err)
	}
	if err := editor.Include(api.NameRoleYear); err != nil {
		return bhdNameEditorError("include TV year", err)
	}
	return nil
}

func firstBHDTitle(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// bhdMovieTitles preserves finalized manual title facts while preferring the
// provider title selected by BHD.
func bhdMovieTitles(meta api.UploadSubject) (string, string, int) {
	providerTitle := ""
	providerYear := 0
	if meta.ProviderMetadata.IsCurrentFor(meta.SourcePath, meta.Identity) {
		providerTitle = firstBHDTitle(
			bhdTMDBTitle(meta.ProviderMetadata.TMDB),
			bhdIMDBTitle(meta.ProviderMetadata.IMDB),
		)
		if meta.ProviderMetadata.TMDB != nil {
			providerYear = meta.ProviderMetadata.TMDB.Year
		}
		if meta.ProviderMetadata.IMDB != nil && meta.ProviderMetadata.IMDB.Year > 0 {
			providerYear = meta.ProviderMetadata.IMDB.Year
		}
	}
	return trackers.PreferredTitle(meta, providerTitle), bhdAlternateTitle(meta), trackers.PreferredYear(meta, providerYear)
}

func bhdAlternateTitle(meta api.UploadSubject) string {
	if meta.NamePresentation.Version == api.ReleaseNamePresentationVersionV1 && meta.NamePresentation.OmitAlternateTitle {
		return ""
	}
	return trimBHDAKAPrefix(trackers.PreferredAlternateTitle(meta, meta.AlternateTitle))
}

func trimBHDAKAPrefix(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > len("AKA ") && strings.EqualFold(value[:len("AKA ")], "AKA ") {
		return strings.TrimSpace(value[len("AKA "):])
	}
	return value
}

func isBHDTV(meta api.UploadSubject) bool {
	return strings.EqualFold(strings.TrimSpace(string(meta.Identity.Category)), string(api.CanonicalCategoryTV)) ||
		strings.EqualFold(strings.TrimSpace(meta.Release.Category), "TV")
}

type bhdAudioName struct{ codec, channels, object string }

func parseBHDAudioName(value string) bhdAudioName {
	fields := strings.Fields(strings.ReplaceAll(strings.TrimSpace(value), "DD+", "DDP"))
	result := bhdAudioName{}
	codec := make([]string, 0, len(fields))
	for _, field := range fields {
		switch {
		case strings.EqualFold(field, "Atmos"):
			result.object = "Atmos"
		case isBHDAudioChannel(field):
			result.channels = field
		default:
			matches := bhdAudioChannelPattern.FindStringSubmatch(field)
			if len(matches) == 3 && isBHDAudioChannel(matches[2]) {
				codec = append(codec, matches[1])
				result.channels = matches[2]
			} else {
				codec = append(codec, field)
			}
		}
	}
	result.codec = strings.Join(codec, " ")
	return result
}

func (a bhdAudioName) formatted() string {
	if strings.EqualFold(a.codec, "DD") && a.channels != "" {
		return strings.TrimSpace(a.codec + a.channels + " " + a.object)
	}
	return strings.Join(strings.Fields(strings.Join([]string{a.codec, a.object, a.channels}, " ")), " ")
}

func isBHDAudioChannel(value string) bool {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) < 2 || len(parts) > 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, char := range part {
			if char < '0' || char > '9' {
				return false
			}
		}
	}
	return true
}

func isBHDSDRHEVC(meta api.UploadSubject) bool {
	if !strings.EqualFold(strings.TrimSpace(meta.VideoCodec), "HEVC") ||
		(!isBHDFullDisc(meta) && !strings.EqualFold(strings.TrimSpace(meta.Type), "REMUX")) {
		return false
	}
	return meta.HDRFacts.Status == api.HDREvidenceComplete && len(meta.HDRFacts.Formats) == 1 && meta.HDRFacts.Formats[0] == api.HDRFormatSDR
}

func applyBHDGroupDefaults(editor *trackers.NameEditor, meta api.UploadSubject) error {
	group := bhdReleaseGroup(meta)
	if group == "" && isBHDFullDisc(meta) {
		if err := editor.Omit(api.NameRoleGroup); err != nil {
			return bhdNameEditorError("omit full-disc group", err)
		}
		return nil
	}
	if group == "" {
		group = "NOGROUP"
	}
	if err := editor.Set(api.NameRoleGroup, "-"+group); err != nil {
		return bhdNameEditorError("set group", err)
	}
	if err := editor.Include(api.NameRoleGroup); err != nil {
		return bhdNameEditorError("include group", err)
	}
	return nil
}

func bhdTMDBTitle(metadata *api.TMDBMetadata) string {
	if metadata == nil {
		return ""
	}
	return metadata.Title
}

func bhdIMDBTitle(metadata *api.IMDBMetadata) string {
	if metadata == nil {
		return ""
	}
	return metadata.Title
}

func bhdNameEditorError(action string, err error) error {
	return fmt.Errorf("BHD name policy %s: %w", action, err)
}

func bhdReleaseGroup(meta api.UploadSubject) string {
	for _, candidate := range []string{meta.Tag, meta.Release.Group, meta.ArrReleaseGroup} {
		group := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(candidate), "-"))
		switch strings.ToLower(group) {
		case "", "nogrp", "nogroup", "notag", "unknown", "unk":
			continue
		default:
			return group
		}
	}
	return ""
}

func isBHDFullDisc(meta api.UploadSubject) bool {
	if strings.EqualFold(strings.TrimSpace(meta.Type), "DISC") {
		return true
	}
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "BDMV", "DVD", "HDDVD", "HD-DVD":
		return true
	default:
		return false
	}
}
