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

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

// ResolveUploadSubject validates and projects one exact prepared generation
// into the upload module's operation-owned read model.
func (m *Module) ResolveUploadSubject(ctx context.Context, input api.UploadSubjectInput) (api.UploadSubject, error) {
	owned, err := m.resolveEnvelope(ctx, input.Release)
	if err != nil {
		return api.UploadSubject{}, err
	}
	release := owned.result.Release
	resources := owned.resources
	binding, err := preparedMediaBinding(release)
	if err != nil {
		return api.UploadSubject{}, err
	}
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
		SourceIdentity:              release.SourceIdentity,
		EffectiveMetadata:           release.MetadataFacts(),
		ManualLanguages:             release.Media.ManualLanguages(),
		HardcodedSubs:               release.Media.HardcodedSubs,
		HardcodedSubtitleLanguages:  append([]string(nil), release.Media.HardcodedSubtitleLanguages...),
		MediaBinding:                binding,
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
		DescriptionOverride:         input.DescriptionOverride,
		DescriptionGroupsFinal:      input.DescriptionGroupsFinal,
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
		Discs:                       projectDiscResources(resources.discs),
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
		HDRFacts:                    release.Media.HDRFacts,
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
		AlternateTitle:              release.Naming.AlternateTitle,
		NamePresentation:            release.Naming.NamePresentation,
		GeneratedName:               release.Naming.GeneratedName.Clone(),
		GeneratedReleaseNames:       release.Naming.GeneratedReleaseNames,
		ArrReleaseGroup:             release.Naming.Group,
		InfoHash:                    resources.clientEvidence.Result.InfoHash,
		ClientTorrentPath:           resources.clientEvidence.Result.TorrentPath,
		ClientTorrentDataVerified:   resources.clientEvidence.Result.TorrentDataVerified,
		TrackerIDs:                  maps.Clone(resources.clientEvidence.Result.TrackerIDs),
		MatchedTrackers:             append([]string(nil), resources.clientEvidence.Result.MatchedTrackers...),
	}
	if override := owned.result.EffectiveInstructions.Metadata.PersonalRelease; override != nil {
		value := *override
		subject.PersonalReleaseOverride = &value
	}
	cloned, err := cloneWithJSON(subject)
	if err != nil {
		return api.UploadSubject{}, fmt.Errorf("prepared release: clone upload subject: %w", err)
	}
	cloned.SourceIdentity = api.SourceContentIdentity{
		Version:             subject.SourceIdentity.Version,
		Digest:              subject.SourceIdentity.Digest,
		ManifestFingerprint: subject.SourceIdentity.ManifestFingerprint,
		Files:               append([]api.VerifiedSourceFile(nil), subject.SourceIdentity.Files...),
	}
	cloned.SourceManifest = release.Source
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
		EffectiveMetadata: release.MetadataFacts(),
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
		Disc:              release.Disc,
		Type:              release.Media.Type,
		Source:            release.Media.Source,
		Tag:               release.Naming.Tag,
		HDR:               release.Media.HDR,
		HDRFacts:          release.Media.HDRFacts,
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
	binding, err := preparedMediaBinding(release)
	if err != nil {
		return api.DVDMenuSubject{}, err
	}
	discs := make([]api.DVDMenuDiscSubject, 0, len(owned.resources.discs))
	for _, disc := range owned.resources.discs {
		if disc.Type == "DVD" {
			discs = append(discs, api.DVDMenuDiscSubject{
				ID:   disc.ID,
				Name: disc.Name,
				Root: disc.Root,
			})
		}
	}
	return api.DVDMenuSubject{
		MediaBinding: binding,
		SourcePath:   release.Source.SourcePath,
		DiscType:     firstNonEmpty(release.Disc.Type, release.Source.Classification.DiscType),
		Discs:        discs,
	}, nil
}

// ResolveAudioAnalysisSubject validates one exact generation and projects the
// private decode path together with its authoritative stable audio inventory.
func (m *Module) ResolveAudioAnalysisSubject(
	ctx context.Context,
	instructions api.AudioAnalysisInstructions,
) (api.AudioAnalysisSubject, error) {
	normalized, err := instructions.Normalize()
	if err != nil {
		return api.AudioAnalysisSubject{}, fmt.Errorf("prepared release: audio analysis instructions: %w", err)
	}
	owned, err := m.resolveEnvelope(ctx, normalized.Release)
	if err != nil {
		if _, ok := errors.AsType[*StalePreparationError](err); ok {
			return api.AudioAnalysisSubject{}, fmt.Errorf("resolve audio-analysis source: %w", api.NewAudioAnalysisError(api.AudioAnalysisFailure{
				Code:    api.AudioAnalysisFailureStaleSource,
				Message: "the prepared audio source changed and must be refreshed",
			}, err))
		}
		return api.AudioAnalysisSubject{}, err
	}
	release := owned.result.Release
	if !release.Media.TrackCoverageComplete {
		cause := &IncompatiblePreparationError{
			SourcePath: release.Source.SourcePath,
			Reason:     "audio stream mapping is not authoritative for this source",
		}
		return api.AudioAnalysisSubject{}, fmt.Errorf("resolve audio-analysis subject: %w", api.NewAudioAnalysisError(api.AudioAnalysisFailure{
			Code:    api.AudioAnalysisFailureAmbiguousBinding,
			Message: "prepared audio stream mapping is not authoritative",
		}, cause))
	}
	tracks := make([]api.MediaTrackFacts, 0)
	manifestFingerprint := ""
	for _, track := range release.Media.Tracks {
		if track.Kind != api.MediaTrackAudio || track.ResourceID != normalized.ResourceID {
			continue
		}
		if manifestFingerprint == "" {
			manifestFingerprint = track.ManifestFingerprint
		}
		if track.ManifestFingerprint != manifestFingerprint {
			cause := &IncompatiblePreparationError{
				SourcePath: release.Source.SourcePath,
				Reason:     "audio track manifest is inconsistent",
			}
			return api.AudioAnalysisSubject{}, fmt.Errorf("resolve audio-analysis manifest: %w", api.NewAudioAnalysisError(api.AudioAnalysisFailure{
				Code:    api.AudioAnalysisFailureStaleSource,
				Message: "prepared audio facts no longer match one source manifest",
			}, cause))
		}
		cloned := track
		cloned.DetectedLanguages = append([]string(nil), track.DetectedLanguages...)
		cloned.Languages = append([]string(nil), track.Languages...)
		tracks = append(tracks, cloned)
	}
	if len(tracks) == 0 {
		cause := &IncompatiblePreparationError{
			SourcePath: release.Source.SourcePath,
			Reason:     "prepared resource has no audio tracks",
		}
		return api.AudioAnalysisSubject{}, fmt.Errorf("resolve audio-analysis tracks: %w", api.NewAudioAnalysisError(api.AudioAnalysisFailure{
			Code:    api.AudioAnalysisFailureNoAudio,
			Message: "the prepared resource has no audio tracks",
		}, cause))
	}
	if err := validateAudioAnalysisSelection(normalized, tracks, release.Media.PrimaryAudioTrackID); err != nil {
		cause := &IncompatiblePreparationError{SourcePath: release.Source.SourcePath, Reason: err.Error()}
		return api.AudioAnalysisSubject{}, fmt.Errorf("validate audio-analysis selection: %w", api.NewAudioAnalysisError(api.AudioAnalysisFailure{
			Code:    api.AudioAnalysisFailureInvalidSelection,
			Message: "the requested audio-track selection is invalid",
		}, cause))
	}
	videoPath := strings.TrimSpace(owned.resources.videoPath)
	if videoPath == "" {
		cause := &IncompatiblePreparationError{
			SourcePath: release.Source.SourcePath,
			Reason:     "prepared resource has no decodable media path",
		}
		return api.AudioAnalysisSubject{}, fmt.Errorf("resolve audio-analysis media path: %w", api.NewAudioAnalysisError(api.AudioAnalysisFailure{
			Code:    api.AudioAnalysisFailureUnsupportedSource,
			Message: "the prepared resource has no decodable media path",
		}, cause))
	}
	return api.AudioAnalysisSubject{
		Release:             normalized.Release,
		SourcePath:          release.Source.SourcePath,
		VideoPath:           videoPath,
		SourceFingerprint:   release.Compatibility.SourceFingerprint,
		ResourceID:          normalized.ResourceID,
		ManifestFingerprint: manifestFingerprint,
		PrimaryTrackID:      release.Media.PrimaryAudioTrackID,
		Tracks:              tracks,
	}, nil
}

func validateAudioAnalysisSelection(instructions api.AudioAnalysisInstructions, tracks []api.MediaTrackFacts, primaryTrackID string) error {
	byID := make(map[string]int, len(tracks))
	for index, track := range tracks {
		byID[track.ID] = index
	}
	lastIndex := -1
	for _, trackID := range instructions.TrackIDs {
		index, ok := byID[trackID]
		if !ok {
			return errors.New("audio analysis selection contains an unknown track")
		}
		if index <= lastIndex {
			return errors.New("audio analysis tracks must use prepared source order")
		}
		lastIndex = index
	}
	switch instructions.Selection {
	case api.AudioAnalysisSelectionPrimary:
		if strings.TrimSpace(primaryTrackID) == "" || len(instructions.TrackIDs) != 1 || instructions.TrackIDs[0] != primaryTrackID {
			return errors.New("primary audio track is ambiguous")
		}
	case api.AudioAnalysisSelectionAll:
		if len(instructions.TrackIDs) != len(tracks) {
			return errors.New("all audio tracks must be selected")
		}
		for index := range tracks {
			if instructions.TrackIDs[index] != tracks[index].ID {
				return errors.New("all audio tracks must use prepared source order")
			}
		}
	case api.AudioAnalysisSelectionSelected:
	}
	return nil
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
	binding, err := preparedMediaBinding(release)
	if err != nil {
		return api.ScreenshotSubject{}, err
	}
	selectedPlaylists := clonePreparedPlaylists(resources.selectedBDMVPlaylists)
	if len(selectedPlaylists) == 0 {
		selectedPlaylists = clonePreparedPlaylists(release.Source.SelectedPlaylists)
	}
	discs := make([]api.ScreenshotDiscSubject, 0, len(resources.discs))
	for _, disc := range resources.discs {
		discs = append(discs, api.ScreenshotDiscSubject{
			ID:                    disc.ID,
			Name:                  disc.Name,
			Type:                  disc.Type,
			Root:                  disc.Root,
			VideoPath:             disc.VideoPath,
			MediaInfoJSONPath:     disc.MediaInfoJSONPath,
			SelectedBDMVPlaylists: clonePreparedPlaylists(disc.SelectedPlaylists),
		})
	}
	return api.ScreenshotSubject{
		MediaBinding:          binding,
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
		Discs:                 discs,
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
	binding, err := preparedMediaBinding(release)
	if err != nil {
		return api.ImageHostingSubject{}, err
	}
	galleryName := firstNonEmpty(
		release.Naming.ReleaseName,
		release.Naming.NameWithoutTag,
		release.Naming.Title,
		release.Naming.Filename,
		filepath.Base(release.Source.SourcePath),
	)
	discs := make([]api.ImageHostingDiscSubject, 0, len(release.Disc.Items))
	for _, disc := range release.Disc.Items {
		discs = append(discs, api.ImageHostingDiscSubject{ID: disc.ID, Name: disc.Name})
	}
	return api.ImageHostingSubject{
		MediaBinding: binding,
		SourcePath:   release.Source.SourcePath,
		GalleryName:  galleryName,
		Discs:        discs,
	}, nil
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
	binding, err := preparedMediaBinding(release)
	if err != nil {
		return api.DescriptionSubject{}, err
	}
	selectedPlaylists := clonePreparedPlaylists(resources.selectedBDMVPlaylists)
	if len(selectedPlaylists) == 0 {
		selectedPlaylists = clonePreparedPlaylists(release.Source.SelectedPlaylists)
	}
	return api.DescriptionSubject{
		EffectiveMetadata:          release.MetadataFacts(),
		ManualLanguages:            release.Media.ManualLanguages(),
		HardcodedSubs:              release.Media.HardcodedSubs,
		HardcodedSubtitleLanguages: append([]string(nil), release.Media.HardcodedSubtitleLanguages...),
		MediaBinding:               binding,
		SourcePath:                 release.Source.SourcePath,
		DiscType:                   firstNonEmpty(release.Disc.Type, release.Source.Classification.DiscType),
		MediaInfoTextPath:          resources.mediaInfoTextPath,
		DVDVOBMediaInfoText:        resources.dvdVOBMediaInfoText,
		EpisodeOverview:            release.Episode.Overview,
		DescriptionTemplate:        resources.descriptionTemplate,
		Options:                    input.Options,
		Release:                    releaseInfo(release),
		SelectedBDMVPlaylists:      selectedPlaylists,
		Disc:                       release.Disc,
		Discs:                      projectDiscResources(resources.discs),
		Tag:                        release.Naming.Tag,
		Identity:                   release.Identity,
		ProviderMetadata:           release.ProviderMetadata,
		SeasonInt:                  release.Episode.Season,
		EpisodeInt:                 release.Episode.Episode,
		Filename:                   release.Naming.Filename,
		ReleaseName:                release.Naming.ReleaseName,
		ReleaseNameNoTag:           release.Naming.NameWithoutTag,
		ServiceLongName:            release.Media.ServiceLongName,
		Type:                       release.Media.Type,
		HDR:                        release.Media.HDR,
		ArrReleaseGroup:            release.Naming.Group,
	}, nil
}

func projectDiscResources(resources []preparationstate.DiscResource) []api.DiscEvidenceResource {
	result := make([]api.DiscEvidenceResource, 0, len(resources))
	for _, resource := range resources {
		reports := make([]api.DiscReportResource, 0, len(resource.Reports))
		for _, report := range resource.Reports {
			reports = append(reports, api.DiscReportResource{
				Playlist:        report.Playlist,
				Summary:         report.Summary,
				ExtSummary:      report.ExtSummary,
				FullSummary:     report.FullSummary,
				SummaryPath:     report.SummaryPath,
				ExtSummaryPath:  report.ExtSummaryPath,
				FullSummaryPath: report.FullSummaryPath,
			})
		}
		result = append(result, api.DiscEvidenceResource{
			ID:                  resource.ID,
			Name:                resource.Name,
			Type:                resource.Type,
			Root:                resource.Root,
			SelectedPlaylists:   clonePreparedPlaylists(resource.SelectedPlaylists),
			VideoPath:           resource.VideoPath,
			FileList:            append([]string(nil), resource.FileList...),
			Reports:             reports,
			MediaInfoJSONPath:   resource.MediaInfoJSONPath,
			MediaInfoTextPath:   resource.MediaInfoTextPath,
			DVDIFOPath:          resource.DVDIFOPath,
			DVDVOBPath:          resource.DVDVOBPath,
			DVDVOBSet:           resource.DVDVOBSet,
			DurationSeconds:     resource.DurationSeconds,
			DVDVOBMediaInfoJSON: resource.DVDVOBMediaInfoJSON,
			DVDVOBMediaInfoText: resource.DVDVOBMediaInfoText,
		})
	}
	return result
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
		Category:   releaseInfoCategory(release.Identity.Category),
		Type:       release.Media.Type,
		Artist:     naming.Artist,
		Title:      naming.Title,
		Subtitle:   naming.Subtitle,
		Alt:        naming.AlternateTitle,
		Year:       naming.Year,
		Month:      naming.Month,
		Day:        naming.Day,
		Version:    naming.Version,
		Source:     release.Media.Source,
		Resolution: naming.Resolution,
		Codec:      append([]string(nil), naming.Codecs...),
		Audio:      append([]string(nil), naming.Audio...),
		HDR:        append([]string(nil), naming.HDR...),
		Ext:        naming.Extension,
		Language:   append([]string(nil), naming.Languages...),
		Site:       naming.Site,
		Genre:      naming.Genre,
		Channels:   release.Media.Channels,
		Collection: naming.Collection,
		Region:     release.Media.Region,
		Size:       naming.Size,
		Group:      naming.Group,
		Disc:       naming.Disc,
		Season:     release.Episode.Season,
		Episode:    release.Episode.Episode,
		Edition:    singletonFact(release.Media.Edition),
		Other:      append([]string(nil), naming.Other...),
	}
}

func releaseInfoCategory(category api.CanonicalCategory) string {
	switch category {
	case api.CanonicalCategoryMovie:
		return string(api.CategoryMovie)
	case api.CanonicalCategoryTV:
		return string(api.CategoryTV)
	case api.CanonicalCategoryUnknown:
		return ""
	default:
		return ""
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
