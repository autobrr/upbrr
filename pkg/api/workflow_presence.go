// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// WorkflowPatch preserves JSON absence, null/reset, and explicit values including zero values.
// Present=false means leave unchanged; Present=true with Reset=true means return to automatic.
type WorkflowPatch[T any] struct {
	Present bool `json:"-"`
	Reset   bool `json:"-"`
	Value   T    `json:"-"`
}

// IsZero reports whether this patch was absent.
func (p WorkflowPatch[T]) IsZero() bool { return !p.Present }

// MarshalJSON serializes a present patch as null/reset or its explicit value.
func (p WorkflowPatch[T]) MarshalJSON() ([]byte, error) {
	if !p.Present || p.Reset {
		return []byte("null"), nil
	}
	payload, err := json.Marshal(p.Value)
	if err != nil {
		return nil, fmt.Errorf("marshal workflow patch: %w", err)
	}
	return payload, nil
}

// UnmarshalJSON records null separately from an explicit zero value.
func (p *WorkflowPatch[T]) UnmarshalJSON(payload []byte) error {
	if p == nil {
		return nil
	}
	p.Present = true
	p.Reset = bytes.Equal(bytes.TrimSpace(payload), []byte("null"))
	if p.Reset {
		var zero T
		p.Value = zero
		return nil
	}
	if err := json.Unmarshal(payload, &p.Value); err != nil {
		return fmt.Errorf("unmarshal workflow patch: %w", err)
	}
	return nil
}

// ReleaseFactInstructionUpdate replaces a complete instruction snapshot.
// A null instructions member resets every field to automatic; an absent member leaves it unchanged.
type ReleaseFactInstructionUpdate struct {
	Instructions WorkflowPatch[ReleaseFactInstructions] `json:"-"`
}

// MarshalJSON preserves absent, null/reset, and explicit instruction values.
func (u ReleaseFactInstructionUpdate) MarshalJSON() ([]byte, error) {
	if !u.Instructions.Present {
		return []byte("{}"), nil
	}
	payload, err := u.Instructions.MarshalJSON()
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(map[string]json.RawMessage{"instructions": payload})
	if err != nil {
		return nil, fmt.Errorf("marshal fact instruction update: %w", err)
	}
	return encoded, nil
}

// UnmarshalJSON preserves whether instructions were absent, null, or explicitly supplied.
func (u *ReleaseFactInstructionUpdate) UnmarshalJSON(payload []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return fmt.Errorf("unmarshal fact instruction update: %w", err)
	}
	raw, ok := fields["instructions"]
	if !ok {
		u.Instructions = WorkflowPatch[ReleaseFactInstructions]{}
		return nil
	}
	if err := json.Unmarshal(raw, &u.Instructions); err != nil {
		return fmt.Errorf("unmarshal fact instruction update instructions: %w", err)
	}
	return nil
}

// MarshalJSON preserves tracker projection instruction absence, null/reset, and explicit empty names.
func (i TrackerProjectionInstructions) MarshalJSON() ([]byte, error) {
	fields := make(map[string]any, 5)
	if i.UploadReleaseName.Present {
		if i.UploadReleaseName.Reset {
			fields["uploadReleaseName"] = nil
		} else {
			fields["uploadReleaseName"] = i.UploadReleaseName.Value
		}
	}
	if i.AdditionalNames != nil {
		fields["additionalNames"] = i.AdditionalNames
	}
	if i.Questionnaire != nil {
		fields["questionnaire"] = i.Questionnaire
	}
	if i.TrackerConfig.Anon != nil || i.TrackerConfig.Draft != nil || i.TrackerConfig.ModQ != nil || i.TrackerConfig.Channel != nil {
		fields["trackerConfig"] = i.TrackerConfig
	}
	if i.TrackerSite.TIK.Foreign != nil || i.TrackerSite.TIK.Opera != nil || i.TrackerSite.TIK.Asian != nil || i.TrackerSite.TIK.DiscType != nil {
		fields["trackerSite"] = i.TrackerSite
	}
	payload, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("marshal tracker projection instructions: %w", err)
	}
	return payload, nil
}

// UnmarshalJSON preserves tracker projection instruction field presence.
func (i *TrackerProjectionInstructions) UnmarshalJSON(payload []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return fmt.Errorf("unmarshal tracker projection instructions: %w", err)
	}
	*i = TrackerProjectionInstructions{}
	if raw, ok := fields["uploadReleaseName"]; ok {
		if err := json.Unmarshal(raw, &i.UploadReleaseName); err != nil {
			return fmt.Errorf("unmarshal tracker projection upload name: %w", err)
		}
	}
	if raw, ok := fields["additionalNames"]; ok {
		if err := json.Unmarshal(raw, &i.AdditionalNames); err != nil {
			return fmt.Errorf("unmarshal tracker projection additional names: %w", err)
		}
	}
	if raw, ok := fields["questionnaire"]; ok {
		if err := json.Unmarshal(raw, &i.Questionnaire); err != nil {
			return fmt.Errorf("unmarshal tracker projection questionnaire: %w", err)
		}
	}
	if raw, ok := fields["trackerConfig"]; ok {
		if err := json.Unmarshal(raw, &i.TrackerConfig); err != nil {
			return fmt.Errorf("unmarshal tracker projection config: %w", err)
		}
	}
	if raw, ok := fields["trackerSite"]; ok {
		if err := json.Unmarshal(raw, &i.TrackerSite); err != nil {
			return fmt.Errorf("unmarshal tracker projection site: %w", err)
		}
	}
	return nil
}
