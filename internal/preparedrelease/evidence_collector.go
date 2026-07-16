// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/externalidentity"
	"github.com/autobrr/upbrr/pkg/api"
)

// EvidencePipeline is the single private collection port owned by canonical
// preparation. Ordering and intermediate mutable evidence stay behind it.
type EvidencePipeline interface {
	CollectPreparationEvidence(context.Context, preparationstate.Request) (preparationstate.State, error)
}

// EvidenceCollector adapts the existing evidence pipeline to canonical grouped
// facts while canonical identity/persistence ownership moves out of metadata.
// It also supplies the resulting provider candidate to externalidentity.
type EvidenceCollector struct {
	pipeline EvidencePipeline
	mu       sync.Mutex
	pending  map[string]externalidentity.CandidateEvidence
}

// NewEvidenceCollector constructs the metadata evidence adapter.
func NewEvidenceCollector(pipeline EvidencePipeline) (*EvidenceCollector, error) {
	if pipeline == nil {
		return nil, errors.New("prepared release: metadata evidence pipeline is required")
	}
	return &EvidenceCollector{
		pipeline: pipeline,
		pending:  make(map[string]externalidentity.CandidateEvidence),
	}, nil
}

// Collect runs the canonical evidence sequence without identity persistence and
// maps its result into non-aliased fact groups.
func (c *EvidenceCollector) Collect(
	ctx context.Context,
	request preparationstate.Request,
) (CollectedFacts, error) {
	if c == nil || c.pipeline == nil {
		return CollectedFacts{}, errors.New("prepared release: evidence collector is not initialized")
	}
	meta, err := c.pipeline.CollectPreparationEvidence(ctx, request)
	if err != nil {
		return CollectedFacts{}, fmt.Errorf("prepared release: collect preparation evidence: %w", err)
	}
	if canonicalSourceKey(meta.SourcePath) != canonicalSourceKey(request.Manifest.SourcePath) {
		return CollectedFacts{}, fmt.Errorf("prepared release: collected source differs from manifest: %w", internalerrors.ErrInvalidInput)
	}
	meta.ProviderMetadata = cloneCollectedProviderMetadata(meta.ProviderMetadata)
	if err := applyBlurayFactInstruction(&meta, request.Input.Instructions.BlurayReleaseID); err != nil {
		return CollectedFacts{}, err
	}

	candidate := externalidentity.CandidateEvidence{
		Identity:   cloneCollectedIdentity(meta.Identity),
		Metadata:   cloneCollectedProviderMetadata(meta.ProviderMetadata),
		Candidates: cloneCollectedCandidates(meta.ExternalIdentityCandidates),
	}
	candidate.Identity.SourcePath = request.Manifest.SourcePath
	candidate.Metadata.SourcePath = request.Manifest.SourcePath
	c.mu.Lock()
	c.pending[canonicalSourceKey(request.Manifest.SourcePath)] = candidate
	c.mu.Unlock()

	return mapCollectedFacts(meta), nil
}

func applyBlurayFactInstruction(meta *preparationstate.State, releaseID string) error {
	releaseID = strings.TrimSpace(releaseID)
	if releaseID == "" {
		return nil
	}
	if meta == nil || meta.ProviderMetadata.Bluray == nil || !meta.ProviderMetadata.Bluray.SelectCandidate(releaseID, false, "manual") {
		return fmt.Errorf("prepared release: Blu-ray candidate %q: %w", releaseID, internalerrors.ErrNotFound)
	}
	candidate := meta.ProviderMetadata.Bluray.SelectedCandidate()
	if candidate == nil {
		return fmt.Errorf("prepared release: Blu-ray candidate %q: %w", releaseID, internalerrors.ErrNotFound)
	}
	if region := strings.TrimSpace(candidate.Region); region != "" {
		meta.Region = region
		meta.Release.Region = region
	}
	if publisher := strings.TrimSpace(candidate.Publisher); publisher != "" {
		meta.Distributor = strings.ToUpper(publisher)
	}
	return nil
}

// ResolveIdentityCandidate supplies and consumes the provider candidate built
// by the immediately preceding source-scoped collection.
func (c *EvidenceCollector) ResolveIdentityCandidate(
	_ context.Context,
	request externalidentity.Request,
) (externalidentity.CandidateEvidence, error) {
	if c == nil {
		return externalidentity.CandidateEvidence{}, internalerrors.ErrNotFound
	}
	key := canonicalSourceKey(request.SourcePath)
	c.mu.Lock()
	candidate, ok := c.pending[key]
	if ok {
		delete(c.pending, key)
	}
	c.mu.Unlock()
	if !ok {
		return externalidentity.CandidateEvidence{}, internalerrors.ErrNotFound
	}
	return candidate, nil
}

func mapCollectedFacts(meta preparationstate.State) CollectedFacts {
	namingStatus := api.NamingStatusComplete
	if len(meta.ReleaseNameMissing) > 0 {
		namingStatus = api.NamingStatusIncomplete
	}
	missing := make([]api.NamingRequirement, 0, len(meta.ReleaseNameMissing))
	for _, value := range meta.ReleaseNameMissing {
		if value = strings.TrimSpace(value); value != "" {
			missing = append(missing, api.NamingRequirement(value))
		}
	}
	assessments := meta.ReleaseAssessments()
	assessments.Naming = api.NamingAssessment{Status: namingStatus, Missing: missing}
	diagnostics := make([]api.PreparationDiagnostic, 0, len(meta.LookupWarnings))
	for _, warning := range meta.LookupWarnings {
		if warning = strings.TrimSpace(warning); warning != "" {
			diagnostics = append(diagnostics, api.PreparationDiagnostic{
				Code:     "source_lookup_warning",
				Severity: api.DiagnosticSeverityWarning,
				Message:  warning,
			})
		}
	}
	return CollectedFacts{
		Naming: api.NamingFacts{
			Filename:       meta.Filename,
			ReleaseName:    meta.ReleaseName,
			NameWithoutTag: meta.ReleaseNameNoTag,
			CleanName:      meta.ReleaseNameClean,
			Tag:            meta.Tag,
			Type:           meta.Release.Type,
			Artist:         meta.Release.Artist,
			Title:          meta.Release.Title,
			Subtitle:       meta.Release.Subtitle,
			AlternateTitle: meta.Release.Alt,
			Year:           meta.Release.Year,
			Month:          meta.Release.Month,
			Day:            meta.Release.Day,
			Source:         meta.Release.Source,
			Resolution:     meta.Release.Resolution,
			Codecs:         append([]string(nil), meta.Release.Codec...),
			Audio:          append([]string(nil), meta.Release.Audio...),
			HDR:            append([]string(nil), meta.Release.HDR...),
			Extension:      meta.Release.Ext,
			Languages:      append([]string(nil), meta.Release.Language...),
			Site:           meta.Release.Site,
			Genre:          meta.Release.Genre,
			Channels:       meta.Release.Channels,
			Collection:     meta.Release.Collection,
			Region:         meta.Release.Region,
			Size:           meta.Release.Size,
			Group:          meta.Release.Group,
			Disc:           meta.Release.Disc,
			Editions:       append([]string(nil), meta.Release.Edition...),
			Other:          append([]string(nil), meta.Release.Other...),
			Scene:          meta.Scene,
			SceneName:      meta.SceneName,
			Personal:       meta.PersonalRelease,
		},
		Episode: api.EpisodeFacts{
			Season:            meta.SeasonInt,
			Episode:           meta.EpisodeInt,
			SeasonLabel:       meta.SeasonStr,
			EpisodeLabel:      meta.EpisodeStr,
			DailyDate:         meta.DailyEpisodeDate,
			Pack:              meta.TVPack,
			Title:             meta.EpisodeTitle,
			Overview:          meta.EpisodeOverview,
			Year:              meta.EpisodeYear,
			AiredDate:         meta.TVDBAiredDate,
			AirDays:           append([]string(nil), meta.TVDBAirsDays...),
			AirTime:           meta.TVDBAirsTime,
			AirTimezone:       meta.TVDBAirsTimezone,
			AirTimezoneSource: meta.TVDBAirsTimezoneSource,
			DateMatched:       meta.TMDBDateMatch,
		},
		Media: api.MediaFacts{
			AudioLanguages:    append([]string(nil), meta.AudioLanguages...),
			SubtitleLanguages: append([]string(nil), meta.SubtitleLanguages...),
			Container:         meta.Container,
			Audio:             meta.Audio,
			Channels:          meta.Channels,
			Commentary:        meta.HasCommentary,
			ThreeD:            meta.Is3D,
			Source:            meta.Source,
			Type:              meta.Type,
			UHD:               meta.UHD,
			HDR:               meta.HDR,
			Distributor:       meta.Distributor,
			Region:            meta.Region,
			VideoCodec:        meta.VideoCodec,
			VideoEncode:       meta.VideoEncode,
			HasEncodeSettings: meta.HasEncodeSettings,
			BitDepth:          meta.BitDepth,
			Edition:           meta.Edition,
			Repack:            meta.Repack,
			WebDV:             meta.WebDV,
			StreamOptimized:   meta.StreamOptimized,
			Service:           meta.Service,
			ServiceLongName:   meta.ServiceLongName,
			MediaInfoUniqueID: meta.MediaInfoUniqueID,
			Anime:             meta.Anime,
		},
		Disc: api.DiscFacts{
			Type:            meta.DiscType,
			Summary:         collectedBDInfoSummary(meta.BDInfo),
			DurationSeconds: collectedBDInfoDurationSeconds(meta.BDInfo),
			PlaylistCount:   len(meta.SelectedBDMVPlaylists),
			DVDVOBSet:       meta.DVDVOBSet,
		},
		Assessments: assessments,
		Identity: externalidentity.ResolutionIntent{
			Title:   meta.Release.Title,
			Year:    meta.Release.Year,
			Season:  meta.SeasonInt,
			Episode: meta.EpisodeInt,
		},
		Diagnostics: diagnostics,
		Resources: CollectedResources{
			SourcePath:            firstCollectedSourcePath(meta.Paths),
			VideoPath:             meta.VideoPath,
			FileList:              append([]string(nil), meta.FileList...),
			MediaInfoJSONPath:     meta.MediaInfoJSONPath,
			MediaInfoTextPath:     meta.MediaInfoTextPath,
			DVDIFOPath:            meta.DVDIFOPath,
			DVDVOBPath:            meta.DVDVOBPath,
			DVDVOBMediaInfoJSON:   meta.DVDVOBMediaInfoJSON,
			DVDVOBMediaInfoText:   meta.DVDVOBMediaInfoText,
			SceneNFOPath:          meta.SceneNFOPath,
			DescriptionTemplate:   meta.DescriptionTemplate,
			SelectedBDMVPlaylists: clonePlaylists(meta.SelectedBDMVPlaylists),
		},
	}
}

func firstCollectedSourcePath(paths []string) string {
	for _, sourcePath := range paths {
		if sourcePath = strings.TrimSpace(sourcePath); sourcePath != "" {
			return sourcePath
		}
	}
	return ""
}

func collectedBDInfoSummary(value map[string]any) string {
	summary, _ := value["summary"].(string)
	return strings.TrimSpace(summary)
}

func collectedBDInfoDurationSeconds(value map[string]any) float64 {
	text := strings.TrimSpace(fmt.Sprint(value["length"]))
	if text == "" || text == "<nil>" {
		return 0
	}
	parts := strings.Split(text, ":")
	if len(parts) != 3 {
		return 0
	}
	hours, hoursErr := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	minutes, minutesErr := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	seconds, secondsErr := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
	if hoursErr != nil || minutesErr != nil || secondsErr != nil || hours < 0 || minutes < 0 || seconds < 0 {
		return 0
	}
	return hours*3600 + minutes*60 + seconds
}

func cloneCollectedIdentity(value api.ExternalIdentity) api.ExternalIdentity {
	return value
}

func cloneCollectedProviderMetadata(value api.SourceScopedMetadata) api.SourceScopedMetadata {
	cloned, err := cloneWithJSON(value)
	if err != nil {
		panic(err)
	}
	return cloned
}

func cloneCollectedCandidates(value []api.ExternalIdentityCandidate) []api.ExternalIdentityCandidate {
	cloned, err := cloneWithJSON(value)
	if err != nil {
		panic(err)
	}
	return cloned
}

func clonePlaylists(value []api.PlaylistInfo) []api.PlaylistInfo {
	cloned, err := cloneWithJSON(value)
	if err != nil {
		panic(err)
	}
	return cloned
}
