// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"encoding/json"
	"testing"
)

func TestNewDescriptionSubjectDetachesNestedFacts(t *testing.T) {
	t.Parallel()

	source := UploadSubject{
		Release: ReleaseInfo{Codec: []string{"H.265"}},
		ProviderMetadata: SourceScopedMetadata{TMDB: &TMDBMetadata{
			LocalizedTitles: map[string]string{"en": "Example Release 2026"},
		}},
		SelectedBDMVPlaylists: []PlaylistInfo{{File: "00001.mpls"}},
	}
	projected := NewDescriptionSubject(source)
	projected.Release.Codec[0] = "changed"
	projected.ProviderMetadata.TMDB.LocalizedTitles["en"] = "changed"
	projected.SelectedBDMVPlaylists[0].File = "changed"

	if source.Release.Codec[0] != "H.265" {
		t.Fatal("release facts share storage with description subject")
	}
	if source.ProviderMetadata.TMDB.LocalizedTitles["en"] != "Example Release 2026" {
		t.Fatal("provider metadata shares storage with description subject")
	}
	if source.SelectedBDMVPlaylists[0].File != "00001.mpls" {
		t.Fatal("playlist facts share storage with description subject")
	}
}

func TestRuleFailureDispositionFailClosed(t *testing.T) {
	t.Parallel()
	failures := []RuleFailure{
		{Rule: "legacy"},
		{Rule: "advisory", Disposition: RuleDispositionAdvisory},
		{Rule: "unknown", Disposition: "unexpected"},
	}
	if !HasBlockingRuleFailures(failures) {
		t.Fatal("expected legacy and unknown dispositions to block")
	}
	storedFailures := []TrackerRuleFailure{
		{Rule: "legacy"},
		{Rule: "advisory", Disposition: RuleDispositionAdvisory},
		{Rule: "unknown", Disposition: "unexpected"},
	}
	if got := CountBlockingRuleFailures(storedFailures); got != 2 {
		t.Fatalf("blocking count = %d, want 2", got)
	}
	if got := BlockingRuleFailures(failures); len(got) != 2 || got[0].Rule != "legacy" || got[1].Rule != "unknown" {
		t.Fatalf("unexpected blocking subset: %#v", got)
	}
	if got := AdvisoryRuleFailures(failures); len(got) != 1 || got[0].Rule != "advisory" {
		t.Fatalf("unexpected advisory subset: %#v", got)
	}
	if NormalizeRuleDisposition("warning") != RuleDispositionAdvisory ||
		NormalizeRuleDisposition("blocking") != RuleDispositionWaivable ||
		NormalizeRuleDisposition("unexpected") != RuleDispositionStrict {
		t.Fatal("legacy or unknown disposition normalization changed")
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
