// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"context"
	"errors"
	"fmt"
	"strings"

	descriptionunit3d "github.com/autobrr/upbrr/internal/description/unit3d"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/ruletypes"
	"github.com/autobrr/upbrr/pkg/api"
)

// Definition provides one Unit3D site profile through the shared tracker contracts.
type Definition struct {
	profile Profile
}

// Profile declares Unit3D site identity, endpoint, and site-owned policies.
type Profile struct {
	// Name is the stable normalized tracker identifier.
	Name string
	// BaseURL is the site's default Unit3D endpoint.
	BaseURL string
	// Site contains optional site-specific payload callbacks.
	Site SiteProfile
	// Rules contains site-specific release validation requirements.
	Rules *ruletypes.RuleSet
	// AudioPolicy contains site-specific multi-language constraints.
	AudioPolicy *trackers.AudioPolicy
	// DupePolicy contains site-specific duplicate comparison settings.
	DupePolicy *trackers.DupePolicy
	// UploadArtifact contains site-specific torrent personalization settings.
	UploadArtifact *trackers.UploadArtifactPolicy
	// BannedPolicy contains optional dynamic banned-group retrieval settings.
	BannedPolicy *trackers.BannedGroupPolicy
	// BannedGroups is the site's static banned release-group list.
	BannedGroups []string
	// ImageHost contains accepted image-host restrictions.
	ImageHost *trackers.ImageHostPolicy
	// TorrentIdentity contains site-specific torrent identity aliases or overrides.
	TorrentIdentity *trackers.TorrentIdentityPolicy
	// ClaimPolicy contains generic claim-orchestration settings.
	ClaimPolicy *trackers.ClaimPolicy
	// DescriptionGroup identifies the site's description override group.
	DescriptionGroup string
}

// New returns a Unit3D definition for the requested registered profile name.
func New(name string) *Definition {
	return NewWithProfile(Profile{Name: name})
}

// NewWithProfile constructs a Unit3D definition from an explicitly composed
// site profile.
func NewWithProfile(profile Profile) *Definition {
	profile.Name = strings.ToUpper(strings.TrimSpace(profile.Name))
	profile.BaseURL = strings.TrimSpace(profile.BaseURL)
	profile.DescriptionGroup = strings.ToLower(strings.TrimSpace(profile.DescriptionGroup))
	profile.BannedGroups = append([]string(nil), profile.BannedGroups...)
	return &Definition{profile: profile}
}

// DescriptionGroup returns the site-specific description override group.
func (d *Definition) DescriptionGroup() string { return d.profile.DescriptionGroup }

// DefaultBaseURL returns the site's endpoint used when configuration supplies none.
func (d *Definition) DefaultBaseURL() string { return d.profile.BaseURL }

// ClaimPolicy returns an independent copy of the site's claim policy.
func (d *Definition) ClaimPolicy() *trackers.ClaimPolicy {
	if d.profile.ClaimPolicy == nil {
		return nil
	}
	policy := *d.profile.ClaimPolicy
	return &policy
}

// UploadArtifactPolicy returns an independent copy of the site's torrent personalization policy.
func (d *Definition) UploadArtifactPolicy() *trackers.UploadArtifactPolicy {
	if d.profile.UploadArtifact == nil {
		return nil
	}
	policy := *d.profile.UploadArtifact
	return &policy
}

// MetadataPolicy returns the metadata requirements shared by Unit3D sites.
func (d *Definition) MetadataPolicy() *trackers.TrackerMetadataPolicy {
	return &trackers.TrackerMetadataPolicy{
		Requirements: []trackers.MetadataRequirement{{Scope: trackers.MetadataScopeAny, AnyOf: []trackers.MetadataField{trackers.MetadataFieldTMDB}}},
	}
}

// Rules declares validation required by every Unit3D upload.
func (d *Definition) Rules() *ruletypes.RuleSet {
	if d.profile.Rules == nil {
		return &ruletypes.RuleSet{RequireValidMISetting: true}
	}
	rules := *d.profile.Rules
	rules.RequireValidMISetting = true
	return &rules
}

// BannedGroups returns a defensive copy of the site's static release-group blacklist.
func (d *Definition) BannedGroups() []string { return append([]string(nil), d.profile.BannedGroups...) }

// BannedGroupPolicy returns the site's dynamic banned-group retrieval settings.
func (d *Definition) BannedGroupPolicy() *trackers.BannedGroupPolicy { return d.profile.BannedPolicy }

// DupePolicy returns the site's duplicate comparison settings.
func (d *Definition) DupePolicy() *trackers.DupePolicy { return d.profile.DupePolicy }

// AudioPolicy returns the site's multi-language constraints.
func (d *Definition) AudioPolicy() *trackers.AudioPolicy { return d.profile.AudioPolicy }

// ImageHostPolicy returns the site's accepted image-host restrictions.
func (d *Definition) ImageHostPolicy() *trackers.ImageHostPolicy { return d.profile.ImageHost }

// TorrentIdentityPolicy returns common Unit3D identity behavior plus site aliases.
func (d *Definition) TorrentIdentityPolicy() *trackers.TorrentIdentityPolicy {
	policy := trackers.TorrentIdentityPolicy{
		TrackerURLPatterns: []string{d.profile.BaseURL},
		CommentURLPatterns: []string{d.profile.BaseURL},
		DetailIDPattern:    `/(\d+)`,
	}
	if d.profile.TorrentIdentity != nil {
		policy.TrackerURLPatterns = append(policy.TrackerURLPatterns, d.profile.TorrentIdentity.TrackerURLPatterns...)
		policy.CommentURLPatterns = append(policy.CommentURLPatterns, d.profile.TorrentIdentity.CommentURLPatterns...)
		if d.profile.TorrentIdentity.DetailIDPattern != "" {
			policy.DetailIDPattern = d.profile.TorrentIdentity.DetailIDPattern
		}
		policy.WorkingTrackerID = d.profile.TorrentIdentity.WorkingTrackerID
		policy.SearchPreference = d.profile.TorrentIdentity.SearchPreference
	}
	return &policy
}

// Name returns the stable tracker identifier for this Unit3D profile.
func (d *Definition) Name() string {
	return d.profile.Name
}

// TrackerFamily identifies the definition as Unit3D-backed.
func (d *Definition) TrackerFamily() trackers.Family { return trackers.FamilyUnit3D }

// AuthCapability declares the API key required by standard Unit3D APIs.
func (d *Definition) AuthCapability() api.TrackerAuthCapability {
	return api.TrackerAuthCapability{
		TrackerID:      d.profile.Name,
		DisplayName:    d.profile.Name,
		AuthKind:       "api_key",
		RequiresAPIKey: true,
	}
}

// Prepare builds a fresh intent-scoped tracker plan for this Unit3D profile.
func (d *Definition) Prepare(ctx context.Context, input trackers.PreparationInput) (trackers.TrackerPlan, *trackers.PreparationFailure) {
	return trackers.PrepareAdapter(ctx, input, d.prepareDescription, d.prepareDryRun, d.submit)
}

func (d *Definition) submit(ctx context.Context, req trackers.PreparationInput) (api.UploadSummary, error) {
	if d.profile.BaseURL != "" {
		return uploadUnit3D(ctx, req, d.profile.BaseURL, d.profile.Site)
	}
	select {
	case <-ctx.Done():
		return api.UploadSummary{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}
	if req.Logger != nil {
		req.Logger.Infof("trackers: %s upload not implemented (unit3d scaffold)", d.profile.Name)
	}
	return api.UploadSummary{}, internalerrors.ErrNotImplemented
}

func (d *Definition) prepareDryRun(ctx context.Context, req trackers.PreparationInput) (api.TrackerDryRunEntry, error) {
	if d.profile.BaseURL != "" {
		return buildUploadDryRunUnit3D(ctx, req, d.profile.BaseURL, d.profile.Site)
	}
	select {
	case <-ctx.Done():
		return api.TrackerDryRunEntry{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}
	if req.Logger != nil {
		req.Logger.Infof("trackers: dry-run decision=not_implemented tracker=%s", d.profile.Name)
	}
	return api.TrackerDryRunEntry{}, internalerrors.ErrNotImplemented
}

func (d *Definition) prepareDescription(ctx context.Context, req trackers.PreparationInput) (trackers.DescriptionResult, error) {
	select {
	case <-ctx.Done():
		return trackers.DescriptionResult{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}
	if req.Logger != nil {
		req.Logger.Debugf("trackers: %s building unit3d description", d.profile.Name)
	}
	var err error
	assets := trackers.DescriptionAssets{}
	if req.Assets != nil {
		assets = *req.Assets
	} else {
		assets, err = trackers.PreparedDescriptionAssets(req.Assets)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return trackers.DescriptionResult{}, fmt.Errorf("trackers: %w", err)
			}
			if req.Logger != nil {
				req.Logger.Warnf("trackers: description assets failed tracker=%s err=%s", d.profile.Name, redaction.RedactValue(err.Error(), nil))
			}
			assets = trackers.DescriptionAssets{}
		}
	}
	description := strings.TrimSpace(assets.Description)
	if !assets.Final {
		description, err = buildUnit3DDescription(
			ctx,
			d.profile.Name,
			req.Meta,
			req.Runtime.DescriptionConfig(),
			req.TrackerConfig,
			req.Logger,
			assets.Description,
			assets.MenuImages,
			assets.Screenshots,
			d.profile.Site,
		)
		if err != nil {
			return trackers.DescriptionResult{}, err
		}
	}
	if req.Intent == trackers.PreparationIntentDryRun && description != "" {
		descriptionunit3d.SaveDescriptionDebug(api.NewDescriptionSubject(req.Meta), "unit3d", req.Runtime.DBPath, description, req.Logger)
	}
	return trackers.DescriptionResult{
		Group:       "unit3d",
		Description: description,
	}, nil
}

// Register adds scaffolded Unit3D definitions for the supplied tracker names.
func Register(registry *trackers.Registry, trackersList []string) error {
	if registry == nil {
		return nil
	}
	for _, name := range trackersList {
		if err := registry.Register(New(name)); err != nil {
			return fmt.Errorf("trackers: %w", err)
		}
	}
	return nil
}

// RegisterProfiles explicitly registers composed Unit3D site profiles.
func RegisterProfiles(registry *trackers.Registry, profiles []Profile) error {
	if registry == nil {
		return nil
	}
	for _, profile := range profiles {
		if strings.TrimSpace(profile.Name) == "" {
			return errors.New("trackers: unit3d profile has empty name")
		}
		definition := NewWithProfile(profile)
		if err := registry.Register(definition); err != nil {
			return fmt.Errorf("trackers: %w", err)
		}
	}
	return nil
}
