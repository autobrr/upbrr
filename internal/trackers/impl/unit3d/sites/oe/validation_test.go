// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package oe

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestUploadGuideValidation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		mutate      func(*api.TrackerValidationSubject)
		rule        string
		disposition api.RuleDisposition
	}{
		{name: "compliant WEB release"},
		{
			name: "archive prohibited",
			mutate: func(s *api.TrackerValidationSubject) {
				s.PackageFacts.ArchiveFileCount = 1
			},
			rule:        "oe_package_extras",
			disposition: api.RuleDispositionStrict,
		},
		{
			name: "AVI extension prohibited",
			mutate: func(s *api.TrackerValidationSubject) {
				s.PackageFacts.Extensions = []string{".avi"}
			},
			rule:        "oe_package_extras",
			disposition: api.RuleDispositionStrict,
		},
		{
			name: "Xvid in MKV prohibited",
			mutate: func(s *api.TrackerValidationSubject) {
				s.VideoEncode = "XviD"
			},
			rule:        "oe_avi_xvid",
			disposition: api.RuleDispositionStrict,
		},
		{
			name: "MP3 audio prohibited",
			mutate: func(s *api.TrackerValidationSubject) {
				s.Audio = "MP3 2.0"
			},
			rule:        "oe_mp3_audio",
			disposition: api.RuleDispositionStrict,
		},
		{
			name: "multi-season pack prohibited",
			mutate: func(s *api.TrackerValidationSubject) {
				s.TVPack = true
				s.PackageFacts.DetectedSeasons = []int{1, 2}
			},
			rule:        "oe_multi_season_pack",
			disposition: api.RuleDispositionStrict,
		},
		{
			name: "single season pack needs airing confirmation",
			mutate: func(s *api.TrackerValidationSubject) {
				s.TVPack = true
				s.PackageFacts.DetectedSeasons = []int{1}
			},
			rule:        "oe_season_finished_airing",
			disposition: api.RuleDispositionWaivable,
		},
		{
			name: "screener prohibited",
			mutate: func(s *api.TrackerValidationSubject) {
				s.Source = "DVDSCR"
			},
			rule:        "oe_preretail",
			disposition: api.RuleDispositionStrict,
		},
		{
			name: "iVy requires empty encode availability",
			mutate: func(s *api.TrackerValidationSubject) {
				s.Tag = "-iVy"
			},
			rule:        "oe_ivy_availability",
			disposition: api.RuleDispositionWaivable,
		},
		{
			name: "NFO must be outside torrent",
			mutate: func(s *api.TrackerValidationSubject) {
				s.PackageFacts.ExtraKinds = []api.PackageFileKind{api.PackageFileKindNFO}
			},
			rule:        "oe_package_extras",
			disposition: api.RuleDispositionStrict,
		},
		{
			name: "missing file evidence",
			mutate: func(s *api.TrackerValidationSubject) {
				s.PackageFacts.Status = api.MetadataEvidenceStatusPartial
			},
			rule:        "oe_package_extras",
			disposition: api.RuleDispositionAdvisory,
		},
		{name: "external subtitles remain allowed", mutate: func(s *api.TrackerValidationSubject) {
			s.PackageFacts.ExtraKinds = []api.PackageFileKind{api.PackageFileKindExternalSubtitle}
		}},
		{
			name: "BDInfo only needed for full Blu-ray",
			mutate: func(s *api.TrackerValidationSubject) {
				s.Type = "DISC"
				s.DiscType = "BDMV"
				s.AssetFacts.BDInfo = api.AssetEvidence{Status: api.MetadataEvidenceStatusComplete}
			},
			rule:        "oe_required_assets_bdinfo",
			disposition: api.RuleDispositionStrict,
		},
		{name: "disc structure files preserved", mutate: func(s *api.TrackerValidationSubject) {
			s.Type = "DISC"
			s.DiscType = "BDMV"
			s.PackageFacts.ExtraKinds = []api.PackageFileKind{api.PackageFileKindOther}
			s.AssetFacts.BDInfo = api.AssetEvidence{
				Status: api.MetadataEvidenceStatusComplete,
				Ready:  true,
				Count:  1,
			}
		}},
		{
			name: "full disc NFO must be outside torrent",
			mutate: func(s *api.TrackerValidationSubject) {
				s.Type = "DISC"
				s.DiscType = "BDMV"
				s.PackageFacts.ExtraKinds = []api.PackageFileKind{api.PackageFileKindOther, api.PackageFileKindNFO}
				s.AssetFacts.BDInfo = api.AssetEvidence{
					Status: api.MetadataEvidenceStatusComplete,
					Ready:  true,
					Count:  1,
				}
			},
			rule:        "oe_package_extras",
			disposition: api.RuleDispositionStrict,
		},
		{
			name: "missing group requires staff approval",
			mutate: func(s *api.TrackerValidationSubject) {
				s.Tag = ""
			},
			rule:        "oe_nogrp_approval",
			disposition: api.RuleDispositionWaivable,
		},
		{
			name: "explicit NOGRP requires staff approval",
			mutate: func(s *api.TrackerValidationSubject) {
				s.Tag = "-NOGRP"
			},
			rule:        "oe_nogrp_approval",
			disposition: api.RuleDispositionWaivable,
		},
		{name: "AV1 MediaInfo settings satisfy requirement", mutate: func(s *api.TrackerValidationSubject) {
			s.Type = "ENCODE"
			s.VideoCodec = "AV1"
			s.HasEncodeSettings = true
		}},
		{name: "AV1 WEBRip permits description settings fallback", mutate: func(s *api.TrackerValidationSubject) {
			s.Type = "WEBRIP"
			s.VideoCodec = "AV1"
			s.QuestionnaireAnswers = map[string]string{oeEncodingSettingsKey: "SVT-AV1 preset=4 crf=20"}
		}},
		{name: "AV1 DVD rip permits description settings fallback", mutate: func(s *api.TrackerValidationSubject) {
			s.Type = "DVDRIP"
			s.VideoCodec = "AV1"
			s.QuestionnaireAnswers = map[string]string{oeEncodingSettingsKey: "SVT-AV1 preset=4 crf=20"}
		}},
		{
			name: "AVC WEBRip requires encode settings",
			mutate: func(s *api.TrackerValidationSubject) {
				s.Type = "WEBRIP"
			},
			rule:        "oe_encode_settings",
			disposition: api.RuleDispositionStrict,
		},
		{name: "DVD rip with settings remains allowed", mutate: func(s *api.TrackerValidationSubject) {
			s.Type = "DVDRIP"
			s.HasEncodeSettings = true
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := oeValidationSubject()
			if test.mutate != nil {
				test.mutate(&s)
			}
			failures, err := Profile().ValidationPolicy.Check(t.Context(), s, nil)
			if err != nil {
				t.Fatal(err)
			}
			if test.rule == "" {
				if len(failures) != 0 {
					t.Fatalf("unexpected failures: %#v", failures)
				}
				return
			}
			if len(failures) != 1 || failures[0].Rule != test.rule || failures[0].Disposition != test.disposition {
				t.Fatalf("want %s/%s, got %#v", test.rule, test.disposition, failures)
			}
		})
	}
}

func TestValidationPolicyCombinesGuideAndDescriptionRequirements(t *testing.T) {
	subject := oeValidationSubject()
	subject.Type = "WEBRIP"
	subject.VideoCodec = "AV1"
	subject.PackageFacts.ArchiveFileCount = 1

	failures, err := Profile().ValidationPolicy.Check(t.Context(), subject, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 2 || failures[0].Rule != "oe_package_extras" || failures[1].Rule != "oe_av1_encoding_settings" {
		t.Fatalf("combined validation failures = %#v", failures)
	}
}

func TestEncodeSettingsThroughRegisteredRules(t *testing.T) {
	t.Parallel()
	registry := trackers.NewRegistry()
	if err := registry.Register(unit3d.NewWithProfile(Profile())); err != nil {
		t.Fatal(err)
	}
	for _, status := range []api.EncodeSettingsStatus{api.EncodeSettingsStatusMissing, api.EncodeSettingsStatusPresent, api.EncodeSettingsStatusNotApplicable} {
		s := oeValidationSubject()
		s.Assessments.MediaInfoEncodeSettings = status
		failures, err := trackers.EvaluateTrackerValidationWithRegistry(t.Context(), registry, "OE", s, nil)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, failure := range failures {
			if failure.Rule == "require_valid_mi_setting" {
				found = true
				if failure.Disposition != api.RuleDispositionStrict {
					t.Fatalf("settings failure not strict: %#v", failure)
				}
			}
		}
		if found != (status == api.EncodeSettingsStatusMissing) {
			t.Fatalf("status=%s failures=%#v", status, failures)
		}
	}
}

func TestConditionalBannedGroups(t *testing.T) {
	t.Parallel()
	registry := trackers.NewRegistry()
	if err := registry.Register(unit3d.NewWithProfile(Profile())); err != nil {
		t.Fatal(err)
	}
	checker := trackers.NewBannedGroupCheckerWithRegistry(t.TempDir(), registry)
	for _, group := range []string{"INFINITY", "KONTRAST", "Lootera", "UTOPIA"} {
		banned, err := checker.IsBanned("OE", group)
		if err != nil || !banned {
			t.Fatalf("%s: banned=%t err=%v", group, banned, err)
		}
	}
	for _, group := range []string{"EVO", "iVy", "SM737", "BHDStudio", "Trix", "GRP"} {
		banned, err := checker.IsBanned("OE", group)
		if err != nil || banned {
			t.Fatalf("%s must not be unconditionally banned: banned=%t err=%v", group, banned, err)
		}
	}
	for _, mediaType := range []string{"WEBDL", "ENCODE", "REMUX"} {
		s := oeValidationSubject()
		s.Tag, s.Type = "-EVO", mediaType
		failures, err := trackers.EvaluateTrackerValidationWithRegistry(t.Context(), registry, "OE", s, nil)
		if err != nil {
			t.Fatal(err)
		}
		blocked := false
		for _, failure := range failures {
			if failure.Rule == "block_group_unless_type" {
				blocked = true
			}
		}
		if blocked != (mediaType != "WEBDL") {
			t.Fatalf("EVO type=%s failures=%#v", mediaType, failures)
		}
	}
}

func TestContentKeywordsDoNotClassifyForbiddenContent(t *testing.T) {
	t.Parallel()
	for _, keyword := range []string{"concert", "music video", "hentai"} {
		s := oeValidationSubject()
		s.SourcePath = "Example.Concert.2026-GRP"
		s.Identity = api.ExternalIdentity{SourcePath: s.SourcePath, Generation: 2}
		s.ProviderMetadata = api.SourceScopedMetadata{
			SourcePath: s.SourcePath,
			Generation: 2,
			TMDB:       &api.TMDBMetadata{Keywords: keyword},
		}
		failures, err := ValidationPolicy().Check(t.Context(), s, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(failures) != 0 {
			t.Fatalf("keyword %q incorrectly classified content: %#v", keyword, failures)
		}
	}
}

func oeValidationSubject() api.TrackerValidationSubject {
	return api.TrackerValidationSubject{
		Type:         "WEBDL",
		Tag:          "-GRP",
		VideoCodec:   "AVC",
		PackageFacts: api.PackageFacts{Status: api.MetadataEvidenceStatusComplete},
		AssetFacts: api.AssetFacts{
			Status: api.MetadataEvidenceStatusComplete,
			MediaInfoText: api.AssetEvidence{
				Status: api.MetadataEvidenceStatusComplete,
				Ready:  true,
				Count:  1,
			},
		},
	}
}

func TestDescriptionValidationDoesNotRequireScreenshotsBeforeGeneration(t *testing.T) {
	meta := api.NewTrackerValidationSubject(oeTestSubject(), "OE")
	meta.AssetFacts = api.AssetFacts{Status: api.MetadataEvidenceStatusComplete}
	failures, err := checkDescriptionRequirements(t.Context(), meta, api.NopLogger{})
	if err != nil || len(failures) != 0 {
		t.Fatalf("pre-dupe validation blocked screenshot generation: %v %v", failures, err)
	}
}

func TestDescriptionValidationRejectsRemovedFinalEvidence(t *testing.T) {
	meta := oeTestSubject()
	description, err := buildDescription(t.Context(), meta, config.Config{}, config.TrackerConfig{}, api.NopLogger{}, "notes", nil, oeTestScreenshots())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, remove, rule string }{
		{"settings", "SVT-AV1 preset=4 crf=20", "oe_av1_encoding_settings_description"},
		{"source", "Example BluRay source; original HDR10 only", "oe_sm737_source_notes_description"},
		{"screenshot", "[url=https://images.example/two.png][img=350]https://images.example/two.png[/img][/url]", "oe_description_screenshots"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta.DescriptionGroupsFinal = true
			meta.DescriptionOverride = strings.ReplaceAll(description, tc.remove, "")
			failures, err := checkDescriptionRequirements(t.Context(), api.NewTrackerValidationSubject(meta, "OE"), api.NopLogger{})
			if err != nil || len(failures) != 1 || failures[0].Rule != tc.rule || failures[0].Disposition != api.RuleDispositionStrict {
				t.Fatalf("removed evidence must block upload: %+v %v", failures, err)
			}
		})
	}
	meta.DescriptionOverride = strings.Repeat("[url=https://images.example/one][img=350]https://images.example/one.png[/img][/url]", 3)
	meta.Type = "WEBDL"
	meta.Tag = "GRP"
	failures, err := checkDescriptionRequirements(t.Context(), api.NewTrackerValidationSubject(meta, "OE"), api.NopLogger{})
	if err != nil || len(failures) != 1 || failures[0].Rule != "oe_description_screenshots" {
		t.Fatalf("repeated image must not meet minimum: %+v %v", failures, err)
	}
}

func TestDescriptionValidationAcceptsReformattedFinalEvidence(t *testing.T) {
	meta := oeTestSubject()
	description, err := buildDescription(t.Context(), meta, config.Config{}, config.TrackerConfig{}, api.NopLogger{}, "notes", nil, oeTestScreenshots())
	if err != nil {
		t.Fatal(err)
	}
	// The required evidence can be moved into uploader prose; OE does not
	// require the composer's headings or code blocks in the final description.
	description = oeEvidenceBlockPattern.ReplaceAllString(description, "")
	meta.DescriptionOverride = description + "\n\nEncoder: SVT-AV1 preset=4 crf=20\nSource: Example BluRay source; original HDR10 only"
	meta.DescriptionGroupsFinal = true
	failures, err := checkDescriptionRequirements(t.Context(), api.NewTrackerValidationSubject(meta, "OE"), api.NopLogger{})
	if err != nil || len(failures) != 0 {
		t.Fatalf("reformatted evidence must remain valid: %+v %v", failures, err)
	}
}
