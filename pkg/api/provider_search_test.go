// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"encoding/json"
	"testing"
)

func TestProviderEvidenceJSONPreservesArrayContracts(t *testing.T) {
	for _, test := range []struct {
		name   string
		value  any
		fields map[string]string
	}{
		{"legacy links", TVDBRemoteLinks{}, map[string]string{"Links": "[]", "Status": `""`}},
		{"unavailable links", TVDBRemoteLinks{Status: MetadataEvidenceStatusUnavailable}, map[string]string{"Links": "[]", "Status": `"unavailable"`}},
		{"absent credits", ExternalIdentityCandidate{}, map[string]string{"Cast": "[]", "Directors": "[]"}},
		{"known credits", ExternalIdentityCandidate{Cast: []string{"Example Actor"}, Directors: []string{"Example Director"}}, map[string]string{"Cast": `["Example Actor"]`, "Directors": `["Example Director"]`}},
	} {
		t.Run(test.name, func(t *testing.T) {
			wire, err := json.Marshal(test.value)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(wire, &fields); err != nil {
				t.Fatal(err)
			}
			for name, want := range test.fields {
				if string(fields[name]) != want {
					t.Fatalf("%s=%s, want %s", name, fields[name], want)
				}
			}
		})
	}
}
