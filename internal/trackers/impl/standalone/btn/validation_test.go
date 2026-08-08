// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestBTNValidationObjectiveRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		mutate          func(*api.TrackerValidationSubject)
		wantRule        string
		wantDisposition api.RuleDisposition
	}{
		{
			name: "archive",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.PackageFacts.ArchiveFileCount = 1
			},
			wantRule:        "btn_package_safety",
			wantDisposition: api.RuleDispositionStrict,
		},
		{
			name: "extra file",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.PackageFacts.ExtraKinds = []api.PackageFileKind{api.PackageFileKindNFO}
			},
			wantRule:        "btn_package_safety",
			wantDisposition: api.RuleDispositionStrict,
		},
		{
			name: "single file folder",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.PackageFacts.SingleFileFolder = true
			},
			wantRule:        "btn_single_file_layout",
			wantDisposition: api.RuleDispositionStrict,
		},
		{
			name: "multi season",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.PackageFacts.DetectedSeasons = []int{1, 2}
			},
			wantRule:        "btn_multi_season_package",
			wantDisposition: api.RuleDispositionStrict,
		},
		{
			name: "unsupported codec",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.MediaFileFacts.Files[0].VideoCodec = "AV1"
			},
			wantRule:        "btn_media_constraints",
			wantDisposition: api.RuleDispositionStrict,
		},
		{
			name: "dvd remux",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.Type, subject.Source, subject.DiscType = "REMUX", "DVD", "DVD"
				subject.MediaFileFacts.Files[0].Source = "DVD9"
			},
			wantRule:        "btn_dvd_remux",
			wantDisposition: api.RuleDispositionStrict,
		},
		{
			name: "scene dvd image",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.Scene, subject.DiscType, subject.Container = true, "DVD", "ISO"
				subject.MediaFileFacts.Files[0].Container = "ISO"
				subject.MediaFileFacts.Files[0].Source = "DVD9"
			},
			wantRule:        "btn_scene_dvd_image",
			wantDisposition: api.RuleDispositionStrict,
		},
		{
			name: "mixed pack",
			mutate: func(subject *api.TrackerValidationSubject) {
				makeBTNValidationPack(subject)
				subject.FileList = []string{
					"Example.Show.S01E01.1080p.WEB-DL.H.264-GRP.mkv",
					"Example.Show.S01E02.1080p.WEB-DL.H.264-OTHER.mkv",
				}
			},
			wantRule:        "btn_mixed_pack",
			wantDisposition: api.RuleDispositionWaivable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			subject := cleanBTNValidationSubject()
			test.mutate(&subject)
			failures, err := validationPolicy().Check(context.Background(), subject, nil)
			if err != nil {
				t.Fatalf("check requirements: %v", err)
			}
			failure, ok := btnValidationFailure(failures, test.wantRule)
			if !ok || failure.Disposition != test.wantDisposition {
				t.Fatalf("failure %q = %#v in %#v", test.wantRule, failure, failures)
			}
		})
	}
}

func TestBTNValidationCleanEpisodeAndSeasonPackPass(t *testing.T) {
	t.Parallel()

	episode := cleanBTNValidationSubject()
	season := cleanBTNValidationSubject()
	makeBTNValidationPack(&season)
	unknownSource := cleanBTNValidationSubject()
	unknownSource.MediaFileFacts.Files[0].Source = "Unknown"
	for name, subject := range map[string]api.TrackerValidationSubject{
		"episode":        episode,
		"season pack":    season,
		"unknown source": unknownSource,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			failures, err := validationPolicy().Check(context.Background(), subject, nil)
			if err != nil || len(failures) != 0 {
				t.Fatalf("clean validation failures=%#v err=%v", failures, err)
			}
		})
	}
}

func TestBTNValidationPartialEvidenceNeverStrictlyBlocks(t *testing.T) {
	t.Parallel()

	subject := cleanBTNValidationSubject()
	subject.PackageFacts.Status = api.MetadataEvidenceStatusPartial
	subject.MediaFileFacts.Status = api.MetadataEvidenceStatusPartial
	subject.MediaFileFacts.TechnicalStatus = api.MetadataEvidenceStatusPartial
	failures, err := validationPolicy().Check(context.Background(), subject, nil)
	if err != nil {
		t.Fatalf("check requirements: %v", err)
	}
	if len(failures) == 0 {
		t.Fatal("partial evidence should remain visible")
	}
	for _, failure := range failures {
		if failure.Disposition == api.RuleDispositionStrict {
			t.Fatalf("partial evidence produced strict failure: %#v", failure)
		}
	}
}

func cleanBTNValidationSubject() api.TrackerValidationSubject {
	return api.TrackerValidationSubject{
		Tracker:    "BTN",
		Identity:   api.ExternalIdentity{Category: api.CanonicalCategoryTV},
		SeasonInt:  1,
		EpisodeInt: 1,
		FileList:   []string{"Example.Show.S01E01.1080p.WEB-DL.H.264-GRP.mkv"},
		PackageFacts: api.PackageFacts{
			Status:           api.MetadataEvidenceStatusComplete,
			KnownFileCount:   1,
			MediaFileCount:   1,
			DetectedSeasons:  []int{1},
			DetectedEpisodes: []api.SeasonEpisodeFacts{{Season: 1, Episodes: []int{1}}},
		},
		MediaFileFacts: api.MediaFileFacts{
			Status:            api.MetadataEvidenceStatusComplete,
			TechnicalStatus:   api.MetadataEvidenceStatusComplete,
			ExpectedFileCount: 1,
			Files: []api.MediaFileFact{{
				FileName:    "Example.Show.S01E01.1080p.WEB-DL.H.264-GRP.mkv",
				Container:   "MKV",
				Source:      "Web",
				Resolution:  "1080p",
				VideoCodec:  "H.264",
				VideoEncode: "x264",
			}},
		},
	}
}

func makeBTNValidationPack(subject *api.TrackerValidationSubject) {
	subject.TVPack, subject.EpisodeInt = true, 0
	subject.FileList = []string{
		"Example.Show.S01E01.1080p.WEB-DL.H.264-GRP.mkv",
		"Example.Show.S01E02.1080p.WEB-DL.H.264-GRP.mkv",
	}
	subject.PackageFacts.KnownFileCount = 2
	subject.PackageFacts.MediaFileCount = 2
	subject.PackageFacts.DetectedEpisodes = []api.SeasonEpisodeFacts{{Season: 1, Episodes: []int{1, 2}}}
	subject.MediaFileFacts.ExpectedFileCount = 2
	second := subject.MediaFileFacts.Files[0]
	second.FileName = "Example.Show.S01E02.1080p.WEB-DL.H.264-GRP.mkv"
	subject.MediaFileFacts.Files = append(subject.MediaFileFacts.Files, second)
}

func btnValidationFailure(failures []api.RuleFailure, rule string) (api.RuleFailure, bool) {
	for _, failure := range failures {
		if failure.Rule == rule {
			return failure, true
		}
	}
	return api.RuleFailure{}, false
}
