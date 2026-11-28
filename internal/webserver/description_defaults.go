// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/description"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

type descriptionDefaultsTracker struct {
	Name  string                           `json:"name"`
	Items []trackers.DescriptionLayoutItem `json:"items"`
}

type descriptionDefaultsRequest struct {
	Tracker string
	Sample  string
	Draft   config.DescriptionSettingsConfig
}

func (s *Server) registerDescriptionDefaultsRoutes(mux *http.ServeMux) {
	registry, registryErr := trackerimpl.NewRegistry()
	schemas, schemaErr := config.OrderedTrackerSchemas()
	rendererFor := func(name string) (trackers.DescriptionSampleRenderer, []trackers.DescriptionLayoutItem) {
		definition, _ := registry.Lookup(name)
		renderer, ok := definition.(trackers.DescriptionSampleRenderer)
		if !ok {
			return nil, nil
		}
		return renderer, renderer.DescriptionLayout()
	}
	checkRegistry := func(w http.ResponseWriter) bool {
		if registryErr != nil || schemaErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Description previews are unavailable."})
			return false
		}
		return true
	}
	mux.HandleFunc("GET /api/app/ListDescriptionDefaultsTrackers", s.requireSession(func(w http.ResponseWriter, _ *http.Request, _ session) {
		if !checkRegistry(w) {
			return
		}
		cfg := s.backend.currentConfig()
		results := make([]descriptionDefaultsTracker, 0)
		for _, schema := range schemas {
			site, _ := config.TrackerConfigByName(cfg.Trackers.Trackers, schema.Name)
			if !config.TrackerConfigured(site, schema) {
				continue
			}
			if _, items := rendererFor(schema.Name); len(items) > 0 {
				for i := range items {
					items[i].CanHide = description.OptionalSection(items[i].ID)
				}
				results = append(results, descriptionDefaultsTracker{Name: schema.Name, Items: items})
			}
		}
		writeJSON(w, http.StatusOK, results)
	}))
	mux.HandleFunc("POST /api/app/PreviewDescriptionDefaults", s.requireSession(func(w http.ResponseWriter, r *http.Request, _ session) {
		if !checkRegistry(w) {
			return
		}
		var req descriptionDefaultsRequest
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
		if err := decodeJSON(r, &req); err != nil || len(req.Draft.SectionOrder) > 100 || len(req.Draft.ScreensPerRow) > 3 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid description settings."})
			return
		}
		name := strings.ToUpper(strings.TrimSpace(req.Tracker))
		cfg := s.backend.currentConfig()
		site, _ := config.TrackerConfigByName(cfg.Trackers.Trackers, name)
		configured := slices.ContainsFunc(schemas, func(schema config.TrackerSchema) bool {
			return schema.Name == name && config.TrackerConfigured(site, schema)
		})
		renderer, items := rendererFor(name)
		if !configured || len(items) == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "No description preview is available for this configured tracker."})
			return
		}
		sample, images, ok := buildDescriptionSample(req.Sample)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Unknown sample release type."})
			return
		}
		// Pass only presentation preferences, never the stored tracker secrets.
		previewSite := config.TrackerConfig{
			AddWebSourceToDesc:  site.AddWebSourceToDesc,
			ScreenshotBlankLine: site.ScreenshotBlankLine,
			APIUpload:           site.APIUpload,
			ImgRehost:           site.ImgRehost,
			ImageCount:          site.ImageCount,
		}
		raw, err := renderer.RenderDescriptionSample(r.Context(), sample, req.Draft, previewSite)
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "This tracker could not render the sample description."})
			return
		}
		writeJSON(w, http.StatusOK, description.RenderSample(raw, images))
	}))
}

func buildDescriptionSample(kind string) (trackers.DescriptionSample, map[string]string, bool) {
	const report = "General\nFormat : Matroska\nDuration : 1 h 36 min\n\nVideo\nFormat : AVC\nWidth : 1 920 pixels\n\nAudio\nFormat : AAC\nLanguage : English"
	images := make(map[string]string)
	image := func(id, title, color string) string {
		address := "https://description-sample.invalid/" + id + ".png"
		svg := fmt.Sprintf(
			`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 320 180" width="320" height="180"><rect width="320" height="180" fill="%s"/><path d="M0 160l90-75 45 39 65-66 120 108v14H0z" fill="#172338"/><text x="16" y="29" fill="white" font-family="sans-serif" font-size="17">%s</text></svg>`,
			color,
			title,
		)
		images[address] = "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg))
		return address
	}
	logo := image("logo", "The Lantern Coast", "#244753")
	sample := trackers.DescriptionSample{
		Subject: api.UploadSubject{
			DescriptionTemplate: "[b]The Lantern Coast (2026)[/b]",
			ReleaseName:         "The.Lantern.Coast.2026.1080p.WEB-DL-GRP",
			Release:             api.ReleaseInfo{Title: "The Lantern Coast", Year: 2026},
			Identity:            api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
			ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{
				Logo:     logo,
				Poster:   image("poster", "The Lantern Coast · Poster", "#365362"),
				Overview: "A lighthouse keeper investigates strange lights beyond the harbour.",
				Genres:   "Drama",
			}},
			Type:            "WEBDL",
			ServiceLongName: "Sample Stream",
		},
		Assets: trackers.DescriptionAssets{
			Description: "[quote]Sample release notes. No media or tracker account is used.[/quote]",
		},
		Report: report,
	}
	switch kind {
	case "", "movie":
	case "episode":
		sample.Subject.EpisodeTitle = "Signals at Sea"
		sample.Subject.SeasonInt = 1
		sample.Subject.EpisodeInt = 3
		sample.Subject.ProviderMetadata.TMDB.Category = "TV"
		sample.Subject.Release.Category = "TV"
		sample.Subject.Identity.Category = api.CanonicalCategoryTV
		sample.Subject.EpisodeOverview = "A lighthouse keeper investigates strange lights beyond the harbour."
	case "dvd", "bluray":
		sample.Subject.Type = "REMUX"
		sample.Subject.DiscType = "BDMV"
		sample.Report = "DISC INFO:\nDisc Title: The Lantern Coast\n\nPLAYLIST REPORT:\nName: 00001.MPLS\nLength: 1:36:00\n\nVIDEO:\nMPEG-4 AVC Video / 1080p\n\nAUDIO:\nEnglish / DTS-HD Master Audio / 5.1"
		if kind == "dvd" {
			sample.Subject.DiscType = "DVD"
			sample.Subject.DVDVOBMediaInfoText = "Video\nFormat : MPEG Video\n\nAudio\nFormat : AC-3"
			sample.Report = sample.Subject.DVDVOBMediaInfoText
			menu := image("menu", "Disc menu", "#4f4665")
			sample.Assets.MenuImages = []api.ScreenshotImage{{
				ImgURL: menu,
				RawURL: menu,
				WebURL: menu,
				Host:   "pixhost",
			}}
		}
		sample.Subject.ProviderMetadata.Bluray = &api.BlurayMetadata{
			SelectedReleaseID: "sample",
			Candidates: []api.BlurayReleaseCandidate{{
				ReleaseID:   "sample",
				URL:         "https://description-sample.invalid/disc-edition",
				CoverImages: []api.BlurayImage{{URL: image("cover", "Sample disc cover", "#4b485d")}},
			}},
		}
	default:
		return trackers.DescriptionSample{}, nil, false
	}
	colors := []string{"#3e5177", "#365d57", "#754d5d"}
	for index, color := range colors {
		id := fmt.Sprintf("frame-%d", index+1)
		address := image(id, fmt.Sprintf("Sample screenshot %d", index+1), color)
		sample.Assets.Screenshots = append(sample.Assets.Screenshots, api.ScreenshotImage{
			ImgURL:      address,
			RawURL:      address,
			WebURL:      address,
			Host:        "pixhost",
			Tonemapping: api.ScreenshotTonemapped,
		})
	}
	sample.Assets.Description += "\n\n[spoiler=Audio Plots]\n[img]" + image("audio", "English AAC · waveform", "#405b68") +
		"[/img]\n[/spoiler]\n\n[spoiler=HDR plots]\n[img]" + image("hdr", "HDR10 · luminance", "#69537f") + "[/img]\n[/spoiler]"
	sample.Assets.Description += "\n\n[spoiler=source_comparisons]\n[comparison=Source, Encode]\n" +
		image("comparison-source", "Source frame", "#274452") + "\n" +
		image("comparison-encode", "Encoded frame", "#355451") + "\n[/comparison]\n[/spoiler]"
	return sample, images, true
}
