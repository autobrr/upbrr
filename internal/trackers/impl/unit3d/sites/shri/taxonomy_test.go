// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package shri

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
	"testing"
)

func TestSHRINamedDiscTaxonomy(t *testing.T) {
	subject := api.TrackerValidationSubject{
		DiscType:    "DVD",
		Region:      "GBR",
		Distributor: "Arrow",
	}
	failures, err := Profile().ValidationPolicy.Check(t.Context(), subject, api.NopLogger{})
	if err != nil || len(failures) != 0 {
		t.Fatalf("known named taxonomy rejected: failures=%+v err=%v", failures, err)
	}
	data := map[string]string{}
	Profile().Site.ApplyAdditionalPayload(trackers.PreparationInput{Meta: api.UploadSubject{
		DiscType:    subject.DiscType,
		Region:      subject.Region,
		Distributor: subject.Distributor,
	}}, data)
	if data["region_id"] != "78" || data["distributor_id"] != "75" {
		t.Fatalf("payload=%v", data)
	}
}

func TestSHRIDiscRegionRequirementsRemainStrict(t *testing.T) {
	for _, value := range []string{"", "B", "Unknown"} {
		t.Run(value, func(t *testing.T) {
			failures, err := Profile().ValidationPolicy.Check(t.Context(), api.TrackerValidationSubject{DiscType: "DVD", Region: value}, api.NopLogger{})
			if err != nil || len(failures) != 1 || failures[0].Disposition != api.RuleDispositionStrict {
				t.Fatalf("invalid region accepted: failures=%+v err=%v", failures, err)
			}
		})
	}
}
