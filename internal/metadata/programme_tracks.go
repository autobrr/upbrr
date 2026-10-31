// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/autobrr/upbrr/internal/mediafacts"

	"github.com/autobrr/upbrr/internal/pathing"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

// inspectProgrammeTracks extends inspected evidence without changing the selected primary file.
func (s *Service) inspectProgrammeTracks(ctx context.Context, meta preparationstate.State) (preparationstate.State, error) {
	if s.mi == nil {
		return meta, nil
	}
	root, err := db.Subdir(s.cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		return meta, fmt.Errorf("programme track cache: %w", err)
	}
	complete := len(meta.FileList) > 0
	meta.MediaTracks = nil
	meta.PrimaryAudioTrackID = ""
	meta.MediaFileFacts = api.MediaFileFacts{
		ExpectedFileCount: len(meta.FileList),
		Status:            api.MetadataEvidenceStatusPartial,
		TechnicalStatus:   api.MetadataEvidenceStatusPartial,
		LanguageStatus:    api.MetadataEvidenceStatusPartial,
	}
	fingerprint := meta.MediaInspectionFingerprint
	if fingerprint == "" {
		fingerprint = meta.SourceFingerprint
	}
	for _, file := range meta.FileList {
		primary := pathing.SamePath(file, meta.VideoPath)
		doc, _, inspectErr := inspectReleaseMediaInfoReports(ctx, s.mi, meta, file, fingerprint, root)
		if inspectErr != nil {
			if ctx.Err() != nil {
				return meta, fmt.Errorf("programme track inspection canceled: %w", ctx.Err())
			}
			complete = false
			continue
		}
		resource := meta
		resource.VideoPath = file
		tracks, primaryAudioID, _, _, trackErr := mediaTrackFacts(resource, doc)
		if trackErr != nil {
			return meta, fmt.Errorf("programme track facts: %w", trackErr)
		}
		if len(doc.Media.Track) == 0 {
			complete = false
			continue
		}
		meta.MediaTracks = append(meta.MediaTracks, tracks...)
		if primary {
			meta.PrimaryAudioTrackID = primaryAudioID
		}
		general, video, _ := splitMediaInfoTracks(doc)
		if len(video) == 0 {
			complete = false
		}
		container := ""
		if len(general) == 1 {
			switch strings.ToUpper(trackString(general[0], "Format")) {
			case "MATROSKA":
				container = "mkv"
			case "MPEG-4":
				container = "mp4"
			case "MPEG-TS":
				container = "ts"
			case "AVI":
				container = "avi"
			}
		}
		encoder, codec, settings, depth := videoEncodeFromMedia(doc, meta.Type)
		meta.MediaFileFacts.Files = append(meta.MediaFileFacts.Files, mediafacts.ResolveResourceLanguages(api.MediaFileFact{
			ResourceID:          mediaTrackResourceID(resource),
			FileName:            filepath.Base(file),
			Primary:             primary,
			Container:           container,
			HasEncodeSettings:   settings,
			EncodeSettingsKnown: len(video) == 1,
			RateControl:         mediafacts.VideoRateControlFromMediaInfo(doc),
			Resolution:          resolutionFromMediaInfo(doc, file, ""),
			VideoCodec:          codec,
			VideoEncode:         encoder,
			VideoEncoder:        measuredVideoEncoder(doc),
			BitDepth:            depth,
			HDRFacts:            mediafacts.HDRFromMediaInfoDocument(doc),
			VideoTrackCount:     len(video),
			AudioLanguages:      aggregateTrackLanguages(tracks, api.MediaTrackAudio),
			SubtitleLanguages:   aggregateTrackLanguages(tracks, api.MediaTrackSubtitle),
			AudioStatus:         api.MetadataEvidenceStatusComplete,
			SubtitleStatus:      api.MetadataEvidenceStatusComplete,
		}, tracks))
	}
	meta.TrackCoverageComplete = complete
	meta.MediaFileFacts = mediafacts.SummarizeFiles(meta.MediaFileFacts)
	meta.TrackAudioLanguages = aggregateTrackLanguages(meta.MediaTracks, api.MediaTrackAudio)
	meta.TrackSubtitleLanguages = aggregateTrackLanguages(meta.MediaTracks, api.MediaTrackSubtitle)
	meta.AudioAbsent = complete && len(meta.TrackAudioLanguages) == 0
	for _, track := range meta.MediaTracks {
		if track.Kind == api.MediaTrackAudio {
			meta.AudioAbsent = false
			break
		}
	}
	return meta, nil
}
