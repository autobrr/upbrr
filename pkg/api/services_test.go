// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestNewDescriptionSubjectDetachesNestedFacts(t *testing.T) {
	t.Parallel()

	source := UploadSubject{
		Release: ReleaseInfo{Codec: []string{"H.265"}},
		ProviderMetadata: SourceScopedMetadata{TMDB: &TMDBMetadata{
			LocalizedTitles: map[string]string{"en": "Example Release 2026"},
		}},
		SelectedBDMVPlaylists: []PlaylistInfo{{File: "00001.mpls"}},
		ImageHostOverrides:    ImageHostOverrides{FailedHosts: []string{"imgbox"}},
		ExactMedia: &ExactMediaAssets{
			Screenshots: []ScreenshotImage{{
				Path:    `C:\private\screen.png`,
				Purpose: ScreenshotPurposeFinal,
			}},
			DVDMenus: []DVDMenuCaptureImage{{
				ScreenshotImage: ScreenshotImage{
					Path:    `C:\private\menu.png`,
					Purpose: ScreenshotPurposeMenu,
				},
			}},
		},
	}
	projected := NewDescriptionSubject(source)
	projected.Release.Codec[0] = "changed"
	projected.ProviderMetadata.TMDB.LocalizedTitles["en"] = "changed"
	projected.SelectedBDMVPlaylists[0].File = "changed"
	projected.ImageHost.FailedHosts[0] = "changed"
	projected.ExactMedia.Screenshots[0].Path = "changed"
	projected.ExactMedia.DVDMenus[0].Path = "changed"

	if source.Release.Codec[0] != "H.265" {
		t.Fatal("release facts share storage with description subject")
	}
	if source.ProviderMetadata.TMDB.LocalizedTitles["en"] != "Example Release 2026" {
		t.Fatal("provider metadata shares storage with description subject")
	}
	if source.SelectedBDMVPlaylists[0].File != "00001.mpls" {
		t.Fatal("playlist facts share storage with description subject")
	}
	if source.ImageHostOverrides.FailedHosts[0] != "imgbox" {
		t.Fatal("failed image hosts share storage with description subject")
	}
	if source.ExactMedia.Screenshots[0].Path != `C:\private\screen.png` ||
		source.ExactMedia.DVDMenus[0].Path != `C:\private\menu.png` {
		t.Fatal("exact media shares storage with description subject")
	}
}

func TestExactMediaAssetsValidateRejectsCrossChannelPurposesAndUploads(t *testing.T) {
	t.Parallel()

	assets := ExactMediaAssets{
		Screenshots: []ScreenshotImage{{
			Path:    `C:\private\menu.png`,
			Purpose: ScreenshotPurposeMenu,
		}},
	}
	if err := assets.Validate(); err == nil {
		t.Fatal("menu-purpose image entered exact screenshot channel")
	}

	assets = ExactMediaAssets{
		Screenshots: []ScreenshotImage{{
			Path:    `C:\private\screen.png`,
			Purpose: ScreenshotPurposeFinal,
		}},
		DVDMenus: []DVDMenuCaptureImage{{
			ScreenshotImage: ScreenshotImage{
				Path:    `C:\private\menu.png`,
				Purpose: ScreenshotPurposeMenu,
			},
		}},
		ScreenshotUploads: []UploadedImageLink{{
			ImagePath: `C:\private\menu.png`,
		}},
	}
	if err := assets.Validate(); err == nil {
		t.Fatal("menu upload entered exact screenshot upload channel")
	}
}

func TestNewTrackerValidationSubjectDetachesMutableFacts(t *testing.T) {
	t.Parallel()

	anon := true
	source := UploadSubject{
		Release: ReleaseInfo{Codec: []string{"H.265"}},
		ProviderMetadata: SourceScopedMetadata{TMDB: &TMDBMetadata{
			LocalizedTitles: map[string]string{"en": "Example Release 2026"},
		}},
		TrackerQuestionnaireAnswers: map[string]map[string]string{
			"EXAMPLE": {"edition": "director"},
		},
		TrackerConfigOverrides: TrackerConfigOverrides{Anon: &anon},
		MediaInfoJSONPath:      `C:\private\MEDIAINFO.json`,
		SceneNFOPath:           `C:\private\release.nfo`,
		Disc:                   DiscFacts{Summary: "BDINFO"},
		AudioLanguages:         []string{"English"},
		SubtitleLanguages:      []string{"Japanese"},
		FileList:               []string{filepath.Join(t.TempDir(), "Example.Release.2026.mkv")},
		ExactMedia: &ExactMediaAssets{
			Screenshots: []ScreenshotImage{{
				Path:    filepath.Join(t.TempDir(), "screen.png"),
				Purpose: ScreenshotPurposeFinal,
			}},
		},
	}
	source.ProviderMetadata.ProviderAvailability = []ProviderAvailabilityEvidence{{
		Provider: IdentityProviderTMDB,
		Status:   ProviderAvailabilityStatusAvailable,
		Source:   "provider_api",
	}}

	projected := NewTrackerValidationSubject(source, "example")
	projected.Release.Codec[0] = "changed"
	projected.ProviderMetadata.TMDB.LocalizedTitles["en"] = "changed"
	projected.QuestionnaireAnswers["edition"] = "changed"
	*projected.TrackerConfigOverrides.Anon = false
	projected.MediaFileFacts.Files[0].AudioLanguages[0] = "changed"
	projected.AvailabilityFacts.Providers[0].Source = "changed"
	projected.PackageFacts.Extensions[0] = ".changed"

	if source.Release.Codec[0] != "H.265" {
		t.Fatal("release facts share storage with validation subject")
	}
	if source.ProviderMetadata.TMDB.LocalizedTitles["en"] != "Example Release 2026" {
		t.Fatal("provider metadata shares storage with validation subject")
	}
	if source.TrackerQuestionnaireAnswers["EXAMPLE"]["edition"] != "director" {
		t.Fatal("questionnaire answers share storage with validation subject")
	}
	if !*source.TrackerConfigOverrides.Anon {
		t.Fatal("tracker overrides share storage with validation subject")
	}
	if source.AudioLanguages[0] != "English" ||
		source.ProviderMetadata.ProviderAvailability[0].Source != "provider_api" ||
		filepath.Ext(source.FileList[0]) != ".mkv" {
		t.Fatal("validation evidence shares storage with upload subject")
	}
	if projected.Tracker != "EXAMPLE" || !projected.MediaInfoJSONReady || !projected.SceneNFOReady || !projected.BDInfoReady {
		t.Fatalf("unexpected projected validation facts: %#v", projected)
	}
	if projected.PreparedResourceFingerprint == "" {
		t.Fatal("prepared-resource fingerprint is empty")
	}

	source.Release.Codec[0] = "source changed"
	source.ProviderMetadata.TMDB.LocalizedTitles["en"] = "source changed"
	source.TrackerQuestionnaireAnswers["EXAMPLE"]["edition"] = "source changed"
	if projected.Release.Codec[0] != "changed" ||
		projected.ProviderMetadata.TMDB.LocalizedTitles["en"] != "changed" ||
		projected.QuestionnaireAnswers["edition"] != "changed" {
		t.Fatal("validation subject changed after source mutation")
	}
}

func TestNewTrackerValidationSubjectDerivesFailSafeEvidence(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "Example.Show.S03E01.srt")
	source := UploadSubject{
		SourcePath: filepath.Join(root, "Example.Show"),
		VideoPath:  filepath.Join(root, "Example.Show", "Season 01", "Example.Show.S01E01.mkv"),
		FileList: []string{
			filepath.Join(root, "Example.Show", "Season 01", "Example.Show.S01E01.mkv"),
			filepath.Join(root, "Example.Show", "Season 02", "Example.Show.S02E01.mkv"),
			filepath.Join(root, "Example.Show", "proof.jpg"),
			filepath.Join(root, "Example.Show", "release.rar"),
			outside,
		},
		Container:         "MKV",
		Source:            "WEB-DL",
		VideoCodec:        "H.265",
		BitDepth:          "10",
		AudioLanguages:    []string{"ja"},
		SubtitleLanguages: []string{"en"},
		ProviderMetadata: SourceScopedMetadata{
			TMDB: &TMDBMetadata{OriginalLanguage: "ja"},
		},
		MediaInfoJSONPath: filepath.Join(root, "MEDIAINFO.json"),
		SceneNFOPath:      filepath.Join(root, "release.nfo"),
		ExactMedia: &ExactMediaAssets{
			Screenshots: []ScreenshotImage{{
				Path:    filepath.Join(root, "screen.png"),
				Purpose: ScreenshotPurposeFinal,
			}},
			ScreenshotUploads: []UploadedImageLink{{
				ImagePath: filepath.Join(root, "screen.png"),
				ImgURL:    "https://images.example/screen.png",
			}},
		},
	}

	projected := NewTrackerValidationSubject(source, "example")
	if projected.PackageFacts.Status != MetadataEvidenceStatusPartial {
		t.Fatalf("package evidence status = %q", projected.PackageFacts.Status)
	}
	if projected.PackageFacts.KnownFileCount != 5 ||
		projected.PackageFacts.MediaFileCount != 2 ||
		projected.PackageFacts.ArchiveFileCount != 1 ||
		projected.PackageFacts.ExternalSubtitleFileCount != 1 ||
		projected.PackageFacts.ExternalFileCount != 1 ||
		projected.PackageFacts.NestedFileCount != 2 {
		t.Fatalf("package evidence = %+v", projected.PackageFacts)
	}
	if len(projected.PackageFacts.DetectedSeasons) != 3 ||
		projected.PackageFacts.DetectedSeasons[0] != 1 ||
		projected.PackageFacts.DetectedSeasons[1] != 2 ||
		projected.PackageFacts.DetectedSeasons[2] != 3 {
		t.Fatalf("detected seasons = %v", projected.PackageFacts.DetectedSeasons)
	}
	if projected.MediaFileFacts.Status != MetadataEvidenceStatusPartial ||
		projected.MediaFileFacts.TechnicalStatus != MetadataEvidenceStatusPartial ||
		projected.MediaFileFacts.LanguageStatus != MetadataEvidenceStatusPartial ||
		projected.MediaFileFacts.ExpectedFileCount != 2 ||
		len(projected.MediaFileFacts.Files) != 1 ||
		projected.MediaFileFacts.OriginalLanguage != "ja" {
		t.Fatalf("media evidence = %+v", projected.MediaFileFacts)
	}
	if projected.AssetFacts.Status != MetadataEvidenceStatusComplete ||
		!projected.AssetFacts.MediaInfoJSON.Ready ||
		!projected.AssetFacts.NFO.Ready ||
		projected.AssetFacts.Screenshots.Count != 1 ||
		projected.AssetFacts.HostedScreenshots.Count != 1 {
		t.Fatalf("asset evidence = %+v", projected.AssetFacts)
	}
	if projected.AvailabilityFacts.Status != MetadataEvidenceStatusUnavailable {
		t.Fatalf("availability status = %q", projected.AvailabilityFacts.Status)
	}
	if projected.ProvenanceFacts.Status != MetadataEvidenceStatusUnavailable {
		t.Fatalf("provenance status = %q", projected.ProvenanceFacts.Status)
	}
}

func TestNewTrackerValidationSubjectProjectsFinalTrackerDescription(t *testing.T) {
	t.Parallel()

	subject := UploadSubject{
		DescriptionGroupsFinal: true,
		DescriptionGroups: []DescriptionBuilderGroup{
			{
				GroupKey:    "other",
				Trackers:    []string{"OTHER"},
				Description: "Other description.",
			},
			{
				GroupKey:       "hdb",
				Trackers:       []string{"HDB"},
				Description:    "Manual synopsis.\n[img]https://img.example/poster.jpg[/img]",
				HasOverride:    true,
				RawDescription: "unused raw description",
			},
		},
	}

	projected := NewTrackerValidationSubject(subject, "hdb")
	if !projected.DescriptionGroupsFinal ||
		projected.DescriptionOverride != "Manual synopsis.\n[img]https://img.example/poster.jpg[/img]" {
		t.Fatalf("tracker description evidence = %#v", projected)
	}
}

func TestNewTrackerValidationSubjectDoesNotInventMissingEvidence(t *testing.T) {
	t.Parallel()

	projected := NewTrackerValidationSubject(UploadSubject{}, "example")
	if projected.PackageFacts.Status != MetadataEvidenceStatusUnavailable ||
		projected.MediaFileFacts.Status != MetadataEvidenceStatusUnavailable ||
		projected.AvailabilityFacts.Status != MetadataEvidenceStatusUnavailable ||
		projected.ProvenanceFacts.Status != MetadataEvidenceStatusUnavailable {
		t.Fatalf("missing evidence was promoted: %+v", projected)
	}
	if projected.AssetFacts.Status != MetadataEvidenceStatusPartial ||
		projected.AssetFacts.Screenshots.Status != MetadataEvidenceStatusUnavailable ||
		projected.AssetFacts.MediaInfoJSON.Status != MetadataEvidenceStatusComplete ||
		projected.AssetFacts.MediaInfoJSON.Ready {
		t.Fatalf("asset readiness evidence = %+v", projected.AssetFacts)
	}
}

func TestTrackerValidationResourceFingerprintIncludesBDInfoReadiness(t *testing.T) {
	t.Parallel()

	withoutBDInfo := NewTrackerValidationSubject(UploadSubject{DiscType: "BDMV"}, "EXAMPLE")
	withBDInfo := NewTrackerValidationSubject(UploadSubject{DiscType: "BDMV", Disc: DiscFacts{Summary: "BDINFO"}}, "EXAMPLE")
	if withoutBDInfo.PreparedResourceFingerprint == withBDInfo.PreparedResourceFingerprint {
		t.Fatal("BDInfo readiness did not invalidate the prepared-resource fingerprint")
	}
}

func TestRuleFailureDispositionFailClosed(t *testing.T) {
	t.Parallel()
	failures := []RuleFailure{
		{Rule: "legacy"},
		{Rule: "advisory", Disposition: RuleDispositionAdvisory},
		{Rule: "unknown", Disposition: "unexpected"},
	}
	if !HasBlockingRuleFailures(failures) {
		t.Fatal("expected legacy and unknown dispositions to block")
	}
	storedFailures := []TrackerRuleFailure{
		{Rule: "legacy"},
		{Rule: "advisory", Disposition: RuleDispositionAdvisory},
		{Rule: "unknown", Disposition: "unexpected"},
		{
			Rule:        "accepted",
			Disposition: RuleDispositionWaivable,
			Authorized:  true,
		},
	}
	if got := CountBlockingRuleFailures(storedFailures); got != 2 {
		t.Fatalf("blocking count = %d, want 2", got)
	}
	if got := BlockingRuleFailures(failures); len(got) != 2 || got[0].Rule != "legacy" || got[1].Rule != "unknown" {
		t.Fatalf("unexpected blocking subset: %#v", got)
	}
	if got := AdvisoryRuleFailures(failures); len(got) != 1 || got[0].Rule != "advisory" {
		t.Fatalf("unexpected advisory subset: %#v", got)
	}
	if NormalizeRuleDisposition("warning") != RuleDispositionAdvisory ||
		NormalizeRuleDisposition("blocking") != RuleDispositionWaivable ||
		NormalizeRuleDisposition("unexpected") != RuleDispositionStrict {
		t.Fatal("legacy or unknown disposition normalization changed")
	}
}

func TestTMDBMetadataMarshalLocalizedTitlesAsObject(t *testing.T) {
	tests := []struct {
		name            string
		localizedTitles map[string]string
		wantJSON        string
	}{
		{
			name:     "nil",
			wantJSON: `{}`,
		},
		{
			name:            "empty",
			localizedTitles: map[string]string{},
			wantJSON:        `{}`,
		},
		{
			name:            "preserves keys",
			localizedTitles: map[string]string{"de": "Die Probe", "pt-BR": "Titulo Brasil"},
			wantJSON:        `{"de":"Die Probe","pt-BR":"Titulo Brasil"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(TMDBMetadata{LocalizedTitles: tt.localizedTitles})
			if err != nil {
				t.Fatalf("marshal TMDBMetadata: %v", err)
			}

			var payload map[string]json.RawMessage
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatalf("unmarshal marshaled TMDBMetadata: %v", err)
			}

			got, ok := payload["LocalizedTitles"]
			if !ok {
				t.Fatalf("expected LocalizedTitles field in payload %s", raw)
			}
			if string(got) != tt.wantJSON {
				t.Fatalf("LocalizedTitles JSON = %s, want %s", got, tt.wantJSON)
			}
		})
	}
}

func TestTVDBMetadataJSONPreservesExplicitEvidenceWithoutInventingLegacyEvidence(t *testing.T) {
	var legacy TVDBMetadata
	if err := json.Unmarshal([]byte(`{"TVDBID":987650001,"Name":"Example Series","Year":2026,"YearFromAlias":true}`), &legacy); err != nil {
		t.Fatalf("decode legacy TVDB metadata: %v", err)
	}
	if legacy.NameDisambiguation.Status != "" ||
		legacy.NameDisambiguation.IncludeYear ||
		legacy.NameDisambiguation.IncludeLocale {
		t.Fatalf("legacy JSON invented evidence: %+v", legacy.NameDisambiguation)
	}
	if !legacy.YearFromAlias || legacy.Year != 2026 {
		t.Fatalf("legacy year compatibility = year=%d alias=%t", legacy.Year, legacy.YearFromAlias)
	}

	current := TVDBMetadata{
		TVDBID: 987650001,
		Name:   "Example Series",
		Year:   2026,
		NameDisambiguation: TVDBNameDisambiguation{
			CanonicalName:         "Example Series",
			SeriesYear:            2026,
			Locale:                "US",
			SameNameSeries:        1,
			SameNameAndYearSeries: 1,
			IncludeYear:           true,
			IncludeLocale:         true,
			Status:                MetadataEvidenceStatusPartial,
			Source:                "tvdb_v4_search_unpaged",
		},
	}
	payload, err := json.Marshal(current)
	if err != nil {
		t.Fatalf("encode current TVDB metadata: %v", err)
	}
	var decoded TVDBMetadata
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode current TVDB metadata: %v", err)
	}
	if decoded.NameDisambiguation != current.NameDisambiguation {
		t.Fatalf("disambiguation = %+v, want %+v", decoded.NameDisambiguation, current.NameDisambiguation)
	}
}
