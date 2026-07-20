// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediainfo

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestExportWritesCleanedArtifacts(t *testing.T) {
	tmpRoot := t.TempDir()
	targetDir := t.TempDir()
	targetPath := filepath.Join(targetDir, "Movie.Title.mkv")
	if err := os.WriteFile(targetPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}

	textOutput := strings.Join([]string{
		"General",
		"ReportBy: MediaInfo",
		"Report created by MediaInfo",
		"Complete name                            : " + targetPath,
	}, "\n")
	jsonOutput := "{\"media\":{\"track\":[{\"@type\":\"General\"}]}}"

	analyzer := &fakeAnalyzer{text: textOutput, json: jsonOutput}
	service := NewService(api.NopLogger{}, analyzer)

	result, err := service.Export(context.Background(), Request{
		SourcePath: targetPath,
		VideoPath:  targetPath,
		TempRoot:   tmpRoot,
		Release: api.ReleaseInfo{
			Title: "Movie.Title",
			Year:  2024,
		},
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	textData, err := os.ReadFile(result.TextPath)
	if err != nil {
		t.Fatalf("read text: %v", err)
	}
	text := string(textData)
	if strings.Contains(text, "ReportBy") {
		t.Fatalf("expected ReportBy lines removed")
	}
	if strings.Contains(text, targetPath) {
		t.Fatalf("expected target path to be cleaned")
	}
	if !strings.Contains(text, filepath.Base(targetPath)) {
		t.Fatalf("expected basename in cleaned text")
	}

	jsonData, err := os.ReadFile(result.JSONPath)
	if err != nil {
		t.Fatalf("read json: %v", err)
	}
	if string(jsonData) != jsonOutput {
		t.Fatalf("unexpected json output: %s", string(jsonData))
	}
}

func TestExportReusesExistingArtifactsWhenConformanceOK(t *testing.T) {
	tmpRoot := t.TempDir()
	targetDir := t.TempDir()
	targetPath := filepath.Join(targetDir, "Movie.Title.mkv")
	if err := os.WriteFile(targetPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}

	release := api.ReleaseInfo{Title: "Movie.Title", Year: 2024}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, preparationstate.State{Release: release}, targetPath)
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}

	textPath := filepath.Join(tmpDir, "mediainfo.txt")
	jsonPath := filepath.Join(tmpDir, "MediaInfo.json")
	if err := os.WriteFile(textPath, []byte("existing"), 0o600); err != nil {
		t.Fatalf("write text: %v", err)
	}
	jsonPayload := "{\"media\":{\"track\":[{\"@type\":\"General\",\"extra\":{\"ConformanceErrors\":{}}}]}}"
	if err := os.WriteFile(jsonPath, []byte(jsonPayload), 0o600); err != nil {
		t.Fatalf("write json: %v", err)
	}

	service := NewService(api.NopLogger{}, panicAnalyzer{t: t})
	result, err := service.Export(context.Background(), Request{
		SourcePath: targetPath,
		VideoPath:  targetPath,
		TempRoot:   tmpRoot,
		Release:    release,
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if result.TextPath != textPath {
		t.Fatalf("unexpected text path: %s", result.TextPath)
	}
	if result.JSONPath != jsonPath {
		t.Fatalf("unexpected json path: %s", result.JSONPath)
	}
}

func TestSelectDVDTargetReturnsMatchingVOBSet(t *testing.T) {
	root := t.TempDir()
	videoTS := filepath.Join(root, "VIDEO_TS")
	if err := os.MkdirAll(videoTS, 0o700); err != nil {
		t.Fatalf("mkdir VIDEO_TS: %v", err)
	}

	if err := os.WriteFile(filepath.Join(videoTS, "VTS_01_0.IFO"), []byte("ifo1"), 0o600); err != nil {
		t.Fatalf("write ifo1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(videoTS, "VTS_02_0.IFO"), []byte("ifo2"), 0o600); err != nil {
		t.Fatalf("write ifo2: %v", err)
	}
	if err := os.WriteFile(filepath.Join(videoTS, "VTS_01_1.VOB"), []byte(strings.Repeat("a", 100)), 0o600); err != nil {
		t.Fatalf("write vob1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(videoTS, "VTS_02_1.VOB"), []byte(strings.Repeat("b", 300)), 0o600); err != nil {
		t.Fatalf("write vob2: %v", err)
	}
	if err := os.WriteFile(filepath.Join(videoTS, "VTS_02_2.VOB"), []byte(strings.Repeat("b", 200)), 0o600); err != nil {
		t.Fatalf("write vob2b: %v", err)
	}

	target, err := selectDVDTarget(context.Background(), root)
	if err != nil {
		t.Fatalf("select dvd target: %v", err)
	}
	if !strings.HasSuffix(strings.ToUpper(target.IFOPath), "VTS_02_0.IFO") {
		t.Fatalf("expected VTS_02_0.IFO, got %s", target.IFOPath)
	}
	if !strings.HasSuffix(strings.ToUpper(target.VOBPath), "VTS_02_1.VOB") {
		t.Fatalf("expected matching VTS_02_1.VOB, got %s", target.VOBPath)
	}
	if target.VOBSet != "02" {
		t.Fatalf("expected set 02, got %q", target.VOBSet)
	}
}

func TestExportDVDAnalyzesIFOAndMatchingVOB(t *testing.T) {
	tmpRoot := t.TempDir()
	root := t.TempDir()
	videoTS := filepath.Join(root, "VIDEO_TS")
	if err := os.MkdirAll(videoTS, 0o700); err != nil {
		t.Fatalf("mkdir VIDEO_TS: %v", err)
	}
	ifoPath := filepath.Join(videoTS, "VTS_01_0.IFO")
	vobPath := filepath.Join(videoTS, "VTS_01_1.VOB")
	if err := os.WriteFile(ifoPath, []byte("ifo"), 0o600); err != nil {
		t.Fatalf("write ifo: %v", err)
	}
	if err := os.WriteFile(vobPath, []byte(strings.Repeat("v", 10)), 0o600); err != nil {
		t.Fatalf("write vob: %v", err)
	}

	analyzer := &recordingAnalyzer{text: "General\nComplete name : X", json: "{\"media\":{\"track\":[{\"@type\":\"General\"}]}}"}
	service := NewService(api.NopLogger{}, analyzer)

	result, err := service.Export(context.Background(), Request{
		SourcePath: root,
		DiscType:   "DVD",
		TempRoot:   tmpRoot,
		Release:    api.ReleaseInfo{Title: "Movie", Year: 2024},
	})
	if err != nil {
		t.Fatalf("export dvd: %v", err)
	}
	if result.IFOPath == "" || result.VOBPath == "" {
		t.Fatalf("expected IFO and VOB paths in result: %#v", result)
	}
	if !slices.Contains(analyzer.targets, ifoPath) {
		t.Fatalf("expected analyzer to scan ifo path %s, got %v", ifoPath, analyzer.targets)
	}
	if !slices.Contains(analyzer.targets, vobPath) {
		t.Fatalf("expected analyzer to scan vob path %s, got %v", vobPath, analyzer.targets)
	}
	if strings.TrimSpace(result.VOBText) == "" || strings.TrimSpace(result.VOBJSON) == "" {
		t.Fatalf("expected vob mediainfo outputs in result")
	}
}

type fakeAnalyzer struct {
	text string
	json string
}

func (f *fakeAnalyzer) Analyze(_ context.Context, _ string) (string, []byte, error) {
	return f.text, []byte(f.json), nil
}

type panicAnalyzer struct {
	t *testing.T
}

func (p panicAnalyzer) Analyze(_ context.Context, _ string) (string, []byte, error) {
	p.t.Fatalf("runner should not be called")
	return "", nil, nil
}

type recordingAnalyzer struct {
	text    string
	json    string
	targets []string
}

func (r *recordingAnalyzer) Analyze(_ context.Context, target string) (string, []byte, error) {
	r.targets = append(r.targets, target)
	return r.text, []byte(r.json), nil
}
