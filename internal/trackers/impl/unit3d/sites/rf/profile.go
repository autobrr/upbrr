package rf

import (
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns RF's no-group naming, site-specific type and resolution
// mappings, required release-group dupe policy, and conditional owned image host.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:    "RF",
		BaseURL: "https://reelflix.cc",
		Rules:   Rules(),
		Site: unit3d.SiteProfile{
			BuildName:           buildName,
			ResolveTypeID:       typeID,
			ResolveResolutionID: resolutionID,
		},
		DupePolicy: &trackers.DupePolicy{
			RequireReleaseGroup: true,
		},
		ImageHost: &trackers.ImageHostPolicy{
			ConditionalHost:      "reelflix",
			OwnedHosts:           []string{"reelflix"},
			EnableWhenConfigured: true,
		},
		TorrentIdentity: &trackers.TorrentIdentityPolicy{
			TrackerURLPatterns: []string{"https://reelflix.xyz"},
			CommentURLPatterns: []string{"https://reelflix.xyz"},
			DetailIDPattern:    `(?i)reelflix\.(?:cc|xyz)/torrents/(\d+)`,
		},
	}
}
func buildName(meta api.UploadSubject, _ config.TrackerConfig) string {
	return addNoGroup(meta, "NoGroup")
}
func addNoGroup(meta api.UploadSubject, suffix string) string {
	name := strings.TrimSpace(meta.ReleaseName)
	if name == "" {
		name = strings.TrimSpace(meta.ReleaseNameNoTag)
	}
	name = strings.TrimSpace(strings.Join(strings.Fields(name), " "))
	tag := strings.TrimSpace(strings.TrimPrefix(meta.Tag, "-"))
	if tag != "" && !strings.EqualFold(tag, "nogrp") && !strings.EqualFold(tag, "nogroup") && !strings.EqualFold(tag, "unknown") &&
		!strings.EqualFold(tag, "-unk-") {
		return name
	}
	if name == "" || strings.HasSuffix(strings.ToUpper(name), "-"+strings.ToUpper(suffix)) {
		return name
	}
	return name + "-" + suffix
}
func typeID(meta api.UploadSubject) string {
	return map[string]string{
		"DISC":   "43",
		"REMUX":  "40",
		"WEBDL":  "42",
		"WEBRIP": "45",
		"ENCODE": "41",
		"HDTV":   "35",
	}[unit3d.InferType(meta)]
}
func resolutionID(meta api.UploadSubject) string {
	if value, ok := map[string]string{
		"4320p": "1",
		"2160p": "2",
		"1080p": "3",
		"1080i": "4",
		"720p":  "5",
		"576p":  "6",
		"576i":  "7",
		"480p":  "8",
		"480i":  "9",
	}[unit3d.Resolution(meta)]; ok {
		return value
	}
	return "10"
}
