package core

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/comparisons"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestComparisonOrderingUsesTitleTimeAndRejectsOriginDisagreement(t *testing.T) {
	early := screenshots.ComparisonSample{
		FrameID:                  "early",
		PTS:                      8000,
		TimeBaseNumerator:        1,
		TimeBaseDenominator:      1000,
		RelativeTimestampSeconds: 1,
	}
	late := screenshots.ComparisonSample{
		FrameID:                  "late",
		PTS:                      5000,
		TimeBaseNumerator:        1,
		TimeBaseDenominator:      1000,
		RelativeTimestampSeconds: 10,
	}
	ordered, err := orderedComparisonSamples([]screenshots.ComparisonSample{late, early, early})
	if err != nil || len(ordered) != 2 || ordered[0].FrameID != "early" {
		t.Fatalf("title ordering=%+v err=%v", ordered, err)
	}
	contradiction := early
	contradiction.PTS++
	if _, err := orderedComparisonSamples([]screenshots.ComparisonSample{early, contradiction}); err == nil {
		t.Fatal("different origin replaced an existing frame witness")
	}
	contradiction = early
	contradiction.RelativeTimestampSeconds = 2
	if _, err := orderedComparisonSamples([]screenshots.ComparisonSample{early, contradiction}); err == nil {
		t.Fatal("the same identity acquired two title times")
	}
}

func TestMainComparisonCandidatesRespectPreparedDiscAndPlaylistRoots(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "disc-a"), filepath.Join(root, "disc-b")
	discovery := api.ComparisonSourceDiscovery{Candidates: []api.ComparisonProgrammeCandidate{
		{ID: "dvd-a", DiscID: "disc-a"}, {ID: "dvd-b", DiscID: "disc-b"}, {ID: "bd-a", DiscID: "disc-a"}, {ID: "bd-other", DiscID: "disc-a"},
	}}
	private := filesystem.ComparisonDiscovery{Inputs: map[string]filesystem.ResolvedComparisonInput{
		"dvd-a": {Kind: "dvd", Root: first},
		"dvd-b": {Kind: "dvd", Root: second},
		"bd-a": {
			Kind:     "bluray",
			Root:     first,
			Playlist: "00001.mpls",
		},
		"bd-other": {
			Kind:     "bluray",
			Root:     first,
			Playlist: "00002.mpls",
		},
	}}
	constrainMainComparisonDiscovery(&discovery, private, api.ScreenshotSubject{SourcePath: root, Discs: []api.ScreenshotDiscSubject{
		{
			ID:                    "disc-a",
			Root:                  first,
			SelectedBDMVPlaylists: []api.PlaylistInfo{{File: "00001.mpls"}},
		},
	}})
	if len(discovery.Candidates) != 2 || discovery.Candidates[0].ID != "dvd-a" || discovery.Candidates[1].ID != "bd-a" {
		t.Fatalf("comparison discovery escaped prepared authority: %+v", discovery.Candidates)
	}
}

// TestLocalDiscComparisonFastAcceptance is explicitly opt-in and never contacts
// trackers/providers/image hosts. Original subjects are read-only inputs.
func TestLocalDiscComparisonFastAcceptance(t *testing.T) {
	mainPath := os.Getenv("UPBRR_COMPARISON_MAIN")
	dvdPath := os.Getenv("UPBRR_COMPARISON_DVD")
	bdPath := os.Getenv("UPBRR_COMPARISON_BLURAY")
	if mainPath == "" || dvdPath == "" || bdPath == "" {
		t.Skip("opt-in file/DVD/Blu-ray bounded acceptance")
	}
	assertDiscComparisonFastAcceptance(t, mainPath, dvdPath, bdPath, nil)
}

func assertDiscComparisonFastAcceptance(t *testing.T, mainPath, dvdPath, bdPath string, originals map[string]string) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(root, "app.sqlite")}}
	repository, err := db.Open(cfg.MainSettings.DBPath)
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
	preparedResult, err := prepared.Prepare(t.Context(), api.PrepareInput{SourcePath: mainPath})
	if err != nil {
		t.Fatal(err)
	}
	release := api.ReleaseRef{SourcePath: mainPath, Generation: preparedResult.Release.Generation}
	logger := &recordingMediaLogger{}
	service := screenshots.NewService(cfg, logger, root, nil)
	b := workflowMediaBuilder{
		config:             cfg,
		media:              &mediaModule{logger: logger, repo: repository},
		comparisonRenderer: service,
		resolver:           prepared,
	}
	scratchRoot, err := db.Subdir(cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		t.Fatal(err)
	}
	retained := workflowMediaPrivateArtifacts{ComparisonSources: map[string]workflowComparisonSource{}}
	plan := api.ComparisonPlan{
		MainSourceID:          "main",
		SizeReferenceSourceID: "main",
		RenderPolicyVersion:   1,
		Preferences:           api.ComparisonPreferences{Count: 3, ScanMode: "fast"},
	}
	selections := make([]api.ComparisonVideoSelection, 0, 3)
	for index, path := range []string{mainPath, dvdPath, bdPath} {
		id := []string{"main", "dvd", "bluray"}[index]
		var selection *api.ComparisonProgrammeSelection
		if index > 0 {
			discovery, err := b.DiscoverComparisonSource(t.Context(), release, id, path, false)
			if err != nil {
				t.Fatal(err)
			}
			wanted := os.Getenv("UPBRR_COMPARISON_" + []string{"MAIN", "DVD", "BLURAY"}[index] + "_PROGRAMME")
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
			if selection == nil {
				t.Fatal("explicit local programme selection required")
			}
		}
		selections = append(selections, api.ComparisonVideoSelection{
			ID:        id,
			Path:      path,
			Programme: selection,
		})
		plan.Sources = append(plan.Sources, api.ComparisonSource{
			ID:                id,
			Label:             id,
			PreferBFrames:     true,
			Overlay:           true,
			AllowedFrameTypes: []string{"B"},
		})
	}
	if err := b.configureComparisonVideos(t.Context(), release, &retained, &plan, selections); err != nil {
		t.Fatal(err)
	}
	for _, public := range plan.Sources {
		source := retained.ComparisonSources[public.ID].Source
		t.Logf("source=%s rate=%d/%d duration=%.3f geometry=%dx%d hdr=%v", public.ID, source.FrameRateNumerator, source.FrameRateDenominator, source.DurationSeconds, source.Width, source.Height, source.HDRFacts.Formats)
	}
	for _, indices := range [][]int{{0, 1}, {0, 2}, {0, 1, 2}} {
		if pair := os.Getenv("UPBRR_COMPARISON_PAIR"); pair != "" && (len(indices) != 2 || []string{"main", "dvd", "bluray"}[indices[1]] != pair) {
			continue
		}
		candidate := plan
		candidate.Sources = nil
		for _, index := range indices {
			candidate.Sources = append(candidate.Sources, plan.Sources[index])
		}
		logStart := len(logger.entries)
		progress := b.comparisonProgress(t.Context(), 5)
		result, observations, accepted, err := b.tryAlignedComparisons(t.Context(), &retained, candidate, comparisons.MatchOptions{Count: 3}, progress)
		progress.close()
		if err != nil || !accepted {
			if evidence := os.Getenv("UPBRR_COMPARISON_EVIDENCE"); evidence != "" {
				main := retained.ComparisonSources["main"].Source
				secondary := retained.ComparisonSources[candidate.Sources[1].ID].Source
				center := main.VideoDurationSeconds * .25
				reference, sampleErr := service.SampleComparisonSource(t.Context(), screenshots.ComparisonSampleRequest{
					Source:           main,
					StartSeconds:     center - 2,
					DurationSeconds:  4,
					StrideSeconds:    .002,
					NormalizeSDR:     true,
					MatchAspectRatio: 16.0 / 9,
				})
				if sampleErr != nil {
					t.Fatal(sampleErr)
				}
				crop, cropErr := service.DetectComparisonActivePicture(t.Context(), secondary, secondary.VideoDurationSeconds*.2-2, 4, nil)
				if cropErr != nil {
					t.Fatal(cropErr)
				}
				target, sampleErr := service.SampleComparisonSource(t.Context(), screenshots.ComparisonSampleRequest{
					Source:           secondary,
					StartSeconds:     secondary.VideoDurationSeconds*.25 - 32,
					DurationSeconds:  64,
					StrideSeconds:    .002,
					NormalizeSDR:     true,
					MatchCrop:        crop,
					MatchAspectRatio: 16.0 / 9,
				})
				if sampleErr != nil {
					t.Fatal(sampleErr)
				}
				diagnostic, diagnosticErr := comparisons.Match([]comparisons.SampledSource{{ID: "main", Frames: comparisonSamples(reference)}, {ID: "target", Frames: comparisonSamples(target)}}, "main", comparisons.MatchOptions{Count: 1, MainMoments: []float64{center}})
				t.Logf("diagnostic matching=%+v err=%v", diagnostic, diagnosticErr)
				if len(diagnostic.Rows) > 0 {
					_ = os.MkdirAll(evidence, 0o700)
					for i, frames := range [][]screenshots.ComparisonSample{reference, target} {
						wanted := diagnostic.Rows[0].Frames[i].FrameID
						for _, frame := range frames {
							if comparisonObservationID(frame.ComparisonFrameObservation) == wanted {
								source := main
								if i == 1 {
									source = secondary
								}
								image, captureErr := service.ComparisonPreview(t.Context(), screenshots.ComparisonCaptureRequest{
									Source:           source,
									TimestampSeconds: frame.RelativeTimestampSeconds,
									Replay:           frame.Replay,
								})
								if captureErr != nil {
									t.Fatal(captureErr)
								}
								if err := os.WriteFile(filepath.Join(evidence, []string{"main", "secondary"}[i]+".png"), image.ImageBytes, 0o600); err != nil {
									t.Fatal(err)
								}
								break
							}
						}
					}
				}
			}
			for _, entry := range logger.entries {
				if entry.level != "TRACE" {
					t.Log(entry.message)
				}
			}
			t.Fatalf("columns=%d bounded=%t err=%v", len(indices), accepted, err)
		}
		candidate = applyComparisonMatches(candidate, result, observations, comparisons.MatchOptions{Count: 3})
		for _, entry := range logger.entries[logStart:] {
			if strings.Contains(entry.message, "decision=verified") || strings.Contains(entry.message, "phase=alignment_budget") || strings.Contains(entry.message, "decision=bounded_local") {
				t.Log(entry.message)
			}
		}
		if err := retainMatchedComparisonWitnesses(candidate, observations, retained); err != nil {
			t.Fatal(err)
		}
		for _, row := range candidate.Rows {
			for _, cell := range row.Cells {
				if cell.TimestampSeconds == nil || cell.ObservedIdentity == nil {
					t.Fatal("match lacks canonical frame identity")
				}
				{
					found := false
					for _, observed := range observations[cell.SourceID] {
						if observed.Identity != nil && *observed.Identity == *cell.ObservedIdentity {
							found = true
							source := retained.ComparisonSources[cell.SourceID].Source
							if original := originals[cell.SourceID]; original != "" {
								ordinal := int64(math.Round(observed.RelativeTimestampSeconds * source.FrameRate))
								assertOriginalComparisonPicture(t, original, ordinal, source, observed)
							} else {
								assertOriginalProgrammePicture(t, source, observed)
							}
							break
						}
					}
					if !found {
						t.Fatal("selected picture has no independent observation")
					}
				}
			}
		}
		t.Logf("columns=%d bounded_rows=%d", len(indices), len(candidate.Rows))
		media := api.MediaArtifactSet{Comparisons: &candidate}
		manualCell := candidate.Rows[0].Cells[1]
		preview, err := b.PreviewComparisonFrame(t.Context(), release, media, retained, manualCell.SourceID, *manualCell.TimestampSeconds, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		previous, err := b.PreviewComparisonFrame(t.Context(), release, media, retained, manualCell.SourceID, preview.TimestampSeconds, -1, &preview)
		if err != nil || previous.TimestampSeconds >= preview.TimestampSeconds || previous.ObservedIdentity == nil {
			t.Fatalf("native previous-frame step failed: %+v %v", previous, err)
		}
		legacy := cloneWorkflowMediaPrivateArtifacts(retained)
		source := legacy.ComparisonSources[manualCell.SourceID]
		key, err := api.CanonicalWorkflowFingerprint(*manualCell.ObservedIdentity)
		if err != nil {
			t.Fatal(err)
		}
		old := source.Witnesses[string(key)]
		old.Version = 0
		source.Witnesses[string(key)] = old
		if !old.CanReinspectDisc() {
			t.Fatal("historical fixture lost its original authored span")
		}
		stepped, err := b.PreviewComparisonFrame(t.Context(), release, media, legacy, manualCell.SourceID, *manualCell.TimestampSeconds, -1, nil)
		if err != nil || stepped.ObservedIdentity == nil || *stepped.ObservedIdentity != *previous.ObservedIdentity {
			t.Fatalf("historical authored span did not restore a direct native step: %v", err)
		}
		if len(indices) == 2 && manualCell.SourceID == "dvd" && originals["dvd"] != "" {
			var bytes int64
			for _, span := range old.Window.Spans {
				bytes += span.End - span.Start
			}
			seconds := float64(old.Window.End90-old.Window.Start90)/90000 + old.Window.PrerollSeconds
			budget := screenshots.ComparisonDecodeBudget{
				RemainingEncodedBytes: 2 * bytes,
				AttemptRemaining:      new(2 * bytes),
				MovieSecondsLimit:     seconds,
			}
			verified, err := service.ReinspectComparisonReplay(t.Context(), screenshots.ComparisonSampleRequest{Source: source.Source, Budget: &budget}, &old)
			if err != nil || !old.SamePicture(verified) || budget.ProcessStarts != 1 || budget.RemainingEncodedBytes != 0 || *budget.AttemptRemaining != 0 || budget.ConsumedMovieSeconds != seconds {
				t.Fatalf("historical span was not verified within its physical allowance: %+v %v", budget, err)
			}
			manual, err := candidate.Clone()
			if err != nil {
				t.Fatal(err)
			}
			manual.MainSourceID, manual.SizeReferenceSourceID = manualCell.SourceID, manualCell.SourceID
			manual.Sources[0], manual.Sources[1] = manual.Sources[1], manual.Sources[0]
			manualCell.Confidence = nil
			manual.Rows = []api.ComparisonRow{{ID: "legacy", Cells: []api.ComparisonCell{manualCell, candidate.Rows[0].Cells[0]}}}
			restored := cloneWorkflowMediaPrivateArtifacts(legacy)
			guarded := api.MediaArtifactSet{Comparisons: &manual}
			captured, err := b.captureWorkflowComparisons(t.Context(), release, &guarded, &restored, manual, b.comparisonProgress(t.Context(), 1), false)
			if err != nil || captured.Rows[0].Cells[0].ArtifactID == "" {
				t.Fatalf("capture could not reestablish the missing historical disc guard: %v", err)
			}
			// Automatic rows from a historical Deep inventory can select the
			// same archived observation before and after capture verifies it.
			automatic, err := manual.Clone()
			if err != nil {
				t.Fatal(err)
			}
			automatic.Rows[0].Evidence = captured.Rows[0].Evidence
			automatic.Rows[0].Cells[0].Confidence = candidate.Rows[0].Cells[1].Confidence
			archived := map[string][]screenshots.ComparisonSample{manualCell.SourceID: {{FrameID: string(key), Replay: &old}}}
			repeated := cloneWorkflowMediaPrivateArtifacts(legacy)
			if err := retainMatchedComparisonWitnesses(automatic, archived, repeated); err != nil {
				t.Fatal(err)
			}
			media := api.MediaArtifactSet{Comparisons: &automatic}
			completed, err := b.captureWorkflowComparisons(t.Context(), release, &media, &repeated, automatic, b.comparisonProgress(t.Context(), 1), false)
			if err != nil {
				t.Fatal(err)
			}
			if err := retainMatchedComparisonWitnesses(completed, archived, repeated); err != nil ||
				repeated.ComparisonSources[manualCell.SourceID].Witnesses[string(key)].Version != 2 {
				t.Fatalf("rematching the historical authored picture lost its verified capture address: %v", err)
			}
			conflict := old
			conflict.Window.EncodedSHA256 = strings.Repeat("0", 64)
			archived[manualCell.SourceID][0].Replay = &conflict
			if err := retainMatchedComparisonWitnesses(completed, archived, repeated); err == nil {
				t.Fatal("a different historical authored span reused the captured address")
			}
			// Match the authored title to its original PAL file so this checks
			// restoration independently of a new reverse playback calibration.
			original := originals["dvd"]
			facts, err := service.InspectComparisonSource(t.Context(), original)
			if err != nil {
				t.Fatal(err)
			}
			facts.InputFingerprint, err = comparisonVideoFingerprint(t.Context(), original)
			if err != nil {
				t.Fatal(err)
			}
			legacy.ComparisonSources["main"] = workflowComparisonSource{
				Path:        original,
				Source:      facts,
				Fingerprint: facts.InputFingerprint,
			}
			manual.Sources[1].Fingerprint = facts.InputFingerprint
			manual.Rows[0].Cells[1] = api.ComparisonCell{SourceID: "main"}
			rematched, err := b.matchWorkflowComparisons(t.Context(), &legacy, manual, b.comparisonProgress(t.Context(), 5))
			if err != nil || len(rematched.Rows) != 1 || rematched.Rows[0].Cells[0].ObservedIdentity == nil || *rematched.Rows[0].Cells[0].ObservedIdentity != *manualCell.ObservedIdentity {
				t.Fatalf("manual rematch lost its historical disc selection: %v", err)
			}
		}
		vaultRoot := filepath.Join(root, "vault")
		vault, err := releaseworkflow.NewPrivateArtifactVault(vaultRoot, workflowPrivateResourceCodecs(b, workflowAudioAnalysisBuilder{root: scratchRoot})...)
		if err != nil {
			t.Fatal(err)
		}
		if err := vault.Put("local-owner", "local-workflow", "comparison-media", retained, time.Now().Add(24*time.Hour)); err != nil {
			t.Fatal(err)
		}
		if err := vault.Put("local-owner", "local-workflow", "manual-preview", preview, time.Now().Add(15*time.Minute)); err != nil {
			t.Fatal(err)
		}
		restarted := b
		restarted.comparisonRenderer = screenshots.NewService(cfg, logger, root, nil)
		reopened, err := releaseworkflow.NewPrivateArtifactVault(vaultRoot, workflowPrivateResourceCodecs(restarted, workflowAudioAnalysisBuilder{root: scratchRoot})...)
		if err != nil {
			t.Fatal(err)
		}
		private, err := reopened.Get("local-owner", "local-workflow", "comparison-media", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		hydrated, ok := private.(workflowMediaPrivateArtifacts)
		if !ok {
			t.Fatal("restart lost comparison-private media")
		}
		previewResource, err := reopened.Get("local-owner", "local-workflow", "manual-preview", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		retainedPreview, ok := previewResource.(releaseworkflow.MediaPreviewContent)
		if !ok {
			t.Fatal("restart lost manual preview")
		}
		selected, selectedPrivate, err := restarted.ApplyComparisonFrames(t.Context(), media, hydrated, []api.ComparisonFrameSelection{{
			RowID:     candidate.Rows[0].ID,
			SourceID:  manualCell.SourceID,
			PreviewID: "manual-preview",
		}}, []releaseworkflow.MediaPreviewContent{retainedPreview})
		if err != nil {
			t.Fatal(err)
		}
		media, candidate = selected, *selected.Comparisons
		hydrated, ok = selectedPrivate.(workflowMediaPrivateArtifacts)
		if !ok {
			t.Fatal("manual selection lost comparison-private media")
		}
		if err := reopened.Put("local-owner", "local-workflow", "comparison-media", hydrated, time.Now().Add(24*time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err := reopened.Get("local-owner", "local-workflow", "manual-preview", time.Now().Add(time.Hour)); err == nil {
			t.Fatal("preview remained available past its expiry")
		}
		secondRestart, err := releaseworkflow.NewPrivateArtifactVault(vaultRoot, workflowPrivateResourceCodecs(restarted, workflowAudioAnalysisBuilder{root: scratchRoot})...)
		if err != nil {
			t.Fatal(err)
		}
		applied, err := secondRestart.Get("local-owner", "local-workflow", "comparison-media", time.Now().Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		hydrated, ok = applied.(workflowMediaPrivateArtifacts)
		if !ok {
			t.Fatal("applied preview witness did not survive restart")
		}
		captureProgress := restarted.comparisonProgress(t.Context(), 1)
		captured, err := restarted.captureWorkflowComparisons(t.Context(), release, &media, &hydrated, candidate, captureProgress, false)
		captureProgress.close()
		if err != nil {
			t.Fatal(err)
		}
		if len(media.Artifacts) != 3*len(indices) {
			t.Fatalf("retained frame captures=%d", len(media.Artifacts))
		}
		for rowIndex, row := range captured.Rows {
			for columnIndex, cell := range row.Cells {
				before := candidate.Rows[rowIndex].Cells[columnIndex]
				if before.ObservedIdentity == nil || cell.ObservedIdentity == nil || *before.ObservedIdentity != *cell.ObservedIdentity {
					t.Fatal("restart capture changed frame occurrence")
				}
			}
		}
		t.Logf("columns=%d persisted_uncaptured_replay_images=%d", len(indices), len(media.Artifacts))
		captured.Fingerprint, err = comparisons.ReviewFingerprint(captured)
		if err != nil {
			t.Fatal(err)
		}
		captured.ApprovedFingerprint = captured.Fingerprint
		media.Comparisons = &captured
		restored, _, err := restarted.RestoreComparisons(t.Context(), release, api.TrackerReleaseProjectionSet{}, media, hydrated, time.Now())
		if err != nil || restored.Comparisons == nil || restored.Comparisons.ApprovedFingerprint != "" {
			t.Fatalf("retained native comparisons bypassed renewed review: %v", err)
		}
		for r, row := range captured.Rows {
			for c, cell := range row.Cells {
				actual := restored.Comparisons.Rows[r].Cells[c]
				if actual.ArtifactID != cell.ArtifactID || actual.ObservedIdentity == nil || *actual.ObservedIdentity != *cell.ObservedIdentity {
					t.Fatal("restoration changed a retained native picture")
				}
			}
		}
	}
}

// The authoring inputs have a known one-to-one frame sequence. Decode them
// sequentially, independently of the programme index, window origin and replay.
func assertOriginalProgrammePicture(t *testing.T, source screenshots.ComparisonSource, observed screenshots.ComparisonSample) {
	t.Helper()
	executable, err := screenshots.ResolveFFmpegExecutable()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"-hide_banner", "-nostats", "-copyts", "-loglevel", "info"}
	mapStream, count := "0:V:0", "1"
	filter := fmt.Sprintf("select=eq(pts\\,%d)", observed.PTS)
	switch {
	case source.Input != nil && source.Input.Kind == "dvd" && observed.Replay != nil && len(observed.Replay.Window.Spans) == 1:
		// Read the original VOB independently, without the production byte span
		// or dvdvideo's seek-adjusted title clock. Probe its first native PTS so
		// FFmpeg's relative file seek cannot add the VOB's nonzero clock twice.
		path := observed.Replay.Window.Spans[0].Path
		probe := exec.CommandContext(t.Context(), executable, "-hide_banner", "-nostats", "-copyts", "-loglevel", "info", "-f", "mpeg", "-i", path,
			"-map", "0:i:0x1e0", "-an", "-sn", "-dn", "-vf", "showinfo", "-frames:v", "1", "-f", "null", "-")
		probe.WaitDelay = 5 * time.Second
		output, err := probe.CombinedOutput()
		first := regexp.MustCompile(`\bpts_time:([0-9.]+)`).FindSubmatch(output)
		if err != nil || len(first) != 2 {
			t.Fatalf("independent original VOB clock: %v", err)
		}
		origin, err := strconv.ParseFloat(string(first[1]), 64)
		if err != nil {
			t.Fatal(err)
		}
		args = append(args, "-ss", fmt.Sprintf("%.9f", max(0, observed.TimestampSeconds-origin-10)), "-f", "mpeg", "-i", path)
		mapStream = "0:i:0x1e0"
	case source.Input != nil && source.Input.Kind == "dvd":
		// Independently locate pixels with dvdvideo's own seek/navigation. Its
		// seek-adjusted timestamps never supply identity or the expected witness.
		args = append(args, "-ss", fmt.Sprintf("%.9f", max(0, observed.RelativeTimestampSeconds-10)), "-f", "dvdvideo", "-title", strconv.Itoa(source.Input.DVD.Number), "-i", source.Input.Root)
		// dvdvideo's coarse title seek can begin well before the requested
		// time on real time maps. Search a finite 64-second original window;
		// the independent visible-picture check never uses those timestamps
		// to authorize or change the retained address.
		filter, count = "trim=duration=64", strconv.Itoa(min(50000, int(math.Ceil(source.FrameRate*64))))
	case source.Input != nil:
		path, seek := "", 0.0
		for _, item := range source.Input.Occurrences {
			occurrence := fmt.Sprintf("bluray-item-%d-atc-%d-stc-%d", item.Index, item.STC.ATCIndex, item.STC.ID)
			if observed.Identity.Occurrence == occurrence {
				path = item.Path
				seek = max(0, observed.TimestampSeconds-float64(item.STC.PresentationStart45)/45000-2)
				mapStream = fmt.Sprintf("0:i:0x%x", item.Mapping.PID)
			}
		}
		if path == "" {
			t.Fatal("independent original occurrence is unavailable")
		}
		args = append(args, "-ss", fmt.Sprintf("%.9f", seek), "-i", path)
		filter = fmt.Sprintf("select=eq(mod(pts+8589934592\\,8589934592)\\,%d)", observed.PTS%(1<<33))
	default:
		args = append(args, "-ss", fmt.Sprintf("%.9f", max(0, observed.RelativeTimestampSeconds-2)), "-i", source.Path)
	}
	args = append(args, "-map", mapStream, "-an", "-sn", "-dn", "-vf", filter+",format=rgb24,scale=64:64:flags=area,format=gray", "-frames:v", count, "-threads", "1", "-fps_mode", "passthrough", "-f", "rawvideo", "-")
	assertComparisonVisiblePicture(t, executable, args, source, observed)
}

func assertOriginalComparisonPicture(t *testing.T, path string, ordinal int64, source screenshots.ComparisonSource, observed screenshots.ComparisonSample) {
	t.Helper()
	executable, err := screenshots.ResolveFFmpegExecutable()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-i", path, "-map", "0:V:0", "-an", "-sn", "-dn",
		"-vf", fmt.Sprintf("select=eq(n\\,%d),format=rgb24,scale=64:64:flags=area,format=gray", ordinal), "-frames:v", "1", "-threads", "1", "-f", "rawvideo", "-"}
	assertComparisonVisiblePicture(t, executable, args, source, observed)
}

func assertComparisonVisiblePicture(t *testing.T, executable string, args []string, source screenshots.ComparisonSource, observed screenshots.ComparisonSample) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	frame, err := screenshots.NewService(config.Config{}, api.NopLogger{}, t.TempDir(), nil).ComparisonCapture(ctx, screenshots.ComparisonCaptureRequest{
		Source:           source,
		Replay:           observed.Replay,
		TimestampSeconds: observed.RelativeTimestampSeconds,
		ToneMap:          new(false),
	})
	if err != nil || frame.Identity == nil || *frame.Identity != *observed.Identity {
		t.Fatalf("independent native capture: %v", err)
	}
	reduce := exec.CommandContext(ctx, executable, "-hide_banner", "-loglevel", "error", "-i", "pipe:0", "-vf", "format=rgb24,scale=64:64:flags=area,format=gray", "-threads", "1", "-f", "rawvideo", "-")
	reduce.Stdin = bytes.NewReader(frame.ImageBytes)
	got, err := reduce.Output()
	if err != nil || len(got) != 4096 {
		t.Fatalf("reduce captured picture: %v", err)
	}
	// Apply the source's display geometry before RGB conversion, as the PNG
	// does. Comparing a reduced anamorphic raster directly changes edge pixels.
	w, h := source.DisplaySize()
	if w != source.Width || h != source.Height {
		for i, arg := range args {
			if arg == "-vf" {
				args[i+1] = strings.Replace(args[i+1], ",format=rgb24", fmt.Sprintf(",scale=%d:%d,setsar=1,format=rgb24", w, h), 1)
				break
			}
		}
	}
	want, err := exec.CommandContext(ctx, executable, args...).Output()
	if err != nil || len(want)%len(got) != 0 {
		t.Fatalf("independent source render: %v", err)
	}
	minimum := math.Inf(1)
	for len(want) >= len(got) {
		var delta int
		for i, pixel := range got {
			delta += int(math.Abs(float64(int(pixel) - int(want[i]))))
		}
		minimum = min(minimum, float64(delta)/float64(len(got)))
		if delta <= len(got) {
			return
		}
		want = want[len(got):]
	}
	t.Fatalf("native capture at %.9f differs from independent source rendering (mean pixel difference %.3f)", observed.RelativeTimestampSeconds, minimum)
}
