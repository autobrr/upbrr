// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package architecturepolicy

import (
	"go/token"
	"strings"
	"testing"
)

func TestProviderMetadataReviewRejectsBypasses(t *testing.T) {
	t.Parallel()
	const baseline = `package sample
func providerTitle(subject api.UploadSubject) string { return subject.ProviderMetadata.TMDB.Title }
func title(subject api.UploadSubject) string { return trackers.PreferredTitle(subject, providerTitle(subject)) }
func identity(subject api.UploadSubject) int { return subject.ProviderMetadata.TMDB.ID }
func partialYear(year int, include bool) int { if include { return year }; return 0 }
`
	for _, test := range []struct {
		name   string
		source string
		want   string
	}{
		{name: "reviewed provider identity evidence", source: baseline},
		{name: "format and comment changes", source: "// comment\n" + strings.ReplaceAll(baseline, " {", "  {")},
		{
			name:   "new void payload consumer",
			source: baseline + "func bad(s api.UploadSubject, payload map[string]string) { payload[\"title\"] = providerTitle(s) }\n",
			want:   ":bad",
		},
		{
			name:   "changed explicitly reviewed primitive boundary",
			source: strings.ReplaceAll(baseline, "if include { return year }; return 0", "return year"),
			want:   ":partialYear",
		},
		{
			name:   "changed approved getter bypasses manual precedence",
			source: strings.ReplaceAll(baseline, "trackers.PreferredTitle(subject, providerTitle(subject))", "providerTitle(subject)"),
			want:   ":title",
		},
		{
			name:   "new direct provider read",
			source: baseline + "func bad(s api.UploadSubject) string { return s.ProviderMetadata.TMDB.Title }\n",
			want:   ":bad",
		},
		{
			name:   "new typed provider read",
			source: baseline + "func bad(s *api.TMDBMetadata) string { return s.Title }\n",
			want:   ":bad",
		},
		{
			name:   "new typed provider disambiguation year read",
			source: baseline + "func bad(e api.TVDBNameDisambiguation) int { return e.SeriesYear }\n",
			want:   ":bad",
		},
		{
			name:   "new primitive getter caller",
			source: baseline + "func bad(s api.UploadSubject) string { return providerTitle(s) }\n",
			want:   ":bad",
		},
		{
			name: "new scalar forwarding helper and terminal consumer",
			source: baseline + "func forward(s api.UploadSubject) string { return providerTitle(s) }\n" +
				"func bad(s api.UploadSubject, payload map[string]string) { payload[\"title\"] = forward(s) }\n",
			want: ":bad",
		},
		{
			name:   "new function value alias",
			source: baseline + "var bad = providerTitle\n",
			want:   ":bad",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			const path = "internal/trackers/impl/sample/name.go"
			writePolicyFixture(t, root, path, baseline)
			fset := token.NewFileSet()
			consumers, err := collectProviderMetadataConsumers(root, fset)
			if err != nil {
				t.Fatal(err)
			}
			reviews := make(map[string]providerMetadataReview, len(consumers))
			for _, consumer := range consumers {
				fingerprint, err := providerMetadataFingerprint(consumer.decl)
				if err != nil {
					t.Fatal(err)
				}
				reviews[consumer.key] = providerMetadataReview{SHA256: fingerprint, Reason: "Manual preference wraps title; identity remains provider evidence."}
			}
			writePolicyFixture(t, root, path, test.source)
			fset = token.NewFileSet()
			consumers, err = collectProviderMetadataConsumers(root, fset)
			if err != nil {
				t.Fatal(err)
			}
			violations, err := verifyProviderMetadataConsumers(consumers, reviews, fset)
			if err != nil {
				t.Fatal(err)
			}
			if test.want == "" {
				if len(violations) != 0 {
					t.Fatalf("reviewed source rejected: %v", violations)
				}
				return
			}
			assertViolationContains(t, violations, test.want)
		})
	}
}

func TestProviderMetadataReviewRejectsImportedGetterCaller(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writePolicyFixture(t, root, "internal/trackers/impl/sample/name.go", "package sample\nfunc Title(s api.UploadSubject) string { return s.ProviderMetadata.TMDB.Title }\n")
	writePolicyFixture(t, root, "internal/trackers/impl/other/payload.go", `package other
import candidate "github.com/autobrr/upbrr/internal/trackers/impl/sample"
func bad(s api.UploadSubject, payload map[string]string) { payload["title"] = candidate.Title(s) }
`)
	fset := token.NewFileSet()
	consumers, err := collectProviderMetadataConsumers(root, fset)
	if err != nil {
		t.Fatal(err)
	}
	violations, err := verifyProviderMetadataConsumers(consumers, nil, fset)
	if err != nil {
		t.Fatal(err)
	}
	assertViolationContains(t, violations, ":bad")
}

func TestProviderMetadataReviewFollowsPointerGetters(t *testing.T) {
	t.Parallel()
	const pointerBaseline = `package sample
func providerTitle(subject api.UploadSubject) *string { return &subject.ProviderMetadata.TMDB.Title }
func forward(subject api.UploadSubject) **string { value := providerTitle(subject); return &value }
`
	const excludedBaseline = `package sample
type providerHandle struct{}
func providerTitle(subject api.UploadSubject) *providerHandle { _ = subject.ProviderMetadata.TMDB.Title; return nil }
`
	for _, test := range []struct {
		baseline string
		name     string
		source   string
		want     string
	}{
		{
			baseline: pointerBaseline,
			name:     "terminal consumer of nested pointer getter",
			source:   pointerBaseline + "func bad(s api.UploadSubject, payload map[string]string) { payload[\"title\"] = **forward(s) }\n",
			want:     ":bad",
		},
		{
			baseline: excludedBaseline,
			name:     "unsupported pointed-to named type",
			source:   excludedBaseline + "func bad(s api.UploadSubject, payload map[string]string) { _ = providerTitle(s); payload[\"title\"] = \"preserved\" }\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			const path = "internal/trackers/impl/sample/name.go"
			writePolicyFixture(t, root, path, test.baseline)
			fset := token.NewFileSet()
			consumers, err := collectProviderMetadataConsumers(root, fset)
			if err != nil {
				t.Fatal(err)
			}
			reviews := make(map[string]providerMetadataReview, len(consumers))
			for _, consumer := range consumers {
				fingerprint, err := providerMetadataFingerprint(consumer.decl)
				if err != nil {
					t.Fatal(err)
				}
				reviews[consumer.key] = providerMetadataReview{SHA256: fingerprint, Reason: "Manual preference wraps title; identity remains provider evidence."}
			}
			writePolicyFixture(t, root, path, test.source)
			fset = token.NewFileSet()
			consumers, err = collectProviderMetadataConsumers(root, fset)
			if err != nil {
				t.Fatal(err)
			}
			violations, err := verifyProviderMetadataConsumers(consumers, reviews, fset)
			if err != nil {
				t.Fatal(err)
			}
			if test.want == "" {
				if len(violations) != 0 {
					t.Fatalf("unsupported pointer result propagated: %v", violations)
				}
				return
			}
			assertViolationContains(t, violations, test.want)
		})
	}
}
