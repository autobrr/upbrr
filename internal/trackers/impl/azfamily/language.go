// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func languageValues(site string, meta api.PreparedMetadata) languageBundle {
	audioSet := make(map[string]struct{})
	for _, value := range meta.AudioLanguages {
		if id, ok := languageID(site, value); ok {
			audioSet[id] = struct{}{}
		}
	}
	subtitleSet := make(map[string]struct{})
	for _, value := range meta.SubtitleLanguages {
		if id, ok := languageID(site, value); ok {
			subtitleSet[id] = struct{}{}
		}
	}
	return languageBundle{
		Audio:     sortedKeys(audioSet),
		Subtitles: sortedKeys(subtitleSet),
	}
}

func languageID(site string, value string) (string, bool) {
	val := strings.ToLower(strings.TrimSpace(value))
	if val == "" {
		return "", false
	}

	// Tracker-specific overrides
	switch site {
	case "PHD":
		switch val {
		case "portuguese (br)", "por", "pt-br":
			return "187", true
		case "filipino", "fil":
			return "189", true
		case "mooré", "mos":
			return "188", true
		}
	case "AZ":
		switch val {
		case "portuguese (br)", "por", "pt-br":
			return "189", true
		case "filipino", "fil":
			return "188", true
		case "mooré", "mos":
			return "187", true
		}
	case "CZ":
		switch val {
		case "portuguese (br)", "por", "pt-br":
			return "187", true
		case "mooré", "mos":
			return "188", true
		case "filipino", "fil":
			return "189", true
		case "bissa", "bib":
			return "190", true
		case "romani", "rom":
			return "191", true
		}
	}

	// Base language map
	switch val {
	case "abkhazian", "abk", "ab":
		return "1", true
	case "afar", "aar", "aa":
		return "2", true
	case "afrikaans", "afr", "af":
		return "3", true
	case "akan", "aka", "ak":
		return "4", true
	case "albanian", "sqi", "sq":
		return "5", true
	case "amharic", "amh", "am":
		return "6", true
	case "arabic", "ara", "ar":
		return "7", true
	case "aragonese", "arg", "an":
		return "8", true
	case "armenian", "hye", "hy":
		return "9", true
	case "assamese", "asm", "as":
		return "10", true
	case "avaric", "ava", "av":
		return "11", true
	case "avestan", "ave", "ae":
		return "12", true
	case "aymara", "aym", "ay":
		return "13", true
	case "azerbaijani", "aze", "az":
		return "14", true
	case "bambara", "bam", "bm":
		return "15", true
	case "bashkir", "bak", "ba":
		return "16", true
	case "basque", "eus", "eu":
		return "17", true
	case "belarusian", "bel", "be":
		return "18", true
	case "bengali", "ben", "bn":
		return "19", true
	case "bihari languages", "bih", "bh":
		return "20", true
	case "bislama", "bis", "bi":
		return "21", true
	case "bokmål, norwegian", "nob", "nb":
		return "22", true
	case "bosnian", "bos", "bs":
		return "23", true
	case "breton", "bre", "br":
		return "24", true
	case "bulgarian", "bul", "bg":
		return "25", true
	case "burmese", "mya", "my":
		return "26", true
	case "cantonese", "yue":
		return "27", true
	case "catalan", "cat", "ca":
		return "28", true
	case "central khmer", "khm", "km":
		return "29", true
	case "chamorro", "cha", "ch":
		return "30", true
	case "chechen", "che", "ce":
		return "31", true
	case "chichewa", "nya", "ny":
		return "32", true
	case "chinese", "zho", "zh":
		return "33", true
	case "church slavic", "chu", "cu":
		return "34", true
	case "chuvash", "chv", "cv":
		return "35", true
	case "cornish", "cor", "kw":
		return "36", true
	case "corsican", "cos", "co":
		return "37", true
	case "cree", "cre", "cr":
		return "38", true
	case "croatian", "hrv", "hr":
		return "39", true
	case "czech", "ces", "cs":
		return "40", true
	case "danish", "dan", "da":
		return "41", true
	case "dhivehi", "div", "dv":
		return "42", true
	case "dutch", "nld", "nl":
		return "43", true
	case "dzongkha", "dzo", "dz":
		return "44", true
	case "english", "eng", "en":
		return "45", true
	case "esperanto", "epo", "eo":
		return "46", true
	case "estonian", "est", "et":
		return "47", true
	case "ewe", "ee":
		return "48", true
	case "faroese", "fao", "fo":
		return "49", true
	case "fijian", "fij", "fj":
		return "50", true
	case "finnish", "fin", "fi":
		return "51", true
	case "french", "fra", "fr":
		return "52", true
	case "fulah", "ful", "ff":
		return "53", true
	case "gaelic", "gla", "gd":
		return "54", true
	case "galician", "glg", "gl":
		return "55", true
	case "ganda", "lug", "lg":
		return "56", true
	case "georgian", "kat", "ka":
		return "57", true
	case "german", "deu", "de":
		return "58", true
	case "greek", "ell", "el":
		return "59", true
	case "guarani", "grn", "gn":
		return "60", true
	case "gujarati", "guj", "gu":
		return "61", true
	case "haitian", "hat", "ht":
		return "62", true
	case "hausa", "hau", "ha":
		return "63", true
	case "hebrew", "heb", "he":
		return "64", true
	case "herero", "her", "hz":
		return "65", true
	case "hindi", "hin", "hi":
		return "66", true
	case "hiri motu", "hmo", "ho":
		return "67", true
	case "hungarian", "hun", "hu":
		return "68", true
	case "icelandic", "isl", "is":
		return "69", true
	case "ido", "io":
		return "70", true
	case "igbo", "ibo", "ig":
		return "71", true
	case "indonesian", "ind", "id":
		return "72", true
	case "interlingua", "ina", "ia":
		return "73", true
	case "interlingue", "ile", "ie":
		return "74", true
	case "inuktitut", "iku", "iu":
		return "75", true
	case "inupiaq", "ipk", "ik":
		return "76", true
	case "irish", "gle", "ga":
		return "77", true
	case "italian", "ita", "it":
		return "78", true
	case "japanese", "jpn", "ja":
		return "79", true
	case "javanese", "jav", "jv":
		return "80", true
	case "kalaallisut", "kal", "kl":
		return "81", true
	case "kannada", "kan", "kn":
		return "82", true
	case "kanuri", "kau", "kr":
		return "83", true
	case "kashmiri", "kas", "ks":
		return "84", true
	case "kazakh", "kaz", "kk":
		return "85", true
	case "kikuyu", "kik", "ki":
		return "86", true
	case "kinyarwanda", "kin", "rw":
		return "87", true
	case "kirghiz", "kir", "ky":
		return "88", true
	case "komi", "kom", "kv":
		return "89", true
	case "kongo", "kon", "kg":
		return "90", true
	case "korean", "kor", "ko":
		return "91", true
	case "kuanyama", "kua", "kj":
		return "92", true
	case "kurdish", "kur", "ku":
		return "93", true
	case "lao", "lo":
		return "94", true
	case "latin", "lat", "la":
		return "95", true
	case "latvian", "lav", "lv":
		return "96", true
	case "limburgan", "lim", "li":
		return "97", true
	case "lingala", "lin", "ln":
		return "98", true
	case "lithuanian", "lit", "lt":
		return "99", true
	case "luba-katanga", "lub", "lu":
		return "100", true
	case "luxembourgish", "ltz", "lb":
		return "101", true
	case "macedonian", "mkd", "mk":
		return "102", true
	case "malagasy", "mlg", "mg":
		return "103", true
	case "malay", "msa", "ms":
		return "104", true
	case "malayalam", "mal", "ml":
		return "105", true
	case "maltese", "mlt", "mt":
		return "106", true
	case "mandarin", "cmn":
		return "107", true
	case "manx", "glv", "gv":
		return "108", true
	case "maori", "mri", "mi":
		return "109", true
	case "marathi", "mar", "mr":
		return "110", true
	case "marshallese", "mah", "mh":
		return "111", true
	case "mongolian", "mon", "mn":
		return "112", true
	case "nauru", "nau", "na":
		return "113", true
	case "navajo", "nav", "nv":
		return "114", true
	case "ndebele, north", "nde", "nd":
		return "115", true
	case "ndebele, south", "nbl", "nr":
		return "116", true
	case "ndonga", "ndo", "ng":
		return "117", true
	case "nepali", "nep", "ne":
		return "118", true
	case "northern sami", "sme", "se":
		return "119", true
	case "norwegian", "nor", "no":
		return "120", true
	case "norwegian nynorsk", "nno", "nn":
		return "121", true
	case "occitan (post 1500)", "oci", "oc":
		return "122", true
	case "ojibwa", "oji", "oj":
		return "123", true
	case "oriya", "ori", "or":
		return "124", true
	case "oromo", "orm", "om":
		return "125", true
	case "ossetian", "oss", "os":
		return "126", true
	case "pali", "pli", "pi":
		return "127", true
	case "panjabi", "pan", "pa":
		return "128", true
	case "persian", "fas", "fa":
		return "129", true
	case "polish", "pol", "pl":
		return "130", true
	case "portuguese", "por", "pt":
		return "131", true
	case "pushto", "pus", "ps":
		return "132", true
	case "quechua", "que", "qu":
		return "133", true
	case "romanian", "ron", "ro":
		return "134", true
	case "romansh", "roh", "rm":
		return "135", true
	case "rundi", "run", "rn":
		return "136", true
	case "russian", "rus", "ru":
		return "137", true
	case "samoan", "smo", "sm":
		return "138", true
	case "sango", "sag", "sg":
		return "139", true
	case "sanskrit", "san", "sa":
		return "140", true
	case "sardinian", "srd", "sc":
		return "141", true
	case "serbian", "srp", "sr":
		return "142", true
	case "shona", "sna", "sn":
		return "143", true
	case "sichuan yi", "iii", "ii":
		return "144", true
	case "sindhi", "snd", "sd":
		return "145", true
	case "sinhala", "sin", "si":
		return "146", true
	case "slovak", "slk", "sk":
		return "147", true
	case "slovenian", "slv", "sl":
		return "148", true
	case "somali", "som", "so":
		return "149", true
	case "sotho, southern", "sot", "st":
		return "150", true
	case "spanish", "spa", "es":
		return "151", true
	case "sundanese", "sun", "su":
		return "152", true
	case "swahili", "swa", "sw":
		return "153", true
	case "swati", "ssw", "ss":
		return "154", true
	case "swedish", "swe", "sv":
		return "155", true
	case "tagalog", "tgl", "tl":
		return "156", true
	case "tahitian", "tah", "ty":
		return "157", true
	case "tajik", "tgk", "tg":
		return "158", true
	case "tamil", "tam", "ta":
		return "159", true
	case "tatar", "tat", "tt":
		return "160", true
	case "telugu", "tel", "te":
		return "161", true
	case "thai", "tha", "th":
		return "162", true
	case "tibetan", "bod", "bo":
		return "163", true
	case "tigrinya", "tir", "ti":
		return "164", true
	case "tongan", "ton", "to":
		return "165", true
	case "tsonga", "tso", "ts":
		return "166", true
	case "tswana", "tsn", "tn":
		return "167", true
	case "turkish", "tur", "tr":
		return "168", true
	case "turkmen", "tuk", "tk":
		return "169", true
	case "twi", "tw":
		return "170", true
	case "uighur", "uig", "ug":
		return "171", true
	case "ukrainian", "ukr", "uk":
		return "172", true
	case "urdu", "urd", "ur":
		return "173", true
	case "uzbek", "uzb", "uz":
		return "174", true
	case "venda", "ven", "ve":
		return "175", true
	case "vietnamese", "vie", "vi":
		return "176", true
	case "volapük", "vol", "vo":
		return "177", true
	case "walloon", "wln", "wa":
		return "178", true
	case "welsh", "cym", "cy":
		return "179", true
	case "western frisian", "fry", "fy":
		return "180", true
	case "wolof", "wol", "wo":
		return "181", true
	case "xhosa", "xho", "xh":
		return "182", true
	case "yiddish", "yid", "yi":
		return "183", true
	case "yoruba", "yor", "yo":
		return "184", true
	case "zhuang", "zha", "za":
		return "185", true
	case "zulu", "zul", "zu":
		return "186", true
	}

	return "", false
}
