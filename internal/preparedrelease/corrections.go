// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/metadata/seasonep"
	"github.com/autobrr/upbrr/internal/sourcelayout"
	"github.com/autobrr/upbrr/pkg/api"
)

// CorrectionsCurrent reports whether revision still owns the source's saved corrections.
// It reads persisted authority without inspecting media or accepting new instructions.
func (m *Module) CorrectionsCurrent(ctx context.Context, source string, revision uint64) (bool, error) {
	snapshot, err := m.store.LoadReleaseCorrections(ctx, source)
	if err != nil {
		return false, fmt.Errorf("prepared release: check current corrections: %w", err)
	}
	return snapshot.Revision == revision, nil
}

// ResolveInput accepts corrections before external collection. Optimistic
// transactions keep same-source updates atomic without holding a database
// transaction during source inspection or provider requests.
func (m *Module) ResolveInput(ctx context.Context, raw api.PrepareInput, update api.ReleaseCorrectionUpdate) (api.ResolvedPreparationInput, error) {
	if m == nil || m.store == nil || ctx == nil {
		return api.ResolvedPreparationInput{}, errors.New("prepared release: initialized module and context are required")
	}
	normalized, err := normalizePrepareInput(raw)
	if err != nil {
		return api.ResolvedPreparationInput{}, err
	}
	raw = normalized.input
	if err := api.NormalizeReleaseFactInstructionsCategory(&raw.Instructions); err != nil {
		return api.ResolvedPreparationInput{}, fmt.Errorf("prepared release: normalize correction category: %w", err)
	}
	releaseGate, err := m.gates.acquire(ctx, normalized.sourceKey)
	if err != nil {
		return api.ResolvedPreparationInput{}, fmt.Errorf("prepared release: resolve input gate: %w", err)
	}
	defer releaseGate()
	layout, err := sourcelayout.Resolve(ctx, raw.SourcePath)
	if err != nil {
		return api.ResolvedPreparationInput{}, fmt.Errorf("prepared release: inspect correction source: %w", err)
	}
	_, fingerprint, err := inspectSource(ctx, raw, layout)
	if err != nil {
		return api.ResolvedPreparationInput{}, err
	}
	values := correctionValues(raw.Instructions)
	if err := (api.ReleaseCorrectionPatch{Values: values}).Validate(); err != nil {
		return api.ResolvedPreparationInput{}, fmt.Errorf("prepared release: validate correction values: %w", err)
	}
	// Source-start instructions patch inherited intent. Explicit replacement
	// and reset envelopes are chosen by their workflow adapters instead.
	if update.Mode == api.ReleaseCorrectionUpdateInherit && len(values.Fields()) != 0 {
		update = api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{Values: values}}
	}
	explicit := update.Values.Fields()
	if update.Patch != nil {
		explicit = update.Patch.Values.Fields()
	}
	current, hasCurrent, err := m.loadCurrent(ctx, raw.SourcePath)
	if err != nil {
		return api.ResolvedPreparationInput{}, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return api.ResolvedPreparationInput{}, fmt.Errorf("prepared release: resolve corrections canceled: %w", err)
		}
		stored, err := m.store.LoadReleaseCorrections(ctx, raw.SourcePath)
		if err != nil {
			return api.ResolvedPreparationInput{}, fmt.Errorf("prepared release: load corrections: %w", err)
		}
		accepted, err := api.ApplyReleaseCorrectionUpdate(stored, update)
		if err != nil {
			return api.ResolvedPreparationInput{}, fmt.Errorf("prepared release: apply correction update: %w", err)
		}
		if err := normalizeAcceptedCorrections(&accepted); err != nil {
			return api.ResolvedPreparationInput{}, err
		}
		binding := api.ContentBinding{SourceFingerprint: fingerprint}
		if hasCurrent && current.Compatibility.SourceFingerprint == fingerprint {
			binding = contentBinding(fingerprint, current.Identity)
		}
		applyCorrectionIdentity(&binding, accepted)
		confirmedBinding, hasConfirmation, err := correctionConfirmationBinding(update, stored, accepted, fingerprint)
		if err != nil {
			return api.ResolvedPreparationInput{}, err
		}
		if hasConfirmation {
			binding = confirmedBinding
		}
		bindExplicitCorrections(&accepted, binding, explicit)
		markStaleContent(&accepted, binding, explicit)
		if update.Patch != nil {
			for _, field := range update.Patch.ConfirmFields {
				bindExplicitCorrections(&accepted, binding, []api.CorrectionFieldRef{field})
			}
		}
		if err := validateTrackCorrections(accepted.Metadata.TrackLanguages, current, fingerprint); err != nil {
			return api.ResolvedPreparationInput{}, err
		}
		if len(correctionValuesFromStored(accepted).Fields()) != 0 {
			accepted.SourceFingerprint = fingerprint
		}
		snapshot, err := m.store.CompareAndSwapReleaseCorrections(ctx, raw.SourcePath, stored.Revision, accepted)
		if err != nil {
			if errors.Is(err, api.ErrCorrectionConflict) && (update.Patch == nil || update.Patch.ExpectedRevision == nil) {
				continue
			}
			return api.ResolvedPreparationInput{}, fmt.Errorf("prepared release: accept corrections: %w", err)
		}
		effective, err := effectiveCorrectionInstructions(raw.Instructions, snapshot.Corrections)
		if err != nil {
			return api.ResolvedPreparationInput{}, err
		}
		raw.Instructions = effective
		return api.ResolvedPreparationInput{
			Input:             raw,
			Corrections:       snapshot,
			SourceFingerprint: fingerprint,
			ExplicitFields:    explicit,
		}, nil
	}
}

func correctionConfirmationBinding(
	update api.ReleaseCorrectionUpdate,
	stored api.ReleaseCorrectionsSnapshot,
	accepted api.StoredReleaseCorrectionsV1,
	fingerprint string,
) (api.ContentBinding, bool, error) {
	confirming := update.Patch != nil && len(update.Patch.ConfirmFields) > 0
	confirmation := update.Confirmation
	valid := confirmation != nil && confirmation.Revision == stored.Revision && len(confirmation.Fields) > 0 &&
		confirmation.CurrentBinding.SourceFingerprint == fingerprint
	if valid {
		binding := confirmation.CurrentBinding
		applyCorrectionIdentity(&binding, accepted)
		valid = binding == confirmation.CurrentBinding
	}
	if valid && confirming {
		valid = update.Patch.ExpectedRevision != nil && *update.Patch.ExpectedRevision == confirmation.Revision &&
			slices.Equal(slices.Sorted(slices.Values(confirmation.Fields)), slices.Sorted(slices.Values(stored.Corrections.StaleContentFields)))
		for _, field := range update.Patch.ConfirmFields {
			previous, hasPrevious := confirmation.PreviousBindings[field.Field]
			current, hasCurrent := stored.Corrections.ContentBindings[field.Field]
			valid = valid && slices.Contains(confirmation.Fields, field.Field) && hasPrevious && hasCurrent && previous == current
		}
	} else if valid {
		for _, field := range confirmation.Fields {
			binding, exists := stored.Corrections.ContentBindings[field]
			valid = valid && field.IsContentBound() && exists && binding == confirmation.CurrentBinding &&
				!slices.Contains(stored.Corrections.StaleContentFields, field)
		}
	}
	if valid {
		return confirmation.CurrentBinding, true, nil
	}
	if confirming {
		return api.ContentBinding{}, false, &api.CorrectionConflictError{Reason: "confirmation requires the current action and unchanged identity evidence"}
	}
	return api.ContentBinding{}, false, nil
}

func normalizeAcceptedCorrections(stored *api.StoredReleaseCorrectionsV1) error {
	for _, id := range []*int{stored.Identity.TMDBID, stored.Identity.IMDBID, stored.Identity.TVDBID, stored.Identity.TVmazeID, stored.Identity.MALID} {
		if id != nil && *id < 0 {
			return &api.CorrectionConflictError{Reason: "provider IDs must be zero or positive"}
		}
	}
	if year := stored.ReleaseName.ManualYear; year != nil && *year < 0 {
		return &api.CorrectionConflictError{Field: api.CorrectionFieldReleaseNameManualYear, Reason: "year must be zero or positive"}
	}
	if season := stored.ReleaseName.Season; season != nil {
		if _, err := seasonep.ParseSeasonInstruction(*season); err != nil {
			return fmt.Errorf("prepared release: season correction: %w", err)
		}
	}
	if episode := stored.ReleaseName.Episode; episode != nil {
		if _, err := seasonep.ParseEpisodeInstruction(*episode); err != nil {
			return fmt.Errorf("prepared release: episode correction: %w", err)
		}
	}
	if date := stored.ReleaseName.ManualDate; date != nil && strings.TrimSpace(*date) != "" {
		if _, err := time.Parse("2006-01-02", strings.TrimSpace(*date)); err != nil {
			return &api.CorrectionConflictError{Field: api.CorrectionFieldReleaseNameManualDate, Reason: "date must use YYYY-MM-DD"}
		}
	}
	for _, languages := range []**[]string{&stored.Metadata.AudioLanguages, &stored.Metadata.SubtitleLanguages, &stored.Metadata.HardcodedSubtitleLanguages} {
		if *languages != nil {
			*languages = new(languageutil.NormalizeLanguageList(**languages))
		}
	}
	for index := range stored.Metadata.TrackLanguages {
		stored.Metadata.TrackLanguages[index].Languages = languageutil.NormalizeLanguageList(stored.Metadata.TrackLanguages[index].Languages)
	}
	if stored.Metadata.Genres != nil {
		genres := make([]string, 0, len(*stored.Metadata.Genres))
		for _, value := range *stored.Metadata.Genres {
			for part := range strings.SplitSeq(value, ",") {
				part = strings.TrimSpace(part)
				if part != "" && !slices.ContainsFunc(genres, func(existing string) bool { return strings.EqualFold(existing, part) }) {
					genres = append(genres, part)
				}
			}
		}
		stored.Metadata.Genres = &genres
	}
	return nil
}

func correctionValues(instructions api.ReleaseFactInstructions) api.ReleaseCorrectionValues {
	return api.ReleaseCorrectionValues{
		Identity:    instructions.Identity,
		ReleaseName: instructions.ReleaseName,
		Metadata:    instructions.Metadata,
	}
}

func correctionValuesFromStored(stored api.StoredReleaseCorrectionsV1) api.ReleaseCorrectionValues {
	return api.ReleaseCorrectionValues{
		Identity:    stored.Identity,
		ReleaseName: stored.ReleaseName,
		Metadata:    stored.Metadata,
	}
}

func effectiveCorrectionInstructions(instructions api.ReleaseFactInstructions, stored api.StoredReleaseCorrectionsV1) (api.ReleaseFactInstructions, error) {
	stale := make([]api.CorrectionFieldRef, 0, len(stored.StaleContentFields))
	for _, field := range stored.StaleContentFields {
		stale = append(stale, api.CorrectionFieldRef{Field: field})
	}
	values, err := correctionValuesFromStored(stored).WithoutFields(stale)
	if err != nil {
		return api.ReleaseFactInstructions{}, fmt.Errorf("prepared release: omit stale correction values: %w", err)
	}
	instructions.Identity, instructions.ReleaseName, instructions.Metadata = values.Identity, values.ReleaseName, values.Metadata
	instructions.Category = nil
	if err := api.NormalizeReleaseFactInstructionsCategory(&instructions); err != nil {
		return api.ReleaseFactInstructions{}, fmt.Errorf("prepared release: normalize effective category: %w", err)
	}
	return instructions, nil
}

func contentBinding(fingerprint string, identity api.ExternalIdentity) api.ContentBinding {
	return api.ContentBinding{
		SourceFingerprint: fingerprint,
		Category:          identity.Category,
		ProviderIDs: api.ProviderIDSet{
			TMDBID:   identity.TMDBID,
			IMDBID:   identity.IMDBID,
			TVDBID:   identity.TVDBID,
			TVmazeID: identity.TVmazeID,
			MALID:    identity.MALID,
		},
	}
}

func applyCorrectionIdentity(binding *api.ContentBinding, stored api.StoredReleaseCorrectionsV1) {
	if stored.ReleaseName.Category != nil {
		binding.Category = api.CanonicalCategory(*stored.ReleaseName.Category)
	}
	for _, pair := range []struct {
		target *int
		value  *int
	}{
		{&binding.ProviderIDs.TMDBID, stored.Identity.TMDBID}, {&binding.ProviderIDs.IMDBID, stored.Identity.IMDBID},
		{&binding.ProviderIDs.TVDBID, stored.Identity.TVDBID}, {&binding.ProviderIDs.TVmazeID, stored.Identity.TVmazeID}, {&binding.ProviderIDs.MALID, stored.Identity.MALID},
	} {
		if pair.value != nil {
			*pair.target = *pair.value
		}
	}
}

func bindExplicitCorrections(stored *api.StoredReleaseCorrectionsV1, binding api.ContentBinding, fields []api.CorrectionFieldRef) {
	for _, ref := range fields {
		if !ref.Field.IsContentBound() {
			continue
		}
		if stored.ContentBindings == nil {
			stored.ContentBindings = make(map[api.CorrectionField]api.ContentBinding)
		}
		stored.ContentBindings[ref.Field] = binding
		stored.StaleContentFields = slices.DeleteFunc(stored.StaleContentFields, func(field api.CorrectionField) bool { return field == ref.Field })
	}
}

func markStaleContent(stored *api.StoredReleaseCorrectionsV1, current api.ContentBinding, explicit []api.CorrectionFieldRef) {
	for field, previous := range stored.ContentBindings {
		if slices.Contains(explicit, api.CorrectionFieldRef{Field: field}) || slices.Contains(stored.StaleContentFields, field) {
			continue
		}
		if contentBindingChanged(previous, current) {
			stored.StaleContentFields = append(stored.StaleContentFields, field)
		}
	}
	slices.Sort(stored.StaleContentFields)
}

func contentBindingChanged(previous, current api.ContentBinding) bool {
	if previous.SourceFingerprint != "" && previous.SourceFingerprint != current.SourceFingerprint {
		return true
	}
	if previous.Category != "" && current.Category != "" && previous.Category != current.Category {
		return true
	}
	for _, pair := range [][2]int{
		{previous.ProviderIDs.TMDBID, current.ProviderIDs.TMDBID}, {previous.ProviderIDs.IMDBID, current.ProviderIDs.IMDBID},
		{previous.ProviderIDs.TVDBID, current.ProviderIDs.TVDBID}, {previous.ProviderIDs.TVmazeID, current.ProviderIDs.TVmazeID}, {previous.ProviderIDs.MALID, current.ProviderIDs.MALID},
	} {
		if pair[0] > 0 && pair[0] != pair[1] {
			return true
		}
	}
	return false
}

func validateTrackCorrections(corrections []api.TrackLanguageCorrection, current api.PreparedRelease, fingerprint string) error {
	for _, correction := range corrections {
		index := slices.IndexFunc(current.Media.Tracks, func(track api.MediaTrackFacts) bool { return track.ID == correction.TrackID })
		if current.Compatibility.SourceFingerprint != fingerprint || index < 0 ||
			current.Media.Tracks[index].ManifestFingerprint != correction.ManifestFingerprint {
			return &api.CorrectionConflictError{
				Field:   api.CorrectionFieldMetadataTrackLanguages,
				TrackID: correction.TrackID,
				Reason:  "track manifest changed; select a current inspected track",
			}
		}
	}
	return nil
}

func (m *Module) finalizeCorrectionBindings(
	ctx context.Context,
	resolved api.ResolvedPreparationInput,
	identity api.ExternalIdentity,
) (api.ReleaseCorrectionsSnapshot, error) {
	final, err := api.ApplyReleaseCorrectionUpdate(resolved.Corrections, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
	if err != nil {
		return api.ReleaseCorrectionsSnapshot{}, fmt.Errorf("prepared release: finalize correction update: %w", err)
	}
	binding := contentBinding(resolved.SourceFingerprint, identity)
	before := slices.Clone(final.StaleContentFields)
	markStaleContent(&final, binding, resolved.ExplicitFields)
	stored := resolved.Corrections
	if !slices.Equal(before, final.StaleContentFields) {
		stored, err = m.store.CompareAndSwapReleaseCorrections(ctx, resolved.Input.SourcePath, resolved.Corrections.Revision, final)
		if err != nil {
			return api.ReleaseCorrectionsSnapshot{}, fmt.Errorf("prepared release: mark stale corrections: %w", err)
		}
	}
	if len(final.StaleContentFields) > 0 {
		return api.ReleaseCorrectionsSnapshot{}, &api.StaleContentCorrectionsError{Corrections: stored, CurrentBinding: binding}
	}
	bindExplicitCorrections(&final, binding, resolved.ExplicitFields)
	// Legacy name-only rows acquire evidence after their first accepted preparation.
	for _, field := range correctionValuesFromStored(final).Fields() {
		if field.Field.IsContentBound() {
			if _, exists := final.ContentBindings[field.Field]; !exists {
				bindExplicitCorrections(&final, binding, []api.CorrectionFieldRef{field})
			}
		}
	}
	revision := resolved.Corrections.Revision
	if !reflect.DeepEqual(final, resolved.Corrections.Corrections) {
		revision++
	}
	return api.ReleaseCorrectionsSnapshot{Corrections: final, Revision: revision}, nil
}
