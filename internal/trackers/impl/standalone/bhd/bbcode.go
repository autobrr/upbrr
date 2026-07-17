// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/bbcode"
	imagehost "github.com/autobrr/upbrr/internal/imagehosting/host"
)

type BBCodeOptions struct {
	Framestor bool
	Flux      bool
	BaseDir   string
	UUID      string
	OnNFO     func(text string) error
}

var (
	bhdSizePattern   = regexp.MustCompile(`(?i)\[size=.*?\]`)
	bhdURLImgPattern = regexp.MustCompile(`(?is)\[url=(https?://[^\]]+)\]\s*\[img(?:=[^\]]*)?\](https?://[^\[]+?)\[/img\]\s*\[/url\]`)
	bhdImgTagPattern = regexp.MustCompile(`(?is)\[img(?:=[^\]]*)?\](https?://[^\[]+?)\[/img\]`)
	bhdLooseImg      = regexp.MustCompile(`(?i)(https?://[^\s\[\]]+\.(?:png|jpe?g|webp|gif)(?:\?[^\s\[\]]*)?)`)
	bhdEmptyCenter   = regexp.MustCompile(`(?i)\[center\]\s*\[/center\]`)
	bhdEmptyAlign    = regexp.MustCompile(`(?i)\[align=[^\]]+\]\s*\[/align\]`)
	bhdTonemapNotice = regexp.MustCompile(
		`(?is)\[(?:center|align=center)\]\s*\[code\]\s*Screenshots\s+have\s+been\s+tonemapped\s+for\s+reference\s*\[/code\]\s*\[/(?:center|align)\]`,
	)
	bhdBotSignature = regexp.MustCompile(
		`(?is)(?:\[(?:center|right|align=right)\]\s*(?:\[img=\d+\]https://blutopia\.xyz/favicon\.ico\[/img\]\s*)?\[b\]?Uploaded\s+Using\s+\[url=https://github\.com/HDInnovations/UNIT3D\]UNIT3D\[/url\]\s+Auto\s+Uploader\[/b\]?(?:\s*\[img=\d+\]https://blutopia\.xyz/favicon\.ico\[/img\])?\s*\[/(?:center|right|align)\])|(?:\[center\]\s*\[url=https://github\.com/z-ink/uploadrr\]\[img=\d+\]https://i\.ibb\.co/2NVWb0c/uploadrr\.webp\[/img\]\[/url\]\s*\[/center\])|(?:\[center\]\s*\[url=https://github\.com/edge20200/Only-Uploader\]Powered\s+by\s+Only-Uploader\[/url\]\s*\[/center\])|(?:\[center\]\s*\[url=/torrents\?perPage=\d+&name=[^\]]*\]\s*\[/url\]\s*\[/center\])|(?:\[center\]\s*(?:\[b\]\s*(?:\[size=\d+\])?brush(?:\[/size\])?\s*\[/b\]\s*)?This is an internal release which was first released exclusively on Aither\.\s*Cheers to all the Aither(?:\s+users)?\s*\[/center\])|(?:\[(?:center|right|align=right)\]\s*(?:\[url=[^\]]+\]\s*)?(?:\[size=[^\]]+\]\s*)?Created by(?:\s+[^[]*?)?\s*Upload Assistant(?:\s*\[/size\])?(?:\s*\[/url\])?\s*\[/(?:center|right|align)\])`,
	)
)

func CleanDescription(description string, options BBCodeOptions) bbcode.Report {
	desc := bbcode.NormalizeNewlines(description)
	report := bbcode.Report{}
	imagelist := make([]bbcode.Image, 0)

	if options.Framestor {
		if options.OnNFO != nil {
			if err := options.OnNFO(desc); err != nil {
				report.Notes = append(report.Notes, bbcode.Note{Kind: "nfo", Message: err.Error()})
			}
		}
		report.Artifacts = append(report.Artifacts, bbcode.Artifact{
			Name:    "bhd.nfo",
			Kind:    "nfo",
			Content: desc,
		})
	}

	desc = bhdSizePattern.ReplaceAllString(desc, "")
	desc = strings.ReplaceAll(desc, "[/size]", "")
	desc = bhdBotSignature.ReplaceAllString(desc, "")
	desc = strings.ReplaceAll(desc, "<", "/")
	desc = strings.ReplaceAll(desc, "<", "\\")

	seen := make(map[string]struct{})
	appendImage := func(imgURL, webURL string) {
		imgURL = strings.TrimSpace(imgURL)
		if imgURL == "" {
			return
		}
		key := strings.ToLower(imgURL)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		host := imagehost.ExtractHost(imgURL)
		web := strings.TrimSpace(webURL)
		if web == "" {
			web = imgURL
		}
		rawURL := bbcode.NormalizeImageRawURL(imgURL)
		imagelist = append(imagelist, bbcode.Image{
			ImgURL: imgURL,
			RawURL: rawURL,
			WebURL: web,
			Host:   host,
		})
	}

	urlImgMatches := bhdURLImgPattern.FindAllStringSubmatch(desc, -1)
	for _, match := range urlImgMatches {
		if len(match) < 3 {
			continue
		}
		appendImage(match[2], match[1])
	}
	desc = bhdURLImgPattern.ReplaceAllString(desc, "")

	imgTagMatches := bhdImgTagPattern.FindAllStringSubmatch(desc, -1)
	for _, match := range imgTagMatches {
		if len(match) < 2 {
			continue
		}
		appendImage(match[1], "")
	}
	desc = bhdImgTagPattern.ReplaceAllString(desc, "")

	looseImages := bhdLooseImg.FindAllString(desc, -1)
	for _, imgURL := range looseImages {
		appendImage(imgURL, "")
		desc = strings.ReplaceAll(desc, imgURL, "")
	}

	for _, image := range imagelist {
		imgURL := regexp.QuoteMeta(image.ImgURL)
		urlTag := regexp.MustCompile(`(?i)\[URL=` + imgURL + `\]\[/URL\]`)
		desc = urlTag.ReplaceAllString(desc, "")
		urlImg := regexp.MustCompile(`(?i)\[URL=` + imgURL + `\]\[img[^\]]*\]` + imgURL + `\[/img\]\[/URL\]`)
		desc = urlImg.ReplaceAllString(desc, "")
	}

	desc = regexp.MustCompile(`(?i)\[URL=[\s\S]*?\]\[/URL\]`).ReplaceAllString(desc, "")
	desc = bhdTonemapNotice.ReplaceAllString(desc, "")
	desc = bhdEmptyCenter.ReplaceAllString(desc, "")
	desc = bhdEmptyAlign.ReplaceAllString(desc, "")
	desc = bbcode.RemoveExtraLines(desc)
	desc = strings.TrimSpace(desc)

	if options.Flux {
		desc = strings.TrimRight(desc, " \t\n")
		desc = strings.Trim(desc, "\n")
		desc = regexp.MustCompile(`\n\n+`).ReplaceAllString(desc, "\n\n")
		for strings.HasPrefix(desc, "\n") {
			desc = strings.TrimPrefix(desc, "\n")
		}
		desc = strings.Trim(desc, "\n")
		if strings.TrimSpace(strings.ReplaceAll(desc, "\n", "")) == "" {
			return bbcode.Report{Images: imagelist}
		}
		report.Description = "[code]" + desc + "[/code]"
	} else {
		report.Description = desc
	}

	if report.Description == "" {
		report.Description = ""
	}
	if bbcode.IsOnlyTags(report.Description) {
		return bbcode.Report{
			Images:    imagelist,
			Notes:     report.Notes,
			Artifacts: report.Artifacts,
		}
	}
	return bbcode.Report{
		Description: report.Description,
		Images:      imagelist,
		Notes:       report.Notes,
		Artifacts:   report.Artifacts,
	}
}
