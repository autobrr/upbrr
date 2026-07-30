// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package jsondupe executes the authenticated JSON-list search protocol shared
// by standalone trackers while leaving query and result identity tracker-local.
package jsondupe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

const maxResponseBytes = 4 << 20

// ListSpec describes one JSON-array duplicate-search request and projection.
type ListSpec struct {
	Endpoint       string
	Query          url.Values
	Headers        http.Header
	IDField        string
	NameField      string
	SizeField      string
	Link           func(string) string
	FailureMessage string
}

// Search executes a JSON-list duplicate search with number-preserving decoding.
// It rejects non-2xx responses and bodies above 4 MiB through typed adapter
// failure results rather than returning Go errors directly.
func Search(ctx context.Context, client *http.Client, spec ListSpec) dupe.AdapterResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, spec.Endpoint, nil)
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, spec.FailureMessage, err)
	}
	req.URL.RawQuery = spec.Query.Encode()
	for name, values := range spec.Headers {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return dupe.Failed(dupe.FailureRequest, spec.FailureMessage, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return dupe.Failed(dupe.FailureResponseStatus, spec.FailureMessage, nil)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return dupe.Failed(dupe.FailureResponseParse, spec.FailureMessage, err)
	}
	if len(body) > maxResponseBytes {
		return dupe.Failed(dupe.FailureResponseParse, spec.FailureMessage, fmt.Errorf("duplicate response exceeds %d bytes", maxResponseBytes))
	}
	var items []map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&items); err != nil {
		return dupe.Failed(dupe.FailureResponseParse, spec.FailureMessage, err)
	}

	entries := make([]api.DupeEntry, 0, len(items))
	for _, item := range items {
		id := scalarString(item[spec.IDField])
		entry := api.DupeEntry{Name: scalarString(item[spec.NameField]), ID: id}
		if spec.Link != nil {
			entry.Link = spec.Link(id)
		}
		if size := scalarInt64(item[spec.SizeField]); size > 0 {
			entry.SizeKnown, entry.SizeBytes = true, size
		}
		entries = append(entries, entry)
	}
	return dupe.Resolved(entries, nil)
}

func scalarString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func scalarInt64(value any) int64 {
	switch typed := value.(type) {
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case float64:
		return int64(typed)
	default:
		return 0
	}
}
