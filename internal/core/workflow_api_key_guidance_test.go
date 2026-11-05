// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/lst"
	"github.com/autobrr/upbrr/pkg/api"
)

type preflightGuidanceLogger struct {
	api.NopLogger
	messages []string
}

func (l *preflightGuidanceLogger) Infof(format string, args ...any) {
	if message := fmt.Sprintf(format, args...); strings.Contains(message, "guidance tracker=") {
		l.messages = append(l.messages, message)
	}
}

func TestPreflightAPIKeyGuidanceIsPassiveAndRefreshesEachUpload(t *testing.T) {
	t.Parallel()
	guidance := lst.Profile().Site.APIKeyGuidance
	registry := trackers.NewRegistry()
	if err := registry.RegisterDescriptor(trackers.Descriptor{
		Name:       "ALPHA",
		Definition: workflowAuthDefinition{name: "ALPHA"},
		AuthPolicy: &trackers.AuthPolicy{APIKeyGuidance: guidance},
	}); err != nil {
		t.Fatal(err)
	}
	catalog, runtime, initial := workflowPreflightFixtures(t)
	logger := &preflightGuidanceLogger{}
	builder := workflowPreflightBuilder{
		auth:     workflowPreflightAuthFake{},
		registry: registry,
		logger:   logger,
		config: config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"ALPHA": {APIKey: "synthetic-key"},
		}}},
	}
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	retained := initial
	for upload := range 2 {
		for range 3 {
			registry.ObserveAPIResponse("ALPHA", "synthetic-key", nil, http.StatusOK)
		}
		assessment, projections, err := builder.Build(t.Context(), api.UploadSubject{}, catalog, runtime, initial, now)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, decision := range projections[0].PolicyDecisions {
			if decision.Code != trackers.APIKeyGuidanceCode {
				continue
			}
			count++
			if decision.Blocking || decision.Disposition != api.RuleDispositionAdvisory || !strings.Contains(decision.Message, "2027-01-01") {
				t.Fatal("missing-header guidance changed advisory semantics")
			}
		}
		if count != 1 || len(projections[0].RequiredActions) != 0 || !projections[0].DupeReady || assessment.Results[0].State != api.TrackerPreflightStateReady {
			t.Fatalf("upload %d: guidance duplicated or changed readiness", upload)
		}
		if len(logger.messages) != upload+1 || !strings.Contains(logger.messages[upload], "2027-01-01") {
			t.Fatal("normal CLI/WebUI logs omitted or duplicated account guidance")
		}
		for _, decision := range projections[1].PolicyDecisions {
			if decision.Code == trackers.APIKeyGuidanceCode {
				t.Fatal("other tracker gained account advice")
			}
		}
		repeated := initial
		repeated.Projections = projections
		if _, _, err := builder.Build(t.Context(), api.UploadSubject{}, catalog, runtime, repeated, now); err != nil {
			t.Fatal(err)
		}
		if len(logger.messages) != upload+1 {
			t.Fatal("repeated preflight logged the same advice again")
		}
		retained = repeated
	}
	beforeRetry := len(logger.messages)
	for _, history := range [][]api.TrackerReleaseProjection{nil, retained.Projections} {
		ctx := api.WithTrackerGuidanceHistory(t.Context(), history)
		if _, _, err := builder.Build(ctx, api.UploadSubject{}, catalog, runtime, retained, now); err != nil {
			t.Fatal(err)
		}
	}
	if len(logger.messages) != beforeRetry+1 {
		t.Fatal("preflight did not distinguish a new retry from feedback with the same cached projection")
	}
	header := make(http.Header)
	header.Set(guidance.Header, "2027-06-01T00:00:00Z")
	registry.ObserveAPIResponse("ALPHA", "synthetic-key", header, http.StatusOK)
	_, projections, err := builder.Build(t.Context(), api.UploadSubject{}, catalog, runtime, retained, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, decision := range projections[0].PolicyDecisions {
		if decision.Code == trackers.APIKeyGuidanceCode {
			t.Fatal("distant expiry retained a stale notice")
		}
	}
	if len(logger.messages) != beforeRetry+1 {
		t.Fatal("distant expiry emitted stale account guidance")
	}
}
