// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package standalone

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

type profileDupeAdapter struct{}

func (profileDupeAdapter) Search(context.Context, api.DuplicateSubject) dupe.AdapterResult {
	return dupe.NotRun(dupe.NotRunManualCheckRequired, "test", nil)
}

func validProfile() Profile {
	return Profile{
		Name:    " dc ",
		BaseURL: "https://tracker.invalid/",
		PrepareDescription: func(context.Context, trackers.PreparationInput) (trackers.DescriptionResult, error) {
			return trackers.DescriptionResult{Group: "dc"}, nil
		},
		PrepareUpload: func(context.Context, trackers.PreparationInput) (trackers.PreparedOperation, error) {
			return trackers.NewPreparedOperation(api.TrackerDryRunEntry{Tracker: "DC"}, nil, nil), nil
		},
		NewDuplicateAdapter: func(dupe.Dependencies) dupe.Adapter { return profileDupeAdapter{} },
	}
}

func TestNewDefinitionNormalizesAndDefensivelyCopiesProfile(t *testing.T) {
	t.Parallel()

	profile := validProfile()
	profile.BannedGroups = []string{"GRP"}
	profile.AuthCapability = &api.TrackerAuthCapability{Notes: []string{"configured"}}
	definition, err := New(profile)
	if err != nil {
		t.Fatalf("new definition: %v", err)
	}
	profile.BannedGroups[0] = "mutated"
	profile.AuthCapability.Notes[0] = "mutated"
	if definition.Name() != "DC" || definition.DefaultBaseURL() != "https://tracker.invalid" || definition.TrackerFamily() != trackers.FamilyStandalone {
		t.Fatalf("definition identity = %q %q %q", definition.Name(), definition.DefaultBaseURL(), definition.TrackerFamily())
	}
	if definition.DescriptionGroup() != "dc" || definition.BannedGroups()[0] != "GRP" {
		t.Fatalf("definition profile = %q %#v", definition.DescriptionGroup(), definition.BannedGroups())
	}
	capability := definition.AuthCapabilityDescriptor()
	if capability == nil || capability.TrackerID != "DC" || capability.Notes[0] != "configured" {
		t.Fatalf("auth capability = %#v", capability)
	}
}

func TestNewDefinitionRejectsMissingRequiredProfileFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Profile)
	}{
		{name: "name", mutate: func(profile *Profile) { profile.Name = "" }},
		{name: "base URL", mutate: func(profile *Profile) { profile.BaseURL = "" }},
		{name: "description", mutate: func(profile *Profile) { profile.PrepareDescription = nil }},
		{name: "upload", mutate: func(profile *Profile) { profile.PrepareUpload = nil }},
		{name: "duplicate", mutate: func(profile *Profile) { profile.NewDuplicateAdapter = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile := validProfile()
			test.mutate(&profile)
			if _, err := New(profile); err == nil {
				t.Fatal("expected profile validation error")
			}
		})
	}
}

func TestCookieAuthCapabilityNormalizesTrackerID(t *testing.T) {
	capability := CookieAuthCapability(" tl ")
	if capability.TrackerID != "TL" || capability.DisplayName != "TL" || capability.AuthKind != "cookies" || !capability.SupportsCookieFile {
		t.Fatalf("unexpected capability: %#v", capability)
	}
}

func TestDefinitionLeavesUndeclaredAuthCapabilityAbsent(t *testing.T) {
	definition := MustNew(validProfile())
	if capability := definition.AuthCapabilityDescriptor(); capability != nil {
		t.Fatalf("expected absent auth capability, got %#v", capability)
	}
}
