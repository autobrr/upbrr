// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBTNSceneReleasesBypassActiveClaims(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name      string
		scene     bool
		sceneName string
		wantClaim bool
	}{
		{name: "confirmed scene", scene: true},
		{name: "resolved scene name", sceneName: "Example.Show.S01E01.2160p-GRP"},
		{name: "unknown origin remains claimed", wantClaim: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.Config{}
			cfg.MainSettings.DBPath = filepath.Join(t.TempDir(), "test.sqlite")
			cachePath, err := btnClaimsPath(cfg.MainSettings.DBPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := writeBTNClaimedCache(cachePath, btnTitleVariants("Example Show")); err != nil {
				t.Fatal(err)
			}
			checker := New().NewClaimChecker(cfg, nil)
			claimed, err := checker.HasClaim(t.Context(), api.UploadSubject{
				SeasonInt:   1,
				EpisodeInt:  1,
				Identity:    api.ExternalIdentity{Category: "TV"},
				ReleaseName: "Example.Show.S01E01.2160p-GRP",
				Scene:       tt.scene,
				SceneName:   tt.sceneName,
			})
			if err != nil || claimed != tt.wantClaim {
				t.Fatalf("claimed=%t want=%t err=%v", claimed, tt.wantClaim, err)
			}
		})
	}
}

func TestExtractBTNClaimRecordsPreservesSitesGroupsAndConflicts(t *testing.T) {
	t.Parallel()

	records := extractBTNClaimRecords(`
<div id="content1405482" class="postcontent">
  <strong>Current Shows:</strong><br>
  Example Show -- <span><strong>HDB</strong></span> | <span><strong>BTN</strong></span> -- <strong><span>NTb</span></strong> -- AMZN<br>
  Example Show -- HDB | BTN -- NTb -- duplicate row<br>
  Example Show -- BTN -- Other -- NF<br>
  HDB Only -- HDB -- HDBGRP -- DSNP<br>
  Upcoming Shows:<br>
  Ignored Show -- BTN -- GRP -- AMZN<br>
</div>`)
	if len(records) != 3 {
		t.Fatalf("records = %#v", records)
	}
	if records[0].Title != "Example Show" || records[0].Group != "NTb" || !slices.Equal(records[0].Sites, []string{"HDB", "BTN"}) {
		t.Fatalf("first record = %#v", records[0])
	}
	if records[1].Title != "Example Show" || records[1].Group != "Other" {
		t.Fatalf("conflicting record was not preserved: %#v", records[1])
	}
	if records[2].Title != "HDB Only" || !slices.Equal(records[2].Sites, []string{"HDB"}) {
		t.Fatalf("HDB record = %#v", records[2])
	}
}

func TestExtractBTNClaimRecordsRequiresAuthoritativeScopeAndRetainsUnknownOwners(t *testing.T) {
	t.Parallel()

	unscoped := `<strong>Current Shows:</strong><br>Example Show -- BTN -- NTb -- AMZN<br>`
	if records := extractBTNClaimRecords(unscoped); len(records) != 0 {
		t.Fatalf("unscoped records = %#v", records)
	}
	records := extractBTNClaimRecords(`<div id="content1405482"><strong>Current Shows:</strong><br>
Unknown Owner -- BTN -- unknown -- NF<br>
Blank Owner -- BTN -- -- AMZN<br>
Upcoming Shows:<br></div>`)
	if len(records) != 2 {
		t.Fatalf("records = %#v", records)
	}
	if records[0].Title != "Blank Owner" || records[0].Group != "" {
		t.Fatalf("blank-owner record = %#v", records[0])
	}
	if records[1].Title != "Unknown Owner" || records[1].Group != "unknown" {
		t.Fatalf("unknown-owner record = %#v", records[1])
	}
	if claimsOwnedByGroup(records, "NTb") {
		t.Fatalf("unknown claim owners authorized own-group bypass")
	}
}

func TestInternalGroupBypassesOnlyFreshOwnClaimForTargetTracker(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	cachePath, err := btnClaimsPath(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	records := []btnClaimRecord{
		{
			Title: "Example Show",
			Sites: []string{"BTN", "HDB"},
			Group: "NTb",
		},
	}
	if err := writeBTNClaimCache(cachePath, records); err != nil {
		t.Fatal(err)
	}
	base := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"BTN": {InternalGroups: config.CSVList{"NTb"}},
			"HDB": {InternalGroups: config.CSVList{"Other"}},
		}},
	}
	meta := api.UploadSubject{
		Identity:    api.ExternalIdentity{Category: "TV"},
		Type:        "WEB-DL",
		SeasonInt:   1,
		EpisodeInt:  1,
		ReleaseName: "Example.Show.S01E01.1080p.WEB-DL-NTb",
		Tag:         "-NTb",
	}

	btnClaimed, err := New().NewClaimChecker(base, nil).HasClaim(t.Context(), meta)
	if err != nil || btnClaimed {
		t.Fatalf("BTN own claim was not bypassed: claimed=%t err=%v", btnClaimed, err)
	}
}

func TestStaleOrConflictingClaimsNeverAuthorizeOwnGroupBypass(t *testing.T) {
	t.Parallel()

	cachePath := filepath.Join(t.TempDir(), "claims.json")
	records := []btnClaimRecord{
		{
			Title: "Example Show",
			Sites: []string{"BTN"},
			Group: "NTb",
		},
		{
			Title: "Example Show",
			Sites: []string{"BTN"},
			Group: "Other",
		},
	}
	if err := writeBTNClaimCache(cachePath, records); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	var cache btnClaimedShowsCache
	if err := json.Unmarshal(payload, &cache); err != nil {
		t.Fatal(err)
	}
	cache.FetchedAt = time.Now().Add(-49 * time.Hour).Unix()
	payload, err = json.Marshal(cache)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, payload, 0o600); err != nil {
		t.Fatal(err)
	}

	claims, err := readBTNClaimCache(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	meta := api.UploadSubject{ReleaseName: "Example.Show.S01E01.1080p.WEB-DL-NTb"}
	matched, _ := matchBTNClaimRecords(meta, claims)
	if claimsOwnedByGroup(matched, "NTb") {
		t.Fatalf("conflicting claims authorized bypass: %#v", matched)
	}
	cache.Claims = cache.Claims[:1]
	payload, err = json.Marshal(cache)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	checker := &claimChecker{
		logger: api.NopLogger{},
		fetchOverride: func(context.Context) (btnClaimData, error) {
			return btnClaimData{}, errors.New("fetch unavailable")
		},
	}
	stale, err := checker.loadBTNClaims(t.Context(), cachePath, btnClaimedShowsCacheTTL)
	if err != nil {
		t.Fatal(err)
	}
	if stale.FreshStructured {
		t.Fatalf("stale cache reported as fresh structured data")
	}
	matched, _ = matchBTNClaimRecords(meta, stale)
	if !claimsOwnedByGroup(matched, "NTb") {
		t.Fatalf("stale same-group cache lost ownership evidence: %#v", matched)
	}
}

func TestLegacyClaimCacheRefreshesStructuredOwnership(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	cachePath, err := btnClaimsPath(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeBTNClaimedCache(cachePath, btnTitleVariants("Example Show")); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"BTN": {InternalGroups: config.CSVList{"TBN"}},
		}},
	}
	logger := &captureBTNLogger{}
	fetchCalls := 0
	checker := &claimChecker{
		cfg:    cfg,
		logger: logger,
		fetchOverride: func(context.Context) (btnClaimData, error) {
			fetchCalls++
			return btnClaimData{Records: []btnClaimRecord{
				{
					Title: "Example Show",
					Sites: []string{"HDB", "BTN"},
					Group: "TBN",
				},
			}}, nil
		},
	}
	claimed, err := checker.HasClaim(t.Context(), api.UploadSubject{
		Identity:    api.ExternalIdentity{Category: "TV"},
		SeasonInt:   1,
		EpisodeInt:  1,
		ReleaseName: "Example.Show.S01E01.1080p.WEB-DL-TBN",
		Tag:         "-TBN",
	})
	if err != nil || claimed {
		t.Fatalf("refreshed own claim was not bypassed: claimed=%t err=%v", claimed, err)
	}
	if fetchCalls != 1 {
		t.Fatalf("fetch calls = %d, want 1", fetchCalls)
	}
	if !logger.containsDebug("reason=legacy_format") || !logger.containsInfo("group=TBN decision=own_claim") {
		t.Fatalf("debugs=%#v infos=%#v", logger.debugs, logger.infos)
	}
	claims, err := readBTNClaimCache(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims.Records) != 1 || claims.Records[0].Group != "TBN" {
		t.Fatalf("migrated claims = %#v", claims.Records)
	}
}

func TestLegacyClaimCacheCannotAuthorizeOwnGroupBypassWhenRefreshFails(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	cachePath, err := btnClaimsPath(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeBTNClaimedCache(cachePath, btnTitleVariants("Example Show")); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"BTN": {InternalGroups: config.CSVList{"TBN"}},
		}},
	}
	logger := &captureBTNLogger{}
	checker := &claimChecker{
		cfg:    cfg,
		logger: logger,
		fetchOverride: func(context.Context) (btnClaimData, error) {
			return btnClaimData{}, errors.New("BTN session unavailable")
		},
	}
	claimed, err := checker.HasClaim(t.Context(), api.UploadSubject{
		Identity:    api.ExternalIdentity{Category: "TV"},
		SeasonInt:   1,
		EpisodeInt:  1,
		ReleaseName: "Example.Show.S01E01.1080p.WEB-DL-TBN",
		Tag:         "-TBN",
	})
	if err != nil || !claimed {
		t.Fatalf("legacy cache authorized bypass: claimed=%t err=%v", claimed, err)
	}
	if !logger.containsWarning("release_group=\"TBN\" internal_group=true fresh_structured=false own_claim=false") {
		t.Fatalf("warnings=%#v", logger.warnings)
	}
}

func TestClaimCancellationPropagates(t *testing.T) {
	t.Parallel()

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	checker := New().NewClaimChecker(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "upbrr.db")}}, nil)
	_, err := checker.HasClaim(canceled, api.UploadSubject{
		Identity:   api.ExternalIdentity{Category: "TV"},
		SeasonInt:  1,
		EpisodeInt: 1,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("HasClaim() error = %v", err)
	}
}

func TestFreshClaimCacheHonorsPreCanceledContext(t *testing.T) {
	t.Parallel()

	cachePath := filepath.Join(t.TempDir(), "claims.json")
	if err := writeBTNClaimCache(cachePath, []btnClaimRecord{{
		Title: "Example Show",
		Sites: []string{"BTN"},
		Group: "GRP",
	}}); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	checker := &claimChecker{logger: api.NopLogger{}}
	if _, err := checker.loadBTNClaims(canceled, cachePath, btnClaimedShowsCacheTTL); !errors.Is(err, context.Canceled) {
		t.Fatalf("loadBTNClaims() error = %v", err)
	}
}

func TestBTNClaimCacheValidatesIdentityAndReplacesExistingFile(t *testing.T) {
	t.Parallel()

	cachePath := filepath.Join(t.TempDir(), "claims.json")
	if err := writeBTNClaimCache(cachePath, []btnClaimRecord{{
		Title: "First Show",
		Sites: []string{"BTN"},
		Group: "GRP",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := writeBTNClaimCache(cachePath, []btnClaimRecord{{
		Title: "Second Show",
		Sites: []string{"HDB"},
		Group: "GRP",
	}}); err != nil {
		t.Fatalf("replace cache: %v", err)
	}
	claims, err := readBTNClaimCache(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims.Records) != 1 || claims.Records[0].Title != "Second Show" {
		t.Fatalf("replaced cache = %#v", claims.Records)
	}
	payload, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	var cache btnClaimedShowsCache
	if err := json.Unmarshal(payload, &cache); err != nil {
		t.Fatal(err)
	}
	cache.PostID = "post-other"
	payload, err = json.Marshal(cache)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readBTNClaimCache(cachePath); err == nil {
		t.Fatalf("invalid cache identity was accepted")
	}
	cache.PostID = btnClaimedShowsPostID
	cache.FetchedAt = time.Now().Add(time.Hour).Unix()
	payload, err = json.Marshal(cache)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readBTNClaimCache(cachePath); err == nil {
		t.Fatalf("future cache timestamp was accepted")
	}
}
