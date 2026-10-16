// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	dovi "github.com/Audionut/go-dovi-extract"
	dv "github.com/Audionut/go-dovi-extract/extract"
	"github.com/Audionut/go-hdr10-plus/extract"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestDVSidecarCompressionAndRetainedRestoreBudget(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "reordered-hybrid.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	extraction, err := dv.Extract(t.Context(), bytes.NewReader(data), dv.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer extraction.Release()
	analysis, err := extraction.Analysis(dovi.AnalysisOptions{})
	if err != nil {
		t.Fatal(err)
	}
	records, pictures := analysis.Records, analysis.Pictures
	const count = 12000
	analysis.Records = make([]dovi.RPU, count)
	analysis.Pictures = make([]dovi.Picture, count)
	analysis.ObservedRPUs, analysis.SuppressedRPUs = count, 0
	for i := range count {
		analysis.Records[i] = records[i%len(records)]
		picture := pictures[i%len(pictures)]
		picture.AUOrdinal, picture.DecodeOrdinal, picture.POC = uint64(i), uint64(i), int64(i)
		picture.PTS = dovi.Timestamp{
			Value:     int64(i),
			Timescale: 24,
			Valid:     true,
		}
		picture.RPUIndex = uint32(i)
		analysis.Pictures[i] = picture
	}
	const budget = 32 << 20
	if err := analysis.Validate(dovi.AnalysisOptions{MaxRecords: MaxFrames, MaxBytes: budget}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := WriteDVSidecar(t.Context(), &output, DVSidecar{Identity: testIdentity(), Analysis: analysis}); err != nil {
		t.Fatal(err)
	}
	t.Run("compressed full evidence", func(t *testing.T) {
		if !bytes.HasPrefix(output.Bytes(), []byte{0x1f, 0x8b}) {
			t.Fatal("full metadata was not streamed through gzip")
		}
		plain := uncompressDVSidecar(t, output.Bytes())
		if output.Len()*10 >= len(plain) {
			t.Fatalf("repeated authored evidence was not compacted: stored=%d expanded=%d", output.Len(), len(plain))
		}
	})
	t.Run("restore counts retained storage rather than accumulated decoder scratch", func(t *testing.T) {
		restored, err := ReadDVSidecar(t.Context(), bytes.NewReader(output.Bytes()), testIdentity(), budget)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(analysis, restored.Analysis) {
			t.Fatal("compressed restoration changed full native evidence")
		}
	})
	t.Run("sibling evidence leaves no compression allowance", func(t *testing.T) {
		evidence := &NativeEvidence{DolbyVision: analysis}
		used, err := evidence.RetainedBytes()
		if err != nil {
			t.Fatal(err)
		}
		evidence.otherRetainedBytes = MaxRestoreBytes - dvSidecarWriteScratch - used + 1
		directory := t.TempDir()
		if _, _, err := PublishNativeEvidence(t.Context(), directory, testIdentity(), api.HDRAnalysisFormatsAuto, evidence); !errors.Is(err, extract.ErrResourceLimit) {
			t.Fatalf("compression bypassed sibling accounting: %v", err)
		}
		entries, err := os.ReadDir(directory)
		if err != nil || len(entries) != 0 {
			t.Fatal("rejected persistence wrote partial evidence")
		}
	})
	t.Run("cancellation and compressor finalization errors survive", func(t *testing.T) {
		small := *analysis
		small.Records, small.Pictures, small.ObservedRPUs = analysis.Records[:1], analysis.Pictures[:1], 1
		sidecar := DVSidecar{Identity: testIdentity(), Analysis: &small}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := WriteDVSidecar(ctx, io.Discard, sidecar); !errors.Is(err, context.Canceled) {
			t.Fatalf("lost cancellation: %v", err)
		}
		failure := errors.New("synthetic output failure")
		writer := &dvFailAfterHeaderWriter{err: failure}
		if err := WriteDVSidecar(t.Context(), writer, sidecar); !errors.Is(err, failure) || writer.writes != 2 {
			t.Fatalf("lost compressed finalization failure: writes=%d err=%v", writer.writes, err)
		}
	})
}

type dvFailAfterHeaderWriter struct {
	writes int
	err    error
}

func (w *dvFailAfterHeaderWriter) Write(data []byte) (int, error) {
	w.writes++
	if w.writes > 1 {
		return 0, w.err
	}
	return len(data), nil
}

func uncompressDVSidecar(t *testing.T, data []byte) []byte {
	t.Helper()
	if !bytes.HasPrefix(data, []byte{0x1f, 0x8b}) {
		return bytes.Clone(data)
	}
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	plain, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return plain
}

func compressDVSidecar(t *testing.T, data []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := gzip.NewWriter(&output)
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
