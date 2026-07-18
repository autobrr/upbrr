// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bbcode

import (
	"regexp"
	"strconv"
	"strings"

	imagehost "github.com/autobrr/upbrr/internal/imagehosting/host"
)

var (
	ptpPTPURLPattern          = regexp.MustCompile(`(?i)(?:\[url(?:=|\])[^\]]*https?://passthepopcorn\.m[^\]]*\]|\bhttps?://passthepopcorn\.m[^\s]+)`)
	ptpHDBURLPattern          = regexp.MustCompile(`(?i)(\[url[=]]https?://hdbits\.o[^\]]+)([^\[]+)(\[/url\])?`)
	ptpComparePattern         = regexp.MustCompile(`(?i)\[comparison=[\s\S]*?\[/comparison\]`)
	ptpHidePattern            = regexp.MustCompile(`(?i)\[hide[\s\S]*?\[/hide\]`)
	ptpImgPattern             = regexp.MustCompile(`(?is)\[img(?:[^\]]*)?\][\s\S]*?\[/img\]`)
	ptpLooseImg               = regexp.MustCompile(`(?i)(https?://[^\s\[\]]+\.(?:png|jpg))`)
	ptpQuotePattern           = regexp.MustCompile(`(?i)\[quote.*?\]`)
	ptpAlignPattern           = regexp.MustCompile(`(?i)\[align=.*?\]`)
	ptpURLTagPattern          = regexp.MustCompile(`(?i)(\[url[=]]https?://passthepopcorn\.m[^\]]+])`)
	ptpHDBTagPattern          = regexp.MustCompile(`(?i)(\[url[=]]https?://hdbits\.o[^\]]+])`)
	ptpMediaInfoPattern       = regexp.MustCompile(`(?i)\[mediainfo\][\s\S]*?\[/mediainfo\]`)
	ptpGeneralUniquePattern   = regexp.MustCompile(`(?im)(^general\nunique)(.*?)^$`)
	ptpGeneralCompletePattern = regexp.MustCompile(`(?im)(^general\ncomplete)(.*?)^$`)
	ptpFormatPattern          = regexp.MustCompile(`(?im)(^(Format[\s]{2,}:))(.*?)^$`)
	ptpTrackIDPattern         = regexp.MustCompile(`(?im)(^(video|audio|text)( #\d+)?\nid)(.*?)^$`)
	ptpMenuPattern            = regexp.MustCompile(`(?im)(^(menu)( #\d+)?\n)(.*?)^$`)
	ptpBoldMediaPattern       = regexp.MustCompile(
		`(?is)\[b\](.*?)(Matroska|DTS|AVC|x264|Progressive|23\.976 fps|16:9|[0-9]+x[0-9]+|[0-9]+ MiB|[0-9]+ Kbps|[0-9]+ bits|cabac=.*?/ aq=.*?|\d+\.\d+ Mbps)\[/b\]`,
	)
	ptpMediaTokenPattern = regexp.MustCompile(
		`(?is)(Matroska|DTS|AVC|x264|Progressive|23\.976 fps|16:9|[0-9]+x[0-9]+|[0-9]+ MiB|[0-9]+ Kbps|[0-9]+ bits|cabac=.*?/ aq=.*?|\d+\.\d+ Mbps|[0-9]+\s+channels|[0-9]+\.[0-9]+\s+KHz|[0-9]+ KHz|[0-9]+\s+bits)`,
	)
	ptpUnderlinedFieldPattern      = regexp.MustCompile(`(?i)\[u\](Format|Bitrate|Channels|Sampling Rate|Resolution):\[/u\]\s*\d*.*?`)
	ptpNumericMediaLinePattern     = regexp.MustCompile(`(?im)^\s*\d+\s*(channels|KHz|bits)\s*$`)
	ptpWhitespaceLinePattern       = regexp.MustCompile(`(?m)^\s+$`)
	ptpMultiNewlinePattern         = regexp.MustCompile(`\n{2,}`)
	ptpEmptyCenterPattern          = regexp.MustCompile(`(?i)\[center\]\s*\[/center\]`)
	ptpSizePattern                 = regexp.MustCompile(`(?i)\[size=.*?\]`)
	ptpVideoPattern                = regexp.MustCompile(`(?i)\[video\][\s\S]*?\[/video\]`)
	ptpStaffPattern                = regexp.MustCompile(`(?i)\[staff[\s\S]*?\[/staff\]`)
	ptpDiscInfoPattern             = regexp.MustCompile(`(?i)DISC INFO:[\s\S]*?(\n\n|$)`)
	ptpDiscTitlePattern            = regexp.MustCompile(`(?i)Disc Title:[\s\S]*?(\n\n|$)`)
	ptpDiscSizePattern             = regexp.MustCompile(`(?i)Disc Size:[\s\S]*?(\n\n|$)`)
	ptpProtectionPattern           = regexp.MustCompile(`(?i)Protection:[\s\S]*?(\n\n|$)`)
	ptpBDJavaPattern               = regexp.MustCompile(`(?i)BD-Java:[\s\S]*?(\n\n|$)`)
	ptpBDInfoPattern               = regexp.MustCompile(`(?i)BDInfo:[\s\S]*?(\n\n|$)`)
	ptpPlaylistReportPattern       = regexp.MustCompile(`(?i)PLAYLIST REPORT:[\s\S]*?(\n\n|$)`)
	ptpNamePattern                 = regexp.MustCompile(`(?i)Name:[\s\S]*?(\n\n|$)`)
	ptpLengthPattern               = regexp.MustCompile(`(?i)Length:[\s\S]*?(\n\n|$)`)
	ptpSizeLinePattern             = regexp.MustCompile(`(?i)Size:[\s\S]*?(\n\n|$)`)
	ptpTotalBitratePattern         = regexp.MustCompile(`(?i)Total Bitrate:[\s\S]*?(\n\n|$)`)
	ptpVideoSectionPattern         = regexp.MustCompile(`(?i)VIDEO:[\s\S]*?(\n\n|$)`)
	ptpAudioSectionPattern         = regexp.MustCompile(`(?i)AUDIO:[\s\S]*?(\n\n|$)`)
	ptpSubtitlesSectionPattern     = regexp.MustCompile(`(?i)SUBTITLES:[\s\S]*?(\n\n|$)`)
	ptpCodecBitratePattern         = regexp.MustCompile(`(?i)Codec\s+Bitrate\s+Description[\s\S]*?(\n\n|$)`)
	ptpCodecLanguageBitratePattern = regexp.MustCompile(`(?i)Codec\s+Language\s+Bitrate\s+Description[\s\S]*?(\n\n|$)`)
	ptpBotSignature                = regexp.MustCompile(
		`(?is)(?:\[(?:center|right|align=right)\]\s*(?:\[img=\d+\]https://blutopia\.xyz/favicon\.ico\[/img\]\s*)?\[b\]?Uploaded\s+Using\s+\[url=https://github\.com/HDInnovations/UNIT3D\]UNIT3D\[/url\]\s+Auto\s+Uploader\[/b\]?(?:\s*\[img=\d+\]https://blutopia\.xyz/favicon\.ico\[/img\])?\s*\[/(?:center|right|align)\])|(?:\[center\]\s*\[url=https://github\.com/z-ink/uploadrr\]\[img=\d+\]https://i\.ibb\.co/2NVWb0c/uploadrr\.webp\[/img\]\[/url\]\s*\[/center\])|(?:\[center\]\s*\[url=https://github\.com/edge20200/Only-Uploader\]Powered\s+by\s+Only-Uploader\[/url\]\s*\[/center\])|(?:\[center\]\s*\[url=/torrents\?perPage=\d+&name=[^\]]*\]\s*\[/url\]\s*\[/center\])|(?:\[center\]\s*(?:\[b\]\s*(?:\[size=\d+\])?brush(?:\[/size\])?\s*\[/b\]\s*)?This is an internal release which was first released exclusively on Aither\.\s*Cheers to all the Aither(?:\s+users)?\s*\[/center\])|(?:\[(?:center|right|align=right)\]\s*(?:\[url=[^\]]+\]\s*)?(?:\[size=[^\]]+\]\s*)?Created by(?:\s+[^[]*?)?\s*Upload Assistant(?:\s*\[/size\])?(?:\s*\[/url\])?\s*\[/(?:center|right|align)\])`,
	)
)

// CleanPTPDescription removes tracker links, embedded MediaInfo, unsupported
// blocks, and known uploader signatures from a PTP-style description. It
// returns extracted images separately and omits a cleaned body that is blank or
// contains only BBCode tags.
func CleanPTPDescription(description string, discType string) Report {
	desc := strings.ReplaceAll(description, "&bull;", "-")
	desc = NormalizeNewlines(desc)
	desc = ptpBotSignature.ReplaceAllString(desc, "")

	urlTags := ptpPTPURLPattern.FindAllString(desc, -1)
	hdbTags := ptpHDBURLPattern.FindAllString(desc, -1)
	urlTags = append(urlTags, hdbTags...)
	for _, urlTag := range urlTags {
		cleaned := ptpURLTagPattern.ReplaceAllString(urlTag, "")
		cleaned = ptpHDBTagPattern.ReplaceAllString(cleaned, "")
		cleaned = strings.ReplaceAll(cleaned, "[/url]", "")
		desc = strings.ReplaceAll(desc, urlTag, cleaned)
	}

	desc = strings.ReplaceAll(desc, "http://passthepopcorn.me", "PTP")
	desc = strings.ReplaceAll(desc, "https://passthepopcorn.me", "PTP")
	desc = strings.ReplaceAll(desc, "http://hdbits.org", "HDB")
	desc = strings.ReplaceAll(desc, "https://hdbits.org", "HDB")

	imagelist := make([]Image, 0)
	excluded := make(map[string]struct{})

	sourceEncode := regexp.MustCompile(`(?i)\[comparison=Source, Encode\][\s\S]*`).FindAllString(desc, -1)
	sourceVs := regexp.MustCompile(`(?i)Source Vs Encode:[\s\S]*`).FindAllString(desc, -1)
	for _, block := range append(sourceEncode, sourceVs...) {
		urls := ptpLooseImg.FindAllString(block, -1)
		for _, url := range urls {
			excluded[url] = struct{}{}
		}
		desc = strings.ReplaceAll(desc, block, "")
	}

	comps := ptpComparePattern.FindAllString(desc, -1)
	hides := ptpHidePattern.FindAllString(desc, -1)
	comps = append(comps, hides...)
	nocomp := desc
	for url := range excluded {
		nocomp = strings.ReplaceAll(nocomp, url, "")
	}

	compPlaceholders := make([]string, 0, len(comps))
	for i, comp := range comps {
		nocomp = strings.ReplaceAll(nocomp, comp, "")
		placeholder := "COMPARISON_PLACEHOLDER-" + strconv.Itoa(i) + " "
		desc = strings.ReplaceAll(desc, comp, placeholder)
		compPlaceholders = append(compPlaceholders, comp)
	}

	var links []string

	switch strings.ToUpper(strings.TrimSpace(discType)) {
	case "DVD":
		desc = ptpMediaInfoPattern.ReplaceAllString(desc, "")
	case "BDMV":
		desc = ptpMediaInfoPattern.ReplaceAllString(desc, "")
		desc = ptpDiscInfoPattern.ReplaceAllString(desc, "")
		desc = ptpDiscTitlePattern.ReplaceAllString(desc, "")
		desc = ptpDiscSizePattern.ReplaceAllString(desc, "")
		desc = ptpProtectionPattern.ReplaceAllString(desc, "")
		desc = ptpBDJavaPattern.ReplaceAllString(desc, "")
		desc = ptpBDInfoPattern.ReplaceAllString(desc, "")
		desc = ptpPlaylistReportPattern.ReplaceAllString(desc, "")
		desc = ptpNamePattern.ReplaceAllString(desc, "")
		desc = ptpLengthPattern.ReplaceAllString(desc, "")
		desc = ptpSizeLinePattern.ReplaceAllString(desc, "")
		desc = ptpTotalBitratePattern.ReplaceAllString(desc, "")
		desc = ptpVideoSectionPattern.ReplaceAllString(desc, "")
		desc = ptpAudioSectionPattern.ReplaceAllString(desc, "")
		desc = ptpSubtitlesSectionPattern.ReplaceAllString(desc, "")
		desc = ptpCodecBitratePattern.ReplaceAllString(desc, "")
		desc = ptpCodecLanguageBitratePattern.ReplaceAllString(desc, "")
	default:
		desc = ptpMediaInfoPattern.ReplaceAllString(desc, "")
		desc = ptpGeneralUniquePattern.ReplaceAllString(desc, "")
		desc = ptpGeneralCompletePattern.ReplaceAllString(desc, "")
		desc = ptpFormatPattern.ReplaceAllString(desc, "")
		desc = ptpTrackIDPattern.ReplaceAllString(desc, "")
		desc = ptpMenuPattern.ReplaceAllString(desc+"\n\n", "")

		desc, links = protectLinks(desc)
		desc = ptpBoldMediaPattern.ReplaceAllString(desc, "")
		desc = ptpMediaTokenPattern.ReplaceAllString(desc, "")
		desc = ptpUnderlinedFieldPattern.ReplaceAllString(desc, "")
		desc = ptpNumericMediaLinePattern.ReplaceAllString(desc, "")
		desc = ptpWhitespaceLinePattern.ReplaceAllString(desc, "")
		desc = ptpMultiNewlinePattern.ReplaceAllString(desc, "\n")
	}

	desc = restoreLinks(desc, links)

	desc = ptpQuotePattern.ReplaceAllString(desc, "[code]")
	desc = strings.ReplaceAll(desc, "[/quote]", "[/code]")
	desc = ptpAlignPattern.ReplaceAllString(desc, "")
	desc = strings.ReplaceAll(desc, "[/align]", "")

	desc = ptpSizePattern.ReplaceAllString(desc, "")
	desc = strings.ReplaceAll(desc, "[/size]", "")
	desc = ptpVideoPattern.ReplaceAllString(desc, "")
	desc = ptpStaffPattern.ReplaceAllString(desc, "")

	for _, tag := range []string{"[movie]", "[/movie]", "[artist]", "[/artist]", "[user]", "[/user]", "[indent]", "[/indent]", "[size]", "[/size]", "[hr]"} {
		desc = strings.ReplaceAll(desc, tag, "")
	}

	desc = ptpImgPattern.ReplaceAllString(desc, "")

	looseImages := ptpLooseImg.FindAllString(nocomp, -1)
	for _, imgURL := range looseImages {
		if _, ok := excluded[imgURL]; ok {
			continue
		}
		host := imagehost.ExtractHost(imgURL)
		rawURL := NormalizeImageRawURL(imgURL)
		imagelist = append(imagelist, Image{
			ImgURL: imgURL,
			RawURL: rawURL,
			WebURL: imgURL,
			Host:   host,
		})
		desc = strings.ReplaceAll(desc, imgURL, "")
	}

	for i, comp := range compPlaceholders {
		cleanComp := regexp.MustCompile(`(?i)\[/?img[\s\S]*?\]`).ReplaceAllString(comp, "")
		desc = strings.ReplaceAll(desc, "COMPARISON_PLACEHOLDER-"+strconv.Itoa(i)+" ", cleanComp)
	}

	desc = ConvertCollapseToComparison(desc, "hide", hides)
	desc = ptpEmptyCenterPattern.ReplaceAllString(desc, "")

	desc = strings.Trim(desc, "\n")
	desc = regexp.MustCompile(`\n\n+`).ReplaceAllString(desc, "\n\n")
	for strings.HasPrefix(desc, "\n") {
		desc = strings.TrimPrefix(desc, "\n")
	}
	desc = strings.Trim(desc, "\n")

	if strings.TrimSpace(strings.ReplaceAll(desc, "\n", "")) == "" {
		return Report{Images: imagelist}
	}
	if IsOnlyTags(desc) {
		return Report{Images: imagelist}
	}
	return Report{Description: desc, Images: imagelist}
}

func protectLinks(value string) (string, []string) {
	links := regexp.MustCompile(`https?://\S+`).FindAllString(value, -1)
	for i, link := range links {
		value = strings.ReplaceAll(value, link, "__LINK_PLACEHOLDER_"+strconv.Itoa(i)+"__")
	}
	return value, links
}

func restoreLinks(value string, links []string) string {
	for i, link := range links {
		value = strings.ReplaceAll(value, "__LINK_PLACEHOLDER_"+strconv.Itoa(i)+"__", link)
	}
	return value
}
