// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

// These are finalized technical facts, not filename detection or eligibility claims.
func TestRegistryAitherFinalizedTechnicalNames(t *testing.T) {
	registry := MustNewRegistry()
	for _, tc := range []struct {
		name, kind, disc, source, codec, encode, region, threeD, want string
	}{
		{"MGVC disc", "DISC", "BDMV", "Blu-ray", "MGVC", "", "", "", "TMDB Signal 2001 1080p Blu-ray MGVC AAC 2.0-GRP"},
		{"Hi10P encode", "ENCODE", "", "BluRay", "AVC", "Hi10P x264", "", "", "TMDB Signal 2001 1080p BluRay AAC 2.0 Hi10P x264-GRP"},
		{"regional 3D disc", "DISC", "BDMV", "Blu-ray", "AVC", "", "CEE", "3D", "TMDB Signal 2001 1080p CEE 3D Blu-ray AVC AAC 2.0-GRP"},
	} {
		omissions := []bool{false}
		if tc.threeD != "" {
			omissions = append(omissions, true)
		}
		for _, omitted := range omissions {
			t.Run(tc.name+"/omitted="+strconv.FormatBool(omitted), func(t *testing.T) {
				subject := providerNameSubject(api.CanonicalCategoryMovie)
				extension := ".mkv"
				if tc.kind == "DISC" {
					extension = ".iso"
				}
				sourcePath := filepath.Join("media", "Example.Movie.2026"+extension)
				subject.SourcePath, subject.VideoPath, subject.Filename = sourcePath, sourcePath, filepath.Base(sourcePath)
				subject.Identity.SourcePath, subject.ProviderMetadata.SourcePath = sourcePath, sourcePath
				subject.Type, subject.Release.Type, subject.DiscType = tc.kind, tc.kind, tc.disc
				subject.Source, subject.Release.Source = tc.source, tc.source
				subject.VideoCodec, subject.VideoEncode, subject.Region, subject.Is3D = tc.codec, tc.encode, tc.region, tc.threeD
				for index := range subject.GeneratedName.Components {
					component := &subject.GeneratedName.Components[index]
					if component.Role == api.NameRoleVideoFormat {
						component.Role, component.Value, component.AvailableValue = api.NameRoleSource, tc.source, tc.source
					}
					if component.Role == api.NameRoleVideoCodec {
						component.Value, component.AvailableValue = tc.codec, tc.codec
						if tc.encode != "" {
							component.Role, component.Value, component.AvailableValue = api.NameRoleVideoEncode, tc.encode, tc.encode
						}
					}
				}
				if tc.kind == "DISC" {
					audio := slices.IndexFunc(subject.GeneratedName.Components, func(part api.ReleaseNameComponent) bool { return part.Role == api.NameRoleAudio })
					video := slices.IndexFunc(subject.GeneratedName.Components, func(part api.ReleaseNameComponent) bool { return part.Role == api.NameRoleVideoCodec })
					subject.GeneratedName.Components[audio], subject.GeneratedName.Components[video] = subject.GeneratedName.Components[video], subject.GeneratedName.Components[audio]
				}
				if tc.threeD != "" {
					subject.ReleaseFeatures = []api.ReleaseFeature{api.ReleaseFeature3DHalfSBS}
					position := slices.IndexFunc(subject.GeneratedName.Components, func(part api.ReleaseNameComponent) bool { return part.Role == api.NameRoleSource })
					subject.GeneratedName.Components = slices.Insert(subject.GeneratedName.Components, position,
						api.ReleaseNameComponent{
							Role:           api.NameRoleRegion,
							Value:          tc.region,
							AvailableValue: tc.region,
							Present:        true,
							Join:           " ",
						},
						api.ReleaseNameComponent{
							Role:           api.NameRoleThreeD,
							Value:          "3D",
							AvailableValue: "3D",
							Present:        !omitted,
							Manual:         omitted,
							Join:           " ",
						},
					)
				}
				rendered := subject.GeneratedName.Render()
				subject.ReleaseName, subject.ReleaseNameNoTag = rendered.Name, rendered.NameNoTag
				subject.GeneratedReleaseNames.IncludeEpisodeTitle, subject.GeneratedReleaseNames.OmitEpisodeTitle = rendered, rendered
				before := providerSubjectSnapshot(t, subject)
				want := tc.want
				if omitted {
					want = strings.Replace(want, " 3D", "", 1)
				}
				prepared := prepareProviderName(t, registry, "AITHER", subject, nil)
				if prepared.Projection.UploadReleaseName != want || prepared.Projection.DuplicateCriteria.Name != want {
					t.Fatalf("upload/search=%q/%q, want %q", prepared.Projection.UploadReleaseName, prepared.Projection.DuplicateCriteria.Name, want)
				}
				requested := "Exact Manual MGVC Hi10P 3D Name"
				reviewed := prepareProviderName(t, registry, "AITHER", subject, &requested)
				if reviewed.Projection.UploadReleaseName != requested || reviewed.Projection.DuplicateCriteria.Name != want || !slices.Equal(before, providerSubjectSnapshot(t, subject)) {
					t.Fatal("manual name changed, search naming changed, or canonical facts mutated")
				}
			})
		}
	}
}
