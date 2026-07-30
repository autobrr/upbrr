// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	releaseNamePolicyCanonicalV1  = "standalone/canonical/v1"
	releaseNamePolicySceneFirstV1 = "standalone/scene-first/v1"
	releaseNamePolicyDecisionCode = "release_name_policy"
	releaseNameInstructionCode    = "release_name_instruction"
)

// NewReleaseNamePolicy binds a pure resolver to a stable versioned identifier.
func NewReleaseNamePolicy(id string, resolver ReleaseNamePolicy) ReleaseNamePolicyBinding {
	return ReleaseNamePolicyBinding{ID: strings.TrimSpace(id), Resolver: resolver}
}

// SubjectReleaseNamePolicy adapts a subject/config naming function to the
// central contract. Requested names replace automatic base-name candidates
// before tracker-required normalization is applied.
func SubjectReleaseNamePolicy(
	id string,
	resolver func(api.UploadSubject, config.TrackerConfig) string,
) ReleaseNamePolicyBinding {
	return SubjectReleaseNamesPolicy(id, func(subject api.UploadSubject, trackerConfig config.TrackerConfig) (ResolvedReleaseNames, error) {
		return ResolvedReleaseNames{Upload: resolver(subject, trackerConfig)}, nil
	})
}

// SubjectReleaseNamesPolicy adapts a subject/config resolver that explicitly
// owns both the upload name and any distinct duplicate-search name.
func SubjectReleaseNamesPolicy(
	id string,
	resolver func(api.UploadSubject, config.TrackerConfig) (ResolvedReleaseNames, error),
) ReleaseNamePolicyBinding {
	return NewReleaseNamePolicy(id, func(input ReleaseNameInput) (ResolvedReleaseNames, error) {
		subject, err := releaseNamePolicySubject(input)
		if err != nil {
			return ResolvedReleaseNames{}, err
		}
		return resolver(subject, input.TrackerConfig)
	})
}

// SimpleSubjectReleaseNamePolicy adapts a subject-only naming function.
func SimpleSubjectReleaseNamePolicy(
	id string,
	resolver func(api.UploadSubject) string,
) ReleaseNamePolicyBinding {
	return SubjectReleaseNamePolicy(id, func(subject api.UploadSubject, _ config.TrackerConfig) string {
		return resolver(subject)
	})
}

// SimpleSubjectReleaseNamesPolicy adapts a subject-only resolver that
// explicitly owns both upload and duplicate-search names.
func SimpleSubjectReleaseNamesPolicy(
	id string,
	resolver func(api.UploadSubject) ResolvedReleaseNames,
) ReleaseNamePolicyBinding {
	return SubjectReleaseNamesPolicy(id, func(subject api.UploadSubject, _ config.TrackerConfig) (ResolvedReleaseNames, error) {
		return resolver(subject), nil
	})
}

// SubjectReleaseNameSearchPolicy resolves the upload name from requested-name
// aware input while resolving an explicitly different search semantic from the
// unchanged canonical subject.
func SubjectReleaseNameSearchPolicy(
	id string,
	uploadResolver func(api.UploadSubject, config.TrackerConfig) string,
	searchResolver func(api.UploadSubject, config.TrackerConfig) string,
) ReleaseNamePolicyBinding {
	return NewReleaseNamePolicy(id, func(input ReleaseNameInput) (ResolvedReleaseNames, error) {
		uploadSubject, err := releaseNamePolicySubject(input)
		if err != nil {
			return ResolvedReleaseNames{}, err
		}
		return ResolvedReleaseNames{
			Upload:    uploadResolver(uploadSubject, input.TrackerConfig),
			Duplicate: searchResolver(input.Subject, input.TrackerConfig),
		}, nil
	})
}

// SimpleSubjectReleaseNameSearchPolicy is the subject-only form of
// SubjectReleaseNameSearchPolicy.
func SimpleSubjectReleaseNameSearchPolicy(
	id string,
	uploadResolver func(api.UploadSubject) string,
	searchResolver func(api.UploadSubject) string,
) ReleaseNamePolicyBinding {
	return SubjectReleaseNameSearchPolicy(
		id,
		func(subject api.UploadSubject, _ config.TrackerConfig) string { return uploadResolver(subject) },
		func(subject api.UploadSubject, _ config.TrackerConfig) string { return searchResolver(subject) },
	)
}

// CanonicalReleaseNamePolicy returns the explicit standalone fallback policy.
func CanonicalReleaseNamePolicy() ReleaseNamePolicyBinding {
	return NewReleaseNamePolicy(releaseNamePolicyCanonicalV1, func(input ReleaseNameInput) (ResolvedReleaseNames, error) {
		subject, err := releaseNamePolicySubject(input)
		if err != nil {
			return ResolvedReleaseNames{}, err
		}
		return ResolvedReleaseNames{Upload: canonicalProjectionName(subject)}, nil
	})
}

// SceneFirstReleaseNamePolicy returns the explicit scene-first standalone policy.
func SceneFirstReleaseNamePolicy() ReleaseNamePolicyBinding {
	return NewReleaseNamePolicy(releaseNamePolicySceneFirstV1, func(input ReleaseNameInput) (ResolvedReleaseNames, error) {
		subject, err := releaseNamePolicySubject(input)
		if err != nil {
			return ResolvedReleaseNames{}, err
		}
		if sceneName := strings.TrimSpace(subject.SceneName); sceneName != "" {
			return ResolvedReleaseNames{Upload: sceneName}, nil
		}
		return ResolvedReleaseNames{Upload: canonicalProjectionName(subject)}, nil
	})
}

func releaseNamePolicySubject(input ReleaseNameInput) (api.UploadSubject, error) {
	subject := input.Subject
	if input.RequestedName == nil {
		return subject, nil
	}
	requested := strings.TrimSpace(*input.RequestedName)
	if requested == "" {
		return api.UploadSubject{}, errors.New("requested upload name is empty")
	}
	subject.ReleaseName = requested
	subject.ReleaseNameNoTag = ""
	subject.SceneName = ""
	subject.Filename = ""
	return subject, nil
}

func defaultReleaseNamePolicy(family Family) ReleaseNamePolicyBinding {
	switch family {
	case FamilyUnit3D:
		binding := CanonicalReleaseNamePolicy()
		binding.ID = "unit3d/canonical/v1"
		return binding
	case FamilyAZFamily:
		binding := CanonicalReleaseNamePolicy()
		binding.ID = "azfamily/canonical/v1"
		return binding
	case FamilyStandalone, FamilyUnknown:
		return CanonicalReleaseNamePolicy()
	default:
		return ReleaseNamePolicyBinding{}
	}
}

func validateReleaseNamePolicy(binding ReleaseNamePolicyBinding) error {
	if strings.TrimSpace(binding.ID) == "" {
		return errors.New("release-name policy id is empty")
	}
	if !strings.Contains(binding.ID, "/v") {
		return fmt.Errorf("release-name policy id %q is not versioned", binding.ID)
	}
	if binding.Resolver == nil {
		return fmt.Errorf("release-name policy %q has no resolver", binding.ID)
	}
	return nil
}

func resolveReleaseNames(input PreparationInput, binding ReleaseNamePolicyBinding) (ResolvedReleaseNames, error) {
	if err := validateReleaseNamePolicy(binding); err != nil {
		return ResolvedReleaseNames{}, err
	}
	resolved, err := binding.Resolver(ReleaseNameInput{
		Subject:       input.Meta,
		TrackerConfig: input.TrackerConfig,
		RequestedName: input.RequestedUploadName,
	})
	if err != nil {
		return ResolvedReleaseNames{}, fmt.Errorf("resolve release names with %s: %w", binding.ID, err)
	}
	resolved.Upload = strings.TrimSpace(resolved.Upload)
	resolved.Duplicate = strings.TrimSpace(resolved.Duplicate)
	if err := validateResolvedReleaseName("upload", resolved.Upload); err != nil {
		return ResolvedReleaseNames{}, err
	}
	if resolved.Duplicate == "" {
		resolved.Duplicate = resolved.Upload
	}
	if err := validateResolvedReleaseName("duplicate search", resolved.Duplicate); err != nil {
		return ResolvedReleaseNames{}, err
	}
	resolved.Additional = normalizeReleaseNames(append(resolved.Additional, input.AdditionalReleaseNames...))
	if resolved.Duplicate != resolved.Upload && !hasReleaseName(resolved.Additional, api.TrackerReleaseNameRoleSearch, resolved.Duplicate) {
		resolved.Additional = append(resolved.Additional, api.TrackerReleaseName{
			Role:  api.TrackerReleaseNameRoleSearch,
			Value: resolved.Duplicate,
		})
	}
	return resolved, nil
}

func validateResolvedReleaseName(label, value string) error {
	if value == "" {
		return fmt.Errorf("%s release name is required", label)
	}
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fmt.Errorf("%s release name contains control characters", label)
	}
	return nil
}

func normalizeReleaseNames(values []api.TrackerReleaseName) []api.TrackerReleaseName {
	result := make([]api.TrackerReleaseName, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value.Role = api.TrackerReleaseNameRole(strings.TrimSpace(string(value.Role)))
		value.Value = strings.TrimSpace(value.Value)
		if value.Role == "" || value.Value == "" || strings.IndexFunc(value.Value, unicode.IsControl) >= 0 {
			continue
		}
		key := string(value.Role) + "\x00" + value.Value
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	slices.SortFunc(result, func(left, right api.TrackerReleaseName) int {
		if byRole := strings.Compare(string(left.Role), string(right.Role)); byRole != 0 {
			return byRole
		}
		return strings.Compare(left.Value, right.Value)
	})
	return result
}

func hasReleaseName(values []api.TrackerReleaseName, role api.TrackerReleaseNameRole, value string) bool {
	return slices.ContainsFunc(values, func(candidate api.TrackerReleaseName) bool {
		return candidate.Role == role && candidate.Value == value
	})
}

func projectedReleaseNamePolicyID(projection api.TrackerReleaseProjection) string {
	for _, decision := range projection.PolicyDecisions {
		if decision.Code == releaseNamePolicyDecisionCode {
			return strings.TrimSpace(decision.Decision)
		}
	}
	return ""
}

func appendReleaseNameProvenance(
	projection *api.TrackerReleaseProjection,
	binding ReleaseNamePolicyBinding,
	requestedName *string,
) {
	if projection == nil {
		return
	}
	projection.PolicyDecisions = append(projection.PolicyDecisions, api.TrackerPolicyDecision{
		Code:     releaseNamePolicyDecisionCode,
		Decision: binding.ID,
	})
	instruction := api.TrackerPolicyDecision{
		Code:     releaseNameInstructionCode,
		Decision: "automatic",
	}
	if requestedName != nil {
		instruction.Decision = "requested"
		instruction.Message = strings.TrimSpace(*requestedName)
	}
	projection.PolicyDecisions = append(projection.PolicyDecisions, instruction)
}

func projectedRequestedReleaseName(projection api.TrackerReleaseProjection) (*string, bool) {
	for _, decision := range projection.PolicyDecisions {
		if decision.Code != releaseNameInstructionCode {
			continue
		}
		switch decision.Decision {
		case "automatic":
			return nil, true
		case "requested":
			value := strings.TrimSpace(decision.Message)
			return &value, true
		default:
			return nil, false
		}
	}
	return nil, false
}

func applyResolvedReleaseNames(projection *api.TrackerReleaseProjection, resolved ResolvedReleaseNames) {
	projection.UploadReleaseName = resolved.Upload
	projection.DuplicateCriteria.Name = resolved.Duplicate
	projection.AdditionalNames = normalizeReleaseNames(append(projection.AdditionalNames, resolved.Additional...))
}

func releaseNameProjectionFingerprint(
	descriptor Descriptor,
	input PreparationInput,
	projection api.TrackerReleaseProjection,
	policyFingerprint api.WorkflowFingerprint,
) (api.WorkflowFingerprint, error) {
	requestedPresent := input.RequestedUploadName != nil
	requestedName := ""
	if requestedPresent {
		requestedName = strings.TrimSpace(*input.RequestedUploadName)
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Policy           api.WorkflowFingerprint
		PolicyID         string
		ProjectorVersion string
		Config           api.WorkflowFingerprint
		RequestedPresent bool
		RequestedName    string
		UploadName       string
		DuplicateName    string
		AdditionalNames  []api.TrackerReleaseName
	}{
		Policy:           policyFingerprint,
		PolicyID:         descriptor.ReleaseNamePolicy.ID,
		ProjectorVersion: descriptor.ProjectorVersion,
		Config:           projection.ConfigFingerprint,
		RequestedPresent: requestedPresent,
		RequestedName:    requestedName,
		UploadName:       projection.UploadReleaseName,
		DuplicateName:    projection.DuplicateCriteria.Name,
		AdditionalNames:  projection.AdditionalNames,
	})
	if err != nil {
		return "", fmt.Errorf("release-name projection fingerprint: %w", err)
	}
	return fingerprint, nil
}

// PrepareInputWithReleaseNamePolicy supplies a central projection for legacy
// direct preparation and revalidates an existing reviewed projection before
// payload construction.
func PrepareInputWithReleaseNamePolicy(
	input PreparationInput,
	binding ReleaseNamePolicyBinding,
) (PreparationInput, *PreparationFailure) {
	if input.Intent == PreparationIntentDescriptionPreview {
		return input, nil
	}
	if input.Projection == nil {
		projection := pureReleaseProjection(input)
		resolved, err := resolveReleaseNames(input, binding)
		if err != nil {
			return input, NewPreparationFailure(input.Tracker, "name_policy", "tracker release-name policy failed", err)
		}
		applyResolvedReleaseNames(&projection, resolved)
		appendReleaseNameProvenance(&projection, binding, input.RequestedUploadName)
		projection.TrackerID = api.TrackerID(strings.ToUpper(strings.TrimSpace(input.Tracker)))
		projection.DisplayName = strings.ToUpper(strings.TrimSpace(input.Tracker))
		// Direct preparation has no retained workflow projection. Tracker
		// adapters keep their defensive semantic checks at this boundary.
		projection.Readiness = api.ReadinessStatusReady
		projection.UploadReady = true
		input.Projection = &projection
		return input, nil
	}
	reviewed := input.Projection
	if reviewed.Readiness != api.ReadinessStatusReady || !reviewed.UploadReady {
		return input, NewPreparationFailure(
			input.Tracker,
			"name_projection_mismatch",
			"reviewed tracker release-name projection is not upload-ready",
			nil,
		)
	}
	resolved, err := resolveReleaseNames(input, binding)
	if err == nil && releaseNamesMatchProjection(input, resolved, *reviewed) {
		return input, nil
	}
	cause := err
	if cause == nil {
		cause = errors.New("current policy resolves different tracker names")
	}
	return input, NewPreparationFailure(
		input.Tracker,
		"name_projection_mismatch",
		"reviewed tracker release name no longer matches current policy",
		cause,
	)
}

func releaseNamesMatchProjection(input PreparationInput, resolved ResolvedReleaseNames, projection api.TrackerReleaseProjection) bool {
	expected := pureReleaseProjection(input)
	applyResolvedReleaseNames(&expected, resolved)
	return expected.UploadReleaseName == strings.TrimSpace(projection.UploadReleaseName) &&
		expected.DuplicateCriteria.Name == strings.TrimSpace(projection.DuplicateCriteria.Name) &&
		slices.Equal(expected.AdditionalNames, normalizeReleaseNames(projection.AdditionalNames))
}

// ReviewedUploadName returns the exact reviewed principal name for payload construction.
func (input PreparationInput) ReviewedUploadName() (string, error) {
	if input.Projection == nil {
		return "", errors.New("reviewed upload name is unavailable")
	}
	projection := input.Projection
	if projection.Readiness != api.ReadinessStatusReady || !projection.UploadReady {
		return "", fmt.Errorf("tracker %s release-name projection is not upload-ready", input.Tracker)
	}
	name := strings.TrimSpace(projection.UploadReleaseName)
	if err := validateResolvedReleaseName("reviewed upload", name); err != nil {
		return "", err
	}
	if trackerID := strings.TrimSpace(string(projection.TrackerID)); trackerID != "" &&
		!strings.EqualFold(trackerID, strings.TrimSpace(input.Tracker)) {
		return "", fmt.Errorf("reviewed upload name belongs to tracker %s, not %s", trackerID, input.Tracker)
	}
	return name, nil
}

func canonicalSourceBaseName(subject api.UploadSubject) string {
	source := strings.TrimSpace(subject.SourcePath)
	if source == "" {
		return ""
	}
	base := filepath.Base(source)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
