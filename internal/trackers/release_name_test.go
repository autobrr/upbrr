// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"errors"
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
}
