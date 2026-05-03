// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveImageHostPolicyTrackerHostOverridesCLIHost(t *testing.T) {
	t.Parallel()

	preferred := "imgbb"
	policy, err := resolveImageHostPolicy("OE", config.TrackerConfig{ImageHost: "ptpimg"}, api.ImageHostOverrides{
		PreferredHost: &preferred,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := preferredHost(policy); got != "ptpimg" {
		t.Fatalf("expected tracker image_host to win, got %q", got)
	}
	if len(policy.allowed) != 1 || policy.allowed[0] != "ptpimg" {
		t.Fatalf("expected hard ptpimg policy, got %#v", policy)
	}
}

func TestResolveImageHostPolicyCLIHostAppliesWhenTrackerHostEmpty(t *testing.T) {
	t.Parallel()

	preferred := "imgbb"
	policy, err := resolveImageHostPolicy("OE", config.TrackerConfig{}, api.ImageHostOverrides{
		PreferredHost: &preferred,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := preferredHost(policy); got != "imgbb" {
		t.Fatalf("expected CLI image host to be preferred, got %q", got)
	}
	if len(policy.allowed) <= 1 {
		t.Fatalf("expected CLI override to preserve allowed fallback hosts, got %#v", policy)
	}
}

func TestResolveImageHostPolicyConfiguredHostForNoPolicyTracker(t *testing.T) {
	t.Parallel()

	policy, err := resolveImageHostPolicy("AITHER", config.TrackerConfig{ImageHost: "imgbox"}, api.ImageHostOverrides{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := preferredHost(policy); got != "imgbox" {
		t.Fatalf("expected configured host imgbox, got %q", got)
	}
	if len(policy.allowed) != 1 || policy.allowed[0] != "imgbox" {
		t.Fatalf("expected configured no-policy tracker host to be required, got %#v", policy)
	}
}

func TestResolveImageHostPolicyRejectsOwnedCLIHostForOtherTracker(t *testing.T) {
	t.Parallel()

	preferred := "hdb"
	_, err := resolveImageHostPolicy("AITHER", config.TrackerConfig{}, api.ImageHostOverrides{
		PreferredHost: &preferred,
	})
	if err == nil {
		t.Fatal("expected owned host override to fail for other tracker")
	}
}

func TestResolveImageHostPolicyAllowsOwnedCLIHostForOwner(t *testing.T) {
	t.Parallel()

	preferred := "hdb"
	policy, err := resolveImageHostPolicy("HDB", config.TrackerConfig{}, api.ImageHostOverrides{
		PreferredHost: &preferred,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := preferredHost(policy); got != "hdb" {
		t.Fatalf("expected owned host for owner tracker, got %q", got)
	}
}
