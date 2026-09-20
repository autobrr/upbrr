// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package audioanalysis streams selected FFmpeg audio decodes into deterministic
// waveform and spectrogram PNGs without retaining complete PCM in memory.
package audioanalysis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

const maxConcurrentAnalyses = 2

var analysisSlots = make(chan struct{}, maxConcurrentAnalyses)

var errDecodedSourceChanged = errors.New("decoded audio changed between passes")

var errOutputStorageUnavailable = errors.New("audio analysis output storage is unavailable")

// Artifact pairs public artifact metadata with its private managed local path.
type Artifact struct {
	Public api.AudioAnalysisArtifact
	Path   string
}

// TrackResult is one decoded track and its retained private artifacts.
type TrackResult struct {
	Public    api.AudioAnalysisTrackResult
	Artifacts []Artifact
}

// Service owns FFmpeg binding, bounded two-pass PCM analysis, and managed PNG publication.
type Service struct {
	logger     api.Logger
	decoder    decoder
	publishPNG func(string, string, image.Image) error
}

// NewService constructs an analysis service. A nil logger uses [api.NopLogger].
// FFmpeg resolution is deferred until requested validation or analysis so
// disabled analysis adds no startup prerequisite.
func NewService(logger api.Logger) *Service { return newService(logger, nil) }

func newService(logger api.Logger, decoder decoder) *Service {
	if logger == nil {
		logger = api.NopLogger{}
	}
	return &Service{
		logger:     logger,
		decoder:    decoder,
		publishPNG: writePNGAtomic,
	}
}

// ValidateSelection proves the complete selected track set against the current
// decoder inventory without decoding PCM or publishing artifacts.
func (s *Service) ValidateSelection(
	ctx context.Context,
	subject api.AudioAnalysisSubject,
	instructions api.AudioAnalysisInstructions,
) error {
	normalized, err := instructions.Normalize()
	if err != nil {
		return fmt.Errorf("audio analysis: normalize instructions: %w", err)
	}
	selectedDecoder, err := s.analysisDecoder()
	if err != nil {
		return err
	}
	if err := acquireAnalysisSlot(ctx); err != nil {
		return err
	}
	defer releaseAnalysisSlot()
	_, err = s.inspectBindings(ctx, selectedDecoder, subject, normalized.TrackIDs)
	return err
}

// Analyze streams one selected track into the requested image variants. The
// workflow owner supplies the validated managed directory for attemptID and
// owns track scheduling, retention, and aggregate status.
func (s *Service) Analyze(
	ctx context.Context,
	subject api.AudioAnalysisSubject,
	instructions api.AudioAnalysisInstructions,
	attemptID string,
	attemptRoot string,
) (TrackResult, error) {
	normalized, err := instructions.Normalize()
	if err != nil {
		s.logger.Warnf("audioanalysis: state=blocked reason=invalid_instructions")
		return TrackResult{}, fmt.Errorf("audio analysis: normalize instructions: %w", err)
	}
	if len(normalized.TrackIDs) != 1 {
		s.logger.Warnf("audioanalysis: state=blocked reason=invalid_track_count")
		return TrackResult{}, audioAnalysisError(
			api.AudioAnalysisFailureInvalidSelection,
			"audio analysis requires exactly one scheduled track",
			errors.New("audio analysis: service requires exactly one track"),
		)
	}
	if strings.TrimSpace(attemptRoot) == "" {
		s.logger.Warnf("audioanalysis: state=blocked reason=storage_unavailable")
		return TrackResult{}, audioAnalysisError(
			api.AudioAnalysisFailureResourceUnavailable,
			"audio analysis storage is unavailable",
			errors.New("audio analysis: service is unavailable"),
		)
	}
	selectedDecoder, err := s.analysisDecoder()
	if err != nil {
		return TrackResult{}, err
	}
	if err := acquireAnalysisSlot(ctx); err != nil {
		s.logger.Debugf("audioanalysis: state=stopped phase=admission")
		return TrackResult{}, err
	}
	defer releaseAnalysisSlot()
	bindings, err := s.inspectBindings(ctx, selectedDecoder, subject, normalized.TrackIDs)
	if err != nil {
		return TrackResult{}, err
	}
	if err := os.MkdirAll(attemptRoot, 0o700); err != nil {
		s.logger.Warnf("audioanalysis: state=blocked reason=storage_unavailable")
		return TrackResult{}, audioAnalysisError(
			api.AudioAnalysisFailureResourceUnavailable,
			"the managed audio-analysis output root could not be created",
			fmt.Errorf("audio analysis: create output root: %w", err),
		)
	}
	s.logger.Infof("audioanalysis: state=started track_count=1 variant_count=%d", len(normalized.Variants))
	if err := ctx.Err(); err != nil {
		return TrackResult{}, classifyTopLevelFailure(fmt.Errorf("audio analysis: track scheduling stopped: %w", err))
	}
	track, trackErr := s.analyzeTrack(ctx, selectedDecoder, attemptRoot, subject.VideoPath, bindings[0], normalized.Variants, attemptID)
	if trackErr != nil {
		return track, trackErr
	}
	if err := ctx.Err(); err != nil {
		return track, classifyTopLevelFailure(fmt.Errorf("audio analysis: track stopped: %w", err))
	}
	s.logger.Infof("audioanalysis: state=%s track_count=1 artifact_count=%d", track.Public.Status, len(track.Artifacts))
	return track, nil
}

func (s *Service) analysisDecoder() (decoder, error) {
	if s.decoder != nil {
		return s.decoder, nil
	}
	selectedDecoder, err := newFFmpegDecoder()
	if err != nil {
		s.logger.Warnf("audioanalysis: state=blocked reason=ffmpeg_unavailable")
		return nil, err
	}
	return selectedDecoder, nil
}

func acquireAnalysisSlot(ctx context.Context) error {
	select {
	case analysisSlots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return classifyTopLevelFailure(fmt.Errorf("audio analysis: admission stopped: %w", ctx.Err()))
	}
}

func releaseAnalysisSlot() { <-analysisSlots }

func (s *Service) inspectBindings(
	ctx context.Context,
	selectedDecoder decoder,
	subject api.AudioAnalysisSubject,
	selectedTrackIDs []string,
) ([]trackBinding, error) {
	streams, err := selectedDecoder.Inspect(ctx, subject.VideoPath)
	if err != nil {
		s.logger.Warnf("audioanalysis: state=blocked reason=source_inspection_failed")
		return nil, classifyTopLevelFailure(err)
	}
	bindings, err := bindTracks(subject, selectedTrackIDs, streams)
	if err != nil {
		s.logger.Warnf("audioanalysis: state=blocked reason=ambiguous_binding")
		return nil, err
	}
	return bindings, nil
}

type trackBinding struct {
	track        api.MediaTrackFacts
	audioOrdinal int
}

func bindTracks(subject api.AudioAnalysisSubject, selected []string, streams []inspectedStream) ([]trackBinding, error) {
	audio := make([]api.MediaTrackFacts, 0, len(subject.Tracks))
	for _, track := range subject.Tracks {
		if track.Kind == api.MediaTrackAudio {
			audio = append(audio, track)
		}
	}
	if len(audio) != len(streams) {
		return nil, audioAnalysisError(
			api.AudioAnalysisFailureAmbiguousBinding,
			"prepared audio tracks no longer match the source",
			errors.New("audio analysis: prepared track inventory no longer matches the source"),
		)
	}
	byID := make(map[string]trackBinding, len(audio))
	remainingStreams := streams
	for index, track := range audio {
		stream := remainingStreams[0]
		remainingStreams = remainingStreams[1:]
		if track.Ordinal != index+1 || (track.SampleRate > 0 && stream.sampleRate != track.SampleRate) ||
			(track.Channels > 0 && stream.channels > 0 && stream.channels != track.Channels) ||
			!audioCodecsCompatible(track.Codec, stream.codec) || !audioLayoutsCompatible(track.ChannelLayout, stream.layout) {
			return nil, audioAnalysisError(
				api.AudioAnalysisFailureAmbiguousBinding,
				"prepared audio facts no longer match the decoder stream inventory",
				errors.New("audio analysis: prepared audio binding is ambiguous"),
			)
		}
		if track.Channels == 0 {
			track.Channels = stream.channels
		}
		if track.SampleRate == 0 {
			track.SampleRate = stream.sampleRate
		}
		if strings.TrimSpace(track.Codec) == "" {
			track.Codec = stream.codec
		}
		if strings.TrimSpace(track.ChannelLayout) == "" {
			track.ChannelLayout = stream.layout
		}
		byID[track.ID] = trackBinding{track: track, audioOrdinal: index}
	}
	bindings := make([]trackBinding, 0, len(selected))
	for _, trackID := range selected {
		binding, ok := byID[trackID]
		if !ok {
			return nil, audioAnalysisError(
				api.AudioAnalysisFailureInvalidSelection,
				"the selected audio track is not present in the prepared inventory",
				errors.New("audio analysis: selected track is not present in the prepared inventory"),
			)
		}
		if binding.track.Channels <= 0 || binding.track.Channels > 8 {
			return nil, audioAnalysisError(
				api.AudioAnalysisFailureUnsupportedLayout,
				"the selected audio track has an unsupported channel layout",
				fmt.Errorf("audio analysis: track %d has an unsupported channel layout", binding.track.Ordinal),
			)
		}
		bindings = append(bindings, binding)
	}
	return bindings, nil
}

func audioCodecsCompatible(prepared string, inspected string) bool {
	if strings.TrimSpace(prepared) == "" {
		return true
	}
	if strings.TrimSpace(inspected) == "" {
		return false
	}
	preparedFamily := audioCodecFamily(prepared)
	inspectedFamily := audioCodecFamily(inspected)
	return preparedFamily != "" && inspectedFamily != "" && preparedFamily == inspectedFamily
}

func audioCodecFamily(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch {
	case normalized == "dd+", strings.Contains(normalized, "e-ac-3"), strings.Contains(normalized, "eac3"),
		strings.Contains(normalized, "dolby digital plus"):
		return "eac3"
	case strings.Contains(normalized, "truehd"), strings.Contains(normalized, "mlp fba"):
		return "truehd"
	case normalized == "dd", strings.Contains(normalized, "ac-3"), strings.Contains(normalized, "ac3"),
		strings.Contains(normalized, "dolby digital"):
		return "ac3"
	case strings.Contains(normalized, "dts"):
		return "dts"
	case strings.Contains(normalized, "adpcm"):
		return "adpcm"
	case strings.Contains(normalized, "pcm"):
		return "pcm"
	case strings.Contains(normalized, "flac"):
		return "flac"
	case strings.Contains(normalized, "aac"):
		return "aac"
	case strings.Contains(normalized, "alac"):
		return "alac"
	case strings.Contains(normalized, "opus"):
		return "opus"
	case strings.Contains(normalized, "vorbis"):
		return "vorbis"
	case strings.Contains(normalized, "mp2"):
		return "mp2"
	case strings.Contains(normalized, "mp3"):
		return "mp3"
	case strings.Contains(normalized, "wavpack"):
		return "wavpack"
	default:
		return ""
	}
}

func audioLayoutsCompatible(prepared string, inspected string) bool {
	preparedPosition := surroundPosition(prepared)
	inspectedPosition := surroundPosition(inspected)
	return preparedPosition == "" || inspectedPosition == "" || preparedPosition == inspectedPosition
}

func surroundPosition(layout string) string {
	normalized := strings.ToLower(strings.TrimSpace(layout))
	fields := strings.FieldsFunc(normalized, func(character rune) bool {
		return character == ' ' || character == ',' || character == '(' || character == ')' || character == '/'
	})
	contains := func(value string) bool { return slices.Contains(fields, value) }
	switch {
	case strings.Contains(normalized, "side"), contains("ls") && contains("rs"), contains("sl") && contains("sr"):
		return "side"
	case strings.Contains(normalized, "back"), contains("lb") && contains("rb"), contains("bl") && contains("br"):
		return "back"
	default:
		return ""
	}
}

func (s *Service) analyzeTrack(
	ctx context.Context,
	selectedDecoder decoder,
	attemptRoot string,
	sourcePath string,
	binding trackBinding,
	variants []api.AudioAnalysisVariant,
	attemptID string,
) (TrackResult, error) {
	language := ""
	if len(binding.track.Languages) > 0 {
		language = binding.track.Languages[0]
	}
	public := api.AudioAnalysisTrackResult{
		TrackID:       binding.track.ID,
		Ordinal:       binding.track.Ordinal,
		Title:         binding.track.Title,
		Codec:         binding.track.Codec,
		Language:      language,
		ChannelLayout: binding.track.ChannelLayout,
		Channels:      binding.track.Channels,
		SampleRate:    binding.track.SampleRate,
	}
	request := decodeRequest{
		path:         sourcePath,
		audioOrdinal: binding.audioOrdinal,
		codec:        binding.track.Codec,
	}
	frames, countedDigest, err := countFrames(ctx, selectedDecoder, request, binding.track.Channels)
	if err != nil {
		failure := failureForError(err)
		public.Status = api.StageStatusFailed
		public.Failure = &failure
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return TrackResult{Public: public}, audioAnalysisError(failure.Code, failure.Message, err)
		}
		return TrackResult{Public: public}, nil
	}
	if frames == 0 {
		failure := api.AudioAnalysisFailure{Code: api.AudioAnalysisFailureDecode, Message: "decoded audio was empty"}
		public.Status = api.StageStatusFailed
		public.Failure = &failure
		return TrackResult{Public: public}, nil
	}
	public.SampleFrames = frames
	public.Duration = float64(frames) / float64(binding.track.SampleRate)

	waveformRequested := slices.Contains(variants, api.AudioAnalysisWaveform)
	spectrogramRequested := slices.Contains(variants, api.AudioAnalysisSpectrogram)
	var waveform *waveformAnalysis
	if waveformRequested {
		waveform = newWaveformAnalysis(binding.track.Channels)
	}
	var spectrogram *spectrogramAnalysis
	if spectrogramRequested {
		spectrogram = newSpectrogramAnalysis(binding.track.Channels, frames)
	}
	var renderedFrames int64
	renderedHasher := sha256.New()
	err = selectedDecoder.Decode(ctx, request, func(reader io.Reader) error {
		var decodeErr error
		renderedFrames, decodeErr = consumePCM(io.TeeReader(reader, renderedHasher), binding.track.Channels, func(frame int64, samples []float32) error {
			if frame >= frames {
				return errDecodedSourceChanged
			}
			if waveform != nil {
				waveform.add(frame, frames, samples)
			}
			if spectrogram != nil {
				spectrogram.add(frame, samples)
			}
			return nil
		})
		return decodeErr
	})
	if err == nil && renderedFrames != frames {
		err = errDecodedSourceChanged
	}
	var renderedDigest [sha256.Size]byte
	copy(renderedDigest[:], renderedHasher.Sum(nil))
	if err == nil && renderedDigest != countedDigest {
		err = errDecodedSourceChanged
	}
	if err == nil && spectrogram != nil {
		spectrogram.finish()
	}
	if err != nil {
		failure := failureForError(err)
		public.Status = api.StageStatusFailed
		public.Failure = &failure
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
			failure.Code == api.AudioAnalysisFailureStaleSource {
			return TrackResult{Public: public}, audioAnalysisError(failure.Code, failure.Message, err)
		}
		return TrackResult{Public: public}, nil
	}
	if waveform != nil {
		waveform.finish()
	}

	trackDirectory := filepath.Join(attemptRoot, opaquePathPart(binding.track.ID))
	if !pathutil.IsWithinRoot(attemptRoot, trackDirectory) {
		failure := api.AudioAnalysisFailure{Code: api.AudioAnalysisFailureOutput, Message: "managed output path was rejected"}
		public.Status = api.StageStatusFailed
		public.Failure = &failure
		return TrackResult{Public: public}, audioAnalysisError(
			api.AudioAnalysisFailureResourceUnavailable,
			"the managed audio-analysis output path was rejected",
			errors.New("audio analysis: managed track path escaped the attempt root"),
		)
	}
	if err := os.MkdirAll(trackDirectory, 0o700); err != nil {
		failure := api.AudioAnalysisFailure{Code: api.AudioAnalysisFailureOutput, Message: "could not create managed output directory"}
		public.Status = api.StageStatusFailed
		public.Failure = &failure
		return TrackResult{Public: public}, audioAnalysisError(
			api.AudioAnalysisFailureResourceUnavailable,
			"the managed audio-analysis output directory could not be created",
			fmt.Errorf("audio analysis: create track output directory: %w", err),
		)
	}
	result := TrackResult{Public: public}
	publishPNG := s.publishPNG
	if publishPNG == nil {
		publishPNG = writePNGAtomic
	}
	for _, variant := range variants {
		var rendered image.Image
		switch variant {
		case api.AudioAnalysisWaveform:
			rendered = renderWaveform(waveform, binding.track.SampleRate, frames, binding.track.ChannelLayout)
		case api.AudioAnalysisSpectrogram:
			rendered = renderSpectrogram(spectrogram, binding.track.SampleRate, binding.track.ChannelLayout)
		}
		path := filepath.Join(trackDirectory, string(variant)+".png")
		artifact := api.AudioAnalysisArtifact{ID: artifactID(attemptID, binding.track.ID, variant), Variant: variant}
		if err := publishPNG(trackDirectory, path, rendered); err != nil {
			failure := api.AudioAnalysisFailure{Code: api.AudioAnalysisFailureOutput, Message: "could not publish analysis image"}
			artifact.Status = api.StageStatusFailed
			artifact.Failure = &failure
			result.Public.Artifacts = append(result.Public.Artifacts, artifact)
			if isServiceWideOutputError(err) {
				result.Public.Status = api.StageStatusFailed
				return result, audioAnalysisError(
					api.AudioAnalysisFailureResourceUnavailable,
					"audio-analysis storage failed while publishing an image",
					err,
				)
			}
			continue
		}
		artifact.Status = api.StageStatusCompleted
		artifact.Width = rendered.Bounds().Dx()
		artifact.Height = rendered.Bounds().Dy()
		result.Artifacts = append(result.Artifacts, Artifact{Public: artifact, Path: path})
		result.Public.Artifacts = append(result.Public.Artifacts, artifact)
	}
	completed := 0
	for _, artifact := range result.Public.Artifacts {
		if artifact.Status == api.StageStatusCompleted {
			completed++
		}
	}
	switch {
	case completed == len(result.Public.Artifacts):
		result.Public.Status = api.StageStatusCompleted
	case completed > 0:
		result.Public.Status = api.StageStatusPartial
	default:
		result.Public.Status = api.StageStatusFailed
	}
	return result, nil
}

func isServiceWideOutputError(err error) bool {
	return errors.Is(err, errOutputStorageUnavailable) || errors.Is(err, fs.ErrPermission) ||
		errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EROFS) || errors.Is(err, syscall.Errno(112))
}

func countFrames(
	ctx context.Context,
	selectedDecoder decoder,
	request decodeRequest,
	channels int,
) (int64, [sha256.Size]byte, error) {
	var frames int64
	hasher := sha256.New()
	err := selectedDecoder.Decode(ctx, request, func(reader io.Reader) error {
		var consumeErr error
		frames, consumeErr = consumePCM(io.TeeReader(reader, hasher), channels, nil)
		return consumeErr
	})
	var digest [sha256.Size]byte
	copy(digest[:], hasher.Sum(nil))
	if err != nil {
		return frames, digest, fmt.Errorf("audio analysis: count decoded frames: %w", err)
	}
	return frames, digest, nil
}

func opaquePathPart(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "track_" + hex.EncodeToString(sum[:12])
}

func artifactID(attemptID string, trackID string, variant api.AudioAnalysisVariant) api.PublicResourceID {
	sum := sha256.Sum256([]byte(attemptID + "\x00" + trackID + "\x00" + string(variant)))
	return api.PublicResourceID("audio_" + hex.EncodeToString(sum[:12]))
}

func writePNGAtomic(root string, path string, rendered image.Image) error {
	if rendered == nil || !pathutil.IsWithinRoot(root, path) {
		return outputStorageError("validate output path", errors.New("invalid output path"))
	}
	temporary, err := os.CreateTemp(root, ".audio-analysis-*.png.tmp")
	if err != nil {
		return outputStorageError("create temporary PNG", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return outputStorageError("restrict temporary PNG", err)
	}
	if err := png.Encode(temporary, rendered); err != nil {
		_ = temporary.Close()
		if isServiceWideOutputError(err) {
			return outputStorageError("encode PNG", err)
		}
		return fmt.Errorf("audio analysis: encode PNG: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return outputStorageError("sync PNG", err)
	}
	if err := temporary.Close(); err != nil {
		return outputStorageError("close PNG", err)
	}
	file, err := os.Open(temporaryPath)
	if err != nil {
		return outputStorageError("reopen PNG", err)
	}
	configuration, _, decodeErr := image.DecodeConfig(file)
	closeErr := file.Close()
	if closeErr != nil {
		return outputStorageError("close validated PNG", closeErr)
	}
	if decodeErr != nil || configuration.Width != rendered.Bounds().Dx() || configuration.Height != rendered.Bounds().Dy() {
		return errors.New("audio analysis: output PNG validation failed")
	}
	if _, err := os.Stat(path); err == nil {
		return outputStorageError("inspect output destination", errors.New("output already exists"))
	} else if !errors.Is(err, os.ErrNotExist) {
		return outputStorageError("inspect output destination", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return outputStorageError("publish PNG", err)
	}
	return nil
}

func outputStorageError(operation string, err error) error {
	return fmt.Errorf("audio analysis: %s: %w: %w", operation, errOutputStorageUnavailable, err)
}

func failureForError(err error) api.AudioAnalysisFailure {
	if failure, ok := api.AsAudioAnalysisFailure(err); ok {
		return failure
	}
	code := api.AudioAnalysisFailureDecode
	message := "audio decode failed"
	switch {
	case errors.Is(err, context.Canceled):
		code, message = api.AudioAnalysisFailureCanceled, "audio analysis was canceled"
	case errors.Is(err, context.DeadlineExceeded):
		code, message = api.AudioAnalysisFailureInterrupted, "audio analysis timed out"
	case errors.Is(err, errMalformedPCM):
		code, message = api.AudioAnalysisFailureMalformedPCM, "decoder produced malformed PCM"
	case errors.Is(err, errDecodedSourceChanged):
		code, message = api.AudioAnalysisFailureStaleSource, "decoded audio changed between analysis passes"
	}
	return api.AudioAnalysisFailure{Code: code, Message: message}
}

func classifyTopLevelFailure(err error) error {
	if _, ok := api.AsAudioAnalysisFailure(err); ok {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return audioAnalysisError(api.AudioAnalysisFailureCanceled, "audio analysis was canceled", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return audioAnalysisError(api.AudioAnalysisFailureInterrupted, "audio analysis timed out", err)
	}
	return audioAnalysisError(
		api.AudioAnalysisFailureUnsupportedSource,
		"the source audio stream inventory could not be inspected",
		err,
	)
}

func audioAnalysisError(code api.AudioAnalysisFailureCode, message string, cause error) error {
	return fmt.Errorf(
		"audio analysis: %w",
		api.NewAudioAnalysisError(api.AudioAnalysisFailure{Code: code, Message: message}, cause),
	)
}
