// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/pkg/api"
)

// Category IDs from the upload page `categories` prop. Anime uploads must use
// a leaf category (59/61); duplicate search queries the parent animeParentCategory.
const (
	categoryMovie       = "4"
	categorySeries      = "3"
	categoryAnimeMovie  = "59"
	categoryAnimeSeries = "61"
)

// Subtitle values from the upload page `subtitles` prop.
const (
	subtitleEmbedded = "embedded"
	subtitleNone     = "none"
)

// Audio attribute IDs (`audio_channels` profile attribute).
const (
	audioDual      = "199"
	audioDubbed    = "200"
	audioSubtitled = "198"
	audioNational  = "201"
	audioOriginal  = "204"
)

const containerOther = "103"

var portugueseLanguageTokens = []string{"portuguese", "português", "pt", "pt-br", "brazilian portuguese"}

func resolveCategoryID(meta api.UploadSubject) string {
	tv := categoryOf(meta) == "TV"
	switch {
	case meta.Anime && tv:
		return categoryAnimeSeries
	case meta.Anime:
		return categoryAnimeMovie
	case tv:
		return categorySeries
	default:
		return categoryMovie
	}
}

// resolveQualityID maps the resolved release type to the site `resolution`
// attribute, which carries source/disc class rather than pixel height.
func resolveQualityID(meta api.UploadSubject) string {
	if strings.EqualFold(meta.Type, "DISC") {
		return resolveDiscQualityID(meta)
	}
	switch strings.ToUpper(strings.TrimSpace(meta.Type)) {
	case "REMUX":
		return "74"
	case "WEBDL":
		return "59"
	case "WEBRIP":
		return "73"
	case "HDTV":
		return "53"
	case "DVDRIP":
		return "44"
	case "BDRIP":
		return "48"
	case "ENCODE":
		if strings.Contains(strings.ToUpper(meta.Source), "DVD") {
			return "44"
		}
		return "48"
	default:
		return ""
	}
}

// resolveDiscQualityID classifies full discs by size against nominal disc
// capacities, which are decimal: DVD5 4.7 GB, BD25/50/66/100 25/50/66/100 GB.
func resolveDiscQualityID(meta api.UploadSubject) string {
	const gigabyte = 1_000_000_000
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "DVD":
		if meta.SourceSize > 4_700_000_000 {
			return "81"
		}
		return "80"
	case "HDDVD":
		return "52"
	}
	switch size := meta.SourceSize; {
	case size > 66*gigabyte:
		return "78"
	case size > 50*gigabyte:
		return "77"
	case size > 25*gigabyte:
		return "76"
	default:
		return "75"
	}
}

func resolveContainerID(meta api.UploadSubject) string {
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "BDMV":
		return "91"
	case "DVD":
		return "101"
	case "HDDVD":
		return containerOther
	}
	ext := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(meta.Container)), ".")
	if ext == "" {
		return ""
	}
	switch ext {
	case "mkv":
		return "92"
	case "mp4":
		return "94"
	case "avi":
		return "89"
	case "m2ts":
		return "91"
	case "ts":
		return "100"
	case "iso":
		return "105"
	case "m4v":
		return "136"
	case "mov":
		return "93"
	case "wmv":
		return "102"
	case "vob":
		return "101"
	default:
		return containerOther
	}
}

func resolveVideoCodecID(meta api.UploadSubject) string {
	codec := strings.ToUpper(strings.TrimSpace(metautil.FirstNonEmptyTrimmed(meta.VideoEncode, meta.VideoCodec)))
	hdr := strings.ToUpper(strings.TrimSpace(meta.HDR))
	isHEVC := strings.Contains(codec, "265") || strings.Contains(codec, "HEVC")
	isAVC := strings.Contains(codec, "264") || strings.Contains(codec, "AVC")
	switch {
	case isHEVC && strings.Contains(hdr, "HDR"):
		return "191"
	case isHEVC && hdr != "":
		return "193"
	case isAVC && hdr != "":
		return "197"
	case strings.Contains(codec, "AV1"):
		return "194"
	case strings.Contains(codec, "HEVC"):
		return "192"
	case strings.Contains(codec, "265"):
		return "185"
	case strings.Contains(codec, "AVC"):
		return "195"
	case strings.Contains(codec, "264"):
		return "184"
	case strings.Contains(codec, "VC-1"), strings.Contains(codec, "VC1"):
		return "187"
	case strings.Contains(codec, "MPEG-2"), strings.Contains(codec, "MPEG2"):
		return "178"
	case strings.Contains(codec, "XVID"):
		return "182"
	case strings.Contains(codec, "DIVX"):
		return "180"
	case strings.Contains(codec, "VP9"):
		return "189"
	default:
		return "183"
	}
}

// audioCodec identifies the primary audio format family.
type audioCodec int

const (
	audioCodecUnknown audioCodec = iota
	audioCodecDTSX
	audioCodecDTSHDMA
	audioCodecDTSHD
	audioCodecTrueHD
	audioCodecDDPlus
	audioCodecDD
	audioCodecDTS
	audioCodecFLAC
	audioCodecLPCM
	audioCodecPCM
	audioCodecAAC
	audioCodecOpus
	audioCodecMP3
	audioCodecVorbis
)

// classifyAudioCodec maps the composite primary audio string. Order matters:
// more specific families must precede the substrings they contain.
func classifyAudioCodec(audio string) audioCodec {
	audio = strings.ToUpper(strings.TrimSpace(audio))
	switch {
	case strings.Contains(audio, "DTS:X"), strings.Contains(audio, "DTS-X"):
		return audioCodecDTSX
	case strings.Contains(audio, "DTS-HD MA"), strings.Contains(audio, "DTS-HD-MA"):
		return audioCodecDTSHDMA
	case strings.Contains(audio, "DTS-HD"):
		return audioCodecDTSHD
	case strings.Contains(audio, "TRUEHD"):
		return audioCodecTrueHD
	case strings.Contains(audio, "DD+"), strings.Contains(audio, "DDP"), strings.Contains(audio, "E-AC-3"), strings.Contains(audio, "EAC3"):
		return audioCodecDDPlus
	case strings.Contains(audio, "DD"), strings.Contains(audio, "AC3"), strings.Contains(audio, "AC-3"):
		return audioCodecDD
	case strings.Contains(audio, "DTS"):
		return audioCodecDTS
	case strings.Contains(audio, "FLAC"):
		return audioCodecFLAC
	case strings.Contains(audio, "LPCM"):
		return audioCodecLPCM
	case strings.Contains(audio, "PCM"):
		return audioCodecPCM
	case strings.Contains(audio, "AAC"):
		return audioCodecAAC
	case strings.Contains(audio, "OPUS"):
		return audioCodecOpus
	case strings.Contains(audio, "MP3"), strings.Contains(audio, "MPEG"):
		return audioCodecMP3
	case strings.Contains(audio, "VORBIS"):
		return audioCodecVorbis
	default:
		return audioCodecUnknown
	}
}

// resolveAudioCodecID maps the primary audio format. The site has no Atmos
// option, so Atmos resolves through its TrueHD or E-AC-3 carrier. Unknown
// formats fall back to the site's generic "other" id.
func resolveAudioCodecID(meta api.UploadSubject) string {
	switch classifyAudioCodec(meta.Audio) { //nolint:exhaustive // unrecognised formats share the generic id below
	case audioCodecDTSX:
		return "160"
	case audioCodecDTSHDMA:
		return "159"
	case audioCodecDTSHD:
		return "158"
	case audioCodecTrueHD:
		return "164"
	case audioCodecDDPlus:
		return "161"
	case audioCodecDD:
		return "150"
	case audioCodecDTS:
		return "149"
	case audioCodecFLAC:
		return "148"
	case audioCodecLPCM:
		return "156"
	case audioCodecPCM:
		return "163"
	case audioCodecAAC:
		return "151"
	case audioCodecOpus:
		return "162"
	case audioCodecMP3:
		return "152"
	case audioCodecVorbis:
		return "165"
	default:
		return "155"
	}
}

var (
	audioChannelsPattern      = regexp.MustCompile(`\b\d\.\d\b`)
	audioChannelsExactPattern = regexp.MustCompile(`^\d\.\d$`)
)

// audioFileNameToken renders the primary audio as the file-name token the site
// requires (DDP5.1, AAC2.0, DTS-HD.MA.5.1, TrueHD.Atmos.7.1). Atmos is a suffix
// on DD+ (DDP5.1.Atmos) but part of the codec for TrueHD. The channel layout is
// taken from meta.Channels only when it is exactly N.N, otherwise from the first
// N.N in meta.Audio. It returns "" when the codec family or channel layout is
// unknown or the site has no naming convention for it, so callers never invent
// a token.
func audioFileNameToken(meta api.UploadSubject) string {
	channels := strings.TrimSpace(meta.Channels)
	if !audioChannelsExactPattern.MatchString(channels) {
		channels = audioChannelsPattern.FindString(meta.Audio)
	}
	if channels == "" {
		return ""
	}
	atmos := strings.Contains(strings.ToUpper(meta.Audio), "ATMOS")
	switch classifyAudioCodec(meta.Audio) { //nolint:exhaustive // MP3, Vorbis and unknown formats have no site naming convention
	case audioCodecDTSX:
		return "DTS-X." + channels
	case audioCodecDTSHDMA:
		return "DTS-HD.MA." + channels
	case audioCodecDTSHD:
		return "DTS-HD." + channels
	case audioCodecTrueHD:
		if atmos {
			return "TrueHD.Atmos." + channels
		}
		return "TrueHD." + channels
	case audioCodecDDPlus:
		if atmos {
			return "DDP" + channels + ".Atmos"
		}
		return "DDP" + channels
	case audioCodecDD:
		return "DD" + channels
	case audioCodecDTS:
		return "DTS" + channels
	case audioCodecFLAC:
		return "FLAC" + channels
	case audioCodecLPCM:
		return "LPCM." + channels
	case audioCodecPCM:
		return "PCM." + channels
	case audioCodecAAC:
		return "AAC" + channels
	case audioCodecOpus:
		return "OPUS" + channels
	default:
		return ""
	}
}

// resolveAudioID classifies Portuguese availability for the `audio_channels` attribute.
func resolveAudioID(meta api.UploadSubject) string {
	audioLangs := lowerStrings(meta.AudioLanguages)
	hasPTAudio := containsAny(audioLangs, portugueseLanguageTokens)
	isOriginalPT := containsAny([]string{strings.ToLower(strings.TrimSpace(resolveOriginalLanguage(meta)))}, portugueseLanguageTokens)
	switch {
	case hasPTAudio && isOriginalPT:
		return audioNational
	case hasPTAudio && countNonPortuguese(audioLangs) > 0:
		return audioDual
	case hasPTAudio:
		return audioDubbed
	case hasPortugueseSubtitles(meta):
		return audioSubtitled
	default:
		return audioOriginal
	}
}

// resolveSubtitle returns the subtitle enum the site requires for dual or
// subtitled audio; other audio classes send an empty value.
func resolveSubtitle(meta api.UploadSubject, audioID string) string {
	if audioID != audioDual && audioID != audioSubtitled {
		return ""
	}
	if hasPortugueseSubtitles(meta) {
		return subtitleEmbedded
	}
	return subtitleNone
}

func hasPortugueseSubtitles(meta api.UploadSubject) bool {
	return containsAny(lowerStrings(meta.SubtitleLanguages), portugueseLanguageTokens)
}

func resolveLanguageID(meta api.UploadSubject) string {
	return mapLanguage(resolveOriginalLanguage(meta), map[string]string{
		"bg": "15",
		"da": "12",
		"de": "3",
		"en": "1",
		"es": "6",
		"fi": "14",
		"fr": "2",
		"hi": "22",
		"it": "4",
		"ja": "5",
		"ko": "19",
		"nl": "17",
		"no": "16",
		"pl": "18",
		"pt": "8",
		"ru": "7",
		"sv": "13",
		"th": "20",
		"tr": "23",
		"zh": "10",
	}, "11")
}

// genreIDs maps lowercase Portuguese genre names to ASC genre option IDs, as
// the upload profile lists them. ASC keeps near-duplicate sport and children's
// options with separate IDs; each is mapped as listed.
// `talk` covers the TMDB TV genre the shared translator does not.
var genreIDs = map[string]string{
	"animação":          "225",
	"aventura":          "206",
	"ação":              "205",
	"biografia":         "226",
	"comédia":           "227",
	"crime":             "228",
	"curta-metragem":    "229",
	"documentário":      "230",
	"drama":             "231",
	"esporte":           "232",
	"esportes":          "219", //nolint:misspell // Portuguese genre name.
	"família":           "233",
	"fantasia":          "234",
	"faroeste":          "235",
	"ficção científica": "236",
	"filme de tv":       "237",
	"cinema tv":         "237",
	"telefilme":         "237",
	"game show":         "238",
	"guerra":            "210",
	"história":          "239",
	"humor":             "214",
	"infantil":          "240",
	"infantis":          "213", //nolint:misspell // Portuguese genre name.
	"mistério":          "241",
	"musical":           "220",
	"música":            "242",
	"novela":            "243",
	"programa de tv":    "244",
	"reality show":      "245",
	"romance":           "246",
	"seriados":          "247",
	"suspense":          "248",
	"talk show":         "249",
	"talk":              "249",
	"terror":            "250",
	"thriller":          "251",
	"tokusatsu":         "252",
}

// resolveGenreIDs maps comma-separated genre names to ASC genre IDs. Names
// missing from genreIDs go through the shared English-to-Portuguese genre
// translator, which covers TMDB TV genres that pt-BR leaves in English.
// Compound genres ("Ação e Aventura", "Sci-Fi & Fantasy") map through their
// parts; unknown genres are dropped.
func resolveGenreIDs(genres string) []string {
	var out []string
	seen := make(map[string]struct{})
	add := func(name string) bool {
		id, ok := lookupGenreID(name)
		if !ok {
			return false
		}
		if _, dup := seen[id]; !dup {
			seen[id] = struct{}{}
			out = append(out, id)
		}
		return true
	}
	for raw := range strings.SplitSeq(genres, ",") {
		key := strings.ToLower(strings.TrimSpace(raw))
		if key == "" || add(key) {
			continue
		}
		translated := strings.ToLower(metautil.TranslateGenreToPortugueseStrict(key))
		if translated != "" && add(translated) {
			continue
		}
		compound := metautil.FirstNonEmptyTrimmed(translated, key)
		for part := range strings.SplitSeq(strings.ReplaceAll(compound, " e ", "&"), "&") {
			add(strings.TrimSpace(part))
		}
	}
	return out
}

func lookupGenreID(name string) (string, bool) {
	if id, ok := genreIDs[name]; ok {
		return id, true
	}
	if translated := strings.ToLower(metautil.TranslateGenreToPortugueseStrict(name)); translated != "" {
		id, ok := genreIDs[translated]
		return id, ok
	}
	return "", false
}

func categoryOf(meta api.UploadSubject) string {
	category, err := meta.Identity.RequireCategory()
	if err != nil {
		return ""
	}
	return strings.ToUpper(string(category))
}

func mapLanguage(value string, mappings map[string]string, fallback string) string {
	key := strings.ToLower(strings.TrimSpace(value))
	if mapped, ok := mappings[key]; ok {
		return mapped
	}
	return fallback
}

func lowerStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		out = append(out, strings.ToLower(strings.TrimSpace(value)))
	}
	return out
}

func countNonPortuguese(values []string) int {
	count := 0
	for _, value := range values {
		if !containsAny([]string{value}, portugueseLanguageTokens) {
			count++
		}
	}
	return count
}

func containsAny(values []string, targets []string) bool {
	for _, value := range values {
		for _, target := range targets {
			if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(target)) {
				return true
			}
		}
	}
	return false
}
