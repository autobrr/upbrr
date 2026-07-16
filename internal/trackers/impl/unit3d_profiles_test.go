// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/a4k"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/acm"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/aither"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/blu"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/cbr"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/dp"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/emuw"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/friki"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/hhd"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/ihd"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/itt"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/lcd"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/ldu"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/lst"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/lt"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/lume"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/mns"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/oe"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/otw"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/pt"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/ptt"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/r4e"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/ras"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/rf"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/rhd"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/sam"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/shri"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/sp"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/stc"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/tik"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/tlz"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/tos"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/ttr"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/ulcx"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/utp"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/yus"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/znth"
	"github.com/autobrr/upbrr/internal/trackers/unit3dmeta"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestExplicitUnit3DProfilesPreserveDefaultEndpoints(t *testing.T) {
	profiles := []unit3d.Profile{
		a4k.Profile(), acm.Profile(), aither.Profile(), blu.Profile(), cbr.Profile(), dp.Profile(), emuw.Profile(), friki.Profile(), hhd.Profile(), ihd.Profile(), itt.Profile(), lcd.Profile(), ldu.Profile(), lt.Profile(),
		lume.Profile(), lst.Profile(), mns.Profile(), oe.Profile(), otw.Profile(), pt.Profile(), ptt.Profile(), r4e.Profile(), ras.Profile(), rf.Profile(), rhd.Profile(), sam.Profile(), shri.Profile(),
		sp.Profile(), stc.Profile(), tik.Profile(), tlz.Profile(), tos.Profile(), ttr.Profile(), ulcx.Profile(), utp.Profile(), yus.Profile(), znth.Profile(),
	}
	for _, profile := range profiles {
		want, ok := unit3dmeta.BaseURL(profile.Name)
		if !ok {
			t.Fatalf("profile %s is missing from Unit3D metadata", profile.Name)
		}
		if profile.BaseURL != want {
			t.Fatalf("profile %s base URL = %q, want %q", profile.Name, profile.BaseURL, want)
		}
	}
}

func TestExplicitUnit3DProfilesPreserveResolvers(t *testing.T) {
	tests := []struct {
		name    string
		profile unit3d.Profile
		meta    api.UploadSubject
		want    string
	}{
		{
			name:    "A4K rejects WEBRIP",
			profile: a4k.Profile(),
			meta:    api.UploadSubject{Type: "WEBRIP"},
			want:    "",
		},
		{
			name:    "ITT DLMux",
			profile: itt.Profile(),
			meta:    api.UploadSubject{ReleaseName: "Example.Release.2026.1080p.DLMux-GRP", Type: "WEBDL"},
			want:    "27",
		},
		{
			name:    "OE HEVC",
			profile: oe.Profile(),
			meta:    api.UploadSubject{Type: "WEBRIP", VideoCodec: "HEVC"},
			want:    "10",
		},
		{
			name:    "OTW DVD",
			profile: otw.Profile(),
			meta:    api.UploadSubject{DiscType: "DVD", Type: "REMUX"},
			want:    "7",
		},
		{
			name:    "STC pack",
			profile: stc.Profile(),
			meta: api.UploadSubject{
				Type:    "WEBDL",
				TVPack:  true,
				Release: api.ReleaseInfo{Resolution: "1080p"},
			},
			want: "13",
		},
		{
			name:    "TLZ pack",
			profile: tlz.Profile(),
			meta:    api.UploadSubject{Identity: api.ExternalIdentity{Category: "TV"}, TVPack: true},
			want:    "4",
		},
		{
			name:    "ZNTH DVDRIP",
			profile: znth.Profile(),
			meta:    api.UploadSubject{Type: "DVDRIP"},
			want:    "11",
		},
		{
			name:    "RF encode",
			profile: rf.Profile(),
			meta:    api.UploadSubject{Type: "ENCODE"},
			want:    "41",
		},
		{
			name:    "YUS disc",
			profile: yus.Profile(),
			meta:    api.UploadSubject{Type: "DISC"},
			want:    "17",
		},
		{
			name:    "BLU encode",
			profile: blu.Profile(),
			meta:    api.UploadSubject{Type: "ENCODE"},
			want:    "12",
		},
		{
			name:    "PT WEBRIP",
			profile: pt.Profile(),
			meta:    api.UploadSubject{Type: "WEBRIP"},
			want:    "39",
		},
		{
			name:    "SHRI remux",
			profile: shri.Profile(),
			meta:    api.UploadSubject{Type: "REMUX"},
			want:    "7",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.profile.Site.ResolveTypeID(test.meta); got != test.want {
				t.Fatalf("type ID = %q, want %q", got, test.want)
			}
		})
	}
	if got := rf.Profile().Site.ResolveResolutionID(api.UploadSubject{Release: api.ReleaseInfo{Resolution: "1440p"}}); got != "10" {
		t.Fatalf("RF resolution ID = %q, want 10", got)
	}
	if got := utp.Profile().Site.ResolveResolutionID(api.UploadSubject{Release: api.ReleaseInfo{Resolution: "720p"}}); got != "11" {
		t.Fatalf("UTP resolution ID = %q, want 11", got)
	}
	if got := blu.Profile().Site.ResolveResolutionID(api.UploadSubject{Release: api.ReleaseInfo{Resolution: "2160p"}}); got != "1" {
		t.Fatalf("BLU resolution ID = %q, want 1", got)
	}
	if got := ihd.Profile().Site.ResolveCategoryID(api.UploadSubject{Identity: api.ExternalIdentity{Category: "MOVIE"}, Anime: true}); got != "4" {
		t.Fatalf("IHD category ID = %q, want 4", got)
	}
	if got := tos.Profile().Site.ResolveCategoryID(api.UploadSubject{
		Identity: api.ExternalIdentity{Category: "TV"},
		TVPack:   true,
		Tag:      "-vostfr",
	}); got != "9" {
		t.Fatalf("TOS category ID = %q, want 9", got)
	}
	if got := ldu.Profile().Site.ResolveCategoryID(api.UploadSubject{Identity: api.ExternalIdentity{Category: "TV"}, Anime: true}); got != "9" {
		t.Fatalf("LDU anime category ID = %q, want 9", got)
	}
	if got := ldu.Profile().Site.ResolveCategoryID(api.UploadSubject{
		Identity:          api.ExternalIdentity{Category: "MOVIE"},
		AudioLanguages:    []string{"Portuguese"},
		SubtitleLanguages: []string{"Portuguese"},
	}); got != "22" {
		t.Fatalf("LDU non-English category ID = %q, want 22", got)
	}
	data := map[string]string{}
	pt.Profile().Site.ApplyAdditionalPayload(trackers.PreparationInput{Meta: api.UploadSubject{AudioLanguages: []string{"Portuguese"}, SubtitleLanguages: []string{"PT-BR", "English"}}}, data)
	if data["audio_pt"] != "1" || data["legenda_pt"] != "0" {
		t.Fatalf("PT language payload = %#v", data)
	}
	shriData := map[string]string{}
	shri.Profile().Site.ApplyAdditionalPayload(trackers.PreparationInput{Meta: api.UploadSubject{Region: "3", Distributor: "42"}}, shriData)
	if shriData["region_id"] != "3" || shriData["distributor_id"] != "42" {
		t.Fatalf("SHRI payload = %#v", shriData)
	}
	description := shri.Profile().Site.FinalizeDescription("Base description", api.UploadSubject{Release: api.ReleaseInfo{Group: "island"}})
	if !strings.Contains(description, "Release Shareisland") || !strings.Contains(description, "Base description") {
		t.Fatalf("SHRI description = %q", description)
	}
	lstData := map[string]string{}
	lst.Profile().Site.ApplyAdditionalPayload(trackers.PreparationInput{TrackerConfig: config.TrackerConfig{Draft: true}, Meta: api.UploadSubject{Edition: "Director's Cut"}}, lstData)
	if lstData["draft_queue_opt_in"] != "1" || lstData["edition_id"] != "2" {
		t.Fatalf("LST payload = %#v", lstData)
	}
	aitherData := map[string]string{}
	aither.Profile().Site.ApplyAdditionalPayload(trackers.PreparationInput{Meta: api.UploadSubject{HDR: "HDR10+ DV"}}, aitherData)
	if aitherData["hdr10p"] != "1" || aitherData["dv"] != "1" {
		t.Fatalf("AITHER HDR payload = %#v", aitherData)
	}
	aitherMeta := api.UploadSubject{
		ReleaseName: "Example.Release.2020.DVD.DVDRIP.AAC.XVID-GRP",
		Release:     api.ReleaseInfo{Year: 2020, Resolution: "480p"},
		Type:        "DVDRIP",
		Source:      "DVD",
		Audio:       "AAC 2.0",
		VideoEncode: "XVID",
	}
	if got := aither.Profile().Site.BuildName(aitherMeta, config.TrackerConfig{}); got == "" || got == aitherMeta.ReleaseName {
		t.Fatalf("AITHER name = %q", got)
	}
	r4eMeta := api.UploadSubject{Identity: api.ExternalIdentity{Category: "TV"}, ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{GenreIDs: "99,18"}}}
	if got := r4e.Profile().Site.ResolveCategoryID(r4eMeta); got != "2" {
		t.Fatalf("R4E category ID = %q, want 2", got)
	}
}
