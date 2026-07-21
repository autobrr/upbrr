// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// ResolveUploadSubject validates and projects one exact prepared generation
// into the upload module's operation-owned read model.
func (m *Module) ResolveUploadSubject(ctx context.Context, input api.UploadReviewInput) (api.UploadSubject, error) {
	owned, err := m.resolveEnvelope(ctx, input.Release)
	if err != nil {
		return api.UploadSubject{}, err
	}
	release := owned.result.Release
	resources := owned.resources
	discType := firstNonEmpty(release.Disc.Type, release.Source.Classification.DiscType)
	fileList := append([]string(nil), resources.fileList...)
	if len(fileList) == 0 {
		fileList = manifestFiles(release.Source)
	}
	selectedPlaylists := clonePreparedPlaylists(resources.selectedBDMVPlaylists)
	if len(selectedPlaylists) == 0 {
		selectedPlaylists = clonePreparedPlaylists(release.Source.SelectedPlaylists)
	}
	subject := api.UploadSubject{
		SourcePath:                  release.Source.SourcePath,
		Paths:                       []string{resources.sourcePath},
		DiscType:                    discType,
		VideoPath:                   resources.videoPath,
		FileList:                    fileList,
		SourceSize:                  release.Source.Size,
		MediaInfoJSONPath:           resources.mediaInfoJSONPath,
		MediaInfoTextPath:           resources.mediaInfoTextPath,
		DVDVOBMediaInfoText:         resources.dvdVOBMediaInfoText,
		Scene:                       release.Naming.Scene,
		SceneName:                   release.Naming.SceneName,
		SceneNFOPath:                resources.sceneNFOPath,
		DescriptionGroups:           api.CloneDescriptionBuilderGroups(input.DescriptionGroups),
		DescriptionTemplate:         resources.descriptionTemplate,
		Trackers:                    append([]string(nil), input.Trackers...),
		Options:                     input.Options,
		Tag:                         release.Naming.Tag,
		Release:                     releaseInfo(release),
		TrackerConfigOverrides:      input.TrackerConfigOverrides,
		TrackerSiteOverrides:        input.TrackerSiteOverrides,
		ImageHostOverrides:          input.ImageHostOverrides,
		PersonalRelease:             release.Naming.Personal,
		TrackerQuestionnaireAnswers: cloneAnswers(input.QuestionnaireAnswers),
		SeasonInt:                   release.Episode.Season,
		EpisodeInt:                  release.Episode.Episode,
		SeasonStr:                   release.Episode.SeasonLabel,
		EpisodeStr:                  release.Episode.EpisodeLabel,
		TVDBAiredDate:               release.Episode.AiredDate,
		TVDBAirsTime:                release.Episode.AirTime,
		TVDBAirsTimezone:            release.Episode.AirTimezone,
		TVPack:                      release.Episode.Pack,
		DailyEpisodeDate:            release.Episode.DailyDate,
		Anime:                       release.Media.Anime,
		EpisodeTitle:                release.Episode.Title,
		EpisodeOverview:             release.Episode.Overview,
		SelectedBDMVPlaylists:       selectedPlaylists,
		Identity:                    release.Identity,
		ProviderMetadata:            release.ProviderMetadata,
		Disc:                        release.Disc,
		AudioLanguages:              append([]string(nil), release.Media.AudioLanguages...),
		SubtitleLanguages:           append([]string(nil), release.Media.SubtitleLanguages...),
		Container:                   release.Media.Container,
		Audio:                       release.Media.Audio,
		Channels:                    release.Media.Channels,
		HasCommentary:               release.Media.Commentary,
		Is3D:                        release.Media.ThreeD,
		Source:                      release.Media.Source,
		Type:                        release.Media.Type,
		UHD:                         release.Media.UHD,
		HDR:                         release.Media.HDR,
		Distributor:                 release.Media.Distributor,
		Region:                      release.Media.Region,
		VideoCodec:                  release.Media.VideoCodec,
		VideoEncode:                 release.Media.VideoEncode,
		HasEncodeSettings:           release.Media.HasEncodeSettings,
		BitDepth:                    release.Media.BitDepth,
		Edition:                     release.Media.Edition,
		Repack:                      release.Media.Repack,
		WebDV:                       release.Media.WebDV,
		Assessments:                 release.Assessments,
		StreamOptimized:             release.Media.StreamOptimized,
		Service:                     release.Media.Service,
		ServiceLongName:             release.Media.ServiceLongName,
		Filename:                    release.Naming.Filename,
		ReleaseName:                 release.Naming.ReleaseName,
		ReleaseNameNoTag:            release.Naming.NameWithoutTag,
		ReleaseNameClean:            release.Naming.CleanName,
		ArrReleaseGroup:             release.Naming.Group,
		InfoHash:                    resources.clientEvidence.Result.InfoHash,
		ClientTorrentPath:           resources.clientEvidence.Result.TorrentPath,
		TrackerIDs:                  maps.Clone(resources.clientEvidence.Result.TrackerIDs),
		MatchedTrackers:             append([]string(nil), resources.clientEvidence.Result.MatchedTrackers...),
	}
	cloned, err := cloneWithJSON(subject)
	if err != nil {
		return api.UploadSubject{}, fmt.Errorf("prepared release: clone upload subject: %w", err)
	}
	return cloned, nil
}

// ResolveDuplicateSubject validates and projects one exact prepared generation
// into the duplicate module's operation-owned read model.
func (m *Module) ResolveDuplicateSubject(ctx context.Context, input api.DuplicateCheckInput) (api.DuplicateSubject, error) {
	owned, err := m.resolveEnvelope(ctx, input.Release)
	if err != nil {
		return api.DuplicateSubject{}, err
	}
	release := owned.result.Release
	resources := owned.resources
	fileList := append([]string(nil), resources.fileList...)
	if len(fileList) == 0 {
		fileList = manifestFiles(release.Source)
	}
	subject := api.DuplicateSubject{
		SourcePath:        release.Source.SourcePath,
		SourceSize:        release.Source.Size,
		VideoPath:         resources.videoPath,
		FileList:          fileList,
		Filename:          release.Naming.Filename,
		SceneName:         release.Naming.SceneName,
		ReleaseName:       release.Naming.ReleaseName,
		Release:           releaseInfo(release),
		Identity:          release.Identity,
		ProviderMetadata:  release.ProviderMetadata,
		DiscType:          firstNonEmpty(release.Disc.Type, release.Source.Classification.DiscType),
		Type:              release.Media.Type,
		Source:            release.Media.Source,
		Tag:               release.Naming.Tag,
		HDR:               release.Media.HDR,
		UHD:               release.Media.UHD,
		VideoEncode:       release.Media.VideoEncode,
		VideoCodec:        release.Media.VideoCodec,
		HasEncodeSettings: release.Media.HasEncodeSettings,
		SeasonInt:         release.Episode.Season,
		EpisodeInt:        release.Episode.Episode,
		SeasonStr:         release.Episode.SeasonLabel,
		EpisodeStr:        release.Episode.EpisodeLabel,
		DailyEpisodeDate:  release.Episode.DailyDate,
		TVPack:            release.Episode.Pack,
		Anime:             release.Media.Anime,
		TrackerIDs:        maps.Clone(resources.clientEvidence.Result.TrackerIDs),
		MatchedTrackers:   append([]string(nil), resources.clientEvidence.Result.MatchedTrackers...),
	}
	cloned, err := cloneWithJSON(subject)
	if err != nil {
		return api.DuplicateSubject{}, fmt.Errorf("prepared release: clone duplicate subject: %w", err)
	}
	return cloned, nil
}

// ResolveDVDMenuSubject validates and projects one exact prepared generation
// into the DVD-menu module's operation-owned read model.
func (m *Module) ResolveDVDMenuSubject(ctx context.Context, input api.MediaPlanInput) (api.DVDMenuSubject, error) {
	owned, err := m.resolveEnvelope(ctx, input.Release)
	if err != nil {
		return api.DVDMenuSubject{}, err
	}
	release := owned.result.Release
	return api.DVDMenuSubject{
		SourcePath: release.Source.SourcePath,
		DiscType:   firstNonEmpty(release.Disc.Type, release.Source.Classification.DiscType),
	}, nil
}

// ResolveScreenshotSubject validates and projects one exact prepared
// generation into the screenshot module's operation-owned read model.
func (m *Module) ResolveScreenshotSubject(ctx context.Context, input api.MediaPlanInput) (api.ScreenshotSubject, error) {
	owned, err := m.resolveEnvelope(ctx, input.Release)
	if err != nil {
		return api.ScreenshotSubject{}, err
	}
	release := owned.result.Release
	resources := owned.resources
	selectedPlaylists := clonePreparedPlaylists(resources.selectedBDMVPlaylists)
	if len(selectedPlaylists) == 0 {
		selectedPlaylists = clonePreparedPlaylists(release.Source.SelectedPlaylists)
	}
	return api.ScreenshotSubject{
		SourcePath:            release.Source.SourcePath,
		DiscType:              firstNonEmpty(release.Disc.Type, release.Source.Classification.DiscType),
		VideoPath:             resources.videoPath,
		MediaInfoJSONPath:     resources.mediaInfoJSONPath,
		MediaCategory:         string(release.Identity.Category),
		HDR:                   release.Media.HDR,
		TVPack:                release.Episode.Pack,
		Episode:               release.Episode.Episode,
		Release:               releaseInfo(release),
		SelectedBDMVPlaylists: selectedPlaylists,
		DefaultCount:          input.Count,
		ManualFrames:          append([]int(nil), input.Options.ManualFrames...),
	}, nil
}

// ResolveImageHostingSubject validates and projects one exact prepared
// generation into the image-hosting module's operation-owned read model.
func (m *Module) ResolveImageHostingSubject(ctx context.Context, input api.ImageHostingInput) (api.ImageHostingSubject, error) {
	owned, err := m.resolveEnvelope(ctx, input.Release)
	if err != nil {
		return api.ImageHostingSubject{}, err
	}
	release := owned.result.Release
	galleryName := firstNonEmpty(
		release.Naming.ReleaseName,
		release.Naming.NameWithoutTag,
		release.Naming.Title,
		release.Naming.Filename,
		filepath.Base(release.Source.SourcePath),
	)
	return api.ImageHostingSubject{SourcePath: release.Source.SourcePath, GalleryName: galleryName}, nil
}

// ResolveDescriptionSubject validates and projects one exact prepared
// generation into the description module's operation-owned read model.
func (m *Module) ResolveDescriptionSubject(ctx context.Context, input api.DescriptionInput) (api.DescriptionSubject, error) {
	owned, err := m.resolveEnvelope(ctx, input.Release)
	if err != nil {
		return api.DescriptionSubject{}, err
	}
	release := owned.result.Release
	resources := owned.resources
	selectedPlaylists := clonePreparedPlaylists(resources.selectedBDMVPlaylists)
	if len(selectedPlaylists) == 0 {
		selectedPlaylists = clonePreparedPlaylists(release.Source.SelectedPlaylists)
	}
	return api.DescriptionSubject{
		SourcePath:            release.Source.SourcePath,
		DiscType:              firstNonEmpty(release.Disc.Type, release.Source.Classification.DiscType),
		MediaInfoTextPath:     resources.mediaInfoTextPath,
		DVDVOBMediaInfoText:   resources.dvdVOBMediaInfoText,
		EpisodeOverview:       release.Episode.Overview,
		DescriptionTemplate:   resources.descriptionTemplate,
		Options:               input.Options,
		Release:               releaseInfo(release),
		SelectedBDMVPlaylists: selectedPlaylists,
		Tag:                   release.Naming.Tag,
		Identity:              release.Identity,
		ProviderMetadata:      release.ProviderMetadata,
		SeasonInt:             release.Episode.Season,
		EpisodeInt:            release.Episode.Episode,
		Filename:              release.Naming.Filename,
		ReleaseName:           release.Naming.ReleaseName,
		ReleaseNameNoTag:      release.Naming.NameWithoutTag,
		ServiceLongName:       release.Media.ServiceLongName,
		Type:                  release.Media.Type,
		HDR:                   release.Media.HDR,
		ArrReleaseGroup:       release.Naming.Group,
	}, nil
}

func (m *Module) resolveEnvelope(ctx context.Context, ref api.ReleaseRef) (envelope, error) {
	if m == nil {
		return envelope{}, errors.New("prepared release: module is not initialized")
	}
	if ctx == nil {
		return envelope{}, errors.New("prepared release: context is required")
	}
	normalized, err := normalizeSourcePath(ref.SourcePath)
	if err != nil || ref.Generation == 0 {
		return envelope{}, &IncompatiblePreparationError{SourcePath: ref.SourcePath, Reason: "invalid release reference"}
	}
	key := canonicalSourceKey(normalized)
	m.mu.RLock()
	owned, ok := m.envelopes[key]
	m.mu.RUnlock()
	if !ok || owned.result.Release.Generation != ref.Generation {
		return envelope{}, &StalePreparationError{
			SourcePath: key,
			Generation: ref.Generation,
			Reason:     StaleReasonGeneration,
		}
	}
	cloned, err := cloneEnvelope(owned)
	if err != nil {
		return envelope{}, err
	}
	if err := validateSeedSource(ctx, cloned); err != nil {
		return envelope{}, err
	}
	return cloned, nil
}

func releaseInfo(release api.PreparedRelease) api.ReleaseInfo {
	naming := release.Naming
	return api.ReleaseInfo{
		Type:       naming.Type,
		Artist:     naming.Artist,
		Title:      naming.Title,
		Subtitle:   naming.Subtitle,
		Alt:        naming.AlternateTitle,
		Year:       naming.Year,
		Month:      naming.Month,
		Day:        naming.Day,
		Source:     naming.Source,
		Resolution: naming.Resolution,
		Codec:      append([]string(nil), naming.Codecs...),
		Audio:      append([]string(nil), naming.Audio...),
		HDR:        append([]string(nil), naming.HDR...),
		Ext:        naming.Extension,
		Language:   append([]string(nil), naming.Languages...),
		Site:       naming.Site,
		Genre:      naming.Genre,
		Channels:   naming.Channels,
		Collection: naming.Collection,
		Region:     naming.Region,
		Size:       naming.Size,
		Group:      naming.Group,
		Disc:       naming.Disc,
		Season:     release.Episode.Season,
		Episode:    release.Episode.Episode,
		Edition:    append([]string(nil), naming.Editions...),
		Other:      append([]string(nil), naming.Other...),
	}
}

func manifestFiles(manifest api.SourceManifest) []string {
	files := make([]string, 0, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		if entry.Type == api.SourceEntryTypeFile || entry.Type == api.SourceEntryTypePlaylist {
			files = append(files, entry.Path)
		}
	}
	return files
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func cloneAnswers(source map[string]map[string]string) map[string]map[string]string {
	if source == nil {
		return nil
	}
	cloned := make(map[string]map[string]string, len(source))
	for tracker, answers := range source {
		inner := make(map[string]string, len(answers))
		maps.Copy(inner, answers)
		cloned[tracker] = inner
	}
	return cloned
}
