// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"errors"
	"strconv"
	"strings"

	"github.com/Audionut/go-hdr10-plus/extract"
	bridge "github.com/Audionut/go-hdr10-plus/integration/bdinfo"
	bd "github.com/autobrr/go-bdinfo/pkg/bdinfo"
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

// DiscExtraction grants authority only to a fully collected default-angle primary HEVC timeline.
func DiscExtraction(outcome *bridge.Outcome, playlistName string) (Sidecar, error) {
	return discExtractionWithRetained(outcome, playlistName, 0)
}

func discExtractionWithRetained(outcome *bridge.Outcome, playlistName string, otherRetainedBytes int64) (Sidecar, error) {
	if outcome == nil || outcome.Report == nil {
		return Sidecar{}, extract.ErrIncomplete
	}
	if outcome.DeclinedAfterExhaustion != 0 || outcome.OmittedPlaylistResults != 0 {
		return Sidecar{}, extract.ErrResourceLimit
	}
	timelineItems, selectedSources, err := discTimeline(outcome.Report, playlistName)
	if err != nil {
		return Sidecar{}, err
	}
	collectedSources := make(map[string]bool)
	for _, collection := range outcome.Report.Collection {
		key := collection.Info.Source.Path + "|" + strconv.Itoa(int(collection.Info.PID))
		if !selectedSources[key] {
			continue
		}
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
			collectedSources[key] = true
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
	otherRetainedBytes += producerBytes
	for _, playlist := range outcome.Playlists {
		if !strings.EqualFold(playlist.Name, playlistName) {
			otherRetainedBytes += playlist.Extraction.RetainedBytes()
		}
	}
	for _, playlist := range outcome.Playlists {
		if !strings.EqualFold(playlist.Name, playlistName) {
			continue
		}
		if playlist.Err != nil {
			return Sidecar{Timeline: timelineItems}, playlist.Err
		}
		if err := validateExtractionWithRetained(playlist.Extraction, false, false, otherRetainedBytes); err != nil {
			return Sidecar{}, err
		}
		return Sidecar{Extraction: playlist.Extraction, Timeline: timelineItems}, nil
	}
	return Sidecar{}, extract.ErrIncomplete
}

func discTimeline(report *bd.Result, playlistName string) ([]TimelineItem, map[string]bool, error) {
	timelineFound := false
	timelineItems := make([]TimelineItem, 0)
	selectedSources := make(map[string]bool)

	for _, timeline := range report.Timelines {
		if !strings.EqualFold(timeline.Name, playlistName) {
			continue
		}
		timelineFound = true
		if !timeline.Complete || !timeline.Scanned || timeline.Err != nil {
			return nil, nil, errors.Join(extract.ErrUnsupportedInput, timeline.Err)
		}
		for _, item := range timeline.Items {
			if item.Err != nil || len(item.Angles) == 0 {
				return nil, nil, extract.ErrUnsupportedInput
			}
			angle := item.Angles[0]
			if angle.Err != nil || angle.Source.Kind != "m2ts" || angle.STC == nil {
				return nil, nil, extract.ErrUnsupportedInput
			}
			primary := 0
			for _, mapping := range angle.Video {
				if mapping.Role == video.Primary {
					if mapping.Codec != video.HEVC || mapping.EntryType != 1 {
						return nil, nil, extract.ErrUnsupportedInput
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
				return nil, nil, extract.ErrAmbiguousTrack
			}
		}
	}
	if !timelineFound {
		return nil, nil, extract.ErrIncomplete
	}
	if err := validateTimeline("primary_hevc_angle_zero", timelineItems); err != nil {
		return nil, nil, errors.Join(extract.ErrUnsupportedInput, err)
	}
	return timelineItems, selectedSources, nil
}
