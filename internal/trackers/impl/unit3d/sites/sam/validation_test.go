// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sam

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/rls"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestSAMRequiresPerResourceProgrammeInspection(t *testing.T) {
	t.Parallel()
	registry := trackers.NewRegistry()
	if err := registry.Register(unit3d.NewWithProfile(Profile())); err != nil {
		t.Fatal(err)
	}
	requirements, err := trackers.CollectMetadataRequirements(registry, []api.TrackerID{"SAM"})
	if err != nil || !requirements.InspectProgrammeTracks {
		t.Fatalf("SAM metadata requirements = %+v, err = %v", requirements, err)
	}
	readiness, err := trackers.EvaluateInputReadiness(registry, []api.TrackerID{"SAM"}, api.UploadSubject{
		Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
	})
	if err != nil || !slices.ContainsFunc(readiness.Fields, func(field api.InputReadinessFieldOutcome) bool {
		return field.Key == "metadata.tmdb" && field.Status == api.InputReadinessFieldMissing && field.Disposition == api.RuleDispositionStrict
	}) {
		t.Fatalf("missing TMDB readiness = %+v, err = %v", readiness.Fields, err)
	}
}

func TestSAMValidatesContentAndLanguageEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		edit func(*api.UploadSubject)
		rule string
	}{
		{name: "allowed ended pack"},
		{"same-season numbered parts", func(s *api.UploadSubject) {
			s.PackageInventory.Files[0] = filepath.Join(s.SourcePath, "Example.S01.Part.1.mkv")
			s.PackageInventory.Files[1] = filepath.Join(s.SourcePath, "Example.S01.Part.2.mkv")
		}, ""},
		{"spelled-out same-season parts", func(s *api.UploadSubject) {
			s.PackageInventory.Files[0] = filepath.Join(s.SourcePath, "Example.Season 01 Part 1.mkv")
			s.PackageInventory.Files[1] = filepath.Join(s.SourcePath, "Example.Series.01.Part.2.mkv")
		}, ""},
		{"spelled-out conflicting season", func(s *api.UploadSubject) {
			s.PackageInventory.Files[0] = filepath.Join(s.SourcePath, "Example.Season 01 E01.mkv")
			s.PackageInventory.Files[1] = filepath.Join(s.SourcePath, "Example.Series 02 E02.mkv")
		}, "sam_package"},
		{"unpadded season may identify a part without an episode number", func(s *api.UploadSubject) {
			s.PackageInventory.Files[0] = filepath.Join(s.SourcePath, "Example.Season 1 Part 1.mkv")
			s.PackageInventory.Files[1] = filepath.Join(s.SourcePath, "Example.Season 1 Part 2.mkv")
		}, ""},
		{"unpadded conflicting season", func(s *api.UploadSubject) {
			s.PackageInventory.Files[0] = filepath.Join(s.SourcePath, "Example.Season 1 E01.mkv")
			s.PackageInventory.Files[1] = filepath.Join(s.SourcePath, "Example.Season 2 E02.mkv")
		}, "sam_package"},
		{"same-season mixture of episodes and numbered parts", func(s *api.UploadSubject) {
			s.PackageInventory.Files[0] = filepath.Join(s.SourcePath, "Example.E01.mkv")
			s.PackageInventory.Files[1] = filepath.Join(s.SourcePath, "Example.S01.Part.2.mkv")
		}, ""},
		{"parts belonging to different seasons", func(s *api.UploadSubject) {
			s.PackageInventory.Files[0] = filepath.Join(s.SourcePath, "Example.S01.Part.1.mkv")
			s.PackageInventory.Files[1] = filepath.Join(s.SourcePath, "Example.S02.Part.2.mkv")
		}, "sam_package"},
		{"ongoing pack", func(s *api.UploadSubject) { s.ProviderMetadata.TVDB.Status = "Continuing" }, "sam_pack_status"},
		{"stale pack status", func(s *api.UploadSubject) { s.ProviderMetadata.SourcePath = s.SourcePath + "-other" }, "sam_pack_status"},
		{"wrong series status", func(s *api.UploadSubject) { s.ProviderMetadata.TVDB.TVDBID++ }, "sam_pack_status"},
		{"cross-season pack", func(s *api.UploadSubject) {
			s.PackageInventory.Files[1] = filepath.Join(s.SourcePath, "Example.S02E01.mkv")
		}, "sam_package"},
		{"unidentified main video in pack", func(s *api.UploadSubject) {
			s.PackageInventory.Files[1] = filepath.Join(s.SourcePath, "Unidentified.mkv")
		}, "sam_package"},
		{"camera source alias", func(s *api.UploadSubject) {
			s.Source = rls.ParseString("Example.2026.1080p.HDCAM.x264-GRP").Source
		}, "sam_source"},
		{"prepared R5 transfer with no disc-region field", func(s *api.UploadSubject) {
			s.Source = "HDTC"
			s.Assessments.CaptureSource = "R5"
		}, "sam_source"},
		{"prepared camera source with a normalized disc source", func(s *api.UploadSubject) {
			s.Source = "BluRay"
			s.Assessments.CaptureSource = "CAM"
		}, "sam_source"},
		{"explicit pornography genre", func(s *api.UploadSubject) { s.EffectiveMetadata.Genres = []string{"Pornography"} }, "sam_content"},
		{"general adult genre alone", func(s *api.UploadSubject) { s.EffectiveMetadata.Genres = []string{"Adult"} }, ""},
		{"multiple episodes in an individual upload", func(s *api.UploadSubject) { s.TVPack = false }, "sam_package"},
		{"sidecar archive", func(s *api.UploadSubject) {
			s.PackageInventory.Files = append(s.PackageInventory.Files, filepath.Join(s.SourcePath, "subtitles.zip"))
		}, "sam_package"},
		{"extra NFO", func(s *api.UploadSubject) {
			s.PackageInventory.Files = append(s.PackageInventory.Files, filepath.Join(s.SourcePath, "Example.nfo"))
		}, "sam_package"},
		{"Portuguese from Portugal on second episode", func(s *api.UploadSubject) {
			s.MediaFileFacts.Files[1].SubtitleLanguages = []string{"pt-PT"}
			s.LanguageFacts.Tracks[3].Languages = []string{"pt-PT"}
		}, "sam_language"},
		{"regional subtitle title qualifies generic tag", func(s *api.UploadSubject) {
			s.MediaFileFacts.Files[1].SubtitleLanguages = []string{"pt"}
			s.LanguageFacts.Tracks[3].Languages = []string{"pt"}
			s.LanguageFacts.Tracks[3].Title = "Portuguese (Brazil)"
		}, ""},
		{"conflicting regional subtitle title", func(s *api.UploadSubject) {
			s.LanguageFacts.Tracks[3].Title = "pt-PT"
		}, "sam_language"},
		{"manual regional subtitle correction", func(s *api.UploadSubject) {
			s.LanguageFacts.Tracks[3].Title = "pt-PT"
			s.LanguageFacts.Tracks[3].LanguageProvenance = api.FactProvenanceManual
		}, ""},
		{"aggregate subtitle correction on primary video", func(s *api.UploadSubject) {
			s.ManualLanguages = api.ManualLanguageFacts{AggregateSubtitles: true, Subtitles: []string{"pt-PT"}}
			s.MediaFileFacts.Files[0].SubtitleLanguages = []string{"pt-PT"}
		}, "sam_language"},
		{"Brazilian Portuguese forced subtitle", func(s *api.UploadSubject) {
			s.MediaFileFacts.Files[1].SubtitleLanguages = []string{"Portuguese [pt-BR] (Forced)"}
		}, ""},
		{"dub is default on second episode", func(s *api.UploadSubject) {
			s.LanguageFacts.Tracks[1].Default = false
			s.LanguageFacts.Tracks = append(s.LanguageFacts.Tracks, api.MediaTrackFacts{
				ResourceID:   "episode-2",
				Kind:         api.MediaTrackAudio,
				Role:         api.AudioRoleProgramme,
				Languages:    []string{"pt-BR"},
				Default:      true,
				DefaultKnown: true,
			})
		}, "sam_language"},
		{"missing inspected episode", func(s *api.UploadSubject) {
			s.MediaFileFacts.Files = s.MediaFileFacts.Files[:1]
		}, "sam_language"},
		{"audio-only episode", func(s *api.UploadSubject) {
			s.MediaFileFacts.Files[1].VideoTrackCount = 0
		}, "sam_language"},
		{"sole track explicitly marked non-default", func(s *api.UploadSubject) {
			s.LanguageFacts.Tracks[1].Default = false
		}, "sam_language"},
		{"explicit aggregate audio clear", func(s *api.UploadSubject) {
			s.LanguageFacts = mediafacts.ResolveLanguages(api.MediaFacts{
				OriginalLanguage:         "ja",
				AudioLanguagesProvenance: api.FactProvenanceManualEmpty,
				TrackCoverageComplete:    true,
				Tracks:                   s.LanguageFacts.Tracks,
			})
		}, "sam_language"},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := samSeasonPack(t)
			if test.edit != nil {
				test.edit(&subject)
			}
			binding := unit3d.NewWithProfile(Profile()).ValidationPolicy()
			failures, err := binding.Check(t.Context(), api.NewTrackerValidationSubject(subject, "SAM"), api.NopLogger{})
			samRules := slices.DeleteFunc(failures, func(f api.RuleFailure) bool { return !strings.HasPrefix(f.Rule, "sam_") })
			if err != nil || (test.rule == "" && len(samRules) != 0) || (test.rule != "" && (len(samRules) != 1 || samRules[0].Rule != test.rule)) {
				t.Fatalf("SAM rules = %+v, err = %v, want %q", samRules, err, test.rule)
			}
		})
	}
}

func TestSAMUsesSelectedDVDProgrammeInsteadOfDiscFileCounts(t *testing.T) {
	root := t.TempDir()
	video := filepath.Join(root, "VIDEO_TS", "VTS_01_1.VOB")
	reportPath := filepath.Join(root, "MediaInfo.json")
	media := api.MediaFacts{
		OriginalLanguage:    "pt",
		PrimaryAudioTrackID: "dvd-audio",
		Tracks: []api.MediaTrackFacts{{
			ID:         "dvd-audio",
			ResourceID: mediafacts.TrackResourceID(reportPath),
			Kind:       api.MediaTrackAudio,
			Role:       api.AudioRoleProgramme,
			Languages:  []string{"pt-BR"},
		}},
	}
	meta := api.UploadSubject{
		SourcePath:     root,
		Type:           "DISC",
		DiscType:       "DVD",
		Source:         "PAL DVD",
		Region:         "R5",
		AudioLanguages: []string{"pt-BR"},
		LanguageFacts:  mediafacts.ResolveLanguages(media),
		PackageInventory: api.PackageInventory{
			Status: api.MetadataEvidenceStatusComplete,
			Files:  []string{video, filepath.Join(root, "VIDEO_TS", "VTS_01_2.VOB")},
		},
	}
	subject := api.NewTrackerValidationSubject(meta, "SAM")
	if reason := languageFailure(subject); reason != "" {
		t.Fatalf("selected original DVD audio rejected: %s", reason)
	}
	if prohibitedSource(subject) || packageFailure(subject) != "" {
		t.Fatal("ordinary region-5 DVD rejected")
	}
	episode := meta
	episode.Identity.Category = api.CanonicalCategoryTV
	episode.SeasonInt = 1
	episode.EpisodeInt = 3
	episodicDisc := api.NewTrackerValidationSubject(episode, "SAM")
	if reason := packageFailure(episodicDisc); reason != "" {
		t.Fatalf("one episode split across VOB files rejected: %s", reason)
	}
	episodicDisc.MultipleEpisodes = true
	if reason := packageFailure(episodicDisc); reason == "" {
		t.Fatal("a multi-episode DVD passed individual episode validation")
	}
	for _, parts := range [][]string{
		{"Example.nfo"}, {"Sample.mkv"}, {"Extra.exe"}, {"Proof", "Example.nfo"}, {"Proof", "Extra.exe"},
		{"Another.Movie.mkv"}, {"Notes.txt"}, {"VIDEO_TS", "Another.Movie.mkv"},
	} {
		name := filepath.Join(parts...)
		withExtra := meta
		withExtra.PackageInventory.Files = append(slices.Clone(meta.PackageInventory.Files), filepath.Join(root, name))
		if reason := packageFailure(api.NewTrackerValidationSubject(withExtra, "SAM")); reason == "" {
			t.Fatalf("DVD with unrelated %s passed package validation", name)
		}
	}
}

func TestSAMAcceptsPrimaryAudioCorrectionWhenOtherEpisodesHaveExtraLanguages(t *testing.T) {
	meta := samSeasonPack(t)
	media := api.MediaFacts{
		OriginalLanguage:         "ja",
		TrackCoverageComplete:    true,
		AudioLanguages:           []string{"ja"},
		AudioLanguagesProvenance: api.FactProvenanceManual,
		MediaFileFacts:           meta.MediaFileFacts,
		Tracks: append(meta.LanguageFacts.Tracks, api.MediaTrackFacts{
			ResourceID:   "episode-2",
			Kind:         api.MediaTrackAudio,
			Role:         api.AudioRoleProgramme,
			Languages:    []string{"fr"},
			DefaultKnown: true,
		}),
	}
	media.LanguageFacts = mediafacts.ResolveLanguages(media)
	meta.LanguageFacts = media.LanguageFacts
	meta.MediaFileFacts = mediafacts.ResolveFiles(media, "1080p")
	meta.ManualLanguages = media.ManualLanguages()
	if reason := languageFailure(api.NewTrackerValidationSubject(meta, "SAM")); reason != "" {
		t.Fatalf("matching primary correction rejected: %s", reason)
	}
	media.AudioLanguages = []string{"fr"}
	media.LanguageFacts = mediafacts.ResolveLanguages(media)
	meta.LanguageFacts = media.LanguageFacts
	meta.MediaFileFacts = mediafacts.ResolveFiles(media, "1080p")
	meta.ManualLanguages = media.ManualLanguages()
	if reason := languageFailure(api.NewTrackerValidationSubject(meta, "SAM")); reason == "" {
		t.Fatal("a correction conflicting with the primary resource satisfied validation")
	}
}

func TestSAMAcceptsNativeBluRayResourcesAndRejectsUnrelatedFiles(t *testing.T) {
	root := t.TempDir()
	meta := api.UploadSubject{
		SourcePath: root,
		DiscType:   "BDMV",
		Type:       "DISC",
		Identity:   api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
		PackageInventory: api.PackageInventory{
			Status: api.MetadataEvidenceStatusComplete,
			Files: []string{
				filepath.Join(root, "BDMV", "index.bdmv"),
				filepath.Join(root, "BDMV", "STREAM", "00001.m2ts"),
				filepath.Join(root, "BDMV", "BACKUP", "BDJO", "00001.bdjo"),
				filepath.Join(root, "BDMV", "META", "DL", "thumbnail.jpg"),
				filepath.Join(root, "CERTIFICATE", "id.bdmv"),
			},
		},
	}
	if reason := packageFailure(api.NewTrackerValidationSubject(meta, "SAM")); reason != "" {
		t.Fatalf("native Blu-ray files were rejected: %s", reason)
	}
	for _, file := range []string{
		filepath.Join(root, "BDMV", "notes.txt"),
		filepath.Join(root, "BDMV", "Other", "Another.Movie.mp3"),
	} {
		withExtra := meta
		withExtra.PackageInventory.Files = append(slices.Clone(meta.PackageInventory.Files), file)
		if reason := packageFailure(api.NewTrackerValidationSubject(withExtra, "SAM")); reason == "" {
			t.Fatalf("unrelated Blu-ray file was accepted: %s", filepath.Base(file))
		}
	}
}

func TestSAMRejectsSelectedAuxiliaryVideoWithoutRejectingFeatureTitles(t *testing.T) {
	for _, test := range []struct{ title, filename, want string }{
		{"Example", "Example.2026.sample.mkv", "reject"},
		{"Example", "Example.2026.proof.mkv", "reject"},
		{"The Sample", "The.Sample.mkv", "accept"},
		{"Sample Proof", "Sample.Proof.2026.mkv", "accept"},
	} {
		source := filepath.Join(t.TempDir(), test.filename)
		meta := api.UploadSubject{
			SourcePath: source,
			Identity:   api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
			Release:    api.ReleaseInfo{Title: test.title},
			PackageInventory: api.PackageInventory{
				Status: api.MetadataEvidenceStatusComplete,
				Files:  []string{source},
			},
		}
		if rejected := packageFailure(api.NewTrackerValidationSubject(meta, "SAM")) != ""; rejected != (test.want == "reject") {
			t.Errorf("%s rejected = %t, want %s", test.filename, rejected, test.want)
		}
	}
}

func TestSAMVerifiesSpelledOutSeasonForIndividualEpisode(t *testing.T) {
	for _, test := range []struct {
		filename string
		valid    bool
	}{
		{"Example.Season 01 E01.mkv", true},
		{"Example.Series 02 E01.mkv", false},
		{"Example.Season 2 E01.mkv", false},
		{"Example.Series.2025.S01E01.mkv", true},
	} {
		subject := samSeasonPack(t)
		subject.TVPack = false
		subject.EpisodeInt = 1
		subject.PackageInventory.Files = []string{filepath.Join(subject.SourcePath, test.filename)}
		if reason := packageFailure(api.NewTrackerValidationSubject(subject, "SAM")); (reason == "") != test.valid {
			t.Errorf("%s validity = %t, want %t (reason %q)", test.filename, reason == "", test.valid, reason)
		}
	}
}

func TestSAMAcceptsFullHardcodedBrazilianSubtitlesOnOneVideo(t *testing.T) {
	meta := samSeasonPack(t)
	meta.MediaFileFacts.ExpectedFileCount = 1
	meta.MediaFileFacts.Files = meta.MediaFileFacts.Files[:1]
	meta.MediaFileFacts.Files[0].SubtitleLanguages = nil
	meta.HardcodedSubs = true
	meta.HardcodedSubtitleLanguages = []string{"pt-BR"}
	meta.HardcodedSubtitleCoverage = []api.SubtitleLanguageCoverage{{Language: "pt-BR", Coverage: api.SubtitleCoverageFull}}
	if reason := languageFailure(api.NewTrackerValidationSubject(meta, "SAM")); reason != "" {
		t.Fatalf("full hardcoded subtitles rejected: %s", reason)
	}
	meta.HardcodedSubtitleLanguages = []string{"pt-PT"}
	if reason := languageFailure(api.NewTrackerValidationSubject(meta, "SAM")); reason == "" {
		t.Fatal("European Portuguese hardcoded subtitles satisfied the Brazilian Portuguese requirement")
	}
}

func samSeasonPack(t *testing.T) api.UploadSubject {
	t.Helper()
	root := t.TempDir()
	media := api.MediaFacts{
		OriginalLanguage:      "ja",
		TrackCoverageComplete: true,
		Tracks: []api.MediaTrackFacts{
			{
				ResourceID:   "episode-1",
				Kind:         api.MediaTrackAudio,
				Role:         api.AudioRoleProgramme,
				Languages:    []string{"ja"},
				DefaultKnown: true,
				Default:      true,
			},
			{
				ResourceID:   "episode-2",
				Kind:         api.MediaTrackAudio,
				Role:         api.AudioRoleProgramme,
				Languages:    []string{"ja"},
				DefaultKnown: true,
				Default:      true,
			},
			{
				ResourceID: "episode-1",
				Kind:       api.MediaTrackSubtitle,
				Languages:  []string{"pt-BR"},
			},
			{
				ResourceID: "episode-2",
				Kind:       api.MediaTrackSubtitle,
				Languages:  []string{"pt-BR"},
			},
		},
		MediaFileFacts: api.MediaFileFacts{
			ExpectedFileCount: 2,
			Files: []api.MediaFileFact{
				{
					ResourceID:      "episode-1",
					VideoTrackCount: 1,
					Primary:         true,
				},
				{ResourceID: "episode-2", VideoTrackCount: 1},
			},
		},
	}
	return api.UploadSubject{
		SourcePath:       root,
		Identity:         api.ExternalIdentity{Category: api.CanonicalCategoryTV, TVDBID: 123},
		ProviderMetadata: api.SourceScopedMetadata{SourcePath: root, TVDB: &api.TVDBMetadata{TVDBID: 123, Status: "Ended"}},
		Type:             "WEBDL",
		Source:           "WEB",
		SeasonInt:        1,
		TVPack:           true,
		PackageInventory: api.PackageInventory{
			Status: api.MetadataEvidenceStatusComplete,
			Files:  []string{filepath.Join(root, "Example.S01E01.mkv"), filepath.Join(root, "Example.S01E02.mkv")},
		},
		LanguageFacts:  mediafacts.ResolveLanguages(media),
		MediaFileFacts: mediafacts.ResolveFiles(media, "1080p"),
	}
}
