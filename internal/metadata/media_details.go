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

	"github.com/autobrr/upbrr/internal/metadata/discparse"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

var repackPattern = regexp.MustCompile(`REPACK\d?`)
var numericPattern = regexp.MustCompile(`\d+`)

func (s *Service) ApplyMediaDetails(ctx context.Context, meta api.PreparedMetadata) (api.PreparedMetadata, error) {
	select {
	case <-ctx.Done():
		return api.PreparedMetadata{}, ctx.Err()
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
	if s.logger != nil {
		s.logger.Debugf("metadata: media details source=%q type=%q resolution=%q", meta.Source, meta.Type, meta.Release.Resolution)
	}

	meta.UHD = uhdFromMeta(meta)
	meta.HDR = hdrFromMedia(miDoc, bdinfo)
	if s.logger != nil {
		s.logger.Debugf("metadata: media details uhd=%q hdr=%q", meta.UHD, meta.HDR)
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

	meta.Edition, meta.Repack, meta.WebDV = editionFromMeta(meta)
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

	nameRequest := releaseNameRequestFromMeta(meta, s.logger)
	nameRequest = applyReleaseNameOverrides(nameRequest, meta.ReleaseNameOverrides, s.logger)
	nameResult := BuildReleaseName(nameRequest, s.logger)
	meta.ReleaseNameNoTag = nameResult.NameNoTag
	meta.ReleaseName = nameResult.Name
	meta.ReleaseNameClean = nameResult.CleanName
	meta.ReleaseNameMissing = append([]string{}, nameResult.MissingFields...)
	if s.logger != nil && nameResult.Name != "" {
		s.logger.Tracef("metadata: release name resolved %q", nameResult.Name)
	}

	meta, err = s.applyTrackerRules(ctx, meta)
	if err != nil {
		return api.PreparedMetadata{}, err
	}
	meta, err = s.applyTrackerClaims(ctx, meta)
	if err != nil {
		return api.PreparedMetadata{}, err
	}

	return meta, nil
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

func audioFromMedia(meta api.PreparedMetadata, doc mediaInfoDoc, bdinfo *discparse.BDInfo) (string, string, bool) {
	if bdinfo != nil && len(bdinfo.Audio) > 0 {
		track := bdinfo.Audio[0]
		codec := strings.TrimSpace(track.Codec)
		if track.Atmos != "" && !strings.Contains(strings.ToLower(codec), "atmos") {
			codec = strings.TrimSpace(codec + " Atmos")
		}
		channels := strings.TrimSpace(track.Channels)
		if channels == "" {
			channels = "Unknown"
		}
		return strings.TrimSpace(codec + " " + channels), channels, false
	}

	_, _, audioTracks := splitMediaInfoTracks(doc)
	if len(audioTracks) == 0 {
		return "", "", false
	}
	firstAudioTitle := strings.ToLower(trackString(audioTracks[0], "Title", "title"))
	track := selectPrimaryAudioTrack(audioTracks)
	format := normalizeAudioFormat(track)
	additional := trackString(track, "Format_AdditionalFeatures", "Format_AdditionalFeatures_String", "Format_AdditionalFeatures_Original")
	format = applyDTSAudioAdditional(format, additional)
	channels := determineChannelCount(
		trackString(track, "Channels", "Channel_s_", "Channel_s__Original"),
		trackString(track, "ChannelLayout", "ChannelLayout_Original", "ChannelPositions", "ChannelPositions_Original"),
		additional,
		trackString(track, "Format", "Format_String"),
	)
	if channels == "" {
		channels = "Unknown"
	}

	formatLower := strings.ToLower(format)
	if isAtmosAudio(additional, formatLower, trackString(track, "ChannelLayout", "ChannelPositions")) && !strings.Contains(formatLower, "atmos") {
		format = strings.TrimSpace(format + " Atmos")
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
	if !strings.Contains(strings.ToLower(format), "atmos") && strings.Contains(firstAudioTitle, "auro3d") {
		format = strings.TrimSpace(format + " Auro3D")
	}
	commentary := false
	for _, audioTrack := range audioTracks {
		title := strings.ToLower(trackString(audioTrack, "Title", "title"))
		if strings.Contains(title, "commentary") {
			commentary = true
			break
		}
	}
	return strings.TrimSpace(format + " " + channels), channels, commentary
}

// selectPrimaryAudioTrack filters out compatibility tracks before selection.
// This intentionally diverges from the Python reference, which selects from
// all tracks, to avoid fallback compatibility tracks representing the release.
func selectPrimaryAudioTrack(tracks []map[string]any) map[string]any {
	if len(tracks) == 0 {
		return nil
	}
	filtered := filterCompatibilityTracks(tracks)
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

func filterCompatibilityTracks(tracks []map[string]any) []map[string]any {
	filtered := make([]map[string]any, 0, len(tracks))
	for _, track := range tracks {
		title := strings.ToLower(trackString(track, "Title", "title"))
		if strings.Contains(title, "compatibility") {
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
	if value == "" {
		value = trackString(track, "CodecID", "CodecID_Compatible")
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
	for _, field := range strings.Fields(value) {
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
	parts := strings.Fields(upper)
	for _, part := range parts {
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
	if strings.EqualFold(source, "Web") && typeValue == "ENCODE" {
		typeValue = "WEBRIP"
	}
	if strings.EqualFold(source, "Ultra HDTV") {
		source = "UHDTV"
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
	if meta.Type == "DISC" || meta.Type == "REMUX" || meta.Type == "ENCODE" || meta.Type == "WEBRIP" {
		if strings.EqualFold(meta.Release.Resolution, "2160p") {
			return "UHD"
		}
	}
	if strings.Contains(strings.ToLower(meta.Release.Source), "ultra") {
		return "UHD"
	}
	return ""
}

func hdrFromMedia(doc mediaInfoDoc, bdinfo *discparse.BDInfo) string {
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
		return strings.TrimSpace(strings.Join([]string{dv, hdr}, " "))
	}

	_, videoTracks, _ := splitMediaInfoTracks(doc)
	if len(videoTracks) == 0 {
		return ""
	}
	track := videoTracks[0]
	primaries := trackString(track, "colour_primaries", "colour_primaries_Original")
	primariesUpper := strings.ToUpper(primaries)
	hdr := ""
	dv := ""
	if primariesUpper == "BT.2020" || primariesUpper == "REC.2020" {
		compat := trackString(track, "HDR_Format_Compatibility")
		formatStr := trackString(track, "HDR_Format_String")
		format := trackString(track, "HDR_Format")
		hdrFormat := metautil.FirstNonEmptyTrimmed(compat, formatStr, format)
		upperFormat := strings.ToUpper(hdrFormat)
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
		if hdrFormat == "" && strings.Contains(strings.ToUpper(transfer), "PQ") {
			hdr = "PQ10"
		}
		if strings.Contains(strings.ToUpper(transfer), "HLG") {
			hdr = "HLG"
		}
		if hdr != "HLG" && strings.Contains(strings.ToUpper(transfer), "BT.2020 (10-BIT)") {
			hdr = "WCG"
		}
	}
	if strings.Contains(trackString(track, "HDR_Format"), "Dolby Vision") || strings.Contains(trackString(track, "HDR_Format_String"), "Dolby Vision") {
		dv = "DV"
	}
	return strings.TrimSpace(strings.Join([]string{dv, hdr}, " "))
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
	fields := strings.Fields(label)
	for _, field := range fields {
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

func editionFromMeta(meta api.PreparedMetadata) (string, string, bool) {
	edition := strings.TrimSpace(resolveMultiPlaylistEdition(meta))
	if edition == "" {
		edition = strings.TrimSpace(meta.Edition)
	}
	if edition == "" && len(meta.Release.Edition) > 0 {
		edition = strings.TrimSpace(strings.Join(meta.Release.Edition, " "))
	}
	if edition == "" {
		return "", "", false
	}
	repack := ""
	if repackPattern.MatchString(edition) {
		repack = repackPattern.FindString(edition)
		edition = strings.TrimSpace(repackPattern.ReplaceAllString(edition, ""))
		edition = strings.ReplaceAll(edition, "  ", " ")
	}
	return edition, repack, false
}

func resolveMultiPlaylistEdition(meta api.PreparedMetadata) string {
	if !strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		return ""
	}
	if len(meta.SelectedBDMVPlaylists) < 2 || meta.ExternalMetadata.IMDB == nil || len(meta.ExternalMetadata.IMDB.EditionDetails) == 0 {
		return ""
	}

	const leewaySeconds = 50.0
	withAttributes := make(map[string]struct{})
	withoutAttributes := false

	for _, playlist := range meta.SelectedBDMVPlaylists {
		if playlist.Duration <= 0 {
			continue
		}

		var best *api.IMDBEditionDetail
		bestDiff := leewaySeconds + 1
		for _, detail := range meta.ExternalMetadata.IMDB.EditionDetails {
			diff := absFloat(playlist.Duration - float64(detail.Seconds))
			if diff > leewaySeconds {
				continue
			}
			if best == nil || diff < bestDiff {
				copy := detail
				best = &copy
				bestDiff = diff
			}
		}
		if best == nil {
			continue
		}
		if len(best.Attributes) > 0 {
			name := strings.TrimSpace(strings.Join(best.Attributes, " "))
			if name != "" {
				withAttributes[name] = struct{}{}
			}
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
