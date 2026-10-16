// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestNativeCombinedUsesThreeGiBOperationBudget(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "single-hybrid.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	err = New(NewAdmission(), nil).WithSession(t.Context(), func(session *Session) error {
		result, err := session.extractNative(t.Context(), bytes.NewReader(data), api.HDRAnalysisFormatsBoth, 2<<30)
		if err != nil {
			return err
		}
		defer result.Release()
		if !result.ScanComplete || result.HDR10PlusError != nil || result.DolbyVisionError != nil {
			t.Fatalf("combined outcomes: %+v", result)
		}
		evidence, err := result.Evidence(api.HDRAnalysisFormatsBoth, api.HDRSourceEvidence{})
		if err != nil {
			return err
		}
		defer evidence.Release()
		used, err := evidence.AccountedBytes()
		if err != nil {
			return err
		}
		if used <= 2<<30 || used > 3<<30 {
			t.Fatalf("accounted evidence = %d, want retained sibling plus native evidence within 3 GiB", used)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
