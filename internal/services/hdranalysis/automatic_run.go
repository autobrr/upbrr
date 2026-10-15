// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/Audionut/go-hdr10-plus/extract"
	gomediainfo "github.com/autobrr/go-mediainfo"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/services/audioanalysis"
	"github.com/autobrr/upbrr/internal/services/ffmpegstream"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

const automaticReservedBytes = 1536 << 20

// InspectFileSource obtains bounded source facts before a complete shared pass.
// It probes headers only; prepared workflows normally already have these facts.
func InspectFileSource(ctx context.Context, path string) (api.HDRSourceEvidence, error) {
	probe, err := gomediainfo.AnalyzeFileContext(ctx, path)
	if err != nil {
		return api.HDRSourceEvidence{}, Classify(err)
	}
	payload, err := gomediainfo.Render([]gomediainfo.Report{probe}, gomediainfo.OutputJSON)
	if err != nil {
		return api.HDRSourceEvidence{}, Classify(err)
	}
	var document mediafacts.MediaInfoDocument
	if err := json.Unmarshal([]byte(payload), &document); err != nil {
		return api.HDRSourceEvidence{}, Classify(err)
	}
	facts := mediafacts.HDRSourceFromMediaInfo(document)
	if !facts.UniqueHEVC {
		// Some short elementary streams have no MediaInfo video section. The
		// existing native producer identifies the physical format before reading
		// coded bodies; its collector still owns compressed-syntax validation.
		file, err := os.Open(path)
		if err != nil {
			return api.HDRSourceEvidence{}, fmt.Errorf("open HDR source header: %w", err)
		}
		stop := errors.New("HDR source header identified")
		var format extract.Format
		probeErr := extract.Walk(ctx, file, extract.Options{Limits: extract.Limits{
			MaxRetainedBytes:       16 << 20,
			MaxContainerIndexBytes: 16 << 20,
			MaxBlockBytes:          16 << 20,
		}}, func(source extract.SourceInfo) (extract.Consumer, error) {
			format = source.Format
			return nil, stop
		})
		if err := file.Close(); err != nil {
			return api.HDRSourceEvidence{}, fmt.Errorf("close HDR source header: %w", err)
		}
		if errors.Is(probeErr, stop) && format == extract.RawHEVC {
			facts.UniqueHEVC = true
			facts.Decoded = &api.HDRDecodedSourceEvidence{
				RawHEVC:      true,
				Codec:        "hevc",
				VideoStreams: 1,
			}
		}
	}
	return facts, nil
}

// AnalyzeAutomatic reads each authorized input once in its own FFmpeg process.
// Complete metadata, selected decoded pictures and existing primary audio analysis
// share that traversal. Header/capability probes do not traverse complete media.
// The caller owns returned native metadata and checkpoints it before reports.
// New v3 options require AnalyzePreparedAutomatic and prepared audio authority.
func (s *Session) AnalyzeAutomatic(ctx context.Context, primary DecodedInput, reference *DecodedInput,
	options api.HDRDecodedOptions, formats api.HDRAnalysisFormats, directory string, otherBytes int64,
) (*NativeExtraction, []*MeasurementReport, error) {
	if options.RequiresV3() {
		return nil, nil, errors.New("v3 automatic analysis requires prepared audio authority")
	}
	return s.analyzeAutomatic(ctx, primary, reference, options, formats, directory, otherBytes, nil)
}

func (s *Session) analyzeAutomatic(ctx context.Context, primary DecodedInput, reference *DecodedInput,
	options api.HDRDecodedOptions, formats api.HDRAnalysisFormats, directory string, otherBytes int64, prepared *preparedAudioCapture,
) (*NativeExtraction, []*MeasurementReport, error) {
	if !options.Automatic && !options.Audio || primary.Path == "" || primary.Fingerprint == "" || directory == "" || otherBytes < 0 {
		return nil, nil, errors.New("automatic HDR analysis lacks source authority")
	}
	if err := options.Validate(); err != nil {
		return nil, nil, fmt.Errorf("validate shared HDR analysis: %w", err)
	}
	// Complete UHD measurement can outlast a fixed wall-clock deadline. The
	// caller owns cancellation; frame, evidence and memory limits still apply.
	if primary.Source.Codec == "" {
		facts, inspectErr := InspectFileSource(ctx, primary.Path)
		if inspectErr != nil {
			return s.automaticMetadataOnly(ctx, primary, options, formats, otherBytes, inspectErr)
		}
		if facts.Decoded != nil {
			primary.Source = *facts.Decoded
		}
	}
	if primary.Source.RawHEVC {
		return s.automaticMetadataOnly(ctx, primary, options, formats, otherBytes, errDecodedMapping)
	}
	if primary.Source.Codec != "hevc" || primary.Source.VideoStreams < 1 {
		return s.automaticMetadataOnly(ctx, primary, options, formats, otherBytes, errDecodedMapping)
	}
	executable, err := screenshots.ResolveFFmpegExecutable()
	if err != nil {
		return s.automaticMetadataOnly(ctx, primary, options, formats, otherBytes, err)
	}
	decoder := pictureDecoder{executable: executable}
	var identity DecoderIdentity
	var capabilityErr error
	if options.Automatic {
		identity, capabilityErr = decoder.capabilities(ctx, true)
	}
	if identity.Fingerprint == "" {
		version, err := decoder.probe(ctx, nil, "-hide_banner", "-version")
		if err != nil {
			return s.automaticMetadataOnly(ctx, primary, options, formats, otherBytes, err)
		}
		first, _, _ := strings.Cut(string(version), "\n")
		identity.Version = first[:min(len(first), 200)]
		digest := sha256.Sum256(version)
		identity.Fingerprint = hex.EncodeToString(digest[:])
	}
	inputs := []DecodedInput{primary}
	var referenceNote *api.HDRCheckResult
	if reference != nil {
		primaryFile, primaryErr := os.Stat(primary.Path)
		referenceFile, referenceErr := os.Stat(reference.Path)
		cropFits := options.Comparison != nil
		if cropFits {
			crop := options.Comparison.Crop
			for _, source := range []api.HDRDecodedSourceEvidence{primary.Source, reference.Source} {
				cropFits = cropFits && crop.Left <= source.Width-crop.Width && crop.Top <= source.Height-crop.Height
			}
		}
		if referenceErr != nil || primaryErr != nil || os.SameFile(primaryFile, referenceFile) ||
			!cropFits || reference.Source.RawHEVC || strings.Split(reference.Source.DolbyVisionProfile, ".")[0] == "5" {
			referenceNote = &api.HDRCheckResult{
				Code:    "reference_unavailable",
				State:   api.HDRCheckUnsupported,
				Message: "The reference requires an available, distinct HDR10-compatible picture and a common crop inside both rasters. Supported primary-source checks were run.",
			}
			reference = nil
		}
	}
	if reference != nil {
		inputs = append(inputs, *reference)
	}
	native := make([]*NativeExtraction, len(inputs))
	pictures := make([]*automaticPictures, len(inputs))
	previews := make([]*automaticPictures, len(inputs))
	proofs := make([]*automaticProof, len(inputs))
	profile5 := make([]bool, len(inputs))
	pictureErrors := make([]error, len(inputs))
	var crop *api.HDRCrop
	step := 1
	if options.Sampled {
		step = automaticSampleStep(primary.Source)
	}
	if options.Comparison != nil && reference != nil {
		crop = &options.Comparison.Crop
	}
	for i, input := range inputs {
		proofs[i] = &automaticProof{source: input.Source, name: fmt.Sprintf("[showinfo@hdr%d @ ", i)}
		if !options.Automatic {
			pictureErrors[i] = errDecodedMapping
			continue
		}
		profile5[i] = strings.Split(input.Source.DolbyVisionProfile, ".")[0] == "5"
		pictureErrors[i] = capabilityErr
		if pictureErrors[i] == nil && !profile5[i] && (input.Source.Range != "limited" && input.Source.Range != "full" ||
			input.Source.Transfer != "smpte2084" || input.Source.Matrix != "bt2020nc" || input.Source.Primaries != "bt2020") {
			pictureErrors[i] = errDecodedMapping
		}
		if pictureErrors[i] == nil {
			pictures[i], pictureErrors[i] = newAutomaticPictures(input.Source, crop)
			if pictureErrors[i] == nil && options.Sampled {
				pictures[i].selectedOnly = true
				pictures[i].chromaStep, pictures[i].chromaBucket = step*30, step*30
				previewSource := input.Source
				previewSource.Width, previewSource.Height = sceneWidth, sceneHeight
				previews[i], pictureErrors[i] = newAutomaticPictures(previewSource, nil)
				pictures[i].preview = previews[i]
			}
			if i == 1 && pictures[i] != nil && options.Comparison != nil && options.Comparison.FrameOffset != nil {
				pictures[i].chromaOffset = *options.Comparison.FrameOffset
				if pictures[0] != nil {
					pictures[i].chromaStep = pictures[0].chromaStep
					pictures[i].chromaBucket = pictures[0].chromaBucket
				}
			}
		}
		if pictureErrors[i] == nil && profile5[i] {
			pictureErrors[i] = probeDVReshape(ctx, decoder)
		}
	}
	defer func() {
		if len(native) > 1 {
			native[1].Release()
		}
	}()
	audioRoot, err := os.MkdirTemp(directory, ".hdr-audio-")
	if err != nil {
		return nil, nil, fmt.Errorf("create automatic audio scratch: %w", err)
	}
	defer func() { _ = os.RemoveAll(audioRoot) }()
	audioService := audioanalysis.NewService(s.service.logger)
	var audio *audioanalysis.Batch
	var audioErr error
	if prepared != nil {
		audio, audioErr = prepared.prepare(ctx, audioService, audioRoot)
	} else {
		audio, audioErr = audioService.PrepareBatch(ctx, primary.Path, audioRoot, "automatic")
	}
	if audioErr == nil {
		defer audio.Close()
	}
	// Each parser shares the operation budget with all picture/audio scratch
	// and, for a reference, the other parser pair. No source gets a second quota.
	reservedBytes := int64(automaticReservedBytes)
	if !options.Automatic {
		// No RGB decoder or picture measurements are active. 512 MiB covers the
		// primary audio batch, corrective decode/render overlap and FFmpeg scratch.
		reservedBytes = 512 << 20
	}
	if prepared != nil {
		// The prepared batch includes up to four tracks and corrective/render
		// overlap. Preserve the existing picture reservation independently.
		reservedBytes += audioanalysis.SharedBatchMemoryBytes - 512<<20
	}
	parserOther := otherBytes + reservedBytes
	if len(inputs) == 2 {
		parserOther += (int64(MaxRestoreBytes) - parserOther) / 2
	}
	var completion ffmpegstream.Result
	audioSlot := 0
	if options.RequiresV3() {
		identity.RequestedThreads = options.VideoDecoderThreads
	}
	for i := range inputs {
		if i == 1 && pictureErrors[i] != nil {
			continue
		}
		// A secondary input cannot make FFmpeg terminate the primary traversal.
		// Each eligible input gets exactly one complete demux/decode pass.
		threadCount := min(8, max(1, runtime.GOMAXPROCS(0)))
		if pictures[i] != nil {
			// Limit in-flight decoder pictures conservatively using RGB-sized
			// buffers, within the existing shared capture scratch allowance.
			threadCount = min(threadCount, max(1, (512<<20)/(pictures[i].width*pictures[i].height*6)))
		}
		threadCount = requestedVideoThreads(options.VideoDecoderThreads, threadCount, inputs[i:i+1])
		if options.RequiresV3() && pictureErrors[i] == nil {
			identity.EffectiveThreads = append(identity.EffectiveThreads, threadCount)
		}
		threads := strconv.Itoa(threadCount)
		// zscale's output can vary with filter threading. Preserve the calibrated
		// colour transform; decode and Go pixel measurement have bounded parallelism.
		args := []string{
			"-hide_banner",
			"-nostdin",
			"-nostats",
			"-loglevel",
			"info",
			"-y",
			"-max_alloc",
			"268435456",
			"-copyts",
			"-filter_threads",
			"1",
			"-filter_complex_threads",
			"1",
			"-threads:v",
			threads,
			"-hwaccel",
			"none",
			"-noautorotate",
			"-apply_cropping",
			"0",
		}
		if i == 0 && audio != nil {
			args = append(args, "-threads:a", strconv.Itoa(api.AudioAnalysisDefaultDecoderThreads))
			args = append(args, audio.InputArguments()...)
		}
		args = append(args, "-i", inputs[i].Path)
		if pictureErrors[i] == nil {
			filter := fmt.Sprintf("[0:V:%d]showinfo@hdr%d=checksum=0,%s[picture%d]",
				inputs[i].Source.VideoStreamOrdinal, i, automaticColourFilter(inputs[i].Source, profile5[i]), i)
			if options.Sampled {
				offset := 0
				if i == 1 {
					offset = *options.Comparison.FrameOffset
				}
				filter = fmt.Sprintf("[0:V:%d]showinfo@hdr%d=checksum=0,split=2[preview-in%d][picture-in%d];"+
					"[preview-in%d]%s[preview%d];[picture-in%d]select='eq(mod(n-%d,%d),0)',%s[picture%d]",
					inputs[i].Source.VideoStreamOrdinal, i, i, i, i, automaticPreviewFilter(inputs[i].Source, profile5[i]), i,
					i, offset, step, automaticColourFilter(inputs[i].Source, profile5[i]), i)
			}
			args = append(args, "-filter_complex", filter)
		}
		var outputs []ffmpegstream.Output
		outputs = append(outputs, ffmpegstream.Output{
			Arguments: []string{
				"-map",
				fmt.Sprintf("0:V:%d", inputs[i].Source.VideoStreamOrdinal),
				"-an",
				"-sn",
				"-dn",
				"-c:v",
				"copy",
				"-f",
				"matroska",
				"-live",
				"1",
				"-avoid_negative_ts",
				"disabled",
			},
			Consume: func(reader io.Reader) error {
				var err error
				var progress func(uint64)
				if !options.Automatic {
					progress = func(bytes uint64) {
						s.service.reportProgress(ctx, Progress{
							Phase: "reading_shared_media",
							Message: fmt.Sprintf(
								"Read %.1f MiB of compressed video; analyzing selected audio without picture decoding",
								float64(bytes)/(1<<20),
							),
						})
					}
				}
				native[i], err = ffmpegMetadata(ctx, reader, parserOther, progress)
				return err
			},
		})
		pictureSlot, previewSlot := -1, -1
		if pictureErrors[i] == nil && options.Sampled {
			previewSlot = len(outputs)
			outputs = append(outputs, ffmpegstream.Output{
				Arguments: []string{"-map", fmt.Sprintf("[preview%d]", i), "-an", "-sn", "-dn", "-c:v", "rawvideo", "-threads:v", "1",
					"-pix_fmt", "gbrp16le", "-fps_mode", "passthrough", "-f", "rawvideo"},
				Consume: func(reader io.Reader) error {
					return previews[i].consume(ctx, reader, 0, 1, func(count int) {
						s.service.reportProgress(ctx, Progress{
							Phase: "measuring_pictures",
							Message: fmt.Sprintf(
								"Inspected %d preview frames from input %d; full-resolution pictures sampled every %d frames",
								count,
								i+1,
								step,
							),
						})
					})
				},
			})
		}
		if pictureErrors[i] == nil {
			pictureSlot = len(outputs)
			outputs = append(outputs, ffmpegstream.Output{
				Arguments: []string{
					"-map",
					fmt.Sprintf("[picture%d]", i),
					"-an",
					"-sn",
					"-dn",
					"-c:v",
					"rawvideo",
					"-threads:v",
					"1",
					"-pix_fmt",
					"gbrp16le",
					"-fps_mode",
					"passthrough",
					"-f",
					"rawvideo",
				},
				Consume: func(reader io.Reader) error {
					first := 0
					if options.Sampled && i == 1 {
						first = automaticFirstSample(*options.Comparison.FrameOffset, step)
					}
					var progress func(int)
					if !options.Sampled {
						progress = func(count int) {
							s.service.reportProgress(
								ctx,
								Progress{Phase: "measuring_pictures", Message: fmt.Sprintf("Measured %d video frames from input %d", count, i+1)},
							)
						}
					}
					return pictures[i].consume(ctx, reader, first, step, progress)
				},
			})
		}
		if i == 0 {
			audioSlot = len(outputs)
			outputs = append(outputs, audio.Outputs(ctx)...)
		}
		cmd := exec.CommandContext(ctx, executable, args...)
		cmd.Stderr = &automaticDiagnostics{proofs: proofs}
		if i == 0 {
			if prepared != nil {
				s.service.reportProgress(ctx, Progress{Phase: "reading_shared_media", Message: "Reading HDR metadata and the selected audio batch"})
			} else {
				s.service.progress(ctx, "reading_shared_media")
			}
		} else {
			s.service.reportProgress(ctx, Progress{Phase: "reading_reference_media", Message: "Measuring the optional HDR10 reference"})
		}
		finished, runErr := ffmpegstream.Run(ctx, cmd, outputs)
		if runErr != nil {
			if i == 0 || ctx.Err() != nil {
				for _, value := range native {
					value.Release()
				}
				return nil, nil, fmt.Errorf("run shared HDR capture: %w", runErr)
			}
			pictureErrors[i] = runErr
			continue
		}
		if i == 0 {
			completion = finished
		}
		metadataErr := errors.Join(finished.ProcessError, finished.Errors[0])
		if native[i] == nil || metadataErr != nil {
			native[i].Release()
			native[i] = nil
			if i == 0 {
				return nil, nil, fmt.Errorf("shared source traversal did not complete: %w", errors.Join(metadataErr, errDecodedOutput))
			}
			pictureErrors[i] = errors.Join(metadataErr, errDecodedOutput)
			continue
		}
		if pictureSlot >= 0 {
			pictureErrors[i] = errors.Join(pictureErrors[i], finished.ProcessError, finished.Errors[pictureSlot], proofs[i].finish(),
				bindAutomaticTimeline(native[i], pictures[i], proofs[i], profile5[i]))
			if previewSlot >= 0 {
				pictureErrors[i] = errors.Join(pictureErrors[i], finished.Errors[previewSlot])
				if len(previews[i].frames) != len(proofs[i].times) {
					pictureErrors[i] = errors.Join(pictureErrors[i], errDecodedMapping)
				}
				for index := range min(len(previews[i].frames), len(proofs[i].times)) {
					previews[i].frames[index].PTS = measurementTimestamp(proofs[i].times[index])
				}
			}
		}
	}
	selectAutomaticFormats(native[0], formats)
	reports := make([]*MeasurementReport, 0, 2)
	if options.Automatic {
		report := newMeasurementReport(api.HDRDecodedAutomatic, primary, options)
		report.Decoder = identity
		if referenceNote != nil {
			report.Result.Checks = append(report.Result.Checks, *referenceNote)
		}
		report.Processing = []string{
			"one FFmpeg traversal per input; primary metadata/pictures/audio share capture; live Matroska native collectors; timestamp rounding checked before exact rebinding",
			automaticColourFilter(
				primary.Source,
				profile5[0],
			),
			"coded raster retained for border checks; complete 2x2 linear RGB footprints; FALL=mean(MaxRGB); RGB means use independently lit Y>1-nit pixels; sparse full-resolution xy clouds at Y>0.5 nit; uniform Reinhard SDR scene previews",
		}
		if options.Sampled {
			report.Processing[2] = fmt.Sprintf(
				"RGB measurement every %d source frames; colour every %d frames; small resized PQ previews for metadata-selected scene windows; full-resolution borders measured only on selected RGB frames; complete audio and compressed metadata",
				step,
				step*30,
			)
		}
		if pictureErrors[0] == nil && pictures[0] != nil {
			report.Automatic = &AutomaticMeasurement{
				Frames:       pictures[0].frames,
				Profile5:     profile5[0],
				Chromaticity: [2][]ChromaticityPoint{pictures[0].chroma, nil},
			}
			if options.Sampled {
				report.Automatic.Sampling = &AutomaticSampling{
					Step:    step,
					Frames:  [2]int{len(proofs[0].times), 0},
					Origins: [2]MeasurementTimestamp{measurementTimestamp(proofs[0].times[0]), {}},
				}
			}
			report.Result.Selected, report.Result.Measured = len(pictures[0].frames), len(pictures[0].frames)
			report.Result.State = api.HDRDecodedComplete
			if reference != nil && pictureErrors[1] == nil && native[1] != nil {
				report.Sources[1] = reference.Fingerprint
				report.Automatic.Reference, report.Automatic.Chromaticity[1] = pictures[1].frames, pictures[1].chroma
				if options.Sampled {
					report.Automatic.Sampling.Frames[1] = len(proofs[1].times)
					report.Automatic.Sampling.Origins[1] = measurementTimestamp(proofs[1].times[0])
					if err := setAutomaticSampleCadence(report, proofs); err != nil {
						report.OmitAutomaticReference()
					}
				}
			} else if reference != nil {
				report.Result.Checks = append(report.Result.Checks, api.HDRCheckResult{
					Code:    "reference_unavailable",
					State:   api.HDRCheckUnsupported,
					Message: "The selected HDR10 reference could not be measured; primary-source checks remain available.",
				})
			}
			if len(native) > 1 {
				native[1].Release()
				native[1] = nil
			}
			native[0].otherRetainedBytes = otherBytes + reservedBytes
			if err := finishAutomaticReport(ctx, report, native[0], pictures[0]); err != nil {
				measurementError(report, err, false)
			}
		} else {
			measurementError(report, pictureErrors[0], true)
			if profile5[0] || primary.Source.DolbyVisionProfile != "" && primary.Source.Transfer != "smpte2084" {
				report.Result.Checks = append(report.Result.Checks, optionalHDR10ReferenceNote())
			}
		}
		setDecodedCoverage(report)
		reports = append(reports, report)
	}
	if prepared != nil {
		if err := prepared.checkpoint(native[0], reports); err != nil {
			return native[0], reports, err
		}
		if err := prepared.finish(ctx, primary, options, identity, audio, audioErr, completion.Errors[audioSlot:], completion.ProcessError); err != nil {
			return native[0], reports, err
		}
		if audio != nil {
			audio.Close()
		}
		if err := prepared.remaining(ctx, audioService, audioRoot, primary, options, identity); err != nil {
			return native[0], reports, err
		}
		return native[0], reports, nil
	}
	audioReport := newMeasurementReport(api.HDRDecodedAudio, primary, options)
	audioReport.Decoder, audioReport.Result.Profile = identity, api.AudioAnalysisProfileVersion
	audioReport.Decoder.RequestedThreads, audioReport.Decoder.EffectiveThreads = 0, nil
	switch {
	case audio == nil && audioErr == nil:
		audioReport.Result.State, audioReport.Result.Reason = api.HDRDecodedNotRun, "no_audio"
	case audioErr != nil:
		measurementError(audioReport, audioErr, true)
	default:
		tracks, err := audio.Finish(ctx, completion.Errors[audioSlot:], completion.ProcessError)
		if err != nil {
			measurementError(audioReport, err, false)
		} else if err := captureAutomaticAudio(audioReport, tracks); err != nil {
			measurementError(audioReport, err, false)
		}
		if audio.CorrectiveDecode {
			audioReport.Result.Checks = append(audioReport.Result.Checks, api.HDRCheckResult{
				Code:    "audio_corrective_decode",
				State:   api.HDRCheckInconclusive,
				Message: "The measured audio duration required a corrective audio-only decode for the existing spectrogram geometry. Sample count and PCM digest were verified against the shared pass.",
			})
		}
	}
	reports = append(reports, audioReport)
	return native[0], reports, nil
}

func (s *Session) automaticMetadataOnly(ctx context.Context, primary DecodedInput, options api.HDRDecodedOptions,
	formats api.HDRAnalysisFormats, otherBytes int64, decoderError error,
) (*NativeExtraction, []*MeasurementReport, error) {
	// No complete traversal has started. The existing native walker can still
	// supply every supported metadata check in one pass without FFmpeg.
	native, err := s.ExtractNativeFileWithRetained(ctx, primary.Path, formats, otherBytes)
	if err != nil {
		return nil, nil, err
	}
	reports := make([]*MeasurementReport, 0, 2)
	for _, mode := range []api.HDRDecodedMode{api.HDRDecodedAutomatic, api.HDRDecodedAudio} {
		if !options.Requested(mode) {
			continue
		}
		report := newMeasurementReport(mode, primary, options)
		measurementError(report, decoderError, true)
		if primary.Source.RawHEVC {
			if mode == api.HDRDecodedAudio {
				report.Result.State, report.Result.Reason = api.HDRDecodedNotRun, "no_audio"
			} else {
				report.Result.Checks = append(report.Result.Checks, api.HDRCheckResult{
					Code:    "raw_picture_timestamps",
					State:   api.HDRCheckUnsupported,
					Message: "Raw HEVC has no authoritative presentation timestamps. Native metadata checks were retained without fabricating picture timing.",
				})
			}
		}
		if mode == api.HDRDecodedAutomatic && strings.Split(primary.Source.DolbyVisionProfile, ".")[0] == "5" {
			report.Result.Checks = append(report.Result.Checks, optionalHDR10ReferenceNote())
		}
		reports = append(reports, report)
	}
	return native, reports, nil
}

func automaticColourFilter(source api.HDRDecodedSourceEvidence, profile5 bool) string {
	if profile5 {
		return "libplacebo=apply_dolbyvision=true:tonemapping=clip:peak_detect=false:gamut_mode=clip:format=gbrp16le:colorspace=gbr:color_primaries=bt2020:color_trc=smpte2084:range=pc"
	}
	return "zscale=rangein=" + source.Range + ":matrixin=2020_ncl:transferin=smpte2084:primariesin=2020:range=full:primaries=2020:transfer=smpte2084:matrix=gbr,format=gbrp16le"
}

func probeDVReshape(ctx context.Context, decoder pictureDecoder) error {
	_, err := decoder.probe(ctx, nil, "-hide_banner", "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "color=black:s=8x8:d=1",
		"-vf", automaticColourFilter(api.HDRDecodedSourceEvidence{}, true), "-frames:v", "1", "-f", "null", "-")
	return err
}

type automaticProof struct {
	source      api.HDRDecodedSourceEvidence
	name        string
	numerator   int64
	denominator uint64
	times       []pictureTime
	pending     bool
	err         error
}

func (p *automaticProof) line(line string) {
	if p.err != nil || !strings.HasPrefix(line, p.name) {
		return
	}
	if match := showTimeBase.FindStringSubmatch(line); match != nil {
		n, ne := strconv.ParseInt(match[1], 10, 32)
		d, de := strconv.ParseUint(match[2], 10, 32)
		if ne != nil || de != nil || n <= 0 || d == 0 || p.numerator != 0 {
			p.err = errDecodedMapping
			return
		}
		p.numerator, p.denominator = n, d
	} else if match := showPicture.FindStringSubmatch(line); match != nil {
		index, ie := strconv.Atoi(match[1])
		pts, pe := strconv.ParseInt(match[2], 10, 64)
		width, we := strconv.Atoi(match[6])
		height, he := strconv.Atoi(match[7])
		if ie != nil || pe != nil || we != nil || he != nil || p.pending || index != len(p.times) || index >= MaxFrames ||
			p.numerator == 0 || width != p.source.Width || height != p.source.Height || match[4] != "1" || match[5] != "1" ||
			!strings.HasSuffix(match[3], strconv.Itoa(p.source.BitDepth)+"le") ||
			pts > math.MaxInt64/p.numerator || pts < math.MinInt64/p.numerator {
			p.err = errDecodedMapping
			return
		}
		stamp := pictureTime{
			Value:     pts * p.numerator,
			Timescale: p.denominator,
			Valid:     true,
		}
		if len(p.times) > 0 && stamp.rational().Cmp(p.times[len(p.times)-1].rational()) <= 0 {
			p.err = errDecodedMapping
			return
		}
		p.times = append(p.times, stamp)
		p.pending = true
	} else if match := showColour.FindStringSubmatch(line); match != nil {
		if !p.pending {
			p.err = errDecodedMapping
			return
		}
		p.pending = false
		if strings.Split(p.source.DolbyVisionProfile, ".")[0] != "5" {
			rangeValue := "tv"
			if p.source.Range == "full" {
				rangeValue = "pc"
			}
			if match[1] != rangeValue || match[2] != p.source.Matrix || match[3] != p.source.Primaries || match[4] != p.source.Transfer {
				p.err = errDecodedMapping
			}
		}
	}
}

func (p *automaticProof) finish() error {
	if p.err != nil {
		return p.err
	}
	if p.pending || p.numerator == 0 || len(p.times) == 0 {
		return errDecodedOutput
	}
	return nil
}

type automaticDiagnostics struct {
	proofs   []*automaticProof
	line     []byte
	overflow bool
}

func (d *automaticDiagnostics) Write(data []byte) (int, error) {
	for _, value := range data {
		switch {
		case value == '\n':
			if !d.overflow {
				for _, proof := range d.proofs {
					proof.line(string(d.line))
				}
			}
			d.line, d.overflow = d.line[:0], false
		case len(d.line) < 8192:
			d.line = append(d.line, value)
		default:
			d.overflow = true
		}
	}
	return len(data), nil
}

func bindAutomaticTimeline(native *NativeExtraction, pictures *automaticPictures, proof *automaticProof, profile5 bool) error {
	if native == nil || pictures == nil || len(pictures.frames) == 0 || !pictures.selectedOnly && len(pictures.frames) != len(proof.times) ||
		profile5 && (native.DolbyVision == nil || len(native.DolbyVision.Records) == 0) {
		return errDecodedMapping
	}
	if pictures.selectedOnly {
		if pictures.sampleStep < 1 || len(pictures.frames) != automaticSampleCount(len(proof.times), pictures.sampleFirst, pictures.sampleStep) {
			return errDecodedMapping
		}
		for i, frame := range pictures.frames {
			if frame.Index != pictures.sampleFirst+i*pictures.sampleStep {
				return errDecodedMapping
			}
		}
	}
	if native.DolbyVision != nil {
		if len(native.DolbyVision.Pictures) != len(proof.times) {
			return errDecodedMapping
		}
		for _, record := range native.DolbyVision.Records {
			if (record.RPU.Profile == 5) != profile5 {
				return errDecodedMapping
			}
		}
	}
	if native.HDR10Plus != nil && len(native.HDR10Plus.Frames) != len(proof.times) {
		return errDecodedMapping
	}
	for i, stamp := range proof.times {
		var metadataTimes []pictureTime
		if native.DolbyVision != nil {
			observed := native.DolbyVision.Pictures[i].PTS
			metadataTimes = append(metadataTimes, pictureTime{
				Value:     observed.Value,
				Timescale: observed.Timescale,
				Valid:     observed.Valid,
			})
		}
		if native.HDR10Plus != nil {
			other := native.HDR10Plus.Frames[i].PTS
			metadataTimes = append(metadataTimes, pictureTime{
				Value:     other.Value,
				Timescale: other.Timescale,
				Valid:     other.Valid,
			})
		}
		for j, observed := range metadataTimes {
			if !observed.Valid || observed.Timescale == 0 {
				return errDecodedMapping
			}
			// FFmpeg's live Matroska copy rounds to milliseconds. Each available
			// metadata timeline must agree within half a tick before exact rebinding.
			delta := new(big.Rat).Sub(observed.rational(), stamp.rational())
			if delta.Abs(delta).Cmp(big.NewRat(501, 1000000)) > 0 || j > 0 && observed.rational().Cmp(metadataTimes[0].rational()) != 0 {
				return errDecodedMapping
			}
		}
	}
	// Commit the exact-clock rebind only after every picture and both metadata
	// timelines have passed, leaving failed captures untouched and reportable.
	for i, stamp := range proof.times {
		if native.DolbyVision != nil {
			native.DolbyVision.Pictures[i].PTS.Value, native.DolbyVision.Pictures[i].PTS.Timescale = stamp.Value, stamp.Timescale
		}
		if !pictures.selectedOnly {
			pictures.frames[i].PTS = measurementTimestamp(stamp)
		}
		if native.HDR10Plus != nil {
			native.HDR10Plus.Frames[i].PTS.Value, native.HDR10Plus.Frames[i].PTS.Timescale = stamp.Value, stamp.Timescale
		}
	}
	if pictures.selectedOnly {
		for i := range pictures.frames {
			index := pictures.frames[i].Index
			if index < 0 || index >= len(proof.times) {
				return errDecodedMapping
			}
			pictures.frames[i].PTS = measurementTimestamp(proof.times[index])
		}
	}
	return nil
}

func optionalHDR10ReferenceNote() api.HDRCheckResult {
	return api.HDRCheckResult{
		Code:    "optional_hdr10_reference",
		State:   api.HDRCheckNotRun,
		Message: "This DV-only input has no HDR10-compatible base layer. An optional HDR10 reference grade can provide additional brightness, grade and colour comparisons.",
	}
}
