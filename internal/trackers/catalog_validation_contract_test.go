// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestValidationContractInvalidatesRetainedProjectionCatalog(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(stubDefinition{name: "EXAMPLE"}); err != nil {
		t.Fatal(err)
	}
	projector, err := NewWorkflowProjector(registry, config.Config{}, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	current, err := projector.catalogSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Trackers) != 1 {
		t.Fatalf("unexpected catalog: %+v", current)
	}
	// This fixture's published policy fingerprint predates ValidationContractID.
	// Its site validation and projector versions stay unchanged across the upgrade.
	retained := current
	retained.Trackers = slices.Clone(current.Trackers)
	retained.Trackers[0].PolicyFingerprint = "3cd7ba927a1c921e039489e3d162c10b018658efb70ed258bf7de1b26ecae7ef"
	retained, err = retained.WithFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if valid, err := projector.CatalogCurrent(retained); err != nil || valid {
		t.Fatalf("pre-contract projection catalog was reused: valid=%t err=%v", valid, err)
	}
	if valid, err := projector.CatalogCurrent(current); err != nil || !valid {
		t.Fatalf("current projection catalog was invalidated: valid=%t err=%v", valid, err)
	}
}

func TestGPWPolicyVersionInvalidatesRetainedReadiness(t *testing.T) {
	registry := NewRegistry()
	descriptor := Descriptor{
		Name:       "GPW",
		Definition: stubDefinition{name: "GPW"},
		Validation: ValidationPolicyBinding{ID: "standalone-gpw-constructibility-v1", Check: func(context.Context, api.TrackerValidationSubject, api.Logger) ([]api.RuleFailure, error) {
			return nil, nil
		}},
	}
	if err := registry.RegisterDescriptor(descriptor); err != nil {
		t.Fatal(err)
	}
	old, err := NewWorkflowProjector(registry, config.Config{}, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	retained, err := old.catalogSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	currentRegistry := NewRegistry()
	descriptor.Validation.ID = "standalone-gpw-constructibility-v2"
	if err := currentRegistry.RegisterDescriptor(descriptor); err != nil {
		t.Fatal(err)
	}
	current, err := NewWorkflowProjector(currentRegistry, config.Config{}, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	if valid, err := current.CatalogCurrent(retained); err != nil || valid {
		t.Fatal("v1 GPW readiness retained upload authority after policy change")
	}
}
