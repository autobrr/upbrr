// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

const (
	// ReleaseNameDocumentVersionV1 identifies the first structured generated-name representation.
	ReleaseNameDocumentVersionV1 = "release-name-document/v1"
)

// ReleaseNameRole identifies one semantic component in a generated release name.
type ReleaseNameRole string

const (
	NameRoleTitle          ReleaseNameRole = "title"
	NameRoleAlternateTitle ReleaseNameRole = "alternate_title"
	NameRoleYear           ReleaseNameRole = "year"
	NameRoleSeason         ReleaseNameRole = "season"
	NameRoleEpisode        ReleaseNameRole = "episode"
	NameRoleEpisodeTitle   ReleaseNameRole = "episode_title"
	NameRoleDailyDate      ReleaseNameRole = "daily_date"
	NameRolePart           ReleaseNameRole = "part"
	NameRoleThreeD         ReleaseNameRole = "3d"
	NameRoleEdition        ReleaseNameRole = "edition"
	NameRoleHybrid         ReleaseNameRole = "hybrid"
	NameRoleRepack         ReleaseNameRole = "repack"
	NameRoleResolution     ReleaseNameRole = "resolution"
	NameRoleRegion         ReleaseNameRole = "region"
	NameRoleUHD            ReleaseNameRole = "uhd"
	NameRoleSource         ReleaseNameRole = "source"
	NameRoleDVDSystem      ReleaseNameRole = "dvd_system"
	NameRoleDVDSize        ReleaseNameRole = "dvd_size"
	NameRoleService        ReleaseNameRole = "service"
	NameRoleVideoFormat    ReleaseNameRole = "video_format"
	NameRoleHDR            ReleaseNameRole = "hdr"
	NameRoleVideoCodec     ReleaseNameRole = "video_codec"
	NameRoleVideoEncode    ReleaseNameRole = "video_encode"
	NameRoleAudio          ReleaseNameRole = "audio"
	NameRoleDubbed         ReleaseNameRole = "dubbed"
	NameRoleDualAudio      ReleaseNameRole = "dual_audio"
	NameRoleLanguageMarker ReleaseNameRole = "language_marker"
	NameRoleLocale         ReleaseNameRole = "locale"
	NameRoleDistributor    ReleaseNameRole = "distributor"
	NameRoleSubtitleMarker ReleaseNameRole = "subtitle_marker"
	NameRoleGroup          ReleaseNameRole = "group"
	NameRoleOriginalGroup  ReleaseNameRole = "original_group"
)

// ReleaseNameComponent preserves one generated component's semantic identity,
// rendered value, retained finalized value, manual authority, and layout.
// Present controls rendering; AvailableValue retains known evidence even when
// presentation omits it. An empty available value is not permission to infer one.
// Manual protects the component from optional tracker defaults, not declared
// mandatory tracker authority.
// Join is emitted immediately before Value when the component is present; the
// first rendered component never receives its Join. AttachTo preserves layouts
// that concatenate this component to one immediately preceding semantic role.
// When no listed role immediately precedes it, Render uses Join instead.
// Empty AttachTo lists are omitted from JSON; absence means no attachment anchors.
type ReleaseNameComponent struct {
	Role           ReleaseNameRole
	Value          string
	AvailableValue string
	Present        bool
	Manual         bool
	Join           string
	AttachTo       []ReleaseNameRole `json:"AttachTo,omitempty"`
}

// ReleaseNameDocument is the canonical structured representation of one
// generated release-name layout. Components retain absent positions so policy
// projections can change presence without rediscovering name semantics.
type ReleaseNameDocument struct {
	Version    string
	Components []ReleaseNameComponent
}

// Clone returns an independently mutable copy of the document.
func (d *ReleaseNameDocument) Clone() *ReleaseNameDocument {
	if d == nil {
		return nil
	}
	cloned := *d
	cloned.Components = append([]ReleaseNameComponent(nil), d.Components...)
	for index := range cloned.Components {
		cloned.Components[index].AttachTo = append([]ReleaseNameRole(nil), d.Components[index].AttachTo...)
	}
	return &cloned
}

// Component returns the component for role, if this layout contains it.
func (d *ReleaseNameDocument) Component(role ReleaseNameRole) (ReleaseNameComponent, bool) {
	if d == nil {
		return ReleaseNameComponent{}, false
	}
	for _, component := range d.Components {
		if component.Role == role {
			return component, true
		}
	}
	return ReleaseNameComponent{}, false
}

// Validate checks the version, semantic roles, component uniqueness, and
// renderable component values without changing the document.
func (d *ReleaseNameDocument) Validate() error {
	if d == nil {
		return errors.New("release name document is required")
	}
	if d.Version != ReleaseNameDocumentVersionV1 {
		return fmt.Errorf("unsupported release name document version %q", d.Version)
	}
	if len(d.Components) == 0 {
		return errors.New("release name document components are required")
	}
	roles := make(map[ReleaseNameRole]struct{}, len(d.Components))
	for _, component := range d.Components {
		if !component.Role.Valid() {
			return fmt.Errorf("unsupported release name role %q", component.Role)
		}
		if _, exists := roles[component.Role]; exists {
			return fmt.Errorf("duplicate release name role %q", component.Role)
		}
		roles[component.Role] = struct{}{}
		for _, anchor := range component.AttachTo {
			if !anchor.Valid() {
				return fmt.Errorf("unsupported release name attachment role %q", anchor)
			}
			if anchor == component.Role {
				return fmt.Errorf("release name role %q cannot attach to itself", component.Role)
			}
		}
		if component.Present && strings.TrimSpace(component.Value) == "" {
			return fmt.Errorf("present release name role %q requires a value", component.Role)
		}
	}
	return nil
}

// Render renders the document's exact layout into legacy name projections.
func (d *ReleaseNameDocument) Render() ReleaseNameVariant {
	if d == nil {
		return ReleaseNameVariant{}
	}
	return ReleaseNameVariant{
		NameNoTag: renderReleaseNameComponents(d.Components, true),
		Name:      renderReleaseNameComponents(d.Components, false),
		CleanName: CleanReleaseNameFilename(renderReleaseNameComponents(d.Components, false)),
	}
}

func renderReleaseNameComponents(components []ReleaseNameComponent, omitGroup bool) string {
	var rendered strings.Builder
	previousRole := ReleaseNameRole("")
	for _, component := range components {
		if !component.Present || (omitGroup && component.Role == NameRoleGroup) {
			continue
		}
		value := strings.Join(strings.Fields(component.Value), " ")
		if value == "" {
			continue
		}
		if rendered.Len() > 0 && !slices.Contains(component.AttachTo, previousRole) {
			rendered.WriteString(component.Join)
		}
		rendered.WriteString(value)
		previousRole = component.Role
	}
	return rendered.String()
}

// CleanReleaseNameFilename applies canonical filename character substitutions to
// a release name or an individual component, without interpreting naming roles.
func CleanReleaseNameFilename(name string) string {
	for _, invalid := range "<>:\"/\\|?*" {
		name = strings.ReplaceAll(name, string(invalid), "-")
	}
	return name
}

// Valid reports whether role is a supported component identity.
func (role ReleaseNameRole) Valid() bool {
	switch role {
	case NameRoleTitle, NameRoleAlternateTitle, NameRoleYear, NameRoleSeason, NameRoleEpisode,
		NameRoleEpisodeTitle, NameRoleDailyDate, NameRolePart, NameRoleThreeD,
		NameRoleEdition, NameRoleHybrid, NameRoleRepack, NameRoleResolution,
		NameRoleRegion, NameRoleUHD, NameRoleSource, NameRoleDVDSystem, NameRoleDVDSize, NameRoleService,
		NameRoleVideoFormat, NameRoleHDR, NameRoleVideoCodec, NameRoleVideoEncode, NameRoleAudio,
		NameRoleDubbed, NameRoleDualAudio, NameRoleLanguageMarker, NameRoleLocale, NameRoleDistributor, NameRoleSubtitleMarker, NameRoleGroup, NameRoleOriginalGroup:
		return true
	default:
		return false
	}
}
