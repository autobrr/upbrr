// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"fmt"
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
)

func TestMediaTrackFactsDefaultEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		fields string
		value  bool
		known  bool
	}{
		{
			name:   "yes",
			fields: `,"Default":"Yes"`,
			value:  true,
			known:  true,
		},
		{
			name:   "no",
			fields: `,"Default":"No"`,
			known:  true,
		},
		{
			name:   "normalized yes",
			fields: `,"Default":" yEs "`,
			value:  true,
			known:  true,
		},
		{
			name:   "normalized no",
			fields: `,"Default":" nO "`,
			known:  true,
		},
		{
			name:   "display yes",
			fields: `,"Default/String":"Yes"`,
			value:  true,
			known:  true,
		},
		{
			name:   "display no",
			fields: `,"Default":"","Default/String":"No"`,
			known:  true,
		},
		{name: "missing"},
		{name: "empty", fields: `,"Default":""`},
		{name: "null", fields: `,"Default":null`},
		{name: "malformed", fields: `,"Default":"Yes / No"`},
		{name: "malformed primary", fields: `,"Default":"unknown","Default/String":"Yes"`},
		{
			name:   "boolean true",
			fields: `,"Default":true`,
			value:  true,
		},
		{
			name:   "string true",
			fields: `,"Default":"true"`,
			value:  true,
		},
		{
			name:   "numeric one",
			fields: `,"Default":1`,
			value:  true,
		},
		{
			name:   "string one",
			fields: `,"Default":"1"`,
			value:  true,
		},
		{name: "boolean false", fields: `,"Default":false`},
		{name: "numeric zero", fields: `,"Default":0`},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, kind := range []string{"Audio", "Text"} {
				doc, err := loadMediaInfoDocFromJSONPayload(fmt.Sprintf(`{"media":{"track":[{"@type":%q%s}]}}`, kind, test.fields))
				if err != nil {
					t.Fatal(err)
				}
				tracks, _, _, _, err := mediaTrackFacts(preparationstate.State{}, doc)
				if err != nil {
					t.Fatal(err)
				}
				if len(tracks) != 1 || tracks[0].Default != test.value || tracks[0].DefaultKnown != test.known {
					t.Fatalf("%s default evidence = %+v, want default=%t known=%t", kind, tracks, test.value, test.known)
				}
			}
		})
	}
}
