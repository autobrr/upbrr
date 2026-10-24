// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"errors"
	"html"
	"net/url"
	"regexp"
	"strings"

	"github.com/frustra/bbcode"

	descriptionunit3d "github.com/autobrr/upbrr/internal/description/unit3d"
	"github.com/autobrr/upbrr/internal/metadata/discparse"
	"github.com/autobrr/upbrr/pkg/api"
)

func exceptionDescriptionAppendix(note string) string {
	note = strings.TrimSpace(note)
	if note == "" {
		return ""
	}
	note = strings.NewReplacer("[", "&#91;", "]", "&#93;").Replace(html.EscapeString(note))
	return "[b]Container exception[/b]\n" + note
}

// validateFinalDescription preserves mandatory disc parser fields and embedded
// VOB reports. Screenshot deficiencies use the normal evidence-bound review.
func validateFinalDescription(meta api.UploadSubject, description, mediaInfo, bdInfo string) error {
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "BDMV", "HDDVD":
		summary, _, _ := discparse.SplitBDInfoReport(bdInfo)
		report := discparse.ParseBDInfoSummary(summary, "", "")
		if summary == "" || report.Playlist == "" || report.Length == "" {
			return errors.New("LST: the BDInfo parser requires a prepared QUICK SUMMARY report")
		}
	case "DVD":
		if !dvdIFOFormat.MatchString(mediaInfo) {
			return errors.New("LST: the MediaInfo parser requires the main DVD IFO report")
		}
		block := descriptionunit3d.DVDVOBMediaInfoBlock(api.NewDescriptionSubject(meta))
		if block == "" || !strings.Contains(description, block) {
			return errors.New("LST: include the main DVD VOB MediaInfo inside its description spoiler")
		}
	}
	return nil
}

var dvdIFOFormat = regexp.MustCompile(`(?mi)^Format\s*:\s*DVD Video\s*$`)

func validScreenshotFullURL(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	return err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

// countDescriptionScreenshotReferences counts rendered prepared images, merging
// aliases that share either a selected image identity or a full image URL.
func countDescriptionScreenshotReferences(references []api.ScreenshotReference, description string) int {
	images := make([]api.ScreenshotReference, 0, len(references))
	for _, image := range references {
		image.RawURL = strings.TrimSpace(image.RawURL)
		if !validScreenshotFullURL(image.RawURL) || strings.TrimSpace(string(image.ID)) == "" {
			continue
		}
		images = append(images, image)
	}
	var included []api.ScreenshotReference
	var visit func(*bbcode.BBCodeNode)
	visit = func(node *bbcode.BBCodeNode) {
		if tag := node.GetOpeningTag(); tag != nil {
			switch strings.ToLower(tag.Name) {
			case "code", "pre", "noparse", "comparison":
				return
			case "img":
				if node.ClosingTag == nil || len(node.Children) != 1 {
					return
				}
				imageURL, ok := node.Children[0].Value.(string)
				if !ok || node.Children[0].ID != bbcode.TEXT {
					return
				}
				imageURL = strings.TrimSpace(html.UnescapeString(imageURL))
				linkURL := ""
				if parent := node.Parent; parent != nil && parent.ClosingTag != nil {
					if link := parent.GetOpeningTag(); link != nil && strings.EqualFold(link.Name, "url") {
						linkURL = html.UnescapeString(link.Value)
					}
				}
				for _, image := range images {
					if imageURL == image.RawURL || imageURL == image.ImgURL && (linkURL == image.RawURL || linkURL != "" && linkURL == image.WebURL) {
						included = append(included, image)
					}
				}
				return
			}
		}
		for _, child := range node.Children {
			visit(child)
		}
	}
	visit(bbcode.Parse(bbcode.Lex(description)))
	return api.CountDistinctScreenshotReferences(images, included)
}

func finalizeImportedScreenshotDescription(description string, meta api.UploadSubject) string {
	for _, image := range importedReferences(meta.CurrentImportedScreenshotEvidence()) {
		// Match the existing Unit3D renderer's BBCode URL escaping.
		image.RawURL = strings.NewReplacer("[", "%5B", "]", "%5D").Replace(image.RawURL)
		if countDescriptionScreenshotReferences([]api.ScreenshotReference{image}, description) == 0 {
			description = strings.TrimRight(description, "\r\n") + "\n[img]" + image.RawURL + "[/img]"
		}
	}
	return description
}
