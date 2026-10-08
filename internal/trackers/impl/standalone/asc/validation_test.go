// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func validationTestMovie() api.UploadSubject {
	meta := payloadTestMovie()
	meta.ProviderMetadata.TMDB.Overview = "Overview"
	meta.ProviderMetadata.TMDB.Poster = "https://image.example/poster.jpg"
	return meta
}

func TestValidationPolicyRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*api.UploadSubject)
		want   string
	}{
		{"valid", func(*api.UploadSubject) {}, ""},
		{"unsupported type", func(m *api.UploadSubject) { m.Type = "MYSTERY" }, "unsupported_type"},
		{"missing container", func(m *api.UploadSubject) { m.Container = "" }, "required_container"},
		{"genre unknown to ASC", func(m *api.UploadSubject) { m.ProviderMetadata.TMDB.Genres = "Desconhecido" }, "required_genre"},
		{"anime without IMDb is allowed", func(m *api.UploadSubject) { m.Anime = true; m.Identity.IMDBID = 0 }, ""},
		{"movie without IMDb", func(m *api.UploadSubject) { m.Identity.IMDBID = 0 }, "required_provider_id"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			meta := validationTestMovie()
			tc.mutate(&meta)
			failures, err := validationPolicy().Check(t.Context(), api.NewTrackerValidationSubject(meta, "ASC"), api.NopLogger{})
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			codes := make([]string, 0, len(failures))
			for _, failure := range failures {
				codes = append(codes, failure.Rule)
			}
			if tc.want == "" && len(codes) != 0 {
				t.Fatalf("unexpected failures %v", codes)
			}
			if tc.want != "" && !slices.Contains(codes, tc.want) {
				t.Fatalf("failures %v, want %s", codes, tc.want)
			}
		})
	}
}

func TestValidatePayloadFieldsBlocksMissingFacts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*api.UploadSubject)
		want   string
	}{
		{"missing year", func(m *api.UploadSubject) { m.Release.Year = 0 }, "missing year"},
		{"missing resolution", func(m *api.UploadSubject) { m.Release.Resolution = "" }, "missing video resolution"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			meta := validationTestMovie()
			tc.mutate(&meta)
			payload := buildPayload(meta, config.TrackerConfig{}, "desc", "Example Movie", "General report")
			if got := validatePayloadFields(meta, payload, 2, "https://image.example/poster.jpg"); got != tc.want {
				t.Fatalf("reason = %q, want %q", got, tc.want)
			}
		})
	}
}
