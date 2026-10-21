// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/externalidentity"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPrepareCertifiesEqualFinalizedFactsWithNewCorrectionAuthority(t *testing.T) {
	for _, test := range []struct {
		name       string
		change     func(*api.PrepareInput)
		equivalent bool
	}{
		{name: "ordinary", equivalent: true},
		{name: "changed finalized source", change: func(input *api.PrepareInput) { value := "WEB"; input.Instructions.ReleaseName.Source = &value }},
		{name: "force", change: func(input *api.PrepareInput) { input.Force = true }},
		{name: "provider refresh", change: func(input *api.PrepareInput) { input.ExternalFreshness = api.ExternalFreshnessRefresh }},
		{name: "provider load", change: func(input *api.PrepareInput) { input.ExternalFreshness = api.ExternalFreshnessLoad }},
		{name: "capture", change: func(input *api.PrepareInput) { input.Controls.CaptureHDRMetadata = true }},
		{name: "rescan", change: func(input *api.PrepareInput) { input.Controls.ConfirmBDMVRescan = true }},
		{name: "recheck", change: func(input *api.PrepareInput) { value := true; input.Controls.ForceRecheck = &value }},
		{name: "policy", change: func(input *api.PrepareInput) { input.Search.Skip = true }},
		{name: "unverified", change: func(input *api.PrepareInput) { input.VerifiedSource = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := writePreparedTestFile(t, "Example.2026.mkv", "synthetic media")
			input := api.PrepareInput{SourcePath: source}
			verified, err := VerifyInputSource(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			input.VerifiedSource = &verified
			store := newMemoryStore()
			module := newTestModule(t, store, sourceCorrectionCollector{})
			first, err := module.Prepare(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			if first.EquivalentPreviousRelease != nil {
				t.Fatal("initial preparation certified a predecessor")
			}
			explicit := first.Release.Media.Source
			input.Instructions.ReleaseName.Source = &explicit
			if test.change != nil {
				test.change(&input)
			}
			second, err := module.Prepare(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			if second.Release.Generation != first.Release.Generation+1 || second.Corrections.Revision <= first.Corrections.Revision {
				t.Fatalf("new authority missing: first=%#v second=%#v", first, second)
			}
			if (second.EquivalentPreviousRelease != nil) != test.equivalent {
				t.Fatalf("equivalent predecessor = %#v, want present %v", second.EquivalentPreviousRelease, test.equivalent)
			}
			if test.equivalent {
				want := api.ReleaseRef{SourcePath: source, Generation: first.Release.Generation}
				if *second.EquivalentPreviousRelease != want {
					t.Fatalf("predecessor = %#v, want %#v", *second.EquivalentPreviousRelease, want)
				}
				if second.Release.Compatibility.FactInstructionFingerprint == first.Release.Compatibility.FactInstructionFingerprint {
					t.Fatal("correction authority did not change compatibility")
				}
				reused, err := module.Prepare(t.Context(), input)
				if err != nil {
					t.Fatal(err)
				}
				if reused.Release.Generation != second.Release.Generation || reused.EquivalentPreviousRelease != nil {
					t.Fatal("cache reuse replayed predecessor certificate")
				}
			}
		})
	}
}

func TestEquivalentPreparedFactsPreservesSemanticAuthority(t *testing.T) {
	baseline := api.PreparedRelease{
		Generation: 1,
		Compatibility: api.PreparationCompatibility{
			SourceFingerprint: "source",
			PolicyFingerprint: "policy",
			ContractVersion:   ContractVersion,
		},
		SourceIdentity: api.SourceContentIdentity{Version: "sample", Digest: "digest"},
	}
	for _, test := range []struct {
		name       string
		change     func(*api.PreparedRelease)
		equivalent bool
	}{
		{
			name:       "bookkeeping",
			equivalent: true,
			change: func(release *api.PreparedRelease) {
				release.Generation = 2
				release.Compatibility.FactInstructionFingerprint = "new correction authority"
				release.PreparedAt = time.Now()
				release.Identity.Generation = 2
				release.Identity.ResolvedAt = time.Now()
				release.ProviderMetadata.Generation = 2
				release.ProviderMetadata.UpdatedAt = time.Now()
			},
		},
		{name: "source bytes", change: func(release *api.PreparedRelease) { release.SourceIdentity.Digest = "changed" }},
		{name: "source fingerprint", change: func(release *api.PreparedRelease) { release.Compatibility.SourceFingerprint = "changed" }},
		{name: "policy", change: func(release *api.PreparedRelease) { release.Compatibility.PolicyFingerprint = "changed" }},
		{name: "contract", change: func(release *api.PreparedRelease) { release.Compatibility.ContractVersion = "changed" }},
		{name: "media", change: func(release *api.PreparedRelease) { release.Media.Source = "WEB" }},
		{name: "naming", change: func(release *api.PreparedRelease) { release.Naming.Year = 2027 }},
		{name: "provider", change: func(release *api.PreparedRelease) { release.ProviderMetadata.TMDB = &api.TMDBMetadata{TMDBID: 123} }},
		{name: "provenance", change: func(release *api.PreparedRelease) { release.Identity.Provenance.TMDB = api.IdentityProvenanceProvider }},
		{name: "override", change: func(release *api.PreparedRelease) { release.Identity.Overrides.TMDB = api.OverrideStateValue }},
		{name: "identity intent", change: func(release *api.PreparedRelease) { release.Identity.Resolution.IntentFingerprint = "changed" }},
		{name: "conflict", change: func(release *api.PreparedRelease) { release.Identity.Conflict = api.IdentityConflictUnverified }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := baseline
			test.change(&candidate)
			if got := equivalentPreparedFacts(baseline, candidate); got != test.equivalent {
				t.Fatalf("equivalent = %v, want %v", got, test.equivalent)
			}
		})
	}
}

// sourceCorrectionCollector models the finalized source override in the metadata collector.
type sourceCorrectionCollector struct{}

func (sourceCorrectionCollector) Collect(ctx context.Context, request preparationstate.Request) (CollectedFacts, error) {
	facts, err := (correctedFactsCollector{}).Collect(ctx, request)
	if source := request.Input.Instructions.ReleaseName.Source; source != nil {
		facts.Media.Source = *source
		facts.Naming.Source = *source
	}
	return facts, err
}

func TestPrepareEquivalentMetadataUsesCompletePublishedPredecessor(t *testing.T) {
	for _, name := range []string{
		"unchanged availability", "changed availability", "missing published predecessor", "advanced stored generation",
		"hydrated missing availability", "transferred complete", "transferred incomplete",
	} {
		t.Run(name, func(t *testing.T) {
			repo, err := db.OpenContext(t.Context(), filepath.Join(t.TempDir(), "prepared.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repo.Close() })
			if err := repo.MigrateContext(t.Context()); err != nil {
				t.Fatal(err)
			}
			resolver := &availabilityIdentityResolver{source: "original"}
			module, err := New(repo, resolver, sourceCorrectionCollector{})
			if err != nil {
				t.Fatal(err)
			}
			input := api.PrepareInput{SourcePath: writePreparedTestFile(t, "Example.2026.mkv", "synthetic media")}
			verified, err := VerifyInputSource(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			input.VerifiedSource = &verified
			first, err := module.Prepare(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			stored, err := repo.LoadPreparedRelease(t.Context(), input.SourcePath)
			if err != nil {
				t.Fatal(err)
			}
			if len(first.Release.ProviderMetadata.ProviderAvailability) != 1 || len(stored.ProviderMetadata.ProviderAvailability) != 0 {
				t.Fatal("fixture did not exercise provider availability omitted by persistence")
			}
			switch name {
			case "changed availability":
				resolver.source = "changed"
			case "missing published predecessor":
				module.Invalidate(input.SourcePath)
			case "advanced stored generation":
				stored.Generation++
				stored.Identity.Generation = stored.Generation
				stored.ProviderMetadata.Generation = stored.Generation
				if err := repo.CommitPreparedRelease(t.Context(), stored); err != nil {
					t.Fatal(err)
				}
			}
			if name == "hydrated missing availability" || name == "transferred incomplete" {
				module.Invalidate(input.SourcePath)
				hydrated, err := module.Prepare(t.Context(), input)
				if err != nil {
					t.Fatal(err)
				}
				if hydrated.Release.Generation != first.Release.Generation || len(hydrated.Release.ProviderMetadata.ProviderAvailability) != 0 {
					t.Fatal("fixture did not hydrate the persisted incomplete generation")
				}
				resolver.source = ""
			}
			if name == "transferred complete" || name == "transferred incomplete" {
				seed, err := module.Export(t.Context(), api.ReleaseRef{SourcePath: input.SourcePath, Generation: first.Release.Generation})
				if err != nil {
					t.Fatal(err)
				}
				module, err = New(repo, resolver, sourceCorrectionCollector{})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := module.Import(t.Context(), seed); err != nil {
					t.Fatal(err)
				}
			}
			input.Instructions.ReleaseName.Source = new(first.Release.Media.Source)
			second, err := module.Prepare(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := second.EquivalentPreviousRelease != nil, name == "unchanged availability" || name == "transferred complete"; got != want {
				t.Fatalf("equivalent predecessor = %#v, want present %v", second.EquivalentPreviousRelease, want)
			}
			if second.Corrections.Revision <= first.Corrections.Revision || second.Release.Generation <= first.Release.Generation {
				t.Fatal("equivalence did not publish new correction and generation authority")
			}
			if second.EquivalentPreviousRelease != nil && *second.EquivalentPreviousRelease != (api.ReleaseRef{
				SourcePath: input.SourcePath, Generation: first.Release.Generation,
			}) {
				t.Fatal("certificate identifies a different predecessor")
			}
		})
	}
}

type availabilityIdentityResolver struct{ source string }

func (r *availabilityIdentityResolver) Resolve(ctx context.Context, request externalidentity.Request) (externalidentity.Result, error) {
	result, err := (staticIdentityResolver{}).Resolve(ctx, request)
	if r.source != "" {
		result.ProviderMetadata.ProviderAvailability = []api.ProviderAvailabilityEvidence{{
			Provider: api.IdentityProviderTMDB,
			Status:   api.ProviderAvailabilityStatusAvailable,
			Source:   r.source,
		}}
	}
	return result, err
}

func (sourceCorrectionCollector) HydratePrivateResources(_ context.Context, request preparationstate.Request) (CollectedResources, error) {
	return CollectedResources{SourcePath: request.Manifest.SourcePath}, nil
}
