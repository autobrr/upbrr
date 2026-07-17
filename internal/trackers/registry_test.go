// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

type stubDefinition struct {
	name string
}

type stubAuthDefinition struct{ stubDefinition }

func (stubAuthDefinition) AuthSessionResolver() AuthSessionResolver {
	return func(context.Context, config.TrackerConfig, string, api.TrackerAuthLoginRequest) error { return nil }
}

func (s stubDefinition) Name() string {
	return s.name
}

func (stubDefinition) UploadContentMode() UploadContentMode { return UploadContentModeDescription }

func (stubDefinition) DefaultBaseURL() string { return "https://tracker.example.invalid" }

func testTrackerFamily(name string) Family {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "AITHER", "BLU", "HHD", "LST", "OE", "RF", "RHD", "STC":
		return FamilyUnit3D
	default:
		return FamilyStandalone
	}
}

func testImageHostPolicyForTracker(name string) *ImageHostPolicy {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "GPW":
		return &ImageHostPolicy{AllowedHosts: []string{"kshare", "pixhost", "pterclub", "ilikeshots", "imgbox"}}
	case "HDB":
		return &ImageHostPolicy{
			AllowedHosts:         []string{"hdb"},
			OwnedHosts:           []string{"hdb"},
			DisableWithoutRehost: true,
		}
	case "LST":
		return &ImageHostPolicy{
			ConditionalHost:   "lostimg",
			OwnedHosts:        []string{"lostimg"},
			EnableWithLostimg: true,
		}
	case "MTV":
		return &ImageHostPolicy{AllowedHosts: []string{"imgbox", "imgbb"}}
	case "OE":
		return &ImageHostPolicy{AllowedHosts: []string{"imgbox", "imgbb", "onlyimage", "ptscreens", "passtheimage"}}
	case "PTP":
		return &ImageHostPolicy{AllowedHosts: []string{"pixhost", "imgbb", "onlyimage", "ptscreens", "passtheimage"}}
	case "RF":
		return &ImageHostPolicy{
			ConditionalHost:      "reelflix",
			OwnedHosts:           []string{"reelflix"},
			EnableWhenConfigured: true,
		}
	case "STC":
		return &ImageHostPolicy{AllowedHosts: []string{"imgbox", "imgbb"}}
	default:
		return nil
	}
}

func (s stubDefinition) Prepare(ctx context.Context, input PreparationInput) (TrackerPlan, *PreparationFailure) {
	return prepareTestDefinition(ctx, input, s)
}

//nolint:unparam // Error is required by the adapter submission callback contract.
func (s stubDefinition) submit(context.Context, PreparationInput) (api.UploadSummary, error) {
	return api.UploadSummary{Uploaded: 1}, nil
}

func TestRegistryRegisterLookup(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(stubDefinition{name: "Blu"}); err != nil {
		t.Fatalf("unexpected register error: %v", err)
	}
	if _, ok := registry.Lookup("BLU"); !ok {
		t.Fatalf("expected lookup to succeed")
	}
	if _, ok := registry.Lookup("blu"); !ok {
		t.Fatalf("expected lookup to be case-insensitive")
	}
}

func TestRegistryRegistersAuthResolver(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(stubAuthDefinition{stubDefinition{name: "AUTH"}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, ok := registry.LookupAuthSessionResolver("auth"); !ok {
		t.Fatal("expected auth resolver")
	}
}

func TestRegistryRegisterDuplicate(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(stubDefinition{name: "BLU"}); err != nil {
		t.Fatalf("unexpected register error: %v", err)
	}
	if err := registry.Register(stubDefinition{name: "blu"}); err == nil {
		t.Fatalf("expected duplicate register error")
	}
}

func TestRegistryRegisterDescriptorRejectsMismatchedName(t *testing.T) {
	registry := NewRegistry()
	err := registry.RegisterDescriptor(Descriptor{Name: "BLU", Definition: stubDefinition{name: "AITHER"}})
	if err == nil {
		t.Fatal("expected mismatched descriptor name error")
	}
}

func TestRegistryDiscoversCapabilitiesAndSortsNames(t *testing.T) {
	registry := NewRegistry()
	for _, definition := range []Definition{
		stubDefinition{name: "ZNTH"},
		stubDefinition{name: "BLU"},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	if got, want := registry.Names(), []string{"BLU", "ZNTH"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
}

func TestRegistryRuleCapability(t *testing.T) {
	registry := NewRegistry()
	rules := RuleSet{RequireMovieOnly: true}
	if err := registry.RegisterDescriptor(Descriptor{
		Name:       "BLU",
		Definition: stubDefinition{name: "BLU"},
		Rules:      &rules,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	got, ok := registry.LookupRules("blu")
	if !ok || !got.RequireMovieOnly {
		t.Fatalf("rules = %#v, ok=%t", got, ok)
	}
}
