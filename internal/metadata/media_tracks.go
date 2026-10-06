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
	"github.com/autobrr/upbrr/internal/metadata/discparse"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func mediaTrackFacts(meta preparationstate.State, doc mediaInfoDoc) ([]api.MediaTrackFacts, string, []string, []string, error) {
	manifest, err := mediaTrackManifestFingerprint(meta, doc)
	if err != nil {
		return nil, "", nil, nil, err
	}
	resourceID := mediaTrackResourceID(meta)
	tracks, err := bdInfoAudioTrackFacts(meta)
	if err != nil {
		return nil, "", nil, nil, err
	}
	bdInfoAudio := len(tracks) > 0
	ordinals := map[api.MediaTrackKind]int{}
	nativeCounts := make(map[string]int)
	_, _, audioTracks := splitMediaInfoTracks(doc)
	primaryAudioIndex := selectPrimaryAudioTrackIndex(audioTracks)
	primaryAudioTrackID := ""
	for _, track := range tracks {
		if !track.Commentary {
			primaryAudioTrackID = track.ID
			break
		}
	}
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
		if kind == api.MediaTrackAudio && bdInfoAudio {
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
		facts := api.MediaTrackFacts{
			ID:                  opaqueMediaTrackID(resourceID, kind, trackKey),
			Kind:                kind,
			ResourceID:          resourceID,
			ManifestFingerprint: manifest,
			NativeID:            nativeID,
			Ordinal:             ordinal,
			Title:               strings.TrimSpace(title),
			Codec:               strings.TrimSpace(normalizeAudioFormat(track)),
			ChannelLayout:       trackString(track, "ChannelLayout", "ChannelLayout_Original", "ChannelPositions", "ChannelPositions_Original"),
			Channels:            mediaTrackPositiveInt(track, "Channels_Original", "Channels", "Channel_s_", "Channel_s__Original"),
			SampleRate:          mediaTrackPositiveInt(track, "SamplingRate", "SamplingRate_String"),
			DetectedLanguages:   append([]string(nil), detected...),
			Languages:           append([]string(nil), detected...),
			LanguageProvenance:  api.FactProvenanceAutomatic,
			Default:             mediaTrackDefault(track),
			Commentary:          isCommentaryOrCompatibilityAudioValue(title),
		}
		tracks = append(tracks, facts)
		if kind == api.MediaTrackAudio && ordinal-1 == primaryAudioIndex {
			primaryAudioTrackID = facts.ID
		}
	}
	return tracks, primaryAudioTrackID, aggregateTrackLanguages(tracks, api.MediaTrackAudio), aggregateTrackLanguages(tracks, api.MediaTrackSubtitle), nil
}

// bdInfoAudioTrackFacts keeps every reported stream distinct within its disc and
// playlist. A hidden marker is evidence only; it never establishes commentary.
func bdInfoAudioTrackFacts(meta preparationstate.State) ([]api.MediaTrackFacts, error) {
	if !strings.EqualFold(meta.DiscType, "BDMV") {
		return nil, nil
	}
	var tracks []api.MediaTrackFacts
	for discIndex, disc := range meta.Discs {
		discID := disc.ID
		if discID == "" {
			discID = strconv.Itoa(discIndex)
		}
		for reportIndex, report := range disc.Reports {
			info := discparse.ParseBDInfoSummary(report.Summary, "", "")
			if len(info.Audio) == 0 {
				continue
			}
			playlistID := report.Playlist.ID
			if playlistID == "" {
				playlistID = strconv.Itoa(reportIndex)
			}
			resourceID := "media_" + shortMediaTrackHash(strings.Join([]string{meta.SourcePath, discID, playlistID}, "\x00"))
			manifest, err := api.CanonicalWorkflowFingerprint(struct {
				SourceFingerprint string
				ResourceID        string
				Playlist          api.PlaylistInfo
				Audio             []discparse.BDAudio
			}{meta.SourceFingerprint, resourceID, report.Playlist, info.Audio})
			if err != nil {
				return nil, fmt.Errorf("metadata: fingerprint BDInfo audio manifest: %w", err)
			}
			for index, audio := range info.Audio {
				languages := languageutil.NormalizeLanguageList([]string{languageutil.NormalizeLanguageLabel(audio.Language)})
				tracks = append(tracks, api.MediaTrackFacts{
					ID:                   opaqueMediaTrackID(resourceID, api.MediaTrackAudio, string(manifest)+":"+strconv.Itoa(index+1)),
					Kind:                 api.MediaTrackAudio,
					ResourceID:           resourceID,
					DiscID:               discID,
					PlaylistID:           playlistID,
					ManifestFingerprint:  string(manifest),
					Ordinal:              index + 1,
					Codec:                normalizeAudioFormat(map[string]any{"Format": audio.Codec}),
					DetectedLanguages:    append([]string(nil), languages...),
					Languages:            languages,
					LanguageProvenance:   api.FactProvenanceAutomatic,
					Commentary:           isBDInfoCommentary(audio),
					BitrateBitsPerSecond: audio.BitrateBitsPerSecond,
					Hidden:               audio.Hidden,
				})
			}
		}
	}
	return tracks, nil
}

func isBDInfoCommentary(track discparse.BDAudio) bool {
	return track.BitrateBitsPerSecond > 0 && track.BitrateBitsPerSecond < 258_000 &&
		languageutil.NormalizeLanguageLabel(track.Language) != ""
}

func mediaTrackPositiveInt(track map[string]any, keys ...string) int {
	for _, key := range keys {
		if value, ok := trackFirstInt(track, key); ok && value > 0 {
			return value
		}
	}
	return 0
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
