// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestRegistryProjectionAppliesMandatoryComponentAuthority(t *testing.T) {
	t.Parallel()

	manual := structuredSubject()
	for index := range manual.GeneratedName.Components {
		component := &manual.GeneratedName.Components[index]
		if component.Role == api.NameRoleYear || component.Role == api.NameRoleAlternateTitle || component.Role == api.NameRoleEdition || component.Role == api.NameRoleGroup {
			component.Manual = true
		}
		if component.Role == api.NameRoleEdition {
			component.Present = false
		}
	}
	manual.ReleaseName = manual.GeneratedName.Render().Name

	defaults := StructuredReleaseNamePolicy("test/mandatory-authority/defaults/v1", StructuredNamePolicy{
		Defaults: func(editor *NameEditor, _ api.UploadSubject, _ config.TrackerConfig) error {
			if err := editor.Include(api.NameRoleEdition); err != nil {
				return err
			}
			if err := editor.Set(api.NameRoleYear, "2025"); err != nil {
				return err
			}
			return editor.MoveBefore(api.NameRoleAlternateTitle, api.NameRoleYear)
		},
	})
	defaultProjection, failure := projectMandatoryAuthority(t, defaults, manual, nil)
	if failure != nil {
		t.Fatalf("project default policy: %v", failure)
	}
	if defaultProjection.UploadReleaseName != manual.ReleaseName || len(namingOverrideDecisions(defaultProjection.PolicyDecisions)) != 0 {
		t.Fatalf("optional policy changed manual components: %#v", defaultProjection)
	}

	mandatory := StructuredReleaseNamePolicy("test/mandatory-authority/mandatory/v1", StructuredNamePolicy{
		Authority: []NameAuthority{
			{Role: api.NameRoleEdition, Aspect: NamePresence},
			{Role: api.NameRoleYear, Aspect: NameValue},
			{Role: api.NameRoleAlternateTitle, Aspect: NameOrder},
		},
		Mandatory: func(editor *NameEditor, _ api.UploadSubject, _ config.TrackerConfig) error {
			if err := editor.Include(api.NameRoleEdition); err != nil {
				return err
			}
			if err := editor.Set(api.NameRoleYear, "2025"); err != nil {
				return err
			}
			return editor.MoveBefore(api.NameRoleAlternateTitle, api.NameRoleYear)
		},
	})
	mandatoryProjection, failure := projectMandatoryAuthority(t, mandatory, manual, nil)
	if failure != nil {
		t.Fatalf("project mandatory policy: %v", failure)
	}
	const want = "The Uncut Signal AKA Uncut Nights 2025 The Uncut Version Uncut 576p DVD AC3 2.0 x264-GRP"
	if mandatoryProjection.UploadReleaseName != want ||
		mandatoryProjection.DuplicateCriteria.Name != want ||
		mandatoryProjection.NamingFingerprint == defaultProjection.NamingFingerprint {
		t.Fatalf("mandatory projection = %#v", mandatoryProjection)
	}
	notices := namingOverrideDecisions(mandatoryProjection.PolicyDecisions)
	if len(notices) != 3 || notices[0].NamingRole != string(api.NameRoleEdition) ||
		notices[1].NamingRole != string(api.NameRoleYear) || notices[2].NamingRole != string(api.NameRoleAlternateTitle) {
		t.Fatalf("mandatory notices = %#v", notices)
	}
	if !strings.HasSuffix(mandatoryProjection.UploadReleaseName, "x264-GRP") {
		t.Fatalf("unclaimed manual group changed: %q", mandatoryProjection.UploadReleaseName)
	}
}

func TestRegistryProjectionRejectsUnsatisfiedMandatoryAuthority(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		policy    StructuredNamePolicy
		subject   api.UploadSubject
		requested *string
		wantRole  api.ReleaseNameRole
		wantText  string
	}{
		{
			name: "undeclared order",
			policy: StructuredNamePolicy{
				Authority: []NameAuthority{{Role: api.NameRoleYear, Aspect: NameValue}},
				Mandatory: func(editor *NameEditor, _ api.UploadSubject, _ config.TrackerConfig) error {
					return editor.MoveBefore(api.NameRoleAlternateTitle, api.NameRoleYear)
				},
			},
			subject:  structuredSubject(),
			wantRole: api.NameRoleAlternateTitle,
			wantText: "not declared mandatory",
		},
		{
			name: "missing anchor",
			policy: StructuredNamePolicy{
				Authority: []NameAuthority{{Role: api.NameRoleAlternateTitle, Aspect: NameOrder}},
				Mandatory: func(editor *NameEditor, _ api.UploadSubject, _ config.TrackerConfig) error {
					return editor.MoveBefore(api.NameRoleAlternateTitle, api.NameRoleYear)
				},
			},
			subject:  mandatoryAuthorityWithoutYear(t),
			wantRole: api.NameRoleAlternateTitle,
			wantText: "target or anchor is absent",
		},
		{
			name: "opaque reject",
			policy: StructuredNamePolicy{
				Authority: []NameAuthority{{Role: api.NameRoleEdition, Aspect: NamePresence}},
				Mandatory: func(editor *NameEditor, _ api.UploadSubject, _ config.TrackerConfig) error {
					return editor.Omit(api.NameRoleEdition)
				},
			},
			subject:   structuredSubject(),
			requested: new("Opaque Name-GRP"),
			wantText:  "clear the name override and reprepare",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			binding := StructuredReleaseNamePolicy("test/mandatory-authority/reject/v1", test.policy)
			projection, failure := projectMandatoryAuthority(t, binding, test.subject, test.requested)
			var rule *NameRuleError
			if failure == nil || failure.Code() != "name_rule_unsatisfied" || !errors.As(failure, &rule) ||
				rule.Role != test.wantRole || !strings.Contains(rule.Reason, test.wantText) ||
				projection.Readiness != api.ReadinessStatusBlocked || projection.DupeReady || projection.UploadReady {
				t.Fatalf("unsatisfied authority = projection=%#v failure=%v rule=%#v", projection, failure, rule)
			}
		})
	}

	rebuild := StructuredReleaseNamePolicy("test/mandatory-authority/rebuild/v1", StructuredNamePolicy{
		Opaque:    OpaqueNameRebuild,
		Authority: []NameAuthority{{Role: api.NameRoleEdition, Aspect: NamePresence}},
		Mandatory: func(editor *NameEditor, _ api.UploadSubject, _ config.TrackerConfig) error {
			return editor.Omit(api.NameRoleEdition)
		},
	})
	requested := "Opaque Name-GRP"
	projection, failure := projectMandatoryAuthority(t, rebuild, structuredSubject(), &requested)
	automatic, automaticFailure := projectMandatoryAuthority(t, rebuild, structuredSubject(), nil)
	if failure != nil || projection.UploadReleaseName == requested || projection.NamingFingerprint == "" ||
		automaticFailure != nil || projection.NamingFingerprint == automatic.NamingFingerprint ||
		!hasNamingDecision(projection.PolicyDecisions, "rebuilt", "name") {
		t.Fatalf("opaque rebuild = projection=%#v failure=%v automatic=%#v automatic_failure=%v", projection, failure, automatic, automaticFailure)
	}
}

func TestRegistryProjectionMandatoryProviderYearRequiresCurrentTMDB(t *testing.T) {
	t.Parallel()

	policy := WithMovieYearProvider(StructuredReleaseNamePolicy("test/mandatory-authority/provider-year/v1", StructuredNamePolicy{
		Authority: []NameAuthority{{Role: api.NameRoleYear, Aspect: NameValue}},
		Mandatory: func(editor *NameEditor, subject api.UploadSubject, _ config.TrackerConfig) error {
			if !subject.ProviderMetadata.IsCurrentFor(subject.SourcePath, subject.Identity) {
				return &NameRuleError{
					Rule:   "test/mandatory-authority/provider-year/v1",
					Role:   api.NameRoleYear,
					Reason: "TMDB metadata is stale; reprepare the release",
				}
			}
			metadata := subject.ProviderMetadata.TMDB
			if metadata == nil || metadata.Year <= 0 || (subject.Identity.TMDBID > 0 && metadata.TMDBID != subject.Identity.TMDBID) {
				return &NameRuleError{
					Rule:   "test/mandatory-authority/provider-year/v1",
					Role:   api.NameRoleYear,
					Reason: "current TMDB year is unavailable",
				}
			}
			return editor.Set(api.NameRoleYear, strconv.Itoa(metadata.Year))
		},
	}), api.IdentityProviderTMDB)

	subject := mandatoryAuthorityProviderYearSubject(t)
	projection, failure := projectMandatoryAuthority(t, policy, subject, nil)
	const want = "The 2026 Signal 2025 AKA 2026 Nights The Uncut Version Uncut 576p DVD AC3 2.0 x264-GRP"
	if failure != nil || projection.UploadReleaseName != want ||
		!hasNamingDecision(projection.PolicyDecisions, "enforced", string(api.NameRoleYear)) {
		t.Fatalf("current provider year = projection=%#v failure=%v", projection, failure)
	}

	for _, test := range []struct {
		name string
		edit func(*api.UploadSubject)
		want string
	}{
		{
			name: "stale generation",
			edit: func(subject *api.UploadSubject) {
				subject.ProviderMetadata.Generation--
			},
			want: "stale",
		},
		{
			name: "missing provider",
			edit: func(subject *api.UploadSubject) {
				subject.ProviderMetadata.TMDB = nil
			},
			want: "unavailable",
		},
		{
			name: "mismatched provider identity",
			edit: func(subject *api.UploadSubject) {
				subject.ProviderMetadata.TMDB.TMDBID++
			},
			want: "unavailable",
		},
		{
			name: "missing year",
			edit: func(subject *api.UploadSubject) {
				subject.ProviderMetadata.TMDB.Year = 0
			},
			want: "unavailable",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			failed := mandatoryAuthorityProviderYearSubject(t)
			test.edit(&failed)
			_, failure := projectMandatoryAuthority(t, policy, failed, nil)
			var rule *NameRuleError
			if failure == nil || !errors.As(failure, &rule) || rule.Role != api.NameRoleYear || !strings.Contains(rule.Reason, test.want) {
				t.Fatalf("provider failure = %v, rule=%#v", failure, rule)
			}
		})
	}
}

func projectMandatoryAuthority(
	t *testing.T,
	policy ReleaseNamePolicyBinding,
	subject api.UploadSubject,
	requested *string,
) (api.TrackerReleaseProjection, *PreparationFailure) {
	t.Helper()
	registry := NewRegistry()
	if err := registry.RegisterDescriptor(Descriptor{
		Name:              "AUTHORITY",
		Definition:        stubDefinition{name: "AUTHORITY"},
		Family:            FamilyStandalone,
		ReleaseNamePolicy: policy,
	}); err != nil {
		t.Fatalf("register synthetic authority policy: %v", err)
	}
	fingerprint := mustProjectionFingerprint(t, "mandatory-authority")
	return registry.ProjectRelease(t.Context(), PreparationInput{
		Tracker:             "AUTHORITY",
		Meta:                subject,
		RequestedUploadName: requested,
	}, fingerprint, fingerprint, fingerprint)
}

func mandatoryAuthorityWithoutYear(t *testing.T) api.UploadSubject {
	t.Helper()
	subject := structuredSubject()
	for index := range subject.GeneratedName.Components {
		if subject.GeneratedName.Components[index].Role == api.NameRoleYear {
			subject.GeneratedName.Components[index].Present = false
		}
	}
	subject.ReleaseName = subject.GeneratedName.Render().Name
	return subject
}

func mandatoryAuthorityProviderYearSubject(t *testing.T) api.UploadSubject {
	t.Helper()
	subject := structuredSubject()
	subject.SourcePath = filepath.Join(t.TempDir(), "The.Uncut.Signal")
	subject.Identity = api.ExternalIdentity{
		SourcePath: subject.SourcePath,
		Generation: 7,
		TMDBID:     4242,
		IMDBID:     1717,
		Category:   api.CanonicalCategoryMovie,
	}
	subject.Release.Year = 2026
	subject.ProviderMetadata = api.SourceScopedMetadata{
		SourcePath: subject.SourcePath,
		Generation: 7,
		TMDB:       &api.TMDBMetadata{TMDBID: 4242, Year: 2025},
		IMDB:       &api.IMDBMetadata{IMDBID: 1717, Year: 2024},
	}
	for index := range subject.GeneratedName.Components {
		component := &subject.GeneratedName.Components[index]
		if component.Role == api.NameRoleTitle {
			component.Value = "The 2026 Signal"
		}
		if component.Role == api.NameRoleAlternateTitle {
			component.Value = "AKA 2026 Nights"
		}
		if component.Role == api.NameRoleYear {
			component.Manual = true
		}
	}
	subject.ReleaseName = subject.GeneratedName.Render().Name
	return subject
}

func hasNamingDecision(decisions []api.TrackerPolicyDecision, decision string, role string) bool {
	for _, candidate := range namingOverrideDecisions(decisions) {
		if candidate.Decision == decision && candidate.NamingRole == role {
			return true
		}
	}
	return false
}
