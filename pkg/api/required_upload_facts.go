// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "strings"

// MissingRequiredUploadFacts reports absent finalized facts in correction order.
// Provider evidence, source paths, and release names cannot fill these fields.
func (r PreparedRelease) MissingRequiredUploadFacts() []string {
	var missing []string
	if category := strings.TrimSpace(string(r.Identity.Category)); category == "" || strings.EqualFold(category, string(CanonicalCategoryUnknown)) {
		missing = append(missing, "category")
	}
	for _, field := range []struct{ key, value string }{
		{"type", r.Media.Type},
		{"source", r.Media.Source},
		{"resolution", r.Naming.Resolution},
	} {
		if strings.TrimSpace(field.value) == "" {
			missing = append(missing, field.key)
		}
	}
	return missing
}
