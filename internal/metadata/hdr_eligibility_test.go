// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/externalidentity"
	"github.com/autobrr/upbrr/internal/metadata/mediainfo"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

type hdrTrackAnalyzer struct {
	hdrFiles map[string]bool
	calls    map[string]int
}

func (a hdrTrackAnalyzer) Analyze(_ context.Context, target string) (string, []byte, error) {
	a.calls[target]++
	hdr := "HDR10"
	if a.hdrFiles[target] {
		hdr = "HDR10+"
	}
	return "Video\nFormat : HEVC", []byte(fmt.Sprintf(`{"media":{"track":[{"@type":"Video","Format":"HEVC","HDR_Format":"%s"}]}}`, hdr)), nil
}

func TestHDRTrackEligibilityUsesEachPackFilesOwnMediaInfo(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "Synthetic.Show.S01.2160p.WEB-DL.HDR10+-GRP")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	first, second := filepath.Join(source, "Synthetic.Show.S01E01.mkv"), filepath.Join(source, "Synthetic.Show.S01E02.HDR10+.mkv")
	sanitizedHDR, sanitizedStatic := filepath.Join(source, "Video 1.mkv"), filepath.Join(source, "Video_1.mkv")
	for _, file := range []string{first, second, sanitizedHDR, sanitizedStatic} {
		if err := os.WriteFile(file, []byte("synthetic media"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dbPath := filepath.Join(base, "db.sqlite")
	repo, err := db.OpenContext(t.Context(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.MigrateContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	service := NewService(
		repo,
		WithMediaInfoExporter(mediainfo.NewService(nil, hdrTrackAnalyzer{
			hdrFiles: map[string]bool{first: true, sanitizedHDR: true},
			calls:    make(map[string]int),
		})),
		WithSceneDetector(stubSceneDetector{}),
		WithConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}}),
		WithTMDBClient(&stubTMDB{metadata: tmdb.MetadataResult{Title: "Synthetic Show", Year: 2026}}),
		WithIMDBClient(&stubIMDB{}),
		WithTVDBClient(&stubTVDB{}),
		WithTVmazeClient(&stubTVmaze{}),
	)
	state, err := service.collectSourceEvidence(t.Context(), testCollectionRequest(t, api.Request{SourcePath: source}))
	if err != nil {
		t.Fatal(err)
	}
	if state.VideoPath != first || !state.HDRFileEligibility[first] || state.HDRFileEligibility[second] ||
		!state.HDRFileEligibility[sanitizedHDR] || state.HDRFileEligibility[sanitizedStatic] || len(state.HDRFileEligibility) != 4 {
		t.Fatalf("per-file evidence=%v primary=%s", state.HDRFileEligibility, state.VideoPath)
	}
	collector, err := preparedrelease.NewEvidenceCollector(&recordingEvidencePipeline{service: service})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := externalidentity.NewWithCandidateSource(repo, collector)
	if err != nil {
		t.Fatal(err)
	}
	module, err := preparedrelease.New(repo, identity, collector)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := module.Prepare(
		t.Context(),
		api.PrepareInput{SourcePath: source, Instructions: api.ReleaseFactInstructions{Identity: api.ExternalIDOverrides{TMDBID: new(1234567)}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	ref := api.ReleaseRef{SourcePath: source, Generation: prepared.Release.Generation}
	for _, test := range []struct {
		path     string
		eligible bool
	}{{first, true}, {second, false}, {sanitizedHDR, true}, {sanitizedStatic, false}} {
		subject, err := module.ResolveHDRAnalysisSubject(
			t.Context(),
			api.HDRAnalysisInstructions{Release: ref, TargetIDs: []string{api.HDRTargetID(test.path, "")}},
		)
		if test.eligible {
			if err != nil || len(subject.Targets) != 1 {
				t.Fatalf("confirmed track rejected: %v", err)
			}
		} else {
			failure, ok := api.AsHDRAnalysisFailure(err)
			if !ok || failure.Code != api.HDRAnalysisFailureUnsupportedInput {
				t.Fatalf("unconfirmed track gained authority: %v", err)
			}
		}
	}
}

func TestHDREligibilityCacheBindsTargetInventoryAndVerifiedContent(t *testing.T) {
	base := t.TempDir()
	first, second := filepath.Join(base, "a", "Shared.mkv"), filepath.Join(base, "b", "Shared.mkv")
	for _, file := range []string{first, second} {
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("synthetic media"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	analyzer := hdrTrackAnalyzer{hdrFiles: map[string]bool{first: true}, calls: make(map[string]int)}
	service := NewService(&stubRepo{}, WithMediaInfoExporter(mediainfo.NewService(nil, analyzer)), WithSceneDetector(stubSceneDetector{}),
		WithConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(base, "db.sqlite")}}))
	request := testCollectionRequest(t, api.Request{SourcePath: first})
	request.SourceFingerprint = strings.Repeat("1", 64)
	state, err := service.collectSourceEvidence(t.Context(), request)
	if err != nil || !state.HDRFileEligibility[first] {
		t.Fatalf("confirmed first source rejected: eligibility=%v err=%v", state.HDRFileEligibility, err)
	}
	calls := analyzer.calls[first]
	state, err = service.collectSourceEvidence(t.Context(), request)
	if err != nil || !state.HDRFileEligibility[first] || analyzer.calls[first] != calls {
		t.Fatalf("unchanged source did not reuse evidence: eligibility=%v calls=%d err=%v", state.HDRFileEligibility, analyzer.calls[first], err)
	}
	other := testCollectionRequest(t, api.Request{SourcePath: second})
	other.SourceFingerprint = strings.Repeat("2", 64)
	state, err = service.collectSourceEvidence(t.Context(), other)
	if err != nil || state.HDRFileEligibility[second] || analyzer.calls[second] == 0 {
		t.Fatalf("same basename borrowed HDR evidence: eligibility=%v calls=%d err=%v", state.HDRFileEligibility, analyzer.calls[second], err)
	}
	analyzer.hdrFiles[first] = false
	request.SourceFingerprint = strings.Repeat("3", 64)
	state, err = service.collectSourceEvidence(t.Context(), request)
	if err != nil || state.HDRFileEligibility[first] || analyzer.calls[first] == calls {
		t.Fatalf("changed inventory reused HDR evidence: eligibility=%v calls=%d err=%v", state.HDRFileEligibility, analyzer.calls[first], err)
	}
	request.Input.VerifiedSource = &api.VerifiedInputSource{Identity: api.SourceContentIdentity{Digest: strings.Repeat("a", 64)}}
	state, err = service.collectSourceEvidence(t.Context(), request)
	if err != nil || state.HDRFileEligibility[first] {
		t.Fatalf("static verified source gained eligibility: eligibility=%v err=%v", state.HDRFileEligibility, err)
	}
	calls = analyzer.calls[first]
	analyzer.hdrFiles[first] = true
	request.Input.VerifiedSource.Identity.Digest = strings.Repeat("b", 64)
	state, err = service.collectSourceEvidence(t.Context(), request)
	if err != nil || !state.HDRFileEligibility[first] || analyzer.calls[first] == calls {
		t.Fatalf("changed verified content reused static evidence: eligibility=%v calls=%d err=%v", state.HDRFileEligibility, analyzer.calls[first], err)
	}
	calls = analyzer.calls[first]
	if _, err := service.collectSourceEvidence(t.Context(), request); err != nil || analyzer.calls[first] != calls {
		t.Fatalf("unchanged verified content was reanalyzed: calls=%d err=%v", analyzer.calls[first], err)
	}
}
