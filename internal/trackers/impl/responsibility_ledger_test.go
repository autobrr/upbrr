// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
)

type trackerResponsibilityRow struct {
	name              string
	family            trackers.Family
	contentMode       trackers.UploadContentMode
	authMode          string
	authOwner         string
	hasAuthResolver   bool
	supportsLogin     bool
	supports2FA       bool
	taxonomyOwner     string
	descriptionOwner  string
	mediaOwner        string
	questionnaireKeys []string
	descriptionGroup  string
	releaseNamePolicy string
	projectorVersion  string
	principalName     string
}

func unit3DResponsibility(name string, policy string, descriptionGroup string) trackerResponsibilityRow {
	return trackerResponsibilityRow{
		name:              name,
		family:            trackers.FamilyUnit3D,
		contentMode:       trackers.UploadContentModeDescription,
		authMode:          "api_key",
		authOwner:         "unit3d/auth.go",
		taxonomyOwner:     "unit3d/taxonomy.go",
		descriptionOwner:  "unit3d/description.go",
		mediaOwner:        "unit3d/media.go",
		descriptionGroup:  descriptionGroup,
		releaseNamePolicy: "unit3d/" + policy + "/v1",
		projectorVersion:  "unit3d-v2",
		principalName:     "name",
	}
}

func azFamilyResponsibility(name string) trackerResponsibilityRow {
	return trackerResponsibilityRow{
		name:              name,
		family:            trackers.FamilyAZFamily,
		contentMode:       trackers.UploadContentModeDescription,
		authMode:          "form",
		authOwner:         "azfamily/auth.go",
		taxonomyOwner:     "azfamily/taxonomy.go",
		descriptionOwner:  "azfamily/description.go",
		mediaOwner:        "azfamily/media.go",
		releaseNamePolicy: "azfamily/" + strings.ToLower(name) + "/v1",
		projectorVersion:  "azfamily-v2",
		principalName:     "name",
	}
}

var trackerResponsibilityLedger = []trackerResponsibilityRow{
	unit3DResponsibility("A4K", "canonical", ""),
	unit3DResponsibility("ACM", "acm", "acm"),
	unit3DResponsibility("AITHER", "aither", ""),
	unit3DResponsibility("BLU", "canonical", ""),
	unit3DResponsibility("CBR", "cbr", ""),
	unit3DResponsibility("DP", "dp", ""),
	unit3DResponsibility("EMUW", "canonical", ""),
	unit3DResponsibility("FRIKI", "canonical", ""),
	unit3DResponsibility("HHD", "canonical", ""),
	unit3DResponsibility("IHD", "canonical", ""),
	unit3DResponsibility("ITT", "canonical", ""),
	unit3DResponsibility("LCD", "lcd", ""),
	unit3DResponsibility("LDU", "ldu", ""),
	unit3DResponsibility("LST", "canonical", ""),
	unit3DResponsibility("LT", "canonical", ""),
	unit3DResponsibility("LUME", "canonical", ""),
	unit3DResponsibility("MNS", "canonical", ""),
	unit3DResponsibility("OE", "oe", ""),
	unit3DResponsibility("OTW", "canonical", ""),
	unit3DResponsibility("PT", "canonical", ""),
	unit3DResponsibility("PTT", "canonical", ""),
	unit3DResponsibility("R4E", "canonical", ""),
	unit3DResponsibility("RAS", "canonical", ""),
	unit3DResponsibility("RF", "rf", ""),
	unit3DResponsibility("RHD", "rhd", ""),
	unit3DResponsibility("SAM", "sam", ""),
	unit3DResponsibility("SHRI", "canonical", ""),
	unit3DResponsibility("SP", "canonical", ""),
	unit3DResponsibility("STC", "canonical", ""),
	unit3DResponsibility("TIK", "canonical", ""),
	unit3DResponsibility("TLZ", "canonical", ""),
	unit3DResponsibility("TOS", "canonical", ""),
	unit3DResponsibility("TTR", "canonical", ""),
	unit3DResponsibility("ULCX", "ulcx", ""),
	unit3DResponsibility("UTP", "canonical", ""),
	unit3DResponsibility("YUS", "canonical", ""),
	unit3DResponsibility("ZNTH", "znth", ""),
	azFamilyResponsibility("AZ"),
	azFamilyResponsibility("CZ"),
	azFamilyResponsibility("PHD"),
	{
		name:              "ANT",
		family:            trackers.FamilyStandalone,
		contentMode:       trackers.UploadContentModeScreenshots,
		authMode:          "api_key",
		authOwner:         "../auth/contract/requirements.go",
		taxonomyOwner:     "standalone/ant/taxonomy.go",
		descriptionOwner:  "standalone/ant/description.go",
		mediaOwner:        "standalone/ant/media.go",
		questionnaireKeys: []string{"type", "tags", "adult_screens"},
		descriptionGroup:  "ant",
		releaseNamePolicy: "standalone/canonical/v1",
		projectorVersion:  "standalone-v2",
		principalName:     "name",
	},
	{
		name:              "AR",
		family:            trackers.FamilyStandalone,
		contentMode:       trackers.UploadContentModeDescription,
		authMode:          "cookies_or_login",
		authOwner:         "standalone/ar/auth.go",
		hasAuthResolver:   true,
		supportsLogin:     true,
		taxonomyOwner:     "standalone/ar/taxonomy.go",
		descriptionOwner:  "standalone/ar/description.go",
		mediaOwner:        "standalone/ar/media.go",
		descriptionGroup:  "ar",
		releaseNamePolicy: "standalone/ar/v1",
		projectorVersion:  "standalone-v2",
		principalName:     "name",
	},
	{
		name:              "ASC",
		family:            trackers.FamilyStandalone,
		contentMode:       trackers.UploadContentModeDescription,
		authMode:          "cookies",
		authOwner:         "standalone/asc/auth.go",
		taxonomyOwner:     "standalone/asc/taxonomy.go",
		descriptionOwner:  "standalone/asc/description.go",
		mediaOwner:        "standalone/asc/media.go",
		questionnaireKeys: []string{"overview", "genre"},
		descriptionGroup:  "asc",
		releaseNamePolicy: "standalone/asc/v1",
		projectorVersion:  "standalone-v2",
		principalName:     "name",
	},
	{
		name:              "BHD",
		family:            trackers.FamilyStandalone,
		contentMode:       trackers.UploadContentModeDescription,
		authMode:          "api_key",
		authOwner:         "../auth/contract/requirements.go",
		taxonomyOwner:     "standalone/bhd/taxonomy.go",
		descriptionOwner:  "standalone/bhd/description.go",
		mediaOwner:        "standalone/bhd/media.go",
		descriptionGroup:  "bhd",
		releaseNamePolicy: "standalone/bhd/v1",
		projectorVersion:  "standalone-v2",
		principalName:     "name",
	},
	{
		name:              "BHDTV",
		family:            trackers.FamilyStandalone,
		contentMode:       trackers.UploadContentModeDescription,
		authMode:          "api_key",
		authOwner:         "../auth/contract/requirements.go",
		taxonomyOwner:     "standalone/bhdtv/taxonomy.go",
		descriptionOwner:  "standalone/bhdtv/description.go",
		mediaOwner:        "standalone/bhdtv/media.go",
		descriptionGroup:  "bhdtv",
		releaseNamePolicy: "standalone/bhdtv/v1",
		projectorVersion:  "standalone-v2",
		principalName:     "name",
	},
	{
		name:              "BJS",
		family:            trackers.FamilyStandalone,
		contentMode:       trackers.UploadContentModeDescription,
		authMode:          "cookies",
		authOwner:         "standalone/bjs/auth.go",
		taxonomyOwner:     "standalone/bjs/taxonomy.go",
		descriptionOwner:  "standalone/bjs/description.go",
		mediaOwner:        "standalone/bjs/media.go",
		questionnaireKeys: []string{"overview", "tags"},
		descriptionGroup:  "bjs",
		releaseNamePolicy: "standalone/canonical/v1",
		projectorVersion:  "standalone-v2",
		principalName:     "name",
	},
	{
		name:              "BT",
		family:            trackers.FamilyStandalone,
		contentMode:       trackers.UploadContentModeDescription,
		authMode:          "cookies",
		authOwner:         "standalone/bt/auth.go",
		taxonomyOwner:     "standalone/bt/taxonomy.go",
		descriptionOwner:  "standalone/bt/description.go",
		mediaOwner:        "standalone/bt/media.go",
		questionnaireKeys: []string{"overview", "tags"},
		descriptionGroup:  "bt",
		releaseNamePolicy: "standalone/bt/v1",
		projectorVersion:  "standalone-v2",
		principalName:     "name",
	},
	{
		name:              "BTN",
		family:            trackers.FamilyStandalone,
		contentMode:       trackers.UploadContentModeNone,
		authMode:          "api_and_upload_session",
		authOwner:         "standalone/btn/auth.go",
		hasAuthResolver:   true,
		supportsLogin:     true,
		supports2FA:       true,
		taxonomyOwner:     "standalone/btn/taxonomy.go",
		descriptionOwner:  "standalone/btn/description.go",
		mediaOwner:        "standalone/btn/media.go",
		descriptionGroup:  "btn",
		releaseNamePolicy: "standalone/btn/v1",
		projectorVersion:  "standalone-v2",
		principalName:     "release_name",
	},
	{
		name:              "CZT",
		family:            trackers.FamilyStandalone,
		contentMode:       trackers.UploadContentModeDescription,
		authMode:          "passkey",
		authOwner:         "../auth/contract/requirements.go",
		taxonomyOwner:     "standalone/czt/taxonomy.go",
		descriptionOwner:  "standalone/czt/description.go",
		mediaOwner:        "standalone/czt/media.go",
		questionnaireKeys: []string{"category"},
		descriptionGroup:  "czt",
		releaseNamePolicy: "standalone/scene-first/v1",
		projectorVersion:  "standalone-v2",
		principalName:     "name",
	},
	{
		name:              "DC",
		family:            trackers.FamilyStandalone,
		contentMode:       trackers.UploadContentModeDescription,
		authMode:          "api_key",
		authOwner:         "../auth/contract/requirements.go",
		taxonomyOwner:     "standalone/dc/taxonomy.go",
		descriptionOwner:  "standalone/dc/description.go",
		mediaOwner:        "standalone/dc/media.go",
		descriptionGroup:  "dc",
		releaseNamePolicy: "standalone/dc/v1",
		projectorVersion:  "standalone-v2",
		principalName:     "name",
	},
	{
		name:              "FF",
		family:            trackers.FamilyStandalone,
		contentMode:       trackers.UploadContentModeDescription,
		authMode:          "cookies_or_login",
		authOwner:         "standalone/ff/auth.go",
		hasAuthResolver:   true,
		supportsLogin:     true,
		taxonomyOwner:     "standalone/ff/taxonomy.go",
		descriptionOwner:  "standalone/ff/description.go",
		mediaOwner:        "standalone/ff/media.go",
		descriptionGroup:  "ff",
		releaseNamePolicy: "standalone/ff/v1",
		projectorVersion:  "standalone-v2",
		principalName:     "name",
	},
	{
		name:              "FL",
		family:            trackers.FamilyStandalone,
		contentMode:       trackers.UploadContentModeDescription,
		authMode:          "cookies_or_login",
		authOwner:         "standalone/fl/auth.go",
		hasAuthResolver:   true,
		supportsLogin:     true,
		taxonomyOwner:     "standalone/fl/taxonomy.go",
		descriptionOwner:  "standalone/fl/description.go",
		mediaOwner:        "standalone/fl/media.go",
		questionnaireKeys: []string{"name"},
		descriptionGroup:  "fl",
		releaseNamePolicy: "standalone/fl/v1",
		projectorVersion:  "standalone-v2",
		principalName:     "name",
	},
	{
		name:              "GPW",
		family:            trackers.FamilyStandalone,
		contentMode:       trackers.UploadContentModeDescription,
		authMode:          "api_key",
		authOwner:         "../auth/contract/requirements.go",
		taxonomyOwner:     "standalone/gpw/taxonomy.go",
		descriptionOwner:  "standalone/gpw/description.go",
		questionnaireKeys: []string{"poster_url", "director_imdb", "director_name", "director_chinese", "tags"},
		descriptionGroup:  "gpw",
		releaseNamePolicy: "standalone/canonical/v1",
		projectorVersion:  "standalone-v2",
		principalName:     "name",
	},
	{
		name:              "HDB",
		family:            trackers.FamilyStandalone,
		contentMode:       trackers.UploadContentModeDescription,
		authMode:          "passkey_cookie",
		authOwner:         "standalone/hdb/auth.go",
		hasAuthResolver:   true,
		taxonomyOwner:     "standalone/hdb/taxonomy.go",
		descriptionOwner:  "standalone/hdb/description.go",
		descriptionGroup:  "hdb",
		releaseNamePolicy: "standalone/canonical/v1",
		projectorVersion:  "standalone-v2",
		principalName:     "name",
	},
	{
		name:              "HDS",
		family:            trackers.FamilyStandalone,
		contentMode:       trackers.UploadContentModeDescription,
		authMode:          "cookies",
		authOwner:         "standalone/hds/auth.go",
		taxonomyOwner:     "standalone/hds/taxonomy.go",
		descriptionOwner:  "standalone/hds/description.go",
		mediaOwner:        "standalone/hds/media.go",
		descriptionGroup:  "hds",
		releaseNamePolicy: "standalone/canonical/v1",
		projectorVersion:  "standalone-v2",
		principalName:     "name",
	},
	{
		name:              "HDT",
		family:            trackers.FamilyStandalone,
		contentMode:       trackers.UploadContentModeDescription,
		authMode:          "cookies",
		authOwner:         "standalone/hdt/auth.go",
		taxonomyOwner:     "standalone/hdt/taxonomy.go",
		descriptionOwner:  "standalone/hdt/description.go",
		mediaOwner:        "standalone/hdt/media.go",
		descriptionGroup:  "hdt",
		releaseNamePolicy: "standalone/hdt/v1",
		projectorVersion:  "standalone-v2",
		principalName:     "name",
	},
	{
		name:              "IS",
		family:            trackers.FamilyStandalone,
		contentMode:       trackers.UploadContentModeDescription,
		authMode:          "cookies",
		authOwner:         "standalone/is/auth.go",
		taxonomyOwner:     "standalone/is/taxonomy.go",
		descriptionOwner:  "standalone/is/description.go",
		mediaOwner:        "standalone/is/media.go",
		descriptionGroup:  "is",
		releaseNamePolicy: "standalone/is/v1",
		projectorVersion:  "standalone-v2",
		principalName:     "name",
	},
	{
		name:              "MTV",
		family:            trackers.FamilyStandalone,
		contentMode:       trackers.UploadContentModeDescription,
		authMode:          "api_and_upload_session",
		authOwner:         "standalone/mtv/auth.go",
		hasAuthResolver:   true,
		supportsLogin:     true,
		supports2FA:       true,
		taxonomyOwner:     "standalone/mtv/taxonomy.go",
		descriptionOwner:  "standalone/mtv/description.go",
		mediaOwner:        "standalone/mtv/media.go",
		descriptionGroup:  "mtv",
		releaseNamePolicy: "standalone/mtv/v1",
		projectorVersion:  "standalone-v2",
		principalName:     "title",
	},
	{
		name:              "NBL",
		family:            trackers.FamilyStandalone,
		contentMode:       trackers.UploadContentModeNone,
		authMode:          "api_key",
		authOwner:         "../auth/contract/requirements.go",
		taxonomyOwner:     "standalone/nbl/taxonomy.go",
		mediaOwner:        "standalone/nbl/media.go",
		descriptionGroup:  "nbl",
		releaseNamePolicy: "standalone/nbl/v1",
		projectorVersion:  "standalone-v2",
		principalName:     "name",
	},
	{
		name:              "PTP",
		family:            trackers.FamilyStandalone,
		contentMode:       trackers.UploadContentModeDescription,
		authMode:          "api_and_upload_session",
		authOwner:         "standalone/ptp/auth.go",
		hasAuthResolver:   true,
		supportsLogin:     true,
		supports2FA:       true,
		taxonomyOwner:     "standalone/ptp/taxonomy.go",
		descriptionOwner:  "standalone/ptp/description.go",
		mediaOwner:        "standalone/ptp/media.go",
		questionnaireKeys: []string{"title", "year", "poster", "tags", "trailer", "album_desc"},
		descriptionGroup:  "ptp",
		releaseNamePolicy: "standalone/canonical/v1",
		projectorVersion:  "standalone-v2",
		principalName:     "title",
	},
	{
		name:              "PTS",
		family:            trackers.FamilyStandalone,
		contentMode:       trackers.UploadContentModeDescription,
		authMode:          "cookies",
		authOwner:         "standalone/pts/auth.go",
		taxonomyOwner:     "standalone/pts/taxonomy.go",
		descriptionOwner:  "standalone/pts/description.go",
		questionnaireKeys: []string{"mandarin_override"},
		descriptionGroup:  "pts",
		releaseNamePolicy: "standalone/canonical/v1",
		projectorVersion:  "standalone-v2",
		principalName:     "name",
	},
	{
		name:              "RTF",
		family:            trackers.FamilyStandalone,
		contentMode:       trackers.UploadContentModeScreenshots,
		authMode:          "api_key_or_refresh",
		authOwner:         "standalone/rtf/auth.go",
		hasAuthResolver:   true,
		supportsLogin:     true,
		taxonomyOwner:     "standalone/rtf/taxonomy.go",
		descriptionOwner:  "standalone/rtf/description.go",
		descriptionGroup:  "rtf",
		releaseNamePolicy: "standalone/rtf/v1",
		projectorVersion:  "standalone-v2",
		principalName:     "name",
	},
	{
		name:              "SPD",
		family:            trackers.FamilyStandalone,
		contentMode:       trackers.UploadContentModeDescription,
		authMode:          "api_key",
		authOwner:         "../auth/contract/requirements.go",
		taxonomyOwner:     "standalone/spd/taxonomy.go",
		descriptionOwner:  "standalone/spd/description.go",
		questionnaireKeys: []string{"channel"},
		descriptionGroup:  "spd",
		releaseNamePolicy: "standalone/spd/v1",
		projectorVersion:  "standalone-v2",
		principalName:     "name",
	},
	{
		name:              "THR",
		family:            trackers.FamilyStandalone,
		contentMode:       trackers.UploadContentModeDescription,
		authMode:          "credential_login",
		authOwner:         "standalone/thr/auth.go",
		hasAuthResolver:   true,
		supportsLogin:     true,
		taxonomyOwner:     "standalone/thr/taxonomy.go",
		descriptionOwner:  "standalone/thr/description.go",
		questionnaireKeys: []string{"name_override"},
		descriptionGroup:  "thr",
		releaseNamePolicy: "standalone/thr/v1",
		projectorVersion:  "standalone-v2",
		principalName:     "name",
	},
	{
		name:              "TL",
		family:            trackers.FamilyStandalone,
		contentMode:       trackers.UploadContentModeDescription,
		authMode:          "form_upload",
		authOwner:         "standalone/tl/auth.go",
		taxonomyOwner:     "standalone/tl/taxonomy.go",
		descriptionOwner:  "standalone/tl/description.go",
		descriptionGroup:  "tl",
		releaseNamePolicy: "standalone/tl/v1",
		projectorVersion:  "standalone-v2",
		principalName:     "name",
	},
	{
		name:              "TVC",
		family:            trackers.FamilyStandalone,
		contentMode:       trackers.UploadContentModeDescription,
		authMode:          "api_key",
		authOwner:         "../auth/contract/requirements.go",
		taxonomyOwner:     "standalone/tvc/taxonomy.go",
		descriptionOwner:  "standalone/tvc/description.go",
		questionnaireKeys: []string{"name_override"},
		descriptionGroup:  "tvc",
		releaseNamePolicy: "standalone/tvc/v1",
		projectorVersion:  "standalone-v2",
		principalName:     "name",
	},
}

func TestTrackerResponsibilityLedgerCoversEveryBuiltIn(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	if len(trackerResponsibilityLedger) != 66 {
		t.Fatalf("responsibility rows = %d, want 66", len(trackerResponsibilityLedger))
	}

	ledgerNames := make([]string, 0, len(trackerResponsibilityLedger))
	seen := make(map[string]struct{}, len(trackerResponsibilityLedger))
	for _, row := range trackerResponsibilityLedger {
		if _, duplicate := seen[row.name]; duplicate {
			t.Fatalf("duplicate responsibility row for %s", row.name)
		}
		seen[row.name] = struct{}{}
		ledgerNames = append(ledgerNames, row.name)
	}
	slices.Sort(ledgerNames)
	if !slices.Equal(ledgerNames, registry.Names()) {
		t.Fatalf("responsibility names = %v, registry names = %v", ledgerNames, registry.Names())
	}

	for _, row := range trackerResponsibilityLedger {
		t.Run(row.name, func(t *testing.T) {
			descriptor, ok := registry.LookupDescriptor(row.name)
			if !ok {
				t.Fatal("descriptor missing")
			}
			if descriptor.Family != row.family || descriptor.UploadContentMode != row.contentMode {
				t.Fatalf("family/content = %s/%s, want %s/%s", descriptor.Family, descriptor.UploadContentMode, row.family, row.contentMode)
			}
			if descriptor.DescriptionGroup != row.descriptionGroup {
				t.Fatalf("description group = %q, want %q", descriptor.DescriptionGroup, row.descriptionGroup)
			}
			if descriptor.ReleaseNamePolicy.ID != row.releaseNamePolicy || descriptor.ProjectorVersion != row.projectorVersion {
				t.Fatalf(
					"naming policy/projector = %q/%q, want %q/%q",
					descriptor.ReleaseNamePolicy.ID,
					descriptor.ProjectorVersion,
					row.releaseNamePolicy,
					row.projectorVersion,
				)
			}
			if descriptor.Validation.Check == nil || strings.TrimSpace(descriptor.Validation.ID) == "" {
				t.Fatal("versioned pre-dupe validation policy is unrecorded")
			}
			if !strings.HasSuffix(descriptor.Validation.ID, "-v1") {
				t.Fatalf("validation policy %q is not explicitly versioned", descriptor.Validation.ID)
			}
			if strings.TrimSpace(row.principalName) == "" {
				t.Fatal("principal payload name field is unrecorded")
			}

			requirements, ok := registry.ResolveEffectiveAuthRequirements(row.name, config.Config{}, config.TrackerConfig{})
			if !ok || requirements.Mode != row.authMode || len(requirements.Alternatives) == 0 {
				t.Fatalf("effective auth requirements = %#v, %t; want mode %q", requirements, ok, row.authMode)
			}
			if requirements.Supports2FA != row.supports2FA {
				t.Fatalf("2FA support = %t, want %t", requirements.Supports2FA, row.supports2FA)
			}
			capability, ok := registry.LookupAuthCapability(row.name)
			if !ok || capability.SupportsLogin != row.supportsLogin {
				t.Fatalf("auth capability = %#v, %t; want supportsLogin=%t", capability, ok, row.supportsLogin)
			}
			_, hasResolver := registry.LookupAuthSessionResolver(row.name)
			if hasResolver != row.hasAuthResolver {
				t.Fatalf("auth resolver = %t, want %t", hasResolver, row.hasAuthResolver)
			}

			for responsibility, owner := range map[string]string{
				"auth":          row.authOwner,
				"taxonomy":      row.taxonomyOwner,
				"description":   row.descriptionOwner,
				"media":         row.mediaOwner,
				"questionnaire": questionnaireOwner(row),
			} {
				if owner == "" {
					continue
				}
				if _, err := os.Stat(filepath.FromSlash(owner)); err != nil {
					t.Fatalf("%s owner %q: %v", responsibility, owner, err)
				}
			}
			if len(row.questionnaireKeys) > 0 {
				content, err := os.ReadFile(filepath.FromSlash(questionnaireOwner(row)))
				if err != nil {
					t.Fatalf("read questionnaire owner: %v", err)
				}
				for _, key := range row.questionnaireKeys {
					if !strings.Contains(string(content), key) {
						t.Errorf("questionnaire owner missing answer key %q", key)
					}
				}
			}
		})
	}
}

func questionnaireOwner(row trackerResponsibilityRow) string {
	if len(row.questionnaireKeys) == 0 {
		return ""
	}
	return "standalone/" + strings.ToLower(row.name) + "/questionnaire.go"
}
