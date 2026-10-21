// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	descriptionunit3d "github.com/autobrr/upbrr/internal/description/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func lstDescriptionScreenshots(meta *api.UploadSubject) string {
	meta.ExactMedia = &api.ExactMediaAssets{}
	images := make([]string, 0, 3)
	for i := range 3 {
		raw := fmt.Sprintf("https://images.example.invalid/full-%d.png", i)
		imagePath := filepath.Join("screenshots", fmt.Sprintf("screen-%d.png", i))
		meta.ExactMedia.Screenshots = append(meta.ExactMedia.Screenshots, api.ScreenshotImage{
			Path:    imagePath,
			Purpose: api.ScreenshotPurposeFinal,
			Format:  "png",
		})
		meta.ExactMedia.ScreenshotUploads = append(meta.ExactMedia.ScreenshotUploads, api.UploadedImageLink{
			ImagePath: imagePath,
			RawURL:    raw,
			ImgURL:    fmt.Sprintf("https://images.example.invalid/thumb-%d.png", i),
			WebURL:    fmt.Sprintf("https://images.example.invalid/page-%d", i),
		})
		images = append(images, "[img]"+raw+"[/img]")
	}
	if meta.TrackerQuestionnaireAnswers == nil {
		meta.TrackerQuestionnaireAnswers = make(map[string]map[string]string)
	}
	if meta.TrackerQuestionnaireAnswers["LST"] == nil {
		meta.TrackerQuestionnaireAnswers["LST"] = make(map[string]string)
	}
	subject := api.NewTrackerValidationSubject(*meta, "LST")
	for _, field := range assetReviewQuestions(subject) {
		value := "Reviewed track notes"
		if field.Kind == "select" {
			value = field.Options[1]
		} else if field.Key != assetQuestionKey(subject, "track_notes") {
			value = "https://images.example.invalid/source-evidence"
		}
		meta.TrackerQuestionnaireAnswers["LST"][field.Key] = value
	}
	subject = api.NewTrackerValidationSubject(*meta, "LST")
	for _, field := range sourceReviewQuestions(subject) {
		meta.TrackerQuestionnaireAnswers["LST"][field.Key] = field.Options[1]
	}
	return strings.Join(images, "\n")
}

func TestLSTFinalDescriptionRequiresActualPreparedImages(t *testing.T) {
	meta := api.UploadSubject{}
	description := lstDescriptionScreenshots(&meta)
	for _, tc := range []struct {
		name, description string
		valid             bool
	}{
		{
			name:        "full images",
			description: description,
			valid:       true,
		},
		{name: "text URLs do not embed", description: strings.NewReplacer("[img]", "", "[/img]", "").Replace(description)},
		{name: "code block is literal", description: "[code]" + description + "[/code]"},
		{name: "unclosed image", description: strings.ReplaceAll(description, "[/img]", "")},
		{name: "repeated image", description: strings.Repeat("[img]"+meta.ExactMedia.ScreenshotUploads[0].RawURL+"[/img]", 3)},
		{name: "unprepared images", description: strings.ReplaceAll(description, "full-", "unprepared-")},
		{
			name:        "independent blocks",
			description: strings.ReplaceAll(description, "\n", "\n\nRelease note\n\n"),
			valid:       true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateFinalDescription(meta, tc.description, "MediaInfo", ""); (err == nil) != tc.valid {
				t.Fatalf("valid=%t, error=%v", tc.valid, err)
			}
		})
	}
	linked := make([]string, 0, len(meta.ExactMedia.ScreenshotUploads))
	for _, link := range meta.ExactMedia.ScreenshotUploads {
		linked = append(linked, "[url="+link.WebURL+"][img=350]"+link.ImgURL+"[/img][/url]")
	}
	if err := validateFinalDescription(meta, strings.Join(linked, "\n"), "MediaInfo", ""); err != nil {
		t.Fatalf("linked thumbnails: %v", err)
	}
}

func TestLSTFinalDescriptionRequiresDiscParserReports(t *testing.T) {
	meta := api.UploadSubject{}
	description := lstDescriptionScreenshots(&meta)
	meta.DiscType = "BDMV"
	if err := validateFinalDescription(meta, description, "", "arbitrary report"); err == nil {
		t.Fatal("invalid BDInfo accepted")
	}
	if err := validateFinalDescription(meta, description, "", "QUICK SUMMARY:\nPlaylist: 00001.MPLS\nLength: 01:00:00\n"); err != nil {
		t.Fatal(err)
	}
	meta.DiscType = "DVD"
	meta.DVDVOBMediaInfoText = "General\nFormat : MPEG-PS\nVideo\nFormat : MPEG Video"
	if err := validateFinalDescription(meta, description, "General\nFormat : DVD Video", ""); err == nil {
		t.Fatal("missing VOB spoiler accepted")
	}
	description = descriptionunit3d.AppendDVDVOBMediaInfoBlock(description, api.NewDescriptionSubject(meta))
	if err := validateFinalDescription(meta, description, meta.DVDVOBMediaInfoText, ""); err == nil {
		t.Fatal("VOB report accepted instead of IFO")
	}
	if err := validateFinalDescription(meta, description, "General\nFormat : DVD Video", ""); err != nil {
		t.Fatal(err)
	}
}

func TestLSTFinalDescriptionDoesNotCountHostedAliasesAsDistinctScreenshots(t *testing.T) {
	meta := api.UploadSubject{}
	description := lstDescriptionScreenshots(&meta)
	for i := range meta.ExactMedia.ScreenshotUploads {
		meta.ExactMedia.ScreenshotUploads[i].ImagePath = meta.ExactMedia.Screenshots[0].Path
	}
	if err := validateFinalDescription(meta, description, "", ""); err == nil {
		t.Fatal("same screenshot hosted at three URLs counted three times")
	}
}
