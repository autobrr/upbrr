// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package aither

import (
	"github.com/autobrr/upbrr/pkg/api"
	"slices"
	"testing"
)

func TestThreeDFullDiscRequiresISOPackage(t *testing.T) {
	for _, test := range []struct {
		name, threeD, kind string
		extensions         []string
		status             api.MetadataEvidenceStatus
		blocked            bool
	}{
		{"3D folder", "3D", "DISC", []string{".m2ts"}, api.MetadataEvidenceStatusComplete, true},
		{"mixed ISO and unpacked disc", "3D", "DISC", []string{".iso", ".m2ts"}, api.MetadataEvidenceStatusComplete, true},
		{"3D ISO", "3D", "DISC", []string{".iso"}, api.MetadataEvidenceStatusComplete, false},
		{"missing package evidence", "3D", "DISC", nil, api.MetadataEvidenceStatusUnavailable, true},
		{"2D folder", "", "DISC", []string{".m2ts"}, api.MetadataEvidenceStatusComplete, false},
		{"3D remux", "3D", "REMUX", []string{".mkv"}, api.MetadataEvidenceStatusComplete, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := aitherPassingSubject()
			subject.DiscType = "BDMV"
			subject.Type = test.kind
			subject.Is3D = test.threeD
			files := make([]string, 0, len(test.extensions))
			for _, extension := range test.extensions {
				files = append(files, "synthetic"+extension)
			}
			subject.PackageFacts = api.NewTrackerValidationSubject(api.UploadSubject{FileList: files}, "AITHER").PackageFacts
			subject.PackageFacts.Status = test.status
			failures := evaluateAitherEvidence(t, subject)
			found := slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "aither_3d_iso" })
			if found != test.blocked {
				t.Fatalf("ISO gate = %t, want %t: %+v", found, test.blocked, failures)
			}
		})
	}
}

func TestThreeDDVDNameKeepsResolutionBeforeSubtype(t *testing.T) {
	for _, test := range []struct {
		name, kind, disc, source, size, region, threeD, want string
	}{
		{"rip", "DVDRIP", "", "DVD", "", "", "3D", "Example Film 2026 480p 3D DVDRip-GRP"},
		{"DVD5", "DISC", "DVD", "DVD", "DVD5", "", "3D Full SBS", "Example Film 2026 480p 3D Full SBS DVD5-GRP"},
		{"DVD9", "DISC", "DVD", "DVD", "DVD9", "", "3D Half OU", "Example Film 2026 480p 3D Half OU DVD9-GRP"},
		{"system and region", "DISC", "DVD", "PAL DVD", "DVD9", "R2", "3D", "Example Film 2026 480p R2 PAL 3D DVD9-GRP"},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := aitherGeneratedSubject(t, api.ReleaseNameRequest{
				Category:   "MOVIE",
				Type:       test.kind,
				DiscType:   test.disc,
				Title:      "Example Film",
				Year:       2026,
				Resolution: "480p",
				Source:     test.source,
				DVDSize:    test.size,
				Region:     test.region,
				ThreeD:     test.threeD,
				Tag:        "-GRP",
			}, []string{"English"})
			if got := aitherReviewedName(t, subject, nil); got != test.want {
				t.Fatalf("name = %q, want %q", got, test.want)
			}
		})
	}
}

func TestThreeDISORejectsOtherMediaWithDerivedFacts(t *testing.T) {
	for _, test := range []struct {
		name    string
		files   []string
		blocked bool
	}{
		{"ISO only", []string{"disc.iso"}, false},
		{"ISO plus MKV", []string{"disc.iso", "extra.mkv"}, true},
		{"ISO plus MP4", []string{"disc.iso", "extra.mp4"}, true},
		{"ISO plus EVO", []string{"disc.iso", "extra.evo"}, true},
		{"ISO plus extras", []string{"disc.iso", "release.nfo", "proof.jpg"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			facts := api.NewTrackerValidationSubject(api.UploadSubject{FileList: test.files}, "AITHER").PackageFacts
			facts.Status = api.MetadataEvidenceStatusComplete
			subject := aitherPassingSubject()
			subject.Type, subject.DiscType, subject.Is3D = "DISC", "BDMV", "3D"
			subject.PackageFacts = facts
			failures := evaluateAitherEvidence(t, subject)
			blocked := slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "aither_3d_iso" })
			if blocked != test.blocked {
				t.Fatalf("ISO gate = %t, want %t: %+v", blocked, test.blocked, failures)
			}
		})
	}
}
