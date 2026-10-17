// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fld

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestFLDNamePolicyReplacesDDPlusWithDDP(t *testing.T) {
	t.Parallel()

	result := metadata.BuildReleaseName(api.ReleaseNameRequest{
		Category:   "TV",
		Type:       "WEBDL",
		Title:      "Example Show",
		Year:       2026,
		Season:     "1",
		Episode:    "1",
		Resolution: "1080p",
		Source:     "Web",
		Audio:      "DD+ 5.1",
		Tag:        "-GRP",
	}, api.NopLogger{})

	subject := api.UploadSubject{
		ReleaseName:      result.Name,
		ReleaseNameNoTag: result.NameNoTag,
		GeneratedName:    result.GeneratedName,
	}

	prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker: "FLD",
		Meta:    subject,
	}, Profile().ReleaseNamePolicy)
	if failure != nil {
		t.Fatalf("prepare input failure: %v", failure)
	}

	reviewedName, err := prepared.ReviewedUploadName()
	if err != nil {
		t.Fatalf("reviewed upload name error: %v", err)
	}

	if !strings.Contains(reviewedName, "DDP 5.1") {
		t.Errorf("expected DDP in reviewed name, got %q", reviewedName)
	}
	if strings.Contains(reviewedName, "DD+") {
		t.Errorf("did not expect DD+ in reviewed name, got %q", reviewedName)
	}
}

func TestFLDNamePolicyDVDVideoCodecBeforeAudio(t *testing.T) {
	t.Parallel()

	result := metadata.BuildReleaseName(api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "DISC",
		DiscType:   "DVD",
		Title:      "Example Movie",
		Year:       2026,
		Source:     "DVD",
		Audio:      "AC3 5.1",
		VideoCodec: "MPEG2",
		Tag:        "-GRP",
	}, api.NopLogger{})

	subject := api.UploadSubject{
		Source:           "DVD",
		VideoCodec:       "MPEG2",
		DiscType:         "DVD",
		ReleaseName:      result.Name,
		ReleaseNameNoTag: result.NameNoTag,
		GeneratedName:    result.GeneratedName,
		Release: api.ReleaseInfo{
			Source: "DVD",
			Codec:  []string{"MPEG2"},
			Audio:  []string{"AC3", "5.1"},
		},
	}

	prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker: "FLD",
		Meta:    subject,
	}, Profile().ReleaseNamePolicy)
	if failure != nil {
		t.Fatalf("prepare input failure: %v", failure)
	}

	reviewedName, err := prepared.ReviewedUploadName()
	if err != nil {
		t.Fatalf("reviewed upload name error: %v", err)
	}

	codecIdx := strings.Index(reviewedName, "MPEG2")
	audioIdx := strings.Index(reviewedName, "AC3")
	if codecIdx < 0 || audioIdx < 0 {
		t.Fatalf("expected MPEG2 and AC3 in reviewed name, got %q", reviewedName)
	}
	if codecIdx > audioIdx {
		t.Errorf("expected MPEG2 before AC3 in DVD name, got %q", reviewedName)
	}
}

func TestFLDNamePolicyNonDVDDoesNotInsertCodecBeforeAudio(t *testing.T) {
	t.Parallel()

	result := metadata.BuildReleaseName(api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "WEBDL",
		Title:      "Example Movie",
		Year:       2026,
		Source:     "Web",
		Resolution: "1080p",
		Audio:      "AAC 2.0",
		VideoCodec: "H.264",
		Tag:        "-GRP",
	}, api.NopLogger{})

	subject := api.UploadSubject{
		Source:           "Web",
		VideoCodec:       "H.264",
		ReleaseName:      result.Name,
		ReleaseNameNoTag: result.NameNoTag,
		GeneratedName:    result.GeneratedName,
	}

	prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker: "FLD",
		Meta:    subject,
	}, Profile().ReleaseNamePolicy)
	if failure != nil {
		t.Fatalf("prepare input failure: %v", failure)
	}

	reviewedName, err := prepared.ReviewedUploadName()
	if err != nil {
		t.Fatalf("reviewed upload name error: %v", err)
	}

	if strings.Contains(reviewedName, "DD+") {
		t.Errorf("did not expect DD+ in reviewed name, got %q", reviewedName)
	}
}
