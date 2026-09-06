// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ulcx

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestDeterministicValidationEvidence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		mutate          func(*api.TrackerValidationSubject)
		wantRule        string
		wantDisposition api.RuleDisposition
		wantStatus      api.MetadataEvidenceStatus
	}{
		{name: "non encode 1080p HEVC passes"},
		{
			name: "archive is strict",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.PackageFacts.ArchiveFileCount = 1
			},
			wantRule:        "ulcx_package_safety",
			wantDisposition: api.RuleDispositionStrict,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
		{
			name: "missing package evidence is advisory",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.PackageFacts.Status = api.MetadataEvidenceStatusUnavailable
			},
			wantRule:        "ulcx_package_safety",
			wantDisposition: api.RuleDispositionAdvisory,
			wantStatus:      api.MetadataEvidenceStatusUnavailable,
		},
		{
			name: "MP4 release is strict",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.PackageFacts.Extensions = []string{".mp4"}
			},
			wantRule:        "ulcx_media_container",
			wantDisposition: api.RuleDispositionStrict,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
		{
			name: "TS HDTV passes",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.Type = "HDTV"
				subject.PackageFacts.Extensions = []string{".ts"}
			},
		},
		{
			name: "TS WEB-DL is strict",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.PackageFacts.Extensions = []string{".ts"}
			},
			wantRule:        "ulcx_media_container",
			wantDisposition: api.RuleDispositionStrict,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
		{
			name: "too few screenshots is strict",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.AssetFacts.HostedScreenshots.Count = 2
			},
			wantRule:        "ulcx_required_assets_hosted_screenshot",
			wantDisposition: api.RuleDispositionStrict,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
		{
			name: "live action AV1 encode is strict",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.Type = "ENCODE"
				subject.VideoCodec = "AV1"
			},
			wantRule:        "ulcx_av1_animation_only",
			wantDisposition: api.RuleDispositionStrict,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
		{
			name: "animated AV1 encode passes",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.Type = "ENCODE"
				subject.VideoCodec = "AV1"
				subject.SourcePath = "Example.Release.2026.1080p-GRP"
				subject.Identity = api.ExternalIdentity{SourcePath: subject.SourcePath, Generation: 1}
				subject.ProviderMetadata = api.SourceScopedMetadata{
					SourcePath: subject.SourcePath,
					Generation: 1,
					TMDB:       &api.TMDBMetadata{Genres: "Animation"},
				}
			},
		},
		{
			name: "LPCM is strict",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.Audio = "LPCM"
				subject.Channels = "2.0"
				subject.MediaFileFacts.TechnicalStatus = api.MetadataEvidenceStatusPartial
			},
			wantRule:        "ulcx_lpcm_audio",
			wantDisposition: api.RuleDispositionStrict,
			wantStatus:      api.MetadataEvidenceStatusPartial,
		},
		{
			name: "full disc LPCM passes",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.Type = "DISC"
				subject.DiscType = "BDMV"
				subject.Audio = "LPCM 2.0"
				subject.Channels = "2.0"
				subject.MediaFileFacts.TechnicalStatus = api.MetadataEvidenceStatusPartial
				subject.AssetFacts.BDInfo = api.AssetEvidence{
					Status: api.MetadataEvidenceStatusComplete,
					Ready:  true,
					Count:  1,
				}
			},
		},
		{
			name: "multichannel FLAC is strict",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.Audio = "FLAC"
				subject.Channels = "5.1"
			},
			wantRule:        "ulcx_flac_channels",
			wantDisposition: api.RuleDispositionStrict,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
		{
			name: "stereo FLAC passes",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.Audio = "FLAC"
				subject.Channels = "2.0"
			},
		},
		{
			name: "1080p encode lossless multichannel is strict",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.Type = "ENCODE"
				subject.Audio = "TrueHD"
				subject.Channels = "5.1"
			},
			wantRule:        "ulcx_encode_lossless_multichannel",
			wantDisposition: api.RuleDispositionStrict,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
		{
			name: "1080p encode DTS:X multichannel is strict",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.Type = "ENCODE"
				subject.VideoCodec = "AVC"
				subject.Audio = "DTS:X 7.1"
				subject.Channels = "7.1"
			},
			wantRule:        "ulcx_encode_lossless_multichannel",
			wantDisposition: api.RuleDispositionStrict,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
		{
			name: "1080p encode DTS:X stereo passes",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.Type = "ENCODE"
				subject.VideoCodec = "AVC"
				subject.Audio = "DTS:X 2.0"
				subject.Channels = "2.0"
			},
		},
		{
			name: "2160p encode DTS:X multichannel passes",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.Type = "ENCODE"
				subject.VideoCodec = "AVC"
				subject.Audio = "DTS:X 7.1"
				subject.Channels = "7.1"
				subject.Release.Resolution = "2160p"
				subject.MediaFileFacts.Files[0].Source = "2160p Blu-ray"
			},
		},
		{
			name: "1080p encode lossy DTS multichannel passes",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.Type = "ENCODE"
				subject.VideoCodec = "AVC"
				subject.Audio = "DTS 5.1"
				subject.Channels = "5.1"
			},
		},
		{
			name: "1080p encode ADPCM multichannel passes",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.Type = "ENCODE"
				subject.VideoCodec = "AVC"
				subject.Audio = "ADPCM"
				subject.Channels = "5.1"
			},
		},
		{
			name: "2160p encode lossless multichannel passes",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.Type = "ENCODE"
				subject.Audio = "TrueHD"
				subject.Channels = "5.1"
				subject.Release.Resolution = "2160p"
				subject.MediaFileFacts.Files[0].Source = "2160p Blu-ray"
			},
		},
		{
			name: "missing screenshot evidence is advisory",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.AssetFacts.HostedScreenshots.Status = api.MetadataEvidenceStatusUnavailable
			},
			wantRule:        "ulcx_required_assets_hosted_screenshot",
			wantDisposition: api.RuleDispositionAdvisory,
			wantStatus:      api.MetadataEvidenceStatusUnavailable,
		},
		{
			name: "proved live action HD x265 source is strict",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.Type = "ENCODE"
				subject.VideoEncode = "x265"
				subject.MediaFileFacts.Files[0].Source = "1080p Blu-ray"
			},
			wantRule:        "hevc_resolution_2160p",
			wantDisposition: api.RuleDispositionStrict,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
		{
			name: "ambiguous x265 source is advisory",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.Type = "ENCODE"
				subject.VideoEncode = "x265"
				subject.MediaFileFacts.Files[0].Source = "Blu-ray"
			},
			wantRule:        "ulcx_x265_source",
			wantDisposition: api.RuleDispositionAdvisory,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			subject := ulcxValidationSubject()
			if test.mutate != nil {
				test.mutate(&subject)
			}
			failures, err := ValidationPolicy().Check(context.Background(), subject, nil)
			if err != nil {
				t.Fatalf("validate ULCX subject: %v", err)
			}
			if test.wantRule == "" {
				if len(failures) != 0 {
					t.Fatalf("unexpected failures: %#v", failures)
				}
				return
			}
			requireULCXValidationFailure(t, failures, test.wantRule, test.wantDisposition, test.wantStatus)
		})
	}
}

func TestMissingResolutionDoesNotRejectLosslessAudio(t *testing.T) {
	t.Parallel()
	subject := ulcxValidationSubject()
	subject.Type = "ENCODE"
	subject.Audio = "TrueHD 5.1"
	subject.Channels = "5.1"
	subject.Release.Resolution = ""
	subject.MediaFileFacts.TechnicalStatus = api.MetadataEvidenceStatusPartial
	subject.MediaFileFacts.Files[0].Resolution = ""
	failures, err := ValidationPolicy().Check(t.Context(), subject, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 {
		t.Fatalf("expected only a missing-resolution advisory, got %#v", failures)
	}
	requireULCXValidationFailure(t, failures, "ulcx_encode_resolution_evidence", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
}

func TestRulesRequireEncodeSettings(t *testing.T) {
	t.Parallel()
	if !Rules().RequireValidMISetting {
		t.Fatal("ULCX encode-settings rule is disabled")
	}
}

func ulcxValidationSubject() api.TrackerValidationSubject {
	return api.TrackerValidationSubject{
		Type:       "WEBDL",
		Container:  "mkv",
		VideoCodec: "HEVC",
		Release:    api.ReleaseInfo{Resolution: "1080p"},
		PackageFacts: api.PackageFacts{
			Status:         api.MetadataEvidenceStatusComplete,
			KnownFileCount: 1,
			MediaFileCount: 1,
			Extensions:     []string{".mkv"},
		},
		MediaFileFacts: api.MediaFileFacts{
			Status:            api.MetadataEvidenceStatusComplete,
			TechnicalStatus:   api.MetadataEvidenceStatusComplete,
			LanguageStatus:    api.MetadataEvidenceStatusComplete,
			ExpectedFileCount: 1,
			OriginalLanguage:  "English",
			Files: []api.MediaFileFact{{
				FileName:        "Example.Release.2026.1080p.WEB-DL-GRP.mkv",
				Container:       "MKV",
				Source:          "WEB",
				Resolution:      "1080p",
				VideoCodec:      "HEVC",
				VideoTrackCount: 1,
				AudioLanguages:  []string{"English"},
			}},
		},
		AssetFacts: api.AssetFacts{
			Status: api.MetadataEvidenceStatusComplete,
			MediaInfoText: api.AssetEvidence{
				Status: api.MetadataEvidenceStatusComplete,
				Ready:  true,
				Count:  1,
			},
			HostedScreenshots: api.AssetEvidence{
				Status: api.MetadataEvidenceStatusComplete,
				Ready:  true,
				Count:  3,
			},
		},
	}
}

func requireULCXValidationFailure(
	t *testing.T,
	failures []api.RuleFailure,
	rule string,
	disposition api.RuleDisposition,
	status api.MetadataEvidenceStatus,
) {
	t.Helper()
	for _, failure := range failures {
		if failure.Rule == rule && failure.Disposition == disposition && failure.EvidenceStatus == status {
			return
		}
	}
	t.Fatalf("missing failure rule=%s disposition=%s status=%s in %#v", rule, disposition, status, failures)
}
