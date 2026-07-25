// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// pureReleaseProjection derives only canonical tracker-facing facts. It never
// invokes tracker preparation, builds payloads, reads artifacts, or performs
// remote work. Readiness remains unproven until naming, generic rules,
// metadata policy, and tracker-native validation complete.
func pureReleaseProjection(input PreparationInput) api.TrackerReleaseProjection {
	canonicalName := canonicalProjectionName(input.Meta)
	if canonicalName == "" {
		canonicalName = "unresolved"
	}
	category := projectionTaxonomyLabel(input.Meta.Release.Category)
	typeValue := projectionTaxonomyLabel(input.Meta.Type, input.Meta.Release.Type)
	resolution := projectionTaxonomyLabel(input.Meta.Release.Resolution)
	source := projectionTaxonomyLabel(input.Meta.Source, input.Meta.Release.Source)
	container := projectionTaxonomyLabel(input.Meta.Container, input.Meta.Release.Ext)
	codec := projectionTaxonomyLabel(input.Meta.VideoCodec)
	if codec.Label == "" && len(input.Meta.Release.Codec) > 0 {
		codec = projectionTaxonomyLabel(input.Meta.Release.Codec[0])
	}
	criteria := api.TrackerDuplicateCriteria{
		Name:        canonicalName,
		ProviderIDs: projectionProviderIDs(input.Meta.Identity),
		Category:    category,
		Type:        typeValue,
		Resolution:  resolution,
		Source:      source,
		Container:   container,
		Codecs:      append([]string(nil), input.Meta.Release.Codec...),
		Season:      input.Meta.SeasonInt,
		Episode:     input.Meta.EpisodeInt,
		Date:        strings.TrimSpace(input.Meta.DailyEpisodeDate),
	}
	projection := api.TrackerReleaseProjection{
		TrackerID:            api.TrackerID(strings.ToUpper(strings.TrimSpace(input.Tracker))),
		DisplayName:          strings.ToUpper(strings.TrimSpace(input.Tracker)),
		CanonicalReleaseName: canonicalName,
		UploadReleaseName:    canonicalName,
		Taxonomy: api.TrackerTaxonomy{
			Category:   category,
			Type:       typeValue,
			Resolution: resolution,
			Source:     source,
			Container:  container,
			Codec:      codec,
		},
		ProviderIDs:       append([]api.TrackerProviderID(nil), criteria.ProviderIDs...),
		DuplicateCriteria: criteria,
		Readiness:         api.ReadinessStatusUnknown,
	}
	if sceneName := strings.TrimSpace(input.Meta.SceneName); sceneName != "" && sceneName != canonicalName {
		projection.AdditionalNames = append(projection.AdditionalNames, api.TrackerReleaseName{
			Role:  api.TrackerReleaseNameRoleScene,
			Value: sceneName,
		})
	}
	return projection
}

func projectionTaxonomyLabel(values ...string) api.TrackerTaxonomyValue {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return api.TrackerTaxonomyValue{Label: value}
		}
	}
	return api.TrackerTaxonomyValue{}
}

func projectDryRunEntry(input PreparationInput, preview api.TrackerDryRunEntry) (api.TrackerReleaseProjection, error) {
	canonicalName := canonicalProjectionName(input.Meta)
	uploadName := strings.TrimSpace(preview.UploadReleaseName)
	if uploadName == "" {
		uploadName = strings.TrimSpace(preview.ReleaseName)
	}
	if uploadName == "" {
		uploadName = canonicalName
	}
	if uploadName == "" {
		return api.TrackerReleaseProjection{}, fmt.Errorf("tracker %s produced no upload release name", input.Tracker)
	}
	criteria := api.TrackerDuplicateCriteria{
		Name:        uploadName,
		ProviderIDs: projectionProviderIDs(input.Meta.Identity),
		Category:    taxonomyValue(preview.Payload, "category_id", "category", "cat"),
		Type:        taxonomyValue(preview.Payload, "type_id", "type", "release_type", "format"),
		Resolution:  taxonomyValue(preview.Payload, "resolution_id", "resolution", "res"),
		Source:      taxonomyValue(preview.Payload, "source_id", "source", "origin"),
		Container:   taxonomyValue(preview.Payload, "container_id", "container"),
		Codecs:      append([]string(nil), input.Meta.Release.Codec...),
		Season:      input.Meta.SeasonInt,
		Episode:     input.Meta.EpisodeInt,
		Date:        strings.TrimSpace(input.Meta.DailyEpisodeDate),
	}
	criteriaFingerprint, err := api.CanonicalWorkflowFingerprint(criteria)
	if err != nil {
		return api.TrackerReleaseProjection{}, fmt.Errorf("tracker projection criteria fingerprint: %w", err)
	}
	readiness := api.ReadinessStatusReady
	dupeReady := true
	if !strings.EqualFold(strings.TrimSpace(preview.Status), "ready") {
		readiness = api.ReadinessStatusBlocked
		dupeReady = false
	}
	projection := api.TrackerReleaseProjection{
		TrackerID:            api.TrackerID(strings.ToUpper(strings.TrimSpace(input.Tracker))),
		DisplayName:          strings.ToUpper(strings.TrimSpace(input.Tracker)),
		CanonicalReleaseName: canonicalName,
		UploadReleaseName:    uploadName,
		Taxonomy: api.TrackerTaxonomy{
			Category:   criteria.Category,
			Type:       criteria.Type,
			Resolution: criteria.Resolution,
			Source:     criteria.Source,
			Container:  criteria.Container,
			Codec:      taxonomyValue(preview.Payload, "codec_id", "codec", "video_codec"),
		},
		ProviderIDs:         append([]api.TrackerProviderID(nil), criteria.ProviderIDs...),
		DuplicateCriteria:   criteria,
		DescriptionGroup:    strings.ToLower(strings.TrimSpace(preview.DescriptionGroup)),
		Questionnaire:       projectQuestionnaire(preview.Questionnaire),
		Readiness:           readiness,
		DupeReady:           dupeReady,
		UploadReady:         dupeReady,
		CriteriaFingerprint: criteriaFingerprint,
	}
	if sceneName := strings.TrimSpace(input.Meta.SceneName); sceneName != "" && sceneName != uploadName {
		projection.AdditionalNames = append(projection.AdditionalNames, api.TrackerReleaseName{
			Role:  api.TrackerReleaseNameRoleScene,
			Value: sceneName,
		})
	}
	if preview.ContentFailure != nil || readiness == api.ReadinessStatusBlocked {
		message := strings.TrimSpace(preview.Message)
		if message == "" {
			message = "tracker projection requires additional input"
		}
		projection.PolicyDecisions = append(projection.PolicyDecisions, api.TrackerPolicyDecision{
			Code:     "projection_readiness",
			Decision: "blocked",
			Blocking: true,
			Message:  message,
		})
	}
	return projection, nil
}

func applyReviewedProjection(input PreparationInput) PreparationInput {
	if input.Projection == nil {
		return input
	}
	projection := *input.Projection
	input.Projection = &projection
	if value := strings.TrimSpace(projection.Taxonomy.Type.Label); value != "" {
		input.Meta.Type = value
	}
	if value := strings.TrimSpace(projection.Taxonomy.Source.Label); value != "" {
		input.Meta.Source = value
	}
	if value := strings.TrimSpace(projection.Taxonomy.Container.Label); value != "" {
		input.Meta.Container = value
	}
	if value := strings.TrimSpace(projection.Taxonomy.Codec.Label); value != "" {
		input.Meta.VideoCodec = value
	}
	return input
}

func validatePreparedProjection(input PreparationInput, preview api.TrackerDryRunEntry) error {
	if input.Projection == nil {
		return nil
	}
	prepared, err := projectDryRunEntry(input, preview)
	if err != nil {
		return err
	}
	want := input.Projection
	if prepared.UploadReleaseName != want.UploadReleaseName {
		return fmt.Errorf(
			"prepared upload name %q differs from reviewed projection %q",
			prepared.UploadReleaseName,
			want.UploadReleaseName,
		)
	}
	for _, comparison := range []struct {
		label string
		got   api.TrackerTaxonomyValue
		want  api.TrackerTaxonomyValue
	}{
		{
			label: "category",
			got:   prepared.Taxonomy.Category,
			want:  want.Taxonomy.Category,
		},
		{
			label: "type",
			got:   prepared.Taxonomy.Type,
			want:  want.Taxonomy.Type,
		},
		{
			label: "resolution",
			got:   prepared.Taxonomy.Resolution,
			want:  want.Taxonomy.Resolution,
		},
		{
			label: "source",
			got:   prepared.Taxonomy.Source,
			want:  want.Taxonomy.Source,
		},
		{
			label: "container",
			got:   prepared.Taxonomy.Container,
			want:  want.Taxonomy.Container,
		},
		{
			label: "codec",
			got:   prepared.Taxonomy.Codec,
			want:  want.Taxonomy.Codec,
		},
	} {
		if strings.TrimSpace(comparison.want.ID) != "" && comparison.got.ID != comparison.want.ID {
			return fmt.Errorf(
				"prepared %s taxonomy %q differs from reviewed projection %q",
				comparison.label,
				comparison.got.ID,
				comparison.want.ID,
			)
		}
	}
	return nil
}

func blockedReleaseProjection(input PreparationInput, message string) api.TrackerReleaseProjection {
	name := canonicalProjectionName(input.Meta)
	if name == "" {
		name = "unresolved"
	}
	return api.TrackerReleaseProjection{
		TrackerID:            api.TrackerID(strings.ToUpper(strings.TrimSpace(input.Tracker))),
		DisplayName:          strings.ToUpper(strings.TrimSpace(input.Tracker)),
		CanonicalReleaseName: name,
		UploadReleaseName:    name,
		DuplicateCriteria:    api.TrackerDuplicateCriteria{Name: name},
		PolicyDecisions: []api.TrackerPolicyDecision{{
			Code:     "projection_failed",
			Decision: "blocked",
			Blocking: true,
			Message:  strings.TrimSpace(message),
		}},
		Readiness: api.ReadinessStatusBlocked,
	}
}

func canonicalProjectionName(meta api.UploadSubject) string {
	for _, value := range []string{meta.ReleaseName, meta.ReleaseNameNoTag, meta.Filename} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return canonicalSourceBaseName(meta)
}

func taxonomyValue(payload map[string]string, keys ...string) api.TrackerTaxonomyValue {
	for _, key := range keys {
		for candidate, value := range payload {
			if strings.EqualFold(strings.TrimSpace(candidate), key) {
				value = strings.TrimSpace(value)
				return api.TrackerTaxonomyValue{ID: value, Label: value}
			}
		}
	}
	return api.TrackerTaxonomyValue{}
}

func projectionProviderIDs(identity api.ExternalIdentity) []api.TrackerProviderID {
	values := []struct {
		provider string
		id       int
	}{
		{provider: string(api.IdentityProviderTMDB), id: identity.TMDBID},
		{provider: string(api.IdentityProviderIMDB), id: identity.IMDBID},
		{provider: string(api.IdentityProviderTVDB), id: identity.TVDBID},
		{provider: string(api.IdentityProviderTVmaze), id: identity.TVmazeID},
		{provider: string(api.IdentityProviderMAL), id: identity.MALID},
	}
	result := make([]api.TrackerProviderID, 0, len(values))
	for _, value := range values {
		if value.id > 0 {
			result = append(result, api.TrackerProviderID{Provider: value.provider, Value: strconv.Itoa(value.id)})
		}
	}
	return result
}

func projectQuestionnaire(questionnaire *api.TrackerQuestionnaire) []api.TrackerQuestionnaireRequirement {
	if questionnaire == nil {
		return nil
	}
	result := make([]api.TrackerQuestionnaireRequirement, len(questionnaire.Fields))
	for index, field := range questionnaire.Fields {
		result[index] = api.TrackerQuestionnaireRequirement{
			Key:      strings.TrimSpace(field.Key),
			Label:    strings.TrimSpace(field.Label),
			Required: field.Required,
			Options:  append([]string(nil), field.Options...),
		}
	}
	return result
}

// ProjectRelease invokes one registry-owned tracker projector and stamps
// deterministic identity and dependency fingerprints on its safe result.
func (r *Registry) ProjectRelease(
	ctx context.Context,
	input PreparationInput,
	inputFingerprint api.WorkflowFingerprint,
	catalogFingerprint api.WorkflowFingerprint,
	configFingerprint api.WorkflowFingerprint,
) (api.TrackerReleaseProjection, *PreparationFailure) {
	descriptor, ok := r.LookupDescriptor(input.Tracker)
	if !ok {
		failure := NewPreparationFailure(input.Tracker, "not_supported", "tracker release projection is not supported", nil)
		return blockedReleaseProjection(input, failure.Message()), failure
	}
	projection := pureReleaseProjection(input)
	var failure *PreparationFailure
	if contextErr := ctx.Err(); contextErr != nil {
		failure = NewPreparationFailure(input.Tracker, "projection", "tracker projection canceled", contextErr)
		projection.Readiness = api.ReadinessStatusBlocked
		projection.DupeReady = false
		projection.UploadReady = false
		projection.PolicyDecisions = append(projection.PolicyDecisions, api.TrackerPolicyDecision{
			Code:     "projection",
			Decision: "blocked",
			Blocking: true,
			Message:  failure.Message(),
		})
	} else if resolvedNames, resolveErr := resolveReleaseNames(input, descriptor.ReleaseNamePolicy); resolveErr != nil {
		code := "name_policy"
		if input.RequestedUploadName != nil && strings.TrimSpace(*input.RequestedUploadName) == "" {
			code = "name_instruction"
		} else if strings.Contains(resolveErr.Error(), "required") {
			code = "name_required"
		}
		failure = NewPreparationFailure(input.Tracker, code, "tracker release-name policy failed", resolveErr)
		projection.UploadReleaseName = ""
		projection.DuplicateCriteria.Name = ""
		projection.Readiness = api.ReadinessStatusBlocked
		projection.DupeReady = false
		projection.UploadReady = false
		projection.PolicyDecisions = append(projection.PolicyDecisions, api.TrackerPolicyDecision{
			Code:     code,
			Decision: "blocked",
			Blocking: true,
			Message:  failure.Message(),
		})
	} else {
		applyResolvedReleaseNames(&projection, resolvedNames)
	}
	appendReleaseNameProvenance(&projection, descriptor.ReleaseNamePolicy, input.RequestedUploadName)
	projection.TrackerID = api.TrackerID(descriptor.Name)
	projection.DisplayName = descriptor.DisplayName
	projection.DescriptionGroup = descriptor.DescriptionGroup
	projection.MetadataLocale = descriptor.MetadataLocale
	projection.InputFingerprint = inputFingerprint
	projection.CatalogFingerprint = catalogFingerprint
	projection.ConfigFingerprint = configFingerprint
	policyFingerprint, err := descriptorPolicyFingerprint(descriptor)
	if err != nil {
		failure = NewPreparationFailure(input.Tracker, "fingerprint", "tracker policy fingerprint failed", err)
		projection.Readiness = api.ReadinessStatusBlocked
		projection.DupeReady = false
		projection.UploadReady = false
		return projection, failure
	}
	projection.ProjectorFingerprint, err = releaseNameProjectionFingerprint(descriptor, input, projection, policyFingerprint)
	if err != nil {
		failure = NewPreparationFailure(input.Tracker, "fingerprint", "tracker name projection fingerprint failed", err)
		projection.Readiness = api.ReadinessStatusBlocked
		projection.DupeReady = false
		projection.UploadReady = false
		return projection, failure
	}
	if failure == nil {
		validationSubject := api.NewTrackerValidationSubject(input.Meta, input.Tracker)
		projection.PreparedResourceFingerprint = api.WorkflowFingerprint(validationSubject.PreparedResourceFingerprint)
		ruleFailures, ruleErr := EvaluateTrackerValidationWithRegistry(
			ctx,
			r,
			input.Tracker,
			validationSubject,
			input.Logger,
		)
		if ruleErr != nil {
			failure = NewPreparationFailure(input.Tracker, "rules", "tracker projection policy evaluation failed", ruleErr)
			projection.Readiness = api.ReadinessStatusBlocked
			projection.DupeReady = false
			projection.UploadReady = false
		} else {
			ApplyProjectionRuleFailures(&projection, ruleFailures, input.ExecutionMode)
			if projection.Readiness == api.ReadinessStatusUnknown {
				projection.Readiness = api.ReadinessStatusReady
				projection.DupeReady = true
				projection.UploadReady = true
			}
		}
	}
	if projection.CriteriaFingerprint == "" {
		projection.CriteriaFingerprint, err = api.CanonicalWorkflowFingerprint(projection.DuplicateCriteria)
		if err != nil {
			failure = NewPreparationFailure(input.Tracker, "fingerprint", "tracker criteria fingerprint failed", err)
			projection.Readiness = api.ReadinessStatusBlocked
			projection.DupeReady = false
			projection.UploadReady = false
		}
	}
	return projection, failure
}

// ApplyProjectionRuleFailures records validation failures and updates tracker
// eligibility using the shared execution-mode policy.
func ApplyProjectionRuleFailures(
	projection *api.TrackerReleaseProjection,
	failures []api.RuleFailure,
	executionMode api.WorkflowExecutionMode,
) {
	if projection == nil {
		return
	}
	for _, failure := range failures {
		disposition := api.NormalizeRuleDisposition(failure.Disposition)
		blocking := RuleFailureBlocksExecution(failure, executionMode)
		decision := string(disposition)
		if disposition == api.RuleDispositionWaivable && !blocking {
			decision = "bypassed"
		}
		if blocking {
			decision = "ineligible"
			projection.Readiness = api.ReadinessStatusIneligible
			projection.DupeReady = false
			projection.UploadReady = false
			projection.Failures = append(projection.Failures, api.WorkflowFailure{
				Failure: api.OperationFailure{
					Code:      api.OperationFailureNoEligibleTrackers,
					Operation: api.OperationKindDuplicateCheck,
					Message:   strings.TrimSpace(failure.Reason),
					Recovery:  api.OperationRecoverySelectTrackers,
				},
				TrackerID: projection.TrackerID,
			})
		}
		projection.PolicyDecisions = append(projection.PolicyDecisions, api.TrackerPolicyDecision{
			Code:     strings.TrimSpace(failure.Rule),
			Decision: decision,
			Blocking: blocking,
			Message:  strings.TrimSpace(failure.Reason),
		})
	}
}
