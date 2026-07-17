// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
)

func TestNewRegistryIncludesHDB(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := registry.Lookup("HDB"); !ok {
		t.Fatal("expected HDB definition to be registered")
	}
	if _, ok := registry.Lookup("MTV"); !ok {
		t.Fatal("expected MTV definition to be registered")
	}
	if _, ok := registry.Lookup("ANT"); !ok {
		t.Fatal("expected ANT definition to be registered")
	}
	if _, ok := registry.Lookup("AR"); !ok {
		t.Fatal("expected AR definition to be registered")
	}
	if _, ok := registry.Lookup("ASC"); !ok {
		t.Fatal("expected ASC definition to be registered")
	}
	if _, ok := registry.Lookup("BHD"); !ok {
		t.Fatal("expected BHD definition to be registered")
	}
	if _, ok := registry.Lookup("BHDTV"); !ok {
		t.Fatal("expected BHDTV definition to be registered")
	}
	if _, ok := registry.Lookup("BJS"); !ok {
		t.Fatal("expected BJS definition to be registered")
	}
	if _, ok := registry.Lookup("BT"); !ok {
		t.Fatal("expected BT definition to be registered")
	}
	if _, ok := registry.Lookup("DC"); !ok {
		t.Fatal("expected DC definition to be registered")
	}
	if _, ok := registry.Lookup("FF"); !ok {
		t.Fatal("expected FF definition to be registered")
	}
	if _, ok := registry.Lookup("FL"); !ok {
		t.Fatal("expected FL definition to be registered")
	}
	if _, ok := registry.Lookup("GPW"); !ok {
		t.Fatal("expected GPW definition to be registered")
	}
	if _, ok := registry.Lookup("ACM"); !ok {
		t.Fatal("expected ACM definition to be registered")
	}
	if _, ok := registry.Lookup("HDS"); !ok {
		t.Fatal("expected HDS definition to be registered")
	}
	if _, ok := registry.Lookup("HDT"); !ok {
		t.Fatal("expected HDT definition to be registered")
	}
	if _, ok := registry.Lookup("IS"); !ok {
		t.Fatal("expected IS definition to be registered")
	}
	if _, ok := registry.Lookup("NBL"); !ok {
		t.Fatal("expected NBL definition to be registered")
	}
	if _, ok := registry.Lookup("PTS"); !ok {
		t.Fatal("expected PTS definition to be registered")
	}
	if _, ok := registry.Lookup("RTF"); !ok {
		t.Fatal("expected RTF definition to be registered")
	}
	if _, ok := registry.Lookup("SPD"); !ok {
		t.Fatal("expected SPD definition to be registered")
	}
	if _, ok := registry.Lookup("THR"); !ok {
		t.Fatal("expected THR definition to be registered")
	}
	if _, ok := registry.Lookup("TL"); !ok {
		t.Fatal("expected TL definition to be registered")
	}
	if _, ok := registry.Lookup("TVC"); !ok {
		t.Fatal("expected TVC definition to be registered")
	}
	if _, ok := registry.Lookup("AZ"); !ok {
		t.Fatal("expected AZ definition to be registered")
	}
	if _, ok := registry.Lookup("CZ"); !ok {
		t.Fatal("expected CZ definition to be registered")
	}
	if _, ok := registry.Lookup("PHD"); !ok {
		t.Fatal("expected PHD definition to be registered")
	}
}

func TestNewRegistryCapabilityInventory(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	names := registry.Names()
	if !slices.IsSorted(names) {
		t.Fatalf("registry names are not deterministic: %v", names)
	}
	schemas, err := config.OrderedTrackerSchemas()
	if err != nil {
		t.Fatalf("ordered tracker schemas: %v", err)
	}
	want := make([]string, 0, len(schemas))
	for _, schema := range schemas {
		want = append(want, schema.Name)
	}
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Fatalf("registered trackers = %v, want %v", names, want)
	}
	if slices.Contains(names, "MANUAL") {
		t.Fatal("MANUAL must not be registered")
	}
	specialModes := map[string]trackers.UploadContentMode{
		"ANT": trackers.UploadContentModeScreenshots,
		"BTN": trackers.UploadContentModeNone,
		"NBL": trackers.UploadContentModeNone,
		"RTF": trackers.UploadContentModeScreenshots,
	}
	for _, name := range names {
		mode, ok := registry.LookupUploadContentMode(name)
		if !ok || !mode.Valid() {
			t.Fatalf("%s upload content mode = %q, %t", name, mode, ok)
		}
		want := trackers.UploadContentModeDescription
		if special, exists := specialModes[name]; exists {
			want = special
		}
		if mode != want {
			t.Fatalf("%s upload content mode = %q, want %q", name, mode, want)
		}
	}
	if definition, ok := registry.Lookup("BHDTV"); !ok {
		t.Fatal("expected BHDTV definition")
	} else if _, ok := definition.(dupe.Factory); !ok {
		t.Fatal("expected BHDTV tracker-owned dupe capability")
	}
	if definition, ok := registry.Lookup("ANT"); !ok {
		t.Fatal("expected ANT definition")
	} else if _, ok := definition.(dupe.Factory); !ok {
		t.Fatal("expected ANT tracker-owned dupe factory")
	}
	if _, ok := registry.LookupDataFactory("ANT"); !ok {
		t.Fatal("expected ANT tracker-owned data factory")
	}
	if policy, ok := registry.LookupArtifactPolicy("ANT"); !ok || policy.MaxTorrentBytes != 250<<10 {
		t.Fatalf("ANT artifact policy = %#v, %t", policy, ok)
	}
	if _, ok := registry.LookupRules("ANT"); !ok {
		t.Fatal("expected ANT tracker-owned rules")
	}
	if groups, ok := registry.LookupBannedGroups("ANT"); !ok || !slices.Contains(groups, "ZMNT") {
		t.Fatalf("ANT banned groups = %#v, %t", groups, ok)
	}
	if policy, ok := registry.LookupUploadArtifactPolicy("ANT"); !ok || policy.Source != "ANT" {
		t.Fatalf("ANT upload artifact policy = %#v, %t", policy, ok)
	}
	if _, ok := registry.LookupMetadataPolicy("ANT"); !ok {
		t.Fatal("expected ANT tracker-owned metadata policy")
	}
	if policy, ok := registry.LookupDupePolicy("ANT"); !ok || !policy.DolbyVisionImpliesHDR {
		t.Fatalf("ANT dupe policy = %#v, %t", policy, ok)
	}
}

func TestNewRegistryOwnsUploadArtifactPolicies(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	want := map[string]trackers.UploadArtifactPolicy{
		"ACM":   {Source: "AsianCinema"},
		"AR":    {Source: "AlphaRatio"},
		"ASC":   {Source: "ASC"},
		"AZ":    {Source: "AvistaZ", DefaultAnnounce: "https://tracker.avistaz.to/announce"},
		"BHDTV": {Source: "BIT-HDTV", UseMyAnnounce: true},
		"BJS":   {Source: "BJ"},
		"BT":    {Source: "BT"},
		"CZ":    {Source: "CinemaZ", DefaultAnnounce: "https://tracker.cinemaz.to/announce"},
		"CZT":   {Source: "CzT"},
		"DC":    {Source: "DigitalCore.club"},
		"FF":    {Source: "FunFile"},
		"FL":    {Source: "FL"},
		"GPW":   {Source: "GreatPosterWall"},
		"HDS":   {Source: "HD-Space"},
		"HDT":   {Source: "hd-torrents.org"},
		"IS":    {Source: "https://immortalseed.me"},
		"MTV":   {Source: "MTV"},
		"NBL":   {Source: "NBL"},
		"PHD":   {Source: "PrivateHD", DefaultAnnounce: "https://tracker.privatehd.to/announce"},
		"PTS":   {Source: "[www.ptskit.org] PTSKIT"},
		"RTF":   {Source: "sunshine"},
		"THR":   {Source: "[https://www.torrenthr.org] TorrentHR.org"},
		"TL":    {Source: "TorrentLeech.org"},
		"TOS":   {Source: "TheOldSchool"},
		"TVC":   {Source: "TVCHAOS"},
	}
	for name, expected := range want {
		got, ok := registry.LookupUploadArtifactPolicy(name)
		if !ok || got != expected {
			t.Errorf("%s upload artifact policy = %#v, %t; want %#v", name, got, ok, expected)
		}
	}
}

func TestNewRegistryOwnsMetadataPolicies(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	for _, name := range []string{"AR", "AZ", "BJS", "CZ", "CZT", "MTV", "NBL", "PHD", "PTP", "SPD", "THR", "TL", "TVC", "AITHER"} {
		if _, ok := registry.LookupMetadataPolicy(name); !ok {
			t.Errorf("expected %s tracker-owned metadata policy", name)
		}
	}
}

func TestNewRegistryIncludesUnit3DRuleCapabilities(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}

	trackersWithRules := []string{
		"A4K", "AITHER", "BLU", "DP", "HHD", "LST", "LUME", "MNS", "OE", "OTW", "RAS",
		"RF", "RHD", "SHRI", "SP", "STC", "TIK", "TOS", "TTR", "ULCX", "ZNTH",
	}
	for _, name := range trackersWithRules {
		if _, ok := registry.LookupRules(name); !ok {
			t.Errorf("expected %s tracker-owned rule capability", name)
		}
	}
	if baseURL, ok := registry.LookupBaseURL("AITHER"); !ok || baseURL != "https://aither.cc" {
		t.Fatalf("AITHER base URL = %q, %t", baseURL, ok)
	}
}

func TestNewRegistryIncludesBHDPolicies(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	if _, ok := registry.LookupRules("BHD"); !ok {
		t.Fatal("expected BHD rules")
	}
	if _, ok := registry.LookupMetadataPolicy("BHD"); !ok {
		t.Fatal("expected BHD metadata policy")
	}
	if policy, ok := registry.LookupUploadArtifactPolicy("BHD"); !ok || policy.Source != "BHD" {
		t.Fatalf("BHD upload artifact policy = %#v, %t", policy, ok)
	}
	if policy, ok := registry.LookupAudioPolicy("BHD"); !ok || !policy.BlockEnglishOriginalWithForeign {
		t.Fatalf("BHD audio policy = %#v, %t", policy, ok)
	}
	if groups, ok := registry.LookupBannedGroups("BHD"); !ok || !slices.Contains(groups, "TGS") {
		t.Fatalf("BHD banned groups = %#v, %t", groups, ok)
	}
	if policy, ok := registry.LookupDupePolicy("BHD"); !ok || !policy.MatchAggregateSize || !policy.NormalizeDDPlusName {
		t.Fatalf("BHD dupe policy = %#v, %t", policy, ok)
	}
	if definition, ok := registry.Lookup("BHD"); !ok {
		t.Fatal("expected BHD definition")
	} else if _, ok := definition.(dupe.Factory); !ok {
		t.Fatal("expected BHD dupe factory")
	}
	if _, ok := registry.LookupDataFactory("BHD"); !ok {
		t.Fatal("expected BHD data factory")
	}
}

func TestNewRegistryIncludesBTNPolicies(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	if _, ok := registry.LookupMetadataPolicy("BTN"); !ok {
		t.Fatal("expected BTN metadata policy")
	}
	if policy, ok := registry.LookupUploadArtifactPolicy("BTN"); !ok || policy.Source != "BTN" || !policy.RequireAnnounce {
		t.Fatalf("BTN upload artifact policy = %#v, %t", policy, ok)
	}
	if groups, ok := registry.LookupBannedGroups("BTN"); !ok || !slices.Contains(groups, "ZMNT") {
		t.Fatalf("BTN banned groups = %#v, %t", groups, ok)
	}
	if definition, ok := registry.Lookup("BTN"); !ok {
		t.Fatal("expected BTN definition")
	} else if _, ok := definition.(dupe.Factory); !ok {
		t.Fatal("expected BTN dupe factory")
	}
	if _, ok := registry.LookupDataFactory("BTN"); !ok {
		t.Fatal("expected BTN data factory")
	}
	if _, ok := registry.LookupClaimCheckerFactory("BTN"); !ok {
		t.Fatal("expected BTN claim checker factory")
	}
	btnConfig := config.Config{Metadata: config.MetadataConfig{BTNAPI: strings.Repeat("x", 25)}}
	if ready, owned := registry.DataLookupConfigured("BTN", btnConfig); !owned || !ready {
		t.Fatalf("BTN lookup readiness = %t, %t", ready, owned)
	}
}

func TestNewRegistryIncludesHDBPolicies(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	if _, ok := registry.LookupMetadataPolicy("HDB"); !ok {
		t.Fatal("expected HDB metadata policy")
	}
	if policy, ok := registry.LookupUploadArtifactPolicy("HDB"); !ok || policy.Source != "HDBits" {
		t.Fatalf("HDB upload artifact policy = %#v, %t", policy, ok)
	}
	if policy, ok := registry.LookupArtifactPolicy("HDB"); !ok || policy.MaxPieceSizeMiB != 16 {
		t.Fatalf("HDB artifact policy = %#v, %t", policy, ok)
	}
	if _, ok := registry.LookupDataFactory("HDB"); !ok {
		t.Fatal("expected HDB data factory")
	}
	cfg := config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"HDB": {Username: "user", Passkey: "pass"}}}}
	if ready, owned := registry.DataLookupConfigured("HDB", cfg); !owned || !ready {
		t.Fatalf("HDB lookup readiness = %t, %t", ready, owned)
	}
}

func TestNewRegistryIncludesPTPDataPolicy(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	policy, ok := registry.LookupDataPolicy("PTP")
	if !ok || policy.Cooldown != time.Minute {
		t.Fatalf("PTP data policy = %#v, %t", policy, ok)
	}
}

func TestNewRegistryIncludesImageHostPolicies(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	tests := []struct {
		tracker              string
		host                 string
		conditionalHost      string
		disableWithoutRehost bool
		disableWithoutAPI    bool
	}{
		{tracker: "A4K", host: "onlyimage"},
		{tracker: "BHD", host: "bhd"},
		{
			tracker:              "HDB",
			host:                 "hdb",
			disableWithoutRehost: true,
		},
		{tracker: "PTP", host: "passtheimage"},
		{
			tracker:           "THR",
			host:              "thr",
			disableWithoutAPI: true,
		},
		{tracker: "LST", conditionalHost: "lostimg"},
		{tracker: "RF", conditionalHost: "reelflix"},
	}
	for _, test := range tests {
		policy, ok := registry.LookupImageHostPolicy(test.tracker)
		if !ok || (test.host != "" && !slices.Contains(policy.AllowedHosts, test.host)) {
			t.Errorf("%s image policy = %#v, %t", test.tracker, policy, ok)
			continue
		}
		if policy.DisableWithoutRehost != test.disableWithoutRehost || policy.DisableWithoutAPI != test.disableWithoutAPI {
			t.Errorf("%s image policy flags = %#v", test.tracker, policy)
		}
		if policy.ConditionalHost != test.conditionalHost {
			t.Errorf("%s conditional image host = %q", test.tracker, policy.ConditionalHost)
		}
	}
}

func TestNewRegistryIncludesAuthResolvers(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	for _, tracker := range []string{"BTN", "FF", "FL", "HDB", "MTV", "PTP", "RTF"} {
		if _, ok := registry.LookupAuthSessionResolver(tracker); !ok {
			t.Errorf("expected %s tracker-owned auth resolver", tracker)
		}
		capability, ok := registry.LookupAuthCapability(tracker)
		if !ok || capability.TrackerID != tracker {
			t.Errorf("%s auth capability = %#v, %t", tracker, capability, ok)
		}
		if tracker != "HDB" && !capability.SupportsLogin {
			t.Errorf("expected %s login capability", tracker)
		}
	}
}

func TestNewRegistryIncludesAuthCapabilities(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	trackersWithAuth := []string{"AR", "ASC", "AZ", "BJS", "BT", "BTN", "CZ", "FF", "FL", "HDB", "HDS", "HDT", "IS", "MTV", "PHD", "PTP", "PTS", "RTF", "THR", "TL"}
	for _, tracker := range trackersWithAuth {
		capability, ok := registry.LookupAuthCapability(tracker)
		if !ok || capability.TrackerID != tracker {
			t.Errorf("%s auth capability = %#v, %t", tracker, capability, ok)
		}
	}
}

func TestNewRegistryClassifiesTrackerFamilies(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	if family, ok := registry.LookupFamily("AITHER"); !ok || family != trackers.FamilyUnit3D {
		t.Fatalf("AITHER family = %q, %t", family, ok)
	}
	if family, ok := registry.LookupFamily("AZ"); !ok || family != trackers.FamilyAZFamily {
		t.Fatalf("AZ family = %q, %t", family, ok)
	}
	if family, ok := registry.LookupFamily("PTP"); !ok || family != trackers.FamilyStandalone {
		t.Fatalf("PTP family = %q, %t", family, ok)
	}
	if !slices.Contains(registry.NamesByFamily(trackers.FamilyUnit3D), "AITHER") {
		t.Fatal("expected AITHER in Unit3D registry names")
	}
}

func TestNewRegistryDeclaresLocalizedMetadataConsumers(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	if !registry.NeedsLocalizedMetadata([]string{"ASC", "BJS", "BT"}, "pt-BR") {
		t.Fatal("expected pt-BR localized metadata consumers")
	}
	if registry.NeedsLocalizedMetadata([]string{"PTP"}, "pt-BR") {
		t.Fatal("did not expect PTP to consume pt-BR metadata")
	}
}

func TestNewRegistryDeclaresDescriptionGroups(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	if got := trackers.DescriptionOverrideGroupForTrackerWithRegistry("ACM", registry); got != "acm" {
		t.Fatalf("ACM description group = %q", got)
	}
	if got := trackers.DescriptionOverrideGroupForTrackerWithRegistry("AITHER", registry); got != "unit3d" {
		t.Fatalf("AITHER description group = %q", got)
	}
	if policy, ok := registry.LookupClaimPolicy("AITHER"); !ok || !policy.APIBacked {
		t.Fatalf("AITHER claim policy = %#v, %t", policy, ok)
	}
}
