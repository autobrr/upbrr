// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestNewRegistryIncludesHDB(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := registry.Lookup("HDB"); !ok {
		t.Fatal("expected HDB definition to be registered")
	}
	if _, ok := registry.Lookup("ANT"); !ok {
		t.Fatal("expected ANT definition to be registered")
	}
	if _, ok := registry.Lookup("MTV"); ok {
		t.Fatal("did not expect MTV definition to be registered")
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

func TestMovieYearProvidersFollowTrackerMetadataAuthority(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	for _, family := range []trackers.Family{trackers.FamilyUnit3D, trackers.FamilyAZFamily} {
		for _, name := range registry.NamesByFamily(family) {
			descriptor, ok := registry.LookupDescriptor(name)
			if !ok || descriptor.ReleaseNamePolicy.MovieYearProvider != api.IdentityProviderTMDB {
				t.Fatalf("%s movie-year provider = %q", name, descriptor.ReleaseNamePolicy.MovieYearProvider)
			}
		}
	}
	for name, provider := range map[string]api.IdentityProvider{
		"ANT": api.IdentityProviderTMDB,
		"BJS": api.IdentityProviderTMDB,
		"BHD": api.IdentityProviderIMDB,
		"CZT": api.IdentityProviderIMDB,
		"HDB": api.IdentityProviderIMDB,
		"PTP": api.IdentityProviderIMDB,
	} {
		descriptor, ok := registry.LookupDescriptor(name)
		if !ok || descriptor.ReleaseNamePolicy.MovieYearProvider != provider {
			t.Fatalf("%s movie-year provider = %q, want %q", name, descriptor.ReleaseNamePolicy.MovieYearProvider, provider)
		}
	}
}

func TestDescriptionDefinitionsPreserveFinalReviewedDescription(t *testing.T) {
	t.Parallel()

	registry := MustNewRegistry()
	const finalDescription = "Reviewed body\n\n[right][url=https://github.com/autobrr/upbrr]Uploaded by upbrr[/url][/right]"

	for _, tracker := range registry.Names() {
		mode, ok := registry.LookupUploadContentMode(tracker)
		if !ok || mode != trackers.UploadContentModeDescription {
			continue
		}
		definition, ok := registry.Lookup(tracker)
		if !ok {
			t.Fatalf("description definition missing tracker=%s", tracker)
		}
		t.Run(tracker, func(t *testing.T) {
			plan, failure := definition.Prepare(context.Background(), trackers.PreparationInput{
				Intent:  trackers.PreparationIntentDescriptionPreview,
				Tracker: tracker,
				Assets: &trackers.DescriptionAssets{
					Description: finalDescription,
					Final:       true,
				},
			})
			if failure != nil {
				t.Fatalf("prepare final description: %v", failure)
			}
			defer func() {
				if err := plan.Release(); err != nil {
					t.Fatalf("release description plan: %v", err)
				}
			}()

			if got := plan.Description().Description; got != finalDescription {
				t.Fatalf("final description changed:\nwant %q\ngot  %q", finalDescription, got)
			}
		})
	}
}

func TestRegistryProjectsARNamesBeforeDuplicateChecking(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	tests := []struct {
		tracker       string
		subject       api.UploadSubject
		wantUpload    string
		wantDuplicate string
	}{
		{
			tracker: "AR",
			subject: api.UploadSubject{
				SourcePath:  `C:\Media\Example Release 2026.mkv`,
				ReleaseName: "Canonical.Release.Name.2026-GRP",
				Tag:         "GRP",
				Release: api.ReleaseInfo{
					Title: "Example Release",
					Year:  2026,
				},
			},
			wantUpload:    "Example Release 2026",
			wantDuplicate: "Example Release 2026",
		},
	}
	for _, test := range tests {
		t.Run(test.tracker, func(t *testing.T) {
			t.Parallel()
			projection, failure := registry.ProjectRelease(context.Background(), trackers.PreparationInput{
				Tracker: test.tracker,
				Meta:    test.subject,
			}, "", "", "")
			if failure != nil {
				t.Fatalf("project %s: %v", test.tracker, failure)
			}
			if projection.UploadReleaseName != test.wantUpload {
				t.Fatalf("upload name = %q, want %q", projection.UploadReleaseName, test.wantUpload)
			}
			if projection.DuplicateCriteria.Name != test.wantDuplicate {
				t.Fatalf("duplicate name = %q, want %q", projection.DuplicateCriteria.Name, test.wantDuplicate)
			}
			if projection.UploadReleaseName != projection.DuplicateCriteria.Name &&
				!slices.ContainsFunc(projection.AdditionalNames, func(name api.TrackerReleaseName) bool {
					return name.Role == api.TrackerReleaseNameRoleSearch && name.Value == projection.DuplicateCriteria.Name
				}) {
				t.Fatalf("explicit search name missing from projection: %#v", projection.AdditionalNames)
			}
		})
	}
}

func TestRegistryProjectsVersionedReleaseNamesForEveryBuiltIn(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	subject := api.UploadSubject{
		SourcePath:       `C:\Media\Example.Release.2026.1080p.WEB-DL.H.264-GRP.mkv`,
		Filename:         "Example.Release.2026.1080p.WEB-DL.H.264-GRP.mkv",
		ReleaseName:      "Example.Release.2026.1080p.WEB-DL.H.264-GRP",
		ReleaseNameNoTag: "Example.Release.2026.1080p.WEB-DL.H.264",
		SceneName:        "Example.Release.2026.1080p.WEB-DL.H.264-GRP",
		Tag:              "-GRP",
		Type:             "WEBDL",
		Source:           "WEB",
		VideoCodec:       "H.264",
		Audio:            "DDP 5.1",
		Identity: api.ExternalIdentity{
			Category: "MOVIE",
			IMDBID:   1234567,
			TMDBID:   12345,
		},
		Release: api.ReleaseInfo{
			Title:      "Example Release",
			Year:       2026,
			Resolution: "1080p",
			Type:       "WEBDL",
		},
		ProviderMetadata: api.SourceScopedMetadata{
			IMDB: &api.IMDBMetadata{
				IMDBID: 1234567,
				Title:  "Example Release",
				AKA:    "Example Release",
				Year:   2026,
			},
			TMDB: &api.TMDBMetadata{
				TMDBID:           12345,
				Title:            "Example Release",
				OriginalTitle:    "Example Release",
				OriginalLanguage: "en",
				Year:             2026,
			},
		},
	}
	for _, name := range registry.Names() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			descriptor, ok := registry.LookupDescriptor(name)
			if !ok {
				t.Fatalf("descriptor missing")
			}
			projection, failure := registry.ProjectRelease(context.Background(), trackers.PreparationInput{
				Tracker: name,
				Meta:    subject,
			}, "", "", "")
			if failure != nil {
				t.Fatalf("project release: %v", failure)
			}
			if strings.TrimSpace(projection.UploadReleaseName) == "" || strings.TrimSpace(projection.DuplicateCriteria.Name) == "" {
				t.Fatalf("projected names = upload %q, search %q", projection.UploadReleaseName, projection.DuplicateCriteria.Name)
			}
			wantEpisodeTitleMode := api.EpisodeTitleModeInclude
			if name == "BLU" || name == "HDB" {
				wantEpisodeTitleMode = api.EpisodeTitleModeOmit
			}
			if projection.NamingElementPolicyVersion != api.ReleaseNameElementPolicyVersionV1 ||
				projection.EpisodeTitleMode != wantEpisodeTitleMode {
				t.Fatalf(
					"element policy = version %q mode %q, want version %q mode %q",
					projection.NamingElementPolicyVersion,
					projection.EpisodeTitleMode,
					api.ReleaseNameElementPolicyVersionV1,
					wantEpisodeTitleMode,
				)
			}
			if !slices.ContainsFunc(projection.PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
				return decision.Code == "release_name_policy" && decision.Decision == descriptor.ReleaseNamePolicy.ID
			}) {
				t.Fatalf("release-name policy provenance missing: %#v", projection.PolicyDecisions)
			}
			if projection.DuplicateCriteria.Name != projection.UploadReleaseName &&
				!slices.ContainsFunc(projection.AdditionalNames, func(candidate api.TrackerReleaseName) bool {
					return candidate.Role == api.TrackerReleaseNameRoleSearch && candidate.Value == projection.DuplicateCriteria.Name
				}) {
				t.Fatalf("distinct duplicate-search name is undeclared: %#v", projection)
			}
		})
	}
}

func TestRegistryOmitsGeneratedEpisodeTitleForBLU(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	descriptor, ok := registry.LookupDescriptor("BLU")
	if !ok {
		t.Fatal("BLU descriptor missing")
	}
	const included = "Example.Show.S01E02.Example.Episode.1080p.WEB-DL-GRP"
	const omitted = "Example.Show.S01E02.1080p.WEB-DL-GRP"
	input, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker: "BLU",
		Meta: api.UploadSubject{
			ReleaseName: included,
			GeneratedReleaseNames: api.GeneratedReleaseNameVariants{
				IncludeEpisodeTitle: api.ReleaseNameVariant{Name: included},
				OmitEpisodeTitle:    api.ReleaseNameVariant{Name: omitted},
			},
		},
	}, descriptor.ReleaseNamePolicy)
	if failure != nil {
		t.Fatalf("prepare BLU name: %v", failure)
	}
	name, err := input.ReviewedUploadName()
	if err != nil {
		t.Fatalf("reviewed BLU name: %v", err)
	}
	if name != omitted {
		t.Fatalf("BLU release name = %q, want %q", name, omitted)
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
	if policy, ok := registry.LookupDupePolicy("ANT"); !ok || policy.ID != "ant/duplicate/v3" ||
		policy.EvidenceID != "ant-dupes-trumping" {
		t.Fatalf("ANT dupe policy = %#v, %t", policy, ok)
	}
}

func TestNewRegistryMigrationInventoryClassifiesEveryBuiltIn(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}

	familyCounts := map[trackers.Family]int{}
	for _, name := range registry.Names() {
		descriptor, ok := registry.LookupDescriptor(name)
		if !ok {
			t.Fatalf("%s descriptor missing", name)
		}
		definition, ok := registry.Lookup(name)
		if !ok {
			t.Fatalf("%s definition missing", name)
		}
		if descriptor.ReleaseNamePolicy.Resolver == nil || strings.TrimSpace(descriptor.ReleaseNamePolicy.ID) == "" ||
			strings.TrimSpace(descriptor.ProjectorVersion) == "" {
			t.Fatalf("%s versioned release projector missing", name)
		}
		if policy, ok := registry.LookupDupePolicy(name); !ok || strings.TrimSpace(policy.ID) == "" {
			t.Fatalf("%s versioned duplicate policy missing", name)
		}

		var namingAndTaxonomyOwner string
		switch descriptor.Family {
		case trackers.FamilyUnit3D:
			namingAndTaxonomyOwner = "unit3d-site-profile"
		case trackers.FamilyAZFamily:
			namingAndTaxonomyOwner = "azfamily-site-definition"
		case trackers.FamilyStandalone:
			namingAndTaxonomyOwner = "standalone-prepared-operation"
		case trackers.FamilyUnknown:
			t.Fatalf("%s family is unclassified", name)
		}
		familyCounts[descriptor.Family]++

		_, hasDupeAdapter := definition.(dupe.Factory)
		t.Logf(
			"tracker=%s family=%s naming_taxonomy=%s dupe_adapter=%t metadata=%t rules=%t audio=%t artifacts=%t upload_artifact=%t images=%t description_group=%q auth=%t claim=%t banned_static=%t banned_live=%t localized_metadata=%t content_mode=%s",
			name,
			descriptor.Family,
			namingAndTaxonomyOwner,
			hasDupeAdapter,
			descriptor.Metadata != nil,
			descriptor.Rules != nil,
			descriptor.AudioPolicy != nil,
			descriptor.Artifact != nil,
			descriptor.UploadArtifact != nil,
			descriptor.ImageHost != nil,
			descriptor.DescriptionGroup,
			descriptor.AuthCapability != nil || descriptor.AuthResolver != nil,
			descriptor.ClaimPolicy != nil || descriptor.ClaimFactory != nil,
			len(descriptor.BannedGroups) > 0,
			descriptor.BannedPolicy != nil,
			descriptor.MetadataLocale != "",
			descriptor.UploadContentMode,
		)
	}

	for _, family := range []trackers.Family{trackers.FamilyUnit3D, trackers.FamilyAZFamily, trackers.FamilyStandalone} {
		if familyCounts[family] == 0 {
			t.Fatalf("migration inventory has no %s definitions", family)
		}
	}
}

func TestNewRegistryEveryBuiltInPublishesTypedProjectionOutcome(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint("registry-projection-contract")
	if err != nil {
		t.Fatalf("projection fingerprint: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, name := range registry.Names() {
		projection, failure := registry.ProjectRelease(ctx, trackers.PreparationInput{
			Tracker: name,
			Meta: api.UploadSubject{
				ReleaseName: "Example.Release.2026.1080p-GRP",
			},
		}, fingerprint, fingerprint, fingerprint)
		if failure == nil {
			t.Errorf("%s canceled projection returned no typed failure", name)
		}
		if projection.TrackerID != api.TrackerID(name) || projection.UploadReleaseName == "" {
			t.Errorf("%s projection identity/name = %#v", name, projection)
		}
		if projection.InputFingerprint != fingerprint || projection.CatalogFingerprint != fingerprint ||
			projection.ConfigFingerprint != fingerprint || projection.ProjectorFingerprint == "" || projection.CriteriaFingerprint == "" {
			t.Errorf("%s projection fingerprints (failure=%v cause=%v) = %#v", name, failure, failure.Unwrap(), projection)
		}
		if projection.Readiness != api.ReadinessStatusBlocked || projection.DupeReady || projection.UploadReady {
			t.Errorf("%s canceled projection readiness = %#v", name, projection)
		}
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
	for _, name := range []string{"AR", "AZ", "BJS", "CZ", "CZT", "NBL", "PHD", "PTP", "SPD", "THR", "TL", "TVC", "AITHER"} {
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
	if groups, ok := registry.LookupBannedGroups("MNS"); !ok || !slices.Contains(groups, "4K4U") || !slices.Contains(groups, "ZMNT") {
		t.Fatalf("MNS banned groups = %#v, %t", groups, ok)
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
	if policy, ok := registry.LookupDupePolicy("BHD"); !ok || policy.ID != "bhd/duplicate/v2" || policy.SizeVariancePercent != 20 {
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
	if policy, ok := registry.LookupArtifactPolicy("HDB"); !ok || policy.MaxPieceSizeMiB != 32 {
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

func TestNewRegistryIncludesPTPPolicies(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	uploadPolicy, ok := registry.LookupUploadArtifactPolicy("PTP")
	if !ok || uploadPolicy.Source != "PTP" || !uploadPolicy.RequireAnnounce {
		t.Fatalf("PTP upload artifact policy = %#v, %t", uploadPolicy, ok)
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
	bhdPolicy, ok := registry.LookupImageHostPolicy("BHD")
	if !ok ||
		!slices.Equal(bhdPolicy.AllowedHosts, []string{"imgbox", "imgbb", "pixhost", "bhd", "passtheimage"}) ||
		bhdPolicy.DisableWithoutRehost || bhdPolicy.DisableWithoutAPI || bhdPolicy.ConditionalHost != "" {
		t.Errorf("BHD image policy = %#v, %t", bhdPolicy, ok)
	}
}

func TestNewRegistryIncludesAuthResolvers(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	for _, tracker := range []string{"BTN", "FF", "FL", "HDB", "PTP", "RTF"} {
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
	for _, tracker := range registry.Names() {
		capability, ok := registry.LookupAuthCapability(tracker)
		if !ok || capability.TrackerID != tracker {
			t.Errorf("%s auth capability = %#v, %t", tracker, capability, ok)
		}
		requirements, ok := registry.ResolveEffectiveAuthRequirements(tracker, config.Config{}, config.TrackerConfig{})
		if !ok || len(requirements.Alternatives) == 0 {
			t.Errorf("%s effective auth requirements = %#v, %t", tracker, requirements, ok)
		}
	}
}

func TestNewRegistryResolvesHybridAuthRequirements(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	tests := []struct {
		name       string
		tracker    string
		cfg        config.TrackerConfig
		wantMode   string
		want2FA    bool
		wantFirst  []trackers.AuthRequirement
		wantSecond []trackers.AuthRequirement
	}{
		{
			name:      "BTN API and cookie",
			tracker:   "BTN",
			wantMode:  "api_and_upload_session",
			want2FA:   true,
			wantFirst: []trackers.AuthRequirement{trackers.AuthRequirementAPIKey, trackers.AuthRequirementStoredCookie},
			wantSecond: []trackers.AuthRequirement{
				trackers.AuthRequirementAPIKey,
				trackers.AuthRequirementCredentialLogin,
			},
		},
		{
			name:     "HDB username passkey cookie",
			tracker:  "HDB",
			wantMode: "passkey_cookie",
			wantFirst: []trackers.AuthRequirement{
				trackers.AuthRequirementUsername,
				trackers.AuthRequirementPasskey,
				trackers.AuthRequirementStoredCookie,
			},
		},
		{
			name:     "PTP API and upload session",
			tracker:  "PTP",
			wantMode: "api_and_upload_session",
			want2FA:  true,
			wantFirst: []trackers.AuthRequirement{
				trackers.AuthRequirementAPIUser,
				trackers.AuthRequirementAPIKey,
				trackers.AuthRequirementStoredCookie,
			},
		},
		{
			name:      "RTF API key or credential refresh",
			tracker:   "RTF",
			wantMode:  "api_key_or_refresh",
			wantFirst: []trackers.AuthRequirement{trackers.AuthRequirementAPIKey},
			wantSecond: []trackers.AuthRequirement{
				trackers.AuthRequirementCredentialLogin,
			},
		},
		{
			name:      "TL API upload",
			tracker:   "TL",
			cfg:       config.TrackerConfig{APIUpload: true},
			wantMode:  "api_upload",
			wantFirst: []trackers.AuthRequirement{trackers.AuthRequirementPasskey},
		},
		{
			name:      "TL form upload",
			tracker:   "TL",
			wantMode:  "form_upload",
			wantFirst: []trackers.AuthRequirement{trackers.AuthRequirementStoredCookie},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			requirements, ok := registry.ResolveEffectiveAuthRequirements(test.tracker, config.Config{}, test.cfg)
			if !ok {
				t.Fatalf("%s requirements missing", test.tracker)
			}
			if requirements.Mode != test.wantMode || requirements.Supports2FA != test.want2FA {
				t.Fatalf("%s requirements = %#v", test.tracker, requirements)
			}
			if len(requirements.Alternatives) == 0 || !slices.Equal(requirements.Alternatives[0].AllOf, test.wantFirst) {
				t.Fatalf("%s first alternative = %#v, want %#v", test.tracker, requirements.Alternatives, test.wantFirst)
			}
			if test.wantSecond != nil &&
				(len(requirements.Alternatives) < 2 || !slices.Equal(requirements.Alternatives[1].AllOf, test.wantSecond)) {
				t.Fatalf("%s second alternative = %#v, want %#v", test.tracker, requirements.Alternatives, test.wantSecond)
			}
		})
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
