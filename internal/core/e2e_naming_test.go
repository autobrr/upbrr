// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build e2e

package core

import (
	"context"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestMaybeApplyE2ENamingRegistryLeavesBaseRegistryUntouched(t *testing.T) {
	t.Setenv(e2eEnabledEnv, "1")
	t.Setenv(e2eNamingModeEnv, "")
	registry := trackerimpl.MustNewRegistry()

	got, err := maybeApplyE2ENamingRegistry(registry)
	if err != nil {
		t.Fatalf("apply e2e naming registry: %v", err)
	}
	if got != registry {
		t.Fatal("base registry was replaced without a naming fixture mode")
	}
	descriptor, ok := got.LookupDescriptor("BTN")
	if !ok || descriptor.ReleaseNamePolicy.ID != "standalone/btn/v5" {
		t.Fatalf("base BTN naming policy = %#v", descriptor.ReleaseNamePolicy)
	}
}

func TestMaybeApplyE2ENamingRegistryRejectsUnknownMode(t *testing.T) {
	t.Setenv(e2eEnabledEnv, "1")
	t.Setenv(e2eNamingModeEnv, "unknown")

	if _, err := maybeApplyE2ENamingRegistry(trackerimpl.MustNewRegistry()); err == nil ||
		!strings.Contains(err.Error(), e2eNamingModeEnv) {
		t.Fatalf("unknown naming mode error = %v", err)
	}
}

func TestMaybeApplyE2ENamingRegistryConfiguresBTNPolicy(t *testing.T) {
	t.Setenv(e2eEnabledEnv, "true")
	t.Setenv(e2eNamingModeEnv, "rebuild")
	registry := trackerimpl.MustNewRegistry()

	fixture, err := maybeApplyE2ENamingRegistry(registry)
	if err != nil {
		t.Fatalf("apply e2e naming registry: %v", err)
	}
	if fixture == registry {
		t.Fatal("fixture registry reused the base registry")
	}
	descriptor, ok := fixture.LookupDescriptor("BTN")
	if !ok {
		t.Fatal("fixture BTN descriptor is missing")
	}
	policy := descriptor.ReleaseNamePolicy
	if policy.ID != "e2e/btn/mandatory-edition/v1" || policy.Confirmation != trackers.ReleaseNameConfirmationNonScene ||
		policy.Structured == nil || policy.Structured.Opaque != trackers.OpaqueNameRebuild {
		t.Fatalf("fixture BTN naming policy = %#v", policy)
	}
	if len(policy.Structured.Authority) != 1 || policy.Structured.Authority[0] !=
		(trackers.NameAuthority{Role: api.NameRoleEdition, Aspect: trackers.NamePresence}) {
		t.Fatalf("fixture BTN naming authority = %#v", policy.Structured.Authority)
	}

	baseBTN, ok := registry.LookupDescriptor("BTN")
	if !ok || baseBTN.ReleaseNamePolicy.ID != "standalone/btn/v5" {
		t.Fatalf("base BTN naming policy changed = %#v", baseBTN.ReleaseNamePolicy)
	}
}

func TestE2EFixtureGeneratedNameIncludesOmittableEdition(t *testing.T) {
	t.Setenv(e2eEnabledEnv, "1")
	t.Setenv(e2eNamingModeEnv, "reject")

	generated, enabled, err := e2eFixtureGeneratedName(api.CategoryMovie, "E2E Movie", "1080p", 0, 0, "")
	if err != nil {
		t.Fatalf("build fixture generated name: %v", err)
	}
	if !enabled {
		t.Fatal("naming fixture was not enabled")
	}
	if got, want := generated.Name, "E2E Movie 2026 Uncut 1080p WEB-DL DD 5.1 H264-UPBRR"; got != want {
		t.Fatalf("fixture generated name = %q, want %q", got, want)
	}
	edition, ok := generated.GeneratedName.Component(api.NameRoleEdition)
	if !ok || !edition.Present || edition.Value != "Uncut" {
		t.Fatalf("fixture edition component = %#v, exists=%t", edition, ok)
	}
}

func TestE2EFixtureGeneratedNameIsDisabledWithoutNamingMode(t *testing.T) {
	t.Setenv(e2eEnabledEnv, "1")
	t.Setenv(e2eNamingModeEnv, "")

	generated, enabled, err := e2eFixtureGeneratedName(api.CategoryMovie, "E2E Movie", "1080p", 0, 0, "")
	if err != nil {
		t.Fatalf("build disabled fixture generated name: %v", err)
	}
	if enabled || generated.Name != "" || generated.GeneratedName != nil {
		t.Fatalf("disabled fixture result = %#v, enabled=%t", generated, enabled)
	}
}

func TestE2EFixtureGeneratedNameSupportsBTNFixtureTVName(t *testing.T) {
	t.Setenv(e2eEnabledEnv, "1")
	t.Setenv(e2eNamingModeEnv, "rebuild")

	generated, enabled, err := e2eFixtureGeneratedName(api.CategoryTV, "E2E Show", "1080p", 1, 1, "Example Episode")
	if err != nil {
		t.Fatalf("build fixture TV generated name: %v", err)
	}
	if !enabled {
		t.Fatal("naming fixture was not enabled")
	}
	if got, want := generated.Name, "E2E Show 2026 S01E01 Example Episode Uncut 1080p WEB-DL DD 5.1 H264-UPBRR"; got != want {
		t.Fatalf("fixture TV generated name = %q, want %q", got, want)
	}
}

func TestE2ENamingRegistryAppliesConfiguredOpaqueNameMode(t *testing.T) {
	for _, test := range []struct {
		name      string
		mode      string
		wantBlock bool
	}{
		{
			name:      "reject",
			mode:      "reject",
			wantBlock: true,
		},
		{name: "rebuild", mode: "rebuild"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(e2eEnabledEnv, "1")
			t.Setenv(e2eNamingModeEnv, test.mode)
			registry, err := maybeApplyE2ENamingRegistry(trackerimpl.MustNewRegistry())
			if err != nil {
				t.Fatalf("apply e2e naming registry: %v", err)
			}
			generated, enabled, err := e2eFixtureGeneratedName(api.CategoryTV, "E2E Show", "1080p", 1, 1, "Example Episode")
			if err != nil || !enabled {
				t.Fatalf("build fixture generated name = %#v, enabled=%t, err=%v", generated, enabled, err)
			}
			fingerprint, err := api.CanonicalWorkflowFingerprint("e2e naming fixture")
			if err != nil {
				t.Fatalf("fixture fingerprint: %v", err)
			}
			opaque := "Opaque Uncut Name-GRP"
			projection, failure := registry.ProjectRelease(context.Background(), trackers.PreparationInput{
				Tracker:             "BTN",
				RequestedUploadName: &opaque,
				Meta: api.UploadSubject{
					SourcePath:    "e2e-fixture",
					ReleaseName:   generated.Name,
					GeneratedName: generated.GeneratedName,
					Release: api.ReleaseInfo{
						Category: "TV",
						Season:   1,
						Episode:  1,
					},
					SeasonInt:  1,
					EpisodeInt: 1,
					Identity: api.ExternalIdentity{
						Category: api.CanonicalCategoryTV,
						TVDBID:   2001,
					},
					ProviderMetadata: api.SourceScopedMetadata{
						SourcePath: "e2e-fixture",
						TVDB:       &api.TVDBMetadata{TVDBID: 2001},
					},
				},
			}, fingerprint, fingerprint, fingerprint)
			if test.wantBlock {
				if failure == nil || projection.Readiness != api.ReadinessStatusBlocked || projection.UploadReleaseName != "" {
					t.Fatalf("reject projection = %#v, failure=%v", projection, failure)
				}
				return
			}
			if failure != nil || projection.UploadReleaseName != "E2E Show 2026 S01E01 Example Episode 1080p WEB-DL DD 5.1 H264-UPBRR" ||
				projection.UploadReady || !projection.DupeReady || len(projection.RequiredActions) != 1 {
				t.Fatalf("rebuilt projection = %#v, failure=%v", projection, failure)
			}
		})
	}
}
