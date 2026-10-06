// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveCategoryID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		meta api.UploadSubject
		want string
	}{
		{"movie", api.UploadSubject{Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie}}, categoryMovie},
		{"series", api.UploadSubject{Identity: api.ExternalIdentity{Category: api.CanonicalCategoryTV}}, categorySeries},
		{"anime movie", api.UploadSubject{Anime: true, Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie}}, categoryAnimeMovie},
		{"anime series", api.UploadSubject{Anime: true, Identity: api.ExternalIdentity{Category: api.CanonicalCategoryTV}}, categoryAnimeSeries},
	}
	for _, tc := range tests {
		if got := resolveCategoryID(tc.meta); got != tc.want {
			t.Errorf("%s: category = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestResolveQualityID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		meta api.UploadSubject
		want string
	}{
		{"remux", api.UploadSubject{Type: "REMUX"}, "74"},
		{"web-dl", api.UploadSubject{Type: "WEBDL"}, "59"},
		{"webrip", api.UploadSubject{Type: "WEBRIP"}, "73"},
		{"hdtv", api.UploadSubject{Type: "HDTV"}, "53"},
		{"bluray encode", api.UploadSubject{Type: "ENCODE", Source: "BluRay"}, "48"},
		{"dvd encode", api.UploadSubject{Type: "ENCODE", Source: "DVD"}, "44"},
		{"bd25", api.UploadSubject{
			Type:       "DISC",
			DiscType:   "BDMV",
			SourceSize: 20 << 30,
		}, "75"},
		{"bd50", api.UploadSubject{
			Type:       "DISC",
			DiscType:   "BDMV",
			SourceSize: 40 << 30,
		}, "76"},
		{"bd66", api.UploadSubject{
			Type:       "DISC",
			DiscType:   "BDMV",
			SourceSize: 60 << 30,
		}, "77"},
		{"bd100", api.UploadSubject{
			Type:       "DISC",
			DiscType:   "BDMV",
			SourceSize: 90 << 30,
		}, "78"},
		{"dvd5", api.UploadSubject{
			Type:       "DISC",
			DiscType:   "DVD",
			SourceSize: 4_000_000_000,
		}, "80"},
		{"dvd9", api.UploadSubject{
			Type:       "DISC",
			DiscType:   "DVD",
			SourceSize: 7_000_000_000,
		}, "81"},
		{"unknown", api.UploadSubject{Type: "MYSTERY"}, ""},
	}
	for _, tc := range tests {
		if got := resolveQualityID(tc.meta); got != tc.want {
			t.Errorf("%s: quality = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestResolveVideoCodecID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		meta api.UploadSubject
		want string
	}{
		{"x265 hdr10", api.UploadSubject{VideoEncode: "x265", HDR: "HDR"}, "191"},
		{"hevc dolby vision only", api.UploadSubject{VideoCodec: "HEVC", HDR: "DV"}, "193"},
		{"avc hdr", api.UploadSubject{VideoCodec: "AVC", HDR: "HDR"}, "197"},
		{"hevc sdr", api.UploadSubject{VideoCodec: "HEVC"}, "192"},
		{"x265 sdr", api.UploadSubject{VideoEncode: "x265"}, "185"},
		{"avc", api.UploadSubject{VideoCodec: "AVC"}, "195"},
		{"x264", api.UploadSubject{VideoEncode: "x264"}, "184"},
		{"av1", api.UploadSubject{VideoCodec: "AV1"}, "194"},
		{"vc-1", api.UploadSubject{VideoCodec: "VC-1"}, "187"},
		{"mpeg-2", api.UploadSubject{VideoCodec: "MPEG-2"}, "178"},
		{"unknown", api.UploadSubject{VideoCodec: "Theora"}, "183"},
	}
	for _, tc := range tests {
		if got := resolveVideoCodecID(tc.meta); got != tc.want {
			t.Errorf("%s: video codec = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestResolveAudioCodecID(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"TrueHD Atmos 7.1": "164",
		"DD+ 5.1 Atmos":    "161",
		"DTS:X 7.1":        "160",
		"DTS-HD MA 5.1":    "159",
		"DTS-HD HRA 7.1":   "158",
		"DD 5.1":           "150",
		"DTS 5.1":          "149",
		"FLAC 2.0":         "148",
		"LPCM 2.0":         "156",
		"AAC 2.0":          "151",
		"Opus 5.1":         "162",
		"":                 "155",
	}
	for audio, want := range tests {
		if got := resolveAudioCodecID(api.UploadSubject{Audio: audio}); got != want {
			t.Errorf("%q: audio codec = %q, want %q", audio, got, want)
		}
	}
}

func TestResolveAudioIDAndSubtitle(t *testing.T) {
	t.Parallel()

	english := &api.TMDBMetadata{OriginalLanguage: "en"}
	tests := []struct {
		name         string
		meta         api.UploadSubject
		wantAudio    string
		wantSubtitle string
	}{
		{
			name:         "dual audio with portuguese subtitles",
			meta:         api.UploadSubject{AudioLanguages: []string{"English", "Portuguese"}, SubtitleLanguages: []string{"Portuguese"}},
			wantAudio:    audioDual,
			wantSubtitle: subtitleEmbedded,
		},
		{
			name:         "dual audio without portuguese subtitles",
			meta:         api.UploadSubject{AudioLanguages: []string{"English", "Portuguese"}},
			wantAudio:    audioDual,
			wantSubtitle: subtitleNone,
		},
		{
			name:      "dubbed only",
			meta:      api.UploadSubject{AudioLanguages: []string{"Portuguese"}},
			wantAudio: audioDubbed,
		},
		{
			name:         "subtitled",
			meta:         api.UploadSubject{AudioLanguages: []string{"English"}, SubtitleLanguages: []string{"Portuguese"}},
			wantAudio:    audioSubtitled,
			wantSubtitle: subtitleEmbedded,
		},
		{
			name:      "original only",
			meta:      api.UploadSubject{AudioLanguages: []string{"English"}},
			wantAudio: audioOriginal,
		},
		{
			name: "national production",
			meta: api.UploadSubject{
				AudioLanguages:   []string{"Portuguese"},
				ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{OriginalLanguage: "pt"}},
			},
			wantAudio: audioNational,
		},
	}
	for _, tc := range tests {
		if tc.meta.ProviderMetadata.TMDB == nil {
			tc.meta.ProviderMetadata.TMDB = english
		}
		audio := resolveAudioID(tc.meta)
		if audio != tc.wantAudio {
			t.Errorf("%s: audio = %q, want %q", tc.name, audio, tc.wantAudio)
		}
		if got := resolveSubtitle(tc.meta, audio); got != tc.wantSubtitle {
			t.Errorf("%s: subtitle = %q, want %q", tc.name, got, tc.wantSubtitle)
		}
	}
}

func TestResolveLanguageIDUsesSiteIDs(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"en": "1",
		"ko": "19",
		"hi": "22",
		"tr": "23",
		"pt": "8",
		"xx": "11",
	}
	for language, want := range tests {
		meta := api.UploadSubject{ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{OriginalLanguage: language}}}
		if got := resolveLanguageID(meta); got != want {
			t.Errorf("%s: language = %q, want %q", language, got, want)
		}
	}
}

func TestResolveGenreIDs(t *testing.T) {
	t.Parallel()

	got := resolveGenreIDs("Ação e Aventura, Drama, Sci-Fi & Fantasy, Ação, Desconhecido, Faroeste")
	want := []string{"205", "206", "231", "236", "234", "235"}
	if !slices.Equal(got, want) {
		t.Fatalf("genres = %v, want %v", got, want)
	}
	if got := resolveGenreIDs("Desconhecido"); len(got) != 0 {
		t.Fatalf("unknown genre mapped to %v", got)
	}
}

func TestResolveContainerID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		meta api.UploadSubject
		want string
	}{
		{api.UploadSubject{Container: "mkv"}, "92"},
		{api.UploadSubject{Container: ".MP4"}, "94"},
		{api.UploadSubject{Container: "iso"}, "105"},
		{api.UploadSubject{Container: "flv"}, containerOther},
		{api.UploadSubject{DiscType: "BDMV"}, "91"},
		{api.UploadSubject{DiscType: "DVD"}, "101"},
		{api.UploadSubject{}, ""},
	}
	for _, tc := range tests {
		if got := resolveContainerID(tc.meta); got != tc.want {
			t.Errorf("container %q/%q = %q, want %q", tc.meta.Container, tc.meta.DiscType, got, tc.want)
		}
	}
}
