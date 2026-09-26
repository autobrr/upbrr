// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

// NameAspect identifies the part of a component a mandatory rule may control.
type NameAspect string

const (
	NamePresence NameAspect = "presence"
	NameValue    NameAspect = "value"
	NameOrder    NameAspect = "order"
)

// NameAuthority grants a tracker rule control over one aspect, including manual choices.
type NameAuthority struct {
	Role   api.ReleaseNameRole
	Aspect NameAspect
}

// OpaqueNameMode controls mandatory policies applied to names without component identity.
type OpaqueNameMode string

const (
	// OpaqueNameReject blocks mandatory component edits to opaque names.
	OpaqueNameReject OpaqueNameMode = ""
	// OpaqueNameRebuild explicitly replaces an opaque name using its exact
	// prepared generation's document; unavailable documents still fail.
	OpaqueNameRebuild OpaqueNameMode = "rebuild"
)

// StructuredNamePolicy customizes copies of canonical components before central rendering.
// Mandatory may change only declared Authority aspects. Defaults respect manual components.
// Names without component identity bypass Defaults. Mandatory policies must
// reject them or explicitly opt into rebuilding; opaque wording is not parsed.
// Callbacks operate only on the supplied editor's copy and must not perform I/O.
type StructuredNamePolicy struct {
	Defaults  func(*NameEditor, api.UploadSubject, config.TrackerConfig) error
	Mandatory func(*NameEditor, api.UploadSubject, config.TrackerConfig) error
	Authority []NameAuthority
	Opaque    OpaqueNameMode
	// Separator is empty for spaces, or "." for dotted tracker names.
	Separator string
	// ExactName selects an existing authoritative name, such as a questionnaire
	// answer or an anime source filename. A nonempty result is opaque: defaults
	// cannot edit it. Requested names take precedence; mandatory opaque rules
	// still apply. This selector must not parse or rewrite generated components.
	ExactName func(api.UploadSubject, config.TrackerConfig) string
	// SearchGeneratedName keeps an ExactName selection upload-only when Search
	// returns empty. Duplicate search then uses the current generated document
	// and defaults, not the selected opaque name. A missing document is an error.
	SearchGeneratedName bool
	// Search optionally supplies an independent search name from prepared facts
	// adjusted for naming presentation. Requested names preserve the existing
	// unadjusted search-fact behavior and do not replace the search text.
	Search func(api.UploadSubject, config.TrackerConfig) string
}

// StructuredReleaseNamePolicy binds a component policy to reviewed-name authority.
func StructuredReleaseNamePolicy(id string, policy StructuredNamePolicy) ReleaseNamePolicyBinding {
	binding := NewReleaseNamePolicy(id, nil)
	binding.Structured = &policy
	return binding
}

// NameRuleError identifies an invalid component operation or a mandatory naming
// rule that cannot be safely satisfied. Reason is safe, actionable display text.
type NameRuleError struct {
	Rule   string
	Role   api.ReleaseNameRole
	Reason string
}

func (e *NameRuleError) Error() string {
	if e.Role == "" {
		return fmt.Sprintf("naming rule %s: %s", e.Rule, e.Reason)
	}
	return fmt.Sprintf("naming rule %s (%s): %s", e.Rule, e.Role, e.Reason)
}

// NameEditor addresses components by role. It never matches text in the rendered name.
// Editors are callback-scoped and mutate only a copy of the prepared document.
// Optional operations leave manual components unchanged and skip unavailable
// targets. Mandatory operations fail with NameRuleError for unavailable targets
// or undeclared authority; unknown roles fail in either mode.
type NameEditor struct {
	document  *api.ReleaseNameDocument
	mandatory bool
	authority []NameAuthority
	rule      string
	decisions []api.TrackerPolicyDecision
}

// PresentRoles returns a detached snapshot of currently present roles in render order.
// It reflects completed editor operations, including optional edits skipped for
// manual components. Changing the returned slice does not change the document.
func (e *NameEditor) PresentRoles() []api.ReleaseNameRole {
	roles := make([]api.ReleaseNameRole, 0, len(e.document.Components))
	for _, component := range e.document.Components {
		if component.Present {
			roles = append(roles, component.Role)
		}
	}
	return roles
}

// Component returns a detached snapshot of the selected component, including
// its current value and presentation state. Mutating it cannot edit the name.
func (e *NameEditor) Component(role api.ReleaseNameRole) (api.ReleaseNameComponent, bool) {
	component, exists := e.document.Component(role)
	component.AttachTo = slices.Clone(component.AttachTo)
	return component, exists
}

func (e *NameEditor) index(role api.ReleaseNameRole, aspect NameAspect) (int, error) {
	if !role.Valid() {
		return -1, &NameRuleError{
			Rule:   e.rule,
			Role:   role,
			Reason: "unknown component role",
		}
	}
	if e.mandatory && !slices.Contains(e.authority, NameAuthority{Role: role, Aspect: aspect}) {
		return -1, &NameRuleError{
			Rule:   e.rule,
			Role:   role,
			Reason: "aspect is not declared mandatory",
		}
	}
	i := slices.IndexFunc(e.document.Components, func(c api.ReleaseNameComponent) bool { return c.Role == role })
	if i < 0 {
		if e.mandatory {
			return -1, &NameRuleError{
				Rule:   e.rule,
				Role:   role,
				Reason: "component is unavailable; reprepare the release",
			}
		}
		return -1, nil
	}
	if !e.mandatory && e.document.Components[i].Manual {
		return -1, nil
	}
	return i, nil
}

func (e *NameEditor) changed(role api.ReleaseNameRole) {
	if !e.mandatory {
		return
	}
	decision := api.TrackerPolicyDecision{
		Code:         "release_name_override",
		Decision:     "enforced",
		NamingRole:   string(role),
		NamingRuleID: e.rule,
		Message:      fmt.Sprintf("Tracker naming rule %s controls %s; conflicting manual choices are overridden.", e.rule, role),
	}
	if !slices.Contains(e.decisions, decision) {
		e.decisions = append(e.decisions, decision)
	}
}

// Omit hides a component without removing identical text from other components.
func (e *NameEditor) Omit(role api.ReleaseNameRole) error {
	i, err := e.index(role, NamePresence)
	if err != nil || i < 0 {
		return err
	}
	if e.document.Components[i].Present {
		e.changed(role)
	}
	e.document.Components[i].Present = false
	return nil
}

// Include restores an available component. Required unavailable values fail explicitly.
func (e *NameEditor) Include(role api.ReleaseNameRole) error {
	i, err := e.index(role, NamePresence)
	if err != nil || i < 0 {
		return err
	}
	c := &e.document.Components[i]
	if c.Value == "" {
		c.Value = c.AvailableValue
	}
	if c.Value == "" {
		if e.mandatory {
			return &NameRuleError{
				Rule:   e.rule,
				Role:   role,
				Reason: "required value is unavailable",
			}
		}
		return nil
	}
	if !c.Present {
		e.changed(role)
	}
	c.Present = true
	return nil
}

// Set changes only a component's display value; inclusion is a separate presence decision.
func (e *NameEditor) Set(role api.ReleaseNameRole, value string) error {
	i, err := e.index(role, NameValue)
	if err != nil || i < 0 {
		return err
	}
	if e.document.Components[i].Value != value {
		e.changed(role)
	}
	e.document.Components[i].Value = value
	return nil
}

// SetJoin changes the separator before a component under order authority.
// It preserves manual layout by default and leaves attachment anchors unchanged.
func (e *NameEditor) SetJoin(role api.ReleaseNameRole, join string) error {
	i, err := e.index(role, NameOrder)
	if err != nil || i < 0 {
		return err
	}
	if e.document.Components[i].Join != join {
		e.changed(role)
	}
	e.document.Components[i].Join = join
	return nil
}

// InsertBefore adds a role at an explicit anchor, claiming presence, value and order.
// Optional rules leave manually controlled components unchanged.
func (e *NameEditor) InsertBefore(role api.ReleaseNameRole, value string, anchor api.ReleaseNameRole) error {
	return e.insertRelative(role, value, anchor, false)
}

// InsertAfter adds a role after a present anchor, with the same presence, value,
// order authority and manual-component protection as InsertBefore.
func (e *NameEditor) InsertAfter(role api.ReleaseNameRole, value string, anchor api.ReleaseNameRole) error {
	return e.insertRelative(role, value, anchor, true)
}

func (e *NameEditor) insertRelative(role api.ReleaseNameRole, value string, anchor api.ReleaseNameRole, after bool) error {
	if !role.Valid() {
		return &NameRuleError{
			Rule:   e.rule,
			Role:   role,
			Reason: "unknown component role",
		}
	}
	if !anchor.Valid() {
		return &NameRuleError{
			Rule:   e.rule,
			Role:   anchor,
			Reason: "unknown component anchor",
		}
	}
	if e.mandatory {
		for _, aspect := range []NameAspect{NamePresence, NameValue, NameOrder} {
			if !slices.Contains(e.authority, NameAuthority{Role: role, Aspect: aspect}) {
				return &NameRuleError{
					Rule:   e.rule,
					Role:   role,
					Reason: "insertion requires presence, value and order authority",
				}
			}
		}
	}
	if c, exists := e.document.Component(role); exists && c.Manual && !e.mandatory {
		return nil
	}
	anchorComponent, exists := e.document.Component(anchor)
	if !exists || !anchorComponent.Present {
		if e.mandatory {
			return &NameRuleError{
				Rule:   e.rule,
				Role:   role,
				Reason: "required insertion anchor is absent",
			}
		}
		return nil
	}
	if strings.TrimSpace(value) == "" {
		return &NameRuleError{
			Rule:   e.rule,
			Role:   role,
			Reason: "inserted value is empty",
		}
	}
	if _, exists := e.document.Component(role); !exists {
		e.document.Components = append(e.document.Components, api.ReleaseNameComponent{Role: role, Join: " "})
	}
	if err := e.Set(role, value); err != nil {
		return err
	}
	if err := e.Include(role); err != nil {
		return err
	}
	return e.moveRelative(role, anchor, after)
}

// MoveBefore moves one selected component relative to a selected anchor.
func (e *NameEditor) MoveBefore(role, anchor api.ReleaseNameRole) error {
	return e.moveRelative(role, anchor, false)
}

// MoveAfter moves one selected component after a present anchor. Like MoveBefore,
// optional edits preserve manual targets and mandatory edits require order authority.
func (e *NameEditor) MoveAfter(role, anchor api.ReleaseNameRole) error {
	return e.moveRelative(role, anchor, true)
}

func (e *NameEditor) moveRelative(role, anchor api.ReleaseNameRole, after bool) error {
	if !anchor.Valid() {
		return &NameRuleError{
			Rule:   e.rule,
			Role:   anchor,
			Reason: "unknown component anchor",
		}
	}
	i, err := e.index(role, NameOrder)
	if err != nil || i < 0 {
		return err
	}
	j := slices.IndexFunc(e.document.Components, func(c api.ReleaseNameComponent) bool { return c.Role == anchor && c.Present })
	if !e.document.Components[i].Present || j < 0 {
		if e.mandatory {
			return &NameRuleError{
				Rule:   e.rule,
				Role:   role,
				Reason: "required ordering target or anchor is absent",
			}
		}
		return nil
	}
	if i == j {
		return nil
	}
	c := e.document.Components[i]
	e.document.Components = slices.Delete(e.document.Components, i, i+1)
	if i < j {
		j--
	}
	if after {
		j++
	}
	e.document.Components = slices.Insert(e.document.Components, j, c)
	if i != j {
		e.changed(role)
	}
	return nil
}

func validateStructuredNamePolicy(policy *StructuredNamePolicy) error {
	if policy.Opaque != OpaqueNameReject && policy.Opaque != OpaqueNameRebuild {
		return fmt.Errorf("unsupported opaque-name mode %q", policy.Opaque)
	}
	if policy.Separator != "" && policy.Separator != "." {
		return fmt.Errorf("unsupported name separator %q", policy.Separator)
	}
	if (policy.Mandatory == nil) != (len(policy.Authority) == 0) {
		return errors.New("mandatory callback and authority must be declared together")
	}
	seen := make(map[NameAuthority]bool)
	for _, authority := range policy.Authority {
		if !authority.Role.Valid() || (authority.Aspect != NamePresence && authority.Aspect != NameValue && authority.Aspect != NameOrder) || seen[authority] {
			return fmt.Errorf("invalid or duplicate naming authority %v", authority)
		}
		seen[authority] = true
	}
	return nil
}

func resolveStructuredNames(input ReleaseNameInput, binding ReleaseNamePolicyBinding) (ResolvedReleaseNames, error) {
	policy := binding.Structured
	subject := input.Subject
	searchSubject := applyReleaseNamePresentation(subject, input.RequestedName)
	policyFingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		ID                  string
		Version             string
		Authority           []NameAuthority
		Opaque              OpaqueNameMode
		Separator           string
		YearProvider        api.IdentityProvider
		SearchGeneratedName bool
	}{binding.ID, api.ReleaseNameDocumentVersionV1, policy.Authority, policy.Opaque, policy.Separator, binding.MovieYearProvider, policy.SearchGeneratedName})
	if err != nil {
		return ResolvedReleaseNames{}, fmt.Errorf("structured name policy fingerprint: %w", err)
	}
	decisions := []api.TrackerPolicyDecision{{Code: "release_name_structure", Decision: string(policyFingerprint)}}
	document := subject.GeneratedName
	exactName := ""
	if policy.ExactName != nil {
		exactName = strings.TrimSpace(policy.ExactName(subject, input.TrackerConfig))
	}
	opaque := input.RequestedName != nil || document == nil || exactName != ""
	if document != nil {
		if err := document.Validate(); err != nil {
			return ResolvedReleaseNames{}, fmt.Errorf("generated name: %w", err)
		}
		opaque = opaque || strings.TrimSpace(subject.ReleaseName) != document.Render().Name
	}
	if opaque {
		if policy.Mandatory == nil {
			name := canonicalProjectionName(subject)
			if exactName != "" {
				name = exactName
			}
			if input.RequestedName != nil {
				name = *input.RequestedName
			}
			resolved := ResolvedReleaseNames{Upload: name, Decisions: decisions}
			if policy.Search != nil {
				resolved.Duplicate = policy.Search(searchSubject, input.TrackerConfig)
			}
			if exactName != "" && policy.SearchGeneratedName && strings.TrimSpace(resolved.Duplicate) == "" {
				if document == nil {
					return ResolvedReleaseNames{}, &NameRuleError{
						Rule:   binding.ID,
						Reason: "generated search-name components are unavailable; reprepare the release",
					}
				}
				searchInput := input
				searchInput.RequestedName = nil
				searchInput.Subject.ReleaseName = document.Render().Name
				searchInput.Subject.Scene = false
				searchInput.Subject.SceneName = ""
				searchPolicy := *policy
				searchPolicy.ExactName = nil
				searchPolicy.Search = nil
				searchPolicy.SearchGeneratedName = false
				searchBinding := binding
				searchBinding.Structured = &searchPolicy
				generated, err := resolveStructuredNames(searchInput, searchBinding)
				if err != nil {
					return ResolvedReleaseNames{}, err
				}
				resolved.Duplicate = generated.Upload
			}
			return resolved, nil
		}
		if policy.Opaque != OpaqueNameRebuild || document == nil {
			reason := "opaque name cannot satisfy mandatory component rules; clear the name override and reprepare"
			if document == nil {
				reason = "generated name components are unavailable; reprepare the release"
			}
			return ResolvedReleaseNames{}, &NameRuleError{
				Rule:   binding.ID,
				Reason: reason,
			}
		}
		decisions = append(decisions, api.TrackerPolicyDecision{
			Code:         "release_name_override",
			Decision:     "rebuilt",
			NamingRole:   "name",
			NamingRuleID: binding.ID,
			Message:      "The tracker requires automatic naming; the opaque name was replaced.",
		})
	}
	editor := &NameEditor{
		document:  document.Clone(),
		rule:      binding.ID,
		authority: policy.Authority,
	}
	applyStructuredMovieYear(editor.document, subject, binding.MovieYearProvider)
	if input.ElementPolicy.EpisodeTitleMode == api.EpisodeTitleModeOmit {
		if err := editor.Omit(api.NameRoleEpisodeTitle); err != nil {
			return ResolvedReleaseNames{}, err
		}
	}
	if policy.Defaults != nil {
		if err := policy.Defaults(editor, subject, input.TrackerConfig); err != nil {
			return ResolvedReleaseNames{}, err
		}
	}
	editor.mandatory = true
	if policy.Mandatory != nil {
		if err := policy.Mandatory(editor, subject, input.TrackerConfig); err != nil {
			return ResolvedReleaseNames{}, err
		}
	}
	if err := editor.document.Validate(); err != nil {
		return ResolvedReleaseNames{}, fmt.Errorf("tracker name: %w", err)
	}
	name := editor.document.Render().Name
	if policy.Separator == "." {
		name = strings.Join(strings.Fields(name), ".")
	}
	resolved := ResolvedReleaseNames{Upload: name, Decisions: append(decisions, editor.decisions...)}
	if policy.Search != nil {
		resolved.Duplicate = policy.Search(searchSubject, input.TrackerConfig)
	}
	return resolved, nil
}

func applyStructuredMovieYear(document *api.ReleaseNameDocument, subject api.UploadSubject, provider api.IdentityProvider) {
	if subject.EffectiveMetadata.YearProvenance.IsManual() || !subject.ProviderMetadata.IsCurrentFor(subject.SourcePath, subject.Identity) {
		return
	}
	category, err := api.NormalizeCanonicalCategory(firstProjectionValue(string(subject.Identity.Category), subject.Release.Category))
	if err != nil || category != api.CanonicalCategoryMovie {
		return
	}
	year := 0
	switch provider {
	case api.IdentityProviderTMDB:
		if data := subject.ProviderMetadata.TMDB; data != nil && (subject.Identity.TMDBID <= 0 || data.TMDBID <= 0 || data.TMDBID == subject.Identity.TMDBID) {
			year = data.Year
		}
	case api.IdentityProviderIMDB:
		if data := subject.ProviderMetadata.IMDB; data != nil && (subject.Identity.IMDBID <= 0 || data.IMDBID <= 0 || data.IMDBID == subject.Identity.IMDBID) {
			year = data.Year
		}
	case "", api.IdentityProviderTVDB, api.IdentityProviderTVmaze, api.IdentityProviderMAL:
		return
	}
	if year <= 0 {
		return
	}
	for i := range document.Components {
		c := &document.Components[i]
		if c.Role == api.NameRoleYear && c.Present && !c.Manual {
			c.Value = strconv.Itoa(year)
		}
	}
}

func structuredNameAuthority(binding ReleaseNamePolicyBinding) []NameAuthority {
	if binding.Structured == nil {
		return nil
	}
	return binding.Structured.Authority
}

func structuredNameOpaqueMode(binding ReleaseNamePolicyBinding) OpaqueNameMode {
	if binding.Structured == nil {
		return OpaqueNameReject
	}
	return binding.Structured.Opaque
}

func structuredNameDocumentVersion(binding ReleaseNamePolicyBinding) string {
	if binding.Structured == nil {
		return ""
	}
	return api.ReleaseNameDocumentVersionV1
}

func structuredNameSeparator(binding ReleaseNamePolicyBinding) string {
	if binding.Structured == nil {
		return ""
	}
	return binding.Structured.Separator
}

func namingOverrideDecisions(decisions []api.TrackerPolicyDecision) []api.TrackerPolicyDecision {
	var result []api.TrackerPolicyDecision
	for _, decision := range decisions {
		if strings.HasPrefix(decision.Code, "release_name_override") {
			result = append(result, decision)
		}
	}
	return result
}
