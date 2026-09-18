// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dvl

import (
	"fmt"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func namePolicy() trackers.ReleaseNamePolicyBinding {
	return trackers.StructuredReleaseNamePolicy("unit3d/dvl/v2", trackers.StructuredNamePolicy{
		Defaults: applyDVLNameDefaults,
	})
}

func applyDVLNameDefaults(editor *trackers.NameEditor, meta api.UploadSubject, _ config.TrackerConfig) error {
	nameType := strings.ToUpper(strings.TrimSpace(meta.Type))
	source := strings.TrimSpace(meta.Source)
	if err := editor.MoveBefore(api.NameRoleAlternateTitle, api.NameRoleYear); err != nil {
		return fmt.Errorf("move DVL alternate title before year: %w", err)
	}

	switch {
	case nameType == "DVDRIP":
		if err := applyDVDRipNameOrder(editor); err != nil {
			return err
		}
	case nameType == "ENCODE" && unit3d.Resolution(meta) != "" && isDVDSource(source):
		if err := omitDVDEncodeMarkers(editor); err != nil {
			return err
		}
		if err := editor.Set(api.NameRoleSource, "DVDRip"); err != nil {
			return fmt.Errorf("set DVL DVD encode source: %w", err)
		}
	case strings.EqualFold(meta.DiscType, "DVD"):
		if err := addDVDSystemForDisc(editor, meta, source); err != nil {
			return err
		}
	case nameType == "REMUX" && source == "DVD":
		if err := addDVDSystemForRemux(editor, meta); err != nil {
			return err
		}
	}

	return addDVLLanguageMarker(editor, meta, nameType, source)
}

func applyDVDRipNameOrder(editor *trackers.NameEditor) error {
	if err := editor.Omit(api.NameRoleSource); err != nil {
		return fmt.Errorf("omit DVL DVDRip source: %w", err)
	}
	if err := editor.Include(api.NameRoleResolution); err != nil {
		return fmt.Errorf("include DVL DVDRip resolution: %w", err)
	}
	cluster := dvlAudioClusterRoles(editor.PresentRoles())
	videoAnchor := api.NameRoleVideoFormat
	if len(cluster) > 0 {
		videoAnchor = cluster[len(cluster)-1]
		if err := editor.MoveBefore(api.NameRoleVideoFormat, cluster[0]); err != nil {
			return fmt.Errorf("move DVL DVDRip format before audio cluster: %w", err)
		}
	}
	for _, role := range []api.ReleaseNameRole{api.NameRoleVideoEncode, api.NameRoleVideoCodec} {
		if err := editor.MoveAfter(role, videoAnchor); err != nil {
			return fmt.Errorf("move DVL DVDRip %s after format or audio: %w", role, err)
		}
	}
	if err := editor.MoveBefore(api.NameRoleResolution, api.NameRoleVideoFormat); err != nil {
		return fmt.Errorf("move DVL DVDRip resolution before format: %w", err)
	}
	return nil
}

func omitDVDEncodeMarkers(editor *trackers.NameEditor) error {
	for _, role := range []api.ReleaseNameRole{api.NameRoleEdition, api.NameRoleRepack} {
		if err := editor.Omit(role); err != nil {
			return fmt.Errorf("omit DVL DVD encode %s: %w", role, err)
		}
	}
	return nil
}

func addDVDSystemForDisc(editor *trackers.NameEditor, meta api.UploadSubject, source string) error {
	if !isDVDSource(source) || !isDVDSize(meta.Release.Size) || source != "DVD" {
		return nil
	}
	system := dvdSystem(unit3d.Resolution(meta))
	if system == "" {
		return nil
	}
	if err := editor.InsertBefore(api.NameRoleDVDSystem, system, api.NameRoleDVDSize); err != nil {
		return fmt.Errorf("insert DVL DVD disc system: %w", err)
	}
	return nil
}

func addDVDSystemForRemux(editor *trackers.NameEditor, meta api.UploadSubject) error {
	system := dvdSystem(unit3d.Resolution(meta))
	if system == "" {
		return nil
	}
	if err := editor.InsertBefore(api.NameRoleDVDSystem, system, api.NameRoleSource); err != nil {
		return fmt.Errorf("insert DVL DVD remux system: %w", err)
	}
	return nil
}

func addDVLLanguageMarker(editor *trackers.NameEditor, meta api.UploadSubject, nameType, source string) error {
	language := dvlLanguageMarker(meta)
	if language == "" {
		return nil
	}

	anchor := dvlLanguageAnchor(editor.PresentRoles(), nameType, source)
	if !anchor.Valid() {
		return nil
	}
	if err := editor.InsertBefore(api.NameRoleLanguageMarker, language, anchor); err != nil {
		return fmt.Errorf("insert DVL language marker: %w", err)
	}
	return nil
}

func dvlLanguageMarker(meta api.UploadSubject) string {
	if unit3d.HasEnglishLanguage(meta.AudioLanguages) || strings.EqualFold(meta.DiscType, "BDMV") {
		return ""
	}
	for _, value := range meta.AudioLanguages {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "", "zxx", "no linguistic content", "und", "undetermined":
			continue
		default:
			return strings.ToUpper(strings.TrimSpace(value))
		}
	}
	return ""
}

func dvlLanguageAnchor(roles []api.ReleaseNameRole, nameType, source string) api.ReleaseNameRole {
	if nameType == "DVDRIP" {
		return firstPresentNameRole(roles, api.NameRoleResolution, api.NameRoleVideoFormat)
	}
	if nameType == "REMUX" && isDVDSource(source) {
		if index := slices.Index(roles, api.NameRoleYear); index >= 0 && index+1 < len(roles) {
			return roles[index+1]
		}
	}
	return firstPresentNameRole(roles, api.NameRoleResolution, api.NameRoleService, api.NameRoleRegion, api.NameRoleDVDSystem,
		api.NameRoleSource, api.NameRoleDVDSize, api.NameRoleVideoFormat, api.NameRoleAudio, api.NameRoleGroup)
}

func firstPresentNameRole(present []api.ReleaseNameRole, candidates ...api.ReleaseNameRole) api.ReleaseNameRole {
	for _, role := range candidates {
		if slices.Contains(present, role) {
			return role
		}
	}
	return ""
}

func dvlAudioClusterRoles(present []api.ReleaseNameRole) []api.ReleaseNameRole {
	roles := make([]api.ReleaseNameRole, 0, 3)
	for _, role := range present {
		if role == api.NameRoleDubbed || role == api.NameRoleAudio || role == api.NameRoleDualAudio {
			roles = append(roles, role)
		}
	}
	return roles
}

func isDVDSource(source string) bool {
	switch strings.ToUpper(strings.TrimSpace(source)) {
	case "PAL DVD", "NTSC DVD", "DVD":
		return true
	default:
		return false
	}
}

func isDVDSize(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "DVD5") || strings.EqualFold(strings.TrimSpace(value), "DVD9")
}

func dvdSystem(resolution string) string {
	switch resolution {
	case "576i", "576p":
		return "PAL"
	case "480i", "480p":
		return "NTSC"
	default:
		return ""
	}
}
