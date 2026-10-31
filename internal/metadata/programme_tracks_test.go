// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/metadata/mediainfo"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestProgrammeInspectionUsesEveryResourceAndRetainsPrimary(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "episode-a.mkv"), filepath.Join(root, "episode-b.mkv")
	for _, file := range []string{first, second} {
		if err := os.WriteFile(file, []byte("synthetic"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	analyzer := &packAnalyzer{}
	service := newTestService(t,
		&stubRepo{},
		WithMediaInfoExporter(mediainfo.NewService(nil, analyzer)),
		WithConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(root, "db.sqlite")}}),
	)
	state := preparationstate.State{
		SourcePath:          root,
		VideoPath:           first,
		FileList:            []string{first, second},
		SourceFingerprint:   "inventory-a",
		PrimaryAudioTrackID: "primary",
		MediaTracks: []api.MediaTrackFacts{{
			ID:         "primary",
			Kind:       api.MediaTrackAudio,
			ResourceID: "first",
			Languages:  []string{"Japanese"},
		}},
	}
	got, err := service.inspectProgrammeTracks(t.Context(), state)
	if err != nil {
		t.Fatal(err)
	}
	if !got.TrackCoverageComplete || len(got.MediaTracks) != 2 || got.PrimaryAudioTrackID == "primary" || got.PrimaryAudioTrackID == "" ||
		got.VideoPath != first ||
		len(got.MediaFileFacts.Files) != 2 {
		t.Fatalf("inspection=%+v", got)
	}
	if got.MediaTracks[1].ResourceID == got.MediaTracks[0].ResourceID || got.MediaFileFacts.Files[1].Container != "mkv" {
		t.Fatalf("resource evidence=%+v", got.MediaFileFacts)
	}
	if got.MediaFileFacts.TechnicalStatus != api.MetadataEvidenceStatusPartial || got.MediaFileFacts.Files[1].Source != "" {
		t.Fatal("unknown resource source was promoted to complete")
	}
	if len(analyzer.targets) != 2 {
		t.Fatalf("probes=%v", analyzer.targets)
	}
	if _, err = service.inspectProgrammeTracks(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	if len(analyzer.targets) != 2 {
		t.Fatal("unchanged inspection was not cached")
	}
	state.SourceFingerprint = "inventory-b"
	if _, err = service.inspectProgrammeTracks(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	if len(analyzer.targets) != 4 {
		t.Fatal("changed inventory reused source evidence")
	}
}

type programmeReportAnalyzer struct{ report string }

func (a *programmeReportAnalyzer) Analyze(_ context.Context, target string) (string, []byte, error) {
	return "General\nComplete name : " + target, []byte(a.report), nil
}

func TestProgrammeInspectionRefreshesPrimaryAndRejectsIncompleteReports(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "a.mkv"), filepath.Join(root, "b.mkv")
	for _, file := range []string{first, second} {
		if err := os.WriteFile(file, []byte("synthetic media"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	analyzer := &programmeReportAnalyzer{report: packMediaReport}
	service := newTestService(t,
		&stubRepo{},
		WithMediaInfoExporter(mediainfo.NewService(nil, analyzer)),
		WithConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(root, "db.sqlite")}}),
	)
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
	oldID := got.PrimaryAudioTrackID
	analyzer.report = strings.ReplaceAll(packMediaReport, `"Language":"ja"`, `"Language":"en"`)
	state = got
	state.SourceFingerprint = "two"
	got, err = service.inspectProgrammeTracks(t.Context(), state)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.MediaTracks) != 2 || got.PrimaryAudioTrackID == oldID || len(got.TrackAudioLanguages) != 1 || got.TrackAudioLanguages[0] != "English" {
		t.Fatalf("stale primary: %+v", got)
	}
	for _, tc := range []struct {
		name, report string
		coverage     bool
		status       api.MetadataEvidenceStatus
	}{
		{"unknown language", strings.ReplaceAll(packMediaReport, `,"Language":"ja"`, ""), true, api.MetadataEvidenceStatusPartial},
		{"general only", `{"media":{"track":[{"@type":"General","Format":"Matroska"}]}}`, false, api.MetadataEvidenceStatusUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			analyzer.report = tc.report
			state.SourceFingerprint = tc.name
			current, inspectErr := service.inspectProgrammeTracks(t.Context(), state)
			if inspectErr != nil {
				t.Fatal(inspectErr)
			}
			if current.TrackCoverageComplete != tc.coverage || len(current.MediaFileFacts.Files) != 2 {
				t.Fatalf("coverage: %+v", current)
			}
			for _, file := range current.MediaFileFacts.Files {
				if file.AudioStatus != tc.status {
					t.Fatalf("file status: %+v", file)
				}
			}
		})
	}
}

func TestProgrammeDerivationUsesFreshPrimaryTechnicalEvidence(t *testing.T) {
	root := t.TempDir()
	stale := filepath.Join(root, "stale.json")
	if err := os.WriteFile(stale, []byte(packMediaReport), 0o600); err != nil {
		t.Fatal(err)
	}
	fresh := strings.ReplaceAll(packMediaReport, `"BitDepth":"8"`, `"BitDepth":"10"`)
	fresh = strings.ReplaceAll(fresh, `"Format":"AVC"`, `"Format":"HEVC"`)
	analyzer := &programmeReportAnalyzer{report: fresh}
	service := newTestService(t,
		&stubRepo{},
		WithMediaInfoExporter(mediainfo.NewService(nil, analyzer)),
		WithConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(root, "db.sqlite")}}),
	)
	first, second := filepath.Join(root, "a.mkv"), filepath.Join(root, "b.mkv")
	for _, file := range []string{first, second} {
		if err := os.WriteFile(file, []byte("synthetic media"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	state := preparationstate.State{
		SourcePath:           root,
		VideoPath:            first,
		FileList:             []string{first, second},
		MediaInfoJSONPath:    stale,
		SourceFingerprint:    "current",
		MetadataRequirements: api.MetadataRequirementSet{InspectProgrammeTracks: true},
	}
	got, err := service.deriveMediaFacts(t.Context(), state)
	if err != nil {
		t.Fatal(err)
	}
	if got.MediaInfoJSONPath == stale || got.MediaInfoTextPath == "" {
		t.Fatal("fresh facts retained stale parser report paths")
	}
	jsonReport, readErr := os.ReadFile(got.MediaInfoJSONPath)
	if readErr != nil || !strings.Contains(string(jsonReport), "HEVC") {
		t.Fatal("submitted JSON does not match inspected facts")
	}
	textReport, readErr := os.ReadFile(got.MediaInfoTextPath)
	if readErr != nil || !strings.Contains(string(textReport), filepath.Base(first)) {
		t.Fatal("submitted text does not match current primary")
	}
	if got.BitDepth != "10" || got.VideoCodec != "HEVC" {
		t.Fatalf("stale primary technical facts: codec=%s depth=%s", got.VideoCodec, got.BitDepth)
	}
}

func TestSingleResourceInspectionOverridesMisleadingNames(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "Example.2160p.avi")
	if err := os.WriteFile(file, []byte("synthetic media"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(root, "old.json")
	if err := os.WriteFile(stale, []byte(packMediaReport), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, codec := range []string{"WavPack", "unclassified-format", "WMA", "DTS-HD High-Res Audio", "ADPCM"} {
		report := strings.ReplaceAll(packMediaReport, `"Format":"AC-3"`, `"Format":"`+codec+`"`)
		analyzer := &programmeReportAnalyzer{report: report}
		service := newTestService(t, &stubRepo{}, WithMediaInfoExporter(mediainfo.NewService(nil, analyzer)), WithConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(root, codec, "db.sqlite")}}))
		state := preparationstate.State{
			SourcePath:           file,
			VideoPath:            file,
			FileList:             []string{file},
			MediaInfoJSONPath:    stale,
			SourceFingerprint:    codec,
			Type:                 "ENCODE",
			Container:            "avi",
			Release:              api.ReleaseInfo{Resolution: "2160p"},
			MetadataRequirements: api.MetadataRequirementSet{InspectProgrammeTracks: true},
		}
		got, err := service.deriveMediaFacts(t.Context(), state)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.MediaFileFacts.Files) != 1 || got.MediaFileFacts.Files[0].ResourceID == "" || got.MediaFileFacts.Files[0].Resolution != "1080p" || got.MediaFileFacts.Files[0].Container != "mkv" {
			t.Fatalf("filename replaced primary measurement: %+v", got.MediaFileFacts)
		}
		facts := mediafacts.ResolveLanguages(api.MediaFacts{
			OriginalLanguage:      "English",
			Tracks:                got.MediaTracks,
			PrimaryAudioTrackID:   got.PrimaryAudioTrackID,
			TrackCoverageComplete: got.TrackCoverageComplete,
		})
		subject := api.NewTrackerValidationSubject(api.UploadSubject{
			Type:           "ENCODE",
			Release:        state.Release,
			MediaFileFacts: got.MediaFileFacts,
			LanguageFacts:  facts,
		}, "LST")
		registry, err := impl.NewRegistry()
		if err != nil {
			t.Fatal(err)
		}
		failures, err := trackers.EvaluateTrackerValidationWithRegistry(t.Context(), registry, "LST", subject, api.NopLogger{})
		if err != nil {
			t.Fatal(err)
		}
		want := api.MetadataEvidenceStatusComplete
		if codec == "unclassified-format" || codec == "WMA" {
			want = api.MetadataEvidenceStatusPartial
		}
		found := false
		for _, failure := range failures {
			if strings.HasPrefix(failure.Rule, "language_lst_audio_") && failure.Disposition == api.RuleDispositionStrict && failure.EvidenceStatus == want {
				found = true
			}
		}
		if codec == "DTS-HD High-Res Audio" || codec == "ADPCM" {
			for _, failure := range failures {
				if failure.Rule == "language_lst_audio_codec_evidence" || failure.Rule == "language_lst_audio_codec" {
					t.Fatalf("measured lossy codec treated as unknown: %s %+v", codec, failure)
				}
			}
		} else if !found {
			t.Fatalf("raw codec escaped measured tier: %s %+v", codec, failures)
		}
	}
}
