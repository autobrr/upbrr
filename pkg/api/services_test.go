// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"encoding/json"
	"testing"
)

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
