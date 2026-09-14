// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dp

import (
	"testing"

	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDPStructuredReleaseNamePolicyUsesTVDBRoles(t *testing.T) {
	t.Parallel()
	subject := dpGeneratedSubject(t, api.ReleaseNameRequest{
		Category:     "TV",
		Type:         "WEBDL",
		Title:        "Dual-Audio Series",
		AltTitle:     "AKA Example Original",
		Year:         2026,
		Season:       "S01",
		Episode:      "E02",
		EpisodeTitle: "Example Episode",
		Resolution:   "1080p",
		Source:       "Web",
		Audio:        "Dual-Audio DD+ 5.1",
		VideoEncode:  "H.265",
		Tag:          "-GRP",
	})
	subject.ProviderMetadata.TVDB = &api.TVDBMetadata{NameDisambiguation: api.TVDBNameDisambiguation{
		CanonicalName: "Dual-Audio Series",
		SeriesYear:    2026,
		Locale:        "US",
		IncludeLocale: true,
		IncludeYear:   true,
	}}
	subject.AudioLanguages = []string{"English", "Japanese", "French"}
	if got, want := dpReviewedName(t, subject, nil), "Dual-Audio Series AKA Example Original US 2026 S01E02 Example Episode 1080p WEB-DL MULTi DD+ 5.1 H.265-GRP"; got != want {
		t.Fatalf("DP name = %q, want %q", got, want)
	}
	disc := subject
	disc.DiscType = "DVD"
	if got, want := dpReviewedName(t, disc, nil), "Dual-Audio Series AKA Example Original US 2026 S01E02 Example Episode 1080p WEB-DL Dual-Audio DD+ 5.1 H.265-GRP"; got != want {
		t.Fatalf("disc audio label = %q, want %q", got, want)
	}
	manual := subject
	markDPManual(t, manual.GeneratedName, api.NameRoleDualAudio)
	manual.ReleaseName = manual.GeneratedName.Render().Name
	if got, want := dpReviewedName(t, manual, nil), "Dual-Audio Series AKA Example Original US 2026 S01E02 Example Episode 1080p WEB-DL Dual-Audio DD+ 5.1 H.265-GRP"; got != want {
		t.Fatalf("manual dual-audio component changed: %q, want %q", got, want)
	}
	override := "Manual DP Name-GRP"
	if got := dpReviewedName(t, subject, &override); got != override {
		t.Fatalf("opaque override = %q, want %q", got, override)
	}
	policy := unit3d.NewWithProfile(Profile()).ReleaseNamePolicy()
	if policy.ID != "unit3d/dp/v3" || policy.Structured == nil || policy.Resolver != nil {
		t.Fatalf("DP policy = %#v", policy)
	}
}

func TestDPStructuredReleaseNamePolicyRequiresCurrentMatchingTVDBEvidence(t *testing.T) {
	t.Parallel()
	base := dpGeneratedSubject(t, api.ReleaseNameRequest{
		Category:   "TV",
		Type:       "WEBDL",
		Title:      "Example Series",
		AltTitle:   "AKA Original",
		Year:       2026,
		Season:     "S01",
		Episode:    "E02",
		Resolution: "1080p",
		Source:     "Web",
		Tag:        "-GRP",
	})
	cases := []struct {
		name string
		edit func(*api.UploadSubject)
	}{
		{"missing disambiguation", func(subject *api.UploadSubject) { subject.ProviderMetadata.TVDB = &api.TVDBMetadata{} }},
		{"stale snapshot", func(subject *api.UploadSubject) {
			subject.SourcePath, subject.Identity.SourcePath, subject.ProviderMetadata.SourcePath = "current", "current", "stale"
			subject.ProviderMetadata.TVDB = &api.TVDBMetadata{NameDisambiguation: api.TVDBNameDisambiguation{
				CanonicalName: "Example Series",
				SeriesYear:    2030,
				IncludeYear:   true,
				IncludeLocale: true,
				Locale:        "US",
			}}
		}},
		{"conflicting canonical title", func(subject *api.UploadSubject) {
			subject.ProviderMetadata.TVDB = &api.TVDBMetadata{NameDisambiguation: api.TVDBNameDisambiguation{
				CanonicalName: "Other Series",
				SeriesYear:    2030,
				IncludeYear:   true,
				IncludeLocale: true,
				Locale:        "US",
			}}
		}},
		{"manual title", func(subject *api.UploadSubject) {
			markDPComponent(t, subject.GeneratedName, api.NameRoleTitle, "Manual Series", true)
			subject.ReleaseName = subject.GeneratedName.Render().Name
			subject.ProviderMetadata.TVDB = &api.TVDBMetadata{NameDisambiguation: api.TVDBNameDisambiguation{
				CanonicalName: "Example Series",
				SeriesYear:    2030,
				IncludeYear:   true,
				IncludeLocale: true,
				Locale:        "US",
			}}
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			subject := base
			subject.GeneratedName = base.GeneratedName.Clone()
			test.edit(&subject)
			if got := dpReviewedName(t, subject, nil); got != subject.ReleaseName {
				t.Fatalf("TVDB gate changed %q to %q", subject.ReleaseName, got)
			}
		})
	}
}

func dpGeneratedSubject(t *testing.T, request api.ReleaseNameRequest) api.UploadSubject {
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
			Category:   request.Category,
			Title:      request.Title,
			Year:       request.Year,
			Resolution: request.Resolution,
		},
	}
}

func dpReviewedName(t *testing.T, subject api.UploadSubject, requested *string) string {
	t.Helper()
	prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker:             "DP",
		Meta:                subject,
		RequestedUploadName: requested,
	}, unit3d.NewWithProfile(Profile()).ReleaseNamePolicy())
	if failure != nil {
		t.Fatal(failure)
	}
	name, err := prepared.ReviewedUploadName()
	if err != nil {
		t.Fatal(err)
	}
	return name
}

func markDPManual(t *testing.T, document *api.ReleaseNameDocument, role api.ReleaseNameRole) {
	markDPComponent(t, document, role, "", true)
}

func markDPComponent(t *testing.T, document *api.ReleaseNameDocument, role api.ReleaseNameRole, value string, manual bool) {
	t.Helper()
	for index := range document.Components {
		if document.Components[index].Role == role {
			if value != "" {
				document.Components[index].Value = value
			}
			document.Components[index].Manual = manual
			return
		}
	}
	t.Fatalf("generated document missing %s", role)
}
