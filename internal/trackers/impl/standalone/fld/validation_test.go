// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fld

import (
	"context"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestValidationPolicyRequiresProviderID(t *testing.T) {
	t.Parallel()

	policy := validationPolicy()

	// Missing both
	failures, err := policy.Check(context.Background(), api.TrackerValidationSubject{
		Tracker:            "FLD",
		MediaInfoTextReady: true,
	}, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(failures) == 0 {
		t.Fatal("expected failure for missing both IMDb and TMDb ID")
	}
	hasProviderFail := false
	for _, f := range failures {
		if f.Rule == "required_provider_id" {
			hasProviderFail = true
		}
	}
	if !hasProviderFail {
		t.Fatalf("expected required_provider_id rule failure, got %#v", failures)
	}

	// Having TMDb ID
	failures, err = policy.Check(context.Background(), api.TrackerValidationSubject{
		Tracker:            "FLD",
		Identity:           api.ExternalIdentity{TMDBID: 123},
		MediaInfoTextReady: true,
	}, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, f := range failures {
		if f.Rule == "required_provider_id" {
			t.Fatalf("did not expect required_provider_id failure when TMDB ID is present: %#v", f)
		}
	}

	// Having IMDb ID
	failures, err = policy.Check(context.Background(), api.TrackerValidationSubject{
		Tracker:            "FLD",
		Identity:           api.ExternalIdentity{IMDBID: 456},
		MediaInfoTextReady: true,
	}, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, f := range failures {
		if f.Rule == "required_provider_id" {
			t.Fatalf("did not expect required_provider_id failure when IMDb ID is present: %#v", f)
		}
	}
}

func TestValidationPolicyRequiresPreparedMedia(t *testing.T) {
	t.Parallel()

	policy := validationPolicy()
	failures, err := policy.Check(context.Background(), api.TrackerValidationSubject{
		Tracker:            "FLD",
		Identity:           api.ExternalIdentity{TMDBID: 123},
		MediaInfoTextReady: false,
	}, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hasMediaFail := false
	for _, f := range failures {
		if f.Rule == "prepared_media_missing" {
			hasMediaFail = true
		}
	}
	if !hasMediaFail {
		t.Fatalf("expected prepared_media_missing failure, got %#v", failures)
	}
}

func TestValidationPolicyContainerRules(t *testing.T) {
	t.Parallel()

	policy := validationPolicy()

	// AVI disallowed
	failures, err := policy.Check(context.Background(), api.TrackerValidationSubject{
		Tracker:            "FLD",
		Identity:           api.ExternalIdentity{TMDBID: 123},
		MediaInfoTextReady: true,
		Container:          "avi",
		Release:            api.ReleaseInfo{Resolution: "1080p"},
	}, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hasContainerFail := false
	for _, f := range failures {
		if f.Rule == "disallowed_content" && strings.Contains(f.Reason, "container") {
			hasContainerFail = true
		}
	}
	if !hasContainerFail {
		t.Fatalf("expected container failure for avi, got %#v", failures)
	}

	// MKV allowed
	failures, err = policy.Check(context.Background(), api.TrackerValidationSubject{
		Tracker:            "FLD",
		Identity:           api.ExternalIdentity{TMDBID: 123},
		MediaInfoTextReady: true,
		Container:          "mkv",
		Release:            api.ReleaseInfo{Resolution: "1080p"},
	}, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, f := range failures {
		if f.Rule == "disallowed_content" && strings.Contains(f.Reason, "container") {
			t.Fatalf("did not expect container failure for mkv: %#v", f)
		}
	}

	// TS allowed for HDTV
	failures, err = policy.Check(context.Background(), api.TrackerValidationSubject{
		Tracker:            "FLD",
		Identity:           api.ExternalIdentity{TMDBID: 123},
		MediaInfoTextReady: true,
		Container:          "ts",
		Type:               "HDTV",
		Release:            api.ReleaseInfo{Resolution: "1080p"},
	}, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, f := range failures {
		if f.Rule == "disallowed_content" && strings.Contains(f.Reason, "container") {
			t.Fatalf("did not expect container failure for ts with HDTV: %#v", f)
		}
	}
}

func TestValidationPolicyResolutionRules(t *testing.T) {
	t.Parallel()

	policy := validationPolicy()

	// 720p disallowed
	failures, err := policy.Check(context.Background(), api.TrackerValidationSubject{
		Tracker:            "FLD",
		Identity:           api.ExternalIdentity{TMDBID: 123},
		MediaInfoTextReady: true,
		Container:          "mkv",
		Release:            api.ReleaseInfo{Resolution: "720p"},
	}, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hasResFail := false
	for _, f := range failures {
		if f.Rule == "disallowed_content" && strings.Contains(f.Reason, "resolution") {
			hasResFail = true
		}
	}
	if !hasResFail {
		t.Fatalf("expected resolution failure for 720p, got %#v", failures)
	}

	// 1080p allowed
	failures, err = policy.Check(context.Background(), api.TrackerValidationSubject{
		Tracker:            "FLD",
		Identity:           api.ExternalIdentity{TMDBID: 123},
		MediaInfoTextReady: true,
		Container:          "mkv",
		Release:            api.ReleaseInfo{Resolution: "1080p"},
	}, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, f := range failures {
		if f.Rule == "disallowed_content" {
			t.Fatalf("did not expect disallowed_content failure for 1080p: %#v", f)
		}
	}
}

func TestValidationPolicyDiscTypeBypassesContentRules(t *testing.T) {
	t.Parallel()

	policy := validationPolicy()

	failures, err := policy.Check(context.Background(), api.TrackerValidationSubject{
		Tracker:            "FLD",
		Identity:           api.ExternalIdentity{TMDBID: 123},
		DiscType:           "DVD",
		DVDVOBMediaInfoReady: true,
		Release:            api.ReleaseInfo{Resolution: "480p"},
	}, api.NopLogger{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, f := range failures {
		if f.Rule == "disallowed_content" {
			t.Fatalf("did not expect disallowed_content failure for DVD disc: %#v", f)
		}
	}
}
