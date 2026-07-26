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
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	cliE2EFakeServicesEnv = "UPBRR_E2E_FAKE_SERVICES"
	cliE2EAuthNeededEnv   = "UPBRR_E2E_AUTH_REQUIRED_TRACKERS"
)

func newCLITrackerAuthService(cfg config.Config, logger api.Logger) cliTrackerAuthService {
	value := strings.TrimSpace(os.Getenv(cliE2EFakeServicesEnv))
	if value == "1" || strings.EqualFold(value, "true") {
		return e2eCLITrackerAuthService{}
	}
	return trackerauth.NewServiceWithRegistryAndLogger(cfg, trackerimpl.MustNewRegistry(), logger)
}

type e2eCLITrackerAuthService struct{}

func (e2eCLITrackerAuthService) Capabilities(context.Context) ([]api.TrackerAuthCapability, error) {
	capabilities := make([]api.TrackerAuthCapability, 0)
	for trackerID := range strings.SplitSeq(os.Getenv(cliE2EAuthNeededEnv), ",") {
		trackerID = strings.ToUpper(strings.TrimSpace(trackerID))
		if trackerID == "" {
			continue
		}
		capabilities = append(capabilities, api.TrackerAuthCapability{
			TrackerID:          trackerID,
			AuthKind:           "cookies",
			SupportsCookieFile: true,
		})
	}
	return capabilities, nil
}

func (e2eCLITrackerAuthService) ValidateMany(_ context.Context, trackerIDs []string) ([]api.TrackerAuthStatus, error) {
	statuses := make([]api.TrackerAuthStatus, 0, len(trackerIDs))
	for _, trackerID := range trackerIDs {
		trackerID = strings.ToUpper(strings.TrimSpace(trackerID))
		status := api.TrackerAuthStatus{TrackerID: trackerID, State: trackerauth.StateConfigured}
		if e2eCLITrackerAuthRequired(trackerID) {
			status.State = trackerauth.StateLoginRequired
			status.Message = "synthetic E2E authentication required"
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func (e2eCLITrackerAuthService) Submit2FA(context.Context, string, string) (api.TrackerAuthStatus, error) {
	return api.TrackerAuthStatus{State: trackerauth.StateConfigured}, nil
}

func e2eCLITrackerAuthRequired(trackerID string) bool {
	for required := range strings.SplitSeq(os.Getenv(cliE2EAuthNeededEnv), ",") {
		if strings.EqualFold(strings.TrimSpace(required), strings.TrimSpace(trackerID)) {
			return true
		}
	}
	return false
}
