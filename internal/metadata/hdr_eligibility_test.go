// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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
	reports  map[string]string
}

func (a hdrTrackAnalyzer) Analyze(_ context.Context, target string) (string, []byte, error) {
	a.calls[target]++
	if report, ok := a.reports[target]; ok {
		return "Video", []byte(report), nil
	}
	hdr := "HDR10"
	if a.hdrFiles[target] {
		hdr = "HDR10+"
	}
	return "Video\nFormat : HEVC", []byte(fmt.Sprintf(`{"media":{"track":[{"@type":"Video","Format":"HEVC","HDR_Format":"%s"}]}}`, hdr)), nil
}

func TestHDRPackUsesOnlyPrimaryReportAndTarget(t *testing.T) {
	for _, test := range []struct {
		name      string
		report    string
		hevc      bool
		hdr10Plus bool
	}{
		{"SDR AVC", packMediaReport, false, false},
		{"static HEVC", `{"media":{"track":[{"@type":"Video","Format":"HEVC","HDR_Format":"HDR10"}]}}`, true, false},
		{"HDR10+ HEVC", `{"media":{"track":[{"@type":"Video","Format":"HEVC","HDR_Format":"HDR10+"}]}}`, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := t.TempDir()
			source := filepath.Join(base, "Example.Show.S01.2160p.WEB-DL.HDR10+-GRP")
			if err := os.Mkdir(source, 0o700); err != nil {
				t.Fatal(err)
			}
			first := filepath.Join(source, "Z.Show.S01E1.mkv")
			second := filepath.Join(source, "A.Show.S01E10.HDR10+.mkv")
			for _, file := range []string{first, second} {
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
			analyzer := hdrTrackAnalyzer{
				hdrFiles: map[string]bool{second: !test.hdr10Plus},
				calls:    make(map[string]int),
				reports:  map[string]string{first: test.report},
			}
			service := NewService(repo,
				WithMediaInfoExporter(mediainfo.NewService(nil, analyzer)),
				WithSceneDetector(stubSceneDetector{}),
				WithConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}}),
				WithTMDBClient(&stubTMDB{metadata: tmdb.MetadataResult{Title: "Example Show", Year: 2026}}),
				WithIMDBClient(&stubIMDB{}), WithTVDBClient(&stubTVDB{}), WithTVmazeClient(&stubTVmaze{}))
			collector, err := preparedrelease.NewEvidenceCollector(service)
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
			input := api.PrepareInput{SourcePath: source, Instructions: api.ReleaseFactInstructions{Identity: api.ExternalIDOverrides{TMDBID: new(1234567)}}}
			prepared, err := module.Prepare(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			if analyzer.calls[first] != 1 || len(analyzer.calls) != 1 {
				t.Fatalf("expected only one primary analysis, calls=%v", analyzer.calls)
			}
			if got := slices.Contains(prepared.Release.Media.HDRFacts.Formats, api.HDRFormatHDR10Plus); got != test.hdr10Plus {
				t.Fatalf("pack borrowed later HDR evidence: %+v", prepared.Release.Media.HDRFacts)
			}
			ref := api.ReleaseRef{SourcePath: source, Generation: prepared.Release.Generation}
			targets, err := module.HDRAnalysisTargets(t.Context(), ref)
			if err != nil || len(targets) != 1 || targets[0].ID != api.HDRTargetID(first, "") || targets[0].Supported != test.hevc || targets[0].Evidence.HDR10Plus != test.hdr10Plus {
				t.Fatalf("primary-only targets=%+v err=%v", targets, err)
			}
			for _, profile := range []string{api.HDRAnalysisProfileVersion, api.HDRAnalysisProfileV2} {
				subject, err := module.ResolveHDRAnalysisSubject(t.Context(), api.HDRAnalysisInstructions{
					Release: ref,
 TargetIDs: []string{api.HDRTargetID(first, "")},
 ProfileVersion: profile,
				})
				eligible := test.hdr10Plus || profile == api.HDRAnalysisProfileV2 && test.hevc
				if eligible {
					if err != nil || len(subject.Targets) != 1 || subject.Targets[0].Path != first {
						t.Fatalf("primary rejected: %+v %v", subject, err)
					}
				} else if failure, ok := api.AsHDRAnalysisFailure(err); !ok || failure.Code != api.HDRAnalysisFailureUnsupportedInput {
					t.Fatalf("unsupported primary gained authority: %v", err)
				}
				_, err = module.ResolveHDRAnalysisSubject(t.Context(), api.HDRAnalysisInstructions{
					Release: ref,
 TargetIDs: []string{api.HDRTargetID(second, "")},
 ProfileVersion: profile,
				})
				if failure, ok := api.AsHDRAnalysisFailure(err); !ok || failure.Code != api.HDRAnalysisFailureInvalidSelection {
					t.Fatalf("uninspected sibling remained selectable: %v", err)
				}
			}
			// Restart hydration must reuse the same primary report without a deferred sweep.
			restarted, err := preparedrelease.New(repo, identity, collector)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := restarted.Prepare(t.Context(), input); err != nil {
				t.Fatal(err)
			}
			reused, err := restarted.HDRAnalysisTargets(t.Context(), ref)
			if err != nil || !reflect.DeepEqual(targets, reused) || analyzer.calls[first] != 1 || len(analyzer.calls) != 1 {
				t.Fatalf("restart changed targets or rescanned media: targets=%+v calls=%v err=%v", reused, analyzer.calls, err)
			}
			// Explicit inspection of an unselected file remains available and separately cached.
			evidence, err := service.InspectHDRComparison(t.Context(), second, prepared.Release.Compatibility.SourceFingerprint)
			if err != nil || evidence.HDR10Plus == test.hdr10Plus || analyzer.calls[second] != 1 {
				t.Fatalf("explicit inspection=%+v calls=%v err=%v", evidence, analyzer.calls, err)
			}
			calls := len(analyzer.calls)
			primaryCalls, secondaryCalls := analyzer.calls[first], analyzer.calls[second]
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := service.InspectHDRComparison(ctx, second, "canceled"); !errors.Is(err, context.Canceled) {
				t.Fatalf("explicit cancellation=%v", err)
			}
			if _, err := service.collectSourceEvidence(ctx, testCollectionRequest(t, api.Request{SourcePath: source})); !errors.Is(err, context.Canceled) {
				t.Fatalf("preparation cancellation=%v", err)
			}
			if len(analyzer.calls) != calls || analyzer.calls[first] != primaryCalls || analyzer.calls[second] != secondaryCalls {
				t.Fatalf("canceled work analyzed media: calls=%v", analyzer.calls)
			}
		})
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
