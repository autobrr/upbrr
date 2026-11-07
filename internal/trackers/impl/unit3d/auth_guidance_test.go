// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/submission"
	"github.com/autobrr/upbrr/pkg/api"
)

type guidanceLogger struct {
	api.NopLogger
	messages []string
}

func (l *guidanceLogger) Infof(format string, args ...any) {
	if message := fmt.Sprintf(format, args...); strings.Contains(message, "guidance:") {
		l.messages = append(l.messages, message)
	}
}

func TestAPIKeyGuidanceReporterDeduplicatesWithinEachUpload(t *testing.T) {
	t.Parallel()
	guidance := &trackers.APIKeyGuidance{Header: "X-Api-Key-Expires-At", Message: func(expiry trackers.APIKeyExpiry, _ time.Time) string {
		if !expiry.Reported {
			return "Review the account configuration."
		}
		return "Renew the account key."
	}}
	logger := &guidanceLogger{}
	input := trackers.PreparationInput{
		Tracker:       "EXAMPLE",
		TrackerConfig: config.TrackerConfig{APIKey: "synthetic-key"},
		Logger:        logger,
	}
	for range 2 {
		report := apiKeyGuidanceReporter(t.Context(), input, guidance)
		for range 3 {
			report(nil, http.StatusOK)
		}
	}
	if len(logger.messages) != 2 {
		t.Fatal("repeated responses duplicated advice or later upload lost it")
	}
	input.Projection = &api.TrackerReleaseProjection{TrackerID: "EXAMPLE", PolicyDecisions: []api.TrackerPolicyDecision{{
		Code: trackers.APIKeyGuidanceCode, Message: "Review the account configuration.",
	}}}
	report := apiKeyGuidanceReporter(t.Context(), input, guidance)
	report(nil, 0)
	if len(logger.messages) != 2 {
		t.Fatal("shared projection advice was repeated in upload logs")
	}
	header := make(http.Header)
	header.Set(guidance.Header, "2026-11-20T12:00:00Z")
	for range 3 {
		report(header, http.StatusOK)
	}
	if len(logger.messages) != 3 {
		t.Fatal("newer response guidance was lost or duplicated")
	}
	for _, history := range [][]api.TrackerReleaseProjection{nil, {*input.Projection}} {
		guidance.Observe(input.TrackerConfig.APIKey, nil, http.StatusOK)
		before := len(logger.messages)
		ctx := api.WithTrackerGuidanceHistory(t.Context(), history)
		report := apiKeyGuidanceReporter(ctx, input, guidance)
		for range 3 {
			report(nil, 0)
		}
		want := 0
		if len(history) == 0 {
			want = 1
		}
		if len(logger.messages)-before != want {
			t.Fatal("reporter did not honor current-attempt history over an older projection")
		}
	}
}

func TestUploadResponseCapturesExpiryWithoutChangingSubmissionOutcome(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		status      int
		body        string
		outcome     submission.Outcome
		finalHeader string
	}{
		{
			name:    "accepted",
			status:  http.StatusOK,
			body:    `{"success":true}`,
			outcome: submission.Accepted,
		},
		{
			name:    "rejected with expiry",
			status:  http.StatusBadRequest,
			body:    `{"success":false,"message":"Invalid torrent"}`,
			outcome: submission.Rejected,
		},
		{
			name:    "headerless redirected GET",
			status:  http.StatusFound,
			outcome: submission.Accepted,
		},
		{
			name:        "conflicting redirected GET",
			status:      http.StatusSeeOther,
			outcome:     submission.Accepted,
			finalHeader: "2040-01-01T00:00:00Z",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			guidance := &trackers.APIKeyGuidance{Header: "X-Api-Key-Expires-At", Message: func(expiry trackers.APIKeyExpiry, _ time.Time) string {
				return expiry.ExpiresAt.UTC().Format(time.RFC3339)
			}}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/result" {
					if request.Method != http.MethodGet {
						t.Error("upload redirect did not preserve the existing GET follow-up")
					}
					if tc.finalHeader != "" {
						w.Header().Set(guidance.Header, tc.finalHeader)
					}
					_, _ = w.Write([]byte(`{"success":true}`))
					return
				}
				w.Header().Set(guidance.Header, "2026-11-20T12:00:00Z")
				if tc.status == http.StatusFound || tc.status == http.StatusSeeOther {
					w.Header().Set("Location", "/result")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			logger := &guidanceLogger{}
			report := apiKeyGuidanceReporter(t.Context(), trackers.PreparationInput{
				Tracker:       "EXAMPLE",
				TrackerConfig: config.TrackerConfig{APIKey: "synthetic-key"},
				Logger:        logger,
			}, guidance)
			summary, err := submitUnit3DUpload(t.Context(), "EXAMPLE", "Example.Release.2026-GRP", "synthetic-key", server.URL,
				server.URL, "text/plain", "synthetic-payload", api.UploadSubject{}, t.TempDir(), logger, report)
			if submission.OutcomeOf(err) != tc.outcome || (summary.Uploaded == 1) != (tc.outcome == submission.Accepted) {
				t.Fatal("advisory changed the existing submission result")
			}
			if len(logger.messages) != 1 || !strings.Contains(logger.messages[0], "2026-11-20") {
				t.Fatal("upload response expiry was not presented")
			}
		})
	}
}

func TestAPIKeyGuidanceReporterUsesLatestPostAcrossUploads(t *testing.T) {
	t.Parallel()
	guidance := &trackers.APIKeyGuidance{Header: "X-Api-Key-Expires-At", Message: func(expiry trackers.APIKeyExpiry, _ time.Time) string {
		if !expiry.Reported {
			return "Review the account configuration."
		}
		if expiry.ExpiresAt.IsZero() {
			return "Unreadable expiry."
		}
		return expiry.ExpiresAt.UTC().Format(time.RFC3339)
	}}
	logger := &guidanceLogger{}
	input := trackers.PreparationInput{
		Tracker:       "EXAMPLE",
		TrackerConfig: config.TrackerConfig{APIKey: "synthetic-key"},
		Logger:        logger,
	}
	report := apiKeyGuidanceReporter(t.Context(), input, guidance)
	report(nil, 0)
	if len(logger.messages) != 0 {
		t.Fatal("preparation invented guidance before an upload POST response")
	}
	for _, tc := range []struct {
		value  []string
		status int
		want   string
	}{
		{[]string{"2026-11-20T12:00:00Z"}, http.StatusOK, "2026-11-20T12:00:00Z"},
		{[]string{"2026-11-21T12:00:00Z"}, http.StatusBadRequest, "2026-11-21T12:00:00Z"},
		{nil, http.StatusUnauthorized, "Review the account configuration."},
		{[]string{""}, http.StatusOK, "Unreadable expiry."},
		{[]string{"not-a-date"}, http.StatusBadRequest, "Unreadable expiry."},
	} {
		header := make(http.Header)
		if tc.value != nil {
			header[http.CanonicalHeaderKey(guidance.Header)] = tc.value
		}
		report(header, tc.status)
		if got := guidance.Guidance(input.TrackerConfig.APIKey, time.Now()); got != tc.want {
			t.Fatalf("latest upload POST guidance = %q, want %q", got, tc.want)
		}
	}
	if len(logger.messages) != 4 {
		t.Fatal("changed POST results were lost or repeated")
	}
	// A new upload reuses this key's observation before sending another POST.
	nextUpload := apiKeyGuidanceReporter(t.Context(), input, guidance)
	nextUpload(nil, 0)
	if len(logger.messages) != 5 {
		t.Fatal("same-key upload lost the recorded POST result")
	}
	input.TrackerConfig.APIKey = "synthetic-replacement-key"
	replacement := apiKeyGuidanceReporter(t.Context(), input, guidance)
	replacement(nil, 0)
	withoutPolicy := apiKeyGuidanceReporter(t.Context(), input, nil)
	withoutPolicy(nil, 0)
	if len(logger.messages) != 5 {
		t.Fatal("unsubmitted replacement or tracker without a policy gained guidance")
	}
}
