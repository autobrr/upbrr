// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/autobrr/upbrr/internal/config"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	releaseNamePolicyCanonicalV1  = "standalone/canonical/v1"
	releaseNamePolicySceneFirstV1 = "standalone/scene-first/v1"
	releaseNamePolicyDecisionCode = "release_name_policy"
	releaseNameInstructionCode    = "release_name_instruction"
	releaseNameConfirmationCode   = "release_name_confirmation"
)

// NewReleaseNamePolicy binds a pure resolver to a stable versioned identifier.
func NewReleaseNamePolicy(id string, resolver ReleaseNamePolicy) ReleaseNamePolicyBinding {
	return ReleaseNamePolicyBinding{
		ID: strings.TrimSpace(id),
		Elements: api.ReleaseNameElementPolicy{
			Version: api.ReleaseNameElementPolicyVersionV1,
		},
		Resolver: resolver,
	}
}

// WithEpisodeTitleMode is the v1 binding seam for an evidenced tracker-local
// episode-title exception.
func WithEpisodeTitleMode(binding ReleaseNamePolicyBinding, mode api.EpisodeTitleMode) ReleaseNamePolicyBinding {
	binding.Elements = api.ReleaseNameElementPolicy{
		Version:          api.ReleaseNameElementPolicyVersionV1,
		EpisodeTitleMode: mode,
	}
	return binding
}

// WithNonSceneReleaseNameConfirmation requires explicit review of automatic
// non-scene names while allowing evidenced scene names unchanged.
func WithNonSceneReleaseNameConfirmation(binding ReleaseNamePolicyBinding) ReleaseNamePolicyBinding {
	binding.Confirmation = ReleaseNameConfirmationNonScene
	return binding
}

// WithMovieYearProvider selects the current provider year used by automatic movie names.
// Missing or mismatched metadata leaves the parsed release year unchanged.
func WithMovieYearProvider(binding ReleaseNamePolicyBinding, provider api.IdentityProvider) ReleaseNamePolicyBinding {
	binding.MovieYearProvider = provider
	return binding
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
	subject.GeneratedReleaseNames = api.GeneratedReleaseNameVariants{}
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
	if _, err := normalizedReleaseNameElementPolicy(binding.Elements); err != nil {
		return fmt.Errorf("release-name policy %q element policy: %w", binding.ID, err)
	}
	switch binding.Confirmation {
	case ReleaseNameConfirmationNone, ReleaseNameConfirmationNonScene:
	default:
		return fmt.Errorf("release-name policy %q has unsupported confirmation mode %q", binding.ID, binding.Confirmation)
	}
	switch binding.MovieYearProvider {
	case "", api.IdentityProviderTMDB, api.IdentityProviderIMDB:
	case api.IdentityProviderTVDB, api.IdentityProviderTVmaze, api.IdentityProviderMAL:
		return fmt.Errorf("release-name policy %q has unsupported movie-year provider %q", binding.ID, binding.MovieYearProvider)
	}
	return nil
}

func releaseNameConfirmationRequired(input PreparationInput, binding ReleaseNamePolicyBinding) bool {
	return releaseNameConfirmationApplies(input, binding) &&
		input.RequestedUploadName == nil
}

func releaseNameConfirmationApplies(input PreparationInput, binding ReleaseNamePolicyBinding) bool {
	return binding.Confirmation == ReleaseNameConfirmationNonScene && !input.Meta.Scene
}

func resolveProjectedReleaseNames(input PreparationInput, binding ReleaseNamePolicyBinding) (ResolvedReleaseNames, error) {
	resolved, err := resolveReleaseNames(input, binding)
	if err != nil || input.RequestedUploadName == nil {
		return resolved, err
	}
	automaticInput := input
	automaticInput.RequestedUploadName = nil
	automatic, err := resolveReleaseNames(automaticInput, binding)
	if err != nil {
		return ResolvedReleaseNames{}, err
	}
	resolved.Duplicate = automatic.Duplicate
	resolved.Additional = automatic.Additional
	return resolved, nil
}

func resolveReleaseNames(input PreparationInput, binding ReleaseNamePolicyBinding) (ResolvedReleaseNames, error) {
	if err := validateReleaseNamePolicy(binding); err != nil {
		return ResolvedReleaseNames{}, err
	}
	elementPolicy, err := normalizedReleaseNameElementPolicy(binding.Elements)
	if err != nil {
		return ResolvedReleaseNames{}, err
	}
	subject := applyReleaseNameElementPolicy(input.Meta, input.RequestedUploadName, elementPolicy)
	resolved, err := binding.Resolver(ReleaseNameInput{
		Subject:       subject,
		TrackerConfig: input.TrackerConfig,
		RequestedName: input.RequestedUploadName,
		ElementPolicy: elementPolicy,
	})
	if err != nil {
		return ResolvedReleaseNames{}, fmt.Errorf("resolve release names with %s: %w", binding.ID, err)
	}
	if input.RequestedUploadName == nil {
		resolved = applyProviderMovieYear(resolved, input.Meta, binding.MovieYearProvider)
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

// applyProviderMovieYear replaces parsed movie-year tokens when current matching metadata supplies a different provider year.
func applyProviderMovieYear(
	resolved ResolvedReleaseNames,
	meta api.UploadSubject,
	provider api.IdentityProvider,
) ResolvedReleaseNames {
	category, err := api.NormalizeCanonicalCategory(firstProjectionValue(string(meta.Identity.Category), meta.Release.Category))
	if err != nil || category != api.CanonicalCategoryMovie || meta.Release.Year <= 0 ||
		!meta.ProviderMetadata.IsCurrentFor(meta.SourcePath, meta.Identity) {
		return resolved
	}
	providerYear := 0
	switch provider {
	case api.IdentityProviderTMDB:
		if value := meta.ProviderMetadata.TMDB; value != nil &&
			(meta.Identity.TMDBID <= 0 || value.TMDBID <= 0 || value.TMDBID == meta.Identity.TMDBID) {
			providerYear = value.Year
		}
	case api.IdentityProviderIMDB:
		if value := meta.ProviderMetadata.IMDB; value != nil &&
			(meta.Identity.IMDBID <= 0 || value.IMDBID <= 0 || value.IMDBID == meta.Identity.IMDBID) {
			providerYear = value.Year
		}
	case "":
		return resolved
	case api.IdentityProviderTVDB, api.IdentityProviderTVmaze, api.IdentityProviderMAL:
		return resolved
	}
	if providerYear <= 0 || providerYear == meta.Release.Year {
		return resolved
	}
	resolved.Upload = replaceLastYearToken(resolved.Upload, meta.Release.Year, providerYear)
	resolved.Duplicate = replaceLastYearToken(resolved.Duplicate, meta.Release.Year, providerYear)
	for index := range resolved.Additional {
		resolved.Additional[index].Value = replaceLastYearToken(resolved.Additional[index].Value, meta.Release.Year, providerYear)
	}
	return resolved
}

// replaceLastYearToken replaces the last Unicode-bounded year token without altering digits embedded in other name elements.
func replaceLastYearToken(value string, oldYear int, newYear int) string {
	oldText := strconv.Itoa(oldYear)
	replacement := strconv.Itoa(newYear)
	replaceAt := -1
	for offset := 0; offset < len(value); {
		relative := strings.Index(value[offset:], oldText)
		if relative < 0 {
			break
		}
		start := offset + relative
		end := start + len(oldText)
		beforeOK := start == 0
		if !beforeOK {
			r, _ := utf8.DecodeLastRuneInString(value[:start])
			beforeOK = !unicode.IsLetter(r) && !unicode.IsDigit(r)
		}
		afterOK := end == len(value)
		if !afterOK {
			r, _ := utf8.DecodeRuneInString(value[end:])
			afterOK = !unicode.IsLetter(r) && !unicode.IsDigit(r)
		}
		if beforeOK && afterOK {
			replaceAt = start
		}
		offset = end
	}
	if replaceAt < 0 {
		return value
	}
	return value[:replaceAt] + replacement + value[replaceAt+len(oldText):]
}

func normalizedReleaseNameElementPolicy(policy api.ReleaseNameElementPolicy) (api.ReleaseNameElementPolicy, error) {
	normalized := policy.Normalized()
	if normalized.Version == "" {
		return api.ReleaseNameElementPolicy{}, errors.New("version is empty")
	}
	if !strings.Contains(normalized.Version, "/v") {
		return api.ReleaseNameElementPolicy{}, fmt.Errorf("version %q is not versioned", normalized.Version)
	}
	switch normalized.EpisodeTitleMode {
	case api.EpisodeTitleModeInclude, api.EpisodeTitleModeOmit:
		return normalized, nil
	case api.EpisodeTitleModeUnspecified:
		return api.ReleaseNameElementPolicy{}, errors.New("episode-title mode did not normalize")
	default:
		return api.ReleaseNameElementPolicy{}, fmt.Errorf("episode-title mode %q is unsupported", normalized.EpisodeTitleMode)
	}
}

func applyReleaseNameElementPolicy(
	subject api.UploadSubject,
	requestedName *string,
	policy api.ReleaseNameElementPolicy,
) api.UploadSubject {
	if requestedName != nil || policy.EpisodeTitleMode != api.EpisodeTitleModeOmit {
		return subject
	}
	included := subject.GeneratedReleaseNames.IncludeEpisodeTitle
	omitted := subject.GeneratedReleaseNames.OmitEpisodeTitle
	if !subjectUsesGeneratedReleaseName(subject, included) || strings.TrimSpace(omitted.Name) == "" {
		return subject
	}
	if sceneName := strings.TrimSpace(subject.SceneName); sceneName != "" &&
		sceneName == strings.TrimSpace(subject.ReleaseName) {
		return subject
	}
	subject.ReleaseName = omitted.Name
	subject.ReleaseNameNoTag = omitted.NameNoTag
	subject.ReleaseNameClean = omitted.CleanName
	return subject
}

func subjectUsesGeneratedReleaseName(subject api.UploadSubject, generated api.ReleaseNameVariant) bool {
	switch {
	case strings.TrimSpace(subject.ReleaseName) != "":
		return strings.TrimSpace(subject.ReleaseName) == strings.TrimSpace(generated.Name)
	case strings.TrimSpace(subject.ReleaseNameNoTag) != "":
		return strings.TrimSpace(subject.ReleaseNameNoTag) == strings.TrimSpace(generated.NameNoTag)
	default:
		return false
	}
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
	elementPolicy := binding.Elements.Normalized()
	projection.NamingElementPolicyVersion = elementPolicy.Version
	projection.EpisodeTitleMode = elementPolicy.EpisodeTitleMode
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
		ElementVersion   string
		EpisodeTitleMode api.EpisodeTitleMode
		Confirmation     ReleaseNameConfirmationMode
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
		ElementVersion:   descriptor.ReleaseNamePolicy.Elements.Normalized().Version,
		EpisodeTitleMode: descriptor.ReleaseNamePolicy.Elements.Normalized().EpisodeTitleMode,
		Confirmation:     descriptor.ReleaseNamePolicy.Confirmation,
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
		if releaseNameConfirmationRequired(input, binding) {
			return input, NewPreparationFailure(
				input.Tracker,
				releaseNameConfirmationCode,
				"tracker release name requires confirmation",
				nil,
			)
		}
		projection := pureReleaseProjection(input)
		resolved, err := resolveProjectedReleaseNames(input, binding)
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
	resolved, err := resolveProjectedReleaseNames(input, binding)
	if err == nil && releaseNamesMatchProjection(input, binding, resolved, *reviewed) {
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

func releaseNamesMatchProjection(
	input PreparationInput,
	binding ReleaseNamePolicyBinding,
	resolved ResolvedReleaseNames,
	projection api.TrackerReleaseProjection,
) bool {
	expected := pureReleaseProjection(input)
	applyResolvedReleaseNames(&expected, resolved)
	elementPolicy := binding.Elements.Normalized()
	return expected.UploadReleaseName == strings.TrimSpace(projection.UploadReleaseName) &&
		expected.DuplicateCriteria.Name == strings.TrimSpace(projection.DuplicateCriteria.Name) &&
		slices.Equal(expected.AdditionalNames, normalizeReleaseNames(projection.AdditionalNames)) &&
		elementPolicy.Version == strings.TrimSpace(projection.NamingElementPolicyVersion) &&
		elementPolicy.EpisodeTitleMode == api.NormalizeEpisodeTitleMode(projection.EpisodeTitleMode)
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

// SourceReleaseName returns the source folder or filename exactly, removing an
// extension only when retained subject facts identify the source as a file.
func SourceReleaseName(subject api.UploadSubject) string {
	sourceBase := strings.TrimSpace(pathutil.Base(subject.SourcePath))
	name := sourceBase
	filenameFallback := false
	if name == "" {
		name = strings.TrimSpace(pathutil.Base(subject.Filename))
		filenameFallback = true
	}
	if name == "" {
		return ""
	}
	extension := filepath.Ext(name)
	if extension == "" {
		return name
	}
	if filenameFallback || sourceReleasePathIsFile(subject, name, extension) {
		return strings.TrimSpace(strings.TrimSuffix(name, extension))
	}
	return name
}

func sourceReleasePathIsFile(subject api.UploadSubject, sourceBase, extension string) bool {
	for _, candidate := range []string{subject.VideoPath, subject.Filename} {
		if candidateBase := strings.TrimSpace(pathutil.Base(candidate)); candidateBase != "" &&
			strings.EqualFold(candidateBase, sourceBase) {
			return true
		}
	}
	extension = strings.TrimPrefix(extension, ".")
	releaseExtension := strings.TrimPrefix(strings.TrimSpace(subject.Release.Ext), ".")
	if releaseExtension != "" && strings.EqualFold(extension, releaseExtension) {
		return true
	}
	switch strings.ToLower(extension) {
	case "avi", "evo", "iso", "m2ts", "m4v", "mkv", "mov", "mp4", "mpeg", "mpg", "mts", "ts", "vob", "webm", "wmv":
		return true
	default:
		return false
	}
}
