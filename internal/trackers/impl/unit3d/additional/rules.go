// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package additional

import "strings"

var (
	languagesEnglish = []string{"english", "en", "eng"}
	languagesFrench  = []string{"french", "fr", "fra", "fre"}
	languagesSpanish = []string{"spanish", "es", "spa"}
	languagesNordic  = []string{
		"english",
		"norwegian",
		"norsk",
		"no",
		"nb",
		"nn",
		"swedish",
		"sv",
		"danish",
		"da",
		"finnish",
		"fi",
		"icelandic",
		"is",
	}
)

var trackerRuleFactories = map[string]func() RuleSet{
	"AITHER": rulesAITHER,
	"ANT":    rulesANT,
	"A4K":    rulesA4K,
	"BHD":    rulesBHD,
	"BLU":    rulesBLU,
	"DP":     rulesDP,
	"HHD":    rulesHHD,
	"LST":    rulesLST,
	"LUME":   rulesLUME,
	"OE":     rulesOE,
	"OTW":    rulesOTW,
	"RAS":    rulesRAS,
	"RF":     rulesRF,
	"SHRI":   rulesSHRI,
	"SP":     rulesSP,
	"STC":    rulesSTC,
	"TIK":    rulesTIK,
	"TOS":    rulesTOS,
	"TTR":    rulesTTR,
	"ULCX":   rulesULCX,
	"NBL":    rulesNBL,
	"ZNTH":   rulesZNTH,
}

func RulesFor(tracker string) (RuleSet, bool) {
	key := strings.ToUpper(strings.TrimSpace(tracker))
	factory, ok := trackerRuleFactories[key]
	if !ok {
		return RuleSet{}, false
	}
	return factory(), true
}

func nonDiscEnglishAudioSubsRule() *LanguageRule {
	return &LanguageRule{
		Languages:      languagesEnglish,
		RequireAudio:   true,
		RequireSubs:    true,
		AllowOriginal:  true,
		ApplyIfNonDisc: true,
	}
}

func rulesAITHER() RuleSet {
	return RuleSet{
		RequireUniqueID: true,
		Language:        nonDiscEnglishAudioSubsRule(),
	}
}

func rulesANT() RuleSet {
	return RuleSet{RequireMovieOnly: true}
}

func rulesA4K() RuleSet {
	return RuleSet{Language: nonDiscEnglishAudioSubsRule()}
}

func rulesBHD() RuleSet {
	return RuleSet{
		RequireValidMISetting: true,
		ExtraCheck:            checkBHDRequirements,
	}
}

func rulesBLU() RuleSet {
	return RuleSet{ExtraCheck: checkBLUContainer}
}

func rulesHHD() RuleSet {
	return RuleSet{BlockDVDRip: true}
}

func rulesLST() RuleSet {
	return RuleSet{
		RequireValidMISetting: true,
		Language:              nonDiscEnglishAudioSubsRule(),
	}
}

func rulesTIK() RuleSet {
	return RuleSet{RequireDiscOnly: true}
}

func rulesNBL() RuleSet {
	return RuleSet{
		RequireTVOnly: true,
		Language: &LanguageRule{
			Languages:      languagesEnglish,
			RequireAudio:   true,
			RequireSubs:    true,
			AllowOriginal:  true,
			ApplyIfNonBDMV: true,
		},
	}
}

func rulesDP() RuleSet {
	return RuleSet{
		BlockSingleFileFolder: true,
		BlockHardcodedSubs:    true,
		BlockGroupUnlessType:  map[string][]string{"EVO": {"WEBDL"}},
		Language: &LanguageRule{
			Languages:    languagesNordic,
			RequireAudio: true,
			RequireSubs:  true,
		},
	}
}

func rulesLUME() RuleSet {
	return RuleSet{
		RequireValidMISetting: true,
		BlockAdult:            true,
		AdultMessage:          "Porn is not allowed on LUME.",
		Language: &LanguageRule{
			Languages:      languagesEnglish,
			RequireAudio:   true,
			RequireSubs:    true,
			AllowOriginal:  true,
			ApplyIfNonDisc: true,
		},
		ExtraCheck: checkLUMERequirements,
	}
}

func rulesOE() RuleSet {
	return RuleSet{
		BlockAdult:   true,
		AdultMessage: "Porn is not allowed",
		Language: &LanguageRule{
			Languages:      languagesEnglish,
			RequireAudio:   true,
			RequireSubs:    true,
			ApplyIfNonDisc: true,
		},
	}
}

func rulesOTW() RuleSet {
	return RuleSet{ExtraCheck: checkOTWGenres}
}

func rulesRAS() RuleSet {
	return RuleSet{
		Language: &LanguageRule{
			Languages:    languagesNordic,
			RequireAudio: true,
			RequireSubs:  true,
		},
	}
}

func rulesRF() RuleSet {
	return RuleSet{
		BlockAdult:       true,
		AdultMessage:     "Porn is not allowed",
		RequireMovieOnly: true,
	}
}

func rulesSHRI() RuleSet {
	return RuleSet{ExtraCheck: checkSHRIRegion}
}

func rulesSP() RuleSet {
	return RuleSet{
		BlockAdult:    true,
		AdultMessage:  "Porn is not allowed",
		MinResolution: "1080p",
	}
}

func rulesSTC() RuleSet {
	return RuleSet{
		BlockAdult:    true,
		AdultMessage:  "Porn is not allowed",
		RequireTVOnly: true,
	}
}

func rulesTOS() RuleSet {
	return RuleSet{
		Language: &LanguageRule{
			Languages:     languagesFrench,
			RequireAudio:  true,
			RequireSubs:   true,
			AllowOriginal: true,
		},
		RequireSceneNFO: true,
	}
}

func rulesTTR() RuleSet {
	return RuleSet{
		Language: &LanguageRule{
			Languages:    languagesSpanish,
			RequireAudio: true,
			RequireSubs:  true,
		},
		ExtraCheck: checkTTRSubtitleOnly,
	}
}

func rulesULCX() RuleSet {
	return RuleSet{
		RequireValidMISetting: true,
		BlockDVDRip:           true,
		Language: &LanguageRule{
			Languages:      languagesEnglish,
			RequireAudio:   true,
			RequireSubs:    true,
			ApplyIfNonDisc: true,
		},
		ExtraCheck: checkULCXRules,
	}
}

func rulesZNTH() RuleSet {
	return RuleSet{
		BlockAdult:   true,
		AdultMessage: "Porn/xxx is not allowed at ZNTH.",
	}
}
