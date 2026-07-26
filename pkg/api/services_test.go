// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"encoding/json"
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
	}

	projected := NewTrackerValidationSubject(source, "example")
	projected.Release.Codec[0] = "changed"
	projected.ProviderMetadata.TMDB.LocalizedTitles["en"] = "changed"
	projected.QuestionnaireAnswers["edition"] = "changed"
	*projected.TrackerConfigOverrides.Anon = false

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
