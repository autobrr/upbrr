// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTrackerAuthStatusJSONContractFields(t *testing.T) {
	t.Parallel()

	status := TrackerAuthStatus{
		TrackerID:        "MTV",
		DisplayName:      "MTV",
		State:            "needs_2fa",
		CookieCount:      2,
		LastCheckedAt:    "2026-07-08T01:02:03Z",
		LastError:        "tracker auth validation failed",
		EncryptedStorage: true,
		Needs2FA:         true,
		ChallengeID:      "challenge-123",
		Message:          "enter 2FA code",
	}

	payload, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal tracker auth status: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal tracker auth status: %v", err)
	}

	want := map[string]any{
		"trackerID":        "MTV",
		"displayName":      "MTV",
		"state":            "needs_2fa",
		"cookieCount":      float64(2),
		"lastCheckedAt":    "2026-07-08T01:02:03Z",
		"lastError":        "tracker auth validation failed",
		"encryptedStorage": true,
		"needs2FA":         true,
		"challengeID":      "challenge-123",
		"message":          "enter 2FA code",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected tracker auth status JSON: got %#v want %#v", got, want)
	}
}

func TestTrackerAuthStatusConsumerContractsIncludeAPIFields(t *testing.T) {
	t.Parallel()

	fields := trackerAuthStatusJSONFields(t)
	frontendTypes := readRepoFile(t, "webui", "src", "types.ts")

	assertContractBlockFields(t, frontendTypes, "export type TrackerAuthStatus = {", "};", fields)
}

// trackerAuthStatusJSONFields returns the serialized field names that every
// frontend-facing auth status consumer must keep in its local contract.
func trackerAuthStatusJSONFields(t *testing.T) []string {
	t.Helper()

	statusType := reflect.TypeFor[TrackerAuthStatus]()
	fields := make([]string, 0, statusType.NumField())
	for field := range statusType.Fields() {
		tag := field.Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			t.Fatalf("TrackerAuthStatus field %s missing JSON contract tag", field.Name)
		}
		fields = append(fields, name)
	}
	return fields
}

// assertContractBlockFields verifies that a hand-maintained or generated
// frontend contract block still exposes all shared auth status JSON fields.
func assertContractBlockFields(t *testing.T, source string, start string, end string, fields []string) {
	t.Helper()

	startIndex := strings.Index(source, start)
	if startIndex < 0 {
		t.Fatalf("contract block %q not found", start)
	}
	block := source[startIndex:]
	endIndex := strings.Index(block, end)
	if endIndex < 0 {
		t.Fatalf("contract block %q has no end marker %q", start, end)
	}
	block = block[:endIndex]

	for _, field := range fields {
		if !strings.Contains(block, field) {
			t.Fatalf("contract block %q missing tracker auth field %q", start, field)
		}
	}
}

func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()

	content, err := readRepoFileContent(parts...)
	if err != nil {
		t.Fatalf("read repo contract file %v: %v", parts, err)
	}
	return string(content)
}

func readRepoFileContent(parts ...string) ([]byte, error) {
	pathParts := append([]string{"..", ".."}, parts...)
	content, err := os.ReadFile(filepath.Join(pathParts...))
	if err != nil {
		return nil, fmt.Errorf("read repo file: %w", err)
	}
	return content, nil
}
