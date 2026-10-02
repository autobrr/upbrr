// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package evidence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type jsonResult struct {
	Data     json.RawMessage
	NotFound bool
}

// JSON retains a decoded provider response before local matching or overlays.
// The target type participates in the key because different wire projections
// of the same endpoint may retain different fields. notFound is replayed as a
// genuine empty result rather than a failed request. An optional endpoint-owned
// empty predicate classifies decoded collections that carry non-result metadata.
func JSON(ctx context.Context, domain string, query, target any, notFound error, empty func() bool, fetch func() error) error {
	if !Enabled(ctx) {
		return fetch()
	}
	result, err := Lookup(ctx, domain, []any{query, fmt.Sprintf("%T", target)}, func() (jsonResult, error) {
		if err := fetch(); err != nil {
			if notFound != nil && errors.Is(err, notFound) {
				return jsonResult{NotFound: true}, nil
			}
			return jsonResult{}, err
		}
		data, err := json.Marshal(target)
		if err != nil {
			return jsonResult{}, fmt.Errorf("encode provider response: %w", err)
		}
		return jsonResult{Data: data}, nil
	}, func(value jsonResult) bool {
		if value.NotFound {
			return true
		}
		if empty != nil {
			return empty()
		}
		return emptyJSON(value.Data)
	})
	if err != nil {
		return err
	}
	if result.NotFound {
		return notFound
	}
	if err := json.Unmarshal(result.Data, target); err != nil {
		return errors.New("metadata evidence response cannot be decoded")
	}
	return nil
}

// emptyJSON recognizes provider result containers, including successful search
// envelopes whose pagination/status fields do not themselves constitute facts.
func emptyJSON(data json.RawMessage) bool {
	var value any
	if json.Unmarshal(data, &value) != nil {
		return false
	}
	return emptyJSONValue(value)
}

func emptyJSONValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case []any:
		return len(typed) == 0
	case map[string]any:
		collections := false
		for key, child := range typed {
			switch strings.ToLower(key) {
			case "results", "movie_results", "tv_results", "logos", "backdrops", "posters", "translations", "keywords", "cast", "crew", "episodes", "edges":
				collections = true
				if !emptyJSONValue(child) {
					return false
				}
			}
		}

		for key, child := range typed {
			switch strings.ToLower(key) {
			case "status", "page", "total_pages", "total_results", "pagination", "pageinfo", "links":
				continue
			}
			if collections && strings.EqualFold(key, "id") {
				continue
			}
			if !emptyJSONValue(child) {
				return false
			}
		}
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case float64:
		return typed == 0
	case bool:
		return !typed
	default:
		return false
	}
}
