// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"encoding/json"

	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/pkg/api"
)

// TraceSearchRequest logs the actual duplicate-search filters without credentials.
// Callers supply a static endpoint label and selected request fields, excluding
// headers, credential-bearing URLs, and opaque pagination tokens.
func TraceSearchRequest(logger api.Logger, tracker, method, endpoint string, params map[string]any) {
	if logger == nil {
		return
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		logger.Tracef("dupechecking: request tracker=%s method=%s endpoint=%s params=unavailable", tracker, method, endpoint)
		return
	}
	logger.Tracef("dupechecking: request tracker=%s method=%s endpoint=%s params=%s",
		tracker, method, endpoint, redaction.RedactValue(string(encoded), nil))
}
