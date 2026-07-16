// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Definition provides BTN tracker preparation and optional policy capabilities.
type Definition struct{}

// New returns a fresh BTN tracker definition.
func New() *Definition {
	return &Definition{}
}

// Name returns the stable BTN tracker identifier.
func (d *Definition) Name() string {
	return "BTN"
}

// MetadataPolicy returns BTN metadata requirements.
func (d *Definition) MetadataPolicy() *trackers.TrackerMetadataPolicy {
	return &trackers.TrackerMetadataPolicy{
		RequireKnownCategory: true,
		Requirements: []trackers.MetadataRequirement{
			{Scope: trackers.MetadataScopeTV, AnyOf: []trackers.MetadataField{trackers.MetadataFieldIMDB, trackers.MetadataFieldTVDB}},
		},
	}
}

// UploadArtifactPolicy returns BTN torrent personalization settings.
func (d *Definition) UploadArtifactPolicy() *trackers.UploadArtifactPolicy {
	return &trackers.UploadArtifactPolicy{Source: "BTN", RequireAnnounce: true}
}

// DataLookupConfigured reports whether the BTN API token is available.
func (d *Definition) DataLookupConfigured(cfg config.Config) bool {
	return len(config.ResolveBTNAPIToken(cfg)) >= 25
}

// DataLookupPolicy returns BTN metadata lookup orchestration settings.
func (d *Definition) DataLookupPolicy() *trackers.DataLookupPolicy {
	return &trackers.DataLookupPolicy{DeferWhenCollectingImages: true}
}

// BannedGroups returns BTN's static banned release-group list.
func (d *Definition) BannedGroups() []string {
	return []string{
		"3LTON", "4yEo", "7VFr33104D", "AFG", "AniHLS", "AnimeRG", "AniURL", "DeadFish", "ELiTE", "eSc",
		"EVO", "FGT", "FUM", "GalaxyTV", "GRANiTEN", "HAiKU", "Hi10", "ION10", "JFF", "JIVE", "LOAD", "MeGusta",
		"mSD", "NhaNc3", "NOIVTC", "PHOENiX", "PlaySD", "playXD", "Pr1M371M3", "RAPiDCOWS", "REsuRRecTioN", "RMTeam",
		"ROBOTS", "RUBiK", "SPASM", "Telly", "TM", "URANiME", "ViSiON", "W45Ps", "xRed", "XS", "ZKBL", "ZmN", "ZMNT", "[Oj]",
	}
}

// Prepare builds a fresh intent-scoped BTN tracker plan.
func (d *Definition) Prepare(ctx context.Context, input trackers.PreparationInput) (trackers.TrackerPlan, *trackers.PreparationFailure) {
	return trackers.PrepareAdapter(ctx, input, d.prepareDescription, d.prepareDryRun, d.submit)
}

func (d *Definition) submit(ctx context.Context, req trackers.PreparationInput) (api.UploadSummary, error) {
	return upload(ctx, req)
}

func (d *Definition) prepareDryRun(ctx context.Context, req trackers.PreparationInput) (api.TrackerDryRunEntry, error) {
	return buildUploadDryRun(ctx, req)
}

func (d *Definition) prepareDescription(ctx context.Context, req trackers.PreparationInput) (trackers.DescriptionResult, error) {
	select {
	case <-ctx.Done():
		return trackers.DescriptionResult{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		assets = trackers.DescriptionAssets{}
	}

	description := strings.TrimSpace(assets.Description)
	if description == "" {
		description = strings.TrimSpace(req.Meta.DescriptionOverride)
	}
	if description == "" {
		description = "No description provided."
	}

	return trackers.DescriptionResult{
		Group:       "btn",
		Description: description,
	}, nil
}

func validateBTNRequest(req trackers.PreparationInput) error {
	category, err := req.Meta.Identity.RequireCategory()
	if err != nil || category != api.CanonicalCategoryTV {
		return errors.New("trackers: BTN only supports TV uploads")
	}
	if strings.TrimSpace(req.Runtime.BTNAPIToken) == "" {
		return errors.New("trackers: BTN requires trackers.BTN.api_key")
	}
	return nil
}

var btnInternalGroups = []string{
	"BTW",
	"ESPNtb",
	"HiSD",
	"HRiP",
	"iPRiP",
	"iT00NZ",
	"JJ",
	"LoTV",
	"NTb",
	"PreBS",
	"RAWR",
	"TTVa",
	"TVSmash",
}

func isBTNInternalGroup(meta api.UploadSubject) bool {
	if strings.TrimSpace(meta.Tag) == "" {
		return false
	}

	group := strings.ToLower(strings.TrimPrefix(meta.Tag, "-"))
	for _, value := range btnInternalGroups {
		if strings.ToLower(value) == group {
			return true
		}
	}
	return false
}
