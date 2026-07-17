// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package jsondupe

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers/dupe"
)

func TestSearchProjectsAuthenticatedJSONList(t *testing.T) {
	requestErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("imdb") != "1234567" || r.Header.Get("Authorization") != "secret" {
			requestErr <- errors.New("unexpected JSON duplicate request shape")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":9007199254740993,"name":" Example Release ","size":1234}]`))
	}))
	defer server.Close()

	result := Search(context.Background(), server.Client(), ListSpec{
		Endpoint: server.URL,
		Query: url.Values{
			"imdb": {"1234567"},
		},
		Headers:        http.Header{"Authorization": {"secret"}},
		IDField:       "id",
		NameField:     "name",
		SizeField:     "size",
		Link:          func(id string) string { return server.URL + "/" + id },
		FailureMessage: "search failed",
	})
	select {
	case err := <-requestErr:
		t.Fatal(err)
	default:
	}
	if result.Disposition() != dupe.DispositionResolved {
		t.Fatalf("expected resolved result, got %v: %v", result.Disposition(), result.Cause())
	}
	entries := result.Entries()
	if len(entries) != 1 || entries[0].ID != "9007199254740993" || entries[0].Name != "Example Release" {
		t.Fatalf("unexpected entries: %#v", entries)
	}
	if !entries[0].SizeKnown || entries[0].SizeBytes != 1234 {
		t.Fatalf("expected known size, got %#v", entries[0])
	}
}

func TestSearchClassifiesStatusAndParseFailures(t *testing.T) {
	tests := []struct {
		name string
		code int
		body string
		want string
	}{
		{
name: "status",
 code: http.StatusUnauthorized,
 body: `{}`,
 want: dupe.FailureResponseStatus,
},
		{
name: "parse",
 code: http.StatusOK,
 body: `{}`,
 want: dupe.FailureResponseParse,
},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.code)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			result := Search(context.Background(), server.Client(), ListSpec{Endpoint: server.URL, FailureMessage: "safe failure"})
			if result.Disposition() != dupe.DispositionFailed || result.Code() != test.want || result.SafeMessage() != "safe failure" {
				t.Fatalf("unexpected result disposition=%v code=%q message=%q", result.Disposition(), result.Code(), result.SafeMessage())
			}
		})
	}
}
