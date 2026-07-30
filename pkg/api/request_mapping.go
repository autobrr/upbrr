// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"errors"
	"fmt"
	"maps"
	"strings"
)

// ErrPreparationSourceRequired reports a request without a preparation source.
var ErrPreparationSourceRequired = errors.New("preparation source path is required")

// MapPreparationRequest maps one shared operation request to detached canonical
// preparation input. It performs no filesystem access or entrypoint-specific
// policy.
func MapPreparationRequest(request Request, intent PreparationIntent) (PrepareInput, error) {
	sourcePath := strings.TrimSpace(request.SourcePath)
	if sourcePath == "" {
		return PrepareInput{}, ErrPreparationSourceRequired
	}

	input := PrepareInput{
		SourcePath: sourcePath,
		Intent:     intent,
		Instructions: ReleaseFactInstructions{
			Identity:     cloneExternalIDOverrides(request.ExternalIDOverrides),
			ReleaseName:  cloneReleaseNameOverrides(request.ReleaseNameOverrides),
			Metadata:     cloneMetadataOverrides(request.MetadataOverrides),
			SourceLookup: strings.TrimSpace(request.SourceLookupURL),
			TrackerIDs:   cloneStringMap(request.TrackerIDOverrides),
			Playlist: PlaylistInstruction{
				Set:      request.PlaylistInstruction.Set,
				Selected: append([]string(nil), request.PlaylistInstruction.Selected...),
				UseAll:   request.PlaylistInstruction.UseAll,
			},
		},
		Policy: PreparationPolicy{
			KeepFolder: request.Options.KeepFolder,
			KeepImages: request.Options.KeepImages,
			OnlyID:     request.Options.OnlyID,
		},
		Search: ClientSearchPolicy{
			Skip:   request.Options.SkipAutoTorrent,
			Client: cloneString(request.ClientOverrides.Client),
		},
		Controls: PreparationControls{
			Interaction:       request.Options.InteractionMode,
			ConfirmBDMVRescan: request.ConfirmBDMVRescan,
			ForceRecheck:      cloneBool(request.ClientOverrides.ForceRecheck),
		},
	}
	if request.ReleaseNameOverrides.Category != nil {
		category, err := NormalizeCanonicalCategory(*request.ReleaseNameOverrides.Category)
		if err != nil {
			return PrepareInput{}, fmt.Errorf("canonical category instruction: %w", err)
		}
		input.Instructions.Category = &category
	}
	return input, nil
}

// cloneStringMap detaches caller-owned map storage before request normalization.
func cloneStringMap(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	cloned := make(map[string]string, len(value))
	maps.Copy(cloned, value)
	return cloned
}

func cloneExternalIDOverrides(value ExternalIDOverrides) ExternalIDOverrides {
	return ExternalIDOverrides{
		TMDBID:   cloneInt(value.TMDBID),
		IMDBID:   cloneInt(value.IMDBID),
		TVDBID:   cloneInt(value.TVDBID),
		TVmazeID: cloneInt(value.TVmazeID),
		MALID:    cloneInt(value.MALID),
	}
}

func cloneReleaseNameOverrides(value ReleaseNameOverrides) ReleaseNameOverrides {
	return ReleaseNameOverrides{
		Category:         cloneString(value.Category),
		Type:             cloneString(value.Type),
		Source:           cloneString(value.Source),
		Resolution:       cloneString(value.Resolution),
		Tag:              cloneString(value.Tag),
		Service:          cloneString(value.Service),
		Edition:          cloneString(value.Edition),
		Season:           cloneString(value.Season),
		Episode:          cloneString(value.Episode),
		EpisodeTitle:     cloneString(value.EpisodeTitle),
		ManualYear:       cloneInt(value.ManualYear),
		ManualDate:       cloneString(value.ManualDate),
		UseSeasonEpisode: cloneBool(value.UseSeasonEpisode),
		NoSeason:         cloneBool(value.NoSeason),
		NoYear:           cloneBool(value.NoYear),
		NoAKA:            cloneBool(value.NoAKA),
		NoTag:            cloneBool(value.NoTag),
		NoEdition:        cloneBool(value.NoEdition),
		NoDub:            cloneBool(value.NoDub),
		NoDual:           cloneBool(value.NoDual),
		DualAudio:        cloneBool(value.DualAudio),
		Region:           cloneString(value.Region),
	}
}

func cloneMetadataOverrides(value MetadataOverrides) MetadataOverrides {
	return MetadataOverrides{
		Distributor:      cloneString(value.Distributor),
		OriginalLanguage: cloneString(value.OriginalLanguage),
		PersonalRelease:  cloneBool(value.PersonalRelease),
		Commentary:       cloneBool(value.Commentary),
		WebDV:            cloneBool(value.WebDV),
		StreamOptimized:  cloneBool(value.StreamOptimized),
		Anime:            cloneBool(value.Anime),
	}
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
