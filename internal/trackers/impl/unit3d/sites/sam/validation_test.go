// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sam

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestSAMNameKeepsTVYearAndDropsUnverifiedAudioMarker(t *testing.T) {
	subject := samNamedSubject(t, api.ReleaseNameRequest{
		Category:   "TV",
		Type:       "WEBDL",
		Title:      "A Good Day to Ascend",
		Year:       2026,
		Season:     "S01",
		Resolution: "2160p",
		Source:     "Web",
		Audio:      "DD+ 5.1",
		VideoCodec: "H.265",
		Tag:        "-QHstudIo",
	})
	subject.Identity.Category = api.CanonicalCategoryTV
	subject.AudioLanguages = []string{"Korean"}
	if got, want := samReviewedName(t, subject), "A Good Day to Ascend 2026 S01 2160p WEB-DL DDP5.1 H.265-QHstudIo"; got != want {
		t.Fatalf("SAM name = %q, want %q", got, want)
	}

	subject.AudioLanguages = []string{"Korean", "Portuguese"}
	if got, want := samReviewedName(t, subject), "A Good Day to Ascend 2026 S01 2160p WEB-DL DDP5.1 H.265 DUAL-QHstudIo"; got != want {
		t.Fatalf("SAM Portuguese audio name = %q, want %q", got, want)
	}
}

func TestSAMValidation(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*api.TrackerValidationSubject)
		wantRule string
	}{
		{name: "passing completed season pack"},
		{
			name: "movie rejects multiple videos",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.Identity.Category = api.CanonicalCategoryMovie
				subject.TVPack = false
				subject.PackageFacts.MediaFileCount = 2
			},
			wantRule: "sam_movie_structure",
		},
		{
			name: "TV upload rejects multiple seasons",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.PackageFacts.DetectedSeasons = []int{1, 2}
			},
			wantRule: "sam_tv_structure",
		},
		{
			name: "episode upload rejects multiple episodes",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.TVPack = false
				subject.EpisodeInt = 1
				subject.PackageFacts.MediaFileCount = 2
				subject.PackageFacts.DetectedEpisodes[0].Episodes = []int{1, 2}
			},
			wantRule: "sam_tv_structure",
		},
		{
			name: "season pack rejects ongoing show",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.ProviderMetadata.TVDB.Status = "Continuing"
			},
			wantRule: "sam_season_pack_status",
		},
		{
			name: "season pack rejects unknown status",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.ProviderMetadata.TVDB.Status = ""
			},
			wantRule: "sam_season_pack_status",
		},
		{
			name: "foreign content requires original audio",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.MediaFileFacts.Files[0].AudioLanguages = []string{"Portuguese"}
			},
			wantRule: "sam_language",
		},
		{
			name: "foreign content requires Portuguese subtitles",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.MediaFileFacts.Files[0].SubtitleLanguages = []string{"English"}
			},
			wantRule: "sam_language",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			subject := samPassingSubject()
			if test.mutate != nil {
				test.mutate(&subject)
			}
			failures, err := ValidationPolicy().Check(context.Background(), subject, api.NopLogger{})
			if err != nil {
				t.Fatalf("validate SAM: %v", err)
			}
			if test.wantRule == "" {
				if len(failures) != 0 {
					t.Fatalf("unexpected failures: %#v", failures)
				}
				return
			}
			for _, failure := range failures {
				if failure.Rule == test.wantRule && failure.Disposition == api.RuleDispositionStrict {
					return
				}
			}
			t.Fatalf("missing strict %q failure in %#v", test.wantRule, failures)
		})
	}
}

func samNamedSubject(t *testing.T, request api.ReleaseNameRequest) api.UploadSubject {
	t.Helper()
	result := metadata.BuildReleaseName(request, api.NopLogger{})
	if result.GeneratedName == nil {
		t.Fatal("BuildReleaseName did not produce a structured document")
	}
	return api.UploadSubject{
		ReleaseName:      result.Name,
		ReleaseNameNoTag: result.NameNoTag,
		GeneratedName:    result.GeneratedName,
		Identity:         api.ExternalIdentity{Category: api.CanonicalCategoryTV},
		Release: api.ReleaseInfo{
			Category: request.Category,
			Title:    request.Title,
			Year:     request.Year,
			Group:    request.Tag,
		},
	}
}

func samReviewedName(t *testing.T, subject api.UploadSubject) string {
	t.Helper()
	prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{Tracker: "SAM", Meta: subject}, namePolicy())
	if failure != nil {
		t.Fatal(failure)
	}
	name, err := prepared.ReviewedUploadName()
	if err != nil {
		t.Fatal(err)
	}
	return name
}

func samPassingSubject() api.TrackerValidationSubject {
	return api.TrackerValidationSubject{
		SourcePath:       "current",
		Identity:         api.ExternalIdentity{Category: api.CanonicalCategoryTV, SourcePath: "current"},
		SeasonInt:        1,
		TVPack:           true,
		ProviderMetadata: api.SourceScopedMetadata{SourcePath: "current", TVDB: &api.TVDBMetadata{Status: "Ended"}},
		PackageFacts: api.PackageFacts{
			Status:           api.MetadataEvidenceStatusComplete,
			KnownFileCount:   2,
			MediaFileCount:   2,
			DetectedSeasons:  []int{1},
			DetectedEpisodes: []api.SeasonEpisodeFacts{{Season: 1, Episodes: []int{1, 2}}},
		},
		MediaFileFacts: api.MediaFileFacts{
			LanguageStatus:    api.MetadataEvidenceStatusComplete,
			ExpectedFileCount: 2,
			OriginalLanguage:  "Korean",
			Files: []api.MediaFileFact{
				{AudioLanguages: []string{"Korean"}, SubtitleLanguages: []string{"Portuguese"}},
				{AudioLanguages: []string{"Korean"}, SubtitleLanguages: []string{"Portuguese"}},
			},
		},
	}
}
