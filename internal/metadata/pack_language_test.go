// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/metadata/mediainfo"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

const packLanguageReport = `{"media":{"track":[
{"@type":"General","Format":"Matroska","UniqueID":"123"},
{"@type":"Video","Format":"AVC","Width":"1920","Height":"1080","BitDepth":"8","StreamOrder":"0"},
{"@type":"Audio","Format":"FLAC","Language":"eng","Title":"FLAC 5.1 [GRP]","Channels":"6","Default":"Yes","StreamOrder":"1"},
{"@type":"Audio","Format":"FLAC","Language":"jpn","Title":"FLAC 2.0 [GRP]","Channels":"2","Default":"No","StreamOrder":"2"},
{"@type":"Audio","Format":"FLAC","Language":"eng","Title":"Commentary - FLAC 2.0 [GRP]","Channels":"2","Default":"No","StreamOrder":"3"},
{"@type":"Audio","Format":"FLAC","Language":"fra","Title":"Commentary - FLAC 2.0 [GRP]","Channels":"2","Default":"No","StreamOrder":"4"}
]}}`

type packLanguageAnalyzer struct{ packAnalyzer }

func (a *packLanguageAnalyzer) Analyze(ctx context.Context, target string) (string, []byte, error) {
	text, _, err := a.packAnalyzer.Analyze(ctx, target)
	return text, []byte(packLanguageReport), err
}

func TestPackSelectedMediaLanguagesDriveTrackerEligibility(t *testing.T) {
	t.Parallel()
	registry, err := impl.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, numbered := range []bool{true, false} {
		t.Run(fmt.Sprintf("numbered=%t", numbered), func(t *testing.T) {
			base := t.TempDir()
			source := filepath.Join(base, "Example.Show.S01.1080p.BluRay.FLAC.x264-GRP")
			if err := os.MkdirAll(filepath.Join(source, "Extras"), 0o700); err != nil {
				t.Fatal(err)
			}
			var firstEpisode string
			for episode := 1; episode <= 12; episode++ {
				name := fmt.Sprintf("Example.Show.S01E%02d.1080p.BluRay.FLAC.x264-GRP.mkv", episode)
				if !numbered {
					name = fmt.Sprintf("Part.%c.mkv", 'A'+episode-1)
				}
				path := filepath.Join(source, name)
				if err := os.WriteFile(path, []byte(strings.Repeat("synthetic video", episode)), 0o600); err != nil {
					t.Fatal(err)
				}
				if episode == 1 {
					firstEpisode = path
				}
			}
			extra := filepath.Join(source, "Extras", "Example.Featurette.mkv")
			if err := os.WriteFile(extra, []byte(strings.Repeat("synthetic extra", 20)), 0o600); err != nil {
				t.Fatal(err)
			}
			analyzer := &packLanguageAnalyzer{}
			service := NewService(&stubRepo{},
				WithMediaInfoExporter(mediainfo.NewService(nil, analyzer)),
				WithSceneDetector(stubSceneDetector{}),
				WithConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(base, "db.sqlite")}}),
			)
			selected, err := service.collectSourceEvidence(t.Context(), testCollectionRequest(t, api.Request{SourcePath: source}))
			if err != nil {
				t.Fatal(err)
			}
			// Collection inspects the primary report, then each selected file for HDR eligibility.
			wantProbes := append([]string{selected.VideoPath}, selected.FileList...)
			if !selected.TVPack || len(selected.FileList) != 12 || slices.Contains(selected.FileList, extra) ||
				!slices.Equal(analyzer.targets, wantProbes) {
				t.Fatalf("pack selection changed: pack=%t files=%v video=%q probes=%v", selected.TVPack, selected.FileList, selected.VideoPath, analyzer.targets)
			}
			if numbered && selected.VideoPath != firstEpisode {
				t.Fatalf("numbered pack did not inspect its first episode: selected=%q first=%q", selected.VideoPath, firstEpisode)
			}
			selected.ProviderMetadata.TMDB = &api.TMDBMetadata{OriginalLanguage: "Japanese"}
			derived, err := service.deriveMediaFacts(t.Context(), selected)
			if err != nil {
				t.Fatal(err)
			}
			facts := mediafacts.ResolveLanguages(api.MediaFacts{
				OriginalLanguage:      "Japanese",
				AudioLanguages:        derived.AudioLanguages,
				SubtitleLanguages:     derived.SubtitleLanguages,
				Tracks:                derived.MediaTracks,
				PrimaryAudioTrackID:   derived.PrimaryAudioTrackID,
				TrackCoverageComplete: derived.TrackCoverageComplete,
			})
			if derived.TrackCoverageComplete || facts.TrackCoverageComplete || facts.ProgrammeStatus != api.MetadataEvidenceStatusPartial ||
				!slices.Equal(facts.ProgrammeLanguages, []string{"English", "Japanese"}) {
				t.Fatalf("selected report invented complete pack coverage or commentary programme audio: %+v", facts)
			}
			before := facts.Clone()
			for _, tracker := range []string{"AITHER", "BHD", "LST"} {
				t.Run(tracker, func(t *testing.T) {
					failures, err := trackers.EvaluateTrackerValidationWithRegistry(t.Context(), registry, tracker, api.TrackerValidationSubject{
						Tracker:       tracker,
						SourcePath:    derived.SourcePath,
						VideoPath:     derived.VideoPath,
						FileList:      derived.FileList,
						Type:          derived.Type,
						Anime:         true,
						LanguageFacts: facts,
					}, api.NopLogger{})
					if err != nil {
						t.Fatal(err)
					}
					for _, failure := range failures {
						if failure.Rule == "language_evidence" ||
							strings.HasPrefix(failure.Rule, "language_") && trackers.RuleFailureBlocksExecution(failure, api.WorkflowExecutionModeNormal, false) {
							t.Errorf("%s blocked the selected report's ordinary English/Japanese audio: %+v", tracker, failure)
						}
					}
					if !reflect.DeepEqual(facts, before) {
						t.Fatal("tracker eligibility mutated canonical partial pack facts")
					}
				})
			}
			if !slices.Equal(analyzer.targets, wantProbes) || derived.VideoPath != selected.VideoPath {
				t.Fatalf("language eligibility inspected extra files or changed selection: video=%q probes=%v", derived.VideoPath, analyzer.targets)
			}
		})
	}
}
