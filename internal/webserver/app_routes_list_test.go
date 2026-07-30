// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"encoding/json"
	"testing"
)

func TestNonNilAppListPreservesJSONArrayShape(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(nonNilAppList[string](nil))
	if err != nil {
		t.Fatalf("marshal normalized list: %v", err)
	}
	if string(payload) != "[]" {
		t.Fatalf("normalized nil list = %s, want []", payload)
	}

	values := []string{"one"}
	if got := nonNilAppList(values); len(got) != 1 || got[0] != "one" {
		t.Fatalf("normalized populated list = %#v, want %#v", got, values)
	}
}
