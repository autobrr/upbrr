// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sp

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSPEvidencePolicyPassViolationAndMissingEvidence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		mutate          func(*api.TrackerValidationSubject)
		wantRule        string
		wantDisposition api.RuleDisposition
		wantStatus      api.MetadataEvidenceStatus
	}{
		{name: "complete evidence passes"},
		{
			name: "archive is strict",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.PackageFacts.ArchiveFileCount = 1
			},
			wantRule:        "sp_package_safety",
			wantDisposition: api.RuleDispositionStrict,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
		{
			name: "missing package evidence is advisory",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.PackageFacts.Status = api.MetadataEvidenceStatusUnavailable
			},
			wantRule:        "sp_package_safety",
			wantDisposition: api.RuleDispositionAdvisory,
			wantStatus:      api.MetadataEvidenceStatusUnavailable,
		},
		{
			name: "incomplete local pack is strict",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.PackageFacts.DetectedEpisodes[0].Episodes = []int{1, 3}
			},
			wantRule:        "sp_pack_completeness",
			wantDisposition: api.RuleDispositionStrict,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
		{
			name: "lower resolution without availability proof is advisory",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.Release.Resolution = "720p"
				for index := range subject.MediaFileFacts.Files {
					subject.MediaFileFacts.Files[index].Resolution = "720p"
				}
			},
			wantRule:        "sp_lower_resolution_availability",
			wantDisposition: api.RuleDispositionAdvisory,
			wantStatus:      api.MetadataEvidenceStatusUnavailable,
		},
		{
			name: "current provider adult classification is strict",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.Release.Genre = ""
				subject.Identity.SourcePath = subject.SourcePath
				subject.Identity.Generation = 2
				subject.ProviderMetadata.SourcePath = subject.SourcePath
				subject.ProviderMetadata.Generation = 2
				subject.ProviderMetadata.TVmaze = &api.TVmazeMetadata{Genres: "Adult"}
			},
			wantRule:        "sp_block_adult",
			wantDisposition: api.RuleDispositionStrict,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
		{
			name: "release genre and provider keyword cannot classify adult content",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.Release.Genre = "Adult"
				subject.ProviderMetadata.TMDB.Keywords = "Adult"
			},
		},
		{
			name: "stale provider source cannot block adult content",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.Release.Genre = ""
				subject.Identity.SourcePath = subject.SourcePath
				subject.Identity.Generation = 2
				subject.ProviderMetadata.SourcePath = "stale-source"
				subject.ProviderMetadata.Generation = 2
				subject.ProviderMetadata.TMDB.Genres = "Adult"
			},
		},
		{
			name: "stale provider generation cannot block adult content",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.Release.Genre = ""
				subject.Identity.SourcePath = subject.SourcePath
				subject.Identity.Generation = 2
				subject.ProviderMetadata.SourcePath = subject.SourcePath
				subject.ProviderMetadata.Generation = 1
				subject.ProviderMetadata.TMDB.Genres = "Adult"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			subject := spPassingSubject()
			if test.mutate != nil {
				test.mutate(&subject)
			}
			failures, err := ValidationPolicy().Check(context.Background(), subject, api.NopLogger{})
			if err != nil {
				t.Fatalf("validate SP subject: %v", err)
			}
			if test.wantRule == "" {
				if len(failures) != 0 {
					t.Fatalf("unexpected failures: %#v", failures)
				}
				return
			}
			requireSPValidationFailure(t, failures, test.wantRule, test.wantDisposition, test.wantStatus)
		})
	}
}

func TestSPValidationPolicyVersion(t *testing.T) {
	t.Parallel()
	if got := Profile().ValidationPolicy.ID; got != "unit3d-sp-policy-v4" {
		t.Fatalf("validation policy ID = %q", got)
	}
}

func spPassingSubject() api.TrackerValidationSubject {
	const releaseName = "Example.Show.S01.1080p.WEB-DL.H.264-GRP"
	return api.TrackerValidationSubject{
		SourcePath:  releaseName,
		ReleaseName: releaseName,
		Identity: api.ExternalIdentity{
			Category: api.CanonicalCategoryTV,
			TMDBID:   1234567,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{
				TMDBID: 1234567,
				Genres: "Drama",
			},
		},
		Release: api.ReleaseInfo{
			Genre:      "Drama",
			Resolution: "1080p",
		},
		Type:      "WEBDL",
		SeasonInt: 1,
		TVPack:    true,
		PackageFacts: api.PackageFacts{
			Status:          api.MetadataEvidenceStatusComplete,
			KnownFileCount:  2,
			MediaFileCount:  2,
			DetectedSeasons: []int{1},
			DetectedEpisodes: []api.SeasonEpisodeFacts{{
				Season:   1,
				Episodes: []int{1, 2},
			}},
		},
		MediaFileFacts: api.MediaFileFacts{
			Status:            api.MetadataEvidenceStatusComplete,
			TechnicalStatus:   api.MetadataEvidenceStatusComplete,
			LanguageStatus:    api.MetadataEvidenceStatusComplete,
			ExpectedFileCount: 2,
			Files: []api.MediaFileFact{
				spUniformFile("Example.Show.S01E01.1080p.WEB-DL.H.264-GRP.mkv"),
				spUniformFile("Example.Show.S01E02.1080p.WEB-DL.H.264-GRP.mkv"),
			},
		},
		ProvenanceFacts: api.ProvenanceFacts{Status: api.MetadataEvidenceStatusComplete},
	}
}

func spUniformFile(fileName string) api.MediaFileFact {
	return api.MediaFileFact{
		FileName:          fileName,
		Source:            "WEB",
		Resolution:        "1080p",
		VideoCodec:        "AVC",
		VideoEncode:       "H.264",
		AudioLanguages:    []string{"English"},
		SubtitleLanguages: []string{"English"},
	}
}

func requireSPValidationFailure(
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
