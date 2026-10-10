// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"regexp"
	"slices"
	"strings"

	"github.com/autobrr/rls"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func duplicatePolicy() *trackers.DupePolicy {
	return &trackers.DupePolicy{
		ID:                 "ant/duplicate/v5",
		EvidenceID:         "ant-slots-staff-overrides",
		SearchScope:        trackers.DupeSearchScope{MaxPages: 100},
		TargetSlot:         resolveTargetSlot,
		CompareSlots:       compareSlots,
		StaffTokenOverride: true,
	}
}

func resolveTargetSlot(meta api.UploadSubject) string {
	kind := strings.ToUpper(strings.TrimSpace(meta.Type))
	if fullDisc(meta) {
		kind = "DISC"
	}
	return antSlot(kind, meta.Source, meta.Release.Resolution, meta.VideoCodec, resolveFlags(meta), discCountry(meta.Region), true)
}

// antSlot follows ANT's kind priority and exact edition-flag combination.
// Unknown components remain '?' so independently proved variants can coexist.
func antSlot(kind, source, resolution, codec string, flags []string, country string, flagsComplete bool) string {
	tier := "?"
	switch strings.ToLower(strings.TrimSpace(resolution)) {
	case "480p", "480i", "576p", "576i", "sd":
		tier = "SD"
	case "720p":
		tier = "720"
	case "1080p", "1080i":
		tier = "1080"
	case "2160p":
		tier = "2160"
	}
	upperFlags, known := slotFlags(flags)
	known = known && flagsComplete
	slotKind := "?"
	switch {
	case !known:
	case slices.Contains(upperFlags, "3D"):
		if tier != "1080" {
			return ""
		}
		slotKind = "3D"
	case strings.EqualFold(kind, "REMUX") || slices.Contains(upperFlags, "REMUX"):
		slotKind = "Remux"
	case strings.EqualFold(kind, "DISC"):
		if country == "" {
			country = "?"
		}
		slotKind = "FullDisc"
	case strings.EqualFold(codec, "AV1"):
		slotKind = "AV1"
	case strings.TrimSpace(codec) == "":
	case resolveMediaSource(source) == "WEB":
		slotKind = "WEB"
	case source != "" && resolveMediaSource(source) != "Unknown" && resolveMediaSource(source) != "Other":
		slotKind = "Encode"
	}
	rangeSlot := ""
	switch slotKind {
	case "?":
		rangeSlot = "?"
	case "Encode", "WEB":
		hdr := slices.Contains(upperFlags, "HDR10")
		dv := slices.Contains(upperFlags, "DV")
		rangeSlot = "SDR"
		switch {
		case tier == "?":
			rangeSlot = "?"
		case tier == "1080" && (hdr || dv):
			rangeSlot = "HDR"
		case tier == "2160":
			switch {
			case hdr && dv:
				rangeSlot = "DV+HDR"
			case dv:
				rangeSlot = "DV"
			case hdr:
				rangeSlot = "HDR"
			}
		}
	}
	if slotKind != "FullDisc" {
		country = ""
	}
	editions := make([]string, 0, 6)
	for _, flag := range []string{"DIRECTORS", "EXTENDED", "UNCUT", "UNRATED", "IMAX", "CRITERION", "4KREMASTER"} {
		if slices.Contains(upperFlags, flag) {
			editions = append(editions, flag)
		}
	}
	if !known {
		editions = []string{"?"}
	}
	return strings.Join([]string{slotKind, tier, rangeSlot, country, strings.Join(editions, "+")}, "/")
}

func slotFlags(flags []string) ([]string, bool) {
	upperFlags := make([]string, 0, len(flags))
	for _, flag := range flags {
		upper := strings.ToUpper(flag)
		switch upper {
		case "DIRECTORS",
			"EXTENDED",
			"UNCUT",
			"UNRATED",
			"IMAX",
			"CRITERION",
			"4KREMASTER",
			"HDR10",
			"DV",
			"ATMOS",
			"ENGLISHDUB",
			"DUALAUDIO",
			"COMMENTARY",
			"REMUX",
			"3D":
			upperFlags = append(upperFlags, upper)
		default:
			return nil, false
		}
	}
	return upperFlags, true
}

func compareSlots(target, candidate string, targetNames []string, candidateName string) api.DupeRelation {
	proposed, existing := strings.Split(target, "/"), strings.Split(candidate, "/")
	if len(proposed) != 5 || len(existing) != 5 {
		return api.DupeRelationManualReview
	}
	for _, name := range targetNames {
		if slotContradictsTitle(proposed, name) {
			return api.DupeRelationManualReview
		}
	}
	if slotContradictsTitle(existing, candidateName) {
		return api.DupeRelationManualReview
	}
	if proposed[1] != "?" && existing[1] != "?" && proposed[1] != existing[1] {
		return api.DupeRelationCoexists
	}
	if proposed[0] == "?" || existing[0] == "?" {
		return api.DupeRelationManualReview
	}
	if proposed[0] == "WEB" && existing[0] == "Encode" {
		existing[0] = "WEB"
		if proposed[4] == "" {
			existing[4] = ""
		}
	} else if proposed[0] != existing[0] {
		return api.DupeRelationCoexists
	}
	unknown := false
	for index := range proposed {
		if proposed[index] == "?" || existing[index] == "?" {
			unknown = true
		} else if proposed[index] != existing[index] {
			return api.DupeRelationCoexists
		}
	}
	if unknown {
		return api.DupeRelationManualReview
	}
	return api.DupeRelationSameSlot
}

var title4KRemaster = regexp.MustCompile(`(?i)\b4k[ ._-]*remaster(?:ed)?\b`)

// A positive bounded title marker must be present in complete native flags.
// Missing title markers and incomplete flags cannot establish a contradiction.
func slotContradictsTitle(slot []string, name string) bool {
	if slot[4] == "?" {
		return false
	}
	metadata, bounded := dupe.TrackerTitleMetadata(rls.ParseString(name))
	if !bounded {
		return false
	}
	tags, _ := rls.ParseTagsString(metadata)
	var editions []string
	meta := api.UploadSubject{}
	for _, tag := range tags {
		switch {
		case tag.Is(rls.TagTypeCut, rls.TagTypeEdition, rls.TagTypeCollection):
			editions = append(editions, strings.ReplaceAll(tag.Normalize(), ".", " "))
		case tag.Is(rls.TagTypeOther) && tag.Other() == "3D", tag.Is(rls.TagTypeSource) && tag.Source() == "BluRay3D":
			meta.Is3D = "3D"
		}
	}
	if title4KRemaster.MatchString(metadata) {
		editions = append(editions, "4K Remaster")
	}
	meta.Edition = strings.Join(editions, " ")
	for _, flag := range resolveFlags(meta) {
		if flag == "3D" {
			if slot[0] != "3D" {
				return true
			}
		} else if !slices.Contains(strings.Split(slot[4], "+"), strings.ToUpper(flag)) {
			return true
		}
	}
	return false
}
