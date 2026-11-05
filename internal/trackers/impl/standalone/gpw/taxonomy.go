// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gpw

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveCodec(meta api.UploadSubject) string {
	codec := strings.ToLower(codecFact(meta))
	codec = strings.TrimPrefix(codec, "hi10p ")
	switch codec {
	case "x265":
		return "x265"
	case "x264":
		return "x264"
	case "hevc", "h265", "h.265":
		return "H.265"
	case "avc", "h264", "h.264":
		return "H.264"
	case "divx":
		return "DivX"
	case "xvid":
		return "XviD"
	default:
		return "Other"
	}
}

// codecFact excludes a bare measured profile, which supplies no encoder identity.
func codecFact(meta api.UploadSubject) string {
	encode := strings.TrimSpace(meta.VideoEncode)
	if strings.EqualFold(encode, "Hi10P") {
		encode = ""
	}
	return metautil.FirstNonEmptyTrimmed(encode, meta.VideoCodec)
}

func resolveContainer(meta api.UploadSubject) string {
	container := strings.ToLower(strings.TrimSpace(meta.Container))
	switch container {
	case "mkv", "mp4", "avi", "mpg", "iso":
		return strings.ToUpper(container)
	case "m2ts":
		return "m2ts"
	case "vob", "vob ifo":
		return "VOB IFO"
	default:
		return "Other"
	}
}

func resolveProcessing(meta api.UploadSubject) string {
	switch strings.ToUpper(strings.TrimSpace(meta.Type)) {
	case "ENCODE":
		return "Encode"
	case "REMUX":
		return "Remux"
	case "DIY":
		return "DIY"
	default:
		return "Untouched"
	}
}

func resolveResolution(meta api.UploadSubject) string {
	resolution := strings.ToLower(strings.TrimSpace(meta.Release.Resolution))
	switch resolution {
	case "480p", "576p", "720p", "1080i", "1080p", "2160p":
		return resolution
	default:
		return "Other"
	}
}

func resolveSource(meta api.UploadSubject) string {
	switch strings.ToUpper(strings.TrimSpace(meta.Type)) {
	case "DISC":
		if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
			return "Blu-ray"
		}
		return "DVD"
	case "WEBDL", "WEBRIP":
		return "WEB"
	case "REMUX", "ENCODE":
		return "Blu-ray"
	case "HDTV":
		return "HDTV"
	default:
		return "Other"
	}
}

func resolveSubtitleType(meta api.UploadSubject) string {
	if meta.HardcodedSubs {
		return "2"
	}
	if len(meta.SubtitleLanguages) > 0 {
		return "1"
	}
	return "3"
}

func resolveSubtitles(meta api.UploadSubject) []string {
	values := meta.SubtitleLanguages
	if meta.HardcodedSubs {
		values = meta.HardcodedSubtitleLanguages
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		key := subtitleKey(value)
		if key != "" && !slices.Contains(out, key) {
			out = append(out, key)
		}
	}
	return out
}

func subtitleKey(value string) string {
	tag, ok := languageutil.LanguageTag(value)
	if !ok {
		return ""
	}
	base, script, region := tag.Raw()
	if base.String() == "zh" || base.String() == "cmn" {
		switch script.String() {
		case "Hans":
			return "chinese_simplified"
		case "Hant":
			return "chinese_traditional"
		}
		switch region.String() {
		case "CN", "SG":
			return "chinese_simplified"
		case "TW", "HK", "MO":
			return "chinese_traditional"
		}
		return ""
	}
	if base.String() == "pt" && region.String() == "BR" {
		return "brazilian_port"
	}
	return map[string]string{
		"en": "english",
		"ja": "japanese",
		"ko": "korean",
		"ar": "arabic",
		"bg": "bulgarian",
		"hr": "croatian",
		"cs": "czech",
		"da": "danish",
		"nl": "dutch",
		"et": "estonian",
		"fi": "finnish",
		"fr": "french",
		"de": "german",
		"el": "greek",
		"he": "hebrew",
		"hi": "hindi",
		"hu": "hungarian",
		"is": "icelandic",
		"id": "indonesian",
		"it": "italian",
		"lv": "latvian",
		"lt": "lithuanian",
		"no": "norwegian",
		"nb": "norwegian",
		"nn": "norwegian",
		"fa": "persian",
		"pl": "polish",
		"pt": "portuguese",
		"ro": "romanian",
		"ru": "russian",
		"sr": "serbian",
		"sk": "slovak",
		"sl": "slovenian",
		"es": "spanish",
		"sv": "swedish",
		"th": "thai",
		"tr": "turkish",
		"uk": "ukrainian",
		"vi": "vietnamese",
	}[base.String()]
}

func resolveTags(meta api.UploadSubject) string {
	if meta.EffectiveMetadata.GenresProvenance.IsManual() {
		return strings.TrimSpace(strings.ToLower(strings.ReplaceAll(trackers.PreferredGenreText(meta, ""), ", ", ",")))
	}
	if meta.ProviderMetadata.TMDB != nil {
		return strings.TrimSpace(strings.ToLower(strings.ReplaceAll(meta.ProviderMetadata.TMDB.Genres, ", ", ",")))
	}
	return strings.TrimSpace(strings.ToLower(meta.Release.Genre))
}

func resolveMediaFlags(meta api.UploadSubject) map[string]string {
	flags := map[string]string{}
	audio := strings.ToLower(strings.TrimSpace(meta.Audio))
	hdr := strings.ToUpper(strings.TrimSpace(meta.HDR))
	if strings.Contains(audio, "atmos") {
		flags["dolby_atmos"] = "on"
	}
	if strings.Contains(audio, "dts:x") {
		flags["dts_x"] = "on"
	}
	if meta.Channels == "5.1" {
		flags["audio_51"] = "on"
	}
	if meta.Channels == "7.1" {
		flags["audio_71"] = "on"
	}
	if meta.BitDepth == "10" && hdr == "" {
		flags["10_bit"] = "on"
	}
	if strings.Contains(hdr, "DV") {
		flags["dolby_vision"] = "on"
	}
	if strings.Contains(hdr, "HDR10+") {
		flags["hdr10plus"] = "on"
	} else if strings.Contains(hdr, "HDR") {
		flags["hdr10"] = "on"
	}
	return flags
}

func resolveMovieType(meta api.UploadSubject) string {
	if meta.ProviderMetadata.IMDB != nil && meta.ProviderMetadata.IMDB.RuntimeMinutes > 0 && meta.ProviderMetadata.IMDB.RuntimeMinutes < 45 {
		return "2"
	}
	return "1"
}

// editionFields maps categorized facts exactly; opaque/display text is never identity.
func editionFields(meta api.UploadSubject) (string, string) {
	var keys, custom []string
	add := func(value, key string) {
		value = strings.TrimSpace(value)
		if key != "" {
			if !slices.Contains(keys, key) {
				keys = append(keys, key)
			}
		} else if value != "" && !slices.Contains(custom, value) {
			custom = append(custom, value)
		}
	}
	add(meta.Cut, map[string]string{
		"director's cut":   "director_s_cut",
		"directors cut":    "director_s_cut",
		"extended":         "extended_edition",
		"extended edition": "extended_edition",
		"extended cut":     "extended_edition",
		"theatrical":       "theatrical_cut",
		"theatrical cut":   "theatrical_cut",
		"uncut":            "uncut",
		"unrated":          "unrated",
	}[strings.ToLower(strings.ReplaceAll(strings.TrimSpace(meta.Cut), "’", "'"))])
	add(meta.Edition, map[string]string{
		"masters of cinema":         "masters_of_cinema",
		"criterion":                 "the_criterion_collection",
		"the criterion collection":  "the_criterion_collection",
		"warner archive collection": "warner_archive_collection",
		"rifftrax":                  "rifftrax",
	}[strings.ToLower(strings.TrimSpace(meta.Edition))])
	add(meta.Presentation, map[string]string{
		"3d anaglyph":   "3d_anaglyph",
		"3d full sbs":   "3d_full_sbs",
		"3d half ou":    "3d_half_ou",
		"3d half sbs":   "3d_half_sbs",
		"2d/3d edition": "2d_3d_edition",
	}[strings.ToLower(strings.TrimSpace(meta.Presentation))])
	setKey := ""
	if strings.EqualFold(strings.TrimSpace(meta.EditionSet), "2in1") {
		setKey = "2_in_1"
	}
	add(meta.EditionSet, setKey)
	for _, feature := range meta.ReleaseFeatures {
		key := map[api.ReleaseFeature]string{
			api.ReleaseFeatureRemaster:      "remaster",
			api.ReleaseFeature4KRemaster:    "4k_remaster",
			api.ReleaseFeature4KRestoration: "4k_restoration",
			api.ReleaseFeatureTwoDiscSet:    "2_disc_set",
			api.ReleaseFeatureExtras:        "extras",
			api.ReleaseFeature2D3DEdition:   "2d_3d_edition",
			api.ReleaseFeature3DAnaglyph:    "3d_anaglyph",
			api.ReleaseFeature3DFullSBS:     "3d_full_sbs",
			api.ReleaseFeature3DHalfOU:      "3d_half_ou",
			api.ReleaseFeature3DHalfSBS:     "3d_half_sbs",
		}[feature]
		text := string(feature)
		if feature == api.ReleaseFeatureHybrid {
			text = "Hybrid"
		}
		add(text, key)
	}
	for _, part := range meta.EditionDisplay {
		if part.Feature == "" {
			if strings.Trim(part.Text, " /·+|\t\n") == "" ||
				slices.ContainsFunc([]string{meta.Cut, meta.Edition, meta.Presentation, meta.EditionSet}, func(value string) bool {
					return strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(part.Text))
				}) {
				continue
			}
			add(part.Text, "")
		}
	}
	return strings.Join(keys, " / "), strings.Join(custom, " / ")
}
