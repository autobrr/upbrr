// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bytes"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	dovi "github.com/Audionut/go-dovi-extract"
	dv "github.com/Audionut/go-dovi-extract/extract"
)

func TestDVSidecarPreservesFullEvidence(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "reordered-hybrid.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	extraction, err := dv.Extract(t.Context(), bytes.NewReader(data), dv.Options{})
	if err != nil {
		t.Fatal(err)
	}
	analysis, err := extraction.Analysis(dovi.AnalysisOptions{})
	if err != nil {
		t.Fatal(err)
	}
	analysis.Pictures[0].PTS = dovi.Timestamp{
		Value:     -9007199254740993,
		Timescale: 9007199254740997,
		Valid:     true,
	}
	analysis.Pictures[1].PTS = dovi.Timestamp{
		Value:     0,
		Timescale: 1000000000,
		Valid:     true,
	}
	analysis.Pictures[2].PTS = dovi.Timestamp{}
	identity := testIdentity()
	var output bytes.Buffer
	if err := WriteDVSidecar(t.Context(), &output, DVSidecar{Identity: identity, Analysis: analysis}); err != nil {
		t.Fatal(err)
	}
	restored, err := ReadDVSidecar(t.Context(), bytes.NewReader(output.Bytes()), identity, MaxRestoreBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(analysis, restored.Analysis) {
		t.Fatal("full native evidence changed")
	}
	if _, err := ReadDVSidecar(t.Context(), bytes.NewReader(output.Bytes()), identity, maxDVRecordBytes); err == nil {
		t.Fatal("restoration ignored memory already owned by its sibling collector")
	}
	want, err := dovi.Summarize(analysis, dovi.SummaryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := dovi.Summarize(restored.Analysis, dovi.SummaryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatal("restored summary changed")
	}
	plain := uncompressDVSidecar(t, output.Bytes())
	legacy, err := ReadDVSidecar(t.Context(), bytes.NewReader(plain), identity, MaxRestoreBytes)
	if err != nil || !reflect.DeepEqual(analysis, legacy.Analysis) {
		t.Fatalf("legacy uncompressed evidence changed: %v", err)
	}
	for name, mutate := range map[string]func([]byte) []byte{
		"trailing":  func(b []byte) []byte { return append(b, 'x') },
		"truncated": func(b []byte) []byte { return b[:len(b)-1] },
		"unknown": func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"schema":`), []byte(`"extra":true,"schema":`), 1)
		},
		"duplicate": func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"complete":true`), []byte(`"complete":true,"complete":true`), 1)
		},
		"version": func(b []byte) []byte {
			return bytes.Replace(b, []byte("dovi-extraction-v1"), []byte("dovi-extraction-v9"), 1)
		},
		"oversize": func(b []byte) []byte { return append(bytes.Repeat([]byte{' '}, maxDVRecordBytes), b...) },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := mutate(bytes.Clone(plain))
			for _, stored := range [][]byte{invalid, compressDVSidecar(t, invalid)} {
				if _, err := ReadDVSidecar(t.Context(), bytes.NewReader(stored), identity, MaxRestoreBytes); err == nil {
					t.Fatal("accepted invalid sidecar")
				}
			}
		})
	}
	corrupt := bytes.Clone(output.Bytes())
	corrupt[len(corrupt)-8] ^= 1
	for name, invalid := range map[string][]byte{
		"truncated gzip": output.Bytes()[:output.Len()-1],
		"invalid CRC":    corrupt,
		"second member":  append(bytes.Clone(output.Bytes()), output.Bytes()...),
		"trailing byte":  append(bytes.Clone(output.Bytes()), 'x'),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ReadDVSidecar(t.Context(), bytes.NewReader(invalid), identity, MaxRestoreBytes); err == nil {
				t.Fatal("accepted invalid compression envelope")
			}
		})
	}
	wrong := identity
	wrong.ResolvedTrackID++
	if _, err := ReadDVSidecar(t.Context(), bytes.NewReader(output.Bytes()), wrong, MaxRestoreBytes); err == nil {
		t.Fatal("accepted another track")
	}
}

func TestDVRecordShapeLimitsBeforeAllocation(t *testing.T) {
	for _, value := range []any{make([]int, 513), map[string]any{"Blocks": make([]int, 513)}} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateDVRecordShape(data); err == nil {
			t.Fatal("accepted oversized nested array")
		}
	}
	if err := validateDVRecordShape([]byte(`{"Blocks":[{"Values":[0,1,2]}]}`)); err != nil {
		t.Fatal(err)
	}
}
