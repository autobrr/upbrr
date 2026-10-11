// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import "github.com/autobrr/upbrr/pkg/api"

// TitleSearchPolicy declares when a tracker needs a whole-work lookup before
// rule assessment. Its duplicate adapter must search all release variants.
type TitleSearchPolicy struct {
	ID       string
	Required func(api.TrackerValidationSubject) bool
}

// TitleSearchPolicyProvider opts into preflight title-existence evidence.
// The predicate is pure; the workflow owns remote lookup and freshness.
type TitleSearchPolicyProvider interface {
	TitleSearchPolicy() TitleSearchPolicy
}
