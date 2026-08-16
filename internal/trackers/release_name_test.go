// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveReleaseNamesDefaultsDuplicateToUpload(t *testing.T) {
	t.Parallel()

	resolved, err := resolveReleaseNames(PreparationInput{
		Meta: api.UploadSubject{ReleaseName: "Example.Release.2026.1080p-GRP"},
	}, CanonicalReleaseNamePolicy())
	if err != nil {
		t.Fatalf("resolve names: %v", err)
	}
	if resolved.Upload != "Example.Release.2026.1080p-GRP" || resolved.Duplicate != resolved.Upload {
		t.Fatalf("resolved names = %#v", resolved)
	}
	if len(resolved.Additional) != 0 {
		t.Fatalf("additional names = %#v", resolved.Additional)
	}
}

func TestResolveReleaseNamesUsesConfiguredMovieYearProvider(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		provider api.IdentityProvider
		wantYear string
	}{
		{
			name:     "TMDB",
			provider: api.IdentityProviderTMDB,
			wantYear: "2026",
		},
		{
			name:     "IMDb",
			provider: api.IdentityProviderIMDB,
			wantYear: "2024",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			resolved, err := resolveReleaseNames(PreparationInput{Meta: api.UploadSubject{
				ReleaseName: "Example Release 2025 1080p-GRP2025",
				Identity:    api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
				Release:     api.ReleaseInfo{Category: "MOVIE", Year: 2025},
				ProviderMetadata: api.SourceScopedMetadata{
					TMDB: &api.TMDBMetadata{Year: 2026},
					IMDB: &api.IMDBMetadata{Year: 2024},
				},
			}}, WithMovieYearProvider(CanonicalReleaseNamePolicy(), test.provider))
			if err != nil {
				t.Fatalf("resolve names: %v", err)
			}
			want := "Example Release " + test.wantYear + " 1080p-GRP2025"
			if resolved.Upload != want || resolved.Duplicate != want {
				t.Fatalf("resolved names = %#v, want %q", resolved, want)
			}
		})
	}
}

func TestResolveReleaseNamesPreservesRequestedMovieYear(t *testing.T) {
	t.Parallel()

	requested := "Example Release 2025 1080p-GRP"
	resolved, err := resolveProjectedReleaseNames(PreparationInput{
		Meta: api.UploadSubject{
			ReleaseName:      requested,
			Identity:         api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
			Release:          api.ReleaseInfo{Category: "MOVIE", Year: 2025},
			ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Year: 2026}},
		},
		RequestedUploadName: &requested,
	}, WithMovieYearProvider(CanonicalReleaseNamePolicy(), api.IdentityProviderTMDB))
	if err != nil {
		t.Fatalf("resolve names: %v", err)
	}
	if resolved.Upload != requested || resolved.Duplicate != "Example Release 2026 1080p-GRP" {
		t.Fatalf("resolved names = %#v", resolved)
	}
}

func TestResolveReleaseNamesNormalizesUnspecifiedEpisodeTitleModeToInclude(t *testing.T) {
	t.Parallel()

	var observed api.ReleaseNameElementPolicy
	binding := NewReleaseNamePolicy("standalone/example/v1", func(input ReleaseNameInput) (ResolvedReleaseNames, error) {
		observed = input.ElementPolicy
		return ResolvedReleaseNames{Upload: input.Subject.ReleaseName}, nil
	})
	_, err := resolveReleaseNames(PreparationInput{
		Meta: api.UploadSubject{ReleaseName: "Example.Show.S01E02.Example.Episode-GRP"},
	}, binding)
	if err != nil {
		t.Fatalf("resolve names: %v", err)
	}
	if observed.Version != api.ReleaseNameElementPolicyVersionV1 || observed.EpisodeTitleMode != api.EpisodeTitleModeInclude {
		t.Fatalf("effective element policy = %#v", observed)
	}
}

func TestResolveReleaseNamesAppliesEpisodeTitleOmitBeforeTrackerFormatting(t *testing.T) {
	t.Parallel()

	binding := WithEpisodeTitleMode(
		SimpleSubjectReleaseNamePolicy("standalone/example/v1", func(subject api.UploadSubject) string {
			return strings.ReplaceAll(subject.ReleaseName, " ", ".")
		}),
		api.EpisodeTitleModeOmit,
	)
	resolved, err := resolveReleaseNames(PreparationInput{
		Meta: api.UploadSubject{
			ReleaseName:      "Example Show S01E02 Example Episode 1080p-GRP",
			ReleaseNameNoTag: "Example Show S01E02 Example Episode 1080p",
			GeneratedReleaseNames: api.GeneratedReleaseNameVariants{
				IncludeEpisodeTitle: api.ReleaseNameVariant{
					Name:      "Example Show S01E02 Example Episode 1080p-GRP",
					NameNoTag: "Example Show S01E02 Example Episode 1080p",
				},
				OmitEpisodeTitle: api.ReleaseNameVariant{
					Name:      "Example Show S01E02 1080p-GRP",
					NameNoTag: "Example Show S01E02 1080p",
				},
			},
		},
	}, binding)
	if err != nil {
		t.Fatalf("resolve names: %v", err)
	}
	if resolved.Upload != "Example.Show.S01E02.1080p-GRP" {
		t.Fatalf("resolved upload name = %q", resolved.Upload)
	}
}

func TestResolveReleaseNamesDoesNotMutateExactOriginalName(t *testing.T) {
	t.Parallel()

	const original = "Example.Show.S01E02.Example.Episode.1080p-GRP"
	binding := WithEpisodeTitleMode(CanonicalReleaseNamePolicy(), api.EpisodeTitleModeOmit)
	resolved, err := resolveReleaseNames(PreparationInput{
		Meta: api.UploadSubject{
			Scene:       true,
			SceneName:   original,
			ReleaseName: original,
			GeneratedReleaseNames: api.GeneratedReleaseNameVariants{
				IncludeEpisodeTitle: api.ReleaseNameVariant{Name: original},
				OmitEpisodeTitle:    api.ReleaseNameVariant{Name: "Example.Show.S01E02.1080p-GRP"},
			},
		},
	}, binding)
	if err != nil {
		t.Fatalf("resolve names: %v", err)
	}
	if resolved.Upload != original {
		t.Fatalf("exact original name mutated to %q", resolved.Upload)
	}
}

func TestResolveReleaseNamesPublishesExplicitSearchName(t *testing.T) {
	t.Parallel()

	binding := NewReleaseNamePolicy("standalone/example/v1", func(ReleaseNameInput) (ResolvedReleaseNames, error) {
		return ResolvedReleaseNames{
			Upload:    "Example.Release.2026.1080p-GRP",
			Duplicate: "Example Release 2026",
		}, nil
	})
	resolved, err := resolveReleaseNames(PreparationInput{}, binding)
	if err != nil {
		t.Fatalf("resolve names: %v", err)
	}
	if !hasReleaseName(resolved.Additional, api.TrackerReleaseNameRoleSearch, "Example Release 2026") {
		t.Fatalf("search name not published: %#v", resolved.Additional)
	}
}

func TestResolveReleaseNamesRejectsInvalidOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
	}{
		{name: "blank", value: "  "},
		{name: "control", value: "Example\nRelease"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := resolveReleaseNames(PreparationInput{}, NewReleaseNamePolicy(
				"standalone/example/v1",
				func(ReleaseNameInput) (ResolvedReleaseNames, error) {
					return ResolvedReleaseNames{Upload: test.value}, nil
				},
			))
			if err == nil {
				t.Fatal("expected invalid release-name error")
			}
		})
	}
}

func TestRequestedReleaseNameIsPolicyInputAndDoesNotMutateCanonicalSubject(t *testing.T) {
	t.Parallel()

	requested := " Example Release 2026 "
	input := PreparationInput{
		Meta: api.UploadSubject{
			ReleaseName:      "Example.Release.2026.ORIGINAL-GRP",
			ReleaseNameNoTag: "Example.Release.2026.ORIGINAL",
		},
		RequestedUploadName: &requested,
	}
	binding := SimpleSubjectReleaseNamePolicy("standalone/example/v1", func(subject api.UploadSubject) string {
		return strings.ReplaceAll(strings.TrimSpace(subject.ReleaseName), " ", ".")
	})
	resolved, err := resolveReleaseNames(input, binding)
	if err != nil {
		t.Fatalf("resolve requested name: %v", err)
	}
	if resolved.Upload != "Example.Release.2026" {
		t.Fatalf("resolved upload name = %q", resolved.Upload)
	}
	if input.Meta.ReleaseName != "Example.Release.2026.ORIGINAL-GRP" ||
		input.Meta.ReleaseNameNoTag != "Example.Release.2026.ORIGINAL" {
		t.Fatalf("canonical subject mutated: %#v", input.Meta)
	}
}

func TestSourceReleaseNameDistinguishesDottedFoldersFromFiles(t *testing.T) {
	t.Parallel()
	const dottedFolder = "Example.Release.2026.2160p.WEB-DL.H.265-GRP"

	tests := []struct {
		name    string
		subject api.UploadSubject
		want    string
	}{
		{
			name: "prepared dotted folder",
			subject: api.UploadSubject{
				SourcePath: filepath.Join("media", dottedFolder),
				VideoPath:  filepath.Join("media", dottedFolder, dottedFolder+".mkv"),
				Filename:   dottedFolder,
			},
			want: dottedFolder,
		},
		{
			name:    "known media extension",
			subject: api.UploadSubject{SourcePath: "C:/media/Example.Release.2026.mkv"},
			want:    "Example.Release.2026",
		},
		{
			name: "parser extension evidence",
			subject: api.UploadSubject{
				SourcePath: "C:/media/Example.Release.2026.custom",
				Release:    api.ReleaseInfo{Ext: "custom"},
			},
			want: "Example.Release.2026",
		},
		{
			name:    "filename fallback",
			subject: api.UploadSubject{Filename: "Example.Release.2026.custom"},
			want:    "Example.Release.2026",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := SourceReleaseName(test.subject); got != test.want {
				t.Fatalf("source release name = %q, want %q", got, test.want)
			}
		})
	}
}

func TestPrepareInputWithReleaseNamePolicyRequiresNonSceneConfirmation(t *testing.T) {
	t.Parallel()

	binding := WithNonSceneReleaseNameConfirmation(CanonicalReleaseNamePolicy())
	input := PreparationInput{
		Tracker: "EXAMPLE",
		Meta:    api.UploadSubject{ReleaseName: "Example.Release.2026-GRP"},
	}
	if _, failure := PrepareInputWithReleaseNamePolicy(input, binding); failure == nil ||
		failure.Code() != releaseNameConfirmationCode {
		t.Fatalf("confirmation failure = %#v", failure)
	}

	confirmed := "Example.Release.2026-GRP"
	input.RequestedUploadName = &confirmed
	prepared, failure := PrepareInputWithReleaseNamePolicy(input, binding)
	if failure != nil {
		t.Fatalf("confirmed preparation: %v", failure)
	}
	if prepared.Projection == nil || prepared.Projection.UploadReleaseName != confirmed {
		t.Fatalf("confirmed projection = %#v", prepared.Projection)
	}
}

func TestPrepareInputWithReleaseNamePolicyRejectsReviewedMismatch(t *testing.T) {
	t.Parallel()

	binding := SimpleSubjectReleaseNamePolicy("standalone/example/v1", func(subject api.UploadSubject) string {
		return strings.ReplaceAll(strings.TrimSpace(subject.ReleaseName), " ", ".")
	})
	input := PreparationInput{
		Tracker: "EXAMPLE",
		Meta:    api.UploadSubject{ReleaseName: "Example Release 2026"},
		Projection: &api.TrackerReleaseProjection{
			TrackerID:            "EXAMPLE",
			UploadReleaseName:    "Different.Release.2026",
			DuplicateCriteria:    api.TrackerDuplicateCriteria{Name: "Different.Search.2026"},
			Readiness:            api.ReadinessStatusReady,
			UploadReady:          true,
			CriteriaFingerprint:  "criteria",
			ProjectorFingerprint: "projector",
		},
	}
	_, failure := PrepareInputWithReleaseNamePolicy(input, binding)
	if failure == nil || failure.Code() != "name_projection_mismatch" {
		t.Fatalf("mismatch failure = %#v", failure)
	}
}

func TestPrepareInputWithReleaseNamePolicyRejectsReviewedAdditionalNameMismatch(t *testing.T) {
	t.Parallel()

	binding := NewReleaseNamePolicy("standalone/example/v1", func(ReleaseNameInput) (ResolvedReleaseNames, error) {
		return ResolvedReleaseNames{
			Upload: "Example.Release.2026-GRP",
			Additional: []api.TrackerReleaseName{{
				Role:  api.TrackerReleaseNameRoleAlternate,
				Value: "Example Release 2026",
			}},
		}, nil
	})
	input := PreparationInput{
		Tracker: "EXAMPLE",
		Meta:    api.UploadSubject{ReleaseName: "Example.Release.2026-GRP"},
		Projection: &api.TrackerReleaseProjection{
			TrackerID:         "EXAMPLE",
			UploadReleaseName: "Example.Release.2026-GRP",
			DuplicateCriteria: api.TrackerDuplicateCriteria{Name: "Example.Release.2026-GRP"},
			Readiness:         api.ReadinessStatusReady,
			UploadReady:       true,
		},
	}
	_, failure := PrepareInputWithReleaseNamePolicy(input, binding)
	if failure == nil || failure.Code() != "name_projection_mismatch" {
		t.Fatalf("additional-name mismatch failure = %#v", failure)
	}
}

func TestPrepareInputWithReleaseNamePolicyRejectsStaleElementPolicy(t *testing.T) {
	t.Parallel()

	const name = "Example.Show.S01E02.1080p-GRP"
	binding := CanonicalReleaseNamePolicy()
	input := PreparationInput{
		Tracker: "EXAMPLE",
		Meta:    api.UploadSubject{ReleaseName: name},
		Projection: &api.TrackerReleaseProjection{
			TrackerID:                  "EXAMPLE",
			UploadReleaseName:          name,
			DuplicateCriteria:          api.TrackerDuplicateCriteria{Name: name},
			NamingElementPolicyVersion: api.ReleaseNameElementPolicyVersionV1,
			EpisodeTitleMode:           api.EpisodeTitleModeOmit,
			Readiness:                  api.ReadinessStatusReady,
			UploadReady:                true,
		},
	}
	_, failure := PrepareInputWithReleaseNamePolicy(input, binding)
	if failure == nil || failure.Code() != "name_projection_mismatch" {
		t.Fatalf("stale element policy failure = %#v", failure)
	}
}

func TestReviewedUploadNameRequiresProjection(t *testing.T) {
	t.Parallel()

	if _, err := (PreparationInput{Meta: api.UploadSubject{ReleaseName: "Example.Release.2026-GRP"}}).ReviewedUploadName(); err == nil {
		t.Fatal("expected missing reviewed projection error")
	}
}

func TestReleaseNameProjectionFingerprintCoversPolicyAndRequestedName(t *testing.T) {
	t.Parallel()

	requested := "Example.Release.2026.REQUESTED-GRP"
	input := PreparationInput{
		Meta:                api.UploadSubject{ReleaseName: "Example.Release.2026-GRP"},
		RequestedUploadName: &requested,
	}
	projection := pureReleaseProjection(input)
	projection.UploadReleaseName = requested
	projection.DuplicateCriteria.Name = requested
	policyFingerprint, err := api.CanonicalWorkflowFingerprint("policy")
	if err != nil {
		t.Fatalf("policy fingerprint: %v", err)
	}
	first, err := releaseNameProjectionFingerprint(Descriptor{
		ProjectorVersion: "standalone-v2",
		ReleaseNamePolicy: NewReleaseNamePolicy(
			"standalone/example/v1",
			func(ReleaseNameInput) (ResolvedReleaseNames, error) {
				return ResolvedReleaseNames{}, errors.New("unused")
			},
		),
	}, input, projection, policyFingerprint)
	if err != nil {
		t.Fatalf("first fingerprint: %v", err)
	}
	secondDescriptor := Descriptor{
		ProjectorVersion: "standalone-v2",
		ReleaseNamePolicy: NewReleaseNamePolicy(
			"standalone/example/v2",
			func(ReleaseNameInput) (ResolvedReleaseNames, error) {
				return ResolvedReleaseNames{}, errors.New("unused")
			},
		),
	}
	second, err := releaseNameProjectionFingerprint(secondDescriptor, input, projection, policyFingerprint)
	if err != nil {
		t.Fatalf("second fingerprint: %v", err)
	}
	if first == second {
		t.Fatal("policy version did not change release-name fingerprint")
	}

	elementChangedDescriptor := Descriptor{
		ProjectorVersion: "standalone-v2",
		ReleaseNamePolicy: WithEpisodeTitleMode(
			NewReleaseNamePolicy(
				"standalone/example/v1",
				func(ReleaseNameInput) (ResolvedReleaseNames, error) {
					return ResolvedReleaseNames{}, errors.New("unused")
				},
			),
			api.EpisodeTitleModeOmit,
		),
	}
	elementChanged, err := releaseNameProjectionFingerprint(elementChangedDescriptor, input, projection, policyFingerprint)
	if err != nil {
		t.Fatalf("element-changed fingerprint: %v", err)
	}
	if first == elementChanged {
		t.Fatal("episode-title mode did not change release-name fingerprint")
	}

	confirmationChangedDescriptor := Descriptor{
		ProjectorVersion: "standalone-v2",
		ReleaseNamePolicy: WithNonSceneReleaseNameConfirmation(NewReleaseNamePolicy(
			"standalone/example/v1",
			func(ReleaseNameInput) (ResolvedReleaseNames, error) {
				return ResolvedReleaseNames{}, errors.New("unused")
			},
		)),
	}
	confirmationChanged, err := releaseNameProjectionFingerprint(confirmationChangedDescriptor, input, projection, policyFingerprint)
	if err != nil {
		t.Fatalf("confirmation-changed fingerprint: %v", err)
	}
	if first == confirmationChanged {
		t.Fatal("confirmation mode did not change release-name fingerprint")
	}
}
