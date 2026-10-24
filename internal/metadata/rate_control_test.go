// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata/mediainfo"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

type rateControlAnalyzer struct{ reports map[string]string }

func (a *rateControlAnalyzer) Analyze(_ context.Context, target string) (string, []byte, error) {
	return "General", []byte(a.reports[target]), nil
}

func TestProgrammeRateControlIsResourceAndInspectionBound(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "a.mkv"), filepath.Join(root, "b.mkv")
	const crf = `{"media":{"track":[{"@type":"General","Format":"Matroska"},{"@type":"Video","Format":"AVC","Encoded_Library_Name":"x264","Encoded_Library_Settings":"rc=crf / crf=18"}]}}`
	const cbr = `{"media":{"track":[{"@type":"General","Format":"Matroska"},{"@type":"Video","Format":"AVC","Encoded_Library_Name":"x264","Encoded_Library_Settings":"rc=cbr / bitrate=4000"}]}}`
	const unknown = `{"media":{"track":[{"@type":"General","Format":"Matroska"},{"@type":"Video","Format":"AVC","Encoded_Library_Name":"x264","BitRate_Mode":"VBR"}]}}`
	analyzer := &rateControlAnalyzer{reports: map[string]string{first: crf, second: cbr}}
	service := NewService(&stubRepo{}, WithMediaInfoExporter(mediainfo.NewService(nil, analyzer)), WithConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(root, "db.sqlite")}}))
	state := preparationstate.State{
		SourcePath:        root,
		VideoPath:         first,
		FileList:          []string{first, second},
		SourceFingerprint: "one",
	}
	got, err := service.inspectProgrammeTracks(t.Context(), state)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.MediaFileFacts.Files) != 2 || got.MediaFileFacts.Files[0].RateControl != api.VideoRateControlCRF || got.MediaFileFacts.Files[1].RateControl != api.VideoRateControlCBR {
		t.Fatalf("separate rate-control evidence: %+v", got.MediaFileFacts)
	}
	if got.MediaFileFacts.Files[0].ResourceID == got.MediaFileFacts.Files[1].ResourceID {
		t.Fatal("resources shared identity")
	}
	analyzer.reports[second] = unknown
	state = got
	state.SourceFingerprint = "two"
	refreshed, err := service.inspectProgrammeTracks(t.Context(), state)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.MediaFileFacts.Files[0].RateControl != api.VideoRateControlCRF || refreshed.MediaFileFacts.Files[1].RateControl != "" {
		t.Fatalf("stale rate-control authority: %+v", refreshed.MediaFileFacts)
	}
}
