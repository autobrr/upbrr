// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sam

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const samDupeEvidenceID = "sam-upload-rules-slots-v1"

func duplicatePolicy() *trackers.DupePolicy {
	return &trackers.DupePolicy{
		ID:         "sam/duplicate/v1",
		EvidenceID: samDupeEvidenceID,
		SlotDimensions: []trackers.DupeDimension{
			trackers.DupeDimensionSource,
			trackers.DupeDimensionResolution,
			trackers.DupeDimensionCodec,
		},
		CoexistenceRules: []trackers.DupeRule{{
			ID:         "sam_new_audio_languages",
			Relation:   string(api.DupeRelationCoexists),
			ReasonCode: "sam_new_audio_languages",
			Conditions: []trackers.DupeCondition{{
				Dimension:            trackers.DupeDimensionAudioLanguages,
				ValuesDifferent:      true,
				MissingNotApplicable: true,
			}},
		}},
	}
}
