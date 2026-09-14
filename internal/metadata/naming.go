// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

var (
	namingTVPathHintPattern = regexp.MustCompile(`(?i)[\\/](tv|tvshows?|series)[\\/]`)
	namingTVNameHintPattern = regexp.MustCompile(
		`(?i)\bS\d{1,2}(?:E\d{1,3})?\b|\b\d{1,2}x\d{2,3}\b|\b(?:season|series)\s*\d+\b|\b(19\d{2}|20\d{2})[.-]\d{2}[.-]\d{2}\b`,
	)
	namingSubsPleaseHintPattern = regexp.MustCompile(`(?i)subsplease`)
	namingAnimeEpisodeHint      = regexp.MustCompile(`(?i)-\s*\d{1,3}\s*\(1080p\)`)
	namingWebDLFilenamePattern  = regexp.MustCompile(`(?i)(^|[ ._-])web([ ._-]|$)|web-?dl`)
)

// BuildReleaseName normalizes category and format signals, applies the request's
// manual omission controls, and builds tracker-style name variants. Unsupported
// category/format combinations return an empty name while preserving the
// applicable missing-field hints; a nil logger is accepted.
func BuildReleaseName(req api.ReleaseNameRequest, logger api.Logger) api.ReleaseNameResult {
	included := buildReleaseName(req, logger)
	omittedRequest := req
	if !req.ManualEpisodeTitle {
		omittedRequest.EpisodeTitle = ""
	}
	omitted := buildReleaseName(omittedRequest, api.NopLogger{})
	included.GeneratedVariants = api.GeneratedReleaseNameVariants{
		IncludeEpisodeTitle: releaseNameVariant(included),
		OmitEpisodeTitle:    releaseNameVariant(omitted),
	}
	return included
}

func buildReleaseName(req api.ReleaseNameRequest, logger api.Logger) api.ReleaseNameResult {
	if logger == nil {
		logger = api.NopLogger{}
	}

	category := normalizeNamingCategory(req.Category)
	typeValue := strings.ToUpper(strings.TrimSpace(req.Type))
	typeValue = normalizeReleaseTypeForCategory(category, typeValue, strings.TrimSpace(req.Source), "")
	matchType := normalizeReleaseType(typeValue)
	logger.Tracef(
		"metadata: release name input category=%q type=%q normalized_type=%q source=%q season=%q episode=%q date=%q manual_date=%t",
		category,
		typeValue,
		matchType,
		strings.TrimSpace(req.Source),
		strings.TrimSpace(req.Season),
		strings.TrimSpace(req.Episode),
		strings.TrimSpace(req.DailyDate),
		req.ManualDate,
	)
	title := strings.TrimSpace(req.Title)
	altTitle := strings.TrimSpace(req.AltTitle)
	year := req.Year
	if req.ManualYear > 0 {
		year = req.ManualYear
	}

	resolution := strings.TrimSpace(req.Resolution)
	if strings.EqualFold(resolution, "OTHER") {
		resolution = ""
	}

	audio, audioMarkers := splitReleaseNameAudioMarkers(normalizeAudioExtraOrder(strings.TrimSpace(req.Audio)))
	service := strings.TrimSpace(req.Service)
	season := strings.TrimSpace(req.Season)
	episode := strings.TrimSpace(req.Episode)
	part := strings.TrimSpace(req.Part)
	repack := strings.TrimSpace(req.Repack)
	threeD := strings.TrimSpace(req.ThreeD)
	tag := strings.TrimSpace(req.Tag)
	source := strings.TrimSpace(req.Source)
	uhd := strings.TrimSpace(req.UHD)
	hdr := strings.TrimSpace(req.HDR)
	episodeTitle := strings.TrimSpace(req.EpisodeTitle)
	dailyDate := strings.TrimSpace(req.DailyDate)
	videoCodec := strings.TrimSpace(req.VideoCodec)
	videoEncode := strings.TrimSpace(req.VideoEncode)
	region := strings.TrimSpace(req.Region)
	dvdSize := strings.TrimSpace(req.DVDSize)
	dvdSourceVisible := false
	dvdSystem := ""
	if matchType == "DISC" && strings.EqualFold(req.DiscType, "DVD") {
		if sourceIn(source, "DVD", "PAL DVD", "NTSC DVD") {
			if sourceIn(source, "PAL DVD", "NTSC DVD") {
				dvdSystem = strings.TrimSpace(source[:len(source)-len("DVD")])
			}
			dvdSourceVisible = !strings.EqualFold(dvdSize, "DVD5") && !strings.EqualFold(dvdSize, "DVD9")
			source = source[len(source)-len("DVD"):]
		} else {
			dvdSourceVisible = true
		}
	}
	edition := strings.TrimSpace(req.Edition)
	retained := retainedReleaseNameValues{
		Title:          title,
		AlternateTitle: altTitle,
		Year:           releaseNameYearValue(year),
		Season:         season,
		Episode:        episode,
		EpisodeTitle:   episodeTitle,
		DailyDate:      dailyDate,
		Edition:        edition,
		Repack:         repack,
		Resolution:     resolution,
		Region:         region,
		Source:         source,
		UHD:            uhd,
		HDR:            hdr,
		Service:        service,
		DVDSystem:      dvdSystem,
		DVDSize:        dvdSize,
		VideoCodec:     videoCodec,
		VideoEncode:    videoEncode,
		Audio:          audio,
		ThreeD:         threeD,
		Part:           part,
	}

	hybrid := ""
	if req.WebDV || containsExactHybrid(strings.Fields(edition)) {
		hybrid = "Hybrid"
	}
	edition = removeHybrid(edition)
	retained.Edition = edition
	retained.Hybrid = hybrid
	retained.VideoFormat = releaseNameVideoFormat(matchType)
	if matchType != "DISC" || !strings.EqualFold(req.DiscType, "DVD") {
		retained.DVDSystem = ""
		retained.DVDSize = ""
	}

	if category == "TV" && !req.ManualEpisodeTitle && episode == "" && !req.ManualDate {
		episodeTitle = ""
	}
	if req.ManualDate {
		season = ""
		episode = ""
		if dailyDate != "" {
			episodeTitle = dailyDate
		}
	}
	if req.NoSeason {
		season = ""
		episode = ""
	}
	if req.NoYear {
		year = 0
	}
	if req.NoAKA {
		altTitle = ""
	}

	if category == "TV" {
		searchYear := strings.TrimSpace(req.SearchYear)
		if parsedYear, err := strconv.Atoi(searchYear); err == nil && parsedYear > 0 {
			title = trimTrailingParentheticalYear(title, parsedYear)
			if !req.NoYear {
				year = parsedYear
			}
		} else {
			year = 0
		}
	}

	logger.Tracef(
		"metadata: release name build category=%q type=%q source=%q disc=%q title=%q season=%q episode=%q",
		category,
		matchType,
		source,
		req.DiscType,
		title,
		season,
		episode,
	)

	missing := make([]string, 0)
	yearValue := ""
	if year > 0 {
		yearValue = strconv.Itoa(year)
	}

	switch category {
	case "MOVIE":
		switch {
		case matchType == "DISC" && strings.EqualFold(req.DiscType, "BDMV"):
			missing = []string{"edition", "region", "distributor"}
		case matchType == "DISC" && strings.EqualFold(req.DiscType, "DVD"):
			missing = []string{"edition", "distributor"}
		case matchType == "DISC" && strings.EqualFold(req.DiscType, "HDDVD"):
			missing = []string{"edition", "region", "distributor"}
		case matchType == "REMUX" && (sourceIn(source, "BluRay", "HDDVD") || sourceIn(source, "PAL DVD", "NTSC DVD", "DVD")):
			missing = []string{"edition", "description"}
		case matchType == "ENCODE":
			missing = []string{"edition", "description"}
		case matchType == "WEBDL", matchType == "WEBRIP":
			missing = []string{"edition", "service"}
		}
	case "TV":
		switch {
		case matchType == "DISC" && strings.EqualFold(req.DiscType, "BDMV"), matchType == "DISC" && strings.EqualFold(req.DiscType, "HDDVD"):
			missing = []string{"edition", "region", "distributor"}
		case matchType == "DISC" && strings.EqualFold(req.DiscType, "DVD"):
			missing = []string{"edition", "distributor"}
		case matchType == "REMUX" && (sourceIn(source, "BluRay", "HDDVD") || sourceIn(source, "PAL DVD", "NTSC DVD", "DVD")):
			missing = []string{"edition", "description"}
		case matchType == "ENCODE":
			missing = []string{"edition", "description"}
		case matchType == "WEBDL", matchType == "WEBRIP":
			missing = []string{"edition", "service"}
		}
	}

	document := generatedReleaseNameDocument(
		category,
		matchType,
		strings.ToUpper(strings.TrimSpace(req.DiscType)),
		title,
		altTitle,
		yearValue,
		season,
		episode,
		episodeTitle,
		dailyDate,
		part,
		threeD,
		edition,
		hybrid,
		repack,
		resolution,
		region,
		uhd,
		source,
		service,
		dvdSystem,
		dvdSourceVisible,
		dvdSize,
		hdr,
		videoCodec,
		videoEncode,
		audio,
		tag,
		audioMarkers,
		req.ManualDate,
		retained,
	)
	if document == nil || document.Render().NameNoTag == "" {
		logger.Tracef(
			"metadata: release name build skipped (empty base) category=%q type=%q source=%q season=%q episode=%q",
			category,
			matchType,
			source,
			season,
			episode,
		)
		return api.ReleaseNameResult{MissingFields: missing}
	}
	rendered := document.Render()
	logger.Tracef("metadata: release name built name=%q clean=%q", rendered.Name, rendered.CleanName)
	return api.ReleaseNameResult{
		NameNoTag:     rendered.NameNoTag,
		Name:          rendered.Name,
		CleanName:     rendered.CleanName,
		GeneratedName: document,
		MissingFields: missing,
	}
}

func releaseNameVariant(result api.ReleaseNameResult) api.ReleaseNameVariant {
	return api.ReleaseNameVariant{
		NameNoTag: result.NameNoTag,
		Name:      result.Name,
		CleanName: result.CleanName,
	}
}

func generatedReleaseNameDocument(
	category, releaseType, discType string,
	title, alternateTitle, year, season, episode, episodeTitle, dailyDate, part, threeD, edition, hybrid, repack,
	resolution, region, uhd, source, service, dvdSystem string,
	dvdSourceVisible bool,
	dvdSize, hdr, videoCodec, videoEncode, audio, tag string,
	audioMarkers releaseNameAudioMarkers,
	manualDate bool,
	retained retainedReleaseNameValues,
) *api.ReleaseNameDocument {
	components := make([]api.ReleaseNameComponent, 0, 18)
	appendComponents := func(values ...releaseNameDocumentValue) {
		for _, value := range values {
			components = append(components, api.ReleaseNameComponent{
				Role:           value.role,
				Value:          value.value,
				AvailableValue: value.value,
				Present:        strings.TrimSpace(value.value) != "",
				Join:           value.join,
				AttachTo:       append([]api.ReleaseNameRole(nil), value.attachTo...),
			})
		}
	}
	space := func(role api.ReleaseNameRole, value string) releaseNameDocumentValue {
		return releaseNameDocumentValue{
			role:  role,
			value: value,
			join:  " ",
		}
	}
	attached := func(role api.ReleaseNameRole, value string, anchors ...api.ReleaseNameRole) releaseNameDocumentValue {
		return releaseNameDocumentValue{
			role:     role,
			value:    value,
			join:     " ",
			attachTo: anchors,
		}
	}
	seasonComponent := space(api.NameRoleSeason, season)
	episodeComponent := attached(api.NameRoleEpisode, episode, api.NameRoleSeason)
	dvdSourceComponent := space(api.NameRoleSource, "")
	if dvdSourceVisible {
		dvdSourceComponent = space(api.NameRoleSource, source)
	}
	videoComponent := func() releaseNameDocumentValue {
		if videoEncode != "" {
			return space(api.NameRoleVideoEncode, videoEncode)
		}
		return space(api.NameRoleVideoCodec, videoCodec)
	}

	switch category {
	case "MOVIE":
		switch {
		case releaseType == "DISC" && discType == "BDMV":
			appendComponents(space(api.NameRoleTitle, title), space(api.NameRoleAlternateTitle, alternateTitle), space(api.NameRoleYear, year),
				space(api.NameRoleThreeD, threeD), space(api.NameRoleEdition, edition), space(api.NameRoleHybrid, hybrid), space(api.NameRoleRepack, repack),
				space(api.NameRoleResolution, resolution), space(api.NameRoleRegion, region), space(api.NameRoleUHD, uhd), space(api.NameRoleSource, source),
				space(api.NameRoleHDR, hdr), space(api.NameRoleVideoCodec, videoCodec), space(api.NameRoleAudio, audio))
		case releaseType == "DISC" && discType == "DVD":
			appendComponents(
				space(api.NameRoleTitle, title),
				space(api.NameRoleAlternateTitle, alternateTitle),
				space(api.NameRoleYear, year),
				space(
					api.NameRoleRepack,
					repack,
				),
				space(api.NameRoleEdition, edition),
				space(api.NameRoleRegion, region),
				space(api.NameRoleDVDSystem, dvdSystem),
				dvdSourceComponent,
				space(api.NameRoleDVDSize, dvdSize),
				space(api.NameRoleAudio, audio),
			)
		case releaseType == "DISC" && discType == "HDDVD":
			appendComponents(
				space(api.NameRoleTitle, title),
				space(api.NameRoleAlternateTitle, alternateTitle),
				space(api.NameRoleYear, year),
				space(
					api.NameRoleEdition,
					edition,
				),
				space(api.NameRoleRepack, repack),
				space(api.NameRoleResolution, resolution),
				space(api.NameRoleSource, source),
				space(api.NameRoleVideoCodec, videoCodec),
				space(api.NameRoleAudio, audio),
			)
		case releaseType == "REMUX" && sourceIn(source, "BluRay", "HDDVD"):
			appendComponents(
				space(api.NameRoleTitle, title),
				space(api.NameRoleAlternateTitle, alternateTitle),
				space(api.NameRoleYear, year),
				space(api.NameRoleThreeD, threeD),
				space(api.NameRoleEdition, edition),
				space(api.NameRoleHybrid, hybrid),
				space(api.NameRoleRepack, repack),
				space(
					api.NameRoleResolution,
					resolution,
				),
				space(api.NameRoleUHD, uhd),
				space(api.NameRoleSource, source),
				space(api.NameRoleVideoFormat, "REMUX"),
				space(api.NameRoleHDR, hdr),
				space(api.NameRoleVideoCodec, videoCodec),
				space(api.NameRoleAudio, audio),
			)
		case releaseType == "REMUX" && sourceIn(source, "PAL DVD", "NTSC DVD", "DVD"):
			appendComponents(
				space(api.NameRoleTitle, title),
				space(api.NameRoleAlternateTitle, alternateTitle),
				space(api.NameRoleYear, year),
				space(
					api.NameRoleEdition,
					edition,
				),
				space(api.NameRoleRepack, repack),
				space(api.NameRoleSource, source),
				space(api.NameRoleVideoFormat, "REMUX"),
				space(api.NameRoleAudio, audio),
			)
		case releaseType == "ENCODE":
			appendComponents(
				space(api.NameRoleTitle, title),
				space(api.NameRoleAlternateTitle, alternateTitle),
				space(api.NameRoleYear, year),
				space(
					api.NameRoleEdition,
					edition,
				),
				space(api.NameRoleHybrid, hybrid),
				space(api.NameRoleRepack, repack),
				space(api.NameRoleResolution, resolution),
				space(
					api.NameRoleUHD,
					uhd,
				),
				space(api.NameRoleSource, source),
				space(api.NameRoleAudio, audio),
				space(api.NameRoleHDR, hdr),
				videoComponent(),
			)
		case releaseType == "WEBDL", releaseType == "WEBRIP":
			webFormat := "WEB-DL"
			if releaseType == "WEBRIP" {
				webFormat = "WEBRip"
			}
			appendComponents(
				space(api.NameRoleTitle, title),
				space(api.NameRoleAlternateTitle, alternateTitle),
				space(api.NameRoleYear, year),
				space(
					api.NameRoleEdition,
					edition,
				),
				space(api.NameRoleHybrid, hybrid),
				space(api.NameRoleRepack, repack),
				space(api.NameRoleResolution, resolution),
				space(api.NameRoleUHD, uhd),
				space(api.NameRoleService, service),
				space(api.NameRoleVideoFormat, webFormat),
				space(api.NameRoleAudio, audio),
				space(api.NameRoleHDR, hdr),
				videoComponent(),
			)
		case releaseType == "HDTV":
			appendComponents(
				space(api.NameRoleTitle, title),
				space(api.NameRoleAlternateTitle, alternateTitle),
				space(api.NameRoleYear, year),
				space(api.NameRoleEdition, edition),
				space(
					api.NameRoleRepack,
					repack,
				),
				space(api.NameRoleResolution, resolution),
				space(api.NameRoleSource, source),
				space(api.NameRoleAudio, audio),
				videoComponent(),
			)
		case releaseType == "DVDRIP":
			appendComponents(
				space(api.NameRoleTitle, title),
				space(api.NameRoleAlternateTitle, alternateTitle),
				space(api.NameRoleYear, year),
				space(api.NameRoleSource, ""),
				space(api.NameRoleVideoFormat, "DVDRip"),
				space(api.NameRoleAudio, audio),
				videoComponent(),
			)
		}
	case "TV":
		switch {
		case releaseType == "DISC" && discType == "BDMV":
			appendComponents(
				space(api.NameRoleTitle, title),
				space(api.NameRoleYear, year),
				space(api.NameRoleAlternateTitle, alternateTitle),
				seasonComponent,
				episodeComponent,
				space(
					api.NameRoleThreeD,
					threeD,
				),
				space(api.NameRoleEdition, edition),
				space(api.NameRoleHybrid, hybrid),
				space(api.NameRoleRepack, repack),
				space(api.NameRoleResolution, resolution),
				space(
					api.NameRoleRegion,
					region,
				),
				space(api.NameRoleUHD, uhd),
				space(api.NameRoleSource, source),
				space(api.NameRoleHDR, hdr),
				space(api.NameRoleVideoCodec, videoCodec),
				space(api.NameRoleAudio, audio),
			)
		case releaseType == "DISC" && discType == "DVD":
			appendComponents(
				space(api.NameRoleTitle, title),
				space(api.NameRoleYear, year),
				space(api.NameRoleAlternateTitle, alternateTitle),
				seasonComponent,
				episodeComponent,
				attached(api.NameRoleThreeD, threeD, api.NameRoleEpisode, api.NameRoleSeason),
				space(api.NameRoleRepack, repack),
				space(api.NameRoleEdition, edition),
				space(api.NameRoleRegion, region),
				space(api.NameRoleDVDSystem, dvdSystem),
				dvdSourceComponent,
				space(api.NameRoleDVDSize, dvdSize),
				space(api.NameRoleAudio, audio),
			)
		case releaseType == "DISC" && discType == "HDDVD":
			appendComponents(
				space(api.NameRoleTitle, title),
				space(api.NameRoleAlternateTitle, alternateTitle),
				space(api.NameRoleYear, year),
				space(api.NameRoleEdition, edition),
				space(
					api.NameRoleRepack,
					repack,
				),
				space(api.NameRoleResolution, resolution),
				space(api.NameRoleSource, source),
				space(api.NameRoleVideoCodec, videoCodec),
				space(api.NameRoleAudio, audio),
			)
		case releaseType == "REMUX" && sourceIn(source, "BluRay", "HDDVD"):
			appendTVStandardComponents(
				appendComponents,
				space,
				title,
				year,
				alternateTitle,
				season,
				episode,
				episodeTitle,
				part,
				threeD,
				edition,
				hybrid,
				repack,
				resolution,
				uhd,
				source,
				"REMUX",
				hdr,
				videoCodec,
				audio,
			)
		case releaseType == "REMUX" && sourceIn(source, "PAL DVD", "NTSC DVD", "DVD"):
			appendComponents(
				space(api.NameRoleTitle, title),
				space(api.NameRoleYear, year),
				space(api.NameRoleAlternateTitle, alternateTitle),
				seasonComponent,
				episodeComponent,
				space(
					api.NameRoleEpisodeTitle,
					episodeTitle,
				),
				space(api.NameRolePart, part),
				space(api.NameRoleEdition, edition),
				space(api.NameRoleRepack, repack),
				space(api.NameRoleSource, source),
				space(api.NameRoleVideoFormat, "REMUX"),
				space(api.NameRoleAudio, audio),
			)
		case releaseType == "ENCODE":
			appendTVEncodeComponents(
				appendComponents,
				space,
				title,
				year,
				alternateTitle,
				season,
				episode,
				episodeTitle,
				part,
				edition,
				hybrid,
				repack,
				resolution,
				uhd,
				source,
				audio,
				hdr,
				videoComponent(),
			)
		case releaseType == "WEBDL", releaseType == "WEBRIP":
			webFormat := "WEB-DL"
			if releaseType == "WEBRIP" {
				webFormat = "WEBRip"
			}
			appendTVWebComponents(
				appendComponents,
				space,
				title,
				year,
				alternateTitle,
				season,
				episode,
				episodeTitle,
				part,
				edition,
				hybrid,
				repack,
				resolution,
				uhd,
				service,
				webFormat,
				audio,
				hdr,
				videoComponent(),
			)
		case releaseType == "HDTV":
			appendComponents(
				space(api.NameRoleTitle, title),
				space(api.NameRoleYear, year),
				space(api.NameRoleAlternateTitle, alternateTitle),
				seasonComponent,
				episodeComponent,
				space(api.NameRoleEpisodeTitle, episodeTitle),
				space(api.NameRolePart, part),
				space(api.NameRoleEdition, edition),
				space(api.NameRoleRepack, repack),
				space(api.NameRoleResolution, resolution),
				space(api.NameRoleSource, source),
				space(api.NameRoleAudio, audio),
				videoComponent(),
			)
		case releaseType == "DVDRIP":
			appendComponents(
				space(api.NameRoleTitle, title),
				space(api.NameRoleYear, year),
				space(api.NameRoleAlternateTitle, alternateTitle),
				seasonComponent,
				space(api.NameRoleSource, ""),
				space(api.NameRoleVideoFormat, "DVDRip"),
				space(api.NameRoleAudio, audio),
				videoComponent(),
			)
		}
	}
	if len(components) == 0 {
		return nil
	}
	if manualDate {
		for index := range components {
			if components[index].Role == api.NameRoleEpisodeTitle && components[index].Value == dailyDate {
				components[index].Role = api.NameRoleDailyDate
				break
			}
		}
	}
	components = retainUnavailableReleaseNameComponents(components, retained)
	components = withReleaseNameAudioMarkers(components, audioMarkers)
	appendComponents(releaseNameDocumentValue{role: api.NameRoleGroup, value: tag})
	return &api.ReleaseNameDocument{Version: api.ReleaseNameDocumentVersionV1, Components: components}
}

type releaseNameAudioMarkers struct {
	Dubbed         bool
	DualAudio      bool
	DualAudioFirst bool
}

func splitReleaseNameAudioMarkers(value string) (string, releaseNameAudioMarkers) {
	value = strings.TrimSpace(value)
	markers := releaseNameAudioMarkers{}
	if after, ok := strings.CutPrefix(value, "Dubbed "); ok {
		markers.Dubbed = true
		value = strings.TrimSpace(after)
	} else if strings.EqualFold(value, "Dubbed") {
		markers.Dubbed = true
		value = ""
	}
	if after, ok := strings.CutPrefix(value, "Dual-Audio "); ok {
		markers.DualAudio = true
		markers.DualAudioFirst = true
		value = strings.TrimSpace(after)
	} else if strings.EqualFold(value, "Dual-Audio") {
		markers.DualAudio = true
		markers.DualAudioFirst = true
		value = ""
	}
	if before, ok := strings.CutSuffix(value, " Dual-Audio"); ok {
		markers.DualAudio = true
		value = strings.TrimSpace(before)
	}
	return value, markers
}

func renderReleaseNameAudioMarkers(value string, markers releaseNameAudioMarkers) string {
	parts := make([]string, 0, 3)
	if markers.Dubbed {
		parts = append(parts, "Dubbed")
	}
	if markers.DualAudio && markers.DualAudioFirst {
		parts = append(parts, "Dual-Audio")
	}
	if value = strings.TrimSpace(value); value != "" {
		parts = append(parts, value)
	}
	if markers.DualAudio && !markers.DualAudioFirst {
		parts = append(parts, "Dual-Audio")
	}
	return strings.Join(parts, " ")
}

func withReleaseNameAudioMarkers(components []api.ReleaseNameComponent, markers releaseNameAudioMarkers) []api.ReleaseNameComponent {
	for index, component := range components {
		if component.Role != api.NameRoleAudio {
			continue
		}
		markerJoin := component.Join
		audioJoin := component.Join
		before := make([]api.ReleaseNameComponent, 0, 2)
		after := make([]api.ReleaseNameComponent, 0, 1)
		if markers.Dubbed {
			before = append(before, api.ReleaseNameComponent{
				Role:           api.NameRoleDubbed,
				Value:          "Dubbed",
				AvailableValue: "Dubbed",
				Present:        true,
				Join:           markerJoin,
			})
			audioJoin = " "
		} else {
			before = append(before, api.ReleaseNameComponent{Role: api.NameRoleDubbed, Join: markerJoin})
		}
		dual := api.ReleaseNameComponent{
			Role:    api.NameRoleDualAudio,
			Present: markers.DualAudio,
			Join:    " ",
		}
		if markers.DualAudio {
			dual.Value = "Dual-Audio"
			dual.AvailableValue = "Dual-Audio"
		}
		if markers.DualAudio && markers.DualAudioFirst {
			dual.Join = markerJoin
			before = append(before, dual)
			audioJoin = " "
		} else {
			after = append(after, dual)
		}
		component.Join = audioJoin
		result := make([]api.ReleaseNameComponent, 0, len(components)+2)
		result = append(result, components[:index]...)
		result = append(result, before...)
		result = append(result, component)
		result = append(result, after...)
		result = append(result, components[index+1:]...)
		return result
	}
	return components
}

type releaseNameDocumentValue struct {
	role     api.ReleaseNameRole
	value    string
	join     string
	attachTo []api.ReleaseNameRole
}

type retainedReleaseNameValues struct {
	Title          string
	AlternateTitle string
	Year           string
	Season         string
	Episode        string
	DailyDate      string
	EpisodeTitle   string
	Edition        string
	Hybrid         string
	Repack         string
	Resolution     string
	Region         string
	Source         string
	UHD            string
	HDR            string
	Service        string
	DVDSystem      string
	DVDSize        string
	VideoFormat    string
	VideoCodec     string
	VideoEncode    string
	Audio          string
	ThreeD         string
	Part           string
}

func retainUnavailableReleaseNameComponents(
	components []api.ReleaseNameComponent,
	values retainedReleaseNameValues,
) []api.ReleaseNameComponent {
	for _, retained := range []releaseNameDocumentValue{
		{
			role:  api.NameRoleTitle,
			value: values.Title,
			join:  " ",
		},
		{
			role:  api.NameRoleAlternateTitle,
			value: values.AlternateTitle,
			join:  " ",
		},
		{
			role:  api.NameRoleYear,
			value: values.Year,
			join:  " ",
		},
		{
			role:  api.NameRoleSeason,
			value: values.Season,
			join:  " ",
		},
		{
			role:     api.NameRoleEpisode,
			value:    values.Episode,
			join:     " ",
			attachTo: []api.ReleaseNameRole{api.NameRoleSeason},
		},
		{
			role:  api.NameRoleDailyDate,
			value: values.DailyDate,
			join:  " ",
		},
		{
			role:  api.NameRoleEpisodeTitle,
			value: values.EpisodeTitle,
			join:  " ",
		},
		{
			role:  api.NameRoleEdition,
			value: values.Edition,
			join:  " ",
		},
		{
			role:  api.NameRoleHybrid,
			value: values.Hybrid,
			join:  " ",
		},
		{
			role:  api.NameRoleRepack,
			value: values.Repack,
			join:  " ",
		},
		{
			role:  api.NameRoleResolution,
			value: values.Resolution,
			join:  " ",
		},
		{
			role:  api.NameRoleRegion,
			value: values.Region,
			join:  " ",
		},
		{
			role:  api.NameRoleSource,
			value: values.Source,
			join:  " ",
		},
		{
			role:  api.NameRoleUHD,
			value: values.UHD,
			join:  " ",
		},
		{
			role:  api.NameRoleHDR,
			value: values.HDR,
			join:  " ",
		},
		{
			role:  api.NameRoleService,
			value: values.Service,
			join:  " ",
		},
		{
			role:  api.NameRoleDVDSystem,
			value: values.DVDSystem,
			join:  " ",
		},
		{
			role:  api.NameRoleDVDSize,
			value: values.DVDSize,
			join:  " ",
		},
		{
			role:  api.NameRoleVideoFormat,
			value: values.VideoFormat,
			join:  " ",
		},
		{
			role:  api.NameRoleVideoCodec,
			value: values.VideoCodec,
			join:  " ",
		},
		{
			role:  api.NameRoleVideoEncode,
			value: values.VideoEncode,
			join:  " ",
		},
		{
			role:  api.NameRoleAudio,
			value: values.Audio,
			join:  " ",
		},
		{
			role:  api.NameRoleThreeD,
			value: values.ThreeD,
			join:  " ",
		},
		{
			role:  api.NameRolePart,
			value: values.Part,
			join:  " ",
		},
	} {
		if strings.TrimSpace(retained.value) == "" {
			continue
		}
		if index := releaseNameComponentSliceIndex(components, retained.role); index >= 0 {
			if strings.TrimSpace(components[index].Value) == "" {
				components[index].Value = retained.value
				components[index].AvailableValue = retained.value
				components[index].AttachTo = append([]api.ReleaseNameRole(nil), retained.attachTo...)
			}
			continue
		}
		component := api.ReleaseNameComponent{
			Role:           retained.role,
			Value:          retained.value,
			AvailableValue: retained.value,
			Join:           retained.join,
			AttachTo:       append([]api.ReleaseNameRole(nil), retained.attachTo...),
		}
		components = insertRetainedReleaseNameComponent(components, component)
	}
	return components
}

func insertRetainedReleaseNameComponent(components []api.ReleaseNameComponent, component api.ReleaseNameComponent) []api.ReleaseNameComponent {
	return slices.Insert(components, retainedReleaseNameComponentIndex(components, component.Role), component)
}

func releaseNameComponentSliceIndex(components []api.ReleaseNameComponent, role api.ReleaseNameRole) int {
	return slices.IndexFunc(components, func(component api.ReleaseNameComponent) bool { return component.Role == role })
}

func retainedReleaseNameComponentIndex(components []api.ReleaseNameComponent, role api.ReleaseNameRole) int {
	anchors := retainedReleaseNameAnchors(role)
	for index, component := range components {
		if slices.Contains(anchors, component.Role) {
			return index
		}
	}
	if group := slices.IndexFunc(components, func(component api.ReleaseNameComponent) bool { return component.Role == api.NameRoleGroup }); group >= 0 {
		return group
	}
	return len(components)
}

func retainedReleaseNameAnchors(role api.ReleaseNameRole) []api.ReleaseNameRole {
	order := []api.ReleaseNameRole{
		api.NameRoleSeason, api.NameRoleEpisode, api.NameRoleDailyDate, api.NameRoleEpisodeTitle,
		api.NameRolePart, api.NameRoleThreeD, api.NameRoleEdition, api.NameRoleHybrid,
		api.NameRoleRepack, api.NameRoleResolution, api.NameRoleRegion, api.NameRoleSource,
	}
	if index := slices.Index(order, role); index >= 0 {
		return order[index+1:]
	}
	return nil
}

func releaseNameYearValue(year int) string {
	if year <= 0 {
		return ""
	}
	return strconv.Itoa(year)
}

func releaseNameVideoFormat(releaseType string) string {
	switch releaseType {
	case "DISC":
		return "DISC"
	case "REMUX":
		return "REMUX"
	case "WEBDL":
		return "WEB-DL"
	case "WEBRIP":
		return "WEBRip"
	case "DVDRIP":
		return "DVDRip"
	default:
		return ""
	}
}

func appendTVStandardComponents(
	appendComponents func(...releaseNameDocumentValue),
	space func(api.ReleaseNameRole, string) releaseNameDocumentValue,
	title, year, alternateTitle, season, episode, episodeTitle, part, threeD, edition, hybrid, repack, resolution, uhd, source, videoFormat, hdr, videoCodec, audio string,
) {
	appendComponents(
		space(api.NameRoleTitle, title),
		space(api.NameRoleYear, year),
		space(api.NameRoleAlternateTitle, alternateTitle),
		space(api.NameRoleSeason, season),
		releaseNameDocumentValue{
			role:     api.NameRoleEpisode,
			value:    episode,
			join:     " ",
			attachTo: []api.ReleaseNameRole{api.NameRoleSeason},
		},
		space(
			api.NameRoleEpisodeTitle,
			episodeTitle,
		),
		space(api.NameRolePart, part),
		space(api.NameRoleThreeD, threeD),
		space(api.NameRoleEdition, edition),
		space(api.NameRoleHybrid, hybrid),
		space(api.NameRoleRepack, repack),
		space(api.NameRoleResolution, resolution),
		space(api.NameRoleUHD, uhd),
		space(api.NameRoleSource, source),
		space(api.NameRoleVideoFormat, videoFormat),
		space(api.NameRoleHDR, hdr),
		space(api.NameRoleVideoCodec, videoCodec),
		space(api.NameRoleAudio, audio),
	)
}

func appendTVEncodeComponents(
	appendComponents func(...releaseNameDocumentValue),
	space func(api.ReleaseNameRole, string) releaseNameDocumentValue,
	title, year, alternateTitle, season, episode, episodeTitle, part, edition, hybrid, repack, resolution, uhd, source, audio, hdr string,
	video releaseNameDocumentValue,
) {
	appendComponents(
		space(api.NameRoleTitle, title),
		space(api.NameRoleYear, year),
		space(api.NameRoleAlternateTitle, alternateTitle),
		space(api.NameRoleSeason, season),
		releaseNameDocumentValue{
			role:     api.NameRoleEpisode,
			value:    episode,
			join:     " ",
			attachTo: []api.ReleaseNameRole{api.NameRoleSeason},
		},
		space(
			api.NameRoleEpisodeTitle,
			episodeTitle,
		),
		space(api.NameRolePart, part),
		space(api.NameRoleEdition, edition),
		space(api.NameRoleHybrid, hybrid),
		space(api.NameRoleRepack, repack),
		space(api.NameRoleResolution, resolution),
		space(api.NameRoleUHD, uhd),
		space(api.NameRoleSource, source),
		space(api.NameRoleAudio, audio),
		space(api.NameRoleHDR, hdr),
		video,
	)
}

func appendTVWebComponents(
	appendComponents func(...releaseNameDocumentValue),
	space func(api.ReleaseNameRole, string) releaseNameDocumentValue,
	title, year, alternateTitle, season, episode, episodeTitle, part, edition, hybrid, repack, resolution, uhd, service, webFormat, audio, hdr string,
	video releaseNameDocumentValue,
) {
	appendComponents(
		space(api.NameRoleTitle, title),
		space(api.NameRoleYear, year),
		space(api.NameRoleAlternateTitle, alternateTitle),
		space(api.NameRoleSeason, season),
		releaseNameDocumentValue{
			role:     api.NameRoleEpisode,
			value:    episode,
			join:     " ",
			attachTo: []api.ReleaseNameRole{api.NameRoleSeason},
		},
		space(
			api.NameRoleEpisodeTitle,
			episodeTitle,
		),
		space(api.NameRolePart, part),
		space(api.NameRoleEdition, edition),
		space(api.NameRoleHybrid, hybrid),
		space(api.NameRoleRepack, repack),
		space(api.NameRoleResolution, resolution),
		space(api.NameRoleUHD, uhd),
		space(api.NameRoleService, service),
		space(api.NameRoleVideoFormat, webFormat),
		space(api.NameRoleAudio, audio),
		space(api.NameRoleHDR, hdr),
		video,
	)
}

// releaseNameRequestFromMeta converts prepared metadata into the naming input,
// omitting TV-pack season titles that are stored in EpisodeTitle only as scoped
// metadata fallback text.
func releaseNameRequestFromMeta(meta preparationstate.State, logger api.Logger) api.ReleaseNameRequest {
	if logger == nil {
		logger = api.NopLogger{}
	}

	category := normalizeNamingCategory(string(meta.Identity.Category))
	if category == "" {
		category = normalizeNamingCategory(meta.MediaInfoCategory)
	}
	if category == "" {
		category = normalizeNamingCategory(meta.Release.Category)
	}
	if category == "" {
		category = normalizeCategoryFromType(meta.Type)
	}
	if category == "" {
		category = inferCategoryFromMetadata(meta)
	}

	baseType := strings.TrimSpace(meta.Type)
	typeValue := baseType
	if typeValue == "" || isCategoryType(typeValue) {
		typeValue = strings.TrimSpace(meta.Release.Type)
	}
	if typeValue == "" || isCategoryType(typeValue) {
		if meta.DiscType != "" {
			typeValue = "DISC"
		}
	}

	source := strings.TrimSpace(meta.Source)
	if source == "" {
		source = strings.TrimSpace(meta.Release.Source)
	}
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
	if typeValue == "" || isCategoryType(typeValue) {
		if meta.VideoEncode != "" {
			typeValue = "ENCODE"
		}
	}
	if typeValue == "" || isCategoryType(typeValue) {
		if strings.TrimSpace(meta.VideoCodec) != "" || strings.TrimSpace(meta.Release.Resolution) != "" || strings.TrimSpace(meta.Release.Ext) != "" {
			typeValue = "ENCODE"
		}
	}
	if source == "" || !isKnownReleaseSource(source) {
		if inferred := inferReleaseSourceFromName(meta.SourcePath, typeValue); inferred != "" {
			source = inferred
		}
	}

	title, altTitle, year := resolveReleaseNameTitle(category, meta)
	if meta.ReleaseNameOverrides.ManualYear != nil && !strings.EqualFold(category, "TV") {
		year = *meta.ReleaseNameOverrides.ManualYear
	}
	searchYear := ""
	if strings.EqualFold(category, "TV") && year > 0 {
		title = trimTrailingParentheticalYear(title, year)
		searchYear = strconv.Itoa(year)
	}
	tvdbYearSource := ""
	tvdbYearFromAlias := false
	if strings.EqualFold(category, "TV") && meta.ProviderMetadata.TVDB != nil {
		tvdbYearSource = strings.TrimSpace(meta.ProviderMetadata.TVDB.YearSource)
		tvdbYearFromAlias = meta.ProviderMetadata.TVDB.YearFromAlias
	}

	typeValue = normalizeReleaseTypeForCategory(category, typeValue, source, meta.SourcePath)

	logger.Tracef(
		"metadata: release name request resolved category=%q type=%q base_type=%q source=%q season=%q episode=%q date=%q tv_pack=%t year=%d search_year=%q year_source=%q tvdb_year_from_alias=%t",
		category,
		typeValue,
		baseType,
		source,
		strings.TrimSpace(meta.SeasonStr),
		strings.TrimSpace(meta.EpisodeStr),
		strings.TrimSpace(meta.DailyEpisodeDate),
		meta.TVPack,
		year,
		searchYear,
		tvdbYearSource,
		tvdbYearFromAlias,
	)

	dailyDate := strings.TrimSpace(meta.DailyEpisodeDate)
	manualDate := strings.EqualFold(category, "TV") && dailyDate != "" && !meta.TVPack
	episodeTitle := preferredGeneratedEpisodeTitle(meta)
	if meta.TVPack || strings.TrimSpace(meta.EpisodeStr) == "" {
		episodeTitle = ""
	} else if titleIdentityKey(episodeTitle) != "" {
		episodeTitleKey := titleIdentityKey(episodeTitle)
		if episodeTitleKey == titleIdentityKey(title) || episodeTitleKey == titleIdentityKey(altTitle) {
			episodeTitle = ""
		}
	}

	return api.ReleaseNameRequest{
		Category:      category,
		Type:          typeValue,
		Title:         title,
		AltTitle:      altTitle,
		Year:          year,
		Resolution:    meta.Release.Resolution,
		Audio:         meta.Audio,
		Service:       meta.Service,
		Season:        strings.TrimSpace(meta.SeasonStr),
		Episode:       strings.TrimSpace(meta.EpisodeStr),
		Part:          "",
		Repack:        meta.Repack,
		ThreeD:        meta.Is3D,
		Tag:           meta.Tag,
		Source:        source,
		UHD:           meta.UHD,
		HDR:           meta.HDR,
		WebDV:         meta.WebDV,
		EpisodeTitle:  episodeTitle,
		VideoCodec:    meta.VideoCodec,
		VideoEncode:   meta.VideoEncode,
		DiscType:      meta.DiscType,
		Region:        meta.Region,
		DVDSize:       meta.Release.Size,
		Edition:       meta.Edition,
		SearchYear:    searchYear,
		DailyDate:     dailyDate,
		ManualDate:    manualDate,
		TMDBDateMatch: meta.TMDBDateMatch,
	}
}

func preferredGeneratedEpisodeTitle(meta preparationstate.State) string {
	parsed := strings.TrimSpace(meta.EpisodeTitle)
	tvdb := meta.ProviderMetadata.TVDB
	if !namingProviderMetadataCurrent(meta) || tvdb == nil || meta.Identity.TVDBID <= 0 || tvdb.TVDBID != meta.Identity.TVDBID {
		return parsed
	}
	if tvdb.EpisodeSeason > 0 && meta.SeasonInt > 0 && tvdb.EpisodeSeason != meta.SeasonInt {
		return parsed
	}
	if tvdb.EpisodeNumber > 0 && meta.EpisodeInt > 0 && tvdb.EpisodeNumber != meta.EpisodeInt {
		return parsed
	}
	if english := strings.TrimSpace(tvdb.EpisodeNameEnglish); english != "" {
		return english
	}
	original := strings.TrimSpace(tvdb.EpisodeName)
	if original != "" && !isGenericEpisodeTitle(original) &&
		(strings.TrimSpace(tvdb.OriginalLanguage) == "" || isEnglishLanguage(tvdb.OriginalLanguage)) {
		return original
	}
	return parsed
}

func resolvedEpisodeTitle(meta preparationstate.State) string {
	overrides := meta.ReleaseNameOverrides
	if overrides.NoEpisodeTitle != nil && *overrides.NoEpisodeTitle {
		return ""
	}
	if overrides.EpisodeTitle != nil {
		return strings.TrimSpace(*overrides.EpisodeTitle)
	}
	return preferredGeneratedEpisodeTitle(meta)
}

func resolvedGenre(meta preparationstate.State) string {
	fallback := strings.TrimSpace(meta.Release.Genre)
	if !namingProviderMetadataCurrent(meta) {
		return fallback
	}

	tmdbGenres := ""
	if value := meta.ProviderMetadata.TMDB; value != nil && meta.Identity.TMDBID > 0 && value.TMDBID == meta.Identity.TMDBID {
		tmdbGenres = value.Genres
	}
	imdbGenres := ""
	if value := meta.ProviderMetadata.IMDB; value != nil && meta.Identity.IMDBID > 0 && value.IMDBID == meta.Identity.IMDBID {
		imdbGenres = value.Genres
	}
	tvdbGenres := ""
	if value := meta.ProviderMetadata.TVDB; value != nil && meta.Identity.TVDBID > 0 && value.TVDBID == meta.Identity.TVDBID {
		tvdbGenres = value.Genres
	}
	tvmazeGenres := ""
	if value := meta.ProviderMetadata.TVmaze; value != nil && meta.Identity.TVmazeID > 0 && value.TVmazeID == meta.Identity.TVmazeID {
		tvmazeGenres = value.Genres
	}
	return metautil.FirstNonEmptyTrimmed(tmdbGenres, imdbGenres, tvdbGenres, tvmazeGenres, fallback)
}

// resolveReleaseNameTitle selects naming fields from current matching provider
// metadata while preserving parsed values when no eligible snapshot exists.
// TV year is zero unless matching TVDB metadata supplies a positive alias year.
func resolveReleaseNameTitle(category string, meta preparationstate.State) (string, string, int) {
	title := strings.TrimSpace(meta.Release.Title)
	altTitle := strings.TrimSpace(meta.Release.Alt)
	year := meta.Release.Year
	isTV := strings.EqualFold(strings.TrimSpace(category), "TV")

	if isTV && matchingTVDBMetadataForNaming(meta) {
		tvdb := meta.ProviderMetadata.TVDB
		englishTitle := strings.TrimSpace(tvdb.NameEnglish)
		nativeTitle := strings.TrimSpace(tvdb.Name)
		imdbAKA := ""
		if matchingIMDBMetadataForNaming(meta) {
			imdbAKA = meta.ProviderMetadata.IMDB.AKA
		}
		if englishTitle != "" {
			title = englishTitle
			altTitle = fillProviderAlternateTitle(altTitle, title, imdbAKA, nativeTitle)
		} else {
			title = nativeTitle
			altTitle = fillProviderAlternateTitle(altTitle, title, imdbAKA)
		}
		if tvdb.Year > 0 && tvdb.YearFromAlias {
			year = tvdb.Year
		} else {
			year = 0
		}
		return title, altTitle, year
	}

	switch {
	case matchingTMDBMetadataForNaming(meta):
		tmdb := meta.ProviderMetadata.TMDB
		title = strings.TrimSpace(tmdb.Title)
		imdbAKA := ""
		if matchingIMDBMetadataForNaming(meta) {
			imdbAKA = meta.ProviderMetadata.IMDB.AKA
		}
		altTitle = fillProviderAlternateTitle(altTitle, title, tmdb.RetrievedAKA, imdbAKA, tmdb.OriginalTitle)
		if year == 0 && tmdb.Year > 0 {
			year = tmdb.Year
		}
	case matchingIMDBMetadataForNaming(meta):
		imdb := meta.ProviderMetadata.IMDB
		title = strings.TrimSpace(imdb.Title)
		altTitle = fillProviderAlternateTitle(altTitle, title, imdb.AKA)
		if year == 0 && imdb.Year > 0 {
			year = imdb.Year
		}
	}
	if isTV {
		year = 0
	}
	return title, altTitle, year
}

// matchingTMDBMetadataForNaming reports whether TMDB can supply the naming title.
func matchingTMDBMetadataForNaming(meta preparationstate.State) bool {
	value := meta.ProviderMetadata.TMDB
	return namingProviderMetadataCurrent(meta) && value != nil && meta.Identity.TMDBID > 0 &&
		value.TMDBID == meta.Identity.TMDBID && strings.TrimSpace(value.Title) != ""
}

// matchingIMDBMetadataForNaming reports whether IMDb can supply the naming title.
func matchingIMDBMetadataForNaming(meta preparationstate.State) bool {
	value := meta.ProviderMetadata.IMDB
	return namingProviderMetadataCurrent(meta) && value != nil && meta.Identity.IMDBID > 0 &&
		value.IMDBID == meta.Identity.IMDBID && strings.TrimSpace(value.Title) != ""
}

// matchingTVDBMetadataForNaming reports whether TVDB can supply the TV naming title.
func matchingTVDBMetadataForNaming(meta preparationstate.State) bool {
	value := meta.ProviderMetadata.TVDB
	return namingProviderMetadataCurrent(meta) && value != nil && meta.Identity.TVDBID > 0 &&
		value.TVDBID == meta.Identity.TVDBID &&
		(strings.TrimSpace(value.NameEnglish) != "" || strings.TrimSpace(value.Name) != "")
}

// namingProviderMetadataCurrent reports whether both provider IDs and snapshots
// are unscoped or belong to the prepared source.
func namingProviderMetadataCurrent(meta preparationstate.State) bool {
	return namingSourceMatches(meta.Identity.SourcePath, meta.SourcePath) &&
		namingSourceMatches(meta.ProviderMetadata.SourcePath, meta.SourcePath)
}

// namingSourceMatches accepts legacy unscoped data and case-insensitive source matches.
func namingSourceMatches(scopedPath, currentPath string) bool {
	trimmed := strings.TrimSpace(scopedPath)
	return trimmed == "" || strings.EqualFold(trimmed, strings.TrimSpace(currentPath))
}

// fillProviderAlternateTitle returns the first non-empty alternate that differs
// from the primary, preferring the parsed value before provider candidates.
func fillProviderAlternateTitle(current, primary string, candidates ...string) string {
	for _, candidate := range append([]string{current}, candidates...) {
		alternate := strings.TrimSpace(candidate)
		if len(alternate) > len("AKA ") && strings.EqualFold(alternate[:len("AKA ")], "AKA ") {
			alternate = strings.TrimSpace(alternate[len("AKA "):])
		}
		if alternate == "" || strings.EqualFold(strings.TrimSpace(primary), alternate) ||
			metautil.SimilarityRatio(titleIdentityKey(primary), titleIdentityKey(alternate)) >= 0.7 {
			continue
		}
		return "AKA " + alternate
	}
	return ""
}

func trimTrailingParentheticalYear(title string, year int) string {
	trimmed := strings.TrimSpace(title)
	if year <= 0 {
		return trimmed
	}
	suffix := "(" + strconv.Itoa(year) + ")"
	if !strings.HasSuffix(trimmed, suffix) {
		return trimmed
	}
	return strings.TrimSpace(strings.TrimSuffix(trimmed, suffix))
}

func inferReleaseTypeFromName(path string) string {
	base := strings.ToUpper(pathutil.Base(path))
	if webType := inferWebReleaseTypeFromFilename(pathutil.Base(path)); webType != "" {
		return webType
	}
	compact := strings.NewReplacer(".", "", "-", "", "_", "", " ", "").Replace(base)
	switch {
	case strings.Contains(compact, "REMUX"):
		return "REMUX"
	case strings.Contains(compact, "WEBDL"):
		return "WEBDL"
	case strings.Contains(compact, "WEBRIP"):
		return "WEBRIP"
	case strings.Contains(compact, "HDTV"):
		return "HDTV"
	case strings.Contains(compact, "DVDRIP"):
		return "DVDRIP"
	case strings.Contains(compact, "BDRIP"):
		return "ENCODE"
	}
	return ""
}

func inferReleaseSourceFromName(path string, typeValue string) string {
	base := strings.ToUpper(pathutil.Base(path))
	compact := strings.NewReplacer(".", "", "-", "", "_", "", " ", "").Replace(base)
	switch {
	case strings.Contains(compact, "HDDVD"):
		return "HDDVD"
	case strings.Contains(compact, "BLURAY") || strings.Contains(compact, "BLU") && strings.Contains(compact, "RAY") ||
		strings.Contains(compact, "BDREMUX") || strings.Contains(compact, "BDRIP") || strings.Contains(compact, "BDMV"):
		if strings.EqualFold(typeValue, "DISC") {
			return "Blu-ray"
		}
		return "BluRay"
	case strings.Contains(compact, "WEBDL") || strings.Contains(compact, "WEBRIP"):
		return "Web"
	case strings.Contains(compact, "HDTV"):
		return "HDTV"
	case strings.Contains(compact, "UHDTV"):
		return "UHDTV"
	case strings.Contains(compact, "PALDVD"):
		return "PAL DVD"
	case strings.Contains(compact, "NTSCDVD"):
		return "NTSC DVD"
	case strings.Contains(compact, "DVD"):
		return "DVD"
	}
	return ""
}

func isKnownReleaseSource(source string) bool {
	upper := strings.ToUpper(strings.TrimSpace(source))
	switch upper {
	case "BLURAY", "BLU-RAY", "BLU RAY", "BLU-RAY 3D", "BD", "BDMV", "DVD", "PAL DVD", "NTSC DVD", "HDDVD", "HD DVD", "WEB", "HDTV", "UHDTV":
		return true
	}
	return false
}

// normalizeAudioExtraOrder keeps object-audio markers after the channel token
// even when an override or parsed input supplied the older "Atmos 7.1" order.
func normalizeAudioExtraOrder(value string) string {
	fields := strings.Fields(strings.TrimSpace(value))
	for idx := 0; idx < len(fields)-1; idx++ {
		if !strings.EqualFold(fields[idx], "Atmos") || !isAudioChannelToken(fields[idx+1]) {
			continue
		}
		fields[idx], fields[idx+1] = fields[idx+1], fields[idx]
	}
	return strings.Join(fields, " ")
}

// isAudioChannelToken reports whether value is a release-name channel token
// that can be safely swapped before an adjacent Atmos marker.
func isAudioChannelToken(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	if strings.EqualFold(trimmed, "Unknown") {
		return true
	}
	parts := strings.Split(trimmed, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, char := range part {
			if char < '0' || char > '9' {
				return false
			}
		}
	}
	return true
}

func sourceIn(source string, candidates ...string) bool {
	for _, value := range candidates {
		if strings.EqualFold(strings.TrimSpace(source), strings.TrimSpace(value)) {
			return true
		}
	}
	return false
}

func removeHybrid(edition string) string {
	if edition == "" {
		return ""
	}
	fields := strings.Fields(edition)
	cleaned := fields[:0]
	for _, value := range fields {
		if strings.EqualFold(value, "Hybrid") {
			continue
		}
		cleaned = append(cleaned, value)
	}
	return strings.TrimSpace(strings.Join(cleaned, " "))
}

func normalizeNamingCategory(value string) string {
	upper := strings.ToUpper(strings.TrimSpace(value))
	if upper == "MOVIE" || upper == "TV" {
		return upper
	}
	return ""
}

func normalizeCategoryFromType(value string) string {
	return normalizeNamingCategory(value)
}

func inferCategoryFromMetadata(meta preparationstate.State) string {
	if meta.ProviderMetadata.TVDB != nil || meta.ProviderMetadata.TVmaze != nil {
		return "TV"
	}
	if meta.HasTVSeasonEpisodeSignal() {
		return "TV"
	}
	if category := normalizeNamingCategory(meta.Release.Category); category != "" {
		return category
	}
	if strings.TrimSpace(meta.DailyEpisodeDate) != "" {
		return "TV"
	}
	releaseType := strings.ToUpper(strings.TrimSpace(meta.Release.Type))
	if strings.Contains(releaseType, "TV") || strings.Contains(releaseType, "SERIES") || strings.Contains(releaseType, "EPISODE") {
		return "TV"
	}
	sourcePath := filepath.ToSlash(strings.TrimSpace(meta.SourcePath))
	pathHint := pathutil.Base(meta.SourcePath)
	if namingTVPathHintPattern.MatchString(sourcePath) || namingTVNameHintPattern.MatchString(pathHint) {
		return "TV"
	}
	if namingSubsPleaseHintPattern.MatchString(sourcePath) && namingAnimeEpisodeHint.MatchString(pathHint) {
		return "TV"
	}
	return "MOVIE"
}

func isCategoryType(value string) bool {
	upper := strings.ToUpper(strings.TrimSpace(value))
	return upper == "MOVIE" || upper == "TV"
}

func normalizeReleaseType(value string) string {
	upper := strings.ToUpper(strings.TrimSpace(value))
	if upper == "" {
		return ""
	}
	upper = strings.ReplaceAll(upper, "-", "")
	upper = strings.ReplaceAll(upper, " ", "")
	switch upper {
	case "WEBDL", "WEB-DL", "WEB_DL":
		return "WEBDL"
	case "WEBRIP", "WEB-RIP", "WEB_RIP":
		return "WEBRIP"
	}
	return upper
}

func normalizeReleaseTypeForCategory(category string, typeValue string, source string, sourcePath string) string {
	normalizedType := normalizeReleaseType(typeValue)
	if webType := webReleaseTypeFromSignals(normalizedType, source, sourcePath); webType != "" {
		return webType
	}
	if !strings.EqualFold(strings.TrimSpace(category), "TV") {
		return normalizedType
	}

	switch normalizedType {
	case "EP", "EPS", "EPISODE", "SERIES", "SEASON", "SEASONPACK", "TV", "TVSHOW":
		if inferred := inferReleaseTypeFromSource(source); inferred != "" {
			return inferred
		}
		if inferred := inferReleaseTypeFromName(sourcePath); inferred != "" {
			return inferred
		}
		return "ENCODE"
	}

	if normalizedType == "" {
		if inferred := inferReleaseTypeFromSource(source); inferred != "" {
			return inferred
		}
		if inferred := inferReleaseTypeFromName(sourcePath); inferred != "" {
			return inferred
		}
	}

	return normalizedType
}

func webReleaseTypeFromSignals(typeValue string, source string, sourcePath string) string {
	normalizedType := normalizeReleaseType(typeValue)
	switch normalizedType {
	case "WEBDL", "WEBRIP":
		return normalizedType
	case "", "ENCODE", "EP", "EPS", "EPISODE", "SERIES", "SEASON", "SEASONPACK", "TV", "TVSHOW":
	default:
		return ""
	}

	if inferred := inferReleaseTypeFromSource(source); inferred == "WEBDL" || inferred == "WEBRIP" {
		return inferred
	}
	if inferred := inferReleaseTypeFromName(sourcePath); inferred == "WEBDL" || inferred == "WEBRIP" {
		return inferred
	}
	if isWebSourceValue(source) {
		return "WEBDL"
	}
	return ""
}

func inferWebReleaseTypeFromFilename(filename string) string {
	lower := strings.ToLower(strings.TrimSpace(filename))
	if lower == "" {
		return ""
	}
	if namingWebDLFilenamePattern.MatchString(lower) {
		return "WEBDL"
	}
	if strings.Contains(lower, "webrip") {
		return "WEBRIP"
	}
	return ""
}

func inferReleaseTypeFromSource(source string) string {
	upper := strings.ToUpper(strings.TrimSpace(source))
	upper = strings.ReplaceAll(upper, "-", "")
	upper = strings.ReplaceAll(upper, " ", "")
	upper = strings.ReplaceAll(upper, "_", "")
	switch {
	case strings.Contains(upper, "WEBDL"):
		return "WEBDL"
	case strings.Contains(upper, "WEBRIP"):
		return "WEBRIP"
	case strings.Contains(upper, "HDTV"):
		return "HDTV"
	}
	return ""
}

func isWebSourceValue(source string) bool {
	upper := strings.ToUpper(strings.TrimSpace(source))
	upper = strings.ReplaceAll(upper, "-", "")
	upper = strings.ReplaceAll(upper, " ", "")
	upper = strings.ReplaceAll(upper, "_", "")
	return upper == "WEB" || upper == "WEBDL" || upper == "WEBRIP"
}
