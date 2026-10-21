// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"errors"
	"html"
	"regexp"

	"github.com/frustra/bbcode"

	"strings"

	descriptionunit3d "github.com/autobrr/upbrr/internal/description/unit3d"
	"github.com/autobrr/upbrr/internal/metadata/discparse"
	"github.com/autobrr/upbrr/internal/trackers"
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

// validateFinalDescription checks the exact content captured for preview and submission.
func validateFinalDescription(meta api.UploadSubject, description, mediaInfo, bdInfo string) error {
	if countPreparedDescriptionImages(meta, description) < 3 {
		return errors.New("LST: include three distinct prepared screenshots as full images or linked thumbnails in the final description")
	}
	subject := api.NewTrackerValidationSubject(meta, "LST")
	if hybridAssetsRequired(subject) && !generatedHybridEvidenceIncluded(subject, description) &&
		subject.QuestionnaireAnswers[assetQuestionKey(subject, "hybrid_will_add")] != "will_add_required_data" {
		subject.DescriptionOverride = description
		subject.DescriptionGroupsFinal = true
		var fields []api.TrackerQuestionnaireField
		for _, field := range assetReviewQuestions(subject) {
			if field.HDRGradeComparison {
				fields = append(fields, field)
			}
		}
		return trackers.NewQuestionnairePreparationFailure("LST",
			"Include the source-linked grade comparison and HDR10+ plot, or confirm that you will add the required data to the description",
			&api.TrackerQuestionnaire{Fields: fields})
	}
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

func countPreparedDescriptionImages(meta api.UploadSubject, description string) int {
	if meta.ExactMedia == nil {
		return 0
	}
	prepared := make(map[string]bool)
	for _, image := range meta.ExactMedia.Screenshots {
		if image.Purpose == api.ScreenshotPurposeFinal && image.Format == "png" && strings.TrimSpace(image.Path) != "" {
			prepared[image.Path] = true
		}
	}
	seen := make(map[string]bool)
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
				for _, image := range meta.ExactMedia.ScreenshotUploads {
					raw := strings.TrimSpace(image.RawURL)
					if raw == "" || !prepared[image.ImagePath] {
						continue
					}
					if imageURL == raw || imageURL == image.ImgURL && (linkURL == raw || linkURL != "" && linkURL == image.WebURL) {
						seen[image.ImagePath] = true
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
	return len(seen)
}
