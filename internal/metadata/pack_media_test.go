// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata/mediainfo"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/sp"
	"github.com/autobrr/upbrr/pkg/api"
)

const packMediaReport = `{"media":{"track":[{"@type":"General","Format":"Matroska","UniqueID":"123"},{"@type":"Video","Format":"AVC","Width":"1920","Height":"1080","BitDepth":"8"},{"@type":"Audio","Format":"AC-3","Language":"ja","Default":"Yes"}]}}`

type packAnalyzer struct{ targets []string }

func (a *packAnalyzer) Analyze(_ context.Context, target string) (string, []byte, error) {
	a.targets = append(a.targets, target)
	return "General\nComplete name : " + target, []byte(packMediaReport), nil
}

func TestSPPackPreparationPreservesPrimaryMediaAndCachesHDRTrackChecks(t *testing.T) {
	t.Parallel()
	for _, releaseType := range []string{"WEB-DL", "BluRay.REMUX"} {
		t.Run(releaseType, func(t *testing.T) {
			base := t.TempDir()
			source := filepath.Join(base, "Example.Show.S01.1080p."+releaseType+"-GRP")
			if err := os.MkdirAll(source, 0o700); err != nil {
				t.Fatal(err)
			}
			for episode := 1; episode <= 3; episode++ {
				path := filepath.Join(source, fmt.Sprintf("Example.Show.S01E%02d.1080p.%s-GRP.mkv", episode, releaseType))
				if err := os.WriteFile(path, []byte("synthetic video"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			analyzer := &packAnalyzer{}
			service := NewService(
				&stubRepo{},
				WithMediaInfoExporter(mediainfo.NewService(nil, analyzer)),
				WithSceneDetector(stubSceneDetector{}),
				WithConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(base, "db.sqlite")}}),
			)
			request := testCollectionRequest(t, api.Request{SourcePath: source})
			first, err := service.collectSourceEvidence(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if !first.TVPack || len(first.FileList) != 3 || len(analyzer.targets) != 4 || analyzer.targets[0] != first.VideoPath ||
				first.MediaInfoJSONPath == "" ||
				len(first.HDRFileEligibility) != 3 {
				t.Fatalf(
					"ordinary primary preparation: pack=%t files=%v probes=%v report=%q",
					first.TVPack,
					first.FileList,
					analyzer.targets,
					first.MediaInfoJSONPath,
				)
			}
			registry := trackers.NewRegistry()
			definition := unit3d.NewWithProfile(sp.Profile())
			if err := registry.RegisterDescriptor(trackers.Descriptor{
				Name:       "SP",
				Definition: definition,
				Metadata:   definition.MetadataPolicy(),
			}); err != nil {
				t.Fatal(err)
			}
			request.Input.MetadataRequirements, err = trackers.CollectMetadataRequirements(registry, []api.TrackerID{"SP"})
			if err != nil {
				t.Fatal(err)
			}
			selected, err := service.collectSourceEvidence(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if len(analyzer.targets) != 4 || selected.MediaInfoJSONPath != first.MediaInfoJSONPath || len(selected.MediaFileFacts.Files) != 0 {
				t.Fatalf(
					"SP guidance triggered extra probes or invented pack evidence: probes=%v report=%q facts=%+v",
					analyzer.targets,
					selected.MediaInfoJSONPath,
					selected.MediaFileFacts,
				)
			}
		})
	}
}
