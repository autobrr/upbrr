// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metautil

import (
	"fmt"
	"strings"
	"unicode"
)

// ISO639PortugueseName returns the Portuguese-language display name for an
// ISO 639-1 two-letter language code.  When the code is not recognized,
// fallback is returned instead.
func ISO639PortugueseName(code, fallback string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "" {
		return fallback
	}
	if name, ok := iso639PTBR[code]; ok {
		return name
	}
	return fallback
}

// ParseDimensionStr extracts numeric digits from a value that may contain
// formatting like "1 920" or "1,920".  It returns a string of pure digits,
// or "" when no digits are found.
func ParseDimensionStr(val any) string {
	if val == nil {
		return ""
	}
	s := fmt.Sprintf("%v", val)
	var digits strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	return digits.String()
}

// iso639PTBR maps ISO 639-1 codes to Portuguese display names.
var iso639PTBR = map[string]string{
	"aa": "Afar",
	"ab": "Abcázio",
	"ae": "Avéstico",
	"af": "Africânder",
	"ak": "Acã",
	"am": "Amárico",
	"an": "Aragonês",
	"ar": "Árabe",
	"as": "Assamês",
	"av": "Avárico",
	"ay": "Aimará",
	"az": "Azerbaijano",
	"ba": "Basquir",
	"be": "Bielorrusso",
	"bg": "Búlgaro",
	"bh": "Maithili",
	"bi": "Bislamábichlamar",
	"bm": "Bâmbara",
	"bn": "Bengali ou bangla",
	"bo": "Tibetano",
	"br": "Bretão",
	"bs": "Bósnio",
	"ca": "Catalão",
	"ce": "Tchechenoou checheno",
	"ch": "Chamorro",
	"co": "Corso",
	"cr": "Cree",
	"cs": "Tcheco",
	"cu": "Eslavo eclesiástico",
	"cv": "Tchuvache",
	"cy": "Galês",
	"da": "Dinamarquês",
	"de": "Alemão",
	"dv": "Diveí",
	"dz": "Dzongkha",
	"ee": "Jeje",
	"el": "Grego moderno(desde 1453)",
	"en": "Inglês",
	"eo": "Esperanto",
	"es": "Castelhano",
	"et": "Estoniano",
	"eu": "Basco",
	"fa": "Persa",
	"ff": "Fula",
	"fi": "Finlandês",
	"fj": "Fidjiano",
	"fo": "Feroêsou feróico",
	"fr": "Francês",
	"fy": "Frisãoocidental",
	"ga": "Irlandês",
	"gd": "Gaélico escocês",
	"gl": "Galego",
	"gn": "Guarani",
	"gu": "Gujarati",
	"gv": "Manês",
	"ha": "Hauçá",
	"he": "Hebraico",
	"hi": "Hindi",
	"ho": "Hiri Motu",
	"hr": "Croata",
	"ht": "Crioulo haitiano",
	"hu": "Húngaro",
	"hy": "Armênio",
	"hz": "Hereró",
	"ia": "Interlíngua",
	"id": "Indonésio",
	"ie": "Interlíngua",
	"ig": "Ibo",
	"ii": "YideSichuan",
	"ik": "Inupiaq",
	"io": "Ido",
	"is": "Islandês",
	"it": "Italiano",
	"iu": "Inuktitut",
	"ja": "Japonês",
	"jv": "Javanês",
	"ka": "Georgiano",
	"kg": "Kongo",
	"ki": "Kikuyu",
	"kj": "Oshikwanyama",
	"kk": "Cazaque",
	"kl": "Groenlandês",
	"km": "Khmer",
	"kn": "Canarês",
	"ko": "Coreano",
	"kr": "Kanuri ou canúri",
	"ks": "Caxemir",
	"ku": "Curdo",
	"kv": "Komi",
	"kw": "Córnico",
	"ky": "Quirguiz",
	"la": "Latim",
	"lb": "Luxemburguês",
	"lg": "Luganda",
	"li": "Limburguês",
	"ln": "Lingala",
	"lo": "Laociano",
	"lt": "Lituano",
	"lu": "Luba-catanga",
	"lv": "Letão",
	"mg": "Malgaxe",
	"mh": "Marshallês",
	"mi": "Maori",
	"mk": "Macedônio",
	"ml": "Malaiala",
	"mn": "Mongol",
	"mo": "Moldavo",
	"mr": "Marata",
	"ms": "Malaio",
	"mt": "Maltês",
	"my": "Birmanês",
	"na": "Nauruano",
	"nb": "Bokmål norueguês",
	"nd": "Ndebele do norte",
	"ne": "Nepali, nepalês",
	"ng": "Ndonga",
	"nl": "Holandês",
	"nn": "Novo norueguês",
	"no": "Norueguês",
	"nr": "Ndebele do sul",
	"nv": "Navajo",
	"ny": "Nianja",
	"oc": "Occitano(depois 1500)",
	"oj": "Chippewa",
	"om": "Oromo",
	"or": "Oriá",
	"os": "Oseto",
	"pa": "Panjabi",
	"pi": "Páli",
	"pl": "Polaco",
	"ps": "Pachto",
	"pt": "Português",
	"qu": "Quíchua",
	"rm": "Reto-romano",
	"rn": "Kirundi",
	"ro": "Romeno",
	"ru": "Russo",
	"rw": "Quiniaruanda",
	"sa": "Sânscrito",
	"sc": "Sardo",
	"sd": "Sindi",
	"se": "Samido norte",
	"sg": "Sango",
	"sh": "Servo-croata",
	"si": "Cingalês",
	"sk": "Eslovaco",
	"sl": "Esloveno",
	"sm": "Samoano",
	"sn": "Chona",
	"so": "Somali",
	"sq": "Albanês",
	"sr": "Sérvio",
	"ss": "Suázi",
	"st": "Soto do sul",
	"su": "Sundanês",
	"sv": "Sueco",
	"sw": "Suaíli",
	"ta": "Tâmil",
	"te": "Telugu",
	"tg": "Tajique",
	"th": "Tailandês",
	"ti": "Tigrínia",
	"tk": "Turcomano",
	"tl": "Tagalo",
	"tn": "Tswana",
	"to": "Tonganês",
	"tr": "Turco",
	"ts": "Tsonga",
	"tt": "Tártaro",
	"tw": "Twi",
	"ty": "Taitiano",
	"ug": "Uigur",
	"uk": "Ucraniano",
	"ur": "Urdu",
	"uz": "Uzbeque",
	"ve": "Venda",
	"vi": "Vietnamita",
	"vo": "Volapuque",
	"wa": "Valão",
	"wo": "Uolofe",
	"xh": "Xhosa",
	"yi": "Iídiche",
	"yo": "Iorubá",
	"za": "Zhuang",
	"zh": "Chinês",
	"zu": "Zulu",
}

// TranslateGenreToPortuguese maps a genre string from English to Portuguese if a translation exists.
// Otherwise it returns the genre as-is.
func TranslateGenreToPortuguese(genre string) string {
	cleaned := strings.ToLower(strings.TrimSpace(genre))
	if translated, ok := EnglishToPortugueseGenre[cleaned]; ok {
		return translated
	}
	return genre
}

// TranslateGenreToPortugueseStrict maps an English genre string to Portuguese.
// It returns the translated string, or "" if no translation is found.
func TranslateGenreToPortugueseStrict(genre string) string {
	cleaned := strings.ToLower(strings.TrimSpace(genre))
	if translated, ok := EnglishToPortugueseGenre[cleaned]; ok {
		return translated
	}
	return ""
}

// CapitalizeGenre capitalizes the first letter of a genre string.
func CapitalizeGenre(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// EnglishToPortugueseGenre maps English genre tags to Portuguese.
var EnglishToPortugueseGenre = map[string]string{
	"action & adventure":         "ação e aventura",
	"action":                     "ação",
	"adult":                      "adulto",
	"adventure":                  "aventura",
	"animation":                  "animação",
	"arcade":                     "arcade",
	"biography & autobiography":  "biografia e autobiografia",
	"biography":                  "biografia",
	"board game":                 "tabuleiro",
	"board":                      "tabuleiro",
	"card":                       "cartas",
	"casual":                     "casual",
	"classic":                    "clássico",
	"comedy":                     "comédia",
	"crime":                      "crime",
	"documentary":                "documentário",
	"drama":                      "drama",
	"driving":                    "corrida",
	"educational":                "educativo",
	"family & relationships":     "família e relacionamentos",
	"family":                     "família",
	"film noir":                  "filme noir",
	"game show":                  "game show",
	"fantasy":                    "fantasia",
	"fiction":                    "ficção",
	"fighting":                   "luta",
	"hack and slash":             "ação",
	"hack and slash/beat 'em up": "ação",
	"history":                    "história",
	"horror":                     "terror",
	"indie":                      "indie",
	"juvenile fiction":           "ficção juvenil",
	"kids":                       "infantil",
	"mmo":                        "mmo",
	"moba":                       "moba",
	"music":                      "musical",
	"musical":                    "musical",
	"mystery":                    "mistério",
	"philosophy":                 "filosofia",
	"platform":                   "plataforma",
	"platformer":                 "plataforma",
	"point-and-click":            "aventura",
	"psychological":              "psicológico",
	"psychology":                 "psicologia",
	"puzzle":                     "puzzle",
	"racing":                     "corrida",
	"real time strategy (rts)":   "rts",
	"real time strategy":         "rts",
	"reality":                    "reality show",
	"reality-tv":                 "reality show",
	"religion":                   "religião",
	"rhythm":                     "musical",
	"role-playing (rpg)":         "rpg",
	"role-playing":               "rpg",
	"romance":                    "romance",
	"rpg":                        "rpg",
	"rts":                        "rts",
	"sandbox":                    "sandbox",
	"sci-fi & fantasy":           "ficção científica e fantasia",
	"sci-fi":                     "ficção científica",
	"science fiction":            "ficção científica",
	"science":                    "ciência",
	"self-help":                  "autoajuda",
	"shooter":                    "fps",
	"short":                      "curta-metragem",
	"simulation":                 "simulação",
	"simulator":                  "simulação",
	"slice of life":              "cotidiano",
	"soap":                       "novela",
	"social science":             "ciências sociais",
	"sport":                      "esportes", //nolint:misspell
	"sports":                     "esportes", //nolint:misspell
	"strategy":                   "estratégia",
	"survival":                   "sobrevivência",
	"tactical":                   "estratégia",
	"talk-show":                  "talk show",
	"thriller":                   "suspense",
	"turn-based strategy (tbs)":  "estratégia",
	"turn-based strategy":        "estratégia",
	"tv movie":                   "telefilme",
	"virtual reality":            "rv",
	"visual novel":               "aventura",
	"vr":                         "rv",
	"war":                        "guerra",
	"western":                    "faroeste",
	"young adult fiction":        "ficção jovem adulto",
}
