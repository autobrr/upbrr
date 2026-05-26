// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func doJSONGet(ctx context.Context, client *http.Client, endpoint string, params url.Values, headers map[string]string) (int, map[string]any, error) {
	status, payload, err := doJSONGetAny(ctx, client, endpoint, params, headers)
	if err != nil {
		return status, nil, err
	}
	if payload == nil {
		return status, nil, nil
	}
	result, ok := payload.(map[string]any)
	if !ok {
		return status, nil, nil
	}
	return status, result, nil
}

func doJSONGetAny(ctx context.Context, client *http.Client, endpoint string, params url.Values, headers map[string]string) (int, any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("dupechecking: create JSON GET request: %w", err)
	}
	if len(params) > 0 {
		req.URL.RawQuery = params.Encode()
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("dupechecking: JSON GET request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, nil, nil
	}
	var payload any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, payload, nil
}

func doJSONPost(ctx context.Context, client *http.Client, endpoint string, body map[string]any, headers map[string]string) (int, map[string]any, error) {
	status, payload, err := doJSONPostAny(ctx, client, endpoint, body, headers)
	if err != nil {
		return status, nil, err
	}
	if payload == nil {
		return status, nil, nil
	}
	result, ok := payload.(map[string]any)
	if !ok {
		return status, nil, nil
	}
	return status, result, nil
}

func doJSONPostAny(ctx context.Context, client *http.Client, endpoint string, body map[string]any, headers map[string]string) (int, any, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return 0, nil, fmt.Errorf("dupechecking: create JSON POST request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("dupechecking: JSON POST request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, nil, nil
	}
	var payload any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, payload, nil
}

func intFromAny(value any) int64 {
	switch v := value.(type) {
	case int:
		return int64(v)
	case int32:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	case json.Number:
		i, _ := v.Int64()
		return i
	case string:
		s := strings.TrimSpace(strings.TrimPrefix(v, "tt"))
		if s == "" {
			return 0
		}
		i, _ := strconv.ParseInt(s, 10, 64)
		return i
	default:
		return 0
	}
}

func stringFromAny(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func anyToSlice(value any) ([]any, bool) {
	list, ok := value.([]any)
	return list, ok
}
