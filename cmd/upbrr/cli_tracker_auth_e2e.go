// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build e2e

package main

import (
	"context"
	"os"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
	"github.com/autobrr/upbrr/pkg/api"
)

const cliE2EFakeServicesEnv = "UPBRR_E2E_FAKE_SERVICES"

// newCLITrackerAuthService returns the network-free auth seam only when the
// e2e build and fake-services environment gate are both active.
func newCLITrackerAuthService(cfg config.Config, logger api.Logger) cliTrackerAuthService {
	value := strings.TrimSpace(os.Getenv(cliE2EFakeServicesEnv))
	if value == "1" || strings.EqualFold(value, "true") {
		return e2eCLITrackerAuthService{}
	}
	return trackerauth.NewServiceWithLogger(cfg, logger)
}

// e2eCLITrackerAuthService treats tracker auth as configured without network IO.
type e2eCLITrackerAuthService struct{}

// Capabilities exposes no managed tracker-auth workflows to fake-services E2E runs.
func (e2eCLITrackerAuthService) Capabilities(context.Context) ([]api.TrackerAuthCapability, error) {
	return nil, nil
}

// ValidateMany returns configured state without contacting trackers.
func (e2eCLITrackerAuthService) ValidateMany(_ context.Context, trackerIDs []string) ([]api.TrackerAuthStatus, error) {
	statuses := make([]api.TrackerAuthStatus, 0, len(trackerIDs))
	for _, trackerID := range trackerIDs {
		statuses = append(statuses, api.TrackerAuthStatus{TrackerID: strings.ToUpper(strings.TrimSpace(trackerID)), State: trackerauth.StateConfigured})
	}
	return statuses, nil
}

// Submit2FA completes the fake challenge without external IO.
func (e2eCLITrackerAuthService) Submit2FA(context.Context, string, string) (api.TrackerAuthStatus, error) {
	return api.TrackerAuthStatus{State: trackerauth.StateConfigured}, nil
}
