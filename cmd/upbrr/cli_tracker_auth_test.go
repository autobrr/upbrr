// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"context"
	"strings"
	"testing"

	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
	"github.com/autobrr/upbrr/pkg/api"
)

type cliTrackerAuthStub struct {
	capabilities       []api.TrackerAuthCapability
	statuses           map[string]api.TrackerAuthStatus
	submitStatus       api.TrackerAuthStatus
	submittedChallenge string
	submittedCode      string
}

func (s *cliTrackerAuthStub) Capabilities(context.Context) ([]api.TrackerAuthCapability, error) {
	return append([]api.TrackerAuthCapability(nil), s.capabilities...), nil
}

func (s *cliTrackerAuthStub) ValidateMany(_ context.Context, trackerIDs []string) ([]api.TrackerAuthStatus, error) {
	statuses := make([]api.TrackerAuthStatus, 0, len(trackerIDs))
	for _, trackerID := range trackerIDs {
		name := strings.ToUpper(strings.TrimSpace(trackerID))
		status, ok := s.statuses[name]
		if !ok {
			status = api.TrackerAuthStatus{TrackerID: name, State: trackerauth.StateConfigured}
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func (s *cliTrackerAuthStub) Submit2FA(_ context.Context, challengeID string, code string) (api.TrackerAuthStatus, error) {
	s.submittedChallenge = challengeID
	s.submittedCode = code
	return s.submitStatus, nil
}

func TestCLIWorkflowAuthCompletesManualTwoFactorAction(t *testing.T) {
	t.Parallel()
	service := &cliTrackerAuthStub{
		capabilities: []api.TrackerAuthCapability{{
			TrackerID:         "EXAMPLE",
			SupportsLogin:     true,
			SupportsManual2FA: true,
		}},
		statuses: map[string]api.TrackerAuthStatus{
			"EXAMPLE": {
				TrackerID:   "EXAMPLE",
				State:       trackerauth.StateLoginRequired,
				Needs2FA:    true,
				ChallengeID: "challenge-1",
				Message:     "2FA required",
			},
		},
		submitStatus: api.TrackerAuthStatus{TrackerID: "EXAMPLE", State: trackerauth.StateConfigured},
	}
	ready, err := ensureCLITrackerAuthBeforeDupeCheckWithServiceAndLogger(
		context.Background(),
		bufio.NewReader(strings.NewReader("123456\n")),
		service,
		api.InteractionModeInteractive,
		[]string{"EXAMPLE"},
		api.MetadataPreview{},
		api.NopLogger{},
	)
	if err != nil {
		t.Fatalf("complete tracker auth: %v", err)
	}
	if len(ready) != 1 || ready[0] != "EXAMPLE" {
		t.Fatalf("ready trackers = %#v", ready)
	}
	if service.submittedChallenge != "challenge-1" || service.submittedCode != "123456" {
		t.Fatalf("submitted challenge/code = %q/%q", service.submittedChallenge, service.submittedCode)
	}
}

func TestCLIWorkflowAuthSkipsPromptInUnattendedMode(t *testing.T) {
	t.Parallel()
	service := &cliTrackerAuthStub{
		capabilities: []api.TrackerAuthCapability{{
			TrackerID:         "EXAMPLE",
			SupportsLogin:     true,
			SupportsManual2FA: true,
		}},
		statuses: map[string]api.TrackerAuthStatus{
			"EXAMPLE": {
				TrackerID:   "EXAMPLE",
				State:       trackerauth.StateLoginRequired,
				Needs2FA:    true,
				ChallengeID: "challenge-1",
				Message:     "2FA required",
			},
		},
	}
	ready, err := ensureCLITrackerAuthBeforeDupeCheckWithServiceAndLogger(
		context.Background(),
		bufio.NewReader(strings.NewReader("")),
		service,
		api.InteractionModeUnattended,
		[]string{"EXAMPLE"},
		api.MetadataPreview{},
		api.NopLogger{},
	)
	if err != nil {
		t.Fatalf("unattended tracker auth: %v", err)
	}
	if len(ready) != 0 || service.submittedChallenge != "" || service.submittedCode != "" {
		t.Fatalf("unattended tracker auth prompted or retained tracker: ready=%#v challenge=%q code=%q", ready, service.submittedChallenge, service.submittedCode)
	}
}

func TestCLIWorkflowAuthRedactsStatusDetails(t *testing.T) {
	t.Parallel()
	message := cliTrackerAuthStatusMessage(api.TrackerAuthStatus{
		Message:   "stored session invalid",
		LastError: `remote validation failed: {"api_key":"secret-token"}`,
	})
	if strings.Contains(message, "secret-token") || !strings.Contains(message, "[REDACTED]") {
		t.Fatalf("status message was not redacted: %q", message)
	}
}
