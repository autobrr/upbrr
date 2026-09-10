// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func mediaTrackFacts(meta preparationstate.State, doc mediaInfoDoc) ([]api.MediaTrackFacts, []string, []string, error) {
	manifest, err := mediaTrackManifestFingerprint(meta, doc)
	if err != nil {
		return nil, nil, nil, err
	}
	resourceID := mediaTrackResourceID(meta)
	tracks := make([]api.MediaTrackFacts, 0)
	ordinals := map[api.MediaTrackKind]int{}
	nativeCounts := make(map[string]int)
	for _, track := range doc.Media.Track {
		if kind, ok := mediaTrackKind(track); ok {
			nativeCounts[string(kind)+":"+trackString(track, "StreamOrder", "ID", "UniqueID")]++
		}
	}
	for _, track := range doc.Media.Track {
		kind, ok := mediaTrackKind(track)
		if !ok {
			continue
		}
		ordinals[kind]++
		ordinal := ordinals[kind]
		nativeID := trackString(track, "StreamOrder", "ID", "UniqueID")
		if nativeCounts[string(kind)+":"+nativeID] > 1 {
			nativeID = ""
		}
		trackKey := nativeID
		if trackKey == "" {
			trackKey = manifest + ":" + strconv.Itoa(ordinal)
		}
		title := trackString(track, "Title", "Title_String", "Title_String2", "Title_String3")
		detected := languageutil.NormalizeLanguageList([]string{trackString(track, "Language", "Language_String", "Language_String2", "Language_String3")})
		tracks = append(tracks, api.MediaTrackFacts{
			ID:                  opaqueMediaTrackID(resourceID, kind, trackKey),
			Kind:                kind,
			ResourceID:          resourceID,
			ManifestFingerprint: manifest,
			NativeID:            nativeID,
			Ordinal:             ordinal,
			DetectedLanguages:   append([]string(nil), detected...),
			Languages:           append([]string(nil), detected...),
			LanguageProvenance:  api.FactProvenanceAutomatic,
			Default:             mediaTrackDefault(track),
			Commentary:          isCommentaryOrCompatibilityAudioValue(title),
		})
	}
	return tracks, aggregateTrackLanguages(tracks, api.MediaTrackAudio), aggregateTrackLanguages(tracks, api.MediaTrackSubtitle), nil
}

func mediaTrackKind(track map[string]any) (api.MediaTrackKind, bool) {
	switch strings.ToLower(trackString(track, "@type")) {
	case "audio":
		return api.MediaTrackAudio, true
	case "text", "subtitle":
		return api.MediaTrackSubtitle, true
	default:
		return "", false
	}
}

func mediaTrackDefault(track map[string]any) bool {
	value := strings.ToLower(trackString(track, "Default", "Default/String"))
	return value == "yes" || value == "true" || value == "1"
}

func mediaTrackResourceID(meta preparationstate.State) string {
	resource := strings.TrimSpace(meta.VideoPath)
	if resource == "" {
		resource = strings.TrimSpace(meta.MediaInfoJSONPath)
	}
	if resource == "" {
		resource = strings.TrimSpace(meta.SourcePath)
	}
	return "media_" + shortMediaTrackHash(resource)
}

func mediaTrackManifestFingerprint(meta preparationstate.State, doc mediaInfoDoc) (string, error) {
	ordered := make([]map[string]any, 0)
	for _, track := range doc.Media.Track {
		if _, ok := mediaTrackKind(track); ok {
			ordered = append(ordered, track)
		}
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		SourceFingerprint string
		ResourceID        string
		Playlists         []api.PlaylistInfo
		Tracks            []map[string]any
	}{meta.SourceFingerprint, mediaTrackResourceID(meta), meta.SelectedBDMVPlaylists, ordered})
	if err != nil {
		return "", fmt.Errorf("metadata: fingerprint inspected track manifest: %w", err)
	}
	return string(fingerprint), nil
}

func opaqueMediaTrackID(resourceID string, kind api.MediaTrackKind, key string) string {
	return "track_" + shortMediaTrackHash(strings.Join([]string{resourceID, string(kind), key}, "\x00"))
}

func shortMediaTrackHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:12])
}

func applyTrackLanguageOverrides(meta *preparationstate.State, corrections []api.TrackLanguageCorrection) error {
	if meta == nil || len(corrections) == 0 {
		return nil
	}
	byID := make(map[string]int, len(meta.MediaTracks))
	for index, track := range meta.MediaTracks {
		byID[track.ID] = index
	}
	for _, correction := range corrections {
		index, ok := byID[strings.TrimSpace(correction.TrackID)]
		if !ok {
			return &api.CorrectionConflictError{
				Field:   api.CorrectionFieldMetadataTrackLanguages,
				TrackID: correction.TrackID,
				Reason:  "track is not present in the inspected media",
			}
		}
		track := &meta.MediaTracks[index]
		if strings.TrimSpace(correction.ManifestFingerprint) == "" || correction.ManifestFingerprint != track.ManifestFingerprint {
			return &api.CorrectionConflictError{
				Field:   api.CorrectionFieldMetadataTrackLanguages,
				TrackID: correction.TrackID,
				Reason:  "track manifest changed",
			}
		}
		track.Languages = languageutil.NormalizeLanguageList(correction.Languages)
		track.LanguageProvenance = factProvenanceForList(track.Languages)
	}
	return nil
}

func aggregateTrackLanguages(tracks []api.MediaTrackFacts, kind api.MediaTrackKind) []string {
	values := make([]string, 0)
	for _, track := range tracks {
		if track.Kind != kind || (kind == api.MediaTrackAudio && track.Commentary) {
			continue
		}
		values = append(values, track.Languages...)
	}
	return languageutil.NormalizeLanguageList(values)
}

func factProvenanceForList(values []string) api.FactProvenance {
	if len(values) == 0 {
		return api.FactProvenanceManualEmpty
	}
	return api.FactProvenanceManual
}

func hasHardcodedSubtitleMarker(sourcePath string) bool {
	for _, token := range strings.FieldsFunc(strings.ToUpper(filepath.Base(sourcePath)), func(r rune) bool { return strings.ContainsRune(" ._-[]()", r) }) {
		if token == "HARDSUB" || token == "HARDSUBS" || token == "HARDCODED" {
			return true
		}
	}
	return false
}
