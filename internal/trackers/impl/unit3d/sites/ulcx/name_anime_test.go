// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ulcx

import (
	"reflect"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestULCXAnimeAlternateTitle(t *testing.T) {
	for _, test := range []struct {
		name          string
		change        func(*api.UploadSubject)
		wantAlternate bool
	}{
		{name: "current MAL movie"},
		{name: "current MAL TV", change: func(s *api.UploadSubject) { s.Identity.Category = api.CanonicalCategoryTV }},
		{name: "no MAL", change: func(s *api.UploadSubject) { s.Identity.MALID = 0 }, wantAlternate: true},
		{name: "anime without MAL", change: func(s *api.UploadSubject) {
			s.Identity.MALID = 0
			s.Anime = true
		}, wantAlternate: true},
		{name: "raw MAL only", change: func(s *api.UploadSubject) {
			s.Identity.MALID = 0
			s.ProviderMetadata.AniList = &api.AniListMetadata{MALID: 123}
		}, wantAlternate: true},
		{name: "wrong identity source", change: func(s *api.UploadSubject) { s.Identity.SourcePath = "other" }, wantAlternate: true},
		{name: "wrong metadata source", change: func(s *api.UploadSubject) { s.ProviderMetadata.SourcePath = "other" }, wantAlternate: true},
		{name: "stale generation", change: func(s *api.UploadSubject) { s.ProviderMetadata.Generation = 2 }, wantAlternate: true},
		{name: "unbound identity", change: func(s *api.UploadSubject) { s.Identity.Generation = 0 }, wantAlternate: true},
		{name: "manual metadata", change: func(s *api.UploadSubject) {
			s.EffectiveMetadata.AlternateTitleProvenance = api.FactProvenanceManual
		}, wantAlternate: true},
		{name: "manual component", change: func(s *api.UploadSubject) { s.GeneratedName.Components[1].Manual = true }, wantAlternate: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := ulcxAnimeNameSubject()
			if test.change != nil {
				test.change(&subject)
			}
			before := subject.GeneratedName.Clone()
			prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{Tracker: "ULCX", Meta: subject}, namePolicy())
			if failure != nil {
				t.Fatal(failure)
			}
			got, err := prepared.ReviewedUploadName()
			if err != nil {
				t.Fatal(err)
			}
			want := "Example 2026 1080p"
			if test.wantAlternate {
				want = "Example AKA Other 2026 1080p"
			}
			if got != want {
				t.Fatalf("name = %q, want %q", got, want)
			}
			if prepared.Projection.UploadReleaseName != got || prepared.Projection.DuplicateCriteria.Name != got {
				t.Fatal("review and duplicate projections differ")
			}
			if !reflect.DeepEqual(before, subject.GeneratedName) {
				t.Fatal("canonical document mutated")
			}
		})
	}
}

func TestULCXAnimeAlternateTitlePreservesOverrides(t *testing.T) {
	subject := ulcxAnimeNameSubject()
	requested := "Complete Custom Name"
	prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker: "ULCX", Meta: subject, RequestedUploadName: &requested,
	}, namePolicy())
	if failure != nil {
		t.Fatal(failure)
	}
	if got, err := prepared.ReviewedUploadName(); err != nil || got != requested {
		t.Fatalf("override = %q: %v", got, err)
	}
	for _, manual := range []bool{false, true} {
		subject := ulcxAnimeNameSubject()
		subject.GeneratedName.Components[1].Present = false
		subject.GeneratedName.Components[1].Manual = manual
		if manual {
			subject.EffectiveMetadata.AlternateTitleProvenance = api.FactProvenanceManualEmpty
		}
		subject.ReleaseName = subject.GeneratedName.Render().Name
		prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{Tracker: "ULCX", Meta: subject}, namePolicy())
		if failure != nil {
			t.Fatal(failure)
		}
		if got, err := prepared.ReviewedUploadName(); err != nil || got != "Example 2026 1080p" {
			t.Fatalf("absent alternate = %q: %v", got, err)
		}
	}
}

func ulcxAnimeNameSubject() api.UploadSubject {
	document := &api.ReleaseNameDocument{
		Version: api.ReleaseNameDocumentVersionV1,
		Components: []api.ReleaseNameComponent{
			{Role: api.NameRoleTitle, Value: "Example", Present: true},
			{Role: api.NameRoleAlternateTitle, Value: "AKA Other", Present: true, Join: " "},
			{Role: api.NameRoleYear, Value: "2026", Present: true, Join: " "},
			{Role: api.NameRoleResolution, Value: "1080p", Present: true, Join: " "},
		},
	}
	return api.UploadSubject{
		SourcePath: "source", GeneratedName: document, ReleaseName: document.Render().Name,
		Identity:         api.ExternalIdentity{SourcePath: "source", Generation: 1, MALID: 123, Category: api.CanonicalCategoryMovie},
		ProviderMetadata: api.SourceScopedMetadata{SourcePath: "source", Generation: 1},
	}
}
