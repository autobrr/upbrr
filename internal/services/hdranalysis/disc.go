// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"errors"
	"strconv"
	"strings"

	"github.com/Audionut/go-hdr10-plus/extract"
	bridge "github.com/Audionut/go-hdr10-plus/integration/bdinfo"
	"github.com/autobrr/go-bdinfo/pkg/bdinfo/video"
)

// TimelineItem retains exact MPLS occurrence and primary stream/clock evidence.
type TimelineItem struct {
	Index               int
	In45                uint32
	Out45               uint32
	Offset45            uint64
	Duration45          uint64
	ConnectionCondition uint8
	Source              video.Source
	STCID               uint8
	STC                 video.STCSequence
	Mapping             video.Mapping
}

// DiscExtraction grants authority only to a fully collected ordinary primary HEVC timeline.
func DiscExtraction(outcome *bridge.Outcome, playlistName string) (Sidecar, error) {
	if outcome == nil || outcome.Report == nil {
		return Sidecar{}, extract.ErrIncomplete
	}
	if outcome.DeclinedAfterExhaustion != 0 || outcome.OmittedPlaylistResults != 0 {
		return Sidecar{}, extract.ErrResourceLimit
	}
	timelineFound := false
	timelineItems := make([]TimelineItem, 0)
	selectedSources := make(map[string]bool)
	collectedSources := make(map[string]bool)
	for _, timeline := range outcome.Report.Timelines {
		if !strings.EqualFold(timeline.Name, playlistName) {
			continue
		}
		timelineFound = true
		if !timeline.Complete || !timeline.Scanned || timeline.SelectedAngle != 0 || timeline.Err != nil {
			return Sidecar{}, errors.Join(extract.ErrUnsupportedInput, timeline.Err)
		}
		for _, item := range timeline.Items {
			if item.Err != nil || item.SelectedAlternative != 0 || len(item.Angles) == 0 {
				return Sidecar{}, extract.ErrUnsupportedInput
			}
			angle := item.Angles[0]
			if angle.Err != nil || angle.Source.Kind != "m2ts" || angle.STC == nil {
				return Sidecar{}, extract.ErrUnsupportedInput
			}
			primary := 0
			for _, mapping := range angle.Video {
				if mapping.Role == video.Primary {
					if mapping.Codec != video.HEVC || mapping.EntryType != 1 {
						return Sidecar{}, extract.ErrUnsupportedInput
					}
					primary++
					selectedSources[angle.Source.Path+"|"+strconv.Itoa(int(mapping.PID))] = true
					timelineItems = append(timelineItems, TimelineItem{
						Index:               item.Index,
						In45:                item.In45,
						Out45:               item.Out45,
						Offset45:            item.Offset45,
						Duration45:          item.Duration45,
						ConnectionCondition: item.ConnectionCondition,
						Source:              angle.Source,
						STCID:               angle.STCID,
						STC:                 *angle.STC,
						Mapping:             mapping,
					})
				}
			}
			if primary != 1 {
				return Sidecar{}, extract.ErrAmbiguousTrack
			}
		}
	}
	if !timelineFound {
		return Sidecar{}, extract.ErrIncomplete
	}
	if err := validateTimeline("primary_hevc_angle_zero", timelineItems); err != nil {
		return Sidecar{}, errors.Join(extract.ErrUnsupportedInput, err)
	}
	for _, collection := range outcome.Report.Collection {
		selected := false
		for _, occurrence := range collection.Info.Occurrences {
			if strings.EqualFold(occurrence.Playlist, playlistName) && occurrence.Mapping.Role == video.Primary {
				selected = true
			}
		}
		verifiedAbsence := collection.Status == video.Failed && errors.Is(collection.Err, extract.ErrNoMetadata)
		if selected &&
			(collection.Status != video.Complete && !verifiedAbsence || !collection.CleanEOF || collection.Err != nil && !errors.Is(collection.Err, extract.ErrNoMetadata)) {
			return Sidecar{}, errors.Join(extract.ErrIncomplete, collection.Err)
		}
		if selected {
			collectedSources[collection.Info.Source.Path+"|"+strconv.Itoa(int(collection.Info.PID))] = true
			for _, sourceError := range outcome.SourceErrors {
				if sourceError.Key.ClipID == collection.Info.Source.Path && sourceError.Key.TrackID == uint64(collection.Info.PID) &&
					!errors.Is(sourceError.Err, extract.ErrNoMetadata) {
					return Sidecar{}, sourceError.Err
				}
			}
		}
	}
	for key := range selectedSources {
		if !collectedSources[key] {
			return Sidecar{}, extract.ErrIncomplete
		}
	}
	for _, playlist := range outcome.Playlists {
		if !strings.EqualFold(playlist.Name, playlistName) {
			continue
		}
		if playlist.Err != nil {
			return Sidecar{Timeline: timelineItems}, playlist.Err
		}
		if err := ValidateExtraction(playlist.Extraction, false); err != nil {
			return Sidecar{}, err
		}
		return Sidecar{Extraction: playlist.Extraction, Timeline: timelineItems}, nil
	}
	return Sidecar{}, extract.ErrIncomplete
}
