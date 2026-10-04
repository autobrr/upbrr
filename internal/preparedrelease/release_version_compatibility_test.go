// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestReleaseVersionInvalidatesPreOverrideInstructionFingerprint(t *testing.T) {
	t.Parallel()
	// Prepared generations without the Repack instruction field must be
	// invalidated even when the new release-version control remains automatic.
	payload, err := json.Marshal(struct {
		Instructions       api.ReleaseFactInstructions
		CorrectionRevision uint64
	}{})
	if err != nil {
		t.Fatal(err)
	}
	marker := []byte(`"Repack":null,`)
	if bytes.Count(payload, marker) != 1 {
		t.Fatalf("automatic Repack is absent from instruction encoding: %s", payload)
	}
	current, err := preparationCompatibility(api.PrepareInput{}, "source", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(payload)); got != current.FactInstructionFingerprint {
		t.Fatalf("instruction encoding no longer matches compatibility: %s != %s", got, current.FactInstructionFingerprint)
	}
	legacy := bytes.Replace(payload, marker, nil, 1)
	if old := fmt.Sprintf("%x", sha256.Sum256(legacy)); old == current.FactInstructionFingerprint {
		t.Fatal("pre-Repack instructions can reuse the current generation")
	}
	if current.ContractVersion != ContractVersion {
		t.Fatalf("contract=%q want %q", current.ContractVersion, ContractVersion)
	}
}
