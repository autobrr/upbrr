// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gpw

import (
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveCodec(meta api.UploadSubject) string {
	codec := strings.ToLower(strings.TrimSpace(metautil.FirstNonEmptyTrimmed(meta.VideoEncode, meta.VideoCodec)))
	switch {
	case strings.Contains(codec, "hevc"), strings.Contains(codec, "265"):
		return "HEVC"
	case strings.Contains(codec, "avc"), strings.Contains(codec, "264"):
		return "AVC"
	case strings.Contains(codec, "vc-1"):
		return "VC-1"
	default:
		return "Other"
	}
}

func resolveContainer(meta api.UploadSubject) string {
	container := strings.ToLower(strings.TrimSpace(meta.Container))
	switch container {
	case "mkv", "mp4", "avi", "vob", "m2ts":
		return strings.ToUpper(container)
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
	if len(meta.SubtitleLanguages) > 0 {
		return "1"
	}
	return "3"
}

func resolveSubtitles(meta api.UploadSubject) []string {
	out := make([]string, 0, len(meta.SubtitleLanguages))
	for _, lang := range meta.SubtitleLanguages {
		switch strings.ToLower(strings.TrimSpace(lang)) {
		case "english", "en":
			out = append(out, "English")
		case "chinese", "zh":
			out = append(out, "Chinese")
		case "portuguese", "pt":
			out = append(out, "Portuguese")
		}
	}
	return out
}

func resolveTags(meta api.UploadSubject) string {
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
