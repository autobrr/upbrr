// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dp

import (
	"strings"
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
	subject.LanguageFacts = api.LanguageFacts{
OriginalLanguages: []string{"English"},
 OriginalLanguagesKnown: true,
 ProgrammeLanguages: subject.AudioLanguages,
 ProgrammeStatus: api.MetadataEvidenceStatusComplete,
}
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
	if policy.ID != "unit3d/dp/v4" || policy.Structured == nil || policy.Resolver != nil {
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

func TestDPApprovedAudioCompositionRows(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, original string
		languages      []string
		want           string
		established    bool
	}{
		{
name: "original only",
 original: "Japanese",
 languages: []string{"Japanese"},
 established: true,
},
		{
name: "original and English",
 original: "Japanese",
 languages: []string{"Japanese", "English"},
 want: "Dual-Audio",
 established: true,
},
		{
name: "English dub only",
 original: "Japanese",
 languages: []string{"English"},
 want: "Dubbed",
 established: true,
},
		{
name: "Nordic dub only",
 original: "Japanese",
 languages: []string{"Swedish"},
 want: "Swedish Dubbed",
 established: true,
},
		{
name: "three including original",
 original: "Japanese",
 languages: []string{"Japanese", "English", "German"},
 want: "MULTi",
 established: true,
},
		{
name: "English original plus another",
 original: "English",
 languages: []string{"English", "Japanese"},
 want: "Japanese MULTi",
 established: true,
},
		{
name: "unspecified two-language row stays deferred",
 original: "Japanese",
 languages: []string{"Japanese", "German"},
},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := api.UploadSubject{LanguageFacts: api.LanguageFacts{
OriginalLanguages: []string{test.original},
 OriginalLanguagesKnown: true,
 ProgrammeLanguages: test.languages,
 ProgrammeStatus: api.MetadataEvidenceStatusComplete,
}}
			label, established := audioLabelForFacts(subject)
			if label != test.want || established != test.established {
				t.Fatalf("got %q/%v, want %q/%v", label, established, test.want, test.established)
			}
			subject.LanguageFacts.ProgrammeStatus = api.MetadataEvidenceStatusPartial
			if label, established := audioLabelForFacts(subject); label != "" || !established {
				t.Fatalf("partial facts created label or legacy fallback: %q/%v", label, established)
			}
			subject.LanguageFacts.ProgrammeStatus = api.MetadataEvidenceStatusContradictory
			if label, _ := audioLabelForFacts(subject); label != "" {
				t.Fatalf("contradictory facts created %q", label)
			}
		})
	}
}

func TestDPDiscRemuxAndDefaultAudioBoundaries(t *testing.T) {
	t.Parallel()
	base := dpGeneratedSubject(t, api.ReleaseNameRequest{
Category: "MOVIE",
 Type: "WEBDL",
 Source: "WEB",
 Resolution: "1080p",
 VideoEncode: "H.265",
 Tag: "-GRP",
 Title: "Example",
 Year: 2026,
 Audio: "Dual-Audio AAC 2.0",
})
	base.AudioLanguages = []string{"Japanese", "English", "German"}
	base.LanguageFacts = api.LanguageFacts{
OriginalLanguages: []string{"Japanese"},
 OriginalLanguagesKnown: true,
 ProgrammeLanguages: []string{"Japanese", "English"},
 ProgrammeStatus: api.MetadataEvidenceStatusComplete,
 Tracks: []api.MediaTrackFacts{{
Kind: api.MediaTrackAudio,
 Role: api.AudioRoleProgramme,
 Default: true,
 AudioLabel: "DD+ 5.1",
}},
}
	base.Type = "DISC"
	if got := dpReviewedName(t, base, nil); !strings.Contains(got, "MULTi AAC 2.0") {
		t.Fatalf("canonical full-disc baseline changed: %q", got)
	}
	base.Type = "REMUX"
	base.DiscType = "BDMV"
	if got := dpReviewedName(t, base, nil); !strings.Contains(got, "Dual-Audio DD+ 5.1") {
		t.Fatalf("disc-sourced remux ignored finalized audio: %q", got)
	}
	base.Type = "DISC"
	if got := dpReviewedName(t, base, nil); !strings.Contains(got, "Dual-Audio AAC 2.0") {
		t.Fatalf("known full-disc baseline changed: %q", got)
	}
}

func TestDPEnglishOriginalRowRequiresEnglishProgrammeAudio(t *testing.T) {
	subject := api.UploadSubject{LanguageFacts: api.LanguageFacts{
OriginalLanguagesKnown: true,
 OriginalLanguages: []string{"English", "French"},
 ProgrammeLanguages: []string{"French", "German"},
 ProgrammeStatus: api.MetadataEvidenceStatusComplete,
}}
	if label, established := audioLabelForFacts(subject); established || label != "" {
		t.Fatalf("absent English programme audio selected English-original row: %q/%v", label, established)
	}
}
