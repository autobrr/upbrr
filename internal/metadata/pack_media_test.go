// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/metadata/mediainfo"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/sp"
	"github.com/autobrr/upbrr/pkg/api"
)

const packMediaReport = `{"media":{"track":[{"@type":"General","Format":"Matroska","UniqueID":"123"},{"@type":"Video","Format":"AVC","Width":"1920","Height":"1080","BitDepth":"8"},{"@type":"Audio","Format":"AC-3","Language":"ja","Default":"Yes"}]}}`

type packAnalyzer struct {
	targets []string
	inspect func(context.Context, string) (string, error)
}

func (a *packAnalyzer) Analyze(ctx context.Context, target string) (string, []byte, error) {
	a.targets = append(a.targets, target)
	report := packMediaReport
	if a.inspect != nil {
		var err error
		report, err = a.inspect(ctx, target)
		if err != nil {
			return "", nil, err
		}
	}
	return "General\nComplete name : " + target, []byte(report), nil
}

func packCollectionFixture(t *testing.T, analyzer *packAnalyzer) (*Service, preparationstate.Request) {
	t.Helper()
	base := t.TempDir()
	source := filepath.Join(base, "Example.Show.S01.1080p.WEB-DL-GRP")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	for episode := 1; episode <= 2; episode++ {
		path := filepath.Join(source, fmt.Sprintf("Example.Show.S01E%02d.1080p.WEB-DL-GRP.mkv", episode))
		if err := os.WriteFile(path, []byte("synthetic video"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	request := testCollectionRequest(t, api.Request{SourcePath: source})
	request.SourceFingerprint = "source-a"
	request.Input.MetadataRequirements = api.MetadataRequirementSet{Requirements: []api.MetadataRequirement{{
		Scope:       api.MetadataRequirementScopeTV,
		AnyOf:       []api.MetadataRequirementField{api.MetadataRequirementNonDiscTVPackMedia},
		Disposition: api.RuleDispositionAdvisory,
	}}}
	service := NewService(&stubRepo{}, WithMediaInfoExporter(mediainfo.NewService(nil, analyzer)), WithSceneDetector(stubSceneDetector{}), WithConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(base, "db.sqlite")}}))
	return service, request
}

func TestPackMediaDemandOptsInExactlyOncePerFile(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		demand bool
		single bool
		want   int
	}{
		{
			name:   "selected pack",
			demand: true,
			want:   2,
		},
		{name: "unselected pack", want: 1},
		{
			name:   "selected single episode",
			demand: true,
			single: true,
			want:   1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			analyzer := &packAnalyzer{}
			service, request := packCollectionFixture(t, analyzer)
			if !test.demand {
				request.Input.MetadataRequirements = api.MetadataRequirementSet{}
			}
			if test.single {
				source := filepath.Join(request.Input.SourcePath, "Example.Show.S01E01.1080p.WEB-DL-GRP.mkv")
				demand := request.Input.MetadataRequirements
				request = testCollectionRequest(t, api.Request{SourcePath: source})
				request.Input.MetadataRequirements = demand
			}
			meta, err := service.collectSourceEvidence(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if len(analyzer.targets) != test.want {
				t.Fatalf("probes=%v, want %d", analyzer.targets, test.want)
			}
			if test.demand && !test.single {
				if meta.MediaFileFacts.LanguageStatus != api.MetadataEvidenceStatusComplete || meta.MediaFileFacts.TechnicalStatus != api.MetadataEvidenceStatusPartial || len(meta.MediaFileFacts.Files) != 2 {
					t.Fatalf("facts=%+v", meta.MediaFileFacts)
				}
				if meta.MediaInfoJSONPath == "" || !meta.MediaFileFacts.Files[0].Primary || meta.MediaFileFacts.Files[1].Primary {
					t.Fatalf("primary evidence=%+v", meta)
				}
				for _, file := range meta.MediaFileFacts.Files {
					if file.SubtitleStatus != api.MetadataEvidenceStatusComplete || len(file.SubtitleLanguages) != 0 || !slices.Equal(file.AudioLanguages, []string{"Japanese"}) {
						t.Fatalf("file=%+v", file)
					}
				}
			} else if len(meta.MediaFileFacts.Files) != 0 {
				t.Fatalf("unexpected per-file evidence=%+v", meta.MediaFileFacts)
			}
		})
	}
}

func TestPackMediaDemandExemptsDiscsButIncludesRemuxes(t *testing.T) {
	for _, test := range []struct {
		name, disc, releaseType string
		want                    bool
	}{
		{name: "full Blu-ray", disc: "BDMV"},
		{name: "full DVD", disc: "DVD"},
		{name: "full HD DVD", disc: "HDDVD"},
		{name: "canonical full disc", releaseType: "DISC"},
		{
			name:        "Blu-ray remux",
			disc:        "BDMV",
			releaseType: "REMUX",
			want:        true,
		},
		{
			name:        "DVD remux",
			disc:        "DVD",
			releaseType: "REMUX",
			want:        true,
		},
		{
			name:        "HD DVD remux",
			disc:        "HDDVD",
			releaseType: "REMUX",
			want:        true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := preparationstate.State{
				TVPack:               true,
				DiscType:             test.disc,
				Release:              api.ReleaseInfo{Type: test.releaseType},
				MetadataRequirements: api.MetadataRequirementSet{Requirements: []api.MetadataRequirement{{AnyOf: []api.MetadataRequirementField{api.MetadataRequirementNonDiscTVPackMedia}}}},
			}
			if got := requiresPackMediaEvidence(meta); got != test.want {
				t.Fatalf("pack evidence demand=%t, want %t", got, test.want)
			}
		})
	}
	analyzer := &packAnalyzer{}
	service, request := packCollectionFixture(t, analyzer)
	source := strings.Replace(request.Input.SourcePath, "WEB-DL", "BluRay.REMUX", 1)
	if err := os.Rename(request.Input.SourcePath, source); err != nil {
		t.Fatal(err)
	}
	demand := request.Input.MetadataRequirements
	request = testCollectionRequest(t, api.Request{SourcePath: source})
	request.Input.MetadataRequirements = demand
	if _, err := service.collectSourceEvidence(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if len(analyzer.targets) != 2 {
		t.Fatalf("remux probes=%v", analyzer.targets)
	}
}

func TestPackMediaDemandHonorsTypeOverrides(t *testing.T) {
	for _, test := range []struct {
		name, tagType string
		manualType    *string
		want          int
	}{
		{
			name:    "tag full disc",
			tagType: "DISC",
			want:    1,
		},
		{
			name:       "manual full disc",
			manualType: new("DISC"),
			want:       1,
		},
		{
			name:       "manual remux overrides tag disc",
			tagType:    "DISC",
			manualType: new("REMUX"),
			want:       2,
		},
		{
			name:       "manual disc overrides tag remux",
			tagType:    "REMUX",
			manualType: new("DISC"),
			want:       1,
		},
		{
			name:       "explicit empty overrides tag disc",
			tagType:    "DISC",
			manualType: new(""),
			want:       2,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			analyzer := &packAnalyzer{}
			service, request := packCollectionFixture(t, analyzer)
			request.Input.Instructions.ReleaseName.Type = test.manualType
			if test.tagType != "" {
				service.tagsPath = filepath.Join(t.TempDir(), "tags.json")
				if err := os.WriteFile(service.tagsPath, []byte(fmt.Sprintf(`{"GRP":{"type":%q}}`, test.tagType)), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			meta, err := service.collectSourceEvidence(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if len(analyzer.targets) != test.want || (len(meta.MediaFileFacts.Files) == 2) != (test.want == 2) {
				t.Fatalf("probes=%v facts=%+v, want %d probes", analyzer.targets, meta.MediaFileFacts, test.want)
			}
		})
	}
}

func TestPackMediaTagOverridesPreserveCacheIdentity(t *testing.T) {
	for _, selected := range []bool{false, true} {
		t.Run(fmt.Sprintf("selected=%t", selected), func(t *testing.T) {
			analyzer := &packAnalyzer{}
			service, request := packCollectionFixture(t, analyzer)
			if !selected {
				request.Input.MetadataRequirements = api.MetadataRequirementSet{}
			}
			first, err := service.collectSourceEvidence(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			probes := len(analyzer.targets)
			service.tagsPath = filepath.Join(t.TempDir(), "tags.json")
			if err := os.WriteFile(service.tagsPath, []byte(`{"GRP":{"type":"REMUX","source":"BluRay"}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			tagged, err := service.collectSourceEvidence(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if tagged.Release.Type != "REMUX" || tagged.Release.Source != "BluRay" || len(analyzer.targets) != probes ||
				tagged.MediaInfoJSONPath != first.MediaInfoJSONPath || tagged.MediaInfoTextPath != first.MediaInfoTextPath {
				t.Fatalf("tag override changed cache identity: probes=%v first=%q tagged=%q", analyzer.targets, first.MediaInfoJSONPath, tagged.MediaInfoJSONPath)
			}
		})
	}
}

func TestPackMediaDemandCollectsDiscSourcedRemux(t *testing.T) {
	originalDiscover, originalParse := discoverBDMVPlaylists, parseBDMVPlaylist
	t.Cleanup(func() { discoverBDMVPlaylists, parseBDMVPlaylist = originalDiscover, originalParse })
	discoverBDMVPlaylists = func(context.Context, string) ([]filesystem.PlaylistInfo, error) {
		return []filesystem.PlaylistInfo{{File: "00001.MPLS", Duration: 5400}}, nil
	}
	parseBDMVPlaylist = func(string) (float64, []filesystem.PlaylistItem, error) {
		return 5400, []filesystem.PlaylistItem{{File: "00001.m2ts", Size: 100}, {File: "00002.m2ts", Size: 100}}, nil
	}
	for _, selected := range []bool{false, true} {
		t.Run(fmt.Sprintf("selected=%t", selected), func(t *testing.T) {
			analyzer := &packAnalyzer{}
			service, request := packCollectionFixture(t, analyzer)
			source := strings.Replace(request.Input.SourcePath, "WEB-DL", "BluRay.REMUX", 1)
			stream := filepath.Join(source, "BDMV", "STREAM")
			if err := os.MkdirAll(stream, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(source, "BDMV", "PLAYLIST"), 0o700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"00001.m2ts", "00002.m2ts"} {
				if err := os.WriteFile(filepath.Join(stream, name), []byte("synthetic stream"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			demand := request.Input.MetadataRequirements
			request = testCollectionRequest(t, api.Request{SourcePath: source})
			request.Input.Instructions.Playlist = api.PlaylistInstruction{Set: true, Selected: []string{"00001.MPLS"}}
			wantProbes, wantFiles := 1, 0
			if selected {
				request.Input.MetadataRequirements = demand
				wantProbes, wantFiles = 3, 2
			}
			meta, err := service.collectSourceEvidence(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if meta.Type != "" || meta.Release.Type != "REMUX" || meta.DiscType != "BDMV" || !meta.TVPack || len(meta.FileList) != 2 {
				t.Fatalf("collection boundary type=%q release=%q disc=%q pack=%t", meta.Type, meta.Release.Type, meta.DiscType, meta.TVPack)
			}
			if len(analyzer.targets) != wantProbes || len(meta.MediaFileFacts.Files) != wantFiles {
				t.Fatalf("probes=%v facts=%+v, want %d probes and %d files", analyzer.targets, meta.MediaFileFacts, wantProbes, wantFiles)
			}
		})
	}
}

func TestPackMediaCacheBindsFileAndSourceIdentity(t *testing.T) {
	analyzer := &packAnalyzer{}
	service, request := packCollectionFixture(t, analyzer)
	first, err := service.collectSourceEvidence(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.collectSourceEvidence(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if len(analyzer.targets) != 2 {
		t.Fatalf("unchanged files reprobed: %v", analyzer.targets)
	}
	file := first.FileList[1]
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(file, info.ModTime().Add(time.Second), info.ModTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = service.collectSourceEvidence(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if len(analyzer.targets) != 3 || analyzer.targets[2] != file {
		t.Fatalf("mtime cache isolation=%v", analyzer.targets)
	}
	if err = os.WriteFile(file, []byte("changed size"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = service.collectSourceEvidence(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if len(analyzer.targets) != 4 {
		t.Fatalf("size cache identity=%v", analyzer.targets)
	}
	request.SourceFingerprint = "source-b"
	changed, err := service.collectSourceEvidence(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(analyzer.targets) != 6 || changed.MediaInfoJSONPath == first.MediaInfoJSONPath {
		t.Fatalf("source cache identity=%v", analyzer.targets)
	}
	source := filepath.Join(t.TempDir(), filepath.Base(request.Input.SourcePath))
	if err = os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, old := range first.FileList {
		if err = os.WriteFile(filepath.Join(source, filepath.Base(old)), []byte("synthetic video"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	demand := request.Input.MetadataRequirements
	request = testCollectionRequest(t, api.Request{SourcePath: source})
	request.Input.MetadataRequirements = demand
	if _, err = service.collectSourceEvidence(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if len(analyzer.targets) != 8 {
		t.Fatalf("same-basename source reused another pack: %v", analyzer.targets)
	}
}

func TestPackMediaFailuresRemainUnresolvedAndRetry(t *testing.T) {
	analyzer := &packAnalyzer{inspect: func(_ context.Context, target string) (string, error) {
		if strings.Contains(filepath.Base(target), "E02") {
			return "", errors.New("probe failed")
		}
		return packMediaReport, nil
	}}
	service, request := packCollectionFixture(t, analyzer)
	first, err := service.collectSourceEvidence(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.MediaFileFacts.Status != api.MetadataEvidenceStatusPartial || len(first.MediaFileFacts.Files) != 2 || first.MediaFileFacts.Files[1].AudioStatus == api.MetadataEvidenceStatusComplete {
		t.Fatalf("failed evidence=%+v", first.MediaFileFacts)
	}
	analyzer.inspect = nil
	retried, err := service.collectSourceEvidence(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(analyzer.targets) != 3 || retried.MediaFileFacts.LanguageStatus != api.MetadataEvidenceStatusComplete {
		t.Fatalf("retry probes=%v facts=%+v", analyzer.targets, retried.MediaFileFacts)
	}
}

func TestPackMediaCancellationStopsFurtherProbes(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	analyzer := &packAnalyzer{inspect: func(context.Context, string) (string, error) { cancel(); return "", context.Canceled }}
	service, request := packCollectionFixture(t, analyzer)
	_, err := service.collectSourceEvidence(ctx, request)
	if !errors.Is(err, context.Canceled) || len(analyzer.targets) != 1 {
		t.Fatalf("cancellation err=%v probes=%v", err, analyzer.targets)
	}
}

func TestPackMediaIncompleteReportsPreserveUnknowns(t *testing.T) {
	for _, test := range []struct {
		name, report    string
		audio, subtitle api.MetadataEvidenceStatus
	}{
		{
			name:     "missing audio language",
			report:   strings.Replace(packMediaReport, `"Language":"ja",`, "", 1),
			audio:    api.MetadataEvidenceStatusPartial,
			subtitle: api.MetadataEvidenceStatusComplete,
		},
		{
			name:     "unknown audio language",
			report:   strings.Replace(packMediaReport, `"Language":"ja"`, `"Language":"und"`, 1),
			audio:    api.MetadataEvidenceStatusPartial,
			subtitle: api.MetadataEvidenceStatusComplete,
		},
		{
			name:     "missing subtitle language",
			report:   strings.Replace(packMediaReport, `}]}}`, `},{"@type":"Text"}]}}`, 1),
			audio:    api.MetadataEvidenceStatusComplete,
			subtitle: api.MetadataEvidenceStatusPartial,
		},
		{
			name:     "omitted audio track",
			report:   strings.Replace(packMediaReport, `"UniqueID":"123"`, `"UniqueID":"123","AudioCount":"2"`, 1),
			audio:    api.MetadataEvidenceStatusUnavailable,
			subtitle: api.MetadataEvidenceStatusComplete,
		},
		{
			name:     "omitted subtitle track",
			report:   strings.Replace(packMediaReport, `"UniqueID":"123"`, `"UniqueID":"123","TextCount":"1"`, 1),
			audio:    api.MetadataEvidenceStatusComplete,
			subtitle: api.MetadataEvidenceStatusUnavailable,
		},
		{name: "missing video", report: `{"media":{"track":[{"@type":"General","Format":"Matroska"}]}}`},
		{name: "invalid json", report: `not json`},
	} {
		t.Run(test.name, func(t *testing.T) {
			analyzer := &packAnalyzer{inspect: func(_ context.Context, target string) (string, error) {
				if strings.Contains(filepath.Base(target), "E01") {
					return packMediaReport, nil
				}
				return test.report, nil
			}}
			service, request := packCollectionFixture(t, analyzer)
			meta, err := service.collectSourceEvidence(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if meta.MediaFileFacts.Status != api.MetadataEvidenceStatusPartial {
				t.Fatalf("partial evidence=%+v", meta.MediaFileFacts)
			}
			if file := meta.MediaFileFacts.Files[1]; file.AudioStatus != test.audio || file.SubtitleStatus != test.subtitle {
				t.Fatalf("file=%+v", file)
			}
		})
	}
	fact := packMediaFileFact("Example.Show.S01E02.2160i.BluRay.x265-GRP.mkv", false, mustParseMediaInfoDoc(packMediaReport))
	if fact.Source != "" || fact.Resolution != "1080p" || fact.VideoCodec != "AVC" || fact.VideoEncode != "" {
		t.Fatalf("filename replaced measured evidence=%+v", fact)
	}
}

func TestPackMediaProducerWarnsAboutUnmeasuredSPSource(t *testing.T) {
	analyzer := &packAnalyzer{}
	service, request := packCollectionFixture(t, analyzer)
	meta, err := service.collectSourceEvidence(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if meta.MediaFileFacts.Status != api.MetadataEvidenceStatusPartial || meta.MediaFileFacts.LanguageStatus != api.MetadataEvidenceStatusComplete {
		t.Fatalf("producer completeness=%+v", meta.MediaFileFacts)
	}
	subject := api.UploadSubject{
		SourcePath:     meta.SourcePath,
		VideoPath:      meta.VideoPath,
		FileList:       meta.FileList,
		TVPack:         true,
		Type:           "WEBDL",
		MediaFileFacts: meta.MediaFileFacts,
	}
	profile := sp.Profile()
	questionnaire := profile.Site.ProjectionQuestionnaire(trackers.PreparationInput{Meta: subject})
	if questionnaire != nil {
		t.Fatalf("unmeasured source history became a questionnaire: %+v", questionnaire)
	}
	validation := api.NewTrackerValidationSubject(subject, "SP")
	failures, err := profile.ValidationPolicy.Check(t.Context(), validation, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range failures {
		if failure.Rule == "sp_pack_uniformity" {
			t.Fatalf("uniform collected files rejected: %+v", failure)
		}
	}
	if !slices.ContainsFunc(failures, func(failure api.RuleFailure) bool {
		return failure.Rule == "guidance_sp_pack_source_history" && failure.Disposition == api.RuleDispositionAdvisory &&
			failure.EvidenceStatus == api.MetadataEvidenceStatusPartial && !trackers.RuleFailureBlocksExecution(failure, api.WorkflowExecutionModeNormal, false)
	}) {
		t.Fatalf("unknown source provenance lost passive guidance: %+v", failures)
	}
	if len(analyzer.targets) != 2 {
		t.Fatalf("projection reprobed files: %v", analyzer.targets)
	}
	validation.MediaFileFacts.Files[1].SubtitleStatus = api.MetadataEvidenceStatusPartial
	failures, err = profile.ValidationPolicy.Check(t.Context(), validation, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(failures, func(failure api.RuleFailure) bool {
		return failure.Rule == "sp_pack_uniformity" && failure.Disposition == api.RuleDispositionStrict
	}) {
		t.Fatalf("unknown subtitle evidence passed: %+v", failures)
	}
}

func TestPackMediaDemandDoesNotAddFullDiscExports(t *testing.T) {
	source := filepath.Join(t.TempDir(), "Example.Show.S01.PAL.DVD-GRP")
	root := filepath.Join(source, "VIDEO_TS")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "VTS_01_1.VOB"), []byte("synthetic disc"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, selected := range []bool{false, true} {
		exporter := &packExportRecorder{}
		request := testCollectionRequest(t, api.Request{SourcePath: source})
		if selected {
			request.Input.MetadataRequirements = api.MetadataRequirementSet{Requirements: []api.MetadataRequirement{{Scope: api.MetadataRequirementScopeTV, AnyOf: []api.MetadataRequirementField{api.MetadataRequirementNonDiscTVPackMedia}}}}
		}
		service := NewService(&stubRepo{}, WithMediaInfoExporter(exporter), WithSceneDetector(stubSceneDetector{}), WithConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "db.sqlite")}}))
		meta, err := service.collectSourceEvidence(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		if !meta.TVPack || meta.DiscType != "DVD" || len(exporter.requests) != 1 || exporter.requests[0].DiscType != "DVD" || len(meta.MediaFileFacts.Files) != 0 {
			t.Fatalf("selected=%t disc=%s pack=%t exports=%+v facts=%+v", selected, meta.DiscType, meta.TVPack, exporter.requests, meta.MediaFileFacts)
		}
	}
}

type packExportRecorder struct{ requests []mediainfo.Request }

func (e *packExportRecorder) Export(_ context.Context, request mediainfo.Request) (mediainfo.Result, error) {
	e.requests = append(e.requests, request)
	return mediainfo.Result{}, nil
}

func TestPackMediaDemandCannotReuseLegacyPrimaryOnlyArtifacts(t *testing.T) {
	analyzer := &packAnalyzer{}
	service, request := packCollectionFixture(t, analyzer)
	demand := request.Input.MetadataRequirements
	request.Input.MetadataRequirements = api.MetadataRequirementSet{}
	legacy, err := service.collectSourceEvidence(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(analyzer.targets) != 1 {
		t.Fatalf("legacy probes=%v", analyzer.targets)
	}
	request.Input.MetadataRequirements = demand
	pack, err := service.collectSourceEvidence(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(analyzer.targets) != 3 || pack.MediaInfoJSONPath == legacy.MediaInfoJSONPath || len(pack.MediaFileFacts.Files) != 2 {
		t.Fatalf("demand reused primary-only evidence: probes=%v pack=%+v", analyzer.targets, pack.MediaFileFacts)
	}
	if _, err = service.collectSourceEvidence(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if len(analyzer.targets) != 3 {
		t.Fatalf("unchanged pack reprobed=%v", analyzer.targets)
	}
}

func TestPackMediaMissingBitDepthStaysUnresolvedWithRetainedSourceAnswer(t *testing.T) {
	analyzer := &packAnalyzer{}
	service, request := packCollectionFixture(t, analyzer)
	complete, err := service.collectSourceEvidence(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	subject := api.UploadSubject{
		SourcePath:     complete.SourcePath,
		VideoPath:      complete.VideoPath,
		FileList:       complete.FileList,
		TVPack:         true,
		Type:           "WEBDL",
		MediaFileFacts: complete.MediaFileFacts,
	}
	profile := sp.Profile()
	question := profile.Site.ProjectionQuestionnaire(trackers.PreparationInput{Meta: subject})
	if question != nil {
		t.Fatalf("initial source history became a questionnaire: %+v", question)
	}
	subject.TrackerQuestionnaireAnswers = map[string]map[string]string{"SP": {"pack_source_consistency_legacy": "consistent"}}
	analyzer.inspect = func(context.Context, string) (string, error) {
		return strings.Replace(packMediaReport, `,"BitDepth":"8"`, "", 1), nil
	}
	request.SourceFingerprint = "missing-bit-depth"
	missing, err := service.collectSourceEvidence(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	subject.MediaFileFacts = missing.MediaFileFacts
	for _, file := range subject.MediaFileFacts.Files {
		if file.BitDepth != "0" {
			t.Fatalf("missing measured bit depth=%q", file.BitDepth)
		}
	}
	if question := profile.Site.ProjectionQuestionnaire(trackers.PreparationInput{Meta: subject}); question != nil {
		t.Fatalf("unmeasured technical evidence offered waiver questions: %+v", question)
	}
	failures, err := profile.ValidationPolicy.Check(t.Context(), api.NewTrackerValidationSubject(subject, "SP"), api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(failures, func(failure api.RuleFailure) bool {
		return failure.Rule == "sp_pack_uniformity" && failure.Disposition == api.RuleDispositionStrict && strings.Contains(failure.Reason, "bit depth")
	}) {
		t.Fatalf("unmeasured bit depth accepted source answer: %+v", failures)
	}
}

func TestPackPrimaryProbeFailureStopsPreparation(t *testing.T) {
	failure := errors.New("synthetic primary probe failure")
	analyzer := &packAnalyzer{inspect: func(_ context.Context, target string) (string, error) {
		if strings.Contains(filepath.Base(target), "E01") {
			return "", failure
		}
		return packMediaReport, nil
	}}
	service, request := packCollectionFixture(t, analyzer)
	_, err := service.collectSourceEvidence(t.Context(), request)
	if !errors.Is(err, failure) || len(analyzer.targets) != 1 {
		t.Fatalf("primary failure err=%v probes=%v", err, analyzer.targets)
	}
}

func TestPackPrimaryStatFailureStopsPreparation(t *testing.T) {
	analyzer := &packAnalyzer{}
	service, request := packCollectionFixture(t, analyzer)
	meta, err := service.collectSourceEvidence(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(meta.VideoPath); err != nil {
		t.Fatal(err)
	}
	err = service.collectPackMediaEvidence(t.Context(), &meta)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("primary stat failure did not stop preparation: %v", err)
	}
}

func TestPackPrimaryMalformedReportStopsPreparation(t *testing.T) {
	analyzer := &packAnalyzer{inspect: func(context.Context, string) (string, error) { return "not json", nil }}
	service, request := packCollectionFixture(t, analyzer)
	if _, err := service.collectSourceEvidence(t.Context(), request); err == nil {
		t.Fatal("malformed primary report did not stop preparation")
	}
	if len(analyzer.targets) != 1 {
		t.Fatalf("continued after malformed primary report: %v", analyzer.targets)
	}
}
