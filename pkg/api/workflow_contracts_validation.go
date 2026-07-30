// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
)

func normalizeTrackerID(value TrackerID) TrackerID {
	return TrackerID(strings.ToUpper(strings.TrimSpace(string(value))))
}

func validateTypedRef[T ~string](id T, revision WorkflowRevision, label string) error {
	if strings.TrimSpace(string(id)) == "" {
		return fmt.Errorf("%s id is required", label)
	}
	if revision == 0 {
		return fmt.Errorf("%s revision must be positive", label)
	}
	return nil
}

func cloneWorkflowValue[T any](value T) (T, error) {
	cloned, err := clonePreparedValue(value)
	if err != nil {
		var zero T
		return zero, fmt.Errorf("clone workflow contract: %w", err)
	}
	return cloned, nil
}

// Clone returns a detached fact-instruction snapshot.
func (s ReleaseFactInstructionSnapshot) Clone() (ReleaseFactInstructionSnapshot, error) {
	return cloneWorkflowValue(s)
}

// Normalize returns a detached snapshot with normalized tracker keys and source lookup.
func (s ReleaseFactInstructionSnapshot) Normalize() (ReleaseFactInstructionSnapshot, error) {
	normalized, err := s.Clone()
	if err != nil {
		return ReleaseFactInstructionSnapshot{}, err
	}
	normalized.Instructions.SourceLookup = strings.TrimSpace(normalized.Instructions.SourceLookup)
	normalized.Instructions.BlurayReleaseID = strings.TrimSpace(normalized.Instructions.BlurayReleaseID)
	if normalized.Instructions.TrackerIDs != nil {
		trackerIDs := make(map[string]string, len(normalized.Instructions.TrackerIDs))
		for tracker, id := range normalized.Instructions.TrackerIDs {
			tracker = strings.ToUpper(strings.TrimSpace(tracker))
			if tracker != "" {
				trackerIDs[tracker] = strings.TrimSpace(id)
			}
		}
		normalized.Instructions.TrackerIDs = trackerIDs
	}
	return normalized, nil
}

// ComputeFingerprint returns the normalized fact-instruction fingerprint.
func (s ReleaseFactInstructionSnapshot) ComputeFingerprint() (WorkflowFingerprint, error) {
	normalized, err := s.Normalize()
	if err != nil {
		return "", err
	}
	return CanonicalWorkflowFingerprint(normalized.Instructions)
}

// WithFingerprint returns a normalized snapshot with its deterministic fingerprint populated.
func (s ReleaseFactInstructionSnapshot) WithFingerprint() (ReleaseFactInstructionSnapshot, error) {
	normalized, err := s.Normalize()
	if err != nil {
		return ReleaseFactInstructionSnapshot{}, err
	}
	normalized.Fingerprint, err = normalized.ComputeFingerprint()
	return normalized, err
}

// Validate verifies snapshot identity and deterministic fingerprint integrity.
func (s ReleaseFactInstructionSnapshot) Validate() error {
	if err := validateSnapshotIdentity(string(s.ID), s.Revision, s.CreatedAt); err != nil {
		return fmt.Errorf("fact instructions: %w", err)
	}
	if err := validateWorkflowIdentity(s.WorkflowID, s.Revision); err != nil {
		return fmt.Errorf("fact instructions: %w", err)
	}
	want, err := s.ComputeFingerprint()
	if err != nil {
		return fmt.Errorf("fact instructions: %w", err)
	}
	if s.Fingerprint != want {
		return errors.New("fact instructions fingerprint does not match normalized instructions")
	}
	return nil
}

// Clone returns a detached release snapshot.
func (s ReleaseSnapshot) Clone() (ReleaseSnapshot, error) { return cloneWorkflowValue(s) }

// ComputeFingerprint returns the deterministic release-snapshot content fingerprint.
func (s ReleaseSnapshot) ComputeFingerprint() (WorkflowFingerprint, error) {
	return CanonicalWorkflowFingerprint(struct {
		FactInstructions       ReleaseFactInstructionSnapshotRef
		PreparationFingerprint WorkflowFingerprint `json:"preparationFingerprint,omitempty"`
		Release                PreparedRelease
		Display                PreparedReleaseDisplay
		Diagnostics            []PreparationDiagnostic
	}{s.FactInstructions, s.PreparationFingerprint, s.Release, s.Display, s.Diagnostics})
}

// Validate verifies release lineage, canonical generation, and fingerprint integrity.
func (s ReleaseSnapshot) Validate() error {
	if err := validateSnapshotIdentity(string(s.ID), s.Revision, s.CreatedAt); err != nil {
		return fmt.Errorf("release snapshot: %w", err)
	}
	if err := validateWorkflowIdentity(s.WorkflowID, s.Revision); err != nil {
		return fmt.Errorf("release snapshot: %w", err)
	}
	if err := validateTypedRef(s.FactInstructions.ID, s.FactInstructions.Revision, "fact instructions"); err != nil {
		return fmt.Errorf("release snapshot: %w", err)
	}
	if s.Release.Generation == 0 || strings.TrimSpace(s.Release.Source.SourcePath) == "" {
		return errors.New("release snapshot requires an exact prepared generation")
	}
	want, err := s.ComputeFingerprint()
	if err != nil {
		return fmt.Errorf("release snapshot: %w", err)
	}
	if s.Fingerprint != want {
		return errors.New("release snapshot fingerprint does not match content")
	}
	return nil
}

// Clone returns a detached tracker catalog snapshot.
func (s TrackerCatalogSnapshot) Clone() (TrackerCatalogSnapshot, error) { return cloneWorkflowValue(s) }

// Normalize returns a deterministic catalog ordered by stable tracker ID.
func (s TrackerCatalogSnapshot) Normalize() (TrackerCatalogSnapshot, error) {
	normalized, err := s.Clone()
	if err != nil {
		return TrackerCatalogSnapshot{}, err
	}
	for index := range normalized.Trackers {
		entry := &normalized.Trackers[index]
		entry.TrackerID = normalizeTrackerID(entry.TrackerID)
		entry.DisplayName = strings.TrimSpace(entry.DisplayName)
		if entry.DisplayName == "" {
			entry.DisplayName = string(entry.TrackerID)
		}
		entry.Family = strings.ToLower(strings.TrimSpace(entry.Family))
		entry.BaseURL = strings.TrimRight(strings.TrimSpace(entry.BaseURL), "/")
		entry.UploadContentMode = strings.ToLower(strings.TrimSpace(entry.UploadContentMode))
		entry.ProjectorVersion = strings.TrimSpace(entry.ProjectorVersion)
		aliases := make([]string, 0, len(entry.Aliases))
		seen := map[string]struct{}{string(entry.TrackerID): {}}
		for _, alias := range entry.Aliases {
			alias = strings.ToUpper(strings.TrimSpace(alias))
			if alias == "" {
				continue
			}
			if _, ok := seen[alias]; ok {
				continue
			}
			seen[alias] = struct{}{}
			aliases = append(aliases, alias)
		}
		slices.Sort(aliases)
		entry.Aliases = aliases
	}
	slices.SortFunc(normalized.Trackers, func(left, right TrackerCatalogDescriptor) int {
		return strings.Compare(string(left.TrackerID), string(right.TrackerID))
	})
	return normalized, nil
}

// ComputeFingerprint returns the normalized catalog fingerprint.
func (s TrackerCatalogSnapshot) ComputeFingerprint() (WorkflowFingerprint, error) {
	normalized, err := s.Normalize()
	if err != nil {
		return "", err
	}
	return CanonicalWorkflowFingerprint(struct {
		CatalogVersion string
		Trackers       []TrackerCatalogDescriptor
	}{normalized.CatalogVersion, normalized.Trackers})
}

// WithFingerprint returns a normalized catalog with its fingerprint populated.
func (s TrackerCatalogSnapshot) WithFingerprint() (TrackerCatalogSnapshot, error) {
	normalized, err := s.Normalize()
	if err != nil {
		return TrackerCatalogSnapshot{}, err
	}
	normalized.Fingerprint, err = normalized.ComputeFingerprint()
	return normalized, err
}

// ResolveTrackerID resolves a stable ID or explicit alias from this catalog.
func (s TrackerCatalogSnapshot) ResolveTrackerID(value string) (TrackerID, bool) {
	wanted := strings.ToUpper(strings.TrimSpace(value))
	for _, entry := range s.Trackers {
		if string(normalizeTrackerID(entry.TrackerID)) == wanted {
			return normalizeTrackerID(entry.TrackerID), true
		}
		for _, alias := range entry.Aliases {
			if strings.ToUpper(strings.TrimSpace(alias)) == wanted {
				return normalizeTrackerID(entry.TrackerID), true
			}
		}
	}
	return "", false
}

// Validate verifies catalog identity, stable IDs, aliases, and fingerprints.
func (s TrackerCatalogSnapshot) Validate() error {
	if err := validateSnapshotIdentity(string(s.ID), s.Revision, s.CreatedAt); err != nil {
		return fmt.Errorf("tracker catalog: %w", err)
	}
	if strings.TrimSpace(s.CatalogVersion) == "" || len(s.Trackers) == 0 {
		return errors.New("tracker catalog requires a version and trackers")
	}
	seen := make(map[string]struct{}, len(s.Trackers))
	aliases := make(map[string]string)
	for _, entry := range s.Trackers {
		id := string(normalizeTrackerID(entry.TrackerID))
		if id == "" || strings.TrimSpace(entry.DisplayName) == "" || strings.TrimSpace(entry.ProjectorVersion) == "" {
			return errors.New("tracker catalog entry requires stable id, display name, and projector version")
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("tracker catalog contains duplicate id %s", id)
		}
		seen[id] = struct{}{}
		if err := validateWorkflowFingerprint(entry.PolicyFingerprint); err != nil {
			return fmt.Errorf("tracker catalog %s policy: %w", id, err)
		}
		for _, alias := range entry.Aliases {
			alias = strings.ToUpper(strings.TrimSpace(alias))
			if alias == "" || alias == id {
				return fmt.Errorf("tracker catalog %s contains invalid alias", id)
			}
			if owner, ok := aliases[alias]; ok && owner != id {
				return fmt.Errorf("tracker alias %s belongs to both %s and %s", alias, owner, id)
			}
			aliases[alias] = id
		}
	}
	for alias, owner := range aliases {
		if _, ok := seen[alias]; ok && alias != owner {
			return fmt.Errorf("tracker alias %s conflicts with a stable id", alias)
		}
	}
	want, err := s.ComputeFingerprint()
	if err != nil {
		return fmt.Errorf("tracker catalog: %w", err)
	}
	if s.Fingerprint != want {
		return errors.New("tracker catalog fingerprint does not match normalized catalog")
	}
	return nil
}

// Clone returns a detached tracker runtime snapshot.
func (s TrackerRuntimeSnapshot) Clone() (TrackerRuntimeSnapshot, error) { return cloneWorkflowValue(s) }

// Normalize returns runtime entries ordered by normalized stable tracker ID.
func (s TrackerRuntimeSnapshot) Normalize() (TrackerRuntimeSnapshot, error) {
	normalized, err := s.Clone()
	if err != nil {
		return TrackerRuntimeSnapshot{}, err
	}
	for index := range normalized.Trackers {
		normalized.Trackers[index].TrackerID = normalizeTrackerID(normalized.Trackers[index].TrackerID)
		normalized.Trackers[index].ConfigurationVersion = strings.TrimSpace(normalized.Trackers[index].ConfigurationVersion)
	}
	slices.SortFunc(normalized.Trackers, func(left, right TrackerRuntimeEntry) int {
		return strings.Compare(string(left.TrackerID), string(right.TrackerID))
	})
	return normalized, nil
}

// ComputeFingerprint returns the normalized safe runtime fingerprint.
func (s TrackerRuntimeSnapshot) ComputeFingerprint() (WorkflowFingerprint, error) {
	normalized, err := s.Normalize()
	if err != nil {
		return "", err
	}
	return CanonicalWorkflowFingerprint(struct {
		Catalog           TrackerCatalogSnapshotRef
		RuntimeGeneration string
		Trackers          []TrackerRuntimeEntry
	}{normalized.Catalog, normalized.RuntimeGeneration, normalized.Trackers})
}

// WithFingerprint returns a normalized runtime snapshot with its fingerprint populated.
func (s TrackerRuntimeSnapshot) WithFingerprint() (TrackerRuntimeSnapshot, error) {
	normalized, err := s.Normalize()
	if err != nil {
		return TrackerRuntimeSnapshot{}, err
	}
	normalized.Fingerprint, err = normalized.ComputeFingerprint()
	return normalized, err
}

// Validate verifies runtime lineage, unique trackers, and fingerprints.
func (s TrackerRuntimeSnapshot) Validate() error {
	if err := validateSnapshotIdentity(string(s.ID), s.Revision, s.CreatedAt); err != nil {
		return fmt.Errorf("tracker runtime: %w", err)
	}
	if err := validateTypedRef(s.Catalog.ID, s.Catalog.Revision, "tracker catalog"); err != nil {
		return fmt.Errorf("tracker runtime: %w", err)
	}
	if strings.TrimSpace(s.RuntimeGeneration) == "" {
		return errors.New("tracker runtime generation is required")
	}
	seen := make(map[TrackerID]struct{}, len(s.Trackers))
	for _, entry := range s.Trackers {
		id := normalizeTrackerID(entry.TrackerID)
		if id == "" {
			return errors.New("tracker runtime entry id is required")
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("tracker runtime contains duplicate id %s", id)
		}
		seen[id] = struct{}{}
		if err := validateWorkflowFingerprint(entry.ConfigFingerprint); err != nil {
			return fmt.Errorf("tracker runtime %s config: %w", id, err)
		}
	}
	want, err := s.ComputeFingerprint()
	if err != nil {
		return fmt.Errorf("tracker runtime: %w", err)
	}
	if s.Fingerprint != want {
		return errors.New("tracker runtime fingerprint does not match normalized runtime")
	}
	return nil
}

// Clone returns a detached tracker selection.
func (s TrackerSelection) Clone() (TrackerSelection, error) { return cloneWorkflowValue(s) }

// Normalize returns an ordered selection with normalized, deduplicated stable IDs.
func (s TrackerSelection) Normalize() (TrackerSelection, error) {
	normalized, err := s.Clone()
	if err != nil {
		return TrackerSelection{}, err
	}
	trackers := make([]TrackerID, 0, len(normalized.TrackerIDs))
	seen := make(map[TrackerID]struct{}, len(normalized.TrackerIDs))
	for _, trackerID := range normalized.TrackerIDs {
		trackerID = normalizeTrackerID(trackerID)
		if trackerID == "" {
			continue
		}
		if _, ok := seen[trackerID]; ok {
			continue
		}
		seen[trackerID] = struct{}{}
		trackers = append(trackers, trackerID)
	}
	normalized.TrackerIDs = trackers
	return normalized, nil
}

// ComputeFingerprint returns the ordered tracker-selection fingerprint.
func (s TrackerSelection) ComputeFingerprint() (WorkflowFingerprint, error) {
	normalized, err := s.Normalize()
	if err != nil {
		return "", err
	}
	return CanonicalWorkflowFingerprint(struct {
		Catalog    TrackerCatalogSnapshotRef
		Runtime    TrackerRuntimeSnapshotRef
		TrackerIDs []TrackerID
	}{normalized.Catalog, normalized.Runtime, normalized.TrackerIDs})
}

// WithFingerprint returns a normalized selection with its fingerprint populated.
func (s TrackerSelection) WithFingerprint() (TrackerSelection, error) {
	normalized, err := s.Normalize()
	if err != nil {
		return TrackerSelection{}, err
	}
	normalized.Fingerprint, err = normalized.ComputeFingerprint()
	return normalized, err
}

// Validate verifies selection lineage, unique trackers, and fingerprint integrity.
func (s TrackerSelection) Validate() error {
	if err := validateSnapshotIdentity(string(s.ID), s.Revision, s.CreatedAt); err != nil {
		return fmt.Errorf("tracker selection: %w", err)
	}
	if err := validateWorkflowIdentity(s.WorkflowID, s.Revision); err != nil {
		return fmt.Errorf("tracker selection: %w", err)
	}
	if err := validateTypedRef(s.Catalog.ID, s.Catalog.Revision, "tracker catalog"); err != nil {
		return fmt.Errorf("tracker selection: %w", err)
	}
	if err := validateTypedRef(s.Runtime.ID, s.Runtime.Revision, "tracker runtime"); err != nil {
		return fmt.Errorf("tracker selection: %w", err)
	}
	if len(s.TrackerIDs) == 0 {
		return errors.New("tracker selection requires at least one tracker")
	}
	normalized, err := s.Normalize()
	if err != nil {
		return fmt.Errorf("tracker selection: %w", err)
	}
	if len(normalized.TrackerIDs) != len(s.TrackerIDs) {
		return errors.New("tracker selection contains blank or duplicate tracker ids")
	}
	want, err := normalized.ComputeFingerprint()
	if err != nil {
		return fmt.Errorf("tracker selection: %w", err)
	}
	if s.Fingerprint != want {
		return errors.New("tracker selection fingerprint does not match normalized selection")
	}
	return nil
}

// Clone returns detached tracker projection instructions.
func (s TrackerProjectionInstructionSnapshot) Clone() (TrackerProjectionInstructionSnapshot, error) {
	return cloneWorkflowValue(s)
}

// Normalize returns projection instructions keyed by normalized stable tracker ID.
func (s TrackerProjectionInstructionSnapshot) Normalize() (TrackerProjectionInstructionSnapshot, error) {
	normalized, err := s.Clone()
	if err != nil {
		return TrackerProjectionInstructionSnapshot{}, err
	}
	instructions := make(map[TrackerID]TrackerProjectionInstructions, len(normalized.Instructions))
	for trackerID, value := range normalized.Instructions {
		trackerID = normalizeTrackerID(trackerID)
		if trackerID != "" {
			instructions[trackerID] = value
		}
	}
	normalized.Instructions = instructions
	return normalized, nil
}

// ComputeFingerprint returns the normalized tracker-projection instruction fingerprint.
func (s TrackerProjectionInstructionSnapshot) ComputeFingerprint() (WorkflowFingerprint, error) {
	normalized, err := s.Normalize()
	if err != nil {
		return "", err
	}
	return CanonicalWorkflowFingerprint(normalized.Instructions)
}

// WithFingerprint returns normalized projection instructions with their fingerprint populated.
func (s TrackerProjectionInstructionSnapshot) WithFingerprint() (TrackerProjectionInstructionSnapshot, error) {
	normalized, err := s.Normalize()
	if err != nil {
		return TrackerProjectionInstructionSnapshot{}, err
	}
	normalized.Fingerprint, err = normalized.ComputeFingerprint()
	return normalized, err
}

// Validate verifies projection-instruction identity and fingerprint integrity.
func (s TrackerProjectionInstructionSnapshot) Validate() error {
	if err := validateSnapshotIdentity(string(s.ID), s.Revision, s.CreatedAt); err != nil {
		return fmt.Errorf("tracker projection instructions: %w", err)
	}
	if err := validateWorkflowIdentity(s.WorkflowID, s.Revision); err != nil {
		return fmt.Errorf("tracker projection instructions: %w", err)
	}
	normalized, err := s.Normalize()
	if err != nil {
		return fmt.Errorf("tracker projection instructions: %w", err)
	}
	if len(normalized.Instructions) != len(s.Instructions) {
		return errors.New("tracker projection instructions contain blank or duplicate tracker ids")
	}
	want, err := normalized.ComputeFingerprint()
	if err != nil {
		return fmt.Errorf("tracker projection instructions: %w", err)
	}
	if s.Fingerprint != want {
		return errors.New("tracker projection instruction fingerprint does not match content")
	}
	return nil
}

// Clone returns a detached tracker projection set.
func (s TrackerReleaseProjectionSet) Clone() (TrackerReleaseProjectionSet, error) {
	return cloneWorkflowValue(s)
}

// Validate verifies exact-generation projection lineage and readiness invariants.
func (s TrackerReleaseProjectionSet) Validate() error {
	if err := validateSnapshotIdentity(string(s.ID), s.Revision, s.CreatedAt); err != nil {
		return fmt.Errorf("tracker projections: %w", err)
	}
	if err := validateWorkflowIdentity(s.WorkflowID, s.Revision); err != nil {
		return fmt.Errorf("tracker projections: %w", err)
	}
	for _, ref := range []struct {
		id       string
		revision WorkflowRevision
		label    string
	}{
		{string(s.Release.ID), s.Release.Revision, "release"},
		{string(s.Catalog.ID), s.Catalog.Revision, "tracker catalog"},
		{string(s.Runtime.ID), s.Runtime.Revision, "tracker runtime"},
		{string(s.Selection.ID), s.Selection.Revision, "tracker selection"},
	} {
		if err := validateTypedRef(ref.id, ref.revision, ref.label); err != nil {
			return fmt.Errorf("tracker projections: %w", err)
		}
	}
	if strings.TrimSpace(s.ReleaseRef.SourcePath) == "" || s.ReleaseRef.Generation == 0 {
		return errors.New("tracker projections require an exact release ref")
	}
	if !validWorkflowExecutionMode(s.ExecutionMode) {
		return fmt.Errorf("tracker projections have unsupported execution mode %q", s.ExecutionMode)
	}
	if err := validateWorkflowFingerprint(s.InputFingerprint); err != nil {
		return fmt.Errorf("tracker projections input: %w", err)
	}
	if err := validateWorkflowFingerprint(s.PolicyFingerprint); err != nil {
		return fmt.Errorf("tracker projections policy: %w", err)
	}
	if len(s.Projections) == 0 {
		return errors.New("tracker projection set requires projections")
	}
	seen := make(map[TrackerID]struct{}, len(s.Projections))
	for _, projection := range s.Projections {
		id := normalizeTrackerID(projection.TrackerID)
		if id == "" {
			return errors.New("tracker projection requires tracker id")
		}
		if strings.TrimSpace(projection.UploadReleaseName) == "" && projection.Readiness == ReadinessStatusReady {
			return errors.New("ready tracker projection requires upload release name")
		}
		if projection.Readiness == ReadinessStatusReady {
			uploadName := strings.TrimSpace(projection.UploadReleaseName)
			searchName := strings.TrimSpace(projection.DuplicateCriteria.Name)
			if searchName == "" {
				return errors.New("ready tracker projection requires duplicate-search name")
			}
			if strings.IndexFunc(uploadName, unicode.IsControl) >= 0 || strings.IndexFunc(searchName, unicode.IsControl) >= 0 {
				return errors.New("ready tracker projection names must not contain control characters")
			}
			if uploadName != searchName && !projectionDeclaresSearchName(projection.AdditionalNames, searchName) {
				return fmt.Errorf("ready tracker projection %s has undeclared duplicate-search name", id)
			}
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("tracker projections contain duplicate tracker %s", id)
		}
		seen[id] = struct{}{}
		for label, fingerprint := range map[string]WorkflowFingerprint{
			"input":     projection.InputFingerprint,
			"catalog":   projection.CatalogFingerprint,
			"config":    projection.ConfigFingerprint,
			"projector": projection.ProjectorFingerprint,
			"criteria":  projection.CriteriaFingerprint,
		} {
			if err := validateWorkflowFingerprint(fingerprint); err != nil {
				return fmt.Errorf("tracker projection %s %s: %w", id, label, err)
			}
		}
		if projection.DupeReady && projection.Readiness != ReadinessStatusReady {
			return fmt.Errorf("tracker projection %s is dupe-ready without ready status", id)
		}
		if projection.DupeReady && slices.ContainsFunc(projection.PolicyDecisions, func(decision TrackerPolicyDecision) bool {
			return decision.Blocking
		}) {
			return fmt.Errorf("tracker projection %s is dupe-ready with a blocking policy decision", id)
		}
	}
	if (s.Status == StageStatusReady || s.Status == StageStatusCompleted) && !hasDupeReadyProjection(s.Projections) {
		return errors.New("ready tracker projection set requires at least one dupe-ready projection")
	}
	if s.Status == StageStatusBlocked && len(s.RequiredActions) == 0 && len(s.Failures) == 0 {
		return errors.New("blocked tracker projection set requires actions or failures")
	}
	return nil
}

func projectionDeclaresSearchName(names []TrackerReleaseName, searchName string) bool {
	return slices.ContainsFunc(names, func(name TrackerReleaseName) bool {
		return name.Role == TrackerReleaseNameRoleSearch && strings.TrimSpace(name.Value) == searchName
	})
}

func hasDupeReadyProjection(projections []TrackerReleaseProjection) bool {
	for _, projection := range projections {
		if projection.DupeReady && projection.Readiness == ReadinessStatusReady {
			return true
		}
	}
	return false
}

// Clone returns a detached tracker preflight assessment.
func (s TrackerPreflightAssessment) Clone() (TrackerPreflightAssessment, error) {
	return cloneWorkflowValue(s)
}

// Validate verifies projection lineage, freshness, and unique per-tracker results.
func (s TrackerPreflightAssessment) Validate() error {
	if err := validateSnapshotIdentity(string(s.ID), s.Revision, s.CreatedAt); err != nil {
		return fmt.Errorf("tracker preflight: %w", err)
	}
	if err := validateWorkflowIdentity(s.WorkflowID, s.Revision); err != nil {
		return fmt.Errorf("tracker preflight: %w", err)
	}
	if !validWorkflowExecutionMode(s.ExecutionMode) {
		return fmt.Errorf("tracker preflight has unsupported execution mode %q", s.ExecutionMode)
	}
	if err := validateTypedRef(s.ProjectionSet.ID, s.ProjectionSet.Revision, "tracker projections"); err != nil {
		return fmt.Errorf("tracker preflight: %w", err)
	}
	if err := validateTypedRef(s.Runtime.ID, s.Runtime.Revision, "tracker runtime"); err != nil {
		return fmt.Errorf("tracker preflight: %w", err)
	}
	if err := validateWorkflowFingerprint(s.InputFingerprint); err != nil {
		return fmt.Errorf("tracker preflight input: %w", err)
	}
	if !s.ExpiresAt.After(s.CreatedAt) {
		return errors.New("tracker preflight expiry must follow creation")
	}
	if len(s.Results) == 0 {
		return errors.New("tracker preflight requires results")
	}
	seen := make(map[TrackerID]struct{}, len(s.Results))
	readyCount := 0
	hasFailed := false
	for _, result := range s.Results {
		id := normalizeTrackerID(result.TrackerID)
		if id == "" || result.AssessedAt.IsZero() || !result.FreshUntil.After(result.AssessedAt) {
			return errors.New("tracker preflight result requires tracker id and valid freshness")
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("tracker preflight contains duplicate tracker %s", id)
		}
		seen[id] = struct{}{}
		if err := validateWorkflowFingerprint(result.ConfigFingerprint); err != nil {
			return fmt.Errorf("tracker preflight %s config: %w", id, err)
		}
		if err := validateWorkflowFingerprint(result.ProjectionFingerprint); err != nil {
			return fmt.Errorf("tracker preflight %s projection: %w", id, err)
		}
		switch result.State {
		case TrackerPreflightStateReady:
			readyCount++
			if !result.AuthReady || !result.ClaimsReady || !result.BannedGroupsReady || !result.RemoteMetadataReady {
				return fmt.Errorf("ready tracker preflight %s has incomplete prerequisites", id)
			}
		case TrackerPreflightStateActionRequired:
			if len(result.RequiredActions) == 0 {
				return fmt.Errorf("action-required tracker preflight %s requires an action", id)
			}
		case TrackerPreflightStateRetryable, TrackerPreflightStateExpired:
			if len(result.Failures) == 0 {
				return fmt.Errorf("%s tracker preflight %s requires a failure", result.State, id)
			}
		case TrackerPreflightStateFailed:
			hasFailed = true
			if len(result.Failures) == 0 {
				return fmt.Errorf("failed tracker preflight %s requires a failure", id)
			}
		default:
			return fmt.Errorf("tracker preflight %s has invalid state %q", id, result.State)
		}
	}
	if s.Status == StageStatusReady && readyCount == 0 {
		return errors.New("ready tracker preflight requires at least one ready result")
	}
	if s.Status == StageStatusFailed && !hasFailed {
		return errors.New("failed tracker preflight requires a failed result")
	}
	if s.Status == StageStatusBlocked && readyCount == len(s.Results) {
		return errors.New("blocked tracker preflight requires a non-ready result")
	}
	return nil
}

// Clone returns a detached duplicate assessment.
func (s DupeAssessment) Clone() (DupeAssessment, error) { return cloneWorkflowValue(s) }

// Validate verifies exact projection lineage, unique tracker results, and freshness.
func (s DupeAssessment) Validate() error {
	if err := validateSnapshotIdentity(string(s.ID), s.Revision, s.CreatedAt); err != nil {
		return fmt.Errorf("dupe assessment: %w", err)
	}
	if err := validateWorkflowIdentity(s.WorkflowID, s.Revision); err != nil {
		return fmt.Errorf("dupe assessment: %w", err)
	}
	for _, ref := range []struct {
		id       string
		revision WorkflowRevision
		label    string
	}{
		{string(s.Release.ID), s.Release.Revision, "release"},
		{string(s.Selection.ID), s.Selection.Revision, "tracker selection"},
		{string(s.ProjectionSet.ID), s.ProjectionSet.Revision, "tracker projections"},
	} {
		if err := validateTypedRef(ref.id, ref.revision, ref.label); err != nil {
			return fmt.Errorf("dupe assessment: %w", err)
		}
	}
	if strings.TrimSpace(s.ReleaseRef.SourcePath) == "" || s.ReleaseRef.Generation == 0 {
		return errors.New("dupe assessment requires an exact release ref")
	}
	if err := validateWorkflowFingerprint(s.InputFingerprint); err != nil {
		return fmt.Errorf("dupe assessment input: %w", err)
	}
	if !s.ExpiresAt.After(s.CreatedAt) {
		return errors.New("dupe assessment expiry must follow creation")
	}
	if len(s.Results) == 0 {
		return errors.New("dupe assessment requires results")
	}
	seen := make(map[TrackerID]struct{}, len(s.Results))
	hasPending := false
	hasFailed := false
	for _, result := range s.Results {
		id := normalizeTrackerID(result.TrackerID)
		if id == "" || strings.TrimSpace(result.UploadReleaseName) == "" {
			return errors.New("dupe result requires tracker id and upload release name")
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("dupe assessment contains duplicate tracker %s", id)
		}
		seen[id] = struct{}{}
		if result.CheckedAt.IsZero() || !result.FreshUntil.After(result.CheckedAt) {
			return fmt.Errorf("dupe result %s has invalid freshness", id)
		}
		if err := validateWorkflowFingerprint(result.ProjectionFingerprint); err != nil {
			return fmt.Errorf("dupe result %s projection: %w", id, err)
		}
		if err := validateWorkflowFingerprint(result.CriteriaFingerprint); err != nil {
			return fmt.Errorf("dupe result %s criteria: %w", id, err)
		}
		switch result.Decision {
		case DupeDecisionPending:
			hasPending = true
			if len(result.RequiredActions) == 0 || result.Status != StageStatusBlocked {
				return fmt.Errorf("pending dupe result %s requires blocked status and an action", id)
			}
		case DupeDecisionAccepted, DupeDecisionIgnored:
			if len(result.Matches) == 0 || result.Status != StageStatusCompleted {
				return fmt.Errorf("reviewed dupe result %s requires matches and completed status", id)
			}
		case DupeDecisionNoMatch:
			if result.Status != StageStatusCompleted && result.Status != StageStatusSkipped {
				return fmt.Errorf("resolved dupe result %s has invalid status %s", id, result.Status)
			}
		case DupeDecisionBypassed:
			if result.Status != StageStatusCompleted || len(result.Matches) > 0 || len(result.RequiredActions) > 0 || len(result.Failures) > 0 {
				return fmt.Errorf("bypassed dupe result %s must be completed without matches, actions, or failures", id)
			}
		case DupeDecisionSkipped:
			if result.Status != StageStatusCompleted && result.Status != StageStatusSkipped && result.Status != StageStatusFailed {
				return fmt.Errorf("skipped dupe result %s has invalid status %s", id, result.Status)
			}
		default:
			return fmt.Errorf("dupe result %s has invalid decision %q", id, result.Decision)
		}
		if result.Status == StageStatusFailed {
			hasFailed = true
			if len(result.Failures) == 0 {
				return fmt.Errorf("failed dupe result %s requires a failure", id)
			}
		}
	}
	if s.Status == StageStatusBlocked && !hasPending {
		return errors.New("blocked dupe assessment requires a pending result")
	}
	if s.Status == StageStatusFailed && !hasFailed {
		return errors.New("failed dupe assessment requires a failed result")
	}
	if s.Status == StageStatusCompleted && hasPending {
		return errors.New("completed dupe assessment contains pending results")
	}
	return nil
}

// Clone returns a detached tracker-approval snapshot.
func (s TrackerApprovalSnapshot) Clone() (TrackerApprovalSnapshot, error) {
	return cloneWorkflowValue(s)
}

// Validate verifies exact post-dupe lineage and explicit candidate/approved sets.
func (s TrackerApprovalSnapshot) Validate() error {
	if err := validateSnapshotIdentity(string(s.ID), s.Revision, s.CreatedAt); err != nil {
		return fmt.Errorf("tracker approval: %w", err)
	}
	if err := validateWorkflowIdentity(s.WorkflowID, s.Revision); err != nil {
		return fmt.Errorf("tracker approval: %w", err)
	}
	for _, ref := range []struct {
		id       string
		revision WorkflowRevision
		label    string
	}{
		{string(s.Release.ID), s.Release.Revision, "release"},
		{string(s.Selection.ID), s.Selection.Revision, "tracker selection"},
		{string(s.ProjectionSet.ID), s.ProjectionSet.Revision, "tracker projections"},
		{string(s.Preflight.ID), s.Preflight.Revision, "tracker preflight"},
		{string(s.Dupes.ID), s.Dupes.Revision, "duplicate assessment"},
	} {
		if err := validateTypedRef(ref.id, ref.revision, ref.label); err != nil {
			return fmt.Errorf("tracker approval: %w", err)
		}
	}
	if err := validateWorkflowFingerprint(s.InputFingerprint); err != nil {
		return fmt.Errorf("tracker approval input: %w", err)
	}
	if len(s.CandidateTrackerIDs) == 0 || len(s.ApprovedTrackerIDs) == 0 {
		return errors.New("tracker approval requires non-empty candidate and approved tracker IDs")
	}
	candidates := make(map[TrackerID]int, len(s.CandidateTrackerIDs))
	for index, trackerID := range s.CandidateTrackerIDs {
		trackerID = normalizeTrackerID(trackerID)
		if trackerID == "" {
			return errors.New("tracker approval candidate ID is required")
		}
		if _, duplicate := candidates[trackerID]; duplicate {
			return fmt.Errorf("tracker approval contains duplicate candidate %s", trackerID)
		}
		candidates[trackerID] = index
	}
	approved := make(map[TrackerID]struct{}, len(s.ApprovedTrackerIDs))
	priorCandidateIndex := -1
	for _, trackerID := range s.ApprovedTrackerIDs {
		trackerID = normalizeTrackerID(trackerID)
		candidateIndex, candidate := candidates[trackerID]
		if !candidate {
			return fmt.Errorf("tracker approval includes non-candidate tracker %s", trackerID)
		}
		if candidateIndex <= priorCandidateIndex {
			return errors.New("tracker approval IDs must preserve candidate order")
		}
		if _, duplicate := approved[trackerID]; duplicate {
			return fmt.Errorf("tracker approval contains duplicate approved tracker %s", trackerID)
		}
		approved[trackerID] = struct{}{}
		priorCandidateIndex = candidateIndex
	}
	return nil
}

// Clone returns a detached media-artifact set.
func (s MediaArtifactSet) Clone() (MediaArtifactSet, error) { return cloneWorkflowValue(s) }

// Validate verifies exact release/projection lineage and opaque artifact identity.
func (s MediaArtifactSet) Validate() error {
	if err := validateSnapshotIdentity(string(s.ID), s.Revision, s.CreatedAt); err != nil {
		return fmt.Errorf("media artifacts: %w", err)
	}
	if err := validateWorkflowIdentity(s.WorkflowID, s.Revision); err != nil {
		return fmt.Errorf("media artifacts: %w", err)
	}
	if err := validateTypedRef(s.Release.ID, s.Release.Revision, "release"); err != nil {
		return fmt.Errorf("media artifacts: %w", err)
	}
	if err := validateTypedRef(s.ProjectionSet.ID, s.ProjectionSet.Revision, "tracker projections"); err != nil {
		return fmt.Errorf("media artifacts: %w", err)
	}
	if err := validateOptionalTrackerApprovalRef(s.TrackerApproval); err != nil {
		return fmt.Errorf("media artifacts: %w", err)
	}
	if strings.TrimSpace(s.ReleaseRef.SourcePath) == "" || s.ReleaseRef.Generation == 0 {
		return errors.New("media artifacts require an exact release ref")
	}
	if err := validateWorkflowFingerprint(s.CaptureFingerprint); err != nil {
		return fmt.Errorf("media capture: %w", err)
	}
	if err := validateWorkflowFingerprint(s.RequirementsFingerprint); err != nil {
		return fmt.Errorf("media requirements: %w", err)
	}
	seen := make(map[PublicResourceID]struct{}, len(s.Artifacts))
	for _, artifact := range s.Artifacts {
		if strings.TrimSpace(string(artifact.ID)) == "" {
			return errors.New("media artifact id is required")
		}
		if _, ok := seen[artifact.ID]; ok {
			return fmt.Errorf("media artifacts contain duplicate id %s", artifact.ID)
		}
		seen[artifact.ID] = struct{}{}
		switch artifact.Kind {
		case MediaArtifactScreenshot, MediaArtifactDVDMenu, MediaArtifactHostedImage:
		default:
			return fmt.Errorf("media artifact %s has invalid kind %q", artifact.ID, artifact.Kind)
		}
		if artifact.Index < 0 || artifact.TimestampSeconds < 0 || artifact.Width < 0 || artifact.Height < 0 || artifact.SizeBytes < 0 {
			return fmt.Errorf("media artifact %s has invalid dimensions or size", artifact.ID)
		}
	}
	if s.Status == StageStatusBlocked && len(s.RequiredActions) == 0 && len(s.Failures) == 0 {
		return errors.New("blocked media artifacts require actions or failures")
	}
	if s.Status == StageStatusFailed && len(s.Failures) == 0 {
		return errors.New("failed media artifacts require failures")
	}
	return nil
}

// Clone returns a detached description set.
func (s DescriptionSet) Clone() (DescriptionSet, error) { return cloneWorkflowValue(s) }

// Validate verifies transport-safe description generation choices.
func (i DescriptionInstructions) Validate() error {
	seen := make(map[string]struct{}, len(i.Overrides))
	for _, override := range i.Overrides {
		key := strings.ToLower(strings.TrimSpace(override.GroupKey))
		if key == "" || strings.TrimSpace(override.Source) == "" {
			return errors.New("description overrides require a group key and source")
		}
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate description override group %s", key)
		}
		seen[key] = struct{}{}
	}
	for trackerID, answers := range i.QuestionnaireAnswers {
		if strings.TrimSpace(string(trackerID)) == "" {
			return errors.New("description questionnaire tracker ID is required")
		}
		for key := range answers {
			if strings.TrimSpace(key) == "" {
				return fmt.Errorf("description questionnaire key is required for tracker %s", trackerID)
			}
		}
	}
	return nil
}

// Validate verifies exact dependency lineage and description fingerprints.
func (s DescriptionSet) Validate() error {
	if err := validateSnapshotIdentity(string(s.ID), s.Revision, s.CreatedAt); err != nil {
		return fmt.Errorf("description set: %w", err)
	}
	if err := validateWorkflowIdentity(s.WorkflowID, s.Revision); err != nil {
		return fmt.Errorf("description set: %w", err)
	}
	if err := validateTypedRef(s.Release.ID, s.Release.Revision, "release"); err != nil {
		return fmt.Errorf("description set: %w", err)
	}
	if err := validateTypedRef(s.ProjectionSet.ID, s.ProjectionSet.Revision, "tracker projections"); err != nil {
		return fmt.Errorf("description set: %w", err)
	}
	if err := validateOptionalTrackerApprovalRef(s.TrackerApproval); err != nil {
		return fmt.Errorf("description set: %w", err)
	}
	if strings.TrimSpace(s.ReleaseRef.SourcePath) == "" || s.ReleaseRef.Generation == 0 {
		return errors.New("description set requires an exact release ref")
	}
	if s.Media == nil {
		return errors.New("description set requires exact media artifacts")
	}
	if err := validateTypedRef(s.Media.ID, s.Media.Revision, "media artifacts"); err != nil {
		return fmt.Errorf("description set: %w", err)
	}
	if err := validateWorkflowFingerprint(s.InputFingerprint); err != nil {
		return fmt.Errorf("description input: %w", err)
	}
	if err := validateWorkflowFingerprint(s.TemplateFingerprint); err != nil {
		return fmt.Errorf("description template: %w", err)
	}
	seen := make(map[string]struct{}, len(s.Descriptions))
	for _, description := range s.Descriptions {
		key := strings.TrimSpace(description.GroupKey)
		if key == "" {
			return errors.New("description group key is required")
		}
		if _, ok := seen[key]; ok {
			return fmt.Errorf("description set contains duplicate group %s", key)
		}
		seen[key] = struct{}{}
		seenTrackers := make(map[TrackerID]struct{}, len(description.TrackerIDs))
		for _, trackerID := range description.TrackerIDs {
			if strings.TrimSpace(string(trackerID)) == "" {
				return fmt.Errorf("description group %s contains an empty tracker ID", key)
			}
			if _, ok := seenTrackers[trackerID]; ok {
				return fmt.Errorf("description group %s contains duplicate tracker %s", key, trackerID)
			}
			seenTrackers[trackerID] = struct{}{}
		}
		if err := validateWorkflowFingerprint(description.ContentFingerprint); err != nil {
			return fmt.Errorf("description group %s: %w", key, err)
		}
	}
	seenTrackerResults := make(map[TrackerID]struct{}, len(s.TrackerResults))
	for _, result := range s.TrackerResults {
		trackerID := normalizeTrackerID(result.TrackerID)
		if trackerID == "" {
			return errors.New("description tracker result requires a tracker ID")
		}
		if _, ok := seenTrackerResults[trackerID]; ok {
			return fmt.Errorf("description set contains duplicate tracker result %s", trackerID)
		}
		seenTrackerResults[trackerID] = struct{}{}
		switch result.Status {
		case StageStatusCompleted:
		case StageStatusSkipped, StageStatusFailed:
			if strings.TrimSpace(result.Message) == "" {
				return fmt.Errorf("description tracker result %s requires a message", trackerID)
			}
		case StageStatusPending, StageStatusQueued, StageStatusReady, StageStatusBlocked, StageStatusStale, StageStatusPartial,
			StageStatusRunning, StageStatusExecuted, StageStatusInterrupted, StageStatusCanceled, StageStatusUnavailable, "":
			return fmt.Errorf("description tracker result %s has invalid status %q", trackerID, result.Status)
		default:
			return fmt.Errorf("description tracker result %s has invalid status %q", trackerID, result.Status)
		}
	}
	switch s.Status {
	case StageStatusCompleted:
		if len(s.Descriptions) == 0 {
			return errors.New("completed description set requires descriptions")
		}
	case StageStatusSkipped:
	case StageStatusBlocked:
		if len(s.RequiredActions) == 0 && len(s.Failures) == 0 {
			return errors.New("blocked description set requires actions or failures")
		}
	case StageStatusFailed:
		if len(s.Failures) == 0 {
			return errors.New("failed description set requires failures")
		}
	case StageStatusPending, StageStatusQueued, StageStatusReady, StageStatusStale, StageStatusPartial, StageStatusRunning,
		StageStatusExecuted, StageStatusInterrupted, StageStatusCanceled, StageStatusUnavailable, "":
		return fmt.Errorf("description set has invalid status %q", s.Status)
	default:
		return fmt.Errorf("description set has invalid status %q", s.Status)
	}
	return nil
}

// Clone returns a detached upload-plan projection.
func (s UploadPlan) Clone() (UploadPlan, error) { return cloneWorkflowValue(s) }

// Validate verifies exact reviewed dependencies and single-use expiry semantics.
func (s UploadPlan) Validate() error {
	if err := validateSnapshotIdentity(string(s.ID), s.Revision, s.CreatedAt); err != nil {
		return fmt.Errorf("upload plan: %w", err)
	}
	if err := validateWorkflowIdentity(s.WorkflowID, s.Revision); err != nil {
		return fmt.Errorf("upload plan: %w", err)
	}
	for _, ref := range []struct {
		id       string
		revision WorkflowRevision
		label    string
	}{
		{string(s.Release.ID), s.Release.Revision, "release"},
		{string(s.ProjectionSet.ID), s.ProjectionSet.Revision, "tracker projections"},
		{string(s.Dupes.ID), s.Dupes.Revision, "dupe assessment"},
	} {
		if err := validateTypedRef(ref.id, ref.revision, ref.label); err != nil {
			return fmt.Errorf("upload plan: %w", err)
		}
	}
	if strings.TrimSpace(s.ReleaseRef.SourcePath) == "" || s.ReleaseRef.Generation == 0 {
		return errors.New("upload plan requires an exact release ref")
	}
	if err := validateOptionalTrackerApprovalRef(s.TrackerApproval); err != nil {
		return fmt.Errorf("upload plan: %w", err)
	}
	if s.Media == nil || s.Descriptions == nil {
		return errors.New("upload plan requires exact media and description refs")
	}
	if err := validateTypedRef(s.Media.ID, s.Media.Revision, "media artifacts"); err != nil {
		return fmt.Errorf("upload plan: %w", err)
	}
	if err := validateTypedRef(s.Descriptions.ID, s.Descriptions.Revision, "descriptions"); err != nil {
		return fmt.Errorf("upload plan: %w", err)
	}
	if !s.SingleUse {
		return errors.New("upload plan must be single use")
	}
	if !s.ExpiresAt.After(s.CreatedAt) {
		return errors.New("upload plan expiry must follow creation")
	}
	if err := validateWorkflowFingerprint(s.InputFingerprint); err != nil {
		return fmt.Errorf("upload plan input: %w", err)
	}
	seen := make(map[TrackerID]struct{}, len(s.Trackers))
	eligible := 0
	for _, tracker := range s.Trackers {
		id := normalizeTrackerID(tracker.TrackerID)
		if id == "" || strings.TrimSpace(tracker.UploadReleaseName) == "" {
			return errors.New("upload plan tracker requires id and upload release name")
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("upload plan contains duplicate tracker %s", id)
		}
		seen[id] = struct{}{}
		if tracker.Eligible {
			eligible++
		}
		switch tracker.Status {
		case StageStatusReady:
			if !tracker.Eligible {
				return fmt.Errorf("upload plan tracker %s cannot be ready and ineligible", id)
			}
		case StageStatusSkipped, StageStatusBlocked:
			if tracker.Eligible {
				return fmt.Errorf("upload plan tracker %s cannot be %s and eligible", id, tracker.Status)
			}
		case StageStatusFailed:
			if tracker.Eligible || len(tracker.Failures) == 0 {
				return fmt.Errorf("failed upload plan tracker %s requires an ineligible operation and failure detail", id)
			}
		case "":
			// Older retained plans predate explicit tracker status and derive it
			// from Eligible. New plans always populate Status.
		case StageStatusPending, StageStatusQueued, StageStatusStale, StageStatusPartial, StageStatusRunning, StageStatusCompleted,
			StageStatusExecuted, StageStatusInterrupted, StageStatusCanceled, StageStatusUnavailable:
			return fmt.Errorf("upload plan tracker %s has invalid status %q", id, tracker.Status)
		default:
			return fmt.Errorf("upload plan tracker %s has invalid status %q", id, tracker.Status)
		}
		switch tracker.ClientInjectionStatus {
		case StageStatusCompleted:
			if strings.TrimSpace(tracker.ClientInjectionMessage) == "" {
				return fmt.Errorf("upload plan tracker %s completed client injection requires a message", id)
			}
		case StageStatusSkipped, StageStatusFailed:
			if strings.TrimSpace(tracker.ClientInjectionMessage) == "" {
				return fmt.Errorf("upload plan tracker %s %s client injection requires a message", id, tracker.ClientInjectionStatus)
			}
		case "":
			if strings.TrimSpace(tracker.ClientInjectionMessage) != "" {
				return fmt.Errorf("upload plan tracker %s client injection message requires a status", id)
			}
		case StageStatusPending, StageStatusQueued, StageStatusReady, StageStatusBlocked, StageStatusStale, StageStatusPartial,
			StageStatusRunning, StageStatusExecuted, StageStatusInterrupted, StageStatusCanceled, StageStatusUnavailable:
			return fmt.Errorf("upload plan tracker %s has invalid client injection status %q", id, tracker.ClientInjectionStatus)
		default:
			return fmt.Errorf("upload plan tracker %s has invalid client injection status %q", id, tracker.ClientInjectionStatus)
		}
		if strings.Contains(tracker.Endpoint, "?") {
			return fmt.Errorf("upload plan tracker %s endpoint must not expose query values", id)
		}
		seenFields := make(map[string]struct{}, len(tracker.Fields))
		for _, field := range tracker.Fields {
			key := strings.ToLower(strings.TrimSpace(field.Key))
			if key == "" {
				return fmt.Errorf("upload plan tracker %s contains an empty field key", id)
			}
			if _, ok := seenFields[key]; ok {
				return fmt.Errorf("upload plan tracker %s contains duplicate field %s", id, key)
			}
			seenFields[key] = struct{}{}
			if workflowFieldLooksSecret(key) && strings.TrimSpace(field.Value) != "[redacted]" {
				return fmt.Errorf("upload plan tracker %s field %s must be redacted", id, key)
			}
		}
		if err := validateWorkflowFingerprint(tracker.SemanticFingerprint); err != nil {
			return fmt.Errorf("upload plan tracker %s: %w", id, err)
		}
		if tracker.Eligible && strings.TrimSpace(string(tracker.PreparedOperationID)) == "" {
			return fmt.Errorf("upload plan tracker %s eligible operation requires an opaque prepared-operation id", id)
		}
		if (strings.TrimSpace(string(tracker.TorrentArtifactID)) == "") != (tracker.TorrentFingerprint == "") {
			return fmt.Errorf("upload plan tracker %s exact torrent id and fingerprint must be paired", id)
		}
		if tracker.TorrentFingerprint != "" {
			if err := validateWorkflowFingerprint(tracker.TorrentFingerprint); err != nil {
				return fmt.Errorf("upload plan tracker %s torrent fingerprint: %w", id, err)
			}
		}
		if tracker.ClientInjectionStatus == StageStatusFailed && tracker.ClientFailureCode == "" {
			return fmt.Errorf("upload plan tracker %s failed client injection requires a stable failure code", id)
		}
		if tracker.ClientInjectionStatus != StageStatusFailed && tracker.ClientFailureCode != "" {
			return fmt.Errorf("upload plan tracker %s client failure code requires failed injection", id)
		}
	}
	switch s.Status {
	case StageStatusReady:
		if eligible == 0 {
			return errors.New("ready upload plan requires an eligible tracker")
		}
	case StageStatusSkipped:
		if len(s.Trackers) != 0 {
			return errors.New("skipped upload plan cannot contain tracker operations")
		}
	case StageStatusBlocked:
	case StageStatusUnavailable:
	case StageStatusPending, StageStatusQueued, StageStatusStale, StageStatusFailed, StageStatusPartial, StageStatusRunning,
		StageStatusCompleted, StageStatusExecuted, StageStatusInterrupted, StageStatusCanceled, "":
		return fmt.Errorf("upload plan has invalid status %q", s.Status)
	default:
		return fmt.Errorf("upload plan has invalid status %q", s.Status)
	}
	return nil
}

func workflowFieldLooksSecret(key string) bool {
	for _, marker := range []string{"password", "passkey", "api_key", "apikey", "api_token", "token", "cookie", "auth", "secret"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

// Clone returns a detached direct dry-run result.
func (s UploadDryRunResult) Clone() (UploadDryRunResult, error) { return cloneWorkflowValue(s) }

// Validate verifies exact dry-run lineage and safe terminal tracker reports.
func (s UploadDryRunResult) Validate() error {
	if err := validateSnapshotIdentity(string(s.ID), s.Revision, s.CreatedAt); err != nil {
		return fmt.Errorf("upload dry run: %w", err)
	}
	if err := validateWorkflowIdentity(s.WorkflowID, s.Revision); err != nil {
		return fmt.Errorf("upload dry run: %w", err)
	}
	for _, ref := range []struct {
		id       string
		revision WorkflowRevision
		label    string
	}{
		{string(s.ProjectionSet.ID), s.ProjectionSet.Revision, "tracker projections"},
		{string(s.Dupes.ID), s.Dupes.Revision, "dupe assessment"},
		{string(s.Media.ID), s.Media.Revision, "media artifacts"},
		{string(s.Descriptions.ID), s.Descriptions.Revision, "descriptions"},
	} {
		if err := validateTypedRef(ref.id, ref.revision, ref.label); err != nil {
			return fmt.Errorf("upload dry run: %w", err)
		}
	}
	if err := validateWorkflowFingerprint(s.InputFingerprint); err != nil {
		return fmt.Errorf("upload dry run input: %w", err)
	}
	if err := validateOptionalTrackerApprovalRef(s.TrackerApproval); err != nil {
		return fmt.Errorf("upload dry run: %w", err)
	}
	if len(s.TrackerIDs) == 0 {
		return errors.New("upload dry run requires target tracker IDs")
	}
	targets := make(map[TrackerID]struct{}, len(s.TrackerIDs))
	for _, trackerID := range s.TrackerIDs {
		trackerID = normalizeTrackerID(trackerID)
		if trackerID == "" {
			return errors.New("upload dry run tracker ID is required")
		}
		if _, duplicate := targets[trackerID]; duplicate {
			return fmt.Errorf("upload dry run contains duplicate target tracker %s", trackerID)
		}
		targets[trackerID] = struct{}{}
	}
	seen := make(map[TrackerID]struct{}, len(s.Reports))
	if len(s.Reports) != len(s.TrackerIDs) {
		return errors.New("upload dry run requires one report per target tracker")
	}
	failed := false
	succeeded := false
	skipped := false
	succeededCount := 0
	failedCount := 0
	skippedCount := 0
	for index, report := range s.Reports {
		id := normalizeTrackerID(report.TrackerID)
		if id == "" || strings.TrimSpace(report.UploadReleaseName) == "" {
			return errors.New("upload dry-run report requires tracker id and upload release name")
		}
		if _, targeted := targets[id]; !targeted {
			return fmt.Errorf("upload dry run report includes untargeted tracker %s", id)
		}
		if id != normalizeTrackerID(s.TrackerIDs[index]) {
			return errors.New("upload dry run report order does not match target tracker order")
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("upload dry run contains duplicate tracker %s", id)
		}
		seen[id] = struct{}{}
		switch report.Status {
		case StageStatusCompleted:
			succeeded = true
			succeededCount++
			if len(report.Failures) > 0 {
				return fmt.Errorf("completed upload dry-run tracker %s must not contain failures", id)
			}
		case StageStatusFailed:
			failed = true
			failedCount++
			if len(report.Failures) == 0 && len(report.Warnings) == 0 {
				return fmt.Errorf("failed upload dry-run tracker %s requires failure detail", id)
			}
		case StageStatusSkipped:
			skipped = true
			skippedCount++
			if len(report.Failures) > 0 {
				return fmt.Errorf("skipped upload dry-run tracker %s must not contain failures", id)
			}
		case StageStatusPending, StageStatusQueued, StageStatusReady, StageStatusBlocked, StageStatusStale, StageStatusPartial,
			StageStatusRunning, StageStatusExecuted, StageStatusInterrupted, StageStatusCanceled, StageStatusUnavailable:
			return fmt.Errorf("upload dry-run tracker %s has invalid status %q", id, report.Status)
		default:
			return fmt.Errorf("upload dry-run tracker %s has invalid status %q", id, report.Status)
		}
		if strings.Contains(report.Endpoint, "?") {
			return fmt.Errorf("upload dry-run tracker %s endpoint must not expose query values", id)
		}
		if err := validateWorkflowFingerprint(report.SemanticFingerprint); err != nil {
			return fmt.Errorf("upload dry-run tracker %s semantic fingerprint: %w", id, err)
		}
		if (strings.TrimSpace(string(report.TorrentArtifactID)) == "") != (report.TorrentFingerprint == "") {
			return fmt.Errorf("upload dry-run tracker %s exact torrent id and fingerprint must be paired", id)
		}
		if report.TorrentFingerprint != "" {
			if err := validateWorkflowFingerprint(report.TorrentFingerprint); err != nil {
				return fmt.Errorf("upload dry-run tracker %s torrent fingerprint: %w", id, err)
			}
		}
		for _, field := range report.Fields {
			key := strings.ToLower(strings.TrimSpace(field.Key))
			if key == "" {
				return fmt.Errorf("upload dry-run tracker %s contains an empty field key", id)
			}
			if workflowFieldLooksSecret(key) && strings.TrimSpace(field.Value) != "[redacted]" {
				return fmt.Errorf("upload dry-run tracker %s field %s must be redacted", id, key)
			}
		}
		switch report.ClientInjection.Status {
		case StageStatusCompleted, StageStatusSkipped, StageStatusFailed:
		case "":
			return fmt.Errorf("upload dry-run tracker %s requires a client-injection outcome", id)
		case StageStatusPending, StageStatusQueued, StageStatusReady, StageStatusBlocked, StageStatusStale, StageStatusPartial,
			StageStatusRunning, StageStatusExecuted, StageStatusInterrupted, StageStatusCanceled, StageStatusUnavailable:
			return fmt.Errorf("upload dry-run tracker %s has invalid client-injection status %q", id, report.ClientInjection.Status)
		default:
			return fmt.Errorf("upload dry-run tracker %s has invalid client-injection status %q", id, report.ClientInjection.Status)
		}
	}
	if s.SucceededCount != succeededCount || s.FailedCount != failedCount || s.SkippedCount != skippedCount {
		return errors.New("upload dry run outcome counts do not match reports")
	}
	switch s.Status {
	case StageStatusSkipped:
		if succeeded || failed || (len(s.Reports) > 0 && !skipped) {
			return errors.New("skipped upload dry run requires only skipped reports")
		}
	case StageStatusCompleted:
		if len(s.Reports) == 0 || !succeeded || failed {
			return errors.New("completed upload dry run requires successful reports and no failures")
		}
	case StageStatusFailed:
		if !failed || succeeded {
			return errors.New("failed upload dry run requires failures and no successes")
		}
	case StageStatusPartial:
		if !failed || !succeeded {
			return errors.New("partial upload dry run requires successful and failed reports")
		}
	case StageStatusPending, StageStatusQueued, StageStatusReady, StageStatusBlocked, StageStatusStale, StageStatusRunning,
		StageStatusExecuted, StageStatusInterrupted, StageStatusCanceled, StageStatusUnavailable:
		return fmt.Errorf("upload dry run has invalid status %q", s.Status)
	default:
		return fmt.Errorf("upload dry run has invalid status %q", s.Status)
	}
	return nil
}

// Clone returns a detached upload result.
func (s UploadResult) Clone() (UploadResult, error) { return cloneWorkflowValue(s) }

// Validate verifies upload-result lineage and terminal tracker outcomes.
func (s UploadResult) Validate() error {
	if err := validateSnapshotIdentity(string(s.ID), s.Revision, s.CreatedAt); err != nil {
		return fmt.Errorf("upload result: %w", err)
	}
	if err := validateWorkflowIdentity(s.WorkflowID, s.Revision); err != nil {
		return fmt.Errorf("upload result: %w", err)
	}
	for _, ref := range []struct {
		id       string
		revision WorkflowRevision
		label    string
	}{
		{string(s.ProjectionSet.ID), s.ProjectionSet.Revision, "tracker projections"},
		{string(s.Dupes.ID), s.Dupes.Revision, "dupe assessment"},
		{string(s.Media.ID), s.Media.Revision, "media artifacts"},
		{string(s.Descriptions.ID), s.Descriptions.Revision, "descriptions"},
	} {
		if err := validateTypedRef(ref.id, ref.revision, ref.label); err != nil {
			return fmt.Errorf("upload result: %w", err)
		}
	}
	if err := validateWorkflowFingerprint(s.InputFingerprint); err != nil {
		return fmt.Errorf("upload result input: %w", err)
	}
	if err := validateOptionalTrackerApprovalRef(s.TrackerApproval); err != nil {
		return fmt.Errorf("upload result: %w", err)
	}
	if s.Status != StageStatusCompleted && s.Status != StageStatusPartial && s.Status != StageStatusFailed {
		return errors.New("upload result must have completed, partial, or failed status")
	}
	seen := make(map[TrackerID]struct{}, len(s.Results))
	failedSubmission := false
	failedClientInjection := false
	for _, result := range s.Results {
		id := normalizeTrackerID(result.TrackerID)
		if id == "" {
			return errors.New("upload tracker result id is required")
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("upload result contains duplicate tracker %s", id)
		}
		seen[id] = struct{}{}
		submissionStatus := result.EffectiveSubmissionStatus()
		clientStatus := result.EffectiveClientInjectionStatus()
		if result.SubmissionStatus != "" || result.ClientInjectionStatus != "" {
			if derived := result.DerivedStatus(); result.Status != derived {
				return fmt.Errorf("upload tracker %s aggregate status %q does not match derived status %q", id, result.Status, derived)
			}
		}
		switch submissionStatus {
		case StageStatusCompleted:
			switch clientStatus {
			case StageStatusCompleted:
				if !result.ClientInjected && !result.CrossSeeded {
					return fmt.Errorf("upload tracker %s completed client injection without a success marker", id)
				}
				if len(result.Failures) > 0 || result.ClientFailureCode != "" {
					return fmt.Errorf("upload tracker %s completed client injection cannot contain failures", id)
				}
			case StageStatusSkipped:
				if result.ClientInjected || result.CrossSeeded {
					return fmt.Errorf("upload tracker %s skipped client injection with a success marker", id)
				}
				if len(result.Failures) > 0 || result.ClientFailureCode != "" {
					return fmt.Errorf("upload tracker %s skipped client injection cannot contain failures", id)
				}
			case StageStatusFailed:
				failedClientInjection = true
				if result.ClientFailureCode == "" || strings.TrimSpace(result.ClientInjectionMessage) == "" {
					return fmt.Errorf("upload tracker %s failed client injection requires code and message", id)
				}
				if len(result.Failures) == 0 || !result.hasClientInjectionFailure() {
					return fmt.Errorf("upload tracker %s failed client injection requires a client failure", id)
				}
				if slices.ContainsFunc(result.Failures, func(failure WorkflowFailure) bool {
					return failure.Failure.Operation != OperationKindClientInjection
				}) {
					return fmt.Errorf("upload tracker %s completed submission cannot contain submission failures", id)
				}
			case StageStatusPending, StageStatusQueued, StageStatusReady, StageStatusBlocked, StageStatusStale, StageStatusPartial,
				StageStatusRunning, StageStatusExecuted, StageStatusInterrupted, StageStatusCanceled, StageStatusUnavailable, "":
				return fmt.Errorf("upload tracker %s has invalid client injection status %q", id, clientStatus)
			default:
				return fmt.Errorf("upload tracker %s has invalid client injection status %q", id, clientStatus)
			}
		case StageStatusFailed, StageStatusUnavailable:
			failedSubmission = true
			if clientStatus != StageStatusPending {
				return fmt.Errorf("upload tracker %s failed submission must leave client injection pending", id)
			}
			if len(result.Failures) == 0 {
				return fmt.Errorf("failed upload tracker %s requires submission failures", id)
			}
			if result.ClientInjected || result.CrossSeeded || result.ClientFailureCode != "" ||
				slices.ContainsFunc(result.Failures, func(failure WorkflowFailure) bool {
					return failure.Failure.Operation == OperationKindClientInjection
				}) {
				return fmt.Errorf("failed upload tracker %s cannot contain client injection outcomes", id)
			}
		case StageStatusSkipped:
			if clientStatus != StageStatusSkipped && clientStatus != StageStatusPending &&
				clientStatus != StageStatusCompleted && clientStatus != StageStatusFailed {
				return fmt.Errorf("skipped upload tracker %s has invalid client injection status %q", id, clientStatus)
			}
			if clientStatus == StageStatusFailed {
				failedClientInjection = true
			}
		case StageStatusPending, StageStatusQueued, StageStatusReady, StageStatusBlocked, StageStatusStale, StageStatusPartial,
			StageStatusRunning, StageStatusExecuted, StageStatusInterrupted, StageStatusCanceled, "":
			return fmt.Errorf("upload tracker %s has invalid submission status %q", id, submissionStatus)
		default:
			return fmt.Errorf("upload tracker %s has invalid submission status %q", id, submissionStatus)
		}
	}
	switch s.Status {
	case StageStatusCompleted:
		if failedSubmission || failedClientInjection {
			return errors.New("completed upload result cannot contain failed trackers")
		}
	case StageStatusPartial:
		if failedSubmission || !failedClientInjection {
			return errors.New("partial upload result requires only a failed client injection")
		}
	case StageStatusFailed:
		if !failedSubmission {
			return errors.New("failed upload result requires a failed tracker submission")
		}
	case StageStatusPending, StageStatusQueued, StageStatusReady, StageStatusBlocked, StageStatusStale, StageStatusSkipped,
		StageStatusRunning, StageStatusExecuted, StageStatusInterrupted, StageStatusCanceled, StageStatusUnavailable, "":
		return fmt.Errorf("upload result has invalid status %q", s.Status)
	}
	return nil
}

func validateOptionalTrackerApprovalRef(ref *TrackerApprovalSnapshotRef) error {
	if ref == nil {
		return nil
	}
	if err := validateTypedRef(ref.ID, ref.Revision, "tracker approval"); err != nil {
		return err
	}
	return nil
}

// Clone returns a detached release workflow aggregate.
func (w ReleaseWorkflow) Clone() (ReleaseWorkflow, error) { return cloneWorkflowValue(w) }

// Validate verifies aggregate identity, status, and monotonic dependency lineage.
func (w ReleaseWorkflow) Validate() error {
	if err := validateWorkflowIdentity(w.ID, w.Revision); err != nil {
		return fmt.Errorf("release workflow: %w", err)
	}
	if w.CreatedAt.IsZero() || w.UpdatedAt.Before(w.CreatedAt) {
		return errors.New("release workflow requires valid timestamps")
	}
	if err := validateTypedRef(w.FactInstructions.ID, w.FactInstructions.Revision, "fact instructions"); err != nil {
		return fmt.Errorf("release workflow: %w", err)
	}
	if w.Selection != nil && (w.TrackerCatalog == nil || w.TrackerRuntime == nil) {
		return errors.New("tracker selection requires catalog and runtime snapshots")
	}
	if w.TrackerProjections != nil && (w.Release == nil || w.Selection == nil || w.TrackerCatalog == nil || w.TrackerRuntime == nil) {
		return errors.New("tracker projections require release, catalog, runtime, and selection")
	}
	if w.TrackerPreflight != nil && w.TrackerProjections == nil {
		return errors.New("tracker preflight requires tracker projections")
	}
	if w.Dupes != nil && (w.TrackerProjections == nil || w.Selection == nil) {
		return errors.New("dupe assessment requires tracker projections and selection")
	}
	if w.TrackerApproval != nil && (w.Release == nil || w.Selection == nil || w.TrackerProjections == nil ||
		w.TrackerPreflight == nil || w.Dupes == nil) {
		return errors.New("tracker approval requires exact post-dupe dependencies")
	}
	if (w.Media != nil || w.Descriptions != nil) && w.TrackerProjections == nil {
		return errors.New("media and descriptions require tracker projections")
	}
	if (w.DryRun != nil || w.UploadResult != nil) &&
		(w.Release == nil || w.TrackerProjections == nil || w.Dupes == nil || w.Media == nil || w.Descriptions == nil) {
		return errors.New("dry-run and upload results require exact workflow dependencies")
	}
	if w.Status == WorkflowStatusCompleted && w.UploadResult == nil {
		return errors.New("completed workflow requires an upload result")
	}
	if w.Status == WorkflowStatusBlocked && len(w.RequiredActions) == 0 && len(w.Failures) == 0 {
		return errors.New("blocked workflow requires actions or failures")
	}
	return nil
}

// Clone returns a detached workflow operation status.
func (s WorkflowOperationStatus) Clone() (WorkflowOperationStatus, error) {
	return cloneWorkflowValue(s)
}

// Validate verifies operation identity, progress, and terminal timestamps.
func (s WorkflowOperationStatus) Validate() error {
	if err := validateTypedRef(s.ID, s.Revision, "workflow operation"); err != nil {
		return fmt.Errorf("workflow operation: %w", err)
	}
	if strings.TrimSpace(string(s.WorkflowID)) == "" || strings.TrimSpace(s.Command) == "" || s.StartedAt.IsZero() || s.UpdatedAt.IsZero() {
		return errors.New("workflow operation requires workflow id, command, and timestamps")
	}
	if s.Sequence == 0 {
		return errors.New("workflow operation sequence must be positive")
	}
	if s.Progress < 0 || s.Progress > 100 {
		return errors.New("workflow operation progress must be between 0 and 100")
	}
	if s.Completed < 0 || s.Total < 0 || (s.Total > 0 && s.Completed > s.Total) {
		return errors.New("workflow operation counts are invalid")
	}
	terminal := s.Status == StageStatusCompleted || s.Status == StageStatusPartial || s.Status == StageStatusFailed || s.Status == StageStatusInterrupted ||
		s.Status == StageStatusExecuted || s.Status == StageStatusCanceled || s.Status == StageStatusBlocked ||
		s.Status == StageStatusStale || s.Status == StageStatusUnavailable
	if terminal && s.CompletedAt == nil {
		return errors.New("terminal workflow operation requires completion time")
	}
	if !terminal && s.CompletedAt != nil {
		return errors.New("non-terminal workflow operation cannot have completion time")
	}
	if s.Result != nil {
		if s.Result.Kind == "" || strings.TrimSpace(s.Result.RefID) == "" || s.Result.WorkflowRevision == 0 || s.Result.RefRevision == 0 {
			return errors.New("workflow operation result requires kind, workflow revision, and exact ref")
		}
		if s.ResultRevision != 0 && s.ResultRevision != s.Result.WorkflowRevision {
			return errors.New("workflow operation result revision mismatch")
		}
	}
	return nil
}
