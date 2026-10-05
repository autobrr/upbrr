// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package spd

import (
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestProjectionQuestionnairePreservesConfiguredChannel(t *testing.T) {
	for _, test := range []struct{ configured, answer, want string }{{"", "", "1"}, {"example-channel", "", "example-channel"}, {"example-channel", "42", "42"}} {
		schema := projectionQuestionnaire(trackers.PreparationInput{TrackerConfig: config.TrackerConfig{Channel: test.configured}, Meta: api.UploadSubject{TrackerQuestionnaireAnswers: map[string]map[string]string{"SPD": {"channel": test.answer}}}})
		if len(schema.Fields) != 1 || schema.Fields[0].Value != test.want {
			t.Fatalf("test=%+v schema=%+v", test, schema)
		}
	}
}

type questionnaireRoundTripper func(*http.Request) (*http.Response, error)

func (f questionnaireRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestRejectedChannelQuestionnaireRequiresCorrection(t *testing.T) {
	previous := http.DefaultTransport
	http.DefaultTransport = questionnaireRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("[]")),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	_, reason, schema := resolveChannel(t.Context(), trackers.PreparationInput{TrackerConfig: config.TrackerConfig{Channel: "unknown-channel"}})
	if reason == "" || schema == nil || len(schema.Fields) != 1 || !schema.Fields[0].Required || schema.Fields[0].Value != "" {
		t.Fatalf("rejected channel appeared validated: %+v reason=%q", schema, reason)
	}
}
