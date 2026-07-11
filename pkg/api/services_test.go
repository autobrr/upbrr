// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"encoding/json"
	"testing"
)

func TestPreparedMetadataSeasonEpisodeHelpers(t *testing.T) {
	meta := PreparedMetadata{
		SeasonInt: 9,
		Release: ReleaseInfo{
			Season:  1,
			Episode: 2,
		},
	}

	canonicalSeason, canonicalEpisode := meta.CanonicalSeasonEpisode()
	if canonicalSeason != 9 || canonicalEpisode != 0 {
		t.Fatalf("canonical season/episode = %d/%d, want 9/0", canonicalSeason, canonicalEpisode)
	}

	fallbackSeason, fallbackEpisode := meta.SeasonEpisodeWithParsedFallback()
	if fallbackSeason != 9 || fallbackEpisode != 2 {
		t.Fatalf("fallback season/episode = %d/%d, want 9/2", fallbackSeason, fallbackEpisode)
	}
	if !meta.HasTVSeasonEpisodeSignal() {
		t.Fatalf("expected parsed release episode to provide TV signal")
	}
}

func TestRuleFailureSeverityFailClosed(t *testing.T) {
	t.Parallel()
	failures := []RuleFailure{
		{Rule: "legacy"},
		{Rule: "warning", Severity: RuleFailureSeverityWarning},
		{Rule: "unknown", Severity: "unexpected"},
	}
	if !HasBlockingRuleFailures(failures) {
		t.Fatal("expected legacy and unknown severities to block")
	}
	if got := BlockingRuleFailures(failures); len(got) != 2 || got[0].Rule != "legacy" || got[1].Rule != "unknown" {
		t.Fatalf("unexpected blocking subset: %#v", got)
	}
	if got := WarningRuleFailures(failures); len(got) != 1 || got[0].Rule != "warning" {
		t.Fatalf("unexpected warning subset: %#v", got)
	}
}

func TestTMDBMetadataMarshalLocalizedTitlesAsObject(t *testing.T) {
	tests := []struct {
		name            string
		localizedTitles map[string]string
		wantJSON        string
	}{
		{
			name:     "nil",
			wantJSON: `{}`,
		},
		{
			name:            "empty",
			localizedTitles: map[string]string{},
			wantJSON:        `{}`,
		},
		{
			name:            "preserves keys",
			localizedTitles: map[string]string{"de": "Die Probe", "pt-BR": "Titulo Brasil"},
			wantJSON:        `{"de":"Die Probe","pt-BR":"Titulo Brasil"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(TMDBMetadata{LocalizedTitles: tt.localizedTitles})
			if err != nil {
				t.Fatalf("marshal TMDBMetadata: %v", err)
			}

			var payload map[string]json.RawMessage
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatalf("unmarshal marshaled TMDBMetadata: %v", err)
			}

			got, ok := payload["LocalizedTitles"]
			if !ok {
				t.Fatalf("expected LocalizedTitles field in payload %s", raw)
			}
			if string(got) != tt.wantJSON {
				t.Fatalf("LocalizedTitles JSON = %s, want %s", got, tt.wantJSON)
			}
		})
	}
}
