// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveType(meta api.UploadSubject) string {
	category := strings.ToLower(strings.TrimSpace(string(meta.Identity.Category)))
	if meta.ProviderMetadata.IMDB != nil {
		imdbType := strings.ToLower(strings.TrimSpace(meta.ProviderMetadata.IMDB.Type))
		switch {
		case strings.Contains(imdbType, "concert"):
			return "Live Performance"
		case strings.Contains(imdbType, "short"):
			return "Short Film"
		case strings.Contains(imdbType, "mini series"), strings.Contains(imdbType, "miniseries"):
			return "Miniseries"
		case strings.Contains(imdbType, "stand-up"), strings.Contains(imdbType, "stand up"):
			return "Stand-up Comedy"
		}
	}
	if meta.ProviderMetadata.TMDB != nil {
		keywords := strings.ToLower(meta.ProviderMetadata.TMDB.Keywords)
		switch {
		case strings.Contains(keywords, "concert"):
			return "Live Performance"
		case strings.Contains(keywords, "stand-up comedy"), strings.Contains(keywords, "stand up comedy"):
			return "Stand-up Comedy"
		case strings.Contains(keywords, "miniseries"), strings.Contains(keywords, "mini-series"):
			return "Miniseries"
		case strings.Contains(keywords, "short film"):
			return "Short Film"
		}
	}
	if category == "movie" {
		return "Feature Film"
	}
	if category == "tv" {
		if meta.TVPack {
			return "Miniseries"
		}
		return "Short Film"
	}
	return "Feature Film"
}

func resolveCodec(meta api.UploadSubject) string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		switch {
		case meta.SourceSize <= 0:
			return "BD50"
		case meta.SourceSize <= 2328*(1<<30)/100:
			return "BD25"
		case meta.SourceSize <= 4657*(1<<30)/100:
			return "BD50"
		case meta.SourceSize <= 6147*(1<<30)/100:
			return "BD66"
		default:
			return "BD100"
		}
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		if meta.SourceSize > 0 && meta.SourceSize <= 437*(1<<30)/100 {
			return "DVD5"
		}
		return "DVD9"
	}
	codec := strings.TrimSpace(meta.VideoCodec)
	if codec == "" {
		codec = strings.TrimSpace(meta.VideoEncode)
	}
	replacer := strings.NewReplacer("AVC", "H.264", "HEVC", "H.265")
	codec = replacer.Replace(codec)
	if meta.HasEncodeSettings {
		codec = strings.ReplaceAll(codec, "H.", "x")
	}
	if codec == "" {
		return "Other"
	}
	return codec
}

func resolveResolution(meta api.UploadSubject) (string, string, string) {
	resolution := strings.TrimSpace(meta.Release.Resolution)
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		source := strings.TrimSuffix(strings.ToUpper(strings.TrimSpace(meta.Source)), " DVD")
		if source == "NTSC" || source == "PAL" {
			return source, "", ""
		}
	}
	switch strings.ToLower(resolution) {
	case "ntsc":
		return "NTSC", "", ""
	case "pal":
		return "PAL", "", ""
	case "480p", "576p", "720p", "1080i", "1080p", "2160p":
		return strings.ToLower(resolution), "", ""
	}
	if width, height, ok := ptpOtherResolution(resolution); ok {
		return "Other", width, height
	}
	return "Other", "", ""
}

func ptpOtherResolution(resolution string) (string, string, bool) {
	if dimensions, ok := map[string][2]string{
		"480i":  {"720", "480"},
		"540p":  {"960", "540"},
		"576i":  {"720", "576"},
		"1440p": {"2560", "1440"},
		"4320p": {"7680", "4320"},
		"8640p": {"15360", "8640"},
	}[strings.ToLower(strings.TrimSpace(resolution))]; ok {
		return dimensions[0], dimensions[1], true
	}
	width, height, ok := strings.Cut(strings.ToLower(strings.TrimSpace(resolution)), "x")
	if !ok {
		return "", "", false
	}
	parsedWidth, widthErr := strconv.Atoi(width)
	parsedHeight, heightErr := strconv.Atoi(height)
	if widthErr != nil || heightErr != nil || parsedWidth <= 0 || parsedHeight <= 0 {
		return "", "", false
	}
	return strconv.Itoa(parsedWidth), strconv.Itoa(parsedHeight), true
}

func resolveContainer(meta api.UploadSubject) string {
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "BDMV":
		return "m2ts"
	case "DVD":
		return "VOB IFO"
	default:
		switch strings.ToLower(strings.TrimPrefix(strings.TrimSpace(meta.Container), ".")) {
		case "mkv":
			return "MKV"
		case "mp4":
			return "MP4"
		default:
			return "Other"
		}
	}
}

func resolveSource(source string) string {
	switch strings.ToUpper(strings.TrimSpace(source)) {
	case "BLU-RAY", "BLURAY":
		return "Blu-ray"
	case "HD DVD", "HDDVD", "HD-DVD":
		return "HD-DVD"
	case "WEB", "WEB-DL", "WEBDL":
		return "WEB"
	case "HDTV", "UHDTV":
		return "HDTV"
	case "NTSC", "PAL", "DVD":
		return "DVD"
	case "TV":
		return "TV"
	case "VHS":
		return "VHS"
	default:
		return "Other"
	}
}

func resolveSubtitles(meta api.UploadSubject) []int {
	ids := make([]int, 0, len(meta.SubtitleLanguages)+len(meta.HardcodedSubtitleLanguages))
	add := func(value int) {
		if !slices.Contains(ids, value) {
			ids = append(ids, value)
		}
	}
	for _, language := range meta.SubtitleLanguages {
		if value, ok := subtitleID(language); ok {
			add(value)
		}
	}
	for _, language := range meta.HardcodedSubtitleLanguages {
		covered := false
		for _, detail := range meta.HardcodedSubtitleCoverage {
			if detail.Language != language {
				continue
			}
			covered = true
			if ptpEnglishLanguage(language) && detail.Coverage == api.SubtitleCoverageForced {
				add(50)
			} else if value, ok := subtitleID(language); ok {
				add(value)
			}
		}
		if !covered {
			if value, ok := subtitleID(language); ok {
				add(value)
			}
		}
	}
	if len(ids) == 0 {
		return []int{44}
	}
	return ids
}

func resolveTags(meta api.UploadSubject) string {
	values := make([]string, 0, 8)
	var genreText string
	switch {
	case meta.EffectiveMetadata.GenresProvenance.IsManual():
		genreText = trackers.PreferredGenreText(meta, "")
	case meta.ProviderMetadata.TMDB != nil:
		genreText = meta.ProviderMetadata.TMDB.Genres
	default:
		genreText = meta.Release.Genre
	}
	for item := range strings.SplitSeq(genreText, ",") {
		if tag := ptpTag(item); tag != "" {
			values = append(values, tag)
		}
	}
	if len(values) == 0 && !meta.EffectiveMetadata.GenresProvenance.IsManual() {
		for item := range strings.SplitSeq(meta.Release.Genre, ",") {
			if tag := ptpTag(item); tag != "" {
				values = append(values, tag)
			}
		}
	}
	seen := make(map[string]struct{}, len(values))
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		filtered = append(filtered, value)
	}
	return strings.Join(filtered, ", ")
}

func ptpTag(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "science fiction", "sci-fi", "sci fi":
		return "sci.fi"
	case "martial arts":
		return "martial.arts"
	case "film noir":
		return "film.noir"
	case "music":
		return "musical"
	case "war & politics", "war and politics":
		return "war"
	}
	for _, allowed := range []string{
		"action", "adventure", "animation", "arthouse", "asian", "biography", "camp", "comedy", "crime", "cult", "documentary",
		"drama", "experimental", "exploitation", "family", "fantasy", "film.noir", "history", "horror", "martial.arts", "musical", "mystery",
		"performance", "philosophy", "politics", "romance", "sci.fi", "short", "silent", "sport", "thriller", "video.art", "war", "western",
	} {
		if normalized == allowed {
			return allowed
		}
	}
	return ""
}

func resolveTrumpable(meta api.UploadSubject) []int {
	values := make([]int, 0, 2)
	if hasHardcodedSubtitles(meta) {
		values = append(values, 4)
	}
	switch strings.ToLower(strings.TrimSpace(standalone.QuestionnaireAnswers(meta, "PTP")["no_english_subtitles"])) {
	case "yes":
		return append(values, 14)
	case "no":
		return values
	}
	subtitles := append(append([]string(nil), meta.SubtitleLanguages...), meta.HardcodedSubtitleLanguages...)
	foreignAudio := len(meta.AudioLanguages) > 0 && !ptpEnglishLanguage(meta.AudioLanguages[0])
	unknownAudioWithHardcodedLanguages := len(meta.AudioLanguages) == 0 && meta.HardcodedSubs && len(meta.HardcodedSubtitleLanguages) > 0
	if (foreignAudio || unknownAudioWithHardcodedLanguages) && !ptpHasEnglishLanguage(subtitles) {
		values = append(values, 14)
	}
	return values
}

func hasHardcodedSubtitles(meta api.UploadSubject) bool {
	return meta.HardcodedSubs
}

func withHardcodedSubtitleLanguages(meta api.UploadSubject, value string) (api.UploadSubject, error) {
	if !hasHardcodedSubtitles(meta) || len(meta.HardcodedSubtitleLanguages) > 0 {
		return meta, nil
	}
	if strings.TrimSpace(value) == "" {
		return api.UploadSubject{}, errors.New("trackers: PTP hardcoded subtitle languages are required")
	}
	for language := range strings.SplitSeq(value, ",") {
		language = strings.TrimSpace(language)
		if language == "" {
			continue
		}
		if _, ok := subtitleID(language); !ok {
			return api.UploadSubject{}, fmt.Errorf("trackers: PTP unsupported hardcoded subtitle language %q", language)
		}
		meta.HardcodedSubtitleLanguages = append(meta.HardcodedSubtitleLanguages, language)
	}
	if len(meta.HardcodedSubtitleLanguages) == 0 {
		return api.UploadSubject{}, errors.New("trackers: PTP hardcoded subtitle languages are required")
	}
	return meta, nil
}

func ptpHasEnglishLanguage(values []string) bool {
	return slices.ContainsFunc(values, ptpEnglishLanguage)
}

func ptpEnglishLanguage(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	return languageutil.NormalizeLanguageCode(value) == "en" || strings.HasPrefix(normalized, "english")
}

var subtitleIDs = map[string]int{
	"arabic":               22,
	"brazilian portuguese": 49,
	"bulgarian":            29,
	"chinese":              14,
	"croatian":             23,
	"czech":                30,
	"danish":               10,
	"dutch":                9,
	"english":              3,
	"english - forced":     50,
	"english forced":       50,
	"english intertitles":  51,
	"estonian":             38,
	"finnish":              15,
	"french":               5,
	"german":               6,
	"greek":                26,
	"hebrew":               40,
	"hindi":                41,
	"hungarian":            24,
	"icelandic":            28,
	"indonesian":           47,
	"italian":              16,
	"japanese":             8,
	"korean":               19,
	"latvian":              37,
	"lithuanian":           39,
	"malay":                54,
	"norwegian":            12,
	"persian":              52,
	"polish":               17,
	"portuguese":           21,
	"romanian":             13,
	"russian":              7,
	"serbian":              31,
	"slovak":               42,
	"slovenian":            43,
	"spanish":              4,
	"swedish":              11,
	"thai":                 20,
	"turkish":              18,
	"ukrainian":            34,
	"vietnamese":           25,
	"welsh":                55,
}

// reviewedSubtitles follows the owner-supplied PTP selection and call-site
// normalization. A selected hardcoded choice is explicit tracker-local intent;
// temporary choice values never become trumpable wire IDs.
func reviewedSubtitles(meta api.UploadSubject, answers map[string]string) ([]int, []int, error) {
	if answers == nil {
		answers = standalone.QuestionnaireAnswers(meta, "PTP")
	}
	meta.TrackerQuestionnaireAnswers = map[string]map[string]string{"PTP": answers}

	legacy := strings.ToLower(strings.TrimSpace(answers["no_english_subtitles"]))
	if err := validateSubtitleReview(meta, answers, nil); err != nil {
		return nil, nil, err
	}

	if !meta.HardcodedSubs && (legacy == "yes" || legacy == "no") {
		return resolveSubtitles(meta), resolveTrumpable(meta), nil
	}
	if !requiresSubtitleReview(meta) {
		return resolveSubtitles(meta), resolveTrumpable(meta), nil
	}
	if !meta.HardcodedSubs && answers["trumpable_review"] == "no" {
		return resolveSubtitles(meta), nil, nil
	}
	selected, err := parseSubtitleReview(answers["subtitle_tags"])
	if err != nil {
		return nil, nil, err
	}
	if err := validateSubtitleReview(meta, answers, selected); err != nil {
		return nil, nil, err
	}
	subs := resolveSubtitles(meta)
	if len(selected) == 0 {
		return subs, resolveTrumpable(meta), nil
	}
	tags := make([]int, 0, 3)
	addSub := func(id int) {
		if !slices.Contains(subs, id) {
			subs = append(subs, id)
		}
		subs = slices.DeleteFunc(subs, func(v int) bool { return v == 44 })
	}
	for _, choice := range selected {
		switch choice {
		case "English Hardcoded Subs (Full)":
			tags = append(tags, 4)
			addSub(3)
		case "English Hardcoded Subs (Forced)":
			tags = append(tags, 50)
			addSub(50)
		case "No English Subs":
			tags = append(tags, 14)
		case "English Softsubs Exist (Mislabeled)":
		case "Hardcoded Subs (Non-English)":
			tags = append(tags, 15)
			languages := meta.HardcodedSubtitleLanguages
			if len(languages) == 0 {
				parsed, parseErr := withHardcodedSubtitleLanguages(api.UploadSubject{HardcodedSubs: true}, answers["hardcoded_subtitle_languages"])
				if parseErr != nil {
					return nil, nil, parseErr
				}
				languages = parsed.HardcodedSubtitleLanguages
			}
			for _, language := range languages {
				if id, ok := subtitleID(language); ok {
					addSub(id)
				}
			}
		}
	}
	if meta.HardcodedSubs || slices.Contains(tags, 4) || slices.Contains(tags, 50) || slices.Contains(tags, 15) {
		other := slices.Contains(tags, 15)
		for i, v := range tags {
			if v == 50 || v == 15 {
				tags[i] = 4
			}
		}
		if other && (len(meta.AudioLanguages) == 0 || !ptpEnglishLanguage(meta.AudioLanguages[0])) && !slices.Contains(subs, 3) && !slices.Contains(subs, 50) {
			tags = append(tags, 14)
		}
		if slices.Contains(tags, 14) {
			subs = slices.DeleteFunc(subs, func(v int) bool { return v == 44 })
		}
	}
	switch legacy {
	case "no":
		tags = slices.DeleteFunc(tags, func(value int) bool { return value == 14 })
	case "yes":
		tags = append(tags, 14)
	}
	unique := make([]int, 0, len(tags))
	for _, v := range tags {
		if !slices.Contains(unique, v) {
			unique = append(unique, v)
		}
	}
	return subs, unique, nil
}

func subtitleID(value string) (int, bool) {
	base, coverage := languageutil.SubtitleLanguageParts(value)
	if normalized := languageutil.NormalizeLanguageLabel(base); normalized != "" {
		base = normalized
	}
	if strings.EqualFold(base, "English") && coverage == "Forced" {
		return 50, true
	}
	id, ok := subtitleIDs[strings.ToLower(strings.TrimSpace(base))]
	return id, ok
}

var theatricalEditionPattern = regexp.MustCompile(`(?i)\btheatrical(?:[ ._-]+(?:cut|edition|version))?\b`)

var editionFeatureCatalogue = []struct {
	label    string
	category string
	pattern  *regexp.Regexp
}{
	{"Masters of Cinema", "Collection", regexp.MustCompile(`(?i)\bmasters[ ._-]+of[ ._-]+cinema\b`)},
	{"The Criterion Collection", "Collection", regexp.MustCompile(`(?i)\b(?:the[ ._-]+)?criterion(?:[ ._-]+collection)?\b`)},
	{"Warner Archive Collection", "Collection", regexp.MustCompile(`(?i)\bwarner[ ._-]+archive(?:[ ._-]+collection)?\b`)},
	{"Director's Cut", "Edition", regexp.MustCompile(`(?i)\bdirector'?s[ ._-]+cut\b`)},
	{"Extended Edition", "Edition", regexp.MustCompile(`(?i)\bextended(?:[ ._-]+(?:cut|edition))?\b`)},
	{"Theatrical Cut", "Edition", theatricalEditionPattern},
	{"Uncut", "Edition", regexp.MustCompile(`(?i)\buncut\b`)},
	{"Unrated", "Edition", regexp.MustCompile(`(?i)\bunrated(?:[ ._-]+cut)?\b`)},
	{"Rifftrax", "Edition", regexp.MustCompile(`(?i)\brifftrax\b`)},
	{"Remux", "Feature", regexp.MustCompile(`(?i)\bremux\b`)},
	{"DTS:X", "Feature", regexp.MustCompile(`(?i)\bdts:x\b`)},
	{"Dolby Atmos", "Feature", regexp.MustCompile(`(?i)\bdolby[ ._-]+atmos\b`)},
	{"Dual Audio", "Feature", regexp.MustCompile(`(?i)\bdual[ ._-]+audio\b`)},
	{"English Dub", "Feature", regexp.MustCompile(`(?i)\benglish[ ._-]+dub\b`)},
	{"10-bit", "Feature", regexp.MustCompile(`(?i)\b10[ ._-]?bit\b`)},
	{"Dolby Vision", "Feature", regexp.MustCompile(`(?i)\bdolby[ ._-]+vision\b`)},
	{"HDR10+", "Feature", regexp.MustCompile(`(?i)\bhdr10\+(?:$|[ ./_-])`)},
	{"HDR10", "Feature", regexp.MustCompile(`(?i)\bhdr10(?:$|[ ./_-])`)},
	{"HLG", "Feature", regexp.MustCompile(`(?i)\bhlg\b`)},
	{"With Commentary", "Feature", regexp.MustCompile(`(?i)\bwith[ ._-]+commentary\b`)},
	{"2in1", "Feature", regexp.MustCompile(`(?i)\b2in1\b`)},
	{"2D/3D Edition", "Feature", regexp.MustCompile(`(?i)\b2d[ ./_-]+3d(?:[ ._-]+edition)?\b`)},
	{"3D Anaglyph", "Feature", regexp.MustCompile(`(?i)\b(?:3d[ ._-]+)?anaglyph\b`)},
	{"3D Full SBS", "Feature", regexp.MustCompile(`(?i)\b(?:3d[ ._-]+)?f(?:ull)?[ ._-]*sbs\b`)},
	{"3D Half OU", "Feature", regexp.MustCompile(`(?i)\b(?:3d[ ._-]+)?h(?:alf)?[ ._-]*ou\b`)},
	{"3D Half SBS", "Feature", regexp.MustCompile(`(?i)\b(?:3d[ ._-]+)?h(?:alf)?[ ._-]*sbs\b`)},
	{"2-Disc Set", "Feature", regexp.MustCompile(`(?i)\b2[ ._-]*disc[ ._-]+set\b`)},
	{"4K Restoration", "Feature", regexp.MustCompile(`(?i)\b4k[ ._-]+restoration\b`)},
	{"4K Remaster", "Feature", regexp.MustCompile(`(?i)\b4k[ ._-]+remaster(?:ed)?\b`)},
	{"Extras", "Feature", regexp.MustCompile(`(?i)\b(?:digital[ ._-]+)?extras\b`)},
}

// editionFeatures maps only prepared category and technical facts. The same
// ordered catalogue drives pre-upload review and PTP's remaster_title field.
func editionFeatures(meta api.UploadSubject) []api.TrackerEditionFeature {
	selected := make(map[string]string)
	distributor := strings.TrimSpace(meta.Distributor)
	collectionSource := "Distributor: " + distributor
	if distributor == "" && !meta.EffectiveMetadata.DistributorProvenance.IsManual() {
		distributor = strings.TrimSpace(meta.Release.Collection)
		collectionSource = "Collection: " + distributor
	}
	switch strings.ToUpper(distributor) {
	case "WARNER ARCHIVE", "WARNER ARCHIVE COLLECTION", "WAC":
		selected["Warner Archive Collection"] = collectionSource
	case "CRITERION", "THE CRITERION COLLECTION", "CRITERION COLLECTION", "CRITERION.COLLECTION", "CC":
		selected["The Criterion Collection"] = collectionSource
	case "MASTERS OF CINEMA", "MOC":
		selected["Masters of Cinema"] = collectionSource
	}
	for _, feature := range meta.ReleaseFeatures {
		var label string
		switch feature {
		case api.ReleaseFeatureTwoDiscSet:
			label = "2-Disc Set"
		case api.ReleaseFeature4KRestoration:
			label = "4K Restoration"
		case api.ReleaseFeature4KRemaster:
			label = "4K Remaster"
		case api.ReleaseFeatureExtras:
			label = "Extras"
		case api.ReleaseFeature2D3DEdition:
			label = "2D/3D Edition"
		case api.ReleaseFeature3DAnaglyph:
			label = "3D Anaglyph"
		case api.ReleaseFeature3DFullSBS:
			label = "3D Full SBS"
		case api.ReleaseFeature3DHalfOU:
			label = "3D Half OU"
		case api.ReleaseFeature3DHalfSBS:
			label = "3D Half SBS"
		}
		if label != "" {
			selected[label] = "Prepared feature: " + label
		}
	}
	var remaining []string
	for _, value := range []struct{ name, text string }{{"Cut", meta.Cut}, {"Edition", meta.Edition}, {"Presentation", meta.Presentation}, {"Edition set", meta.EditionSet}} {
		residue := value.text
		matched := false
		for _, option := range editionFeatureCatalogue {
			if option.pattern.MatchString(value.text) {
				matched = true
				// Commentary is controlled by the presence-aware metadata correction,
				// including explicit false; free-form edition wording cannot override it.
				if option.label != "With Commentary" {
					selected[option.label] = value.name + ": " + value.text
				}
				residue = option.pattern.ReplaceAllString(residue, "")
			}
		}
		residue = strings.TrimSpace(residue)
		if matched {
			if strings.Trim(residue, " ./_-+") == "" {
				residue = ""
			} else {
				residue = strings.Trim(strings.Join(strings.Fields(residue), " "), " ./_-")
			}
		}
		if residue != "" {
			remaining = append(remaining, residue)
		}
	}
	if selected["Theatrical Cut"] != "" {
		other := strings.Join([]string{meta.Cut, meta.Edition, meta.EditionSet}, " ")
		if strings.Trim(theatricalEditionPattern.ReplaceAllString(other, ""), " ./_-+") == "" {
			delete(selected, "Theatrical Cut")
		}
	}
	for _, fact := range []struct {
		label    string
		present  bool
		evidence string
	}{
		{"Remux", strings.EqualFold(strings.TrimSpace(meta.Type), "REMUX"), "Release type: " + meta.Type},
		{"DTS:X", strings.Contains(meta.Audio, "DTS:X"), "Audio: " + meta.Audio},
		{"Dolby Atmos", strings.Contains(meta.Audio, "Atmos"), "Audio: " + meta.Audio},
		{"Dual Audio", strings.Contains(meta.Audio, "Dual"), "Audio: " + meta.Audio},
		{"English Dub", strings.Contains(meta.Audio, "Dubbed"), "Audio: " + meta.Audio},
		{"10-bit", meta.HDR == "" && meta.BitDepth == "10", "10-bit video without HDR"},
		{"Dolby Vision", strings.Contains(meta.HDR, "DV"), "HDR: " + meta.HDR},
		{"HDR10+", strings.Contains(meta.HDR, "HDR10+"), "HDR: " + meta.HDR},
		{"HDR10", strings.Contains(meta.HDR, "HDR") && !strings.Contains(meta.HDR, "HDR10+"), "HDR: " + meta.HDR},
		{"HLG", strings.Contains(meta.HDR, "HLG"), "HDR: " + meta.HDR},
		{"With Commentary", meta.HasCommentary, "Effective commentary: true (inspected tracks or manual correction)"},
		{"2-Disc Set", len(meta.Disc.Items) == 2, fmt.Sprintf("Prepared source contains %d discs", len(meta.Disc.Items))},
	} {
		if fact.present {
			selected[fact.label] = fact.evidence
		}
	}
	options := make([]api.TrackerEditionFeature, 0, len(editionFeatureCatalogue)+1)
	for _, item := range editionFeatureCatalogue {
		evidence, present := selected[item.label]
		if !present {
			evidence = "No supporting prepared evidence"
			if item.label == "With Commentary" {
				evidence = "Effective commentary: false; the Commentary correction controls this tag"
			}
			if item.label != "With Commentary" {
				evidence += "; explicit Edition input is supported"
			}
		}
		options = append(options, api.TrackerEditionFeature{
			Label:    item.label,
			Category: item.category,
			Selected: present,
			Evidence: evidence,
		})
	}
	if label := strings.Join(remaining, " "); label != "" {
		options = append(options, api.TrackerEditionFeature{
			Label:    label,
			Category: "Edition",
			Selected: true,
			Evidence: "Additional prepared edition/presentation wording",
		})
	}
	return options
}

func resolveRemasterTitle(meta api.UploadSubject) string {
	var labels []string
	for _, option := range editionFeatures(meta) {
		if option.Selected {
			labels = append(labels, option.Label)
		}
	}
	return strings.Join(labels, " / ")
}
