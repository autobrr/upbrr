// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"time"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// reusableEmptyTitleSearch avoids repeating the preflight's exhaustive empty
// lookup. In-client, skip-remote and banned-group handling run before this path.
func (s *Service) reusableEmptyTitleSearch(meta api.DuplicateSubject, tracker string, now time.Time) (AdapterResult, bool) {
	if meta.Projection == nil || s.registry == nil || string(meta.Projection.TrackerID) != tracker {
		return AdapterResult{}, false
	}
	evidence := meta.Projection.TitleSearchEvidence
	if evidence == nil || !evidence.Current(meta.Identity) || evidence.TorrentCount != 0 || !evidence.FreshUntil.After(now) ||
		evidence.ConfigFingerprint != meta.Projection.ConfigFingerprint {
		return AdapterResult{}, false
	}
	descriptor, ok := s.registry.LookupDescriptor(tracker)
	if !ok {
		return AdapterResult{}, false
	}
	provider, ok := descriptor.Definition.(trackers.TitleSearchPolicyProvider)
	if !ok || provider.TitleSearchPolicy().ID == "" || provider.TitleSearchPolicy().ID != evidence.PolicyID {
		return AdapterResult{}, false
	}
	return ResolvedWithSearch(nil, nil, SearchEvidence{
		Complete:  true,
		WorkScope: WorkScopeProviderID,
		Scope:     "title_preflight",
	}), true
}
