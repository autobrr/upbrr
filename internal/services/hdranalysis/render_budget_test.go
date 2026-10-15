// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bytes"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	dovi "github.com/Audionut/go-dovi-extract"
	"github.com/Audionut/go-hdr10-plus/extract"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDolbyVisionRenderAcceptsAccountedInputAboveLibraryDefault(t *testing.T) {
	err := New(NewAdmission(), nil).WithSession(t.Context(), func(session *Session) error {
		native, err := session.ExtractNativeFile(t.Context(), filepath.Join("testdata", "single-hybrid.mkv"), api.HDRAnalysisFormatsBoth)
		if err != nil {
			return err
		}
		defer native.Release()
		evidence, err := native.Evidence(api.HDRAnalysisFormatsBoth, api.HDRSourceEvidence{})
		if err != nil {
			return err
		}
		defer evidence.Release()
		record := evidence.DolbyVision.Records[0]
		// Borrow one opaque buffer across records: the logical accounting exceeds
		// the library default without allocating hundreds of MiB for this check.
		record.Remaining = make([]byte, 1<<20)
		evidence.DolbyVision = &dovi.Analysis{
			SchemaVersion: 1,
			Timeline:      dovi.RPUSequence,
			Complete:      true,
			Records:       make([]dovi.RPU, 513),
			ObservedRPUs:  513,
		}
		for i := range evidence.DolbyVision.Records {
			evidence.DolbyVision.Records[i] = record
		}
		used, err := evidence.RetainedBytes()
		if err != nil || used <= 512<<20 || used >= MaxRestoreBytes {
			t.Fatalf("invalid accounting fixture: %d %v", used, err)
		}
		path := filepath.Join(t.TempDir(), "large-dovi.png")
		if err := session.RenderNative(t.Context(), evidence, api.HDRNativeDolbyVision, api.HDRPeakHistogram, "Synthetic HDR", path, 0); err != nil {
			return err
		}
		if info, err := os.Stat(path); err != nil || info.Size() == 0 {
			t.Fatalf("large metadata plot missing: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNativeRenderBudgetAllowsLargePlotWithinTotalBudget(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "single-hybrid.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := extract.Extract(t.Context(), bytes.NewReader(data), extract.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer baseline.Release()
	large := &extract.Extraction{
		Frames:      make([]extract.Picture, MaxFrames),
		Payloads:    baseline.Payloads,
		SceneStarts: []uint64{0},
		Profile:     baseline.Profile,
	}
	for i := range large.Frames {
		large.Frames[i] = baseline.Frames[0]
	}
	path := filepath.Join(t.TempDir(), "plot.png")
	err = New(NewAdmission(), nil).Render(t.Context(), large, api.HDRPeakHistogram, "Synthetic HDR", path)
	if err != nil {
		t.Fatalf("large plot within total budget: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("large plot was not published: %v", err)
	}
	evidence := &NativeEvidence{
		HDR10Plus:          large,
		otherRetainedBytes: MaxRestoreBytes - large.RetainedBytes() - nativeRenderBytes,
	}
	blockedPath := filepath.Join(t.TempDir(), "blocked.png")
	err = New(NewAdmission(), nil).WithSession(t.Context(), func(session *Session) error {
		return session.RenderNative(t.Context(), evidence, api.HDRNativeHDR10Plus, api.HDRPeakHistogram, "Synthetic HDR", blockedPath, 0)
	})
	if !errors.Is(err, extract.ErrResourceLimit) {
		t.Fatalf("large plot ignored retained sibling storage: %v", err)
	}
	if _, err := os.Stat(blockedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("over-budget plot published an artifact: %v", err)
	}
}

func TestNativeRenderBudgetCoversBothFormatsAndOtherResults(t *testing.T) {
	err := New(NewAdmission(), nil).WithSession(t.Context(), func(session *Session) error {
		native, err := session.ExtractNativeFile(t.Context(), filepath.Join("testdata", "single-hybrid.mkv"), api.HDRAnalysisFormatsBoth)
		if err != nil {
			return err
		}
		defer native.Release()
		evidence, err := native.Evidence(api.HDRAnalysisFormatsBoth, api.HDRSourceEvidence{})
		if err != nil {
			return err
		}
		defer evidence.Release()
		for _, format := range []api.HDRNativeFormat{api.HDRNativeHDR10Plus, api.HDRNativeDolbyVision} {
			path := filepath.Join(t.TempDir(), "plot.png")
			if err := session.RenderNative(t.Context(), evidence, format, api.HDRPeakHistogram, "Synthetic HDR", path, MaxRestoreBytes-nativeRenderBytes); !errors.Is(err, extract.ErrResourceLimit) {
				t.Fatalf("%s ignored other retained results: %v", format, err)
			}
			if err := session.RenderNative(t.Context(), evidence, format, api.HDRPeakHistogram, "Synthetic HDR", path, 0); err != nil {
				return err
			}
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			config, decodeErr := png.DecodeConfig(file)
			closeErr := file.Close()
			if err := errors.Join(decodeErr, closeErr); err != nil {
				return err
			}
			if config.Width != api.HDRAnalysisWidth || config.Height != api.HDRAnalysisHeight {
				t.Fatalf("%s plot dimensions = %dx%d", format, config.Width, config.Height)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNativeAndDecodedRenderingChargeOutstandingDiscResults(t *testing.T) {
	err := New(NewAdmission(), nil).WithSession(t.Context(), func(session *Session) error {
		native, err := session.ExtractNativeFile(t.Context(), filepath.Join("testdata", "single-hybrid.mkv"), api.HDRAnalysisFormatsBoth)
		if err != nil {
			return err
		}
		defer native.Release()
		evidence, err := native.Evidence(api.HDRAnalysisFormatsBoth, api.HDRSourceEvidence{})
		if err != nil {
			return err
		}
		defer evidence.Release()
		owned, err := evidence.RetainedBytes()
		if err != nil {
			return err
		}
		evidence.otherRetainedBytes = MaxRestoreBytes - owned - nativeRenderBytes + 1
		for _, format := range []api.HDRNativeFormat{api.HDRNativeHDR10Plus, api.HDRNativeDolbyVision} {
			if err := session.RenderNative(t.Context(), evidence, format, api.HDRPeakHistogram, "Synthetic", filepath.Join(t.TempDir(), "plot.png"), 0); !errors.Is(err, extract.ErrResourceLimit) {
				t.Fatalf("%s discarded outstanding disc reservation: %v", format, err)
			}
		}
		if err := decodedMemory([]DecodedInput{{Native: evidence}}, 0, 192*112*6); err == nil {
			t.Fatal("decoded work discarded outstanding disc reservation")
		}
		// B's borrowed reservation includes A; A's reservation is the one
		// authoritative charge for unrelated live disc results in the pair.
		evidence.otherRetainedBytes = 256 << 20
		comparison := &NativeEvidence{otherRetainedBytes: MaxRestoreBytes}
		if err := decodedMemory([]DecodedInput{{Native: evidence}, {Native: comparison}}, 0, 192*112*6); err != nil {
			t.Fatalf("comparison double-counted A's reservation: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
