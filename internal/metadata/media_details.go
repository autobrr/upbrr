// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/metadata/discparse"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

var repackPattern = regexp.MustCompile(`(?i)\b(?:REPACK\d?|RERIP|PROPER\d?|V[2-4])\b`)
var editionWordPattern = regexp.MustCompile(`(?i)\bedition\b`)
var editionBadTokenPattern = regexp.MustCompile(`(?i)\b(?:internal|limited|retail|version|remastered)\b`)
var editionWhitespacePattern = regexp.MustCompile(`\s+`)
var durationTokenPattern = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(milliseconds?|msecs?|ms|hours?|hrs?|h|minutes?|mins?|min|seconds?|secs?|sec|s)\b`)
var numericPattern = regexp.MustCompile(`\d+`)
var releaseTokenSeparatorPattern = regexp.MustCompile(`[^A-Z0-9]+`)

// ApplyMediaDetails enriches prepared metadata from MediaInfo, BDInfo, filename
// tokens, overrides, and tracker rules, then rebuilds the release name.
func (s *Service) ApplyMediaDetails(ctx context.Context, meta api.PreparedMetadata) (api.PreparedMetadata, error) {
	select {
	case <-ctx.Done():
		return api.PreparedMetadata{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	miDoc, err := loadMediaInfoDoc(meta.MediaInfoJSONPath)
	if err != nil {
		return api.PreparedMetadata{}, err
	}

	meta.MediaInfoUniqueID, meta.ValidMediaInfo = validateMediaInfoUniqueID(meta, miDoc)
	if !meta.ValidMediaInfo && s.logger != nil {
		s.logger.Warnf("metadata: mediainfo validation failed (missing unique id)")
	}
	meta.AudioLanguages, meta.SubtitleLanguages = extractMediaInfoLanguages(miDoc)
	if s.logger != nil && (len(meta.AudioLanguages) > 0 || len(meta.SubtitleLanguages) > 0) {
		s.logger.Debugf("metadata: media languages audio=%v subs=%v", meta.AudioLanguages, meta.SubtitleLanguages)
	}

	bdinfo := loadBDInfo(meta, s.cfg.MainSettings.DBPath)

	meta.Container = containerFromMeta(meta)
	if s.logger != nil {
		s.logger.Debugf("metadata: media details container=%q", meta.Container)
	}

	audio, channels, hasCommentary := audioFromMedia(meta, miDoc, bdinfo)
	meta.Audio = audio
	meta.Channels = channels
	meta.HasCommentary = hasCommentary
	if s.logger != nil {
		s.logger.Debugf("metadata: media details audio=%q channels=%q commentary=%t", meta.Audio, meta.Channels, meta.HasCommentary)
	}

	meta.Is3D = threeDFromBDInfo(bdinfo)
	if s.logger != nil {
		s.logger.Debugf("metadata: media details 3d=%q", meta.Is3D)
	}

	source, typeValue := sourceAndType(meta, miDoc)
	if source != "" {
		meta.Source = source
		meta.Release.Source = source
	}
	if typeValue != "" {
		meta.Type = typeValue
		meta.Release.Type = typeValue
	}
	if strings.EqualFold(meta.DiscType, "DVD") {
		dvdDetails := extractDVDMediaInfo(meta)
		if strings.TrimSpace(dvdDetails.Resolution) != "" && !strings.EqualFold(dvdDetails.Resolution, "OTHER") {
			meta.Release.Resolution = dvdDetails.Resolution
		}
		dvdDetails.SourcePath = meta.SourcePath
		dvdDetails.IFOPath = meta.DVDIFOPath
		dvdDetails.VOBPath = meta.DVDVOBPath
		dvdDetails.VOBSet = meta.DVDVOBSet
		dvdDetails.MediaInfoJSON = meta.MediaInfoJSONPath
		dvdDetails.MediaInfoText = meta.MediaInfoTextPath
		dvdDetails.VOBMediaInfoRaw = metautil.FirstNonEmptyTrimmed(strings.TrimSpace(meta.DVDVOBMediaInfoText), strings.TrimSpace(meta.DVDVOBMediaInfoJSON))
		dvdDetails.UpdatedAt = time.Now().UTC()
		if err := s.repo.SaveDVDMediaInfo(ctx, dvdDetails); err != nil {
			return api.PreparedMetadata{}, fmt.Errorf("metadata: persist dvd mediainfo details: %w", err)
		}
	}
	// For non-DVD content, if rls did not parse a resolution from the filename,
	// fall back to deriving it from the MediaInfo video track dimensions —
	// matching the Python get_resolution() behaviour.
	if strings.TrimSpace(meta.Release.Resolution) == "" && !strings.EqualFold(meta.DiscType, "DVD") {
		if res := resolutionFromMediaInfo(miDoc, meta.SourcePath); res != "" {
			meta.Release.Resolution = res
			if s.logger != nil {
				s.logger.Debugf("metadata: resolution derived from mediainfo %q", res)
			}
		}
	}
	if s.logger != nil {
		s.logger.Debugf("metadata: media details source=%q type=%q resolution=%q", meta.Source, meta.Type, meta.Release.Resolution)
	}

	meta.UHD = uhdFromMeta(meta)
	meta.HDR = hdrFromMedia(miDoc, bdinfo, meta)
	if s.logger != nil {
		s.logger.Debugf("metadata: media details uhd=%q hdr=%q", meta.UHD, meta.HDR)
	}

	meta, err = s.applyBlurayMetadata(ctx, meta, bdinfo)
	if err != nil {
		return api.PreparedMetadata{}, err
	}
	if s.logger != nil && meta.ExternalMetadata.Bluray != nil {
		s.logger.Debugf("metadata: blu-ray.com candidates=%d selected=%q score=%.1f threshold=%.1f", len(meta.ExternalMetadata.Bluray.Candidates), meta.ExternalMetadata.Bluray.SelectedReleaseID, meta.ExternalMetadata.Bluray.BestScore, meta.ExternalMetadata.Bluray.Threshold)
	}

	meta.Distributor = normalizeDistributor(meta.Distributor)
	if s.logger != nil {
		s.logger.Debugf("metadata: media details distributor=%q", meta.Distributor)
	}

	if strings.EqualFold(meta.DiscType, "BDMV") {
		meta.Region = regionFromBDInfo(bdinfo, meta.Region)
		meta.VideoCodec = videoCodecFromBDInfo(bdinfo)
	} else {
		meta.VideoEncode, meta.VideoCodec, meta.HasEncodeSettings, meta.BitDepth = videoEncodeFromMedia(miDoc, meta.Type)
	}
	if s.logger != nil {
		s.logger.Debugf("metadata: media details region=%q video_encode=%q video_codec=%q bit_depth=%q", meta.Region, meta.VideoEncode, meta.VideoCodec, meta.BitDepth)
	}

	meta.Edition, meta.Repack = editionFromMeta(meta, miDoc)
	meta.WebDV = false
	if s.logger != nil {
		s.logger.Debugf("metadata: media details edition=%q repack=%q webdv=%t", meta.Edition, meta.Repack, meta.WebDV)
	}

	meta.ValidMediaInfoSettings = true
	if !strings.EqualFold(meta.DiscType, "BDMV") && strings.EqualFold(meta.Type, "ENCODE") && !strings.EqualFold(meta.VideoCodec, "AV1") {
		meta.ValidMediaInfoSettings = validateMediaInfoSettings(miDoc)
		if !meta.ValidMediaInfoSettings && s.logger != nil {
			s.logger.Warnf("metadata: mediainfo validation failed (missing encode settings)")
		}
	}

	if meta.StreamOptimized != 0 {
		meta.StreamOptimized = 1
	}
	if s.logger != nil {
		s.logger.Debugf("metadata: media details stream_optimized=%d", meta.StreamOptimized)
	}

	service, longName, filename := resolveService(meta)
	if meta.Service == "" && service != "" {
		meta.Service = service
	}
	if meta.ServiceLongName == "" && longName != "" {
		meta.ServiceLongName = longName
	}
	if meta.Filename == "" && filename != "" {
		meta.Filename = filename
	}
	if s.logger != nil {
		s.logger.Debugf("metadata: media details service=%q service_longname=%q", meta.Service, meta.ServiceLongName)
	}

	applyMetadataOverrides(&meta)
	ApplyRequestScopedAudioPolicy(&meta, s.cfg, s.logger)
	RebuildReleaseName(&meta, s.logger)

	// Scene detection runs here — after external IDs are resolved and the release
	// name is rebuilt — so the srrdb imdb: lookup has a resolved IMDb id and full
	// normalized metadata, and so rename/scene fields are populated before tracker
	// rules (which read meta.Scene / meta.SceneNFOPath / meta.SceneRenamed).
	meta, err = s.applySceneDetection(ctx, meta)
	if err != nil {
		return api.PreparedMetadata{}, err
	}
	// Scene detection can supply the service (from the NFO), which WEB release
	// names embed, so rebuild once more to fold in scene-derived metadata.
	RebuildReleaseName(&meta, s.logger)

	meta, err = s.applyTrackerRules(ctx, meta)
	if err != nil {
		return api.PreparedMetadata{}, err
	}

	return meta, nil
}

// applySceneDetection runs srrdb scene/rename detection and copies the result
// onto meta. It is best-effort for transient srrdb/network failures (skip scene,
// never block the upload), but context cancellation/deadline is propagated so the
// pipeline aborts rather than continuing into tracker rules. Detection is gated
// by config (scene_detection) via a nil detector.
func (s *Service) applySceneDetection(ctx context.Context, meta api.PreparedMetadata) (api.PreparedMetadata, error) {
	if s.scene == nil {
		return meta, nil
	}
	result, err := s.scene.Detect(ctx, meta)
	if err != nil {
		switch {
		case isContextError(ctx, err):
			return meta, fmt.Errorf("metadata: scene detect: %w", err)
		case isSceneNFOError(err):
			if s.logger != nil {
				s.logger.Warnf("metadata: scene nfo side effect failed: %s", redaction.RedactValue(err.Error(), nil))
			}
			// The primary match may still be present alongside a recoverable NFO
			// side-effect error; apply it when it carries data.
			if !sceneResultHasData(result) {
				return meta, nil
			}
		default:
			// Transient srrdb/network failure: skip scene, never block the upload.
			if s.logger != nil {
				s.logger.Debugf("metadata: scene detect skipped: %s", redaction.RedactValue(err.Error(), nil))
			}
			return meta, nil
		}
	}
	applySceneResult(&meta, result)
	// Detection now runs after ID resolution, so backfill a scene-discovered IMDb
	// id only when resolution found none, so upload payloads still carry it.
	if meta.ExternalIDs.IMDBID == 0 && result.IMDBID > 0 {
		meta.ExternalIDs.IMDBID = result.IMDBID
	}
	if s.logger != nil {
		if meta.Scene {
			s.logger.Debugf("metadata: scene release detected name=%q imdb=%d renamed=%t", meta.SceneName, meta.SceneIMDB, meta.SceneRenamed)
		}
		if meta.SceneNFOPath != "" {
			if meta.SceneNFONew {
				s.logger.Debugf("metadata: scene nfo downloaded %s", meta.SceneNFOPath)
			} else {
				s.logger.Debugf("metadata: scene nfo found %s", meta.SceneNFOPath)
			}
		}
	}
	return meta, nil
}

// ApplyTrackerClaims refreshes claim-based tracker blocks after media details
// and tracker rules have populated the release attributes used for matching.
func (s *Service) ApplyTrackerClaims(ctx context.Context, meta api.PreparedMetadata) (api.PreparedMetadata, error) {
	return s.applyTrackerClaims(ctx, meta)
}

// RefreshPreparedMetadata reapplies request-scoped audio, naming, rule, and
// claim state without rereading media files. HDR may be refreshed from the
// current source path only when it still matches the parsed release HDR.
func (s *Service) RefreshPreparedMetadata(ctx context.Context, meta api.PreparedMetadata) (api.PreparedMetadata, error) {
	if s == nil {
		return meta, nil
	}
	refreshService := *s

	meta.BlockedTrackers = removeTrackerBlockReason(meta.BlockedTrackers, api.TrackerBlockReasonAudio)
	meta.BlockedTrackers = removeTrackerBlockReason(meta.BlockedTrackers, api.TrackerBlockReasonClaim)
	meta.TrackerRuleFailures = removeTrackerRule(meta.TrackerRuleFailures, trackerClaimRuleActive)

	if filenameHDR := filenameHDRFromMeta(meta); filenameHDR != "" && meta.HDR == normalizeFilenameHDRTokens(meta.Release.HDR) {
		meta.HDR = filenameHDR
	}
	normalizeMediaInfoSettings(&meta)
	ApplyRequestScopedAudioPolicy(&meta, refreshService.cfg, refreshService.logger)
	RebuildReleaseName(&meta, refreshService.logger)

	var err error
	meta, err = refreshService.applyTrackerRules(ctx, meta)
	if err != nil {
		return api.PreparedMetadata{}, err
	}
	meta, err = refreshService.applyTrackerClaims(ctx, meta)
	if err != nil {
		return api.PreparedMetadata{}, err
	}
	return meta, nil
}

// normalizeMediaInfoSettings re-asserts the ValidMediaInfoSettings invariant on
// refresh paths that reuse cached or imported metadata without re-reading media.
// The full media pass (PrepareMetadata) only ever sets this false for a non-BDMV,
// non-AV1 encode that is missing encoding settings; for every other release kind
// it is unconditionally true. Cached or imported metadata can carry a stale or
// zero-value false here, which would wrongly trip the UNIT3D MediaInfo-settings
// rule (see internal/trackers/rules.go) and block valid remux/web/disc uploads,
// so restore true whenever the release cannot be a validatable encode.
func normalizeMediaInfoSettings(meta *api.PreparedMetadata) {
	if meta == nil || meta.ValidMediaInfoSettings {
		return
	}
	if strings.EqualFold(meta.DiscType, "BDMV") ||
		!strings.EqualFold(meta.Type, "ENCODE") ||
		strings.EqualFold(meta.VideoCodec, "AV1") {
		meta.ValidMediaInfoSettings = true
	}
}

func applyMetadataOverrides(meta *api.PreparedMetadata) {
	if meta == nil {
		return
	}

	overrides := meta.MetadataOverrides
	if overrides.Distributor != nil {
		meta.Distributor = normalizeDistributor(*overrides.Distributor)
	}
	applyOriginalLanguageOverride(meta, overrides.OriginalLanguage)
	if overrides.PersonalRelease != nil {
		meta.PersonalRelease = *overrides.PersonalRelease
	}
	if overrides.Commentary != nil {
		meta.HasCommentary = *overrides.Commentary
	}
	if overrides.WebDV != nil {
		meta.WebDV = *overrides.WebDV
	}
	if overrides.StreamOptimized != nil {
		if *overrides.StreamOptimized {
			meta.StreamOptimized = 1
		} else {
			meta.StreamOptimized = 0
		}
	}
	if overrides.Anime != nil {
		meta.Anime = *overrides.Anime
	}
}

func applyOriginalLanguageOverride(meta *api.PreparedMetadata, language *string) {
	if meta == nil || language == nil {
		return
	}

	trimmed := strings.TrimSpace(*language)
	if trimmed == "" {
		return
	}
	if meta.ExternalMetadata.TMDB != nil {
		meta.ExternalMetadata.TMDB.OriginalLanguage = trimmed
	}
	if meta.ExternalMetadata.IMDB != nil {
		meta.ExternalMetadata.IMDB.OriginalLanguage = trimmed
	}
	if meta.ExternalMetadata.TVDB != nil {
		meta.ExternalMetadata.TVDB.OriginalLanguage = trimmed
	}
	if meta.ExternalMetadata.TVmaze != nil {
		meta.ExternalMetadata.TVmaze.Language = trimmed
	}
}

func loadBDInfo(meta api.PreparedMetadata, dbPath string) *discparse.BDInfo {
	if !strings.EqualFold(meta.DiscType, "BDMV") && !strings.EqualFold(meta.DiscType, "DVD") {
		return nil
	}
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return nil
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return nil
	}
	path := paths.BDMVSummaryPath(tmpDir, paths.PrimaryBDMVPlaylist(meta))
	if strings.TrimSpace(path) == "" {
		return nil
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	summary, files, _ := discparse.SplitBDInfoReport(string(payload))
	if strings.TrimSpace(summary) == "" {
		return nil
	}
	return discparse.ParseBDInfoSummary(summary, files, meta.SourcePath)
}

func containerFromMeta(meta api.PreparedMetadata) string {
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "BDMV":
		return "m2ts"
	case "HDDVD":
		return "evo"
	case "DVD":
		return "vob"
	}
	if len(meta.FileList) == 0 {
		ext := strings.TrimPrefix(filepath.Ext(meta.SourcePath), ".")
		return strings.ToLower(ext)
	}
	largest := meta.FileList[0]
	largestSize := int64(-1)
	for _, path := range meta.FileList {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if size := info.Size(); size > largestSize {
			largestSize = size
			largest = path
		}
	}
	ext := strings.TrimPrefix(filepath.Ext(largest), ".")
	return strings.ToLower(ext)
}

// audioFromMedia derives the primary audio label, channel count, and
// commentary presence from BDInfo or MediaInfo. Object-audio markers such as
// Atmos are emitted after the channel count to match release-name ordering.
// MediaInfo title-derived markers such as Auro3D come from the selected
// primary audio track.
func audioFromMedia(meta api.PreparedMetadata, doc mediaInfoDoc, bdinfo *discparse.BDInfo) (string, string, bool) {
	if bdinfo != nil && len(bdinfo.Audio) > 0 {
		track := bdinfo.Audio[0]
		codec := normalizeAudioFormat(map[string]any{
			"Format_Commercial": track.Codec,
			"Format":            track.Codec,
		})
		codec = strings.TrimSpace(codec)
		channels := strings.TrimSpace(track.Channels)
		if channels == "" {
			channels = "Unknown"
		}
		extra := ""
		if track.Atmos != "" && !strings.Contains(strings.ToLower(codec), "atmos") {
			extra = "Atmos"
		}
		return strings.Join(strings.Fields(strings.Join([]string{codec, channels, extra}, " ")), " "), channels, false
	}

	_, _, audioTracks := splitMediaInfoTracks(doc)
	if len(audioTracks) == 0 {
		return "", "", false
	}
	track := selectPrimaryAudioTrack(audioTracks)
	primaryAudioTitle := strings.ToLower(audioTrackTitle(track))
	format := normalizeAudioFormat(track)
	additional := trackString(track, "Format_AdditionalFeatures", "Format_AdditionalFeatures_String", "Format_AdditionalFeatures_Original")
	formatSettings := normalizeAudioFormatSettings(trackString(track, "Format_Settings"))
	format = applyDTSAudioAdditional(format, additional)
	channels := determineChannelCount(
		trackString(track, "Channels_Original", "Channels", "Channel_s_", "Channel_s__Original"),
		trackString(track, "ChannelLayout", "ChannelLayout_Original", "ChannelPositions", "ChannelPositions_Original"),
		additional,
		trackString(track, "Format", "Format_String"),
	)
	if channels == "" {
		channels = "Unknown"
	}

	formatLower := strings.ToLower(format)
	extra := ""
	if isAtmosAudio(additional, formatLower, trackString(track, "ChannelLayout", "ChannelPositions")) && !strings.Contains(formatLower, "atmos") {
		extra = "Atmos"
	}
	if isDTSXAudio(additional, formatLower) && !strings.Contains(strings.ToLower(format), "dts:x") {
		if strings.EqualFold(format, "DTS") {
			format = "DTS:X"
		} else {
			format = strings.TrimSpace(format + " DTS:X")
		}
	}
	if strings.EqualFold(format, "DD") && channels == "7.1" {
		format = "DD+"
	}
	if formatSettings == "EX" && channels != "5.1" {
		formatSettings = ""
	}
	if extra == "" && strings.Contains(primaryAudioTitle, "auro3d") {
		extra = "Auro3D"
	}
	commentary := false
	for _, audioTrack := range audioTracks {
		title := strings.ToLower(audioTrackTitle(audioTrack))
		if strings.Contains(title, "commentary") {
			commentary = true
			break
		}
	}
	prefix := ""
	if strings.TrimSpace(meta.DiscType) == "" {
		prefix = audioLanguagePrefix(meta, audioTracks)
	}
	return strings.Join(strings.Fields(strings.Join([]string{prefix, format, formatSettings, channels, extra}, " ")), " "), channels, commentary
}

func normalizeAudioFormatSettings(value string) string {
	trimmed := strings.TrimSpace(value)
	if strings.EqualFold(trimmed, "Explicit") {
		return ""
	}
	if strings.EqualFold(trimmed, "Dolby Surround EX") {
		return "EX"
	}
	return ""
}

// audioTrackTitle returns the first MediaInfo title field used to identify
// commentary or compatibility tracks before audio language classification.
func audioTrackTitle(track map[string]any) string {
	return trackString(track, "Title", "title", "Title_String", "Title_String2", "Title_String3")
}

func audioLanguagePrefix(meta api.PreparedMetadata, tracks []map[string]any) string {
	filtered := make([]map[string]any, 0, len(tracks))
	for _, track := range tracks {
		if isCommentaryOrCompatibilityAudioValue(audioTrackTitle(track)) {
			continue
		}
		filtered = append(filtered, track)
	}
	if len(filtered) == 0 {
		return ""
	}

	languages := make([]string, 0, len(filtered))
	for _, track := range filtered {
		languages = append(languages, trackString(track, "Language", "Language_String", "Language_String2", "Language_String3"))
	}
	return audioLanguagePrefixFromLanguages(meta, languages)
}

func audioLanguagePrefixFromLanguages(meta api.PreparedMetadata, languages []string) string {
	if len(languages) == 0 {
		return ""
	}

	original := canonicalAudioLanguage(originalAudioLanguage(meta))
	if original == "" || original == "unknown" {
		return ""
	}

	hasEnglish := false
	hasOriginal := false
	hasOther := false
	for _, value := range languages {
		language := canonicalAudioLanguage(value)
		if language == "" || language == "unknown" {
			continue
		}
		if language == "english" {
			hasEnglish = true
		}
		if language == original {
			hasOriginal = true
		}
		if language != "english" && language != original {
			hasOther = true
		}
	}

	if len(languages) > 1 && ((hasEnglish && (hasOriginal || hasOther)) || (hasOriginal && hasOther)) {
		return "Dual-Audio"
	}
	if hasEnglish && !hasOriginal && original != "english" {
		return "Dubbed"
	}
	return ""
}

func originalAudioLanguage(meta api.PreparedMetadata) string {
	switch {
	case meta.ExternalMetadata.TMDB != nil && strings.TrimSpace(meta.ExternalMetadata.TMDB.OriginalLanguage) != "":
		return meta.ExternalMetadata.TMDB.OriginalLanguage
	case meta.ExternalMetadata.IMDB != nil && strings.TrimSpace(meta.ExternalMetadata.IMDB.OriginalLanguage) != "":
		return meta.ExternalMetadata.IMDB.OriginalLanguage
	case meta.ExternalMetadata.TVDB != nil && strings.TrimSpace(meta.ExternalMetadata.TVDB.OriginalLanguage) != "":
		return meta.ExternalMetadata.TVDB.OriginalLanguage
	case meta.ExternalMetadata.TVmaze != nil && strings.TrimSpace(meta.ExternalMetadata.TVmaze.Language) != "":
		return meta.ExternalMetadata.TVmaze.Language
	default:
		return ""
	}
}

func canonicalAudioLanguage(value string) string {
	token := strings.ToLower(strings.TrimSpace(value))
	if token == "" {
		return ""
	}
	for _, separator := range []string{"-", "_", ",", " "} {
		if index := strings.Index(token, separator); index >= 0 {
			token = token[:index]
			break
		}
	}
	switch token {
	case "en", "eng", "english":
		return "english"
	case "zh", "zho", "chi", "cn", "cmn", "chinese", "mandarin":
		return "chinese"
	case "no", "nor", "nb", "nob", "norwegian":
		return "norwegian"
	case "zxx", "xx", "und":
		return "unknown"
	}
	if normalized := strings.ToLower(strings.TrimSpace(languageutil.NormalizeLanguageDisplay(value))); normalized != "" {
		return normalized
	}
	return token
}

func isCommentaryOrCompatibilityAudioValue(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.Contains(lower, "commentary") || strings.Contains(lower, "compatibility")
}

// ApplyRequestScopedAudioPolicy updates audio labels and tracker audio blocks
// for the request's active tracker set.
func ApplyRequestScopedAudioPolicy(meta *api.PreparedMetadata, cfg config.Config, logger api.Logger) {
	if meta == nil {
		return
	}

	meta.Audio = applyAudioLanguagePrefix(meta.Audio, *meta)
	meta.BlockedTrackers = removeTrackerBlockReason(meta.BlockedTrackers, api.TrackerBlockReasonAudio)
	meta.TrackerRuleFailures = removeTrackerRule(meta.TrackerRuleFailures, "audio_bloat")
	applyAudioBloatPolicy(meta, trackers.ResolveTrackersWithDefaults(cfg, meta.Trackers, meta.TrackersRemove, logger), logger)
}

// RebuildReleaseName regenerates the prepared release-name fields from the
// current metadata and release-name overrides.
func RebuildReleaseName(meta *api.PreparedMetadata, logger api.Logger) {
	if meta == nil {
		return
	}

	applyReleaseNameValueOverrides(meta)

	nameRequest := releaseNameRequestFromMeta(*meta, logger)
	nameRequest = applyReleaseNameOverrides(nameRequest, meta.ReleaseNameOverrides, logger)
	nameResult := BuildReleaseName(nameRequest, logger)
	meta.ReleaseNameNoTag = nameResult.NameNoTag
	meta.ReleaseName = nameResult.Name
	meta.ReleaseNameClean = nameResult.CleanName
	meta.ReleaseNameMissing = append([]string{}, nameResult.MissingFields...)
	if logger != nil && nameResult.Name != "" {
		logger.Tracef("metadata: release name resolved %q", nameResult.Name)
	}
}

func applyAudioLanguagePrefix(audio string, meta api.PreparedMetadata) string {
	base := strings.TrimSpace(audio)
	for _, prefix := range []string{"Dual-Audio", "Dubbed"} {
		if strings.EqualFold(base, prefix) {
			base = ""
			break
		}
		if after, ok := strings.CutPrefix(base, prefix+" "); ok {
			base = strings.TrimSpace(after)
			break
		}
	}

	if strings.TrimSpace(meta.DiscType) != "" {
		return base
	}

	filteredLanguages := make([]string, 0, len(meta.AudioLanguages))
	for _, language := range meta.AudioLanguages {
		if isCommentaryOrCompatibilityAudioValue(language) {
			continue
		}
		filteredLanguages = append(filteredLanguages, language)
	}

	prefix := audioLanguagePrefixFromLanguages(meta, filteredLanguages)
	if prefix == "" || base == "" {
		return base
	}
	return strings.TrimSpace(prefix + " " + base)
}

func applyAudioBloatPolicy(meta *api.PreparedMetadata, candidateTrackers []string, logger api.Logger) {
	if meta == nil || strings.TrimSpace(meta.DiscType) != "" {
		return
	}

	blocked, warned := resolveAudioBloatPolicy(*meta, candidateTrackers)
	if len(blocked) == 0 && len(warned) == 0 {
		return
	}

	for tracker, languages := range blocked {
		meta.BlockedTrackers = addMetadataTrackerBlockReason(meta.BlockedTrackers, tracker, api.TrackerBlockReasonAudio)
		meta.TrackerRuleFailures = addMetadataTrackerRuleFailure(meta.TrackerRuleFailures, tracker, api.RuleFailure{
			Rule:   "audio_bloat",
			Reason: audioBloatReason(languages, true),
		})
		if logger != nil {
			logger.Warnf("metadata: removed tracker %s due to audio bloat languages=%v", tracker, languages)
		}
	}
	for tracker, languages := range warned {
		if logger != nil {
			logger.Warnf("metadata: audio may be considered bloated on %s languages=%v", tracker, languages)
		}
	}
}

func resolveAudioBloatPolicy(meta api.PreparedMetadata, candidateTrackers []string) (map[string][]string, map[string][]string) {
	original := canonicalAudioLanguage(originalAudioLanguage(meta))
	if original == "" || original == "unknown" {
		return nil, nil
	}

	languages := make([]string, 0, len(meta.AudioLanguages))
	seenLanguages := make(map[string]struct{}, len(meta.AudioLanguages))
	hasEnglish := false
	hasOther := false
	for _, value := range meta.AudioLanguages {
		canonical := canonicalAudioLanguage(value)
		if canonical == "" || canonical == "unknown" {
			continue
		}
		if _, ok := seenLanguages[canonical]; ok {
			continue
		}
		seenLanguages[canonical] = struct{}{}
		languages = append(languages, canonical)
		if canonical == "english" {
			hasEnglish = true
		}
		if canonical != "english" && canonical != original {
			hasOther = true
		}
	}
	if len(languages) == 0 || !hasOther {
		return nil, nil
	}

	resolvedTrackers := uniqueUpperTrackers(candidateTrackers)
	if len(resolvedTrackers) == 0 {
		return nil, nil
	}

	bloatAllowed := map[string]struct{}{
		"ASC": {}, "BJS": {}, "BT": {}, "DC": {}, "FF": {}, "TL": {},
	}
	trackerAllowedLanguages := map[string][]string{
		"AITHER": {"english"},
		"ANT":    {"english"},
		"SPD":    {"romanian"},
		"UTP":    {"ukrainian", "english"},
	}
	hardBlockedForEnglishOriginal := map[string]struct{}{
		"ANT": {}, "BHD": {}, "MTV": {},
	}
	isEnglishOriginalWithNonEnglish := original == "english" && hasEnglish && hasOther

	blocked := make(map[string][]string)
	warned := make(map[string][]string)
	for _, language := range languages {
		if language == "english" || language == original {
			continue
		}
		for _, tracker := range resolvedTrackers {
			if _, ok := bloatAllowed[tracker]; ok {
				continue
			}
			if allowed, ok := trackerAllowedLanguages[tracker]; ok && containsCanonicalLanguage(allowed, language) {
				continue
			}
			if isEnglishOriginalWithNonEnglish {
				if _, ok := hardBlockedForEnglishOriginal[tracker]; ok {
					blocked[tracker] = appendUniqueString(blocked[tracker], languageutil.NormalizeLanguageDisplay(language))
					continue
				}
			}
			warned[tracker] = appendUniqueString(warned[tracker], languageutil.NormalizeLanguageDisplay(language))
		}
	}
	if len(blocked) == 0 {
		blocked = nil
	}
	if len(warned) == 0 {
		warned = nil
	}
	return blocked, warned
}

func appendUniqueString(values []string, value string) []string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return values
	}
	for _, existing := range values {
		if strings.EqualFold(strings.TrimSpace(existing), trimmed) {
			return values
		}
	}
	return append(values, trimmed)
}

func removeTrackerBlockReason(blocked map[string][]api.TrackerBlockReason, reason api.TrackerBlockReason) map[string][]api.TrackerBlockReason {
	if len(blocked) == 0 {
		return blocked
	}

	filtered := make(map[string][]api.TrackerBlockReason, len(blocked))
	for tracker, reasons := range blocked {
		kept := make([]api.TrackerBlockReason, 0, len(reasons))
		for _, existing := range reasons {
			if existing == reason {
				continue
			}
			kept = append(kept, existing)
		}
		if len(kept) == 0 {
			continue
		}
		filtered[tracker] = append([]api.TrackerBlockReason{}, kept...)
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func removeTrackerRule(failures map[string][]api.RuleFailure, rule string) map[string][]api.RuleFailure {
	if len(failures) == 0 {
		return failures
	}

	filtered := make(map[string][]api.RuleFailure, len(failures))
	for tracker, trackerFailures := range failures {
		kept := make([]api.RuleFailure, 0, len(trackerFailures))
		for _, failure := range trackerFailures {
			if strings.EqualFold(strings.TrimSpace(failure.Rule), strings.TrimSpace(rule)) {
				continue
			}
			kept = append(kept, failure)
		}
		if len(kept) == 0 {
			continue
		}
		filtered[tracker] = kept
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func containsCanonicalLanguage(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}

func audioBloatReason(languages []string, hardBlocked bool) string {
	parts := strings.Join(languages, ", ")
	if hardBlocked {
		return fmt.Sprintf("audio languages %s are not allowed for this tracker on bloated releases", parts)
	}
	return fmt.Sprintf("audio languages %s may be considered bloated", parts)
}

// selectPrimaryAudioTrack returns the track used to derive the release audio
// label, excluding commentary and compatibility tracks when possible. This
// intentionally diverges from the Python reference, which selects from all
// tracks, to avoid fallback tracks representing the release.
func selectPrimaryAudioTrack(tracks []map[string]any) map[string]any {
	if len(tracks) == 0 {
		return nil
	}
	filtered := filterPrimaryAudioTracks(tracks)
	if len(filtered) == 0 {
		filtered = tracks
	}
	if selected, ok := lowestTrackByNumericField(filtered, "StreamOrder"); ok {
		return selected
	}
	if selected, ok := lowestTrackByNumericField(filtered, "ID"); ok {
		return selected
	}
	return filtered[0]
}

// filterPrimaryAudioTracks returns tracks eligible to represent primary release
// audio by ignoring commentary and compatibility markers in any MediaInfo title
// variant. It leaves all-track fallback decisions to the caller.
func filterPrimaryAudioTracks(tracks []map[string]any) []map[string]any {
	filtered := make([]map[string]any, 0, len(tracks))
	for _, track := range tracks {
		if isCommentaryOrCompatibilityAudioValue(audioTrackTitle(track)) {
			continue
		}
		filtered = append(filtered, track)
	}
	return filtered
}

func lowestTrackByNumericField(tracks []map[string]any, key string) (map[string]any, bool) {
	var selected map[string]any
	selectedValue := 0
	found := false
	for _, track := range tracks {
		value, ok := trackFirstInt(track, key)
		if !ok {
			continue
		}
		if !found || value < selectedValue {
			selected = track
			selectedValue = value
			found = true
		}
	}
	if !found {
		return nil, false
	}
	return selected, true
}

func trackFirstInt(track map[string]any, key string) (int, bool) {
	raw, ok := track[key]
	if !ok || raw == nil {
		return 0, false
	}
	value := strings.TrimSpace(fmtSprint(raw))
	if value == "" {
		return 0, false
	}
	matched := numericPattern.FindString(value)
	if matched == "" {
		return 0, false
	}
	parsed, err := strconv.Atoi(matched)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func normalizeAudioFormat(track map[string]any) string {
	commercial := trackString(track, "Format_Commercial", "Format_Commercial_IfAny")
	format := trackString(track, "Format", "Format_String")
	formatProfile := trackString(track, "Format_Profile", "Format_Profile_String")
	if commercial != "" {
		lowerCommercial := strings.ToLower(commercial)
		switch {
		case strings.Contains(lowerCommercial, "dts-hd master audio"):
			return "DTS-HD MA"
		case strings.Contains(lowerCommercial, "dts-hd high"):
			return "DTS-HD HRA"
		case strings.Contains(lowerCommercial, "dts-es"):
			return "DTS-ES"
		case strings.Contains(lowerCommercial, "dolby digital plus"):
			return "DD+"
		case strings.Contains(lowerCommercial, "dolby truehd"):
			return "TrueHD"
		case strings.Contains(lowerCommercial, "dolby digital"):
			return "DD"
		case strings.Contains(lowerCommercial, "free lossless audio codec"):
			return "FLAC"
		}
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "dts":
		return "DTS"
	case "aac", "aac lc":
		return "AAC"
	case "ac-3":
		return "DD"
	case "e-ac-3", "a_eac3", "enhanced ac-3":
		return "DD+"
	case "mlp fba":
		return "TrueHD"
	case "flac":
		return "FLAC"
	case "opus":
		return "Opus"
	case "vorbis":
		return "VORBIS"
	case "pcm", "lpcm audio":
		return "LPCM"
	case "dolby digital audio":
		return "DD"
	case "dolby digital plus audio", "dolby digital plus":
		return "DD+"
	case "dolby truehd audio":
		return "TrueHD"
	case "dts audio":
		return "DTS"
	case "dts-hd master audio":
		return "DTS-HD MA"
	case "dts-hd high-res audio":
		return "DTS-HD HRA"
	case "dts:x master audio":
		return "DTS:X"
	case "mpeg audio":
		switch {
		case strings.Contains(strings.ToLower(formatProfile), "layer 2"):
			return "MP2"
		case strings.Contains(strings.ToLower(formatProfile), "layer 3"):
			return "MP3"
		}
	}
	value := metautil.FirstNonEmptyTrimmed(commercial, format)
	lower := strings.ToLower(value)

	switch {
	case strings.Contains(lower, "dts:x"):
		return "DTS:X"
	case strings.Contains(lower, "dolby digital plus"):
		return "DD+"
	case strings.Contains(lower, "dolby digital"):
		return "DD"
	case strings.Contains(lower, "mlp fba"):
		return "TrueHD"
	case strings.Contains(lower, "truehd"):
		return "TrueHD"
	case strings.Contains(lower, "dts-hd master"):
		return "DTS-HD MA"
	case strings.Contains(lower, "dts-hd high"):
		return "DTS-HD HRA"
	case strings.Contains(lower, "dts-es"):
		return "DTS-ES"
	case strings.Contains(lower, "dts"):
		return "DTS"
	case strings.Contains(lower, "aac"):
		return "AAC"
	case strings.Contains(lower, "flac"):
		return "FLAC"
	case strings.Contains(lower, "opus"):
		return "Opus"
	case strings.Contains(lower, "vorbis"):
		return "VORBIS"
	case strings.Contains(lower, "lpcm"), strings.Contains(lower, "pcm"):
		return "LPCM"
	case strings.Contains(lower, "mp3"):
		return "MP3"
	}
	if value == "" || strings.EqualFold(value, "audio") {
		codecID := trackString(track, "CodecID", "CodecID_Compatible")
		lower = strings.ToLower(codecID)
		switch {
		case strings.Contains(lower, "eac3"), strings.Contains(lower, "e-ac-3"):
			return "DD+"
		case strings.Contains(lower, "ac3"), strings.Contains(lower, "ac-3"):
			return "DD"
		case strings.Contains(lower, "truehd"):
			return "TrueHD"
		case strings.Contains(lower, "dts"):
			return "DTS"
		case strings.Contains(lower, "aac"):
			return "AAC"
		case strings.Contains(lower, "flac"):
			return "FLAC"
		case strings.Contains(lower, "opus"):
			return "Opus"
		case strings.Contains(lower, "vorbis"):
			return "VORBIS"
		case strings.Contains(lower, "mp3"), strings.Contains(lower, "mpeg/l3"):
			return "MP3"
		case strings.Contains(lower, "mp2"), strings.Contains(lower, "mpeg/l2"):
			return "MP2"
		case strings.Contains(lower, "lpcm"), strings.Contains(lower, "pcm"):
			return "LPCM"
		}
	}
	return value
}

func determineChannelCount(channelsValue, channelLayout, additional, formatValue string) string {
	s := strings.TrimSpace(channelsValue)
	if s == "" {
		return ""
	}
	channelLayout = strings.TrimSpace(channelLayout)

	channels := firstNumericToken(s)
	if channels == 0 {
		return ""
	}

	if isAtmosAudio(additional, strings.ToLower(formatValue), channelLayout) && channelLayout != "" {
		bed, lfe, height := parseAtmosLayout(channelLayout)
		if height > 0 {
			if lfe > 0 {
				return fmt.Sprintf("%d.%d.%d", bed, lfe, height)
			}
			return fmt.Sprintf("%d.0.%d", bed, height)
		}
		return parseChannelLayout(channels, channelLayout)
	}
	if channelLayout != "" {
		return parseChannelLayout(channels, channelLayout)
	}
	return fallbackChannelCount(channels)
}

func firstNumericToken(value string) int {
	for field := range strings.FieldsSeq(value) {
		if num := parseLeadingInt(field); num > 0 {
			return num
		}
	}
	return parseLeadingInt(value)
}

func parseLeadingInt(value string) int {
	digits := strings.Builder{}
	for _, r := range value {
		if r < '0' || r > '9' {
			break
		}
		digits.WriteRune(r)
	}
	if digits.Len() == 0 {
		return 0
	}
	var parsed int
	_, _ = fmt.Sscanf(digits.String(), "%d", &parsed)
	return parsed
}

func isAtmosAudio(additional, formatValue, channelLayout string) bool {
	lowerAdditional := strings.ToLower(additional)
	lowerFormat := strings.ToLower(formatValue)
	if strings.Contains(lowerAdditional, "joc") || strings.Contains(lowerAdditional, "atmos") || strings.Contains(lowerAdditional, "16-ch") {
		return true
	}
	if strings.Contains(lowerFormat, "atmos") || strings.Contains(lowerFormat, "16-ch") {
		return true
	}
	layoutLower := strings.ToLower(channelLayout)
	return strings.Contains(layoutLower, "top") || strings.Contains(layoutLower, "height") || strings.Contains(layoutLower, "tfl")
}

func isDTSXAudio(additional, formatValue string) bool {
	lowerAdditional := strings.ToLower(additional)
	lowerFormat := strings.ToLower(formatValue)
	if strings.Contains(lowerAdditional, "dts:x") || strings.Contains(lowerAdditional, "xll x") {
		return true
	}
	if strings.Contains(lowerFormat, "dts:x") {
		return true
	}
	return strings.Contains(lowerFormat, "dts") && strings.HasSuffix(strings.TrimSpace(lowerAdditional), "x")
}

func applyDTSAudioAdditional(codec, additional string) string {
	upperCodec := strings.ToUpper(strings.TrimSpace(codec))
	upperAdditional := strings.ToUpper(strings.TrimSpace(additional))
	if !strings.HasPrefix(upperCodec, "DTS") {
		return codec
	}
	switch {
	case strings.Contains(upperAdditional, "XLL X"):
		return "DTS:X"
	case strings.Contains(upperAdditional, "XLL") && strings.EqualFold(codec, "DTS"):
		return "DTS-HD MA"
	case upperAdditional == "ES" && strings.EqualFold(codec, "DTS"):
		return "DTS-ES"
	default:
		return codec
	}
}

func parseAtmosLayout(layout string) (bed int, lfe int, height int) {
	upper := strings.ToUpper(layout)
	parts := strings.FieldsSeq(upper)
	for part := range parts {
		if strings.Contains(part, "LFE") {
			lfe++
			continue
		}
		switch {
		case strings.Contains(part, "TFC"), strings.Contains(part, "TFL"), strings.Contains(part, "TFR"),
			strings.Contains(part, "TBL"), strings.Contains(part, "TBR"), strings.Contains(part, "TBC"),
			strings.Contains(part, "VHC"), strings.Contains(part, "VHL"), strings.Contains(part, "VHR"),
			strings.Contains(part, "CH"), strings.Contains(part, "LH"), strings.Contains(part, "RH"),
			strings.Contains(part, "CHR"), strings.Contains(part, "LHR"), strings.Contains(part, "RHR"),
			strings.Contains(part, "TSL"), strings.Contains(part, "TSR"), strings.Contains(part, "TLS"), strings.Contains(part, "TRS"):
			height++
		default:
			bed++
		}
	}
	return bed, lfe, height
}

func parseChannelLayout(channels int, layout string) string {
	upper := strings.ToUpper(layout)
	lfe := strings.Count(upper, "LFE")
	if lfe == 0 && strings.Contains(upper, "LFE") {
		lfe = 1
	}
	if lfe > 1 {
		return fmt.Sprintf("%d.%d", channels-lfe, lfe)
	}
	if lfe == 1 {
		return fmt.Sprintf("%d.1", channels-1)
	}
	if strings.Contains(strings.ToLower(layout), "object") && channels > 7 {
		return fmt.Sprintf("%d.1", channels-1)
	}
	if channels <= 2 {
		return fmt.Sprintf("%d.0", channels)
	}
	if strings.Contains(upper, "MONO") || channels == 1 {
		return "1.0"
	}
	if channels == 2 {
		return "2.0"
	}
	return fmt.Sprintf("%d.0", channels)
}

func fallbackChannelCount(channels int) string {
	switch channels {
	case 1:
		return "1.0"
	case 2:
		return "2.0"
	case 3:
		return "2.1"
	case 4:
		return "3.1"
	case 5:
		return "4.1"
	case 6:
		return "5.1"
	case 7:
		return "6.1"
	case 8:
		return "7.1"
	default:
		return fmt.Sprintf("%d.1", channels-1)
	}
}

func threeDFromBDInfo(info *discparse.BDInfo) string {
	if info == nil || len(info.Video) == 0 {
		return ""
	}
	if strings.TrimSpace(info.Video[0].ThreeD) != "" {
		return "3D"
	}
	return ""
}

func sourceAndType(meta api.PreparedMetadata, doc mediaInfoDoc) (string, string) {
	source := strings.TrimSpace(meta.Release.Source)
	typeValue := strings.TrimSpace(meta.Release.Type)
	if typeValue == "" || isCategoryType(typeValue) {
		if inferred := inferReleaseTypeFromSource(source); inferred != "" {
			typeValue = inferred
		}
	}
	if typeValue == "" || isCategoryType(typeValue) {
		if inferred := inferReleaseTypeFromName(meta.SourcePath); inferred != "" {
			typeValue = inferred
		}
	}
	if typeValue == "" && (strings.EqualFold(meta.DiscType, "BDMV") || strings.EqualFold(meta.DiscType, "DVD") || strings.EqualFold(meta.DiscType, "HDDVD")) {
		typeValue = "DISC"
	}
	if source == "" || !isKnownReleaseSource(source) {
		if inferred := inferReleaseSourceFromName(meta.SourcePath, typeValue); inferred != "" {
			source = inferred
		}
	}
	if strings.EqualFold(meta.DiscType, "BDMV") {
		switch typeValue {
		case "DISC":
			source = "Blu-ray"
		case "ENCODE", "REMUX":
			source = "BluRay"
		}
	}
	if strings.EqualFold(meta.DiscType, "DVD") {
		system := dvdSystemFromMedia(doc)
		if typeValue == "REMUX" && system != "" {
			system = strings.TrimSpace(system + " DVD")
		}
		if system != "" {
			source = system
		}
	}
	if strings.EqualFold(meta.DiscType, "HDDVD") {
		source = "HD DVD"
		if typeValue == "ENCODE" || typeValue == "REMUX" {
			source = "HDDVD"
		}
	}
	switch normalizeReleaseType(typeValue) {
	case "WEBDL":
		typeValue = "WEBDL"
		source = "Web"
	case "WEBRIP":
		typeValue = "WEBRIP"
		source = "Web"
	}
	if webType := webReleaseTypeFromSignals(typeValue, source, meta.SourcePath); webType != "" {
		typeValue = webType
		source = "Web"
	}
	if strings.EqualFold(source, "Ultra HDTV") {
		source = "UHDTV"
	}
	// Python get_type() falls back to "ENCODE" for any release that does not
	// match a known keyword and is not a disc. Apply the same default here.
	if typeValue == "" && !strings.EqualFold(meta.DiscType, "BDMV") && !strings.EqualFold(meta.DiscType, "DVD") && !strings.EqualFold(meta.DiscType, "HDDVD") {
		typeValue = "ENCODE"
	}
	return source, typeValue
}

func dvdSystemFromMedia(doc mediaInfoDoc) string {
	generalTracks, videoTracks, _ := splitMediaInfoTracks(doc)
	for _, track := range append(generalTracks, videoTracks...) {
		standard := strings.ToUpper(trackString(track, "Standard"))
		if standard == "PAL" || standard == "NTSC" {
			return standard
		}
		frameRate := trackString(track, "FrameRate", "FrameRate_Original", "FrameRate_Num")
		if strings.Contains(frameRate, "25") || strings.Contains(frameRate, "50") {
			return "PAL"
		}
		if frameRate != "" {
			return "NTSC"
		}
	}
	return ""
}

func uhdFromMeta(meta api.PreparedMetadata) string {
	upperPath := strings.ToUpper(meta.SourcePath)
	if strings.Contains(upperPath, "UHD") {
		return "UHD"
	}
	for _, value := range meta.Release.Other {
		if strings.EqualFold(strings.TrimSpace(value), "Ultra HD") {
			return "UHD"
		}
	}
	if meta.Type == "DISC" || meta.Type == "REMUX" || meta.Type == "ENCODE" && !isWebSourceValue(meta.Source) && !isWebSourceValue(meta.Release.Source) {
		if strings.EqualFold(meta.Release.Resolution, "2160p") {
			return "UHD"
		}
	}
	if strings.Contains(strings.ToLower(meta.Release.Source), "ultra") {
		return "UHD"
	}
	return ""
}

// hdrFromMedia prefers BDInfo and MediaInfo HDR evidence, including Dolby
// Vision HDR10/HDR10+ compatibility markers, then falls back to normalized
// filename tokens from the current source path or parsed release. Bare PQ
// transfer metadata is normalized to HDR only when BT.2020 primaries confirm
// the HDR color space.
func hdrFromMedia(doc mediaInfoDoc, bdinfo *discparse.BDInfo, meta api.PreparedMetadata) string {
	if bdinfo != nil && len(bdinfo.Video) > 0 {
		hdr := ""
		dv := ""
		hdrMi := bdinfo.Video[0].HDRDV
		switch {
		case strings.Contains(hdrMi, "HDR10+"):
			hdr = "HDR10+"
		case hdrMi == "HDR10":
			hdr = "HDR"
		}
		if len(bdinfo.Video) > 1 && bdinfo.Video[1].HDRDV == "Dolby Vision" {
			dv = "DV"
		}
		if mediaHDR := strings.TrimSpace(strings.Join([]string{dv, hdr}, " ")); mediaHDR != "" {
			return mediaHDR
		}
		return filenameHDRFromMeta(meta)
	}

	_, videoTracks, _ := splitMediaInfoTracks(doc)
	if len(videoTracks) == 0 {
		return filenameHDRFromMeta(meta)
	}
	track := videoTracks[0]
	primaries := trackString(track, "colour_primaries", "colour_primaries_Original")
	primariesUpper := strings.ToUpper(primaries)
	hdr := ""
	dv := ""
	formatEvidence := strings.Join([]string{
		trackString(track, "HDR_Format_Compatibility"),
		trackString(track, "HDR_Format_String"),
		trackString(track, "HDR_Format"),
	}, " ")
	upperFormat := strings.ToUpper(formatEvidence)
	switch {
	case strings.Contains(upperFormat, "HDR10+"):
		hdr = "HDR10+"
	case strings.Contains(upperFormat, "HDR10") || strings.Contains(upperFormat, "SMPTE ST 2094"):
		hdr = "HDR"
	}
	if strings.Contains(upperFormat, "HLG") {
		hdr = strings.TrimSpace(hdr + " HLG")
	}
	transfer := trackString(track, "transfer_characteristics", "transfer_characteristics_Original")
	transferUpper := strings.ToUpper(transfer)
	if (primariesUpper == "BT.2020" || primariesUpper == "REC.2020") && strings.TrimSpace(formatEvidence) == "" && strings.Contains(transferUpper, "PQ") {
		hdr = "HDR"
	}
	if strings.Contains(transferUpper, "HLG") {
		hdr = "HLG"
	}
	if hdr != "HLG" && strings.Contains(transferUpper, "BT.2020 (10-BIT)") {
		hdr = "WCG"
	}
	if strings.Contains(trackString(track, "HDR_Format"), "Dolby Vision") || strings.Contains(trackString(track, "HDR_Format_String"), "Dolby Vision") {
		dv = "DV"
	}
	if mediaHDR := strings.TrimSpace(strings.Join([]string{dv, hdr}, " ")); mediaHDR != "" {
		return mediaHDR
	}
	return filenameHDRFromMeta(meta)
}

// filenameHDRFromMeta returns normalized HDR tokens from SourcePath before
// falling back to previously parsed release tokens.
func filenameHDRFromMeta(meta api.PreparedMetadata) string {
	if sourceHDR := normalizeFilenameHDRTokens(ParseReleaseInfo(meta.SourcePath).HDR); sourceHDR != "" {
		return sourceHDR
	}
	return normalizeFilenameHDRTokens(meta.Release.HDR)
}

// normalizeFilenameHDRTokens keeps recognized filename HDR tokens in release
// name order and ignores ambiguous labels such as SDR.
func normalizeFilenameHDRTokens(tokens []string) string {
	dv := ""
	hdr := ""
	hlg := ""
	for _, token := range tokens {
		normalized := strings.ToUpper(strings.TrimSpace(token))
		switch normalized {
		case "DV", "DOVI", "DOLBY VISION":
			dv = "DV"
		case "HDR10+", "HDR+", "HDR10PLUS":
			hdr = "HDR10+"
		case "HDR", "HDR10":
			if hdr == "" {
				hdr = "HDR"
			}
		case "HLG":
			hlg = "HLG"
		}
	}
	return strings.TrimSpace(strings.Join([]string{dv, hdr, hlg}, " "))
}

func normalizeDistributor(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}
	return trimmed
}

func regionFromBDInfo(info *discparse.BDInfo, existing string) string {
	if strings.TrimSpace(existing) != "" {
		return strings.ToUpper(strings.TrimSpace(existing))
	}
	if info == nil {
		return ""
	}
	label := strings.ToUpper(strings.ReplaceAll(info.Label, ".", " "))
	if label == "" {
		label = strings.ToUpper(strings.ReplaceAll(info.Title, ".", " "))
	}
	if label == "" {
		label = strings.ToUpper(strings.ReplaceAll(info.Path, ".", " "))
	}
	return detectRegionCode(label)
}

func detectRegionCode(label string) string {
	fields := strings.FieldsSeq(label)
	for field := range fields {
		code := strings.TrimSpace(field)
		if len(code) == 3 {
			return code
		}
	}
	return ""
}

func videoCodecFromBDInfo(info *discparse.BDInfo) string {
	if info == nil || len(info.Video) == 0 {
		return ""
	}
	switch info.Video[0].Codec {
	case "MPEG-2 Video":
		return "MPEG-2"
	case "MPEG-4 AVC Video":
		return "AVC"
	case "MPEG-H HEVC Video":
		return "HEVC"
	case "VC-1 Video":
		return "VC-1"
	default:
		return strings.TrimSpace(info.Video[0].Codec)
	}
}

func videoEncodeFromMedia(doc mediaInfoDoc, typeValue string) (string, string, bool, string) {
	_, videoTracks, _ := splitMediaInfoTracks(doc)
	if len(videoTracks) == 0 {
		return "", "", false, ""
	}
	track := videoTracks[0]
	format := trackString(track, "Format")
	profile := trackString(track, "Format_Profile", "Format_Profile_Original")
	encodedSettings := trackString(track, "Encoded_Library_Settings")
	bitDepth := "0"
	if parsedBitDepth := trackString(track, "BitDepth"); parsedBitDepth != "" {
		bitDepth = parsedBitDepth
	}
	library := trackString(track, "Encoded_Library_Name")
	codec := ""
	videoCodec := format

	switch format {
	case "AV1", "VP9", "VC-1":
		codec = format
	case "AVC":
		switch typeValue {
		case "ENCODE", "WEBRIP", "DVDRIP":
			codec = "x264"
		case "WEBDL", "HDTV":
			codec = "H.264"
		}
	case "HEVC":
		switch typeValue {
		case "ENCODE", "WEBRIP", "DVDRIP":
			codec = "x265"
		case "WEBDL", "HDTV":
			codec = "H.265"
		}
	case "MPEG-4 Visual":
		if typeValue == "ENCODE" || typeValue == "WEBRIP" || typeValue == "DVDRIP" {
			lowerLib := strings.ToLower(library)
			if strings.Contains(lowerLib, "xvid") {
				codec = "XviD"
			} else if strings.Contains(lowerLib, "divx") {
				codec = "DivX"
			}
		}
	}
	if typeValue == "HDTV" && encodedSettings != "" {
		codec = strings.ReplaceAll(codec, "H.", "x")
	}
	profileTag := ""
	if profile == "High 10" {
		profileTag = "Hi10P"
	}
	videoEncode := strings.TrimSpace(strings.TrimSpace(profileTag + " " + codec))
	if videoCodec == "MPEG Video" {
		version := trackString(track, "Format_Version")
		if version != "" {
			videoCodec = "MPEG-" + version
		}
	}
	return videoEncode, videoCodec, encodedSettings != "", bitDepth
}

func editionFromMeta(meta api.PreparedMetadata, doc mediaInfoDoc) (string, string) {
	edition := ""
	isIMDbEdition := false
	applyAnimeOverride(&meta)
	if hasNoEditionOverride(meta.ReleaseNameOverrides) {
		return "", ""
	}
	if !hasManualEditionOverride(meta.ReleaseNameOverrides) {
		edition = strings.TrimSpace(resolveIMDbEditionFromMediaDuration(meta, doc))
		isIMDbEdition = edition != ""
		if edition == "" {
			edition = strings.TrimSpace(resolveMultiPlaylistEdition(meta))
			isIMDbEdition = edition != ""
		}
	}
	if edition == "" {
		edition = strings.TrimSpace(meta.Edition)
	}
	if edition == "" && len(meta.Release.Edition) > 0 {
		edition = strings.TrimSpace(strings.Join(meta.Release.Edition, " "))
	}
	repack := repackFromMeta(meta, edition)
	if edition == "" {
		return withHybrid(meta, ""), repack
	}
	if repackPattern.MatchString(edition) {
		if repack == "" {
			repack = repackPattern.FindString(edition)
		}
		edition = strings.TrimSpace(repackPattern.ReplaceAllString(edition, ""))
	}
	if isIMDbEdition {
		return withHybrid(meta, cleanIMDbEditionText(edition)), strings.ToUpper(repack)
	}
	return withHybrid(meta, cleanEditionText(edition)), strings.ToUpper(repack)
}

// withHybrid folds a hybrid marker into the edition. rls files HYBRID under the
// parsed Other tokens, but the release-name builder and the trackers read hybrid
// off the edition, so a source that never carries it there loses the marker: the
// name builder renders it in its own slot and BHD tags from the same field.
func withHybrid(meta api.PreparedMetadata, edition string) string {
	if !hybridFromMeta(meta, edition) {
		return edition
	}
	if strings.Contains(strings.ToUpper(edition), "HYBRID") {
		return edition
	}
	return strings.TrimSpace("Hybrid " + edition)
}

// hybridFromMeta scans source basenames and parsed release tokens for the hybrid
// marker, the same way repack markers are recovered.
func hybridFromMeta(meta api.PreparedMetadata, edition string) bool {
	return hasReleaseToken(releaseTokens(
		pathutil.Base(meta.SourcePath),
		pathutil.Base(meta.VideoPath),
		edition,
		strings.Join(meta.Release.Edition, " "),
		strings.Join(meta.Release.Other, " "),
	), "HYBRID")
}

// repackFromMeta scans source basenames and parsed release tokens so repack
// markers do not depend on guessit/rls placing them in the edition field.
func repackFromMeta(meta api.PreparedMetadata, edition string) string {
	return repackFromText(
		pathutil.Base(meta.SourcePath),
		pathutil.Base(meta.VideoPath),
		edition,
		strings.Join(meta.Release.Edition, " "),
		strings.Join(meta.Release.Other, " "),
	)
}

// repackFromText returns the highest-priority repack/proper marker present in
// release tokens, matching Upload Assistant's V2/V3/V4 repack aliases.
func repackFromText(values ...string) string {
	tokens := releaseTokens(values...)
	repack := ""
	if hasReleaseToken(tokens, "REPACK") || hasReleaseToken(tokens, "V2") {
		repack = "REPACK"
	}
	if hasReleaseToken(tokens, "REPACK2") || hasReleaseToken(tokens, "V3") {
		repack = "REPACK2"
	}
	if hasReleaseToken(tokens, "REPACK3") || hasReleaseToken(tokens, "V4") {
		repack = "REPACK3"
	}
	if hasReleaseToken(tokens, "PROPER") {
		repack = "PROPER"
	}
	if hasReleaseToken(tokens, "PROPER2") {
		repack = "PROPER2"
	}
	if hasReleaseToken(tokens, "PROPER3") {
		repack = "PROPER3"
	}
	if hasReleaseToken(tokens, "RERIP") {
		repack = "RERIP"
	}
	return repack
}

// releaseTokens splits release-name text into uppercase tokens using
// punctuation, whitespace, and path separators as boundaries.
func releaseTokens(values ...string) map[string]struct{} {
	tokens := make(map[string]struct{})
	for _, value := range values {
		for _, token := range releaseTokenSeparatorPattern.Split(strings.ToUpper(value), -1) {
			if token == "" {
				continue
			}
			tokens[token] = struct{}{}
		}
	}
	return tokens
}

// hasReleaseToken reports whether a normalized token set contains value.
func hasReleaseToken(tokens map[string]struct{}, value string) bool {
	_, ok := tokens[value]
	return ok
}

func resolveMultiPlaylistEdition(meta api.PreparedMetadata) string {
	if !strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		return ""
	}
	if len(meta.SelectedBDMVPlaylists) < 2 || meta.ExternalMetadata.IMDB == nil || len(meta.ExternalMetadata.IMDB.EditionDetails) == 0 {
		return ""
	}

	withAttributes := make(map[string]struct{})
	withoutAttributes := false

	for _, playlist := range meta.SelectedBDMVPlaylists {
		if playlist.Duration <= 0 {
			continue
		}

		matches := imdbEditionMatches(playlist.Duration, meta.ExternalMetadata.IMDB.EditionDetails, true)
		if len(matches) == 0 {
			continue
		}
		best := matches[0]
		if best.hasAttribute {
			withAttributes[best.name] = struct{}{}
			continue
		}
		withoutAttributes = true
	}

	if len(withAttributes) == 0 {
		return ""
	}

	editions := make([]string, 0, len(withAttributes)+1)
	if withoutAttributes {
		editions = append(editions, "Theatrical")
	}
	attributeNames := make([]string, 0, len(withAttributes))
	for name := range withAttributes {
		attributeNames = append(attributeNames, name)
	}
	sort.Strings(attributeNames)
	editions = append(editions, attributeNames...)

	if len(editions) == 1 {
		return editions[0]
	}
	return fmt.Sprintf("%din1 %s", len(editions), strings.Join(editions, " / "))
}

type imdbEditionMatch struct {
	name          string
	differenceSec float64
	hasAttribute  bool
	minutes       int
}

func resolveIMDbEditionFromMediaDuration(meta api.PreparedMetadata, doc mediaInfoDoc) string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") || !isMovieMetadata(meta) || meta.Anime {
		return ""
	}
	if meta.ExternalMetadata.IMDB == nil || len(meta.ExternalMetadata.IMDB.EditionDetails) <= 1 {
		return ""
	}
	duration := mediaDurationSeconds(doc)
	if duration <= 0 {
		return ""
	}
	matches := imdbEditionMatches(duration, meta.ExternalMetadata.IMDB.EditionDetails, true)
	if len(matches) == 0 || !matches[0].hasAttribute {
		return ""
	}
	return matches[0].name
}

func mediaDurationSeconds(doc mediaInfoDoc) float64 {
	generalTracks, _, _ := splitMediaInfoTracks(doc)
	for _, track := range generalTracks {
		if value := trackString(track, "Duration"); value != "" {
			if seconds := parseMediaDurationValue(value); seconds > 0 {
				return seconds
			}
		}
		if value := trackString(track, "Duration/String3", "Duration/String2", "Duration/String"); value != "" {
			if seconds := parseMediaDurationValue(value); seconds > 0 {
				return seconds
			}
		}
	}
	return 0
}

func parseMediaDurationValue(value string) float64 {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}
	if strings.Contains(trimmed, ":") {
		return parseMediaDurationColonValue(trimmed)
	}
	if seconds := parseMediaDurationTokens(trimmed); seconds > 0 {
		return seconds
	}
	parsed, err := strconv.ParseFloat(strings.ReplaceAll(trimmed, ",", ""), 64)
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

func parseMediaDurationTokens(value string) float64 {
	var total float64
	for _, match := range durationTokenPattern.FindAllStringSubmatch(value, -1) {
		if len(match) != 3 {
			continue
		}
		amount, err := strconv.ParseFloat(strings.ReplaceAll(match[1], ",", ""), 64)
		if err != nil || amount <= 0 {
			continue
		}
		switch strings.ToLower(match[2]) {
		case "h", "hr", "hrs", "hour", "hours":
			total += amount * 3600
		case "m", "min", "mins", "minute", "minutes":
			total += amount * 60
		case "s", "sec", "secs", "second", "seconds":
			total += amount
		case "ms", "msec", "msecs", "millisecond", "milliseconds":
			total += amount / 1000
		}
	}
	return total
}

func parseMediaDurationColonValue(value string) float64 {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) < 2 {
		parsed, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(value), ",", ""), 64)
		if err != nil || parsed <= 0 {
			return 0
		}
		return parsed
	}
	var seconds float64
	multiplier := 1.0
	for i := len(parts) - 1; i >= 0; i-- {
		part := strings.TrimSpace(parts[i])
		if part == "" {
			continue
		}
		amount, err := strconv.ParseFloat(strings.ReplaceAll(part, ",", ""), 64)
		if err != nil {
			return 0
		}
		seconds += amount * multiplier
		multiplier *= 60
	}
	return seconds
}

func imdbEditionMatches(duration float64, details map[string]api.IMDBEditionDetail, includeTheatrical bool) []imdbEditionMatch {
	const leewaySeconds = 50.0
	matches := make([]imdbEditionMatch, 0)
	for _, detail := range details {
		if detail.Seconds <= 0 {
			continue
		}
		diff := absFloat(duration - float64(detail.Seconds))
		if diff > leewaySeconds {
			continue
		}
		name := imdbEditionAttributeName(detail.Attributes)
		hasAttribute := name != ""
		if !hasAttribute {
			if !includeTheatrical {
				continue
			}
			name = fmt.Sprintf("%d Minute Version (Theatrical)", detail.Minutes)
		}
		matches = append(matches, imdbEditionMatch{
			name:          name,
			differenceSec: diff,
			hasAttribute:  hasAttribute,
			minutes:       detail.Minutes,
		})
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].differenceSec == matches[j].differenceSec {
			if matches[i].hasAttribute != matches[j].hasAttribute {
				return matches[i].hasAttribute
			}
			if matches[i].name == matches[j].name {
				return matches[i].minutes < matches[j].minutes
			}
			return matches[i].name < matches[j].name
		}
		return matches[i].differenceSec < matches[j].differenceSec
	})
	return matches
}

func imdbEditionAttributeName(attributes []string) string {
	names := make([]string, 0, len(attributes))
	for _, attr := range attributes {
		name := smartEditionTitle(attr)
		if name != "" {
			names = append(names, name)
		}
	}
	return strings.TrimSpace(strings.Join(names, " "))
}

func smartEditionTitle(value string) string {
	fields := strings.Fields(strings.TrimSpace(value))
	if len(fields) == 0 {
		return ""
	}
	for idx, field := range fields {
		fields[idx] = smartEditionWord(field)
	}
	return strings.Join(fields, " ")
}

func smartEditionWord(value string) string {
	if value == "" {
		return ""
	}
	if isAllUpperEditionWord(value) {
		return value
	}
	lower := strings.ToLower(value)
	runes := []rune(lower)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func isAllUpperEditionWord(value string) bool {
	hasLetter := false
	for _, r := range value {
		if !unicode.IsLetter(r) {
			continue
		}
		hasLetter = true
		if unicode.IsLower(r) {
			return false
		}
	}
	return hasLetter
}

// cleanEditionText normalizes parser-derived edition text and drops filename
// tokens that are not meaningful release-name editions.
func cleanEditionText(edition string) string {
	return cleanEditionTextWithOptions(edition, cleanEditionTextOptions{
		shouldDrop: func(edition string) bool {
			return len(edition) < 6
		},
		stripBadTokens: true,
	})
}

// cleanIMDbEditionText normalizes IMDb runtime edition attributes while
// preserving IMDb-specific labels that the parser cleanup would otherwise drop.
func cleanIMDbEditionText(edition string) string {
	return cleanEditionTextWithOptions(edition, cleanEditionTextOptions{})
}

type cleanEditionTextOptions struct {
	shouldDrop     func(string) bool
	stripBadTokens bool
}

// cleanEditionTextWithOptions applies preprocessing shared by parser and IMDb
// edition normalization before final residue cleanup.
func cleanEditionTextWithOptions(edition string, opts cleanEditionTextOptions) string {
	edition = strings.TrimSpace(strings.ReplaceAll(edition, ",", " "))
	if edition == "" {
		return ""
	}
	lower := strings.ToLower(edition)
	if lower == "cut" || lower == "approximate" || opts.shouldDrop != nil && opts.shouldDrop(edition) {
		return ""
	}
	if strings.Contains(lower, "edition") {
		edition = editionWordPattern.ReplaceAllString(edition, "")
	}
	lower = strings.ToLower(edition)
	if strings.Contains(lower, "extended") && !strings.Contains(lower, "in1") && !strings.Contains(edition, "/") {
		edition = "Extended"
	}
	if opts.stripBadTokens {
		edition = editionBadTokenPattern.ReplaceAllString(edition, "")
	}
	return cleanEditionResidue(edition)
}

// cleanEditionResidue removes separator-only leftovers after edition token
// cleanup, such as ". ." from stripped "Limited.Edition" markers.
func cleanEditionResidue(edition string) string {
	edition = editionWhitespacePattern.ReplaceAllString(edition, " ")
	edition = strings.Trim(edition, " ._-+/()[]{}")
	return strings.TrimSpace(edition)
}

func applyAnimeOverride(meta *api.PreparedMetadata) {
	if meta == nil || meta.MetadataOverrides.Anime == nil {
		return
	}
	meta.Anime = *meta.MetadataOverrides.Anime
}

func hasManualEditionOverride(overrides api.ReleaseNameOverrides) bool {
	return overrides.Edition != nil
}

func hasNoEditionOverride(overrides api.ReleaseNameOverrides) bool {
	return overrides.NoEdition != nil && *overrides.NoEdition
}

func isMovieMetadata(meta api.PreparedMetadata) bool {
	category := normalizeNamingCategory(meta.ExternalIDs.Category)
	if category == "" {
		category = normalizeNamingCategory(meta.MediaInfoCategory)
	}
	if category == "" {
		category = normalizeNamingCategory(meta.Release.Category)
	}
	if category == "" {
		category = inferCategoryFromMetadata(meta)
	}
	return strings.EqualFold(category, "MOVIE")
}

func absFloat(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}

func validateMediaInfoSettings(doc mediaInfoDoc) bool {
	_, _, audioTracks := splitMediaInfoTracks(doc)
	if len(audioTracks) == 0 {
		return false
	}
	_, videoTracks, _ := splitMediaInfoTracks(doc)
	for _, track := range videoTracks {
		settings := trackString(track, "Encoded_Library_Settings")
		if settings != "" {
			return true
		}
	}
	return false
}
