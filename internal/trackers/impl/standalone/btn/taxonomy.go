// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

var (
	btnInputPattern       = regexp.MustCompile(`(?is)<input[^>]*name=["']([^"']+)["'][^>]*value=["']([^"']*)["'][^>]*>`)
	btnTextAreaPattern    = regexp.MustCompile(`(?is)<textarea[^>]*name=["']album_desc["'][^>]*>(.*?)</textarea>`)
	btnSelectPattern      = regexp.MustCompile(`(?is)<select[^>]*name=["']([^"']+)["'][^>]*>(.*?)</select>`)
	btnSuccessURLPattern  = regexp.MustCompile(`torrents\.php\?id=(\d+)(?:&torrentid=(\d+))?`)
	btnHTMLURLAttrPattern = regexp.MustCompile(`(?is)\b(?:href|action)=["']([^"']+)["']`)
	btnIMDBEpisodePattern = regexp.MustCompile(`(?i)(?:^|\bE|episode\s*)(\d{1,4})(?:\b|$)`)
	// btnCountryMap maps normalized BTN country option labels and exact
	// metadata-source country codes to BTN's country select values.
	//literalpolicy:allow aliases are grouped by BTN country value
	btnCountryMap = map[string]string{
		"se": "1", "swe": "1", "sweden": "1",
		"us": "2", "usa": "2", "united states": "2", "united states of america": "2",
		"ru": "3", "rus": "3", "russia": "3", "russian federation": "3",
		"fi": "4", "fin": "4", "finland": "4",
		"ca": "5", "can": "5", "canada": "5",
		"fr": "6", "fra": "6", "france": "6",
		"de": "7", "deu": "7", "germany": "7",
		"cn": "8", "chn": "8", "china": "8",
		"it": "9", "ita": "9", "italy": "9",
		"dk": "10", "dnk": "10", "denmark": "10",
		"no": "11", "nor": "11", "norway": "11",
		"gb": "12", "uk": "12", "gbr": "12", "united kingdom": "12",
		"ie": "13", "irl": "13", "ireland": "13",
		"pl": "14", "pol": "14", "poland": "14",
		"nl": "15", "nld": "15", "netherlands": "15",
		"be": "16", "bel": "16", "belgium": "16",
		"jp": "17", "jpn": "17", "japan": "17",
		"br": "18", "bra": "18", "brazil": "18",
		"ar": "19", "arg": "19", "argentina": "19",
		"au": "20", "aus": "20", "australia": "20",
		"nz": "21", "nzl": "21", "new zealand": "21",
		"es": "22", "esp": "22", "spain": "22",
		"pt": "23", "prt": "23", "portugal": "23",
		"mx": "24", "mex": "24", "mexico": "24",
		"sg": "25", "sgp": "25", "singapore": "25",
		"za": "26", "zaf": "26", "south africa": "26",
		"kr": "27", "kor": "27", "south korea": "27",
		"jm": "28", "jam": "28", "jamaica": "28",
		"lu": "29", "lux": "29", "luxembourg": "29",
		"hk": "30", "hkg": "30", "hong kong": "30",
		"bz": "31", "blz": "31", "belize": "31",
		"dz": "32", "dza": "32", "algeria": "32",
		"ao": "33", "ago": "33", "angola": "33",
		"at": "34", "aut": "34", "austria": "34",
		"yu": "35", "yug": "35", "yugoslavia": "35",
		"ws": "36", "wsm": "36", "western samoa": "36",
		"my": "37", "mys": "37", "malaysia": "37",
		"do": "38", "dom": "38", "dominican republic": "38",
		"gr": "39", "grc": "39", "greece": "39",
		"gt": "40", "gtm": "40", "guatemala": "40",
		"il": "41", "isr": "41", "israel": "41",
		"pk": "42", "pak": "42", "pakistan": "42",
		"cz": "43", "cze": "43", "czech republic": "43", "czechia": "43",
		"rs": "44", "srb": "44", "serbia": "44",
		"sc": "45", "syc": "45", "seychelles": "45",
		"tw": "46", "twn": "46", "taiwan": "46",
		"pr": "47", "pri": "47", "puerto rico": "47",
		"cl": "48", "chl": "48", "chile": "48",
		"cu": "49", "cub": "49", "cuba": "49",
		"cg": "50", "cog": "50", "congo": "50",
		"af": "51", "afg": "51", "afghanistan": "51",
		"tr": "52", "tur": "52", "turkey": "52",
		"uz": "53", "uzb": "53", "uzbekistan": "53",
		"ch": "54", "che": "54", "switzerland": "54",
		"ki": "55", "kir": "55", "kiribati": "55",
		"ph": "56", "phl": "56", "philippines": "56",
		"bf": "57", "bfa": "57", "burkina faso": "57",
		"ng": "58", "nga": "58", "nigeria": "58",
		"is": "59", "isl": "59", "iceland": "59",
		"nr": "60", "nru": "60", "nauru": "60",
		"si": "61", "svn": "61", "slovenia": "61",
		"al": "62", "alb": "62", "albania": "62",
		"tm": "63", "tkm": "63", "turkmenistan": "63",
		"ba": "64", "bih": "64", "bosnia herzegovina": "64", "bosnia and herzegovina": "64",
		"ad": "65", "and": "65", "andorra": "65",
		"lt": "66", "ltu": "66", "lithuania": "66",
		"in": "67", "ind": "67", "india": "67",
		"an": "68", "ant": "68", "netherlands antilles": "68",
		"ua": "69", "ukr": "69", "ukraine": "69",
		"ve": "70", "ven": "70", "venezuela": "70",
		"hu": "71", "hun": "71", "hungary": "71",
		"ro": "72", "rou": "72", "romania": "72",
		"vu": "73", "vut": "73", "vanuatu": "73",
		"vn": "74", "vnm": "74", "vietnam": "74",
		"tt": "75", "tto": "75", "trinidad": "75", "trinidad and tobago": "75",
		"hn": "76", "hnd": "76", "honduras": "76",
		"kg": "77", "kgz": "77", "kyrgyzstan": "77",
		"ec": "78", "ecu": "78", "ecuador": "78",
		"bs": "79", "bhs": "79", "bahamas": "79",
		"pe": "80", "per": "80", "peru": "80",
		"kh": "81", "khm": "81", "cambodia": "81",
		"bb": "82", "brb": "82", "barbados": "82",
		"bd": "83", "bgd": "83", "bangladesh": "83",
		"la": "84", "lao": "84", "laos": "84",
		"uy": "85", "ury": "85", "uruguay": "85",
		"ag": "86", "atg": "86", "antigua barbuda": "86", "antigua and barbuda": "86",
		"py": "87", "pry": "87", "paraguay": "87",
		"su": "88", "sun": "88", "soviet": "88", "soviet union": "88", "ussr": "88", "union of soviet socialist repu": "88",
		"th": "89", "tha": "89", "thailand": "89",
		"sn": "90", "sen": "90", "senegal": "90",
		"tg": "91", "tgo": "91", "togo": "91",
		"kp": "92", "prk": "92", "north korea": "92",
		"hr": "93", "hrv": "93", "croatia": "93",
		"ee": "94", "est": "94", "estonia": "94",
		"co": "95", "col": "95", "colombia": "95",
		"lb": "96", "lbn": "96", "lebanon": "96",
		"lv": "97", "lva": "97", "latvia": "97",
		"cr": "98", "cri": "98", "costa rica": "98",
		"eg": "99", "egy": "99", "egypt": "99",
		"bg": "100", "bgr": "100", "bulgaria": "100",
		"isle de muerte": "101",
		"fj":             "102", "fji": "102", "fiji": "102",
		"mk": "103", "mkd": "103", "macedonia": "103",
		"kw": "104", "kwt": "104", "kuwait": "104",
		"lk": "105", "lka": "105", "sri lanka": "105",
		"ir": "106", "irn": "106", "iran": "106",
		"arab league": "107",
		"sa":          "108", "sau": "108", "saudi arabia": "108",
		"scotland": "109",
		"sk":       "110", "svk": "110", "slovakia": "110",
		"id": "111", "idn": "111", "indonesia": "111",
		"wales": "112",
		"bn":    "113", "brn": "113", "brunei": "113",
	}
)

// resolveOrigin derives the origin from
// prepared scene and season-pack metadata, group tag and username
func resolveOrigin(meta api.UploadSubject) string {
	group := strings.TrimSpace(meta.Release.Group)
	if seasonPackHasMixedGroups(meta) {
		return "Mixed"
	}
	if isBTNSceneRelease(meta) {
		return "Scene"
	}
	if group != "" && isNoGroupTag(group) || isBTNInternalGroup(meta) {
		return "None"
	}
	return "P2P"
}

// resolveBTNSameOriginURL resolves an HTML URL attribute against the current
// BTN page and accepts only URLs on the configured BTN origin.

// mapContainer maps local container metadata to BTN's format dropdown. Autofill
// is used only when metadata does not resolve to a BTN-supported value.
func mapContainer(meta api.UploadSubject, fields map[string]string) string {
	allowed := map[string]struct{}{
		"AVI":   {},
		"MKV":   {},
		"VOB":   {},
		"MPEG":  {},
		"MP4":   {},
		"ISO":   {},
		"WMV":   {},
		"TS":    {},
		"M4V":   {},
		"M2TS":  {},
		"Mixed": {},
	}
	container := strings.ToLower(strings.TrimSpace(meta.Container))
	mapped := map[string]string{
		"avi":  "AVI",
		"mkv":  "MKV",
		"vob":  "VOB",
		"mpg":  "MPEG",
		"mpeg": "MPEG",
		"mp4":  "MP4",
		"iso":  "ISO",
		"wmv":  "WMV",
		"ts":   "TS",
		"m4v":  "M4V",
		"m2ts": "M2TS",
	}[container]
	if mapped == "" && strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		mapped = "M2TS"
	}
	if mapped == "" && strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		mapped = "VOB"
	}
	for _, candidate := range []string{mapped, fields["format"], "Mixed"} {
		if _, ok := allowed[candidate]; ok {
			return candidate
		}
	}
	return ""
}

// mapCodec maps local video codec metadata to BTN's bitrate dropdown. Autofill
// is used only when metadata does not resolve to a BTN-supported value.
func mapCodec(meta api.UploadSubject, fields map[string]string) string {
	allowed := map[string]struct{}{
		"XViD":       {},
		"MPEG2":      {},
		"DiVX":       {},
		"DVDR":       {},
		"VC-1":       {},
		"H.264":      {},
		"H.265":      {},
		"WMV":        {},
		"BD":         {},
		"x264-Hi10P": {},
		"VP9":        {},
		"Mixed":      {},
	}
	videoEncode := strings.ToLower(strings.TrimSpace(meta.VideoEncode))
	videoCodec := strings.ToLower(strings.TrimSpace(meta.VideoCodec))
	bitDepth := strings.TrimSpace(meta.BitDepth)
	mapped := ""
	if (strings.Contains(videoEncode, "hi10") || bitDepth == "10") &&
		(strings.Contains(videoEncode, "x264") || strings.Contains(videoCodec, "avc") || strings.Contains(videoCodec, "h.264")) {
		mapped = "x264-Hi10P"
	}
	if mapped == "" {
		lookup := map[string]string{
			"xvid":   "XViD",
			"divx":   "DiVX",
			"mpeg-2": "MPEG2",
			"mpeg2":  "MPEG2",
			"vc-1":   "VC-1",
			"wmv":    "WMV",
			"vp9":    "VP9",
			"avc":    "H.264",
			"h.264":  "H.264",
			"h264":   "H.264",
			"x264":   "H.264",
			"hevc":   "H.265",
			"h.265":  "H.265",
			"h265":   "H.265",
			"x265":   "H.265",
		}
		for _, value := range []string{videoEncode, videoCodec} {
			for needle, resolved := range lookup {
				if strings.Contains(value, needle) {
					mapped = resolved
					break
				}
			}
			if mapped != "" {
				break
			}
		}
	}
	for _, candidate := range []string{mapped, fields["bitrate"], "Mixed"} {
		if _, ok := allowed[candidate]; ok {
			return candidate
		}
	}
	return ""
}

// mapResolution returns the BTN resolution value derived from local metadata,
// falling back to BTN autofill only when metadata does not map to a BTN option.
func mapResolution(meta api.UploadSubject, fields map[string]string) string {
	switch strings.ToLower(strings.TrimSpace(meta.Release.Resolution)) {
	case "2160p", "4320p", "8640p", "4k", "8k":
		return "2160p"
	case "1080p", "1440p":
		return "1080p"
	case "1080i":
		return "1080i"
	case "720p":
		return "720p"
	case "sd":
		return "SD"
	case "portable device":
		return "Portable Device"
	case "mixed":
		return "Mixed"
	}
	switch strings.TrimSpace(fields["resolution"]) {
	case "SD", "720p", "1080p", "1080i", "2160p", "Portable Device", "Mixed":
		return strings.TrimSpace(fields["resolution"])
	default:
		return "SD"
	}
}

// resolveCountryID extracts the first available country from TVDB, TMDB, then
// IMDB metadata and returns its BTN country id. Country codes and names are
// matched only against normalized exact aliases so ambiguous inputs do not
// depend on map iteration order.
func resolveCountryID(meta api.UploadSubject) string {
	var countryStr string

	// Try TVDB first (ISO 3166-1 alpha-3, lowercase)
	if meta.ProviderMetadata.TVDB != nil && meta.ProviderMetadata.TVDB.OriginalCountry != "" {
		countryStr = meta.ProviderMetadata.TVDB.OriginalCountry
	}

	// Fall back to TMDB (ISO 3166-1 alpha-2, uppercase)
	if countryStr == "" && meta.ProviderMetadata.TMDB != nil && len(meta.ProviderMetadata.TMDB.OriginCountry) > 0 {
		countryStr = meta.ProviderMetadata.TMDB.OriginCountry[0]
	}

	// Fall back to IMDB (full country names)
	if countryStr == "" && meta.ProviderMetadata.IMDB != nil && meta.ProviderMetadata.IMDB.Country != "" {
		// IMDB can have multiple countries separated by commas, take the first one
		parts := strings.Split(meta.ProviderMetadata.IMDB.Country, ",")
		if len(parts) > 0 {
			countryStr = strings.TrimSpace(parts[0])
		}
	}

	if countryStr == "" {
		return ""
	}

	if id, ok := btnCountryMap[normalizeBTNCountryAlias(countryStr)]; ok {
		return id
	}

	return ""
}

// resolveBTNOriginalLanguage returns provider original language in BTN
// priority order: TVDB first, then IMDb when TVDB has no value.
func resolveBTNOriginalLanguage(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TVDB != nil {
		if language := strings.TrimSpace(meta.ProviderMetadata.TVDB.OriginalLanguage); language != "" {
			return language
		}
	}
	if meta.ProviderMetadata.IMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.OriginalLanguage)
	}
	return ""
}

// isBTNEnglishLanguage reports whether a provider language value represents
// English and should therefore not trigger BTN's foreign flag.
func isBTNEnglishLanguage(language string) bool {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "en", "eng", "english":
		return true
	default:
		return false
	}
}

// normalizeBTNCountryAlias lowercases and collapses punctuation so metadata
// country names can be compared against BTN's exact alias table.
func normalizeBTNCountryAlias(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.NewReplacer(
		"&", " and ",
		".", " ",
		",", " ",
		"-", " ",
		"_", " ",
		"'", " ",
		"(", " ",
		")", " ",
	).Replace(normalized)
	return strings.Join(strings.Fields(normalized), " ")
}

func resolveUploadType(meta api.UploadSubject) string {
	if meta.TVPack {
		return "Season"
	}
	_, episode := resolveBTNTVSeasonEpisode(meta)
	if episode > 0 {
		return "Episode"
	}
	return "Season"
}

func resolveBTNTags(meta api.UploadSubject, fields map[string]string) string {
	if tags := strings.TrimSpace(fields["tags"]); tags != "" {
		return tags
	}
	if meta.ProviderMetadata.TVDB != nil {
		if tags := mapBTNGenres(meta.ProviderMetadata.TVDB.Genres); tags != "" {
			return tags
		}
	}
	if meta.ProviderMetadata.IMDB != nil {
		return mapBTNGenres(meta.ProviderMetadata.IMDB.Genres)
	}
	return ""
}

func mapBTNGenres(genres string) string {
	normalized := normalizeBTNGenreText(genres)
	if normalized == "" {
		return ""
	}
	type genreAlias struct {
		label   string
		aliases []string
	}
	allowed := []genreAlias{
		{label: "Action", aliases: []string{"action"}},
		{label: "Adventure", aliases: []string{"adventure"}},
		{label: "Animation", aliases: []string{"animation"}},
		{label: "Anime", aliases: []string{"anime"}},
		{label: "Awards Show", aliases: []string{"awards show"}},
		{label: "Children", aliases: []string{"children", "kids"}},
		{label: "Comedy", aliases: []string{"comedy"}},
		{label: "Crime", aliases: []string{"crime"}},
		{label: "Documentary", aliases: []string{"documentary"}},
		{label: "Drama", aliases: []string{"drama"}},
		{label: "Family", aliases: []string{"family"}},
		{label: "Fantasy", aliases: []string{"fantasy"}},
		{label: "Food", aliases: []string{"food"}},
		{label: "Game Show", aliases: []string{"game show"}},
		{label: "History", aliases: []string{"history"}},
		{label: "Home and Garden", aliases: []string{"home and garden", "home garden"}},
		{label: "Horror", aliases: []string{"horror"}},
		{label: "Indie", aliases: []string{"indie"}},
		{label: "Martial Arts", aliases: []string{"martial arts"}},
		{label: "Mini-Series", aliases: []string{"mini series", "miniseries"}},
		{label: "Musical", aliases: []string{"musical", "music"}},
		{label: "Mystery", aliases: []string{"mystery"}},
		{label: "News", aliases: []string{"news"}},
		{label: "Podcast", aliases: []string{"podcast"}},
		{label: "Reality", aliases: []string{"reality"}},
		{label: "Romance", aliases: []string{"romance"}},
		{label: "Science Fiction", aliases: []string{"science fiction", "sci fi", "scifi"}},
		{label: "Soap", aliases: []string{"soap"}},
		{label: "Sport", aliases: []string{"sport", "sports"}},
		{label: "Suspense", aliases: []string{"suspense"}},
		{label: "Talk Show", aliases: []string{"talk show"}},
		{label: "Thriller", aliases: []string{"thriller"}},
		{label: "Travel", aliases: []string{"travel"}},
		{label: "War", aliases: []string{"war"}},
		{label: "Western", aliases: []string{"western"}},
	}
	tags := make([]string, 0, len(allowed))
	for _, genre := range allowed {
		for _, alias := range genre.aliases {
			if normalizedBTNGenreContains(normalized, alias) {
				tags = append(tags, genre.label)
				break
			}
		}
	}
	return strings.Join(tags, ", ")
}

func normalizeBTNGenreText(value string) string {
	replacer := strings.NewReplacer(
		"&", " and ",
		"/", " ",
		";", " ",
		":", " ",
		".", " ",
		",", " ",
		"-", " ",
		"_", " ",
		"(", " ",
		")", " ",
	)
	return strings.Join(strings.Fields(replacer.Replace(strings.ToLower(strings.TrimSpace(value)))), " ")
}

func normalizedBTNGenreContains(normalized string, alias string) bool {
	alias = normalizeBTNGenreText(alias)
	return strings.Contains(" "+normalized+" ", " "+alias+" ")
}

func mapSource(meta api.UploadSubject, fields map[string]string) string {
	allowed := map[string]struct{}{
		"HDTV":    {},
		"PDTV":    {},
		"DSR":     {},
		"DVDRip":  {},
		"TVRip":   {},
		"VHSRip":  {},
		"Bluray":  {},
		"BDRip":   {},
		"BRRip":   {},
		"DVD5":    {},
		"DVD9":    {},
		"HDDVD":   {},
		"WEB-DL":  {},
		"WEBRip":  {},
		"BD5":     {},
		"BD9":     {},
		"BD25":    {},
		"BD50":    {},
		"Mixed":   {},
		"Unknown": {},
	}
	source := strings.ToLower(strings.TrimSpace(meta.Source))
	typeName := strings.ToUpper(strings.TrimSpace(meta.Type))
	resolution := strings.ToUpper(strings.TrimSpace(meta.Release.Resolution))
	var mapped string
	switch {
	case strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD"):
		mapped = "DVD9"
	case strings.EqualFold(strings.TrimSpace(meta.DiscType), "HDDVD"):
		mapped = "HDDVD"
	case typeName == "WEBDL":
		mapped = "WEB-DL"
	case typeName == "WEBRIP":
		mapped = "WEBRip"
	case typeName == "HDTV" || source == "hdtv":
		mapped = "HDTV"
	case typeName == "DVDRIP":
		mapped = "DVDRip"
	case resolution == "SD" && (source == "bluray" || source == "blu-ray"):
		mapped = "BDRip"
	default:
		mapped = map[string]string{
			"bluray":  "Bluray",
			"blu-ray": "Bluray",
			"bdrip":   "BDRip",
			"brrip":   "BRRip",
			"dvd5":    "DVD5",
			"dvd9":    "DVD9",
			"web-dl":  "WEB-DL",
			"webrip":  "WEBRip",
			"pdtv":    "PDTV",
			"dsr":     "DSR",
			"tvrip":   "TVRip",
			"vhsrip":  "VHSRip",
			"bd5":     "BD5",
			"bd9":     "BD9",
			"bd25":    "BD25",
			"bd50":    "BD50",
		}[source]
	}
	for _, candidate := range []string{mapped, fields["media"], "Unknown"} {
		if _, ok := allowed[candidate]; ok {
			return candidate
		}
	}
	return ""
}
