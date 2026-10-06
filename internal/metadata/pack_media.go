// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/metadata/mediainfo"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func requiresPackMediaEvidence(meta preparationstate.State) bool {
	return meta.TVPack && strings.TrimSpace(meta.DiscType) == "" &&
		requiresMetadataField(meta.MetadataRequirements, api.CanonicalCategoryTV, api.MetadataRequirementNonDiscTVPackMedia)
}

// collectPackMediaEvidence probes each selected non-disc pack file sequentially.
// Primary probe failures stop preparation; other incomplete reports remain unresolved.
// Cancellation stops collection.
// Namespaced artifacts prevent one episode, changed source, or changed file
// from reusing another report. Unchanged files reuse the existing exporter cache.
func (s *Service) collectPackMediaEvidence(ctx context.Context, meta *preparationstate.State) error {
	logger := logging.FromContext(ctx, s.logger)
	tmpRoot, err := db.Subdir(s.cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		return fmt.Errorf("metadata: pack media temporary directory: %w", err)
	}
	releaseDir, _, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, meta.Release)
	if err != nil {
		return fmt.Errorf("metadata: pack media release directory: %w", err)
	}
	facts := api.MediaFileFacts{ExpectedFileCount: len(meta.FileList)}
	logger.Infof("metadata: collecting per-file pack media evidence count=%d", len(meta.FileList))
	for _, file := range meta.FileList {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("metadata: pack media canceled: %w", err)
		}
		fact := api.MediaFileFact{FileName: filepath.Base(file), Primary: pathutil.SamePath(file, meta.VideoPath)}
		facts.Files = append(facts.Files, fact)
		if s.mi == nil {
			continue
		}
		info, err := os.Stat(file)
		if err != nil {
			if fact.Primary {
				return fmt.Errorf("metadata: primary pack media file: %w", err)
			}
			logger.Warnf("metadata: pack media file unavailable; evidence remains unresolved")
			continue
		}
		fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
			Version           string
			SourceFingerprint string
			SourcePath        string
			Path              string
			Size              int64
			Modified          int64
		}{"pack-media-v1", meta.SourceFingerprint, filepath.Clean(meta.SourcePath), filepath.Clean(file), info.Size(), info.ModTime().UnixNano()})
		if err != nil {
			return fmt.Errorf("metadata: fingerprint pack media: %w", err)
		}
		result, err := s.mi.Export(ctx, mediainfo.Request{
			SourcePath:  meta.SourcePath,
			VideoPath:   file,
			TempRoot:    tmpRoot,
			ArtifactDir: filepath.Join(releaseDir, "pack-media", string(fingerprint)),
			Release:     meta.Release,
		})
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("metadata: pack media: %w", err)
			}
			if fact.Primary {
				return fmt.Errorf("metadata: mediainfo: %w", err)
			}
			logger.Warnf("metadata: pack media probe failed; evidence remains unresolved")
			continue
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("metadata: pack media canceled: %w", err)
		}
		doc, err := loadMediaInfoDoc(result.JSONPath)
		if err != nil {
			if fact.Primary {
				return fmt.Errorf("metadata: primary pack media report: %w", err)
			}
			logger.Warnf("metadata: pack media report unreadable; evidence remains unresolved")
			continue
		}
		facts.Files[len(facts.Files)-1] = packMediaFileFact(file, fact.Primary, doc)
		if fact.Primary {
			meta.MediaInfoJSONPath, meta.MediaInfoTextPath = result.JSONPath, result.TextPath
		}
	}
	meta.MediaFileFacts = mediafacts.SummarizeFiles(facts)
	logger.Debugf("metadata: per-file pack media evidence status=%s count=%d", meta.MediaFileFacts.Status, len(facts.Files))
	return nil
}

func packMediaFileFact(file string, primary bool, doc mediaInfoDoc) api.MediaFileFact {
	general, video, audio := splitMediaInfoTracks(doc)
	fact := api.MediaFileFact{
		FileName:        filepath.Base(file),
		Primary:         primary,
		VideoTrackCount: len(video),
	}
	if len(general) == 0 || len(video) == 0 {
		return fact
	}
	// MediaInfo does not establish source provenance; filenames are not proof.
	fact.Container = strings.ToLower(trackString(general[0], "Format"))
	switch fact.Container {
	case "matroska":
		fact.Container = "mkv"
	case "mpeg-4":
		fact.Container = "mp4"
	case "mpeg-ts":
		fact.Container = "ts"
	}
	fact.Resolution = resolutionFromMediaInfo(doc, "")
	fact.VideoEncode, fact.VideoCodec, _, fact.BitDepth = videoEncodeFromMedia(doc, "")
	fact.AudioStatus, fact.SubtitleStatus = api.MetadataEvidenceStatusComplete, api.MetadataEvidenceStatusComplete
	if trackNumericInt(general[0], "VideoCount") > len(video) {
		fact.VideoTrackCount = 0
	}
	subtitleCount := 0
	for _, track := range doc.Media.Track {
		kind, ok := mediaTrackKind(track)
		if !ok {
			continue
		}
		if kind == api.MediaTrackAudio && isCommentaryOrCompatibilityAudioValue(trackString(track, "Title", "Title_String", "Title_String2", "Title_String3")) {
			continue
		}
		language := languageutil.NormalizeLanguageList([]string{trackString(track, "Language", "Language_String", "Language_String2", "Language_String3")})
		known := len(language) > 0
		for _, value := range language {
			code := languageutil.NormalizeLanguageCode(value)
			if code == "" || code == "und" || code == "mul" {
				known = false
			}
		}
		if kind == api.MediaTrackAudio {
			fact.AudioLanguages = append(fact.AudioLanguages, language...)
			if !known {
				fact.AudioStatus = api.MetadataEvidenceStatusPartial
			}
		} else {
			subtitleCount++
			fact.SubtitleLanguages = append(fact.SubtitleLanguages, language...)
			if !known {
				fact.SubtitleStatus = api.MetadataEvidenceStatusPartial
			}
		}
	}
	if trackNumericInt(general[0], "AudioCount") > len(audio) {
		fact.AudioStatus = api.MetadataEvidenceStatusUnavailable
	}
	if trackNumericInt(general[0], "TextCount") > subtitleCount {
		fact.SubtitleStatus = api.MetadataEvidenceStatusUnavailable
	}
	fact.AudioLanguages = languageutil.NormalizeLanguageList(fact.AudioLanguages)
	fact.SubtitleLanguages = languageutil.NormalizeLanguageList(fact.SubtitleLanguages)
	return fact
}
