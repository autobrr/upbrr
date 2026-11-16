// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

type localSceneAcceptanceManifest struct {
	OutputDir                string
	DiscoveryOnly, Capture   bool
	RecordWindows            bool
	AllowUnderfill           bool
	ReuseFrom                string
	RequireMainReuse         bool
	UnrestrictedPictureTypes bool
	DiagnosticMoments        []float64
	Repeat                   int
	Variants                 []api.ComparisonPreferences
	Preferences              api.ComparisonPreferences
	Sources                  []struct {
		ID, Path, Kind, Programme string
		HighestRanked             bool
	}
}

type localSceneSourceWork struct {
	SampleSeconds, AnalysisSeconds                    float64
	SamplePictures, AnalysisPictures, AnalysisWindows int
	ProbeSeconds                                      float64
	AdmittedEncodedBytes                              int64
}
type localSceneRecorder struct {
	*screenshots.Service
	ids    map[string]string
	work   map[string]localSceneSourceWork
	output string
	window int
}

func (r *localSceneRecorder) DetectComparisonActivePicture(ctx context.Context, source screenshots.ComparisonSource, start, seconds float64, budget *screenshots.ComparisonDecodeBudget) (*screenshots.ComparisonCrop, error) {
	id := r.ids[source.InputFingerprint]
	work := r.work[id]
	work.ProbeSeconds += seconds
	before := budget.RemainingEncodedBytes
	crop, err := r.Service.DetectComparisonActivePicture(ctx, source, start, seconds, budget)
	work.AdmittedEncodedBytes += before - budget.RemainingEncodedBytes
	r.work[id] = work
	if err != nil {
		return crop, fmt.Errorf("record comparison crop probe: %w", err)
	}
	return crop, nil
}

func (r *localSceneRecorder) SampleComparisonSource(ctx context.Context, request screenshots.ComparisonSampleRequest) ([]screenshots.ComparisonSample, error) {
	id := r.ids[request.Source.InputFingerprint]
	work := r.work[id]
	work.SampleSeconds += request.DurationSeconds
	before := int64(0)
	if request.Budget != nil {
		before = request.Budget.RemainingEncodedBytes
	}
	frames, err := r.Service.SampleComparisonSource(ctx, request)
	if r.output != "" {
		r.window++
		data, marshalErr := json.Marshal(struct {
			ID              string
			Start, Duration float64
			Frames          []screenshots.ComparisonSample
		}{id, request.StartSeconds, request.DurationSeconds, frames})
		if marshalErr != nil {
			return nil, fmt.Errorf("encode comparison window receipt: %w", marshalErr)
		}
		if writeErr := os.WriteFile(filepath.Join(r.output, fmt.Sprintf("window-%03d.json", r.window)), data, 0o600); writeErr != nil {
			return nil, fmt.Errorf("comparison scene analysis: %w", writeErr)
		}
	}
	work.SamplePictures += len(frames)
	if request.Budget != nil {
		work.AdmittedEncodedBytes += before - request.Budget.RemainingEncodedBytes
	}
	r.work[id] = work
	if err != nil {
		return frames, fmt.Errorf("comparison scene analysis: %w", err)
	}
	return frames, nil
}

func (r *localSceneRecorder) AnalyzeComparisonSource(ctx context.Context, request screenshots.ComparisonSampleRequest) ([]screenshots.ComparisonSample, error) {
	id := r.ids[request.Source.InputFingerprint]
	work := r.work[id]
	work.AnalysisSeconds += request.DurationSeconds
	work.AnalysisWindows++
	before := int64(0)
	if request.Budget != nil {
		before = request.Budget.RemainingEncodedBytes
	}
	frames, err := r.Service.AnalyzeComparisonSource(ctx, request)
	work.AnalysisPictures += len(frames)
	if request.Budget != nil {
		work.AdmittedEncodedBytes += before - request.Budget.RemainingEncodedBytes
	}
	r.work[id] = work
	if err != nil {
		return frames, fmt.Errorf("comparison scene analysis: %w", err)
	}
	return frames, nil
}

// TestLocalComparisonSceneAcceptance extends the opt-in native comparison
// acceptance with an external private source/selection manifest. It calls the
// production configure, match, capture and restore owners, never remote services.
func TestLocalComparisonSceneAcceptance(t *testing.T) {
	manifestPath := os.Getenv("UPBRR_COMPARISON_SCENE_MANIFEST")
	if manifestPath == "" {
		t.Skip("set the private comparison scene acceptance manifest")
	}
	var manifest localSceneAcceptanceManifest
	payload, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(payload, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Sources) < 2 || manifest.Sources[0].ID != "main" || manifest.OutputDir == "" {
		t.Fatal("private acceptance needs main-first sources and an output directory")
	}
	if err := os.MkdirAll(manifest.OutputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	parent := manifest.OutputDir
	//nolint:usetesting // Opt-in private acceptance receipts must survive test cleanup.
	attempt, err := os.MkdirTemp(parent, "attempt-")
	if err != nil {
		t.Fatal(err)
	}
	manifest.OutputDir = attempt
	locator, err := json.Marshal(struct{ Directory string }{attempt})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "latest-attempt.json"), locator, 0o600); err != nil {
		t.Fatal(err)
	}
	write := func(name string, value any) {
		t.Helper()
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(manifest.OutputDir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.MainSettings.DBPath = filepath.Join(manifest.OutputDir, "app.sqlite")
	repositoryPath := cfg.MainSettings.DBPath
	if manifest.ReuseFrom != "" {
		cfg.MainSettings.DBPath = filepath.Join(manifest.ReuseFrom, "app.sqlite")
	}
	repository, err := db.Open(repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	if err := repository.Migrate(); err != nil {
		t.Fatal(err)
	}
	prepared, err := preparedrelease.New(repository, workflowMediaIdentityResolver{}, workflowMediaCollector{})
	if err != nil {
		t.Fatal(err)
	}
	mainPath := manifest.Sources[0].Path
	preparedResult, err := prepared.Prepare(t.Context(), api.PrepareInput{SourcePath: mainPath})
	if err != nil {
		t.Fatal(err)
	}
	release := api.ReleaseRef{SourcePath: mainPath, Generation: preparedResult.Release.Generation}
	logger := &recordingMediaLogger{}
	renderer := &localSceneRecorder{
		Service: screenshots.NewService(*cfg, logger, manifest.OutputDir, nil),
		ids:     map[string]string{},
		work:    map[string]localSceneSourceWork{},
	}
	if manifest.RecordWindows {
		renderer.output = manifest.OutputDir
	}
	b := workflowMediaBuilder{
		config:             *cfg,
		media:              &mediaModule{logger: logger, repo: repository},
		comparisonRenderer: renderer,
		resolver:           prepared,
	}
	retained := workflowMediaPrivateArtifacts{ComparisonSources: map[string]workflowComparisonSource{}}
	plan := api.ComparisonPlan{
		MainSourceID:          "main",
		RenderPolicyVersion:   1,
		SizeReferenceSourceID: "main",
		Preferences:           manifest.Preferences,
	}
	if plan.Preferences.Count == 0 {
		plan.Preferences.Count = 9
	}
	if plan.Preferences.ScanMode == "" {
		plan.Preferences.ScanMode = "fast"
	}
	selections := make([]api.ComparisonVideoSelection, 0, len(manifest.Sources))
	for _, source := range manifest.Sources {
		var selection *api.ComparisonProgrammeSelection
		if source.Kind == "dvd" || source.Kind == "bluray" {
			discovery, err := b.DiscoverComparisonSource(t.Context(), release, source.ID, source.Path, false)
			if err != nil {
				t.Fatal(err)
			}
			write(source.ID+"-discovery.json", discovery)
			wanted := source.Programme
			if source.HighestRanked {
				for _, candidate := range discovery.Candidates {
					if candidate.Supported && (wanted == "" || candidate.Rank == 0) {
						wanted = candidate.ID
						break
					}
				}
			}
			if wanted == "" && len(discovery.Candidates) == 1 {
				wanted = discovery.Candidates[0].ID
			}
			for _, candidate := range discovery.Candidates {
				if candidate.ID == wanted {
					selection = &api.ComparisonProgrammeSelection{
						Kind:                 candidate.Kind,
						DiscID:               candidate.DiscID,
						CandidateID:          candidate.ID,
						DiscoveryFingerprint: discovery.Fingerprint,
					}
				}
			}
			if selection == nil && !manifest.DiscoveryOnly {
				t.Fatal("private programme selection is ambiguous; inspect the retained discovery manifest")
			}
		}
		selections = append(selections, api.ComparisonVideoSelection{
			ID:        source.ID,
			Path:      source.Path,
			Programme: selection,
		})
		plan.Sources = append(plan.Sources, api.ComparisonSource{
			ID:            source.ID,
			Label:         source.ID,
			Overlay:       true,
			PreferBFrames: !manifest.UnrestrictedPictureTypes,
			AllowedFrameTypes: func() []string {
				if manifest.UnrestrictedPictureTypes {
					return nil
				}
				return []string{"B"}
			}(),
		})
	}
	if manifest.DiscoveryOnly {
		return
	}
	if err := b.configureComparisonVideos(t.Context(), release, &retained, &plan, selections); err != nil {
		t.Fatal(err)
	}
	if manifest.ReuseFrom != "" {
		data, err := os.ReadFile(filepath.Join(manifest.ReuseFrom, "matched-private.json"))
		if err != nil {
			t.Fatal(err)
		}
		old, err := decodeWorkflowMediaPrivateArtifacts(b, data)
		if err != nil {
			t.Fatal(err)
		}
		if old.ComparisonSources[plan.MainSourceID].Fingerprint != retained.ComparisonSources[plan.MainSourceID].Fingerprint {
			t.Fatal("reused inventory main input changed")
		}
		retained.ComparisonInventory = cloneComparisonInventory(old.ComparisonInventory)
		retained.ComparisonAnalysisExecutable = old.ComparisonAnalysisExecutable
	}
	for id, source := range retained.ComparisonSources {
		renderer.ids[source.Fingerprint] = id
	}
	write("sources-private.json", retained.ComparisonSources)
	write("analysis-executable-path-private.json", func() string {
		path, err := screenshots.ResolveFFmpegExecutable()
		if err != nil {
			t.Fatal(err)
		}
		return path
	}())
	if len(manifest.DiagnosticMoments) > 0 {
		aspect := 0.0
		for _, source := range retained.ComparisonSources {
			w, h := source.Source.DisplaySize()
			aspect = max(aspect, float64(w)/float64(h))
		}
		for _, moment := range manifest.DiagnosticMoments {
			mainFrames, err := renderer.SampleComparisonSource(t.Context(), screenshots.ComparisonSampleRequest{
				Source:           retained.ComparisonSources["main"].Source,
				StartSeconds:     moment - 2,
				DurationSeconds:  4,
				StrideSeconds:    .002,
				NativeFrames:     true,
				NormalizeSDR:     true,
				MatchAspectRatio: aspect,
			})
			if err != nil {
				t.Fatal(err)
			}
			targetFrames, err := renderer.SampleComparisonSource(t.Context(), screenshots.ComparisonSampleRequest{
				Source:           retained.ComparisonSources[manifest.Sources[1].ID].Source,
				StartSeconds:     moment - 13.259 - 6,
				DurationSeconds:  12,
				StrideSeconds:    .002,
				NativeFrames:     true,
				NormalizeSDR:     true,
				MatchAspectRatio: aspect,
			})
			if err != nil {
				t.Fatal(err)
			}
			write(fmt.Sprintf("moment-%0.3f.json", moment), struct {
				Main, Target []screenshots.ComparisonSample
			}{mainFrames, targetFrames})
		}
		return
	}
	baseOutput := manifest.OutputDir
	for index := range max(1, manifest.Repeat, len(manifest.Variants)) {
		if manifest.Repeat > 1 || len(manifest.Variants) > 0 {
			manifest.OutputDir = filepath.Join(baseOutput, fmt.Sprintf("run-%02d", index+1))
			if err := os.MkdirAll(manifest.OutputDir, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if len(manifest.Variants) > 0 {
			plan.Preferences = manifest.Variants[index%len(manifest.Variants)]
		}
		logger.entries = nil
		renderer.work = map[string]localSceneSourceWork{}
		_, resourcePayload, err := retained.MarshalPrivateResource()
		if err != nil {
			t.Fatal(err)
		}
		retained, err = decodeWorkflowMediaPrivateArtifacts(b, resourcePayload)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		progress := b.comparisonProgress(t.Context(), 5)
		matched, matchErr := b.matchWorkflowComparisons(t.Context(), &retained, plan, progress)
		progress.close()
		matchSeconds := time.Since(start).Seconds()
		write("match.json", matched)
		write("work.json", renderer.work)
		write("inventory-private.json", retained.ComparisonInventory)
		write("analysis-executable.json", retained.ComparisonAnalysisExecutable)
		_, privatePayload, privateErr := retained.MarshalPrivateResource()
		if privateErr != nil {
			t.Fatal(privateErr)
		}
		if err := os.WriteFile(filepath.Join(manifest.OutputDir, "matched-private.json"), privatePayload, 0o600); err != nil {
			t.Fatal(err)
		}
		var logs []struct{ Level, Message string }
		for _, entry := range logger.entries {
			logs = append(logs, struct{ Level, Message string }{entry.level, entry.message})
		}
		write("logs-private.json", logs)
		write("timing.json", struct {
			MatchSeconds   float64
			CaptureSeconds float64
		}{MatchSeconds: matchSeconds})
		if matchErr != nil {
			t.Fatal(matchErr)
		}
		for id, work := range renderer.work {
			if id != "main" && work.AnalysisWindows != 0 {
				t.Fatal("a secondary received scene discovery")
			}
			if id == "main" && manifest.RequireMainReuse &&
				(work.ProbeSeconds != 0 || work.AnalysisSeconds != 0 || work.SampleSeconds != 0) {
				t.Fatal("compatible Deep reuse decoded the main input")
			}
		}
		if len(matched.Rows) != plan.Preferences.Count && !manifest.AllowUnderfill {
			t.Fatalf("requested rows=%d verified rows=%d; inspect the private receipt", plan.Preferences.Count, len(matched.Rows))
		}
		matchedWitnesses := map[string]map[string]screenshots.ComparisonFrameReplay{}
		for id, source := range retained.ComparisonSources {
			matchedWitnesses[id] = source.Witnesses
		}
		write("matched-witnesses-private.json", matchedWitnesses)
		for _, row := range matched.Rows {
			for _, cell := range row.Cells {
				if cell.ObservedIdentity == nil || cell.TimestampSeconds == nil {
					t.Fatal("a selected row lacks a native identity")
				}
				key, _ := api.CanonicalWorkflowFingerprint(*cell.ObservedIdentity)
				source := retained.ComparisonSources[cell.SourceID]
				witness, ok := source.Witnesses[string(key)]
				if !ok {
					t.Fatal("a selected row lost its private replay")
				}
				assertOriginalProgrammePicture(t, source.Source, screenshots.ComparisonSample{
					Replay:                   &witness,
					Identity:                 cell.ObservedIdentity,
					PTS:                      cell.ObservedIdentity.PTS,
					TimestampSeconds:         float64(cell.ObservedIdentity.PTS) * float64(cell.ObservedIdentity.TimeBaseNumerator) / float64(cell.ObservedIdentity.TimeBaseDenominator),
					RelativeTimestampSeconds: *cell.TimestampSeconds,
					PictureChecksum:          witness.PictureChecksum,
				})
			}
		}
		if !manifest.Capture {
			continue
		}
		_, encoded, err := retained.MarshalPrivateResource()
		if err != nil {
			t.Fatal(err)
		}
		hydrated, err := decodeWorkflowMediaPrivateArtifacts(b, encoded)
		if err != nil {
			t.Fatal(err)
		}
		media := api.MediaArtifactSet{Comparisons: &matched}
		captureStart := time.Now()
		captureProgress := b.comparisonProgress(t.Context(), 1)
		captured, captureErr := b.captureWorkflowComparisons(t.Context(), release, &media, &hydrated, matched, captureProgress, false)
		captureProgress.close()
		write("capture.json", captured)
		write("timing.json", struct{ MatchSeconds, CaptureSeconds float64 }{matchSeconds, time.Since(captureStart).Seconds()})
		if captureErr != nil {
			t.Fatal(captureErr)
		}
		width, height := retained.ComparisonSources[plan.MainSourceID].Source.DisplaySize()
		for _, row := range captured.Rows {
			for _, cell := range row.Cells {
				image := hydrated.ArtifactImages[cell.ArtifactID]
				if image.Width != width || image.Height != height {
					t.Fatal("final comparison canvas differs from the main input")
				}
			}
		}
		write("images-private.json", hydrated.ArtifactImages)
		retained = hydrated
	}
}
