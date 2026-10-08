// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dp

import (
	"errors"
	"slices"
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
		Tracks:                 subject.LanguageFacts.Tracks,
		OriginalLanguages:      []string{"English"},
		OriginalLanguagesKnown: true,
		ProgrammeLanguages:     subject.AudioLanguages,
		ProgrammeStatus:        api.MetadataEvidenceStatusComplete,
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
	if policy.ID != "unit3d/dp/v7" || policy.Structured == nil || policy.Resolver != nil {
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
			markDPComponent(t, subject.GeneratedName, api.NameRoleTitle, "Manual Series")
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
	audio, _ := result.GeneratedName.Component(api.NameRoleAudio)
	facts := api.LanguageFacts{AudioAbsent: audio.Value == ""}
	if audio.Value != "" {
		facts.Tracks = []api.MediaTrackFacts{{
			Kind:         api.MediaTrackAudio,
			Role:         api.AudioRoleProgramme,
			Default:      true,
			DefaultKnown: true,
			Codec:        strings.Fields(audio.Value)[0],
			AudioLabel:   audio.Value,
		}}
	}
	return api.UploadSubject{
		LanguageFacts:    facts,
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
	markDPComponent(t, document, role, "")
}

func markDPComponent(t *testing.T, document *api.ReleaseNameDocument, role api.ReleaseNameRole, value string) {
	t.Helper()
	for index := range document.Components {
		if document.Components[index].Role == role {
			if value != "" {
				document.Components[index].Value = value
			}
			document.Components[index].Manual = true
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
	}{
		{
			name:      "original only",
			original:  "Japanese",
			languages: []string{"Japanese"},
		},
		{
			name:      "original and English",
			original:  "Japanese",
			languages: []string{"Japanese", "English"},
			want:      "Dual-Audio",
		},
		{
			name:      "Nordic original and English keep dual precedence",
			original:  "Swedish",
			languages: []string{"Swedish", "English"},
			want:      "Dual-Audio",
		},
		{
			name:      "foreign original plus two non-English languages",
			original:  "Japanese",
			languages: []string{"Japanese", "German", "Finnish"},
			want:      "MULTi",
		},
		{
			name:      "English original plus two languages",
			original:  "English",
			languages: []string{"English", "German", "French"},
			want:      "MULTi",
		},
		{
			name:      "English dub only",
			original:  "Japanese",
			languages: []string{"English"},
			want:      "Dubbed",
		},
		{
			name:      "Nordic dub only",
			original:  "Japanese",
			languages: []string{"Swedish"},
			want:      "Swedish Dubbed",
		},
		{
			name:      "three including original",
			original:  "Japanese",
			languages: []string{"Japanese", "English", "German"},
			want:      "MULTi",
		},
		{
			name:      "English original plus another",
			original:  "English",
			languages: []string{"English", "Japanese"},
			want:      "Japanese MULTi",
		},
		{
			name:      "foreign original plus another",
			original:  "Japanese",
			languages: []string{"Japanese", "German"},
			want:      "German MULTi",
		},
		{
			name:      "Nordic original plus another Nordic language",
			original:  "Swedish",
			languages: []string{"Swedish", "Finnish"},
			want:      "Finnish MULTi",
		},
		{
			name:      "English dub plus another",
			original:  "Japanese",
			languages: []string{"English", "German"},
			want:      "German MULTi",
		},
		{
			name:      "Nordic dub plus another",
			original:  "Japanese",
			languages: []string{"Swedish", "German"},
			want:      "German MULTi",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := api.UploadSubject{LanguageFacts: api.LanguageFacts{
				OriginalLanguages:      []string{test.original},
				OriginalLanguagesKnown: true,
				ProgrammeLanguages:     test.languages,
				ProgrammeStatus:        api.MetadataEvidenceStatusComplete,
			}}
			for _, languages := range [][]string{test.languages, reversedDPLanguages(test.languages)} {
				subject.LanguageFacts.ProgrammeLanguages = languages
				label, established, err := audioLabelForFacts(subject)
				if err != nil || label != test.want || !established {
					t.Fatalf("languages %v: got %q/%v (error %v), want %q/true", languages, label, established, err, test.want)
				}
			}
			subject.LanguageFacts.ProgrammeStatus = api.MetadataEvidenceStatusPartial
			if label, established, err := audioLabelForFacts(subject); err != nil || label != "" || !established {
				t.Fatalf("partial facts created label or legacy fallback: %q/%v", label, established)
			}
			subject.LanguageFacts.ProgrammeStatus = api.MetadataEvidenceStatusContradictory
			if label, _, err := audioLabelForFacts(subject); err != nil || label != "" {
				t.Fatalf("contradictory facts created %q", label)
			}
		})
	}
}

func TestDPDiscRemuxAndDefaultAudioBoundaries(t *testing.T) {
	t.Parallel()
	base := dpGeneratedSubject(t, api.ReleaseNameRequest{
		Category:    "MOVIE",
		Type:        "WEBDL",
		Source:      "WEB",
		Resolution:  "1080p",
		VideoEncode: "H.265",
		Tag:         "-GRP",
		Title:       "Example",
		Year:        2026,
		Audio:       "Dual-Audio AAC 2.0",
	})
	base.AudioLanguages = []string{"Japanese", "English", "German"}
	base.LanguageFacts = api.LanguageFacts{
		OriginalLanguages:      []string{"Japanese"},
		OriginalLanguagesKnown: true,
		ProgrammeLanguages:     []string{"Japanese", "English"},
		ProgrammeStatus:        api.MetadataEvidenceStatusComplete,
		Tracks: []api.MediaTrackFacts{{
			Kind:         api.MediaTrackAudio,
			Role:         api.AudioRoleProgramme,
			Default:      true,
			DefaultKnown: true,
			Codec:        "DD+",
			AudioLabel:   "DD+ 5.1",
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
		OriginalLanguages:      []string{"English", "French"},
		ProgrammeLanguages:     []string{"French", "German"},
		ProgrammeStatus:        api.MetadataEvidenceStatusComplete,
	}}
	if label, established, err := audioLabelForFacts(subject); err != nil || established || label != "" {
		t.Fatalf("absent English programme audio selected English-original row: %q/%v", label, established)
	}
}

func reversedDPLanguages(languages []string) []string {
	reversed := slices.Clone(languages)
	slices.Reverse(reversed)
	return reversed
}

func TestDPLanguageMultiNameIgnoresPrimaryAndDefaultLanguage(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, original string
		languages      []string
		want           string
	}{
		{"original plus other", "Japanese", []string{"Japanese", "German"}, "German MULTi"},
		{"Nordic original plus other", "Swedish", []string{"Swedish", "Finnish"}, "Finnish MULTi"},
		{"English plus other", "Japanese", []string{"English", "German"}, "German MULTi"},
		{"Nordic plus other", "Japanese", []string{"Swedish", "German"}, "German MULTi"},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, languages := range [][]string{test.languages, reversedDPLanguages(test.languages)} {
				for primary := range 2 {
					for defaultTrack := range 2 {
						subject := dpGeneratedSubject(t, api.ReleaseNameRequest{
							Category:    "MOVIE",
							Type:        "WEBDL",
							Title:       "Example",
							Year:        2026,
							Resolution:  "1080p",
							Source:      "WEB",
							Audio:       "Dual-Audio AAC 2.0",
							VideoEncode: "H.265",
							Tag:         "-GRP",
						})
						subject.AudioLanguages = []string{"Japanese", "English", "German"}
						subject.LanguageFacts = api.LanguageFacts{
							OriginalLanguages:      []string{test.original},
							OriginalLanguagesKnown: true,
							ProgrammeLanguages:     languages,
							ProgrammeStatus:        api.MetadataEvidenceStatusComplete,
							PrimaryAudioTrackID:    languages[primary],
							Tracks: []api.MediaTrackFacts{
								{
									ID:           languages[0],
									Kind:         api.MediaTrackAudio,
									Role:         api.AudioRoleProgramme,
									Languages:    languages[:1],
									Default:      defaultTrack == 0,
									DefaultKnown: true,
									Codec:        "AAC",
									AudioLabel:   "AAC 2.0",
								},
								{
									ID:           languages[1],
									Kind:         api.MediaTrackAudio,
									Role:         api.AudioRoleProgramme,
									Languages:    languages[1:],
									Default:      defaultTrack == 1,
									DefaultKnown: true,
									Codec:        "DD+",
									AudioLabel:   "DD+ 5.1",
								},
								{
									ID:           "commentary",
									DefaultKnown: true,
									Kind:         api.MediaTrackAudio,
									Role:         api.AudioRoleCommentary,
									Languages:    []string{"English"},
								},
							},
						}
						want := "Example 2026 1080p WEB-DL " + test.want + " " + subject.LanguageFacts.Tracks[defaultTrack].AudioLabel + " H.265-GRP"
						if got := dpReviewedName(t, subject, nil); got != want {
							t.Fatalf("languages %v, primary %d, default %d: got %q, want %q", languages, primary, defaultTrack, got, want)
						}
					}
				}
			}
		})
	}
}

func TestDPTwoNordicDubsUsePrimaryProgrammeLanguage(t *testing.T) {
	t.Parallel()
	for _, primary := range []string{"Swedish", "Finnish"} {
		for _, reverseLanguages := range []bool{false, true} {
			for _, reverseTracks := range []bool{false, true} {
				for defaultTrack := range 2 {
					subject := dpNordicDubSubject(t)
					subject.LanguageFacts.PrimaryAudioTrackID = primary
					subject.LanguageFacts.Tracks[0].Default = defaultTrack == 0
					subject.LanguageFacts.Tracks[1].Default = defaultTrack == 1
					audio := subject.LanguageFacts.Tracks[defaultTrack].AudioLabel
					if reverseLanguages {
						slices.Reverse(subject.LanguageFacts.ProgrammeLanguages)
					}
					if reverseTracks {
						slices.Reverse(subject.LanguageFacts.Tracks)
					}
					want := "Example 2026 1080p WEB-DL " + primary + " MULTi " + audio + " H.265-GRP"
					if got := dpReviewedName(t, subject, nil); got != want {
						t.Fatalf("primary %s, default %d, reverse languages %t, reverse tracks %t: got %q, want %q", primary, defaultTrack, reverseLanguages, reverseTracks, got, want)
					}
				}
			}
		}
	}
}

func TestDPTwoNordicDubsRequireUnambiguousPrimaryTrack(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*api.LanguageFacts)
	}{
		{"missing primary", func(f *api.LanguageFacts) { f.PrimaryAudioTrackID = "" }},
		{"unknown primary", func(f *api.LanguageFacts) { f.PrimaryAudioTrackID = "absent" }},
		{"duplicate primary ID", func(f *api.LanguageFacts) { f.Tracks[1].ID = f.Tracks[0].ID }},
		{"non-audio primary", func(f *api.LanguageFacts) { f.Tracks[0].Kind = api.MediaTrackSubtitle }},
		{"commentary primary", func(f *api.LanguageFacts) { f.Tracks[0].Role = api.AudioRoleCommentary }},
		{"unresolved role", func(f *api.LanguageFacts) { f.Tracks[0].Role = "" }},
		{"missing language", func(f *api.LanguageFacts) { f.Tracks[0].Languages = nil }},
		{"ambiguous language", func(f *api.LanguageFacts) { f.Tracks[0].Languages = []string{"Swedish", "Finnish"} }},
		{"conflicting language", func(f *api.LanguageFacts) { f.Tracks[0].Languages = []string{"German"} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := dpNordicDubSubject(t)
			test.mutate(&subject.LanguageFacts)
			_, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
				Tracker: "DP", Meta: subject,
			}, namePolicy())
			var namingFailure *trackers.NameRuleError
			if !errors.As(failure, &namingFailure) || namingFailure.Rule != "primary_audio_language" {
				t.Fatalf("missing unresolved primary naming failure: %#v", failure)
			}
		})
	}
}

func TestDPTwoNordicDubsInvalidateStaleReviewedName(t *testing.T) {
	t.Parallel()
	input, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker: "DP", Meta: dpNordicDubSubject(t),
	}, namePolicy())
	if failure != nil {
		t.Fatal(failure)
	}
	input.Meta.LanguageFacts.PrimaryAudioTrackID = "Finnish"
	if _, failure = trackers.PrepareInputWithReleaseNamePolicy(input, namePolicy()); failure == nil {
		t.Fatal("changed primary track reused the stale reviewed name")
	}
}

func TestDPTwoNordicDubsPreserveManualAndFullDiscNames(t *testing.T) {
	t.Parallel()
	subject := dpNordicDubSubject(t)
	manual := "Manual DP Name-GRP"
	if got := dpReviewedName(t, subject, &manual); got != manual {
		t.Fatalf("manual name changed to %q", got)
	}
	subject.LanguageFacts.PrimaryAudioTrackID = ""
	markDPManual(t, subject.GeneratedName, api.NameRoleDualAudio)
	if got := dpReviewedName(t, subject, nil); !strings.Contains(got, "Dual-Audio AAC 2.0") {
		t.Fatalf("manual language element changed: %q", got)
	}
	subject = dpNordicDubSubject(t)
	subject.LanguageFacts.PrimaryAudioTrackID = ""
	subject.Type, subject.DiscType = "DISC", "BDMV"
	if got := dpReviewedName(t, subject, nil); got != subject.ReleaseName {
		t.Fatalf("full-disc naming changed: %q, want %q", got, subject.ReleaseName)
	}
}

func dpNordicDubSubject(t *testing.T) api.UploadSubject {
	t.Helper()
	subject := dpGeneratedSubject(t, api.ReleaseNameRequest{
		Category:    "MOVIE",
		Type:        "WEBDL",
		Title:       "Example",
		Year:        2026,
		Resolution:  "1080p",
		Source:      "WEB",
		Audio:       "Dual-Audio AAC 2.0",
		VideoEncode: "H.265",
		Tag:         "-GRP",
	})
	subject.LanguageFacts = api.LanguageFacts{
		OriginalLanguages:      []string{"Japanese"},
		OriginalLanguagesKnown: true,
		ProgrammeLanguages:     []string{"Swedish", "Finnish"},
		ProgrammeStatus:        api.MetadataEvidenceStatusComplete,
		PrimaryAudioTrackID:    "Swedish",
		Tracks: []api.MediaTrackFacts{
			{
				ID:           "Swedish",
				Kind:         api.MediaTrackAudio,
				Role:         api.AudioRoleProgramme,
				Languages:    []string{"Swedish"},
				Default:      true,
				DefaultKnown: true,
				Codec:        "AAC",
				AudioLabel:   "AAC 2.0",
			},
			{
				ID:           "Finnish",
				DefaultKnown: true,
				Kind:         api.MediaTrackAudio,
				Role:         api.AudioRoleProgramme,
				Languages:    []string{"Finnish"},
				Codec:        "DD+",
				AudioLabel:   "DD+ 5.1",
			},
		},
	}
	return subject
}
