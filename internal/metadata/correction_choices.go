// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"fmt"
	"slices"
	"strings"

	"github.com/autobrr/rls"
	"github.com/autobrr/rls/taginfo"

	"github.com/autobrr/upbrr/internal/metadata/bluraycom"
	"github.com/autobrr/upbrr/pkg/api"
)

// CorrectionChoice pairs a persisted correction value with its display label.
type CorrectionChoice struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// CorrectionChoices returns known Input suggestions without restricting custom
// corrections. Services use the naming alias map; regions and resolutions merge
// parser tags with values produced by the metadata providers. The generator adds
// tracker-owned catalog suggestions without coupling metadata to tracker implementations.
func CorrectionChoices() (map[string][]CorrectionChoice, error) {
	infos, err := taginfo.LoadAll()
	if err != nil {
		return nil, fmt.Errorf("load correction choice tags: %w", err)
	}
	regions := make(map[string]string)
	for _, info := range infos["region"] {
		regions[info.Tag()] = info.Title()
	}
	for country, code := range bluraycom.CountryRegionCodes() {
		regions[code] = country
	}
	resolutions := make(map[string]string)
	for _, info := range infos["resolution"] {
		if rls.Type(info.Type()).Is(rls.Unknown, rls.Movie, rls.Series, rls.Episode) && !strings.Contains(info.Tag(), "$") {
			resolutions[info.Tag()] = info.Tag()
		}
	}
	// Include interlaced and high-resolution values from the same dimension
	// mapping used by MediaInfo, rather than maintaining another resolution list.
	for _, width := range canonicalResolutionWidths {
		for _, height := range canonicalResolutionHeights {
			for _, scan := range []string{"p", "i"} {
				_, _, value := snapAndMapResolution(width, height, scan, "")
				if value != "OTHER" {
					resolutions[value] = value
				}
			}
		}
	}
	services := serviceCodeMap()
	serviceNames := make(map[string]string)
	for _, service := range services {
		serviceNames[service] = serviceLongName(service, services)
	}
	serviceNames["AMZN"] = "Amazon Prime Video"

	return map[string][]CorrectionChoice{
		"Category": {
			{Value: string(api.CanonicalCategoryMovie), Label: "Movie"},
			{Value: string(api.CanonicalCategoryTV), Label: "TV"},
		},
		// These types have dedicated movie and TV branches in BuildReleaseName.
		"Type": {
			{Value: "DISC", Label: "Disc"},
			{Value: "REMUX", Label: "Remux"},
			{Value: "ENCODE", Label: "Encode"},
			{Value: "WEBDL", Label: "WEB-DL"},
			{Value: "WEBRIP", Label: "WEBRip"},
			{Value: "HDTV", Label: "HDTV"},
			{Value: "DVDRIP", Label: "DVDRip"},
		},
		// Canonical spellings used by sourceAndType and release naming. Disc
		// and encoded Blu-ray/HD DVD spellings intentionally remain distinct.
		"Source": {
			{Value: "Blu-ray", Label: "Blu-ray (disc)"},
			{Value: "BluRay", Label: "BluRay (encode/remux)"},
			{Value: "Blu-ray 3D", Label: "Blu-ray 3D"},
			{Value: "DVD", Label: "DVD"},
			{Value: "PAL DVD", Label: "PAL DVD"},
			{Value: "NTSC DVD", Label: "NTSC DVD"},
			{Value: "HD DVD", Label: "HD DVD (disc)"},
			{Value: "HDDVD", Label: "HDDVD (encode/remux)"},
			{Value: "Web", Label: "Web"},
			{Value: "HDTV", Label: "HDTV"},
			{Value: "UHDTV", Label: "UHDTV"},
		},
		"Resolution": labeledCorrectionChoices(resolutions),
		"Service":    labeledCorrectionChoices(serviceNames),
		"Region":     labeledCorrectionChoices(regions),
	}, nil
}

func labeledCorrectionChoices(names map[string]string) []CorrectionChoice {
	choices := make([]CorrectionChoice, 0, len(names))
	for value, label := range names {
		suffix := " (" + value + ")"
		if label != value && !strings.HasSuffix(label, suffix) {
			label += suffix
		}
		choices = append(choices, CorrectionChoice{Value: value, Label: label})
	}
	slices.SortFunc(choices, func(left, right CorrectionChoice) int {
		return strings.Compare(left.Value, right.Value)
	})
	return choices
}
