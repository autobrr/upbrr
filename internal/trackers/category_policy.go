// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"maps"

	"github.com/autobrr/upbrr/pkg/api"
)

// CloneCategoryPolicies copies mutable category policy data at construction boundaries.
func CloneCategoryPolicies(policies map[api.CanonicalCategory]CategoryPolicy) map[api.CanonicalCategory]CategoryPolicy {
	cloned := maps.Clone(policies)
	for category, policy := range cloned {
		if policy.DupePolicy != nil {
			dupe := cloneDupePolicy(*policy.DupePolicy)
			policy.DupePolicy = &dupe
		}
		cloned[category] = policy
	}
	return cloned
}

// ForCategory resolves explicit overrides without changing the registered descriptor.
func (d Descriptor) ForCategory(category api.CanonicalCategory) Descriptor {
	policy, ok := d.CategoryPolicies[category]
	if !ok {
		return d
	}
	if policy.UploadContentMode != "" {
		d.UploadContentMode = policy.UploadContentMode
	}
	if policy.DupePolicy != nil {
		d.DupePolicy = policy.DupePolicy
	}
	return d
}

// LookupUploadContentModeForCategory returns the effective content workflow.
func (r *Registry) LookupUploadContentModeForCategory(tracker string, category api.CanonicalCategory) (UploadContentMode, bool) {
	descriptor, ok := r.LookupDescriptor(tracker)
	descriptor = descriptor.ForCategory(category)
	return descriptor.UploadContentMode, ok && descriptor.UploadContentMode.Valid()
}

// LookupDupePolicyForCategory returns the effective duplicate policy.
func (r *Registry) LookupDupePolicyForCategory(tracker string, category api.CanonicalCategory) (DupePolicy, bool) {
	descriptor, ok := r.LookupDescriptor(tracker)
	descriptor = descriptor.ForCategory(category)
	if !ok || descriptor.DupePolicy == nil {
		return DupePolicy{}, false
	}
	return cloneDupePolicy(*descriptor.DupePolicy), true
}
