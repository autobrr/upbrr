// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

type persistedAssessment struct {
	Assessed bool                       `json:"assessed"`
	Entries  []persistedAssessmentEntry `json:"entries"`
}

type persistedAssessmentEntry struct {
	Tracker       string            `json:"tracker"`
	Binding       string            `json:"binding"`
	OutcomeID     string            `json:"outcomeId"`
	Disposition   Disposition       `json:"disposition"`
	Code          string            `json:"code,omitempty"`
	HasDupes      bool              `json:"hasDupes"`
	Verdict       Verdict           `json:"verdict"`
	Authorization AuthorizationKind `json:"authorization,omitempty"`
	Match         api.DupeMatch     `json:"match"`
	PrivateRaw    []api.DupeEntry   `json:"privateRaw,omitempty"`
}

// MarshalBinary serializes private duplicate decision authority for the scoped artifact vault.
func (a Assessment) MarshalBinary() ([]byte, error) {
	persisted := persistedAssessment{
		Assessed: a.assessed,
		Entries:  make([]persistedAssessmentEntry, 0, len(a.entries)),
	}
	for _, tracker := range sortedAssessmentTrackers(a.entries) {
		entry := a.entries[tracker]
		persisted.Entries = append(persisted.Entries, persistedAssessmentEntry{
			Tracker:       entry.tracker,
			Binding:       entry.binding,
			OutcomeID:     entry.outcomeID,
			Disposition:   entry.disposition,
			Code:          entry.code,
			HasDupes:      entry.hasDupes,
			Verdict:       entry.verdict,
			Authorization: entry.authorization,
			Match:         clonePrivateMatch(entry.match),
			PrivateRaw:    cloneEntries(entry.privateRaw),
		})
	}
	payload, err := json.Marshal(persisted)
	if err != nil {
		return nil, fmt.Errorf("marshal duplicate assessment: %w", err)
	}
	return payload, nil
}

// UnmarshalAssessment restores private duplicate decision authority from the scoped artifact vault.
func UnmarshalAssessment(payload []byte) (Assessment, error) {
	var persisted persistedAssessment
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return Assessment{}, fmt.Errorf("unmarshal duplicate assessment: %w", err)
	}
	assessment := Assessment{
		entries:  make(map[string]assessmentEntry, len(persisted.Entries)),
		assessed: persisted.Assessed,
	}
	for _, persistedEntry := range persisted.Entries {
		tracker := normalizeTracker(persistedEntry.Tracker)
		if tracker == "" || tracker != strings.TrimSpace(persistedEntry.Tracker) {
			return Assessment{}, errors.New("duplicate assessment contains invalid tracker identity")
		}
		if _, duplicate := assessment.entries[tracker]; duplicate {
			return Assessment{}, errors.New("duplicate assessment contains duplicate tracker identity")
		}
		if persistedEntry.Disposition != DispositionResolved && persistedEntry.Disposition != DispositionNotRun &&
			persistedEntry.Disposition != DispositionFailed {
			return Assessment{}, errors.New("duplicate assessment contains invalid disposition")
		}
		switch persistedEntry.Verdict {
		case VerdictClear, VerdictBlocked, VerdictOverridden, VerdictWaived:
		default:
			return Assessment{}, errors.New("duplicate assessment contains invalid verdict")
		}
		switch persistedEntry.Authorization {
		case AuthorizationNone, AuthorizationOverride, AuthorizationWaiver:
		default:
			return Assessment{}, errors.New("duplicate assessment contains invalid authorization")
		}
		assessment.entries[tracker] = assessmentEntry{
			tracker:       tracker,
			binding:       strings.TrimSpace(persistedEntry.Binding),
			outcomeID:     strings.TrimSpace(persistedEntry.OutcomeID),
			disposition:   persistedEntry.Disposition,
			code:          strings.TrimSpace(persistedEntry.Code),
			hasDupes:      persistedEntry.HasDupes,
			verdict:       persistedEntry.Verdict,
			authorization: persistedEntry.Authorization,
			match:         clonePrivateMatch(persistedEntry.Match),
			privateRaw:    cloneEntries(persistedEntry.PrivateRaw),
		}
	}
	if len(assessment.entries) == 0 && !assessment.assessed {
		return Assessment{}, nil
	}
	return assessment, nil
}
