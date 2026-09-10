// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

var (
	// ErrCorrectionConflict identifies invalid or incompatible correction changes.
	ErrCorrectionConflict = errors.New("release correction conflict")
	// ErrUnsupportedCorrectionVersion identifies a persisted correction payload
	// that this binary must leave untouched.
	ErrUnsupportedCorrectionVersion = errors.New("unsupported release correction version")
)

// CorrectionConflictError describes a rejected correction target or revision.
type CorrectionConflictError struct {
	Field   CorrectionField
	TrackID string
	Reason  string
}

func (e *CorrectionConflictError) Error() string {
	if e.TrackID != "" {
		return fmt.Sprintf("release correction conflict for %s track %q: %s", e.Field, e.TrackID, e.Reason)
	}
	if e.Field != "" {
		return fmt.Sprintf("release correction conflict for %s: %s", e.Field, e.Reason)
	}
	return "release correction conflict: " + e.Reason
}

func (e *CorrectionConflictError) Unwrap() error { return ErrCorrectionConflict }

// UnsupportedCorrectionVersionError reports a payload that this binary cannot mutate.
type UnsupportedCorrectionVersionError struct{ Version uint }

func (e *UnsupportedCorrectionVersionError) Error() string {
	return fmt.Sprintf("unsupported release correction version %d", e.Version)
}

func (e *UnsupportedCorrectionVersionError) Unwrap() error { return ErrUnsupportedCorrectionVersion }

// CorrectionRevisionConflictError reports an update based on an older correction record.
type CorrectionRevisionConflictError struct {
	Expected uint64
	Actual   uint64
}

func (e *CorrectionRevisionConflictError) Error() string {
	return fmt.Sprintf("release correction revision %d does not match current revision %d", e.Expected, e.Actual)
}

func (e *CorrectionRevisionConflictError) Unwrap() error { return ErrCorrectionConflict }

// CorrectionField identifies one closed, resettable correction target.
type CorrectionField string

const (
	CorrectionFieldIdentityTMDB   CorrectionField = "identity.tmdb"
	CorrectionFieldIdentityIMDB   CorrectionField = "identity.imdb"
	CorrectionFieldIdentityTVDB   CorrectionField = "identity.tvdb"
	CorrectionFieldIdentityTVmaze CorrectionField = "identity.tvmaze"
	CorrectionFieldIdentityMAL    CorrectionField = "identity.mal"

	CorrectionFieldReleaseNameCategory         CorrectionField = "release_name.category"
	CorrectionFieldReleaseNameType             CorrectionField = "release_name.type"
	CorrectionFieldReleaseNameSource           CorrectionField = "release_name.source"
	CorrectionFieldReleaseNameResolution       CorrectionField = "release_name.resolution"
	CorrectionFieldReleaseNameTag              CorrectionField = "release_name.tag"
	CorrectionFieldReleaseNameService          CorrectionField = "release_name.service"
	CorrectionFieldReleaseNameEdition          CorrectionField = "release_name.edition"
	CorrectionFieldReleaseNameSeason           CorrectionField = "release_name.season"
	CorrectionFieldReleaseNameEpisode          CorrectionField = "release_name.episode"
	CorrectionFieldReleaseNameEpisodeTitle     CorrectionField = "release_name.episode_title"
	CorrectionFieldReleaseNameManualYear       CorrectionField = "release_name.manual_year"
	CorrectionFieldReleaseNameManualDate       CorrectionField = "release_name.manual_date"
	CorrectionFieldReleaseNameUseSeasonEpisode CorrectionField = "release_name.use_season_episode"
	CorrectionFieldReleaseNameNoSeason         CorrectionField = "release_name.no_season"
	CorrectionFieldReleaseNameNoYear           CorrectionField = "release_name.no_year"
	CorrectionFieldReleaseNameNoAKA            CorrectionField = "release_name.no_aka"
	CorrectionFieldReleaseNameNoTag            CorrectionField = "release_name.no_tag"
	CorrectionFieldReleaseNameNoEpisodeTitle   CorrectionField = "release_name.no_episode_title"
	CorrectionFieldReleaseNameNoDistributor    CorrectionField = "release_name.no_distributor"
	CorrectionFieldReleaseNameNoEdition        CorrectionField = "release_name.no_edition"
	CorrectionFieldReleaseNameNoDub            CorrectionField = "release_name.no_dub"
	CorrectionFieldReleaseNameNoDual           CorrectionField = "release_name.no_dual"
	CorrectionFieldReleaseNameDualAudio        CorrectionField = "release_name.dual_audio"
	CorrectionFieldReleaseNameRegion           CorrectionField = "release_name.region"

	CorrectionFieldMetadataDistributor                CorrectionField = "metadata.distributor"
	CorrectionFieldMetadataOriginalLanguage           CorrectionField = "metadata.original_language"
	CorrectionFieldMetadataPersonalRelease            CorrectionField = "metadata.personal_release"
	CorrectionFieldMetadataCommentary                 CorrectionField = "metadata.commentary"
	CorrectionFieldMetadataWebDV                      CorrectionField = "metadata.web_dv"
	CorrectionFieldMetadataStreamOptimized            CorrectionField = "metadata.stream_optimized"
	CorrectionFieldMetadataAnime                      CorrectionField = "metadata.anime"
	CorrectionFieldMetadataTitle                      CorrectionField = "metadata.title"
	CorrectionFieldMetadataAlternateTitle             CorrectionField = "metadata.alternate_title"
	CorrectionFieldMetadataOriginalTitle              CorrectionField = "metadata.original_title"
	CorrectionFieldMetadataGenres                     CorrectionField = "metadata.genres"
	CorrectionFieldMetadataAudioLanguages             CorrectionField = "metadata.audio_languages"
	CorrectionFieldMetadataSubtitleLanguages          CorrectionField = "metadata.subtitle_languages"
	CorrectionFieldMetadataHardcodedSubs              CorrectionField = "metadata.hardcoded_subs"
	CorrectionFieldMetadataHardcodedSubtitleLanguages CorrectionField = "metadata.hardcoded_subtitle_languages"
	CorrectionFieldMetadataTrackLanguages             CorrectionField = "metadata.track_languages"
)

// CorrectionFieldRef identifies one scalar field or one exact track correction.
type CorrectionFieldRef struct {
	Field   CorrectionField `json:"field"`
	TrackID string          `json:"trackId,omitempty"`
}

// ReleaseCorrectionValues carries the only fact-producing correction fields.
type ReleaseCorrectionValues struct {
	Identity    ExternalIDOverrides
	ReleaseName ReleaseNameOverrides
	Metadata    MetadataOverrides
}

// ReleaseCorrectionPatch merges explicit fields into saved correction intent.
// Nil pointers omit a field, while pointers to false, zero, empty strings, or
// empty slices are explicit values.
// ResetFields removes saved intent so preparation can derive the value again.
// ConfirmFields retains a content-bound value for the newly resolved identity
// and requires ExpectedRevision plus the current workflow confirmation action.
// A supplied revision also guards ordinary edits.
type ReleaseCorrectionPatch struct {
	Values           ReleaseCorrectionValues `json:"values"`
	ResetFields      []CorrectionFieldRef    `json:"resetFields,omitempty"`
	ConfirmFields    []CorrectionFieldRef    `json:"confirmFields,omitempty"`
	ExpectedRevision *uint64                 `json:"expectedRevision,omitempty"`
}

// ContentBinding records the source and provider identity that accepted a
// content-derived correction. Provider IDs are resolved values, never manual intent.
type ContentBinding struct {
	SourceFingerprint string            `json:"sourceFingerprint"`
	Category          CanonicalCategory `json:"category"`
	ProviderIDs       ProviderIDSet     `json:"providerIds"`
}

// ProviderIDSet is the resolved provider identity subset required to bind content corrections.
type ProviderIDSet struct {
	TMDBID   int `json:"tmdbId"`
	IMDBID   int `json:"imdbId"`
	TVDBID   int `json:"tvdbId"`
	TVmazeID int `json:"tvmazeId"`
	MALID    int `json:"malId"`
}

// StoredReleaseCorrectionsV1 is the persisted v1 correction payload. Its
// revision belongs to the containing row and is exposed by ReleaseCorrectionsSnapshot.
type StoredReleaseCorrectionsV1 struct {
	Version            uint                               `json:"version"`
	Identity           ExternalIDOverrides                `json:"identity"`
	ReleaseName        ReleaseNameOverrides               `json:"releaseName"`
	Metadata           MetadataOverrides                  `json:"metadata"`
	SourceFingerprint  string                             `json:"sourceFingerprint,omitempty"`
	ContentBindings    map[CorrectionField]ContentBinding `json:"contentBindings,omitempty"`
	StaleContentFields []CorrectionField                  `json:"staleContentFields,omitempty"`
	// IdentityResetFields keeps Auto authoritative over legacy persisted identity pins.
	IdentityResetFields []CorrectionField `json:"identityResetFields,omitempty"`
}

// ReleaseCorrectionsSnapshot combines one persisted correction payload and its row revision.
type ReleaseCorrectionsSnapshot struct {
	Corrections StoredReleaseCorrectionsV1 `json:"corrections"`
	Revision    uint64                     `json:"revision"`
}

// Clone returns a detached correction read model.
func (s ReleaseCorrectionsSnapshot) Clone() (ReleaseCorrectionsSnapshot, error) {
	return cloneWorkflowValue(s)
}

// ReleaseCorrectionUpdateMode makes persistence intent explicit.
type ReleaseCorrectionUpdateMode string

const (
	ReleaseCorrectionUpdateInherit  ReleaseCorrectionUpdateMode = "inherit"
	ReleaseCorrectionUpdatePatch    ReleaseCorrectionUpdateMode = "patch"
	ReleaseCorrectionUpdateReplace  ReleaseCorrectionUpdateMode = "replace"
	ReleaseCorrectionUpdateResetAll ReleaseCorrectionUpdateMode = "reset_all"
)

// ReleaseCorrectionUpdate is the internal persistence operation selected by an adapter.
type ReleaseCorrectionUpdate struct {
	Mode   ReleaseCorrectionUpdateMode
	Values ReleaseCorrectionValues
	Patch  *ReleaseCorrectionPatch
	// Confirmation carries workflow-validated identity evidence across acceptance
	// and preparation. It is internal authority, never a transport input.
	Confirmation *CorrectionConfirmation
}

// UnmarshalJSON rejects unknown fields throughout this new patch contract.
func (p *ReleaseCorrectionPatch) UnmarshalJSON(payload []byte) error {
	if p == nil {
		return errors.New("nil release correction patch")
	}
	if !bytes.HasPrefix(bytes.TrimSpace(payload), []byte("{")) {
		return errors.New("release correction patch must be an object")
	}
	type patch ReleaseCorrectionPatch
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var decoded patch
	if err := decoder.Decode(&decoded); err != nil {
		return fmt.Errorf("decode release correction patch: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("release correction patch contains trailing JSON")
	}
	*p = ReleaseCorrectionPatch(decoded)
	return nil
}

// Validate verifies closed correction targets and mutually exclusive operations.
func (p ReleaseCorrectionPatch) Validate() error {
	if err := normalizeReleaseCorrectionValues(&p.Values); err != nil {
		return err
	}
	setFields, err := correctionValueRefs(p.Values)
	if err != nil {
		return err
	}
	resetFields, err := validateCorrectionFieldRefs(p.ResetFields, false)
	if err != nil {
		return err
	}
	confirmFields, err := validateCorrectionFieldRefs(p.ConfirmFields, true)
	if err != nil {
		return err
	}
	for key, field := range setFields {
		if _, ok := resetFields[key]; ok {
			return correctionConflict(field, "value and reset cannot target the same field")
		}
		if _, ok := confirmFields[key]; ok {
			return correctionConflict(field, "value and confirmation cannot target the same field")
		}
	}
	for key, field := range resetFields {
		if _, ok := confirmFields[key]; ok {
			return correctionConflict(field, "reset and confirmation cannot target the same field")
		}
	}
	if len(confirmFields) > 0 && p.ExpectedRevision == nil {
		return &CorrectionConflictError{Reason: "confirmation requires an expected revision"}
	}
	return nil
}

// ApplyReleaseCorrectionUpdate performs deterministic merge and reset logic.
// Persistence owns compare-and-swap, revision increments, and content freshness.
func ApplyReleaseCorrectionUpdate(snapshot ReleaseCorrectionsSnapshot, update ReleaseCorrectionUpdate) (StoredReleaseCorrectionsV1, error) {
	stored := cloneStoredReleaseCorrections(snapshot.Corrections)
	if stored.Version != 0 && stored.Version != 1 {
		return StoredReleaseCorrectionsV1{}, &UnsupportedCorrectionVersionError{Version: stored.Version}
	}
	if err := normalizeIdentityResetFields(&stored); err != nil {
		return StoredReleaseCorrectionsV1{}, err
	}
	switch update.Mode {
	case ReleaseCorrectionUpdateInherit:
		if update.Patch != nil || !releaseCorrectionValuesZero(update.Values) {
			return StoredReleaseCorrectionsV1{}, &CorrectionConflictError{Reason: "inherit update cannot contain correction values"}
		}
		return stored, nil
	case ReleaseCorrectionUpdatePatch:
		if update.Patch == nil {
			return StoredReleaseCorrectionsV1{}, &CorrectionConflictError{Reason: "patch update requires a patch"}
		}
		patch := cloneReleaseCorrectionPatch(*update.Patch)
		if err := normalizeReleaseCorrectionValues(&patch.Values); err != nil {
			return StoredReleaseCorrectionsV1{}, err
		}
		if err := patch.Validate(); err != nil {
			return StoredReleaseCorrectionsV1{}, err
		}
		if patch.ExpectedRevision != nil && *patch.ExpectedRevision != snapshot.Revision {
			return StoredReleaseCorrectionsV1{}, &CorrectionRevisionConflictError{Expected: *patch.ExpectedRevision, Actual: snapshot.Revision}
		}
		stored.Version = 1
		applyReleaseCorrectionValues(&stored, patch.Values)
		for _, ref := range patch.ResetFields {
			resetStoredCorrectionField(&stored, ref)
		}
		return stored, nil
	case ReleaseCorrectionUpdateReplace:
		if update.Patch != nil {
			return StoredReleaseCorrectionsV1{}, &CorrectionConflictError{Reason: "replace update cannot contain a patch"}
		}
		values := cloneReleaseCorrectionValues(update.Values)
		if err := normalizeReleaseCorrectionValues(&values); err != nil {
			return StoredReleaseCorrectionsV1{}, err
		}
		if _, err := correctionValueRefs(values); err != nil {
			return StoredReleaseCorrectionsV1{}, err
		}
		stored = StoredReleaseCorrectionsV1{Version: 1, IdentityResetFields: slices.Sorted(slices.Values(identityCorrectionFields))}
		applyReleaseCorrectionValues(&stored, values)
		return stored, nil
	case ReleaseCorrectionUpdateResetAll:
		if update.Patch != nil || !releaseCorrectionValuesZero(update.Values) {
			return StoredReleaseCorrectionsV1{}, &CorrectionConflictError{Reason: "reset-all update cannot contain correction values"}
		}
		return StoredReleaseCorrectionsV1{Version: 1, IdentityResetFields: slices.Sorted(slices.Values(identityCorrectionFields))}, nil
	default:
		return StoredReleaseCorrectionsV1{}, &CorrectionConflictError{Reason: "unsupported correction update mode"}
	}
}

// NormalizeReleaseFactInstructionsCategory normalizes both legacy category representations.
func NormalizeReleaseFactInstructionsCategory(instructions *ReleaseFactInstructions) error {
	if instructions == nil {
		return errors.New("release fact instructions are required")
	}
	var (
		releaseNameCategory *CanonicalCategory
		category            *CanonicalCategory
	)
	if instructions.ReleaseName.Category != nil {
		normalized, err := NormalizeCanonicalCategory(*instructions.ReleaseName.Category)
		if err != nil {
			return fmt.Errorf("release name category: %w", err)
		}
		releaseNameCategory = &normalized
	}
	if instructions.Category != nil {
		normalized, err := NormalizeCanonicalCategory(string(*instructions.Category))
		if err != nil {
			return fmt.Errorf("canonical category: %w", err)
		}
		category = &normalized
	}
	if releaseNameCategory != nil && category != nil && *releaseNameCategory != *category {
		return &CorrectionConflictError{Field: CorrectionFieldReleaseNameCategory, Reason: "release-name and canonical categories conflict"}
	}
	if releaseNameCategory == nil && category == nil {
		return nil
	}
	var normalized CanonicalCategory
	if releaseNameCategory != nil {
		normalized = *releaseNameCategory
	} else {
		normalized = *category
	}
	instructions.Category = new(normalized)
	instructions.ReleaseName.Category = new(string(normalized))
	return nil
}

func normalizeReleaseCorrectionValues(values *ReleaseCorrectionValues) error {
	if values == nil || values.ReleaseName.Category == nil {
		return nil
	}
	normalized, err := NormalizeCanonicalCategory(*values.ReleaseName.Category)
	if err != nil {
		return fmt.Errorf("release correction category: %w", err)
	}
	values.ReleaseName.Category = new(string(normalized))
	return nil
}

func correctionValueRefs(values ReleaseCorrectionValues) (map[string]CorrectionFieldRef, error) {
	refs := make(map[string]CorrectionFieldRef)
	add := func(field CorrectionField, present bool, trackID string) error {
		if !present {
			return nil
		}
		ref := CorrectionFieldRef{Field: field, TrackID: trackID}
		key, err := correctionFieldRefKey(ref, false)
		if err != nil {
			return err
		}
		if _, exists := refs[key]; exists {
			return correctionConflict(ref, "duplicate correction target")
		}
		refs[key] = ref
		return nil
	}
	for _, item := range []struct {
		field   CorrectionField
		present bool
	}{
		{CorrectionFieldIdentityTMDB, values.Identity.TMDBID != nil}, {CorrectionFieldIdentityIMDB, values.Identity.IMDBID != nil},
		{CorrectionFieldIdentityTVDB, values.Identity.TVDBID != nil}, {CorrectionFieldIdentityTVmaze, values.Identity.TVmazeID != nil},
		{CorrectionFieldIdentityMAL, values.Identity.MALID != nil}, {CorrectionFieldReleaseNameCategory, values.ReleaseName.Category != nil},
		{CorrectionFieldReleaseNameType, values.ReleaseName.Type != nil}, {CorrectionFieldReleaseNameSource, values.ReleaseName.Source != nil},
		{CorrectionFieldReleaseNameResolution, values.ReleaseName.Resolution != nil}, {CorrectionFieldReleaseNameTag, values.ReleaseName.Tag != nil},
		{CorrectionFieldReleaseNameService, values.ReleaseName.Service != nil}, {CorrectionFieldReleaseNameEdition, values.ReleaseName.Edition != nil},
		{CorrectionFieldReleaseNameSeason, values.ReleaseName.Season != nil}, {CorrectionFieldReleaseNameEpisode, values.ReleaseName.Episode != nil},
		{CorrectionFieldReleaseNameEpisodeTitle, values.ReleaseName.EpisodeTitle != nil}, {CorrectionFieldReleaseNameManualYear, values.ReleaseName.ManualYear != nil},
		{CorrectionFieldReleaseNameManualDate, values.ReleaseName.ManualDate != nil}, {CorrectionFieldReleaseNameUseSeasonEpisode, values.ReleaseName.UseSeasonEpisode != nil},
		{CorrectionFieldReleaseNameNoSeason, values.ReleaseName.NoSeason != nil}, {CorrectionFieldReleaseNameNoYear, values.ReleaseName.NoYear != nil},
		{CorrectionFieldReleaseNameNoAKA, values.ReleaseName.NoAKA != nil}, {CorrectionFieldReleaseNameNoTag, values.ReleaseName.NoTag != nil},
		{CorrectionFieldReleaseNameNoEpisodeTitle, values.ReleaseName.NoEpisodeTitle != nil}, {CorrectionFieldReleaseNameNoDistributor, values.ReleaseName.NoDistributor != nil},
		{CorrectionFieldReleaseNameNoEdition, values.ReleaseName.NoEdition != nil}, {CorrectionFieldReleaseNameNoDub, values.ReleaseName.NoDub != nil},
		{CorrectionFieldReleaseNameNoDual, values.ReleaseName.NoDual != nil}, {CorrectionFieldReleaseNameDualAudio, values.ReleaseName.DualAudio != nil},
		{CorrectionFieldReleaseNameRegion, values.ReleaseName.Region != nil}, {CorrectionFieldMetadataDistributor, values.Metadata.Distributor != nil},
		{CorrectionFieldMetadataOriginalLanguage, values.Metadata.OriginalLanguage != nil}, {CorrectionFieldMetadataPersonalRelease, values.Metadata.PersonalRelease != nil},
		{CorrectionFieldMetadataCommentary, values.Metadata.Commentary != nil}, {CorrectionFieldMetadataWebDV, values.Metadata.WebDV != nil},
		{CorrectionFieldMetadataStreamOptimized, values.Metadata.StreamOptimized != nil}, {CorrectionFieldMetadataAnime, values.Metadata.Anime != nil},
		{CorrectionFieldMetadataTitle, values.Metadata.Title != nil}, {CorrectionFieldMetadataAlternateTitle, values.Metadata.AlternateTitle != nil},
		{CorrectionFieldMetadataOriginalTitle, values.Metadata.OriginalTitle != nil}, {CorrectionFieldMetadataGenres, values.Metadata.Genres != nil},
		{CorrectionFieldMetadataAudioLanguages, values.Metadata.AudioLanguages != nil}, {CorrectionFieldMetadataSubtitleLanguages, values.Metadata.SubtitleLanguages != nil},
		{CorrectionFieldMetadataHardcodedSubs, values.Metadata.HardcodedSubs != nil}, {CorrectionFieldMetadataHardcodedSubtitleLanguages, values.Metadata.HardcodedSubtitleLanguages != nil},
	} {
		if err := add(item.field, item.present, ""); err != nil {
			return nil, err
		}
	}
	if values.Metadata.TrackLanguages != nil {
		for _, correction := range values.Metadata.TrackLanguages {
			if strings.TrimSpace(correction.ManifestFingerprint) == "" {
				return nil, correctionConflict(
					CorrectionFieldRef{Field: CorrectionFieldMetadataTrackLanguages, TrackID: correction.TrackID},
					"track correction requires a manifest fingerprint",
				)
			}
			if err := add(CorrectionFieldMetadataTrackLanguages, true, correction.TrackID); err != nil {
				return nil, err
			}
		}
	}
	return refs, nil
}

func validateCorrectionFieldRefs(refs []CorrectionFieldRef, confirm bool) (map[string]CorrectionFieldRef, error) {
	validated := make(map[string]CorrectionFieldRef, len(refs))
	for _, ref := range refs {
		key, err := correctionFieldRefKey(ref, confirm)
		if err != nil {
			return nil, err
		}
		if _, exists := validated[key]; exists {
			return nil, correctionConflict(ref, "duplicate correction target")
		}
		validated[key] = ref
	}
	return validated, nil
}

func correctionFieldRefKey(ref CorrectionFieldRef, confirm bool) (string, error) {
	if !isCorrectionField(ref.Field) {
		return "", correctionConflict(ref, "unknown correction field")
	}
	if ref.Field == CorrectionFieldMetadataTrackLanguages {
		if confirm {
			return "", correctionConflict(ref, "track language corrections cannot be confirmed")
		}
		if strings.TrimSpace(ref.TrackID) == "" {
			return "", correctionConflict(ref, "track language correction requires a track ID")
		}
		return string(ref.Field) + ":" + ref.TrackID, nil
	}
	if strings.TrimSpace(ref.TrackID) != "" {
		return "", correctionConflict(ref, "track ID is only valid for track languages")
	}
	if confirm && !ref.Field.IsContentBound() {
		return "", correctionConflict(ref, "field cannot be confirmed")
	}
	return string(ref.Field), nil
}

func correctionConflict(ref CorrectionFieldRef, reason string) error {
	return &CorrectionConflictError{
		Field:   ref.Field,
		TrackID: ref.TrackID,
		Reason:  reason,
	}
}

func isCorrectionField(field CorrectionField) bool {
	_, ok := correctionFields[field]
	return ok
}

var identityCorrectionFields = []CorrectionField{
	CorrectionFieldIdentityTMDB, CorrectionFieldIdentityIMDB, CorrectionFieldIdentityTVDB,
	CorrectionFieldIdentityTVmaze, CorrectionFieldIdentityMAL, CorrectionFieldReleaseNameCategory,
}

func normalizeIdentityResetFields(stored *StoredReleaseCorrectionsV1) error {
	values := ReleaseCorrectionValues{Identity: stored.Identity, ReleaseName: stored.ReleaseName}
	for _, field := range stored.IdentityResetFields {
		if !slices.Contains(identityCorrectionFields, field) {
			return correctionConflict(CorrectionFieldRef{Field: field}, "invalid identity reset field")
		}
		if slices.Contains(values.Fields(), CorrectionFieldRef{Field: field}) {
			return correctionConflict(CorrectionFieldRef{Field: field}, "identity reset conflicts with a stored value")
		}
	}
	slices.Sort(stored.IdentityResetFields)
	stored.IdentityResetFields = slices.Compact(stored.IdentityResetFields)
	return nil
}

// WithoutResetPins removes prior explicit pins for fields returned to Auto.
// Automatic provider and legacy evidence, and unrelated explicit pins, remain available.
func (identity ExternalIdentity) WithoutResetPins(fields []CorrectionField) ExternalIdentity {
	for _, provider := range []struct {
		field      CorrectionField
		id         *int
		provenance *IdentityProvenance
		override   *OverrideState
	}{
		{CorrectionFieldIdentityTMDB, &identity.TMDBID, &identity.Provenance.TMDB, &identity.Overrides.TMDB},
		{CorrectionFieldIdentityIMDB, &identity.IMDBID, &identity.Provenance.IMDB, &identity.Overrides.IMDB},
		{CorrectionFieldIdentityTVDB, &identity.TVDBID, &identity.Provenance.TVDB, &identity.Overrides.TVDB},
		{CorrectionFieldIdentityTVmaze, &identity.TVmazeID, &identity.Provenance.TVmaze, &identity.Overrides.TVmaze},
		{CorrectionFieldIdentityMAL, &identity.MALID, &identity.Provenance.MAL, &identity.Overrides.MAL},
	} {
		if slices.Contains(fields, provider.field) &&
			(*provider.provenance == IdentityProvenanceExplicit || *provider.override == OverrideStateValue || *provider.override == OverrideStateClear) {
			*provider.id, *provider.provenance, *provider.override = 0, IdentityProvenanceUnknown, OverrideStateUnset
		}
	}
	if slices.Contains(fields, CorrectionFieldReleaseNameCategory) &&
		(identity.Provenance.Category == IdentityProvenanceExplicit || identity.Overrides.Category == OverrideStateValue || identity.Overrides.Category == OverrideStateClear) {
		identity.Category, identity.Provenance.Category, identity.Overrides.Category = CanonicalCategoryUnknown, IdentityProvenanceUnknown, OverrideStateUnset
	}
	return identity
}

var correctionFields = func() map[CorrectionField]struct{} {
	fields := map[CorrectionField]struct{}{}
	for _, field := range allCorrectionFields {
		fields[field] = struct{}{}
	}
	return fields
}()

var allCorrectionFields = []CorrectionField{
	CorrectionFieldIdentityTMDB, CorrectionFieldIdentityIMDB, CorrectionFieldIdentityTVDB, CorrectionFieldIdentityTVmaze, CorrectionFieldIdentityMAL,
	CorrectionFieldReleaseNameCategory, CorrectionFieldReleaseNameType, CorrectionFieldReleaseNameSource, CorrectionFieldReleaseNameResolution,
	CorrectionFieldReleaseNameTag, CorrectionFieldReleaseNameService, CorrectionFieldReleaseNameEdition, CorrectionFieldReleaseNameSeason,
	CorrectionFieldReleaseNameEpisode, CorrectionFieldReleaseNameEpisodeTitle, CorrectionFieldReleaseNameManualYear, CorrectionFieldReleaseNameManualDate,
	CorrectionFieldReleaseNameUseSeasonEpisode, CorrectionFieldReleaseNameNoSeason, CorrectionFieldReleaseNameNoYear, CorrectionFieldReleaseNameNoAKA,
	CorrectionFieldReleaseNameNoTag, CorrectionFieldReleaseNameNoEpisodeTitle, CorrectionFieldReleaseNameNoDistributor, CorrectionFieldReleaseNameNoEdition,
	CorrectionFieldReleaseNameNoDub, CorrectionFieldReleaseNameNoDual, CorrectionFieldReleaseNameDualAudio, CorrectionFieldReleaseNameRegion,
	CorrectionFieldMetadataDistributor, CorrectionFieldMetadataOriginalLanguage, CorrectionFieldMetadataPersonalRelease, CorrectionFieldMetadataCommentary,
	CorrectionFieldMetadataWebDV, CorrectionFieldMetadataStreamOptimized, CorrectionFieldMetadataAnime, CorrectionFieldMetadataTitle,
	CorrectionFieldMetadataAlternateTitle, CorrectionFieldMetadataOriginalTitle, CorrectionFieldMetadataGenres, CorrectionFieldMetadataAudioLanguages,
	CorrectionFieldMetadataSubtitleLanguages, CorrectionFieldMetadataHardcodedSubs, CorrectionFieldMetadataHardcodedSubtitleLanguages,
	CorrectionFieldMetadataTrackLanguages,
}

// IsContentBound identifies corrections that require confirmation when content identity changes.
func (field CorrectionField) IsContentBound() bool {
	return slices.Contains([]CorrectionField{
		CorrectionFieldMetadataTitle, CorrectionFieldMetadataAlternateTitle, CorrectionFieldMetadataOriginalTitle,
		CorrectionFieldMetadataGenres, CorrectionFieldMetadataOriginalLanguage, CorrectionFieldReleaseNameManualYear,
		CorrectionFieldReleaseNameManualDate, CorrectionFieldReleaseNameSeason, CorrectionFieldReleaseNameEpisode,
		CorrectionFieldReleaseNameEpisodeTitle,
	}, field)
}

func applyReleaseCorrectionValues(stored *StoredReleaseCorrectionsV1, values ReleaseCorrectionValues) {
	applyExternalIDOverrides(&stored.Identity, values.Identity)
	applyReleaseNameOverrides(&stored.ReleaseName, values.ReleaseName)
	applyMetadataOverrides(&stored.Metadata, values.Metadata)
	for _, field := range values.Fields() {
		stored.IdentityResetFields = slices.DeleteFunc(stored.IdentityResetFields, func(reset CorrectionField) bool { return reset == field.Field })
	}
}

func applyExternalIDOverrides(destination *ExternalIDOverrides, source ExternalIDOverrides) {
	if source.TMDBID != nil {
		destination.TMDBID = cloneInt(source.TMDBID)
	}
	if source.IMDBID != nil {
		destination.IMDBID = cloneInt(source.IMDBID)
	}
	if source.TVDBID != nil {
		destination.TVDBID = cloneInt(source.TVDBID)
	}
	if source.TVmazeID != nil {
		destination.TVmazeID = cloneInt(source.TVmazeID)
	}
	if source.MALID != nil {
		destination.MALID = cloneInt(source.MALID)
	}
}

func applyReleaseNameOverrides(destination *ReleaseNameOverrides, source ReleaseNameOverrides) {
	for _, item := range []struct {
		destination **string
		source      *string
	}{
		{&destination.Category, source.Category}, {&destination.Type, source.Type}, {&destination.Source, source.Source}, {&destination.Resolution, source.Resolution},
		{&destination.Tag, source.Tag}, {&destination.Service, source.Service}, {&destination.Edition, source.Edition}, {&destination.Season, source.Season},
		{&destination.Episode, source.Episode}, {&destination.EpisodeTitle, source.EpisodeTitle}, {&destination.ManualDate, source.ManualDate}, {&destination.Region, source.Region},
	} {
		if item.source != nil {
			*item.destination = cloneString(item.source)
		}
	}
	if source.ManualYear != nil {
		destination.ManualYear = cloneInt(source.ManualYear)
	}
	for _, item := range []struct {
		destination **bool
		source      *bool
	}{
		{&destination.UseSeasonEpisode, source.UseSeasonEpisode}, {&destination.NoSeason, source.NoSeason}, {&destination.NoYear, source.NoYear}, {&destination.NoAKA, source.NoAKA},
		{&destination.NoTag, source.NoTag}, {&destination.NoEpisodeTitle, source.NoEpisodeTitle}, {&destination.NoDistributor, source.NoDistributor},
		{&destination.NoEdition, source.NoEdition}, {&destination.NoDub, source.NoDub}, {&destination.NoDual, source.NoDual}, {&destination.DualAudio, source.DualAudio},
	} {
		if item.source != nil {
			*item.destination = cloneBool(item.source)
		}
	}
}

func applyMetadataOverrides(destination *MetadataOverrides, source MetadataOverrides) {
	for _, item := range []struct {
		destination **string
		source      *string
	}{
		{&destination.Distributor, source.Distributor}, {&destination.OriginalLanguage, source.OriginalLanguage}, {&destination.Title, source.Title},
		{&destination.AlternateTitle, source.AlternateTitle}, {&destination.OriginalTitle, source.OriginalTitle},
	} {
		if item.source != nil {
			*item.destination = cloneString(item.source)
		}
	}
	for _, item := range []struct {
		destination **bool
		source      *bool
	}{
		{&destination.PersonalRelease, source.PersonalRelease}, {&destination.Commentary, source.Commentary}, {&destination.WebDV, source.WebDV},
		{&destination.StreamOptimized, source.StreamOptimized}, {&destination.Anime, source.Anime}, {&destination.HardcodedSubs, source.HardcodedSubs},
	} {
		if item.source != nil {
			*item.destination = cloneBool(item.source)
		}
	}
	for _, item := range []struct {
		destination **[]string
		source      *[]string
	}{
		{&destination.Genres, source.Genres}, {&destination.AudioLanguages, source.AudioLanguages}, {&destination.SubtitleLanguages, source.SubtitleLanguages},
		{&destination.HardcodedSubtitleLanguages, source.HardcodedSubtitleLanguages},
	} {
		if item.source != nil {
			*item.destination = cloneStringSlice(item.source)
		}
	}
	if source.TrackLanguages != nil {
		for _, correction := range source.TrackLanguages {
			index := slices.IndexFunc(destination.TrackLanguages, func(value TrackLanguageCorrection) bool { return value.TrackID == correction.TrackID })
			if index < 0 {
				destination.TrackLanguages = append(destination.TrackLanguages, cloneTrackLanguageCorrections([]TrackLanguageCorrection{correction})[0])
				continue
			}
			destination.TrackLanguages[index] = cloneTrackLanguageCorrections([]TrackLanguageCorrection{correction})[0]
		}
	}
}

func resetStoredCorrectionField(stored *StoredReleaseCorrectionsV1, ref CorrectionFieldRef) {
	if slices.Contains(identityCorrectionFields, ref.Field) && !slices.Contains(stored.IdentityResetFields, ref.Field) {
		stored.IdentityResetFields = append(stored.IdentityResetFields, ref.Field)
		slices.Sort(stored.IdentityResetFields)
	}
	switch ref.Field {
	case CorrectionFieldIdentityTMDB:
		stored.Identity.TMDBID = nil
	case CorrectionFieldIdentityIMDB:
		stored.Identity.IMDBID = nil
	case CorrectionFieldIdentityTVDB:
		stored.Identity.TVDBID = nil
	case CorrectionFieldIdentityTVmaze:
		stored.Identity.TVmazeID = nil
	case CorrectionFieldIdentityMAL:
		stored.Identity.MALID = nil
	case CorrectionFieldReleaseNameCategory:
		stored.ReleaseName.Category = nil
	case CorrectionFieldReleaseNameType:
		stored.ReleaseName.Type = nil
	case CorrectionFieldReleaseNameSource:
		stored.ReleaseName.Source = nil
	case CorrectionFieldReleaseNameResolution:
		stored.ReleaseName.Resolution = nil
	case CorrectionFieldReleaseNameTag:
		stored.ReleaseName.Tag = nil
	case CorrectionFieldReleaseNameService:
		stored.ReleaseName.Service = nil
	case CorrectionFieldReleaseNameEdition:
		stored.ReleaseName.Edition = nil
	case CorrectionFieldReleaseNameSeason:
		stored.ReleaseName.Season = nil
	case CorrectionFieldReleaseNameEpisode:
		stored.ReleaseName.Episode = nil
	case CorrectionFieldReleaseNameEpisodeTitle:
		stored.ReleaseName.EpisodeTitle = nil
	case CorrectionFieldReleaseNameManualYear:
		stored.ReleaseName.ManualYear = nil
	case CorrectionFieldReleaseNameManualDate:
		stored.ReleaseName.ManualDate = nil
	case CorrectionFieldReleaseNameUseSeasonEpisode:
		stored.ReleaseName.UseSeasonEpisode = nil
	case CorrectionFieldReleaseNameNoSeason:
		stored.ReleaseName.NoSeason = nil
	case CorrectionFieldReleaseNameNoYear:
		stored.ReleaseName.NoYear = nil
	case CorrectionFieldReleaseNameNoAKA:
		stored.ReleaseName.NoAKA = nil
	case CorrectionFieldReleaseNameNoTag:
		stored.ReleaseName.NoTag = nil
	case CorrectionFieldReleaseNameNoEpisodeTitle:
		stored.ReleaseName.NoEpisodeTitle = nil
	case CorrectionFieldReleaseNameNoDistributor:
		stored.ReleaseName.NoDistributor = nil
	case CorrectionFieldReleaseNameNoEdition:
		stored.ReleaseName.NoEdition = nil
	case CorrectionFieldReleaseNameNoDub:
		stored.ReleaseName.NoDub = nil
	case CorrectionFieldReleaseNameNoDual:
		stored.ReleaseName.NoDual = nil
	case CorrectionFieldReleaseNameDualAudio:
		stored.ReleaseName.DualAudio = nil
	case CorrectionFieldReleaseNameRegion:
		stored.ReleaseName.Region = nil
	case CorrectionFieldMetadataDistributor:
		stored.Metadata.Distributor = nil
	case CorrectionFieldMetadataOriginalLanguage:
		stored.Metadata.OriginalLanguage = nil
	case CorrectionFieldMetadataPersonalRelease:
		stored.Metadata.PersonalRelease = nil
	case CorrectionFieldMetadataCommentary:
		stored.Metadata.Commentary = nil
	case CorrectionFieldMetadataWebDV:
		stored.Metadata.WebDV = nil
	case CorrectionFieldMetadataStreamOptimized:
		stored.Metadata.StreamOptimized = nil
	case CorrectionFieldMetadataAnime:
		stored.Metadata.Anime = nil
	case CorrectionFieldMetadataTitle:
		stored.Metadata.Title = nil
	case CorrectionFieldMetadataAlternateTitle:
		stored.Metadata.AlternateTitle = nil
	case CorrectionFieldMetadataOriginalTitle:
		stored.Metadata.OriginalTitle = nil
	case CorrectionFieldMetadataGenres:
		stored.Metadata.Genres = nil
	case CorrectionFieldMetadataAudioLanguages:
		stored.Metadata.AudioLanguages = nil
	case CorrectionFieldMetadataSubtitleLanguages:
		stored.Metadata.SubtitleLanguages = nil
	case CorrectionFieldMetadataHardcodedSubs:
		stored.Metadata.HardcodedSubs = nil
	case CorrectionFieldMetadataHardcodedSubtitleLanguages:
		stored.Metadata.HardcodedSubtitleLanguages = nil
	case CorrectionFieldMetadataTrackLanguages:
		stored.Metadata.TrackLanguages = slices.DeleteFunc(
			stored.Metadata.TrackLanguages,
			func(value TrackLanguageCorrection) bool { return value.TrackID == ref.TrackID },
		)
		if len(stored.Metadata.TrackLanguages) == 0 {
			stored.Metadata.TrackLanguages = nil
		}
	}
	if stored.ContentBindings != nil {
		delete(stored.ContentBindings, ref.Field)
	}
	stored.StaleContentFields = slices.DeleteFunc(stored.StaleContentFields, func(field CorrectionField) bool { return field == ref.Field })
}

func cloneStoredReleaseCorrections(value StoredReleaseCorrectionsV1) StoredReleaseCorrectionsV1 {
	cloned := StoredReleaseCorrectionsV1{
		Version:             value.Version,
		Identity:            cloneExternalIDOverrides(value.Identity),
		ReleaseName:         cloneReleaseNameOverrides(value.ReleaseName),
		Metadata:            cloneMetadataOverrides(value.Metadata),
		SourceFingerprint:   value.SourceFingerprint,
		StaleContentFields:  slices.Clone(value.StaleContentFields),
		IdentityResetFields: slices.Clone(value.IdentityResetFields),
	}
	if value.ContentBindings != nil {
		cloned.ContentBindings = make(map[CorrectionField]ContentBinding, len(value.ContentBindings))
		maps.Copy(cloned.ContentBindings, value.ContentBindings)
	}
	return cloned
}

func cloneReleaseCorrectionValues(value ReleaseCorrectionValues) ReleaseCorrectionValues {
	return ReleaseCorrectionValues{
		Identity:    cloneExternalIDOverrides(value.Identity),
		ReleaseName: cloneReleaseNameOverrides(value.ReleaseName),
		Metadata:    cloneMetadataOverrides(value.Metadata),
	}
}

func cloneReleaseCorrectionPatch(value ReleaseCorrectionPatch) ReleaseCorrectionPatch {
	cloned := ReleaseCorrectionPatch{
		Values:        cloneReleaseCorrectionValues(value.Values),
		ResetFields:   slices.Clone(value.ResetFields),
		ConfirmFields: slices.Clone(value.ConfirmFields),
	}
	if value.ExpectedRevision != nil {
		cloned.ExpectedRevision = new(*value.ExpectedRevision)
	}
	return cloned
}

// Fields returns each explicit value target in deterministic field order.
// Callers should Validate a patch before relying on track target validity.
func (v ReleaseCorrectionValues) Fields() []CorrectionFieldRef {
	refs, err := correctionValueRefs(v)
	if err != nil {
		return nil
	}
	fields := make([]CorrectionFieldRef, 0, len(refs))
	for _, field := range allCorrectionFields {
		if field == CorrectionFieldMetadataTrackLanguages {
			for _, correction := range v.Metadata.TrackLanguages {
				ref := CorrectionFieldRef{Field: field, TrackID: correction.TrackID}
				if _, ok := refs[string(field)+":"+correction.TrackID]; ok {
					fields = append(fields, ref)
				}
			}
			continue
		}
		if ref, ok := refs[string(field)]; ok {
			fields = append(fields, ref)
		}
	}
	return fields
}

// WithoutFields returns detached values with the selected explicit fields removed.
func (v ReleaseCorrectionValues) WithoutFields(fields []CorrectionFieldRef) (ReleaseCorrectionValues, error) {
	if _, err := validateCorrectionFieldRefs(fields, false); err != nil {
		return ReleaseCorrectionValues{}, err
	}
	stored := StoredReleaseCorrectionsV1{
		Version:     1,
		Identity:    cloneExternalIDOverrides(v.Identity),
		ReleaseName: cloneReleaseNameOverrides(v.ReleaseName),
		Metadata:    cloneMetadataOverrides(v.Metadata),
	}
	for _, field := range fields {
		resetStoredCorrectionField(&stored, field)
	}
	return ReleaseCorrectionValues{
		Identity:    stored.Identity,
		ReleaseName: stored.ReleaseName,
		Metadata:    stored.Metadata,
	}, nil
}

func releaseCorrectionValuesZero(value ReleaseCorrectionValues) bool {
	return value.Identity == (ExternalIDOverrides{}) && value.ReleaseName == (ReleaseNameOverrides{}) &&
		value.Metadata.Distributor == nil && value.Metadata.OriginalLanguage == nil && value.Metadata.PersonalRelease == nil &&
		value.Metadata.Commentary == nil && value.Metadata.WebDV == nil && value.Metadata.StreamOptimized == nil && value.Metadata.Anime == nil &&
		value.Metadata.Title == nil && value.Metadata.AlternateTitle == nil && value.Metadata.OriginalTitle == nil && value.Metadata.Genres == nil &&
		value.Metadata.AudioLanguages == nil && value.Metadata.SubtitleLanguages == nil && value.Metadata.HardcodedSubs == nil &&
		value.Metadata.HardcodedSubtitleLanguages == nil && value.Metadata.TrackLanguages == nil
}
