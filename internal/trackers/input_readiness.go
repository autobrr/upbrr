// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

const metadataRequirementsVersion = "tracker-metadata-v1"

// CollectMetadataRequirements returns the normalized tracker-ID-free union of
// declarative metadata demands for the selected trackers. It rejects selections
// containing an unregistered tracker.
func CollectMetadataRequirements(registry *Registry, selected []api.TrackerID) (api.MetadataRequirementSet, error) {
	set := api.MetadataRequirementSet{Version: metadataRequirementsVersion}
	selected = normalizedSelectedTrackerIDs(selected)
	if registry == nil {
		if len(selected) > 0 {
			return api.MetadataRequirementSet{}, fmt.Errorf("trackers: selected tracker %q is not registered", selected[0])
		}
		return set, nil
	}
	for _, trackerID := range selected {
		if _, found := registry.LookupDescriptor(string(trackerID)); !found {
			return api.MetadataRequirementSet{}, fmt.Errorf("trackers: selected tracker %q is not registered", trackerID)
		}
		policy, ok := registry.LookupMetadataPolicy(string(trackerID))
		if !ok {
			continue
		}
		for _, requirement := range policy.Requirements {
			set.Requirements = append(set.Requirements, api.MetadataRequirement{
				Scope:       api.MetadataRequirementScope(requirement.Scope),
				AnyOf:       metadataRequirementFields(requirement.AnyOf),
				Disposition: requirement.Disposition,
			})
		}
	}
	normalized, err := set.Normalize()
	if err != nil {
		return api.MetadataRequirementSet{}, fmt.Errorf("trackers: normalize metadata requirements: %w", err)
	}
	return normalized, nil
}

// EvaluateInputReadiness evaluates selected local metadata requirements. It
// neither prepares tracker payloads nor performs remote work.
func EvaluateInputReadiness(registry *Registry, selected []api.TrackerID, subject api.UploadSubject) (api.InputReadinessEvaluation, error) {
	set, err := CollectMetadataRequirements(registry, selected)
	if err != nil {
		return api.InputReadinessEvaluation{}, err
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(set)
	if err != nil {
		return api.InputReadinessEvaluation{}, fmt.Errorf("trackers: fingerprint metadata requirements: %w", err)
	}
	evaluation := api.InputReadinessEvaluation{RequirementsFingerprint: fingerprint}
	if registry == nil {
		return evaluation, nil
	}
	ruleSubject := api.NewRuleSubject(subject)
	category := MetadataScope(strings.ToLower(strings.TrimSpace(resolveCategory(ruleSubject))))
	byIdentity := make(map[string]int)
	for _, trackerID := range normalizedSelectedTrackerIDs(selected) {
		if schema := registry.InputSchema(string(trackerID), subject); schema != nil {
			evaluation.Schemas = append(evaluation.Schemas, *schema)
		}
		policy, ok := registry.LookupMetadataPolicy(string(trackerID))
		if ok && policy.RequireKnownCategory && category != MetadataScopeMovie && category != MetadataScopeTV {
			addInputReadinessOutcome(byIdentity, &evaluation.Fields, api.InputReadinessFieldOutcome{
				Key:             "metadata.category",
				CorrectionField: new(api.CorrectionFieldReleaseNameCategory),
				Status:          api.InputReadinessFieldMissing,
				Disposition:     metadataCategoryDisposition(policy),
				Message:         "missing category required to select tracker metadata requirements",
			}, trackerID)
			continue
		}
		for _, requirement := range policy.Requirements {
			if requirement.Scope != MetadataScopeAny && requirement.Scope != category {
				continue
			}
			fields := metadataRequirementFields(requirement.AnyOf)
			outcome := api.InputReadinessFieldOutcome{
				Key:         "metadata." + strings.Join(metadataRequirementFieldStrings(fields), ".or."),
				Status:      api.InputReadinessFieldMissing,
				Disposition: api.NormalizeRuleDisposition(requirement.Disposition),
				Message:     "missing required " + metadataFieldList(requirement.AnyOf),
			}
			if metadataRequirementPresent(requirement.AnyOf, ruleSubject) {
				outcome.Status = api.InputReadinessFieldReady
				outcome.Message = ""
			}
			if len(requirement.AnyOf) == 1 {
				outcome.CorrectionField = metadataCorrectionField(requirement.AnyOf[0])
			}
			addInputReadinessOutcome(byIdentity, &evaluation.Fields, outcome, trackerID)
		}
		descriptor, found := registry.LookupDescriptor(string(trackerID))
		if provider, ok := descriptor.Definition.(InputReadinessProvider); found && ok {
			for _, outcome := range provider.InputReadiness(subject) {
				addInputReadinessOutcome(byIdentity, &evaluation.Fields, outcome, trackerID)
			}
		}
	}
	slices.SortFunc(evaluation.Fields, func(left, right api.InputReadinessFieldOutcome) int {
		return cmp.Compare(left.Key, right.Key)
	})
	evaluation.RequirementsFingerprint, err = api.CanonicalWorkflowFingerprint(struct {
		Requirements api.MetadataRequirementSet
		Schemas      []api.TrackerQuestionnaire
	}{set, evaluation.Schemas})
	if err != nil {
		return api.InputReadinessEvaluation{}, fmt.Errorf("trackers: fingerprint input schemas: %w", err)
	}
	return evaluation, nil
}

func metadataRequirementFields(fields []MetadataField) []api.MetadataRequirementField {
	result := make([]api.MetadataRequirementField, 0, len(fields))
	for _, field := range fields {
		result = append(result, api.MetadataRequirementField(field))
	}
	return result
}

func normalizedSelectedTrackerIDs(selected []api.TrackerID) []api.TrackerID {
	result := make([]api.TrackerID, 0, len(selected))
	for _, trackerID := range selected {
		trackerID = api.TrackerID(strings.ToUpper(strings.TrimSpace(string(trackerID))))
		if trackerID != "" && !slices.Contains(result, trackerID) {
			result = append(result, trackerID)
		}
	}
	return result
}

func metadataRequirementFieldStrings(fields []api.MetadataRequirementField) []string {
	values := make([]string, len(fields))
	for index := range fields {
		values[index] = string(fields[index])
	}
	return values
}

func addInputReadinessOutcome(
	byIdentity map[string]int,
	outcomes *[]api.InputReadinessFieldOutcome,
	outcome api.InputReadinessFieldOutcome,
	trackerID api.TrackerID,
) {
	identity := inputReadinessOutcomeIdentity(outcome)
	if index, ok := byIdentity[identity]; ok {
		(*outcomes)[index].TrackerIDs = append((*outcomes)[index].TrackerIDs, trackerID)
		return
	}
	outcome.TrackerIDs = []api.TrackerID{trackerID}
	byIdentity[identity] = len(*outcomes)
	*outcomes = append(*outcomes, outcome)
}

func inputReadinessOutcomeIdentity(outcome api.InputReadinessFieldOutcome) string {
	correction := ""
	if outcome.CorrectionField != nil {
		correction = string(*outcome.CorrectionField)
	}
	return strings.Join([]string{outcome.Key, correction, string(outcome.Status), string(outcome.Disposition), outcome.Message}, "\x00")
}

func metadataCorrectionField(field MetadataField) *api.CorrectionField {
	var correction api.CorrectionField
	switch field {
	case MetadataFieldTitle:
		correction = api.CorrectionFieldMetadataTitle
	case MetadataFieldAlternateTitle:
		correction = api.CorrectionFieldMetadataAlternateTitle
	case MetadataFieldOriginalTitle:
		correction = api.CorrectionFieldMetadataOriginalTitle
	case MetadataFieldGenres:
		correction = api.CorrectionFieldMetadataGenres
	case MetadataFieldOriginalLanguage:
		correction = api.CorrectionFieldMetadataOriginalLanguage
	case MetadataFieldDistributor:
		correction = api.CorrectionFieldMetadataDistributor
	case MetadataFieldAudioLanguages:
		correction = api.CorrectionFieldMetadataAudioLanguages
	case MetadataFieldSubtitleLanguages:
		correction = api.CorrectionFieldMetadataSubtitleLanguages
	case MetadataFieldHardcodedSubs:
		correction = api.CorrectionFieldMetadataHardcodedSubs
	case MetadataFieldHardcodedSubtitleLanguages:
		correction = api.CorrectionFieldMetadataHardcodedSubtitleLanguages
	case MetadataFieldTMDBIDOnly, MetadataFieldIMDBIDOnly, MetadataFieldTVDBIDOnly, MetadataFieldTVmazeIDOnly,
		MetadataFieldTMDB, MetadataFieldIMDB, MetadataFieldTVDB, MetadataFieldTVmaze,
		MetadataFieldTMDBTitle, MetadataFieldIMDBTitle, MetadataFieldTVDBTitle, MetadataFieldTVDBYear,
		MetadataFieldTVDBDisambiguation, MetadataFieldTMDBOriginCountries, MetadataFieldTMDBUnavailable,
		MetadataFieldIMDBUnavailable, MetadataFieldTVDBUnavailable, MetadataFieldPoster, MetadataFieldYear,
		MetadataFieldTMDBLocalizedPTBR:
		return nil
	default:
		return nil
	}
	return &correction
}
