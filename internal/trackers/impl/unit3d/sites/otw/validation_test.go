// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package otw

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestOTWEvidencePolicyPassViolationAndMissingEvidence(t *testing.T) {
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
			wantRule:        "otw_package_safety",
			wantDisposition: api.RuleDispositionStrict,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
		{
			name: "missing package evidence is advisory",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.PackageFacts.Status = api.MetadataEvidenceStatusUnavailable
			},
			wantRule:        "otw_package_safety",
			wantDisposition: api.RuleDispositionAdvisory,
			wantStatus:      api.MetadataEvidenceStatusUnavailable,
		},
		{
			name: "missing naming metadata is strict",
			mutate: func(subject *api.TrackerValidationSubject) {
				subject.ProviderMetadata.TMDB.Title = ""
			},
			wantRule:        "otw_naming_metadata",
			wantDisposition: api.RuleDispositionStrict,
			wantStatus:      api.MetadataEvidenceStatusComplete,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			subject := otwPassingSubject()
			if test.mutate != nil {
				test.mutate(&subject)
			}
			failures, err := ValidationPolicy().Check(context.Background(), subject, api.NopLogger{})
			if err != nil {
				t.Fatalf("validate OTW subject: %v", err)
			}
			if test.wantRule == "" {
				if len(failures) != 0 {
					t.Fatalf("unexpected failures: %#v", failures)
				}
				return
			}
			requireOTWValidationFailure(t, failures, test.wantRule, test.wantDisposition, test.wantStatus)
		})
	}
}

func TestOTWValidationPolicyVersion(t *testing.T) {
	t.Parallel()
	if got := Profile().ValidationPolicy.ID; got != "unit3d-otw-policy-v3" {
		t.Fatalf("validation policy ID = %q", got)
	}
}

func otwPassingSubject() api.TrackerValidationSubject {
	const sourcePath = "Example.Release.2026.1080p.WEB-DL-GRP"
	return api.TrackerValidationSubject{
		SourcePath: sourcePath,
		Identity: api.ExternalIdentity{
			SourcePath: sourcePath,
			Generation: 3,
			Category:   api.CanonicalCategoryMovie,
			TMDBID:     1234567,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: sourcePath,
			Generation: 3,
			TMDB: &api.TMDBMetadata{
				TMDBID: 1234567,
				Title:  "Example Release",
				Year:   2026,
				Genres: "Animation",
			},
		},
		Release: api.ReleaseInfo{
			Title:      "Example Release",
			Year:       2026,
			Genre:      "Animation",
			Resolution: "1080p",
		},
		Type: "WEBDL",
		PackageFacts: api.PackageFacts{
			Status:         api.MetadataEvidenceStatusComplete,
			KnownFileCount: 1,
			MediaFileCount: 1,
		},
		ProvenanceFacts: api.ProvenanceFacts{Status: api.MetadataEvidenceStatusComplete},
	}
}

func requireOTWValidationFailure(
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
