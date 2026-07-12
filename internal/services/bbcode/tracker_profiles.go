// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bbcode

import (
	"regexp"
	"strings"
)

var (
	ffURLSizedImagePattern = regexp.MustCompile(`(?i)\[url=(?P<href>[^\]]+)\]\[img=(?P<width>\d+)\](?P<src>[^\[]+)\[/img\]\[/url\]`)
	ffURLImagePattern      = regexp.MustCompile(`(?i)\[url=(?P<href>[^\]]+)\]\[img\](?P<src>[^\[]+)\[/img\]\[/url\]`)
	ffSizedImagePattern    = regexp.MustCompile(`(?i)\[img=(?P<width>\d+)\](?P<src>[^\[]+)\[/img\]`)
	tlCodeTagPattern       = regexp.MustCompile(`(?is)\[c\](.*?)\[/c\]`)
	tlImageTagPattern      = regexp.MustCompile(`(?i)\[img=[\d"x]+\]`)
	ptsSceneNFOPattern     = regexp.MustCompile(`(?is)\[center\]\[spoiler=.*? nfo:\]\[code\].*?\[/code\]\[/spoiler\]\[/center\]`)
	thrSceneNFOPattern     = regexp.MustCompile(`(?is)(\[hide=(?:Scene|FraMeSToR) NFO:\]\[pre\])(.*?)(\[/pre\]\[/hide\])`)
)

func FinalizeTrackerDescription(tracker string, desc string) string {
	trimmed := strings.TrimSpace(normalizeNewlines(desc))
	if trimmed == "" {
		return ""
	}
	switch strings.ToUpper(strings.TrimSpace(tracker)) {
	case "ANT":
		return finalizeANTDescription(trimmed)
	case "BJS":
		return finalizeBJSDescription(trimmed)
	case "BT":
		return finalizeBTDescription(trimmed)
	case "DC":
		return finalizeDCDescription(trimmed)
	case "FF":
		return finalizeFFDescription(trimmed)
	case "FL":
		return finalizeFLDescription(trimmed)
	case "FLD":
		return finalizeFLDDescription(trimmed)
	case "GPW":
		return finalizeGPWDescription(trimmed)
	case "HDS":
		return finalizeHDSDescription(trimmed)
	case "HDT":
		return finalizeHDTDescription(trimmed)
	case "IS":
		return finalizeISDescription(trimmed)
	case "PTS":
		return finalizePTSDescription(trimmed)
	case "SPD":
		return finalizeSPDDescription(trimmed)
	case "THR":
		return finalizeTHRDescription(trimmed)
	case "TL":
		return finalizeTLDescription(trimmed)
	case "TVC":
		return finalizeTVCDescription(trimmed)
	default:
		return trimmed
	}
}

func finalizeANTDescription(value string) string {
	value = convertToAlign(value)
	value = removeImgResize(value)
	value = removeSup(value)
	value = removeSub(value)
	value = removeList(value)
	return removeExtraLines(value)
}

func finalizeBJSDescription(value string) string {
	value = convertNamedSpoilerToNamedHide(value)
	value = convertSpoilerToHide(value)
	value = removeImgResize(value)
	value = convertToAlign(value)
	value = removeList(value)
	return removeExtraLines(value)
}

func finalizeBTDescription(value string) string {
	value = removeImgResize(value)
	value = removeList(value)
	return removeExtraLines(value)
}

func finalizeDCDescription(value string) string {
	value = removeSup(value)
	value = removeSub(value)
	value = convertNamedSpoilerToNormalSpoiler(value)
	value = convertComparisonToCentered(value, 1000)
	value = removeList(value)
	return removeExtraLines(value)
}

func finalizeFFDescription(value string) string {
	replacer := strings.NewReplacer(
		"[user]", "", "[/user]", "",
		"[align=left]", "", "[/align]", "",
		"[right]", "", "[/right]", "",
		"[align=right]", "", "[/align]", "",
		"[alert]", "", "[/alert]", "",
		"[note]", "", "[/note]", "",
		"[hr]", "", "[/hr]", "",
		"[h1]", "[u][b]", "[/h1]", "[/b][/u]",
		"[h2]", "[u][b]", "[/h2]", "[/b][/u]",
		"[h3]", "[u][b]", "[/h3]", "[/b][/u]",
		"[ul]", "", "[/ul]", "",
		"[ol]", "", "[/ol]", "",
		"[hide]", "", "[/hide]", "",
		"•", "-", "“", `"`, "”", `"`,
	)
	value = replacer.Replace(value)
	value = removeSub(value)
	value = removeSup(value)
	value = convertComparisonToCentered(value, 1000)
	value = removeSpoiler(value)
	value = ffURLSizedImagePattern.ReplaceAllString(value, `<a href="$href" target="_blank"><img src="$src" width="$width"></a>`)
	value = ffURLImagePattern.ReplaceAllString(value, `<a href="$href" target="_blank"><img src="$src" width="220"></a>`)
	value = ffSizedImagePattern.ReplaceAllString(value, `<img src="$src" width="$width">`)
	return removeExtraLines(value)
}

func finalizeFLDescription(value string) string {
	value = removeSpoiler(value)
	value = convertCodeToQuote(value)
	value = convertComparisonToCentered(value, 900)
	value = removeImgResize(value)
	return removeExtraLines(value)
}

func finalizeFLDDescription(value string) string {
	value = strings.ReplaceAll(value, "[user]", "")
	value = strings.ReplaceAll(value, "[/user]", "")
	value = strings.ReplaceAll(value, "[img]", "[img width=300]")
	return removeExtraLines(value)
}

func finalizeGPWDescription(value string) string {
	value = removeSup(value)
	value = removeSub(value)
	value = convertToAlign(value)
	value = removeList(value)
	return removeExtraLines(value)
}

func finalizeHDSDescription(value string) string {
	replacer := strings.NewReplacer(
		"[user]", "", "[/user]", "",
		"[align=left]", "", "[/align]", "",
		"[right]", "", "[/right]", "",
		"[align=right]", "", "[/align]", "",
		"[alert]", "", "[/alert]", "",
		"[note]", "", "[/note]", "",
		"[hr]", "", "[/hr]", "",
		"[h1]", "[u][b]", "[/h1]", "[/b][/u]",
		"[h2]", "[u][b]", "[/h2]", "[/b][/u]",
		"[h3]", "[u][b]", "[/h3]", "[/b][/u]",
		"[ul]", "", "[/ul]", "",
		"[ol]", "", "[/ol]", "",
	)
	value = replacer.Replace(value)
	value = removeSub(value)
	value = removeSup(value)
	value = removeHide(value)
	value = removeImgResize(value)
	value = convertComparisonToCentered(value, 1000)
	value = removeSpoiler(value)
	value = removeColor(value)
	return removeExtraLines(value)
}

func finalizeHDTDescription(value string) string {
	replacer := strings.NewReplacer(
		"[user]", "", "[/user]", "",
		"[align=left]", "", "[/align]", "",
		"[align=right]", "", "[/align]", "",
		"[alert]", "", "[/alert]", "",
		"[note]", "", "[/note]", "",
		"[hr]", "", "[/hr]", "",
		"[h1]", "[u][b]", "[/h1]", "[/b][/u]",
		"[h2]", "[u][b]", "[/h2]", "[/b][/u]",
		"[h3]", "[u][b]", "[/h3]", "[/b][/u]",
		"[ul]", "", "[/ul]", "",
		"[ol]", "", "[/ol]", "",
	)
	value = replacer.Replace(value)
	value = removeSub(value)
	value = removeSup(value)
	value = convertSpoilerToHide(value)
	value = removeImgResize(value)
	value = convertComparisonToCentered(value, 1000)
	value = removeSpoiler(value)
	value = removeList(value)
	return removeExtraLines(value)
}

func finalizeISDescription(value string) string {
	return removeExtraLines(value)
}

func finalizePTSDescription(value string) string {
	replacer := strings.NewReplacer(
		"[user]", "", "[/user]", "",
		"[align=left]", "", "[/align]", "",
		"[right]", "", "[/right]", "",
		"[align=right]", "", "[/align=right]", "",
		"[sup]", "", "[/sup]", "",
		"[sub]", "", "[/sub]", "",
		"[alert]", "", "[/alert]", "",
		"[note]", "", "[/note]", "",
		"[hr]", "", "[/hr]", "",
		"[h1]", "[u][b]", "[/h1]", "[/b][/u]",
		"[h2]", "[u][b]", "[/h2]", "[/b][/u]",
		"[h3]", "[u][b]", "[/h3]", "[/b][/u]",
		"[ul]", "", "[/ul]", "",
		"[ol]", "", "[/ol]", "",
		"[hide]", "", "[/hide]", "",
	)
	value = replacer.Replace(value)
	value = ptsSceneNFOPattern.ReplaceAllString(value, "")
	value = convertComparisonToCentered(value, 1000)
	value = removeSpoiler(value)
	return removeExtraLines(value)
}

func finalizeSPDDescription(value string) string {
	value = removeImgResize(value)
	value = convertNamedSpoilerToNormalSpoiler(value)
	replacer := strings.NewReplacer("[note]", "Note: ", "[/note]", "", "[code]", "", "[/code]", "", "[*]", "• ")
	value = replacer.Replace(value)
	value = removeSpoiler(value)
	value = removeList(value)
	return removeExtraLines(value)
}

func finalizeTHRDescription(value string) string {
	value = convertNamedSpoilerToNamedHide(value)
	value = convertSpoilerToHide(value)
	value = convertCodeToPre(value)
	value = thrSceneNFOPattern.ReplaceAllString(value, `$1[align=left]$2[/align]$3`)
	return removeExtraLines(value)
}

func finalizeTLDescription(value string) string {
	value = strings.ReplaceAll(value, "[center]", "<center>")
	value = strings.ReplaceAll(value, "[/center]", "</center>")
	value = regexp.MustCompile(`(?i)\[\*\]`).ReplaceAllString(value, "\n[*]")
	value = tlCodeTagPattern.ReplaceAllString(value, "[code]$1[/code]")
	value = regexp.MustCompile(`(?i)\[hr\]`).ReplaceAllString(value, "---")
	value = tlImageTagPattern.ReplaceAllString(value, "[img]")
	replacer := strings.NewReplacer("[*] ", "• ", "[*]", "• ", "[note]", "Note: ", "[/note]", "", "[code]", "", "[/code]", "")
	value = replacer.Replace(value)
	value = removeList(value)
	value = convertComparisonToCentered(value, 1000)
	value = removeSpoiler(value)
	return removeExtraLines(value)
}

func finalizeTVCDescription(value string) string {
	value = convertPreToCode(value)
	value = convertHideToSpoiler(value)
	value = convertComparisonToCollapse(value, 1000)
	return removeExtraLines(value)
}
