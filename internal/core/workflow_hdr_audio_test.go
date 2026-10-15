// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestHDRPreparedAudioWorkflowExpandsSelectionWithoutNativeRecapture(t *testing.T) {
	if os.Getenv("UPBRR_HDR_DECODE_CALIBRATION") != "1" {
		t.Skip("requires actual shared decoder")
	}
	builder, resolver, _ := hdrFixture(t)
	uploads := &hdrUploadCloneRecorder{}
	builder.uploads = uploads
	executable, err := screenshots.ResolveFFmpegExecutable()
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "Synthetic.Prepared.Tracks.mkv")
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-i", filepath.Join("..", "services", "hdranalysis", "testdata", "decoded-pq-cuts-25.mkv"),
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=4.8", "-map", "0:v:0", "-map", "1:a:0", "-map", "1:a:0",
		"-c:v", "copy", "-c:a", "pcm_s16le", source}
	if output, err := exec.CommandContext(t.Context(), executable, args...).CombinedOutput(); err != nil {
		t.Fatalf("author prepared source: %v %s", err, output)
	}
	facts, err := hdranalysis.InspectFileSource(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	target := &resolver.subject.Targets[0]
	target.Path, target.DiscRoot, target.Target.Evidence = source, "", facts
	target.Audio = &api.AudioAnalysisSubject{
		VideoPath:           source,
		SourceFingerprint:   resolver.subject.SourceFingerprint,
		ResourceID:          "resource",
		ManifestFingerprint: "manifest",
		PrimaryTrackID:      "main",
		Tracks: []api.MediaTrackFacts{
			{
				ID:            "main",
				ResourceID:    "resource",
				Kind:          api.MediaTrackAudio,
				Ordinal:       1,
				Codec:         "pcm",
				Channels:      1,
				SampleRate:    48000,
				ChannelLayout: "mono",
			},
			{
				ID:            "alternate",
				ResourceID:    "resource",
				Kind:          api.MediaTrackAudio,
				Ordinal:       2,
				Codec:         "pcm",
				Channels:      1,
				SampleRate:    48000,
				ChannelLayout: "mono",
			},
		},
	}
	request := api.HDRAnalysisInstructions{
		Release:        resolver.subject.Release,
		TargetIDs:      []string{target.Target.ID},
		ProfileVersion: api.HDRAnalysisProfileV3,
		Decoded:        &api.HDRDecodedOptions{Audio: true},
	}
	entries := make(map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord)
	resources := make(map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource)
	publish := func(record releaseworkflow.HDRExtractionRecord, resource releaseworkflow.RetainedHDRExtractionResource) error {
		entries[record.ID], resources[record.ID] = record, resource
		return nil
	}
	store := releaseworkflow.HDRMeasurementStore{Records: make(map[string]releaseworkflow.HDRMeasurementRecord), Resources: make(map[string]releaseworkflow.RetainedHDRMeasurementResource)}
	store.Publish = func(record releaseworkflow.HDRMeasurementRecord, resource releaseworkflow.RetainedHDRMeasurementResource) error {
		if len(entries) != 1 || record.AudioTrackID == "" {
			t.Fatal("track checkpoint preceded native checkpoint or lost track identity")
		}
		if _, err := restoreHDRMeasurement(t.Context(), record, resource); err != nil {
			t.Fatal(err)
		}
		store.Records[record.ID], store.Resources[record.ID] = record, resource
		return nil
	}
	var prior *api.HDRAnalysisResult
	var priorResource releaseworkflow.RetainedHDRAnalysisResource
	for _, phase := range []string{"primary", "all", "missing-alternate", "retry", "source-free", "missing"} {
  uploads.paths = nil
  request.DecodedRetry = nil
  if phase == "missing-alternate" {
   for id, record := range store.Records { if record.AudioTrackID == "alternate" { delete(store.Resources,id) } }
  }
  if phase == "retry" {
   request.DecodedRetry = &api.HDRDecodedRetry{AnalysisAttemptID:prior.AttemptID,Modes:[]api.HDRDecodedMode{api.HDRDecodedAudio}}
  }
  if phase == "all" {
			request.Decoded = &api.HDRDecodedOptions{Audio: true, AllAudioTracks: true}
		}
		if phase == "source-free" {
			if err := os.Remove(source); err != nil {
				t.Fatal(err)
			}
		}
		if phase == "missing" {
			clear(store.Resources)
		}
		result, owned, err := builder.Build(t.Context(), releaseworkflow.HDRPreparation{Release: request.Release}, request,
			"audio-"+phase, time.Now(), prior, priorResource, entries, resources, publish, &store)
		if err != nil || len(result.Targets) != 1 || len(result.Targets[0].Decoded) != 1 {
			t.Fatalf("%s result=%+v error=%v", phase, result, err)
		}
		want := 2
		if phase == "primary" {
			want = 1
		}
		audio := result.Targets[0].Decoded[0]
		records := want
  if phase == "retry" || phase == "source-free" || phase == "missing" {records++}
  if len(entries) != 1 || len(store.Records) != records || len(audio.AudioTracks) != want || audio.Selected != want {
			t.Fatalf("%s recaptured metadata or lost exact selection: native=%d records=%d audio=%+v", phase, len(entries), len(store.Records), audio)
		}
		if phase != "missing" && phase != "missing-alternate" && (audio.Measured != want || len(audio.Artifacts) != want*3 || audio.Coverage != api.HDRCoverageComplete) {
			t.Fatalf("%s lost measured outputs: %+v", phase, audio)
		}
		if phase == "missing-alternate" && (audio.Measured != 1 || audio.AudioTracks[0].State != api.HDRDecodedComplete || audio.AudioTracks[1].State != api.HDRDecodedFailed) {
   t.Fatalf("missing sibling did not produce mixed outcome: %+v",audio)
  }
  if phase == "missing" && (audio.Measured != 0 || audio.Coverage != api.HDRCoveragePartial) {
			t.Fatalf("missing reports granted source reads or PCM authority: %+v", audio)
		}
		if phase == "all" && len(uploads.paths) != 2 || phase == "source-free" && len(uploads.paths) != 4 {
			t.Fatalf("%s lost identical audio image upload provenance: %+v", phase, uploads.paths)
		}
		result.ID, result.WorkflowID, result.Revision = "analysis", "workflow", 1
		if err := result.Validate(); err != nil {
			t.Fatalf("%s invalid public result: %v", phase, err)
		}
		if phase == "all" {
			var exact api.ExactMediaAssets
			if err := appendWorkflowHDRAssets(t.Context(), &exact, result, owned); err != nil || len(exact.HDRStats) != want {
				t.Fatalf("per-track description statistics unavailable: count=%d error=%v", len(exact.HDRStats), err)
			}
			directory, err := hdrAttemptRoot(builder.root, request.Release, "hdr-analysis", "audio-copy")
			if err != nil {
				t.Fatal(err)
			}
			if err := createHDRDirectory(directory); err != nil {
				t.Fatal(err)
			}
			copyResource := hdrManagedResource{
				Root:      builder.root,
				Directory: directory,
				Kind:      "analysis",
				Files:     make(map[api.PublicResourceID]hdrManagedFile),
			}
			copied, _, err := copyNativeHDRTarget(t.Context(), result, result.Targets[0], owned, copyResource, "audio-copy", 0)
			if err != nil {
				t.Fatalf("copy per-track artifacts: %v", err)
			}
			copyResult := result
			copyResult.Targets = []api.HDRAnalysisTargetResult{copied}
			copyResult.AttemptID = "audio-copy"
			if err := copyResult.Validate(); err != nil {
				t.Fatalf("copied ownership: %v", err)
			}
			if copied.Decoded[0].AudioTracks[0].Artifacts[0] == result.Targets[0].Decoded[0].AudioTracks[0].Artifacts[0] {
				t.Fatal("copied track retained original artifact IDs")
			}
			paths := regeneratedHDRImagePaths(t.Context(), result, copyResult, owned, copyResource)
			if len(paths) != 4 {
				t.Fatalf("identical images lost upload reuse: %+v", paths)
			}
			canceled, cancel := context.WithCancel(t.Context())
			cancel()
			if paths := regeneratedHDRImagePaths(canceled, result, copyResult, owned, copyResource); len(paths) != 0 {
				t.Fatal("canceled integrity verification authorized upload reuse")
			}
			image := copyResult.Targets[0].SelectableImages()[0]
			file := copyResource.Files[image.ID]
			data, err := os.ReadFile(file.Path)
			if err != nil {
				t.Fatal(err)
			}
			// A valid PNG with changed bytes must not inherit its previous URL.
			if err := os.WriteFile(file.Path, append(data, 0), 0o600); err != nil {
				t.Fatal(err)
			}
			copyResource.Files[image.ID], err = hdrFileIntegrity(file.Path)
			if err != nil {
				t.Fatal(err)
			}
			if paths := regeneratedHDRImagePaths(t.Context(), result, copyResult, owned, copyResource); len(paths) != 3 {
				t.Fatalf("changed image inherited upload provenance: %+v", paths)
			}
			var restoredExact api.ExactMediaAssets
			if err := appendWorkflowHDRAssets(t.Context(), &restoredExact, copyResult, copyResource); err != nil || len(restoredExact.HDRStats) != want {
				t.Fatalf("copied per-track description statistics unavailable: %v", err)
			}
		}
		prior, priorResource = &result, owned
	}
}

type hdrUploadCloneRecorder struct {
	api.MediaAssetRepository
	paths map[string]string
}

func (r *hdrUploadCloneRecorder) CloneHDRAnalysisUploads(_ context.Context, _ api.ReleaseRef, _ api.PreparedMediaBinding, paths map[string]string) error {
	r.paths = paths
	return nil
}

func TestHDRPreparedAudioCheckpointIdentityAndOwnership(t *testing.T) {
	builder, resolver, _ := hdrFixture(t)
	report := &hdranalysis.MeasurementReport{
		Schema:     "hdr-measurement-v2",
		Dependency: hdranalysis.NativeDependencyFingerprint,
		Sources:    [2]string{"source"},
		Options:    api.HDRDecodedOptions{Audio: true, AllAudioTracks: true},
		Result: api.HDRDecodedResult{
			Mode:     api.HDRDecodedAudio,
			Profile:  api.AudioAnalysisProfileVersion,
			State:    api.HDRDecodedFailed,
			Coverage: api.HDRCoverageUnknown,
			Selected: 1,
			Reason:   "audio_analysis_failed",
		},
		AudioAuthority: &hdranalysis.HDRAudioAuthority{
			SourceFingerprint:   "source",
			ResourceID:          "resource",
			ManifestFingerprint: "manifest",
			TrackID:             "prepared-track",
			Ordinal:             1,
			Codec:               "pcm",
			Channels:            1,
			SampleRate:          48000,
		},
		Decoder: hdranalysis.DecoderIdentity{Fingerprint: strings.Repeat("1", 64)},
	}
	identity, err := hdranalysis.AudioMeasurementIdentity(report)
	if err != nil {
		t.Fatal(err)
	}
	report.Options = api.HDRDecodedOptions{
		Automatic:           true,
		Sampled:             true,
		VideoDecoderThreads: 16,
	}
	primaryIdentity, err := hdranalysis.AudioMeasurementIdentity(report)
	if err != nil || primaryIdentity != identity {
		t.Fatal("video threads, sampled/full, or audio membership changed PCM identity")
	}
	record, resource, err := builder.retainMeasurement(t.Context(), releaseworkflow.HDRExtractionRecord{
		Release:           resolver.subject.Release,
		ID:                "native",
		TargetID:          resolver.subject.Targets[0].Target.ID,
		SourceFingerprint: "source",
	}, identity, "prepared-track-checkpoint", report)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := resource.Release(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := restoreHDRMeasurement(t.Context(), record, resource); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*releaseworkflow.HDRMeasurementRecord){
		func(record *releaseworkflow.HDRMeasurementRecord) { record.AudioTrackID = "other" },
		func(record *releaseworkflow.HDRMeasurementRecord) {
			record.AudioScopeFingerprint = strings.Repeat("2", 64)
		},
		func(record *releaseworkflow.HDRMeasurementRecord) { record.Decoder = strings.Repeat("3", 64) },
	} {
		changed := record
		mutate(&changed)
		if _, err := restoreHDRMeasurement(t.Context(), changed, resource); err == nil {
			t.Fatal("mismatched audio checkpoint ownership restored")
		}
	}
	report.Decoder.Fingerprint = strings.Repeat("4", 64)
	changed, err := hdranalysis.AudioMeasurementIdentity(report)
	if err != nil || changed == identity {
		t.Fatal("decoder changes failed to invalidate audio measurement identity")
	}
}

func TestHDRAudioAggregatePreservesPCMWhenRenderingFails(t *testing.T) {
	var outcome api.HDRAnalysisTargetResult
	for _, ordinal := range []int{2, 1} {
		report := &hdranalysis.MeasurementReport{
			AudioAuthority: &hdranalysis.HDRAudioAuthority{
				TrackID:       map[int]string{1: "main", 2: "alternate"}[ordinal],
				Ordinal:       ordinal,
				Channels:      1,
				SampleRate:    48000,
				Codec:         "pcm",
				ChannelLayout: "mono",
			},
			Result: api.HDRDecodedResult{Measured: 1},
			Audio:  &hdranalysis.AutomaticAudio{Track: api.AudioAnalysisTrackResult{SampleFrames: 48000}},
		}
		artifacts := []api.PublicResourceID{"alt-wave", "alt-spectrogram", "alt-stats"}
		if ordinal == 1 {
			artifacts = nil
		}
		hdranalysis.AppendAudioOutcome(&outcome, report, api.HDRDecodedResult{State: api.HDRDecodedComplete, Artifacts: artifacts})
	}
	if len(outcome.Decoded) != 1 {
		t.Fatal("per-track publication produced multiple public audio outcomes")
	}
	result := outcome.Decoded[0]
	if result.Measured != 2 || result.Selected != 2 || result.Coverage != api.HDRCoveragePartial || result.AudioTracks[0].TrackID != "main" ||
		result.AudioTracks[0].State != api.HDRDecodedInconclusive || !result.AudioTracks[0].PCMMeasured || result.Validate() != nil {
		t.Fatalf("invalid partial audio aggregate: %+v: %v", result, result.Validate())
	}
}

func TestHDRPreparedAudioRejectsUnknownScopeAndExcessTracksWithoutSubset(t *testing.T) {
	for _, count := range []int{0, 64, 65} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			builder, resolver, _ := hdrFixture(t)
			target := resolver.subject.Targets[0]
			if count > 0 {
				target.Audio = &api.AudioAnalysisSubject{
					VideoPath:           target.Path,
					SourceFingerprint:   resolver.subject.SourceFingerprint,
					ResourceID:          "resource",
					ManifestFingerprint: "manifest",
				}
				for i := range count {
					target.Audio.Tracks = append(target.Audio.Tracks, api.MediaTrackFacts{
						ID:         "track-" + strconv.Itoa(i),
						Ordinal:    i + 1,
						Kind:       api.MediaTrackAudio,
						ResourceID: "resource",
					})
				}
			}
			var outcome api.HDRAnalysisTargetResult
			capture, err := builder.prepareHDRAudioCapture(t.Context(), nil, resolver.subject, target,
				api.HDRAnalysisInstructions{ProfileVersion: api.HDRAnalysisProfileV3, Decoded: &api.HDRDecodedOptions{Audio: true, AllAudioTracks: true}},
				&releaseworkflow.HDRExtractionRecord{}, nil, &outcome, hdrManagedResource{}, "selection", 0, "")
			if err != nil || capture == nil {
				t.Fatalf("prepare selection: %v", err)
			}
			if count == 64 {
				if len(capture.Selected) != 64 || len(outcome.Decoded) != 0 {
					t.Fatal("supported complete inventory was truncated")
				}
			} else if len(capture.Selected) != 0 || len(outcome.Decoded) != 1 || outcome.Decoded[0].State != api.HDRDecodedUnsupported {
				t.Fatal("unknown or excessive inventory permitted partial measurement")
			}
		})
	}
}

func TestHDRPreparedKnownAudioAbsenceKeepsNativeComplete(t *testing.T) {
 builder,resolver,_:=hdrFixture(t)
 target:=resolver.subject.Targets[0]
 target.Audio=&api.AudioAnalysisSubject{VideoPath:target.Path,SourceFingerprint:resolver.subject.SourceFingerprint}
 outcome:=api.HDRAnalysisTargetResult{Formats:[]api.HDRFormatResult{{Format:api.HDRNativeDolbyVision,State:api.HDRExtractionComplete}}}
 capture,err:=builder.prepareHDRAudioCapture(t.Context(),nil,resolver.subject,target,
  api.HDRAnalysisInstructions{ProfileVersion:api.HDRAnalysisProfileV3,Decoded:&api.HDRDecodedOptions{Audio:true}},
  &releaseworkflow.HDRExtractionRecord{},nil,&outcome,hdrManagedResource{},"silent",0,"")
 if err!=nil || capture==nil || len(capture.Selected)!=0 || len(outcome.Decoded)!=1 {t.Fatalf("absent audio: %+v %v",capture,err)}
 if outcome.Decoded[0].State!=api.HDRDecodedNotRun || outcome.Decoded[0].Reason!="no_audio" || outcome.AnalysisStatus()!=api.StageStatusCompleted {t.Fatalf("known absence downgraded native completion: %+v",outcome)}
}
